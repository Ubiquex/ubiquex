package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// providersVersionsMux serves a fake Terraform Registry "list versions"
// endpoint, mirroring provider/versions_test.go's own versionsMux
// (independently reimplemented here since that helper is unexported in a
// different package, matching this codebase's own established per-
// package-boundary precedent for such helpers).
func providersVersionsMux(t *testing.T, namespace, typ string, versions []string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/providers/"+namespace+"/"+typ+"/versions", func(w http.ResponseWriter, r *http.Request) {
		type ver struct {
			Version string `json:"version"`
		}
		var resp struct {
			Versions []ver `json:"versions"`
		}
		for _, v := range versions {
			resp.Versions = append(resp.Versions, ver{Version: v})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	return httptest.NewServer(mux)
}

func TestProvidersCheck_UpToDate(t *testing.T) {
	srv := providersVersionsMux(t, "hashicorp", "aws", []string{"6.54.0"})
	defer srv.Close()

	dir := t.TempDir()
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
stack = "payments"

providers = {
  "hashicorp/aws" = "6.54.0"
}
`)

	env := []string{"UBX_REGISTRY_VERSIONS_API_BASE=" + srv.URL + "/v1/providers"}
	out, err := runUbx(t, env, "providers", "check")
	if err != nil {
		t.Fatalf("providers check: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "hashicorp/aws: 6.54.0 is current") {
		t.Fatalf("expected an up-to-date report, got: %s", out)
	}
}

func TestProvidersCheck_NewerAvailable_ExitCode1(t *testing.T) {
	srv := providersVersionsMux(t, "hashicorp", "aws", []string{"6.50.0", "6.58.0"})
	defer srv.Close()

	dir := t.TempDir()
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
stack = "payments"

providers = {
  "hashicorp/aws" = "6.50.0"
}
`)

	env := []string{"UBX_REGISTRY_VERSIONS_API_BASE=" + srv.URL + "/v1/providers"}
	out, err := runUbx(t, env, "providers", "check")
	requireExitCode(t, err, 1, out)
	if !strings.Contains(out, "hashicorp/aws: 6.50.0 -> 6.58.0 available") {
		t.Fatalf("expected a newer-version report, got: %s", out)
	}
}

// TestProvidersCheck_Write_BumpsInPlace is this ticket's own required
// proof that --write does the ONE thing it claims: rewrite the exact
// version pin, in place, leaving every other line untouched (comments,
// unrelated keys, formatting) -- not a full re-serialization.
func TestProvidersCheck_Write_BumpsInPlace(t *testing.T) {
	srv := providersVersionsMux(t, "hashicorp", "aws", []string{"6.50.0", "6.58.0"})
	defer srv.Close()

	dir := t.TempDir()
	withConfigSearchDir(t, dir)
	original := `# a hand-written comment that must survive
stack = "payments"

providers = {
  "hashicorp/aws" = "6.50.0"
}
`
	writeConfig(t, dir, original)

	env := []string{"UBX_REGISTRY_VERSIONS_API_BASE=" + srv.URL + "/v1/providers"}
	out, err := runUbx(t, env, "providers", "check", "--write")
	requireExitCode(t, err, 1, out)
	if !strings.Contains(out, "hashicorp/aws: wrote 6.50.0 -> 6.58.0 in") {
		t.Fatalf("expected a write confirmation, got: %s", out)
	}

	got, readErr := os.ReadFile(filepath.Join(dir, ".ubx", "config"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	want := strings.Replace(original, `"hashicorp/aws" = "6.50.0"`, `"hashicorp/aws" = "6.58.0"`, 1)
	if string(got) != want {
		t.Fatalf("config.hcl after --write:\n%s\nwant:\n%s", got, want)
	}

	// Running again against the now-bumped file reports current, proving
	// the write is genuinely load-bearing, not just printed.
	out2, err2 := runUbx(t, env, "providers", "check")
	if err2 != nil {
		t.Fatalf("providers check (after write): %v\noutput: %s", err2, out2)
	}
	if !strings.Contains(out2, "hashicorp/aws: 6.58.0 is current") {
		t.Fatalf("expected the bumped version to report current, got: %s", out2)
	}
}

func TestProvidersCheck_NoProvidersDeclared_Errors(t *testing.T) {
	dir := t.TempDir()
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `stack = "payments"`)

	out, err := runUbx(t, nil, "providers", "check")
	requireExitCode(t, err, 2, out)
	if !strings.Contains(err.Error(), "[providers]") {
		t.Fatalf("expected the error to name the missing [providers] table, got: %v", err)
	}
}

// TestProvidersCheck_MultipleProviders_SortedDeterministic confirms two
// declared providers are checked in deterministic (sorted) order, never
// Go map iteration order -- this project's own standing determinism
// discipline (CLAUDE.md), reused here via sortedProviderSources exactly
// like `ubx sdk gen` already does.
func TestProvidersCheck_MultipleProviders_SortedDeterministic(t *testing.T) {
	awsSrv := providersVersionsMux(t, "hashicorp", "aws", []string{"6.54.0"})
	defer awsSrv.Close()
	// A single fake server can serve multiple provider paths -- reuse one
	// mux for both sources instead of two separate httptest servers.
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/providers/hashicorp/aws/versions", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"versions": []map[string]string{{"version": "6.54.0"}}})
	})
	mux.HandleFunc("/v1/providers/hashicorp/google/versions", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"versions": []map[string]string{{"version": "5.0.0"}}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
stack = "payments"

providers = {
  "hashicorp/google" = "5.0.0",
  "hashicorp/aws" = "6.54.0"
}
`)

	env := []string{"UBX_REGISTRY_VERSIONS_API_BASE=" + srv.URL + "/v1/providers"}
	out, err := runUbx(t, env, "providers", "check")
	if err != nil {
		t.Fatalf("providers check: %v\noutput: %s", err, out)
	}
	awsIdx := strings.Index(out, "hashicorp/aws")
	googleIdx := strings.Index(out, "hashicorp/google")
	if awsIdx < 0 || googleIdx < 0 || awsIdx > googleIdx {
		t.Fatalf("expected hashicorp/aws before hashicorp/google (sorted order), got: %s", out)
	}
}

func TestProvidersCheck_RegistryUnreachable_ExitCode2(t *testing.T) {
	dir := t.TempDir()
	withConfigSearchDir(t, dir)
	writeConfig(t, dir, `
stack = "payments"

providers = {
  "hashicorp/aws" = "6.54.0"
}
`)

	env := []string{"UBX_REGISTRY_VERSIONS_API_BASE=http://127.0.0.1:0/unreachable"}
	out, err := runUbx(t, env, "providers", "check")
	requireExitCode(t, err, 2, out)
}
