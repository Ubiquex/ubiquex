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
				ctx = executor.WithProgress(ctx, newProgressPrinter(out, st, isTerminal(cmd.OutOrStdout()), addressOpKinds(p)))
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
// review, for a plan a human has already read.
func renderShipConfirmSummary(out io.Writer, st *styler, p *core.Proposal, age string) {
	fmt.Fprintf(out, "Ship  %s · %s · %s %s %s\n",
		p.Stack, age,
		st.Green(fmt.Sprintf("+%d create(s)", p.BlastRadius.Creates)),
		st.Yellow(fmt.Sprintf("~%d modify(ies)", p.BlastRadius.Modifies)),
		st.Red(fmt.Sprintf("-%d destroy(s)", p.BlastRadius.Destroys)))
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
// convention) -- reused by the ticker's own in-place-overwrite padding
// so a shrinking elapsed-time string (rare, but possible if a
// double-digit minute count is somehow followed by a shorter render)
// never leaves stale trailing characters on a real terminal.
const progressLineWidth = 50

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
// (`st.OpHeader(kind, address)`, printed once, flush-left, no address
// convention applied to it at all) announcing each resource, with every
// SUBSEQUENT line for that resource indented and dim-address-prefixed
// underneath it. Confirmed live (four real concurrent resources, SQS's
// own 26s creation lag overlapping everything else finishing in ~1s):
// a resource's own bare header line, being the one line in this whole
// design that DOESN'T carry the indented/prefixed convention, reads as
// its own distinct kind of content -- so when a DIFFERENT resource's
// still-ticking, indented line lands directly under it (pure bad luck
// of concurrent timing, exactly what real concurrency guarantees will
// eventually happen), it reads as "nested under the header above,"
// not "an unrelated resource's own line, interleaved." The header
// itself was the structural inconsistency, not the interleaving.
//
// Fixed by dropping the separate header line entirely: every line this
// printer ever prints -- the first for a resource, same as the last --
// carries that resource's own address, bold+colored by operation kind
// (st.OpHeader, previously reserved for the one-time header only), inline,
// every time (the ticket's own "[sqs] shipping 0:14" prefix-style
// suggestion). There is no longer a "different-looking" line for any
// resource to be mistaken as; every line is self-identifying on its own,
// so however real concurrency interleaves them, each is legible in
// isolation. A blank line still separates a resource's own first printed
// line from whatever preceded it (unchanged from UBI-67), purely for
// visual breathing room, not for grouping correctness -- correctness now
// comes entirely from each line naming its own resource, never from
// position.
//
// The ticker (startTicker/stopTicker, UBI-63 bug 3's own mechanism) stays
// keyed per-address, TTY-only, exactly as UBI-67 left it -- multiple
// resources' own tickers run concurrently, each emitting its own
// discrete, self-identifying line every tickInterval, never colliding.
func newProgressPrinter(out io.Writer, st *styler, tty bool, kinds map[string]resourceOpKind) func(executor.ProgressEvent) {
	starts := map[string]time.Time{}
	seen := map[string]bool{}

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

	// printLine renders every single line this printer ever emits --
	// UBI-75: the resource's own address, bold+colored by operation kind
	// (st.OpHeader), is part of EVERY line now, not a one-time separate
	// header -- so a reader can identify which resource any one line
	// belongs to without relying on its position relative to other lines.
	printLine := func(address string, kind resourceOpKind, glyph, text, elapsed string) {
		padded := fmt.Sprintf("%-*s", progressLineWidth, text)
		prefix := address
		if prefix != "" {
			prefix = st.OpHeader(kind, address) + ":"
		}
		if elapsed != "" {
			fmt.Fprintf(out, "  %s %s %s %s\n", glyph, prefix, padded, elapsed)
		} else {
			fmt.Fprintf(out, "  %s %s %s\n", glyph, prefix, text)
		}
	}

	// startTicker emits one discrete, self-identifying line every
	// tickInterval until stopTicker(address) -- shared by both live
	// phases this printer narrates (the raw provider-call wait, and the
	// read-back verification wait between attempts), TTY-only (see doc
	// comment). Per-address (UBI-67): N concurrently in-flight
	// resources each get their own independent ticker goroutine, none
	// sharing state with any other.
	startTicker := func(address string, kind resourceOpKind, start time.Time, glyph, text string) {
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
					printLine(address, kind, glyph, text, renderElapsed(time.Since(start)))
					mu.Unlock()
				}
			}
		}()
	}

	return func(ev executor.ProgressEvent) {
		mu.Lock()
		defer mu.Unlock()

		now := time.Now()
		kind := kinds[ev.Address]
		if ev.Address != "" {
			if _, ok := starts[ev.Address]; !ok {
				starts[ev.Address] = now
			}
			if !seen[ev.Address] {
				if len(seen) > 0 {
					// UBI-61 comment thread's own second finding, kept by
					// UBI-75: a blank line ahead of a new resource's own
					// first printed line -- breathing room only, every
					// line (this one included) is self-identifying now, so
					// nothing downstream depends on this blank line for
					// correctness the way the old separate-header design did.
					fmt.Fprintln(out)
				}
				seen[ev.Address] = true
			}
		}
		elapsed := renderElapsed(now.Sub(starts[ev.Address]))

		switch ev.Kind {
		case "error":
			// UBI-70: an error gets its own permanent line the moment it
			// happens -- never silently dropped, never folded into the
			// one live-updating line the happy path uses.
			stopTicker(ev.Address)
			printLine(ev.Address, kind, st.Yellow("!"), ev.Detail, "")

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
					startTicker(ev.Address, kind, starts[ev.Address], st.Dim("·"), "shipping")
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
			printLine(ev.Address, kind, glyph, text, elapsed)

		case "reconcile_attempt":
			// UBI-70: ONE discrete verification line per real attempt,
			// plus (TTY only) that same address's own ticker re-emitting
			// a fresh discrete line every tickInterval between attempts
			// so elapsed keeps advancing instead of looking frozen
			// through a real multi-second/minute backoff wait.
			text := fmt.Sprintf("%s (attempt %d/%d)", ev.Detail, ev.Attempt, ev.Total)
			stopTicker(ev.Address)
			printLine(ev.Address, kind, st.Yellow("⠧"), text, elapsed)
			startTicker(ev.Address, kind, starts[ev.Address], st.Yellow("⠧"), text)
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
	for _, ra := range rec.Resources {
		state, _ := ra.LastState()
		glyph := st.Green("✓")
		if state == core.ResourceFailed || state == core.ResourceStillUnknown {
			glyph = st.Red("✗")
		}
		fmt.Fprintf(out, "%s %s: %s\n", glyph, displayResourceState(string(state)), ra.Address)
		for _, e := range ra.Errors {
			fmt.Fprintf(out, "  %s: %s\n", e.Classification, e.Message)
		}
	}
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
