package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/discovery"
	"github.com/ubiquex/ubiquex/provider"
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
		discover        bool
		tagFlags        []string
		discoverTypes   []string
		region          string
		limit           int
		yes             bool
		suggestStacks   bool
		stackTag        string
		fullHashes      bool
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

			if discover {
				if cmd.Flags().Changed("type") || cmd.Flags().Changed("name") || cmd.Flags().Changed("lookup") ||
					cmd.Flags().Changed("surface-as") || cmd.Flags().Changed("tf-dir") || all || tfstatePath != "" {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --discover: --type/--name/--lookup/--surface-as/--tf-dir/--all/--tfstate describe a " +
						"single resource or tfstate-sourced bulk onboarding and don't apply to cloud-side discovery (docs/discovery.md)")}
				}
				tags, err := parseTagFlags(tagFlags)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --discover: %w", err)}
				}
				if len(cfg.Providers) > 0 {
					warnIfLegacyProviderFlagsGiven(cmd)
				}
				ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
				defer cancel()
				opts := scanDiscoverOptions{
					Tags:            tags,
					Types:           discoverTypes,
					Region:          region,
					Limit:           limit,
					Yes:             yes,
					Stack:           stack,
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
					NoAttribution:   noAttribution,
				}
				if suggestStacks {
					return runScanSuggestStacks(ctx, cmd.OutOrStdout(), opts, stackTag)
				}
				if stack == "" {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --discover requires --stack (or --suggest-stacks for a read-only grouping preview first)")}
				}
				return runScanDiscover(ctx, cmd.OutOrStdout(), opts)
			}

			if stack == "" {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan requires --stack (or --all --tfstate <path> for bulk onboarding, or --discover for cloud-side discovery) -- pass --stack or set stack=\"...\" in .ubx/config.hcl")}
			}

			// UBI-49 residual round 1's "fleet-scoped --propose": neither
			// --type nor --name given means "walk every address this
			// stack's own ledger already tracks" (core.Ledger.Fleet, the
			// same walk `ubx status` already does) -- not an error. Both
			// given still means the single-resource path below, unchanged;
			// only ONE of the two given is still ambiguous (which one was
			// meant to narrow what?) and stays a hard error.
			if resourceType == "" && resourceName == "" {
				if jsonOut {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --stack %s: --json is only supported for a single-resource scan -- pass --type and --name to narrow, or use `ubx status --drift --json` for a fleet-wide JSON report", stack)}
				}
				if out != "" || outDir != "" {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --stack %s: --out/--out-dir are only supported for a single-resource scan (--type and --name) -- a fleet-wide walk always saves directly to the plan store", stack)}
				}
				if surfaceAs != "" {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --stack %s: --surface-as is only supported for a single-resource scan -- pass --type and --name to narrow", stack)}
				}
				ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
				defer cancel()
				ledger, closeLedger, err := openLedgerForStack(ctx, ledgerDir, stack, cfg)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --stack %s: %w", stack, err)}
				}
				defer closeLedger()
				if len(cfg.Providers) > 0 {
					warnIfLegacyProviderFlagsGiven(cmd)
				}
				st := newStylerFull(cmd, fullHashes)
				return runScanFleet(ctx, cmd.OutOrStdout(), st, ledger, ledgerDir, stack, propose, noAttribution, cfg, providerPath, source, providerVersion, providerConfig)
			}
			if resourceType == "" || resourceName == "" {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan --stack %s: pass both --type and --name to scan one resource, or neither to scan every resource this stack's ledger already tracks", stack)}
			}
			if surfaceAs != "" && propose == "revert" {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan: --surface-as requires --propose adopt (default) or both -- " +
					"its issue/PR receipt is built around a drift_adopt proposal, which --propose revert doesn't generate")}
			}

			addr := core.Address{Stack: stack, Type: resourceType, Name: resourceName}

			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()

			ledger, closeLedger, err := openLedgerForStack(ctx, ledgerDir, stack, cfg)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: %w", addr, err)}
			}
			defer closeLedger()

			// UBI-49 finding #4: single-resource scan used to resolve a
			// provider ONLY through the legacy singular --provider/--source
			// flags/[provider] config, even on a stack whose real authority
			// is a [providers] table (the same table --all/--discover/ship/
			// status already honor) -- unreadable there without falling
			// back to flags the stack doesn't otherwise need. When
			// cfg.Providers is declared, infer which source owns
			// resourceType (a real, free schema check -- see
			// inferProviderForType) instead of the legacy path.
			if len(cfg.Providers) > 0 {
				warnIfLegacyProviderFlagsGiven(cmd)
				inferredSource, inferredVersion, ierr := inferProviderForType(ctx, cfg.Providers, resourceType)
				if ierr != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: %w", addr, ierr)}
				}
				source = inferredSource
				providerVersion = inferredVersion
				providerPath = ""
				if !cmd.Flags().Changed("provider-config") {
					if pc, ok := cfg.ProviderConfigs[inferredSource]; ok {
						b, merr := json.Marshal(pc)
						if merr != nil {
							return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: provider_configs: marshal %q: %w", addr, inferredSource, merr)}
						}
						providerConfig = string(b)
					}
				}
			}

			path, checksum, err := resolveProviderBinary(ctx, providerPath, source, providerVersion)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: %w", addr, err)}
			}

			client, err := provider.Launch(ctx, path)
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: %w", addr, err)}
			}
			defer client.Close()

			salt, err := ledger.Salt()
			if err != nil {
				return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: %w", addr, err)}
			}

			// UBI-49 finding #5: an already-tracked address's own recorded
			// lookup (the same source status.go's fleet walk already reads,
			// core.Ledger.LastLookup) is consulted first -- --lookup only
			// wins if given explicitly. Fixes the asymmetry where
			// status --drift could find a URL-identified resource's drift
			// but per-resource scan went blind on the exact same address.
			currentState := json.RawMessage(lookup)
			if !cmd.Flags().Changed("lookup") {
				if recorded, found, lerr := ledger.LastLookup(addr); lerr != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: %w", addr, lerr)}
				} else if found {
					currentState = recorded
				}
			}

			res, err := core.RunScan(ctx, newStateReader(client.Provider, salt, source), ledger, core.ScanRequest{
				Address:          addr,
				ProviderConfig:   json.RawMessage(providerConfig),
				CurrentState:     currentState,
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

			// UBI-49 finding #6: scan --propose used to print the full
			// proposal JSON to the terminal and save nothing -- the only
			// hash visible (res.ObservedHash) wasn't even one `ubx accept`
			// could use, so acting on drift meant hand-copying JSON into a
			// file. Every generated proposal is now saved to the plan
			// store (the same .ubx/plans/ `ubx plan` already writes to)
			// and reported as a card naming its own real, ship-able hash --
			// `ubx ship`'s inline accept applies identically here as it
			// does to a plan's own hash.
			st := newStylerFull(cmd, fullHashes)
			header := st.Yellow("Drift found")
			if kindLabel == "new" {
				header = st.Green("New resource found")
			}
			fmt.Fprintf(out2, "%s  %s\n\n", header, addr)

			hashes := make([]string, 0, len(proposals))
			for _, p := range proposals {
				b, err := json.MarshalIndent(p, "", "  ")
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: marshal proposal: %w", addr, err)}
				}
				hash, err := core.Hash(p)
				if err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: %w", addr, err)}
				}
				if _, err := writePlanFile(ledgerDir, hash, b); err != nil {
					return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: %w", addr, err)}
				}
				if out != "" {
					if err := os.WriteFile(out, b, 0o644); err != nil {
						return &ExitCodeError{Code: 2, Err: fmt.Errorf("scan %s: %w", addr, err)}
					}
				}
				hashes = append(hashes, hash)
				renderScanCard(out2, st, p, hash)
			}
			fmt.Fprintf(out2, "\n  saved to plan store            next: %s\n", nextShipHint(hashes))
			// A proposal was generated -- new resource or drift -- an
			// actionable finding, not a failure (UBI-20 exit-code contract).
			return &ExitCodeError{Code: 1, Err: fmt.Errorf("scan %s: %s, %q proposal(s) generated (see above)", addr, kindLabel, propose)}
		},
	}

	cmd.Flags().StringVar(&providerPath, "provider", "", "path to the provider binary (mutually exclusive with --source)")
	cmd.Flags().StringVar(&source, "source", "", "provider source address, e.g. hashicorp/aws (mutually exclusive with --provider; requires --provider-version)")
	cmd.Flags().StringVar(&providerVersion, "provider-version", "", "explicit provider version to acquire, e.g. 6.54.0 (required with --source; no \"latest\" resolution)")
	cmd.Flags().StringVar(&stack, "stack", "", "stack name the resource(s) belong to (required unless --all, where it defaults to the state file's own basename; falls back to .ubx/config's own stack key otherwise)")
	cmd.Flags().StringVar(&resourceType, "type", "", "resource type, e.g. aws_s3_bucket -- pass both --type and --name to scan one resource, or neither to walk every resource this stack's ledger already tracks")
	cmd.Flags().StringVar(&resourceName, "name", "", "resource name within the stack -- pass both --type and --name to scan one resource, or neither to walk every resource this stack's ledger already tracks")
	cmd.Flags().StringVar(&lookup, "lookup", "{}", "JSON object identifying the resource to the provider (e.g. {\"id\":\"...\"})")
	cmd.Flags().StringVar(&providerConfig, "provider-config", "{}", "JSON object configuring the provider (e.g. {\"region\":\"us-east-1\"})")
	cmd.Flags().StringVar(&ledgerDir, "ledger-dir", ".", "root directory containing ledger/ and .ubx/")
	cmd.Flags().StringVar(&out, "out", "", "write the generated proposal here instead of stdout (single-resource mode only; use --out-dir with --all)")
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "overall timeout for the scan (or the whole --all walk)")
	cmd.Flags().BoolVar(&noAttribution, "no-attribution", false, "skip audit-log attribution: drift attribution for drift proposals, or genesis attribution (who created it) for --discover's own adoption proposals")
	cmd.Flags().StringVar(&surfaceAs, "surface-as", "", "on drift, open a GitHub \"issue\" or \"pr\" with a receipt instead of just printing the proposal (requires --github-repo)")
	cmd.Flags().StringVar(&githubRepo, "github-repo", "", "owner/name of the GitHub repository to surface drift in (required with --surface-as)")
	cmd.Flags().StringVar(&tfDir, "tf-dir", "", "directory of .tf files to compute a best-effort write-back preview diff from, for the receipt (optional)")
	cmd.Flags().StringVar(&propose, "propose", "adopt", "on drift, which resolution(s) to generate: \"adopt\" (drift_adopt), \"revert\" (drift_revert), or \"both\" (no effect on a new/never-seen resource, which always generates adoption)")
	cmd.Flags().BoolVar(&all, "all", false, "bulk onboarding: enumerate every resource in --tfstate and generate an adoption proposal for each, instead of scanning a single --type/--name resource")
	cmd.Flags().StringVar(&tfstatePath, "tfstate", "", "path to a Terraform state v4 JSON file, read once as a bulk-onboarding enumeration source (required with --all)")
	cmd.Flags().StringVar(&outDir, "out-dir", "", "write each --all-generated proposal to its own file in this directory, instead of printing all of them to stdout")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit one JSON document instead of human text (single-resource scan only, not --all or --surface-as)")
	cmd.Flags().BoolVar(&discover, "discover", false, "cloud-side discovery: enumerate resources via the AWS Resource Groups Tagging API instead of a tfstate file, generating an adoption proposal for each -- requires --tag")
	cmd.Flags().StringArrayVar(&tagFlags, "tag", nil, "key=value tag filter for --discover (repeatable; multiple --tag with the same key OR-combine, different keys AND-combine)")
	cmd.Flags().StringArrayVar(&discoverTypes, "discover-type", nil, "Terraform resource type allowlist for --discover (repeatable, e.g. aws_sqs_queue) -- narrows client-side; omit to classify every tag-matched resource")
	cmd.Flags().StringVar(&region, "region", "", "AWS region to enumerate with --discover (the tagging API is itself regional)")
	cmd.Flags().IntVar(&limit, "limit", 0, fmt.Sprintf("refuse --discover without --yes past this many adoptable resources (default %d)", discovery.DefaultLimit))
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm proceeding past --discover's own --limit")
	cmd.Flags().BoolVar(&suggestStacks, "suggest-stacks", false, "with --discover, print a read-only stack-grouping preview instead of writing proposals -- writes nothing, assigns nothing")
	cmd.Flags().StringVar(&stackTag, "stack-tag", "", "with --discover --suggest-stacks, the tag key to group discovered resources by (omit to fall back to a naming-prefix heuristic)")
	cmd.Flags().BoolVar(&fullHashes, "full-hashes", false, "render every hash in full instead of the default 12-char short form")

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

