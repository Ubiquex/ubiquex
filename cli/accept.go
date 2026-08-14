package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	adevops "github.com/ubiquex/ubiquex/azuredevops"
	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
	ghub "github.com/ubiquex/ubiquex/github"
	glab "github.com/ubiquex/ubiquex/gitlab"
	"github.com/ubiquex/ubiquex/provider"
)

func newAcceptCmd() *cobra.Command {
	var (
		ledgerDir               string
		reverifyWith            string
		reverifySource          string
		reverifyProviderVersion string
		resourceType            string
		resourceName            string
		providerConfig          string
		timeout                 time.Duration
		fromMerge               string
		repoDir                 string
		proposalFile            string
		githubRepo              string
		gitlabProject           string
		azureDevOpsProject      string
		confirmDestroys         bool
	)

	cmd := &cobra.Command{
		Use:   "accept [proposal.json]",
		Short: "Accept a proposal -- local signing from a file, or PR-merge derivation with --from-merge -- and append it to the ledger",
		Args:  cobra.MaximumNArgs(1),
		// Exit code is a CI contract (UBI-20, docs/exit-codes.mdx): 0
		// accepted, 1 an actionable finding (stale reverify, a
		// parent-mismatched or trailer-hash-mismatched proposal -- the
		// world moved, or the claimed acceptance doesn't check out), 2
		// error. SilenceUsage/Errors: same reasoning as status.go.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept: %w", err)}
			}
			applyGithubRepoDefault(cmd, &githubRepo, cfg)
			applyGitlabProjectDefault(cmd, &gitlabProject, cfg)
			applyAzureDevOpsProjectDefault(cmd, &azureDevOpsProject, cfg)
			if err := applyProviderConfigDefault(cmd, &providerConfig, cfg); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept: %w", err)}
			}
			// Config's [provider] only fills a gap in an ALREADY-opted-into
			// reverify (--reverify-source given without --reverify-provider-version)
			// -- it never turns reverification on by itself. accept's reverify
			// is opt-in per invocation, unlike scan/status where a provider is
			// always needed; config supplying [provider] shouldn't silently
			// make every `ubx accept` start reverifying.
			if reverifySource != "" && !cmd.Flags().Changed("reverify-provider-version") && cfg.Provider.Version != "" {
				reverifyProviderVersion = cfg.Provider.Version
			}

			if fromMerge != "" {
				if len(args) != 0 {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept --from-merge does not take a proposal.json argument (use --proposal-file for its path within the repo)")}
				}
				return acceptFromMerge(cmd, cfg, ledgerDir, fromMerge, repoDir, proposalFile, githubRepo, gitlabProject, azureDevOpsProject, confirmDestroys)
			}
			if len(args) != 1 {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept requires a proposal.json argument, or --from-merge for PR-merge derivation")}
			}

			p, planHash, err := readProposalArg(ledgerDir, args[0])
			if err != nil {
				return &ExitCodeError{Code: 2, Err: err}
			}

			// docs/schema.md's "Amendment: destroys" -- this project's first
			// hardcoded acceptance-time friction invariant, checked as early
			// as possible (before any reverify round trip or pin
			// re-verification work): a human must explicitly acknowledge a
			// destructive proposal by name, every time, not just once
			// implicitly by running `ubx accept` at all.
			if err := checkDestroysConfirmed(p, confirmDestroys); err != nil {
				return &ExitCodeError{Code: acceptErrorCode(err), Err: fmt.Errorf("accept: %w", err)}
			}

			ledger, closeLedger, err := openLedgerForStack(cmd.Context(), ledgerDir, p.Stack, cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept: %w", err)}
			}
			defer closeLedger()

			if reverifyWith != "" || reverifySource != "" {
				if resourceType == "" || resourceName == "" {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept: reverification requires --resource-type and --resource-name")}
				}
				addr := core.Address{Stack: p.Stack, Type: resourceType, Name: resourceName}

				ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
				defer cancel()

				path, _, err := resolveProviderBinary(ctx, reverifyWith, reverifySource, reverifyProviderVersion)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept: reverify: %w", err)}
				}

				client, err := provider.Launch(ctx, path)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept: reverify: %w", err)}
				}
				defer client.Close()

				salt, err := ledger.Salt()
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept: %w", err)}
				}
				if err := core.VerifyFreshness(ctx, newStateReader(client.Provider, salt, reverifySource), ledger, addr, reverifySource,
					json.RawMessage(providerConfig), p); err != nil {
					return &ExitCodeError{Code: acceptErrorCode(err), Err: fmt.Errorf("accept: %w", err)}
				}
			}

			// Cross-stack pins (UBI-27, docs/schema.md's "cross_stack_pin"
			// resolution inputs) are re-verified unconditionally, not gated
			// behind an opt-in flag the way live-state reverify is: unlike a
			// real provider round trip, checking whether a neighbor ledger's
			// head has moved is a free, local filesystem read, so there's no
			// cost/latency reason to make an operator ask for it explicitly.
			if err := resolver.VerifyPins(p); err != nil {
				return &ExitCodeError{Code: acceptErrorCode(err), Err: fmt.Errorf("accept: %w", err)}
			}

			accepted, err := core.Accept(ledger, p)
			if err != nil {
				return &ExitCodeError{Code: acceptErrorCode(err), Err: err}
			}
			// UBI-62: a plan consumed via acceptance (whether or not it's
			// ever shipped afterward) is pruned from the plan store, same
			// as ship.go's own inline-accept path -- "latest" must never
			// re-offer it either way it was consumed. Tidiness, not
			// correctness: a failed removal only warns.
			if planHash != "" {
				if err := os.Remove(planFilePath(ledgerDir, planHash)); err != nil && !os.IsNotExist(err) {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: accept: could not prune consumed plan file: %v\n", err)
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "accepted %s (stack %s)\n", accepted.ID, accepted.Stack)
			return nil
		},
	}

	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&reverifyWith, "reverify-with", "", "path to a provider binary: re-read the resource live and refuse to accept if it no longer matches the proposal's recorded observation (stale). Mutually exclusive with --reverify-source")
	cmd.Flags().StringVar(&reverifySource, "reverify-source", "", "provider source address to acquire for reverification, e.g. hashicorp/aws (mutually exclusive with --reverify-with; requires --reverify-provider-version)")
	cmd.Flags().StringVar(&reverifyProviderVersion, "reverify-provider-version", "", "explicit provider version to acquire for reverification (required with --reverify-source)")
	cmd.Flags().StringVar(&resourceType, "resource-type", "", "resource type to re-read (required with --reverify-with/--reverify-source)")
	cmd.Flags().StringVar(&resourceName, "resource-name", "", "resource name to re-read (required with --reverify-with/--reverify-source)")
	cmd.Flags().StringVar(&providerConfig, "provider-config", "{}", "JSON object configuring the provider (used with --reverify-with/--reverify-source)")
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "timeout for the reverify provider round trip")
	cmd.Flags().StringVar(&fromMerge, "from-merge", "", "derive pr_merge acceptance from a merge commit SHA instead of accepting a local file")
	cmd.Flags().StringVar(&repoDir, "repo-dir", ".", "local git working tree to verify --from-merge's commit history against")
	cmd.Flags().StringVar(&proposalFile, "proposal-file", "", "path, within the repo, to the proposal file the merge commit carries (required with --from-merge)")
	cmd.Flags().StringVar(&githubRepo, "github-repo", "", "owner/name of the GitHub repository (exactly one of --github-repo/--gitlab-project/--azure-devops-project is required with --from-merge)")
	cmd.Flags().StringVar(&gitlabProject, "gitlab-project", "", "namespace/project (or numeric project ID) of the GitLab project (exactly one of --github-repo/--gitlab-project/--azure-devops-project is required with --from-merge)")
	cmd.Flags().StringVar(&azureDevOpsProject, "azure-devops-project", "", "organization/project/repository of the Azure DevOps repo (exactly one of --github-repo/--gitlab-project/--azure-devops-project is required with --from-merge)")
	cmd.Flags().BoolVar(&confirmDestroys, "confirm-destroys", false, "required to accept any proposal with blast_radius.destroys > 0 -- this project's first hardcoded acceptance-time friction invariant (docs/schema.md)")
	return cmd
}

