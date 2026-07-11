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

// ErrTrailerHashMismatch means a pr_merge acceptance's claimed hash (from
// the PR body's "ubx-proposal: <hash>" trailer) doesn't match what the
// proposal file actually hashes to at the merge commit — see
// AcceptFromMerge and docs/schema.md's pr_merge acceptance amendment.
var ErrTrailerHashMismatch = errors.New("proposal file does not hash to the trailer's claimed value")

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

// MergeAcceptance is AcceptFromMerge's already-verified input — everything
// the caller (see package github) derived from git history and the GitHub
// API before calling in. Grouped into one struct because it's inherently
// one finding, not four independent parameters.
type MergeAcceptance struct {
	MergeSHA     string
	PRNumber     int64
	ProposalFile string   // repo-relative path the proposal file lived at, at MergeSHA
	Approvers    []string // every reviewer whose most recent review was APPROVED; may be empty
}

// AcceptFromMerge is the pr_merge acceptance tier (UBI-11 stage 1,
// docs/architecture.md — Decision loop): p was resolved and its hash fixed
// *before* review, embedded in a PR body's "ubx-proposal: <hash>" trailer;
// this derives acceptance from what actually happened, rather than trusting
// the trailer or the caller's own say-so.
//
// claimedHash is the trailer's value; AcceptFromMerge recomputes p's own
// Hash() and requires it to match exactly — a mismatch means the trailer
// and the merged proposal file disagree about what was reviewed, which is
// never accepted regardless of cause (authoring bug or something worse).
// merge is the caller's already-verified findings (git history contains
// merge.MergeSHA; merge.Approvers is every reviewer whose most recent
// review was APPROVED) — AcceptFromMerge itself does not talk to git or
// GitHub (see package github for that; core stays dependency-free, same
// inversion as StateReader/EventLookup). merge.Approvers may legitimately
// be empty: a merge with zero approving reviews is recorded as it
// happened, never rejected — enforcement is GitHub's job.
func AcceptFromMerge(l *Ledger, p *Proposal, claimedHash string, merge MergeAcceptance) (*Proposal, error) {
	hash, err := validateAndHash(p)
	if err != nil {
		return nil, fmt.Errorf("accept from merge: %w", err)
	}
	if hash != claimedHash {
		return nil, fmt.Errorf("accept from merge: %w: trailer claims %s, proposal file hashes to %s",
			ErrTrailerHashMismatch, claimedHash, hash)
	}
	accepted, err := finalizeAndAppend(l, p, hash, &Acceptance{
		Method:       "pr_merge",
		MergeSHA:     merge.MergeSHA,
		PRNumber:     merge.PRNumber,
		ProposalFile: merge.ProposalFile,
		Approvers:    merge.Approvers,
		AcceptedAt:   time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return nil, fmt.Errorf("accept from merge: %w", err)
	}
	return accepted, nil
}

// validateAndHash is the shared preflight both acceptance tiers run:
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

// finalizeAndAppend is the shared final step both acceptance tiers share:
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
