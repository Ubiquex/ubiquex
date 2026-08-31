package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	ghub "github.com/ubiquex/ubiquex/vcs/github"
)

// UBI-171's own real regression surface: before it, this package
// authenticated against whatever API base URL an operator configured and
// then cloned from the literal strings "github.com" and "dev.azure.com",
// unconditionally. A GitHub Enterprise Server or on-prem Azure DevOps
// Server deployment therefore could not work at all, however correctly
// it was configured.
//
// These tests exercise the real clone URLs and the real API paths,
// never a mock: the git cases run the actual `git` binary against an
// actual local repository reached through a real URL rewrite, so a
// regression to a hardcoded host fails by failing to clone, exactly as
// it would in production.

func TestGitHostForGitHub(t *testing.T) {
	tests := []struct {
		name       string
		apiBaseURL string
		want       string
		wantErr    bool
	}{
		{
			name:       "empty means the real SaaS git host",
			apiBaseURL: "",
			want:       "github.com",
		},
		{
			name:       "the SaaS API host maps to the SaaS git host",
			apiBaseURL: "https://api.github.com/",
			want:       "github.com",
		},
		{
			name:       "a GHES instance clones from the instance itself",
			apiBaseURL: "https://github.example.com",
			want:       "github.example.com",
		},
		{
			name:       "a GHES instance spelled with its API path still clones from the instance",
			apiBaseURL: "https://github.example.com/api/v3/",
			want:       "github.example.com",
		},
		{
			name:       "a GHES instance on a non-default port keeps the port",
			apiBaseURL: "https://github.example.com:8443",
			want:       "github.example.com:8443",
		},
		{
			name:       "a value naming no host is refused, never silently cloned from github.com",
			apiBaseURL: "github.example.com",
			wantErr:    true,
		},
		{
			name:       "a malformed URL is refused",
			apiBaseURL: "://nope",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := gitHostForGitHub(tt.apiBaseURL)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("gitHostForGitHub(%q) = %q, want an error", tt.apiBaseURL, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("gitHostForGitHub(%q): %v", tt.apiBaseURL, err)
			}
			if got != tt.want {
				t.Errorf("gitHostForGitHub(%q) = %q, want %q", tt.apiBaseURL, got, tt.want)
			}
		})
	}
}

func TestAzureDevOpsCloneURL(t *testing.T) {
	tests := []struct {
		name       string
		apiBaseURL string
		want       string
		wantErr    bool
	}{
		{
			name:       "empty means the real SaaS shape",
			apiBaseURL: "",
			want:       "https://oauth2:tok@dev.azure.com/acme/payments/_git/infra",
		},
		{
			name: "an on-prem collection keeps its own virtual directory and collection name",
			// The real, documented on-prem shape: a host, a virtual
			// directory, and a collection, none of which the SaaS URL has.
			apiBaseURL: "https://azuredevops.example.com/tfs/DefaultCollection",
			want:       "https://oauth2:tok@azuredevops.example.com/tfs/DefaultCollection/payments/_git/infra",
		},
		{
			name:       "a trailing slash on the collection does not double up",
			apiBaseURL: "https://azuredevops.example.com/tfs/DefaultCollection/",
			want:       "https://oauth2:tok@azuredevops.example.com/tfs/DefaultCollection/payments/_git/infra",
		},
		{
			name:       "a value naming no host is refused, never silently cloned from dev.azure.com",
			apiBaseURL: "azuredevops.example.com/tfs/DefaultCollection",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := azureDevOpsCloneURL(tt.apiBaseURL, "acme", "payments", "infra", "tok")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("azureDevOpsCloneURL(%q) = %q, want an error", tt.apiBaseURL, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("azureDevOpsCloneURL(%q): %v", tt.apiBaseURL, err)
			}
			if got != tt.want {
				t.Errorf("azureDevOpsCloneURL(%q) =\n  %q\nwant\n  %q", tt.apiBaseURL, got, tt.want)
			}
		})
	}
}