// ErrDestroysNotConfirmed means a proposal with blast_radius.destroys > 0
// was accepted without --confirm-destroys -- docs/schema.md's "Amendment:
// destroys," this project's first hardcoded acceptance-time friction
// invariant, distinct in kind from every other check accept makes (which
// are all either structural validation or freshness/staleness detection
// the system verifies for itself, not something asking a human to
// affirmatively acknowledge). The proposal is already fully valid,
// orphan-checked, and fresh by the time this gate is reached -- its only
// job is making sure a human actually meant to accept something with
// teeth, not catching a mistake in the proposal itself.
var ErrDestroysNotConfirmed = errors.New("this proposal has blast_radius.destroys > 0 -- pass --confirm-destroys to accept it")

// checkDestroysConfirmed enforces ErrDestroysNotConfirmed for both accept
// paths (local file and --from-merge) -- checked as early as possible in
// each, before any reverify round trip or cross-stack pin re-verification
// work, since there is no point spending that effort on a proposal this
// gate will refuse regardless of what it finds.
func checkDestroysConfirmed(p *core.Proposal, confirmed bool) error {
	if p.BlastRadius.Destroys > 0 && !confirmed {
		return ErrDestroysNotConfirmed
	}
	return nil
}

// readProposalArg is UBI-49 finding #6's own fix for accept's local-file
// path: ref is tried as a file path first (unchanged for hand-authored
// drafts -- the only thing this command ever supported before this
// session), and only on a not-found does it fall back to the plan store
// (resolvePlanHash, the same short-hash-aware lookup ship.go's own
// acceptPlanInline fallback already uses) -- the hash `ubx plan`/`ubx
// scan --propose`/`ubx terminate` printed as their own "next: ubx ship
// <hash>" handoff is just as acceptable to `ubx accept` directly. A plan
// file found this way gets the identical hash-matches-filename integrity
// check acceptPlanInline already performs. If neither interpretation
// finds anything, the error names both, per docs/cli-output-spec.md
// principle 6 (teaching errors enumerate all modes).
// readProposalArg returns the resolved proposal plus, only when it came
// from the plan store rather than a hand-authored file, that plan's own
// full hash (planHash == "" for a file path) -- so the caller can prune
// it from .ubx/plans/ after a successful accept (UBI-62: "shipped plans
// pruned from latest" applies equally here, since accepting via a hash
// consumes the draft exactly as shipping one does).
func readProposalArg(ledgerDir, ref string) (p *core.Proposal, planHash string, err error) {
	data, fileErr := os.ReadFile(ref)
	if fileErr == nil {
		var fileP core.Proposal
		if err := json.Unmarshal(data, &fileP); err != nil {
			return nil, "", fmt.Errorf("parse proposal: %w", err)
		}
		return &fileP, "", nil
	}
	if !os.IsNotExist(fileErr) {
		return nil, "", fileErr
	}

	fullHash, planP, planErr := resolvePlanHash(ledgerDir, ref)
	if planErr != nil {
		return nil, "", fmt.Errorf("no such file %q, and no matching plan in %s: %w -- "+
			"pass a path to a hand-authored proposal file, or a hash printed by `ubx plan`/`ubx scan --propose`/`ubx terminate`",
			ref, filepath.Join(ledgerDir, ".ubx", "plans"), planErr)
	}
	computedHash, herr := core.Hash(planP)
	if herr != nil {
		return nil, "", herr
	}
	if computedHash != fullHash {
		return nil, "", fmt.Errorf("plan file at %s hashes to %s, not %s -- stale or corrupted plan file", planFilePath(ledgerDir, fullHash), computedHash, fullHash)
	}
	return planP, fullHash, nil
}

