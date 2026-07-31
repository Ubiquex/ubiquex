package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/executor"
	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/provider"
)

// errShipDeclined is confirmAndAccept's own sentinel for "the human typed
// something other than yes" -- a deliberate abort, not a finding or an
// error (UBI-62's own design note: this is the local-tier signing
// moment itself, declining it is exactly as legitimate an outcome as
// chat.go's own /quit), so ship.go's RunE maps it to a clean exit 0
// rather than routing it through acceptErrorCode.
var errShipDeclined = errors.New("ship: declined")

// newShipCmd is UBI-26's executor CLI surface: takes an already-accepted
// drift_revert proposal and actually executes it against live cloud --
// the one command in this codebase, alongside a future writeback --write,
// that changes real infrastructure rather than only ever reading or
// recording. Every mechanism it drives (the failure-state machine,
// idempotent re-run, freshness re-verification, redaction) is built and
// tested in core/executor; this command is a thin CLI wrapper over
// executor.Ship, in the same spirit cli/status.go and cli/accept.go wrap
// core's own primitives.
//
// UBI-62 (2026-07-30, founder first-user test): a plan's own inline
// acceptance used to apply immediately, with no final human checkpoint --
// this session adds one, but ONLY for that path. An already-accepted
// proposal (found via ledger.Read, the four-verb ceremony's own separate
// "ubx accept" already having been the consent moment) still ships
// immediately, exactly as before -- re-prompting there would be new,
// unwanted ceremony on top of a decision already made deliberately.
func newShipCmd() *cobra.Command {
	var (
		ledgerDir       string
		stack           string
		providerPath    string
		source          string
		providerVersion string
		providerConfig  string
		timeout         time.Duration
		jsonOut         bool
		confirmDestroys bool
		yes             bool
		fullHashes      bool
	)

	cmd := &cobra.Command{
		Use:   "ship [hash]",
		Short: "Accept (local tier, if needed) and execute a drift_revert or change proposal against live cloud -- the only command that applies",
		Long: `Executes a drift_revert or change proposal: for a drift_revert, restores the
resource's live state to match the ledger's recorded truth; for a change ("ubx resolve"'s own
output, or "ubx plan"'s fused equivalent), creates and modifies resources for real, in real
dependency order, feeding each resource's real applied output into any sibling still carrying a
$computed marker pointing at it. This is the one ubx command that changes real infrastructure --
accept/why/status/scan/revert-plan/resolve/plan only ever read or record.

<hash> is optional: omitted, it resolves to the most recent unshipped plan for the
resolved stack (--stack, or config's own default) -- shown explicitly before anything happens, never
silently guessed. If plans for more than one stack exist and --stack wasn't given, nothing is
guessed between stacks either: a TTY is prompted to choose, a non-TTY is refused with the same
list as a teaching error.

<hash>, given or resolved, is looked up two ways, in order: first as an already-accepted proposal
id in this stack's ledger (the four-verb ceremony's own path -- "ubx accept" ran separately,
including PR-merge acceptance) -- applied immediately, no further confirmation, since that
acceptance already was the consent moment; if not found there, as a plan "ubx plan" saved at
.ubx/plans/<hash>.json. For THAT path only, the full receipt renders again and a typed "yes" is
required before anything is accepted or applied -- the prompt IS the local-tier signing moment.
--yes skips the prompt (for CI/scripts) but never the receipt render; a non-TTY without --yes
refuses outright rather than hang or silently proceed. --confirm-destroys is still required,
additively, for any plan with blast_radius.destroys > 0 -- two distinct consents for the
irreversible class, checked before the prompt even renders. A plan consumed this way (accepted,
whether shipped cleanly or not) is pruned from .ubx/plans/ so it never reappears as "latest".

Safe to re-run: ubx ship is idempotent by contract (docs/executor.md). A resource already applied in a
prior attempt is skipped -- including, for a change proposal, recovering its real applied output from
the ledger so a still-pending dependent can proceed correctly even after a crash between the two; a
resource left in an unresolved state (a crash, a timeout) is reconciled against live reality before
anything new is attempted where a lookup key exists; a resource whose restore target is itself a
redacted ($redacted) value is declined every time -- ubx never constructs a live apply from a salted
hash, use "ubx revert-plan" for that resource's manual reconciliation steps instead.

Freshness is re-verified for every modified resource, immediately before its own attempt -- not just
once at the start -- so reality moving mid-run is refused, never bulldozed. Only drift_revert and
change proposals can be shipped; every other kind is record-only (nothing to ship).

Long-running provider calls and read-back reconciliation loops narrate live (docs/cli-output-spec.md:
"the read-back verification line is mandatory") -- a real destroy's own honest wait for eventual
consistency shows its own work instead of sitting silent.`,
		Args: cobra.MaximumNArgs(1),
		// Exit code is the CI contract (docs/exit-codes.mdx): 0 applied (or
		// already fully applied -- a genuine no-op -- or a declined
		// confirmation, UBI-62: a deliberate abort is not a failure), 1
		// partially applied, failed, or an inline-accept refusal (an
		// actionable finding -- confirm destroys, resolve staleness,
		// retry, or investigate why), 2 a genuine error (bad input,
		// provider/ledger failure). SilenceUsage/Errors: same reasoning as
		// every other UBI-20-audited command (status.go).
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
			}
			applyStackDefault(cmd, &stack, cfg)

			ledger, closeLedger, err := openLedgerForStack(cmd.Context(), ledgerDir, stack, cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
			}
			defer closeLedger()

			out := cmd.OutOrStdout()
			st := newStylerFull(cmd, fullHashes)

			hashArg := ""
			if len(args) == 1 {
				hashArg = args[0]
			} else {
				resolved, err := resolveBareShipTarget(cmd, ledgerDir, stack)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
				}
				hashArg = resolved
			}

			p, err := ledger.Read(hashArg)
			if err != nil {
				// UBI-49: not yet an accepted proposal in this stack's ledger
				// -- fall back to a plan "ubx plan" saved locally. UBI-62:
				// unlike before, this is no longer silent -- the receipt
				// renders again and a typed "yes" is the real signing
				// moment (or --yes, for automation).
				draft, fullHash, verr := resolveAndValidatePlan(ledgerDir, hashArg, confirmDestroys)
				if verr != nil {
					return &ExitCodeError{Code: acceptErrorCode(verr), Err: fmt.Errorf("ship: %w", verr)}
				}
				accepted, cerr := confirmAndAccept(cmd, ledger, st, draft, yes)
				if errors.Is(cerr, errShipDeclined) {
					return nil
				}
				if cerr != nil {
					return &ExitCodeError{Code: acceptErrorCode(cerr), Err: fmt.Errorf("ship: %w", cerr)}
				}
				// Pruning is tidiness, not correctness -- a failure to
				// remove the now-consumed plan file never fails the ship
				// itself, it just means "latest" might offer a stale
				// candidate next time, worth a warning, not a hard stop.
				if err := os.Remove(planFilePath(ledgerDir, fullHash)); err != nil && !os.IsNotExist(err) {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: ship: could not prune consumed plan file: %v\n", err)
				}
				fmt.Fprintf(out, "accepted %s (stack %s) via local plan\n", st.Hash(accepted.ID), accepted.Stack)
				p = accepted
			}
			// A friendly, early exit before ever launching a provider --
			// executor.Ship enforces both of these authoritatively too
			// (ErrUnsupportedKind/ErrNotAccepted), this just avoids the
			// acquire/launch round trip for an obviously-wrong proposal.
			if p.Acceptance == nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: proposal %s is not accepted", p.ID)}
			}
			if p.Kind != core.KindDriftRevert && p.Kind != core.KindChange {
				// UBI-49 residual #4: an adoption/drift_adopt proposal's own
				// resolution IS its acceptance -- there's nothing left to
				// execute, and that's success, not the failure erroring
				// here used to render it as (the accept above -- whether
				// just now via the plan fallback, or earlier via `ubx
				// accept`/a prior `ubx ship` -- already fully committed
				// it). Recognized here, before ever trying and failing
				// against executor.Ship's own ErrUnsupportedKind.
				if isRecordOnlyKind(p.Kind) {
					fmt.Fprintf(out, "%s (%s) -- record-only, nothing to execute -- %s resolved\n", st.Hash(p.ID), p.Kind, st.Green("✓"))
					return nil
				}
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: proposal %s is kind %q -- ship only executes drift_revert or change proposals", p.ID, p.Kind)}
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			salt, err := ledger.Salt()
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
			}

			// docs/executor.md's own "Amendment (UBI-43): multi-provider
			// stacks" client pool -- a stack with a real [providers] table
			// in .ubx/config gets a genuine multi-entry pool (cli/providerpool.go),
			// lazily launching whichever providers this specific proposal's
			// own nodes actually need; a single-provider stack (no table)
			// keeps working exactly as it always has, one provider launched
			// up front, wrapped in the trivial SingleApplierPool. Never both
			// at once -- see docs/resolver.md's own staged
			// --source/--provider-version retirement plan for what happens
			// when a table AND the singular flags are both given.
			var pool executor.ApplierPool
			if len(cfg.Providers) > 0 {
				warnIfLegacyProviderFlagsGiven(cmd)
				pp, err := newProviderPool(salt, cfg.Providers, cfg.ProviderConfigs)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
				}
				defer pp.Close()
				pool = pp
			} else {
				applyProviderDefaults(cmd, &providerPath, &source, &providerVersion, cfg)
				if err := applyProviderConfigDefault(cmd, &providerConfig, cfg); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
				}
				path, _, err := resolveProviderBinary(ctx, providerPath, source, providerVersion)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
				}
				client, err := provider.Launch(ctx, path)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
				}
				defer client.Close()
				applier := newApplier(client.Provider, salt, source)
				pool = executor.SingleApplierPool(applier, json.RawMessage(providerConfig))
			}

			// UBI-61's own "progress narration" scope: live per-resource
			// transition/reconcile-attempt lines as executor.Ship's own
			// run actually produces them -- including the mandatory
			// read-back verification line -- rather than one report dumped
			// at the very end. --json/non-TTY still get the identical
			// silent-until-done behavior as before (the progress printer
			// itself is the only new behavior, and it's purely additive
			// stdout lines, never consulted for the sealed result itself).
			if !jsonOut {
				ctx = executor.WithProgress(ctx, newProgressPrinter(out, st))
			}

			sealed, err := executor.Ship(ctx, ledger, pool, source, p)
			if errors.Is(err, executor.ErrAlreadyApplied) {
				return reportAlreadyApplied(out, ledger, p, jsonOut)
			}
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
			}

			if jsonOut {
				if err := writeJSON(out, shipJSON{Format: jsonFormatVersion, ApplyRecord: sealed}); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
				}
			} else {
				printShipReport(out, st, sealed)
			}

			switch sealed.Summary.Outcome {
			case "applied":
				return nil
			default: // partially_applied, failed
				return &ExitCodeError{Code: 1, Err: fmt.Errorf("ship: %s (see above)", sealed.Summary.Outcome)}
			}
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&stack, "stack", "", "which stack's ledger to open -- required only when .ubx/config's [ledger] store is a remote store (a bare proposal id carries no stack of its own to derive it from); also which stack's plans are considered for a bare ship with no hash")
	cmd.Flags().StringVar(&providerPath, "provider", "", "path to the provider binary (mutually exclusive with --source)")
	cmd.Flags().StringVar(&source, "source", "", "provider source address, e.g. hashicorp/aws (mutually exclusive with --provider; requires --provider-version)")
	cmd.Flags().StringVar(&providerVersion, "provider-version", "", "explicit provider version to acquire (required with --source)")
	cmd.Flags().StringVar(&providerConfig, "provider-config", "{}", "JSON object configuring the provider, e.g. {\"region\":\"us-east-1\"}")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "overall timeout for the ship run")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit one JSON document instead of human text")
	cmd.Flags().BoolVar(&confirmDestroys, "confirm-destroys", false, "required for inline local-tier acceptance of any plan with blast_radius.destroys > 0 (docs/schema.md); unused when <hash> is already an accepted proposal, since that confirmation already happened at its own accept time")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the interactive \"type yes\" confirmation for inline local-tier acceptance (for CI/scripts) -- the receipt still renders; required on a non-TTY, which never prompts")
	cmd.Flags().BoolVar(&fullHashes, "full-hashes", false, "render every hash in full instead of the default 12-char short form")

	return cmd
}

