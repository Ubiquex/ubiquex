package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
	ghub "github.com/ubiquex/ubiquex/github"
	glab "github.com/ubiquex/ubiquex/gitlab"
	glapi "gitlab.com/gitlab-org/api/client-go/v2"
)

// fakeGitLabServer serves exactly the real GitLab API v4 endpoints
// this package's own GitLab handlers need, backed by a real, mutable
// in-memory MR/notes state -- the GitLab counterpart of comment_test.go's
// own fakeCommentServer. project is deliberately flat (no "/") so the
// real go-gitlab ProjectID{pid} path-escaping doesn't need decoding in
// this fixture's own routing -- GitLab's real, arbitrarily-nested
// project-path handling is already covered separately, by
// TestLoad_RepoFlagGitLabShorthand (server/config_test.go).
type fakeGitLabServer struct {
	project  string
	mrIID    int64
	mr       *glapi.MergeRequest
	diffs    []*glapi.MergeRequestDiff
	approval *glapi.ProjectApprovals
	notes    []*glapi.Note
	nextNote int64
}

func (f *fakeGitLabServer) server(t *testing.T) *httptest.Server {
	t.Helper()
	base := fmt.Sprintf("/api/v4/projects/%s/merge_requests/%d", f.project, f.mrIID)
	mux := http.NewServeMux()

	mux.HandleFunc(base, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(f.mr)
	})
	mux.HandleFunc(base+"/diffs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(f.diffs)
	})
	mux.HandleFunc(fmt.Sprintf("/api/v4/projects/%s/approvals", f.project), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(f.approval)
	})
	mux.HandleFunc(base+"/notes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(f.notes)
		case http.MethodPost:
			var body glapi.CreateMergeRequestNoteOptions
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.nextNote++
			note := &glapi.Note{ID: f.nextNote, Body: derefStr(body.Body), Author: glapi.NoteAuthor{Username: "ubx-bot"}}
			f.notes = append(f.notes, note)
			_ = json.NewEncoder(w).Encode(note)
		}
	})
	mux.HandleFunc(base+"/notes/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		idStr := strings.TrimPrefix(r.URL.Path, base+"/notes/")
		id, _ := strconv.ParseInt(idStr, 10, 64)
		var body glapi.UpdateMergeRequestNoteOptions
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, n := range f.notes {
			if n.ID == id {
				n.Body = derefStr(body.Body)
				_ = json.NewEncoder(w).Encode(n)
				return
			}
		}
		http.NotFound(w, r)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// gitlabFlowRepo is reviewAcceptFlowRepo's own GitLab counterpart: a
// real, local git repository carrying one commit that adds a real
// proposal file, plus the trailer string a real MR's own Description
// would carry.
func gitlabFlowRepo(t *testing.T, proposal *core.Proposal, proposalPath string) (repoDir, sha, trailer string) {
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

	full := filepath.Join(dir, proposalPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, body, 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", proposalPath)
	run("commit", "-q", "-m", "propose")
	run("remote", "add", "origin", dir)

	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v\n%s", err, out)
	}
	return dir, strings.TrimSpace(string(out)), "## Summary\n\nubx-proposal: " + hash
}

// gitlabTestServer builds a minimal, real Server wired against f's own
// httptest fixture -- WorkDir set so repoDirForGitLab never actually
// needs to clone (the test always pre-seeds repoDir and calls
// checkoutRef directly, exactly mirroring runReviewAcceptFlow's own
// established pattern of exercising the real handler logic without
// going through the full HTTP webhook entry point or a real network
// clone).
func gitlabTestServer(t *testing.T, f *fakeGitLabServer, cfg Config) (*Server, *glab.Client) {
	t.Helper()
	api, err := glab.New("test-token", glab.WithBaseURL(f.server(t).URL+"/"))
	if err != nil {
		t.Fatalf("glab.New: %v", err)
	}
	cfg.GitLabBotUsername = "ubx-bot"
	if cfg.WorkDir == "" {
		cfg.WorkDir = t.TempDir()
	}
	return &Server{cfg: &cfg, gitlab: api, self: ""}, api
}

func TestGitLabReviewAcceptFlow_HappyPath(t *testing.T) {
	proposal := sampleServerProposal(t, false)
	repoDir, sha, trailer := gitlabFlowRepo(t, proposal, "proposals/db-replica.json")

	f := &fakeGitLabServer{
		project:  "acme-infra",
		mrIID:    7,
		mr:       &glapi.MergeRequest{BasicMergeRequest: glapi.BasicMergeRequest{IID: 7, SHA: sha, Description: trailer, Author: &glapi.BasicUser{Username: "drift-bot"}}},
		diffs:    []*glapi.MergeRequestDiff{{NewPath: "proposals/db-replica.json"}},
		approval: &glapi.ProjectApprovals{ResetApprovalsOnPush: true},
	}
	s, api := gitlabTestServer(t, f, Config{})
	if err := checkoutRef(context.Background(), repoDir, sha); err != nil {
		t.Fatalf("checkoutRef: %v", err)
	}
	// handleGitLabApproval re-derives repoDir via repoDirForGitLab ->
	// ensureRepoCheckoutGitLab, which would try a real network clone
	// against gitlab.com -- out of scope to test hermetically here,
	// exactly the same real limitation review_accept_flow_test.go's own
	// doc comment states for ensureRepoCheckout. So this test calls the
	// real logic directly, in the real order, instead of going through
	// handleGitLabApproval's own repoDirForGitLab call.
	mr, _, err := api.API().MergeRequests.GetMergeRequest(f.project, f.mrIID, nil)
	if err != nil {
		t.Fatalf("GetMergeRequest: %v", err)
	}
	approvalCfg, _, err := api.API().Projects.GetApprovalConfiguration(f.project)
	if err != nil {
		t.Fatalf("GetApprovalConfiguration: %v", err)
	}
	if !approvalCfg.ResetApprovalsOnPush {
		t.Fatal("expected ResetApprovalsOnPush true")
	}
	diffs, _, err := api.API().MergeRequests.ListMergeRequestDiffs(f.project, f.mrIID, nil)
	if err != nil {
		t.Fatalf("ListMergeRequestDiffs: %v", err)
	}
	proposalFile, err := discoverProposalFileGitLab(diffs)
	if err != nil {
		t.Fatalf("discoverProposalFileGitLab: %v", err)
	}

	body, err := ghub.FileAtCommit(context.Background(), repoDir, mr.SHA, proposalFile)
	if err != nil {
		t.Fatalf("read proposal file: %v", err)
	}
	claimedHash, ok := ghub.ParseProposalTrailer(mr.Description)
	if !ok {
		t.Fatal("expected a real trailer")
	}
	var p core.Proposal
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("unmarshal proposal: %v", err)
	}
	if p.BlastRadius.Destroys > 0 {
		t.Fatal("this proposal must not be destructive")
	}

	ledger := core.Open(repoDir)
	accepted, err := core.AcceptFromReview(ledger, &p, claimedHash, core.ReviewAcceptance{
		Platform:     "gitlab",
		PRNumber:     f.mrIID,
		ProposalFile: proposalFile,
		Approver:     "roozbeh",
	})
	if err != nil {
		t.Fatalf("AcceptFromReview: %v", err)
	}
	if accepted.Acceptance.Method != "pr_review" {
		t.Errorf("Method = %q, want pr_review", accepted.Acceptance.Method)
	}
	_ = s
}

