package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/blueprint"
	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/goeval"
	"github.com/ubiquex/ubiquex/hclstack"
	"github.com/ubiquex/ubiquex/provider"
	"github.com/ubiquex/ubiquex/tseval"
)

// newResolveCmd is UBI-27's resolver CLI surface: a new verb, not a flag
// on `ubx propose`. `ubx propose` has one narrow, already-established job
// -- hash an already-resolved draft proposal for a PR body's trailer -- and
// explicitly refuses anything that isn't already fully resolved; folding
// intent-file resolution into it as a mode flag would conflate two
// genuinely different operations the same way this codebase never
// conflates scan/accept/ship into one multi-purpose verb. `ubx resolve`
// instead slots into the existing pipeline exactly like `ubx scan` already
// does: it reads some input (an intent file, not live drift) and produces
// a draft proposal, which `ubx propose`/`ubx accept` then take unchanged.
//
// Unlike scan/status/ship, resolve never reads live provider state at all
// -- docs/resolver.md's own contract names "live state via
// core.StateReader" as an input, but the actual implementation (UBI-27
// session 2) never needed it: a change proposal's "before" comes from the
// ledger's own FoldState, not a fresh cloud read (that happens later, at
// ship time, the same way it already does for drift_revert). A provider
// is still launched here, for exactly one reason: its schema is what
// SchemaInspector answers Computed/Sensitive/HasType questions against.
func newResolveCmd() *cobra.Command {
	var (
		ledgerDir       string
		providerPath    string
		source          string
		providerVersion string
		out             string
		timeout         time.Duration
		knownDependents []string
		fromCode        string
	)

	cmd := &cobra.Command{
		Use:   "resolve <intent-file>",
		Short: "Resolve a typed ubx:intent/v1 file (or a TypeScript/Go/Python SDK program or a .ubx.hcl blueprint-calling file, --from-code) into a draft change proposal",
		Long: `Resolves a hand-written, machine-shaped intent file (ubx:intent/v1) into a draft
kind:"change" proposal -- creates, modifies, and destroys (docs/resolver.md).
Intra-stack references are checked against the ledger's own dependency graph (with real cycle
detection) and emitted in dependency order; cross-stack references are pinned against a neighbor
ledger's current head, activating neighbor-advance staleness for real once the proposal is accepted
(see "ubx accept"'s own pin re-verification).

--from-code <entry>.ts|.go|.py|.ubx.hcl, mutually exclusive with the positional intent-file
argument, dispatched by the entry file's own extension. .ts/.go/.py evaluate a real SDK program:
.ts through the hermetic Deno evaluator (tseval, @ubx/sdk), .go by compiling the program to a real
binary and running it under this platform's own OS-level sandbox (goeval, github.com/ubiquex/
ubx-sdk-go; sandbox-exec on macOS, bubblewrap on Linux), .py under WASI (pyeval, ubx_sdk; wasmtime
running a real CPython-WASI build -- see docs/sdk.md's own "The Go evaluator" and "The Python
evaluator: decided empirically" sections for the full account of each). .ubx.hcl is parsed, not
evaluated -- a thin, deterministic wrapper for calling blueprints in a stack (UBI-226, hclstack),
never a fourth authoring medium and never able to hold a hand-written resource: the same bytes
always compile to the same intent/v1 document, no code ever runs. Either way, the resulting
intent/v1 document, provenance-stamped with the entry file's own content hash (intent.sources:
{"kind":"document", "ref", "content_hash"}) for an SDK program, is handed to the exact same,
completely unmodified pipeline below: an SDK program or a .ubx.hcl file is just another intent/v1
producer, never a special case, regardless of which. A typed SDK program or a .ubx.hcl file has no
ambiguity to review before resolving -- it says what it says -- so --from-code resolves directly,
one command, no separate draft step.

A destroy is explicit intent only (the intent file's own top-level "destroys" list, addresses
never inferred from a resource's absence) and resolve-time orphan-protected: a destroy target
still referenced by another live resource this proposal doesn't also destroy or update is refused.
Intra-stack orphan checks are automatic, against this ledger's own history; cross-stack orphan
checks are best-effort and explicit -- pass --known-dependent (repeatable) for every neighbor
stack's ledger directory that might have pinned a cross-stack reference against a destroy target
in this stack. Omitting it doesn't mean "no dependents exist" -- it means none were checked, and
the resolved proposal records that gap honestly (resolution.inputs' own cross_stack_orphan_check
entry) rather than silently.

The result is a draft: it has no id or acceptance yet. Pipe it into "ubx propose" for a PR-body
trailer hash, or "ubx accept" directly, exactly like a proposal ubx scan generates.`,
		Args: cobra.MaximumNArgs(1),
		// resolve has no "finding" concept, the same audit outcome as
		// propose/init/version (UBI-20 exit-code contract): it either
		// resolves or it doesn't. 0 or 2 only.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: %w", err)}
			}

			if fromCode != "" && len(args) > 0 {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: --from-code and a positional intent-file argument are mutually exclusive")}
			}
			if fromCode == "" && len(args) == 0 {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: requires either an intent-file argument or --from-code <entry>.ts|.go|.py|.ubx.hcl")}
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			var intent resolver.IntentFile
			switch {
			case strings.HasSuffix(strings.ToLower(fromCode), ".ubx.hcl"):
				// UBI-226: hclstack.Parse never evaluates anything -- a
				// .ubx.hcl file is parsed, not run, so there is no
				// evaluateSDKProgram-style receipts/blueprintRefs output
				// and no StampDirectCallProvenance switch below (that
				// switch exists for a DIRECT SDK import call, Slice 2's
				// own calling convention; a .ubx.hcl file only ever
				// produces BlueprintCalls entries, expanded the same way
				// a hand-written intent/v1 file's own blueprint_calls
				// already are, a few lines down).
				parsed, err := hclstack.Parse(fromCode)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: %w", err)}
				}
				intent = *parsed
			case fromCode != "":
				canon, receipts, blueprintRefs, err := evaluateSDKProgram(ctx, fromCode)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: %w", err)}
				}
				// UBI-130: a Python program's own requirements.txt-declared
				// blueprint dependencies were already pulled+verified BEFORE
				// evaluateSDKProgram ever ran the script -- this project's
				// own "never a silent network call" discipline means every
				// one of those pulls gets a real, visible receipt line here,
				// printed before resolution even starts. Empty for every
				// other language, and for a Python program with no
				// requirements.txt.
				for _, r := range receipts {
					fmt.Fprintln(cmd.OutOrStdout(), r)
				}
				if err := json.Unmarshal(canon, &intent); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: parse evaluated intent: %w", err)}
				}
				// UBI-126: a direct SDK-import call to a blueprint's own
				// compiled function (Slice 2's own original calling
				// convention -- distinct from blueprint_calls/ExpandCalls
				// below, which only ever fires for a diagram/md call) gets
				// each of its own blueprint-produced resources' bare,
				// incomplete "sources" entry (sdk/go/runtime's own
				// PushBlueprintSource / sdk/ts/runtime's own
				// pushBlueprintSource / sdk/py/ubx_sdk's own
				// push_blueprint_source, set by generated code with zero
				// changes needed to the calling stack's own code) resolved
				// to a real "name:content_hash" ref here, one discovery
				// mechanism per language (a Go program's own module graph
				// via `go list`; a TS program's own module graph via `deno
				// info`; a Python program's own already-resolved
				// requirements.txt dependency hashes, UBI-130, needing no
				// separate discovery step at all). A no-op, and never
				// spawns a subprocess, for any program that never imports a
				// blueprint.
				switch strings.ToLower(filepath.Ext(fromCode)) {
				case ".go":
					if err := blueprint.StampDirectCallProvenance(ctx, fromCode, &intent); err != nil {
						return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: %w", err)}
					}
				case ".ts":
					if err := blueprint.StampDirectCallProvenanceTS(ctx, fromCode, &intent); err != nil {
						return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: %w", err)}
					}
				case ".py":
					if err := blueprint.StampDirectCallProvenancePy(&intent, blueprintRefs); err != nil {
						return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: %w", err)}
					}
				}
			default:
				data, err := os.ReadFile(args[0])
				if err != nil {
					return &ExitCodeError{Code: 2, Err: err}
				}
				if err := json.Unmarshal(data, &intent); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: parse intent file: %w", err)}
				}
			}

			// UBI-74 Slice 5, extended by UBI-226: a hand-written file may
			// carry blueprint_calls directly, and so, now, does a .ubx.hcl
			// file (case above) -- an SDK program's own --from-code path
			// never does, since a direct SDK-import call already happened
			// in-process by the time that program's own intent/v1 is
			// emitted. Expanded HERE, once, regardless of which medium
			// produced them, before Resolve ever sees the document -- see
			// resolver.IntentFile.BlueprintCalls's own doc comment for why
			// this is the one shared splice point.
			if err := blueprint.ExpandCalls(ctx, &intent); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: %w", err)}
			}

			// UBI-86 Part 2: applied immediately after ExpandCalls (so an
			// override can target a resource a blueprint call just
			// produced) and before Resolve ever runs (so an override's
			// own value flows through normal $ref/$cross resolution like
			// any other config value) -- see resolver.IntentFile.
			// Overrides's own doc comment for the full account.
			if err := blueprint.ApplyOverrides(&intent); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: %w", err)}
			}

			providers, err := loadResolveProviders(ctx, cmd, cfg, &providerPath, &source, &providerVersion)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: %w", err)}
			}

			ledger, closeLedger, err := openLedgerForStack(ctx, ledgerDir, intent.Stack, cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: %w", err)}
			}
			defer closeLedger()

			p, err := resolver.Resolve(ledger, providers, &intent, knownDependents)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: %w", err)}
			}

			out2 := cmd.OutOrStdout()
			// UBI-88 vocabulary sweep: "change(s)"/"terminate(s)", matching
			// the delta line and op headers everywhere else, not
			// "modify(ies)"/"destroy(s)".
			fmt.Fprintf(out2, "resolved: %s: %d create(s), %d change(s), %d terminate(s)\n",
				intent.Stack, len(p.Delta.Creates), len(p.Delta.Modifies), len(p.Delta.Destroys))

			b, err := json.MarshalIndent(p, "", "  ")
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: marshal proposal: %w", err)}
			}
			if out == "" {
				fmt.Fprintln(out2, string(b))
				return nil
			}
			if err := os.WriteFile(out, b, 0o644); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("resolve: %w", err)}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&providerPath, "provider", "", "path to the provider binary (mutually exclusive with --source)")
	cmd.Flags().StringVar(&source, "source", "", "provider source address, e.g. hashicorp/aws (mutually exclusive with --provider; requires --provider-version)")
	cmd.Flags().StringVar(&providerVersion, "provider-version", "", "explicit provider version to acquire (required with --source)")
	cmd.Flags().StringVar(&out, "out", "", "write the resolved proposal here instead of stdout")
	cmd.Flags().DurationVar(&timeout, "timeout", 120*time.Second, "timeout for launching the provider and fetching its schema, and (--from-code) evaluating the SDK program -- one shared budget for the whole command, not per sub-operation")
	cmd.Flags().StringArrayVar(&knownDependents, "known-dependent", nil,
		"ledger_dir of a neighbor stack to check for cross-stack orphan references before destroying (repeatable)")
	cmd.Flags().StringVar(&fromCode, "from-code", "", "evaluate a TypeScript (@ubx/sdk), Go (ubx-sdk-go), or Python (ubx_sdk) SDK program, or parse a .ubx.hcl blueprint-calling file, dispatched by extension, instead of reading an intent file (mutually exclusive with the positional argument)")

	return cmd
}

