package resolver

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/core"
)

// fakeSchema is a hermetic SchemaInspector -- real provider Sensitive/
// Computed flags exist and are checked elsewhere (UBI-23/24); this proves
// core/resolver's own rules against a schema shape it never has to trust
// blindly. Keys are "<type>.<attrPath>".
type fakeSchema struct {
	types     map[string]bool
	computed  map[string]bool
	sensitive map[string]bool
	// badKeys is UnknownConfigKeys' own opt-in fake (UBI-66): typeName ->
	// {bad config key -> suggestion to report}. A type/key never listed
	// here is never flagged -- deliberately, so every pre-existing test
	// in this file (none of which cares about schema-key validation) is
	// completely unaffected. The real fuzzy-match algorithm itself
	// (substring containment, edit-distance fallback) is unit-tested
	// once, directly, in provider/schemakeys_test.go -- this fake only
	// needs to prove resolveOnce's own wiring/aggregation, not
	// re-implement that logic.
	badKeys map[string]map[string]string
}

func newFakeSchema() *fakeSchema {
	return &fakeSchema{
		types: map[string]bool{
			"aws_vpc":         true,
			"aws_db_instance": true,
		},
		computed: map[string]bool{
			"aws_vpc.id":               true,
			"aws_db_instance.id":       true,
			"aws_db_instance.arn":      true,
			"aws_db_instance.endpoint": true,
		},
		sensitive: map[string]bool{
			"aws_db_instance.master_password": true,
		},
	}
}

func (f *fakeSchema) HasType(t string) bool           { return f.types[t] }
func (f *fakeSchema) IsComputed(t, path string) bool  { return f.computed[t+"."+path] }
func (f *fakeSchema) IsSensitive(t, path string) bool { return f.sensitive[t+"."+path] }

func (f *fakeSchema) UnknownConfigKeys(t string, config map[string]interface{}) []ConfigKeyIssue {
	bad := f.badKeys[t]
	if len(bad) == 0 {
		return nil
	}
	keys := make([]string, 0, len(config))
	for k := range config {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var issues []ConfigKeyIssue
	for _, k := range keys {
		if suggestion, isBad := bad[k]; isBad {
			issues = append(issues, ConfigKeyIssue{Path: k, Suggestion: suggestion})
		}
	}
	return issues
}

// flakySchema wraps a real SchemaInspector, injecting nondeterminism into
// IsComputed on purpose -- docs/resolver-adversarial.md row 1's own
// hermetic instrument: a resolver whose own logic (or, here, whatever it
// consults) isn't actually deterministic must fail hard via
// core.DoubleRun, never silently hash an unstable result.
type flakySchema struct {
	*fakeSchema
	calls int
}

func (f *flakySchema) IsComputed(t, path string) bool {
	f.calls++
	// Alternate the answer for exactly this one (deliberately non-computed
	// in the real fake) path across calls, so Resolve's two DoubleRun
	// passes both succeed -- one marking $computed, the other substituting
	// a literal -- but disagree on the actual bytes. Both branches must
	// succeed for this to exercise DoubleRun's own mismatch detection
	// rather than just erroring on the first call (DoubleRun never even
	// attempts a second run if the first one fails).
	if t == "aws_db_instance" && path == "identifier" {
		return f.calls%2 == 1
	}
	return f.fakeSchema.IsComputed(t, path)
}

// singleProvider wraps a lone SchemaInspector into the one-element
// DeclaredProvider slice Resolve now always takes (docs/resolver.md's own
// "Amendment (UBI-43): multi-provider stacks") -- every hermetic test
// predating that amendment declared exactly one schema, so this preserves
// their own single-provider behavior unchanged.
func singleProvider(s SchemaInspector) []DeclaredProvider {
	return []DeclaredProvider{{Source: "acme/test", Version: "1.0.0", Schema: s}}
}

func intentFile(stack string, resources ...ResourceIntent) *IntentFile {
	return &IntentFile{
		SchemaVersion: 1,
		Kind:          IntentFileKind,
		Stack:         stack,
		Intent:        core.Intent{Summary: "test"},
		Resources:     resources,
	}
}

func ri(typ, name, op string, config string) ResourceIntent {
	return ResourceIntent{Type: typ, Name: name, Op: op, Config: json.RawMessage(config)}
}

// seedLedger adopts addr into l with the given state, via the real
// GenerateProposal/Accept pipeline -- never hand-constructed, so
// FoldState-backed resolver tests exercise the exact same ledger content
// a real adoption would produce.
func seedLedger(t *testing.T, l *core.Ledger, addr core.Address, state string) {
	t.Helper()
	node, err := json.Marshal(map[string]interface{}{
		"stack": addr.Stack, "type": addr.Type, "name": addr.Name,
		"state": json.RawMessage(state),
	})
	if err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	head, err := l.Head()
	if err != nil {
		t.Fatalf("seed ledger: head: %v", err)
	}
	hash, err := core.ObservedHash(json.RawMessage(state))
	if err != nil {
		t.Fatalf("seed ledger: observed hash: %v", err)
	}
	lookup := core.DeriveLookupFromResult(json.RawMessage(state), nil)
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         addr.Stack,
		Parent:        head,
		Kind:          core.KindAdoption,
		Intent:        core.Intent{Summary: "seed " + addr.String()},
		Delta:         core.Delta{Creates: []json.RawMessage{node}},
		Resolution: core.Resolution{
			ResolvedAt: time.Now().UTC().Format(time.RFC3339),
			Inputs: []core.ResolutionInput{
				{Kind: "live_state", Resource: addr.String(), ObservedHash: hash, Lookup: lookup},
			},
		},
		CostDelta: core.CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		Status:    core.StatusDraft,
	}
	if _, err := core.Accept(l, p); err != nil {
		t.Fatalf("seed ledger: accept: %v", err)
	}
}