// renderScanCard prints one generated proposal's own card entry --
// kind, ship-able hash, a short description, then content (UBI-49
// residual round 1's "scan cards regain content" polish,
// docs/cli-output-spec.md's own worked example): a compact one-line diff
// summary (both drift_adopt and drift_revert carry a Delta.Modifies
// entry; an adoption's own Delta.Creates has no prior state to diff
// against, so nothing renders there), and, for drift_adopt specifically,
// its own attribution outcome -- attributed (who:) or the recorded
// reason it came back empty (attribution:, UBI-49 residual #5's
// correction: this used to be silently omitted either way).
//
// UBI-71 part 2 (founder finding): this used to render the FULL
// attribute-level before/after diff, one line per changed path -- the
// exact same diff `ubx status --drift` already just showed for this
// address, restated verbatim with nothing new beyond the proposal's own
// kind/hash/attribution. Trimmed to a compact one-line summary (the
// founder's own "simpler alternative," ridden here rather than
// conditioning on whether a status check happened to run first: scan and
// status are separate process invocations with no shared session state
// to consult, so "was status just run" isn't a thing this command could
// even ask) -- scan's real news is the proposal and its attribution, not
// a second copy of the diff.
func renderScanCard(out io.Writer, st *styler, p *core.Proposal, hash string) {
	var desc string
	switch p.Kind {
	case core.KindAdoption:
		desc = "record-only · adopts into the ledger"
	case core.KindDriftAdopt:
		desc = "record-only · records reality as signed"
	case core.KindDriftRevert:
		desc = "restores the ledger's own recorded state"
	default:
		desc = fmt.Sprintf("+%d create(s) ~%d modify(ies) -%d destroy(s)", p.BlastRadius.Creates, p.BlastRadius.Modifies, p.BlastRadius.Destroys)
	}
	fmt.Fprintf(out, "  %-14s%s     %s\n", p.Kind, st.Ref(hash), desc)
	for _, m := range p.Delta.Modifies {
		paths := sortedAttributePaths(m.Before, m.After)
		if len(paths) == 0 {
			continue
		}
		fmt.Fprintf(out, "      %s %d attribute(s) changed: %s\n", st.Yellow("~"), len(paths), strings.Join(paths, ", "))
	}
	if p.Kind == core.KindDriftAdopt {
		if line := attributionCardLine(st, p.Intent.Sources); line != "" {
			fmt.Fprintf(out, "      %s\n", line)
		}
	}
}

// nextShipHint renders scan --propose's own "next:" handoff (UBI-49
// finding #6, docs/cli-output-spec.md principle 3): one hash names the
// obvious next step directly; two (--propose both) name the primary
// resolution first and the alternative parenthetically, since a human
// still has to pick one -- there's no "obvious" default between adopting
// drift and reverting it the way there is for a single generated
// proposal.
func nextShipHint(hashes []string) string {
	switch len(hashes) {
	case 1:
		return fmt.Sprintf("ubx ship %s", shortRef(hashes[0]))
	case 2:
		return fmt.Sprintf("ubx ship %s  (or %s)", shortRef(hashes[0]), shortRef(hashes[1]))
	default:
		return "ubx ship <hash>"
	}
}
