package bitbucketcloud

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ubiquex/ubiquex/github"
)

// testRepo creates a real, throwaway git repository with one commit
// adding path with the given content, returning the repo dir and the
// commit SHA. Real `git` commands throughout, the same convention every
// other platform package's own derive test already uses.
func testRepo(t *testing.T, path, content string) (repoDir, sha string) {
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

	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "add "+path)

	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return dir, string(out[:len(out)-1])
}

// TestDeriveAcceptance_RealTwoCallFlow drives the whole derivation
// against a real fixture server and a real git repository: the commit
// resolves to a pull request id via Bitbucket Cloud's own dedicated
// endpoint, the full representation is fetched separately for the
// description and participants, the trailer is parsed, and the
// approvers come back as account_ids.
func TestDeriveAcceptance_RealTwoCallFlow(t *testing.T) {
	repoDir, sha := testRepo(t, "proposals/db.json", `{"id":"p1"}`)

	mux := http.NewServeMux()
	mux.HandleFunc("/repositories/acme/infra/commit/"+sha+"/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"repo_indexed":true,"values":[{"type":"pullrequest","id":900,"title":"db replica"}]}`)
	})
	mux.HandleFunc("/repositories/acme/infra/pullrequests/900", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":900,"description":"adds a replica\n\nubx-proposal: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","participants":[
			{"user":{"account_id":"acct-approver","nickname":"jdoe"},"approved":true,"state":"approved"},
			{"user":{"account_id":"acct-other","nickname":"rroe"},"approved":false,"state":null}
		]}`)
	})
	api := newTestClient(t, mux)

	got, err := DeriveAcceptance(context.Background(), api, repoDir, "acme", "infra", sha, "proposals/db.json")
	if err != nil {
		t.Fatalf("DeriveAcceptance: %v", err)
	}
	if got.ClaimedHash != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("ClaimedHash = %q, want the full 64-hex trailer hash", got.ClaimedHash)
	}
	if got.PullRequestID != 900 {
		t.Errorf("PullRequestID = %d, want 900", got.PullRequestID)
	}
	if string(got.ProposalFileContent) != `{"id":"p1"}` {
		t.Errorf("ProposalFileContent = %q", got.ProposalFileContent)
	}
	if len(got.Approvers) != 1 || got.Approvers[0] != "acct-approver" {
		t.Errorf("Approvers = %v, want [acct-approver] (account_ids, never labels)", got.Approvers)
	}
}

func TestDeriveAcceptance_UnknownCommitRefused(t *testing.T) {
	repoDir, _ := testRepo(t, "proposals/db.json", `{"id":"p1"}`)
	api := newTestClient(t, http.NewServeMux())

	_, err := DeriveAcceptance(context.Background(), api, repoDir, "acme", "infra",
		"0000000000000000000000000000000000000000", "proposals/db.json")
	if !errors.Is(err, github.ErrCommitNotFound) {
		t.Fatalf("err = %v, want ErrCommitNotFound", err)
	}
}

func TestDeriveAcceptance_NoTrailerRefused(t *testing.T) {
	repoDir, sha := testRepo(t, "proposals/db.json", `{"id":"p1"}`)

	mux := http.NewServeMux()
	mux.HandleFunc("/repositories/acme/infra/commit/"+sha+"/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"repo_indexed":true,"values":[{"id":900}]}`)
	})
	mux.HandleFunc("/repositories/acme/infra/pullrequests/900", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":900,"description":"no trailer here","participants":[]}`)
	})
	api := newTestClient(t, mux)

	_, err := DeriveAcceptance(context.Background(), api, repoDir, "acme", "infra", sha, "proposals/db.json")
	if !errors.Is(err, github.ErrNoProposalTrailer) {
		t.Fatalf("err = %v, want ErrNoProposalTrailer", err)
	}
}

// TestDeriveAcceptance_UnindexedRepoRefused proves the real
// Bitbucket-Cloud-specific indexing condition propagates all the way
// out of the derivation rather than being reported as "this commit has
// no pull request", which would silently skip a real acceptance.
func TestDeriveAcceptance_UnindexedRepoRefused(t *testing.T) {
	repoDir, sha := testRepo(t, "proposals/db.json", `{"id":"p1"}`)

	mux := http.NewServeMux()
	mux.HandleFunc("/repositories/acme/infra/commit/"+sha+"/pullrequests", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"repo_indexed":false,"values":[]}`)
	})
	api := newTestClient(t, mux)

	_, err := DeriveAcceptance(context.Background(), api, repoDir, "acme", "infra", sha, "proposals/db.json")
	if !errors.Is(err, ErrRepoNotIndexed) {
		t.Fatalf("err = %v, want ErrRepoNotIndexed", err)
	}
}