// mergeDerivation is the common shape both platform-specific derivers
// (deriveFromGithub, deriveFromGitlab) produce -- UBI-160 Phase 1's
// dispatch point, since ghub.DerivedAcceptance and glab.DerivedAcceptance
// necessarily differ in field names (PRNumber vs MRIID) even though
// core.MergeAcceptance itself has always been platform-agnostic (see
// core/accept.go). label is the platform-flavored way to print the
// PR/MR number in the final "accepted ..." line ("PR #%d" for GitHub,
// "MR !%d" for GitLab -- GitLab's own notation, not an arbitrary choice).
type mergeDerivation struct {
	platform            string
	proposalFileContent []byte
	claimedHash         string
	mergeSHA            string
	number              int64
	proposalFile        string
	approvers           []string
	label               string
}

// deriveFromGithub wraps ghub.DeriveAcceptance into the common
// mergeDerivation shape.
func deriveFromGithub(ctx context.Context, repoDir, githubRepo, mergeSHA, proposalFile string) (*mergeDerivation, error) {
	owner, repo, ok := strings.Cut(githubRepo, "/")
	if !ok || owner == "" || repo == "" {
		return nil, fmt.Errorf("--github-repo must be \"owner/name\", got %q", githubRepo)
	}

	var apiOpts []ghub.Option
	if base := os.Getenv("UBX_GITHUB_API_BASE_URL"); base != "" {
		// Test-only seam (same convention as UBX_PROVIDER_MIRROR): points
		// the client at something other than the real api.github.com, so
		// tests never make a real network call.
		apiOpts = append(apiOpts, ghub.WithBaseURL(base))
	}
	api := ghub.New(os.Getenv("GITHUB_TOKEN"), apiOpts...)

	derived, err := ghub.DeriveAcceptance(ctx, api, repoDir, owner, repo, mergeSHA, proposalFile)
	if err != nil {
		return nil, err
	}
	return &mergeDerivation{
		platform:            "github",
		proposalFileContent: derived.ProposalFileContent,
		claimedHash:         derived.ClaimedHash,
		mergeSHA:            derived.MergeSHA,
		number:              derived.PRNumber,
		proposalFile:        derived.ProposalFile,
		approvers:           derived.Approvers,
		label:               fmt.Sprintf("PR #%d", derived.PRNumber),
	}, nil
}

