package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/provider"
	"github.com/ubiquex/ubiquex/sdk/codegen/ir"
	gotemplate "github.com/ubiquex/ubiquex/sdk/codegen/templates/go"
	pytemplate "github.com/ubiquex/ubiquex/sdk/codegen/templates/py"
	tstemplate "github.com/ubiquex/ubiquex/sdk/codegen/templates/ts"
)

// newSDKCmd is UBI-33/34's own CLI entry point -- a parent command, not
// a single leaf verb, matching docs/sdk.md's own naming ("ubx sdk gen")
// and leaving room for a future "ubx sdk eval"/"ubx resolve --from-code"
// sibling without renaming anything already shipped (slice 4/5, not this
// session's).
func newSDKCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sdk",
		Short: "SDK program commands: local codegen today, TypeScript evaluation later",
	}
	cmd.AddCommand(newSDKGenCmd())
	return cmd
}

// newSDKGenCmd is docs/sdk.md's own "ubx sdk gen: local, pinned,
// offline-after-generation" design, built for real this session (slice
// 2). Deliberately has no legacy single-provider (--provider/--source)
// fallback the way "ubx resolve"/"ubx ship" still carry for pre-UBI-43
// compatibility -- codegen is a UBI-33/34-era feature, introduced after
// multi-provider stacks already landed, so there is no legacy shape to
// stay compatible with; requiring a real [providers] table is a real,
// deliberate scope decision, not an oversight.
//
// Two CLI details docs/sdk.md's own "Out of scope" list named as
// "deliberately left to the session that builds it" are decided here,
// for real, not still open: --out defaults to sdk/generated (a sibling
// of ledger/ and dialogues/, matching those authoring-medium
// directories' own top-level placement -- docs/architecture.md's Ledger
// stores section), and one file per declared provider SOURCE for --lang
// ts/py -- a real provider can own dozens to hundreds of types; one
// cohesive, git-reviewable file per source keeps that from becoming a
// file explosion, and a flat <out>/<source-sanitized>.ts is simpler than
// an extra directory level for no behavioral gain.
//
// UBI-98 (2026-08-03/2026-08-04, two sessions): --out's own semantics
// changed for all three languages -- confirmed, not assumed, that Go's
// flat single-file shape above literally cannot compile at real
// full-provider scale (a genuine Go compiler crash, "internal compiler
// error: NewBulk too big" -- unrelated to naming, a hard ceiling on how
// much can live in one package). All three --lang values now write a
// REPO-SHAPED tree per provider source instead: <out>/<source-sanitized>/
// with its own manifest stub (go.mod for Go, package.json for TS,
// pyproject.toml for Python) and one directory/package per derived
// AWS-service boundary (ir.ServiceAndLocalName -- iam/, ecr/, sqs/, ...),
// one file per resource type within its own service directory, the type
// names themselves dropping the redundant Aws<Service> prefix
// (ecr.Repository, never generated.AwsEcrRepository) since the directory
// already encodes provider+service. TS/Python were NOT restructured for
// the compile-crash reason Go was -- confirmed live, not assumed, that
// neither `deno check` nor a real Python import chokes on even the worst
// real single-type case (aws_wafv2_web_acl_rule) -- but were restructured
// the same way anyway for consistency and reviewability (STATE.md has
// the full account of what's genuinely language-specific -- Python's own
// real "lambda" keyword collision, TS needing no directory-name escaping
// at all -- versus what's shared).
func newSDKGenCmd() *cobra.Command {
	var (
		out     string
		lang    string
		timeout time.Duration
	)

	cmd := &cobra.Command{
		Use:   "gen",
		Short: "Generate typed bindings for every provider declared in [providers] -- local, never published",
		Long: `Reads .ubx/config's own [providers] table (the same source of truth "ubx resolve"/"ubx ship" already read),
acquires each declared provider's real binary at its pinned version, dumps its real schema (no Configure, no
credentials needed -- a pure local gRPC call against the launched binary), and writes idiomatic bindings under
--out in the language named by --lang ("ts", "go", or "py") whose generated field map maps back to the provider's
real wire attribute names at evaluation time.

Every --lang writes a repo-shaped tree per provider source: --out/<source-sanitized>/sdk/<lang>/, its own manifest
stub (go.mod / package.json / pyproject.toml), one directory per derived AWS-service boundary (iam/, ecr/, sqs/,
...), one file per resource type, the redundant Aws<Service> prefix dropped from every generated identifier.
Each template self-namespaces under its own "sdk/<lang>/" (UBI-138: the real Pulumi precedent, pulumi-aws's own
sdk/go/, sdk/python/, sdk/nodejs/) so generating multiple languages against the same --out never interleaves
their manifests/source trees into one directory.

Always regenerates from the exact config-pinned version's real, freshly-acquired schema -- never a stale cache
from a different version (the same provider.Acquire version-pinned cache discipline "ubx scan"/"ubx accept
--reverify-with" already trust). Generated files are meant to be committed to git like any other reviewable
generated code (docs/sdk.md); re-run this command after bumping a provider's pinned version in [providers].`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if lang != "ts" && lang != "go" && lang != "py" {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("sdk gen: --lang must be \"ts\", \"go\", or \"py\", got %q", lang)}
			}

			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("sdk gen: %w", err)}
			}
			if len(cfg.Providers) == 0 {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("sdk gen: no [providers] declared in .ubx/config -- ubx sdk gen has no legacy single-provider fallback (codegen is a multi-provider-stacks-era feature); declare at least one source in [providers]")}
			}

			if err := os.MkdirAll(out, 0o755); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("sdk gen: %w", err)}
			}

			for _, source := range sortedProviderSources(cfg.Providers) {
				version := cfg.Providers[source]

				path, count, err := generateOneProvider(cmd.Context(), timeout, source, version, out, lang)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("sdk gen: %w", err)}
				}
				fmt.Fprintf(cmd.OutOrStdout(), "generated %d resource type(s) for %s@%s -> %s\n", count, source, version, path)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&out, "out", "sdk/generated", `directory to write generated bindings into -- a repo-shaped tree (own manifest, one directory per AWS-service boundary) per declared provider source, under <out>/<source-sanitized>/sdk/<lang>/`)
	cmd.Flags().StringVar(&lang, "lang", "ts", `target language for generated bindings: "ts", "go", or "py"`)
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "timeout for launching each provider and fetching its schema (measured per provider, not once for the whole command)")

	return cmd
}

