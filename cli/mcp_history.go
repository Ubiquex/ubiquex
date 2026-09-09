package cli

import (
	"context"
)

// computeHistoryJSON is the ubx_history MCP tool's own "open the ledger,
// walk the chain, build the payload" logic, matching computeWhyJSON/
// computeStatusJSON/computeScanJSON's shape exactly and producing the
// same historyJSON payload `ubx history --json` does.
//
// Why this tool exists at all. ubx_status reports the current FOLD, and
// a fold is not history: a resource created and later destroyed is
// tombstoned and skipped (core/fleet.go), so a ledger holding real
// proposals reports zero resources. ubx_why answers in full detail but
// needs an address or a proposal ID the caller already has. Between
// them there was no way for an assistant to ask what has happened here,
// and an MCP session ran into exactly that: it read total: 0 against a
// ledger holding two proposals and concluded the ledger was empty.
//
// A ninth tool rather than a mode on ubx_status, deliberately. The two
// answer different questions and return different shapes: status is
// resources keyed by address, history is proposals keyed by ID.
// ubx_status's existing drift flag is not a precedent for folding one
// into the other, because drift enriches the same answer with live
// state and keeps the same keys, where history would replace the answer
// outright. More to the point, that session failed at tool SELECTION,
// not at parameter choice: the model had already committed to
// ubx_status before any field description was in play, and only a
// separate entry in the tool list, named for what it answers, changes
// that.
//
// It also makes ubx_why's proposal-ID mode reachable. That mode has
// always existed and, through MCP, was usable only when a human pasted
// an ID in, since nothing enumerated them. Enumerate then drill in is
// how the CLI already works.
type historyJSONOptions struct {
	Config    *Config
	LedgerDir string
	Stack     string
	Limit     int
}

func computeHistoryJSON(ctx context.Context, opts historyJSONOptions) (*historyJSON, error) {
	ledger, closeLedger, err := openLedgerForStack(ctx, opts.LedgerDir, opts.Stack, opts.Config)
	if err != nil {
		return nil, err
	}
	defer closeLedger()

	chain, err := ledger.Chain()
	if err != nil {
		return nil, err
	}
	payload := historyToJSONLimited(chain, opts.Limit)
	return &payload, nil
}
