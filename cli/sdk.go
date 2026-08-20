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
	"github.com/ubiquex/ubiquex/sdk/codegen/describe"
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
// stay compatible with; requiring a real [thirdparty_providers] table is
// a real, deliberate scope decision, not an oversight.
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
		out                string
		lang               string
		timeout            time.Duration
		dynamicProviderBin string
		describeEnabled    bool
		describeModel      string
		descriptionsDir    string
		gapsDir            string
	)

	cmd := &cobra.Command{
		Use:   "gen",
		Short: "Generate typed bindings for every provider declared in [thirdparty_providers] -- local, never published",
		Long: `Reads .ubx/config's own [thirdparty_providers] table (the same source of truth "ubx resolve"/"ubx ship" already read),
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
generated code (docs/sdk.md); re-run this command after bumping a provider's pinned version in [thirdparty_providers].`,
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
			if len(cfg.ThirdpartyProviders) == 0 && len(cfg.DynamicProviders) == 0 {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("sdk gen: no [thirdparty_providers] or [dynamic_providers.<name>] declared in .ubx/config -- ubx sdk gen has no legacy single-provider fallback (codegen is a multi-provider-stacks-era feature); declare at least one source")}
			}

			if err := os.MkdirAll(out, 0o755); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("sdk gen: %w", err)}
			}

			// checkpoint 6's own real, cost-driven redesign (STATE.md):
			// --describe (a real, live, billed Claude API call per
			// undescribed field) stays opt-in, nil Generator otherwise --
			// a plain `ubx sdk gen` must never pay that cost silently.
			// describe.New itself never touches the network (see its own
			// doc comment); a bad/missing credential only ever surfaces
			// once the first real field enrichment actually runs.
			//
			// The REAL default enrichment path is now --descriptions-dir
			// (on by default, sdk/providers/descriptions -- a real,
			// checked-in, human/Claude-Code-authored data file this
			// command reads at zero cost; a provider with no such file yet
			// just sees every field stay genuinely undescribed, the
			// honest status quo). Coverage is now always collected and
			// reported, not just when --describe is passed, since reading
			// a checked-in file is effectively free.
			var describeGen *describe.Generator
			if describeEnabled {
				describeGen = describe.New(describe.Config{Model: describeModel})
			}
			perProviderCoverage := map[string]descriptionCoverage{}
			var coverageOrder []string

			for _, source := range sortedProviderSources(cfg.ThirdpartyProviders) {
				version := cfg.ThirdpartyProviders[source]

				path, count, coverage, err := generateOneProvider(cmd.Context(), timeout, source, version, out, lang, describeGen, descriptionsDir, gapsDir, cfg.ProviderConfigs[source])
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("sdk gen: %w", err)}
				}
				fmt.Fprintf(cmd.OutOrStdout(), "generated %d resource type(s) for %s@%s -> %s\n", count, source, version, path)
				perProviderCoverage[source] = coverage
				coverageOrder = append(coverageOrder, source)
			}

			// Real, deliberate posture difference from the [thirdparty_providers] loop
			// above: a [dynamic_providers.<name>] entry failing (a real,
			// honest "this source's own schema format isn't supported yet"
			// or "this heuristic doesn't recognize this API's own create
			// convention" refusal, not a crash) does NOT stop generation
			// for every other declared entry -- confirmed live this
			// session running the real central provider config
			// (sdk/providers/.ubx/config): Azure's own real spec correctly,
			// honestly discovers zero resources (a real, named, NOT-yet-fixed
			// resourcemap gap), which would have silently prevented every
			// alphabetically-later entry (github, google, kubernetes) from
			// ever being attempted under the [thirdparty_providers] loop's own
			// fail-fast posture. A CI-matrix posture -- generate what
			// genuinely can be generated, report every real failure at the
			// end -- is the correct one for a config that deliberately
			// tracks many independent providers' own real, varying
			// progress; [thirdparty_providers] keeps its own original fail-fast
			// behavior unchanged (a real infra provider acquisition
			// failure is a different, more urgent kind of problem).
			var dynamicFailures []string
			for _, name := range sortedDynamicProviderNames(cfg.DynamicProviders) {
				params := cfg.DynamicProviders[name]

				path, count, coverage, err := generateOneDynamicProvider(cmd.Context(), timeout, name, params, out, lang, dynamicProviderBin, describeGen, descriptionsDir, gapsDir)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "sdk gen: dynamic provider %q: %v\n", name, err)
					dynamicFailures = append(dynamicFailures, name)
					continue
				}
				fmt.Fprintf(cmd.OutOrStdout(), "generated %d resource type(s) for dynamic provider %q -> %s\n", count, name, path)
				perProviderCoverage[name] = coverage
				coverageOrder = append(coverageOrder, name)
			}

			fmt.Fprint(cmd.OutOrStdout(), formatCoverageReport(perProviderCoverage, coverageOrder))

			if len(dynamicFailures) > 0 {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("sdk gen: %d of %d dynamic provider(s) failed: %s", len(dynamicFailures), len(cfg.DynamicProviders), strings.Join(dynamicFailures, ", "))}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&out, "out", "sdk/generated", `directory to write generated bindings into -- a repo-shaped tree (own manifest, one directory per AWS-service boundary) per declared provider source, under <out>/<source-sanitized>/sdk/<lang>/`)
	cmd.Flags().StringVar(&lang, "lang", "ts", `target language for generated bindings: "ts", "go", or "py"`)
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "timeout for launching each provider and fetching its schema (measured per provider, not once for the whole command)")
	cmd.Flags().StringVar(&dynamicProviderBin, "dynamic-provider-bin", "", `path to an already-built ubx-provider-dynamic binary, for [dynamic_providers.<name>] entries -- if unset, built on demand from a local checkout (UBX_PROVIDER_DYNAMIC_REPO, default "../ubx-provider-dynamic")`)
	cmd.Flags().BoolVar(&describeEnabled, "describe", false, `ALSO fill any field still undescribed after --descriptions-dir via a real, live Claude API call (sdk/codegen/describe) -- opt-in: slow, billed, needs a resolvable Anthropic credential (ANTHROPIC_API_KEY or an `+"`ant auth login`"+` profile). The interim, cost-free default is --descriptions-dir; see that flag's own doc.`)
	cmd.Flags().StringVar(&describeModel, "describe-model", "", `model for --describe (default: `+describe.DefaultModel+`)`)
	cmd.Flags().StringVar(&descriptionsDir, "descriptions-dir", defaultDescriptionsDir, `directory of real, checked-in <provider>.json description files (resource -> dotted field path -> description text) this command reads at zero cost and applies to any field a source model left undescribed, labeled "AI-inferred" in generated doc comments -- the real default enrichment path; pass "" to disable entirely`)
	cmd.Flags().StringVar(&gapsDir, "list-undescribed", "", `directory to write one real <provider>.json gap file per declared source, listing every field STILL undescribed after --descriptions-dir/--describe (type, required/optional/computed, parent context, and any real enum/constraint signal found) -- the structured "what's missing" list a description-authoring pass (this session or a future one) reads; unset (default) writes nothing`)

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
// providerConfig is [provider_configs.<source>]'s own real table
// (cfg.ProviderConfigs[source], already generic map[string]any) -- the
// real, provider-agnostic home for a thirdparty provider's own
// describe_exclude list (cli/describeexclude.go), the identical real
// key/shape a [dynamic_providers.<name>] table's own params carries
// directly. nil for a source with no [provider_configs] entry at all,
// exactly like every other real per-source config value this pipeline
// already reads that way.
func generateOneProvider(ctx context.Context, timeout time.Duration, source, version, out, lang string, describeGen *describe.Generator, descriptionsDir, gapsDir string, providerConfig map[string]any) (path string, resourceCount int, coverage descriptionCoverage, err error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	parsed, err := provider.ParseSource(source)
	if err != nil {
		return "", 0, descriptionCoverage{}, err
	}
	result, err := provider.Acquire(ctx, parsed, version)
	if err != nil {
		return "", 0, descriptionCoverage{}, fmt.Errorf("acquire provider %s@%s: %w", source, version, err)
	}
	client, err := provider.Launch(ctx, result.Path)
	if err != nil {
		return "", 0, descriptionCoverage{}, fmt.Errorf("launch provider %s@%s: %w", source, version, err)
	}
	defer client.Close()

	schemas, err := client.Provider.Schema(ctx)
	if err != nil {
		return "", 0, descriptionCoverage{}, fmt.Errorf("fetch schema for %s@%s: %w", source, version, err)
	}

	// nil signalsByType: a real registry-acquired provider has no
	// OpenAPI/Smithy document at all for this codebase to have extracted
	// enum/constraint signal from in the first place -- a real, separate,
	// unaddressed limitation of THIS source, not something writeGeneratedSDK
	// can make up for.
	return writeGeneratedSDK(ctx, schemas, providerShortName(source), source, version, out, lang, describeGen, descriptionsDir, gapsDir, nil, describeExcludeFromParams(providerConfig))
}

