package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRevertPath_LiveEndToEnd is UBI-16's real-world verification, run as
// an actual repeatable test (same discipline as every other
// real-AWS-touching test in this codebase, e.g.
// TestScan_AttributesRealDrift_LiveCloudTrail): adopt the real ubx-states
// bucket's tag, mutate it out of band, generate both resolutions with
// `--propose both`, accept the revert, confirm `revert-plan`'s output is
// correct, apply the correction manually via the `aws` CLI (standing in
// for "the team's own tooling" -- ubx itself never applies a revert), and
// confirm a final scan reports clean. Gated behind UBX_CONFORMANCE_LIVE=1,
// same as every other test in this codebase that touches the real account.
// Leaves the bucket exactly as found: untagged (confirmed before writing
// this test -- GetBucketTagging returned NoSuchTagSet).
func TestRevertPath_LiveEndToEnd(t *testing.T) {
	requireAttributionLive(t)
	providerPath := realAWSProviderPathForAttribution(t)

	const bucket = "ubx-states"
	const lookup = `{"id":"ubx-states","bucket":"ubx-states"}`
	const providerConfig = `{"region":"us-east-1"}`
	ledgerDir := t.TempDir()

	// Starting state confirmed untagged; restore that regardless of how
	// far this test gets.
	t.Cleanup(func() { runAWSForAttribution(t, "s3api", "delete-bucket-tagging", "--bucket", bucket) })

	// Set up an initial tag, then adopt it -- the ledger's recorded truth
	// this revert will eventually restore back to.
	runAWSForAttribution(t, "s3api", "put-bucket-tagging", "--bucket", bucket,
		"--tagging", "TagSet=[{Key=Environment,Value=prod}]")

	adoptPath := filepath.Join(ledgerDir, "adopt.json")
	if _, err := runUbx(t, nil, "scan",
		"--provider", providerPath,
		"--stack", "conformance",
		"--type", "aws_s3_bucket",
		"--name", bucket,
		"--lookup", lookup,
		"--provider-config", providerConfig,
		"--ledger-dir", ledgerDir,
		"--out", adoptPath,
		"--no-attribution",
	); err != nil {
		t.Fatalf("ubx scan (adopt): %v", err)
	}
	if _, err := runUbx(t, nil, "accept", adoptPath, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (adopt): %v", err)
	}

	// Mutate out of band: someone (not ubx) changes the tag.
	runAWSForAttribution(t, "s3api", "put-bucket-tagging", "--bucket", bucket,
		"--tagging", "TagSet=[{Key=Environment,Value=staging}]")

	// scan --propose both: generates drift_adopt AND drift_revert from the
	// same observation. --out doesn't support two proposals (see
	// cli/scan.go), so capture stdout and pull the drift_revert's JSON
	// block out of it -- it's always the second (and therefore last)
	// proposal printed, per scan.go's "both" branch ordering.
	bothOut, err := runUbx(t, nil, "scan",
		"--provider", providerPath,
		"--stack", "conformance",
		"--type", "aws_s3_bucket",
		"--name", bucket,
		"--lookup", lookup,
		"--provider-config", providerConfig,
		"--ledger-dir", ledgerDir,
		"--no-attribution",
		"--propose", "both",
	)
	if err != nil {
		t.Fatalf("ubx scan --propose both: %v\noutput: %s", err, bothOut)
	}
	if !strings.Contains(bothOut, `"drift_adopt"`) || !strings.Contains(bothOut, `"drift_revert"`) {
		t.Fatalf("expected both kinds generated, got: %s", bothOut)
	}
	const marker = `"drift_revert" proposal`
	idx := strings.Index(bothOut, marker)
	if idx == -1 {
		t.Fatalf("expected a drift_revert proposal header, got: %s", bothOut)
	}
	revertJSON := strings.TrimSpace(bothOut[idx+len(marker):])
	if !strings.Contains(revertJSON, `"before"`) || !strings.Contains(revertJSON, "staging") {
		t.Fatalf("expected the revert's before to carry the observed (staging) value, got: %s", revertJSON)
	}
	if !strings.Contains(revertJSON, "prod") {
		t.Fatalf("expected the revert's after to carry the ledger-recorded (prod) value, got: %s", revertJSON)
	}
	revertPath := filepath.Join(ledgerDir, "revert.json")
	if err := os.WriteFile(revertPath, []byte(revertJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	acceptOut, err := runUbx(t, nil, "accept", revertPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (revert): %v\noutput: %s", err, acceptOut)
	}
	revertID := mustExtractID(t, acceptOut)

	planOut, err := runUbx(t, nil, "revert-plan", revertID, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx revert-plan: %v\noutput: %s", err, planOut)
	}
	if !strings.Contains(planOut, "--- plan ---") {
		t.Fatalf("expected a plan section, got: %s", planOut)
	}
	if !strings.Contains(planOut, "tags.Environment") {
		t.Fatalf("expected the tags.Environment attribute named, got: %s", planOut)
	}
	if !strings.Contains(planOut, `"staging" -> "prod"`) {
		t.Fatalf("expected current (staging) -> restore-to (prod), got: %s", planOut)
	}
	if !strings.Contains(planOut, "conformance.aws_s3_bucket.ubx-states") {
		t.Fatalf("expected the resource address, got: %s", planOut)
	}

	// Apply the correction manually -- exactly what revert-plan told us to
	// do, via the team's own tooling (the aws CLI here), never ubx itself.
	runAWSForAttribution(t, "s3api", "put-bucket-tagging", "--bucket", bucket,
		"--tagging", "TagSet=[{Key=Environment,Value=prod}]")

	cleanOut, err := runUbx(t, nil, "scan",
		"--provider", providerPath,
		"--stack", "conformance",
		"--type", "aws_s3_bucket",
		"--name", bucket,
		"--lookup", lookup,
		"--provider-config", providerConfig,
		"--ledger-dir", ledgerDir,
		"--no-attribution",
	)
	if err != nil {
		t.Fatalf("ubx scan (after manual correction): %v\noutput: %s", err, cleanOut)
	}
	if !strings.Contains(cleanOut, "no drift") {
		t.Fatalf("expected a clean scan after the manual correction, got: %s", cleanOut)
	}
}
