package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex-cli/core"
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
		RunE: func(cmd *cobra.Command, args []string) error {
			if fromMerge != "" {
				if len(args) != 0 {
					return fmt.Errorf("accept --from-merge does not take a proposal.json argument (use --proposal-file for its path within the repo)")
				}
				return acceptFromMerge(cmd, ledgerDir, fromMerge, repoDir, proposalFile, githubRepo)
			}
			if len(args) != 1 {
				return fmt.Errorf("accept requires a proposal.json argument, or --from-merge for PR-merge derivation")
			}

			data, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}

			var p core.Proposal
			if err := json.Unmarshal(data, &p); err != nil {
				return fmt.Errorf("parse proposal: %w", err)
			}

			if reverifyWith != "" || reverifySource != "" {
				if resourceType == "" || resourceName == "" {
					return fmt.Errorf("accept: reverification requires --resource-type and --resource-name")
				}
				addr := core.Address{Stack: p.Stack, Type: resourceType, Name: resourceName}

				ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
				defer cancel()

				path, _, err := resolveProviderBinary(ctx, reverifyWith, reverifySource, reverifyProviderVersion)
				if err != nil {
					return fmt.Errorf("accept: reverify: %w", err)
				}

				client, err := provider.Launch(ctx, path)
				if err != nil {
					return fmt.Errorf("accept: reverify: %w", err)
				}
				defer client.Close()

				if err := core.VerifyFreshness(ctx, newStateReader(client.Provider), addr,
					json.RawMessage(providerConfig), &p); err != nil {
					return fmt.Errorf("accept: %w", err)
				}
			}

			ledger := core.Open(ledgerDir)
			accepted, err := core.Accept(ledger, &p)
			if err != nil {
				return err
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
		return fmt.Errorf("accept --from-merge requires --proposal-file and --github-repo")
	}
	owner, repo, ok := strings.Cut(githubRepo, "/")
	if !ok || owner == "" || repo == "" {
		return fmt.Errorf("accept: --github-repo must be \"owner/name\", got %q", githubRepo)
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
		return fmt.Errorf("accept: %w", err)
	}

	var p core.Proposal
	if err := json.Unmarshal(derived.ProposalFileContent, &p); err != nil {
		return fmt.Errorf("accept: parse proposal at %s:%s: %w", mergeSHA, proposalFile, err)
	}

	ledger := core.Open(ledgerDir)
	accepted, err := core.AcceptFromMerge(ledger, &p, derived.ClaimedHash, core.MergeAcceptance{
		MergeSHA:     derived.MergeSHA,
		PRNumber:     derived.PRNumber,
		ProposalFile: derived.ProposalFile,
		Approvers:    derived.Approvers,
	})
	if err != nil {
		return fmt.Errorf("accept: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "accepted %s (stack %s) via PR #%d, %d approver(s)\n",
		accepted.ID, accepted.Stack, accepted.Acceptance.PRNumber, len(accepted.Acceptance.Approvers))
	return nil
}