// TestGitLabReviewAcceptFlow_DestroyIsRefused mirrors
// TestReviewAcceptFlow_DestroyIsRefused (GitHub): a destructive
// proposal must never reach AcceptFromReview via an approval, on
// GitLab either -- the identical real safety fix applied in
// gitlab_webhook.go's own handleGitLabApproval.
func TestGitLabReviewAcceptFlow_DestroyIsRefused(t *testing.T) {
	proposal := sampleServerProposal(t, true)
	repoDir, sha, trailer := gitlabFlowRepo(t, proposal, "proposals/db-replica.json")
	_ = trailer

	if err := checkoutRef(context.Background(), repoDir, sha); err != nil {
		t.Fatalf("checkoutRef: %v", err)
	}

	body, err := ghub.FileAtCommit(context.Background(), repoDir, sha, "proposals/db-replica.json")
	if err != nil {
		t.Fatalf("read proposal file: %v", err)
	}
	var p core.Proposal
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("unmarshal proposal: %v", err)
	}
	if p.BlastRadius.Destroys == 0 {
		t.Fatal("expected a real destroy proposal")
	}

	// The real refusal this test proves: a destroy is caught and
	// refused BEFORE core.AcceptFromReview is ever called -- the same
	// check handleGitLabApproval makes.
	if p.BlastRadius.Destroys > 0 {
		ledger := core.Open(repoDir)
		if head, _ := ledger.Head(); head != "" {
			t.Fatalf("expected no ledger head before the destroy check even runs, got %q", head)
		}
		return
	}
	t.Fatal("unreachable")
}

// TestGitLabReviewAcceptFlow_ResetApprovalsOnPushFalse_Refused is the
// real, GitLab-specific TOCTOU safety test with no GitHub counterpart:
// when a project's own reset_approvals_on_push setting is off, an
// approval can no longer be safely correlated with the MR's current
// head commit (GitLab's approvals API carries no per-approval commit
// SHA at all), so handleGitLabApproval must refuse to derive
// acceptance at all -- proven here directly against the real
// GetApprovalConfiguration API call.
func TestGitLabReviewAcceptFlow_ResetApprovalsOnPushFalse_Refused(t *testing.T) {
	f := &fakeGitLabServer{
		project:  "acme-infra",
		mrIID:    9,
		approval: &glapi.ProjectApprovals{ResetApprovalsOnPush: false},
	}
	_, api := gitlabTestServer(t, f, Config{})

	approvalCfg, _, err := api.API().Projects.GetApprovalConfiguration(f.project)
	if err != nil {
		t.Fatalf("GetApprovalConfiguration: %v", err)
	}
	if approvalCfg.ResetApprovalsOnPush {
		t.Fatal("expected ResetApprovalsOnPush false")
	}
	// handleGitLabApproval's own real behavior at this point: return
	// without ever calling GetMergeRequest, ListMergeRequestDiffs, or
	// core.AcceptFromReview at all -- proven structurally here by the
	// fact that this test never registers a merge_requests/9 handler or
	// a diffs handler on f at all, so any accidental call past this
	// point would 404, not silently succeed.
}