// resolveAndValidatePlan is acceptPlanInline's own pre-UBI-62 body, minus
// the final core.Accept call -- split out so ship.go's RunE can insert
// the interactive confirmation between validation and acceptance
// (confirmAndAccept, below). Order matters and is preserved exactly:
// resolve -> hash-integrity check -> destroys-confirmed -> pins-fresh,
// all fast-fail checks that must run before ever asking a human to type
// "yes" -- there's no point rendering a receipt and prompting for
// something this will refuse regardless of the answer.
func resolveAndValidatePlan(ledgerDir, hash string, confirmDestroys bool) (draft *core.Proposal, fullHash string, err error) {
	// resolvePlanHash (UBI-49 finding #6) accepts hash as either a full
	// content hash or a unique prefix of one -- whatever `ubx scan
	// --propose`/`ubx terminate` printed as their own short "next: ubx
	// ship <shorthash>" handoff, not just plan.go's own full-hash output.
	fullHash, draft, err = resolvePlanHash(ledgerDir, hash)
	if err != nil {
		return nil, "", fmt.Errorf("no accepted proposal %s in this stack's ledger, and no plan file matching it in .ubx/plans/: %w", hash, err)
	}

	// Integrity check specific to this fallback: a plan file is always
	// written at exactly its own content hash's path (plan.go's own
	// writePlanFile) -- if its content doesn't hash to the filename it was
	// found at, the file was hand-edited or corrupted since, and shipping
	// it under the hash the caller actually asked for would be shipping
	// something other than what that hash names.
	computedHash, err := core.Hash(draft)
	if err != nil {
		return nil, "", err
	}
	if computedHash != fullHash {
		return nil, "", fmt.Errorf("plan file at %s hashes to %s, not %s -- stale or corrupted plan file", planFilePath(ledgerDir, fullHash), computedHash, fullHash)
	}

	if err := checkDestroysConfirmed(draft, confirmDestroys); err != nil {
		return nil, "", err
	}
	if err := resolver.VerifyPins(draft); err != nil {
		return nil, "", err
	}
	return draft, fullHash, nil
}

