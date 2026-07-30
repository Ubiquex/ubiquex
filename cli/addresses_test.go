package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/core"
)

// shipFakeWidgetCreate hand-builds and accepts+ships a kind:"change"
// create proposal for a fake_widget resource with a real recorded
// provider (fake/widget@0.1.0, the identical source/type/version
// writeMirrorProvider -- cli/sdk_test.go -- already places under
// UBX_PROVIDER_MIRROR), the cli-package equivalent of
// core.shipChangeCreateWithProviderForTest (unexported to core's own
// _test.go files, so re-built here from the same exported primitives).
func shipFakeWidgetCreate(t *testing.T, l *core.Ledger, addr core.Address, id string) *core.Proposal {
	t.Helper()
	node, err := json.Marshal(map[string]interface{}{
		"stack": addr.Stack, "type": addr.Type, "name": addr.Name,
		"provider": &core.ProviderRef{Source: "fake/widget", Version: "0.1.0"},
		"config":   map[string]interface{}{"name": addr.Name},
	})
	if err != nil {
		t.Fatal(err)
	}
	head, err := l.Head()
	if err != nil {
		t.Fatal(err)
	}
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         addr.Stack,
		Parent:        head,
		Kind:          core.KindChange,
		Intent:        core.Intent{Summary: "test create " + addr.String()},
		Delta:         core.Delta{Creates: []json.RawMessage{node}},
		Resolution:    core.Resolution{ResolvedAt: time.Now().UTC().Format(time.RFC3339)},
		CostDelta:     core.CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius:   core.BlastRadius{Creates: 1},
		Status:        core.StatusDraft,
	}
	accepted, err := core.Accept(l, p)
	if err != nil {
		t.Fatalf("accept change: %v", err)
	}

	rec, err := l.BeginApply(accepted.ID)
	if err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	result := json.RawMessage(`{"id":"` + id + `","name":"` + addr.Name + `"}`)
	ra := &core.ResourceApply{
		Address: addr,
		Transitions: []core.Transition{
			{State: core.ResourcePending, At: now},
			{State: core.ResourceInFlight, At: now},
			{State: core.ResourceApplied, At: now},
		},
		ProviderResult: result,
		Lookup:         core.DeriveLookupFromResult(result),
	}
	rec.Resources = append(rec.Resources, ra)
	if err := l.SaveApplyProgress(rec); err != nil {
		t.Fatalf("save apply progress: %v", err)
	}
	if _, err := l.SealApply(rec, core.ApplySummary{
		Outcome: "applied", StartedAt: now, FinishedAt: now, ResourcesApplied: 1,
	}); err != nil {
		t.Fatalf("seal apply: %v", err)
	}
	return accepted
}

// shipFakeWidgetDestroy hand-builds, accepts, and ships a destroy for an
// already-created fake_widget address -- the cli-package equivalent of
// core.shipChangeDestroyForTest, needed here since that helper is
// unexported to core's own _test.go files too.
func shipFakeWidgetDestroy(t *testing.T, l *core.Ledger, addr core.Address, state json.RawMessage) *core.Proposal {
	t.Helper()
	head, err := l.Head()
	if err != nil {
		t.Fatal(err)
	}
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         addr.Stack,
		Parent:        head,
		Kind:          core.KindChange,
		Intent:        core.Intent{Summary: "destroy " + addr.String()},
		Delta: core.Delta{Destroys: []core.DestroyEntry{
			{Address: addr, State: state, Provider: &core.ProviderRef{Source: "fake/widget", Version: "0.1.0"}},
		}},
		Resolution: core.Resolution{
			ResolvedAt: time.Now().UTC().Format(time.RFC3339),
			Inputs: []core.ResolutionInput{
				{Kind: "destroy_target", Resource: addr.String(), ObservedHash: "deadbeef", Lookup: core.DeriveLookupFromResult(state)},
			},
		},
		CostDelta:   core.CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius: core.BlastRadius{Destroys: 1},
		Status:      core.StatusDraft,
	}
	accepted, err := core.Accept(l, p)
	if err != nil {
		t.Fatalf("accept destroy: %v", err)
	}

	rec, err := l.BeginApply(accepted.ID)
	if err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rec.Resources = append(rec.Resources, &core.ResourceApply{
		Address: addr,
		Transitions: []core.Transition{
			{State: core.ResourcePending, At: now},
			{State: core.ResourceInFlight, At: now},
			{State: core.ResourceApplied, At: now},
		},
		Reconciliation: []core.ReconciliationAttempt{{At: now, Outcome: "destroyed"}},
	})
	if err := l.SaveApplyProgress(rec); err != nil {
		t.Fatalf("save apply progress: %v", err)
	}
	if _, err := l.SealApply(rec, core.ApplySummary{
		Outcome: "applied", StartedAt: now, FinishedAt: now, ResourcesApplied: 1,
	}); err != nil {
		t.Fatalf("seal apply: %v", err)
	}
	return accepted
}

