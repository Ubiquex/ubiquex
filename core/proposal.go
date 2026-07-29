// Package core implements the proposal ledger: the typed proposal object,
// canonical hashing (docs/schema.md — Canonical hashing, RATIFIED v1), and
// the append-only per-stack ledger it's recorded in.
package core

import (
	"encoding/json"
	"strings"
)

// SchemaVersion is the current schema_version for Proposal objects. The
// ledger is forever; any change to hashed-content shape requires bumping
// this and providing a migration (docs/schema.md Ratification note).
//
// Bumped 1 -> 2 (2026-07-17, UBI-30, docs/schema.md "Amendment: destroys"):
// Delta.Destroys' own element shape changed from a bare Address to
// DestroyEntry (address + full folded state + depends_on) -- a real,
// hashed-content shape change, not an additive field. The migration cost
// is genuinely near-zero, checked rather than assumed: core.Validate has
// forbidden a non-empty delta.destroys unconditionally for every proposal
// kind this codebase has ever produced, since before this shape existed --
// there is no real ledger entry anywhere with the old bare-Address shape
// populated to migrate.
const SchemaVersion = 2

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
//
// Assumptions, Defaults, and Questions were added 2026-07-27 (UBI-41,
// docs/schema.md's own "Amendment: intent-provider drafts" amendment): an
// intent-provider draft's ambiguity content, ridden unchanged through
// resolve/accept into the final hashed, signed proposal -- the same
// struct a hand-written intent file's own Intent already occupies, so
// this content is reviewable and signed by construction, never a
// separate unsigned side-channel (docs/intent-provider.md's own
// "ambiguity as visible content" design center). Purely additive/
// optional; a proposal recorded before this amendment simply has all
// three empty/absent, same reasoning as every other additive amendment
// to this struct.
type Intent struct {
	Summary     string          `json:"summary"`
	Sources     []IntentSource  `json:"sources,omitempty"`
	Assumptions []AmbiguityNote `json:"assumptions,omitempty"`
	Defaults    []AmbiguityNote `json:"defaults,omitempty"`
	Questions   []Question      `json:"questions,omitempty"`
}

// AmbiguityNote is one entry of Intent.Assumptions (a real interpretive
// choice made where a source document was genuinely ambiguous) or
// Intent.Defaults (a gap the document left unaddressed entirely, filled
// from context) -- docs/schema.md's UBI-41 amendment. Affects is a list
// of canonical address+path strings ("<stack>.<type>.<name>.<path>", the
// same dot-path convention Modification.Before/.After and $ref's own
// "to" field already use), never a free-text pointer, so a review
// surface can highlight the exact config value in question.
type AmbiguityNote struct {
	Text    string   `json:"text"`
	Affects []string `json:"affects,omitempty"`
}

// Question is one entry of Intent.Questions -- an unresolved tension (a
// genuine contradiction, or an ambiguity too open-ended to responsibly
// guess at) a draft still had to produce some concrete resolution for
// (docs/schema.md's UBI-41 amendment). Blocking is a review-affordance
// signal only -- it carries zero resolver-side enforcement; core.Validate
// and core/resolver never inspect it, never refuse a proposal because of
// it. docs/intent-provider.md's own "Component 3" section names the
// considered-and-rejected alternative (auto-refusing resolve on a
// blocking question) explicitly: it would hand an LLM veto power over
// what a human is allowed to review and sign, inverting the trust chain
// this whole field exists to preserve.
type Question struct {
	Text     string   `json:"text"`
	Affects  []string `json:"affects,omitempty"`
	Blocking bool     `json:"blocking,omitempty"`
}

