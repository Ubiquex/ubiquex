package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
// UBI-98 (2026-08-03): --lang go's own --out semantics changed --
// confirmed, not assumed, that the flat single-file shape above
// literally cannot compile at real full-provider scale (a genuine Go
// compiler crash, "internal compiler error: NewBulk too big" --
// unrelated to naming, a hard ceiling on how much can live in one
// package). --lang go now writes a REPO-SHAPED tree instead:
// <out>/<source-sanitized>/ with its own go.mod stub (module
// github.com/ubiquex/ubx-sdk-<shortName>) and one package per derived
// AWS-service boundary (ir.ServiceAndLocalName -- iam/, ecr/, sqs/, ...),
// one file per resource type within its own service package, the type
// names themselves dropping the redundant Aws<Service> prefix
// (ecr.Repository, never generated.AwsEcrRepository) since the import
// path already encodes provider+service. --lang ts/py are UNCHANGED
// (still one flat file) -- restructuring them the same way is real,
// separately-scoped follow-up work, not done this session (STATE.md).
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

--lang go writes a repo-shaped tree per provider source: --out/<source-sanitized>/, its own go.mod stub, one
package per derived AWS-service boundary (iam/, ecr/, sqs/, ...), one file per resource type -- required so a
full-provider binding actually compiles (a flat single-file/single-package shape hits a real Go compiler ceiling
at real full-provider scale). --lang ts/py still write one flat file per provider source under --out.

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

	cmd.Flags().StringVar(&out, "out", "sdk/generated", `directory to write generated bindings into -- one file per declared provider source for --lang ts/py; a repo-shaped tree (own go.mod, one package per AWS-service boundary) per provider source for --lang go`)
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

	if lang == "go" {
		path, err := generateGoProviderRepo(out, source, version, types)
		if err != nil {
			return "", 0, fmt.Errorf("%s@%s: %w", source, version, err)
		}
		return path, len(types), nil
	}

	var content, ext, filenameStem string
	switch lang {
	case "py":
		content, err = pytemplate.GeneratedFile(source, version, types)
		ext = ".py"
		// Python module names can't contain a hyphen ("import
		// hashicorp-aws" is a SyntaxError) -- underscores, not the
		// hyphenated convention TS/Go both use, so the generated file is
		// actually importable by name.
		filenameStem = strings.ReplaceAll(sanitizeSourceForFilename(source), "-", "_")
	default:
		content, err = tstemplate.GeneratedFile(source, version, types)
		ext = ".ts"
		filenameStem = sanitizeSourceForFilename(source)
	}
	if err != nil {
		return "", 0, fmt.Errorf("%s@%s: %w", source, version, err)
	}
	// UBI-96: a flat package/module can produce a broken package-level
	// naming collision (two different resource types' own generated names
	// coincide -- see each template's own CheckNoDuplicateDeclarations doc
	// comment for the full account, including why this is a possibly-
	// SILENT interface merge in TS, and a fully silent module-namespace
	// overwrite in Python) -- checked here, before ever writing the file,
	// not just caught in a test after the fact.
	var selfCheckErr error
	switch lang {
	case "py":
		selfCheckErr = pytemplate.CheckNoDuplicateDeclarations(content)
	default:
		selfCheckErr = tstemplate.CheckNoDuplicateDeclarations(content)
	}
	if selfCheckErr != nil {
		return "", 0, fmt.Errorf("%s@%s: generated output failed self-check: %w", source, version, selfCheckErr)
	}

	path = filepath.Join(out, filenameStem+ext)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", 0, err
	}
	return path, len(types), nil
}

// generateGoProviderRepo writes UBI-98's own repo-shaped Go output tree
// for one provider source: <out>/<source-sanitized>/ containing its own
// go.mod stub (module github.com/ubiquex/ubx-sdk-<shortName>) and one
// package per derived AWS-service boundary (gotemplate.GeneratedRepo).
// Returns the provider's own repo root directory (the CLI's own
// "generated N resource type(s) ... -> <path>" message names this
// directory, not a single file, since --lang go no longer writes one).
func generateGoProviderRepo(out, source, version string, types []*ir.ResourceType) (string, error) {
	files, err := gotemplate.GeneratedRepo(providerShortName(source), source, version, types)
	if err != nil {
		return "", err
	}
	// UBI-96's own defense-in-depth discipline, unchanged in spirit,
	// updated for UBI-98's own multi-file-per-package shape: refuse to
	// WRITE a tree with a real package-level collision, checked across
	// every service package at once, rather than only ever catching this
	// in a test after the fact.
	if err := gotemplate.CheckRepoNoDuplicateDeclarations(files); err != nil {
		return "", fmt.Errorf("generated repo failed self-check: %w", err)
	}

	repoDir := filepath.Join(out, sanitizeSourceForFilename(source))
	for relPath, content := range files {
		fullPath := filepath.Join(repoDir, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			return "", err
		}
	}
	return repoDir, nil
}

// providerShortName derives a declared provider source's own short name
// for its generated repo's Go module path (github.com/ubiquex/ubx-sdk-<shortName>)
// -- the founder's own worked example on UBI-98 (github.com/ubiquex/ubx-sdk-aws
// for "hashicorp/aws"): the source's own last "/"-separated segment,
// mechanically, never a hand-curated friendlier rename (e.g. "google" ->
// "gcp") -- this session only ever generates against, and verifies
// against, the real hashicorp/aws provider; inventing an unverified
// rename table for providers this session has no way to test against
// would be exactly the kind of un-verified assumption this project's own
// standing discipline refuses to ship.
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
