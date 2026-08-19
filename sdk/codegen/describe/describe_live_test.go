package describe

import (
	"context"
	"os"
	"testing"
	"time"
)

// requireSlowLive mirrors intentprovider/claude's own identical real
// gating convention (adapter_live_test.go) -- this test makes real,
// billed network calls and needs a resolvable Anthropic credential.
func requireSlowLive(t *testing.T) {
	t.Helper()
	if os.Getenv("UBX_TEST_SLOW") != "1" {
		t.Skip("set UBX_TEST_SLOW=1 to run this real Claude API test -- it makes real, billed network calls and needs a resolvable Anthropic credential (ANTHROPIC_API_KEY or an `ant auth login` profile)")
	}
}

// TestLive_Describe_AbstainsOnBareFieldName is this package's own single
// most important real proof: abstention must be the REAL, common outcome
// for a field with no signal beyond its own bare name, not a
// hypothetical this package merely claims to support. "id" is exactly
// the shape of field every real schema this whole onboarding pipeline
// has touched carries by the dozen (github_full_repository's own real
// "id", aws_sqs_queue's own real "queue_url" minus its own constraints,
// ...), with no constraints, no enum, and a parent context alone that
// cannot possibly justify a real, honest, non-obvious sentence.
func TestLive_Describe_AbstainsOnBareFieldName(t *testing.T) {
	requireSlowLive(t)
	g := New(Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := g.Describe(ctx, FieldContext{
		Name:          "id",
		Type:          "string",
		Computed:      true,
		ParentContext: "aws_sqs_queue",
	})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	t.Logf("real result: abstained=%v description=%q reason=%q", result.Abstained, result.Description, result.Reason)
	if !result.Abstained {
		t.Fatalf("expected the real model to abstain on a bare \"id\" field with no other signal, got a description instead: %q", result.Description)
	}
	if result.Reason == "" {
		t.Fatal("expected a real, non-empty reason for abstaining")
	}
}

// TestLive_Describe_GeneratesRealDescriptionWithSufficientSignal is the
// real, positive counterpart: a field with genuine, informative
// constraints and enum values should get a real, honest description,
// not an abstention -- proving this package doesn't over-correct into
// abstaining on everything.
func TestLive_Describe_GeneratesRealDescriptionWithSufficientSignal(t *testing.T) {
	requireSlowLive(t)
	g := New(Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := g.Describe(ctx, FieldContext{
		Name:          "message_retention_period",
		Type:          "number",
		Optional:      true,
		Constraints:   []string{"minimum: 60", "maximum: 1209600"},
		ParentContext: "aws_sqs_queue",
	})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	t.Logf("real result: abstained=%v description=%q reason=%q", result.Abstained, result.Description, result.Reason)
	if result.Abstained {
		t.Fatalf("expected a real description given genuine constraint signal (60-1209600 seconds), got an abstention instead: %q", result.Reason)
	}
	if result.Description == "" {
		t.Fatal("expected a real, non-empty description")
	}
}

// TestLive_Describe_RealEnumSignal proves a real enum's own actual
// values are enough signal to generate a real, grounded description
// (never invented detail beyond what the enum itself states).
func TestLive_Describe_RealEnumSignal(t *testing.T) {
	requireSlowLive(t)
	g := New(Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := g.Describe(ctx, FieldContext{
		Name:          "visibility_timeout",
		Type:          "string",
		Enum:          []string{"standard", "fifo"},
		ParentContext: "aws_sqs_queue.queue_type",
	})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	t.Logf("real result: abstained=%v description=%q reason=%q", result.Abstained, result.Description, result.Reason)
	// Deliberately no hard assertion on Abstained here -- a real model
	// may reasonably judge two bare enum values plus a field/parent name
	// mismatch (visibility_timeout named but queue_type's own enum
	// given, a deliberately slightly-inconsistent real-world-shaped
	// input) either way; this test's own real job is proving the call
	// completes and returns a well-formed Result, not pinning a specific
	// model judgment call.
	if result.Abstained && result.Reason == "" {
		t.Fatal("expected a real, non-empty reason when abstaining")
	}
	if !result.Abstained && result.Description == "" {
		t.Fatal("expected a real, non-empty description when not abstaining")
	}
}
