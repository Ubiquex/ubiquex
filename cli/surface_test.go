package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestScan_SurfaceAsIssue(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	// Adopt.
	adoptPath := filepath.Join(t.TempDir(), "adopt.json")
	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-surface",
		"--lookup", `{"name":"widget-surface","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir,
		"--out", adoptPath,
		"--no-attribution",
	); exitCode(err) != 1 {
		t.Fatalf("ubx scan (adopt): %v", err)
	}
	if _, err := runUbx(t, env, "accept", adoptPath, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (adopt): %v", err)
	}

	var gotTitle, gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/infra/issues", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotTitle, gotBody = req.Title, req.Body
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"number": 5, "html_url": "https://github.com/acme/infra/issues/5"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("UBX_GITHUB_API_BASE_URL", srv.URL+"/")

	out, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-surface",
		"--lookup", `{"name":"widget-surface","tags":{"env":"staging"}}`,
		"--ledger-dir", ledgerDir,
		"--no-attribution",
		"--surface-as", "issue",
		"--github-repo", "acme/infra",
	)
	requireExitCode(t, err, 1, out)
	if !strings.Contains(out, "opened issue https://github.com/acme/infra/issues/5") {
		t.Fatalf("expected the issue URL reported, got: %s", out)
	}
	if !strings.Contains(gotTitle, "payments.fake_widget.widget-surface") {
		t.Errorf("issue title = %q, want it to name the address", gotTitle)
	}
	if strings.Contains(gotBody, "ubx-proposal:") {
		t.Errorf("issue body must not carry a trailer (only PR mode needs one, since only a PR can be merged/accepted): %s", gotBody)
	}
	if !strings.Contains(gotBody, "blast radius: +0 ~0 -0") {
		t.Errorf("expected blast radius in the receipt, got: %s", gotBody)
	}
	if !strings.Contains(gotBody, "Full proposal") {
		t.Errorf("expected the full proposal JSON in the receipt, got: %s", gotBody)
	}
}

func TestScan_SurfaceAsPR(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	adoptPath := filepath.Join(t.TempDir(), "adopt.json")
	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-pr",
		"--lookup", `{"name":"widget-pr","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir,
		"--out", adoptPath,
		"--no-attribution",
	); exitCode(err) != 1 {
		t.Fatalf("ubx scan (adopt): %v", err)
	}
	if _, err := runUbx(t, env, "accept", adoptPath, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (adopt): %v", err)
	}

	var gotBody, gotBranch, gotFileContent string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/infra", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"default_branch": "main"})
	})
	mux.HandleFunc("/repos/acme/infra/git/ref/heads/main", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ref": "refs/heads/main", "object": map[string]any{"sha": "abc123", "type": "commit"}})
	})
	mux.HandleFunc("/repos/acme/infra/git/refs", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Ref, SHA string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotBranch = req.Ref
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ref": req.Ref, "object": map[string]any{"sha": "abc123", "type": "commit"}})
	})
	mux.HandleFunc("/repos/acme/infra/contents/", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Content []byte `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotFileContent = string(req.Content)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"content": map[string]any{"sha": "def456"}})
	})
	mux.HandleFunc("/repos/acme/infra/pulls", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Body string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotBody = req.Body
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"number": 11, "html_url": "https://github.com/acme/infra/pull/11"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("UBX_GITHUB_API_BASE_URL", srv.URL+"/")

	out, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-pr",
		"--lookup", `{"name":"widget-pr","tags":{"env":"staging"}}`,
		"--ledger-dir", ledgerDir,
		"--no-attribution",
		"--surface-as", "pr",
		"--github-repo", "acme/infra",
	)
	requireExitCode(t, err, 1, out)
	if !strings.Contains(out, "opened pull request https://github.com/acme/infra/pull/11") {
		t.Fatalf("expected the PR URL reported, got: %s", out)
	}
	if !strings.HasPrefix(gotBody, "ubx-proposal: ") {
		t.Fatalf("expected the PR body to start with the ubx-proposal trailer, got: %s", gotBody)
	}
	if !strings.Contains(gotBranch, "refs/heads/ubx-drift/payments-fake_widget-widget-pr") {
		t.Errorf("branch ref = %q, want a ubx-drift/ branch naming the address", gotBranch)
	}
	var committed map[string]any
	if err := json.Unmarshal([]byte(gotFileContent), &committed); err != nil {
		t.Fatalf("committed file content isn't valid proposal JSON: %v\n%s", err, gotFileContent)
	}
	if committed["kind"] != "drift_adopt" {
		t.Errorf("committed proposal kind = %v, want drift_adopt", committed["kind"])
	}
}

func TestScan_SurfaceAsOnlyTriggersOnDrift(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	called := false
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { called = true })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("UBX_GITHUB_API_BASE_URL", srv.URL+"/")

	// First scan of a never-before-seen address is "new", not "drifted" --
	// surfacing must not fire.
	out, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-new",
		"--lookup", `{"name":"widget-new"}`,
		"--ledger-dir", ledgerDir,
		"--no-attribution",
		"--surface-as", "issue",
		"--github-repo", "acme/infra",
	)
	requireExitCode(t, err, 1, out)
	if called {
		t.Fatal("expected --surface-as to be skipped for a ScanNew outcome, but the GitHub API was called")
	}
}

func TestScan_SurfaceAsInvalidValue(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	adoptPath := filepath.Join(t.TempDir(), "adopt.json")
	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-bad",
		"--lookup", `{"name":"widget-bad","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir,
		"--out", adoptPath,
		"--no-attribution",
	); exitCode(err) != 1 {
		t.Fatalf("ubx scan (adopt): %v", err)
	}
	if _, err := runUbx(t, env, "accept", adoptPath, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (adopt): %v", err)
	}

	_, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-bad",
		"--lookup", `{"name":"widget-bad","tags":{"env":"staging"}}`,
		"--ledger-dir", ledgerDir,
		"--no-attribution",
		"--surface-as", "carrier-pigeon",
		"--github-repo", "acme/infra",
	)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), `must be "issue" or "pr"`) {
		t.Fatalf("expected a clear invalid-value error, got: %v", err)
	}
}