// --- happy paths ---------------------------------------------------------

func TestResolve_SingleCreate_NoRefs(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_vpc", "main", OpCreate, `{"cidr_block":"10.0.0.0/16"}`),
	)

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if p.Kind != core.KindChange {
		t.Fatalf("kind = %s, want change", p.Kind)
	}
	if len(p.Delta.Creates) != 1 || len(p.Delta.Modifies) != 0 {
		t.Fatalf("delta = %+v", p.Delta)
	}
	if p.BlastRadius.Creates != 1 {
		t.Fatalf("blast radius = %+v", p.BlastRadius)
	}
	var node map[string]interface{}
	json.Unmarshal(p.Delta.Creates[0], &node)
	if node["stack"] != "payments" || node["type"] != "aws_vpc" || node["name"] != "main" {
		t.Fatalf("create node = %v", node)
	}
	if err := core.Validate(p); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestResolve_RefToComputedSibling_MarksComputed_AndOrdersAfter(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments",
		// Deliberately out of dependency order in the input.
		ri("aws_db_instance", "replica", OpCreate, `{"replicate_source_db":{"$ref":{"to":"payments.aws_db_instance.primary.id"}}}`),
		ri("aws_db_instance", "primary", OpCreate, `{"instance_class":"db.t3.medium"}`),
	)

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(p.Delta.Creates) != 2 {
		t.Fatalf("creates = %d, want 2", len(p.Delta.Creates))
	}
	var first, second map[string]interface{}
	json.Unmarshal(p.Delta.Creates[0], &first)
	json.Unmarshal(p.Delta.Creates[1], &second)
	if first["name"] != "primary" || second["name"] != "replica" {
		t.Fatalf("execution order = %v, %v -- want primary before replica (dependency order)", first["name"], second["name"])
	}
	replicaCfg := second["config"].(map[string]interface{})
	rsdb := replicaCfg["replicate_source_db"].(map[string]interface{})
	computed, ok := rsdb["$computed"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected replicate_source_db to be $computed, got %v", rsdb)
	}
	if computed["from"] != "payments.aws_db_instance.primary.id" {
		t.Fatalf("$computed.from = %v", computed["from"])
	}
	dependsOn, _ := second["depends_on"].([]interface{})
	if len(dependsOn) != 1 || dependsOn[0] != "payments.aws_db_instance.primary" {
		t.Fatalf("depends_on = %v, want [payments.aws_db_instance.primary]", dependsOn)
	}
}

