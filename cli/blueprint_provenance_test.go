package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// appendBlueprintCreatedProposal builds, accepts, and SHIPS (BeginApply/
// SaveApplyProgress/SealApply, mirroring core/ubi29_test.go's own
// shipChangeCreateForTest) a kind:"change" proposal whose Delta.Creates
// carries exactly the shape resolver.go's own resolveOnce produces for a
// blueprint-sourced resource (UBI-74 Slice 6) -- a real "sources":
// [{"kind": "blueprint", "ref": ...}] entry inside the create node
// itself, not just document-level Intent.Sources (appendAcceptedProposal,
// promote_test.go, only ever populates the latter -- a genuinely
// different field, per-resource here). Shipping (not just accepting) is
// required here, not optional test ceremony: core.Ledger.
// ProposalsForAddress -- the resolution `ubx why <address>` depends on --
// only ever surfaces a change proposal's own create once it's confirmed
// SHIPPED (see core/state.go's ProposalsForAddress doc comment); an
// accepted-but-unshipped create is invisible to the address form, which
// is exactly the form Slice 6's own success bar names ("ubx why
// <address>").
func appendBlueprintCreatedProposal(t *testing.T, ledgerDir, blueprintRef string) (*core.Proposal, core.Address) {
	t.Helper()
	addr := core.Address{Stack: "payments", Type: "aws_ecr_repository", Name: "container-repo"}
	createJSON := fmt.Sprintf(
		`{"stack":"payments","type":"aws_ecr_repository","name":"container-repo","provider":{"source":"hashicorp/aws","version":"6.54.0"},"config":{"name":"payments-ci-artifacts"},"sources":[{"kind":"blueprint","ref":%q}]}`,
		blueprintRef,
	)
	ledger := core.Open(ledgerDir)
	head, err := ledger.Head()
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	// core.Accept requires p to be draft-shaped (ID/Acceptance unset --
	// it computes and fills both itself), so the fixture's real ID/
	// Acceptance come back on the returned *Proposal, never set by hand
	// here.
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         "payments",
		Parent:        head,
		Kind:          core.KindChange,
		Intent:        core.Intent{Summary: "call the ci-platform blueprint"},
		Delta:         core.Delta{Creates: []json.RawMessage{json.RawMessage(createJSON)}},
		Resolution:    core.Resolution{ResolvedAt: "2026-08-05T00:00:00Z"},
		CostDelta:     core.CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius:   core.BlastRadius{Creates: 1},
		Status:        core.StatusDraft,
	}
	accepted, err := core.Accept(ledger, p)
	if err != nil {
		t.Fatalf("accept fixture proposal: %v", err)
	}

	rec, err := ledger.BeginApply(accepted.ID)
	if err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	now := "2026-08-05T00:00:00Z"
	rec.Resources = append(rec.Resources, &core.ResourceApply{
		Address: addr,
		Transitions: []core.Transition{
			{State: core.ResourcePending, At: now},
			{State: core.ResourceInFlight, At: now},
			{State: core.ResourceApplied, At: now},
		},
		ProviderResult: json.RawMessage(`{"id":"payments-ci-artifacts"}`),
	})
	if err := ledger.SaveApplyProgress(rec); err != nil {
		t.Fatalf("save apply progress: %v", err)
	}
	if _, err := ledger.SealApply(rec, core.ApplySummary{
		Outcome: "applied", StartedAt: now, FinishedAt: now, ResourcesApplied: 1,
	}); err != nil {
		t.Fatalf("seal apply: %v", err)
	}
	return accepted, addr
}

