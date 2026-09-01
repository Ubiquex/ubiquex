package cli

import (
	"testing"
)

// TestShip_FirstRealModify_TagsRoundTrip is UBI-239's own end-to-end
// regression test, against a real fakeprovider subprocess: creating a
// fake_widget with a non-empty "tags" map, then shipping an entirely
// ordinary FIRST modify, used to fail with a spurious stale-observation
// error every time -- confirmed live, not assumed, via two isolated
// manual repros before this fix, then via direct inspection of the
// recorded lookup itself, which never carried "tags" at all (it's
// Optional, not Required, so core.DeriveLookupFromResult never includes
// it), so fakeprovider's own pure-echo ReadResource (ensureFakeProviderStateDir
// was not yet wired into runUbx) had no way to report anything but an
// empty tags map back, mismatching the correctly-recorded create-time
// hash.
//
// The real fix (cli/scan_test.go's ensureFakeProviderStateDir) makes
// fakeprovider's own pre-existing, previously opt-in FAKEPROVIDER_STATE_DIR
// persistence the default for every ok-v5/ok-v6 test -- fakeprovider now
// independently reports the resource's own real current tags, the same
// way a real provider's ReadResource always does regardless of what a
// caller's own lookup key happens to include, rather than defaulting an
// attribute away just because it wasn't part of the lookup.
func TestShip_FirstRealModify_TagsRoundTrip(t *testing.T) {
	requireHermeticSandbox(t)
	dir := t.TempDir()
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	resolveAcceptShip(t, dir, ledgerDir, env, `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "create widget1 with tags"},
  "resources": [
    {"type": "fake_widget", "name": "widget1", "op": "create", "config": {"name": "widget1", "tags": {"env": "staging"}}}
  ],
  "destroys": []
}`, "create")

	// Tags unchanged, only "name" renamed -- an entirely ordinary first
	// modify. Before this fix, this failed unconditionally the moment
	// tags was non-empty at create time, regardless of whether the
	// modify itself touched tags at all.
	resolveAcceptShip(t, dir, ledgerDir, env, `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {"summary": "rename widget1, tags unchanged"},
  "resources": [
    {"type": "fake_widget", "name": "widget1", "op": "modify", "config": {"name": "widget1-renamed", "tags": {"env": "staging"}}}
  ],
  "destroys": []
}`, "modify1")
}
