package cli

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runAWSOutputForStatus runs the real aws CLI and returns its trimmed
// stdout -- runAWSForAttribution (attribution_live_test.go) discards
// output, but creating a throwaway queue needs its URL back.
func runAWSOutputForStatus(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("aws", args...).Output()
	if err != nil {
		t.Fatalf("aws %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// TestStatus_LiveEndToEnd is UBI-17's real-world verification, run as an
// actual repeatable test (same discipline as every other real-AWS-touching
// test in this codebase): adopts the real ubx-states bucket plus a
// throwaway SQS queue (created and deleted for this test, same pattern
// conformance/aws_live_test.go's TestConformance_AWSSQSQueue uses) into one
// ledger, so the fleet walk is genuinely multi-resource, confirms
// ledger-only and --drift (all clean) modes, then mutates one of the two
// resources out of band and confirms the fleet report distinguishes the
// mutated one from the untouched one. Gated behind UBX_CONFORMANCE_LIVE=1.
// Leaves the account as found: bucket confirmed untagged before and after
// (GetBucketTagging -> NoSuchTagSet), queue deleted.
func TestStatus_LiveEndToEnd(t *testing.T) {
	requireAttributionLive(t)
	providerPath := realAWSProviderPathForAttribution(t)

	const bucket = "ubx-states"
	const bucketLookup = `{"id":"ubx-states","bucket":"ubx-states"}`
	const providerConfig = `{"region":"us-east-1"}`
	ledgerDir := t.TempDir()

	t.Cleanup(func() { runAWSForAttribution(t, "s3api", "delete-bucket-tagging", "--bucket", bucket) })

	queueName := "ubx-status-live-" + time.Now().UTC().Format("20060102150405")
	queueURL := runAWSOutputForStatus(t, "sqs", "create-queue", "--queue-name", queueName, "--query", "QueueUrl", "--output", "text")
	t.Cleanup(func() { runAWSForAttribution(t, "sqs", "delete-queue", "--queue-url", queueURL) })
	queueLookup := `{"id":"` + queueURL + `"}`

	// Adopt both into the same ledger -- a genuinely multi-resource fleet,
	// two different types (aws_s3_bucket, aws_sqs_queue).
	bucketAdopt := filepath.Join(ledgerDir, "bucket-adopt.json")
	if _, err := runUbx(t, nil, "scan",
		"--provider", providerPath, "--stack", "conformance", "--type", "aws_s3_bucket", "--name", bucket,
		"--lookup", bucketLookup, "--provider-config", providerConfig, "--ledger-dir", ledgerDir,
		"--out", bucketAdopt, "--no-attribution",
	); err != nil {
		t.Fatalf("ubx scan (adopt bucket): %v", err)
	}
	if _, err := runUbx(t, nil, "accept", bucketAdopt, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (bucket): %v", err)
	}

	queueAdopt := filepath.Join(ledgerDir, "queue-adopt.json")
	if _, err := runUbx(t, nil, "scan",
		"--provider", providerPath, "--stack", "conformance", "--type", "aws_sqs_queue", "--name", queueName,
		"--lookup", queueLookup, "--provider-config", providerConfig, "--ledger-dir", ledgerDir,
		"--out", queueAdopt, "--no-attribution",
	); err != nil {
		t.Fatalf("ubx scan (adopt queue): %v", err)
	}
	if _, err := runUbx(t, nil, "accept", queueAdopt, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (queue): %v", err)
	}

	// Ledger-only: both resources listed, no provider needed.
	ledgerOnlyOut, err := runUbx(t, nil, "status", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx status: %v\noutput: %s", err, ledgerOnlyOut)
	}
	if !strings.Contains(ledgerOnlyOut, "conformance.aws_s3_bucket.ubx-states") || !strings.Contains(ledgerOnlyOut, "conformance.aws_sqs_queue."+queueName) {
		t.Fatalf("expected both resources listed, got: %s", ledgerOnlyOut)
	}
	if !strings.Contains(ledgerOnlyOut, "2 resource(s) (ledger-only") {
		t.Fatalf("expected a 2-resource ledger-only summary, got: %s", ledgerOnlyOut)
	}

	// --drift: both clean, nothing has changed since adoption. --all
	// (UBI-71: a clean resource's own line is hidden by default) so this
	// still checks the per-resource clean classification directly.
	cleanOut, err := runUbx(t, nil, "status",
		"--ledger-dir", ledgerDir, "--drift", "--all",
		"--provider", providerPath, "--provider-config", providerConfig,
	)
	if err != nil {
		t.Fatalf("ubx status --drift (expected all clean): %v\noutput: %s", err, cleanOut)
	}
	if !strings.Contains(cleanOut, "clean: conformance.aws_s3_bucket.ubx-states") {
		t.Fatalf("expected the bucket reported clean, got: %s", cleanOut)
	}
	if !strings.Contains(cleanOut, "clean: conformance.aws_sqs_queue."+queueName) {
		t.Fatalf("expected the queue reported clean, got: %s", cleanOut)
	}
	if !strings.Contains(cleanOut, "2 resource(s), 0 drifted, 0 unreadable") {
		t.Fatalf("expected an all-clean summary, got: %s", cleanOut)
	}

	// Mutate only the bucket, out of band -- the queue is untouched. (The
	// t.Cleanup registered before adoption already removes this tag at
	// the end regardless of when it was applied.)
	runAWSForAttribution(t, "s3api", "put-bucket-tagging", "--bucket", bucket,
		"--tagging", "TagSet=[{Key=Environment,Value=staging}]")

	mixedOut, err := runUbx(t, nil, "status",
		"--ledger-dir", ledgerDir, "--drift", "--all",
		"--provider", providerPath, "--provider-config", providerConfig,
	)
	if err == nil {
		t.Fatal("expected a non-nil error (exit code 1) -- the bucket drifted")
	}
	var exitErr *ExitCodeError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected an *ExitCodeError, got: %T (%v)", err, err)
	}
	if exitErr.Code != 1 {
		t.Fatalf("Code = %d, want 1", exitErr.Code)
	}
	if !strings.Contains(mixedOut, "drifted: conformance.aws_s3_bucket.ubx-states") {
		t.Fatalf("expected the bucket reported drifted, got: %s", mixedOut)
	}
	if !strings.Contains(mixedOut, "clean: conformance.aws_sqs_queue."+queueName) {
		t.Fatalf("expected the untouched queue still reported clean, got: %s", mixedOut)
	}
	if !strings.Contains(mixedOut, "2 resource(s), 1 drifted, 0 unreadable") {
		t.Fatalf("expected a correct mixed summary, got: %s", mixedOut)
	}
}