// confirmAndAccept is UBI-62's own signing moment: render the receipt
// again, then require a typed "yes" (terraform's exact consent pattern,
// not y/n) before core.Accept ever runs -- the prompt IS the local-tier
// acceptance act. --yes skips the read (still renders the receipt); a
// non-TTY without it refuses outright rather than hang reading a stdin
// nobody's watching or silently treat EOF as consent. Returns
// errShipDeclined (not a real error) if the human types anything else --
// the caller maps that to a clean exit 0, matching chat.go's own
// abandoned-session precedent.
func confirmAndAccept(cmd *cobra.Command, ledger *core.Ledger, st *styler, draft *core.Proposal, yes bool) (*core.Proposal, error) {
	out := cmd.OutOrStdout()
	age := "unknown age"
	if t, ok := parseResolvedAt(draft); ok {
		age = humanAge(t)
	}
	renderPlanReceipt(out, st, draft, fmt.Sprintf("Ship  %s · %s", draft.Stack, age))
	warnIfRecentUnattributedAdopt(out, st, draft)

	if !yes {
		if !isTerminal(cmd.InOrStdin()) {
			return nil, errors.New("refusing to apply without confirmation: not an interactive terminal -- pass --yes to confirm non-interactively (e.g. in CI/scripts)")
		}
		fmt.Fprintf(out, "\nShip this to %s? Only 'yes' accepted: ", draft.Stack)
		scanner := bufio.NewScanner(cmd.InOrStdin())
		typed := ""
		if scanner.Scan() {
			typed = scanner.Text()
		}
		if typed != "yes" {
			fmt.Fprintln(out, "ship aborted -- nothing accepted or applied")
			return nil, errShipDeclined
		}
	}
	return core.Accept(ledger, draft)
}

