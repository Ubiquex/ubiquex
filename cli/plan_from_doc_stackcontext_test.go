package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// UBI-81 v1's own CLI-level proof, complementing intentprovider/claude's
// own real, live tool-use tests (stackcontext_live_test.go -- the real
// Claude API genuinely calling read_stack_config and resolving the
// motivating "if this is production..." case): this test proves the
// OTHER half, entirely hermetic -- that the CLI layer actually threads a
// real stackConfigContext into DraftRequest.StackConfig (fake.lastReq),
// and that a context-derived resolution, once the adapter records it as
// a named default, genuinely renders in the real `ubx plan` receipt
// (renderAmbiguityStyled) -- never assumed just because intent.Defaults
// happens to be non-empty.

func TestPlanFromDoc_UBI81_StackConfigThreadedAndReceiptShowsNamedDefault(t *testing.T) {
	fake := &fakeIntentAdapter{draft: `{
	  "schema_version": 1,
	  "kind": "ubx:intent/v1",
	  "stack": "payments-prod",
	  "intent": {
	    "summary": "Payments logs bucket, encryption/retention resolved from stack context",
	    "assumptions": [],
	    "defaults": [
	      {"text": "inferred production from stack name \"payments-prod\" (read via read_stack_config); enabled KMS encryption and 14-day retention", "affects": ["payments-prod.fake_widget.payments-logs"]}
	    ],
	    "questions": []
	  },
	  "resources": [
	    {"type": "fake_widget", "name": "payments-logs", "op": "create", "config": "{\"name\":\"payments-logs\",\"tags\":{\"encryption\":\"kms\",\"retention_days\":\"14\"}}"}
	  ],
	  "destroys": []
	}`}
	withBuildIntentAdapter(t, fake)

	docPath := filepath.Join(t.TempDir(), "payments-logs.md")
	writeFile(t, docPath, "Create an S3 bucket called payments-logs. If this is a production "+
		"environment, enable KMS encryption and 14-day log retention. Otherwise, use default "+
		"encryption and 1-day log retention.")

	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	out, err := runUbx(t, env, "plan", "--from-doc", docPath, "--stack", "payments-prod", "--ledger-dir", ledgerDir, "--provider", fakeProviderBinary)
	if err != nil {
		t.Fatalf("ubx plan --from-doc: %v\noutput: %s", err, out)
	}

	if len(fake.lastReq.StackConfig) == 0 {
		t.Fatal("expected DraftRequest.StackConfig to be populated -- the CLI never threaded stack context in")
	}
	if !strings.Contains(string(fake.lastReq.StackConfig), "payments-prod") {
		t.Errorf("expected StackConfig to carry the real stack name, got: %s", fake.lastReq.StackConfig)
	}

	if !strings.Contains(out, "AI default") {
		t.Fatalf("expected the receipt to render the AI defaults block, got:\n%s", out)
	}
	if !strings.Contains(out, "payments-prod") || !strings.Contains(out, "KMS") {
		t.Fatalf("expected the receipt to render the real, named default text (naming payments-prod and KMS), got:\n%s", out)
	}
}

// TestPlanFromDoc_UBI81_BlueprintDrafting_NeverGetsStackConfig confirms
// the ticket's own explicit exclusion holds in real code, not just by
// review: blueprint-authored drafting (cli/blueprint.go) never threads
// any StackConfig at all -- its own DraftWithRetry call site passes a
// literal nil, unaffected by this ticket.
func TestPlanFromDoc_UBI81_BlueprintDrafting_NeverGetsStackConfig(t *testing.T) {
	// A named subdirectory, not t.TempDir()'s own raw basename directly
	// (its numeric "001"-style naming isn't a valid Go package
	// identifier -- draftBlueprint's own stack argument is
	// filepath.Base(dir), cli/blueprint.go).
	dir := filepath.Join(t.TempDir(), "ciplatform")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	blueprintName := filepath.Base(dir)
	fake := &fakeIntentAdapter{draft: fmt.Sprintf(`{
	  "schema_version": 1,
	  "kind": "ubx:intent/v1",
	  "stack": %q,
	  "intent": {"summary": "blueprint resources", "assumptions": [], "defaults": [], "questions": []},
	  "resources": [
	    {"type": "fake_widget", "name": "queue", "op": "create", "config": "{\"name\":\"queue\"}"}
	  ],
	  "destroys": []
	}`, blueprintName)}
	withBuildIntentAdapter(t, fake)

	writeFile(t, filepath.Join(dir, "resources.md"), "An SQS queue.")
	writeFile(t, filepath.Join(dir, "Ubxfile"), "lang: go\n\nresources: resources.md\n")

	out, err := runUbx(t, nil, "blueprint", "build", dir)
	if err != nil {
		t.Fatalf("ubx blueprint build: %v\noutput: %s", err, out)
	}

	if fake.lastReq.StackConfig != nil {
		t.Errorf("expected blueprint-authored drafting to never receive StackConfig, got: %s", fake.lastReq.StackConfig)
	}
}