func TestResolve_RefToNonComputedSibling_SubstitutesLiteral(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_vpc", "main", OpCreate, `{"cidr_block":"10.0.0.0/16"}`),
		ri("aws_db_instance", "db", OpCreate, `{"vpc_cidr":{"$ref":{"to":"payments.aws_vpc.main.cidr_block"}}}`),
	)

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var dbNode map[string]interface{}
	json.Unmarshal(p.Delta.Creates[1], &dbNode)
	cfg := dbNode["config"].(map[string]interface{})
	if cfg["vpc_cidr"] != "10.0.0.0/16" {
		t.Fatalf("vpc_cidr = %v, want the literal substituted in directly, no $computed", cfg["vpc_cidr"])
	}
	// The dependency edge is still recorded even for a literal substitution.
	dependsOn, _ := dbNode["depends_on"].([]interface{})
	if len(dependsOn) != 1 || dependsOn[0] != "payments.aws_vpc.main" {
		t.Fatalf("depends_on = %v", dependsOn)
	}
}

func TestResolve_RefToExistingLedgeredResource_AlwaysConcrete(t *testing.T) {
	l := core.Open(t.TempDir())
	vpcAddr := core.Address{Stack: "payments", Type: "aws_vpc", Name: "main"}
	seedLedger(t, l, vpcAddr, `{"id":"vpc-123","cidr_block":"10.0.0.0/16"}`)

	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpCreate, `{"vpc_id":{"$ref":{"to":"payments.aws_vpc.main.id"}}}`),
	)

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var node map[string]interface{}
	json.Unmarshal(p.Delta.Creates[0], &node)
	cfg := node["config"].(map[string]interface{})
	if cfg["vpc_id"] != "vpc-123" {
		t.Fatalf("vpc_id = %v, want vpc-123 (already-ledgered resources are always concrete, even for a schema-Computed attribute)", cfg["vpc_id"])
	}
}

func TestResolve_Modify_DiffsAgainstFoldState(t *testing.T) {
	l := core.Open(t.TempDir())
	addr := core.Address{Stack: "payments", Type: "aws_db_instance", Name: "db"}
	seedLedger(t, l, addr, `{"id":"db-1","instance_class":"db.t3.medium","tags":{"env":"prod"}}`)

	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpModify, `{"id":"db-1","instance_class":"db.t3.large","tags":{"env":"prod"}}`),
	)

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(p.Delta.Modifies) != 1 || len(p.Delta.Creates) != 0 {
		t.Fatalf("delta = %+v", p.Delta)
	}
	mod := p.Delta.Modifies[0]
	if string(mod.Before["instance_class"]) != `"db.t3.medium"` || string(mod.After["instance_class"]) != `"db.t3.large"` {
		t.Fatalf("mod = %+v", mod)
	}
	if _, ok := mod.Before["tags.env"]; ok {
		t.Fatalf("unchanged tags.env must not appear in the diff, got %+v", mod.Before)
	}
	if err := core.Validate(p); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// TestResolve_Modify_NullVsAbsentAttribute_FilteredAsNoise is UBI-88's own
// regression test for a real gap found LIVE, not assumed away: this
// package's OpModify diff (before this fix) called core.DiffAttributes
// directly with no core.FilterNormalizationNoise pass at all -- so an
// attribute the ledger recorded as null/its own zero value, that a
// modify's own drafted config simply never mentions (an ordinary,
// legitimate omission -- the config is a partial document, not full
// state), rendered as spurious "null -> (absent)"/"{} -> (absent)" noise
// alongside any real change, exactly the null<->zero-value/materialization
// noise class UBI-63 already suppressed for drift comparison, just never
// wired into this diff. "labels" here stands in for a real live repro
// (fakeprovider's own "tags" map attribute, confirmed empirically to
// record as "{}" rather than null for an unset Optional map -- the SAME
// equivalence class, just spelled as a zero-value literal instead of a
// bare null) -- covering the broader zero-value shape, not only literal
// null, since a live rerun caught the narrower null-only fix missing it.
func TestResolve_Modify_NullVsAbsentAttribute_FilteredAsNoise(t *testing.T) {
	l := core.Open(t.TempDir())
	addr := core.Address{Stack: "payments", Type: "aws_db_instance", Name: "db"}
	seedLedger(t, l, addr, `{"id":"db-1","instance_class":"db.t3.medium","tags":{"env":"prod"},"labels":{}}`)

	schema := newFakeSchema()
	// Deliberately omits "labels" entirely -- never mentioned by the
	// drafted config, exactly like an attribute nobody touched.
	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpModify, `{"id":"db-1","instance_class":"db.t3.large","tags":{"env":"prod"}}`),
	)

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(p.Delta.Modifies) != 1 {
		t.Fatalf("delta = %+v", p.Delta)
	}
	mod := p.Delta.Modifies[0]
	if string(mod.Before["instance_class"]) != `"db.t3.medium"` || string(mod.After["instance_class"]) != `"db.t3.large"` {
		t.Fatalf("real change must still survive the filter, mod = %+v", mod)
	}
	if _, ok := mod.Before["labels"]; ok {
		t.Fatalf("labels: {} -> (absent) must be filtered as normalization noise, got Before=%+v After=%+v", mod.Before, mod.After)
	}
	if _, ok := mod.After["labels"]; ok {
		t.Fatalf("labels: {} -> (absent) must be filtered as normalization noise, got Before=%+v After=%+v", mod.Before, mod.After)
	}
}