// parseResolvedAt is a small, never-fails helper over
// Proposal.Resolution.ResolvedAt -- a malformed/legacy timestamp reports
// ok=false rather than a zero time silently claiming to be "just now" or
// crashing; callers decide their own fallback.
func parseResolvedAt(p *core.Proposal) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, p.Resolution.ResolvedAt)
	return t, err == nil
}

// warnIfRecentUnattributedAdopt is UBI-49 residual #5's one behavioral
// addition (the display-only surfacing lives in attributionCardLine/
// blame.go instead): an unattributed adoption/drift_adopt is PERMANENT
// once accepted -- core.Blame/why never re-attempt attribution later,
// they only ever replay what was recorded at accept time. A drift this
// recent (<10 minutes since it was resolved) may simply be too new for
// CloudTrail's own delivery window (typically 2-5 minutes, per
// core.ReasonDeliveryWindow) -- re-scanning in a few minutes could still
// attribute it for real. Warns, never blocks: the human still decides,
// same as every other consent moment in this codebase.
func warnIfRecentUnattributedAdopt(out io.Writer, st *styler, p *core.Proposal) {
	if !isRecordOnlyKind(p.Kind) {
		return
	}
	reason, backend, ok := recordedUnattributedReason(p.Intent.Sources)
	if !ok {
		return
	}
	resolvedAt, ok := parseResolvedAt(p)
	if !ok || time.Since(resolvedAt) >= 10*time.Minute {
		return
	}
	detail := unattributedReason(reason)
	if backend != "" {
		detail += ", " + backend
	}
	fmt.Fprintf(out, "%s this drift is %s and has no recorded attribution (%s) -- accepting now records it as unattributed permanently; consider re-scanning in a few minutes first\n\n",
		st.Yellow("warning:"), humanAge(resolvedAt), detail)
}

