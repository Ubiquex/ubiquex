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

// Delta is Proposal.Delta. Elements are kept as opaque JSON rather than
// modeled as typed IR nodes — the IR/resolver components (docs/architecture.md
// component map #1-2) don't exist yet; Slice 2 only needs to hash and ledger
// a hand-written proposal, not construct one from a resolver.
//
// docs/schema.md's ratified hashing rule sorts these arrays lexicographically
// by (stack, type, name) rather than preserving authoring/dependency order —
// see sortDeltaElements. That rule is written against Creates' shape (IR
// resource nodes: stack/type/name are direct fields). Modifies/Destroys'
// element shape is still an open "..." placeholder in docs/schema.md, so
// their sort-key extraction (deltaSortKey) is a best-effort interpretation,
// not a pinned schema decision — see STATE.md for this flagged as such.
type Delta struct {
	Creates  []json.RawMessage `json:"creates,omitempty"`
	Modifies []json.RawMessage `json:"modifies,omitempty"`
	Destroys []json.RawMessage `json:"destroys,omitempty"`
}

// Resolution is Proposal.Resolution.
type Resolution struct {
	ResolvedAt string            `json:"resolved_at"`
	Inputs     []ResolutionInput `json:"inputs,omitempty"`
}

// ResolutionInput is one entry of Resolution.Inputs.
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