// TestResolve_Modify_OmittedComputedAttribute_AutoPreserved is UBI-85's
// own regression test for a real gap found LIVE, not assumed away: an
// intent provider's own system prompt now instructs a drafted modify to
// reproduce every currently-recorded attribute unchanged (full-state
// config, matching create's own convention) -- but a real Claude response,
// confirmed running this session's own live finale, correctly detected
// and changed the one attribute that actually differed while genuinely
// omitting an unrelated schema-Computed "id" attribute from its own
// modify config anyway, despite that explicit instruction. Without this
// fix, DiffAttributes would read the omission as "id: removed" -- a
// spurious diff entry, not the clean before/after diff UBI-85 requires.
// A Computed attribute is never something a caller is expected to set in
// the first place, so Resolve now auto-fills one back in from the
// ledger's own current recorded value whenever a modify's own resolved
// config omits it entirely -- deterministic code guaranteeing what
// prompt-engineering alone couldn't. Two Computed attributes (id, arn)
// omitted at once, proving this isn't special-cased to a single key.
func TestResolve_Modify_OmittedComputedAttribute_AutoPreserved(t *testing.T) {
	l := core.Open(t.TempDir())
	addr := core.Address{Stack: "payments", Type: "aws_db_instance", Name: "db"}
	seedLedger(t, l, addr, `{"id":"db-1","arn":"arn:aws:rds:us-east-1:1:db:db-1","instance_class":"db.t3.medium","tags":{"env":"prod"}}`)

	schema := newFakeSchema()
	// Deliberately omits "id" and "arn" entirely -- exactly the real,
	// live-observed shape an imperfectly-compliant model produced.
	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpModify, `{"instance_class":"db.t3.large","tags":{"env":"prod"}}`),
	)

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(p.Delta.Modifies) != 1 {
		t.Fatalf("delta = %+v", p.Delta)
	}
	mod := p.Delta.Modifies[0]
	if string(mod.Before["instance_class"]) != `"db.t3.medium"` || string(mod.After["instance_class"]) != `"db.t3.large"` {
		t.Fatalf("mod = %+v", mod)
	}
	if _, ok := mod.Before["id"]; ok {
		t.Fatalf("omitted Computed \"id\" must be auto-preserved (never shown as removed), got Before=%+v", mod.Before)
	}
	if _, ok := mod.After["id"]; ok {
		t.Fatalf("omitted Computed \"id\" must be auto-preserved (never shown as newly-set either), got After=%+v", mod.After)
	}
	if _, ok := mod.Before["arn"]; ok {
		t.Fatalf("omitted Computed \"arn\" must be auto-preserved (never shown as removed), got Before=%+v", mod.Before)
	}
	if _, ok := mod.Before["tags.env"]; ok {
		t.Fatalf("unchanged tags.env must still not appear in the diff, got %+v", mod.Before)
	}
	if err := core.Validate(p); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

// --- docs/resolver-adversarial.md row 1: double-run divergence -----------