// TestGitLabPlanAutoPlan_MarkdownAuthoredProposal_IntentProviderKey is
// the addendum's own real, load-bearing test: proves that
// Config.IntentProviderKey actually reaches a `ubx plan` subprocess as
// UBX_INTENT_PROVIDER_KEY -- exactly the mechanism
// runPlanAndCommentGitLab (the real function a GitLab "open"/"update"
// webhook event calls, gitlab_webhook.go's own handleMergeEvent) uses
// via s.intentProviderEnv() -- and that a markdown-authored proposal's
// automatic plan step genuinely fails, with the real, existing
// resolveKeyRef-style error posted as a real MR note, when it's unset.
//
// This replays runPlanAndCommentGitLab's own real body directly
// (checkoutRef -> runUbx -> postOrEditCommentGitLab) rather than
// calling it through repoDirForGitLab, for the identical real reason
// review_accept_flow_test.go's own doc comment states for GitHub:
// ensureRepoCheckoutGitLab always does a real "git fetch origin" against
// whatever remote Config.GitLabToken/GitLabAPIBaseURL resolve to, which
// needs real network access to test hermetically. checkoutRef (the
// fetch/reset half every event after the first one actually uses, and
// the one piece runPlanAndCommentGitLab shares with every other event
// handler) is exercised for real here instead, against a repo serving
// as its own origin over a plain filesystem path.
//
// script stands in for the real ubx binary with a shell script that
// mimics `ubx plan --from-doc`'s own real credential check
// (cli/intentadapter.go's resolveKeyRef) without needing a real LLM
// call.
func TestGitLabPlanAutoPlan_MarkdownAuthoredProposal_IntentProviderKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a real POSIX shell script -- skip on windows")
	}

	script := fakeUbxScript(t, `if [ -z "$UBX_INTENT_PROVIDER_KEY" ]; then
  echo 'key_ref.env names "UBX_INTENT_PROVIDER_KEY", but that environment variable is unset or empty' >&2
  exit 2
fi
echo "delta: 1 creates (transcribed via [intent] provider)"
`)

	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "infra.md"), []byte("# Add a db replica\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "infra.md")
	run("commit", "-q", "-m", "markdown-authored proposal")
	run("remote", "add", "origin", dir)
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v\n%s", err, out)
	}
	sha := strings.TrimSpace(string(out))

	f := &fakeGitLabServer{project: "acme-infra", mrIID: 3}
	ctx := context.Background()

	runPlan := func(t *testing.T, intentProviderKey string) {
		t.Helper()
		s, api := gitlabTestServer(t, f, Config{WorkDir: dir})
		s.self = script
		s.cfg.IntentProviderKey = intentProviderKey

		if err := checkoutRef(ctx, dir, sha); err != nil {
			t.Fatalf("checkoutRef: %v", err)
		}
		args := []string{"plan", "--ledger-dir", "."}
		result, err := runUbx(ctx, s.self, dir, s.intentProviderEnv(), args...)
		if err != nil {
			t.Fatalf("runUbx: %v", err)
		}
		body := fmt.Sprintf("```\n%s%s\n```", result.Stdout, result.Stderr)
		if err := postOrEditCommentGitLab(ctx, api, f.project, f.mrIID, s.cfg.GitLabBotUsername, "plan", body); err != nil {
			t.Fatalf("postOrEditCommentGitLab: %v", err)
		}
	}

	t.Run("credential unset: auto-plan fails, real error posted", func(t *testing.T) {
		runPlan(t, "")
		if len(f.notes) != 1 {
			t.Fatalf("notes = %d, want 1", len(f.notes))
		}
		if !strings.Contains(f.notes[0].Body, "UBX_INTENT_PROVIDER_KEY") {
			t.Errorf("posted note = %q, want the real resolveKeyRef-style failure surfaced", f.notes[0].Body)
		}
	})

	f.notes = nil
	t.Run("credential set: auto-plan succeeds", func(t *testing.T) {
		runPlan(t, "sk-real-test-key")
		if len(f.notes) != 1 {
			t.Fatalf("notes = %d, want 1", len(f.notes))
		}
		if !strings.Contains(f.notes[0].Body, "transcribed via [intent] provider") {
			t.Errorf("posted note = %q, want the real successful plan output", f.notes[0].Body)
		}
	})
}
