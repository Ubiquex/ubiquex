// Package intentprovider is UBI-41's own boundary for LLM-authored
// intent/v1 drafts -- kept out of core deliberately (core stays
// dependency-free; this needs an HTTP client and vendor SDKs, the same
// inversion audit/cloudtrail, audit/gcp, audit/k8s already establish for
// platform-specific I/O), and kept as its own package rather than folded
// into core/resolver (which stays provider-import-free, docs/resolver.md
// -- and now, by the same discipline, intentprovider-import-free too).
//
// The whole package exists to enforce one boundary, stated at this
// project's founding session and finally tested for real here
// (docs/architecture.md's own trust-chain invariant #3: "the LLM
// operates in intent-space only... nothing it emits reaches apply
// without resolution + human signature"): an intent provider transcribes
// prose into an intent/v1 draft and nothing else. It never resolves a
// $ref, never touches a ledger, never calls a cloud provider. Adapter's
// own signature makes this structural, not just documented --
// DraftRequest carries no ledger handle, no provider schema, no
// filesystem access, so an Adapter implementation physically cannot
// reach any of those even if it wanted to.
//
// See docs/intent-provider.md for the full design and
// docs/intent-provider-adversarial.md for the required-outcome program
// this package's own hermetic tests are checked against.
package intentprovider

import (
	"context"
	"encoding/json"
)

// Adapter is one LLM backend's transcription capability -- Claude first
// (intentprovider/claude), OpenAI/Gemini/local following, each earning
// "supported" only by passing the conformance suite
// (intentprovider/conformance), never by existing.
type Adapter interface {
	// Name identifies this adapter for config selection, conformance
	// results, and error messages -- "claude", "openai", "gemini", "ollama".
	Name() string

	// Model identifies which model this adapter instance is configured
	// to use -- needed uniformly by PopulateSources's own "<adapter>:<model>"
	// intent_provider source Ref, so provenance never depends on a
	// caller separately tracking what it passed to a specific adapter's
	// own constructor.
	Model() string

	// Draft transcribes one authoring document into a single intent/v1
	// draft attempt, returning the adapter's raw structured-output JSON
	// UNVALIDATED against intent/v1 -- validation is DraftWithRetry's own
	// job (validate.go), never the adapter's, so every adapter is
	// validated by the identical mechanism regardless of which vendor's
	// own claimed schema-conformance guarantee backs it.
	//
	// An error return is treated as an immediate, non-retried failure by
	// DraftWithRetry (network failure, authentication failure, a policy
	// refusal) -- distinct from a validation failure (malformed or
	// semantically invalid JSON), which IS retried. An Adapter
	// implementation should therefore only return an error for something
	// re-asking the model in the same conversation cannot fix.
	Draft(ctx context.Context, req DraftRequest) (json.RawMessage, error)
}

// DraftRequest is deliberately the entire boundary an Adapter can act on
// -- no ledger handle, no provider schema, no filesystem access.
type DraftRequest struct {
	// Stack is the target stack name, transcribed into the draft's own
	// "stack" field.
	Stack string

	// Content is the document content an adapter transcribes. The md
	// pipeline session (docs/plan.md's own UBI-41 wedge subsection) is
	// responsible for redaction-at-capture (docs/intent-provider.md's own
	// "Secret material in a doc" section) before Content ever reaches an
	// Adapter -- this package's own contract already assumes that
	// happened; Content here is whatever the caller hands it, raw or
	// redacted alike.
	Content []byte

	// Attempt is 1 on the first call. A value >1 means this is a
	// retry-with-errors round: PriorOutput/PriorErrors carry the
	// previous attempt's own rejected output and exactly what was wrong
	// with it, verbatim -- an Adapter implementation MUST feed both back
	// to the model, never silently discard them and just try again from
	// scratch.
	Attempt     int
	PriorOutput json.RawMessage
	PriorErrors []string

	// KnownResources is the target stack's own currently-recorded state,
	// keyed by "<type>.<name>" (matching how a resources[] entry itself
	// is addressed) -- UBI-85: fed in so a re-plan against an EXISTING
	// stack can distinguish "this doc still matches what's already
	// shipped" (correctly omit from resources[] entirely) from "this doc
	// now describes something different" (op=modify) from "this doc
	// describes something the ledger has never seen" (op=create,
	// unchanged from pre-UBI-85 behavior). Each value is that address's
	// full recorded state (core.Ledger.FoldState's own output) -- every
	// attribute, including provider-computed ones the document itself
	// never mentions (an id, an ARN), since an Adapter drafting a modify
	// must reproduce them unchanged rather than omit them (see
	// intentprovider/claude's own system prompt for why an omitted
	// attribute reads as REMOVED, not preserved).
	//
	// Nil or empty for a fresh/never-seen stack, or for a caller with no
	// ledger context to offer (ubx propose --from-doc and ubx chat both
	// deliberately never open a ledger at all, UBI-85 session's own
	// scope decision -- preserving their existing "never touches a
	// ledger" boundary) -- an Adapter must then treat every resource as
	// if the stack were empty, matching this package's entire pre-UBI-85
	// behavior exactly.
	//
	// Still just plain, pre-computed DATA -- the caller's own already-
	// read Fleet/FoldState result -- never a live ledger handle or query
	// capability. This does not weaken this struct's own founding
	// boundary (no ledger handle, no provider schema, no filesystem
	// access): an Adapter still cannot reach the ledger itself, ask a new
	// question of it, or see anything beyond the fixed snapshot the
	// caller decided to hand over, the identical relationship Content
	// already has to the source document's own file.
	KnownResources map[string]json.RawMessage
}
