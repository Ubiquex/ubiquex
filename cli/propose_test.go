package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex-cli/core"
)

const draftProposalJSON = `{
	"schema_version": 1,
	"stack": "payments",
	"parent": "",
	"kind": "change",
	"intent": {"summary": "postgres for payments", "sources": [{"kind": "dialogue", "ref": "d-1"}]},
	"delta": {"creates": [{"stack": "payments", "type": "aws_db_instance", "name": "payments-db"}]},
	"resolution": {"resolved_at": "2026-07-11T00:00:00Z"},
	"cost_delta": {"monthly_usd": 59},
	"blast_radius": {"creates": 1, "modifies": 0, "destroys": 0},
	"status": "draft"
}`

func TestPropose_PrintsTheSameHashAcceptWouldCompute(t *testing.T) {
	proposalPath := filepath.Join(t.TempDir(), "proposal.json")
	writeFile(t, proposalPath, draftProposalJSON)

	out, err := runUbx(t, nil, "propose", proposalPath)
	if err != nil {
		t.Fatalf("ubx propose: %v\noutput: %s", err, out)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "ubx-proposal: ") {
		t.Fatalf("expected a ubx-proposal trailer line, got: %s", out)
	}
	trailerHash := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "ubx-proposal: "))

	// Cross-check against core.Hash directly on the same content.
	var p core.Proposal
	if err := json.Unmarshal([]byte(draftProposalJSON), &p); err != nil {
		t.Fatal(err)
	}
	want, err := core.Hash(&p)
	if err != nil {
		t.Fatal(err)
	}
	if trailerHash != want {
		t.Fatalf("propose printed %q, want %q (core.Hash of the same content)", trailerHash, want)
	}
}

func TestPropose_RejectsAlreadyAcceptedProposal(t *testing.T) {
	proposalPath := filepath.Join(t.TempDir(), "proposal.json")
	writeFile(t, proposalPath, `{
		"schema_version": 1, "stack": "payments", "parent": "", "kind": "change",
		"intent": {"summary": "x"}, "delta": {},
		"resolution": {"resolved_at": "2026-07-11T00:00:00Z"},
		"cost_delta": {}, "blast_radius": {"creates": 0, "modifies": 0, "destroys": 0},
		"id": "already-has-one",
		"status": "accepted"
	}`)

	_, err := runUbx(t, nil, "propose", proposalPath)
	if err == nil {
		t.Fatal("expected propose to reject a proposal that already has an id")
	}
	if !strings.Contains(err.Error(), "drafts only") {
		t.Fatalf("expected a drafts-only error, got: %v", err)
	}
}
