package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestWhy_ResourceAddress_ChainOrdering exercises UBI-11's first gap: `ubx
// why <stack>.<type>.<name>` resolves the resource's full proposal chain
// (adoption + subsequent drift), newest first, while `ubx why <id>` keeps
// working unchanged (covered separately by TestAcceptThenWhy and the
// proposal-id assertions below).
func TestWhy_ResourceAddress_ChainOrdering(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	addr := "payments.fake_widget.widget-why"

	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-why",
		"--lookup", `{"name":"widget-why","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "adopt.json"),
	); exitCode(err) != 1 {
		t.Fatalf("ubx scan (adopt): %v", err)
	}
	acceptOut, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "adopt.json"), "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (adopt): %v", err)
	}
	adoptID := mustExtractID(t, acceptOut)

	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-why",
		"--lookup", `{"name":"widget-why","tags":{"env":"staging"}}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "drift.json"),
		"--no-attribution", // hermetic -- see cli/scan_test.go's TestScanAcceptWhy for why
	); exitCode(err) != 1 {
		t.Fatalf("ubx scan (drift): %v", err)
	}
	acceptOut2, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "drift.json"), "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (drift): %v", err)
	}
	driftID := mustExtractID(t, acceptOut2)

	whyOut, err := runUbx(t, env, "why", addr, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why %s: %v\noutput: %s", addr, err, whyOut)
	}

	if !strings.Contains(whyOut, "2 proposal(s)") {
		t.Fatalf("expected a 2-proposal summary, got: %s", whyOut)
	}
	driftIdx := strings.Index(whyOut, displayHash(driftID, false))
	adoptIdx := strings.Index(whyOut, displayHash(adoptID, false))
	if driftIdx == -1 || adoptIdx == -1 {
		t.Fatalf("expected both proposal ids to appear (short form), got: %s", whyOut)
	}
	if driftIdx > adoptIdx {
		t.Fatalf("expected the drift proposal to render before the adoption (newest first), got: %s", whyOut)
	}
	if !strings.Contains(whyOut, "adoption") || !strings.Contains(whyOut, "drift_adopt") {
		t.Fatalf("expected both kinds to appear, got: %s", whyOut)
	}
}

// TestWhy_ResourceAddress_Unknown confirms an address the ledger has never
// recorded is reported as an error, not an empty/silent success.
func TestWhy_ResourceAddress_Unknown(t *testing.T) {
	ledgerDir := t.TempDir()
	_, err := runUbx(t, nil, "why", "payments.aws_s3_bucket.never-scanned", "--ledger-dir", ledgerDir)
	if err == nil {
		t.Fatal("expected an error for an address the ledger has never recorded")
	}
	if !strings.Contains(err.Error(), "no proposals found") {
		t.Fatalf("expected a 'no proposals found' error, got: %v", err)
	}
}

// TestWhy_InvalidArgument confirms an argument that's neither a valid
// proposal id nor a parseable address reports a clear error.
func TestWhy_InvalidArgument(t *testing.T) {
	ledgerDir := t.TempDir()
	_, err := runUbx(t, nil, "why", "not-an-id-or-an-address", "--ledger-dir", ledgerDir)
	if err == nil {
		t.Fatal("expected an error for an argument that's neither an id nor an address")
	}
	if !strings.Contains(err.Error(), "not a valid proposal ID") {
		t.Fatalf("expected the invalid-argument error, got: %v", err)
	}
}

// handWrittenDriftProposal is a minimal, valid drift_adopt proposal (per
// core.Validate: zero blast_radius, no destroys, modifies with a matching
// resolution.inputs entry) carrying one attributed and one unattributed
// intent source, for testing UBI-11's second gap: attribution rendering.
const handWrittenDriftProposal = `{
	"schema_version": 1,
	"stack": "conformance",
	"parent": "",
	"kind": "drift_adopt",
	"intent": {
		"summary": "record drift on conformance.aws_s3_bucket.ubx-states observed outside the ledger",
		"sources": [
			{
				"kind": "cloudtrail",
				"ref": "9910b32a-2f22-44b9-8d18-88cd3b95841a",
				"content_hash": "sha256:deadbeef",
				"event_id": "9910b32a-2f22-44b9-8d18-88cd3b95841a",
				"event_name": "PutBucketTagging",
				"event_time": "2026-07-10T21:42:30Z",
				"actor_arn": "arn:aws:iam::839333509514:user/roozbeh",
				"source_ip": "93.228.76.41"
			},
			{
				"kind": "cloudtrail_unattributed",
				"reason": "delivery_window"
			}
		]
	},
	"delta": {
		"modifies": [
			{
				"target": {"stack": "conformance", "type": "aws_s3_bucket", "name": "ubx-states"},
				"before": {"tags.env": "prod"},
				"after": {"tags.env": "staging"}
			}
		]
	},
	"resolution": {
		"resolved_at": "2026-07-10T21:42:35Z",
		"inputs": [
			{"kind": "live_state", "resource": "conformance.aws_s3_bucket.ubx-states", "observed_hash": "sha256:abc123"}
		]
	},
	"cost_delta": {"monthly_usd": 0},
	"blast_radius": {"creates": 0, "modifies": 0, "destroys": 0},
	"status": "draft"
}`

func TestWhy_RendersAttributedCloudTrailSource(t *testing.T) {
	ledgerDir := t.TempDir()
	proposalPath := filepath.Join(t.TempDir(), "drift.json")
	writeFile(t, proposalPath, handWrittenDriftProposal)

	acceptOut, err := runUbx(t, nil, "accept", proposalPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", err, acceptOut)
	}
	id := mustExtractID(t, acceptOut)

	whyOut, err := runUbx(t, nil, "why", id, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why: %v", err)
	}

	for _, want := range []string{
		"arn:aws:iam::839333509514:user/roozbeh",
		"PutBucketTagging",
		"2026-07-10T21:42:30Z",
		"93.228.76.41",
	} {
		if !strings.Contains(whyOut, want) {
			t.Errorf("expected attributed rendering to contain %q, got: %s", want, whyOut)
		}
	}
	// Event id and content hash still present, just demoted to a detail line.
	if !strings.Contains(whyOut, "9910b32a-2f22-44b9-8d18-88cd3b95841a") || !strings.Contains(whyOut, "sha256:deadbeef") {
		t.Errorf("expected the event id/content_hash detail line to survive, got: %s", whyOut)
	}
}

func TestWhy_RendersUnattributedReasonInWords(t *testing.T) {
	ledgerDir := t.TempDir()
	proposalPath := filepath.Join(t.TempDir(), "drift.json")
	writeFile(t, proposalPath, handWrittenDriftProposal)

	acceptOut, err := runUbx(t, nil, "accept", proposalPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", err, acceptOut)
	}
	id := mustExtractID(t, acceptOut)

	whyOut, err := runUbx(t, nil, "why", id, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why: %v", err)
	}

	if strings.Contains(whyOut, "reason=delivery_window") || strings.Contains(whyOut, `"delivery_window"`) {
		t.Errorf("expected the reason to render in words, not the bare enum value, got: %s", whyOut)
	}
	if !strings.Contains(whyOut, "too recent for CloudTrail to have delivered") {
		t.Errorf("expected the delivery_window reason rendered in words, got: %s", whyOut)
	}
}
