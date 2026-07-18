// Command fakeprovider is a test fixture only: it speaks (or deliberately
// misspeaks) just enough of the tfplugin handshake to exercise
// provider.Launch's success and adversarial paths — including both wire
// protocols ubx supports — without depending on a real Terraform provider
// binary.
//
// Mode is selected by the FAKEPROVIDER_MODE environment variable (not argv:
// real provider binaries are launched with no arguments, so tests drive
// this fixture the same way Launch drives a real provider, via
// provider.WithEnv):
//
//	ok-v6               valid v6 handshake, serves a real schema + ReadResource over gRPC
//	ok-v5               valid v5 handshake, serves a real schema + ReadResource over gRPC
//	conformance-v6      valid v6 handshake, serves a schema/ReadResource shaped by the
//	                    FAKEPROVIDER_RESOURCE_TYPE/FAKEPROVIDER_ATTRS env vars below —
//	                    UBI-9's fake-only per-type conformance fixture
//	conformance-v5      same as conformance-v6, over the v5 wire protocol
//	bad-core            handshake line with the wrong core (go-plugin) protocol version
//	unsupported-version handshake line with an app protocol version ubx has no wire impl for
//	bad-protocol        handshake line with a non-grpc wire protocol
//	malformed           an unparseable line
//	hang                never writes anything to stdout
//	crash                exits immediately with no output
//
// FAKEPROVIDER_EXTRA_TAG ("key=value"), if set, merges an extra tag into
// ok-v5/ok-v6's ReadResource response regardless of current_state — see
// echoWidgetState.
//
// FAKEPROVIDER_APPLY_MODE (UBI-26, ok-v5/ok-v6 only) selects
// ApplyResourceChange's behavior:
//
//	""/"ok"           echoes planned_state back (same computed-id fill-in as
//	                  ReadResource), simulating a clean apply -- UNLESS
//	                  planned_state is a genuine null (UBI-30: ubx's own
//	                  destroy convention), in which case it's a destroy: the
//	                  id named in prior_state is remembered as destroyed for
//	                  the rest of this process's lifetime, and a subsequent
//	                  ReadResource for that same id genuinely reports
//	                  not-found (no DynamicValue at all), the same shape a
//	                  real provider returns for a resource that's actually
//	                  gone. This fixture has no other persistent state at
//	                  all -- see destroyedIDs' own doc comment for why a
//	                  destroy specifically needs one.
//	"diagnostic-error" returns one ERROR-severity Diagnostic and no new_state --
//	                  the "terminal" half of docs/executor.md's error taxonomy
//	"hang"            never responds -- the caller's own context deadline is
//	                  what must fire; the "retryable" half of the taxonomy
//
// conformance-v5/conformance-v6 are driven by:
//
//	FAKEPROVIDER_RESOURCE_TYPE   the resource type name to advertise (e.g. "aws_instance")
//	FAKEPROVIDER_ATTRS           comma-separated attribute names to model — a subset of
//	                             that type's REAL schema (see cmd/schemadump), so the
//	                             identity/mutable fields a conformance test exercises are
//	                             schema-verified, not invented. "tags"/"tags_all" are
//	                             modeled as string maps; everything else as strings.
//	FAKEPROVIDER_MUTATE_ATTR     which attribute name (from FAKEPROVIDER_ATTRS) to change
//	                             on the next ReadResource call — the fake stand-in for a
//	                             real out-of-band mutation.
//	FAKEPROVIDER_MUTATE_VALUE    the value to set it to. If the target attribute is a map
//	                             ("tags"/"tags_all"), this is "key=value" merged into the
//	                             map, same convention as FAKEPROVIDER_EXTRA_TAG; otherwise
//	                             it replaces the attribute's scalar value directly.
//	FAKEPROVIDER_SENSITIVE_ATTRS comma-separated subset of FAKEPROVIDER_ATTRS to advertise
//	                             with Sensitive: true (UBI-23) — lets a test exercise
//	                             provider.Redact/core's $redacted handling end to end
//	                             without a real Sensitive-bearing provider schema.
//
// See conformance/fake_test.go for how the harness drives this.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"

	"github.com/zclconf/go-cty/cty"
	ctymsgpack "github.com/zclconf/go-cty/cty/msgpack"

	"github.com/ubiquex/ubiquex-cli/provider/tfplugin5"
	"github.com/ubiquex/ubiquex-cli/provider/tfplugin6"
)

