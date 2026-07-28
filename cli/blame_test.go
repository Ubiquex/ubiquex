package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// row: attribute modified across multiple proposals -- latest wins,
// correctly, per attribute (built via real ubx scan/accept, the same
// adoption-then-drift shape TestWhy_ResourceAddress_ChainOrdering already
// established, mirrored here for blame).
func TestBlame_MultiTouch_LatestWinsPerAttribute(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	addr := "payments.fake_widget.widget-blame"

	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-blame",
		"--lookup", `{"name":"widget-blame","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "adopt.json"),
	); exitCode(err) != 1 {
		t.Fatalf("ubx scan (adopt): %v", err)
	}
	acceptOut, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "adopt.json"), "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (adopt): %v\noutput: %s", err, acceptOut)
	}
	adoptID := mustExtractID(t, acceptOut)

	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-blame",
		"--lookup", `{"name":"widget-blame","tags":{"env":"staging"}}`,
		"--ledger-dir", ledgerDir,
		"--out", filepath.Join(ledgerDir, "drift.json"),
		"--no-attribution",
	); exitCode(err) != 1 {
		t.Fatalf("ubx scan (drift): %v", err)
	}
	acceptOut2, err := runUbx(t, env, "accept", filepath.Join(ledgerDir, "drift.json"), "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (drift): %v\noutput: %s", err, acceptOut2)
	}
	driftID := mustExtractID(t, acceptOut2)

	out, err := runUbx(t, nil, "blame", addr, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx blame: %v\noutput: %s", err, out)
	}

	envLine := lineContaining(out, "tags.env:")
	if envLine == "" {
		t.Fatalf("expected a tags.env entry, got: %s", out)
	}
	envBlock := blockFor(out, "tags.env:")
	if !strings.Contains(envBlock, shortID(driftID)) {
		t.Fatalf("expected tags.env blamed on the drift proposal %s, got block: %s", shortID(driftID), envBlock)
	}
	if strings.Contains(envBlock, shortID(adoptID)) {
		t.Fatalf("tags.env block should NOT mention the original adoption once drifted, got: %s", envBlock)
	}

	nameBlock := blockFor(out, "name:")
	if !strings.Contains(nameBlock, shortID(adoptID)) {
		t.Fatalf("expected the untouched \"name\" attribute still blamed on the original adoption, got: %s", nameBlock)
	}
}

// row: drift_adopt-set attribute shows the attributed CloudTrail actor.
// TestBlame_CloudTrailActor needs a real genesis first -- blame (like
// FoldState) only ever recognizes a Modification once some create has
// actually seeded the address (TestBlame_ModifyWithNoGenesis_NeverApplied,
// core/blame_test.go, pins exactly this), so handWrittenDriftProposal on
// its own (a modify with no adoption ever recorded) isn't enough here the
// way it is for TestWhy_RendersAttributedCloudTrailSource, which only
// looks the drift proposal up by id directly, never through a fold.
func TestBlame_CloudTrailActor(t *testing.T) {
	ledgerDir := t.TempDir()

	adoptPath := filepath.Join(t.TempDir(), "adopt.json")
	writeFile(t, adoptPath, `{
		"schema_version": 1, "stack": "conformance", "parent": "", "kind": "adoption",
		"intent": {"summary": "adopt"},
		"delta": {"creates": [{"stack": "conformance", "type": "aws_s3_bucket", "name": "ubx-states", "state": {"tags.env": "prod"}}]},
		"resolution": {"resolved_at": "2026-07-10T21:00:00Z", "inputs": [
			{"kind": "live_state", "resource": "conformance.aws_s3_bucket.ubx-states", "observed_hash": "sha256:abc"}
		]},
		"cost_delta": {"monthly_usd": 0}, "blast_radius": {"creates": 0, "modifies": 0, "destroys": 0},
		"status": "draft"
	}`)
	acceptOut, err := runUbx(t, nil, "accept", adoptPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (adopt): %v\noutput: %s", err, acceptOut)
	}
	adoptID := mustExtractID(t, acceptOut)

	driftPath := filepath.Join(t.TempDir(), "drift.json")
	writeFile(t, driftPath, strings.Replace(handWrittenDriftProposal, `"parent": "",`, `"parent": "`+adoptID+`",`, 1))
	acceptOut2, err := runUbx(t, nil, "accept", driftPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept (drift): %v\noutput: %s", err, acceptOut2)
	}

	out, err := runUbx(t, nil, "blame", "conformance.aws_s3_bucket.ubx-states", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx blame: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "arn:aws:iam::839333509514:user/roozbeh") {
		t.Fatalf("expected the attributed CloudTrail actor to render, got: %s", out)
	}
}

// row: redacted attribute provenance.
func TestBlame_RedactedAttributeProvenance(t *testing.T) {
	ledgerDir := t.TempDir()
	proposalPath := filepath.Join(t.TempDir(), "adopt.json")
	writeFile(t, proposalPath, `{
		"schema_version": 1, "stack": "payments", "parent": "", "kind": "adoption",
		"intent": {"summary": "adopt with a sensitive attribute"},
		"delta": {"creates": [{
			"stack": "payments", "type": "aws_db_instance", "name": "payments-db",
			"state": {"id": "db-1", "password": {"$redacted": {"sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}
		}]},
		"resolution": {"resolved_at": "2026-07-20T00:00:00Z", "inputs": [
			{"kind": "live_state", "resource": "payments.aws_db_instance.payments-db", "observed_hash": "sha256:abc"}
		]},
		"cost_delta": {"monthly_usd": 0}, "blast_radius": {"creates": 0, "modifies": 0, "destroys": 0},
		"status": "draft"
	}`)
	acceptOut, err := runUbx(t, nil, "accept", proposalPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", err, acceptOut)
	}
	acceptedID := mustExtractID(t, acceptOut)

	out, err := runUbx(t, nil, "blame", "payments.aws_db_instance.payments-db", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx blame: %v\noutput: %s", err, out)
	}
	if strings.Contains(out, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Fatalf("redacted material must never render, got: %s", out)
	}
	passwordBlock := blockFor(out, "password:")
	if !strings.Contains(passwordBlock, "(redacted)") {
		t.Fatalf("expected \"(redacted)\" in the password block, got: %s", passwordBlock)
	}
	if !strings.Contains(passwordBlock, shortID(acceptedID)) {
		t.Fatalf("expected provenance to survive redaction, got: %s", passwordBlock)
	}
}

// row: address with apply records (create-genesis, UBI-29 discovery path).
func TestBlame_ShippedCreateGenesis(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	intentPath := filepath.Join(ledgerDir, "intent.json")
	writeIntentFile(t, intentPath, map[string]interface{}{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          "payments",
		"intent":         map[string]interface{}{"summary": "genesis for blame"},
		"resources": []map[string]interface{}{
			{"type": "fake_widget", "name": "main", "op": "create", "config": map[string]interface{}{"name": "main"}},
		},
	})
	resolvedPath := filepath.Join(ledgerDir, "resolved.json")
	if out, err := runUbx(t, env, "resolve", intentPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--out", resolvedPath); err != nil {
		t.Fatalf("ubx resolve: %v\noutput: %s", err, out)
	}
	acceptOut, err := runUbx(t, env, "accept", resolvedPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", err, acceptOut)
	}
	changeID := mustExtractID(t, acceptOut)
	if out, err := runUbx(t, env, "ship", changeID, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx ship: %v\noutput: %s", err, out)
	}

	out, err := runUbx(t, nil, "blame", "payments.fake_widget.main", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx blame: %v\noutput: %s", err, out)
	}
	idBlock := blockFor(out, "id:")
	if !strings.Contains(idBlock, shortID(changeID)) {
		t.Fatalf("expected \"id\" (computed, never in config) blamed on the change proposal via its own apply record, got: %s", idBlock)
	}
	if !strings.Contains(idBlock, "change") {
		t.Fatalf("expected the change proposal's own kind to render, got: %s", idBlock)
	}
}

func TestBlame_UnknownAddress_ExitsTwo(t *testing.T) {
	ledgerDir := t.TempDir()
	out, err := runUbx(t, nil, "blame", "payments.aws_s3_bucket.never-recorded", "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 2, out)
	if !strings.Contains(err.Error(), "no recorded history") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBlame_InvalidAddress_ExitsTwo(t *testing.T) {
	out, err := runUbx(t, nil, "blame", "not-a-valid-address", "--ledger-dir", t.TempDir())
	requireExitCode(t, err, 2, out)
	if !strings.Contains(err.Error(), "not a valid resource address") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBlame_JSON_Shape(t *testing.T) {
	ledgerDir := t.TempDir()
	proposalPath := filepath.Join(t.TempDir(), "adopt.json")
	writeFile(t, proposalPath, `{
		"schema_version": 1, "stack": "payments", "parent": "", "kind": "adoption",
		"intent": {"summary": "adopt"},
		"delta": {"creates": [{"stack": "payments", "type": "aws_s3_bucket", "name": "b", "state": {"id": "b-1"}}]},
		"resolution": {"resolved_at": "2026-07-20T00:00:00Z", "inputs": [
			{"kind": "live_state", "resource": "payments.aws_s3_bucket.b", "observed_hash": "sha256:abc"}
		]},
		"cost_delta": {"monthly_usd": 0}, "blast_radius": {"creates": 0, "modifies": 0, "destroys": 0},
		"status": "draft"
	}`)
	acceptOut, err := runUbx(t, nil, "accept", proposalPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", err, acceptOut)
	}
	acceptedID := mustExtractID(t, acceptOut)

	out, err := runUbx(t, nil, "blame", "payments.aws_s3_bucket.b", "--ledger-dir", ledgerDir, "--json")
	if err != nil {
		t.Fatalf("ubx blame --json: %v\noutput: %s", err, out)
	}
	var payload struct {
		Format    int  `json:"format"`
		Destroyed bool `json:"destroyed"`
		Entries   []struct {
			Path       string `json:"path"`
			ProposalID string `json:"proposal_id"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal: %v\noutput: %s", err, out)
	}
	if payload.Format != 1 || payload.Destroyed || len(payload.Entries) != 1 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if payload.Entries[0].Path != "id" || payload.Entries[0].ProposalID != acceptedID {
		t.Fatalf("unexpected entry: %+v", payload.Entries[0])
	}
}

// lineContaining returns the first line of out containing needle, or "".
func lineContaining(out, needle string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}

// blockFor returns the lines from the entry starting with prefix up to
// (not including) the next entry, blame's own "path: value" then indented
// detail lines shape.
func blockFor(out, prefix string) string {
	lines := strings.Split(out, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) {
			start = i
			break
		}
	}
	if start == -1 {
		return ""
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if lines[i] != "" && !strings.HasPrefix(lines[i], " ") {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}
