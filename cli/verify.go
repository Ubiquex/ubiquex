package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
	ghub "github.com/ubiquex/ubiquex/github"
)

// runVerifyAcceptance is `ubx why --verify-acceptance` (UBI-11 stage 1,
// docs/architecture.md — Decision loop: "acceptance claiming a merge must
// be checkable against git history + API anytime"). Runs two independent
// checks:
//
//  1. Git: p.Acceptance.MergeSHA still exists in repoDir's history, and
//     the proposal file at that exact commit still hashes to p.ID. Either
//     failing is a hard error — an acceptance record that can no longer be
//     verified against the history it claims is a serious finding, not a
//     soft one.
//  2. GitHub API (only if githubRepo is supplied): the PR's *current*
//     approving reviewers are re-fetched and compared to what's recorded.
//     A mismatch (e.g. a reviewer dismissed their approval after the fact)
//     is reported clearly but does not fail the command — the ledger
//     entry correctly recorded what was true at acceptance time; reality
//     having moved on since is exactly the kind of thing this check
//     exists to surface, not evidence the ledger itself is wrong.
//
// Non-pr_merge acceptances (or proposals with no acceptance at all) are
// reported as not applicable, not an error.
//
// Exit code is a CI contract (UBI-20, docs/exit-codes.mdx): a git-history
// failure (commit gone, file gone/rehashed at that commit) or a reviewer
// mismatch means the claimed acceptance doesn't check out -- an
// actionable finding, exit 1. A genuine tool/network failure (can't run
// git, can't reach the GitHub API, a malformed --github-repo) is exit 2.
// Nothing to check, or everything checks out, is exit 0.
//
// jsonMode suppresses every human-text print to out (UBI-20 workstream 2:
// `ubx why --json` emits exactly one JSON document, never mixed with the
// human-format progress lines) -- the checks and their exit-code
// classification are identical either way, only the reporting differs.
// The returned *verifyAcceptanceJSON is always non-nil and is `why --json`'s
// "verify_acceptance" field, whether or not jsonMode is set (a caller not
// in JSON mode can simply ignore it).
func runVerifyAcceptance(ctx context.Context, out io.Writer, p *core.Proposal, repoDir, githubRepo string, jsonMode bool) (*verifyAcceptanceJSON, error) {
	if !jsonMode {
		fmt.Fprintln(out, "--- verify-acceptance ---")
	}

	if p.Acceptance == nil || p.Acceptance.Method != "pr_merge" {
		if !jsonMode {
			fmt.Fprintf(out, "acceptance method is %q -- nothing to re-verify (only pr_merge is derived from git/GitHub)\n", acceptanceMethod(p))
		}
		return &verifyAcceptanceJSON{Applicable: false}, nil
	}
	a := p.Acceptance
	result := &verifyAcceptanceJSON{Applicable: true, Method: a.Method}

	exists, err := ghub.CommitExists(ctx, repoDir, a.MergeSHA)
	if err != nil {
		return result, &ExitCodeError{Code: 2, Err: fmt.Errorf("verify-acceptance: %w", err)}
	}
	if !exists {
		return result, &ExitCodeError{Code: 1, Err: fmt.Errorf("verify-acceptance: %w: %s no longer exists in %s's history", ghub.ErrCommitNotFound, a.MergeSHA, repoDir)}
	}
	if !jsonMode {
		fmt.Fprintf(out, "git: merge commit %s exists in %s\n", a.MergeSHA, repoDir)
	}

	if a.ProposalFile != "" {
		content, err := ghub.FileAtCommit(ctx, repoDir, a.MergeSHA, a.ProposalFile)
		if err != nil {
			return result, &ExitCodeError{Code: 2, Err: fmt.Errorf("verify-acceptance: %w", err)}
		}
		var atCommit core.Proposal
		if err := json.Unmarshal(content, &atCommit); err != nil {
			return result, &ExitCodeError{Code: 2, Err: fmt.Errorf("verify-acceptance: parse %s at %s: %w", a.ProposalFile, a.MergeSHA, err)}
		}
		hash, err := core.Hash(&atCommit)
		if err != nil {
			return result, &ExitCodeError{Code: 2, Err: fmt.Errorf("verify-acceptance: %w", err)}
		}
		if hash != p.ID {
			return result, &ExitCodeError{Code: 1, Err: fmt.Errorf("verify-acceptance: %w: %s at %s now hashes to %s, not %s",
				core.ErrTrailerHashMismatch, a.ProposalFile, a.MergeSHA, hash, p.ID)}
		}
		if !jsonMode {
			fmt.Fprintf(out, "git: %s at that commit still hashes to %s\n", a.ProposalFile, p.ID)
		}
	}
	result.GitOK = true

	if githubRepo == "" {
		if !jsonMode {
			fmt.Fprintln(out, "github API: skipped (no --github-repo given) -- reviewer re-check is inconclusive, not a pass")
		}
		return result, nil
	}
	owner, repo, ok := strings.Cut(githubRepo, "/")
	if !ok || owner == "" || repo == "" {
		return result, &ExitCodeError{Code: 2, Err: fmt.Errorf("verify-acceptance: --github-repo must be \"owner/name\", got %q", githubRepo)}
	}

	var apiOpts []ghub.Option
	if base := os.Getenv("UBX_GITHUB_API_BASE_URL"); base != "" {
		apiOpts = append(apiOpts, ghub.WithBaseURL(base))
	}
	api := ghub.New(os.Getenv("GITHUB_TOKEN"), apiOpts...)

	current, err := api.ApprovingReviewers(ctx, owner, repo, a.PRNumber)
	if err != nil {
		if !jsonMode {
			fmt.Fprintf(out, "github API: could not re-fetch reviews for PR #%d: %v\n", a.PRNumber, err)
		}
		return result, &ExitCodeError{Code: 2, Err: fmt.Errorf("verify-acceptance: could not re-fetch reviews for PR #%d: %w", a.PRNumber, err)}
	}
	result.ReviewersChecked = true
	result.CurrentApprovers = current
	result.RecordedApprovers = a.Approvers
	result.ReviewersMatch = sameSet(current, a.Approvers)

	if result.ReviewersMatch {
		if !jsonMode {
			fmt.Fprintf(out, "github API: PR #%d's approvers are unchanged (%v)\n", a.PRNumber, current)
		}
		return result, nil
	}
	if !jsonMode {
		fmt.Fprintf(out, "github API: MISMATCH -- PR #%d's approvers are now %v, recorded acceptance has %v\n",
			a.PRNumber, current, a.Approvers)
	}
	return result, &ExitCodeError{Code: 1, Err: fmt.Errorf("verify-acceptance: PR #%d's approving reviewers changed since acceptance (see above)", a.PRNumber)}
}

