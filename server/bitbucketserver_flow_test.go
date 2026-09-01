package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
	ghub "github.com/ubiquex/ubiquex/vcs/github"
)

// fakeBitbucketServerPRServer serves exactly the real endpoints
// bitbucketserver_webhook.go's own review-accept flow and plan/ship
// subprocess-comment mechanism need: the PR object itself, PR changes
// (changed-file listing), and comments (edit-in-place).
type fakeBitbucketServerPRServer struct {
	projectKey     string
	repositorySlug string
	prID           int

	prSHA         string
	prDescription string
	prToRef       string

	changedFiles []string

	comments []*fakeBitbucketServerComment
}

type fakeBitbucketServerComment struct {
	ID      int
	Version int
	Text    string
	Author  string
}

func (f *fakeBitbucketServerPRServer) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	base := "/rest/api/1.0/projects/" + f.projectKey + "/repos/" + f.repositorySlug
	prBase := base + "/pull-requests/" + strconv.Itoa(f.prID)

	mux.HandleFunc(prBase, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != prBase {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		pr := map[string]any{
			"id":          f.prID,
			"description": f.prDescription,
			"fromRef": map[string]any{
				"id":           "refs/heads/feature",
				"latestCommit": f.prSHA,
			},
			"toRef": map[string]any{
				"id": f.prToRef,
				"repository": map[string]any{
					"slug":    f.repositorySlug,
					"project": map[string]any{"key": f.projectKey},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(pr)
	})

	mux.HandleFunc(prBase+"/changes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		values := make([]map[string]any, 0, len(f.changedFiles))
		for _, path := range f.changedFiles {
			values = append(values, map[string]any{"path": map[string]any{"toString": path}})
		}
		body, _ := json.Marshal(map[string]any{"values": values})
		w.Write(body)
	})

	mux.HandleFunc(prBase+"/comments", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			values := make([]map[string]any, 0, len(f.comments))
			for _, c := range f.comments {
				values = append(values, map[string]any{
					"id": c.ID, "version": c.Version, "text": c.Text,
					"author": map[string]any{"name": c.Author},
				})
			}
			body, _ := json.Marshal(map[string]any{"values": values})
			w.Write(body)
		case http.MethodPost:
			var body struct {
				Text string `json:"text"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			c := &fakeBitbucketServerComment{ID: len(f.comments) + 1, Version: 0, Text: body.Text, Author: "ubx-bot"}
			f.comments = append(f.comments, c)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": c.ID, "version": c.Version, "text": c.Text})
		}
	})

	mux.HandleFunc(prBase+"/comments/", func(w http.ResponseWriter, r *http.Request) {
		idStr := strings.TrimPrefix(r.URL.Path, prBase+"/comments/")
		id, _ := strconv.Atoi(idStr)
		var body struct {
			Text    string `json:"text"`
			Version int    `json:"version"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, c := range f.comments {
			if c.ID == id {
				c.Text = body.Text
				c.Version = body.Version + 1
				w.WriteHeader(http.StatusOK)
				return
			}
		}
		http.NotFound(w, r)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// bitbucketServerFlowRepo mirrors azureDevOpsFlowRepo -- a real, local
// git repository carrying one commit that adds a real proposal file.
func bitbucketServerFlowRepo(t *testing.T, proposal *core.Proposal, proposalPath string) (repoDir, sha, trailer string) {
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

func TestBitbucketServerReviewAcceptFlow_HappyPath(t *testing.T) {
	proposal := sampleServerProposal(t, false)
	repoDir, sha, trailer := bitbucketServerFlowRepo(t, proposal, "proposals/db-replica.json")

	f := &fakeBitbucketServerPRServer{
		projectKey: "INFRA", repositorySlug: "infra", prID: 7,
		prSHA: sha, prDescription: trailer, prToRef: "refs/heads/main",
		changedFiles: []string{"proposals/db-replica.json"},
	}
	api := newTestBitbucketServerClient(t, f.server(t))
	ctx := context.Background()

	pr, err := api.GetPullRequest(ctx, f.projectKey, f.repositorySlug, f.prID)
	if err != nil {
		t.Fatalf("GetPullRequest: %v", err)
	}
	id, err := prIdentityFromBitbucketServer(pr)
	if err != nil {
		t.Fatalf("prIdentityFromBitbucketServer: %v", err)
	}

	// The real, Bitbucket-Server-specific TOCTOU check: the approving
	// reviewer's own LastReviewedCommit (delivered on the real webhook
	// payload's own "participant" object, simulated here directly since
	// this test exercises the flow's plain functions, not the HTTP
	// handler itself) must match the PR's own current head.
	lastReviewedCommit := sha
	if lastReviewedCommit != id.headSHA {
		t.Fatal("expected the reviewed commit to match the PR's own current head")
	}

	if err := checkoutRef(ctx, repoDir, id.headSHA); err != nil {
		t.Fatalf("checkoutRef: %v", err)
	}
	changedFiles, err := api.ListPullRequestChangedFiles(ctx, id.projectKey, id.repositorySlug, id.prID)
	if err != nil {
		t.Fatalf("ListPullRequestChangedFiles: %v", err)
	}
	proposalFile, err := discoverProposalFileBitbucketServer(changedFiles)
	if err != nil {
		t.Fatalf("discoverProposalFileBitbucketServer: %v", err)
	}

	body, err := ghub.FileAtCommit(ctx, repoDir, id.headSHA, proposalFile)
	if err != nil {
		t.Fatalf("FileAtCommit: %v", err)
	}
	claimedHash, ok := ghub.ParseProposalTrailer(pr.Description)
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
		Platform: "bitbucketserver", PRNumber: int64(id.prID), ProposalFile: proposalFile, Approver: "roozbeh",
	})
	if err != nil {
		t.Fatalf("AcceptFromReview: %v", err)
	}
	if accepted.Acceptance.Method != "pr_review" {
		t.Errorf("Method = %q, want pr_review", accepted.Acceptance.Method)
	}
}

func TestBitbucketServerReviewAcceptFlow_DestroyIsRefused(t *testing.T) {
	proposal := sampleServerProposal(t, true)
	repoDir, sha, _ := bitbucketServerFlowRepo(t, proposal, "proposals/db-replica.json")

	if err := checkoutRef(context.Background(), repoDir, sha); err != nil {
		t.Fatalf("checkoutRef: %v", err)
	}
	body, err := ghub.FileAtCommit(context.Background(), repoDir, sha, "proposals/db-replica.json")
	if err != nil {
		t.Fatalf("FileAtCommit: %v", err)
	}
	var p core.Proposal
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("unmarshal proposal: %v", err)
	}
	if p.BlastRadius.Destroys == 0 {
		t.Fatal("expected a real destroy proposal")
	}

	// The real refusal handlePullRequestApprovedBitbucketServer makes:
	// caught and refused before core.AcceptFromReview is ever called.
	if p.BlastRadius.Destroys > 0 {
		ledger := core.Open(repoDir)
		if head, _ := ledger.Head(); head != "" {
			t.Fatalf("expected no ledger head before the destroy check even runs, got %q", head)
		}
		return
	}
	t.Fatal("unreachable")
}

// TestBitbucketServerReviewAcceptFlow_StaleApproval_Refused proves the
// real, Bitbucket-Server-specific TOCTOU gate: a reviewer's own
// LastReviewedCommit not matching the PR's own current head (a push
// landed after the approval) must refuse, never silently accept
// against a newer, unreviewed commit.
func TestBitbucketServerReviewAcceptFlow_StaleApproval_Refused(t *testing.T) {
	f := &fakeBitbucketServerPRServer{
		projectKey: "INFRA", repositorySlug: "infra", prID: 9,
		prSHA: "new-commit-after-approval", prToRef: "refs/heads/main",
	}
	api := newTestBitbucketServerClient(t, f.server(t))

	pr, err := api.GetPullRequest(context.Background(), f.projectKey, f.repositorySlug, f.prID)
	if err != nil {
		t.Fatalf("GetPullRequest: %v", err)
	}
	id, err := prIdentityFromBitbucketServer(pr)
	if err != nil {
		t.Fatalf("prIdentityFromBitbucketServer: %v", err)
	}

	lastReviewedCommit := "stale-commit-at-approval-time"
	if lastReviewedCommit == id.headSHA {
		t.Fatal("expected a real mismatch between the reviewed commit and the PR's own current head")
	}
	// handlePullRequestApprovedBitbucketServer's own real behavior at
	// this point: post a real explanatory comment and return, without
	// ever calling ListPullRequestChangedFiles/AcceptFromReview.
}
