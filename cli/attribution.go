package cli

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ubiquex/ubiquex-cli/cloudtrail"
	"github.com/ubiquex/ubiquex-cli/core"
)

// attributeDrift best-effort attaches CloudTrail attribution (UBI-10) to a
// drift_adopt proposal's Intent.Sources before it's printed/written.
// Best-effort by construction, all the way out to this call site: any
// failure along the way (no region to search in, can't build an AWS
// CloudTrail client, credentials denied) degrades to a
// "cloudtrail_unattributed"/"not_logged" source via core.AttributeDrift
// itself, or is recorded directly as one here when the failure happens
// before core.AttributeDrift even gets a chance to run. This never returns
// an error that could block `ubx scan` from printing/writing the proposal
// it already generated.
func attributeDrift(ctx context.Context, ledger *core.Ledger, addr core.Address, res *core.ScanResult, proposal *core.Proposal, providerConfig json.RawMessage) {
	since, found, err := ledger.LastObservationTime(addr)
	if err != nil || !found {
		// Drift implies a prior observation exists (RunScan only classifies
		// ScanDrifted when a PreviousHash was already found), so this
		// shouldn't happen -- but best-effort means never blocking on it.
		// Fall back to CloudTrail's own ~90-day default retention window
		// rather than skipping attribution outright.
		since = time.Now().UTC().Add(-90 * 24 * time.Hour)
	}
	until, err := time.Parse(time.RFC3339, proposal.Resolution.ResolvedAt)
	if err != nil {
		until = time.Now().UTC()
	}

	lookup, err := cloudtrail.New(ctx, regionFromProviderConfig(providerConfig))
	if err != nil {
		proposal.Intent.Sources = append(proposal.Intent.Sources, core.IntentSource{
			Kind: "cloudtrail_unattributed", Reason: core.ReasonNotLogged,
		})
		return
	}

	sources := core.AttributeDrift(ctx, lookup, addr, res.Observed, since, until)
	proposal.Intent.Sources = append(proposal.Intent.Sources, sources...)
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
