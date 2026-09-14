package blueprint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/tseval"
)

func writeTSBlueprintConfig(t *testing.T, dir, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "deno.json"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeDenoLock gives a blueprint the pin that lets its npm dependencies
// be fetched. Contents do not matter to tsBlueprintImports, which only
// asks whether the blueprint pins at all; the lock's actual entries are
// enforced by deno in the prefetch pass (tslock.go).
func writeDenoLock(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, denoLockFileName), []byte(`{"version":"5"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTSBlueprintImports_RelativeTargets is the original fix: a
// blueprint's own deno.json travels in the content store and is read.
//
// Targets come back absolute, because a scope entry is applied to
// modules anywhere and a relative path would resolve against whoever is
// importing rather than against the blueprint.
func TestTSBlueprintImports_RelativeTargets(t *testing.T) {
	dir := t.TempDir()
	writeTSBlueprintConfig(t, dir, `{"imports":{"helper":"./helper.ts","deep":"./lib/deep.ts"}}`)

	got, err := tsBlueprintImports(dir, "bp")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"helper": "file://" + filepath.ToSlash(filepath.Join(dir, "helper.ts")),
		"deep":   "file://" + filepath.ToSlash(filepath.Join(dir, "lib", "deep.ts")),
	}
	for k, v := range want {
		if got.Imports[k] != v {
			t.Errorf("imports[%q] = %q, want %q", k, got.Imports[k], v)
		}
	}
	// Nothing here resolves through a registry, so evaluation must not be
	// made to pay for a fetch pass it does not need.
	if got.NeedsPrefetch {
		t.Error("purely local imports must not request a prefetch")
	}
}

// TestTSBlueprintImports_NoConfig: a built blueprint imports "@ubx/sdk"
// and nothing else, so it ships no deno.json. That is the ordinary case
// and must stay free.
func TestTSBlueprintImports_NoConfig(t *testing.T) {
	got, err := tsBlueprintImports(t.TempDir(), "bp")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Imports) != 0 || got.NeedsPrefetch {
		t.Fatalf("expected no imports for a blueprint with no deno.json, got %+v", got)
	}
}

// TestTSBlueprintImports_NPMNeedsALock is the boundary: pinned graphs may
// fetch, unpinned ones refuse.
//
// The property that matters is whether the graph is pinned, and a lock is
// the only evidence of it. Specifier kind was always a proxy: a registry
// specifier with a lock is pinned and the same specifier without one is
// not, and the proxy cannot tell those apart.
func TestTSBlueprintImports_NPMNeedsALock(t *testing.T) {
	t.Run("with a lock it resolves and asks for a prefetch", func(t *testing.T) {
		dir := t.TempDir()
		writeTSBlueprintConfig(t, dir, `{"imports":{"dep":"npm:left-pad@1.3.0"}}`)
		writeDenoLock(t, dir)

		got, err := tsBlueprintImports(dir, "bp")
		if err != nil {
			t.Fatalf("a pinned npm dependency must resolve: %v", err)
		}
		if got.Imports["dep"] != "npm:left-pad@1.3.0" {
			t.Errorf("an npm specifier goes into the map verbatim, got %q", got.Imports["dep"])
		}
		// And the subpath prefix, without which "dep/thing" is unmatched.
		if got.Imports["dep/"] != "npm:/left-pad@1.3.0/" {
			t.Errorf("imports[dep/] = %q, want the npm prefix form", got.Imports["dep/"])
		}
		// Without this the evaluation would reach a registry itself,
		// unverified, which is the whole thing the lock is here to prevent.
		if !got.NeedsPrefetch {
			t.Error("an npm dependency must request the fetch-and-verify pass")
		}
	})

	t.Run("without a lock it is refused, naming the remedy", func(t *testing.T) {
		dir := t.TempDir()
		writeTSBlueprintConfig(t, dir, `{"imports":{"dep":"npm:left-pad@1.3.0"}}`)

		_, err := tsBlueprintImports(dir, "widget-bp")
		if err == nil {
			t.Fatal("an unpinned npm dependency must be refused")
		}
		msg := err.Error()
		for _, want := range []string{"widget-bp", "npm:left-pad@1.3.0", denoLockFileName, "ubx blueprint package"} {
			if !strings.Contains(msg, want) {
				t.Errorf("refusal does not name %q: %s", want, msg)
			}
		}
		// The author reached for npm and has no reason to know what a
		// deno.lock is, so the message must name a command rather than a
		// concept.
		if !strings.Contains(msg, "deno install") {
			t.Errorf("refusal must name the by-hand command: %s", msg)
		}
		// npm's own lockfile is the obvious thing to reach for and does
		// not work, so the message says so rather than letting them try.
		if !strings.Contains(msg, "package-lock.json") {
			t.Errorf("refusal should rule out package-lock.json: %s", msg)
		}
	})
}

// TestTSBlueprintImports_JSRIsRefusedEvenWithALock is the limit worth
// knowing about, and the reason it gets its own test.
//
// The evaluator runs deno with --no-remote, which closes the dynamic
// import("https://...") gap. It does not block npm, but it does block
// jsr, because resolving a JSR package fetches
// https://jsr.io/<pkg>/meta.json. Verified against a fully warm cache, so
// this is a resolution-time rule rather than a caching artifact. A lock
// does not help, because the failure is not about pinning at all.
//
// This matters more than it looks: the published Ubiquex TypeScript SDK
// lives on JSR, so "registry dependencies now work" is false for exactly
// the registry an author is most likely to reach for.
func TestTSBlueprintImports_JSRIsRefusedEvenWithALock(t *testing.T) {
	dir := t.TempDir()
	writeTSBlueprintConfig(t, dir, `{"imports":{"dep":"jsr:@ubx/sdk-aws@1.2.0"}}`)
	writeDenoLock(t, dir)

	_, err := tsBlueprintImports(dir, "widget-bp")
	if err == nil {
		t.Fatal("a jsr dependency must be refused even from a blueprint that pins it")
	}
	msg := err.Error()
	// No ticket id: a Linear identifier means nothing to anyone outside
	// this org and does not belong in CLI output. The fact belongs, the
	// citation does not.
	if strings.Contains(msg, "UBI-") {
		t.Errorf("refusal must not cite a ticket id: %s", msg)
	}
	for _, want := range []string{"widget-bp", "jsr:@ubx/sdk-aws@1.2.0", "--no-remote"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not name %q: %s", want, msg)
		}
	}
	// It must NOT suggest committing a lock. This blueprint has one, and
	// sending the author to add what they already have would waste their
	// time on the wrong problem.
	if strings.Contains(msg, "deno install") {
		t.Errorf("a jsr refusal must not suggest locking, which cannot fix it: %s", msg)
	}
}

// TestTSBlueprintImports_WebURLIsRefused: a direct http(s) or VCS target
// is neither pinnable by a lock nor loadable under --no-remote.
func TestTSBlueprintImports_WebURLIsRefused(t *testing.T) {
	dir := t.TempDir()
	writeTSBlueprintConfig(t, dir, `{"imports":{"dep":"https://deno.land/x/thing/mod.ts"}}`)
	writeDenoLock(t, dir)

	_, err := tsBlueprintImports(dir, "bp")
	if err == nil {
		t.Fatal("a direct https import must be refused")
	}
	if !strings.Contains(err.Error(), "--no-remote") {
		t.Errorf("refusal should name the flag that blocks it: %s", err)
	}
}

// TestTSBlueprintImports_NodeBuiltinPassesThrough: "node:fs" is a builtin,
// not a fetch, and classifying it as remote would refuse a blueprint that
// does nothing requiring network at all.
func TestTSBlueprintImports_NodeBuiltinPassesThrough(t *testing.T) {
	dir := t.TempDir()
	writeTSBlueprintConfig(t, dir, `{"imports":{"fs":"node:fs"}}`)

	got, err := tsBlueprintImports(dir, "bp")
	if err != nil {
		t.Fatalf("a node: builtin is not a registry dependency: %v", err)
	}
	if got.Imports["fs"] != "node:fs" {
		t.Errorf("a node: specifier must pass through untouched, got %q", got.Imports["fs"])
	}
	if got.NeedsPrefetch {
		t.Error("a builtin must not trigger a fetch pass")
	}
}

// TestTSBlueprintImports_RefusalIsAllOrNothing: silently dropping the
// refused entries and resolving the rest would produce a blueprint that
// half works, failing later at the first import of the dropped one.
func TestTSBlueprintImports_RefusalIsAllOrNothing(t *testing.T) {
	dir := t.TempDir()
	writeTSBlueprintConfig(t, dir, `{"imports":{"local":"./a.ts","remote":"npm:x@1"}}`)
	got, err := tsBlueprintImports(dir, "bp")
	if err == nil {
		t.Fatal("a config mixing local and unpinned registry targets must be refused, not partly honoured")
	}
	if len(got.Imports) != 0 {
		t.Fatalf("no imports should be returned alongside a refusal, got %v", got.Imports)
	}
}

func writePackageJSON(t *testing.T, dir, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, packageJSONFileName), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeNPMPackage writes a package directory with a main entry.
func writeNPMPackage(t *testing.T, dir, name, main string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePackageJSON(t, dir, `{"name":"`+name+`","version":"1.0.0","main":"`+main+`"}`)
	if err := os.WriteFile(filepath.Join(dir, main), []byte("export const x = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTSBlueprintImports_PackageJSONRangeBecomesAnNPMSpecifier is the
// half the first version of this file missed entirely.
//
// A blueprint authored with npm tooling has a package.json and no
// deno.json, which is the COMMON case. Its dependencies are bare semver
// ranges, and an import map entry needs a specifier, so the range is
// turned into one. The range is kept rather than resolved here, because
// the lock is what pins it: resolving it in ubx would be a second,
// subtly different resolver disagreeing with the one that fetches.
func TestTSBlueprintImports_PackageJSONRangeBecomesAnNPMSpecifier(t *testing.T) {
	dir := t.TempDir()
	writePackageJSON(t, dir, `{"dependencies":{"left-pad":"^1.3.0"}}`)
	writeDenoLock(t, dir)

	got, err := tsBlueprintImports(dir, "ts-bp")
	if err != nil {
		t.Fatalf("a pinned package.json dependency must resolve: %v", err)
	}
	if got.Imports["left-pad"] != "npm:left-pad@^1.3.0" {
		t.Errorf("imports[left-pad] = %q, want the synthesized npm specifier", got.Imports["left-pad"])
	}
	if !got.NeedsPrefetch {
		t.Error("a package.json registry dependency must request the fetch pass")
	}
}

// TestTSBlueprintImports_PackageJSONUnpinnedIsRefused: the boundary is
// the same whichever format declared the dependency. Refusing one format
// and honouring the other would be an inconsistent boundary, which is
// the exact mistake this file made once already.
func TestTSBlueprintImports_PackageJSONUnpinnedIsRefused(t *testing.T) {
	dir := t.TempDir()
	writePackageJSON(t, dir, `{"dependencies":{"left-pad":"1.3.0"}}`)

	_, err := tsBlueprintImports(dir, "ts-bp")
	if err == nil {
		t.Fatal("an unpinned package.json dependency must be refused")
	}
	for _, want := range []string{"ts-bp", "left-pad", denoLockFileName} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name %q: %s", want, err)
		}
	}
}

// TestTSBlueprintImports_NPMAliasPassesThrough: npm's own alias form
// already IS a specifier, so synthesizing one from it would produce
// "npm:foo@npm:bar@1.0.0".
func TestTSBlueprintImports_NPMAliasPassesThrough(t *testing.T) {
	dir := t.TempDir()
	writePackageJSON(t, dir, `{"dependencies":{"foo":"npm:bar@1.0.0"}}`)
	writeDenoLock(t, dir)

	got, err := tsBlueprintImports(dir, "ts-bp")
	if err != nil {
		t.Fatal(err)
	}
	if got.Imports["foo"] != "npm:bar@1.0.0" {
		t.Errorf("an alias must pass through, got %q", got.Imports["foo"])
	}
}

// TestTSBlueprintImports_FileDependencyInsideResolves: a file: dependency
// names a path exactly as a deno.json relative target does, so it is
// local and needs no lock and no fetch.
func TestTSBlueprintImports_FileDependencyInsideResolves(t *testing.T) {
	dir := t.TempDir()
	writeNPMPackage(t, filepath.Join(dir, "deps", "helper"), "helper", "entry.js")
	writePackageJSON(t, dir, `{"dependencies":{"helper":"file:./deps/helper"}}`)

	got, err := tsBlueprintImports(dir, "ts-bp")
	if err != nil {
		t.Fatalf("a file: dependency inside the blueprint must resolve: %v", err)
	}
	want := "file://" + filepath.ToSlash(filepath.Join(dir, "deps", "helper", "entry.js"))
	if got.Imports["helper"] != want {
		t.Fatalf("imports[helper] = %q, want the package's own entry FILE %q", got.Imports["helper"], want)
	}
	// A vendored dependency is already present, so requiring a lock for
	// it would refuse a blueprint that needs no network whatsoever.
	if got.NeedsPrefetch {
		t.Error("a file: dependency must not trigger a fetch pass")
	}
}

// TestTSBlueprintImports_FileDependencyOutsideIsRefused is the wrinkle
// that makes file: unlike a deno.json target.
//
// An npm file: usually points outside the declaring package, which is
// the whole reason to use one. Packaging walks the blueprint directory
// only, so that target never travelled and the pulled blueprint carries
// a reference to something that is not there. Verified: packaging a
// blueprint with `file:../sibling` produces an archive holding the
// package.json and not the sibling.
func TestTSBlueprintImports_FileDependencyOutsideIsRefused(t *testing.T) {
	parent := t.TempDir()
	writeNPMPackage(t, filepath.Join(parent, "sibling"), "sibling", "index.js")
	dir := filepath.Join(parent, "bp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePackageJSON(t, dir, `{"dependencies":{"sibling":"file:../sibling"}}`)

	_, err := tsBlueprintImports(dir, "ts-bp")
	if err == nil {
		t.Fatal("a file: dependency pointing outside the blueprint must be refused: it did not travel")
	}
	if !strings.Contains(err.Error(), "never travelled") {
		t.Errorf("refusal should say the target did not travel, got: %s", err)
	}
	// And it must NOT be reported as an unpinned registry dependency,
	// which would send the reader after a lock that cannot help.
	if strings.Contains(err.Error(), denoLockFileName) {
		t.Errorf("an outside file: is not a registry dependency: %s", err)
	}
}

// TestTSBlueprintImports_ExportsMapIsRefused: npm's "exports" supports
// conditional resolution and subpath patterns. A partial implementation
// would resolve some packages to the WRONG file rather than failing,
// which is worse than not resolving them.
func TestTSBlueprintImports_ExportsMapIsRefused(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "deps", "fancy")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	writePackageJSON(t, pkg, `{"name":"fancy","exports":{".":{"import":"./esm.js","require":"./cjs.js"}}}`)
	writePackageJSON(t, dir, `{"dependencies":{"fancy":"file:./deps/fancy"}}`)

	_, err := tsBlueprintImports(dir, "ts-bp")
	if err == nil {
		t.Fatal("an exports map must be refused rather than guessed")
	}
	if !strings.Contains(err.Error(), "exports") {
		t.Errorf("refusal should name the exports map: %s", err)
	}
}

// TestTSBlueprintImports_BothFormatsMerge: a blueprint may carry both,
// and deno.json wins a collision because it is the format Deno itself
// prefers and a blueprint carrying both has usually adopted it.
func TestTSBlueprintImports_BothFormatsMerge(t *testing.T) {
	dir := t.TempDir()
	writeNPMPackage(t, filepath.Join(dir, "deps", "shared"), "shared", "npm.js")
	writePackageJSON(t, dir, `{"dependencies":{"shared":"file:./deps/shared","onlynpm":"file:./deps/shared"}}`)
	if err := os.WriteFile(filepath.Join(dir, "viadeno.ts"), []byte("export const y = 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTSBlueprintConfig(t, dir, `{"imports":{"shared":"./viadeno.ts","onlydeno":"./viadeno.ts"}}`)

	got, err := tsBlueprintImports(dir, "ts-bp")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got.Imports["shared"], "viadeno.ts") {
		t.Errorf("deno.json should win a collision, got %q", got.Imports["shared"])
	}
	if got.Imports["onlynpm"] == "" || got.Imports["onlydeno"] == "" {
		t.Errorf("entries unique to each format must both survive: %v", got.Imports)
	}
}

// TestTSBlueprintImports_NoPackageJSON keeps the common case free.
func TestTSBlueprintImports_NoPackageJSON(t *testing.T) {
	got, err := tsBlueprintImports(t.TempDir(), "ts-bp")
	if err != nil || len(got.Imports) != 0 {
		t.Fatalf("a blueprint with neither config should resolve nothing: %+v, %v", got, err)
	}
}

// TestTSDeclaresNPMDeps decides whether `ubx blueprint package` makes a
// network call, so what it says yes to is worth pinning directly.
func TestTSDeclaresNPMDeps(t *testing.T) {
	t.Run("a vendored blueprint needs no network", func(t *testing.T) {
		dir := t.TempDir()
		writeNPMPackage(t, filepath.Join(dir, "deps", "helper"), "helper", "entry.js")
		writePackageJSON(t, dir, `{"dependencies":{"helper":"file:./deps/helper"}}`)
		if err := os.WriteFile(filepath.Join(dir, "bp.ts"), []byte("export const x = 1;\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if tsDeclaresNPMDeps(dir) {
			t.Error("a file: dependency must not make packaging reach the network")
		}
	})

	t.Run("a blueprint generated by ubx needs no network", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "bp.ts"), []byte("export const x = 1;\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if tsDeclaresNPMDeps(dir) {
			t.Error("a blueprint with no declaration at all must not reach the network")
		}
	})

	t.Run("an npm dependency does", func(t *testing.T) {
		dir := t.TempDir()
		writePackageJSON(t, dir, `{"dependencies":{"left-pad":"1.3.0"}}`)
		if !tsDeclaresNPMDeps(dir) {
			t.Error("an npm dependency is exactly what the lock is generated for")
		}
	})
}

// TestTSBlueprintImports_NPMSubpathsResolve is a regression test for a
// bug the first fixture could not have caught.
//
// An import map matches a bare specifier EXACTLY. Mapping only
// "@ubx/sdk-aws" leaves "@ubx/sdk-aws/aws/sqs/queue" unmatched, so deno
// falls through to package.json resolution and demands a node_modules
// tree that is not there:
//
//	Could not resolve "@ubx/sdk-aws/aws/sqs/queue", but found it in a
//	package.json. Deno expects the node_modules/ directory to be up to
//	date.
//
// Which is the ORIGINAL error this whole change set exists to remove, so
// it looked like the fetch pass was not running at all.
//
// It was missed because the fixture imported left-pad, a package with no
// subpaths: the one shape that cannot show this. Importing a package by
// a subpath is the normal way to use a generated SDK, so this is the
// common case rather than an edge.
func TestTSBlueprintImports_NPMSubpathsResolve(t *testing.T) {
	t.Run("from package.json", func(t *testing.T) {
		dir := t.TempDir()
		writePackageJSON(t, dir, `{"dependencies":{"@ubx/sdk-aws":"^1.2.0"}}`)
		writeDenoLock(t, dir)

		got, err := tsBlueprintImports(dir, "bp")
		if err != nil {
			t.Fatal(err)
		}
		if got.Imports["@ubx/sdk-aws"] != "npm:@ubx/sdk-aws@^1.2.0" {
			t.Errorf("bare specifier = %q", got.Imports["@ubx/sdk-aws"])
		}
		if got.Imports["@ubx/sdk-aws/"] != "npm:/@ubx/sdk-aws@^1.2.0/" {
			t.Fatalf("subpath prefix = %q, want \"npm:/@ubx/sdk-aws@^1.2.0/\"", got.Imports["@ubx/sdk-aws/"])
		}
	})

	t.Run("from deno.json", func(t *testing.T) {
		dir := t.TempDir()
		writeTSBlueprintConfig(t, dir, `{"imports":{"pkg":"npm:date-fns@3.6.0"}}`)
		writeDenoLock(t, dir)

		got, err := tsBlueprintImports(dir, "bp")
		if err != nil {
			t.Fatal(err)
		}
		if got.Imports["pkg/"] != "npm:/date-fns@3.6.0/" {
			t.Errorf("subpath prefix = %q", got.Imports["pkg/"])
		}
	})
}

// TestNPMPrefixTarget pins the exact spelling, because it is easy to get
// subtly wrong and deno rejects the whole import map rather than ignoring
// a bad entry. The leading slash after the scheme is required.
func TestNPMPrefixTarget(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"npm:date-fns@3.6.0", "npm:/date-fns@3.6.0/"},
		{"npm:@scope/pkg@^1.0.0", "npm:/@scope/pkg@^1.0.0/"},
		// Already a prefix form: nothing emits this, but a hand-written
		// deno.json legitimately could, and doubling the slash or the
		// suffix would produce an entry that matches nothing.
		{"npm:/date-fns@3.6.0/", "npm:/date-fns@3.6.0/"},
		// Not npm, so there is no prefix form to emit.
		{"jsr:@std/encoding@1", ""},
		{"file:///x/y.ts", ""},
		{"npm:", ""},
	} {
		if got := npmPrefixTarget(tc.in); got != tc.want {
			t.Errorf("npmPrefixTarget(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestTSBlueprintImports_LocalPrefixMappingKeepsItsSlash is the same
// class of bug one type over: a trailing slash makes an import-map entry
// a PREFIX mapping, and filepath.Clean eats it.
func TestTSBlueprintImports_LocalPrefixMappingKeepsItsSlash(t *testing.T) {
	dir := t.TempDir()
	writeTSBlueprintConfig(t, dir, `{"imports":{"lib/":"./lib/"}}`)

	got, err := tsBlueprintImports(dir, "bp")
	if err != nil {
		t.Fatal(err)
	}
	want := "file://" + filepath.ToSlash(filepath.Join(dir, "lib")) + "/"
	if got.Imports["lib/"] != want {
		t.Fatalf("imports[lib/] = %q, want %q -- a prefix mapping that loses its slash matches only the bare specifier", got.Imports["lib/"], want)
	}
}

// TestTSBlueprintImports_RuntimeIsNeverResolved is the duplicate-runtime
// bug, at the place that caused it.
//
// An npm-authored blueprint MUST declare @ubx/sdk in its package.json: it
// cannot install or type-check without it. ubx was turning that
// declaration into an npm: entry in the blueprint's own scope, which
// beats the top-level entry pointing at the embedded runtime. The
// blueprint then ran against a second copy of the runtime, with a second
// collector, and failed on its first resource():
//
//	Error: resource() called outside of an active stack() evaluation.
//
// Skipped silently rather than refused, because declaring it is correct.
// Refusing would refuse every correctly authored blueprint.
func TestTSBlueprintImports_RuntimeIsNeverResolved(t *testing.T) {
	dir := t.TempDir()
	writePackageJSON(t, dir, `{"dependencies":{"@ubx/sdk":"1.0.3","left-pad":"1.3.0"}}`)
	writeDenoLock(t, dir)

	got, err := tsBlueprintImports(dir, "bp")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Imports[tseval.RuntimeSpecifier]; ok {
		t.Errorf("the runtime must not be resolved for a blueprint, got %q", got.Imports[tseval.RuntimeSpecifier])
	}
	if _, ok := got.Imports[tseval.RuntimeSpecifier+"/"]; ok {
		t.Error("nor its subpath prefix")
	}
	// Its neighbours are unaffected: this is one specifier, not a mode.
	if got.Imports["left-pad"] != "npm:left-pad@1.3.0" {
		t.Errorf("other dependencies must still resolve, got %q", got.Imports["left-pad"])
	}
	if !got.NeedsPrefetch {
		t.Error("a real npm dependency alongside it must still request the fetch pass")
	}
}

// TestTSBlueprintImports_RuntimeAloneNeedsNothing: every npm-authored
// blueprint declares the runtime, and a blueprint that declares ONLY the
// runtime needs no lock, no mirror and no network. Making it pay for any
// of those would tax the most ordinary blueprint there is.
func TestTSBlueprintImports_RuntimeAloneNeedsNothing(t *testing.T) {
	dir := t.TempDir()
	writePackageJSON(t, dir, `{"dependencies":{"@ubx/sdk":"1.0.3"}}`)

	// No deno.lock on purpose: if the runtime counted as a registry
	// dependency this would be refused as unpinned.
	got, err := tsBlueprintImports(dir, "bp")
	if err != nil {
		t.Fatalf("a blueprint declaring only the runtime must not be refused: %v", err)
	}
	if got.NeedsPrefetch {
		t.Error("declaring only the runtime must not trigger a fetch pass")
	}
	if tsDeclaresNPMDeps(dir) {
		t.Error("declaring only the runtime must not make packaging reach the network")
	}
}
