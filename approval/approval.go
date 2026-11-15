// Package approval takes a pending proposal to a human, somewhere the
// agent that authored it cannot act, and records the human's approval as
// an acceptance (UBI-295).
//
// An agent can plan a change and ask for it to be applied. It cannot
// approve one: no MCP tool creates an acceptance, so the agent has nothing
// to call. Approval happens through a Channel instead, which hands the
// agent a Handle (a place to send a human) and never anything that would
// let the agent approve by itself.
//
// One implementation exists, Loopback: a page served on the loopback
// interface of the machine running `ubx mcp`. It shows the human the
// deterministic render of the proposal (core.RenderProposal), stores those
// exact bytes before showing them, and on approval writes an acceptance
// whose RenderedHash names them. A hosted channel would satisfy the same
// interface.
package approval

import (
	"context"
	"errors"

	"github.com/ubiquex/ubiquex/core"
)

// Channel takes a pending proposal to a human for approval.
//
// Request never approves anything and never returns something that could.
// It returns where a human goes to approve; the acceptance is written by
// the channel when, and only if, a human does.
type Channel interface {
	Request(stack, proposalHash string) (Handle, error)
}

// Handle is where a human approves one pending proposal.
type Handle struct {
	Stack        string
	ProposalHash string
	// URL is what a human opens. It carries no credential: holding it
	// lets you look at the proposal and click, nothing more, which is
	// exactly what the agent is meant to pass on to the human.
	URL string
	// Method is the Acceptance.Method an approval through this handle
	// records.
	Method string
}

// Pending is one proposal a channel can show a human, found by Lookup.
//
// Exactly one of Draft and Accepted is set. Draft is a resolved proposal
// that is not in the ledger yet, already checked to hash to the handle it
// was looked up under. Accepted is the ledger's own entry, when the
// proposal was approved already.
type Pending struct {
	Ledger   *core.Ledger
	Close    func() error
	Draft    *core.Proposal
	Accepted *core.Proposal
}

// ErrNotPending means no proposal with that hash is waiting in that stack.
var ErrNotPending = errors.New("no pending proposal with that hash")

// Lookup finds a proposal by stack and hash. The caller (`ubx mcp`) owns
// where pending proposals live and which stacks exist; a channel only
// asks. Lookup returns ErrNotPending, wrapped, when there is nothing to
// show, and runs every check a proposal must pass before a human is asked
// to accept it.
type Lookup func(ctx context.Context, stack, proposalHash string) (*Pending, error)

// Accepted is told about each approval after the acceptance is in the
// ledger, so the caller can tidy whatever held the pending proposal.
type Accepted func(stack, proposalHash string)
