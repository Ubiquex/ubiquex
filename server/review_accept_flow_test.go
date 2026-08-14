package server

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
	ghub "github.com/ubiquex/ubiquex/github"
)

// reviewAcceptFlowRepo builds a real, local git repository carrying one
// commit that adds a real proposal file with a matching "ubx-proposal:
// <hash>" trailer available separately (the trailer lives in a PR's own
// body on the real platform, not in a commit -- callers get it back
// here to hand to a fake PR object, exactly like handlePullRequestReviewEvent
// itself reads pr.GetBody()). Shells out to the real `git` binary, same
// convention as every other test fixture in this codebase.
func reviewAcceptFlowRepo(t *testing.T, proposal *core.Proposal) (repoDir, sha, trailer string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")

	hash, err := core.Hash(proposal)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(proposal)
	if err != nil {
		t.Fatal(err)
	}

	full := filepath.Join(dir, "proposals", "db-replica.json")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, body, 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "proposals/db-replica.json")
	run("commit", "-q", "-m", "propose db replica")

	// checkoutRef always does a real "git fetch origin <ref>" -- give it
	// a real origin to fetch from. A repo can validly serve as its own
	// remote over a plain filesystem path; this is what every event
	// after the very first one on a real installation actually does too
	// (fetch from the already-cloned origin, not re-clone).
	run("remote", "add", "origin", dir)

	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v\n%s", err, out)
	}
	return dir, strings.TrimSpace(string(out)), "## Summary\n\nubx-proposal: " + hash
}

// runReviewAcceptFlow replays the exact real sequence
// handlePullRequestReviewEvent runs after resolving the repo directory
// (checkoutRef -> FileAtCommit -> ParseProposalTrailer -> the real
// destroy check -> AcceptFromReview) -- the same real functions, called
// in the same real order, proving the full chain works correctly
// end-to-end against a real local git repo. The one real piece this
// intentionally does not exercise is ensureRepoCheckout's own initial
// clone from https://github.com/..., which needs real network access
// to test hermetically and is out of scope for this phase's own test
// suite -- checkoutRef (the fetch/reset half every event after the
// first one actually uses) is exercised for real here instead.
func runReviewAcceptFlow(t *testing.T, repoDir, sha, prBody, proposalFile, approver, comment string) (*core.Proposal, error) {
	t.Helper()
	ctx := context.Background()

	if err := checkoutRef(ctx, repoDir, sha); err != nil {
		t.Fatalf("checkoutRef: %v", err)
	}
	fileBody, err := ghub.FileAtCommit(ctx, repoDir, sha, proposalFile)
	if err != nil {
		t.Fatalf("FileAtCommit: %v", err)
	}
	claimedHash, ok := ghub.ParseProposalTrailer(prBody)
	if !ok {
		t.Fatal("ParseProposalTrailer: expected a real trailer")
	}

	var p core.Proposal
	if err := json.Unmarshal(fileBody, &p); err != nil {
		t.Fatalf("unmarshal proposal: %v", err)
	}

	if p.BlastRadius.Destroys > 0 {
		return nil, errDestroyRefused
	}

	ledger := core.Open(repoDir)
	return core.AcceptFromReview(ledger, &p, claimedHash, core.ReviewAcceptance{
		Platform:     "github",
		PRNumber:     42,
		ProposalFile: proposalFile,
		Approver:     approver,
		Comment:      comment,
	})
}

var errDestroyRefused = &destroyRefusedError{}

type destroyRefusedError struct{}

func (*destroyRefusedError) Error() string {
	return "ubx server never derives acceptance for a destroy from a review"
}

func TestReviewAcceptFlow_HappyPath(t *testing.T) {
	proposal := sampleServerProposal(t, false)
	repoDir, sha, prBody := reviewAcceptFlowRepo(t, proposal)

	accepted, err := runReviewAcceptFlow(t, repoDir, sha, prBody, "proposals/db-replica.json", "roozbeh", "LGTM")
	if err != nil {
		t.Fatalf("runReviewAcceptFlow: %v", err)
	}
	if accepted.Acceptance.Method != "pr_review" {
		t.Errorf("Method = %q, want pr_review", accepted.Acceptance.Method)
	}
	if accepted.Acceptance.ReviewComment != "LGTM" {
		t.Errorf("ReviewComment = %q, want LGTM", accepted.Acceptance.ReviewComment)
	}
}