// TestEnsureRepoCheckout_ClonesFromEnterpriseHost is the real, end-to-end
// half: a GHES-style base URL must make the actual `git clone` reach the
// enterprise host. The enterprise host is rewritten to a real local
// repository via git's own url.<base>.insteadOf, and github.com is
// deliberately left with no rewrite at all -- so the previous hardcoded
// behavior fails here rather than passing quietly.
func TestEnsureRepoCheckout_ClonesFromEnterpriseHost(t *testing.T) {
	root := newOriginRoot(t, "acme/infra.git", "enterprise-content")
	withGitURLRewrite(t, "https://x-access-token:installation-token@github.example.com/", root)

	workDir := t.TempDir()
	repoDir, err := ensureRepoCheckout(context.Background(), workDir,
		"https://github.example.com", "acme", "infra", "installation-token")
	if err != nil {
		t.Fatalf("ensureRepoCheckout against a GHES base URL: %v", err)
	}
	assertClonedContent(t, repoDir, "enterprise-content")
}

// TestEnsureRepoCheckout_ClonesFromGitHubDotComByDefault is the other
// direction: the SaaS default must be untouched by UBI-171. Here it is
// github.com that is rewritten and the enterprise host that is not.
func TestEnsureRepoCheckout_ClonesFromGitHubDotComByDefault(t *testing.T) {
	root := newOriginRoot(t, "acme/infra.git", "saas-content")
	withGitURLRewrite(t, "https://x-access-token:installation-token@github.com/", root)

	workDir := t.TempDir()
	repoDir, err := ensureRepoCheckout(context.Background(), workDir, "", "acme", "infra", "installation-token")
	if err != nil {
		t.Fatalf("ensureRepoCheckout with no base URL configured: %v", err)
	}
	assertClonedContent(t, repoDir, "saas-content")
}

// TestEnsureRepoCheckoutAzureDevOps_ClonesFromOnPremCollection is the
// Azure DevOps counterpart, with the collection URL's own path segments
// (/tfs/DefaultCollection) part of what has to survive: a rewrite keyed
// on the full collection root only matches if those segments really made
// it into the clone URL.
func TestEnsureRepoCheckoutAzureDevOps_ClonesFromOnPremCollection(t *testing.T) {
	root := newOriginRoot(t, "payments/_git/infra", "on-prem-content")
	withGitURLRewrite(t, "https://oauth2:pat@azuredevops.example.com/tfs/DefaultCollection/", root)

	workDir := t.TempDir()
	repoDir, err := ensureRepoCheckoutAzureDevOps(context.Background(), workDir,
		"https://azuredevops.example.com/tfs/DefaultCollection", "acme", "payments", "infra", "pat")
	if err != nil {
		t.Fatalf("ensureRepoCheckoutAzureDevOps against an on-prem collection: %v", err)
	}
	assertClonedContent(t, repoDir, "on-prem-content")
}

func TestEnsureRepoCheckoutAzureDevOps_ClonesFromDevAzureComByDefault(t *testing.T) {
	root := newOriginRoot(t, "payments/_git/infra", "azure-saas-content")
	withGitURLRewrite(t, "https://oauth2:pat@dev.azure.com/acme/", root)

	workDir := t.TempDir()
	repoDir, err := ensureRepoCheckoutAzureDevOps(context.Background(), workDir,
		"", "acme", "payments", "infra", "pat")
	if err != nil {
		t.Fatalf("ensureRepoCheckoutAzureDevOps with no base URL configured: %v", err)
	}
	assertClonedContent(t, repoDir, "azure-saas-content")
}

