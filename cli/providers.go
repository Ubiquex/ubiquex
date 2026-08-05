package cli

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/provider"
)

// newProvidersCmd is UBI-124's own first-class primitive: the "Dependabot,
// but for pinned Terraform provider versions" registry-check UBI-99
// already proved as hand-rolled curl+jq+sort -V, duplicated once per
// consuming GitHub Actions workflow. This ticket's own explicit design
// question -- "should this become a first-class ubx subcommand... or
// stay Actions-only boilerplate" -- is resolved here as a subcommand, for
// reasons recorded on `newProvidersCheckCmd`'s own doc comment, not
// deferred to a later session (docs/state-of-the-project's own standing
// complaint: several designs already sat unbuilt past their own ticket's
// closure -- UBI-128/129/130 all had to be recovered from a different,
// already-closed ticket's own comment thread).
func newProvidersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "providers",
		Short: "Check and update this stack's own pinned [providers] versions against the real Terraform Registry",
	}
	cmd.AddCommand(newProvidersCheckCmd())
	return cmd
}

// newProvidersCheckCmd is the actual registry-check primitive.
//
// THE REAL DESIGN DECISION (UBI-124 item 4), made here, not punted:
// build a first-class `ubx providers check` subcommand, not GitHub-
// Actions-only boilerplate. Reasoning:
//
//  1. Portability the ticket itself names as the deciding property: a
//     shell-script-in-YAML answer only works on GitHub Actions. A real Go
//     subcommand works identically from GitLab CI, a local cron job, a
//     pre-commit hook, or a human's own terminal -- zero of those get a
//     say in how UBI-99's own hand-rolled curl+jq+sort -V is invoked.
//  2. DRY, for real, not hypothetically: UBI-99's own registry-check
//     shell survives independently, hand-copied, in 12 real live repos'
//     own version-watch.yml today (docs/sdk.md's own amendment log).
//     Adding this ticket's own check as shell would have made it the
//     13th independent copy of the identical curl+jq+sort logic, in a
//     DIFFERENT repo family with a genuinely different write target
//     ([providers] in a stack's own config, not a bindings repo's own
//     VERSION file) -- the two were always going to diverge in every
//     detail except the registry call itself; centralizing that ONE
//     shared piece in Go, once, tested, is the only way "shared" means
//     anything real here.
//  3. Correctness: `provider/versions.go`'s own real semver comparison
//     (`golang.org/x/mod/semver`, with pre-releases excluded from
//     consideration) is hermetically tested (provider/versions_test.go)
//     -- shell embedded in a YAML heredoc can't be unit-tested at all,
//     which is exactly the class of bug UBI-99 hit TWICE live (a real
//     rsync self-overlap, a real flag-ordering bug) with zero unit-test
//     coverage able to catch either beforehand.
//  4. `ubx` already owns the ONE piece of information this check
//     actually needs -- .ubx/config's own real, already-parsed
//     [providers] table and its own cascade PROVENANCE (which real file
//     supplied which pin) -- through `LoadConfigResolved`, already
//     built, already tested. A shell script re-deriving "where is the
//     provider pin declared" by grepping/regexing a config file by hand
//     would be redoing real, already-solved work worse.
//
// The counter-considerations (staying Actions-only would ship faster;
// UBI-99 itself never built a subcommand for its OWN registry check) are
// real but don't outweigh the above: UBI-99's own workflow only ever
// needed to check ONE hardcoded provider for ONE repo family it fully
// controls, never asked to generalize; this ticket's own [providers]
// table can name an arbitrary number of providers, in a stack repo ubx
// has no other special relationship to, which is exactly the shape a
// reusable primitive earns its own existence for.
func newProvidersCheckCmd() *cobra.Command {
	var (
		write   bool
		timeout time.Duration
	)

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Query the real Terraform Registry for a newer version of every provider declared in [providers]",
		Long: `Reads .ubx/config's own [providers] table (the same source of truth "ubx resolve"/"ubx ship" already
read) and queries the real, unauthenticated Terraform Registry "list versions" endpoint
(https://registry.terraform.io/v1/providers/:namespace/:type/versions -- UBI-99's own proven, live-verified call
shape) for each declared provider's own latest real release, excluding pre-releases.

Exit code matches "ubx status --drift"'s own established contract (UBI-20): 0 if every pin is already current,
1 if a newer version is available for at least one provider (a real, actionable signal a CI workflow can branch
on -- never a hard failure on its own), 2 on a real error (registry unreachable, no [providers] declared, ...).

--write additionally rewrites the affected provider's own version pin, IN PLACE, in whichever real file the
cascade's own provenance names as that key's source -- a targeted substitution preserving every other line
untouched, never a full re-serialization of the file (which would silently drop comments/formatting). Never
commits, never opens a PR, never touches git at all -- that stays the caller's own job (a real GitHub Actions
workflow, a human, or any other automation), matching every other UBX write path's own "the tool does the one
well-scoped thing, the caller owns the surrounding workflow" posture.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := LoadConfigResolved(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("providers check: %w", err)}
			}
			if len(rc.Config.Providers) == 0 {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("providers check: no [providers] declared in .ubx/config -- declare at least one source to check")}
			}

			var versionsOpts []provider.VersionsOption
			if base := os.Getenv("UBX_REGISTRY_VERSIONS_API_BASE"); base != "" {
				// Test-only seam (same convention as UBX_PROVIDER_MIRROR/
				// UBX_GITHUB_API_BASE_URL): points the registry query at
				// something other than the real registry.terraform.io, so
				// tests never make a real network call.
				versionsOpts = append(versionsOpts, provider.WithVersionsAPIBase(base))
			}

			var anyNewer bool
			for _, source := range sortedProviderSources(rc.Config.Providers) {
				pinned := rc.Config.Providers[source]

				parsed, err := provider.ParseSource(source)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("providers check: %w", err)}
				}

				ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
				latest, err := provider.LatestVersion(ctx, parsed, versionsOpts...)
				cancel()
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("providers check: %s: %w", source, err)}
				}

				if latest == pinned {
					fmt.Fprintf(cmd.OutOrStdout(), "%s: %s is current\n", source, pinned)
					continue
				}

				anyNewer = true
				fmt.Fprintf(cmd.OutOrStdout(), "%s: %s -> %s available\n", source, pinned, latest)

				if write {
					key := "providers." + strconv.Quote(source)
					file, ok := rc.Provenance[key]
					if !ok {
						return &ExitCodeError{Code: 2, Err: fmt.Errorf("providers check: --write: no provenance recorded for %s -- this shouldn't happen for an already-loaded provider pin", source)}
					}
					if err := bumpProviderVersion(file, source, pinned, latest); err != nil {
						return &ExitCodeError{Code: 2, Err: fmt.Errorf("providers check: --write: %w", err)}
					}
					fmt.Fprintf(cmd.OutOrStdout(), "%s: wrote %s -> %s in %s\n", source, pinned, latest, file)
				}
			}

			if anyNewer {
				return &ExitCodeError{Code: 1, Err: fmt.Errorf("providers check: a newer version is available for at least one provider (see above)")}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&write, "write", false, "rewrite each newer-version provider's own pin in place, in the real file its cascade provenance names -- never commits, never opens a PR")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "timeout for querying the registry, applied per provider, not once for the whole command")

	return cmd
}

// providerPinPattern matches one provider's own "<source>" = "<version>"
// line inside a real .ubx/config cascade file -- the exact shape every
// real config in this project's own history writes (a quoted key, "=",
// a quoted value), regardless of whether the file calls itself
// config.hcl, config.toml, or config (all three are read through the
// same genericTree parse, and every one of them renders this same
// quoted-key/quoted-value shape for a provider source, since a provider
// source string is never a bare identifier -- joinProvenancePath's own
// reasoning, configcascade.go).
func providerPinPattern(source, currentVersion string) *regexp.Regexp {
	keyPat := regexp.QuoteMeta(strconv.Quote(source))
	valPat := regexp.QuoteMeta(strconv.Quote(currentVersion))
	return regexp.MustCompile(`(` + keyPat + `\s*=\s*)` + valPat)
}

// bumpProviderVersion rewrites source's own version pin from
// currentVersion to newVersion inside file, a targeted single-line
// substitution (providerPinPattern, above) -- never a full re-parse/
// re-serialize of the file, which would silently drop comments/
// formatting genericTree's own merge already discards. Refuses loudly,
// changing nothing, if the expected line isn't found exactly once --
// either the file has already changed since LoadConfigResolved read it,
// or (a real, if unlikely, edge) the same source+version pair appears
// more than once in the same file.
func bumpProviderVersion(file, source, currentVersion, newVersion string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read %s: %w", file, err)
	}
	pattern := providerPinPattern(source, currentVersion)
	matches := pattern.FindAllStringIndex(string(data), -1)
	switch len(matches) {
	case 0:
		return fmt.Errorf("%s: expected line %q = %q not found -- has the file changed since it was read?", file, source, currentVersion)
	case 1:
		// exactly one match, the expected case -- proceed.
	default:
		return fmt.Errorf("%s: %q = %q appears %d times -- refusing to guess which one to rewrite", file, source, currentVersion, len(matches))
	}

	newData := pattern.ReplaceAllString(string(data), `${1}`+strconv.Quote(newVersion))
	info, err := os.Stat(file)
	if err != nil {
		return fmt.Errorf("stat %s: %w", file, err)
	}
	if err := os.WriteFile(file, []byte(newData), info.Mode()); err != nil {
		return fmt.Errorf("write %s: %w", file, err)
	}
	return nil
}
