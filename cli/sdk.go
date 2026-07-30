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

	"github.com/ubiquex/ubiquex-cli/provider"
	"github.com/ubiquex/ubiquex-cli/sdk/codegen/ir"
	gotemplate "github.com/ubiquex/ubiquex-cli/sdk/codegen/templates/go"
	tstemplate "github.com/ubiquex/ubiquex-cli/sdk/codegen/templates/ts"
)

// newSDKCmd is UBI-33/34's own CLI entry point -- a parent command, not
// a single leaf verb, matching docs/sdk.md's own naming ("ubx sdk gen")
// and leaving room for a future "ubx sdk eval"/"ubx resolve --from-code"
// sibling without renaming anything already shipped (slice 4/5, not this
// session's).
func newSDKCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sdk",
		Short: "SDK program commands (UBI-33/34): local codegen today, TypeScript evaluation later",
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
// stores section), and one file per declared provider SOURCE (never per
// resource type, and never a source/ subdirectory either) -- a real
// provider can own dozens to hundreds of types; one cohesive,
// git-reviewable file per source keeps that from becoming a file
// explosion, and a flat <out>/<source-sanitized>.ts is simpler than an
// extra directory level for no behavioral gain.
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
credentials needed -- a pure local gRPC call against the launched binary), and writes one file per provider
source under --out, in the language named by --lang ("ts" or "go"): idiomatic bindings whose generated field
map maps back to the provider's real wire attribute names at evaluation time.

Always regenerates from the exact config-pinned version's real, freshly-acquired schema -- never a stale cache
from a different version (the same provider.Acquire version-pinned cache discipline "ubx scan"/"ubx accept
--reverify-with" already trust). Generated files are meant to be committed to git like any other reviewable
generated code (docs/sdk.md); re-run this command after bumping a provider's pinned version in [providers].`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if lang != "ts" && lang != "go" {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("sdk gen: --lang must be \"ts\" or \"go\", got %q", lang)}
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

	cmd.Flags().StringVar(&out, "out", "sdk/generated", "directory to write generated bindings into (one file per declared provider source)")
	cmd.Flags().StringVar(&lang, "lang", "ts", `target language for generated bindings: "ts" or "go"`)
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "timeout for launching each provider and fetching its schema (applied per provider, not once for the whole command)")

	return cmd
}

// generateOneProvider acquires+launches one provider, fetches its real
// schema, runs it through sdk/codegen/ir + the lang-selected template,
// and writes the result to <out>/<source-sanitized>.<ts|go>. A separate
// function (not inlined into RunE) so a per-provider context.WithTimeout
// is easy to scope correctly -- each declared provider gets its own
// fresh timeout budget, never one shared budget that a slow first
// provider could eat into a fast second one's own allowance.
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

	var content, ext string
	switch lang {
	case "go":
		content, err = gotemplate.GeneratedFile(goPackageName(out), source, version, types)
		ext = ".go"
	default:
		content, err = tstemplate.GeneratedFile(source, version, types)
		ext = ".ts"
	}
	if err != nil {
		return "", 0, fmt.Errorf("%s@%s: %w", source, version, err)
	}

	path = filepath.Join(out, sanitizeSourceForFilename(source)+ext)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", 0, err
	}
	return path, len(types), nil
}

// goPackageName derives a generated Go file's own package clause from
// --out's own final path component -- every provider source generated
// into the same --out directory must share one Go package (Go's own
// one-package-per-directory rule, unlike TS where each file is its own
// module regardless of directory), so the directory name doubling as the
// package name is the natural, idiomatic default (matches this project's
// own real conformance fixture: sdk/conformance/programs/go/generated/
// -> package generated).
func goPackageName(out string) string {
	base := strings.ToLower(filepath.Base(out))
	var b strings.Builder
	for i, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9' && i > 0:
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	name := b.String()
	if name == "" || (name[0] >= '0' && name[0] <= '9') {
		return "generated"
	}
	return name
}

// sanitizeSourceForFilename turns a provider source ("hashicorp/aws")
// into a filesystem-safe, import-friendly module name
// ("hashicorp-aws") -- the exact convention docs/sdk.md's own runtime
// surface example already shows (`import ... from
// './generated/hashicorp-aws'`).
func sanitizeSourceForFilename(source string) string {
	return strings.ReplaceAll(source, "/", "-")
}