func TestResolve_DoubleRunDivergence_HardFails(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := &flakySchema{fakeSchema: newFakeSchema()}
	intent := intentFile("payments",
		ri("aws_db_instance", "primary", OpCreate, `{"instance_class":"db.t3.medium","identifier":"manual-id-value"}`),
		ri("aws_db_instance", "replica", OpCreate, `{"replicate_source_db":{"$ref":{"to":"payments.aws_db_instance.primary.identifier"}}}`),
	)

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, core.ErrDoubleRunMismatch) {
		t.Fatalf("err = %v, want ErrDoubleRunMismatch", err)
	}
}

// --- row 2: circular intra-stack refs -------------------------------------

func TestResolve_CircularRefs_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_db_instance", "a", OpCreate, `{"peer":{"$ref":{"to":"payments.aws_db_instance.b.id"}}}`),
		ri("aws_db_instance", "b", OpCreate, `{"peer":{"$ref":{"to":"payments.aws_db_instance.a.id"}}}`),
	)

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrCycleDetected) {
		t.Fatalf("err = %v, want ErrCycleDetected", err)
	}
}

// --- row 3: ref to nonexistent resource -----------------------------------

func TestResolve_RefToNonexistentResource_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpCreate, `{"vpc_id":{"$ref":{"to":"payments.aws_vpc.ghost.id"}}}`),
	)

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrRefNotFound) {
		t.Fatalf("err = %v, want ErrRefNotFound", err)
	}
}

// --- row 4: cross-stack pin against empty/missing neighbor ledger --------

func TestResolve_CrossStack_NeighborLedgerNeverInitialized_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	neighborDir := t.TempDir() // never touched -- no ledger/ subdir at all
	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpCreate, `{"vpc_id":{"$cross":{"ledger_dir":"`+neighborDir+`","to":"networking.aws_vpc.main.id"}}}`),
	)

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrNeighborLedgerMissing) {
		t.Fatalf("err = %v, want ErrNeighborLedgerMissing", err)
	}
}

func TestResolve_CrossStack_AddressNeverRecordedInRealNeighborLedger_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()

	neighborDir := t.TempDir()
	neighbor := core.Open(neighborDir)
	seedLedger(t, neighbor, core.Address{Stack: "networking", Type: "aws_vpc", Name: "other"}, `{"id":"vpc-999"}`)

	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpCreate, `{"vpc_id":{"$cross":{"ledger_dir":"`+neighborDir+`","to":"networking.aws_vpc.main.id"}}}`),
	)

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrCrossStackAddressNotFound) {
		t.Fatalf("err = %v, want ErrCrossStackAddressNotFound", err)
	}
}

func TestResolve_CrossStack_Concrete_RecordsPinnedHead(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()

	neighborDir := t.TempDir()
	neighbor := core.Open(neighborDir)
	seedLedger(t, neighbor, core.Address{Stack: "networking", Type: "aws_vpc", Name: "main"}, `{"id":"vpc-123"}`)
	wantHead, err := neighbor.Head()
	if err != nil {
		t.Fatalf("neighbor head: %v", err)
	}

	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpCreate, `{"vpc_id":{"$cross":{"ledger_dir":"`+neighborDir+`","to":"networking.aws_vpc.main.id"}}}`),
	)

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var node map[string]interface{}
	json.Unmarshal(p.Delta.Creates[0], &node)
	cfg := node["config"].(map[string]interface{})
	if cfg["vpc_id"] != "vpc-123" {
		t.Fatalf("vpc_id = %v, want vpc-123", cfg["vpc_id"])
	}

	var pin *core.ResolutionInput
	for i := range p.Resolution.Inputs {
		if p.Resolution.Inputs[i].Kind == "cross_stack_pin" {
			pin = &p.Resolution.Inputs[i]
		}
	}
	if pin == nil {
		t.Fatal("expected a cross_stack_pin resolution input")
	}
	if pin.PinnedHead != wantHead {
		t.Fatalf("pinned_head = %q, want %q", pin.PinnedHead, wantHead)
	}
	if pin.LedgerDir != neighborDir {
		t.Fatalf("ledger_dir = %q, want %q", pin.LedgerDir, neighborDir)
	}

	// row 5: neighbor advances between resolve and accept -- VerifyPins
	// must detect it.
	if err := VerifyPins(p); err != nil {
		t.Fatalf("VerifyPins before the neighbor advances: %v", err)
	}
	seedLedger(t, neighbor, core.Address{Stack: "networking", Type: "aws_vpc", Name: "second"}, `{"id":"vpc-456"}`)
	if err := VerifyPins(p); !errors.Is(err, ErrCrossStackPinStale) {
		t.Fatalf("VerifyPins after the neighbor advances: err = %v, want ErrCrossStackPinStale", err)
	}
}

