package resolver

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex-cli/core"
)

// fakeSchema is a hermetic SchemaInspector -- real provider Sensitive/
// Computed flags exist and are checked elsewhere (UBI-23/24); this proves
// core/resolver's own rules against a schema shape it never has to trust
// blindly. Keys are "<type>.<attrPath>".
type fakeSchema struct {
	types     map[string]bool
	computed  map[string]bool
	sensitive map[string]bool
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
				{Kind: "live_state", Resource: addr.String(), ObservedHash: hash},
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
