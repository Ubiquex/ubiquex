package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/provider"
)

func newScanCmd() *cobra.Command {
	var (
		providerPath    string
		source          string
		providerVersion string
		stack           string
		resourceType    string
		resourceName    string
		lookup          string
		providerConfig  string
		ledgerDir       string
		out             string
		timeout         time.Duration
		noAttribution   bool
		surfaceAs       string
		githubRepo      string
		tfDir           string
		propose         string
	)

	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Compare one resource's live state against the ledger and generate an adoption/drift_adopt/drift_revert proposal if it differs",
		RunE: func(cmd *cobra.Command, args []string) error {
			switch propose {
			case "adopt", "revert", "both":
			default:
				return fmt.Errorf("scan: --propose must be \"adopt\", \"revert\", or \"both\", got %q", propose)
			}
			if surfaceAs != "" && propose == "revert" {
				return fmt.Errorf("scan: --surface-as requires --propose adopt (default) or both -- " +
					"its issue/PR receipt is built around a drift_adopt proposal, which --propose revert doesn't generate")
			}

			addr := core.Address{Stack: stack, Type: resourceType, Name: resourceName}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			path, checksum, err := resolveProviderBinary(ctx, providerPath, source, providerVersion)
			if err != nil {
				return fmt.Errorf("scan %s: %w", addr, err)
			}

			client, err := provider.Launch(ctx, path)
			if err != nil {
				return fmt.Errorf("scan %s: %w", addr, err)
			}
			defer client.Close()

			ledger := core.Open(ledgerDir)
			res, err := core.RunScan(ctx, newStateReader(client.Provider), ledger, core.ScanRequest{
				Address:          addr,
				ProviderConfig:   json.RawMessage(providerConfig),
				CurrentState:     json.RawMessage(lookup),
				ProviderChecksum: checksum,
			})
			if err != nil {
				return err
			}

			out2 := cmd.OutOrStdout()
			if res.Outcome == core.ScanUnchanged {
				fmt.Fprintf(out2, "no drift: %s matches the ledger (observed_hash %s)\n", addr, res.ObservedHash)
				return nil
			}

			var proposals []*core.Proposal
			switch {
			case res.Outcome == core.ScanNew:
				// --propose has no effect on a never-seen-before resource --
				// there's nothing recorded yet to revert to, so adoption is
				// the only valid resolution regardless of the flag's value.
				p, err := core.GenerateProposal(ledger, stack, res)
				if err != nil {
					return fmt.Errorf("scan %s: %w", addr, err)
				}
				proposals = []*core.Proposal{p}

			case propose == "adopt":
				p, err := core.GenerateProposal(ledger, stack, res)
				if err != nil {
					return fmt.Errorf("scan %s: %w", addr, err)
				}
				proposals = []*core.Proposal{p}

			case propose == "revert":
				p, err := core.GenerateRevertProposal(ledger, stack, res)
				if err != nil {
					return fmt.Errorf("scan %s: %w", addr, err)
				}
				proposals = []*core.Proposal{p}

			default: // both
				adopt, err := core.GenerateProposal(ledger, stack, res)
				if err != nil {
					return fmt.Errorf("scan %s: %w", addr, err)
				}
				revert, err := core.GenerateRevertProposal(ledger, stack, res)
				if err != nil {
					return fmt.Errorf("scan %s: %w", addr, err)
				}
				proposals = []*core.Proposal{adopt, revert}
			}

			if out != "" && len(proposals) > 1 {
				return fmt.Errorf("scan %s: --out only supports a single generated proposal -- "+
					"--propose both on a drifted resource generates two; omit --out to print both to stdout", addr)
			}

			// CloudTrail attribution and --surface-as are both drift_adopt-
			// specific (docs/schema.md: attribution sources attach to
			// drift_adopt proposals only; --surface-as's receipt is built
			// around one too, guarded above) -- apply them to whichever
			// generated proposal is the drift_adopt, if any.
			var adoptProposal *core.Proposal
			for _, p := range proposals {
				if p.Kind == core.KindDriftAdopt {
					adoptProposal = p
				}
			}
			if adoptProposal != nil && !noAttribution {
				attributeDrift(ctx, ledger, addr, res, adoptProposal, json.RawMessage(providerConfig))
			}
			if adoptProposal != nil && surfaceAs != "" {
				if err := surfaceDrift(ctx, out2, adoptProposal, addr, surfaceAs, githubRepo, tfDir); err != nil {
					return fmt.Errorf("scan %s: %w", addr, err)
				}
			}

			kindLabel := "new"
			if res.Outcome == core.ScanDrifted {
				kindLabel = "drifted"
			}
			for _, p := range proposals {
				fmt.Fprintf(out2, "%s: %s (%s) -- generated a %q proposal\n", kindLabel, addr, res.ObservedHash, p.Kind)

				b, err := json.MarshalIndent(p, "", "  ")
				if err != nil {
					return fmt.Errorf("scan %s: marshal proposal: %w", addr, err)
				}
				if out == "" {
					fmt.Fprintln(out2, string(b))
					continue
				}
				if err := os.WriteFile(out, b, 0o644); err != nil {
					return fmt.Errorf("scan %s: %w", addr, err)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&providerPath, "provider", "", "path to the provider binary (mutually exclusive with --source)")
	cmd.Flags().StringVar(&source, "source", "", "provider source address, e.g. hashicorp/aws (mutually exclusive with --provider; requires --provider-version)")
	cmd.Flags().StringVar(&providerVersion, "provider-version", "", "explicit provider version to acquire, e.g. 6.54.0 (required with --source; no \"latest\" resolution)")
	cmd.Flags().StringVar(&stack, "stack", "", "stack name the resource belongs to (required)")
	cmd.Flags().StringVar(&resourceType, "type", "", "resource type, e.g. aws_s3_bucket (required)")
	cmd.Flags().StringVar(&resourceName, "name", "", "resource name within the stack (required)")
	cmd.Flags().StringVar(&lookup, "lookup", "{}", "JSON object identifying the resource to the provider (e.g. {\"id\":\"...\"})")
	cmd.Flags().StringVar(&providerConfig, "provider-config", "{}", "JSON object configuring the provider (e.g. {\"region\":\"us-east-1\"})")
	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&out, "out", "", "write the generated proposal here instead of stdout")
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "overall timeout for the scan")
	cmd.Flags().BoolVar(&noAttribution, "no-attribution", false, "skip CloudTrail attribution for drift proposals (UBI-10)")
	cmd.Flags().StringVar(&surfaceAs, "surface-as", "", "on drift, open a GitHub \"issue\" or \"pr\" with a receipt instead of just printing the proposal (UBI-11 stage 3; requires --github-repo)")
	cmd.Flags().StringVar(&githubRepo, "github-repo", "", "owner/name of the GitHub repository to surface drift in (required with --surface-as)")
	cmd.Flags().StringVar(&tfDir, "tf-dir", "", "directory of .tf files to compute a best-effort write-back preview diff from, for the receipt (optional)")
	cmd.Flags().StringVar(&propose, "propose", "adopt", "on drift, which resolution(s) to generate: \"adopt\" (drift_adopt), \"revert\" (drift_revert), or \"both\" (UBI-16; no effect on a new/never-seen resource, which always generates adoption)")

	for _, f := range []string{"stack", "type", "name"} {
		_ = cmd.MarkFlagRequired(f)
	}

	return cmd
}
