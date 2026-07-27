package claude_test

import (
	"context"
	"os"
	"testing"

	"github.com/ubiquex/ubiquex-cli/intentprovider"
	"github.com/ubiquex/ubiquex-cli/intentprovider/claude"
	"github.com/ubiquex/ubiquex-cli/intentprovider/conformance"
)

// requireSlowLive skips t unless UBX_TEST_SLOW=1 is set -- this test
// makes real, billed network calls to the real Claude API (structured
// output plus, on the conformance run, up to MaxAttempts retries) and
// takes real wall-clock time, the same "genuinely slow/costly, opt-in
// only" posture cli/ship_lying_destroy_test.go's own UBX_TEST_SLOW=1
// gate already established, generalized here to network cost rather than
// a real timing budget. Unlike a UBX_CONFORMANCE_LIVE=1 test, this one
// also depends on a real credential being resolvable by the Anthropic
// SDK's own default chain (ANTHROPIC_API_KEY and beyond,
// docs/intent-provider.md's own "The Claude adapter" section did not
// wire [intent] config's key_ref this session -- that's the md-pipeline
// session's own job) -- a missing key surfaces as a real,
// classifyError-named authentication failure from Draft itself, not a
// second skip condition here: set UBX_TEST_SLOW=1 without a resolvable
// credential and this test fails loudly, naming exactly why, rather than
// silently skipping a second time.
func requireSlowLive(t *testing.T) {
	t.Helper()
	if os.Getenv("UBX_TEST_SLOW") != "1" {
		t.Skip("set UBX_TEST_SLOW=1 to run this real Claude API test -- it makes real, billed network calls and needs a resolvable Anthropic credential (ANTHROPIC_API_KEY or an `ant auth login` profile)")
	}
}

// TestAdapter_Draft_RealAPI is the minimal live smoke test: one document,
// one attempt, confirming the real API round trip -- structured output,
// prompt caching's own system-prompt breakpoint, and DraftWithRetry's own
// validation -- actually works end to end against the real service, not
// just against this session's own fake Adapter.
func TestAdapter_Draft_RealAPI(t *testing.T) {
	requireSlowLive(t)

	a := claude.New(claude.Config{})
	doc := []byte("# Payments database\n\nProvision a small Postgres database for the payments service, in the payments stack.\n")

	draft, raw, err := intentprovider.DraftWithRetry(context.Background(), a, "payments", doc)
	if err != nil {
		t.Fatalf("DraftWithRetry against the real Claude API: %v", err)
	}
	if len(raw) == 0 {
		t.Error("expected non-empty raw output")
	}
	if draft.Stack != "payments" {
		t.Errorf("Stack = %q, want %q", draft.Stack, "payments")
	}
	if len(draft.Resources) == 0 {
		t.Error("expected at least one resource in the real draft, got none")
	}
	if draft.Intent.Summary == "" {
		t.Error("expected a non-empty intent.summary in the real draft")
	}
}

// TestAdapter_Conformance_RealAPI runs the full fixture suite (fixture
// #1, the payments doc) against the real Claude API -- this session's
// own "Claude adapter + conformance harness" scope, combined: the same
// intentprovider/conformance.Run call TestRun_FakeAdapterPasses already
// proved hermetically, now exercising the real adapter it was designed
// to validate. Per-adapter published results (docs/intent-provider.md's
// own "Conformance suite" section) are a later session's own doc --
// this test is the mechanism that would produce them, run manually
// against a real credential, not run in this session (no credentials in
// this environment -- see STATE.md for the honest account).
func TestAdapter_Conformance_RealAPI(t *testing.T) {
	requireSlowLive(t)

	conformance.Run(t, claude.New(claude.Config{}))
}