func addressesMirrorEnv(t *testing.T) (mirrorDir string, env []string) {
	t.Helper()
	mirrorDir = t.TempDir()
	writeMirrorProvider(t, mirrorDir, "fake", "widget", "0.1.0")
	return mirrorDir, []string{"FAKEPROVIDER_MODE=ok-v6", "UBX_PROVIDER_MIRROR=" + mirrorDir}
}

func TestAddresses_EmptyLedger(t *testing.T) {
	out, err := runUbx(t, nil, "addresses", "--ledger-dir", t.TempDir())
	if err != nil {
		t.Fatalf("ubx addresses: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "no addresses recorded") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestAddresses_ActiveInventory_AttributesAndCrossForms(t *testing.T) {
	ledgerDir := t.TempDir()
	l := core.Open(ledgerDir)
	addr := core.Address{Stack: "payments", Type: "fake_widget", Name: "widget-1"}
	shipFakeWidgetCreate(t, l, addr, "w-1")

	_, env := addressesMirrorEnv(t)
	out, err := runUbx(t, env, "addresses", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx addresses: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "payments.fake_widget.widget-1") {
		t.Fatalf("expected the address in output, got: %s", out)
	}
	if !strings.Contains(out, "provider: fake/widget@0.1.0") {
		t.Fatalf("expected the provider line, got: %s", out)
	}
	if !strings.Contains(out, "@payments.fake_widget.widget-1.id (computed, known after apply)") {
		t.Fatalf("expected id marked computed, got: %s", out)
	}
	if !strings.Contains(out, "@payments.fake_widget.widget-1.name (always present)") {
		t.Fatalf("expected name marked always-present, got: %s", out)
	}
	if !strings.Contains(out, "@payments.fake_widget.widget-1.tags (always present)") {
		t.Fatalf("expected tags marked always-present, got: %s", out)
	}
}

func TestAddresses_Tombstoned_ExcludedByDefault_IncludedWithAll(t *testing.T) {
	ledgerDir := t.TempDir()
	l := core.Open(ledgerDir)
	addr := core.Address{Stack: "payments", Type: "fake_widget", Name: "widget-gone"}
	shipFakeWidgetCreate(t, l, addr, "w-gone")
	destroy := shipFakeWidgetDestroy(t, l, addr, json.RawMessage(`{"id":"w-gone","name":"widget-gone"}`))

	_, env := addressesMirrorEnv(t)
	out, err := runUbx(t, env, "addresses", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx addresses: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "no addresses recorded") {
		t.Fatalf("expected a destroyed-only stack to render as empty by default, got: %s", out)
	}

	allOut, err := runUbx(t, env, "addresses", "--ledger-dir", ledgerDir, "--all")
	if err != nil {
		t.Fatalf("ubx addresses --all: %v\noutput: %s", err, allOut)
	}
	if !strings.Contains(allOut, "payments.fake_widget.widget-gone") {
		t.Fatalf("expected the tombstoned address with --all, got: %s", allOut)
	}
	if !strings.Contains(allOut, "DESTROYED by "+displayHash(destroy.ID, false)) {
		t.Fatalf("expected a DESTROYED annotation naming %s, got: %s", displayHash(destroy.ID, false), allOut)
	}
}

func TestAddresses_AdoptedResource_SchemaMissingNotAFailure(t *testing.T) {
	ledgerDir := t.TempDir()
	l := core.Open(ledgerDir)
	adoptForTestCLI(t, l, core.Address{Stack: "payments", Type: "fake_widget", Name: "widget-adopted"}, json.RawMessage(`{"id":"w-adopted"}`))

	out, err := runUbx(t, nil, "addresses", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx addresses: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "attributes unknown: no recorded provider") {
		t.Fatalf("expected a schema-missing annotation, not a command failure, got: %s", out)
	}
}

// adoptForTestCLI mirrors core.adoptForTest, re-built from exported
// primitives for the same reason shipFakeWidgetCreate/Destroy are above.
func adoptForTestCLI(t *testing.T, l *core.Ledger, addr core.Address, state json.RawMessage) *core.Proposal {
	t.Helper()
	head, err := l.Head()
	if err != nil {
		t.Fatal(err)
	}
	node, err := json.Marshal(map[string]interface{}{
		"stack": addr.Stack, "type": addr.Type, "name": addr.Name,
		"state": state,
	})
	if err != nil {
		t.Fatal(err)
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
				{Kind: "live_state", Resource: addr.String(), ObservedHash: "deadbeef", Lookup: core.DeriveLookupFromResult(state)},
			},
		},
		CostDelta: core.CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		Status:    core.StatusDraft,
	}
	accepted, err := core.Accept(l, p)
	if err != nil {
		t.Fatalf("adopt %s: %v", addr, err)
	}
	return accepted
}