// verifyAcceptanceJSON is `ubx why --json --verify-acceptance`'s
// "verify_acceptance" field (UBI-20 workstream 2). Applicable is false for
// a non-pr_merge (or unaccepted) proposal, in which case every other field
// is zero-valued -- not a claim that they were checked and found empty.
type verifyAcceptanceJSON struct {
	Applicable        bool     `json:"applicable"`
	Method            string   `json:"method,omitempty"`
	GitOK             bool     `json:"git_ok,omitempty"`
	ReviewersChecked  bool     `json:"reviewers_checked"`
	ReviewersMatch    bool     `json:"reviewers_match,omitempty"`
	CurrentApprovers  []string `json:"current_approvers,omitempty"`
	RecordedApprovers []string `json:"recorded_approvers,omitempty"`
}

func acceptanceMethod(p *core.Proposal) string {
	if p.Acceptance == nil {
		return "(none)"
	}
	return p.Acceptance.Method
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sa, sb := append([]string{}, a...), append([]string{}, b...)
	sort.Strings(sa)
	sort.Strings(sb)
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	return true
}

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
// ever checked the outer shape), and -- where a --repo-dir is given --
// re-derives every pr_merge acceptance against git history (network-
// optional) and, with --github-repo too, the GitHub API (see
// runVerifyAcceptance, reused completely unchanged; this command's own
// job is to run it once per pr_merge-accepted proposal the chain walk
// found, not to reimplement it).
//
// Read-only, no provider, no credentials needed for the offline core
// (hash/chain/apply/redacted checks) -- --repo-dir/--github-repo are the
// one opt-in exception, and even then it's read-only git/API calls, never
// a write anywhere. Local acceptance is reported as convenience-tier --
// there is nothing to independently re-derive it against, and reporting
// it as "verified" would be exactly the kind of rounding-up this command
// exists to refuse.
//
// Exit codes (UBI-20): 0 everything checks out; 1 a real finding (a
// broken hash, a chain break, a failed acceptance re-derivation); 2 a
// genuine error (corrupt head, network/tool failure re-deriving
// acceptance). An inconclusive acceptance check (no --repo-dir given, or
// --repo-dir given but no --github-repo for the reviewer re-check) is
// reported honestly and never rounds up to either 1 or 2 -- "couldn't
// check" and "checked and found a problem" are not the same claim.
func newVerifyCmd() *cobra.Command {
	var (
		ledgerDir  string
		stack      string
		repoDir    string
		githubRepo string
		jsonOut    bool
		timeout    time.Duration
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
			applyGithubRepoDefault(cmd, &githubRepo, cfg)

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
			derived, convenience, inconclusive := 0, 0, 0
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
				case "pr_merge":
					if repoDir == "" {
						inconclusive++
						checks = append(checks, acceptanceCheckJSON{
							ProposalID: p.ID, Method: "pr_merge", Inconclusive: true,
							Note: "not re-verified -- no --repo-dir given",
						})
						continue
					}
					ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
					vres, verr := runVerifyAcceptance(ctx, io.Discard, p, repoDir, githubRepo, true)
					cancel()

					entry := acceptanceCheckJSON{ProposalID: p.ID, Method: "pr_merge"}
					if vres != nil {
						entry.GitOK = vres.GitOK
						entry.ReviewersChecked = vres.ReviewersChecked
						entry.ReviewersMatch = vres.ReviewersMatch
					}
					switch {
					case verr == nil && vres != nil && vres.GitOK && !vres.ReviewersChecked:
						// Git history checks out; the GitHub reviewer
						// re-check never ran at all (no --github-repo) --
						// inconclusive on that half, not a pass rounded up.
						entry.Inconclusive = true
						entry.Note = "git history checks out; reviewer re-check not run -- no --github-repo given"
						inconclusive++
					case verr == nil:
						entry.OK = true
						derived++
					default:
						entry.Note = verr.Error()
						derived++
						if code := exitCodeOf(verr); code > worst {
							worst = code
						}
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
						Checks:       checks,
					},
				}
				if err := writeJSON(out, payload); err != nil {
					return &ExitCodeError{Code: 2, Err: err}
				}
			} else {
				renderVerifyHuman(out, result, checks, derived, convenience, inconclusive)
			}

			if worst == 0 {
				return nil
			}
			return &ExitCodeError{Code: worst, Err: fmt.Errorf("verify: %d chain finding(s), %d acceptance re-derivation(s) failed -- see above", len(result.Findings), countFailedAcceptance(checks))}
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&stack, "stack", "", "which stack to verify -- required only for a remote [ledger] store; git-local verifies every proposal in the chain regardless of stack, --stack narrows which findings get reported")
	cmd.Flags().StringVar(&repoDir, "repo-dir", "", "local git working tree to re-derive pr_merge acceptances against (omit to report them as inconclusive rather than attempt it)")
	cmd.Flags().StringVar(&githubRepo, "github-repo", "", "owner/name of the GitHub repository, for the reviewer re-check half of pr_merge re-derivation (git-history re-check runs without it, given --repo-dir)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit one JSON document instead of human text (UBI-20)")
	cmd.Flags().DurationVar(&timeout, "timeout", time.Minute, "timeout for each pr_merge acceptance's own re-derivation call (only with --repo-dir)")
	return cmd
}

