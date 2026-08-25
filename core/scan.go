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

	// ErrNormalizationNoiseOnly means a ScanDrifted result's diff, once
	// FilterNormalizationNoise strips null<->zero-value/materialization
	// noise, has nothing real left to propose (UBI-63 session 4) --
	// should not normally happen (RunScan's own verdict already applies
	// the identical filter before ever calling this ScanDrifted), but
	// guards GenerateProposal/GenerateRevertProposal against emitting a
	// nonsensical empty-delta modification if a caller ever hands in a
	// ScanResult computed some other way.
	ErrNormalizationNoiseOnly = errors.New("generate proposal: drift is fully explained by normalization noise, nothing real to propose")
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

	// ResourceSchema is the same opaque per-type schema handle
	// readAndFingerprint already fetched and passed to ReadResource --
	// carried onto the result (UBI-63 session 4) so a caller rendering or
	// recording a diff from PreviousState/Observed (cli's live `status
	// --drift`, GenerateProposal/GenerateRevertProposal) can pass it to
	// FilterNormalizationNoise and get the SAME Computed-aware filtering
	// RunScan's own verdict already applies, instead of recomputing a raw,
	// unfiltered DiffAttributes that re-surfaces noise RunScan already
	// determined wasn't real drift. core never inspects it beyond that one
	// type assertion (AttrComputedFlags) -- still opaque, still no import
	// of package provider.
	ResourceSchema any
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
		ResourceSchema:   resourceSchema,
	}
	switch {
	case !found:
		res.Outcome = ScanNew
	case prevHash != hash:
		res.Outcome = ScanDrifted
		// UBI-63 session 3/4: a candidate drift may be fully explained by
		// null<->zero-value/materialization noise rather than a real
		// change -- see FilterNormalizationNoise's own doc comment. Only
		// consulted on this rare path (the fast hash-equality check
		// above already handled the common "genuinely unchanged" case),
		// and only ever downgrades a verdict, never upgrades one.
		if before, after, derr := DiffAttributes(foldedState, observed); derr == nil {
			fb, fa := FilterNormalizationNoise(before, after, resourceSchema)
			if len(fb) == 0 && len(fa) == 0 {
				res.Outcome = ScanUnchanged
			}
		}
	default:
		res.Outcome = ScanUnchanged
	}
	return res, nil
}

// FilterNormalizationNoise strips every before/after entry (before/after,
// dot-notation keyed -- DiffAttributes' own output shape) that's fully
// accounted for by one of the schema-driven equivalences named in the
// founder's own UBI-63 diagnosis ("bug 4"): real SDKv2-vintage providers
// don't round-trip a "no value" attribute byte-for-byte, freely
// alternating between JSON null and the type's own zero value
// (false/""/0/[]/{}) across separate reads of the exact same semantic
// state; and a Computed attribute's null baseline (recorded before the
// provider had actually resolved it) legitimately resolves to ANY
// concrete value on a later read without that being a real divergence
// from what ubx told the world to be. A third equivalence (UBI-88, found
// live in core/resolver's modify diff -- a full ledger-state "before"
// against a partial drafted-config "after"): an explicit JSON null on one
// side and the key missing outright on the other are the same "no value"
// state under two different representations. None of these equivalences
// apply once both sides hold a non-null, genuinely-present value that
// differs -- a real change to an already-resolved value always survives
// the filter.
//
// Exported and reused everywhere a diff is shown or recorded (UBI-63
// session 4: found live -- a resource with exactly one genuinely-real
// change alongside several individually-explainable ones had its ENTIRE
// unfiltered diff rendered/embedded, because the original design only
// ever downgraded RunScan's own overall verdict, never filtered what a
// caller went on to display or persist): RunScan's own verdict above,
// GenerateProposal/GenerateRevertProposal's embedded Delta.Modifies (so
// `scan --propose`/`--revert` never records noise as if it were adopted
// or reverted drift), cli's own live `status --drift` rendering, and
// (UBI-88) core/resolver's own OpModify diff, so an ordinary `ubx plan`/
// `ubx why` modify receipt never shows a "null -> (absent)" line either.
//
// resourceSchema is the same opaque handle ScanResult.ResourceSchema
// carries; a StateReader whose handle doesn't implement
// AttrComputedFlags simply never satisfies the Computed-wildcard half,
// leaving the zero-value equivalence as the only normalization applied.
func FilterNormalizationNoise(before, after map[string]json.RawMessage, resourceSchema any) (filteredBefore, filteredAfter map[string]json.RawMessage) {
	computed, _ := resourceSchema.(AttrComputedFlags)
	keys := make(map[string]struct{}, len(before)+len(after))
	for k := range before {
		keys[k] = struct{}{}
	}
	for k := range after {
		keys[k] = struct{}{}
	}
	filteredBefore = make(map[string]json.RawMessage, len(before))
	filteredAfter = make(map[string]json.RawMessage, len(after))
	for key := range keys {
		if isNormalizationExplained(key, before, after, computed) {
			continue
		}
		if v, ok := before[key]; ok {
			filteredBefore[key] = v
		}
		if v, ok := after[key]; ok {
			filteredAfter[key] = v
		}
	}
	return filteredBefore, filteredAfter
}

