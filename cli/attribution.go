package cli

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ubiquex/ubiquex-cli/cloudtrail"
	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/gcpaudit"
)

// attributeDrift best-effort attaches audit-log attribution (UBI-10, and
// UBI-21's generalization to a per-provider-source backend registry) to a
// drift_adopt proposal's Intent.Sources before it's printed/written.
// Best-effort by construction, all the way out to this call site: any
// failure along the way (no region/project to search in, can't build a
// backend client, credentials denied) degrades to an unattributed source
// via core.AttributeDrift itself, or is recorded directly as one here when
// the failure happens before core.AttributeDrift even gets a chance to
// run. This never returns an error that could block `ubx scan` from
// printing/writing the proposal it already generated.
func attributeDrift(ctx context.Context, ledger *core.Ledger, addr core.Address, res *core.ScanResult, proposal *core.Proposal, providerConfig json.RawMessage, providerSource string) {
	since, found, err := ledger.LastObservationTime(addr)
	if err != nil || !found {
		// Drift implies a prior observation exists (RunScan only classifies
		// ScanDrifted when a PreviousHash was already found), so this
		// shouldn't happen -- but best-effort means never blocking on it.
		// Fall back to a conservative 90-day window (CloudTrail's own
		// default retention) rather than skipping attribution outright.
		since = time.Now().UTC().Add(-90 * 24 * time.Hour)
	}
	until, err := time.Parse(time.RFC3339, proposal.Resolution.ResolvedAt)
	if err != nil {
		until = time.Now().UTC()
	}

	lookup, backend, closeFn, err := newAttributionBackend(ctx, providerSource, providerConfig)
	if err != nil {
		proposal.Intent.Sources = append(proposal.Intent.Sources, core.IntentSource{
			Kind: backend.UnattributedKind, Reason: core.ReasonNotLogged, Backend: backend.Name,
		})
		return
	}
	defer closeFn()

	sources := core.AttributeDrift(ctx, lookup, addr, res.Observed, since, until, backend)
	proposal.Intent.Sources = append(proposal.Intent.Sources, sources...)
}

// newAttributionBackend picks the right core.EventLookup/
// core.AttributionBackend pair for providerSource (UBI-21, docs/
// architecture.md — GCP support: "a small registry mapping provider
// source -> attribution backend"). "hashicorp/google" gets gcpaudit/;
// everything else -- "hashicorp/aws" explicitly, and any unrecognized or
// empty source (a raw --provider path, no known registry source) --
// falls back to cloudtrail/, preserving exactly the behavior every caller
// had before this backend registry existed. closeFn is always non-nil and
// safe to call even when err != nil (a no-op in that case).
func newAttributionBackend(ctx context.Context, providerSource string, providerConfig json.RawMessage) (lookup core.EventLookup, backend core.AttributionBackend, closeFn func() error, err error) {
	noop := func() error { return nil }
	switch providerSource {
	case "hashicorp/google":
		client, err := gcpaudit.New(ctx, projectFromProviderConfig(providerConfig))
		if err != nil {
			return nil, gcpaudit.Backend, noop, err
		}
		return client, gcpaudit.Backend, client.Close, nil
	default:
		client, err := cloudtrail.New(ctx, regionFromProviderConfig(providerConfig))
		if err != nil {
			return nil, cloudtrail.Backend, noop, err
		}
		return client, cloudtrail.Backend, noop, nil
	}
}

// regionFromProviderConfig extracts "region" from a provider config JSON
// object, e.g. {"region":"us-east-1"} -- the same config already passed to
// Provider.Configure for the scan itself, so attribution doesn't need the
// operator to supply a region twice.
func regionFromProviderConfig(raw json.RawMessage) string {
	var cfg struct {
		Region string `json:"region"`
	}
	_ = json.Unmarshal(raw, &cfg)
	return cfg.Region
}

// projectFromProviderConfig is regionFromProviderConfig's GCP counterpart:
// extracts "project" from the same provider-config JSON already passed to
// Provider.Configure, e.g. {"project":"my-project"}.
func projectFromProviderConfig(raw json.RawMessage) string {
	var cfg struct {
		Project string `json:"project"`
	}
	_ = json.Unmarshal(raw, &cfg)
	return cfg.Project
}