// TestResolve_CrossStack_ResolutionInputRecordsReferencingResource proves
// UBI-47 session 4's own real fix: a cross_stack_pin's own From field
// names the LOCAL resource that held the $cross marker, not just the
// neighbor address it pinned (Resource) -- found while building ubx
// render's own $cross-annotation feature, since resolution.inputs used to
// have no way at all to answer "which of my own resources made this
// reference." Two resources in the same batch, only one of which
// cross-references, proves the attribution is per-resource, not merely
// "a cross-stack pin happened somewhere in this proposal."
func TestResolve_CrossStack_ResolutionInputRecordsReferencingResource(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()

	neighborDir := t.TempDir()
	neighbor := core.Open(neighborDir)
	seedLedger(t, neighbor, core.Address{Stack: "networking", Type: "aws_vpc", Name: "main"}, `{"id":"vpc-123"}`)

	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpCreate, `{"vpc_id":{"$cross":{"ledger_dir":"`+neighborDir+`","to":"networking.aws_vpc.main.id"}}}`),
		ri("aws_vpc", "unrelated", OpCreate, `{}`),
	)

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	var pins []core.ResolutionInput
	for _, in := range p.Resolution.Inputs {
		if in.Kind == "cross_stack_pin" {
			pins = append(pins, in)
		}
	}
	if len(pins) != 1 {
		t.Fatalf("cross_stack_pin entries = %+v, want exactly 1", pins)
	}
	if pins[0].From != "payments.aws_db_instance.db" {
		t.Fatalf("From = %q, want the referencing resource's own address, not %q (the neighbor)", pins[0].From, pins[0].Resource)
	}
	if pins[0].Resource != "networking.aws_vpc.main" {
		t.Fatalf("Resource = %q, want the neighbor's own address unchanged", pins[0].Resource)
	}
}

// --- row 6: $computed value used where concrete required -----------------

func TestResolve_ComputedPropagated_UsedWhereConcreteRequired_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	// c's "peer" field is not schema-Computed on aws_db_instance -- but b's
	// own resolved value for it is $computed, inherited from b's own ref to
	// a's real Computed "id". c's ref to b.peer must fail: the schema
	// promised b.peer would be concrete, but it isn't.
	intent := intentFile("payments",
		ri("aws_db_instance", "a", OpCreate, `{"instance_class":"db.t3.medium"}`),
		ri("aws_db_instance", "b", OpCreate, `{"peer":{"$ref":{"to":"payments.aws_db_instance.a.id"}}}`),
		ri("aws_db_instance", "c", OpCreate, `{"peer":{"$ref":{"to":"payments.aws_db_instance.b.peer"}}}`),
	)

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrComputedWhereConcreteRequired) {
		t.Fatalf("err = %v, want ErrComputedWhereConcreteRequired", err)
	}
}

// --- row 7: secret ref in a non-secret-capable field ----------------------

func TestResolve_SecretInNonSensitiveField_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpCreate, `{"instance_class":{"$secret":{"backend":"aws_secrets_manager","path":"x"}}}`),
	)

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrSecretNotSensitive) {
		t.Fatalf("err = %v, want ErrSecretNotSensitive", err)
	}
}

func TestResolve_SecretInSensitiveField_Accepted(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpCreate, `{"master_password":{"$secret":{"backend":"aws_secrets_manager","path":"payments/db-password"}}}`),
	)

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var node map[string]interface{}
	json.Unmarshal(p.Delta.Creates[0], &node)
	cfg := node["config"].(map[string]interface{})
	secret, ok := cfg["master_password"].(map[string]interface{})["$secret"].(map[string]interface{})
	if !ok || secret["path"] != "payments/db-password" {
		t.Fatalf("master_password = %v, want the $secret marker preserved", cfg["master_password"])
	}
}

// --- row 8: intent for a type the provider schema lacks -------------------