func isNormalizationExplained(key string, before, after map[string]json.RawMessage, computed AttrComputedFlags) bool {
	bRaw, bHas := before[key]
	aRaw, aHas := after[key]
	topAttr := key
	if i := strings.IndexByte(key, '.'); i >= 0 {
		topAttr = key[:i]
	}
	if bHas != aHas {
		// UBI-88: a key entirely missing on one side is the SAME "no
		// value" state as an explicit null OR the type's own zero value on
		// the other -- confirmed live, not assumed: a real SDKv2-style
		// null<->zero-value round-trip quirk (fakeprovider's own
		// decodeWidgetState, modeling a real map-typed attribute) produces
		// current="tags: {}" for an attribute a modify's own drafted
		// config simply never mentions (bHas=true/"{}", aHas=false), the
		// exact same equivalence class as the null<->zero-value case
		// below, just with the "no value" side spelled as a missing key
		// instead of an explicit literal. A full-state object (this
		// function's usual before/after) always carries every schema key;
		// a partial document (e.g. core/resolver's modify diff, comparing
		// full ledger state against a drafted config that legitimately
		// omits an attribute it never touched) omits the key outright
		// instead. Only a REAL, non-null, non-zero value on the present
		// side still counts as a genuine add/remove.
		present := bRaw
		if aHas {
			present = aRaw
		}
		if computed != nil && computed.IsAttrComputed(topAttr) {
			return true // materialization: a Computed attribute's null baseline resolving is expected
		}
		return isJSONNull(present) || isZeroishLiteral(present)
	}
	bNull := isJSONNull(bRaw)
	aNull := isJSONNull(aRaw)
	if !bNull && !aNull {
		return false // both sides hold a real, differing value
	}
	if computed != nil && computed.IsAttrComputed(topAttr) {
		return true // materialization: a Computed attribute's null baseline resolving is expected
	}
	nonNull := aRaw
	if aNull {
		nonNull = bRaw
	}
	return isZeroishLiteral(nonNull)
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
		before, after = FilterNormalizationNoise(before, after, res.ResourceSchema)
		if len(before) == 0 && len(after) == 0 {
			return nil, ErrNormalizationNoiseOnly
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
	before, after = FilterNormalizationNoise(before, after, res.ResourceSchema)
	if len(before) == 0 && len(after) == 0 {
		return nil, ErrNormalizationNoiseOnly
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
//
// UBI-89 P1: a raw hash mismatch here used to always mean "stale," full
// stop -- unlike RunScan's own drift verdict, which already downgrades a
// candidate mismatch fully explained by null<->zero-value/materialization
// noise (FilterNormalizationNoise, UBI-63) before ever calling it real
// drift. This was a real, confirmed gap (hermetic repro:
// TestVerifyFreshness_FalseStaleOnNormalizationNoise), not a UBI-88
// regression -- UBI-88 never touched ObservedHash/resolution.inputs at
// all. For a core/resolver-produced modify specifically, "recorded" is
// the hash of the LEDGER's own reconstructed truth at resolve time
// (ObservedHash(FoldState(addr)), core/resolver.go's OpModify case) --
// not a fresh read the way an adoption/drift_adopt's recorded hash is
// (GenerateProposal's own res.ObservedHash, already fresh at generation
// time). A real SDKv2-vintage provider not round-tripping a "no value"
// attribute byte-for-byte between the resource's own last-recorded state
// and a later live read (the founder's own live AWS repro shape) can
// therefore raw-hash-mismatch on a genuinely UNTOUCHED resource. l is now
// used to re-derive that same raw recorded state (FoldState(addr) again
// -- the exact deterministic input that produced recorded's own hash,
// same pattern RunScan already uses) as a fallback ONLY when the raw
// hashes differ: a real, non-noise difference still refuses as stale
// (TestVerifyFreshness_RealChangeStillBlocksAlongsideNoise) -- this only
// ever downgrades a false positive, never masks a real one.
func VerifyFreshness(ctx context.Context, prov StateReader, l *Ledger, addr Address, providerSource string, providerConfig json.RawMessage, p *Proposal) error {
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

	observed, fresh, resourceSchema, err := readAndFingerprint(ctx, prov, addr, providerSource, providerConfig, lookup)
	if err != nil {
		return fmt.Errorf("verify freshness: %w", err)
	}
	if fresh == recorded {
		return nil
	}
	if foldedState, ffound, ferr := l.FoldState(addr); ferr == nil && ffound {
		if before, after, derr := DiffAttributes(foldedState, observed); derr == nil {
			fb, fa := FilterNormalizationNoise(before, after, resourceSchema)
			if len(fb) == 0 && len(fa) == 0 {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: %s recorded %s, now %s", ErrStaleObservation, addr, recorded, fresh)
}

// VerifyDataSourceFreshness re-checks every "data_source"-kind
// resolution.inputs entry in p, unconditionally -- UBI-178's own "no new
// ledger machinery" claim is real for STORAGE (Kind: "data_source"
// reuses the exact same ResolutionInput shape "live_state" already
// established -- ObservedHash/Lookup, nothing new) but was incomplete
// for VERIFICATION: VerifyFreshness above is only ever called per
// Delta.Modifies target (core/executor/ship.go, both call sites,
// confirmed by reading them directly) -- a data source result never
// appears in a delta at all, it changes nothing, so nothing would ever
// re-check it without this. This is that missing pass, not a variant of
// the existing one: it iterates the WHOLE resolution.inputs slice
// itself rather than looking up one target's own single entry, since
// there is no delta entry to loop over in the first place.
//
// Deliberately stricter than VerifyFreshness's own FoldState/
// DiffAttributes/FilterNormalizationNoise fallback: that fallback exists
// for a real, specific provider-fidelity gap in how a MANAGED resource's
// own recorded state round-trips (this file's own doc comment on
// VerifyFreshness has the full account) -- whether the identical
// tolerance is correct for an arbitrary data source read is a real,
// separate, unanswered question, not assumed away by copying the same
// fallback here. A genuine hash mismatch always refuses; there is no
// silent-pass path.
//
// Each entry's own Resource field is expected to parse as an Address
// (ParseAddress) whose Type carries the data-source-vs-resource
// distinction by convention (e.g. "data.aws_ec2_instance", never a bare
// resource type) -- the same three-component (stack, type, name) shape
// every other address in this codebase already uses, deliberately not a
// new struct.
func VerifyDataSourceFreshness(ctx context.Context, prov StateReader, providerSource string, providerConfig json.RawMessage, p *Proposal) error {
	for _, in := range p.Resolution.Inputs {
		if in.Kind != "data_source" {
			continue
		}
		addr, ok := ParseAddress(in.Resource)
		if !ok {
			return fmt.Errorf("verify data source freshness: %q is not a valid address", in.Resource)
		}
		if len(in.Lookup) == 0 {
			return fmt.Errorf("verify data source freshness: resolution.inputs entry for %s has no recorded lookup key", addr)
		}
		_, fresh, _, err := readAndFingerprint(ctx, prov, addr, providerSource, providerConfig, in.Lookup)
		if err != nil {
			return fmt.Errorf("verify data source freshness: %w", err)
		}
		if fresh != in.ObservedHash {
			return fmt.Errorf("%w: %s recorded %s, now %s", ErrStaleObservation, addr, in.ObservedHash, fresh)
		}
	}
	return nil
}
