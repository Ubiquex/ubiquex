package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/diagram"
	"github.com/ubiquex/ubiquex/intentprovider"
	"github.com/ubiquex/ubiquex/provider"
)

// newProposeCmd serves three genuinely different pipeline stages under
// one verb name, disambiguated by --from-doc/--from-diagram:
//
//   - `ubx propose <proposal.json>` (UBI-11 stage 1, unchanged): an
//     author resolves a draft proposal and needs its canonical hash to
//     embed in a PR body's "ubx-proposal: <hash>" trailer, before anyone
//     reviews anything and long before `ubx accept --from-merge` runs.
//     Never touches a ledger -- a pure function of the file's own
//     content, same core.Hash `ubx accept`'s local tier uses internally.
//   - `ubx propose --from-doc <file>.md --stack <stack>` (UBI-41): the
//     md medium's own entry point -- transcribes a markdown authoring
//     document into an intent/v1 DRAFT via the configured [intent]
//     provider adapter and writes it out. Deliberately stops there: it
//     never resolves, never touches a ledger, matching docs/intent-
//     provider.md's own "each step in this trust chain is a deliberate
//     human checkpoint" posture -- the same reason `ubx resolve` itself
//     never auto-chains into `ubx accept`.
//   - `ubx propose --from-diagram <file>.d2 --stack <stack>` (UBI-47,
//     new): the diagram medium's own entry point -- parses a .d2
//     file's own topology into an intent/v1 draft (diagram.Parse, fully
//     deterministic, no LLM in this path at all) and writes it out. The
//     SAME two-step "draft, reviewed, then a separate ubx resolve" shape
//     as --from-doc, not the SDK's own one-step "ubx resolve --from-code"
//     -- docs/diagram-medium.md's own explicit reasoning: a diagram
//     parse CAN produce real, visible ambiguity (an uninferable or
//     ambiguous node type), deterministic though it is, so it needs the
//     same human-review gate before resolution the md medium's own
//     ambiguity does.
func newProposeCmd() *cobra.Command {
	var fromDoc, fromDiagram, stack, out, summary string
	var neighborLedgers []string
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "propose [proposal.json]",
		Short: "Compute a draft proposal's canonical hash, or transcribe a markdown document/D2 diagram into an intent/v1 draft",
		Args:  cobra.MaximumNArgs(1),
		// propose has no "finding" concept -- it either succeeds or it
		// doesn't (UBI-20 exit-code contract): 0 or 2 only.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			modes := len(args)
			if fromDoc != "" {
				modes++
			}
			if fromDiagram != "" {
				modes++
			}
			if modes > 1 {
				return &ExitCodeError{Code: 2, Err: errors.New("propose: a proposal.json argument, --from-doc, and --from-diagram are mutually exclusive")}
			}
			if fromDoc != "" {
				return runProposeFromDoc(cmd, fromDoc, stack, out, timeout)
			}
			if fromDiagram != "" {
				return runProposeFromDiagram(cmd, fromDiagram, stack, out, summary, neighborLedgers, timeout)
			}
			if len(args) != 1 {
				return &ExitCodeError{Code: 2, Err: errors.New("propose: exactly one of a proposal.json argument, --from-doc, or --from-diagram is required")}
			}

			data, err := os.ReadFile(args[0])
			if err != nil {
				return &ExitCodeError{Code: 2, Err: err}
			}

			var p core.Proposal
			if err := json.Unmarshal(data, &p); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("parse proposal: %w", err)}
			}
			if p.ID != "" || p.Acceptance != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("propose: this proposal already has an id/acceptance -- propose is for drafts only")}
			}
			if err := core.Validate(&p); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("propose: %w", err)}
			}

			hash, err := core.Hash(&p)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("propose: %w", err)}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "ubx-proposal: %s\n", hash)
			return nil
		},
	}

	cmd.Flags().StringVar(&fromDoc, "from-doc", "", "path to a markdown authoring document -- transcribes it into an intent/v1 draft via the configured [intent] provider")
	cmd.Flags().StringVar(&fromDiagram, "from-diagram", "", "path to a .d2 diagram -- transcribes its topology into an intent/v1 draft")
	cmd.Flags().StringVar(&stack, "stack", "", "target stack name (required with --from-doc or --from-diagram)")
	cmd.Flags().StringVar(&out, "out", "", "write the intent/v1 draft here instead of stdout (only with --from-doc or --from-diagram)")
	cmd.Flags().StringVar(&summary, "summary", "", "intent.summary for the draft (only with --from-diagram -- a diagram has no prose to derive one from; defaults to a generated summary naming the stack and resource count if omitted)")
	cmd.Flags().StringArrayVar(&neighborLedgers, "neighbor-ledger", nil, "<stack>=<path> mapping a diagram's own cross-stack reference to a real ledger directory, overriding the \"../<stack>\" convention (repeatable, only with --from-diagram)")
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Minute, "timeout for the intent provider's own drafting round trip (only with --from-doc), or for provider acquisition/schema fetch (only with --from-diagram)")
	return cmd
}