// isRecordOnlyKind reports whether k's own resolution IS its acceptance
// -- nothing left for `ubx ship` to execute against a real provider
// (UBI-49 residual #4). adoption/drift_adopt are both "record reality as
// signed" (docs/cli-output-spec.md's own scan-card wording); drift_revert
// and change are the only two kinds executor.Ship actually applies.
func isRecordOnlyKind(k core.ProposalKind) bool {
	return k == core.KindAdoption || k == core.KindDriftAdopt
}

// humanAge renders a duration-since-t the way a person reads it --
// coarsest meaningful unit only, matching docs/cli-output-spec.md's own
// ship example ("plan 0509dd5d · 2m old").
func humanAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds old", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm old", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh old", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd old", int(d.Hours()/24))
	}
}

// planCandidate is one entry in the plan store, for bare `ubx ship`'s own
// latest-plan-for-stack resolution.
type planCandidate struct {
	Hash string
	P    *core.Proposal
}

// listPlanFiles reads every plan currently in ledgerDir's own plan store.
// A file that fails to parse is skipped, never fatal for the whole
// listing -- the plan store is never the canonical source of truth (the
// ledger is), so one corrupted entry is that entry's own problem, not a
// reason to refuse listing every other valid one.
func listPlanFiles(ledgerDir string) ([]planCandidate, error) {
	dir := filepath.Join(ledgerDir, ".ubx", "plans")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var candidates []planCandidate
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		hash := strings.TrimSuffix(e.Name(), ".json")
		p, err := readPlanFile(ledgerDir, hash)
		if err != nil {
			continue
		}
		candidates = append(candidates, planCandidate{Hash: hash, P: p})
	}
	return candidates, nil
}

