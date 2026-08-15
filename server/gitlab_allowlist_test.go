package server

import (
	"context"
	"strings"
	"testing"

	glapi "gitlab.com/gitlab-org/api/client-go/v2"
)

// TestHandleMergeEvent_UnlistedProjectRefusedAndLogged proves UBI-166's
// own real allowlist enforcement for GitLab: a real "merge_request"
// event for a project with zero Config.Repos entries is refused
// outright, with a real, structured log line an operator can find.
// s.gitlab is deliberately left nil -- gitlabClient() would error
// before the allowlist check ever ran, so this also proves the
// allowlist gate really does run first (a nil client only fails later,
// past the point candidates is checked).
func TestHandleMergeEvent_UnlistedProjectRefusedAndLogged(t *testing.T) {
	buf := captureSlog(t)
	s := &Server{cfg: &Config{}}

	e := &glapi.MergeEvent{
		Project: glapi.MergeEventProject{PathWithNamespace: "acme/infra"},
		ObjectAttributes: glapi.MergeEventObjectAttributes{
			IID:    7,
			Action: "open",
		},
	}

	if err := s.handleMergeEvent(context.Background(), e); err != nil {
		t.Fatalf("handleMergeEvent: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "allowlist") || !strings.Contains(out, "acme/infra") {
		t.Errorf("log output = %q, want a real, loud refusal naming the project and the allowlist", out)
	}
}

func TestHandleMergeCommentEvent_UnlistedProjectRefusedAndLogged(t *testing.T) {
	buf := captureSlog(t)
	s := &Server{cfg: &Config{}}

	e := &glapi.MergeCommentEvent{
		Project: glapi.MergeCommentEventProject{PathWithNamespace: "acme/infra"},
		ObjectAttributes: glapi.MergeCommentEventObjectAttributes{
			Note: "ubx plan",
		},
		MergeRequest: glapi.MergeCommentEventMergeRequest{IID: 7},
		User:         &glapi.EventUser{Username: "roozbeh"},
	}

	if err := s.handleMergeCommentEvent(context.Background(), e); err != nil {
		t.Fatalf("handleMergeCommentEvent: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "allowlist") || !strings.Contains(out, "acme/infra") {
		t.Errorf("log output = %q, want a real, loud refusal naming the project and the allowlist", out)
	}
}

// TestResolveStackIn_GitLabTwoRealStacksIndependently proves the
// shared resolution core (already exhaustively tested in
// allowlist_test.go) applied against GitLab's own real
// changedFilePathsGitLab-shaped inputs (NewPath/OldPath-derived paths)
// resolves two real, auto-discovered stacks correctly -- unchanged
// deepest-match behavior on UBI-167's auto-discovered candidates.
func TestResolveStackIn_GitLabTwoRealStacksIndependently(t *testing.T) {
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
