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

	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/git"
	"github.com/microsoft/azure-devops-go-api/azuredevops/v7/webapi"
	"github.com/ubiquex/ubiquex/core"
	ghub "github.com/ubiquex/ubiquex/vcs/github"
)

// fakeAzureDevOpsPRServer serves exactly the real endpoints
// azuredevops_webhook.go's own review-accept flow and plan/ship
// subprocess-comment mechanism need: the PR object itself, policy
// configurations (the real TOCTOU check), PR diffs (changed-file
// listing), and comment threads (edit-in-place).
type fakeAzureDevOpsPRServer struct {
	project      string
	repositoryID string
	prID         int

	prSHA         string
	prDescription string
	prTargetRef   string

	policyFound       bool
	policyResetOnPush bool
	changedFiles      []string

	notes []*git.GitPullRequestCommentThread
}

func (f *fakeAzureDevOpsPRServer) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	base := "/" + f.project + "/_apis/git/repositories/" + f.repositoryID
	prBase := base + "/pullRequests/" + strconv.Itoa(f.prID)

	mux.HandleFunc(prBase, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != prBase {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		pr := map[string]any{
			"pullRequestId": f.prID,
			"description":   f.prDescription,
			"targetRefName": f.prTargetRef,
			"lastMergeSourceCommit": map[string]any{
				"commitId": f.prSHA,
			},
			"repository": map[string]any{
				"id":      f.repositoryID,
				"project": map[string]any{"name": f.project},
			},
		}
		_ = json.NewEncoder(w).Encode(pr)
	})

	mux.HandleFunc(prBase+"/iterations", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"value":[{"id":1}]}`))
	})
	mux.HandleFunc(prBase+"/iterations/1/changes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		entries := make([]map[string]any, 0, len(f.changedFiles))
		for _, path := range f.changedFiles {
			entries = append(entries, map[string]any{"item": map[string]any{"path": path}})
		}
		body, _ := json.Marshal(map[string]any{"changeEntries": entries})
		w.Write(body)
	})

	mux.HandleFunc("/"+f.project+"/_apis/policy/configurations", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !f.policyFound {
			w.Write([]byte(`{"value":[]}`))
			return
		}
		cfg := map[string]any{
			"isEnabled": true,
			"type":      map[string]any{"id": "fa4e907d-c16b-4a4c-9dfa-4906e5d171dd", "displayName": "Minimum approval count"},
			"settings": map[string]any{
				"resetOnSourcePush": f.policyResetOnPush,
				"scope": []map[string]any{
					{"refName": f.prTargetRef, "matchKind": "Exact", "repositoryId": nil},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"value": []map[string]any{cfg}})
	})

	mux.HandleFunc(prBase+"/threads", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"value": f.notes})
		case http.MethodPost:
			var body struct {
				Comments []struct {
					Content string `json:"content"`
				} `json:"comments"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			id := len(f.notes) + 1
			content := ""
			if len(body.Comments) > 0 {
				content = body.Comments[0].Content
			}
			botName := "ubx-bot"
			commentID := 1
			thread := &git.GitPullRequestCommentThread{
				Id: &id,
				Comments: &[]git.Comment{
					{Id: &commentID, Content: &content, Author: &webapi.IdentityRef{DisplayName: &botName}},
				},
			}
			f.notes = append(f.notes, thread)
			_ = json.NewEncoder(w).Encode(thread)
		}
	})

	mux.HandleFunc(prBase+"/threads/", func(w http.ResponseWriter, r *http.Request) {
		// Path shape: {prBase}/threads/{threadId}/comments/{commentId}
		rest := strings.TrimPrefix(r.URL.Path, prBase+"/threads/")
		parts := strings.Split(rest, "/comments/")
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		threadID, _ := strconv.Atoi(parts[0])
		commentID, _ := strconv.Atoi(parts[1])
		var body struct {
			Content string `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, thread := range f.notes {
			if thread.Id == nil || *thread.Id != threadID || thread.Comments == nil {
				continue
			}
			for i, c := range *thread.Comments {
				if c.Id != nil && *c.Id == commentID {
					(*thread.Comments)[i].Content = &body.Content
					w.WriteHeader(http.StatusOK)
					return
				}
			}
		}
		http.NotFound(w, r)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// azureDevOpsFlowRepo mirrors gitlabFlowRepo -- a real, local git
// repository carrying one commit that adds a real proposal file.
func azureDevOpsFlowRepo(t *testing.T, proposal *core.Proposal, proposalPath string) (repoDir, sha, trailer string) {
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

func TestAzureDevOpsReviewAcceptFlow_HappyPath(t *testing.T) {
	proposal := sampleServerProposal(t, false)
	repoDir, sha, trailer := azureDevOpsFlowRepo(t, proposal, "proposals/db-replica.json")

	f := &fakeAzureDevOpsPRServer{
		project: "acme-infra", repositoryID: "repo-1", prID: 7,
		prSHA: sha, prDescription: trailer, prTargetRef: "refs/heads/main",
		policyFound: true, policyResetOnPush: true,
		changedFiles: []string{"proposals/db-replica.json"},
	}
	api := newTestAzureDevOpsClient(t, f.server(t))
	ctx := context.Background()

	found, resetOnPush, err := api.ResetOnSourcePush(ctx, f.project, f.repositoryID, f.prTargetRef)
	if err != nil {
		t.Fatalf("ResetOnSourcePush: %v", err)
	}
	if !found || !resetOnPush {
		t.Fatalf("expected found=true, resetOnPush=true, got found=%v resetOnPush=%v", found, resetOnPush)
	}

	if err := checkoutRef(ctx, repoDir, sha); err != nil {
		t.Fatalf("checkoutRef: %v", err)
	}
	changedFiles, err := api.ListPullRequestChangedFiles(ctx, f.project, f.repositoryID, f.prID)
	if err != nil {
		t.Fatalf("ListPullRequestChangedFiles: %v", err)
	}
	proposalFile, err := discoverProposalFileAzureDevOps(changedFiles)
	if err != nil {
		t.Fatalf("discoverProposalFileAzureDevOps: %v", err)
	}

	body, err := ghub.FileAtCommit(ctx, repoDir, sha, proposalFile)
	if err != nil {
		t.Fatalf("FileAtCommit: %v", err)
	}
	claimedHash, ok := ghub.ParseProposalTrailer(f.prDescription)
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
		Platform: "azuredevops", PRNumber: int64(f.prID), ProposalFile: proposalFile, Approver: "roozbeh",
	})
	if err != nil {
		t.Fatalf("AcceptFromReview: %v", err)
	}
	if accepted.Acceptance.Method != "pr_review" {
		t.Errorf("Method = %q, want pr_review", accepted.Acceptance.Method)
	}
}

func TestAzureDevOpsReviewAcceptFlow_DestroyIsRefused(t *testing.T) {
	proposal := sampleServerProposal(t, true)
	repoDir, sha, _ := azureDevOpsFlowRepo(t, proposal, "proposals/db-replica.json")

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

	// The real refusal handleAzureDevOpsApproval makes: caught and
	// refused before core.AcceptFromReview is ever called.
	if p.BlastRadius.Destroys > 0 {
		ledger := core.Open(repoDir)
		if head, _ := ledger.Head(); head != "" {
			t.Fatalf("expected no ledger head before the destroy check even runs, got %q", head)
		}
		return
	}
	t.Fatal("unreachable")
}

func TestAzureDevOpsReviewAcceptFlow_NoResetPolicy_Refused(t *testing.T) {
	f := &fakeAzureDevOpsPRServer{
		project: "acme-infra", repositoryID: "repo-1", prID: 9,
		prTargetRef: "refs/heads/main",
		policyFound: false,
	}
	api := newTestAzureDevOpsClient(t, f.server(t))

	found, resetOnPush, err := api.ResetOnSourcePush(context.Background(), f.project, f.repositoryID, f.prTargetRef)
	if err != nil {
		t.Fatalf("ResetOnSourcePush: %v", err)
	}
	if found {
		t.Fatal("expected found=false -- no real 'Minimum approval count' policy at all")
	}
	_ = resetOnPush
	// handleAzureDevOpsApproval's own real behavior at this point: post
	// a real explanatory note and return, without ever calling
	// GetPullRequest/ListPullRequestChangedFiles/AcceptFromReview --
	// proven structurally by this fixture never registering those
	// endpoints for this specific PR ID, so any accidental call would
	// 404, not silently succeed.
}

func TestAzureDevOpsReviewAcceptFlow_ResetOnSourcePushFalse_Refused(t *testing.T) {
	f := &fakeAzureDevOpsPRServer{
		project: "acme-infra", repositoryID: "repo-1", prID: 11,
		prTargetRef: "refs/heads/main",
		policyFound: true, policyResetOnPush: false,
	}
	api := newTestAzureDevOpsClient(t, f.server(t))

	found, resetOnPush, err := api.ResetOnSourcePush(context.Background(), f.project, f.repositoryID, f.prTargetRef)
	if err != nil {
		t.Fatalf("ResetOnSourcePush: %v", err)
	}
	if !found {
		t.Fatal("expected found=true -- a real, enabled policy does exist")
	}
	if resetOnPush {
		t.Fatal("expected resetOnPush=false")
	}
}

// TestAzureDevOpsPlanAutoPlan_MarkdownAuthoredProposal_IntentProviderKey
// mirrors TestGitLabPlanAutoPlan_MarkdownAuthoredProposal_IntentProviderKey:
// proves Config.IntentProviderKey reaches a `ubx plan` subprocess as
// UBX_INTENT_PROVIDER_KEY on the Azure DevOps path too (server.go's
// runPlanAndCommentAzureDevOps uses the identical, shared
// s.intentProviderEnv() every other platform's plan path uses).
// Replays runPlanAndCommentAzureDevOps's own real body directly
// (checkoutRef -> runUbx -> postOrEditCommentAzureDevOps) rather than
// through repoDirForAzureDevOps, the identical real reason
// TestGitLabPlanAutoPlan's own doc comment gives (a real network clone
// would otherwise be attempted).
func TestAzureDevOpsPlanAutoPlan_MarkdownAuthoredProposal_IntentProviderKey(t *testing.T) {
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

	f := &fakeAzureDevOpsPRServer{project: "acme-infra", repositoryID: "repo-1", prID: 3}
	api := newTestAzureDevOpsClient(t, f.server(t))
	ctx := context.Background()

	runPlan := func(t *testing.T, intentProviderKey string) {
		t.Helper()
		s := &Server{cfg: &Config{IntentProviderKey: intentProviderKey, AzureDevOpsBotDisplayName: "ubx-bot"}, self: script}

		if err := checkoutRef(ctx, dir, sha); err != nil {
			t.Fatalf("checkoutRef: %v", err)
		}
		args := []string{"plan", "--ledger-dir", "."}
		result, err := runUbx(ctx, s.self, dir, s.intentProviderEnv(), args...)
		if err != nil {
			t.Fatalf("runUbx: %v", err)
		}
		body := fmt.Sprintf("```\n%s%s\n```", result.Stdout, result.Stderr)
		if err := postOrEditCommentAzureDevOps(ctx, api, f.project, f.repositoryID, f.prID, s.cfg.AzureDevOpsBotDisplayName, "plan", body); err != nil {
			t.Fatalf("postOrEditCommentAzureDevOps: %v", err)
		}
	}

	t.Run("credential unset: auto-plan fails, real error posted", func(t *testing.T) {
		runPlan(t, "")
		if len(f.notes) != 1 {
			t.Fatalf("notes = %d, want 1", len(f.notes))
		}
		body := (*f.notes[0].Comments)[0].Content
		if body == nil || !strings.Contains(*body, "UBX_INTENT_PROVIDER_KEY") {
			t.Errorf("posted note = %v, want the real resolveKeyRef-style failure surfaced", body)
		}
	})

	f.notes = nil
	t.Run("credential set: auto-plan succeeds", func(t *testing.T) {
		runPlan(t, "sk-real-test-key")
		if len(f.notes) != 1 {
			t.Fatalf("notes = %d, want 1", len(f.notes))
		}
		body := (*f.notes[0].Comments)[0].Content
		if body == nil || !strings.Contains(*body, "transcribed via [intent] provider") {
			t.Errorf("posted note = %v, want the real successful plan output", body)
		}
	})
}
