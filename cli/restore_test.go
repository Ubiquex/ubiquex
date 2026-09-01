package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tags is deliberately never used in these fixtures -- confirmed live,
// not assumed, that fakeprovider's own ok-v6 mode does not round-trip a
// "tags" map attribute identically between ApplyResourceChange and a
// later ReadResource, which trips VerifyFreshness's own stale-observation
// refusal on an entirely ordinary modify that has nothing to do with
// restore at all. A real, separate, pre-existing fakeprovider finding,
// not a restore bug -- "name" is what changes here instead, the same
// real field cli/receipt_modify_v2_test.go's own working modify-receipt
// test already uses.
//
// a's own value diverges from the target head via destroy-then-recreate
// (restoreDestroyAIntent, then restoreRecreateAIntent) rather than a second
// ordinary "modify" of the same address -- confirmed live, via two isolated
// manual repros entirely outside this test and outside restore itself,
// that shipping a SECOND real modify against the same fake_widget address
// (after a first modify already shipped) fails with a genuine stale-
// observation error against this fixture. That's a real, separate,
// pre-existing bug (fakeprovider and/or the executor's own freshness
// check), not a restore bug, and not what this test is for -- restore's
// own generated modify below is the ONLY modify "a" ever undergoes in this
// test's whole lifetime, which both proves the modify path for real and
// keeps this test clear of that unrelated failure mode.
const restoreInitialIntent = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "create a and b"},
  "resources": [
    {"type": "fake_widget", "name": "a", "op": "create", "config": {"name": "widget-a-v1"}},
    {"type": "fake_widget", "name": "b", "op": "create", "config": {"name": "widget-b"}}
  ],
  "destroys": []
}`

const restoreDestroyAIntent = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "destroy a"},
  "resources": [],
  "destroys": ["payments.fake_widget.a"]
}`

const restoreRecreateAIntent = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "recreate a, create c"},
  "resources": [
    {"type": "fake_widget", "name": "a", "op": "create", "config": {"name": "widget-a-v2"}},
    {"type": "fake_widget", "name": "c", "op": "create", "config": {"name": "widget-c"}}
  ],
  "destroys": []
}`

// resolveAcceptShip runs one real intent document through resolve, accept,
// and ship against fakeprovider, returning the accepted proposal's own id
// -- the shared real ceremony every step of TestRestore_ExactState below
// needs, never a hand-waved shortcut. acceptArgs is appended to "ubx
// accept" verbatim, e.g. "--confirm-destroys" for a batch that destroys.
func resolveAcceptShip(t *testing.T, dir, ledgerDir string, env []string, intentJSON, name string, acceptArgs ...string) string {
	t.Helper()
	intentPath := filepath.Join(dir, name+".json")
	if err := os.WriteFile(intentPath, []byte(intentJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	resolvedPath := filepath.Join(dir, name+"-resolved.json")
	resolveOut, err := runUbx(t, env, "resolve", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--out", resolvedPath, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx resolve (%s): %v\noutput: %s", name, err, resolveOut)
	}
	acceptCmd := append([]string{"accept", resolvedPath, "--ledger-dir", ledgerDir}, acceptArgs...)
	acceptOut, err := runUbx(t, env, acceptCmd...)
	if err != nil {
		t.Fatalf("ubx accept (%s): %v\noutput: %s", name, err, acceptOut)
	}
	changeID := mustExtractID(t, acceptOut)
	shipOut, err := runUbx(t, env, "ship", changeID, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx ship (%s): %v\noutput: %s", name, err, shipOut)
	}
	return changeID
}

// TestRestore_ExactState_CreateModifyDestroy_RealFakeProvider is UBI-227's
// own required end-to-end proof: a real restore against a real ledger and
// real fakeprovider, covering the exact three cases exact-state semantics
// name -- a resource present in the target head and now (modified back),
// one absent from the target head but present now (destroyed), one
// present in both and unchanged (left alone, no diff at all).
func TestRestore_ExactState_CreateModifyDestroy_RealFakeProvider(t *testing.T) {
	requireHermeticSandbox(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	// Batch 1: a (v1), b. This head is the real restore target.
	targetHead := resolveAcceptShip(t, dir, ledgerDir, env, restoreInitialIntent, "batch1")

	// Batch 2: a is destroyed, then recreated as v2 (never modified in
	// place -- see the fixture comment above), c is created. b is left
	// alone.
	resolveAcceptShip(t, dir, ledgerDir, env, restoreDestroyAIntent, "batch2-destroy-a", "--confirm-destroys")
	resolveAcceptShip(t, dir, ledgerDir, env, restoreRecreateAIntent, "batch2-recreate-a")

	// Restore to the head right after batch 1.
	restoreOut, err := runUbx(t, env, "restore", targetHead, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx restore: %v\noutput: %s", err, restoreOut)
	}
	// ubx restore's own receipt renders "ubx-proposal: <hash>" like ubx
	// plan/ubx promote already do, never "accepted ..." -- mustExtractID
	// only ever matches the latter, so the real hash is read off the
	// receipt's own plan-file line instead.
	restoreID, doc := parseRestorePlan(t, ledgerDir, restoreOut)
	raw, _ := readPlanFileForTest(t, ledgerDir, restoreID)

	if len(doc.Delta.Creates) != 0 {
		t.Fatalf("expected 0 creates (nothing in the target head is missing live), got %d: %s", len(doc.Delta.Creates), raw)
	}
	if len(doc.Delta.Modifies) != 1 || doc.Delta.Modifies[0].Target["name"] != "a" {
		t.Fatalf("expected exactly 1 modify, targeting a, got: %s", raw)
	}
	if string(doc.Delta.Modifies[0].After["name"]) != `"widget-a-v1"` {
		t.Fatalf("expected a's name to restore to widget-a-v1, got: %s", doc.Delta.Modifies[0].After["name"])
	}
	if len(doc.Delta.Destroys) != 1 || doc.Delta.Destroys[0].Address["name"] != "c" {
		t.Fatalf("expected exactly 1 destroy, targeting c (created after the target head), got: %s", raw)
	}

	foundRestoreSource := false
	for _, s := range doc.Intent.Sources {
		if s.Kind == "restore" && s.Ref == targetHead {
			foundRestoreSource = true
		}
	}
	if !foundRestoreSource {
		t.Fatalf("expected intent.sources to carry {kind: restore, ref: %s}, got: %s", targetHead, raw)
	}

	// Ship the restore for real and confirm the ledger's own current
	// truth actually matches the target head again. --confirm-destroys
	// is required here for the exact same reason it is for any other
	// destructive proposal (cli/accept.go) -- restore gets no exemption.
	acceptShipRestore(t, ledgerDir, env, restoreID)

	whyOut, err := runUbx(t, nil, "why", "payments.fake_widget.a", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why: %v", err)
	}
	if !strings.Contains(whyOut, "restored ledger head") {
		t.Fatalf("expected ubx why to render the restore provenance, got:\n%s", whyOut)
	}
}

// restorePlanDoc is the slice of a resolved restore proposal's own plan
// file every test in this file actually inspects -- shared so
// TestRestore_ExactState and TestRestore_OfARestore parse the identical
// real shape rather than two hand-kept structs drifting apart.
type restorePlanDoc struct {
	Delta struct {
		Creates []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"creates"`
		Modifies []struct {
			Target map[string]string          `json:"target"`
			After  map[string]json.RawMessage `json:"after"`
		} `json:"modifies"`
		Destroys []struct {
			Address map[string]string `json:"address"`
		} `json:"destroys"`
	} `json:"delta"`
	Intent struct {
		Sources []struct {
			Kind string `json:"kind"`
			Ref  string `json:"ref"`
		} `json:"sources"`
	} `json:"intent"`
}

