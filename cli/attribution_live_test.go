package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex-cli/provider"
)

// attributionAWSProviderVersion is pinned, not "latest" (see
// docs/architecture.md — provider acquisition, UBI-8: explicit version
// pins only), matching the same version conformance/aws_live_test.go pins.
const attributionAWSProviderVersion = "6.54.0"

// requireAttributionLive gates this file's test behind the same env var
// every other real-AWS-touching test in this codebase uses
// (conformance.RequireLive) — duplicated here rather than importing the
// test-only conformance package from cli's own tests, same "small enough
// not to share" call as cli/stateadapter.go vs conformance's own
// stateReaderAdapter.
func requireAttributionLive(t *testing.T) {
	t.Helper()
	if os.Getenv("UBX_CONFORMANCE_LIVE") != "1" {
		t.Skip("skipping: set UBX_CONFORMANCE_LIVE=1 to run CloudTrail attribution against the real AWS account")
	}
}

func realAWSProviderPathForAttribution(t *testing.T) string {
	t.Helper()
	src, err := provider.ParseSource("hashicorp/aws")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	res, err := provider.Acquire(ctx, src, attributionAWSProviderVersion)
	if err != nil {
		t.Fatalf("acquire real aws provider: %v", err)
	}
	return res.Path
}

func runAWSForAttribution(t *testing.T, args ...string) {
	t.Helper()
	out, err := exec.Command("aws", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("aws %v: %v\n%s", args, err, out)
	}
}

// TestScan_AttributesRealDrift_LiveCloudTrail is UBI-10's real-world
// verification, run as an actual repeatable test rather than a one-off
// manual check (same discipline as every prior "real-world verification"
// in this codebase): tag the real `ubx-states` bucket — a genuine
// out-of-band mutation ubx has no part in — scan it through the actual
// `ubx scan` command (without --no-attribution), and confirm the generated
// drift_adopt proposal's intent.sources carries a "cloudtrail" entry whose
// actor_arn is the real caller identity that made the change, not a
// fake/simulated one. Gated behind UBX_CONFORMANCE_LIVE=1, same as every
// other test in this codebase that touches the real AWS account.
func TestScan_AttributesRealDrift_LiveCloudTrail(t *testing.T) {
	requireAttributionLive(t)
	providerPath := realAWSProviderPathForAttribution(t)

	identity, err := exec.Command("aws", "sts", "get-caller-identity", "--query", "Arn", "--output", "text").Output()
	if err != nil {
		t.Fatalf("aws sts get-caller-identity: %v", err)
	}
	callerARN := strings.TrimSpace(string(identity))

	const bucket = "ubx-states"
	ledgerDir := t.TempDir()

	// Adopt first (scan reports "new" the first time, same as every other
	// real-account test in this codebase) so the second scan has a prior
	// ledger observation to diff against and bound the correlation window.
	if _, err := runUbx(t, nil, "scan",
		"--provider", providerPath,
		"--stack", "conformance",
		"--type", "aws_s3_bucket",
		"--name", bucket,
		"--lookup", `{"id":"ubx-states","bucket":"ubx-states"}`,
		"--provider-config", `{"region":"us-east-1"}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "adopt.json"),
	); err != nil {
		t.Fatalf("ubx scan (adopt): %v", err)
	}
	if _, err := runUbx(t, nil, "accept", filepath.Join(ledgerDir, "adopt.json"), "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (adopt): %v", err)
	}

	runAWSForAttribution(t, "s3api", "put-bucket-tagging", "--bucket", bucket,
		"--tagging", "TagSet=[{Key=ubx-attribution-live,Value=1}]")
	t.Cleanup(func() { runAWSForAttribution(t, "s3api", "delete-bucket-tagging", "--bucket", bucket) })

	driftPath := filepath.Join(ledgerDir, "drift.json")
	scanDrift := func() string {
		out, err := runUbx(t, nil, "scan",
			"--provider", providerPath,
			"--stack", "conformance",
			"--type", "aws_s3_bucket",
			"--name", bucket,
			"--lookup", `{"id":"ubx-states","bucket":"ubx-states"}`,
			"--provider-config", `{"region":"us-east-1"}`,
			"--ledger-dir", ledgerDir,
			"--out", driftPath,
		)
		if err != nil {
			t.Fatalf("ubx scan (drift): %v\noutput: %s", err, out)
		}
		if !strings.Contains(out, "drifted:") {
			t.Fatalf("expected a 'drifted' classification, got: %s", out)
		}
		b, err := os.ReadFile(driftPath)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	// CloudTrail's LookupEvents has real, non-trivial delivery latency --
	// confirmed empirically against this exact account/bucket while
	// building this test: a PutBucketTagging call took a little over two
	// minutes to become queryable via LookupEvents, well short of the
	// documented ~15-minute upper bound but far longer than the
	// near-instant response an earlier manual probe happened to see (not
	// something to rely on). Poll for up to 5 minutes rather than assume
	// either extreme. Scanning again doesn't mutate the ledger (only
	// accept does), so re-running this is harmless.
	var proposal string
	for attempt := 0; attempt < 15; attempt++ {
		proposal = scanDrift()
		if strings.Contains(proposal, `"kind": "cloudtrail"`) {
			break
		}
		time.Sleep(20 * time.Second)
	}

	if !strings.Contains(proposal, `"kind": "cloudtrail"`) {
		t.Fatalf("expected a cloudtrail-attributed source after retrying, got: %s", proposal)
	}
	if !strings.Contains(proposal, callerARN) {
		t.Fatalf("expected the real caller ARN %q in the drift proposal's attribution, got: %s", callerARN, proposal)
	}
}