// TestWhy_ProposalID_RendersBlueprintProvenance is UBI-74 Slice 6's own
// required hermetic proof for the bare-proposal-ID form of `ubx why`:
// a blueprint-created resource's own create entry renders its "source:
// blueprint <name>:sha256:<short-hash>" line, the honest dual-signature
// note, AND the surrounding proposal's own real "accepted by" line --
// the two halves of the provenance chain, both visible.
func TestWhy_ProposalID_RendersBlueprintProvenance(t *testing.T) {
	ledgerDir := t.TempDir()
	fullHash := "sha256:" + strings.Repeat("a", 64)
	accepted, _ := appendBlueprintCreatedProposal(t, ledgerDir, "ci-platform:"+fullHash)

	out, err := runUbx(t, nil, "why", accepted.ID, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why: %v\noutput: %s", err, out)
	}
	wantBlueprintLine := "source: blueprint ci-platform:sha256:" + displayHash(strings.Repeat("a", 64), false)
	if !strings.Contains(out, wantBlueprintLine) {
		t.Errorf("expected why to render %q, got:\n%s", wantBlueprintLine, out)
	}
	if !strings.Contains(out, "no separate signing ceremony") {
		t.Errorf("expected why to name the honest authorship-signing gap, got:\n%s", out)
	}
	// The real "local" acceptance Accept() itself records -- approver is
	// whatever OS user actually ran this test, not a fixed name.
	if !strings.Contains(out, "accepted by [") || !strings.Contains(out, "] via local at") {
		t.Errorf("expected why to render the real calling-stack acceptance, got:\n%s", out)
	}
}

// TestWhy_ResourceAddress_RendersBlueprintProvenance is the SAME proof,
// through `ubx why <address>` (the chain view, renderProposalCompact) --
// a genuinely different code path than the bare-proposal-ID form above,
// and the one the ticket's own success bar names directly ("ubx why
// <address>"). Acceptance rendering in the chain view is new this slice
// (renderProposalCompact never rendered it before) -- checked explicitly
// here, not assumed to already work.
func TestWhy_ResourceAddress_RendersBlueprintProvenance(t *testing.T) {
	ledgerDir := t.TempDir()
	fullHash := "sha256:" + strings.Repeat("b", 64)
	_, addr := appendBlueprintCreatedProposal(t, ledgerDir, "ci-platform:"+fullHash)

	out, err := runUbx(t, nil, "why", addr.String(), "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why <address>: %v\noutput: %s", err, out)
	}
	wantBlueprintLine := "source: blueprint ci-platform:sha256:" + displayHash(strings.Repeat("b", 64), false)
	if !strings.Contains(out, wantBlueprintLine) {
		t.Errorf("expected why to render %q, got:\n%s", wantBlueprintLine, out)
	}
	if !strings.Contains(out, "accepted by [") || !strings.Contains(out, "] via local at") {
		t.Errorf("expected the chain view to also render the real calling-stack acceptance, got:\n%s", out)
	}
}

// TestWhy_NonBlueprintResource_RendersUnchanged confirms an ordinary
// resource (no Sources at all) renders exactly as before this session --
// no blueprint line, no regression to the plain create-header/attribute
// shape every other `ubx why` test already pins.
func TestWhy_NonBlueprintResource_RendersUnchanged(t *testing.T) {
	ledgerDir := t.TempDir()
	id := strings.Repeat("7", 64)
	ledger := core.Open(ledgerDir)
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		ID:            id,
		Stack:         "payments",
		Kind:          core.KindChange,
		Intent:        core.Intent{Summary: "ordinary resource, no blueprint involved"},
		Delta: core.Delta{Creates: []json.RawMessage{json.RawMessage(
			`{"stack":"payments","type":"aws_vpc","name":"main","provider":{"source":"hashicorp/aws","version":"6.54.0"},"config":{"cidr_block":"10.0.0.0/16"}}`,
		)}},
		Acceptance: &core.Acceptance{Method: "local", Approvers: []string{"roozbeh"}, AcceptedAt: "2026-08-05T00:00:00Z"},
		Status:     core.StatusAccepted,
	}
	if err := ledger.Append(p); err != nil {
		t.Fatalf("append fixture proposal: %v", err)
	}

	out, err := runUbx(t, nil, "why", id, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why: %v\noutput: %s", err, out)
	}
	if strings.Contains(out, "source: blueprint") {
		t.Errorf("a resource with no Sources should never render a blueprint provenance line, got:\n%s", out)
	}
}
