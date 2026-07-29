package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/provider"
)

// scanJSONOptions is computeScanJSON's input -- mirrors `ubx scan`'s own
// single-resource flags (UBI-25, docs/architecture.md -- MCP server).
// Deliberately excludes --all (bulk onboarding), --surface-as (opens a
// real GitHub issue/PR -- a side effect, not read-only detection), and
// --tf-dir (writeback preview, part of the same non-exposed surface) --
// "boundary by omission."
type scanJSONOptions struct {
	Config          *Config
	Stack           string
	ResourceType    string
	ResourceName    string
	Lookup          string
	ProviderPath    string
	Source          string
	ProviderVersion string
	ProviderConfig  string
	LedgerDir       string
	Out             string // optional: write the generated proposal here too, exactly like --out
	Timeout         time.Duration
	NoAttribution   bool
	Propose         string // "adopt" | "revert" | "both"; defaults to "adopt"
	K8sAudit        K8sAuditConfig
}

// computeScanJSON is the `ubx_scan` MCP tool's own "read live state,
// compare, propose" logic. It produces the exact same *scanJSON shape
// `ubx scan --json` does, but is no longer literally shared code with
// it -- `cli/scan.go`'s own RunE grew its own [ledger]-aware open
// (openLedgerForStack, UBI-32) independently, and this function is the
// one place left that still needed the identical fix; a stale version of
// this comment claimed the two were still one shared implementation,
// which stopped being true the moment that happened and was never
// corrected until now. Read-only: it never calls core.Accept. err is
// non-nil only for a genuine failure (bad --propose, provider
// acquisition/launch failed, the read itself failed); a classification
// of "new"/"drifted"/"unchanged" is always a successful result, never an
// error, matching how a CLI --json caller already sees scan outcomes (the
// exit-code-1-for-a-finding convention has no equivalent concept here).
func computeScanJSON(ctx context.Context, opts scanJSONOptions) (*scanJSON, error) {
	propose := opts.Propose
	if propose == "" {
		propose = "adopt"
	}
	switch propose {
	case "adopt", "revert", "both":
	default:
		return nil, fmt.Errorf("scan: propose must be \"adopt\", \"revert\", or \"both\", got %q", propose)
	}

	addr := core.Address{Stack: opts.Stack, Type: opts.ResourceType, Name: opts.ResourceName}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	path, checksum, err := resolveProviderBinary(ctx, opts.ProviderPath, opts.Source, opts.ProviderVersion)
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", addr, err)
	}
	client, err := provider.Launch(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", addr, err)
	}
	defer client.Close()

	ledger, closeLedger, err := openLedgerForStack(ctx, opts.LedgerDir, opts.Stack, opts.Config)
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", addr, err)
	}
	defer closeLedger()

	salt, err := ledger.Salt()
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", addr, err)
	}
	res, err := core.RunScan(ctx, newStateReader(client.Provider, salt, opts.Source), ledger, core.ScanRequest{
		Address:          addr,
		ProviderConfig:   json.RawMessage(opts.ProviderConfig),
		CurrentState:     json.RawMessage(opts.Lookup),
		ProviderChecksum: checksum,
		ProviderSource:   opts.Source,
	})
	if err != nil {
		return nil, err
	}

	if res.Outcome == core.ScanUnchanged {
		return &scanJSON{
			Format:       jsonFormatVersion,
			Address:      addressToJSON(addr),
			Outcome:      "unchanged",
			ObservedHash: res.ObservedHash,
		}, nil
	}

	var proposals []*core.Proposal
	switch {
	case res.Outcome == core.ScanNew:
		p, err := core.GenerateProposal(ledger, opts.Stack, res)
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", addr, err)
		}
		proposals = []*core.Proposal{p}
	case propose == "adopt":
		p, err := core.GenerateProposal(ledger, opts.Stack, res)
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", addr, err)
		}
		proposals = []*core.Proposal{p}
	case propose == "revert":
		p, err := core.GenerateRevertProposal(ledger, opts.Stack, res)
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", addr, err)
		}
		proposals = []*core.Proposal{p}
	default: // both
		adopt, err := core.GenerateProposal(ledger, opts.Stack, res)
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", addr, err)
		}
		revert, err := core.GenerateRevertProposal(ledger, opts.Stack, res)
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", addr, err)
		}
		proposals = []*core.Proposal{adopt, revert}
	}

	if opts.Out != "" && len(proposals) > 1 {
		return nil, fmt.Errorf("scan %s: out only supports a single generated proposal -- "+
			"propose \"both\" on a drifted resource generates two; omit out to receive both inline", addr)
	}

	// CloudTrail/audit-k8s attribution is drift_adopt-specific (docs/schema.md),
	// same as the CLI's own scan path -- apply it to whichever generated
	// proposal is the drift_adopt, if any.
	var adoptProposal *core.Proposal
	for _, p := range proposals {
		if p.Kind == core.KindDriftAdopt {
			adoptProposal = p
		}
	}
	if adoptProposal != nil && !opts.NoAttribution {
		attributeDrift(ctx, ledger, addr, res, adoptProposal, json.RawMessage(opts.ProviderConfig), opts.Source, opts.K8sAudit)
	}

	if opts.Out != "" {
		b, err := json.MarshalIndent(proposals[0], "", "  ")
		if err != nil {
			return nil, fmt.Errorf("scan %s: marshal proposal: %w", addr, err)
		}
		if err := os.WriteFile(opts.Out, b, 0o644); err != nil {
			return nil, fmt.Errorf("scan %s: %w", addr, err)
		}
	}

	kindLabel := "new"
	if res.Outcome == core.ScanDrifted {
		kindLabel = "drifted"
	}
	return &scanJSON{
		Format:       jsonFormatVersion,
		Address:      addressToJSON(addr),
		Outcome:      kindLabel,
		ObservedHash: res.ObservedHash,
		Proposals:    proposals,
	}, nil
}