// generateOneDynamicProvider is generateOneProvider's own real sibling for
// a [dynamic_providers.<name>] entry -- see cli/dynamicprovider.go's own
// doc comment for why this is a genuinely different launch path (no
// independent registry-acquired binary exists), sharing writeGeneratedSDK's
// own identical codegen/output logic once a real schema dump is in hand,
// so the two paths can never silently diverge in how they turn a
// provider.Schemas into generated files. version is the real, honest
// placeholder "dynamic" -- see resolveDynamicProviderBinary's own doc
// comment for why no real per-target version pin exists yet.
func generateOneDynamicProvider(ctx context.Context, timeout time.Duration, name string, params map[string]any, out, lang, dynamicProviderBin string, describeGen *describe.Generator, descriptionsDir, gapsDir string) (path string, resourceCount int, coverage descriptionCoverage, err error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	repoPath := os.Getenv("UBX_PROVIDER_DYNAMIC_REPO")
	if repoPath == "" {
		repoPath = defaultDynamicProviderRepo
	}
	binPath, err := resolveDynamicProviderBinary(dynamicProviderBin, repoPath)
	if err != nil {
		return "", 0, descriptionCoverage{}, err
	}

	schemas, err := dynamicProviderSchema(ctx, binPath, name, params)
	if err != nil {
		return "", 0, descriptionCoverage{}, err
	}

	// Real enum/constraint signal (ubx-provider-dynamic's own
	// internal/schema.CollectSignals, dumped via --dump-signals) is
	// genuinely supplementary context, not required for a correct
	// generation -- a failure here degrades gracefully (a warning, an
	// empty signal set) rather than failing the whole provider, matching
	// this codebase's own "skip, don't fail" discipline.
	signalsByType, sigErr := dynamicProviderSignals(ctx, binPath, name, params)
	if sigErr != nil {
		fmt.Fprintf(os.Stderr, "sdk gen: dynamic provider %q: signal collection failed, continuing without enum/constraint context: %v\n", name, sigErr)
		signalsByType = nil
	}

	const version = "dynamic"
	return writeGeneratedSDK(ctx, schemas, name, name, version, out, lang, describeGen, descriptionsDir, gapsDir, signalsByType, describeExcludeFromParams(params))
}

