package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

const revertPlanProposal = `{
	"schema_version": 1,
	"stack": "s",
	"parent": "",
	"kind": "drift_revert",
	"intent": {"summary": "revert s.aws_instance.web back to the ledger's recorded state"},
	"delta": {
		"modifies": [
			{
				"target": {"stack": "s", "type": "aws_instance", "name": "web"},
				"before": {"instance_type": "t3.large", "ami": "ami-new"},
				"after": {"instance_type": "t3.medium", "ami": "ami-old"}
			}
		]
	},
	"resolution": {
		"resolved_at": "2026-07-11T00:00:00Z",
		"inputs": [
			{"kind": "live_state", "resource": "s.aws_instance.web", "observed_hash": "sha256:abc"}
		]
	},
	"cost_delta": {"monthly_usd": 0},
	"blast_radius": {"creates": 0, "modifies": 1, "destroys": 0},
	"status": "draft"
}`

func acceptRevertPlanProposal(t *testing.T, ledgerDir string) string {
	t.Helper()
	proposalPath := filepath.Join(t.TempDir(), "revert.json")
	writeFile(t, proposalPath, revertPlanProposal)
	acceptOut, err := runUbx(t, nil, "accept", proposalPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", err, acceptOut)
	}
	return mustExtractID(t, acceptOut)
}

func TestRevertPlan_PrintsHumanReadablePlan(t *testing.T) {
	ledgerDir := t.TempDir()
	id := acceptRevertPlanProposal(t, ledgerDir)

	out, err := runUbx(t, nil, "revert-plan", id, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx revert-plan: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "--- plan ---") {
		t.Fatalf("expected a plan section, got: %s", out)
	}
	if !strings.Contains(out, `s.aws_instance.web: ami: "ami-new" -> "ami-old"`) {
		t.Fatalf("expected the ami line (current -> restore-to), got: %s", out)
	}
	if !strings.Contains(out, `s.aws_instance.web: instance_type: "t3.large" -> "t3.medium"`) {
		t.Fatalf("expected the instance_type line (current -> restore-to), got: %s", out)
	}
	if strings.Contains(out, ".tf diff") {
		t.Fatalf("revert-plan no longer emits a .tf diff (UBI-301), got: %s", out)
	}
	if !strings.Contains(out, "ubx never makes it for you") {
		t.Fatalf("expected the never-makes-it-for-you reminder, got: %s", out)
	}
}

func TestRevertPlan_RejectsNonDriftRevertKind(t *testing.T) {
	ledgerDir := t.TempDir()
	id := acceptDriftAdoptProposal(t, ledgerDir) // kind drift_adopt

	_, err := runUbx(t, nil, "revert-plan", id, "--ledger-dir", ledgerDir)
	if err == nil {
		t.Fatal("expected revert-plan to refuse a non-drift_revert proposal")
	}
	if !strings.Contains(err.Error(), "not drift_revert") {
		t.Fatalf("expected a kind-mismatch error, got: %v", err)
	}
}

func TestRevertPlan_RejectsUnacceptedProposal(t *testing.T) {
	ledgerDir := t.TempDir()
	ledger := core.Open(ledgerDir)

	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		ID:            "0000000000000000000000000000000000000000000000000000000000000000",
		Stack:         "s",
		Kind:          core.KindDriftRevert,
		Intent:        core.Intent{Summary: "x"},
		Resolution:    core.Resolution{ResolvedAt: "2026-07-11T00:00:00Z"},
		Status:        core.StatusDraft,
	}
	if err := ledger.Append(p); err != nil {
		t.Fatal(err)
	}

	_, err := runUbx(t, nil, "revert-plan", p.ID, "--ledger-dir", ledgerDir)
	if err == nil {
		t.Fatal("expected revert-plan to refuse an unaccepted proposal")
	}
	if !strings.Contains(err.Error(), "not accepted") {
		t.Fatalf("expected a not-accepted error, got: %v", err)
	}
}

// TestRawOrAbsent_CompactsIndentedJSON is UBI-61's own regression test: a
// proposal round-tripped through a saved plan file (.ubx/plans/<hash>.json,
// itself written pretty-printed via json.MarshalIndent for human
// readability -- same as the ledger's own on-disk proposal format) must
// never carry that indentation as literal bytes into a one-line rendering
// -- `ubx ship`'s receipt for a plan-store hash rendered nested JSON
// (tags, etc.) indented across several lines before this fix, while `ubx
// plan`'s own receipt for identical content rendered it compact, purely
// because of which code path happened to touch the bytes last.
func TestRawOrAbsent_CompactsIndentedJSON(t *testing.T) {
	indented := []byte("{\n  \"env\": \"prod\",\n  \"team\": \"payments\"\n}")
	got := rawOrAbsent(indented)
	want := `{"env":"prod","team":"payments"}`
	if got != want {
		t.Fatalf("rawOrAbsent(indented JSON) = %q, want compacted %q", got, want)
	}
}

// driftAdoptProposal is a drift_adopt proposal, the kind revert-plan
// refuses. It lived with writeback's tests until UBI-301 removed them.
const driftAdoptProposal = `{
	"schema_version": 1,
	"stack": "s",
	"parent": "",
	"kind": "drift_adopt",
	"intent": {"summary": "record drift on s.aws_instance.web observed outside the ledger"},
	"delta": {
		"modifies": [
			{
				"target": {"stack": "s", "type": "aws_instance", "name": "web"},
				"before": {"instance_type": "t3.medium"},
				"after": {"instance_type": "t3.large"}
			}
		]
	},
	"resolution": {
		"resolved_at": "2026-07-11T00:00:00Z",
		"inputs": [
			{"kind": "live_state", "resource": "s.aws_instance.web", "observed_hash": "sha256:abc"}
		]
	},
	"cost_delta": {"monthly_usd": 0},
	"blast_radius": {"creates": 0, "modifies": 0, "destroys": 0},
	"status": "draft"
}`

func acceptDriftAdoptProposal(t *testing.T, ledgerDir string) string {
	t.Helper()
	proposalPath := filepath.Join(t.TempDir(), "drift.json")
	writeFile(t, proposalPath, driftAdoptProposal)
	acceptOut, err := runUbx(t, nil, "accept", proposalPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", err, acceptOut)
	}
	return mustExtractID(t, acceptOut)
}
