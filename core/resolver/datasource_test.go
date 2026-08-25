package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// fakeDataSourceReader is a hermetic core.StateReader -- mirrors
// core/scan_test.go's own fakeProvider shape (a different package, so
// not directly reusable), extended with a real call counter so tests
// can prove dataSourceReadCache actually memoizes across resolveOnce's
// own two DoubleRun invocations, not just assert the returned bytes
// look right.
type fakeDataSourceReader struct {
	readErr    error
	state      json.RawMessage
	readCalls  int
	knownTypes map[string]bool
}

func (f *fakeDataSourceReader) Schema(context.Context) (any, map[string]any, error) {
	types := make(map[string]any, len(f.knownTypes))
	for t := range f.knownTypes {
		types[t] = struct{}{}
	}
	return struct{}{}, types, nil
}

func (f *fakeDataSourceReader) Configure(context.Context, any, json.RawMessage) error {
	return nil
}

func (f *fakeDataSourceReader) ReadResource(context.Context, any, string, json.RawMessage) (json.RawMessage, error) {
	f.readCalls++
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.state, nil
}

// dataSourceSchema is a fakeSchema pre-populated with a real data
// source type ("data_fake_widget") alongside the existing resource
// types -- HasType must return true for InferProvider to accept it;
// this package's own real production path relies on a real provider
// eventually serving this schema entry (UBI-186 found this is separate,
// unbuilt work) -- hermetic tests supply it directly instead.
func dataSourceSchema() *fakeSchema {
	s := newFakeSchema()
	s.types["data_fake_widget"] = true
	return s
}

func dataSourceProvider(s SchemaInspector, reader core.StateReader) []DeclaredProvider {
	return []DeclaredProvider{{
		Source:         "acme/test",
		Version:        "1.0.0",
		Schema:         s,
		Reader:         reader,
		ProviderConfig: json.RawMessage(`{}`),
	}}
}

func ds(typ, name, lookup string) DataSourceIntent {
	return DataSourceIntent{Type: typ, Name: name, Lookup: json.RawMessage(lookup)}
}