// deriveFromGitlab wraps glab.DeriveAcceptance into the common
// mergeDerivation shape -- the GitLab analog of deriveFromGithub.
func deriveFromGitlab(ctx context.Context, repoDir, gitlabProject, mergeSHA, proposalFile string) (*mergeDerivation, error) {
	if gitlabProject == "" {
		return nil, fmt.Errorf("--gitlab-project must not be empty")
	}

	var apiOpts []glab.Option
	if base := os.Getenv("UBX_GITLAB_API_BASE_URL"); base != "" {
		// Test-only seam, same convention as UBX_GITHUB_API_BASE_URL.
		apiOpts = append(apiOpts, glab.WithBaseURL(base))
	}
	api, err := glab.New(os.Getenv("GITLAB_TOKEN"), apiOpts...)
	if err != nil {
		return nil, err
	}

	derived, err := glab.DeriveAcceptance(ctx, api, repoDir, gitlabProject, mergeSHA, proposalFile)
	if err != nil {
		return nil, err
	}
	return &mergeDerivation{
		platform:            "gitlab",
		proposalFileContent: derived.ProposalFileContent,
		claimedHash:         derived.ClaimedHash,
		mergeSHA:            derived.MergeSHA,
		number:              derived.MRIID,
		proposalFile:        derived.ProposalFile,
		approvers:           derived.Approvers,
		label:               fmt.Sprintf("MR !%d", derived.MRIID),
	}, nil
}

// deriveFromAzureDevOps wraps adevops.DeriveAcceptance into the common
// mergeDerivation shape -- the Azure DevOps analog of deriveFromGithub/
// deriveFromGitlab. azureDevOpsProject is "organization/project/
// repository" -- unlike GitHub's owner/repo or GitLab's namespace/
// project, Azure DevOps genuinely needs three real identifiers to
// address a repository, not two, so the flag's own shape has one more
// slash-separated segment than the other two platforms', not an
// arbitrary choice.
func deriveFromAzureDevOps(ctx context.Context, repoDir, azureDevOpsProject, mergeSHA, proposalFile string) (*mergeDerivation, error) {
	parts := strings.SplitN(azureDevOpsProject, "/", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, fmt.Errorf("--azure-devops-project must be \"organization/project/repository\", got %q", azureDevOpsProject)
	}
	organization, project, repository := parts[0], parts[1], parts[2]

	var apiOpts []adevops.Option
	if base := os.Getenv("UBX_AZURE_DEVOPS_API_BASE_URL"); base != "" {
		// Test-only seam, same convention as UBX_GITHUB_API_BASE_URL /
		// UBX_GITLAB_API_BASE_URL.
		apiOpts = append(apiOpts, adevops.WithBaseURL(base))
	}
	api := adevops.New(organization, os.Getenv("AZURE_DEVOPS_TOKEN"), apiOpts...)

	derived, err := adevops.DeriveAcceptance(ctx, api, repoDir, project, repository, mergeSHA, proposalFile)
	if err != nil {
		return nil, err
	}
	return &mergeDerivation{
		platform:            "azure-devops",
		proposalFileContent: derived.ProposalFileContent,
		claimedHash:         derived.ClaimedHash,
		mergeSHA:            derived.MergeSHA,
		number:              derived.PullRequestID,
		proposalFile:        derived.ProposalFile,
		approvers:           derived.Approvers,
		// "PR 123", no # or ! -- Azure DevOps' own real notation
		// (confirmed against its own default merge-commit message,
		// "Merge PR 123: ..."), distinct from GitHub's "PR #123" and
		// GitLab's "MR !123", not assumed to share either's convention.
		label: fmt.Sprintf("PR %d", derived.PullRequestID),
	}, nil
}