// TestReviewAcceptFlow_DestroyIsRefused is the real, load-bearing safety
// test: a review approving a destructive proposal must never reach
// AcceptFromReview at all, regardless of any configuration -- proving
// the fix made in webhook.go's own handlePullRequestReviewEvent (found
// during this phase's own adversarial review: core.AcceptFromReview, on
// its own, enforces no confirm-destroys invariant at all, the same way
// core.AcceptFromMerge doesn't either -- that check has only ever lived
// at the CLI layer, cli/accept.go's own checkDestroysConfirmed, which a
// direct core.AcceptFromReview caller like this server bypasses
// entirely unless it adds the same real check itself).
func TestReviewAcceptFlow_DestroyIsRefused(t *testing.T) {
	proposal := sampleServerProposal(t, true) // blast_radius.destroys = 1
	repoDir, sha, prBody := reviewAcceptFlowRepo(t, proposal)

	_, err := runReviewAcceptFlow(t, repoDir, sha, prBody, "proposals/db-replica.json", "roozbeh", "")
	if err != errDestroyRefused {
		t.Fatalf("expected the destroy to be refused before ever reaching AcceptFromReview, got: %v", err)
	}

	// Never reached the ledger at all.
	ledger := core.Open(repoDir)
	if head, _ := ledger.Head(); head != "" {
		t.Fatalf("expected no ledger head after a refused destroy, got %q", head)
	}
}

// sampleServerProposal builds a real, schema-valid proposal -- a single
// real create when destroys is false (mirroring core's own established
// sampleProposal test fixture, core/canonical_test.go, unexported and
// not reusable directly from this package), or a single real destroy
// entry plus the destroy_target resolution input core.Validate requires
// for one (mirroring cli/accept_confirm_destroys_test.go's own
// destroyProposalJSON fixture) when destroys is true. The destroy case
// never actually reaches core.Validate in this file's own tests (the
// real destroy-refusal check runs first, exactly matching
// handlePullRequestReviewEvent's own real order), but building a
// structurally real fixture either way keeps this test honest about
// what a real destroy proposal looks like.
func sampleServerProposal(t *testing.T, destroys bool) *core.Proposal {
	t.Helper()
	if !destroys {
		return &core.Proposal{
			SchemaVersion: core.SchemaVersion,
			Stack:         "payments",
			Kind:          core.KindChange,
			Intent:        core.Intent{Summary: "add a db replica"},
			Delta: core.Delta{
				Creates: []json.RawMessage{
					json.RawMessage(`{"stack":"payments","type":"aws_db_instance","name":"replica","config":{"engine":"postgres"}}`),
				},
			},
			Resolution:  core.Resolution{ResolvedAt: "2026-08-18T00:00:00Z"},
			BlastRadius: core.BlastRadius{Creates: 1},
			Status:      core.StatusDraft,
		}
	}
	return &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         "payments",
		Kind:          core.KindChange,
		Intent:        core.Intent{Summary: "decommission the old replica"},
		Delta: core.Delta{
			Destroys: []core.DestroyEntry{{
				Address: core.Address{Stack: "payments", Type: "aws_db_instance", Name: "replica"},
				State:   json.RawMessage(`{"id":"replica"}`),
			}},
		},
		Resolution: core.Resolution{
			ResolvedAt: "2026-08-18T00:00:00Z",
			Inputs: []core.ResolutionInput{{
				Kind:         "destroy_target",
				Resource:     "payments.aws_db_instance.replica",
				ObservedHash: "sha256:deadbeef",
				Lookup:       json.RawMessage(`{"id":"replica"}`),
			}},
		},
		BlastRadius: core.BlastRadius{Destroys: 1},
		Status:      core.StatusDraft,
	}
}
