package core

import (
	"encoding/json"
	"fmt"
	"sort"
)

// BlameEntry is one dot-path attribute's own provenance -- ubx blame's
// own "git blame for infrastructure" unit: the value as currently folded
// (which may itself be a $redacted marker, inherited verbatim from
// whichever proposal set it -- redaction happens at the provider
// boundary, long before a value ever reaches the ledger, so there is
// nothing left for Blame itself to redact), which proposal most recently
// set THIS attribute specifically (never just "the resource's own latest
// touching proposal" -- two different attributes can legitimately have
// two different answers), and who's accountable for that proposal.
type BlameEntry struct {
	Path       string
	Value      json.RawMessage
	Redacted   bool
	ProposalID string
	Kind       ProposalKind
	SetAt      string

	// AcceptanceMethod/Approvers describe who signed the proposal that
	// set this attribute -- "local" carries no signer identity (nothing
	// in this project's own design records "who ran ubx accept," a real,
	// named limit, not an oversight); "pr_merge" carries real approvers.
	AcceptanceMethod string
	Approvers        []string

	// AttributedActors names every CloudTrail/GCP-audit actor this
	// proposal's own intent.sources attributes the change to (Intent.
	// AttributeDrift never guesses a single cause -- multiple matched
	// events are possible and are all surfaced here, newest first,
	// matching AttributeDrift's own ordering) -- populated for a
	// drift_adopt proposal (today's only real producer of cloudtrail/
	// gcp_audit sources), empty otherwise.
	AttributedActors []string

	// UnattributedReason/UnattributedBackend surface WHY attribution came
	// back empty (UBI-49 residual #5's correction, 2026-07-31): the wire
	// layer already recorded this at scan-propose time (a
	// cloudtrail_unattributed/audit_unattributed intent source, complete
	// with its own Reason) -- the gap this fixes is display-only, blame
	// simply never rendered it. Populated only when AttributedActors is
	// empty AND the setting proposal actually attempted attribution and
	// came back empty (never populated for a proposal kind that never
	// attempts attribution at all, e.g. a plain shipped change) --
	// UnattributedBackend is empty for the legacy "cloudtrail_unattributed"
	// kind (which predates needing one; it only ever meant CloudTrail) and
	// names the platform for the generalized "audit_unattributed" kind.
	UnattributedReason  string
	UnattributedBackend string
}

// DestroyProvenance names the proposal that tombstoned an address --
// BlameResult.DestroyedBy, nil unless the address is currently destroyed.
type DestroyProvenance struct {
	ProposalID string
	At         string
}

// BlameResult is Blame's own report for one address.
type BlameResult struct {
	Address Address

	// Found is false if addr has no recorded history at all (never
	// adopted, and never created via a shipped change proposal) --
	// nothing to blame, not a destroyed-and-gone case (see Destroyed).
	Found bool

	// Destroyed is true if addr's own most recent lifecycle event is a
	// shipped destroy. Entries still reports the FINAL pre-destroy
	// state's own provenance in this case -- "gone" doesn't mean
	// "unaccountable for what it used to be."
	Destroyed   bool
	DestroyedBy *DestroyProvenance

	// Entries is sorted by Path, one entry per leaf attribute in the
	// resource's own current (or, if Destroyed, final pre-destroy) folded
	// state.
	Entries []BlameEntry
}

