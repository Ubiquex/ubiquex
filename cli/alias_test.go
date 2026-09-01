package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestAlias_SetResolveList_RealFakeProvider is UBI-228's own core proof:
// assign an alias to a real shipped head, resolve it back, see it in the
// listing -- against a real ledger, not a hand-built aliases.json.
func TestAlias_SetResolveList_RealFakeProvider(t *testing.T) {
	requireHermeticSandbox(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	head := resolveAcceptShip(t, dir, ledgerDir, env, `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "create a"},
  "resources": [
    {"type": "fake_widget", "name": "a", "op": "create", "config": {"name": "widget-a"}}
  ],
  "destroys": []
}`, "batch1")

	setOut, err := runUbx(t, nil, "alias", "set", "stable", head, "--ledger-dir", ledgerDir, "--stack", "payments")
	if err != nil {
		t.Fatalf("alias set: %v\noutput: %s", err, setOut)
	}
	if !strings.Contains(setOut, "stable") || !strings.Contains(setOut, head[:12]) {
		t.Fatalf("expected the set receipt to name the alias and the head, got:\n%s", setOut)
	}

	resolveOut, err := runUbx(t, nil, "alias", "resolve", "stable", "--ledger-dir", ledgerDir, "--stack", "payments")
	if err != nil {
		t.Fatalf("alias resolve: %v\noutput: %s", err, resolveOut)
	}
	if strings.TrimSpace(resolveOut) != head {
		t.Fatalf("expected alias resolve to print the real head %q, got %q", head, strings.TrimSpace(resolveOut))
	}

	listOut, err := runUbx(t, nil, "alias", "list", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("alias list: %v\noutput: %s", err, listOut)
	}
	if !strings.Contains(listOut, "payments.stable") {
		t.Fatalf("expected alias list to render payments.stable, got:\n%s", listOut)
	}

	var doc struct {
		Format  int `json:"format"`
		Entries []struct {
			Stack string `json:"stack"`
			Name  string `json:"name"`
			Head  string `json:"head"`
		} `json:"entries"`
	}
	jsonOut, err := runUbx(t, nil, "alias", "list", "--ledger-dir", ledgerDir, "--json")
	if err != nil {
		t.Fatalf("alias list --json: %v\noutput: %s", err, jsonOut)
	}
	if err := json.Unmarshal([]byte(jsonOut), &doc); err != nil {
		t.Fatalf("parse alias list --json: %v\nraw: %s", err, jsonOut)
	}
	if len(doc.Entries) != 1 || doc.Entries[0].Stack != "payments" || doc.Entries[0].Name != "stable" || doc.Entries[0].Head != head {
		t.Fatalf("unexpected alias list --json entries: %+v", doc.Entries)
	}
}

// TestAlias_SetRefusesRepointWithoutForce proves the ticket's own named
// constraint held: silently repointing something a human uses as a name
// is refused by default, and --force is what makes "moving an alias to
// a new head" the explicit act it needs to be.
func TestAlias_SetRefusesRepointWithoutForce(t *testing.T) {
	requireHermeticSandbox(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	head1 := resolveAcceptShip(t, dir, ledgerDir, env, `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "create a"},
  "resources": [
    {"type": "fake_widget", "name": "a", "op": "create", "config": {"name": "widget-a"}}
  ],
  "destroys": []
}`, "batch1")
	head2 := resolveAcceptShip(t, dir, ledgerDir, env, `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "create b"},
  "resources": [
    {"type": "fake_widget", "name": "b", "op": "create", "config": {"name": "widget-b"}}
  ],
  "destroys": []
}`, "batch2")

	if out, err := runUbx(t, nil, "alias", "set", "stable", head1, "--ledger-dir", ledgerDir, "--stack", "payments"); err != nil {
		t.Fatalf("alias set (initial): %v\noutput: %s", err, out)
	}

	repointOut, err := runUbx(t, nil, "alias", "set", "stable", head2, "--ledger-dir", ledgerDir, "--stack", "payments")
	if err == nil {
		t.Fatalf("expected alias set without --force to refuse repointing, got success:\n%s", repointOut)
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected the refusal to name --force as the way out, got: %v", err)
	}

	forcedOut, err := runUbx(t, nil, "alias", "set", "stable", head2, "--ledger-dir", ledgerDir, "--stack", "payments", "--force")
	if err != nil {
		t.Fatalf("alias set --force: %v\noutput: %s", err, forcedOut)
	}
	resolveOut, err := runUbx(t, nil, "alias", "resolve", "stable", "--ledger-dir", ledgerDir, "--stack", "payments")
	if err != nil {
		t.Fatalf("alias resolve: %v\noutput: %s", err, resolveOut)
	}
	if strings.TrimSpace(resolveOut) != head2 {
		t.Fatalf("expected --force to have repointed stable to head2 %q, got %q", head2, strings.TrimSpace(resolveOut))
	}
}

// TestAlias_SetRejectsIllegalNames proves the two collision guards named
// in cli/alias.go's own doc comment: an alias name can never be mistaken
// for a real hash (proposalIDPattern) or a resource address (a dot).
func TestAlias_SetRejectsIllegalNames(t *testing.T) {
	requireHermeticSandbox(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	head := resolveAcceptShip(t, dir, ledgerDir, env, `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "create a"},
  "resources": [
    {"type": "fake_widget", "name": "a", "op": "create", "config": {"name": "widget-a"}}
  ],
  "destroys": []
}`, "batch1")

	cases := []string{
		"payments.stable",       // contains a dot -- would collide with address parsing
		"1234",                  // starts with a digit, not a letter
		strings.Repeat("a", 64), // itself a well-formed hash shape
		"-leading-dash",
	}
	for _, name := range cases {
		out, err := runUbx(t, nil, "alias", "set", name, head, "--ledger-dir", ledgerDir, "--stack", "payments")
		if err == nil {
			t.Fatalf("expected alias set to reject illegal name %q, got success:\n%s", name, out)
		}
	}
}

// TestAlias_ResolvedByRestoreAndWhy is UBI-228's own stated reason for
// existing: "naming a head rather than pasting a hash is the whole
// interaction" for restore, and ubx why accepts the identical alias.
func TestAlias_ResolvedByRestoreAndWhy(t *testing.T) {
	requireHermeticSandbox(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	targetHead := resolveAcceptShip(t, dir, ledgerDir, env, `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "create a"},
  "resources": [
    {"type": "fake_widget", "name": "a", "op": "create", "config": {"name": "widget-a"}}
  ],
  "destroys": []
}`, "batch1")
	resolveAcceptShip(t, dir, ledgerDir, env, `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "create b"},
  "resources": [
    {"type": "fake_widget", "name": "b", "op": "create", "config": {"name": "widget-b"}}
  ],
  "destroys": []
}`, "batch2")

	if out, err := runUbx(t, nil, "alias", "set", "known-good", targetHead, "--ledger-dir", ledgerDir, "--stack", "payments"); err != nil {
		t.Fatalf("alias set: %v\noutput: %s", err, out)
	}

	// ubx restore, given the alias instead of the raw hash, must resolve
	// to the exact same target as the raw hash would.
	restoreOut, err := runUbx(t, env, "restore", "known-good", "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--stack", "payments", "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx restore known-good: %v\noutput: %s", err, restoreOut)
	}
	_, doc := parseRestorePlan(t, ledgerDir, restoreOut)
	foundSource := false
	for _, s := range doc.Intent.Sources {
		if s.Kind == "restore" && s.Ref == targetHead {
			foundSource = true
		}
	}
	if !foundSource {
		t.Fatalf("expected restore via alias to record intent.sources ref = the real target head %s, got: %+v", targetHead, doc.Intent.Sources)
	}

	// ubx why, given the alias instead of the raw hash, must explain the
	// exact same proposal the raw hash names.
	whyAliasOut, err := runUbx(t, nil, "why", "known-good", "--ledger-dir", ledgerDir, "--stack", "payments", "--full-hashes")
	if err != nil {
		t.Fatalf("ubx why known-good: %v\noutput: %s", err, whyAliasOut)
	}
	whyHashOut, err := runUbx(t, nil, "why", targetHead, "--ledger-dir", ledgerDir, "--stack", "payments", "--full-hashes")
	if err != nil {
		t.Fatalf("ubx why %s: %v\noutput: %s", targetHead, err, whyHashOut)
	}
	if whyAliasOut != whyHashOut {
		t.Fatalf("expected ubx why known-good to render identically to ubx why %s, got:\nalias:\n%s\nhash:\n%s", targetHead, whyAliasOut, whyHashOut)
	}
}

// TestAlias_Remove proves removal only ever touches the alias mapping,
// never the proposal it pointed at -- the ledger keeps the head
// regardless.
func TestAlias_Remove(t *testing.T) {
	requireHermeticSandbox(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	head := resolveAcceptShip(t, dir, ledgerDir, env, `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "create a"},
  "resources": [
    {"type": "fake_widget", "name": "a", "op": "create", "config": {"name": "widget-a"}}
  ],
  "destroys": []
}`, "batch1")

	if out, err := runUbx(t, nil, "alias", "set", "temp", head, "--ledger-dir", ledgerDir, "--stack", "payments"); err != nil {
		t.Fatalf("alias set: %v\noutput: %s", err, out)
	}
	if out, err := runUbx(t, nil, "alias", "remove", "temp", "--ledger-dir", ledgerDir, "--stack", "payments"); err != nil {
		t.Fatalf("alias remove: %v\noutput: %s", err, out)
	}
	if out, err := runUbx(t, nil, "alias", "resolve", "temp", "--ledger-dir", ledgerDir, "--stack", "payments"); err == nil {
		t.Fatalf("expected alias resolve to fail after removal, got success:\n%s", out)
	}
	// The proposal itself is still there, reachable by its real hash.
	whyOut, err := runUbx(t, nil, "why", head, "--ledger-dir", ledgerDir, "--stack", "payments")
	if err != nil {
		t.Fatalf("ubx why %s after alias removal: %v\noutput: %s", head, err, whyOut)
	}
}

// TestAlias_AmbiguousAcrossStacksRequiresExplicitStack proves the same
// name assigned in two different stacks needs --stack to disambiguate
// when looked up without one -- resolveHeadOrAlias's own documented
// behavior for an omitted stack.
func TestAlias_AmbiguousAcrossStacksRequiresExplicitStack(t *testing.T) {
	requireHermeticSandbox(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	headA := resolveAcceptShip(t, dir, ledgerDir, env, `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "create a"},
  "resources": [
    {"type": "fake_widget", "name": "a", "op": "create", "config": {"name": "widget-a"}}
  ],
  "destroys": []
}`, "batch-payments")
	headB := resolveAcceptShip(t, dir, ledgerDir, env, `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "billing",
  "intent": {"summary": "create a"},
  "resources": [
    {"type": "fake_widget", "name": "a", "op": "create", "config": {"name": "widget-a"}}
  ],
  "destroys": []
}`, "batch-billing")

	if out, err := runUbx(t, nil, "alias", "set", "shared-name", headA, "--ledger-dir", ledgerDir, "--stack", "payments"); err != nil {
		t.Fatalf("alias set payments: %v\noutput: %s", err, out)
	}
	if out, err := runUbx(t, nil, "alias", "set", "shared-name", headB, "--ledger-dir", ledgerDir, "--stack", "billing"); err != nil {
		t.Fatalf("alias set billing: %v\noutput: %s", err, out)
	}

	ambiguousOut, err := runUbx(t, nil, "alias", "resolve", "shared-name", "--ledger-dir", ledgerDir)
	if err == nil {
		t.Fatalf("expected an unscoped lookup of a same-named alias in two stacks to refuse, got success:\n%s", ambiguousOut)
	}
	if !strings.Contains(err.Error(), "--stack") {
		t.Fatalf("expected the refusal to name --stack as the way to disambiguate, got: %v", err)
	}

	scopedOut, err := runUbx(t, nil, "alias", "resolve", "shared-name", "--ledger-dir", ledgerDir, "--stack", "billing")
	if err != nil {
		t.Fatalf("alias resolve --stack billing: %v\noutput: %s", err, scopedOut)
	}
	if strings.TrimSpace(scopedOut) != headB {
		t.Fatalf("expected --stack billing to resolve to headB %q, got %q", headB, strings.TrimSpace(scopedOut))
	}
}