func main() {
	mode := os.Getenv("FAKEPROVIDER_MODE")

	switch mode {
	case "ok-v6":
		serveV6()
	case "ok-v5":
		serveV5()
	case "conformance-v6":
		serveConformanceV6()
	case "conformance-v5":
		serveConformanceV5()
	case "bad-core":
		fmt.Println("99|6|unix|/tmp/does-not-matter|grpc")
		block()
	case "unsupported-version":
		fmt.Println("1|4|unix|/tmp/does-not-matter|grpc")
		block()
	case "bad-protocol":
		fmt.Println("1|6|unix|/tmp/does-not-matter|netrpc")
		block()
	case "malformed":
		fmt.Println("not-a-handshake-line")
		block()
	case "hang":
		block()
	case "crash":
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "fakeprovider: unknown mode %q\n", mode)
		os.Exit(2)
	}
}

// block keeps the process alive (as a real plugin's server loop would)
// without writing anything further to stdout, until the test harness kills
// it. A timed sleep, not a bare select{}, so the Go runtime's deadlock
// detector (which fires on a goroutine blocked with no possible wakeup)
// doesn't kill the process itself before the test does.
func block() {
	time.Sleep(24 * time.Hour)
}

func socketPath() string {
	f, err := os.CreateTemp("", "fakeprovider-*.sock")
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakeprovider: tempfile: %v\n", err)
		os.Exit(1)
	}
	path := f.Name()
	_ = f.Close()
	_ = os.Remove(path)
	return path
}

func serveV6() {
	lis, err := net.Listen("unix", socketPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakeprovider: listen: %v\n", err)
		os.Exit(1)
	}

	srv := grpc.NewServer()
	tfplugin6.RegisterProviderServer(srv, &fakeProviderServerV6{})

	fmt.Printf("1|6|unix|%s|grpc\n", lis.Addr().String())

	if err := srv.Serve(lis); err != nil {
		fmt.Fprintf(os.Stderr, "fakeprovider: serve: %v\n", err)
		os.Exit(1)
	}
}

func serveV5() {
	lis, err := net.Listen("unix", socketPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakeprovider: listen: %v\n", err)
		os.Exit(1)
	}

	srv := grpc.NewServer()
	tfplugin5.RegisterProviderServer(srv, &fakeProviderServerV5{})

	fmt.Printf("1|5|unix|%s|grpc\n", lis.Addr().String())

	if err := srv.Serve(lis); err != nil {
		fmt.Fprintf(os.Stderr, "fakeprovider: serve: %v\n", err)
		os.Exit(1)
	}
}

// fakeWidgetSchemaJSON etc. are shared, protocol-independent fixture data:
// one resource type (fake_widget) with a handful of scalar attributes,
// enough to exercise a real schema dump and a real ReadResource round trip.

type fakeProviderServerV6 struct {
	tfplugin6.UnimplementedProviderServer
}

func (s *fakeProviderServerV6) GetProviderSchema(context.Context, *tfplugin6.GetProviderSchema_Request) (*tfplugin6.GetProviderSchema_Response, error) {
	return &tfplugin6.GetProviderSchema_Response{
		Provider: &tfplugin6.Schema{
			Block: &tfplugin6.Schema_Block{
				Attributes: []*tfplugin6.Schema_Attribute{
					{Name: "region", Type: []byte(`"string"`), Optional: true},
				},
			},
		},
		ResourceSchemas: map[string]*tfplugin6.Schema{
			"fake_widget": {
				Version: 1,
				Block: &tfplugin6.Schema_Block{
					Attributes: []*tfplugin6.Schema_Attribute{
						{Name: "id", Type: []byte(`"string"`), Computed: true},
						{Name: "name", Type: []byte(`"string"`), Required: true},
						{Name: "tags", Type: []byte(`["map","string"]`), Optional: true},
					},
				},
			},
		},
	}, nil
}

func (s *fakeProviderServerV6) ConfigureProvider(context.Context, *tfplugin6.ConfigureProvider_Request) (*tfplugin6.ConfigureProvider_Response, error) {
	return &tfplugin6.ConfigureProvider_Response{}, nil
}

