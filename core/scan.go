package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ubiquex/ubiquex/core/lookuphints"
)

var (
	// ErrResourceUnreadable means ReadResource succeeded at the RPC level
	// but returned no state (nil or JSON null) — the provider couldn't find
	// the resource, as opposed to a transport/protocol error.
	ErrResourceUnreadable = errors.New("resource unreadable: provider returned no state")

	// ErrUnknownResourceType means the requested resource type isn't in the
	// provider's schema at all.
	ErrUnknownResourceType = errors.New("unknown resource type")

	// ErrStaleObservation means a proposal's recorded observed_hash for a
	// live-state resource no longer matches reality — something changed
	// between when the proposal was generated (scan) and now (accept).
	// Accepting it would record a "before"/"after" that's no longer true.
	ErrStaleObservation = errors.New("stale observation: reality changed since this proposal was generated")
)

// StateReader is core's own, minimal view of "something that can fetch a
// provider's schema, configure it, and read one resource's live state" —
// exactly what RunScan/VerifyFreshness need, and nothing about how a
// provider binary is launched or which wire protocol it speaks.
//
// core deliberately does not import package provider (UBI-7 follow-up:
// this used to be provider.Provider directly, which meant core depended on
// infrastructure-client internals it never actually needed to know the
// concrete shape of — it only ever passes provider/resource schema handles
// straight through to Configure/ReadResource without inspecting them, which
// is exactly what `any` captures here). provider.Client can satisfy this
// interface via a small adapter at the call site (see cli's
// providerStateReader) without provider needing to import core either.
type StateReader interface {
	// Schema returns an opaque handle for the provider's own config schema,
	// and one opaque handle per resource type it supports, keyed by type
	// name. Callers pass these straight into Configure/ReadResource; core
	// never looks inside them.
	Schema(ctx context.Context) (providerSchema any, resourceSchemas map[string]any, err error)

	// Configure performs the provider's one-time initialization.
	Configure(ctx context.Context, providerSchema any, config json.RawMessage) error

	// ReadResource fetches the live state of one resource instance.
	ReadResource(ctx context.Context, resourceSchema any, typeName string, currentState json.RawMessage) (json.RawMessage, error)
}

// AttrComputedFlags is an OPTIONAL capability a StateReader's own opaque
// resourceSchema handle (the same `any` Schema() returns and ReadResource
// receives back) may additionally satisfy, so RunScan's drift verdict can
// tell a Computed attribute's null<->materialized-value transition (the
// provider filling in something ubx never told it to be -- e.g. a region
// only known after real creation) apart from an ordinary attribute simply
// changing (UBI-63 session 3: the founder's own live repro against a real
// AWS role reads region back as null immediately after create, then a
// real value on the very next scan -- never a divergence from what ubx
// told the world to be, since ubx was never in a position to know it).
// core still never inspects the schema's concrete type -- it only ever
// asks this one yes/no question through a type assertion on the same
// `any` it already treated as opaque. A StateReader whose handle doesn't
// implement this (the assertion fails) simply gets no Computed-wildcard
// normalization, identical to before this existed.
type AttrComputedFlags interface {
	IsAttrComputed(attrName string) bool
}

// ScanOutcome classifies what a scan found for one resource address.
type ScanOutcome int

const (
	// ScanUnchanged means the resource's live state matches the ledger's
	// last-recorded observed_hash — nothing to propose.
	ScanUnchanged ScanOutcome = iota
	// ScanNew means the ledger has never recorded this address.
	ScanNew
	// ScanDrifted means the ledger has recorded this address before, but
	// the live observed_hash no longer matches.
	ScanDrifted
)

// ScanResult is what one Scan/RunScan call found.
type ScanResult struct {
	Address      Address
	Outcome      ScanOutcome
	Observed     json.RawMessage
	ObservedHash string
	PreviousHash string          // "" if Outcome == ScanNew
	Lookup       json.RawMessage // the lookup key used to read Address — persisted into the generated proposal, see GenerateProposal

	// PreviousState is the ledger's own reconstructed truth (the same
	// FoldState result PreviousHash was computed from) -- nil if Outcome
	// == ScanNew. Additive (2026-07-31, UBI-61): lets a caller render an
	// actual attribute-level diff for a ScanDrifted result (core.
	// DiffAttributes(res.PreviousState, res.Observed), already exported)
	// instead of only ever reporting THAT something drifted. Costs
	// nothing extra to populate -- RunScan already calls FoldState for
	// PreviousHash's own sake; this just keeps the result around too
	// instead of discarding it after hashing.
	PreviousState json.RawMessage

	// ProviderChecksum is "sha256:<hex>" of the exact provider binary used
	// for this scan, if the caller supplied one (see ScanRequest) —
	// persisted into the generated proposal's resolution.inputs entry as
	// attribution evidence (docs/architecture.md — provider acquisition,
	// UBI-8). Empty if the caller didn't supply one (e.g. scanning via a
	// hand-picked --provider path rather than an acquired/verified one).
	ProviderChecksum string
}

