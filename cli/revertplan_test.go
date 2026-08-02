package cli

import (
	"os"
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

// revertPlanTF models the "mixed" case: instance_type is a plain literal
// (currently the drifted value, since nothing has corrected .tf yet) --
// safely rewritable; ami is an expression -- undeclinable.
const revertPlanTF = `resource "aws_instance" "web" {
  instance_type = "t3.large"
  ami           = var.ami_id
}
`

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

func TestRevertPlan_PrintsHumanReadablePlan_NoTFDir(t *testing.T) {
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
		t.Fatalf("no --tf-dir given -- must not print a .tf diff section, got: %s", out)
	}
	if !strings.Contains(out, "ubx never makes it for you") {
		t.Fatalf("expected the never-makes-it-for-you reminder, got: %s", out)
	}
}

func TestRevertPlan_TFDir_MixedLiteralAndNonLiteral(t *testing.T) {
	ledgerDir := t.TempDir()
	id := acceptRevertPlanProposal(t, ledgerDir)

	tfDir := t.TempDir()
	writeFile(t, filepath.Join(tfDir, "compute.tf"), revertPlanTF)

	out, err := runUbx(t, nil, "revert-plan", id, "--ledger-dir", ledgerDir, "--tf-dir", tfDir)
	// A declined attribute means manual steps are needed -- an actionable
	// finding, not a failure (UBI-20 exit-code contract): exit 1.
	requireExitCode(t, err, 1, out)

	// instance_type is literal -- applied, shown as a real diff, restoring
	// the ledger's recorded value.
	if !strings.Contains(out, "--- .tf diff ---") {
		t.Fatalf("expected a .tf diff section, got: %s", out)
	}
	if !strings.Contains(out, `-  instance_type = "t3.large"`) || !strings.Contains(out, `+  instance_type = "t3.medium"`) {
		t.Fatalf("expected a unified diff restoring instance_type, got: %s", out)
	}

	// ami is an expression -- declined, reported as a manual step, not a
	// command failure.
	if !strings.Contains(out, "--- manual steps ---") {
		t.Fatalf("expected a manual-steps section, got: %s", out)
	}
	if !strings.Contains(out, "ami") || !strings.Contains(out, "var.ami_id") {
		t.Fatalf("expected ami's offending expression named in manual steps, got: %s", out)
	}

	// The .tf file on disk must be untouched -- revert-plan never writes.
	onDisk, err := os.ReadFile(filepath.Join(tfDir, "compute.tf"))
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != revertPlanTF {
		t.Fatalf("revert-plan must never modify the .tf file; got:\n%s", onDisk)
	}
}

func TestRevertPlan_ResourceNotFoundInTFDir_ManualStep(t *testing.T) {
	ledgerDir := t.TempDir()
	id := acceptRevertPlanProposal(t, ledgerDir)

	tfDir := t.TempDir()
	writeFile(t, filepath.Join(tfDir, "network.tf"), "resource \"aws_vpc\" \"main\" {\n  cidr_block = \"10.0.0.0/16\"\n}\n")

	out, err := runUbx(t, nil, "revert-plan", id, "--ledger-dir", ledgerDir, "--tf-dir", tfDir)
	// A resource absent from --tf-dir means a manual step is needed -- an
	// actionable finding, not a failure (UBI-20 exit-code contract): exit 1.
	requireExitCode(t, err, 1, out)
	if !strings.Contains(out, "--- manual steps ---") {
		t.Fatalf("expected a manual-steps section, got: %s", out)
	}
	if !strings.Contains(out, "no matching resource block found") {
		t.Fatalf("expected the underlying writeback error named, got: %s", out)
	}
	if strings.Contains(out, ".tf diff") {
		t.Fatalf("nothing was applied -- must not print a .tf diff section, got: %s", out)
	}
}

func TestRevertPlan_RejectsNonDriftRevertKind(t *testing.T) {
	ledgerDir := t.TempDir()
	id := acceptWritebackProposal(t, ledgerDir) // kind drift_adopt

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