// runProposeFromDoc is `ubx propose --from-doc`'s own RunE body
// (docs/intent-provider.md's own md-pipeline design): draft via
// draftFromDoc, render the ambiguity content for a human to review, and
// write the draft. Never resolves, never touches a ledger.
func runProposeFromDoc(cmd *cobra.Command, docPath, stack, out string, timeout time.Duration) error {
	rc, err := LoadConfigResolved(cmd.ErrOrStderr())
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("propose --from-doc: %w", err)}
	}
	cfg := rc.Config
	applyStackDefault(cmd, &stack, cfg)
	if stack == "" {
		return &ExitCodeError{Code: 2, Err: stackRequiredError("propose --from-doc", rc.Files)}
	}

	draft, err := draftFromDoc(cmd, cfg, docPath, stack, timeout)
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("propose --from-doc: %w", err)}
	}

	outWriter := cmd.OutOrStdout()
	renderAmbiguity(outWriter, newStyler(cmd), draft)

	data, err := json.MarshalIndent(draft, "", "  ")
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("propose --from-doc: marshal draft: %w", err)}
	}
	if out == "" {
		fmt.Fprintln(outWriter, string(data))
		return nil
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("propose --from-doc: %w", err)}
	}
	fmt.Fprintf(outWriter, "wrote draft: %s\n", out)
	return nil
}

// draftFromDoc is the md medium's own draft producer (docs/intent-
// provider.md): read the raw document, redact secret-shaped material at
// capture (before anything ever leaves this machine), draft via the
// configured intent provider with retry-with-errors/hard-fail already
// built into DraftWithRetry, and populate provenance. Extracted from
// runProposeFromDoc (UBI-49) so `ubx plan --from-doc` (plan.go) can drive
// the identical drafting step before resolving it in the same command,
// rather than a second copy of this logic -- the resolve step itself
// stays a separate, explicit call in each caller, never folded in here.
func draftFromDoc(cmd *cobra.Command, cfg *Config, docPath, stack string, timeout time.Duration) (*resolver.IntentFile, error) {
	if stack == "" {
		return nil, errors.New("--stack is required")
	}

	raw, err := os.ReadFile(docPath)
	if err != nil {
		return nil, err
	}

	adapter, err := buildIntentAdapter(cfg)
	if err != nil {
		return nil, err
	}

	redacted, findings := intentprovider.Redact(raw)
	errOut := cmd.ErrOrStderr()
	for _, f := range findings {
		fmt.Fprintf(errOut, "warning: redacted possible secret material (%s) before sending %s to the %s adapter\n", f, docPath, adapter.Name())
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	draft, rawOutput, err := intentprovider.DraftWithRetry(ctx, adapter, stack, redacted)
	if err != nil {
		return nil, err
	}

	intentprovider.PopulateSources(draft, intentprovider.SourceKindDocument, docPath, intentprovider.HashDocument(raw), adapter.Name(), adapter.Model(), rawOutput)
	return draft, nil
}

// runProposeFromDiagram is `ubx propose --from-diagram`'s own RunE body
// (docs/diagram-medium.md's own design): draft via draftFromDiagram,
// render whatever ambiguity content the parse produced (including the
// $cross structural limitation, which surfaces here as an ordinary
// defaults[] note -- docs/diagram-medium.md's own "a genuine v1 limit, not
// a bug" framing) for a human to review, and write the draft. Never
// resolves, never touches a ledger -- the same two-step shape as
// --from-doc, not --from-code's one-step shape, because a diagram parse
// can produce real, visible ambiguity that needs a review gate first.
func runProposeFromDiagram(cmd *cobra.Command, diagramPath, stack, out, summary string, neighborLedgerFlags []string, timeout time.Duration) error {
	rc, err := LoadConfigResolved(cmd.ErrOrStderr())
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("propose --from-diagram: %w", err)}
	}
	cfg := rc.Config
	applyStackDefault(cmd, &stack, cfg)
	if stack == "" {
		return &ExitCodeError{Code: 2, Err: stackRequiredError("propose --from-diagram", rc.Files)}
	}

	draft, err := draftFromDiagram(cmd, cfg, diagramPath, stack, summary, neighborLedgerFlags, timeout)
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("propose --from-diagram: %w", err)}
	}

	outWriter := cmd.OutOrStdout()
	renderAmbiguity(outWriter, newStyler(cmd), draft)

	data, err := json.MarshalIndent(draft, "", "  ")
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("propose --from-diagram: marshal draft: %w", err)}
	}
	if out == "" {
		fmt.Fprintln(outWriter, string(data))
		return nil
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("propose --from-diagram: %w", err)}
	}
	fmt.Fprintf(outWriter, "wrote draft: %s\n", out)
	return nil
}