// ScanRequest describes one resource to scan.
type ScanRequest struct {
	Address        Address
	ProviderConfig json.RawMessage // passed to Provider.Configure
	CurrentState   json.RawMessage // passed to Provider.ReadResource — the lookup key(s) (e.g. {"id":"...","bucket":"..."})

	// ProviderChecksum is "sha256:<hex>" of the provider binary being used
	// to run this scan (see provider.Acquire's AcquireResult.SHA256) —
	// purely a pass-through value core records, never inspects. Optional;
	// leave empty if unknown or not applicable.
	ProviderChecksum string

	// ProviderSource is the provider's Terraform-source identity (e.g.
	// "hashicorp/google"), used only to look up a teaching-error hint for
	// ErrResourceUnreadable (UBI-21, docs/architecture.md — GCP support:
	// core/lookuphints is keyed by (source, type), not type alone).
	// Populated by the CLI from --source when that's how the provider was
	// resolved; left empty for a raw --provider path, since ubx has no
	// way to know a hand-picked binary's registry identity without
	// guessing. An empty ProviderSource never fails a scan -- it just
	// means ErrResourceUnreadable falls back to its honest generic
	// message instead of a type-specific hint, the same graceful
	// degradation an unrecognized type already gets.
	ProviderSource string
}

// RunScan performs the full scan sequence against an already-launched,
// handshaken provider: fetch its schema, configure it, read the target
// resource, and classify the result against the ledger's last-recorded
// observed state for that address. Each step is wrapped with which step
// failed ("provider errors mid-scan" should be diagnosable, not just a bare
// error).
func RunScan(ctx context.Context, prov StateReader, l *Ledger, req ScanRequest) (*ScanResult, error) {
	observed, hash, resourceSchema, err := readAndFingerprint(ctx, prov, req.Address, req.ProviderSource, req.ProviderConfig, req.CurrentState)
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", req.Address, err)
	}

	// Drift is classified against the ledger's own reconstructed truth
	// (FoldState), not merely the last thing a scan happened to observe
	// (Ledger.LastObservedHash) -- see docs/architecture.md's "Revert path"
	// section ("A necessary correction") for why these two, which coincide
	// for every proposal kind that predates drift_revert, can genuinely
	// diverge once an accepted-but-not-yet-applied drift_revert exists.
	foldedState, found, err := l.FoldState(req.Address)
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", req.Address, err)
	}
	var prevHash string
	if found {
		prevHash, err = ObservedHash(foldedState)
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", req.Address, err)
		}
	}

	res := &ScanResult{
		Address:          req.Address,
		Observed:         observed,
		ObservedHash:     hash,
		PreviousHash:     prevHash,
		PreviousState:    foldedState,
		Lookup:           req.CurrentState,
		ProviderChecksum: req.ProviderChecksum,
	}
	switch {
	case !found:
		res.Outcome = ScanNew
	case prevHash != hash:
		res.Outcome = ScanDrifted
		// UBI-63 session 3: a candidate drift may be fully explained by
		// null<->zero-value/materialization noise rather than a real
		// change -- see explainedByNormalization's own doc comment. Only
		// consulted on this rare path (the fast hash-equality check
		// above already handled the common "genuinely unchanged" case),
		// and only ever downgrades a verdict, never upgrades one.
		if before, after, derr := DiffAttributes(foldedState, observed); derr == nil {
			computed, _ := resourceSchema.(AttrComputedFlags)
			if explainedByNormalization(before, after, computed) {
				res.Outcome = ScanUnchanged
			}
		}
	default:
		res.Outcome = ScanUnchanged
	}
	return res, nil
}

