package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// ChainFinding is one thing VerifyChain found wrong -- never a Go error on
// its own (a broken chain is a real, reportable outcome, not a tool
// failure), always attributed to the proposal it's about so a caller can
// point a human at exactly what to look at.
type ChainFinding struct {
	ProposalID string
	Kind       string
	Detail     string
}

// Chain finding kinds -- exported so a caller (ubx verify's own CLI layer)
// can branch on them without string-matching Detail's own free text.
const (
	FindingHashMismatch      = "hash_mismatch"
	FindingTaintedDescendant = "tainted_descendant"
	FindingMissingParent     = "missing_parent"
	FindingCorruptEntry      = "corrupt_entry"
	FindingCycle             = "cycle"
	FindingRedactedMalformed = "redacted_malformed"
	FindingCorruptApply      = "corrupt_apply"
	FindingApplyHashMismatch = "apply_hash_mismatch"
	FindingApplyChainBreak   = "apply_chain_break"
)

// VerifyResult is VerifyChain's own report.
type VerifyResult struct {
	ProposalsChecked int
	ChainIntact      bool
	Findings         []ChainFinding

	// Proposals holds every proposal VerifyChain actually managed to read,
	// oldest-first (the same order Ledger.Chain returns) -- a proposal
	// that failed to read at all (missing_parent/corrupt_entry) has no
	// object to include and is represented only by its own Finding. A
	// caller that needs to re-derive acceptance per proposal (ubx verify's
	// own CLI layer) walks this list rather than re-reading the ledger a
	// second time.
	Proposals []*Proposal
}

// VerifyChain independently re-verifies l's own entire chain, offline: every
// proposal's content hash against its own id, the parent-link walk itself
// (never trusting Ledger.Chain's own all-or-nothing error return, so one
// broken link is a reported Finding, not a command crash), every sealed
// apply record's own hash and its prior-sealed-attempt chaining, and every
// $redacted marker's own inner shape. Never touches a provider, never
// needs credentials -- a pure read over data the ledger already has.
//
// Returns a non-nil error only when verification couldn't even start (the
// head pointer itself is unreadable/corrupt, UBI-20's exit-2 territory) --
// an empty ledger (no head at all) is not an error, it's a trivially
// intact zero-proposal result.
//
// A tampered proposal (its own stored bytes no longer hash to its own id)
// doesn't just fail its own check: every later proposal in the chain --
// built on top of it, whether or not any of THEIR own bytes were also
// touched -- is flagged FindingTaintedDescendant too. This is deliberate,
// not over-reporting: a later proposal's own hash/parent fields can check
// out perfectly while still resting on corrupted history, and a human
// reading the report needs to know that, not just which one file changed.
func VerifyChain(l *Ledger) (*VerifyResult, error) {
	head, err := l.Head()
	if err != nil {
		return nil, fmt.Errorf("verify: %w", err)
	}

	result := &VerifyResult{ChainIntact: true}
	if head == "" {
		return result, nil
	}

	type step struct {
		id string
		p  *Proposal
	}
	var steps []step
	seen := make(map[string]bool)
	id := head
	for id != "" {
		if seen[id] {
			result.Findings = append(result.Findings, ChainFinding{
				ProposalID: id,
				Kind:       FindingCycle,
				Detail:     "parent chain cycles back to an already-visited proposal -- walk stopped here",
			})
			result.ChainIntact = false
			break
		}
		seen[id] = true

		p, rerr := l.Read(id)
		if rerr != nil {
			kind := FindingCorruptEntry
			if errors.Is(rerr, ErrProposalNotFound) {
				kind = FindingMissingParent
			}
			result.Findings = append(result.Findings, ChainFinding{
				ProposalID: id,
				Kind:       kind,
				Detail:     rerr.Error(),
			})
			result.ChainIntact = false
			break
		}
		steps = append(steps, step{id: id, p: p})
		id = p.Parent
	}

	for i, j := 0, len(steps)-1; i < j; i, j = i+1, j-1 {
		steps[i], steps[j] = steps[j], steps[i]
	}

	tainted := false
	for _, s := range steps {
		result.ProposalsChecked++
		result.Proposals = append(result.Proposals, s.p)

		recomputed, herr := Hash(s.p)
		hashOK := herr == nil && recomputed == s.id
		switch {
		case !hashOK:
			detail := fmt.Sprintf("recomputed content hash %s does not match its own id", recomputed)
			if herr != nil {
				detail = fmt.Sprintf("recomputing content hash failed: %v", herr)
			}
			result.Findings = append(result.Findings, ChainFinding{ProposalID: s.id, Kind: FindingHashMismatch, Detail: detail})
			result.ChainIntact = false
			tainted = true
		case tainted:
			result.Findings = append(result.Findings, ChainFinding{
				ProposalID: s.id,
				Kind:       FindingTaintedDescendant,
				Detail:     "this proposal's own hash checks out, but it extends a chain that already contains a tampered proposal",
			})
			result.ChainIntact = false
		}

		for _, path := range malformedRedactedPaths(s.p) {
			result.Findings = append(result.Findings, ChainFinding{
				ProposalID: s.id,
				Kind:       FindingRedactedMalformed,
				Detail:     fmt.Sprintf("%s: not a well-formed {%q: {%q: <64-hex sha256>}} marker", path, RedactedMarkerKey, "sha256"),
			})
			result.ChainIntact = false
		}

		applyFindings, aerr := verifyApplyChain(l, s.id)
		if aerr != nil {
			result.Findings = append(result.Findings, ChainFinding{ProposalID: s.id, Kind: FindingCorruptApply, Detail: aerr.Error()})
			result.ChainIntact = false
		}
		if len(applyFindings) > 0 {
			result.Findings = append(result.Findings, applyFindings...)
			result.ChainIntact = false
		}
	}

	return result, nil
}