// draftFromDiagram is the diagram medium's own draft producer (docs/
// diagram-medium.md): read the raw .d2 file, load the declared providers'
// schemas for type inference, and parse the topology into an intent/v1
// draft (diagram.Parse -- fully deterministic, no LLM in this path at
// all), pinning the diagram file itself as the draft's own provenance
// source. Extracted from runProposeFromDiagram (UBI-49) so `ubx plan
// --from-diagram` (plan.go) can drive the identical parsing step before
// resolving it in the same command, rather than a second copy of this
// logic -- the resolve step itself stays a separate, explicit call in
// each caller, never folded in here.
func draftFromDiagram(cmd *cobra.Command, cfg *Config, diagramPath, stack, summary string, neighborLedgerFlags []string, timeout time.Duration) (*resolver.IntentFile, error) {
	if stack == "" {
		return nil, errors.New("--stack is required")
	}

	raw, err := os.ReadFile(diagramPath)
	if err != nil {
		return nil, err
	}

	neighborLedgers, err := parseNeighborLedgerFlags(neighborLedgerFlags)
	if err != nil {
		return nil, err
	}

	if len(cfg.Providers) == 0 {
		return nil, errors.New("no [providers] declared in .ubx/config -- the diagram medium has no legacy single-provider fallback (like \"ubx sdk gen\", it's a post-multi-provider-stacks feature); declare at least one source in [providers]")
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	providers, err := loadDiagramProviders(ctx, cfg)
	if err != nil {
		return nil, err
	}

	draft, err := diagram.Parse(diagramPath, bytes.NewReader(raw), stack, providers, diagram.Options{
		NeighborLedgers: neighborLedgers,
		BaseDir:         filepath.Dir(diagramPath),
	})
	if err != nil {
		return nil, err
	}

	if summary == "" {
		summary = fmt.Sprintf("%d resource(s) authored via %s for stack %q", len(draft.Resources), filepath.Base(diagramPath), stack)
	}
	draft.Intent.Summary = summary
	draft.Intent.Sources = append(draft.Intent.Sources, core.IntentSource{
		Kind:        intentprovider.SourceKindDocument,
		Ref:         diagramPath,
		ContentHash: intentprovider.HashDocument(raw),
	})
	return draft, nil
}

// parseNeighborLedgerFlags parses repeated "<stack>=<path>" --neighbor-ledger
// flags into the map diagram.Options.NeighborLedgers expects, overriding
// the "../<stack>" convention diagram.Parse otherwise falls back to when
// resolving a cross-stack reference node's ledger directory.
func parseNeighborLedgerFlags(flags []string) (map[string]string, error) {
	if len(flags) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(flags))
	for _, f := range flags {
		stack, path, ok := strings.Cut(f, "=")
		if !ok || stack == "" || path == "" {
			return nil, fmt.Errorf("--neighbor-ledger %q: want \"<stack>=<path>\"", f)
		}
		out[stack] = path
	}
	return out, nil
}

// loadDiagramProviders acquires and launches every provider declared in
// [providers], fetches its schema for diagram.Parse's own type-inference
// pass (UBI-43's InferProvider, reused unchanged), and closes each client
// immediately after -- newSchemaInspector wraps only the already-fetched
// static schema data, so there's no need to hold clients open until the
// command exits the way cli/resolve.go's own longer-lived resolve pass
// does. Deliberately a standalone helper rather than a refactor of
// resolve.go's own inline loading block: matches cli/sdk.go's own
// precedent of a command owning its own provider-loading logic rather
// than sharing it, and keeps this "CLI wiring" slice from touching
// working, tested code outside its own scope.
func loadDiagramProviders(ctx context.Context, cfg *Config) ([]resolver.DeclaredProvider, error) {
	var providers []resolver.DeclaredProvider
	for _, src := range sortedProviderSources(cfg.Providers) {
		version := cfg.Providers[src]
		parsed, err := provider.ParseSource(src)
		if err != nil {
			return nil, err
		}
		result, err := provider.Acquire(ctx, parsed, version)
		if err != nil {
			return nil, fmt.Errorf("acquire provider %s@%s: %w", src, version, err)
		}
		client, err := provider.Launch(ctx, result.Path)
		if err != nil {
			return nil, fmt.Errorf("launch provider %s@%s: %w", src, version, err)
		}
		schemas, err := client.Provider.Schema(ctx)
		closeErr := client.Close()
		if err != nil {
			return nil, fmt.Errorf("fetch schema for %s@%s: %w", src, version, err)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close provider %s@%s: %w", src, version, closeErr)
		}
		providers = append(providers, resolver.DeclaredProvider{Source: src, Version: version, Schema: newSchemaInspector(schemas)})
	}
	return providers, nil
}
