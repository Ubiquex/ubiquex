package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
)

// newVerifyCmd is UBI-38, the auditor's command: independent full-chain
// verification, one command, offline. The product's own claim is
// "independently verifiable" -- this makes the claim a demo. It re-checks
// everything checkable without trusting a single already-computed value
// anywhere: every proposal's own content hash against its own id (never
// checked anywhere else, on read, until this command -- core.Hash is only
// ever CALLED to compute an id, never to re-verify one), the parent-chain
// walk itself (never core.Ledger.Chain's own all-or-nothing error return,
// so one broken link is a reported finding, not a command crash), every
// sealed apply record's own hash and its prior-sealed-attempt chaining,
// every $redacted marker's own inner shape (core.IsRedactedValue only
// ever checked the outer shape).
//
// Read-only, offline, no provider and no credentials. A loopback acceptance (UBI-298) is re-checked offline
// from the ledger alone: its stored render, that render's hash, and a
// fresh render of the proposal. Local acceptance is reported as convenience-tier --
// there is nothing to independently re-derive it against, and reporting
// it as "verified" would be exactly the kind of rounding-up this command
// exists to refuse.
//
// An acceptance from a method that has since been removed (pr_merge and
// pr_review, UBI-300) is history: the record still reads and the chain
// still verifies, but nothing exists any more to re-derive it against, so
// it is reported as exactly that and never as verified, failed or
// crashed.
//
// Exit codes (UBI-20): 0 everything checks out; 1 a real finding (a
// broken hash, a chain break, a failed acceptance re-check); 2 a genuine
// error (corrupt head, an unreadable store). An inconclusive acceptance
// check is reported honestly and never rounds up to either 1 or 2 --
// "couldn't check" and "checked and found a problem" are not the same
// claim.
func newVerifyCmd() *cobra.Command {
	var (
		ledgerDir string
		stack     string
		jsonOut   bool
	)

	cmd := &cobra.Command{
		Use:           "verify",
		Short:         "Independently re-verify the ledger's own chain integrity, offline",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("verify: %w", err)}
			}
			applyStackDefault(cmd, &stack, cfg)

			ledger, closeLedger, err := openLedgerForStack(cmd.Context(), ledgerDir, stack, cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("verify: %w", err)}
			}
			defer closeLedger()

			result, err := core.VerifyChain(ledger)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("verify: %w", err)}
			}

			out := cmd.OutOrStdout()
			worst := 0
			if !result.ChainIntact {
				worst = 1
			}

			var checks []acceptanceCheckJSON
			derived, convenience, inconclusive, removed := 0, 0, 0, 0
			for _, p := range result.Proposals {
				if stack != "" && p.Stack != stack {
					continue
				}
				if p.Acceptance == nil {
					continue
				}
				switch p.Acceptance.Method {
				case "local":
					convenience++
					checks = append(checks, acceptanceCheckJSON{
						ProposalID: p.ID, Method: "local", OK: true,
						Note: "convenience-tier acceptance -- nothing to independently re-derive it against",
					})
				case "pr_merge", "pr_review":
					removed++
					checks = append(checks, acceptanceCheckJSON{
						ProposalID: p.ID, Method: p.Acceptance.Method, RemovedMethod: true,
						Note: core.RemovedAcceptanceMethod(p.Acceptance.Method),
					})
				case core.MethodLoopback:
					// UBI-298: re-checked from the ledger alone -- the
					// stored render, its hash, and a fresh render of the
					// proposal. Counted with the other re-derived methods.
					lc, lerr := core.VerifyLoopbackAcceptance(ledger, p)
					if lerr != nil {
						return &ExitCodeError{Code: 2, Err: fmt.Errorf("verify: %w", lerr)}
					}
					entry := acceptanceCheckJSON{
						ProposalID: p.ID, Method: core.MethodLoopback,
						Outcome: lc.Outcome, Note: lc.Detail,
					}
					switch {
					case lc.Inconclusive():
						entry.Inconclusive = true
						inconclusive++
					case lc.Failed():
						derived++
						if worst < 1 {
							worst = 1
						}
					default:
						entry.OK = true
						derived++
					}
					checks = append(checks, entry)
				default:
					checks = append(checks, acceptanceCheckJSON{
						ProposalID: p.ID, Method: string(p.Acceptance.Method),
						Note: "unrecognized acceptance method -- not re-verified",
					})
				}
			}

			if jsonOut {
				payload := verifyJSON{
					Format:           jsonFormatVersion,
					ProposalsChecked: result.ProposalsChecked,
					ChainIntact:      result.ChainIntact,
					Findings:         chainFindingsToJSON(result.Findings),
					Acceptance: verifyAcceptanceSummaryJSON{
						Derived:      derived,
						Convenience:  convenience,
						Inconclusive: inconclusive,
						Removed:      removed,
						Checks:       checks,
					},
				}
				if err := writeJSON(out, payload); err != nil {
					return &ExitCodeError{Code: 2, Err: err}
				}
			} else {
				renderVerifyHuman(out, result, checks, derived, convenience, inconclusive, removed)
			}

			if worst == 0 {
				return nil
			}
			return &ExitCodeError{Code: worst, Err: fmt.Errorf("verify: %d chain finding(s), %d acceptance re-derivation(s) failed -- see above", len(result.Findings), countFailedAcceptance(checks))}
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&stack, "stack", "", "which stack to verify -- required only for a remote [ledger] store; git-local verifies every proposal in the chain regardless of stack, --stack narrows which findings get reported")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit one JSON document instead of human text")
	return cmd
}

