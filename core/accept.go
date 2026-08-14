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

// ReviewAcceptance is AcceptFromReview's already-verified input —
// everything package server derived from a real "pull_request_review"
// webhook event (state == "approved") and the GitHub API before calling
// in. Grouped into one struct for the same reason MergeAcceptance is.
type ReviewAcceptance struct {
	Platform     string // "github" only as of UBI-28 Phase 1
	PRNumber     int64
	ProposalFile string
	Approver     string // the review's own author login
	Comment      string // the review's own optional body text; may be empty
}

// AcceptFromReview is the pr_review acceptance tier (UBI-28 Phase 1,
// docs/schema.md — "Amendment: pr_review acceptance method"): a live
// counterpart to AcceptFromMerge, triggered by a GitHub PR's native
// "Approve" review action on a still-open PR rather than derived after
// the fact from an already-merged commit's history.
//
// claimedHash is the "ubx-proposal: <hash>" trailer's value, read from
// the PR body at review.CommitID specifically — the exact commit the
// Approve action applied to, never the PR's current HEAD, which can
// have moved on with new commits since the review was submitted without
// invalidating it (GitHub only dismisses stale reviews automatically
// when a repo's branch protection is configured to require it, never by
// default) — reading at CommitID closes that real TOCTOU-shaped gap.
// AcceptFromReview recomputes p's own Hash() and requires it to match
// exactly, same hard-failure-on-mismatch rule AcceptFromMerge enforces,
// for the identical reason: a mismatch means the trailer and the
// reviewed proposal file disagree about what was actually approved.
//
// AcceptFromReview itself does not talk to GitHub's API — see package
// server for the webhook handling, the review-vs-CommitID fetch, and the
// commenter/reviewer authorization checks; core stays dependency-free,
// same inversion as AcceptFromMerge's own package-github split.
func AcceptFromReview(l *Ledger, p *Proposal, claimedHash string, review ReviewAcceptance) (*Proposal, error) {
	hash, err := validateAndHash(p)
	if err != nil {
		return nil, fmt.Errorf("accept from review: %w", err)
	}
	if hash != claimedHash {
		return nil, fmt.Errorf("accept from review: %w: trailer claims %s, proposal file hashes to %s",
			ErrTrailerHashMismatch, claimedHash, hash)
	}
	accepted, err := finalizeAndAppend(l, p, hash, &Acceptance{
		Method:        "pr_review",
		Platform:      review.Platform,
		PRNumber:      review.PRNumber,
		ProposalFile:  review.ProposalFile,
		Approvers:     []string{review.Approver},
		ReviewComment: review.Comment,
		AcceptedAt:    time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return nil, fmt.Errorf("accept from review: %w", err)
	}
	return accepted, nil
}

// MergeAcceptance is AcceptFromMerge's already-verified input — everything
// the caller (see package github, package gitlab, or package
// azuredevops) derived from git history and the platform's own API
// before calling in. Grouped into one struct because it's inherently one
// finding, not five independent parameters.
type MergeAcceptance struct {
	Platform     string // "github" | "gitlab" | "azure-devops" | "bitbucket-server" (UBI-160) -- which API merge/PRNumber/Approvers were derived against, so a later re-derivation knows which one to call again
	MergeSHA     string
	PRNumber     int64    // the GitHub pull request number, or the GitLab merge request IID, MergeSHA belongs to
	ProposalFile string   // repo-relative path the proposal file lived at, at MergeSHA
	Approvers    []string // every currently-recorded approving reviewer; may be empty
}

// AcceptFromMerge is the pr_merge acceptance tier (UBI-11 stage 1,
// docs/architecture.md — Decision loop; GitLab support added UBI-160 Phase
// 1): p was resolved and its hash fixed *before* review, embedded in a
// PR/MR body's "ubx-proposal: <hash>" trailer; this derives acceptance
// from what actually happened, rather than trusting the trailer or the
// caller's own say-so.
//
// claimedHash is the trailer's value; AcceptFromMerge recomputes p's own
// Hash() and requires it to match exactly — a mismatch means the trailer
// and the merged proposal file disagree about what was reviewed, which is
// never accepted regardless of cause (authoring bug or something worse).
// merge is the caller's already-verified findings (git history contains
// merge.MergeSHA; merge.Approvers is every currently-recorded approving
// reviewer, computed the way each platform's own review model actually
// works — see package github's most-recent-review-wins fold for GitHub,
// package gitlab's direct approved_by read for GitLab) — AcceptFromMerge
// itself does not talk to git or any platform API (see package github/
// package gitlab for that; core stays dependency-free, same inversion as
// StateReader/EventLookup). merge.Approvers may legitimately be empty: a
// merge with zero approving reviews is recorded as it happened, never
// rejected — enforcing "this needed N approvals" is entirely the
// platform's own job (branch/merge-request protection), never ubx's.
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
		Platform:     merge.Platform,
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