// verifyApplyChain re-verifies proposalID's own sealed apply-attempt chain:
// each sealed record's own ApplyHash against its own id, and its own
// Parent against the id of the most recently SEALED attempt before it --
// BeginApply's own exact linkage rule (core/apply.go), not a naive N/N-1
// walk: a crashed, never-sealed attempt bumps the attempt counter but is
// never itself a valid parent, so a legitimate crash-and-retry sequence
// must not be flagged as a broken chain.
func verifyApplyChain(l *Ledger, proposalID string) ([]ChainFinding, error) {
	attempts, err := l.ApplyAttempts(proposalID)
	if err != nil {
		if errors.Is(err, ErrCorruptApplyRecord) {
			return nil, err
		}
		return nil, nil
	}

	var findings []ChainFinding
	var lastSealedID string
	for _, a := range attempts {
		if !a.Sealed() {
			continue
		}
		recomputed, herr := ApplyHash(a)
		if herr != nil || recomputed != a.ID {
			detail := fmt.Sprintf("attempt %d: recomputed apply-record hash %s does not match its own id %s", a.Attempt, recomputed, a.ID)
			if herr != nil {
				detail = fmt.Sprintf("attempt %d: recomputing apply-record hash failed: %v", a.Attempt, herr)
			}
			findings = append(findings, ChainFinding{ProposalID: proposalID, Kind: FindingApplyHashMismatch, Detail: detail})
		}
		if a.Parent != lastSealedID {
			findings = append(findings, ChainFinding{
				ProposalID: proposalID,
				Kind:       FindingApplyChainBreak,
				Detail:     fmt.Sprintf("attempt %d: parent %q, want %q (the most recently sealed attempt before it)", a.Attempt, a.Parent, lastSealedID),
			})
		}
		lastSealedID = a.ID
	}
	return findings, nil
}

// malformedRedactedPaths walks p's own full JSON tree (map keys visited in
// sorted order -- CLAUDE.md's own "no map-iteration ordering" determinism
// rule, load-bearing here since Findings order feeds directly into --json
// output) looking for every "$redacted" marker and reports the dotted
// path of any whose own inner shape isn't exactly {"sha256": "<64-hex>"}
// -- IsRedactedValue only ever checked the OUTER shape (core/redacted.go),
// nothing anywhere has checked the inner one until now.
func malformedRedactedPaths(p *Proposal) []string {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil
	}
	var tree interface{}
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil
	}

	var bad []string
	var walk func(v interface{}, path string)
	walk = func(v interface{}, path string) {
		switch t := v.(type) {
		case map[string]interface{}:
			if inner, ok := t[RedactedMarkerKey]; ok && len(t) == 1 {
				if !wellFormedRedactedInner(inner) {
					bad = append(bad, path)
				}
				return
			}
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(t[k], path+"."+k)
			}
		case []interface{}:
			for i, vv := range t {
				walk(vv, fmt.Sprintf("%s[%d]", path, i))
			}
		}
	}
	walk(tree, "$")
	return bad
}

func wellFormedRedactedInner(inner interface{}) bool {
	m, ok := inner.(map[string]interface{})
	if !ok || len(m) != 1 {
		return false
	}
	sha, ok := m["sha256"].(string)
	if !ok {
		return false
	}
	return IsHash(sha)
}