// resolveBareShipTarget is bare `ubx ship`'s own (no hash argument)
// resolution (UBI-62, founder comment): the most recent plan for the
// resolved stack, named explicitly before anything happens -- never
// silently applied. .ubx/plans/ is not stack-namespaced (it's a flat,
// content-hash-keyed store shared by every stack that uses this ledger
// dir -- the common case for the default git-local store), so multiple
// DISTINCT stacks with unshipped plans is a real, ordinary situation,
// never guessed between: a TTY is prompted to choose, a non-TTY is
// refused with the identical list as a teaching error.
func resolveBareShipTarget(cmd *cobra.Command, ledgerDir, stack string) (string, error) {
	candidates, err := listPlanFiles(ledgerDir)
	if err != nil {
		return "", fmt.Errorf("list plan store: %w", err)
	}
	if len(candidates) == 0 {
		return "", errors.New("no plans in .ubx/plans/ -- run `ubx plan`/`ubx scan --propose`/`ubx terminate` first, or pass a hash explicitly")
	}

	out := cmd.OutOrStdout()

	if stack != "" {
		var forStack []planCandidate
		for _, c := range candidates {
			if c.P.Stack == stack {
				forStack = append(forStack, c)
			}
		}
		if len(forStack) == 0 {
			return "", fmt.Errorf("no unshipped plans for stack %q in .ubx/plans/", stack)
		}
		return reportLatest(out, forStack), nil
	}

	stacks := map[string]bool{}
	for _, c := range candidates {
		stacks[c.P.Stack] = true
	}
	if len(stacks) == 1 {
		return reportLatest(out, candidates), nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		ti, _ := parseResolvedAt(candidates[i].P)
		tj, _ := parseResolvedAt(candidates[j].P)
		return ti.After(tj)
	})
	fmt.Fprintln(out, "multiple stacks have unshipped plans -- pass --stack, or choose one:")
	for i, c := range candidates {
		age := "unknown age"
		if t, ok := parseResolvedAt(c.P); ok {
			age = humanAge(t)
		}
		fmt.Fprintf(out, "  [%d] %s  %s  %s  %s\n", i+1, shortRef(c.Hash), c.P.Stack, c.P.Kind, age)
	}
	if !isTerminal(cmd.InOrStdin()) {
		return "", fmt.Errorf("%d plans span more than one stack -- pass --stack or an explicit hash (non-interactive, refusing to guess)", len(stacks))
	}
	fmt.Fprintf(out, "Ship which plan? [1-%d]: ", len(candidates))
	scanner := bufio.NewScanner(cmd.InOrStdin())
	if !scanner.Scan() {
		return "", errors.New("no selection made")
	}
	idx, convErr := strconv.Atoi(strings.TrimSpace(scanner.Text()))
	if convErr != nil || idx < 1 || idx > len(candidates) {
		return "", fmt.Errorf("invalid selection %q", scanner.Text())
	}
	return candidates[idx-1].Hash, nil
}

// reportLatest picks candidates' own most-recently-resolved entry (by
// Resolution.ResolvedAt, already recorded on every saved plan -- no
// schema change needed), announces which one it picked (implicit
// selection is never silent), and returns its hash.
func reportLatest(out io.Writer, candidates []planCandidate) string {
	best := candidates[0]
	bestAt, _ := parseResolvedAt(best.P)
	for _, c := range candidates[1:] {
		at, ok := parseResolvedAt(c.P)
		if ok && at.After(bestAt) {
			best, bestAt = c, at
		}
	}
	age := "unknown age"
	if !bestAt.IsZero() {
		age = humanAge(bestAt)
	}
	fmt.Fprintf(out, "latest plan for stack %s: %s  %s  %s\n", best.P.Stack, shortRef(best.Hash), best.P.Kind, age)
	return best.Hash
}

