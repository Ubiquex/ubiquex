package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// UBI-251. Two behaviours that a source scan cannot see and that the
// existing end-to-end receipt tests did not cover: which proposals get a
// summary sentence, and when the cost line renders at all.
//
// Both are gates rather than formatting, so each case below is a real
// proposal shape from somewhere in the tree, not a synthetic one.

func renderReceipt(t *testing.T, p *core.Proposal) string {
	t.Helper()
	var buf bytes.Buffer
	renderPlanReceipt(&buf, nil, p, "Plan  payments", true)
	return buf.String()
}

func authoredProposal(summary string) *core.Proposal {
	return &core.Proposal{
		Stack: "payments",
		Kind:  core.KindChange,
		Intent: core.Intent{
			Summary: summary,
			Sources: []core.IntentSource{{Kind: core.SourceKindDocument, Ref: "payments.md"}},
		},
	}
}

func TestReceiptSummary_RendersForAuthoredIntent(t *testing.T) {
	// The real summary from sdk/conformance/golden/payments.json. The
	// clause after the comma is the whole argument for having this line:
	// it is in no resource block and cannot be derived from one.
	const summary = "Provision a small Postgres RDS instance in the payments stack, " +
		"modeled on the staging database but downsized for low initial traffic."
	out := renderReceipt(t, authoredProposal(summary))
	if !strings.Contains(out, summary) {
		t.Fatalf("an authored intent should show its summary, got:\n%s", out)
	}
}

func TestReceiptSummary_SuppressedForMechanicalPaths(t *testing.T) {
	// Exactly the summaries scan, adopt, revert and restore write today,
	// verbatim from core/scan.go and cli/restore.go. Each is a paraphrase
	// of the single resource it names, which is the noise v2 removed.
	cases := []struct {
		name    string
		summary string
		sources []core.IntentSource
	}{
		{
			name:    "scan adopt",
			summary: "adopt existing payments.fake_widget.widget1 into the ledger (discovered by scan)",
		},
		{
			name:    "scan drift",
			summary: "record drift on payments.fake_widget.widget1 observed outside the ledger",
		},
		{
			name:    "drift revert",
			summary: "revert payments.fake_widget.widget1 back to the ledger's recorded state",
		},
		{
			// The one that makes proposal kind the wrong discriminator:
			// restore builds a KindChange proposal, so gating on kind
			// would print this.
			name:    "restore",
			summary: "restore payments to ledger head 4b1e77a2c3d4",
			sources: []core.IntentSource{{Kind: "restore", Ref: "4b1e77a2c3d4"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &core.Proposal{
				Stack:  "payments",
				Kind:   core.KindChange,
				Intent: core.Intent{Summary: tc.summary, Sources: tc.sources},
			}
			out := renderReceipt(t, p)
			if strings.Contains(out, tc.summary) {
				t.Fatalf("a mechanical summary must not render, got:\n%s", out)
			}
		})
	}
}

func TestReceiptSummary_PromotionKeepsItsAuthoredSummary(t *testing.T) {
	// cli/promote.go appends its own source rather than replacing, so a
	// promoted proposal still carries the document it was authored from.
	// If that ever changes to a replace, this fails.
	p := authoredProposal("Provision a small Postgres RDS instance in the payments stack.")
	p.Intent.Sources = append(p.Intent.Sources, core.IntentSource{Kind: "promotion", Base: "staging"})
	if !strings.Contains(renderReceipt(t, p), "Provision a small Postgres") {
		t.Fatal("a promoted proposal should still show the summary it was authored with")
	}
}

func TestReceiptSummary_FirstParagraphOnly(t *testing.T) {
	// The marketing design's second paragraph is a cost claim, and there
	// is no pricing source, so the receipt must not carry it even if a
	// model writes one.
	p := authoredProposal("Adds a primary Postgres instance with a read replica.\n\n" +
		"The replica is the largest share of the cost increase.")
	out := renderReceipt(t, p)
	if !strings.Contains(out, "Adds a primary Postgres instance with a read replica.") {
		t.Fatalf("the first paragraph should render, got:\n%s", out)
	}
	if strings.Contains(out, "largest share of the cost increase") {
		t.Fatalf("a second paragraph must not render, got:\n%s", out)
	}
}

func TestReceiptCostLine_HiddenUntilSomethingCanPrice(t *testing.T) {
	// Every writer in the tree sets a literal 0 today. The old guard was
	// len(...) > 0, which never suppressed it: json.RawMessage("0") has
	// length 1, so every receipt printed "$0/mo".
	for _, raw := range []string{"", "0", `"0"`, "null", `""`} {
		p := authoredProposal("a real authored summary")
		p.CostDelta = core.CostDelta{MonthlyUSD: json.RawMessage(raw)}
		if out := renderReceipt(t, p); strings.Contains(out, "cost delta:") {
			t.Fatalf("MonthlyUSD %q is not a priced figure, got:\n%s", raw, out)
		}
	}
}

func TestReceiptCostLine_RendersOnceThereIsAFigure(t *testing.T) {
	// The suppression above must not become "the line is gone". When a
	// pricing source lands and writes a real number, it renders.
	for _, raw := range []string{"244", `"244.00"`} {
		p := authoredProposal("a real authored summary")
		p.CostDelta = core.CostDelta{MonthlyUSD: json.RawMessage(raw)}
		if out := renderReceipt(t, p); !strings.Contains(out, "cost delta: $") {
			t.Fatalf("MonthlyUSD %q is a real figure and should render, got:\n%s", raw, out)
		}
	}
}
