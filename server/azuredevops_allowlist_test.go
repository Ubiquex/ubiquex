package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// azureDevOpsPRPayload builds a minimal, real webhook "resource" JSON
// payload -- prIdentityFromAzureDevOps's own real, required shape
// (pullRequestId, repository.id/repository.project.name,
// lastMergeSourceCommit.commitId, targetRefName), the same fields
// every real Azure DevOps PR event payload carries.
func azureDevOpsPRPayload(t *testing.T, prID int, project, repositoryID, headSHA, targetRef string) json.RawMessage {
	t.Helper()
	payload := map[string]any{
		"pullRequestId": prID,
		"repository": map[string]any{
			"id":      repositoryID,
			"project": map[string]any{"name": project},
		},
		"lastMergeSourceCommit": map[string]any{"commitId": headSHA},
		"targetRefName":         targetRef,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// TestHandlePullRequestCreatedAzureDevOps_UnlistedRepoRefusedAndLogged
// proves UBI-166's own real allowlist enforcement for Azure DevOps: a
// real "git.pullrequest.created" event for a repository with zero
// Config.Repos entries is refused outright, with a real, structured
// log line an operator can find -- s.azuredevops is deliberately left
// nil (azureDevOpsClient() never errors, so this also proves the
// allowlist gate runs before any real API call would even be
// attempted).
func TestHandlePullRequestCreatedAzureDevOps_UnlistedRepoRefusedAndLogged(t *testing.T) {
	buf := captureSlog(t)
	s := &Server{cfg: &Config{}}

	resource := azureDevOpsPRPayload(t, 42, "acme-infra", "11111111-1111-1111-1111-111111111111", "abc123", "refs/heads/main")
	if err := s.handlePullRequestCreatedAzureDevOps(context.Background(), resource); err != nil {
		t.Fatalf("handlePullRequestCreatedAzureDevOps: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "allowlist") || !strings.Contains(out, "acme-infra/11111111-1111-1111-1111-111111111111") {
		t.Errorf("log output = %q, want a real, loud refusal naming the repo and the allowlist", out)
	}
}

func TestHandlePullRequestUpdatedAzureDevOps_UnlistedRepoRefusedAndLogged(t *testing.T) {
	buf := captureSlog(t)
	s := &Server{cfg: &Config{}}

	resource := azureDevOpsPRPayload(t, 42, "acme-infra", "11111111-1111-1111-1111-111111111111", "abc123", "refs/heads/main")
	if err := s.handlePullRequestUpdatedAzureDevOps(context.Background(), resource, "push"); err != nil {
		t.Fatalf("handlePullRequestUpdatedAzureDevOps: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "allowlist") || !strings.Contains(out, "acme-infra/11111111-1111-1111-1111-111111111111") {
		t.Errorf("log output = %q, want a real, loud refusal naming the repo and the allowlist", out)
	}
}

func TestHandlePullRequestMergedAzureDevOps_UnlistedRepoRefusedAndLogged(t *testing.T) {
	buf := captureSlog(t)
	s := &Server{cfg: &Config{ShipOnMerge: true}}

	payload := map[string]any{
		"pullRequestId": 42,
		"repository": map[string]any{
			"id":      "11111111-1111-1111-1111-111111111111",
			"project": map[string]any{"name": "acme-infra"},
		},
		"targetRefName": "refs/heads/main",
		"mergeStatus":   "succeeded",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.handlePullRequestMergedAzureDevOps(context.Background(), body); err != nil {
		t.Fatalf("handlePullRequestMergedAzureDevOps: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "allowlist") || !strings.Contains(out, "acme-infra/11111111-1111-1111-1111-111111111111") {
		t.Errorf("log output = %q, want a real, loud refusal naming the repo and the allowlist", out)
	}
}

// TestResolveLedgerDir_AzureDevOpsTwoRealStacksIndependently proves the
// shared resolution core, applied against Azure DevOps' own real
// changed-file path shape (ListPullRequestChangedFiles's own real
// return type, already flat repo-relative paths), resolves two
// independently configured stacks correctly.
func TestResolveLedgerDir_AzureDevOpsTwoRealStacksIndependently(t *testing.T) {
	candidates := []RepoConfig{
		{Platform: "azuredevops", Project: "acme-infra", Repository: "11111111-1111-1111-1111-111111111111", LedgerDir: "stacks/payments"},
		{Platform: "azuredevops", Project: "acme-infra", Repository: "11111111-1111-1111-1111-111111111111", LedgerDir: "stacks/networking"},
	}
	dir, err := resolveLedgerDir(candidates, []string{"/stacks/payments/proposals/db-replica.json"})
	if err != nil {
		t.Fatalf("resolveLedgerDir: %v", err)
	}
	if dir != "stacks/payments" {
		t.Errorf("dir = %q, want %q -- Azure DevOps' own real changed-file paths carry a leading slash, which must not break resolution", dir, "stacks/payments")
	}
}
