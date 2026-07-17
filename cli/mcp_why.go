package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/ubiquex/ubiquex-cli/core"
)

// computeWhyJSON is the shared "do the lookup, build the payload" logic
// behind both `ubx why --json` and the `ubx_why` MCP tool (UBI-25,
// docs/architecture.md -- MCP server: "wrapping the existing --json
// contract, not a parallel API"). Returns the exact same *whyJSON shape
// `ubx why --json` already produces; err is non-nil only for a genuine
// failure (bad argument, unreadable ledger, no proposals found, or a
// verify-acceptance TOOL failure -- git/GitHub unreachable) -- never for
// an actionable FINDING (a verify-acceptance mismatch is fully described
// in the returned payload's VerifyAcceptance field, matching how a CLI
// --json caller already sees it: the JSON always carries the finding,
// exit code 1 is a CLI/shell convention this function has no equivalent
// concept of).
func computeWhyJSON(ctx context.Context, ledgerDir, arg string, verifyAcceptance bool, repoDir, githubRepo string) (*whyJSON, error) {
	ledger := core.Open(ledgerDir)

	if proposalIDPattern.MatchString(arg) {
		p, err := ledger.Read(arg)
		if err != nil {
			return nil, err
		}
		payload := &whyJSON{Format: jsonFormatVersion, Proposal: p}
		if !verifyAcceptance {
			return payload, nil
		}
		verifyResult, verifyErr := runVerifyAcceptance(ctx, io.Discard, p, repoDir, githubRepo, true)
		payload.VerifyAcceptance = verifyResult
		var exitErr *ExitCodeError
		if errors.As(verifyErr, &exitErr) && exitErr.Code == 2 {
			// A genuine tool failure (git/GitHub unreachable, malformed
			// --github-repo) -- not a finding the payload already
			// describes.
			return payload, exitErr.Err
		}
		return payload, nil
	}

	addr, ok := core.ParseAddress(arg)
	if !ok {
		return nil, fmt.Errorf("%q is not a valid proposal ID (64-char hex) or resource address (<stack>.<type>.<name>)", arg)
	}
	proposals, err := ledger.ProposalsForAddress(addr)
	if err != nil {
		return nil, err
	}
	if len(proposals) == 0 {
		return nil, fmt.Errorf("no proposals found for %s", addr)
	}
	// Newest first, matching the human/--json view's own order.
	chain := make([]*core.Proposal, len(proposals))
	for i, p := range proposals {
		chain[len(proposals)-1-i] = p
	}
	return &whyJSON{Format: jsonFormatVersion, Chain: chain}, nil
}
