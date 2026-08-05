package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// versionsMux serves a fake "list versions" endpoint at
// /v1/providers/<namespace>/<type>/versions, returning versions verbatim
// -- mirrors acquire_test.go's own httptest.NewServer(mux) convention.
func versionsMux(t *testing.T, namespace, typ string, versions []string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/providers/"+namespace+"/"+typ+"/versions", func(w http.ResponseWriter, r *http.Request) {
		var resp versionsResponse
		for _, v := range versions {
			resp.Versions = append(resp.Versions, struct {
				Version string `json:"version"`
			}{Version: v})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	return httptest.NewServer(mux)
}

func TestLatestVersion_PicksHighestRealSemver(t *testing.T) {
	srv := versionsMux(t, "hashicorp", "aws", []string{"6.54.0", "6.57.1", "6.2.0", "6.56.0"})
	defer srv.Close()

	src := Source{Hostname: "registry.terraform.io", Namespace: "hashicorp", Type: "aws"}
	got, err := LatestVersion(context.Background(), src, WithVersionsHTTPClient(srv.Client()), WithVersionsAPIBase(srv.URL+"/v1/providers"))
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	if got != "6.57.1" {
		t.Fatalf("LatestVersion = %q, want %q", got, "6.57.1")
	}
}

// TestLatestVersion_PrereleaseExcluded confirms a pre-release is never
// picked as "latest" over a real stable release, even one nominally
// higher (6.60.0-beta1 vs 6.57.1) -- real semver PRECEDENCE alone ranks
// 6.60.0-beta1 above 6.57.1 (a pre-release only ranks below its OWN
// associated stable release), so this needs the explicit pre-release
// filter, confirmed by first writing this test WITHOUT the filter and
// watching it fail exactly this way, not assumed correct from the
// library's own name.
func TestLatestVersion_PrereleaseExcluded(t *testing.T) {
	srv := versionsMux(t, "hashicorp", "aws", []string{"6.57.1", "6.60.0-beta1"})
	defer srv.Close()

	src := Source{Hostname: "registry.terraform.io", Namespace: "hashicorp", Type: "aws"}
	got, err := LatestVersion(context.Background(), src, WithVersionsHTTPClient(srv.Client()), WithVersionsAPIBase(srv.URL+"/v1/providers"))
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	if got != "6.57.1" {
		t.Fatalf("LatestVersion = %q, want %q (a pre-release must never be picked over a real stable release)", got, "6.57.1")
	}
}

// TestLatestVersion_AllPrerelease_FallsBack confirms a provider with NO
// stable release at all still returns its own highest pre-release,
// rather than the zero-versions error -- the filter's own real edge case.
func TestLatestVersion_AllPrerelease_FallsBack(t *testing.T) {
	srv := versionsMux(t, "hashicorp", "aws", []string{"6.60.0-alpha1", "6.60.0-beta1"})
	defer srv.Close()

	src := Source{Hostname: "registry.terraform.io", Namespace: "hashicorp", Type: "aws"}
	got, err := LatestVersion(context.Background(), src, WithVersionsHTTPClient(srv.Client()), WithVersionsAPIBase(srv.URL+"/v1/providers"))
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	if got != "6.60.0-beta1" {
		t.Fatalf("LatestVersion = %q, want %q", got, "6.60.0-beta1")
	}
}

func TestLatestVersion_ZeroVersions_Errors(t *testing.T) {
	srv := versionsMux(t, "hashicorp", "aws", nil)
	defer srv.Close()

	src := Source{Hostname: "registry.terraform.io", Namespace: "hashicorp", Type: "aws"}
	_, err := LatestVersion(context.Background(), src, WithVersionsHTTPClient(srv.Client()), WithVersionsAPIBase(srv.URL+"/v1/providers"))
	if err == nil || !strings.Contains(err.Error(), "zero versions") {
		t.Fatalf("expected a zero-versions error, got: %v", err)
	}
}

func TestLatestVersion_RegistryError_Errors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/providers/hashicorp/aws/versions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	src := Source{Hostname: "registry.terraform.io", Namespace: "hashicorp", Type: "aws"}
	_, err := LatestVersion(context.Background(), src, WithVersionsHTTPClient(srv.Client()), WithVersionsAPIBase(srv.URL+"/v1/providers"))
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected a real registry-error to surface, got: %v", err)
	}
}