func TestResolve_UnknownType_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_nonexistent_type", "x", OpCreate, `{}`),
	)

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrUnknownType) {
		t.Fatalf("err = %v, want ErrUnknownType", err)
	}
}

// --- row 9: modify intent whose target isn't in the ledger ----------------

func TestResolve_ModifyTargetMissing_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_db_instance", "ghost", OpModify, `{"instance_class":"db.t3.large"}`),
	)

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrModifyTargetMissing) {
		t.Fatalf("err = %v, want ErrModifyTargetMissing", err)
	}
}

func TestResolve_CreateTargetAlreadyExists_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	addr := core.Address{Stack: "payments", Type: "aws_db_instance", Name: "db"}
	seedLedger(t, l, addr, `{"id":"db-1"}`)

	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpCreate, `{"instance_class":"db.t3.large"}`),
	)

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrCreateTargetExists) {
		t.Fatalf("err = %v, want ErrCreateTargetExists", err)
	}
}

// --- other structural validation ------------------------------------------

func TestResolve_UnknownIntentKind_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments")
	intent.Kind = "something-else/v1"

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrUnknownIntentKind) {
		t.Fatalf("err = %v, want ErrUnknownIntentKind", err)
	}
}

func TestResolve_InvalidOp_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_vpc", "main", "destroy", `{}`),
	)

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrInvalidOp) {
		t.Fatalf("err = %v, want ErrInvalidOp", err)
	}
}

func TestResolve_DuplicateResource_Rejected(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_vpc", "main", OpCreate, `{}`),
		ri("aws_vpc", "main", OpCreate, `{}`),
	)

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrDuplicateResource) {
		t.Fatalf("err = %v, want ErrDuplicateResource", err)
	}
}

// --- ephemeral: atomic pass-through ----------------------------------------

func TestResolve_EphemeralMarker_PassesThroughAtomically(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpCreate, `{"session_token":{"$ephemeral":true}}`),
	)

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var node map[string]interface{}
	json.Unmarshal(p.Delta.Creates[0], &node)
	cfg := node["config"].(map[string]interface{})
	tok, ok := cfg["session_token"].(map[string]interface{})
	if !ok || tok["$ephemeral"] != true {
		t.Fatalf("session_token = %v, want {\"$ephemeral\":true} preserved", cfg["session_token"])
	}
}

// See destroys_test.go for docs/resolver.md's own "Amendment (UBI-30):
// destroys" coverage -- change proposals may now legally carry destroys,
// superseding this file's own former TestValidate_ChangeProposalWithDestroys_Rejected.

// --- schema-key validation (UBI-66) ----------------------------------------

// TestResolve_UnknownConfigKey_ResourceWithThreeWrongKeys is UBI-66's own
// hermetic acceptance criterion, verbatim: "a drafted resource with 3
// wrong keys refused with 3 distinct teaching errors."
func TestResolve_UnknownConfigKey_ResourceWithThreeWrongKeys(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	schema.badKeys = map[string]map[string]string{
		"aws_db_instance": {
			"instance_id":   "id",
			"instance_arn":  "arn",
			"instance_size": "instance_class",
		},
	}
	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpCreate, `{"instance_id":"x","instance_arn":"y","instance_size":"z"}`),
	)

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if err == nil {
		t.Fatal("resolve: expected refusal, got nil error")
	}
	if !errors.Is(err, ErrUnknownConfigKey) {
		t.Fatalf("err = %v, want errors.Is ErrUnknownConfigKey", err)
	}
	// errors.Join separates each wrapped error's own Error() text with a
	// newline -- three distinct lines, not one merged message.
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		t.Fatalf("err = %T, want an errors.Join result (Unwrap() []error)", err)
	}
	errs := joined.Unwrap()
	if len(errs) != 3 {
		t.Fatalf("joined errors = %d, want exactly 3: %v", len(errs), errs)
	}
	for _, want := range []string{
		`"instance_id" does not exist on aws_db_instance (did you mean "id"?)`,
		`"instance_arn" does not exist on aws_db_instance (did you mean "arn"?)`,
		`"instance_size" does not exist on aws_db_instance (did you mean "instance_class"?)`,
	} {
		found := false
		for _, e := range errs {
			if strings.Contains(e.Error(), want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected one error to contain %q, got: %v", want, errs)
		}
	}
}

