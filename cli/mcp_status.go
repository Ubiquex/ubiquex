package cli

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/provider"
)

// statusJSONOptions is computeStatusJSON's input -- mirrors `ubx
// status`'s own flags exactly (UBI-25, docs/architecture.md -- MCP
// server), since an MCP client has no `.ubx/config`/shell environment of
// its own to fall back on the way a human's session might.
type statusJSONOptions struct {
	LedgerDir       string
	Stack           string
	Drift           bool
	ProviderPath    string
	Source          string
	ProviderVersion string
	ProviderConfig  string
	Timeout         time.Duration
}

// computeStatusJSON is the shared "walk the fleet, build the payload"
// logic behind both `ubx status --json` and the `ubx_status` MCP tool --
// returns the exact same *statusJSON shape, never a different one. err
// is non-nil only for a genuine failure (ledger unreadable, provider
// acquisition/launch failed); a per-resource read failure during --drift
// is recorded as that resource's own "unreadable" status in the payload,
// never a function-level error -- the walk always completes, matching
// `ubx status`'s own "never abort the report" behavior.
func computeStatusJSON(ctx context.Context, opts statusJSONOptions) (*statusJSON, error) {
	ledger := core.Open(opts.LedgerDir)
	fleet, err := ledger.Fleet(opts.Stack)
	if err != nil {
		return nil, err
	}

	if !opts.Drift {
		resources := make([]statusResourceJSON, 0, len(fleet))
		for _, e := range fleet {
			resources = append(resources, statusResourceJSON{
				Address:    addressToJSON(e.Address),
				Kind:       string(e.Kind),
				ProposalID: e.ProposalID,
				AcceptedAt: e.AcceptedAt,
			})
		}
		return &statusJSON{
			Format:       jsonFormatVersion,
			DriftChecked: false,
			Resources:    resources,
			Summary:      statusSummaryJSON{Total: len(fleet)},
		}, nil
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	path, _, err := resolveProviderBinary(ctx, opts.ProviderPath, opts.Source, opts.ProviderVersion)
	if err != nil {
		return nil, err
	}
	client, err := provider.Launch(ctx, path)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	salt, err := ledger.Salt()
	if err != nil {
		return nil, err
	}
	stateReader := newStateReader(client.Provider, salt, opts.Source)

	var driftedCount, unreadableCount int
	resources := make([]statusResourceJSON, 0, len(fleet))
	for _, e := range fleet {
		entry := statusResourceJSON{
			Address:    addressToJSON(e.Address),
			Kind:       string(e.Kind),
			ProposalID: e.ProposalID,
			AcceptedAt: e.AcceptedAt,
		}

		if len(e.Lookup) == 0 {
			unreadableCount++
			entry.Status = "unreadable"
			entry.Reason = "no lookup key recorded for this resource (authored before the resolution.inputs lookup amendment, or never had one)"
			resources = append(resources, entry)
			continue
		}

		res, err := core.RunScan(ctx, stateReader, ledger, core.ScanRequest{
			Address:        e.Address,
			ProviderConfig: json.RawMessage(opts.ProviderConfig),
			CurrentState:   e.Lookup,
			ProviderSource: opts.Source,
		})
		if err != nil {
			unreadableCount++
			entry.Status = "unreadable"
			entry.Reason = err.Error()
			resources = append(resources, entry)
			continue
		}

		switch res.Outcome {
		case core.ScanUnchanged:
			entry.Status = "clean"
		case core.ScanDrifted:
			driftedCount++
			entry.Status = "drifted"
		default:
			// ScanNew: see status.go's own identical comment -- a
			// malformed/hand-authored proposal, not something a
			// well-formed scan-generated ledger ever produces.
			unreadableCount++
			entry.Status = "unreadable"
			entry.Reason = "ledger has no reconstructable prior state for this address"
		}
		resources = append(resources, entry)
	}

	return &statusJSON{
		Format:       jsonFormatVersion,
		DriftChecked: true,
		Resources:    resources,
		Summary: statusSummaryJSON{
			Total:      len(fleet),
			Drifted:    driftedCount,
			Unreadable: unreadableCount,
		},
	}, nil
}