// generateOneProvider acquires+launches one provider, fetches its real
// schema, and runs it through sdk/codegen/ir + the lang-selected
// template. --lang go writes UBI-98's own repo-shaped tree
// (<out>/<source-sanitized>/, its own go.mod, one package per derived
// AWS-service boundary -- see generateGoProviderRepo); --lang ts/py are
// UNCHANGED from UBI-33/34/35/36 (one flat <out>/<source-sanitized>.<ts|py>
// file) -- TS/Python restructuring is explicitly out of scope this
// session (STATE.md), named here rather than silently left inconsistent.
// A separate function (not inlined into RunE) so a per-provider
// context.WithTimeout is easy to scope correctly -- each declared
// provider gets its own fresh timeout budget, never one shared budget
// that a slow first provider could eat into a fast second one's own
// allowance.
func generateOneProvider(ctx context.Context, timeout time.Duration, source, version, out, lang string) (path string, resourceCount int, err error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	parsed, err := provider.ParseSource(source)
	if err != nil {
		return "", 0, err
	}
	result, err := provider.Acquire(ctx, parsed, version)
	if err != nil {
		return "", 0, fmt.Errorf("acquire provider %s@%s: %w", source, version, err)
	}
	client, err := provider.Launch(ctx, result.Path)
	if err != nil {
		return "", 0, fmt.Errorf("launch provider %s@%s: %w", source, version, err)
	}
	defer client.Close()

	schemas, err := client.Provider.Schema(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("fetch schema for %s@%s: %w", source, version, err)
	}

	resourceTypeNames := make([]string, 0, len(schemas.Resources))
	for typeName := range schemas.Resources {
		resourceTypeNames = append(resourceTypeNames, typeName)
	}
	// schemas.Resources is a Go map -- sort before walking it, the same
	// determinism discipline sortedProviderSources already applies one
	// level up (declared provider sources), now applied to the resource
	// TYPES within one provider's own schema dump.
	sort.Strings(resourceTypeNames)

	types := make([]*ir.ResourceType, 0, len(resourceTypeNames))
	for _, typeName := range resourceTypeNames {
		resType, err := ir.FromSchema(typeName, schemas.Resources[typeName])
		if err != nil {
			return "", 0, fmt.Errorf("%s@%s: %w", source, version, err)
		}
		types = append(types, resType)
	}

	// UBI-138: <out>/<source-sanitized> is the real repo-shaped output
	// root every language's own GeneratedRepo nests under (sdk/go/,
	// sdk/typescript/, sdk/python/) -- computed here, before generation,
	// so the Go path (UBI-153) can check it for a real, already-existing
	// go.mod before overwriting anything.
	repoDir := filepath.Join(out, sanitizeSourceForFilename(source))

	var files map[string]string
	var checkErr error
	switch lang {
	case "go":
		files, err = gotemplate.GeneratedRepo(providerShortName(source), source, version, types, resolveGoDirective(repoDir))
		if err == nil {
			checkErr = gotemplate.CheckRepoNoDuplicateDeclarations(files)
		}
	case "py":
		files, err = pytemplate.GeneratedRepo(providerShortName(source), source, version, types)
		if err == nil {
			checkErr = pytemplate.CheckRepoNoDuplicateDeclarations(files)
		}
	default:
		files, err = tstemplate.GeneratedRepo(providerShortName(source), source, version, types)
		if err == nil {
			checkErr = tstemplate.CheckRepoNoDuplicateDeclarations(files)
		}
	}
	if err != nil {
		return "", 0, fmt.Errorf("%s@%s: %w", source, version, err)
	}
	// UBI-96's own defense-in-depth discipline, updated for UBI-98's own
	// per-service-directory, multi-file shape: refuse to WRITE a tree
	// with a real declaration collision, checked across the whole repo at
	// once, rather than only ever catching this in a test after the
	// fact. Each template package's own CheckRepoNoDuplicateDeclarations
	// doc comment has the full, per-language account of what can and
	// cannot actually collide (Go: within one directory, since Go's
	// package namespace spans every file in it; TS/Python: within one
	// file only, since both give every file its own independent
	// namespace -- confirmed, not assumed, this session).
	if checkErr != nil {
		return "", 0, fmt.Errorf("%s@%s: generated repo failed self-check: %w", source, version, checkErr)
	}

	// UBI-138: lang is NO LONGER its own path segment here -- superseded,
	// not just changed, by each template's own GeneratedRepo now
	// self-namespacing its output under "go/"/"typescript/"/"python/"
	// (the real Pulumi-precedent sdk/{go,typescript,python}/ layout, one
	// combined repo per provider instead of one repo per (provider,
	// language) pair). That per-language prefix is what now keeps three
	// languages' own manifests (go/go.mod, typescript/package.json,
	// python/pyproject.toml) from interleaving when generated to the same
	// --out -- the original reason `lang` was added as a separate path
	// segment (see git blame for the UBI-98-era account) -- so folding it
	// back into the source-sanitized directory name is no longer the
	// collision risk it used to be; it now produces exactly the target
	// combined-repo shape directly: <out>/<source-sanitized>/go/,
	// .../typescript/, .../python/, side by side, ready to become one
	// real repo's own root.
	for relPath, content := range files {
		fullPath := filepath.Join(repoDir, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return "", 0, err
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			return "", 0, err
		}
	}
	return repoDir, len(types), nil
}

