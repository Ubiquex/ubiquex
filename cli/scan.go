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
	)

	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Compare one resource's live state against the ledger and generate an adoption/drift_adopt proposal if it differs",
		RunE: func(cmd *cobra.Command, args []string) error {
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

			proposal, err := core.GenerateProposal(ledger, stack, res)
			if err != nil {
				return fmt.Errorf("scan %s: %w", addr, err)
			}

			if res.Outcome == core.ScanDrifted && !noAttribution {
				attributeDrift(ctx, ledger, addr, res, proposal, json.RawMessage(providerConfig))
			}

			kindLabel := "new"
			if res.Outcome == core.ScanDrifted {
				kindLabel = "drifted"
			}
			fmt.Fprintf(out2, "%s: %s (%s) -- generated a %q proposal\n", kindLabel, addr, res.ObservedHash, proposal.Kind)

			b, err := json.MarshalIndent(proposal, "", "  ")
			if err != nil {
				return fmt.Errorf("scan %s: marshal proposal: %w", addr, err)
			}
			if out == "" {
				fmt.Fprintln(out2, string(b))
				return nil
			}
			return os.WriteFile(out, b, 0o644)
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

	for _, f := range []string{"stack", "type", "name"} {
		_ = cmd.MarkFlagRequired(f)
	}

	return cmd
}