// explainedByNormalization reports whether every entry in a candidate
// drift diff (before/after, dot-notation keyed -- DiffAttributes' own
// output) is fully accounted for by one of two schema-driven
// equivalences named in the founder's own UBI-63 diagnosis ("bug 4"):
// real SDKv2-vintage providers don't round-trip a "no value" attribute
// byte-for-byte, freely alternating between JSON null and the type's own
// zero value (false/""/0/[]/{}) across separate reads of the exact same
// semantic state; and a Computed attribute's null baseline (recorded
// before the provider had actually resolved it) legitimately resolves to
// ANY concrete value on a later read without that being a real
// divergence from what ubx told the world to be. Neither equivalence
// applies once both sides hold a non-null value that genuinely differs
// -- a real change to an already-resolved value still reports as drift.
//
// computed is nil-safe: a StateReader whose schema handle doesn't
// implement AttrComputedFlags simply never satisfies the
// Computed-wildcard half, leaving the zero-value equivalence as the only
// normalization applied.
func explainedByNormalization(before, after map[string]json.RawMessage, computed AttrComputedFlags) bool {
	keys := make(map[string]struct{}, len(before)+len(after))
	for k := range before {
		keys[k] = struct{}{}
	}
	for k := range after {
		keys[k] = struct{}{}
	}
	for key := range keys {
		bRaw, bHas := before[key]
		aRaw, aHas := after[key]
		if !bHas || !aHas {
			return false // added/removed outright, not a null<->value shift
		}
		bNull := isJSONNull(bRaw)
		aNull := isJSONNull(aRaw)
		if !bNull && !aNull {
			return false // both sides hold a real, differing value
		}
		topAttr := key
		if i := strings.IndexByte(key, '.'); i >= 0 {
			topAttr = key[:i]
		}
		if computed != nil && computed.IsAttrComputed(topAttr) {
			continue // materialization: a Computed attribute's null baseline resolving is expected
		}
		nonNull := aRaw
		if aNull {
			nonNull = bRaw
		}
		if !isZeroishLiteral(nonNull) {
			return false
		}
	}
	return true
}

func isJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

// isZeroishLiteral reports whether raw is one of the handful of JSON
// literals every scalar/collection type's own "no value" naturally
// canonicalizes to -- DiffAttributes' own values always come from
// json.Marshal of a decoded generic (via diffObjects), so exact string
// comparison against these canonical forms is safe; there's no
// whitespace or key-ordering variance to account for.
func isZeroishLiteral(raw json.RawMessage) bool {
	switch strings.TrimSpace(string(raw)) {
	case "false", "0", `""`, "[]", "{}":
		return true
	default:
		return false
	}
}

// ReadAndFingerprint exports readAndFingerprint's own read pipeline for
// core/executor (UBI-26): unlike VerifyFreshness, which only compares a
// fresh read's hash against a recorded one and discards the body,
// core/executor needs the full observed state itself, to construct an
// ApplyResourceChange request's PriorState/PlannedState (docs/executor.md
// -- "Constructing PlannedState without planning"). Behaves identically to
// what RunScan/VerifyFreshness already do internally; this is a reuse, not
// a second read pipeline.
func ReadAndFingerprint(ctx context.Context, prov StateReader, addr Address, providerSource string, providerConfig, currentState json.RawMessage) (observed json.RawMessage, hash string, err error) {
	observed, hash, _, err = readAndFingerprint(ctx, prov, addr, providerSource, providerConfig, currentState)
	return observed, hash, err
}

// readAndFingerprint fetches the provider's schema, configures it, reads
// addr's live state, and fingerprints it. Shared by RunScan and
// VerifyFreshness so both apply the exact same read pipeline.
// providerSource is used only for ErrResourceUnreadable's teaching-error
// hint (see lookupHintText); it never affects the read itself and may be
// empty. resourceSchema is the same opaque handle passed into
// ReadResource, returned back out so RunScan can type-assert it against
// AttrComputedFlags (UBI-63 session 3) without a second Schema() round
// trip -- ReadAndFingerprint's own exported signature is unchanged, so
// core/executor's five call sites (a different concern, verifying
// freshness before an apply, not classifying a drift verdict) need no
// changes.
func readAndFingerprint(ctx context.Context, prov StateReader, addr Address, providerSource string, providerConfig, currentState json.RawMessage) (observed json.RawMessage, hash string, resourceSchema any, err error) {
	providerSchema, resourceSchemas, err := prov.Schema(ctx)
	if err != nil {
		return nil, "", nil, fmt.Errorf("fetch schema: %w", err)
	}
	resourceSchema, ok := resourceSchemas[addr.Type]
	if !ok {
		return nil, "", nil, fmt.Errorf("%w: %q", ErrUnknownResourceType, addr.Type)
	}
	if err := prov.Configure(ctx, providerSchema, providerConfig); err != nil {
		return nil, "", nil, fmt.Errorf("configure provider: %w", err)
	}
	observed, err = prov.ReadResource(ctx, resourceSchema, addr.Type, currentState)
	if err != nil {
		return nil, "", nil, fmt.Errorf("read resource: %w", err)
	}
	if len(observed) == 0 || string(observed) == "null" {
		return nil, "", nil, fmt.Errorf("%w: %s", ErrResourceUnreadable, lookupHintText(providerSource, addr.Type))
	}
	hash, err = ObservedHash(observed)
	if err != nil {
		return nil, "", nil, err
	}
	return observed, hash, resourceSchema, nil
}