// TestResolve_UnknownConfigKey_AggregatesAcrossWholeBatch reproduces the
// exact live incident UBI-66 was filed against, structurally: multiple
// DIFFERENT resources, each with its own hallucinated attribute name,
// resolved together in one intent file -- every mistake reported in one
// refusal, not just the first resource's.
func TestResolve_UnknownConfigKey_AggregatesAcrossWholeBatch(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	schema.types["aws_ecr_repository"] = true
	schema.badKeys = map[string]map[string]string{
		"aws_ecr_repository": {"repository_name": "name"},
		"aws_db_instance":    {"instance_class_name": "instance_class"},
	}
	intent := intentFile("payments",
		ri("aws_ecr_repository", "repo", OpCreate, `{"repository_name":"my-repo"}`),
		ri("aws_db_instance", "db", OpCreate, `{"instance_class_name":"db.t3.small"}`),
	)

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrUnknownConfigKey) {
		t.Fatalf("err = %v, want errors.Is ErrUnknownConfigKey", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, `"repository_name" does not exist on aws_ecr_repository (did you mean "name"?)`) {
		t.Errorf("missing ecr repository issue in: %s", msg)
	}
	if !strings.Contains(msg, `"instance_class_name" does not exist on aws_db_instance (did you mean "instance_class"?)`) {
		t.Errorf("missing db instance issue in: %s", msg)
	}
}

// TestResolve_UnknownConfigKey_NoSuggestion proves the joined error's own
// text omits the "(did you mean ...)" clause entirely when the fake
// schema reports no close match -- never a dangling/empty suggestion.
func TestResolve_UnknownConfigKey_NoSuggestion(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	schema.badKeys = map[string]map[string]string{
		"aws_vpc": {"totally_unrelated_xyz": ""},
	}
	intent := intentFile("payments",
		ri("aws_vpc", "main", OpCreate, `{"totally_unrelated_xyz":"z"}`),
	)

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrUnknownConfigKey) {
		t.Fatalf("err = %v, want errors.Is ErrUnknownConfigKey", err)
	}
	if strings.Contains(err.Error(), "did you mean") {
		t.Errorf("expected no suggestion clause, got: %s", err.Error())
	}
}

// TestResolve_UnknownConfigKey_ModelAgnostic proves the check has no
// awareness of, or special case for, WHERE a ResourceIntent came from --
// resolveOnce's own loop calls SchemaInspector.UnknownConfigKeys for
// every batch entry uniformly, with no branch on provenance at all. This
// intent file is built exactly the way a hand-authored ubx:intent/v1
// file would be (the same `ri`/`intentFile` helpers every other resolver
// test in this file uses for a plain hand-authored case) -- proving the
// SAME refusal applies to a hand-typed file, not just an
// intentprovider.Adapter-produced draft (see also
// intentprovider/conformance's own live-repro regression test, which
// proves the adapter-produced-draft side of this same claim).
func TestResolve_UnknownConfigKey_ModelAgnostic_HandAuthoredFile(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	schema.badKeys = map[string]map[string]string{
		"aws_vpc": {"vpc_cidr": "cidr_block"},
	}
	intent := intentFile("payments",
		ri("aws_vpc", "main", OpCreate, `{"vpc_cidr":"10.0.0.0/16"}`),
	)

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrUnknownConfigKey) {
		t.Fatalf("hand-authored intent file: err = %v, want errors.Is ErrUnknownConfigKey", err)
	}
}

// TestResolve_RecognizedConfigKeys_NoRefusal is the positive twin: a
// resource using only real keys resolves cleanly, even against a schema
// with badKeys configured for OTHER keys on the same type.
func TestResolve_RecognizedConfigKeys_NoRefusal(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	schema.badKeys = map[string]map[string]string{
		"aws_vpc": {"vpc_cidr": "cidr_block"},
	}
	intent := intentFile("payments",
		ri("aws_vpc", "main", OpCreate, `{"cidr_block":"10.0.0.0/16"}`),
	)

	if _, err := Resolve(l, singleProvider(schema), intent, nil); err != nil {
		t.Fatalf("resolve: %v", err)
	}
}
