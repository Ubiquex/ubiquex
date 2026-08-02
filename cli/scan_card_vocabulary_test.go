package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// TestRenderScanCard_DefaultCase_ChangeTerminateVocabulary is UBI-88's own
// vocabulary-sweep regression test for renderScanCard's own "default"
// branch (cli/scan.go). Unlike the other three flagged surfaces, this one
// is exercised directly rather than through a real `ubx scan --propose`
// invocation: every real proposal `ubx scan` ever generates is Kind
// adoption/drift_adopt/drift_revert (core.GenerateProposal/
// GenerateRevertProposal, scan.go's own only two callers feeding
// renderScanCard), each already handled by its own explicit switch case
// above the default -- confirmed by scan_propose_test.go's own
// cardHeaderPattern comment ("kind-specific descriptive text ... replaced
// the old +N create(s) ~N modify(ies) -N destroy(s) summary"). The
// default branch is live, reachable Go code (any other proposal Kind
// would still hit it), just not reachable through the real CLI today --
// so this test calls renderScanCard directly rather than claiming a
// terminal transcript that the current `ubx scan` command can't actually
// produce.
func TestRenderScanCard_DefaultCase_ChangeTerminateVocabulary(t *testing.T) {
	p := &core.Proposal{
		Kind:        core.KindChange,
		BlastRadius: core.BlastRadius{Creates: 1, Modifies: 2, Destroys: 3},
	}
	var buf bytes.Buffer
	renderScanCard(&buf, nil, p, "deadbeef")
	out := buf.String()

	if !strings.Contains(out, "+1 create(s) ~2 change(s) -3 terminate(s)") {
		t.Fatalf("expected the change/terminate vocabulary, got: %s", out)
	}
	if strings.Contains(out, "modify(ies)") || strings.Contains(out, "destroy(s)") {
		t.Fatalf("expected no lingering modify(ies)/destroy(s) wording, got: %s", out)
	}
}