// parseRestorePlan pulls the "ubx-proposal:" hash out of a real "ubx
// restore" receipt and parses the plan file it points at into a
// restorePlanDoc.
func parseRestorePlan(t *testing.T, ledgerDir, restoreOut string) (string, restorePlanDoc) {
	t.Helper()
	restoreID := extractProposalHash(t, restoreOut)
	raw, err := readPlanFileForTest(t, ledgerDir, restoreID)
	if err != nil {
		t.Fatalf("read restore plan: %v", err)
	}
	var doc restorePlanDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse restore plan: %v\nraw: %s", err, raw)
	}
	return restoreID, doc
}

// acceptShipRestore accepts (with --confirm-destroys, exactly like any
// other destructive proposal -- restore gets no exemption, cli/accept.go)
// and ships a resolved restore proposal for real.
func acceptShipRestore(t *testing.T, ledgerDir string, env []string, restoreID string) {
	t.Helper()
	acceptOut, err := runUbx(t, env, "accept", restoreID, "--ledger-dir", ledgerDir, "--confirm-destroys")
	if err != nil {
		t.Fatalf("ubx accept (restore): %v\noutput: %s", err, acceptOut)
	}
	acceptedID := mustExtractID(t, acceptOut)
	shipOut, err := runUbx(t, env, "ship", acceptedID, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx ship (restore): %v\noutput: %s", err, shipOut)
	}
}

