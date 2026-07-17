package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/core/resolver"
	ghub "github.com/ubiquex/ubiquex-cli/github"
	"github.com/ubiquex/ubiquex-cli/provider"
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
				return acceptFromMerge(cmd, ledgerDir, fromMerge, repoDir, proposalFile, githubRepo)
			}
			if len(args) != 1 {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept requires a proposal.json argument, or --from-merge for PR-merge derivation")}
			}

			data, err := os.ReadFile(args[0])
			if err != nil {
				return &ExitCodeError{Code: 2, Err: err}
			}

			var p core.Proposal
			if err := json.Unmarshal(data, &p); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("parse proposal: %w", err)}
			}

			ledger := core.Open(ledgerDir)

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
				if err := core.VerifyFreshness(ctx, newStateReader(client.Provider, salt, reverifySource), addr, reverifySource,
					json.RawMessage(providerConfig), &p); err != nil {
					return &ExitCodeError{Code: acceptErrorCode(err), Err: fmt.Errorf("accept: %w", err)}
				}
			}

			// Cross-stack pins (UBI-27, docs/schema.md's "cross_stack_pin"
			// resolution inputs) are re-verified unconditionally, not gated
			// behind an opt-in flag the way live-state reverify is: unlike a
			// real provider round trip, checking whether a neighbor ledger's
			// head has moved is a free, local filesystem read, so there's no
			// cost/latency reason to make an operator ask for it explicitly.
			if err := resolver.VerifyPins(&p); err != nil {
				return &ExitCodeError{Code: acceptErrorCode(err), Err: fmt.Errorf("accept: %w", err)}
			}

			accepted, err := core.Accept(ledger, &p)
			if err != nil {
				return &ExitCodeError{Code: acceptErrorCode(err), Err: err}
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
	cmd.Flags().StringVar(&fromMerge, "from-merge", "", "derive pr_merge acceptance from a merge commit SHA instead of accepting a local file (UBI-11 stage 1)")
	cmd.Flags().StringVar(&repoDir, "repo-dir", ".", "local git working tree to verify --from-merge's commit history against")
	cmd.Flags().StringVar(&proposalFile, "proposal-file", "", "path, within the repo, to the proposal file the merge commit carries (required with --from-merge)")
	cmd.Flags().StringVar(&githubRepo, "github-repo", "", "owner/name of the GitHub repository (required with --from-merge)")
	return cmd
}

// acceptFromMerge is UBI-11 stage 1's PR-merge acceptance tier: derive
// acceptance from git history + the GitHub API rather than trusting a
// file or the caller's own say-so (see docs/architecture.md's Decision
// loop section, and core.AcceptFromMerge's doc comment for what's
// actually enforced).
func acceptFromMerge(cmd *cobra.Command, ledgerDir, mergeSHA, repoDir, proposalFile, githubRepo string) error {
	if proposalFile == "" || githubRepo == "" {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept --from-merge requires --proposal-file and --github-repo")}
	}
	owner, repo, ok := strings.Cut(githubRepo, "/")
	if !ok || owner == "" || repo == "" {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept: --github-repo must be \"owner/name\", got %q", githubRepo)}
	}

	ctx := cmd.Context()
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
		return &ExitCodeError{Code: deriveAcceptanceErrorCode(err), Err: fmt.Errorf("accept: %w", err)}
	}

	var p core.Proposal
	if err := json.Unmarshal(derived.ProposalFileContent, &p); err != nil {
		return &ExitCodeError{Code: 2, Err: fmt.Errorf("accept: parse proposal at %s:%s: %w", mergeSHA, proposalFile, err)}
	}

	// Same unconditional re-verification as the local-file path (see its
	// own comment): a merge-derived proposal can carry cross_stack_pin
	// resolution inputs just as easily as one accepted directly from a file.
	if err := resolver.VerifyPins(&p); err != nil {
		return &ExitCodeError{Code: acceptErrorCode(err), Err: fmt.Errorf("accept: %w", err)}
	}

	ledger := core.Open(ledgerDir)
	accepted, err := core.AcceptFromMerge(ledger, &p, derived.ClaimedHash, core.MergeAcceptance{
		MergeSHA:     derived.MergeSHA,
		PRNumber:     derived.PRNumber,
		ProposalFile: derived.ProposalFile,
		Approvers:    derived.Approvers,
	})
	if err != nil {
		return &ExitCodeError{Code: acceptErrorCode(err), Err: fmt.Errorf("accept: %w", err)}
	}

	fmt.Fprintf(cmd.OutOrStdout(), "accepted %s (stack %s) via PR #%d, %d approver(s)\n",
		accepted.ID, accepted.Stack, accepted.Acceptance.PRNumber, len(accepted.Acceptance.Approvers))
	return nil
}

// acceptErrorCode classifies core.Accept/core.AcceptFromMerge's failures
// for the UBI-20 exit-code contract: ErrStaleObservation (VerifyFreshness),
// ErrParentMismatch/ErrTrailerHashMismatch (Accept/AcceptFromMerge/Append),
// and resolver.ErrCrossStackPinStale (VerifyPins) all mean "the world
// moved, or the claimed acceptance doesn't check out since this proposal
// was resolved" -- an actionable finding (exit 1), matching the "stale
// block"/"proposal rejected" examples in docs/architecture.md's Hardening
// pass section. Anything else (a malformed proposal, a double-accept
// attempt, ledger I/O) is a genuine error (exit 2).
func acceptErrorCode(err error) int {
	if errors.Is(err, core.ErrStaleObservation) || errors.Is(err, core.ErrParentMismatch) || errors.Is(err, core.ErrTrailerHashMismatch) || errors.Is(err, resolver.ErrCrossStackPinStale) {
		return 1
	}
	return 2
}

// deriveAcceptanceErrorCode classifies ghub.DeriveAcceptance's failures:
// every named sentinel it can return means the merge commit's claimed
// acceptance doesn't check out against git/GitHub history as it stands
// today (commit gone, file gone at that commit, no PR for it, no
// ubx-proposal trailer in the PR body) -- an actionable finding (exit 1),
// the same family as acceptErrorCode's ErrTrailerHashMismatch. A genuine
// network/API/git-tooling failure is exit 2.
func deriveAcceptanceErrorCode(err error) int {
	switch {
	case errors.Is(err, ghub.ErrCommitNotFound),
		errors.Is(err, ghub.ErrFileNotFoundAtCommit),
		errors.Is(err, ghub.ErrNoPullRequestForCommit),
		errors.Is(err, ghub.ErrNoProposalTrailer):
		return 1
	default:
		return 2
	}
}