func renderVerifyHuman(out io.Writer, result *core.VerifyResult, checks []acceptanceCheckJSON, derived, convenience, inconclusive int) {
	fmt.Fprintf(out, "verify: %d proposal(s) checked\n", result.ProposalsChecked)
	for _, f := range result.Findings {
		fmt.Fprintf(out, "  %s: %s -- %s\n", f.ProposalID, f.Kind, f.Detail)
	}
	if result.ChainIntact {
		fmt.Fprintln(out, "chain: intact")
	} else {
		fmt.Fprintf(out, "chain: BROKEN (%d finding(s), see above)\n", len(result.Findings))
	}

	fmt.Fprintf(out, "acceptance: %d re-derived, %d convenience-tier (local), %d inconclusive\n", derived, convenience, inconclusive)
	for _, c := range checks {
		if c.OK || c.Method == "local" {
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
		if !c.OK && !c.Inconclusive && c.Method != "local" {
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
	Derived      int                   `json:"derived"`
	Convenience  int                   `json:"convenience_tier"`
	Inconclusive int                   `json:"inconclusive"`
	Checks       []acceptanceCheckJSON `json:"checks,omitempty"`
}

type acceptanceCheckJSON struct {
	ProposalID       string `json:"proposal_id"`
	Method           string `json:"method"`
	OK               bool   `json:"ok"`
	Inconclusive     bool   `json:"inconclusive,omitempty"`
	GitOK            bool   `json:"git_ok,omitempty"`
	ReviewersChecked bool   `json:"reviewers_checked,omitempty"`
	ReviewersMatch   bool   `json:"reviewers_match,omitempty"`
	Note             string `json:"note,omitempty"`
}
