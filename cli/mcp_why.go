package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/ubiquex/ubiquex-cli/core"
)

// computeWhyJSON is the `ubx_why` MCP tool's own "do the lookup, build the
// payload" logic (UBI-25, docs/architecture.md -- MCP server). It produces
// the exact same *whyJSON shape `ubx why --json` does, but is no longer
// literally shared code with it -- `cli/why.go`'s own RunE grew its own
// [ledger]-aware lookup (openLedgerForStack, UBI-32) independently, and
// this function is the one place left that still needed the identical
// fix; a stale version of this comment claimed the two were still one
// shared implementation, which stopped being true the moment that
// happened and was never corrected until now. err is non-nil only for a
// genuine failure (bad argument, unreadable ledger, no proposals found, or
// a verify-acceptance TOOL failure -- git/GitHub unreachable) -- never for
// an actionable FINDING (a verify-acceptance mismatch is fully described
// in the returned payload's VerifyAcceptance field, matching how a CLI
// --json caller already sees it: the JSON always carries the finding,
// exit code 1 is a CLI/shell convention this function has no equivalent
// concept of).
func computeWhyJSON(ctx context.Context, cfg *Config, ledgerDir, stack, arg string, verifyAcceptance bool, repoDir, githubRepo string) (*whyJSON, error) {
	if proposalIDPattern.MatchString(arg) {
		// A bare proposal ID carries no stack of its own (docs/architecture.md
		// -- Addressing) -- the caller-supplied stack is what a remote store
		// needs to know which chain to open at all, exactly like `ubx why
		// --stack`.
		ledger, closeLedger, err := openLedgerForStack(ctx, ledgerDir, stack, cfg)
		if err != nil {
			return nil, err
		}
		defer closeLedger()

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
	// The address itself already names its own stack -- used directly,
	// regardless of the caller-supplied stack, since it's unambiguous by
	// construction (matching `ubx why`'s own address branch).
	ledger, closeLedger, err := openLedgerForStack(ctx, ledgerDir, addr.Stack, cfg)
	if err != nil {
		return nil, err
	}
	defer closeLedger()

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