// acceptFromMerge is UBI-11 stage 1's PR-merge acceptance tier (GitLab
// support added UBI-160 Phase 1, Azure DevOps support added UBI-160
// Phase 2): derive acceptance from git history + the platform's own API
// rather than trusting a file or the caller's own say-so (see
// docs/architecture.md's Decision loop section, and
// core.AcceptFromMerge's doc comment for what's actually enforced).
// Exactly one of githubRepo/gitlabProject/azureDevOpsProject is required
// -- accept --from-merge always derives against one specific platform,
// never more than one at once or none.
func acceptFromMerge(cmd *cobra.Command, cfg *Config, ledgerDir, mergeSHA, repoDir, proposalFile, githubRepo, gitlabProject, azureDevOpsProject string, confirmDestroys bool) error {
	if proposalFile == "" {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept --from-merge requires --proposal-file")}
	}
	given := 0
	for _, v := range []string{githubRepo, gitlabProject, azureDevOpsProject} {
		if v != "" {
			given++
		}
	}
	if given > 1 {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept --from-merge takes exactly one of --github-repo/--gitlab-project/--azure-devops-project, not more than one")}
	}
	if given == 0 {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept --from-merge requires exactly one of --github-repo, --gitlab-project, or --azure-devops-project")}
	}

	ctx := cmd.Context()
	var derived *mergeDerivation
	var err error
	switch {
	case githubRepo != "":
		derived, err = deriveFromGithub(ctx, repoDir, githubRepo, mergeSHA, proposalFile)
	case gitlabProject != "":
		derived, err = deriveFromGitlab(ctx, repoDir, gitlabProject, mergeSHA, proposalFile)
	default:
		derived, err = deriveFromAzureDevOps(ctx, repoDir, azureDevOpsProject, mergeSHA, proposalFile)
	}
	if err != nil {
		return &ExitCodeError{Code: deriveAcceptanceErrorCode(err), Err: fmt.Errorf("accept: %w", err)}
	}

	var p core.Proposal
	if err := json.Unmarshal(derived.proposalFileContent, &p); err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept: parse proposal at %s:%s: %w", mergeSHA, proposalFile, err)}
	}

	// Same friction as the local-file path (see its own comment) -- a
	// PR/MR-merge-derived acceptance is no less real an acceptance than a
	// local one, so it needs the same explicit human acknowledgment.
	if err := checkDestroysConfirmed(&p, confirmDestroys); err != nil {
		return &ExitCodeError{Code: acceptErrorCode(err), Err: fmt.Errorf("accept: %w", err)}
	}

	// Same unconditional re-verification as the local-file path (see its
	// own comment): a merge-derived proposal can carry cross_stack_pin
	// resolution inputs just as easily as one accepted directly from a file.
	if err := resolver.VerifyPins(&p); err != nil {
		return &ExitCodeError{Code: acceptErrorCode(err), Err: fmt.Errorf("accept: %w", err)}
	}

	// UBI-32: git stays the signing surface -- everything above this line
	// (commit history, the trailer hash, reviewer state) is unchanged,
	// still entirely about git/the platform's own API, regardless of what
	// store the stack configures. Only now, once that verification has
	// already succeeded, does the accepted record get written -- through
	// the stack's own configured LedgerStore, exactly like the
	// local-accept path a few lines above in this same file.
	// core.AcceptFromMerge/Ledger.Append were already store-agnostic (Arc B
	// session 1's own LedgerStore extraction) -- CAS-guarded
	// WriteProposalIfAbsent+AdvanceHead under l.store regardless of git or
	// remote -- so no new mirroring mechanism exists here; opening the
	// right store is the entire change. The git-committed proposal file
	// itself is never touched or treated as disposable -- git history
	// stays permanent regardless of where the authoritative accepted
	// record ends up living.
	ledger, closeLedger, err := openLedgerForStack(ctx, ledgerDir, p.Stack, cfg)
	if err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept: %w", err)}
	}
	defer closeLedger()

	accepted, err := core.AcceptFromMerge(ledger, &p, derived.claimedHash, core.MergeAcceptance{
		Platform:     derived.platform,
		MergeSHA:     derived.mergeSHA,
		PRNumber:     derived.number,
		ProposalFile: derived.proposalFile,
		Approvers:    derived.approvers,
	})
	if err != nil {
		return &ExitCodeError{Code: acceptErrorCode(err), Err: fmt.Errorf("accept: %w", err)}
	}

	fmt.Fprintf(cmd.OutOrStdout(), "accepted %s (stack %s) via %s, %d approver(s)\n",
		accepted.ID, accepted.Stack, derived.label, len(accepted.Acceptance.Approvers))
	return nil
}