// goDirectivePattern matches a go.mod "go" directive line -- real go.mod
// files declare this as a bare top-level line, "go X.Y[.Z]" (the
// toolchain directive, "toolchain goX.Y.Z", is a real, separate,
// optional line this pattern deliberately does not match: preserving
// the wrong line here would be worse than falling back to the real
// default).
var goDirectivePattern = regexp.MustCompile(`(?m)^go (\d+\.\d+(?:\.\d+)?)\s*$`)

// resolveGoDirective is UBI-153's own real fix: reads a real,
// already-existing go.mod at repoDir's own real target path
// (repoDir/sdk/go/go.mod) if one is there, and returns its own real,
// current "go" directive value verbatim -- never a hardcoded template
// constant, which is exactly how UBI-151's own bug happened (a stale
// "go 1.23" silently downgrading a real repo already bumped to
// go 1.26.3 on every regen). Read-before-write is self-healing: whatever
// real value a repo is legitimately bumped to next (a new language
// feature, a security-driven minimum) survives the next regen
// automatically, no template edit required, matching the same "shared
// source of truth, can't drift apart" principle UBI-142/UBI-149's own
// fixes already established.
//
// Falls back to the real go toolchain that built the running ubx binary
// itself (runtime.Version(), e.g. "go1.26.3" -> "1.26.3") only when
// genuinely nothing exists to preserve (a brand-new repo, or an
// existing go.mod this pattern can't parse) -- self-updating by
// construction, never a second hardcoded constant that could itself go
// stale the same way the original bug did.
func resolveGoDirective(repoDir string) string {
	fallback := strings.TrimPrefix(runtime.Version(), "go")
	content, err := os.ReadFile(filepath.Join(repoDir, "sdk", "go", "go.mod"))
	if err != nil {
		return fallback
	}
	m := goDirectivePattern.FindSubmatch(content)
	if m == nil {
		return fallback
	}
	return string(m[1])
}

// providerShortName derives a declared provider source's own short name
// for its generated repo's own manifest (Go's module path
// github.com/ubiquex/ubx-sdk-<shortName>, TS's package.json name
// @ubx/sdk-<shortName>, Python's pyproject.toml name ubx-sdk-<shortName>)
// -- the founder's own worked example on UBI-98 (github.com/ubiquex/ubx-sdk-aws
// for "hashicorp/aws"): the source's own last "/"-separated segment,
// mechanically, never a hand-curated friendlier rename (e.g. "google" ->
// "gcp") -- this project only ever generates against, and verifies
// against, the real hashicorp/aws provider; inventing an unverified
// rename table for providers there's no way to test against would be
// exactly the kind of un-verified assumption this project's own standing
// discipline refuses to ship.
func providerShortName(source string) string {
	if idx := strings.LastIndex(source, "/"); idx >= 0 {
		return source[idx+1:]
	}
	return source
}

// sanitizeSourceForFilename turns a provider source ("hashicorp/aws")
// into a filesystem-safe, import-friendly module name
// ("hashicorp-aws") -- the exact convention docs/sdk.md's own runtime
// surface example already shows (`import ... from
// './generated/hashicorp-aws'`).
func sanitizeSourceForFilename(source string) string {
	return strings.ReplaceAll(source, "/", "-")
}
