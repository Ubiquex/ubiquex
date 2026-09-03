// Package repo generates the one-time SDK repo scaffold a genuinely
// new provider needs at onboarding, beyond what GeneratedRepo
// (sdk/codegen/templates/{go,py,ts}) already produces per language.
//
// UBI-222: confirmed live, twice, onboarding DigitalOcean (the first
// genuinely from-scratch provider in this org): a brand new
// ubx-sdk-<name> repo needs LICENSE, .github/scripts/build-npm.mjs,
// .github/workflows/publish.yml, CLAUDE.md, README.md, STATE.md, and
// HISTORY.md, none of which `ubx sdk gen` produces -- every existing
// provider repo had these hand-copied in from a sibling repo at
// onboarding time, out of band, with nothing generating them. A first
// real `publish.yml` dispatch against a repo missing build-npm.mjs
// fails outright with MODULE_NOT_FOUND; a real `deno check` against a
// repo missing deno.json fails to resolve any import at all -- both
// were the actual, live failures found onboarding DigitalOcean, not
// hypothetical.
//
// deno.json is the one file in that original list NOT produced here:
// its own "exports" map is real, per-provider generated content (the
// same file tree GeneratedRepo already builds), not scaffold, so
// sdk/codegen/templates/ts's own GeneratedRepo now writes it directly
// -- see that package's own denoJSON doc comment.
//
// go.sum is also NOT produced here -- it requires a real `go mod
// tidy` against the actual Go module proxy, not static content this
// package could return. The CLI command that calls Scaffold runs that
// as a real subprocess after writing go.mod (already part of
// GeneratedRepo's own Go output) and this package's own files.
package repo

import (
	"embed"
	"fmt"
	"strings"
	"text/template"
)

//go:embed LICENSE build-npm.mjs publish.yml claude.md.tmpl readme.md.tmpl state.md.tmpl history.md.tmpl .gitignore
var files embed.FS

type scaffoldData struct {
	ShortName       string
	ProviderDisplay string
	SourceNote      string
}

// docsArtifactPlaceholder/schemaPinVersionPlaceholder are publish.yml's
// own real, literal tokens for the docs-artifact step (UBI-240) --
// deliberately distinct from "kubernetes" (the substitution source for
// every other real occurrence in this file, see below): a blanket bare
// "kubernetes" replace would also mangle this file's own real
// historical reference to three OTHER providers ("datadog/github/
// kubernetes each had this exact shape"), which must survive
// untouched. These two tokens appear nowhere else in the template, so
// replacing them is unambiguous.
const (
	docsArtifactPlaceholder     = "__PROVIDER__"
	schemaPinVersionPlaceholder = "__SCHEMA_PIN_VERSION__"
)