// newOriginRoot builds a real git repository at subPath inside a fresh
// temporary root, carrying marker as the content of MARKER, and returns
// a file:// URL for the ROOT (not the repository).
//
// subPath is the exact path a correct clone URL carries after its host,
// which is what makes the rewrite below a real assertion rather than a
// convenience: git rewrites only the host-and-prefix part and appends
// the rest verbatim, so the repository is only found when the code under
// test built both halves correctly.
func newOriginRoot(t *testing.T, subPath, marker string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, filepath.FromSlash(subPath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create origin repo dir: %v", err)
	}
	mustGit(t, dir, "init", "--quiet", "--initial-branch=main", ".")
	mustGit(t, dir, "config", "user.email", "test@example.invalid")
	mustGit(t, dir, "config", "user.name", "ubx test")
	if err := os.WriteFile(filepath.Join(dir, "MARKER"), []byte(marker), 0o644); err != nil {
		t.Fatalf("write MARKER: %v", err)
	}
	mustGit(t, dir, "add", "MARKER")
	mustGit(t, dir, "commit", "--quiet", "-m", "marker")
	return "file://" + root + "/"
}

// withGitURLRewrite makes every git invocation this process spawns
// rewrite prefix to root, via git's own real url.<root>.insteadOf
// configuration carried in the environment (GIT_CONFIG_COUNT et al).
// Nothing about ensureRepoCheckout is stubbed: it builds and passes a
// real remote URL and the real git binary resolves it.
//
// prefix is the FULL credentialed prefix, credentials included, because
// git matches insteadOf against the literal URL and the credentials sit
// between the scheme and the host. Exactly one prefix is registered, so
// a clone aimed at any other host (github.com when a GHES instance was
// configured, dev.azure.com when a collection was) finds no rewrite and
// fails outright, which is the point.
func withGitURLRewrite(t *testing.T, prefix, root string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+root+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", prefix)
}

func assertClonedContent(t *testing.T, repoDir, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(repoDir, "MARKER"))
	if err != nil {
		t.Fatalf("read cloned MARKER: %v", err)
	}
	if string(got) != want {
		t.Errorf("cloned MARKER = %q, want %q -- the clone reached the wrong host", got, want)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestEnterpriseAPIRoot_AgreesWithGoGitHub pins the one place this
// package has to reimplement a convention it does not own.
//
// ghinstallation exchanges the App JWT for an installation token by
// string-joining onto its own BaseURL, with no /api/v3 convention of its
// own; go-github applies that convention itself. If the two disagree,
// the JWT and the calls made with the resulting token go to different
// hosts or different roots. Each case below asserts enterpriseAPIRoot
// produces exactly what go-github's own WithEnterpriseURLs would, minus
// the trailing slash go-github requires and ghinstallation does not.
func TestEnterpriseAPIRoot_AgreesWithGoGitHub(t *testing.T) {
	tests := []struct {
		instanceURL string
		want        string
	}{
		{"https://github.example.com", "https://github.example.com/api/v3"},
		{"https://github.example.com/", "https://github.example.com/api/v3"},
		{"https://github.example.com/api/v3", "https://github.example.com/api/v3"},
		{"https://github.example.com/api/v3/", "https://github.example.com/api/v3"},
		// An API host serves no /api/v3 at all, and go-github skips it
		// for exactly this reason. Appending here would send the token
		// exchange somewhere the SaaS does not serve.
		{"https://api.github.com", "https://api.github.com"},
		{"https://acme.api.example.com", "https://acme.api.example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.instanceURL, func(t *testing.T) {
			if got := enterpriseAPIRoot(tt.instanceURL); got != tt.want {
				t.Errorf("enterpriseAPIRoot(%q) = %q, want %q", tt.instanceURL, got, tt.want)
			}

			// The real cross-check: build a client the same way
			// newInstallationClients does and confirm go-github landed
			// on the same root.
			api, err := ghub.New("tok", ghub.WithEnterpriseBaseURL(tt.instanceURL))
			if err != nil {
				t.Fatalf("ghub.New: %v", err)
			}
			if got := strings.TrimRight(api.API().BaseURL.String(), "/"); got != tt.want {
				t.Errorf("go-github BaseURL = %q, but enterpriseAPIRoot gives %q -- the two must agree", got, tt.want)
			}
		})
	}
}
