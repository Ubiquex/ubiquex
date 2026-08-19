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
	"sync"
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
		ledgerDir        string
		stack            string
		providerPath     string
		source           string
		providerVersion  string
		providerConfig   string
		timeout          time.Duration
		jsonOut          bool
		confirmDestroys  bool
		confirmTerminate bool
		yes              bool
		fullHashes       bool
	)

	cmd := &cobra.Command{
		Use:   "ship [hash]",
		Short: "Accept (local tier, if needed) and execute a drift_revert or change proposal against live cloud -- the only command that ships",
		Long: `Executes a drift_revert or change proposal: for a drift_revert, restores the
resource's live state to match the ledger's recorded truth; for a change ("ubx resolve"'s own
output, or "ubx plan"'s fused equivalent), creates and modifies resources for real, in real
dependency order, feeding each resource's real shipped output into any sibling still carrying a
$computed marker pointing at it. This is the one ubx command that changes real infrastructure --
accept/why/status/scan/revert-plan/resolve/plan only ever read or record.

<hash> is optional: omitted, it resolves to the most recent unshipped plan for the
resolved stack (--stack, or config's own default) -- shown explicitly before anything happens, never
silently guessed. If plans for more than one stack exist and --stack wasn't given, nothing is
guessed between stacks either: a TTY is prompted to choose, a non-TTY is refused with the same
list as a teaching error.

<hash>, given or resolved, is looked up two ways, in order: first as an already-accepted proposal
id in this stack's ledger (the four-verb ceremony's own path -- "ubx accept" ran separately,
including PR-merge acceptance) -- shipped immediately, no further confirmation, since that
acceptance already was the consent moment; if not found there, as a plan "ubx plan" saved at
.ubx/plans/<hash>.json. For THAT path only, the full receipt renders again and a typed "yes" is
required before anything is accepted or shipped -- the prompt IS the local-tier signing moment.
--yes skips the prompt (for CI/scripts) but never the receipt render; a non-TTY without --yes
refuses outright rather than hang or silently proceed. --confirm-terminate (or its unchanged
wire-level name, --confirm-destroys -- both flags set the identical bool, UBI-77) is still
required, additively, for any plan with blast_radius.destroys > 0 -- two distinct consents for the
irreversible class, checked before the prompt even renders. A plan consumed this way (accepted,
whether shipped cleanly or not) is pruned from .ubx/plans/ so it never reappears as "latest".

Safe to re-run: ubx ship is idempotent by contract (docs/executor.md). A resource already shipped in a
prior attempt is skipped -- including, for a change proposal, recovering its real shipped output from
the ledger so a still-pending dependent can proceed correctly even after a crash between the two; a
resource left in an unresolved state (a crash, a timeout) is reconciled against live reality before
anything new is attempted where a lookup key exists; a resource whose restore target is itself a
redacted ($redacted) value is declined every time -- ubx never ships from a salted
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

			p, err := resolveAcceptedProposal(ledger, hashArg)
			if err != nil {
				// UBI-63 session 5: an ambiguous short hash is a real,
				// actionable problem the user must resolve by typing more
				// characters -- surfaced directly, never silently tried
				// against the plan store too (which could coincidentally
				// "resolve" it to the wrong thing entirely).
				if !errors.Is(err, core.ErrProposalNotFound) {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("ship: %w", err)}
				}
				// UBI-49: not yet an accepted proposal in this stack's ledger
				// -- fall back to a plan "ubx plan" saved locally. UBI-62:
				// unlike before, this is no longer silent -- the receipt
				// renders again and a typed "yes" is the real signing
				// moment (or --yes, for automation).
				draft, fullHash, verr := resolveAndValidatePlan(ledgerDir, hashArg, confirmDestroys || confirmTerminate)
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
			if len(cfg.ThirdpartyProviders) > 0 {
				warnIfLegacyProviderFlagsGiven(cmd)
				pp, err := newProviderPool(salt, cfg.ThirdpartyProviders, cfg.Providers, cfg.ProviderConfigs)
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
			progressFinish := func() {}
			if !jsonOut {
				progressFn, pf := newProgressPrinter(out, st, isTerminal(cmd.OutOrStdout()), terminalWidth(cmd.OutOrStdout()), addressOpKinds(p))
				progressFinish = pf
				ctx = executor.WithProgress(ctx, progressFn)
			}

			sealed, err := executor.Ship(ctx, ledger, pool, source, p)
			// UBI-83: finish unconditionally, before ANY subsequent write --
			// success, ErrAlreadyApplied, a real error, all alike -- so a
			// still-open in-place-redrawn row (the run ending mid-phase,
			// e.g. a context timeout) never gets silently overwritten by
			// whatever prints next.
			progressFinish()
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
	cmd.Flags().BoolVar(&confirmDestroys, "confirm-destroys", false, "required for inline local-tier acceptance of any plan with blast_radius.destroys > 0 (docs/schema.md); unused when <hash> is already an accepted proposal, since that confirmation already happened at its own accept time -- --confirm-terminate is the identical flag under \"ubx terminate\"'s own human-facing name (UBI-77), either spelling satisfies the other")
	cmd.Flags().BoolVar(&confirmTerminate, "confirm-terminate", false, "alias for --confirm-destroys (UBI-77) -- the name \"ubx terminate\"'s own \"next:\" hint shows, since that's the verb a human actually typed; sets the identical requirement, either flag satisfies both")
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
// renderShipConfirmSummary is confirmAndAccept's own "just enough to
// confirm this is still the plan being signed" line (UBI-63 bug 3) --
// stack, plan age, and the blast radius, colored exactly like every
// other blast-radius line in this codebase. Deliberately NOT the full
// renderPlanReceipt: that already ran once, at `ubx plan`/`ubx scan
// --propose` time, and re-running it here again in full is noise, not
// review, for a plan a human has already read. UBI-88 vocabulary sweep:
// "change(s)"/"terminate(s)", matching the delta line and op headers
// everywhere else -- this line spells the counts out in words (not the
// symbol-only "+N ~N -N" shape), so it was a real instance of the same
// "modify(ies)"/"destroy(s)" inconsistency, not just a bare blast-radius
// count.
func renderShipConfirmSummary(out io.Writer, st *styler, p *core.Proposal, age string) {
	fmt.Fprintf(out, "Ship  %s · %s · %s %s %s\n",
		p.Stack, age,
		st.Green(fmt.Sprintf("+%d create(s)", p.BlastRadius.Creates)),
		st.Yellow(fmt.Sprintf("~%d change(s)", p.BlastRadius.Modifies)),
		st.Red(fmt.Sprintf("-%d terminate(s)", p.BlastRadius.Destroys)))
}

func confirmAndAccept(cmd *cobra.Command, ledger *core.Ledger, st *styler, draft *core.Proposal, yes bool) (*core.Proposal, error) {
	out := cmd.OutOrStdout()
	age := "unknown age"
	if t, ok := parseResolvedAt(draft); ok {
		age = humanAge(t)
	}
	// UBI-63 bug 3: the full receipt already rendered once, at `ubx plan`/
	// `ubx scan --propose` time -- that's the review. Re-rendering it
	// again in full here (every resource, every attribute, every
	// assumption) is pure noise for a plan a human has already read,
	// especially now that a real multi-resource receipt can run to
	// pages of formatted JSON (docs/cli-output-spec.md's own v2 receipt
	// format). The signing moment needs just enough to confirm THIS is
	// still the plan being signed -- stack, age, and the blast radius --
	// not a second full copy of it.
	renderShipConfirmSummary(out, st, draft, age)
	warnIfRecentUnattributedAdopt(out, st, draft)

	if !yes {
		if !isTerminal(cmd.InOrStdin()) {
			return nil, errors.New("refusing to ship without confirmation: not an interactive terminal -- pass --yes to confirm non-interactively (e.g. in CI/scripts)")
		}
		fmt.Fprintf(out, "\nShip this to %s? Only 'yes' accepted: ", draft.Stack)
		scanner := bufio.NewScanner(cmd.InOrStdin())
		typed := ""
		if scanner.Scan() {
			typed = scanner.Text()
		}
		if typed != "yes" {
			fmt.Fprintln(out, "ship aborted -- nothing accepted or shipped")
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

// tickInterval is how often the in-flight ticker (below) re-renders its
// one live line -- frequent enough that a human watching never wonders
// if the whole thing has hung, infrequent enough not to flood a
// piped/logged run with near-duplicate lines.
const tickInterval = 1 * time.Second

// progressLineWidth is the fixed field width every progress line's own
// glyph+text portion pads to (matching the pre-existing "%-50s"
// convention). UBI-83's own real ANSI line-clear (`\x1b[2K`) means a
// shrinking elapsed-time string can no longer leave stale trailing
// characters on a real terminal regardless of padding -- kept anyway for
// stable column alignment of the trailing elapsed field, and because a
// non-TTY line (never cleared, just appended) still benefits from it.
const progressLineWidth = 50

// spinnerFrames is the shared animated-spinner glyph sequence for both
// live in-progress phases this printer narrates (the raw provider-call
// wait, and the read-back verification wait) -- UBI-83's own exact spec:
// an ANIMATED spinner, not the static "·" (shipping) or fixed single
// frame "⠧" (verifying) this printer used before. The classic braille
// "dots" spinner (cli-spinners' own "dots" set) -- "⠧" was simply frame 7
// of this same 10-frame sequence, so the visual language doesn't change,
// it just starts moving, one frame per redraw.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// addressOpKinds maps every resource address named in p's own delta to
// resourceOpKind (style.go) -- the same classification renderCreates/
// renderModifies/renderDestroys already imply structurally (which Delta
// slice a resource sits in), but ship's own live per-resource progress
// narration needs an address->kind lookup instead of a slice to walk,
// since it observes ProgressEvent.Address one at a time as executor.Ship
// actually processes each resource (UBI-61 comment thread's "general
// +/-/~ header rule", extended to ship's own per-resource header line).
// Creates decode the same permissive {stack,type,name,config} node shape
// renderCreates uses (unexported in core, so re-decoded here) -- a node
// this can't make sense of is skipped silently, matching renderCreates'
// own posture, since an uncolored fallback header is harmless, never a
// panic or a missing line.
func addressOpKinds(p *core.Proposal) map[string]resourceOpKind {
	kinds := make(map[string]resourceOpKind, len(p.Delta.Creates)+len(p.Delta.Modifies)+len(p.Delta.Destroys))
	for _, raw := range p.Delta.Creates {
		var node struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &node); err != nil || node.Type == "" || node.Name == "" {
			continue
		}
		kinds[core.Address{Stack: p.Stack, Type: node.Type, Name: node.Name}.String()] = opCreate
	}
	for _, m := range p.Delta.Modifies {
		kinds[m.Target.String()] = opModify
	}
	for _, d := range p.Delta.Destroys {
		kinds[d.Address.String()] = opDestroy
	}
	return kinds
}

// newProgressPrinter builds executor.ProgressEvent callback for `ubx
// ship`'s own live narration (UBI-61, docs/cli-output-spec.md: "the
// read-back verification line is mandatory"). kinds (addressOpKinds)
// colors+bolds each resource's own address header by its operation kind
// (green create, red destroy, yellow modify) -- the UBI-61 comment
// thread's second finding, since before this the header line rendered
// fully plain regardless of what was actually happening to it.
//
// Returns the callback itself plus finish, which the caller MUST invoke
// exactly once, unconditionally, immediately after the executor.Ship call
// this callback was wired into returns (success, failure, or a context
// timeout alike -- see UBI-83 below for why).
//
// UBI-70 (founder test): the pre-UBI-70 version of this printer
// rendered EVERY transition as its own full line -- pending, in_flight,
// an ambiguous-result's own unknown_post_timeout, each reconcile_attempt
// -- which on a real multi-attempt destroy meant a "pending"/"in_flight"
// flash immediately followed by N nearly-identical "verifying via
// read-back (attempt K/N)" lines duplicating each other's own wording.
// Collapsed to the shape UBI-70 names: pending/in_flight/
// unknown_post_timeout never get their own line at all (sub-second
// scaffolding, or the pivot into verification the very next event
// already narrates); exactly one final checkmark/outcome line closes
// out the resource. An error event (Kind=="error") gets its own
// permanent line the moment it happens, per UBI-70's own "errors still
// get their own visible line" carve-out.
//
// UBI-67 (2026-08-02): core/executor's own walk is now genuinely
// concurrent -- N resources can be in_flight/verifying at once, which
// this printer's own pre-UBI-67 design assumed could never happen (its
// own doc comment said so directly): one SHARED ticker, and a `\r`
// -based single-line in-place overwrite that has no way to represent
// more than one concurrently-updating line on a real terminal. That
// session's own fix adopted real Terraform's own `apply` convention:
// every line fully discrete, never overwritten in place, prefixed with
// its own resource's address.
//
// UBI-75 (founder test, same day, post-UBI-67): the UBI-67 fix above
// wasn't enough on its own. It kept a SEPARATE, un-prefixed header line
// announcing each resource, with every SUBSEQUENT line for that resource
// indented and dim-address-prefixed underneath it -- a DIFFERENT
// resource's own still-ticking line landing directly under another
// resource's bare header read as "nested under that header," not "an
// unrelated resource's own line, interleaved." Fixed by dropping the
// separate header line entirely: every line this printer ever prints
// carries that resource's own address, bold+colored by operation kind,
// inline, every time -- so however real concurrency interleaves DISCRETE
// lines, each is legible in isolation.
//
// UBI-83 (founder re-test, same day, post-UBI-75): UBI-75 fixed
// self-identification, never the actual defect UBI-70 was originally
// meant to fix -- every tick of the "shipping" ticker (and every tick
// between real reconcile_attempt events) still called an APPEND-only
// print, so a real 26s SQS create produced 20+ near-duplicate "shipping"
// lines instead of ONE line ticking in place with an animated spinner,
// and a 45s pre-verification destroy wait produced 12+ near-duplicate
// "verifying via read-back" lines the same way. Root cause: nothing in
// this printer had ever distinguished "append a new line" from "redraw
// the current line in place" -- UBI-67's own discrete-line convention
// was a deliberate move AWAY from in-place redraw (a `\r`-only overwrite
// can't represent N concurrently-updating lines), and nothing since had
// revisited that call once concurrency demanded it.
//
// Fixed with real ANSI cursor addressing (not just `\r`, which UBI-67
// already correctly ruled out as insufficient for N concurrent
// resources): each resource address owns exactly one terminal ROW,
// tracked by its own physical row index (updateRow's own `rowOf`/
// `cursorRow`); redrawing that row moves the cursor up to it
// (`\x1b[<n>A`), clears it (`\x1b[2K`), writes the new content, and
// returns to the bottom (`\x1b[<n>B`) -- so N concurrently-ticking
// resources each redraw their OWN row in place without touching any
// other resource's own row, and a real terminal shows physically ONE
// line per resource changing in place, an actual animated spinner frame
// advancing each tick (spinnerFrames, not the old static "·"/fixed
// single "⠧"), never N appended lines. Named phases (the plain
// "shipping" wait, then read-back "verifying") share the SAME row when
// they're the same resource's own sequential phases -- the second phase
// simply redraws in place over the first's own last frame, exactly the
// founder's own spec ("each named phase gets exactly one line, updated
// in place while that phase runs, replaced by the next phase's own
// single line, or by shipped/failed as the final line"). A row's content
// becomes permanent (a real trailing "\n") the moment it stops being the
// bottom-most row -- either because a NEW row is about to be created
// below it, or because IT reaches its own terminal state while still at
// the bottom; `finish` covers the one remaining case, the run ending
// (success, failure, or timeout) while the last row is still open --
// without it, whatever prints next (the trailing report, or an error
// message) would silently overwrite that row's own final content instead
// of appending after it.
//
// Non-TTY is unaffected by any of this: every real event still renders
// as its own permanent, appended line (no ANSI codes, no redraw) exactly
// as before UBI-83 -- a log file has no use for "redraw in place," and
// ticks never fire there at all (startTicker's own tty check).
//
// truncateForRow (UBI-93) truncates s to at most maxLen runes, appending
// a trailing "…" when it does -- rune-based (not byte-based) so this
// never splits a multi-byte UTF-8 character mid-sequence, and always
// called on PLAIN text before renderContent ever wraps it in ANSI color
// codes, so it never risks splitting an escape sequence either. maxLen
// <= 0 is a no-op (the "truncation disabled" case maxRowTextWidth's own
// 0 return represents).
func truncateForRow(s string, maxLen int) string {
	if maxLen <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return string(r[:maxLen])
	}
	return string(r[:maxLen-1]) + "…"
}

func newProgressPrinter(out io.Writer, st *styler, tty bool, termWidth int, kinds map[string]resourceOpKind) (fn func(executor.ProgressEvent), finish func()) {
	starts := map[string]time.Time{}
	seenAddr := map[string]bool{}
	spin := map[string]int{}

	// UBI-84 (third finding): the max address length across the WHOLE
	// batch, computed once, up front -- the full set of resources this
	// run will ever process is already known (kinds, addressOpKinds' own
	// per-proposal map, covers every Delta.Creates/Modifies/Destroys
	// address before executor.Ship ever starts). Every row's own address
	// is padded to this shared width (renderContent, below) so the
	// ": shipped"/"· confirmed by..."/elapsed-time columns all start at
	// the identical column across every row, matching plan's own
	// attribute-block alignment, regardless of how much any one
	// resource's own address length differs from its neighbors'.
	maxAddrLen := 0
	for addr := range kinds {
		if len(addr) > maxAddrLen {
			maxAddrLen = len(addr)
		}
	}

	// UBI-93: maxRowTextWidth bounds how much of a row's own free-text
	// content (glyph text or a real provider's own diagnostic error,
	// ev.Detail, unbounded in length) this printer will ever render --
	// the load-bearing fix for a real, confirmed corruption mechanism, NOT
	// a locking/serialization issue (mu already serializes every write
	// correctly; verified directly, both via a hermetic stress test with
	// -race across many concurrently-erroring/ticking resources, clean,
	// AND via a real pty repro that reproduced the exact live shape).
	// Every row-tracking invariant above (cursorRow/rowOf, the "cursor up
	// N / redraw / cursor down N" math every existing-row redraw uses)
	// assumes one updateRow call consumes exactly ONE physical terminal
	// row. A row whose own content is longer than the terminal's real
	// column width WRAPS onto a second physical row -- silently breaking
	// that invariant: cursorRow under-counts the true number of physical
	// rows just consumed, so the NEXT row's own redraw computes the wrong
	// "up N" distance and lands on the WRAPPED CONTINUATION of the
	// previous line instead of the row it actually means to redraw,
	// clearing and overwriting only PART of it and leaving the rest
	// behind as orphaned, garbled leftover text -- exactly the "error
	// text and the following line's content interleaved mid-word" shape
	// found live (playground-15) and reproduced hermetically
	// (cli/progress_ticker_test.go's own
	// TestNewProgressPrinter_LongErrorText_TruncatedToOneRow_NeverWraps,
	// plus a real-pty repro in this session's own investigation notes
	// that isolated the exact mechanism before this fix was written).
	// Bounded at the source instead: text is truncated (with a trailing
	// "…") to fit within a single physical row before it's ever
	// rendered, so "one write, one row" always holds regardless of how
	// long the underlying message is. Nothing is lost PERMANENTLY -- the
	// full, untruncated text is already durably recorded in the ledger
	// (core.ResourceApply.Errors/Reconciliation) and always available via
	// `ubx why`; this only bounds what the LIVE ticking display shows.
	// termWidth <= 0 (undeterminable, e.g. every hermetic test's
	// bytes.Buffer) disables truncation entirely -- the pre-UBI-93
	// behavior -- since non-TTY output never uses this row-tracking
	// mechanism in the first place (updateRow's own !tty branch is a
	// plain sequential append, where a wrapped line is harmless: no
	// cursor math ever depends on its row count).
	maxRowTextWidth := func(hasElapsed bool) int {
		if !tty || termWidth <= 0 {
			return 0
		}
		// "  " + glyph(1) + " " + prefix(maxAddrLen+1) + " " -- the fixed
		// overhead every row pays regardless of content, plus a 1-column
		// safety margin (some terminals auto-wrap the instant the LAST
		// column is written, even without a following character, so this
		// stays strictly under the real width rather than landing exactly
		// on it).
		overhead := 2 + 1 + 1 + (maxAddrLen + 1) + 1 + 1
		if hasElapsed {
			// " " + a generous elapsed column (e.g. "12:34") -- this
			// printer's own renderElapsed never exceeds this in practice
			// (a real `ubx ship` finishing in 100+ minutes is not a
			// realistic case to budget column space for).
			overhead += 1 + 6
		}
		w := termWidth - overhead
		if w < 20 {
			// Never truncate to something so small it stops being a
			// useful line at all -- an unusually narrow real terminal
			// still gets a readable (if generously truncated) row rather
			// than a degenerate one.
			w = 20
		}
		return w
	}

	// Row-tracking state (TTY only) -- UBI-83's own in-place-redraw
	// mechanism. order/rowOf record which physical terminal row (a real
	// newline-delimited line) each row key -- a resource's own address,
	// or a uniquely-suffixed key for a permanent error line, see
	// updateRow -- was written to. cursorRow is the row the cursor is
	// CURRENTLY parked at (always at column 0, invariant maintained by
	// every write below); sealed reports whether that current row
	// already carries its own trailing "\n" (true trivially before
	// anything is printed, and again immediately after any row's own
	// final write).
	order := []string{}
	rowOf := map[string]int{}
	cursorRow := 0
	sealed := true

	var mu sync.Mutex
	stopChs := map[string]chan struct{}{}
	doneChs := map[string]chan struct{}{}

	// release/reacquire around stopTicker: the ticker goroutine's own
	// last in-flight tick needs mu to print, so holding it while waiting
	// on doneCh would deadlock. Every call site below re-locks
	// immediately after, so the enclosing mu.Lock()/defer mu.Unlock()
	// pair for the whole event stays balanced.
	release := func() { mu.Unlock() }
	reacquire := func() { mu.Lock() }

	stopTicker := func(address string) {
		stop, ok := stopChs[address]
		if !ok {
			return
		}
		done := doneChs[address]
		delete(stopChs, address)
		delete(doneChs, address)
		release()
		close(stop)
		<-done
		reacquire()
	}

	renderElapsed := func(d time.Duration) string {
		return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
	}

	renderContent := func(address string, kind resourceOpKind, glyph, text, elapsed string) string {
		// UBI-93: truncate BEFORE padding -- padding a truncated string
		// back out to progressLineWidth would silently re-introduce the
		// exact overflow this whole mechanism exists to prevent, on any
		// terminal narrower than progressLineWidth's own fixed 50 columns.
		padWidth := progressLineWidth
		if w := maxRowTextWidth(elapsed != ""); w > 0 {
			text = truncateForRow(text, w)
			if w < padWidth {
				padWidth = w
			}
		}
		padded := fmt.Sprintf("%-*s", padWidth, text)
		prefix := address
		if prefix != "" {
			// UBI-84: pad the address itself, inside the colored span, to
			// the shared maxAddrLen -- the colon (and everything after
			// it) then starts at the identical column on every row,
			// regardless of this row's own address length.
			prefix = st.OpHeader(kind, fmt.Sprintf("%-*s", maxAddrLen, address)) + ":"
		}
		if elapsed != "" {
			return fmt.Sprintf("  %s %s %s %s", glyph, prefix, padded, elapsed)
		}
		return fmt.Sprintf("  %s %s %s", glyph, prefix, text)
	}

	// updateRow is the ONE place this printer ever writes a resource's own
	// row -- UBI-83's core fix. rowKey lets an error line (which must stay
	// its own permanent, never-redrawn line, UBI-70) opt out of sharing a
	// row with the resource's own main ticking/terminal line by passing a
	// unique key instead of the bare address; every other caller passes
	// address for both. final marks a row that will never be redrawn
	// again (a terminal transition, or an error) -- sealed immediately
	// (a real trailing "\n") if it's still the bottom-most row.
	//
	// Non-TTY: no row tracking, no ANSI codes -- every call is its own
	// permanent appended line, exactly the pre-UBI-83 behavior, with the
	// identical blank-line-before-a-new-resource spacing UBI-61/UBI-75
	// already established.
	updateRow := func(rowKey, address string, kind resourceOpKind, glyph, text, elapsed string, final bool) {
		content := renderContent(address, kind, glyph, text, elapsed)

		newAddr := address != "" && !seenAddr[address]
		if newAddr {
			seenAddr[address] = true
		}

		if !tty {
			if newAddr && len(seenAddr) > 1 {
				fmt.Fprintln(out)
			}
			fmt.Fprintln(out, content)
			return
		}

		row, exists := rowOf[rowKey]
		if !exists {
			// UBI-84 (second finding): UBI-61/UBI-75's own blank-line-
			// between-resources spacer is gone here -- it existed to
			// disambiguate interleaved lines from concurrent resources
			// sharing one scrolling stream, a problem UBI-83's own
			// stable, non-interleaving one-row-per-resource design
			// already solved structurally. A blank line between rows now
			// reads as leftover noise, not disambiguation. Still seal
			// whatever's currently at the bottom before starting a new
			// row immediately below it, zero blank lines apart.
			if len(order) > 0 && !sealed {
				fmt.Fprint(out, "\n")
				cursorRow++
			}
			row = cursorRow
			rowOf[rowKey] = row
			order = append(order, rowKey)
			fmt.Fprint(out, content)
			sealed = false
		} else {
			n := cursorRow - row
			if n > 0 {
				fmt.Fprintf(out, "\x1b[%dA", n)
			}
			fmt.Fprint(out, "\r\x1b[2K")
			fmt.Fprint(out, content)
			if n > 0 {
				fmt.Fprint(out, "\r")
				fmt.Fprintf(out, "\x1b[%dB", n)
			}
		}
		if final && row == cursorRow {
			fmt.Fprint(out, "\n")
			cursorRow++
			sealed = true
		}
	}

	// nextSpinner advances address's own animation by one frame and
	// returns it raw (uncolored) -- callers wrap it in whichever color
	// this phase uses (shipping dim, verifying yellow), matching the
	// pre-UBI-83 per-phase color convention exactly; only the glyph
	// itself now moves.
	nextSpinner := func(address string) string {
		i := spin[address]
		spin[address] = i + 1
		return spinnerFrames[i%len(spinnerFrames)]
	}

	// startTicker redraws one resource's own row in place every
	// tickInterval until stopTicker(address) -- shared by both live
	// phases this printer narrates (the raw provider-call wait, and the
	// read-back verification wait between attempts), TTY-only (see doc
	// comment). Per-address (UBI-67): N concurrently in-flight resources
	// each get their own independent ticker goroutine, none sharing state
	// with any other; each redraws only its OWN row (UBI-83), never
	// another's. color is applied fresh each tick (not captured once at
	// start), since the spinner frame itself changes every tick.
	startTicker := func(address string, kind resourceOpKind, start time.Time, color func(string) string, text string) {
		if !tty {
			return
		}
		stop, done := make(chan struct{}), make(chan struct{})
		stopChs[address], doneChs[address] = stop, done
		go func() {
			defer close(done)
			ticker := time.NewTicker(tickInterval)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					mu.Lock()
					updateRow(address, address, kind, color(nextSpinner(address)), text, renderElapsed(time.Since(start)), false)
					mu.Unlock()
				}
			}
		}()
	}

	fn = func(ev executor.ProgressEvent) {
		mu.Lock()
		defer mu.Unlock()

		now := time.Now()
		kind := kinds[ev.Address]
		if ev.Address != "" {
			if _, ok := starts[ev.Address]; !ok {
				starts[ev.Address] = now
			}
		}
		elapsed := renderElapsed(now.Sub(starts[ev.Address]))

		switch ev.Kind {
		case "error":
			// UBI-70: an error gets its own permanent line the moment it
			// happens -- never silently dropped, never folded into the
			// one live-updating line the happy path uses. A unique row
			// key (never reused) keeps it from ever being redrawn over --
			// UBI-83's redraw mechanism only ever targets a row key a
			// caller reuses on purpose.
			stopTicker(ev.Address)
			errKey := fmt.Sprintf("%s#error#%d", ev.Address, len(order))
			updateRow(errKey, ev.Address, kind, st.Yellow("!"), ev.Detail, "", true)

		case "transition":
			switch ev.State {
			case "pending", "in_flight", "unknown_post_timeout":
				// UBI-70's own target shape: sub-second scaffolding
				// (pending/in_flight) or the pivot into verification
				// (unknown_post_timeout, whose own detail -- when it has
				// one -- is the exact wording the reconcile_attempt
				// events immediately following it already narrate) never
				// get their own line. in_flight alone starts the
				// "shipping" ticker (UBI-61 comment thread's rename:
				// "in_flight" read as jargon) for a real, possibly slow
				// raw provider call; the other two states only ever stop
				// whatever ticker preceded them for THIS address.
				stopTicker(ev.Address)
				if ev.State == "in_flight" {
					startTicker(ev.Address, kind, starts[ev.Address], st.Dim, "shipping")
				}
				return
			}

			glyph := st.Dim("·")
			switch ev.State {
			case "applied":
				glyph = st.Green("✓")
			case "failed":
				glyph = st.Red("✗")
			case "still_unknown":
				glyph = st.Yellow("?")
			}
			text := displayResourceState(ev.State)
			if ev.Detail != "" {
				text += " · " + ev.Detail
			}
			stopTicker(ev.Address)
			updateRow(ev.Address, ev.Address, kind, glyph, text, elapsed, true)

		case "reconcile_attempt":
			// UBI-70: ONE row-owning update per real attempt, plus (TTY
			// only) that same address's own ticker redrawing that SAME
			// row in place every tickInterval between attempts (UBI-83)
			// so elapsed and the spinner frame both keep advancing
			// instead of looking frozen through a real multi-second/
			// minute backoff wait -- never a new appended line either
			// way.
			text := fmt.Sprintf("%s (attempt %d/%d)", ev.Detail, ev.Attempt, ev.Total)
			stopTicker(ev.Address)
			updateRow(ev.Address, ev.Address, kind, st.Yellow(nextSpinner(ev.Address)), text, elapsed, false)
			startTicker(ev.Address, kind, starts[ev.Address], st.Yellow, text)
		}
	}

	// finish seals whatever row is still open (the run ended -- success,
	// failure, or a context timeout -- while its own resource was still
	// mid-phase) so the caller's own next write (the trailing report, or
	// an error message) starts on a fresh line instead of overwriting
	// this row's own last content. A no-op if everything already reached
	// its own terminal state (the common case) or nothing was ever
	// printed (--json/non-TTY, or a run that failed before any progress
	// event arrived).
	finish = func() {
		mu.Lock()
		defer mu.Unlock()
		if tty && len(order) > 0 && !sealed {
			fmt.Fprint(out, "\n")
			cursorRow++
			sealed = true
		}
	}

	return fn, finish
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
	fmt.Fprintf(out, "%s: already fully shipped -- nothing to do\n", p.ID)
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
	// UBI-84: no per-resource repeat block here -- UBI-83's own in-place
	// row already showed every resource's own final state ("✓ <address>:
	// shipped · confirmed by reconciliation  0:03"), live, and (UBI-70,
	// unconditionally, TTY or not) any error's own detail already got its
	// own permanent line the moment it happened. Reprinting "✓ shipped:
	// <address>" and each error's classification/message again here was
	// pure duplication of what's already on screen -- `ubx why` remains
	// the authoritative place for full per-resource history after the
	// fact. Only the genuine summary (never shown live) survives below.
	//
	// UBI-75 (third finding): blank line before the closing summary, bold
	// throughout, per-count colored (green shipped, red failed, dim
	// still-unknown -- this package's 7-code palette has no true gray,
	// and dim already carries "neither a pass nor a verdict" elsewhere,
	// e.g. forceDim's own metadata use). "shipped"/"outcome: shipped,"
	// never "applied" (UBI-79) -- displayOutcome only ever touches this
	// rendered text, never rec.Summary.Outcome's own stored/hashed value.
	fmt.Fprintln(out)
	fmt.Fprintln(out, st.forceBold(fmt.Sprintf("%d resource(s), %s, %s, %s -- outcome: %s",
		len(rec.Resources),
		st.Green(fmt.Sprintf("%d shipped", rec.Summary.ResourcesApplied)),
		st.Red(fmt.Sprintf("%d failed", rec.Summary.ResourcesFailed)),
		st.Dim(fmt.Sprintf("%d still unknown", rec.Summary.ResourcesStillUnknown)),
		displayOutcome(rec.Summary.Outcome))))
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