// Scaffold returns path (relative to the repo root) -> content for
// every one-time file a new ubx-sdk-<shortName> repo needs that
// `ubx sdk gen` itself does not produce.
//
// shortName is the real, published SDK repo's own short name (e.g.
// "digitalocean") -- matches the [dynamic_providers.<shortName>]
// entry in ubiquex's own sdk/providers/.ubx/config exactly.
// providerDisplay is the real, human display name (e.g.
// "DigitalOcean"). sourceNote is one real, honest sentence describing
// this provider's own schema source and format (e.g. "OpenAPI-sourced
// via `ubx-provider-dynamic`") -- deliberately a caller-supplied
// argument, not derived here: what's unusual about a given provider's
// own real source (Datadog's own real v1/v2 API merge, for one
// confirmed example) is a judgment call, not something this package
// can infer from a provider name alone. schemaPinVersion is the
// [dynamic_providers.<shortName>]'s own real, current pinned `version`
// value in sdk/providers/.ubx/config at the moment this command runs
// (UBI-240) -- onboard-provider's own hop 5 always switches the pin to
// its pinned source/version shape before hop 6 (this command) runs, so
// this value is always real and already known by the caller, never
// guessed here. A provider whose own upstream has no discrete
// pinnable release (Kubernetes is the one real exception in this org)
// needs the generated publish.yml's own schema-dump step hand-adjusted
// afterward; this parameter still takes the pin version that would
// otherwise apply, left unused in the generated file's own comment
// trail for that one real case.
//
// LICENSE and build-npm.mjs are embedded verbatim -- confirmed
// byte-identical across every existing provider repo before being
// copied in here. publish.yml is the real, current file with every
// ubx-sdk-<name>/sdk_<name> occurrence substituted via plain string
// replacement, never Go's own {{ }} template syntax -- this file's
// real content is full of literal GitHub Actions ${{ }} expressions
// that would collide with it. The docs-artifact step's own bare
// provider-name and schema-pin-version occurrences use two distinct,
// unambiguous literal tokens (docsArtifactPlaceholder,
// schemaPinVersionPlaceholder) for the same reason.
func Scaffold(shortName, providerDisplay, sourceNote, schemaPinVersion string) (map[string]string, error) {
	license, err := files.ReadFile("LICENSE")
	if err != nil {
		return nil, fmt.Errorf("sdk/codegen/templates/repo: %w", err)
	}
	buildNPM, err := files.ReadFile("build-npm.mjs")
	if err != nil {
		return nil, fmt.Errorf("sdk/codegen/templates/repo: %w", err)
	}
	gitignore, err := files.ReadFile(".gitignore")
	if err != nil {
		return nil, fmt.Errorf("sdk/codegen/templates/repo: %w", err)
	}
	publishRaw, err := files.ReadFile("publish.yml")
	if err != nil {
		return nil, fmt.Errorf("sdk/codegen/templates/repo: %w", err)
	}
	// Order matters: "sdk-kubernetes"/"sdk_kubernetes" cover every
	// real ubx-sdk-<name>/@ubx%2fsdk-<name>/ubx_sdk_<name> occurrence;
	// "kubernetes-go" is the one remaining bare occurrence, inside a
	// comment illustrating what NOT to name a Go module tag. A single
	// real historical reference ("datadog/github/kubernetes each had
	// this exact shape", UBI-185) deliberately does NOT match either
	// pattern and is left untouched -- it names three OTHER real
	// repos' own past incident, not a template token.
	publish := string(publishRaw)
	publish = strings.ReplaceAll(publish, "sdk-kubernetes", "sdk-"+shortName)
	publish = strings.ReplaceAll(publish, "sdk_kubernetes", "sdk_"+shortName)
	publish = strings.ReplaceAll(publish, "kubernetes-go", shortName+"-go")
	publish = strings.ReplaceAll(publish, docsArtifactPlaceholder, shortName)
	publish = strings.ReplaceAll(publish, schemaPinVersionPlaceholder, schemaPinVersion)

	data := scaffoldData{ShortName: shortName, ProviderDisplay: providerDisplay, SourceNote: sourceNote}
	claude, err := renderTemplate("claude.md.tmpl", data)
	if err != nil {
		return nil, err
	}
	readme, err := renderTemplate("readme.md.tmpl", data)
	if err != nil {
		return nil, err
	}
	state, err := renderTemplate("state.md.tmpl", data)
	if err != nil {
		return nil, err
	}
	history, err := renderTemplate("history.md.tmpl", data)
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"LICENSE":                       string(license),
		".gitignore":                    string(gitignore),
		".github/scripts/build-npm.mjs": string(buildNPM),
		".github/workflows/publish.yml": publish,
		"CLAUDE.md":                     claude,
		"README.md":                     readme,
		"STATE.md":                      state,
		"HISTORY.md":                    history,
	}, nil
}

func renderTemplate(name string, data scaffoldData) (string, error) {
	raw, err := files.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("sdk/codegen/templates/repo: %w", err)
	}
	tmpl, err := template.New(name).Parse(string(raw))
	if err != nil {
		return "", fmt.Errorf("sdk/codegen/templates/repo: %s: %w", name, err)
	}
	var b strings.Builder
	if err := tmpl.Execute(&b, data); err != nil {
		return "", fmt.Errorf("sdk/codegen/templates/repo: %s: %w", name, err)
	}
	return b.String(), nil
}