func TestAddresses_JSON_Shape(t *testing.T) {
	ledgerDir := t.TempDir()
	l := core.Open(ledgerDir)
	addr := core.Address{Stack: "payments", Type: "fake_widget", Name: "widget-json"}
	shipFakeWidgetCreate(t, l, addr, "w-json")

	_, env := addressesMirrorEnv(t)
	out, err := runUbx(t, env, "addresses", "--ledger-dir", ledgerDir, "--json")
	if err != nil {
		t.Fatalf("ubx addresses --json: %v\noutput: %s", err, out)
	}

	var payload struct {
		Format  int `json:"format"`
		Entries []struct {
			Address struct {
				Stack string `json:"stack"`
				Type  string `json:"type"`
				Name  string `json:"name"`
			} `json:"address"`
			Tombstoned bool `json:"tombstoned"`
			Provider   struct {
				Source  string `json:"source"`
				Version string `json:"version"`
			} `json:"provider"`
			Attributes []struct {
				Name      string `json:"name"`
				Computed  bool   `json:"computed"`
				CrossForm string `json:"cross_form"`
			} `json:"attributes"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal: %v\noutput: %s", err, out)
	}
	if payload.Format != 1 || len(payload.Entries) != 1 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	e := payload.Entries[0]
	if e.Address.Stack != "payments" || e.Address.Type != "fake_widget" || e.Address.Name != "widget-json" {
		t.Fatalf("unexpected address: %+v", e.Address)
	}
	if e.Tombstoned {
		t.Fatalf("Tombstoned = true, want false")
	}
	if e.Provider.Source != "fake/widget" || e.Provider.Version != "0.1.0" {
		t.Fatalf("unexpected provider: %+v", e.Provider)
	}
	found := map[string]bool{}
	for _, a := range e.Attributes {
		found[a.Name] = a.Computed
		if a.CrossForm != "@payments.fake_widget.widget-json."+a.Name {
			t.Fatalf("unexpected cross_form: %s", a.CrossForm)
		}
	}
	if !found["id"] {
		t.Fatalf("expected id to be computed, got: %+v", found)
	}
	if computed, ok := found["name"]; !ok || computed {
		t.Fatalf("expected name to be present and non-computed, got: %+v", found)
	}
}

func TestAddresses_UnknownStack_TeachingError(t *testing.T) {
	dir := t.TempDir()
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `stack = "payments"`)

	out, err := runUbx(t, nil, "addresses", "--ledger-dir", dir, "--stack", "networking")
	requireExitCode(t, err, 2, out)
	if !strings.Contains(err.Error(), `unknown stack "networking"`) {
		t.Fatalf("expected an unknown-stack error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "payments (this workspace's own stack)") {
		t.Fatalf("expected the teaching error to name the known stack, got: %v", err)
	}
}

func TestAddresses_UnknownStack_NoConfigAtAll(t *testing.T) {
	dir := t.TempDir()
	withConfigSearchDir(t, dir)

	out, err := runUbx(t, nil, "addresses", "--ledger-dir", dir, "--stack", "networking")
	requireExitCode(t, err, 2, out)
	if !strings.Contains(err.Error(), "declares no stack of its own and no [ledger.external] entries") {
		t.Fatalf("expected the no-config teaching error, got: %v", err)
	}
}

// TestAddresses_CrossStack_ViaExternalOverride is UBI-48's own central
// claim: --stack resolves a neighbor via [ledger.external], the exact
// mechanism $cross's own "stack" form uses (core/resolver/refs.go
// resolveCross), never requiring a cd into the neighbor's own directory.
// The neighbor's own ledger lives at <base>/networking/ -- a plain
// filesystem path, not a store URI, so core.OpenRef opens it exactly
// the way a git-local $cross "stack" reference already does when
// [ledger.external] points at another local directory tree.
func TestAddresses_CrossStack_ViaExternalOverride(t *testing.T) {
	base := t.TempDir()
	neighborDir := filepath.Join(base, "networking")
	if err := os.MkdirAll(neighborDir, 0o755); err != nil {
		t.Fatal(err)
	}
	neighborLedger := core.Open(neighborDir)
	addr := core.Address{Stack: "networking", Type: "fake_widget", Name: "vpc-main"}
	shipFakeWidgetCreate(t, neighborLedger, addr, "w-vpc")

	localDir := t.TempDir()
	withConfigSearchDir(t, localDir)
	writeConfig(t, localDir, `
stack = "payments"

[ledger.external]
networking = "`+filepath.ToSlash(base)+`"
`)

	_, env := addressesMirrorEnv(t)
	out, err := runUbx(t, env, "addresses", "--ledger-dir", localDir, "--stack", "networking")
	if err != nil {
		t.Fatalf("ubx addresses --stack networking: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "networking.fake_widget.vpc-main") {
		t.Fatalf("expected the neighbor's own address, got: %s", out)
	}
}

func TestAddresses_CrossStack_ReflexiveOwnStack_OpensLocally(t *testing.T) {
	localDir := t.TempDir()
	l := core.Open(localDir)
	addr := core.Address{Stack: "payments", Type: "fake_widget", Name: "widget-local"}
	shipFakeWidgetCreate(t, l, addr, "w-local")

	withConfigSearchDir(t, localDir)
	writeConfig(t, localDir, `stack = "payments"`)

	_, env := addressesMirrorEnv(t)
	out, err := runUbx(t, env, "addresses", "--ledger-dir", localDir, "--stack", "payments")
	if err != nil {
		t.Fatalf("ubx addresses --stack payments: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "payments.fake_widget.widget-local") {
		t.Fatalf("expected the local address, got: %s", out)
	}
}