func TestScan_SurfaceAsIssue_MissingGithubRepo(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	adoptPath := filepath.Join(t.TempDir(), "adopt.json")
	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-norepo",
		"--lookup", `{"name":"widget-norepo","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir,
		"--out", adoptPath,
		"--no-attribution",
	); exitCode(err) != 1 {
		t.Fatalf("ubx scan (adopt): %v", err)
	}
	if _, err := runUbx(t, env, "accept", adoptPath, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (adopt): %v", err)
	}

	_, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-norepo",
		"--lookup", `{"name":"widget-norepo","tags":{"env":"staging"}}`,
		"--ledger-dir", ledgerDir,
		"--no-attribution",
		"--surface-as", "issue",
	)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "--github-repo") {
		t.Fatalf("expected a clear missing-github-repo error, got: %v", err)
	}
}

func TestScan_SurfaceAsIssue_IncludesWritebackPreview(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	adoptPath := filepath.Join(t.TempDir(), "adopt.json")
	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-preview",
		"--lookup", `{"name":"widget-preview","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir,
		"--out", adoptPath,
		"--no-attribution",
	); exitCode(err) != 1 {
		t.Fatalf("ubx scan (adopt): %v", err)
	}
	if _, err := runUbx(t, env, "accept", adoptPath, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept (adopt): %v", err)
	}

	tfDir := t.TempDir()
	writeFile(t, filepath.Join(tfDir, "widget.tf"), "resource \"fake_widget\" \"widget-preview\" {\n  tags = {\n    env = \"prod\"\n  }\n}\n")

	var gotBody string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/infra/issues", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Body string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotBody = req.Body
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"number": 1, "html_url": "https://github.com/acme/infra/issues/1"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	t.Setenv("UBX_GITHUB_API_BASE_URL", srv.URL+"/")

	out, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "widget-preview",
		"--lookup", `{"name":"widget-preview","tags":{"env":"staging"}}`,
		"--ledger-dir", ledgerDir,
		"--no-attribution",
		"--surface-as", "issue",
		"--github-repo", "acme/infra",
		"--tf-dir", tfDir,
	)
	requireExitCode(t, err, 1, out)
	if !strings.Contains(gotBody, ".tf write-back preview") {
		t.Fatalf("expected a write-back preview section, got: %s", gotBody)
	}
	if !strings.Contains(gotBody, `-    env = "prod"`) || !strings.Contains(gotBody, `+    env = "staging"`) {
		t.Fatalf("expected the actual diff content in the preview, got: %s", gotBody)
	}
}