// writeGeneratedSDK is generateOneProvider's/generateOneDynamicProvider's
// own real, shared tail: schema -> sdk/codegen/ir -> lang-selected
// template -> written repo-shaped tree. shortName is the generated repo's
// own manifest name (github.com/ubiquex/ubx-sdk-<shortName>, ...);
// source/version are recorded into the generated manifests/doc comments
// verbatim, and source ALSO drives the real output directory name
// (sanitizeSourceForFilename) -- identical for a real Terraform-registry
// provider (its own real source string) and a dynamic provider (its own
// declared [dynamic_providers.<name>] name, which needs no sanitizing
// itself but shares the identical real code path rather than a parallel
// one).
// describeExclude is cli/describeexclude.go's own real, general,
// config-declared exclusion set (nil and empty both mean "nothing
// excluded"): every name in it is skipped for description generation
// only -- codegen below always receives the full, unfiltered types
// built from schemas, unchanged.
func writeGeneratedSDK(ctx context.Context, schemas *provider.Schemas, shortName, source, version, out, lang string, describeGen *describe.Generator, descriptionsDir, gapsDir string, signalsByType map[string]map[string]*fieldSignal, describeExclude map[string]bool) (path string, resourceCount int, coverage descriptionCoverage, err error) {
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
			return "", 0, descriptionCoverage{}, fmt.Errorf("%s@%s: %w", source, version, err)
		}
		// ir.FromSchema's own real "skip rather than fail the whole
		// resource" discipline (a real field/nested-block whose wire name
		// can't be safely represented in any real target language --
		// confirmed live against Kubernetes' own real
		// CustomResourceDefinition, whose real, official JSONSchemaProps
		// type embeds literal "$ref"/"$schema" field names) -- reported
		// here, not silently dropped: every skip is real, worth a human
		// noticing, even though it doesn't fail generation.
		for _, path := range resType.SkippedFields {
			fmt.Fprintf(os.Stderr, "sdk gen: %s: skipped unrepresentable field %q (unsupported character for any real target language)\n", typeName, path)
		}
		types = append(types, resType)
	}

	// checkpoint 6's own real, cost-driven redesign: fill every real
	// field FromSchema left with DescriptionSourceNone from the real,
	// checked-in descriptions file first (free, on by default), then
	// (only if --describe was passed) a real, live Claude call for
	// whatever's still left -- either way labeled AIInferred so the
	// generated code's own doc comments (below) can render the
	// founder's own required visible label. Always runs (checkedIn/gen/
	// gapsOut may all be nil/empty for a provider with no artifact and
	// no --describe/--list-undescribed yet) so coverage is always real
	// and always reportable, not just when --describe is passed.
	checkedIn, err := loadCheckedInDescriptions(descriptionsDir, shortName)
	if err != nil {
		return "", 0, descriptionCoverage{}, fmt.Errorf("%s@%s: %w", source, version, err)
	}
	var gaps map[string]map[string]gapFieldInfo
	enrichOpts := enrichOptions{checkedIn: checkedIn, gen: describeGen}
	if gapsDir != "" {
		gaps = map[string]map[string]gapFieldInfo{}
		enrichOpts.gapsOut = &gaps
	}

	// describe_exclude's own real effect: a resource named here never
	// reaches enrichDescriptions at all (no checked-in lookup, no
	// --describe call, no gap-file entry) -- codegen already ran above
	// against the full, unfiltered types, so an excluded resource still
	// generates completely normally, it just never gets a description
	// attempt. Its real field count is tallied into Excluded directly
	// (countAllFields, cli/describeexclude.go), the identical recursive
	// rule every other coverage bucket already uses.
	describeTypes, excludedTypes := partitionDescribeTypes(types, describeExclude)
	var prunedStale int
	coverage, prunedStale, err = enrichDescriptions(ctx, source, describeTypes, signalsByType, enrichOpts)
	if err != nil {
		return "", 0, coverage, fmt.Errorf("%s@%s: describe: %w", source, version, err)
	}
	for _, rt := range excludedTypes {
		coverage.Excluded += countAllFields(rt.Fields)
	}
	// Real, direct fix: a checked-in entry whose own field gained a real
	// source description since it was authored is stale (enrichDescriptions
	// already pruned it from the in-memory checkedIn map) -- persist that
	// pruned result back to the real file it came from, so the checked-in
	// artifact itself stays accurate, not just this run's own in-memory
	// state. Only touches disk when something was actually pruned; a
	// normal run changes nothing here.
	if prunedStale > 0 {
		if err := writeCheckedInDescriptions(descriptionsDir, shortName, checkedIn); err != nil {
			return "", 0, coverage, fmt.Errorf("%s@%s: write pruned checked-in descriptions: %w", source, version, err)
		}
		fmt.Fprintf(os.Stderr, "sdk gen: %s: pruned %d stale checked-in description(s) now covered by a real source description\n", source, prunedStale)
	}
	if gapsDir != "" {
		if err := writeGapFile(gapsDir, shortName, gaps); err != nil {
			return "", 0, coverage, fmt.Errorf("%s@%s: write gap file: %w", source, version, err)
		}
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
		files, err = gotemplate.GeneratedRepo(shortName, source, version, types, resolveGoDirective(repoDir))
		if err == nil {
			checkErr = gotemplate.CheckRepoNoDuplicateDeclarations(files)
		}
	case "py":
		files, err = pytemplate.GeneratedRepo(shortName, source, version, types)
		if err == nil {
			checkErr = pytemplate.CheckRepoNoDuplicateDeclarations(files)
		}
	default:
		files, err = tstemplate.GeneratedRepo(shortName, source, version, types)
		if err == nil {
			checkErr = tstemplate.CheckRepoNoDuplicateDeclarations(files)
		}
	}
	if err != nil {
		return "", 0, coverage, fmt.Errorf("%s@%s: %w", source, version, err)
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
		return "", 0, coverage, fmt.Errorf("%s@%s: generated repo failed self-check: %w", source, version, checkErr)
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
			return "", 0, coverage, err
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			return "", 0, coverage, err
		}
	}
	return repoDir, len(types), coverage, nil
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