// IntentSource is one entry of Intent.Sources. ContentHash is a
// "sha256:<hex>" content hash of the referenced dialogue/PR/issue at
// resolution time (docs/schema.md — tamper-evidence for intent evidence
// that otherwise lives outside the proposal's own hash chain).
//
// CloudTrailEvent/Unattributed fields were added 2026-07-10 (docs/schema.md
// — "Amendment: CloudTrail attribution intent sources", UBI-10) for two new
// kinds, "cloudtrail" and "cloudtrail_unattributed" — see AttributeDrift
// (attribution.go). Purely additive/optional, same reasoning as every
// other schema.md amendment since the hashing ratification: existing
// proposals with only dialogue/manual_edit/issue sources are unaffected.
//
// UBI-21 (docs/architecture.md — GCP support) adds two more kinds for the
// GCP Cloud Audit Logs backend: "gcp_audit" (a matched event, GCP's
// counterpart to "cloudtrail") and "audit_unattributed" (a generalized,
// backend-tagged counterpart to "cloudtrail_unattributed" -- see Backend
// below). "cloudtrail_unattributed" is not deprecated or migrated; it
// remains permanently valid, and cloudtrail/'s own AWS backend keeps
// emitting it unchanged, not "audit_unattributed" -- only the newer GCP
// backend uses the generalized kind, since there's no existing AWS output
// to preserve compatibility with there.
//
// UBI-41 (docs/schema.md's own "Amendment: intent-provider drafts") adds
// two more kinds, both provenance for an intent-provider-authored draft:
// "document" (the authoring markdown file itself -- ContentHash covers the
// RAW, unredacted file exactly as committed, never the redacted copy an
// adapter actually saw) and "intent_provider" (which adapter/model
// produced the draft, Ref shaped "<adapter>:<model>", ContentHash pinning
// the adapter's own raw structured-output response as tamper-evident
// audit content -- evidence of provenance, never an enforced binding: a
// human editing the draft file before resolve is legitimate and
// unaffected). See intentprovider.PopulateSources.
type IntentSource struct {
	Kind        string `json:"kind"` // dialogue | manual_edit | issue | cloudtrail | cloudtrail_unattributed | gcp_audit | audit_unattributed | document | intent_provider
	Ref         string `json:"ref,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`

	// The following are populated only for Kind == "cloudtrail" or
	// "gcp_audit". ActorARN carries the GCP caller's principal email for
	// "gcp_audit" sources, not literally an ARN -- reusing the same field
	// (not introducing a GCP-specific one) is a deliberate choice: both
	// backends implement the same core.EventLookup interface and produce
	// the same core.CloudTrailEvent shape (see audit/gcp/client.go's own
	// doc comment for the full reasoning); SessionContext is AWS-specific
	// and always empty for "gcp_audit" sources.
	EventID        string          `json:"event_id,omitempty"`
	EventName      string          `json:"event_name,omitempty"`
	EventTime      string          `json:"event_time,omitempty"` // RFC3339
	ActorARN       string          `json:"actor_arn,omitempty"`
	SourceIP       string          `json:"source_ip,omitempty"`
	SessionContext json.RawMessage `json:"session_context,omitempty"`

	// Reason is populated for Kind == "cloudtrail_unattributed" or
	// "audit_unattributed": no_matching_event | delivery_window |
	// not_logged.
	Reason string `json:"reason,omitempty"`

	// Backend is populated only for Kind == "audit_unattributed": which
	// platform's attribution backend came up empty -- "gcp_audit_logs"
	// today, more values as more backends are added. Never populated for
	// "cloudtrail_unattributed", which predates this field and needs no
	// disambiguation (it only ever meant CloudTrail).
	Backend string `json:"backend,omitempty"`
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

// ParseAddress parses s in the canonical "<stack>.<type>.<name>" form
// (Address.String()'s own output) back into an Address — used by `ubx why`
// (UBI-11: polish before demo recording) to accept a resource address as an
// alternative to a proposal ID. Splits on the first two dots only (not
// strings.Split), so a resource name that itself contains a dot round-trips
// correctly. ok is false if s doesn't have all three non-empty components.
func ParseAddress(s string) (addr Address, ok bool) {
	parts := strings.SplitN(s, ".", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return Address{}, false
	}
	return Address{Stack: parts[0], Type: parts[1], Name: parts[2]}, true
}

// Modification is one entry of Delta.Modifies (docs/schema.md, pinned
// 2026-07-10). Before/After hold only the attributes that changed — not
// full resource state — keyed by dot-notation attribute path for nested
// values (e.g. "tags.Environment", not a nested "tags": {"Environment": ...}
// object). Every Modification's Target MUST have a matching
// Resolution.Inputs entry with a non-empty ObservedHash — see validate.go —
// so a proposal's claimed "before" is provable against what was actually
// observed, not just asserted.
//
// DependsOn was added 2026-07-17 (docs/schema.md — "Amendment: intent
// files and resolved change proposals", UBI-27): canonical addresses
// (Address.String() form) this modification must not be executed before —
// e.g. a modify referencing a sibling create's not-yet-known computed
// output in the same change proposal. Purely additive/optional; a
// drift_revert's own Modification entries never set it (order never
// mattered there — every restore value was already fully known). The
// *authoritative* record of execution order, independent of stored array
// position (core/resolver, UBI-27, also topo-sorts delta.creates/.modifies
// themselves before emitting them, but a future executor session
// consulting cross-array order should read this field, not infer from
// position).
// Provider was added 2026-07-18 (docs/schema.md — "Amendment: the
// provider field returns", UBI-43): which provider binary owns this
// resource. Nil for a drift_revert's own Modification entries (single-
// provider by construction, core/scan.go never populates it) and for any
// change proposal resolved before this amendment (schema_version 1/2) --
// an absent field there is never ambiguous, since every proposal predating
// this amendment was resolved against exactly one provider per invocation.
// core/resolver populates it for every Modification a change proposal's
// own resolveOnce produces, going forward, single-provider stacks
// included -- see docs/resolver.md's own amendment.
type Modification struct {
	Target    Address                    `json:"target"`
	Before    map[string]json.RawMessage `json:"before,omitempty"`
	After     map[string]json.RawMessage `json:"after,omitempty"`
	DependsOn []string                   `json:"depends_on,omitempty"`
	Provider  *ProviderRef               `json:"provider,omitempty"`
}

// ProviderRef names which provider binary owns a resource node -- {source,
// version}, exactly the founding IR draft's own shape (docs/schema.md's
// "IR — resource node" section, top of that document) and the stack's own
// providers config map's own value shape (docs/architecture.md §Multi-
// provider stacks). Reinstated 2026-07-18 (UBI-43) after UBI-27's own
// pinning had dropped it as "redundant with information the outer
// Proposal already carries" -- true only under the single-provider-per-
// invocation invariant that amendment predates. Resolver-populated, never
// hand-authored except IntentFile's own narrow ambiguity-breaking hint
// (source only, no version -- see core/resolver.ProviderHint).
type ProviderRef struct {
	Source  string `json:"source"`
	Version string `json:"version"`
}

// DestroyEntry is one element of Delta.Destroys (docs/schema.md —
// "Amendment: destroys", UBI-30) — re-pinned from a bare Address
// (2026-07-10 draft) to this richer shape, a real hashed-content shape
// change (SchemaVersion 1 -> 2).
//
// Address is unchanged in shape, now nested rather than being the whole
// element (deltaSortKey, canonical.go, reads it the same way it already
// reads Modification.Target).
//
// State is the resource's complete FoldState-folded config at resolve
// time — deliberately the WHOLE state, not a changed-attributes-only diff
// the way Modification.Before/After are: everything about the resource is
// being lost, not a subset of its attributes, so a human signing away a
// destroy needs to see it inline, not a hash to separately dereference
// (docs/resolver.md's own reasoning).
//
// DependsOn reuses Modification.DependsOn's exact field name and meaning
// ("this element's own operation must not execute before every named
// address's own operation, in this same proposal, has completed") —
// populated by core/resolver's own orphan-protection walk with the
// REVERSE edge set (which other same-batch elements must run first: a
// sibling destroy whose own historical dependency graph names this
// target, or a sibling modify that repoints away from it), never the
// forward set a create/modify's own DependsOn would carry. The meaning of
// the field never changes across creates/modifies/destroys — only which
// edge set populates it does, which is exactly what makes "destroys
// execute in reversed order" fall out of one combined topological walk
// (docs/executor.md's own amendment) instead of needing a second
// mechanism.
// Provider was added 2026-07-18 (docs/schema.md — "Amendment: the
// provider field returns", UBI-43) -- see Modification.Provider's own
// doc comment; the same reasoning applies here unchanged. A destroy needs
// to know which provider to call exactly as much as a create does, and
// core/resolver infers it fresh against the stack's currently-declared
// providers, never inherited from whichever provider originally created
// the resource (docs/resolver.md's own amendment).
type DestroyEntry struct {
	Address   Address         `json:"address"`
	State     json.RawMessage `json:"state"`
	DependsOn []string        `json:"depends_on,omitempty"`
	Provider  *ProviderRef    `json:"provider,omitempty"`
}

// Delta is Proposal.Delta. Creates stays opaque JSON — typed IR resource
// nodes don't exist yet (docs/architecture.md component map #1-2 hasn't
// been built; Slice 2/3 only need to hash and ledger hand-written/adoption
// proposals, not construct one from a resolver) — but its elements are
// still expected to carry direct stack/type/name fields per the IR
// resource node shape shown under §IR above, which sortDeltaElements relies
// on. Modifies and Destroys have a pinned shape (see Address, Modification,
// DestroyEntry above); their sort-key extraction (deltaSortKey,
// canonical.go) reads those fields directly now rather than guessing at an
// unpinned shape.
type Delta struct {
	Creates  []json.RawMessage `json:"creates,omitempty"`
	Modifies []Modification    `json:"modifies,omitempty"`
	Destroys []DestroyEntry    `json:"destroys,omitempty"`
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
//
// Lookup was added 2026-07-10 (docs/schema.md — "Amendment: persist
// resource lookup key", UBI-7 follow-up): the JSON object passed to the
// provider's ReadResource to identify this resource (e.g.
// {"id":"...","bucket":"..."}), for kind "live_state" entries. Persisting
// it lets a proposal be independently re-verified later (see
// core.VerifyFreshness) without the caller having to already know, or
// re-supply, exactly what was used to look the resource up the first time.
//
// ProviderChecksum was added 2026-07-10 (docs/architecture.md — provider
// acquisition, UBI-8): "sha256:<hex>" of the exact provider binary that
// produced this observation, once acquired/verified via provider.Acquire.
// Attribution evidence — which build of which provider is responsible for
// this reading — independent of ObservedHash, which fingerprints the
// resource's state, not the tool that read it.
//
// PinnedHead was added 2026-07-17 (docs/schema.md — "Amendment: intent
// files and resolved change proposals", UBI-27): populated only for
// Kind == "cross_stack_pin" entries — the neighbor ledger's own Head() at
// resolve time. This is what activates neighbor-advance staleness for
// real: re-deriving the neighbor's current Head() at accept time and
// comparing against PinnedHead catches "the neighbor moved since this was
// resolved," the same "resolved-time truth vs. accept-time reality" shape
// VerifyFreshness already enforces for live cloud state, one level up (a
// ledger, not a cloud resource).
//
// LedgerDir was added alongside PinnedHead, for the same "cross_stack_pin"
// entries: the neighbor's own ledger directory (the $cross marker's own
// "ledger_dir" field, carried through) -- a real gap found implementing
// the resolver, not in the original amendment text: re-verifying a pin
// needs to know WHERE to re-derive the neighbor's current head from, and
// nothing else in a resolved proposal records that. Purely additive,
// same reasoning as every other amendment to this struct.
// Status and CheckedLedgerDirs were added 2026-07-17 (docs/schema.md —
// "Amendment: destroys", UBI-30), both populated only for Kind ==
// "cross_stack_orphan_check" entries: Status is "checked_clear" (every
// ledger_dir in CheckedLedgerDirs was walked and none pinned this
// address) or "not_performed" (no known_dependents were named at resolve
// time — cross-stack orphan protection is best-effort by design,
// docs/resolver.md's own reasoning; this records the gap as honest
// evidence rather than leaving it silently indistinguishable from a real
// check). Purely additive, same reasoning as every other amendment to
// this struct.
//
// From was added 2026-07-28 (docs/schema.md — "Amendment: cross-stack
// pin attribution", UBI-47 session 4), populated only for Kind ==
// "cross_stack_pin" entries: the REFERENCING resource's own address —
// i.e. the local resource whose config held the $cross marker that
// resolveCross resolved. A real, load-bearing gap found while building
// ubx render's own $cross-annotation feature, not present in the
// original UBI-27 amendment: Resource, for a cross_stack_pin entry, has
// always named the NEIGHBOR's address (what was pinned), never the
// local resource that did the pinning, and resolutionInputs across a
// whole resolve batch were flattened into one slice with no back-
// reference at all — there was no way, from a resolved proposal alone,
// to answer "which of my own resources references this neighbor pin."
// From closes exactly that gap, purely additively (same "no
// schema_version bump" reasoning as PinnedHead/LedgerDir before it).
type ResolutionInput struct {
	Kind              string          `json:"kind"`
	Resource          string          `json:"resource"`
	From              string          `json:"from,omitempty"`
	ObservedHash      string          `json:"observed_hash"`
	Lookup            json.RawMessage `json:"lookup,omitempty"`
	ProviderChecksum  string          `json:"provider_checksum,omitempty"`
	PinnedHead        string          `json:"pinned_head,omitempty"`
	LedgerDir         string          `json:"ledger_dir,omitempty"`
	Status            string          `json:"status,omitempty"`
	CheckedLedgerDirs []string        `json:"checked_ledger_dirs,omitempty"`
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
//
// PRNumber and ProposalFile were added 2026-07-11 (docs/schema.md —
// "Amendment: pr_merge acceptance fields", UBI-11 stage 1), both for
// method "pr_merge": PRNumber is the GitHub pull request MergeSHA belongs
// to; ProposalFile is the repo-relative path the proposal file lived at,
// at that commit. Both are conveniences in the sense that everything
// needed to re-derive acceptance is already derivable from MergeSHA alone
// (the GitHub API resolves a commit to its PR; the PR's own history holds
// the diff) — but recording them directly means re-verification
// (`ubx why --verify-acceptance`) is self-contained from the ledger entry
// alone, never needing the operator to remember or re-supply what path a
// proposal lived at just to re-check it.
type Acceptance struct {
	Method       string   `json:"method"` // pr_merge | local | crypto
	MergeSHA     string   `json:"merge_sha,omitempty"`
	PRNumber     int64    `json:"pr_number,omitempty"`
	ProposalFile string   `json:"proposal_file,omitempty"`
	Approvers    []string `json:"approvers,omitempty"`
	AcceptedAt   string   `json:"accepted_at,omitempty"`
}
