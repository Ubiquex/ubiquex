package core

import (
	"errors"
	"fmt"
	"os/user"
	"time"
)

// ErrAlreadyAccepted means Accept was called on a proposal that already
// has an ID or acceptance data — it looks like it went through this path
// once already.
var ErrAlreadyAccepted = errors.New("proposal already accepted")

// Accept computes p's canonical hash, fills in ID/Status/Acceptance — the
// "local" acceptance tier (docs/architecture.md: "Acceptance = PR merge
// binding ... or local `ubx accept`; optional hardened cryptographic
// signing tier later" — this is that local tier: it records who/how/when,
// not a cryptographic signature) — and appends the result to l.
//
// p must be draft-shaped: ID and Acceptance unset. p.Parent must already be
// set to the ledger's current head (callers are expected to have read
// Head() themselves before resolving/authoring p — Accept doesn't fill
// Parent in for them, since silently doing so would hide a stale-parent
// bug rather than surfacing it as ErrParentMismatch).
func Accept(l *Ledger, p *Proposal) (*Proposal, error) {
	hash, err := validateAndHash(p)
	if err != nil {
		return nil, fmt.Errorf("accept: %w", err)
	}
	accepted, err := finalizeAndAppend(l, p, hash, &Acceptance{
		Method:     "local",
		Approvers:  []string{localApprover()},
		AcceptedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return nil, fmt.Errorf("accept: %w", err)
	}
	return accepted, nil
}

// MethodLoopback is Acceptance.Method for approval obtained on the local
// MCP server's loopback page (UBI-294).
const MethodLoopback = "loopback"

// LoopbackApprover is the only value a loopback acceptance's Approvers
// ever holds. It names what the channel attests, presence at this machine,
// rather than an identity nobody verified.
const LoopbackApprover = "loopback:presence"

// AcceptFromLoopback is the loopback acceptance tier (UBI-294): a human clicked approve on a page the local server served on
// loopback, which showed the bytes Ledger.StageRender stored under
// renderedHash.
//
// Before appending it requires two things, and refuses rather than
// records otherwise:
//
//   - The bytes are stored and hash to renderedHash (ErrRenderNotStored,
//     ErrRenderedHashMismatch). An acceptance never names a render nobody
//     kept.
//   - p renders, now, to exactly those bytes (ErrRenderMismatch). Storage
//     alone proves some render was kept; this proves it is a render of the
//     proposal being accepted, so an approval given while looking at one
//     proposal can never be attached to another.
//
// Nothing about who approved is recorded beyond LoopbackApprover. This
// function is engine API and is never exposed as an MCP tool, the same
// boundary by omission `ubx mcp` already keeps for accept and ship: an
// agent cannot approve its own proposal through a tool that does not exist.
func AcceptFromLoopback(l *Ledger, p *Proposal, renderedHash string) (*Proposal, error) {
	hash, err := validateAndHash(p)
	if err != nil {
		return nil, fmt.Errorf("accept from loopback: %w", err)
	}
	if _, err := l.ReadRendered(renderedHash); err != nil {
		if errors.Is(err, ErrStoreObjectNotFound) {
			return nil, fmt.Errorf("accept from loopback: %w: %s", ErrRenderNotStored, renderedHash)
		}
		return nil, fmt.Errorf("accept from loopback: %w", err)
	}
	rendered, err := RenderProposal(p)
	if err != nil {
		return nil, fmt.Errorf("accept from loopback: %w", err)
	}
	if got := HashRendered(rendered); got != renderedHash {
		return nil, fmt.Errorf("accept from loopback: %w: approved %s, proposal renders to %s",
			ErrRenderMismatch, renderedHash, got)
	}
	accepted, err := finalizeAndAppend(l, p, hash, &Acceptance{
		Method:       MethodLoopback,
		Approvers:    []string{LoopbackApprover},
		AcceptedAt:   time.Now().UTC().Format(time.RFC3339),
		RenderedHash: renderedHash,
	})
	if err != nil {
		return nil, fmt.Errorf("accept from loopback: %w", err)
	}
	return accepted, nil
}

// validateAndHash is the shared preflight every acceptance tier runs:
// refuse a proposal that's already been through this once (has an ID or
// Acceptance already), validate its structural rules, and compute its
// canonical hash.
func validateAndHash(p *Proposal) (string, error) {
	if p.ID != "" || p.Acceptance != nil {
		return "", ErrAlreadyAccepted
	}
	if err := Validate(p); err != nil {
		return "", err
	}
	return Hash(p)
}

// finalizeAndAppend is the final step every acceptance tier shares:
// stamp p with its hash/status/acceptance and append it to the ledger.
func finalizeAndAppend(l *Ledger, p *Proposal, hash string, acceptance *Acceptance) (*Proposal, error) {
	p.ID = hash
	p.Status = StatusAccepted
	p.Acceptance = acceptance
	if err := l.Append(p); err != nil {
		return nil, err
	}
	return p, nil
}

func localApprover() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "unknown"
}

// RemovedAcceptanceMethod says what an acceptance method that no longer
// exists was, or "" for one that does.
//
// pr_merge and pr_review were removed in UBI-300, with the pull-request
// workflow that produced them. Records already written with them are
// history and stay readable: their fields are still on Acceptance, the
// chain they sit in still verifies, and ubx why and ubx verify still show
// them. Nothing can re-derive them any more, so they are reported as from
// a removed method rather than as verified or failed.
func RemovedAcceptanceMethod(method string) string {
	switch method {
	case "pr_merge":
		return "pr_merge, approval derived from a merged pull request: a removed acceptance method (UBI-300), recorded as history and no longer re-verifiable"
	case "pr_review":
		return "pr_review, approval derived from a pull request review: a removed acceptance method (UBI-300), recorded as history and no longer re-verifiable"
	}
	return ""
}