// lookupHintText builds ErrResourceUnreadable's teaching-error suffix
// (UBI-20 workstream 3, docs/architecture.md — Teaching errors): a
// (providerSource, resourceType)-specific hint for the handful of types
// core/lookuphints (generated from conformance.Registry's
// empirically-verified LookupHint field, keyed by provider source since
// UBI-21) actually knows about, or an honest fallback otherwise -- never
// a fabricated guess dressed up as a known fact. providerSource is empty
// when the scan used a raw --provider path (no known registry source);
// lookuphints.For already handles that by simply not matching, so it
// falls through to the same honest fallback an unrecognized type gets.
//
// The hint's direction was itself verified live against real AWS during
// UBI-20 (conformance/lookuphints_live_test.go), not assumed from the
// Notes prose alone: for aws_s3_bucket/aws_iam_role/aws_iam_user,
// {"id": "<name>"} alone successfully reads the resource, but
// {"bucket": "<name>"}/{"name": "<name>"} alone (the type's own natural,
// Terraform-attribute-shaped key -- an easy thing to reach for instead of
// "id") reads back null. So the actionable hint is "make sure id is
// included," not "you're missing bucket/name" -- lookuphints.For's stored
// value is the misleading natural-key attribute a user might have reached
// for alone, not the field that's actually missing.
func lookupHintText(providerSource, resourceType string) string {
	docsLink := "see https://github.com/Ubiquex/ubiquex-docs, cli/lookup"
	if naturalKey, ok := lookuphints.For(providerSource, resourceType); ok {
		return fmt.Sprintf("%s's lookup must include \"id\" -- %s alone is not enough (%s)",
			resourceType, strings.Join(naturalKey, "/"), docsLink)
	}
	return fmt.Sprintf("check %s's provider schema for its required lookup fields (%s)", resourceType, docsLink)
}

// GenerateProposal builds the adoption/drift_adopt proposal for a scan
// result that found something to record. Callers must check
// res.Outcome != ScanUnchanged first — there is nothing to propose
// otherwise, and GenerateProposal returns an error rather than guessing.
func GenerateProposal(l *Ledger, stack string, res *ScanResult) (*Proposal, error) {
	if res.Outcome == ScanUnchanged {
		return nil, errors.New("generate proposal: scan result is unchanged, nothing to propose")
	}

	head, err := l.Head()
	if err != nil {
		return nil, fmt.Errorf("generate proposal: %w", err)
	}

	p := &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         stack,
		Parent:        head,
		Resolution: Resolution{
			ResolvedAt: time.Now().UTC().Format(time.RFC3339),
			Inputs: []ResolutionInput{
				{
					Kind:             "live_state",
					Resource:         res.Address.String(),
					ObservedHash:     res.ObservedHash,
					Lookup:           res.Lookup,
					ProviderChecksum: res.ProviderChecksum,
				},
			},
		},
		CostDelta:   CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius: BlastRadius{},
		Status:      StatusDraft,
	}

	switch res.Outcome {
	case ScanNew:
		p.Kind = KindAdoption
		p.Intent = Intent{Summary: fmt.Sprintf("adopt existing %s into the ledger (discovered by scan)", res.Address)}
		node, err := json.Marshal(map[string]interface{}{
			"stack": res.Address.Stack,
			"type":  res.Address.Type,
			"name":  res.Address.Name,
			"state": json.RawMessage(res.Observed),
		})
		if err != nil {
			return nil, fmt.Errorf("generate proposal: %w", err)
		}
		p.Delta = Delta{Creates: []json.RawMessage{node}}

	case ScanDrifted:
		p.Kind = KindDriftAdopt
		p.Intent = Intent{Summary: fmt.Sprintf("record drift on %s observed outside the ledger", res.Address)}
		prevState, found, err := l.FoldState(res.Address)
		if err != nil {
			return nil, fmt.Errorf("generate proposal: %w", err)
		}
		if !found {
			return nil, fmt.Errorf("generate proposal: %w: %s has a prior observed_hash but no reconstructable state",
				ErrCorruptLedgerEntry, res.Address)
		}
		before, after, err := DiffAttributes(prevState, res.Observed)
		if err != nil {
			return nil, fmt.Errorf("generate proposal: %w", err)
		}
		p.Delta = Delta{Modifies: []Modification{{Target: res.Address, Before: before, After: after}}}
	}

	return p, nil
}