func TestResolve_DataSource_BasicRoundTrip(t *testing.T) {
	l := core.Open(t.TempDir())
	reader := &fakeDataSourceReader{
		knownTypes: map[string]bool{"data_fake_widget": true},
		state:      json.RawMessage(`{"id":"w-123","name":"primary-widget"}`),
	}
	intent := intentFile("payments")
	intent.DataSources = []DataSourceIntent{
		ds("data_fake_widget", "primary", `{"id":"w-123"}`),
	}

	p, err := Resolve(l, dataSourceProvider(dataSourceSchema(), reader), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(p.Delta.Creates) != 0 || len(p.Delta.Modifies) != 0 {
		t.Fatalf("expected no delta entries for a data source, got %+v", p.Delta)
	}
	if len(p.Resolution.Inputs) != 1 {
		t.Fatalf("expected exactly 1 resolution.inputs entry, got %d: %+v", len(p.Resolution.Inputs), p.Resolution.Inputs)
	}
	in := p.Resolution.Inputs[0]
	if in.Kind != "data_source" {
		t.Fatalf("kind = %q, want data_source", in.Kind)
	}
	if in.Resource != "payments.data_fake_widget.primary" {
		t.Fatalf("resource = %q", in.Resource)
	}
	if in.ObservedHash == "" {
		t.Fatal("expected a non-empty ObservedHash")
	}
	var lookup map[string]interface{}
	json.Unmarshal(in.Lookup, &lookup)
	if lookup["id"] != "w-123" {
		t.Fatalf("lookup = %v", lookup)
	}
	if err := core.Validate(p); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestResolve_DataSource_ReadCachedAcrossDoubleRun is the real proof
// for the whole reason dataSourceReadCache exists: Resolve wraps
// resolveOnce in core.DoubleRun, which calls it twice -- without the
// cache, readCalls would be 2, and (if the fake ever varied its answer)
// could trip ErrDoubleRunMismatch for a benign reason. With it,
// readCalls must be exactly 1.
func TestResolve_DataSource_ReadCachedAcrossDoubleRun(t *testing.T) {
	l := core.Open(t.TempDir())
	reader := &fakeDataSourceReader{
		knownTypes: map[string]bool{"data_fake_widget": true},
		state:      json.RawMessage(`{"id":"w-123"}`),
	}
	intent := intentFile("payments")
	intent.DataSources = []DataSourceIntent{
		ds("data_fake_widget", "primary", `{"id":"w-123"}`),
	}

	if _, err := Resolve(l, dataSourceProvider(dataSourceSchema(), reader), intent, nil); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if reader.readCalls != 1 {
		t.Fatalf("real provider read called %d times, want exactly 1 (DoubleRun runs resolveOnce twice; the cache must absorb the second)", reader.readCalls)
	}
}

// TestResolve_DataSource_ResultFeedsResourceConfig proves references
// stay uniform end to end, at the resolver layer (Collector.addDataSource's
// own tests already proved the SDK-authoring half): a resource's $ref
// into a data source's own result resolves to a real, concrete value,
// the same as a $ref into a sibling resource would.
func TestResolve_DataSource_ResultFeedsResourceConfig(t *testing.T) {
	l := core.Open(t.TempDir())
	reader := &fakeDataSourceReader{
		knownTypes: map[string]bool{"data_fake_widget": true},
		state:      json.RawMessage(`{"id":"w-123","name":"primary-widget"}`),
	}
	intent := intentFile("payments",
		ri("aws_vpc", "main", OpCreate, `{"cidr_block":{"$ref":{"to":"payments.data_fake_widget.primary.name"}}}`),
	)
	intent.DataSources = []DataSourceIntent{
		ds("data_fake_widget", "primary", `{"id":"w-123"}`),
	}

	p, err := Resolve(l, dataSourceProvider(dataSourceSchema(), reader), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var node map[string]interface{}
	json.Unmarshal(p.Delta.Creates[0], &node)
	config := node["config"].(map[string]interface{})
	if config["cidr_block"] != "primary-widget" {
		t.Fatalf("expected the data source's own real observed value substituted in, got: %v", config["cidr_block"])
	}
}

// TestResolve_DataSource_LookupReferencesResourceComputedAttr_Refuses is
// the real, honest failure case: a data source cannot look something up
// by a sibling resource's own not-yet-created attribute (its real "id,"
// deferred to $computed since the resource hasn't shipped) -- the read
// would have nothing real to send.
func TestResolve_DataSource_LookupReferencesResourceComputedAttr_Refuses(t *testing.T) {
	l := core.Open(t.TempDir())
	reader := &fakeDataSourceReader{knownTypes: map[string]bool{"data_fake_widget": true}}
	intent := intentFile("payments",
		ri("aws_vpc", "main", OpCreate, `{"cidr_block":"10.0.0.0/16"}`),
	)
	intent.DataSources = []DataSourceIntent{
		ds("data_fake_widget", "primary", `{"id":{"$ref":{"to":"payments.aws_vpc.main.id"}}}`),
	}

	_, err := Resolve(l, dataSourceProvider(dataSourceSchema(), reader), intent, nil)
	if !errors.Is(err, ErrDataSourceLookupNotConcrete) {
		t.Fatalf("expected ErrDataSourceLookupNotConcrete, got %v", err)
	}
	if reader.readCalls != 0 {
		t.Fatalf("expected the real read to never be attempted, got %d calls", reader.readCalls)
	}
}

// TestResolve_DataSource_ReadFailure_IsDistinguishableError is the real
// requirement: a provider read failing mid-walk must surface as its own
// ordinary error, never as core.ErrDoubleRunMismatch or anything
// resembling nondeterminism.
func TestResolve_DataSource_ReadFailure_IsDistinguishableError(t *testing.T) {
	l := core.Open(t.TempDir())
	reader := &fakeDataSourceReader{
		knownTypes: map[string]bool{"data_fake_widget": true},
		readErr:    errors.New("real provider: no such widget"),
	}
	intent := intentFile("payments")
	intent.DataSources = []DataSourceIntent{
		ds("data_fake_widget", "primary", `{"id":"w-123"}`),
	}

	_, err := Resolve(l, dataSourceProvider(dataSourceSchema(), reader), intent, nil)
	if !errors.Is(err, ErrDataSourceReadFailed) {
		t.Fatalf("expected ErrDataSourceReadFailed, got %v", err)
	}
	if errors.Is(err, core.ErrDoubleRunMismatch) {
		t.Fatalf("a real read failure must never be reported as ErrDoubleRunMismatch, got %v", err)
	}
	// DoubleRun's own short-circuit: an error on the FIRST resolveOnce
	// invocation returns immediately, never attempting a second --
	// confirmed directly, not just inferred from the error type.
	if reader.readCalls != 1 {
		t.Fatalf("expected the real read to be attempted exactly once before DoubleRun gave up, got %d calls", reader.readCalls)
	}
}

func TestResolve_DataSource_NoLiveReaderConfigured_RealError(t *testing.T) {
	l := core.Open(t.TempDir())
	intent := intentFile("payments")
	intent.DataSources = []DataSourceIntent{
		ds("data_fake_widget", "primary", `{"id":"w-123"}`),
	}
	// A provider with a real schema but no live Reader -- the common
	// shape of every DeclaredProvider before this feature existed, and
	// still the shape for a stack whose caller never wired one up.
	providers := []DeclaredProvider{{Source: "acme/test", Version: "1.0.0", Schema: dataSourceSchema()}}

	_, err := Resolve(l, providers, intent, nil)
	if !errors.Is(err, ErrDataSourceReadFailed) {
		t.Fatalf("expected ErrDataSourceReadFailed, got %v", err)
	}
}

func TestResolve_DataSource_DuplicateAddress_Refuses(t *testing.T) {
	l := core.Open(t.TempDir())
	reader := &fakeDataSourceReader{knownTypes: map[string]bool{"data_fake_widget": true}}
	intent := intentFile("payments")
	intent.DataSources = []DataSourceIntent{
		ds("data_fake_widget", "primary", `{"id":"1"}`),
		ds("data_fake_widget", "primary", `{"id":"2"}`),
	}

	_, err := Resolve(l, dataSourceProvider(dataSourceSchema(), reader), intent, nil)
	if !errors.Is(err, ErrDuplicateDataSource) {
		t.Fatalf("expected ErrDuplicateDataSource, got %v", err)
	}
}

// TestResolve_NoDataSources_Unaffected confirms zero behavior change
// for the overwhelming majority of intent files, which never declare
// any: no live Reader/ProviderConfig needed at all, matching every
// existing resolver test in this package (none of which sets them).
func TestResolve_NoDataSources_Unaffected(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_vpc", "main", OpCreate, `{"cidr_block":"10.0.0.0/16"}`),
	)

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(p.Delta.Creates) != 1 {
		t.Fatalf("delta = %+v", p.Delta)
	}
}
