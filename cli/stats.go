package cli

import (
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
)

// newStatsCmd is UBI-40, the third of the read-only projection quartet:
// the thesis metrics, self-measured from any ledger -- the product
// proving itself with the user's own data. Mechanically core.Stats -- see
// that function's own doc comment for the full, honest account of what
// "signed-flow drift resolution %" can and can't measure from ledger data
// alone. This command's own job is flag parsing (the --since/--until
// RFC3339 window, --stack) and rendering; no exit-1 concept (a stats
// report is never itself a "finding" the way `ubx verify`/`ubx status
// --drift` produce one) -- 0 on any successful report, including an
// empty ledger, 2 on a genuine error.
func newStatsCmd() *cobra.Command {
	var (
		ledgerDir string
		stack     string
		sinceFlag string
		untilFlag string
		jsonOut   bool
	)

	cmd := &cobra.Command{
		Use:           "stats",
		Short:         "Decision-flow metrics folded from the ledger -- signed-flow drift resolution, time-to-decision, attribution coverage",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("stats: %w", err)}
			}
			applyStackDefault(cmd, &stack, cfg)

			opts := core.StatsOptions{Stack: stack}
			if sinceFlag != "" {
				since, err := time.Parse(time.RFC3339, sinceFlag)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("stats: --since must be RFC3339: %w", err)}
				}
				opts.Since = &since
			}
			if untilFlag != "" {
				until, err := time.Parse(time.RFC3339, untilFlag)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("stats: --until must be RFC3339: %w", err)}
				}
				opts.Until = &until
			}

			ledger, closeLedger, err := openLedgerForStack(cmd.Context(), ledgerDir, stack, cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("stats: %w", err)}
			}
			defer closeLedger()

			result, err := core.Stats(ledger, opts)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("stats: %w", err)}
			}

			out := cmd.OutOrStdout()
			if jsonOut {
				if err := writeJSON(out, statsToJSON(result)); err != nil {
					return &ExitCodeError{Code: 2, Err: err}
				}
				return nil
			}
			renderStatsHuman(out, result)
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&stack, "stack", "", "restrict the report to one stack (default: every stack the ledger holds; required for a remote [ledger] store)")
	cmd.Flags().StringVar(&sinceFlag, "since", "", "only proposals accepted at or after this RFC3339 timestamp")
	cmd.Flags().StringVar(&untilFlag, "until", "", "only proposals accepted at or before this RFC3339 timestamp")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit one JSON document instead of human text (UBI-20)")
	return cmd
}

func renderStatsHuman(out io.Writer, r *core.StatsResult) {
	fmt.Fprintf(out, "ubx stats: %d proposal(s) total\n\n", r.TotalProposals)

	fmt.Fprintln(out, "By kind:")
	for _, kind := range []core.ProposalKind{core.KindAdoption, core.KindChange, core.KindDriftAdopt, core.KindDriftRevert} {
		if n := r.ByKind[kind]; n > 0 {
			fmt.Fprintf(out, "  %s: %d\n", kind, n)
		}
	}
	fmt.Fprintln(out)

	fmt.Fprintln(out, "Acceptance method:")
	for _, method := range []string{"local", "pr_merge"} {
		if n := r.AcceptanceMethod[method]; n > 0 {
			fmt.Fprintf(out, "  %s: %d (%.0f%%)\n", method, n, pctOf(n, r.TotalProposals))
		}
	}
	fmt.Fprintln(out)

	if pct, ok := r.DriftResolutionPct(); ok {
		fmt.Fprintf(out, "Signed-flow drift resolution: %.0f%% (%d of %d surfaced drift(s) resolved -- %d adopted/superseded, %d reverted, %d still open)\n",
			pct, r.DriftResolvedAdopted+r.DriftResolvedReverted, r.DriftEvents, r.DriftResolvedAdopted, r.DriftResolvedReverted, r.DriftOpen)
	} else {
		fmt.Fprintln(out, "Signed-flow drift resolution: no drift proposals in this window")
	}
	fmt.Fprintln(out, "  (measures drift THIS LEDGER has a record of -- a drift never scanned,")
	fmt.Fprintln(out, "  or scanned and never accepted, leaves no trace for an offline fold to find)")
	fmt.Fprintln(out)

	if pct, ok := r.AttributionCoveragePct(); ok {
		fmt.Fprintf(out, "Attribution coverage: %.0f%% (%d of %d drift_adopt proposal(s) carry a real actor)\n",
			pct, r.AttributedDriftAdopts, r.TotalDriftAdoptsForAttribution)
	} else {
		fmt.Fprintln(out, "Attribution coverage: no drift_adopt proposals in this window")
	}
	fmt.Fprintln(out)

	if r.TimeToDecisionSamples > 0 {
		fmt.Fprintf(out, "Mean time-to-decision: %s (%d sample(s))\n", r.TimeToDecision.Round(time.Second), r.TimeToDecisionSamples)
	} else {
		fmt.Fprintln(out, "Mean time-to-decision: no drift proposals with parseable timestamps in this window")
	}

	if len(r.DriftByStack) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Drift by stack:")
		for _, stack := range sortedStringKeys(r.DriftByStack) {
			fmt.Fprintf(out, "  %s: %d\n", stack, r.DriftByStack[stack])
		}
	}
}

func sortedStringKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func pctOf(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total) * 100
}

// statsJSON is `ubx stats --json`'s own top-level document (UBI-20
// workstream 2, UBI-40).
type statsJSON struct {
	Format           int            `json:"format"`
	TotalProposals   int            `json:"total_proposals"`
	ByKind           map[string]int `json:"by_kind"`
	AcceptanceMethod map[string]int `json:"acceptance_method"`

	DriftResolution driftResolutionJSON `json:"drift_resolution"`
	Attribution     attributionJSON     `json:"attribution"`
	TimeToDecision  timeToDecisionJSON  `json:"time_to_decision"`
	DriftByStack    map[string]int      `json:"drift_by_stack,omitempty"`
}

type driftResolutionJSON struct {
	SurfacedEvents   int      `json:"surfaced_events"`
	ResolvedReverted int      `json:"resolved_reverted"`
	ResolvedAdopted  int      `json:"resolved_adopted"`
	Open             int      `json:"open"`
	ResolutionPct    *float64 `json:"resolution_pct,omitempty"`
}

type attributionJSON struct {
	AttributedDriftAdopts int      `json:"attributed_drift_adopts"`
	TotalDriftAdopts      int      `json:"total_drift_adopts"`
	CoveragePct           *float64 `json:"coverage_pct,omitempty"`
}

type timeToDecisionJSON struct {
	MeanSeconds *float64 `json:"mean_seconds,omitempty"`
	Samples     int      `json:"samples"`
}

func statsToJSON(r *core.StatsResult) statsJSON {
	byKind := make(map[string]int, len(r.ByKind))
	for k, v := range r.ByKind {
		byKind[string(k)] = v
	}

	payload := statsJSON{
		Format:           jsonFormatVersion,
		TotalProposals:   r.TotalProposals,
		ByKind:           byKind,
		AcceptanceMethod: r.AcceptanceMethod,
		DriftResolution: driftResolutionJSON{
			SurfacedEvents:   r.DriftEvents,
			ResolvedReverted: r.DriftResolvedReverted,
			ResolvedAdopted:  r.DriftResolvedAdopted,
			Open:             r.DriftOpen,
		},
		Attribution: attributionJSON{
			AttributedDriftAdopts: r.AttributedDriftAdopts,
			TotalDriftAdopts:      r.TotalDriftAdoptsForAttribution,
		},
		TimeToDecision: timeToDecisionJSON{Samples: r.TimeToDecisionSamples},
		DriftByStack:   r.DriftByStack,
	}
	if pct, ok := r.DriftResolutionPct(); ok {
		payload.DriftResolution.ResolutionPct = &pct
	}
	if pct, ok := r.AttributionCoveragePct(); ok {
		payload.Attribution.CoveragePct = &pct
	}
	if r.TimeToDecisionSamples > 0 {
		seconds := r.TimeToDecision.Seconds()
		payload.TimeToDecision.MeanSeconds = &seconds
	}
	return payload
}