// evaluateSDKProgram dispatches --from-code to the language-specific
// evaluator named by entryFile's own extension -- .ts to tseval (the
// hermetic Deno evaluator), .go to goeval (compile + run under this
// platform's own OS-level sandbox), .py to pyeval (run under WASI via
// wasmtime), by way of blueprint.EvaluatePythonWithDeps (UBI-130: resolves
// any requirements.txt-declared blueprint dependencies first). All three
// return the identical canonical, provenance-stamped intent/v1 shape;
// nothing downstream of this function needs to know which language
// produced it. The returned receipt lines are non-empty only for a Python
// program that declared at least one blueprint dependency -- every other
// case returns nil, printing nothing. The returned blueprintRefs map
// (UBI-126) is non-nil only for Python -- Go/TS complete their own
// incomplete blueprint sources via a separate discovery step
// (StampDirectCallProvenance/StampDirectCallProvenanceTS, called
// directly by the two --from-code handlers below after unmarshaling,
// never through this shared dispatch function) since neither needs
// anything ELSE this function already computed the way Python's own
// already-resolved dependency hashes are.
func evaluateSDKProgram(ctx context.Context, entryFile string) (canon []byte, receipts []string, blueprintRefs map[string]string, err error) {
	switch strings.ToLower(filepath.Ext(entryFile)) {
	case ".go":
		canon, err := goeval.Evaluate(ctx, entryFile)
		return canon, nil, nil, err
	case ".py":
		return blueprint.EvaluatePythonWithDeps(ctx, entryFile)
	case ".ts":
		canon, err := tseval.Evaluate(ctx, entryFile)
		return canon, nil, nil, err
	default:
		return nil, nil, nil, fmt.Errorf("--from-code: unrecognized entry file extension %q (%s) -- expected .ts, .go, or .py", filepath.Ext(entryFile), entryFile)
	}
}