// Blame independently re-derives per-attribute provenance for addr --
// mechanically the same fold FoldState performs (adoption/shipped-create
// seeds a base state, each Modification.After patches it in ledger
// order, a shipped destroy tombstones it), except it never discards which
// proposal contributed which leaf the way FoldState's own single merged
// value does. Reuses ProposalsForAddress for enumeration (already
// filtered to exactly the proposals that touch addr, oldest-first) rather
// than re-scanning the whole chain FoldState itself walks -- Blame is a
// per-address query, and there's no reason to pay for proposals that
// never touched addr at all.
//
// A genesis create's own "when" comes from shippedCreateFold's own
// appliedAt for a shipped change-create (the real apply timestamp, not
// when it was merely resolved/accepted) or Resolution.ResolvedAt for an
// adoption (the moment it was first observed) -- the more precise of
// what's actually available in each case, not a single uniform field
// chosen for consistency's own sake. Every later Modification uses its
// own proposal's Resolution.ResolvedAt.
func Blame(l *Ledger, addr Address) (*BlameResult, error) {
	proposals, err := l.ProposalsForAddress(addr)
	if err != nil {
		return nil, fmt.Errorf("blame: %w", err)
	}
	if len(proposals) == 0 {
		return &BlameResult{Address: addr, Found: false}, nil
	}

	// current starts nil, not an empty map -- deliberately mirroring
	// FoldState's own `var current map[string]interface{}` exactly: a
	// Modification touching addr before any genesis create has ever
	// seeded it must be skipped (current == nil), never silently applied
	// onto an empty state. An empty non-nil map here would defeat that
	// guard (Go never treats {} as nil), so this is written as an
	// explicit nil, not the zero-value shorthand.
	var current map[string]interface{}
	provenance := map[string]leafProvenance{}
	found := false
	destroyed := false
	var destroyedBy *DestroyProvenance

	for _, p := range proposals {
		for _, raw := range p.Delta.Creates {
			a, ok := createNodeAddress(raw)
			if !ok || a != addr {
				continue
			}
			var node map[string]interface{}
			if err := json.Unmarshal(raw, &node); err != nil {
				continue
			}
			if st, ok := node["state"].(map[string]interface{}); ok {
				current = st
				provenance = map[string]leafProvenance{}
				stampGenesis(current, p, p.Resolution.ResolvedAt, provenance)
				found = true
				destroyed = false
				destroyedBy = nil
				continue
			}
			if isChangeCreateNode(raw) {
				result, _, appliedAt, shipped, ferr := l.shippedCreateFold(p.ID, addr)
				if ferr != nil {
					return nil, fmt.Errorf("blame: %s: %w", addr, ferr)
				}
				if !shipped {
					continue
				}
				var seed map[string]interface{}
				if err := json.Unmarshal(result, &seed); err != nil {
					return nil, fmt.Errorf("blame: %s: bad provider_result: %w", addr, err)
				}
				current = seed
				provenance = map[string]leafProvenance{}
				setAt := appliedAt
				if setAt == "" {
					setAt = p.Resolution.ResolvedAt
				}
				stampGenesis(current, p, setAt, provenance)
				found = true
				destroyed = false
				destroyedBy = nil
			}
		}

		for i := range p.Delta.Modifies {
			mod := &p.Delta.Modifies[i]
			if mod.Target != addr || current == nil {
				continue
			}
			for path, raw := range mod.After {
				var v interface{}
				if err := json.Unmarshal(raw, &v); err != nil {
					return nil, fmt.Errorf("blame: %s: bad after[%q]: %w", addr, path, err)
				}
				dotSet(current, path, v)
				stampPath(provenance, path, v, p, p.Resolution.ResolvedAt)
			}
			found = true
		}

		for i := range p.Delta.Destroys {
			d := &p.Delta.Destroys[i]
			if d.Address != addr {
				continue
			}
			_, shipped, ferr := l.shippedDestroyFold(p.ID, addr)
			if ferr != nil {
				return nil, fmt.Errorf("blame: %s: %w", addr, ferr)
			}
			if shipped {
				destroyed = true
				destroyedBy = &DestroyProvenance{ProposalID: p.ID, At: p.Resolution.ResolvedAt}
				// current/provenance deliberately retained -- the final
				// pre-destroy snapshot, not cleared, matching this
				// function's own "blame the final pre-destroy state"
				// contract. A later create (if any) resets both fresh,
				// same as the create branch above already does.
			}
		}
	}

	if !found {
		return &BlameResult{Address: addr, Found: false}, nil
	}

	leaves := map[string]interface{}{}
	flattenLeaves(current, "", leaves)

	paths := make([]string, 0, len(leaves))
	for path := range leaves {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	entries := make([]BlameEntry, 0, len(paths))
	for _, path := range paths {
		prov, ok := provenance[path]
		valBytes, err := json.Marshal(leaves[path])
		if err != nil {
			return nil, fmt.Errorf("blame: %s: encode %s: %w", addr, path, err)
		}
		entry := BlameEntry{
			Path:     path,
			Value:    valBytes,
			Redacted: isRedactedLeaf(leaves[path]),
		}
		if ok {
			entry.ProposalID = prov.proposal.ID
			entry.Kind = prov.proposal.Kind
			entry.SetAt = prov.setAt
			if a := prov.proposal.Acceptance; a != nil {
				entry.AcceptanceMethod = a.Method
				entry.Approvers = a.Approvers
			}
			entry.AttributedActors = attributedActors(prov.proposal)
			if len(entry.AttributedActors) == 0 {
				entry.UnattributedReason, entry.UnattributedBackend = unattributedNote(prov.proposal)
			}
		}
		entries = append(entries, entry)
	}

	return &BlameResult{
		Address:     addr,
		Found:       true,
		Destroyed:   destroyed,
		DestroyedBy: destroyedBy,
		Entries:     entries,
	}, nil
}

type leafProvenance struct {
	proposal *Proposal
	setAt    string
}

// stampGenesis attributes every leaf in state to p -- a create (adoption
// or shipped change-create) sets every attribute it seeds at once, so
// every leaf's own provenance starts identical until a later Modification
// overwrites some subset of them.
func stampGenesis(state map[string]interface{}, p *Proposal, setAt string, provenance map[string]leafProvenance) {
	leaves := map[string]interface{}{}
	flattenLeaves(state, "", leaves)
	for path := range leaves {
		provenance[path] = leafProvenance{proposal: p, setAt: setAt}
	}
}

// stampPath attributes path -- and, if val is itself a nested object,
// every leaf beneath it too -- to p. A Modification.After entry's own
// established convention is already leaf-level dot-paths (docs/
// schema.md: "tags.Environment", never nested "tags": {...}), so the
// common case is exactly one stamp; the recursive flattenLeaves call
// only ever does real work for a value that deviates from that
// convention (a provider-shaped block written wholesale at once), and
// costs nothing extra when it doesn't.
func stampPath(provenance map[string]leafProvenance, path string, val interface{}, p *Proposal, setAt string) {
	nested := map[string]interface{}{}
	flattenLeaves(val, path, nested)
	for leaf := range nested {
		provenance[leaf] = leafProvenance{proposal: p, setAt: setAt}
	}
}

// flattenLeaves walks v (a decoded JSON tree) and records every LEAF
// value's own dot-path into out. A $redacted marker or an empty object is
// treated as a leaf itself, never recursed into -- map keys are visited
// in sorted order (CLAUDE.md's own "no map-iteration ordering"
// determinism rule; Entries' own order depends on this).
func flattenLeaves(v interface{}, prefix string, out map[string]interface{}) {
	m, ok := v.(map[string]interface{})
	if !ok {
		out[prefix] = v
		return
	}
	if _, ok := m[RedactedMarkerKey]; ok && len(m) == 1 {
		out[prefix] = v
		return
	}
	if len(m) == 0 {
		out[prefix] = v
		return
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		childPath := k
		if prefix != "" {
			childPath = prefix + "." + k
		}
		flattenLeaves(m[k], childPath, out)
	}
}

func isRedactedLeaf(v interface{}) bool {
	m, ok := v.(map[string]interface{})
	if !ok {
		return false
	}
	_, ok = m[RedactedMarkerKey]
	return ok && len(m) == 1
}

// unattributedNote reports the recorded reason (and, for the generalized
// "audit_unattributed" kind, which backend) p's own attribution attempt
// came back empty -- UBI-49 residual #5's correction: the wire layer
// already records this at scan-propose time, this just surfaces it for
// blame. Only ever called when attributedActors(p) is already empty; "",
// "" means p carries no unattributed source at all (an ordinary proposal
// that never attempted attribution in the first place, e.g. a plain
// shipped change).
func unattributedNote(p *Proposal) (reason, backend string) {
	for _, s := range p.Intent.Sources {
		if s.Kind == "cloudtrail_unattributed" || s.Kind == "audit_unattributed" {
			return s.Reason, s.Backend
		}
	}
	return "", ""
}

// attributedActors names every CloudTrail/GCP-audit actor p's own
// intent.sources attributes a change to, newest event first (matching
// AttributeDrift's own ordering, core/attribution.go) -- never collapsed
// to a single guessed cause. Empty for any proposal with no such source,
// which today means anything other than a drift_adopt.
func attributedActors(p *Proposal) []string {
	type sourceWithTime struct {
		actor string
		at    string
	}
	var found []sourceWithTime
	for _, s := range p.Intent.Sources {
		if s.Kind != "cloudtrail" && s.Kind != "gcp_audit" {
			continue
		}
		if s.ActorARN == "" {
			continue
		}
		found = append(found, sourceWithTime{actor: s.ActorARN, at: s.EventTime})
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].at > found[j].at })
	actors := make([]string, 0, len(found))
	for _, f := range found {
		actors = append(actors, f.actor)
	}
	return actors
}
