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
		all             bool
		tfstatePath     string
		outDir          string
		jsonOut         bool
	)

	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Compare one resource's live state against the ledger and generate an adoption/drift_adopt/drift_revert proposal if it differs",
		// Exit code is a CI contract (UBI-20, docs/exit-codes.mdx): 0 no
		// drift, 1 a proposal was generated (new resource or drift found --
		// an actionable finding, not a failure), 2 error. SilenceUsage/Errors
		// avoid cobra's own usage dump and "Error: ..." doubling ExitCodeError's
		// own message, same reasoning as status.go.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch propose {
			case "adopt", "revert", "both":
			default:
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan: --propose must be \"adopt\", \"revert\", or \"both\", got %q", propose)}
			}

			if jsonOut && all {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan: --json is only supported for a single-resource scan, not --all")}
			}
			if jsonOut && surfaceAs != "" {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan: --json is not supported with --surface-as -- surfacing already reports its own separate result")}
			}

			cfg, err := LoadConfig(cmd.ErrOrStderr())
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan: %w", err)}
			}
			applyStackDefault(cmd, &stack, cfg)
			applyProviderDefaults(cmd, &providerPath, &source, &providerVersion, cfg)
			if err := applyProviderConfigDefault(cmd, &providerConfig, cfg); err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan: %w", err)}
			}
			applyGithubRepoDefault(cmd, &githubRepo, cfg)
			applyTFDirDefault(cmd, &tfDir, cfg)

			if all {
				if cmd.Flags().Changed("type") || cmd.Flags().Changed("name") || cmd.Flags().Changed("lookup") ||
					cmd.Flags().Changed("surface-as") || cmd.Flags().Changed("tf-dir") {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --all: --type/--name/--lookup/--surface-as/--tf-dir describe a single resource " +
						"and don't apply to bulk onboarding")}
				}
				if tfstatePath == "" {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --all requires --tfstate")}
				}
				if len(cfg.Providers) > 0 {
					warnIfLegacyProviderFlagsGiven(cmd)
				}
				ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
				defer cancel()
				return runScanAll(ctx, cmd.OutOrStdout(), scanAllOptions{
					TFStatePath:     tfstatePath,
					Stack:           stack,
					Propose:         propose,
					OutDir:          outDir,
					LedgerDir:       ledgerDir,
					Config:          cfg,
					ProviderPath:    providerPath,
					Source:          source,
					ProviderVersion: providerVersion,
					ProviderConfig:  providerConfig,
					Providers:       cfg.Providers,
					ProviderConfigs: cfg.ProviderConfigs,
					Timeout:         timeout,
				})
			}
			if stack == "" || resourceType == "" || resourceName == "" {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan requires --stack, --type, and --name (or --all --tfstate <path> for bulk onboarding)")}
			}
			if surfaceAs != "" && propose == "revert" {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan: --surface-as requires --propose adopt (default) or both -- " +
					"its issue/PR receipt is built around a drift_adopt proposal, which --propose revert doesn't generate")}
			}

			addr := core.Address{Stack: stack, Type: resourceType, Name: resourceName}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			path, checksum, err := resolveProviderBinary(ctx, providerPath, source, providerVersion)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: %w", addr, err)}
			}

			client, err := provider.Launch(ctx, path)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: %w", addr, err)}
			}
			defer client.Close()

			ledger, closeLedger, err := openLedgerForStack(ctx, ledgerDir, stack, cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: %w", addr, err)}
			}
			defer closeLedger()

			salt, err := ledger.Salt()
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: %w", addr, err)}
			}
			res, err := core.RunScan(ctx, newStateReader(client.Provider, salt, source), ledger, core.ScanRequest{
				Address:          addr,
				ProviderConfig:   json.RawMessage(providerConfig),
				CurrentState:     json.RawMessage(lookup),
				ProviderChecksum: checksum,
				ProviderSource:   source,
			})
			if err != nil {
				return &ExitCodeError{Code: 2, Err: err}
			}

			out2 := cmd.OutOrStdout()
			if res.Outcome == core.ScanUnchanged {
				if jsonOut {
					payload := scanJSON{
						Format:       jsonFormatVersion,
						Address:      addressToJSON(addr),
						Outcome:      "unchanged",
						ObservedHash: res.ObservedHash,
					}
					if err := writeJSON(out2, payload); err != nil {
						return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: %w", addr, err)}
					}
					return nil
				}
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
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: %w", addr, err)}
				}
				proposals = []*core.Proposal{p}

			case propose == "adopt":
				p, err := core.GenerateProposal(ledger, stack, res)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: %w", addr, err)}
				}
				proposals = []*core.Proposal{p}

			case propose == "revert":
				p, err := core.GenerateRevertProposal(ledger, stack, res)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: %w", addr, err)}
				}
				proposals = []*core.Proposal{p}

			default: // both
				adopt, err := core.GenerateProposal(ledger, stack, res)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: %w", addr, err)}
				}
				revert, err := core.GenerateRevertProposal(ledger, stack, res)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: %w", addr, err)}
				}
				proposals = []*core.Proposal{adopt, revert}
			}

			if out != "" && len(proposals) > 1 {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: --out only supports a single generated proposal -- "+
					"--propose both on a drifted resource generates two; omit --out to print both to stdout", addr)}
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
				attributeDrift(ctx, ledger, addr, res, adoptProposal, json.RawMessage(providerConfig), source, cfg.K8sAudit)
			}
			if adoptProposal != nil && surfaceAs != "" {
				if err := surfaceDrift(ctx, out2, adoptProposal, addr, surfaceAs, githubRepo, tfDir); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: %w", addr, err)}
				}
			}

			kindLabel := "new"
			if res.Outcome == core.ScanDrifted {
				kindLabel = "drifted"
			}

			if jsonOut {
				if out != "" {
					// The --out+len(proposals)>1 guard above already
					// guarantees exactly one proposal here.
					b, err := json.MarshalIndent(proposals[0], "", "  ")
					if err != nil {
						return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: marshal proposal: %w", addr, err)}
					}
					if err := os.WriteFile(out, b, 0o644); err != nil {
						return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: %w", addr, err)}
					}
				}
				payload := scanJSON{
					Format:       jsonFormatVersion,
					Address:      addressToJSON(addr),
					Outcome:      kindLabel,
					ObservedHash: res.ObservedHash,
					Proposals:    proposals,
				}
				if err := writeJSON(out2, payload); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: %w", addr, err)}
				}
				return &ExitCodeError{Code: 1, Err: fmt.Errorf("scan %s: %s, %q proposal(s) generated (see above)", addr, kindLabel, propose)}
			}

			for _, p := range proposals {
				fmt.Fprintf(out2, "%s: %s (%s) -- generated a %q proposal\n", kindLabel, addr, res.ObservedHash, p.Kind)

				b, err := json.MarshalIndent(p, "", "  ")
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: marshal proposal: %w", addr, err)}
				}
				if out == "" {
					fmt.Fprintln(out2, string(b))
					continue
				}
				if err := os.WriteFile(out, b, 0o644); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: %w", addr, err)}
				}
			}
			// A proposal was generated -- new resource or drift -- an
			// actionable finding, not a failure (UBI-20 exit-code contract).
			return &ExitCodeError{Code: 1, Err: fmt.Errorf("scan %s: %s, %q proposal(s) generated (see above)", addr, kindLabel, propose)}
		},
	}

	cmd.Flags().StringVar(&providerPath, "provider", "", "path to the provider binary (mutually exclusive with --source)")
	cmd.Flags().StringVar(&source, "source", "", "provider source address, e.g. hashicorp/aws (mutually exclusive with --provider; requires --provider-version)")
	cmd.Flags().StringVar(&providerVersion, "provider-version", "", "explicit provider version to acquire, e.g. 6.54.0 (required with --source; no \"latest\" resolution)")
	cmd.Flags().StringVar(&stack, "stack", "", "stack name the resource belongs to (required unless --all, where it defaults to the state file's own basename)")
	cmd.Flags().StringVar(&resourceType, "type", "", "resource type, e.g. aws_s3_bucket (required unless --all)")
	cmd.Flags().StringVar(&resourceName, "name", "", "resource name within the stack (required unless --all)")
	cmd.Flags().StringVar(&lookup, "lookup", "{}", "JSON object identifying the resource to the provider (e.g. {\"id\":\"...\"})")
	cmd.Flags().StringVar(&providerConfig, "provider-config", "{}", "JSON object configuring the provider (e.g. {\"region\":\"us-east-1\"})")
	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&out, "out", "", "write the generated proposal here instead of stdout (single-resource mode only; use --out-dir with --all)")
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "overall timeout for the scan (or the whole --all walk)")
	cmd.Flags().BoolVar(&noAttribution, "no-attribution", false, "skip CloudTrail attribution for drift proposals (UBI-10)")
	cmd.Flags().StringVar(&surfaceAs, "surface-as", "", "on drift, open a GitHub \"issue\" or \"pr\" with a receipt instead of just printing the proposal (UBI-11 stage 3; requires --github-repo)")
	cmd.Flags().StringVar(&githubRepo, "github-repo", "", "owner/name of the GitHub repository to surface drift in (required with --surface-as)")
	cmd.Flags().StringVar(&tfDir, "tf-dir", "", "directory of .tf files to compute a best-effort write-back preview diff from, for the receipt (optional)")
	cmd.Flags().StringVar(&propose, "propose", "adopt", "on drift, which resolution(s) to generate: \"adopt\" (drift_adopt), \"revert\" (drift_revert), or \"both\" (UBI-16; no effect on a new/never-seen resource, which always generates adoption)")
	cmd.Flags().BoolVar(&all, "all", false, "bulk onboarding (UBI-18): enumerate every resource in --tfstate and generate an adoption proposal for each, instead of scanning a single --type/--name resource")
	cmd.Flags().StringVar(&tfstatePath, "tfstate", "", "path to a Terraform state v4 JSON file, read once as a bulk-onboarding enumeration source (required with --all)")
	cmd.Flags().StringVar(&outDir, "out-dir", "", "write each --all-generated proposal to its own file in this directory, instead of printing all of them to stdout")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit one JSON document instead of human text (UBI-20; single-resource scan only, not --all or --surface-as)")

	return cmd
}

// scanJSON is `ubx scan --json`'s single-resource payload (UBI-20
// workstream 2, docs/exit-codes.mdx / docs/architecture.md's --json
// section). Format is a schema version (see jsonFormatVersion), not the
// product version -- bumped only on an incompatible shape change.
// Proposals is empty for "unchanged"; one entry for "new" or a single
// --propose; two for --propose both.
type scanJSON struct {
	Format       int              `json:"format"`
	Address      addressJSON      `json:"address"`
	Outcome      string           `json:"outcome"` // "new" | "drifted" | "unchanged"
	ObservedHash string           `json:"observed_hash"`
	Proposals    []*core.Proposal `json:"proposals,omitempty"`
}