// acceptErrorCode classifies core.Accept/core.AcceptFromMerge's failures
// for the UBI-20 exit-code contract: ErrStaleObservation (VerifyFreshness),
// ErrParentMismatch/ErrTrailerHashMismatch (Accept/AcceptFromMerge/Append),
// and resolver.ErrCrossStackPinStale (VerifyPins) all mean "the world
// moved, or the claimed acceptance doesn't check out since this proposal
// was resolved" -- an actionable finding (exit 1), matching the "stale
// block"/"proposal rejected" examples in docs/architecture.md's Hardening
// pass section. ErrDestroysNotConfirmed (UBI-30) joins the same tier for a
// different reason -- not staleness, but a human needing to explicitly
// acknowledge a destructive proposal before it's accepted -- still exactly
// as actionable (add --confirm-destroys and re-run) as any of the above.
// Anything else (a malformed proposal, a double-accept attempt, ledger
// I/O) is a genuine error (exit 2).
func acceptErrorCode(err error) int {
	if errors.Is(err, core.ErrStaleObservation) || errors.Is(err, core.ErrParentMismatch) || errors.Is(err, core.ErrTrailerHashMismatch) ||
		errors.Is(err, resolver.ErrCrossStackPinStale) || errors.Is(err, ErrDestroysNotConfirmed) {
		return 1
	}
	return 2
}

// deriveAcceptanceErrorCode classifies ghub.DeriveAcceptance/
// glab.DeriveAcceptance/adevops.DeriveAcceptance's failures: every named
// sentinel any of them can return means the merge commit's claimed
// acceptance doesn't check out against git/the platform's history as it
// stands today (commit gone, file gone at that commit, no PR/MR for it,
// no ubx-proposal trailer in the PR/MR body) -- an actionable finding
// (exit 1), the same family as acceptErrorCode's ErrTrailerHashMismatch.
// ghub.ErrCommitNotFound/ErrFileNotFoundAtCommit/ErrNoProposalTrailer
// cover all three platforms (packages gitlab and azuredevops both reuse
// those three sentinels directly rather than defining their own --
// they're genuinely host-agnostic git-plumbing/regex findings, not
// GitHub-specific). A genuine network/API/git-tooling failure is exit 2.
func deriveAcceptanceErrorCode(err error) int {
	switch {
	case errors.Is(err, ghub.ErrCommitNotFound),
		errors.Is(err, ghub.ErrFileNotFoundAtCommit),
		errors.Is(err, ghub.ErrNoPullRequestForCommit),
		errors.Is(err, ghub.ErrNoProposalTrailer),
		errors.Is(err, glab.ErrNoMergeRequestForCommit),
		errors.Is(err, adevops.ErrNoPullRequestForCommit):
		return 1
	default:
		return 2
	}
}