// GenerateRevertProposal builds the drift_revert proposal for a scan result
// that found drift -- the corrective counterpart to GenerateProposal's
// drift_adopt from the same observation (docs/architecture.md's "Revert
// path"). Only meaningful for ScanDrifted (a never-seen-before resource has
// nothing to revert to); callers must check res.Outcome first, same
// discipline as GenerateProposal.
//
// Unlike drift_adopt's before=ledger/after=observed, a drift_revert's delta
// is before=observed(drifted)/after=ledger(restored-to) -- the same
// diffAttributes function GenerateProposal uses, arguments swapped. Its
// blast_radius is real (docs/schema.md's drift_revert amendment): accepting
// it is a decision to actually change cloud, not a record of something that
// already happened.
func GenerateRevertProposal(l *Ledger, stack string, res *ScanResult) (*Proposal, error) {
	if res.Outcome != ScanDrifted {
		return nil, errors.New("generate revert proposal: scan result is not drifted, nothing to revert")
	}

	head, err := l.Head()
	if err != nil {
		return nil, fmt.Errorf("generate revert proposal: %w", err)
	}

	prevState, found, err := l.FoldState(res.Address)
	if err != nil {
		return nil, fmt.Errorf("generate revert proposal: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("generate revert proposal: %w: %s has a prior observed_hash but no reconstructable state",
			ErrCorruptLedgerEntry, res.Address)
	}

	before, after, err := DiffAttributes(res.Observed, prevState)
	if err != nil {
		return nil, fmt.Errorf("generate revert proposal: %w", err)
	}
	modifies := []Modification{{Target: res.Address, Before: before, After: after}}

	return &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         stack,
		Parent:        head,
		Kind:          KindDriftRevert,
		Intent:        Intent{Summary: fmt.Sprintf("revert %s back to the ledger's recorded state", res.Address)},
		Delta:         Delta{Modifies: modifies},
		Resolution: Resolution{
			ResolvedAt: time.Now().UTC().Format(time.RFC3339),
			Inputs: []ResolutionInput{
				{
					Kind:             "live_state",
					Resource:         res.Address.String(),
					ObservedHash:     res.ObservedHash,
					Lookup:           res.Lookup,
					ProviderChecksum: res.ProviderChecksum,
				},
			},
		},
		CostDelta:   CostDelta{MonthlyUSD: json.RawMessage(`0`)},
		BlastRadius: BlastRadius{Modifies: int64(len(modifies))},
		Status:      StatusDraft,
	}, nil
}

// VerifyFreshness re-reads addr's live state and confirms it still matches
// p's recorded observed_hash for that address, blocking acceptance of a
// proposal whose claimed observation is now stale (docs/plan.md Slice 3 —
// "reality changes between propose and accept"). The lookup key is read
// back from p's own resolution.inputs entry (see ResolutionInput.Lookup) —
// callers don't need to already know, or re-supply, what was used to
// generate the proposal in the first place. providerSource is passed
// straight through to the teaching-error hint path (see
// ScanRequest.ProviderSource); may be empty.
func VerifyFreshness(ctx context.Context, prov StateReader, addr Address, providerSource string, providerConfig json.RawMessage, p *Proposal) error {
	target := addr.String()
	var recorded string
	var lookup json.RawMessage
	found := false
	for _, in := range p.Resolution.Inputs {
		if in.Resource == target {
			recorded = in.ObservedHash
			lookup = in.Lookup
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("verify freshness: no resolution.inputs entry for %s", addr)
	}
	if len(lookup) == 0 {
		return fmt.Errorf("verify freshness: resolution.inputs entry for %s has no recorded lookup key", addr)
	}

	_, fresh, _, err := readAndFingerprint(ctx, prov, addr, providerSource, providerConfig, lookup)
	if err != nil {
		return fmt.Errorf("verify freshness: %w", err)
	}
	if fresh != recorded {
		return fmt.Errorf("%w: %s recorded %s, now %s", ErrStaleObservation, addr, recorded, fresh)
	}
	return nil
}
