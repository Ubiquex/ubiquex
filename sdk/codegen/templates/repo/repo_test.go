package repo

import (
	"strings"
	"testing"
)

func TestScaffold_RealValues(t *testing.T) {
	files, err := Scaffold("digitalocean", "DigitalOcean", "OpenAPI-sourced via `ubx-provider-dynamic`", "1.0.1")
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

	// UBI-240: the docs-artifact step's own real substitution points --
	// every __PROVIDER__ occurrence replaced with the real short name,
	// every __SCHEMA_PIN_VERSION__ occurrence replaced with the real
	// pin version, neither placeholder token left behind.
	mustContain(t, publish, "[dynamic_providers.digitalocean]")
	mustContain(t, publish, `source = "ubiquex/digitalocean"`)
	mustContain(t, publish, `version = "1.0.1"`)
	mustContain(t, publish, "sdk gen --only digitalocean")
	mustContain(t, publish, `cp -r "${{ runner.temp }}/schema-dump/digitalocean" "$staging/schema"`)
	mustContain(t, publish, `cp "artifacts/digitalocean/descriptions.json"`)
	mustContain(t, publish, `'provider': 'digitalocean',`)
	mustNotContain(t, publish, "__PROVIDER__")
	mustNotContain(t, publish, "__SCHEMA_PIN_VERSION__")

	// UBI-225: the version bump opens a PR, it never pushes to main
	// directly -- the template's own prior "Commit version bump" step
	// (a real, direct `git push origin main`) predated this fix and
	// would have failed outright (GH006) against any branch-protected
	// new repo.
	mustContain(t, publish, "Open a PR for the version bump")
	mustContain(t, publish, "gh pr create")
	mustNotContain(t, publish, "Commit version bump")
	mustNotContain(t, publish, "git push origin main")

	buildNPM := files[".github/scripts/build-npm.mjs"]
	mustContain(t, buildNPM, "UBX_SDK_RUNTIME_VERSION")

	// UBI-222: exclusions.json must be optional in the docs-artifact
	// step, not bundled into the same required cp as
	// descriptions/intros/categories -- confirmed live against
	// Cloudflare: a genuinely new provider has no exclusions.json yet,
	// and the prior template's own required cp would have hard-failed
	// this step the first time a real publish.yml dispatch reached it.
	// ubx-sdk-digitalocean's own real, published copy already carries
	// this exact fix by hand; this proves the template now generates
	// it from the start.
	mustContain(t, publish, `cp "artifacts/digitalocean/descriptions.json" \
             "artifacts/digitalocean/intros.json" \
             "artifacts/digitalocean/categories.json" \
             "$staging/artifacts/"`)
	mustContain(t, publish, `cp "artifacts/digitalocean/exclusions.json" \
             "$staging/artifacts/" 2>/dev/null || true`)
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