// newProgressPrinter builds executor.ProgressEvent callback for `ubx
// ship`'s own live narration (UBI-61, docs/cli-output-spec.md: "the
// read-back verification line is mandatory"). Each event prints its own
// line the moment it happens -- not buffered to the end -- with elapsed
// time since the resource's own most recent in_flight transition. Not a
// frame-animated spinner (no goroutine ticking a single line in place
// during a sleep): a deliberate, lower-risk choice for a
// correctness-critical apply path, still genuinely live since every
// reconcile attempt's own line appears exactly when that attempt runs.
func newProgressPrinter(out io.Writer, st *styler) func(executor.ProgressEvent) {
	starts := map[string]time.Time{}
	seen := map[string]bool{}
	return func(ev executor.ProgressEvent) {
		now := time.Now()
		if ev.State == "in_flight" {
			starts[ev.Address] = now
		}
		if ev.Address != "" && !seen[ev.Address] {
			seen[ev.Address] = true
			fmt.Fprintf(out, "%s\n", ev.Address)
		}
		elapsed := ""
		if start, ok := starts[ev.Address]; ok {
			d := now.Sub(start)
			elapsed = fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
		}

		var glyph, text string
		switch ev.Kind {
		case "transition":
			glyph = st.Dim("·")
			switch ev.State {
			case "applied":
				glyph = st.Green("✓")
			case "failed":
				glyph = st.Red("✗")
			}
			text = ev.State
			if ev.Detail != "" {
				text += " -- " + ev.Detail
			}
		case "reconcile_attempt":
			glyph = st.Yellow("⠧")
			text = fmt.Sprintf("%s (attempt %d/%d)", ev.Detail, ev.Attempt, ev.Total)
		default:
			return
		}
		if elapsed != "" {
			fmt.Fprintf(out, "  %s %-50s %s\n", glyph, text, elapsed)
		} else {
			fmt.Fprintf(out, "  %s %s\n", glyph, text)
		}
	}
}

// reportAlreadyApplied handles executor.Ship's simplest idempotency case
// (docs/executor.md): every resource was already applied in a prior
// attempt, so this run touched nothing and wrote no new apply record.
// Reports the most recent sealed attempt as evidence, rather than an empty
// response -- a human asking "did this ship already?" gets the same
// receipt either way.
func reportAlreadyApplied(out io.Writer, ledger *core.Ledger, p *core.Proposal, jsonOut bool) error {
	attempts, err := ledger.ApplyAttempts(p.ID)
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
	}
	var latest *core.ApplyRecord
	for _, a := range attempts {
		if a.Sealed() {
			latest = a
		}
	}
	if jsonOut {
		if err := writeJSON(out, shipJSON{Format: jsonFormatVersion, AlreadyApplied: true, ApplyRecord: latest}); err != nil {
			return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
		}
		return nil
	}
	fmt.Fprintf(out, "%s: already fully applied -- nothing to do\n", p.ID)
	return nil
}

// printShipReport is ubx ship's human output: one line per resource's
// final state for this attempt, any recorded errors underneath it
// (including a redacted-after decline's own manual-steps-style message,
// docs/executor.md), and a trailing summary line. Green ✓ / red ✗ leads
// each line (docs/cli-output-spec.md: green = confirmations, red =
// destroys/failures) -- the underlying "<state>: <address>" wording is
// unchanged, so an existing substring assertion still finds it.
func printShipReport(out io.Writer, st *styler, rec *core.ApplyRecord) {
	for _, ra := range rec.Resources {
		state, _ := ra.LastState()
		glyph := st.Green("✓")
		if state == core.ResourceFailed || state == core.ResourceStillUnknown {
			glyph = st.Red("✗")
		}
		fmt.Fprintf(out, "%s %s: %s\n", glyph, state, ra.Address)
		for _, e := range ra.Errors {
			fmt.Fprintf(out, "  %s: %s\n", e.Classification, e.Message)
		}
	}
	fmt.Fprintf(out, "%d resource(s), %d applied, %d failed, %d still unknown -- outcome: %s\n",
		len(rec.Resources), rec.Summary.ResourcesApplied, rec.Summary.ResourcesFailed,
		rec.Summary.ResourcesStillUnknown, rec.Summary.Outcome)
}

// shipJSON is `ubx ship --json`'s payload -- format:1, the same contract
// UBI-20's scan/status/why already established. ApplyRecord is the
// already-well-shaped core.ApplyRecord itself, wrapped rather than
// re-derived into a bespoke shape (the same convention whyJSON's Proposal
// field already uses).
type shipJSON struct {
	Format         int               `json:"format"`
	AlreadyApplied bool              `json:"already_applied,omitempty"`
	ApplyRecord    *core.ApplyRecord `json:"apply_record,omitempty"`
}