// fetchThirdpartySchema/fetchDynamicSchema are loadResolveProviders' own
// two real, swappable per-entry schema fetchers -- package-level seams
// (the same convention scandiscover.go's own newDiscoveryTaggingAPI/
// newDiscoveryStateReader already establish) so a hermetic test can
// prove the real precedence rule (a resolved [providers] entry never
// even calls fetchThirdpartySchema for the same real key) without
// launching a real provider binary or a real ubx-provider-dynamic
// checkout. Production always uses the real Acquire/Launch/Schema/Close
// sequence and the real loadDynamicProviderSchema, respectively.
var fetchThirdpartySchema = func(ctx context.Context, source, version string) (*provider.Schemas, error) {
	parsed, err := provider.ParseSource(source)
	if err != nil {
		return nil, err
	}
	result, err := provider.Acquire(ctx, parsed, version)
	if err != nil {
		return nil, fmt.Errorf("acquire provider %s@%s: %w", source, version, err)
	}
	client, err := provider.Launch(ctx, result.Path)
	if err != nil {
		return nil, fmt.Errorf("launch provider %s@%s: %w", source, version, err)
	}
	schemas, err := client.Provider.Schema(ctx)
	closeErr := client.Close()
	if err != nil {
		return nil, fmt.Errorf("fetch schema for %s@%s: %w", source, version, err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close provider %s@%s: %w", source, version, closeErr)
	}
	return schemas, nil
}

var fetchDynamicSchema = loadDynamicProviderSchema

// loadResolveProviders is `ubx resolve`'s own provider-loading logic
// (docs/resolver.md's "Amendment (UBI-43): multi-provider stacks"),
// extracted so `ubx plan` (UBI-49) can resolve any medium input through
// the identical, unmodified path rather than a second copy of it: a stack
// with a real [thirdparty_providers] and/or [providers] table in
// .ubx/config gets a genuine multi-provider set, one declared provider
// per real key, each launched to fetch its own schema; a single-provider
// stack (neither table) keeps working exactly as it always has, one
// provider launched, wrapped as the one-element case.
//
// Amendment (2026-08-20): closes the resolver preference gap the prior
// checkpoint named -- providerPool.Get already routed a [providers] key
// through a real dynamic-provider launch, but this function built its
// own declared set directly from cfg.ThirdpartyProviders, so a
// [providers] entry was never even a candidate for an unrecorded
// resource's own inference. Now iterates resolveProviderPrecedence's own
// real, precedence-resolved set (one entry per real key, [providers]
// wins on collision -- the identical real rule providerPool.Get already
// applies) instead of cfg.ThirdpartyProviders directly.
//
// Real, deliberate choice, not declaredProvidersForInference (the
// mechanism status/scanall/scanfleet/drift already share for their own
// legacy/adopted-Fleet-entry inference), flagged here rather than
// silently reused: that function's own resourceTypeSchemaInspector
// wraps StateReader.Schema's opaque map[string]any, a real, deliberate
// stub whose own doc comment says IsComputed/IsSensitive are "always-
// false stubs" because InferProvider (that function's only real caller)
// never calls either -- confirmed live, not assumed, this session: a
// real required-attribute-missing resolve test regressed to silently
// succeeding when this function was first routed through that shared
// helper, because ubx resolve's own downstream validation DOES read
// richer schema data past mere type ownership. Each real provider here
// keeps fetching a real, full *provider.Schemas via the two swappable
// fetchThirdpartySchema/fetchDynamicSchema seams below (the identical
// real package-level-var convention scandiscover.go's own
// newDiscoveryTaggingAPI/newDiscoveryStateReader already establish for
// hermetic testability) -- production always resolves a real provider;
// cli/resolve_test.go's own hermetic tests swap in fakes to prove the
// real precedence rule without launching anything real. The pool
// declaredProvidersForInference itself uses is not needed here at all.
func loadResolveProviders(ctx context.Context, cmd *cobra.Command, cfg *Config, providerPath, source, providerVersion *string) ([]resolver.DeclaredProvider, error) {
	resolved := resolveProviderPrecedence(cfg)
	if len(resolved) > 0 {
		warnIfLegacyProviderFlagsGiven(cmd)
		keys := make([]string, 0, len(resolved))
		for k := range resolved {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		var providers []resolver.DeclaredProvider
		for _, key := range keys {
			r := resolved[key]
			if r.Dynamic {
				schemas, err := fetchDynamicSchema(ctx, r.Key, r.Params)
				if err != nil {
					return nil, err
				}
				providers = append(providers, resolver.DeclaredProvider{Source: r.Key, Version: "", Schema: newSchemaInspector(schemas)})
				continue
			}
			schemas, err := fetchThirdpartySchema(ctx, r.Source, r.Version)
			if err != nil {
				return nil, err
			}
			providers = append(providers, resolver.DeclaredProvider{Source: r.Source, Version: r.Version, Schema: newSchemaInspector(schemas)})
		}
		return providers, nil
	}

	applyProviderDefaults(cmd, providerPath, source, providerVersion, cfg)
	path, _, err := resolveProviderBinary(ctx, *providerPath, *source, *providerVersion)
	if err != nil {
		return nil, err
	}
	client, err := provider.Launch(ctx, path)
	if err != nil {
		return nil, err
	}
	schemas, err := client.Provider.Schema(ctx)
	closeErr := client.Close()
	if err != nil {
		return nil, fmt.Errorf("fetch provider schema: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close provider: %w", closeErr)
	}
	return []resolver.DeclaredProvider{{Source: *source, Version: *providerVersion, Schema: newSchemaInspector(schemas)}}, nil
}
