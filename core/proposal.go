// Package core implements the proposal ledger: the typed proposal object,
// canonical hashing (docs/schema.md — Canonical hashing, RATIFIED v1), and
// the append-only per-stack ledger it's recorded in.
package core

import "encoding/json"

// SchemaVersion is the current schema_version for Proposal objects. The
// ledger is forever; any change to hashed-content shape requires bumping
// this and providing a migration (docs/schema.md Ratification note).
const SchemaVersion = 1

// Proposal is the typed proposal object per docs/schema.md §Proposal.
type Proposal struct {
	SchemaVersion     int64            `json:"schema_version"`
	ID                string           `json:"id,omitempty"`
	Stack             string           `json:"stack"`
	Parent            string           `json:"parent"`
	Kind              ProposalKind     `json:"kind"`
	Intent            Intent           `json:"intent"`
	Delta             Delta            `json:"delta"`
	Resolution        Resolution       `json:"resolution"`
	CostDelta         CostDelta        `json:"cost_delta"`
	BlastRadius       BlastRadius      `json:"blast_radius"`
	InvariantsChecked []InvariantCheck `json:"invariants_checked,omitempty"`
	Acceptance        *Acceptance      `json:"acceptance,omitempty"`
	Status            Status           `json:"status"`
}

// ProposalKind is Proposal.Kind.
type ProposalKind string

const (
	KindChange      ProposalKind = "change"
	KindAdoption    ProposalKind = "adoption"
	KindDriftAdopt  ProposalKind = "drift_adopt"
	KindDriftRevert ProposalKind = "drift_revert"
	KindRevert      ProposalKind = "revert"
)

// Status is Proposal.Status — excluded from the content hash (it is
// recorded, and changes, after the hash exists).
type Status string

const (
	StatusDraft    Status = "draft"
	StatusRefined  Status = "refined"
	StatusAccepted Status = "accepted"
	StatusApplied  Status = "applied"
	StatusStale    Status = "stale"
	StatusRejected Status = "rejected"
)

// Intent is Proposal.Intent.
type Intent struct {
	Summary string         `json:"summary"`
	Sources []IntentSource `json:"sources,omitempty"`
}

// IntentSource is one entry of Intent.Sources. ContentHash is a
// "sha256:<hex>" content hash of the referenced dialogue/PR/issue at
// resolution time (docs/schema.md — tamper-evidence for intent evidence
// that otherwise lives outside the proposal's own hash chain).
type IntentSource struct {
	Kind        string `json:"kind"` // dialogue | manual_edit | issue
	Ref         string `json:"ref"`
	ContentHash string `json:"content_hash,omitempty"`
}

// Address identifies one resource within a stack: (stack, type, name).
// This is the pinned shape for Delta.Destroys elements and
// Modification.Target (docs/schema.md, pinned 2026-07-10).
type Address struct {
	Stack string `json:"stack"`
	Type  string `json:"type"`
	Name  string `json:"name"`
}

// String renders an Address in its canonical cross-reference form,
// "<stack>.<type>.<name>" — how a delta.modifies target is matched against
// a resolution.inputs entry (see validate.go) and how
// ResolutionInput.Resource is expected to be written.
func (a Address) String() string {
	return a.Stack + "." + a.Type + "." + a.Name
}

// Modification is one entry of Delta.Modifies (docs/schema.md, pinned
// 2026-07-10). Before/After hold only the attributes that changed — not
// full resource state — keyed by dot-notation attribute path for nested
// values (e.g. "tags.Environment", not a nested "tags": {"Environment": ...}
// object). Every Modification's Target MUST have a matching
// Resolution.Inputs entry with a non-empty ObservedHash — see validate.go —
// so a proposal's claimed "before" is provable against what was actually
// observed, not just asserted.
type Modification struct {
	Target Address                    `json:"target"`
	Before map[string]json.RawMessage `json:"before,omitempty"`
	After  map[string]json.RawMessage `json:"after,omitempty"`
}

// Delta is Proposal.Delta. Creates stays opaque JSON — typed IR resource
// nodes don't exist yet (docs/architecture.md component map #1-2 hasn't
// been built; Slice 2/3 only need to hash and ledger hand-written/adoption
// proposals, not construct one from a resolver) — but its elements are
// still expected to carry direct stack/type/name fields per the IR
// resource node shape shown under §IR above, which sortDeltaElements relies
// on. Modifies and Destroys have a pinned shape (see Address, Modification
// above); their sort-key extraction (deltaSortKey, canonical.go) reads
// those fields directly now rather than guessing at an unpinned shape.
type Delta struct {
	Creates  []json.RawMessage `json:"creates,omitempty"`
	Modifies []Modification    `json:"modifies,omitempty"`
	Destroys []Address         `json:"destroys,omitempty"`
}

// Resolution is Proposal.Resolution.
type Resolution struct {
	ResolvedAt string            `json:"resolved_at"`
	Inputs     []ResolutionInput `json:"inputs,omitempty"`
}

// ResolutionInput is one entry of Resolution.Inputs. Resource is expected
// to be an Address's canonical string form ("<stack>.<type>.<name>") when
// it corresponds to a Delta.Modifies target — see validate.go, which
// cross-references the two.
type ResolutionInput struct {
	Kind         string `json:"kind"`
	Resource     string `json:"resource"`
	ObservedHash string `json:"observed_hash"`
}

// CostDelta is Proposal.CostDelta. MonthlyUSD is left as raw JSON because
// its legal shape is a union per docs/schema.md's ratified number rule: a
// bare JSON integer (e.g. `59`) OR a JSON string for anything fractional
// (e.g. `"59.99"`) — never a JSON float literal. Canonicalization enforces
// this uniformly across the whole proposal tree (see canonical.go), so this
// field doesn't need its own bespoke Go type/validation.
type CostDelta struct {
	MonthlyUSD json.RawMessage `json:"monthly_usd,omitempty"`
}

// BlastRadius is Proposal.BlastRadius. These are always resource counts —
// inherently integers, so plain int64 fields are both correct and simpler
// than the CostDelta union.
type BlastRadius struct {
	Creates  int64 `json:"creates"`
	Modifies int64 `json:"modifies"`
	Destroys int64 `json:"destroys"`
}

// InvariantCheck is one entry of Proposal.InvariantsChecked.
type InvariantCheck struct {
	Policy  string `json:"policy"`
	Verdict string `json:"verdict"`
}

// Acceptance is Proposal.Acceptance — excluded from the content hash (it is
// recorded after the hash exists, and must not perturb it).
type Acceptance struct {
	Method     string   `json:"method"` // pr_merge | local | crypto
	MergeSHA   string   `json:"merge_sha,omitempty"`
	Approvers  []string `json:"approvers,omitempty"`
	AcceptedAt string   `json:"accepted_at,omitempty"`
}
