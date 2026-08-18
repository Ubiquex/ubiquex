package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// bitbucketServerPRPayload builds a minimal, real webhook "pullRequest"
// JSON payload -- prIdentityFromBitbucketServer's own real, required
// shape (toRef.repository.project.key/toRef.repository.slug,
// fromRef.latestCommit, toRef.id), wrapped as the real top-level
// {"pullRequest": {...}} envelope every Bitbucket Server PR event
// payload carries.
func bitbucketServerPRPayload(t *testing.T, prID int, projectKey, repositorySlug, headSHA, toRef string) []byte {
	t.Helper()
	payload := map[string]any{
		"pullRequest": map[string]any{
			"id":      prID,
			"fromRef": map[string]any{"latestCommit": headSHA},
			"toRef": map[string]any{
				"id": toRef,
				"repository": map[string]any{
					"slug":    repositorySlug,
					"project": map[string]any{"key": projectKey},
				},
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// TestHandlePullRequestOpenedBitbucketServer_UnlistedRepoRefusedAndLogged
// proves UBI-166's own real allowlist enforcement for Bitbucket Server:
// a real "pr:opened" event for a repository with zero Config.Repos
// entries is refused outright, with a real, structured log line an
// operator can find -- s.bitbucketserver is deliberately left nil
// (bitbucketServerClient() never errors, so this also proves the
// allowlist gate runs before any real API call would even be
// attempted).
func TestHandlePullRequestOpenedBitbucketServer_UnlistedRepoRefusedAndLogged(t *testing.T) {
	buf := captureSlog(t)
	s := &Server{cfg: &Config{}}

	body := bitbucketServerPRPayload(t, 42, "INFRA", "infra", "abc123", "refs/heads/main")
	if err := s.handlePullRequestOpenedBitbucketServer(context.Background(), body); err != nil {
		t.Fatalf("handlePullRequestOpenedBitbucketServer: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "allowlist") || !strings.Contains(out, "INFRA/infra") {
		t.Errorf("log output = %q, want a real, loud refusal naming the repo and the allowlist", out)
	}
}

func TestHandlePullRequestMergedBitbucketServer_UnlistedRepoRefusedAndLogged(t *testing.T) {
	buf := captureSlog(t)
	s := &Server{cfg: &Config{ShipOnMerge: true}}

	body := bitbucketServerPRPayload(t, 42, "INFRA", "infra", "abc123", "refs/heads/main")
	if err := s.handlePullRequestMergedBitbucketServer(context.Background(), body); err != nil {
		t.Fatalf("handlePullRequestMergedBitbucketServer: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "allowlist") || !strings.Contains(out, "INFRA/infra") {
		t.Errorf("log output = %q, want a real, loud refusal naming the repo and the allowlist", out)
	}
}

func TestHandlePullRequestApprovedBitbucketServer_UnlistedRepoRefusedAndLogged(t *testing.T) {
	buf := captureSlog(t)
	s := &Server{cfg: &Config{}}

	payload := map[string]any{
		"pullRequest": map[string]any{
			"id":      42,
			"fromRef": map[string]any{"latestCommit": "abc123"},
			"toRef": map[string]any{
				"id": "refs/heads/main",
				"repository": map[string]any{
					"slug":    "infra",
					"project": map[string]any{"key": "INFRA"},
				},
			},
		},
		"participant": map[string]any{
			"user":               map[string]any{"name": "roozbeh"},
			"lastReviewedCommit": "abc123",
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.handlePullRequestApprovedBitbucketServer(context.Background(), body); err != nil {
		t.Fatalf("handlePullRequestApprovedBitbucketServer: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "allowlist") || !strings.Contains(out, "INFRA/infra") {
		t.Errorf("log output = %q, want a real, loud refusal naming the repo and the allowlist", out)
	}
}

// TestResolveStackIn_BitbucketServerTwoRealStacksIndependently proves
// the shared resolution core, applied against Bitbucket Server's own
// real changed-file path shape (ListPullRequestChangedFiles's own
// real return type, path.toString-derived), resolves two real,
// auto-discovered stacks correctly.
func TestResolveStackIn_BitbucketServerTwoRealStacksIndependently(t *testing.T) {
	repoDir := t.TempDir()
	writeStack(t, repoDir, "stacks/payments", "config")
	writeStack(t, repoDir, "stacks/networking", "config")

	dir, err := resolveStackIn(repoDir, func() ([]string, error) {
		return []string{"stacks/networking/proposals/vpc.json"}, nil
	})
	if err != nil {
		t.Fatalf("resolveStackIn: %v", err)
	}
	if dir != "stacks/networking" {
		t.Errorf("dir = %q, want %q", dir, "stacks/networking")
	}
}