func (s *fakeProviderServerV6) ReadResource(_ context.Context, req *tfplugin6.ReadResource_Request) (*tfplugin6.ReadResource_Response, error) {
	if id, ok := currentStateID(req.CurrentState.GetMsgpack()); ok && isDestroyed(id) {
		// A destroy (UBI-30) landed against this id in a prior
		// ApplyResourceChange call, this same process lifetime -- report
		// genuinely not-found (no DynamicValue at all), the same shape a
		// real provider returns for a resource that's actually gone.
		return &tfplugin6.ReadResource_Response{}, nil
	}
	out, err := echoWidgetState(req.CurrentState.GetMsgpack())
	if err != nil {
		return nil, err
	}
	return &tfplugin6.ReadResource_Response{NewState: &tfplugin6.DynamicValue{Msgpack: out}}, nil
}

func (s *fakeProviderServerV6) ApplyResourceChange(ctx context.Context, req *tfplugin6.ApplyResourceChange_Request) (*tfplugin6.ApplyResourceChange_Response, error) {
	switch os.Getenv("FAKEPROVIDER_APPLY_MODE") {
	case "diagnostic-error":
		return &tfplugin6.ApplyResourceChange_Response{
			Diagnostics: []*tfplugin6.Diagnostic{{
				Severity: tfplugin6.Diagnostic_ERROR,
				Summary:  "invalid attribute value",
				Detail:   "fakeprovider: simulated terminal diagnostic",
			}},
		}, nil
	case "hang":
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if id, ok, isDestroy := destroyRequestID(req.PriorState.GetMsgpack(), req.PlannedState.GetMsgpack()); isDestroy {
		// A destroy (UBI-30, docs/executor.md's own amendment): PlannedState
		// is the literal null ubx's own executor sends for "destroy this."
		// Marked here, not deleted from any persistent map (this fixture
		// never had one to begin with -- ubx supplies the same lookup on
		// every read, never re-derives it) -- ReadResource above is what
		// actually honors this mark on a subsequent read within the same
		// process lifetime.
		if ok {
			markDestroyed(id)
		}
		return &tfplugin6.ApplyResourceChange_Response{}, nil
	}
	out, err := echoAppliedState(req.PlannedState.GetMsgpack())
	if err != nil {
		return nil, err
	}
	return &tfplugin6.ApplyResourceChange_Response{NewState: &tfplugin6.DynamicValue{Msgpack: out}}, nil
}

type fakeProviderServerV5 struct {
	tfplugin5.UnimplementedProviderServer
}

func (s *fakeProviderServerV5) GetSchema(context.Context, *tfplugin5.GetProviderSchema_Request) (*tfplugin5.GetProviderSchema_Response, error) {
	return &tfplugin5.GetProviderSchema_Response{
		Provider: &tfplugin5.Schema{
			Block: &tfplugin5.Schema_Block{
				Attributes: []*tfplugin5.Schema_Attribute{
					{Name: "region", Type: []byte(`"string"`), Optional: true},
				},
			},
		},
		ResourceSchemas: map[string]*tfplugin5.Schema{
			"fake_widget": {
				Version: 1,
				Block: &tfplugin5.Schema_Block{
					Attributes: []*tfplugin5.Schema_Attribute{
						{Name: "id", Type: []byte(`"string"`), Computed: true},
						{Name: "name", Type: []byte(`"string"`), Required: true},
						{Name: "tags", Type: []byte(`["map","string"]`), Optional: true},
					},
				},
			},
		},
	}, nil
}

func (s *fakeProviderServerV5) Configure(context.Context, *tfplugin5.Configure_Request) (*tfplugin5.Configure_Response, error) {
	return &tfplugin5.Configure_Response{}, nil
}

func (s *fakeProviderServerV5) ReadResource(_ context.Context, req *tfplugin5.ReadResource_Request) (*tfplugin5.ReadResource_Response, error) {
	if id, ok := currentStateID(req.CurrentState.GetMsgpack()); ok && isDestroyed(id) {
		return &tfplugin5.ReadResource_Response{}, nil
	}
	out, err := echoWidgetState(req.CurrentState.GetMsgpack())
	if err != nil {
		return nil, err
	}
	return &tfplugin5.ReadResource_Response{NewState: &tfplugin5.DynamicValue{Msgpack: out}}, nil
}

func (s *fakeProviderServerV5) ApplyResourceChange(ctx context.Context, req *tfplugin5.ApplyResourceChange_Request) (*tfplugin5.ApplyResourceChange_Response, error) {
	switch os.Getenv("FAKEPROVIDER_APPLY_MODE") {
	case "diagnostic-error":
		return &tfplugin5.ApplyResourceChange_Response{
			Diagnostics: []*tfplugin5.Diagnostic{{
				Severity: tfplugin5.Diagnostic_ERROR,
				Summary:  "invalid attribute value",
				Detail:   "fakeprovider: simulated terminal diagnostic",
			}},
		}, nil
	case "hang":
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if id, ok, isDestroy := destroyRequestID(req.PriorState.GetMsgpack(), req.PlannedState.GetMsgpack()); isDestroy {
		if ok {
			markDestroyed(id)
		}
		return &tfplugin5.ApplyResourceChange_Response{}, nil
	}
	out, err := echoAppliedState(req.PlannedState.GetMsgpack())
	if err != nil {
		return nil, err
	}
	return &tfplugin5.ApplyResourceChange_Response{NewState: &tfplugin5.DynamicValue{Msgpack: out}}, nil
}

// fakeWidgetType mirrors the fake_widget schema advertised above (id/name
// string, tags map of string) as a cty type, so this fixture can do real
// cty-msgpack decode/encode — same wire encoding ubx's provider package
// uses against real binaries — rather than just echoing opaque bytes.
var fakeWidgetType = cty.Object(map[string]cty.Type{
	"id":   cty.String,
	"name": cty.String,
	"tags": cty.Map(cty.String),
})

// echoWidgetState stands in for a real provider's ReadResource logic: it
// decodes the requested current_state, fills in "id" if the caller left it
// null (as a real provider would compute it), and re-encodes — proving a
// real cty-msgpack round trip happened rather than trusting a canned
// response.
//
// If FAKEPROVIDER_EXTRA_TAG ("key=value") is set, it's merged into the
// response's tags regardless of what current_state asked for. This lets a
// test simulate a real out-of-band mutation (the same thing "aws s3api
// put-bucket-tagging" did in the real-world verification) between two
// separate process launches that pass the identical lookup both times —
// varying the lookup itself doesn't model that scenario, since a real
// caller keeps asking the same question and gets a different answer.
//
// Deliberately NOT reused for ApplyResourceChange (see echoAppliedState):
// a real provider's Apply is a deterministic function of exactly what it
// was asked to plan, never something that also re-discovers an unrelated
// out-of-band change on the side -- that's what ReadResource/reconciliation
// are for. Reusing this same "re-inject the drift" behavior for apply too
// would make it impossible for a test (or a manual CLI run, UBI-26 session
// 3) to tell "ubx correctly reverted this" apart from "the fixture
// re-injected the drift into its own apply response regardless."
func echoWidgetState(msgpackBytes []byte) ([]byte, error) {
	vals, err := decodeWidgetState(msgpackBytes)
	if err != nil {
		return nil, err
	}
	if extra := os.Getenv("FAKEPROVIDER_EXTRA_TAG"); extra != "" {
		if k, v, ok := strings.Cut(extra, "="); ok {
			tags := vals["tags"].AsValueMap()
			if tags == nil {
				tags = map[string]cty.Value{}
			}
			tags[k] = cty.StringVal(v)
			vals["tags"] = cty.MapVal(tags)
		}
	}
	return ctymsgpack.Marshal(cty.ObjectVal(vals), fakeWidgetType)
}

// echoAppliedState stands in for a real provider's ApplyResourceChange
// logic (UBI-26): decodes planned_state, fills in "id" if left null (the
// same computed-attribute convention echoWidgetState uses), and re-encodes
// exactly what it was asked to plan -- no FAKEPROVIDER_EXTRA_TAG
// reinjection, since a real Apply is deterministic in its own inputs.
func echoAppliedState(msgpackBytes []byte) ([]byte, error) {
	vals, err := decodeWidgetState(msgpackBytes)
	if err != nil {
		return nil, err
	}
	return ctymsgpack.Marshal(cty.ObjectVal(vals), fakeWidgetType)
}

// decodeWidgetState is echoWidgetState/echoAppliedState's shared decode +
// computed-id-fill-in step. "id" is filled in when it's either Null (the
// pre-UBI-27 convention: an absent JSON key decoded through
// encodeDynamicValue) or genuinely Unknown (UBI-27:
// encodeUnknownAwareDynamicValue's own real cty.UnknownVal, for a
// from-scratch create or an explicit $computed marker) -- a real
// SDKv2-vintage provider's Apply only ever actually computes an attribute
// it finds Unknown, never one it finds Null (docs/executor.md's own
// empirical finding), but this fixture accepts either shape so it keeps
// working against both encoders' output.
func decodeWidgetState(msgpackBytes []byte) (map[string]cty.Value, error) {
	val, err := ctymsgpack.Unmarshal(msgpackBytes, fakeWidgetType)
	if err != nil {
		return nil, fmt.Errorf("fakeprovider: decode state: %w", err)
	}
	vals := val.AsValueMap()
	if id := vals["id"]; id.IsNull() || !id.IsKnown() {
		vals["id"] = cty.StringVal("computed-id")
	}
	if tags := vals["tags"]; tags.IsNull() || !tags.IsKnown() {
		vals["tags"] = cty.MapValEmpty(cty.String)
	}
	return vals, nil
}

// destroyedIDs tracks fake_widget ids ApplyResourceChange has destroyed
// (UBI-30), this process's own lifetime only -- this fixture is otherwise
// fully stateless (every ReadResource call is a pure function of whatever
// current_state ubx itself supplies), but a destroy specifically needs the
// fixture to remember what it did: ubx's own precheck-then-apply-then-
// maybe-reconcile sequence, all within one `ubx ship` invocation, sends
// the exact same lookup on every read and expects a DIFFERENT answer after
// the destroy lands.
var (
	destroyedMu  sync.Mutex
	destroyedIDs = map[string]bool{}
)

func markDestroyed(id string) {
	destroyedMu.Lock()
	defer destroyedMu.Unlock()
	destroyedIDs[id] = true
}

func isDestroyed(id string) bool {
	destroyedMu.Lock()
	defer destroyedMu.Unlock()
	return destroyedIDs[id]
}

// currentStateID extracts "id" from a ReadResource request's current_state
// -- best-effort: ok is false for anything that doesn't decode as a
// fake_widget-shaped value with a known, non-null id (never an error, this
// is purely an optional destroyed-check, not a real read).
func currentStateID(msgpackBytes []byte) (id string, ok bool) {
	val, err := ctymsgpack.Unmarshal(msgpackBytes, fakeWidgetType)
	if err != nil || val.IsNull() {
		return "", false
	}
	idVal := val.GetAttr("id")
	if idVal.IsNull() || !idVal.IsKnown() {
		return "", false
	}
	return idVal.AsString(), true
}

// destroyRequestID reports whether an ApplyResourceChange request is a
// destroy (UBI-30: planned_state decodes to a genuine null value, ubx's
// own shipDestroyNode-constructed PlannedState) and, if so, prior_state's
// own "id" -- what markDestroyed should remember. isDestroy is true
// whenever planned_state is null, even if prior_state's own id couldn't be
// read (ok=false then) -- the caller still must not fall through to
// echoAppliedState, which requires a non-null planned_state to decode at
// all.
func destroyRequestID(priorMsgpackBytes, plannedMsgpackBytes []byte) (id string, ok bool, isDestroy bool) {
	plannedVal, err := ctymsgpack.Unmarshal(plannedMsgpackBytes, fakeWidgetType)
	if err != nil || !plannedVal.IsNull() {
		return "", false, false
	}
	id, ok = currentStateID(priorMsgpackBytes)
	return id, ok, true
}

// --- conformance mode: a generic, env-var-shaped resource, one per UBI-9
// fake-only type. See the package doc comment for the env var contract.

// conformanceAttrs reads FAKEPROVIDER_ATTRS. "id" is always included even if
// the caller forgot it — every real AWS resource schema has one, and
// RunAdoptMutateScanDiff's lookup always keys off it.
func conformanceAttrs() []string {
	raw := os.Getenv("FAKEPROVIDER_ATTRS")
	var attrs []string
	if raw != "" {
		attrs = strings.Split(raw, ",")
	}
	for _, a := range attrs {
		if a == "id" {
			return attrs
		}
	}
	return append([]string{"id"}, attrs...)
}

// conformanceCtyType builds the cty object type for conformanceAttrs():
// "tags"/"tags_all" as string maps (matching every AWS resource that has
// them), everything else as plain strings — see the package doc comment for
// why scalar type-fidelity to AWS's real attribute types doesn't matter here
// (ubx's own core layer treats Observed state as opaque JSON, never
// type-checked against the schema).
func conformanceCtyType() cty.Type {
	fields := make(map[string]cty.Type, len(conformanceAttrs()))
	for _, name := range conformanceAttrs() {
		if name == "tags" || name == "tags_all" {
			fields[name] = cty.Map(cty.String)
		} else {
			fields[name] = cty.String
		}
	}
	return cty.Object(fields)
}

// conformanceSensitiveAttrs reads FAKEPROVIDER_SENSITIVE_ATTRS (UBI-23):
// which of conformanceAttrs() to advertise as Sensitive.
func conformanceSensitiveAttrs() map[string]bool {
	raw := os.Getenv("FAKEPROVIDER_SENSITIVE_ATTRS")
	if raw == "" {
		return nil
	}
	m := make(map[string]bool)
	for _, name := range strings.Split(raw, ",") {
		m[name] = true
	}
	return m
}

func conformanceResourceType() string {
	t := os.Getenv("FAKEPROVIDER_RESOURCE_TYPE")
	if t == "" {
		t = "fake_conformance_resource"
	}
	return t
}

// echoConformanceState mirrors echoWidgetState's role (real cty-msgpack
// round trip, computed "id" fill-in, injected mutation) but against a
// dynamically shaped object instead of the fixed fake_widget type, so one
// mechanism serves every FakeOnly conformance type.
func echoConformanceState(msgpackBytes []byte) ([]byte, error) {
	ty := conformanceCtyType()
	val, err := ctymsgpack.Unmarshal(msgpackBytes, ty)
	if err != nil {
		return nil, fmt.Errorf("fakeprovider: decode current_state: %w", err)
	}
	vals := val.AsValueMap()

	if idVal, ok := vals["id"]; ok && idVal.IsNull() {
		vals["id"] = cty.StringVal("computed-" + conformanceResourceType() + "-id")
	}
	for name, v := range vals {
		if (name == "tags" || name == "tags_all") && v.IsNull() {
			vals[name] = cty.MapValEmpty(cty.String)
		}
	}

	mutateAttr := os.Getenv("FAKEPROVIDER_MUTATE_ATTR")
	mutateValue := os.Getenv("FAKEPROVIDER_MUTATE_VALUE")
	if mutateAttr != "" {
		if cur, ok := vals[mutateAttr]; ok {
			if cur.Type().IsMapType() {
				m := cur.AsValueMap()
				if m == nil {
					m = map[string]cty.Value{}
				}
				if k, v, ok := strings.Cut(mutateValue, "="); ok {
					m[k] = cty.StringVal(v)
					vals[mutateAttr] = cty.MapVal(m)
				}
			} else {
				vals[mutateAttr] = cty.StringVal(mutateValue)
			}
		}
	}

	return ctymsgpack.Marshal(cty.ObjectVal(vals), ty)
}

func conformanceSchemaAttributesV6() []*tfplugin6.Schema_Attribute {
	sensitive := conformanceSensitiveAttrs()
	var attrs []*tfplugin6.Schema_Attribute
	for _, name := range conformanceAttrs() {
		a := &tfplugin6.Schema_Attribute{Name: name, Type: []byte(`"string"`), Optional: true}
		if name == "tags" || name == "tags_all" {
			a.Type = []byte(`["map","string"]`)
		}
		if name == "id" {
			a.Computed, a.Optional = true, false
		}
		if sensitive[name] {
			a.Sensitive = true
		}
		attrs = append(attrs, a)
	}
	return attrs
}

func conformanceSchemaAttributesV5() []*tfplugin5.Schema_Attribute {
	sensitive := conformanceSensitiveAttrs()
	var attrs []*tfplugin5.Schema_Attribute
	for _, name := range conformanceAttrs() {
		a := &tfplugin5.Schema_Attribute{Name: name, Type: []byte(`"string"`), Optional: true}
		if name == "tags" || name == "tags_all" {
			a.Type = []byte(`["map","string"]`)
		}
		if name == "id" {
			a.Computed, a.Optional = true, false
		}
		if sensitive[name] {
			a.Sensitive = true
		}
		attrs = append(attrs, a)
	}
	return attrs
}

type fakeConformanceServerV6 struct {
	tfplugin6.UnimplementedProviderServer
}

func (s *fakeConformanceServerV6) GetProviderSchema(context.Context, *tfplugin6.GetProviderSchema_Request) (*tfplugin6.GetProviderSchema_Response, error) {
	return &tfplugin6.GetProviderSchema_Response{
		Provider: &tfplugin6.Schema{
			Block: &tfplugin6.Schema_Block{
				Attributes: []*tfplugin6.Schema_Attribute{
					{Name: "region", Type: []byte(`"string"`), Optional: true},
				},
			},
		},
		ResourceSchemas: map[string]*tfplugin6.Schema{
			conformanceResourceType(): {
				Version: 1,
				Block:   &tfplugin6.Schema_Block{Attributes: conformanceSchemaAttributesV6()},
			},
		},
	}, nil
}

func (s *fakeConformanceServerV6) ConfigureProvider(context.Context, *tfplugin6.ConfigureProvider_Request) (*tfplugin6.ConfigureProvider_Response, error) {
	return &tfplugin6.ConfigureProvider_Response{}, nil
}

func (s *fakeConformanceServerV6) ReadResource(_ context.Context, req *tfplugin6.ReadResource_Request) (*tfplugin6.ReadResource_Response, error) {
	out, err := echoConformanceState(req.CurrentState.GetMsgpack())
	if err != nil {
		return nil, err
	}
	return &tfplugin6.ReadResource_Response{NewState: &tfplugin6.DynamicValue{Msgpack: out}}, nil
}

type fakeConformanceServerV5 struct {
	tfplugin5.UnimplementedProviderServer
}

func (s *fakeConformanceServerV5) GetSchema(context.Context, *tfplugin5.GetProviderSchema_Request) (*tfplugin5.GetProviderSchema_Response, error) {
	return &tfplugin5.GetProviderSchema_Response{
		Provider: &tfplugin5.Schema{
			Block: &tfplugin5.Schema_Block{
				Attributes: []*tfplugin5.Schema_Attribute{
					{Name: "region", Type: []byte(`"string"`), Optional: true},
				},
			},
		},
		ResourceSchemas: map[string]*tfplugin5.Schema{
			conformanceResourceType(): {
				Version: 1,
				Block:   &tfplugin5.Schema_Block{Attributes: conformanceSchemaAttributesV5()},
			},
		},
	}, nil
}

func (s *fakeConformanceServerV5) Configure(context.Context, *tfplugin5.Configure_Request) (*tfplugin5.Configure_Response, error) {
	return &tfplugin5.Configure_Response{}, nil
}

func (s *fakeConformanceServerV5) ReadResource(_ context.Context, req *tfplugin5.ReadResource_Request) (*tfplugin5.ReadResource_Response, error) {
	out, err := echoConformanceState(req.CurrentState.GetMsgpack())
	if err != nil {
		return nil, err
	}
	return &tfplugin5.ReadResource_Response{NewState: &tfplugin5.DynamicValue{Msgpack: out}}, nil
}

func serveConformanceV6() {
	lis, err := net.Listen("unix", socketPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakeprovider: listen: %v\n", err)
		os.Exit(1)
	}
	srv := grpc.NewServer()
	tfplugin6.RegisterProviderServer(srv, &fakeConformanceServerV6{})
	fmt.Printf("1|6|unix|%s|grpc\n", lis.Addr().String())
	if err := srv.Serve(lis); err != nil {
		fmt.Fprintf(os.Stderr, "fakeprovider: serve: %v\n", err)
		os.Exit(1)
	}
}

func serveConformanceV5() {
	lis, err := net.Listen("unix", socketPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakeprovider: listen: %v\n", err)
		os.Exit(1)
	}
	srv := grpc.NewServer()
	tfplugin5.RegisterProviderServer(srv, &fakeConformanceServerV5{})
	fmt.Printf("1|5|unix|%s|grpc\n", lis.Addr().String())
	if err := srv.Serve(lis); err != nil {
		fmt.Fprintf(os.Stderr, "fakeprovider: serve: %v\n", err)
		os.Exit(1)
	}
}
