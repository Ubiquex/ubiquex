package repo

import (
	"strings"
	"testing"
)

func TestScaffold_RealValues(t *testing.T) {
	files, err := Scaffold("digitalocean", "DigitalOcean", "OpenAPI-sourced via `ubx-provider-dynamic`")
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}

	wantPaths := []string{
		"LICENSE", ".github/scripts/build-npm.mjs", ".github/workflows/publish.yml",
		"CLAUDE.md", "README.md", "STATE.md", "HISTORY.md",
	}
	for _, p := range wantPaths {
		if _, ok := files[p]; !ok {
			t.Errorf("Scaffold: missing expected path %q", p)
		}
	}
	if len(files) != len(wantPaths) {
		t.Errorf("Scaffold: got %d files, want %d", len(files), len(wantPaths))
	}

	mustContain(t, files["LICENSE"], "Apache License")
	mustContain(t, files["CLAUDE.md"], "# CLAUDE.md -- ubx-sdk-digitalocean")
	mustContain(t, files["CLAUDE.md"], "Typed bindings for DigitalOcean")
	mustContain(t, files["CLAUDE.md"], "OpenAPI-sourced via `ubx-provider-dynamic`")
	mustContain(t, files["README.md"], "# ubx-sdk-digitalocean")
	mustContain(t, files["README.md"], "<!-- README-GEN:BEGIN -->")
	mustContain(t, files["STATE.md"], "just scaffolded")
	mustContain(t, files["HISTORY.md"], "DigitalOcean")

	// UBI-222: the real substitution points -- every occurrence of the
	// real kubernetes repo/package name replaced, the one real
	// historical reference to three OTHER providers left untouched.
	publish := files[".github/workflows/publish.yml"]
	mustContain(t, publish, "Check out ubx-sdk-digitalocean")
	mustContain(t, publish, "https://pypi.org/pypi/ubx-sdk-digitalocean/json")
	mustContain(t, publish, "@ubx%2fsdk-digitalocean")
	mustContain(t, publish, `twine upload "dist/ubx_sdk_digitalocean-${v}"*`)
	mustContain(t, publish, "digitalocean-go/vX.Y.Z")
	mustContain(t, publish, "datadog/github/kubernetes each had this exact")
	mustNotContain(t, publish, "ubx-sdk-kubernetes")
	mustNotContain(t, publish, "ubx_sdk_kubernetes")

	buildNPM := files[".github/scripts/build-npm.mjs"]
	mustContain(t, buildNPM, "UBX_SDK_RUNTIME_VERSION")
}

func mustContain(t *testing.T, content, substr string) {
	t.Helper()
	if !strings.Contains(content, substr) {
		t.Errorf("expected content to contain %q, got:\n%s", substr, content)
	}
}

func mustNotContain(t *testing.T, content, substr string) {
	t.Helper()
	if strings.Contains(content, substr) {
		t.Errorf("expected content to NOT contain %q, got:\n%s", substr, content)
	}
}