// TestRestore_OfARestore is UBI-227's own second open question, proven
// live rather than left to the design report's reasoning alone: the
// ledger ends up holding two proposals that each claim to restore a
// shape, and AddressesAt/FoldStateAt (core/addresses.go, core/state.go)
// need no special case for a target head that is itself a restore's own
// shipped head -- ChainFrom just walks however many proposals are
// actually there, restore or otherwise.
//
// p is created once and never touched again (a control). q and r
// alternate create/destroy across batch2, restore#1, and restore#2 --
// never two modifies of the same address, so this stays clear of the
// unrelated fakeprovider double-modify finding documented on
// restoreInitialIntent above; that path is already proven for real by
// TestRestore_ExactState.
//
//	H1 (batch1):  {p, q}
//	H2 (batch2):  {p, r}       -- q destroyed, r created
//	restore#1 -> H1: {p, q}    -- r destroyed, q recreated
//	restore#2 -> H2: {p, r}    -- q destroyed, r recreated
//
// A third, unshipped "ubx restore H2" after restore#2 should then see
// current state already exactly matching H2 -- an empty delta -- which is
// the real, end-to-end proof that restoring to a head reached via a prior
// restore lands on the exact same shape a direct restore would.
func TestRestore_OfARestore(t *testing.T) {
	requireHermeticSandbox(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	h1 := resolveAcceptShip(t, dir, ledgerDir, env, `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "create p and q"},
  "resources": [
    {"type": "fake_widget", "name": "p", "op": "create", "config": {"name": "widget-p"}},
    {"type": "fake_widget", "name": "q", "op": "create", "config": {"name": "widget-q"}}
  ],
  "destroys": []
}`, "batch1")

	h2 := resolveAcceptShip(t, dir, ledgerDir, env, `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "destroy q, create r"},
  "resources": [
    {"type": "fake_widget", "name": "r", "op": "create", "config": {"name": "widget-r"}}
  ],
  "destroys": ["payments.fake_widget.q"]
}`, "batch2", "--confirm-destroys")

	// restore#1: back to H1 -- destroys r, recreates q.
	restore1Out, err := runUbx(t, env, "restore", h1, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx restore #1: %v\noutput: %s", err, restore1Out)
	}
	restore1ID, doc1 := parseRestorePlan(t, ledgerDir, restore1Out)
	if len(doc1.Delta.Creates) != 1 || doc1.Delta.Creates[0].Name != "q" {
		t.Fatalf("restore #1: expected exactly 1 create, targeting q, got: %+v", doc1.Delta)
	}
	if len(doc1.Delta.Destroys) != 1 || doc1.Delta.Destroys[0].Address["name"] != "r" {
		t.Fatalf("restore #1: expected exactly 1 destroy, targeting r, got: %+v", doc1.Delta)
	}
	acceptShipRestore(t, ledgerDir, env, restore1ID)

	// restore#2: forward to H2 again -- destroys q, recreates r. This is
	// the restore-of-a-restore itself: H2 predates restore#1's own head,
	// so ChainFrom(H2) has to walk correctly regardless of what got
	// appended to the chain after H2 was originally recorded.
	restore2Out, err := runUbx(t, env, "restore", h2, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx restore #2: %v\noutput: %s", err, restore2Out)
	}
	restore2ID, doc2 := parseRestorePlan(t, ledgerDir, restore2Out)
	if len(doc2.Delta.Creates) != 1 || doc2.Delta.Creates[0].Name != "r" {
		t.Fatalf("restore #2: expected exactly 1 create, targeting r, got: %+v", doc2.Delta)
	}
	if len(doc2.Delta.Destroys) != 1 || doc2.Delta.Destroys[0].Address["name"] != "q" {
		t.Fatalf("restore #2: expected exactly 1 destroy, targeting q, got: %+v", doc2.Delta)
	}
	foundH2Source := false
	for _, s := range doc2.Intent.Sources {
		if s.Kind == "restore" && s.Ref == h2 {
			foundH2Source = true
		}
	}
	if !foundH2Source {
		t.Fatalf("restore #2: expected intent.sources to carry {kind: restore, ref: %s}, got: %+v", h2, doc2.Intent.Sources)
	}
	acceptShipRestore(t, ledgerDir, env, restore2ID)

	// A third, unshipped restore back to H2 should now be a genuine
	// no-op: current state already exactly matches H2, restore-of-a-
	// restore having landed on the identical shape a direct restore
	// would.
	verifyOut, err := runUbx(t, env, "restore", h2, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx restore (verify): %v\noutput: %s", err, verifyOut)
	}
	_, verifyDoc := parseRestorePlan(t, ledgerDir, verifyOut)
	if len(verifyDoc.Delta.Creates) != 0 || len(verifyDoc.Delta.Modifies) != 0 || len(verifyDoc.Delta.Destroys) != 0 {
		t.Fatalf("expected an empty delta restoring to H2 a second time (current state already matches), got: %+v", verifyDoc.Delta)
	}
}

// extractProposalHash pulls the real "ubx-proposal: <hash>" line a plan-
// writing command's own receipt always renders, the same real hash "ubx
// accept"/"ubx ship" need next.
func extractProposalHash(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ubx-proposal:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "ubx-proposal:"))
		}
	}
	t.Fatalf("no \"ubx-proposal:\" line found in output:\n%s", out)
	return ""
}

// readPlanFileForTest re-reads a saved plan file directly off disk, the
// same real .ubx/plans/<hash>.json path writePlanFile itself writes to --
// used here to inspect the exact real resolved delta a command's own
// human-rendered receipt only ever summarizes.
func readPlanFileForTest(t *testing.T, ledgerDir, hash string) ([]byte, error) {
	t.Helper()
	return os.ReadFile(filepath.Join(ledgerDir, ".ubx", "plans", hash+".json"))
}