func renderVerifyHuman(out io.Writer, result *core.VerifyResult, checks []acceptanceCheckJSON, derived, convenience, inconclusive, removed int) {
	fmt.Fprintf(out, "verify: %d proposal(s) checked\n", result.ProposalsChecked)
	for _, f := range result.Findings {
		fmt.Fprintf(out, "  %s: %s -- %s\n", f.ProposalID, f.Kind, f.Detail)
	}
	if result.ChainIntact {
		fmt.Fprintln(out, "chain: intact")
	} else {
		fmt.Fprintf(out, "chain: BROKEN (%d finding(s), see above)\n", len(result.Findings))
	}

	fmt.Fprintf(out, "acceptance: %d re-derived, %d convenience-tier (local), %d inconclusive", derived, convenience, inconclusive)
	if removed > 0 {
		fmt.Fprintf(out, ", %d from a removed method (not re-verified)", removed)
	}
	fmt.Fprintln(out)
	for _, c := range checks {
		if c.OK && c.Method == core.MethodLoopback {
			// A passing loopback check is printed, unlike other passes,
			// because what it proves is narrower than "verified" suggests.
			fmt.Fprintf(out, "  %s (loopback): VERIFIED -- %s\n", c.ProposalID, c.Note)
			continue
		}
		if c.OK || c.Method == "local" {
			continue
		}
		if c.RemovedMethod {
			fmt.Fprintf(out, "  %s (%s): NOT RE-VERIFIED -- %s\n", c.ProposalID, c.Method, c.Note)
			continue
		}
		status := "INCONCLUSIVE"
		if !c.Inconclusive {
			status = "FAILED"
		}
		fmt.Fprintf(out, "  %s (%s): %s -- %s\n", c.ProposalID, c.Method, status, c.Note)
	}
}

func countFailedAcceptance(checks []acceptanceCheckJSON) int {
	n := 0
	for _, c := range checks {
		if !c.OK && !c.Inconclusive && !c.RemovedMethod && c.Method != "local" {
			n++
		}
	}
	return n
}

// exitCodeOf extracts an *ExitCodeError's own Code, defaulting to 2 for
// any other non-nil error (a tool failure this command didn't anticipate
// is still an error, never silently treated as a mere finding) and 0 for
// nil.
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ec *ExitCodeError
	if errors.As(err, &ec) {
		return ec.Code
	}
	return 2
}

// verifyJSON is `ubx verify --json`'s own top-level document (UBI-20
// workstream 2, UBI-38).
type verifyJSON struct {
	Format           int                         `json:"format"`
	ProposalsChecked int                         `json:"proposals_checked"`
	ChainIntact      bool                        `json:"chain_intact"`
	Findings         []chainFindingJSON          `json:"findings,omitempty"`
	Acceptance       verifyAcceptanceSummaryJSON `json:"acceptance"`
}

type chainFindingJSON struct {
	ProposalID string `json:"proposal_id"`
	Kind       string `json:"kind"`
	Detail     string `json:"detail"`
}

func chainFindingsToJSON(findings []core.ChainFinding) []chainFindingJSON {
	out := make([]chainFindingJSON, 0, len(findings))
	for _, f := range findings {
		out = append(out, chainFindingJSON{ProposalID: f.ProposalID, Kind: f.Kind, Detail: f.Detail})
	}
	return out
}

type verifyAcceptanceSummaryJSON struct {
	Derived      int `json:"derived"`
	Convenience  int `json:"convenience_tier"`
	Inconclusive int `json:"inconclusive"`
	// Removed counts acceptances from a method that no longer exists
	// (pr_merge, pr_review; UBI-300). History: read and chain-verified,
	// never re-derived. Additive under format 1.
	Removed int                   `json:"removed_method"`
	Checks  []acceptanceCheckJSON `json:"checks,omitempty"`
}

type acceptanceCheckJSON struct {
	ProposalID   string `json:"proposal_id"`
	Method       string `json:"method"`
	OK           bool   `json:"ok"`
	Inconclusive bool   `json:"inconclusive,omitempty"`
	// RemovedMethod marks an acceptance from a method that no longer
	// exists (UBI-300). The pr_merge-only git_ok/reviewers_checked/
	// reviewers_match fields went with the re-derivation they reported.
	RemovedMethod bool `json:"removed_method,omitempty"`
	// Outcome is set for method "loopback" only (UBI-298): one of
	// verified, no_rendered_hash, rendered_missing,
	// rendered_hash_mismatch, rerender_differs, renderer_version_changed
	// (core's Loopback* constants), so a script can tell the failures
	// apart without parsing Note. Additive under format 1.
	Outcome string `json:"outcome,omitempty"`
	Note    string `json:"note,omitempty"`
}

// methodLabel is how an acceptance method reads to a person: the recorded
// value, marked when it names a method that no longer exists (pr_merge,
// pr_review; UBI-300). The record is history and shown as recorded; the
// mark keeps a reader from taking it for a method they can still use.
// JSON output keeps the raw recorded value.
func methodLabel(method string) string {
	if core.RemovedAcceptanceMethod(method) != "" {
		return method + " (removed method)"
	}
	return method
}
