package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// applyHashDomainPrefix is prepended to canonical bytes before hashing an
// ApplyRecord -- its own domain, distinct from hashDomainPrefix
// ("ubx:proposal:v1\n"), per docs/schema.md's "Amendment: apply records"
// (UBI-26): an apply record's hash can never collide with a proposal's, or
// any other object's, over the same canonical bytes.
const applyHashDomainPrefix = "ubx:apply:v1\n"

// ApplyRecordSchemaVersion is the current schema_version for ApplyRecord
// objects (docs/schema.md -- "Amendment: apply records").
const ApplyRecordSchemaVersion = 1

// ResourceState is one state in the executor's per-resource failure-state
// machine (docs/executor.md).
type ResourceState string

const (
	ResourcePending            ResourceState = "pending"
	ResourceInFlight           ResourceState = "in_flight"
	ResourceApplied            ResourceState = "applied"
	ResourceFailed             ResourceState = "failed"
	ResourceUnknownPostTimeout ResourceState = "unknown_post_timeout"
	ResourceStillUnknown       ResourceState = "still_unknown"
)

// Transition is one entry of ResourceApply.Transitions -- a state the
// resource moved into, and when. docs/executor.md's THE invariant depends
// on the "at" timestamp reflecting when this was durably written, not
// when the risky operation it precedes was attempted.
type Transition struct {
	State  ResourceState `json:"state"`
	At     string        `json:"at"` // RFC3339
	Detail string        `json:"detail,omitempty"`
}

// ReconciliationAttempt is one entry of ResourceApply.Reconciliation -- one
// reconcile-by-query (ReadResource) attempt made while resolving an
// unknown_post_timeout/still_unknown resource (docs/executor.md).
type ReconciliationAttempt struct {
	At      string `json:"at"`
	Outcome string `json:"outcome"` // applied | failed | inconclusive
	Detail  string `json:"detail,omitempty"`
}

// ErrorClassification is ErrorRecord.Classification's enum (docs/executor.md
// -- "Error taxonomy: retryable vs. terminal").
type ErrorClassification string

const (
	ErrorRetryable ErrorClassification = "retryable"
	ErrorTerminal  ErrorClassification = "terminal"
)

// ErrorRecord is one entry of ResourceApply.Errors.
type ErrorRecord struct {
	At             string              `json:"at"`
	Message        string              `json:"message"`
	Classification ErrorClassification `json:"classification"`
}

// ResourceApply is one per-resource entry of ApplyRecord.Resources.
//
// Lookup was added 2026-07-17 (docs/schema.md — "Amendment: apply-record
// lookup key + Fleet discovery", UBI-29): the JSON object a future
// ReadResource call needs to find this resource again, recorded
// explicitly the moment a create's own ApplyResourceChange call succeeds
// (core/executor's shipCreate) — {"id": "<value>"}, mirroring
// ResolutionInput.Lookup's own convention and the same "never depend on
// derivation at need-time" reasoning that field was added for (docs/schema.md's
// "Amendment: persist resource lookup key", UBI-7 follow-up). Purely
// additive/omitempty; unset (not guessed) if the applied result has no
// derivable "id".
type ResourceApply struct {
	Address        Address                 `json:"address"`
	Transitions    []Transition            `json:"transitions,omitempty"`
	ProviderResult json.RawMessage         `json:"provider_result,omitempty"`
	Lookup         json.RawMessage         `json:"lookup,omitempty"`
	Reconciliation []ReconciliationAttempt `json:"reconciliation,omitempty"`
	Errors         []ErrorRecord           `json:"errors,omitempty"`
}

// LastState returns ra's most recently recorded transition state, and
// whether it has any transitions at all.
func (ra *ResourceApply) LastState() (state ResourceState, ok bool) {
	if len(ra.Transitions) == 0 {
		return "", false
	}
	return ra.Transitions[len(ra.Transitions)-1].State, true
}

// DeriveLookupFromResult builds a lookup key from a resource's own
// applied/observed state (docs/schema.md's UBI-29 amendment): "id" if
// present and non-empty, plus every OTHER non-empty value named in
// requiredAttrNames.
//
// UBI-63 session 2, found live: this used to assume "id" alone is
// always sufficient to re-find a resource later -- true for every type
// this codebase had touched until aws_iam_role_policy_attachment's own
// real AWS Read implementation, confirmed live: even though this type's
// real schema DOES declare an "id" attribute, its own ReadResource
// needs "role"/"policy_arn" present in the current_state it's handed to
// construct a valid API call at all, and a bare {"id": ...} lookup left
// both blank, producing a real, structured API error ("roleName is
// invalid") at the next destroy/scan read. requiredAttrNames names the
// resource type's own real schema-Required attributes (never Optional/
// Computed ones -- those can legitimately be absent or provider-
// defaulted, so baking one in here would risk a stale value in what's
// supposed to be a stable identity key, not a snapshot of observed
// drift) -- core itself stays provider-import-free (core/scan.go), so
// this never inspects a schema itself; the one caller with a concrete
// schema in scope (cli/stateadapter.go's ApplyResourceChange, the same
// place that already redacts Sensitive attributes for the identical
// reason) computes the list and passes it in. nil is always a legal,
// honest value (the shippedCreateFold legacy-fallback call site, below,
// has no live schema to consult for an apply record that predates the
// Lookup field at all) -- this degrades to the old id-only behavior,
// never fails.
//
// Returns nil if nothing at all could be derived -- an honest "can't
// derive a lookup," never a guess.
func DeriveLookupFromResult(result json.RawMessage, requiredAttrNames []string) json.RawMessage {
	if len(result) == 0 {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(result, &m); err != nil {
		return nil
	}
	out := map[string]interface{}{}
	if idRaw, ok := m["id"]; ok {
		var id interface{}
		if err := json.Unmarshal(idRaw, &id); err == nil && id != nil && id != "" {
			out["id"] = id
		}
	}
	for _, name := range requiredAttrNames {
		raw, ok := m[name]
		if !ok {
			continue
		}
		var v interface{}
		if err := json.Unmarshal(raw, &v); err != nil {
			continue
		}
		if v == nil || v == "" {
			continue
		}
		out[name] = v
	}
	if len(out) == 0 {
		return nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	return b
}

// shippedCreateFold is the shared per-(proposal, address) apply-record
// fold every UBI-29 discovery path (FoldState, Fleet, ProposalsForAddress,
// LastObservedHash, LastObservationTime) uses: a specific resource's own
// real applied result, its recorded lookup key (falling back to
// DeriveLookupFromResult for an apply record that predates the Lookup
// field), and the exact moment it reached applied. found is true only if
// this resource's own MOST RECENT transition is ResourceApplied --
// independent of whether the enclosing multi-resource ApplyRecord itself
// has been sealed (a resource's own completion and its attempt's overall
// summary are different things; core/executor's own foldResourceHistory
// established this first, UBI-27 -- mirrored here since core doesn't
// import core/executor). A resource still pending/in_flight/failed/
// still_unknown (a real kill mid-create) is never found here, which is
// exactly what keeps a half-created resource from being surfaced as
// discoverable before it's actually done.
func (l *Ledger) shippedCreateFold(proposalID string, addr Address) (result, lookup json.RawMessage, appliedAt string, found bool, err error) {
	attempts, err := l.ApplyAttempts(proposalID)
	if err != nil {
		return nil, nil, "", false, fmt.Errorf("shipped create fold: %w", err)
	}
	target := addr.String()
	var lastState ResourceState
	var hasState bool
	for _, a := range attempts {
		for _, ra := range a.Resources {
			if ra.Address.String() != target {
				continue
			}
			if st, ok := ra.LastState(); ok {
				lastState = st
				hasState = true
			}
			if len(ra.ProviderResult) > 0 {
				result = ra.ProviderResult
			}
			if len(ra.Lookup) > 0 {
				lookup = ra.Lookup
			}
			for _, t := range ra.Transitions {
				if t.State == ResourceApplied {
					appliedAt = t.At
				}
			}
		}
	}
	if !hasState || lastState != ResourceApplied {
		return nil, nil, "", false, nil
	}
	if len(lookup) == 0 {
		// No live schema to consult here -- an apply record old enough
		// to predate the Lookup field at all has no concrete schema in
		// scope to derive requiredAttrNames from; nil degrades to the
		// original id-only behavior, an honest, accepted limitation for
		// this specific legacy-data path (DeriveLookupFromResult's own
		// doc comment).
		lookup = DeriveLookupFromResult(result, nil)
	}
	return result, lookup, appliedAt, true, nil
}

// shippedDestroyFold is shippedCreateFold's own destroy-side counterpart
// (docs/schema.md — "Amendment: destroys", UBI-30; docs/executor.md's own
// "shipping destroys" amendment): folds proposalID's own apply records for
// a Delta.Destroys entry at addr. found is true only if this resource's
// own MOST RECENT transition is ResourceApplied -- the same per-resource,
// not per-attempt, gating shippedCreateFold already established. For a
// destroy specifically, reaching ResourceApplied at all only ever happens
// via core/executor's own shipDestroyNode determining "destroyed" or
// "already_absent" (never any other path produces it for a Delta.Destroys
// entry), so found alone is sufficient to know addr is tombstoned --
// outcome (the last Reconciliation entry's own Outcome) is returned
// alongside purely so a caller that wants to render WHICH of the two
// actually happened (`ubx why`) doesn't need its own separate fold.
func (l *Ledger) shippedDestroyFold(proposalID string, addr Address) (outcome string, found bool, err error) {
	attempts, err := l.ApplyAttempts(proposalID)
	if err != nil {
		return "", false, fmt.Errorf("shipped destroy fold: %w", err)
	}
	target := addr.String()
	var lastState ResourceState
	var hasState bool
	for _, a := range attempts {
		for _, ra := range a.Resources {
			if ra.Address.String() != target {
				continue
			}
			if st, ok := ra.LastState(); ok {
				lastState = st
				hasState = true
			}
			if len(ra.Reconciliation) > 0 {
				outcome = ra.Reconciliation[len(ra.Reconciliation)-1].Outcome
			}
		}
	}
	if !hasState || lastState != ResourceApplied {
		return "", false, nil
	}
	return outcome, true, nil
}

// shippedModifyFold is shippedCreateFold/shippedDestroyFold's own modify-
// side counterpart (UBI-89 P1): folds proposalID's own apply records for a
// Delta.Modifies entry at addr, exactly the same per-resource "was this
// address's own most recent transition ResourceApplied" gate the create
// and destroy sides already had -- found is true only then. Before this
// existed, FoldState's own modify-folding loop had NO equivalent gate at
// all (the one asymmetry among the three Delta kinds): an ACCEPTED
// Delta.Modifies entry was folded into "current truth" unconditionally,
// regardless of whether it was ever actually shipped, still pending, or
// had FAILED mid-ship (core.VerifyFreshness's own ErrStaleObservation
// refusal, core/executor's shipModifyNode/shipDriftRevert transition
// straight to ResourceFailed on that path, never reaching ResourceApplied
// and never recording a ProviderResult) -- confirmed live, the founder's
// own repro: a modify accepted into the ledger while the real resource
// never actually changed, every reader of ledger truth (`ubx why`, `ubx
// status --drift`, a future `ubx plan`/`resolve` against this stack)
// lied about it. Only found is returned -- unlike shippedCreateFold, a
// modify's own resulting state is reconstructed by FoldState's existing
// dotSet(mod.After) walk, not substituted wholesale from ProviderResult
// (the minimal, surgical fix: gate the existing mechanism, don't replace
// it).
func (l *Ledger) shippedModifyFold(proposalID string, addr Address) (found bool, err error) {
	attempts, err := l.ApplyAttempts(proposalID)
	if err != nil {
		return false, fmt.Errorf("shipped modify fold: %w", err)
	}
	target := addr.String()
	var lastState ResourceState
	var hasState bool
	for _, a := range attempts {
		for _, ra := range a.Resources {
			if ra.Address.String() != target {
				continue
			}
			if st, ok := ra.LastState(); ok {
				lastState = st
				hasState = true
			}
		}
	}
	return hasState && lastState == ResourceApplied, nil
}

// ApplySummary is ApplyRecord.Summary -- populated only once an attempt is
// sealed (docs/schema.md's "sealed vs. live" note). Reflects the full,
// cumulative per-resource accounting for the proposal as of the end of
// THIS attempt (including resources that were already applied in a prior
// attempt and simply carried forward untouched) -- not just what this one
// attempt itself newly did. This is a deliberate choice: a reader (a
// future `ubx why`/`ubx status`) should be able to answer "what's the
// state of this proposal" from the single latest apply record alone,
// without also folding every earlier attempt.
type ApplySummary struct {
	Outcome               string `json:"outcome"` // applied | partially_applied | failed
	StartedAt             string `json:"started_at"`
	FinishedAt            string `json:"finished_at"`
	ResourcesApplied      int64  `json:"resources_applied"`
	ResourcesFailed       int64  `json:"resources_failed"`
	ResourcesStillUnknown int64  `json:"resources_still_unknown"`
}

// ApplyRecord is the typed apply-record object per docs/schema.md's
// "Amendment: apply records" (UBI-26). ID and Summary are unset while an
// attempt is still live/in-progress -- see docs/schema.md's "sealed vs.
// live" note: an apply record's content accretes transition by transition
// as core/executor's state machine runs, so its hash (and therefore its
// ID) cannot exist until the attempt reaches a terminal outcome. Records
// are append-per-attempt: Ledger.SealApply is the only thing that ever
// finalizes one; nothing ever edits an already-sealed record.
type ApplyRecord struct {
	SchemaVersion int64            `json:"schema_version"`
	ID            string           `json:"id,omitempty"`
	Proposal      string           `json:"proposal"`
	Parent        string           `json:"parent"`
	Attempt       int64            `json:"attempt"`
	Resources     []*ResourceApply `json:"resources,omitempty"`
	Summary       *ApplySummary    `json:"summary,omitempty"`
}

// Sealed reports whether r has been finalized -- an ID and summary are
// only ever populated together, at seal time (docs/schema.md).
func (r *ApplyRecord) Sealed() bool { return r.ID != "" && r.Summary != nil }

// ApplyHash computes an ApplyRecord's content hash: domain-prefixed
// SHA-256 (ubx:apply:v1\n) over canonical JSON of the record minus id,
// with Resources sorted by (stack, type, name) -- mirroring Proposal's own
// Hash (canonical.go), but in ApplyRecord's own hash domain, since the two
// object families must never collide over the same canonical bytes.
func ApplyHash(r *ApplyRecord) (string, error) {
	canon, err := DoubleRun(func() ([]byte, error) {
		return canonicalApplyRecordBytes(r)
	})
	if err != nil {
		return "", err
	}
	return sha256Hex(append([]byte(applyHashDomainPrefix), canon...)), nil
}

func canonicalApplyRecordBytes(r *ApplyRecord) ([]byte, error) {
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("marshal apply record: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var generic map[string]interface{}
	if err := dec.Decode(&generic); err != nil {
		return nil, fmt.Errorf("decode apply record: %w", err)
	}
	delete(generic, "id")
	if resources, ok := generic["resources"].([]interface{}); ok {
		sortResourceElements(resources)
	}
	canon, err := canonicalizeNumbers(generic)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(canon); err != nil {
		return nil, fmt.Errorf("encode canonical apply record: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// sortResourceElements sorts a decoded ApplyRecord.Resources array in
// place, lexicographically by its "address"'s (stack, type, name) --
// mirroring sortDeltaElements' own reasoning (canonical.go): no array may
// rely on insertion/map-iteration order for a hashed field.
func sortResourceElements(arr []interface{}) {
	sort.SliceStable(arr, func(i, j int) bool {
		si, ti, ni := resourceSortKey(arr[i])
		sj, tj, nj := resourceSortKey(arr[j])
		if si != sj {
			return si < sj
		}
		if ti != tj {
			return ti < tj
		}
		return ni < nj
	})
}

func resourceSortKey(el interface{}) (stack, typ, name string) {
	obj, ok := el.(map[string]interface{})
	if !ok {
		return "", "", ""
	}
	addr, ok := obj["address"].(map[string]interface{})
	if !ok {
		return "", "", ""
	}
	stack, _ = addr["stack"].(string)
	typ, _ = addr["type"].(string)
	name, _ = addr["name"].(string)
	return stack, typ, name
}

var (
	// ErrApplyRecordNotFound means no apply record exists for the given
	// (proposal ID, attempt) pair.
	ErrApplyRecordNotFound = errors.New("apply record not found in ledger")

	// ErrCorruptApplyRecord means an apply-record file exists but its
	// contents aren't well-formed JSON -- a truncated or interrupted
	// write, disk corruption, hand-editing, ... (docs/executor-adversarial.md
	// row 9: ubx ship must refuse to guess at what state resources were
	// actually left in, the same posture ErrCorruptLedgerEntry already
	// takes for proposals).
	ErrCorruptApplyRecord = errors.New("corrupt apply record")
)

// ApplyAttempts returns every attempt recorded for proposalID so far --
// sealed or still-live/crashed -- ordered oldest (attempt 1) first. A
// crashed, never-sealed attempt is returned exactly as it is on disk (no
// ID, no Summary): per-resource idempotency folding (core/executor) needs
// every transition ever durably written, not just sealed ones -- only the
// Parent hash-chain link requires a sealed ID (see BeginApply). A
// malformed attempt file is a hard error (ErrCorruptApplyRecord), never
// silently skipped -- guessing at what a corrupt record might have said is
// exactly what docs/executor-adversarial.md's row 9 forbids.
func (l *Ledger) ApplyAttempts(proposalID string) ([]*ApplyRecord, error) {
	ctx := context.Background()
	attempts, err := l.store.ListApplyAttempts(ctx, proposalID)
	if err != nil {
		return nil, fmt.Errorf("apply attempts: %w", err)
	}
	sort.Slice(attempts, func(i, j int) bool { return attempts[i] < attempts[j] })

	records := make([]*ApplyRecord, 0, len(attempts))
	for _, n := range attempts {
		b, err := l.store.ReadApply(ctx, proposalID, n)
		if err != nil {
			return nil, fmt.Errorf("apply attempts: attempt %d: %w", n, err)
		}
		var r ApplyRecord
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, fmt.Errorf("apply attempts: attempt %d: %w: %v", n, ErrCorruptApplyRecord, err)
		}
		records = append(records, &r)
	}
	return records, nil
}

// ReadApply reads one specific attempt by (proposal ID, attempt number).
func (l *Ledger) ReadApply(proposalID string, attempt int64) (*ApplyRecord, error) {
	b, err := l.store.ReadApply(context.Background(), proposalID, attempt)
	if errors.Is(err, ErrStoreObjectNotFound) {
		return nil, fmt.Errorf("apply %s attempt %d: %w", proposalID, attempt, ErrApplyRecordNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("read apply %s attempt %d: %w", proposalID, attempt, err)
	}
	var r ApplyRecord
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("apply %s attempt %d: %w: %v", proposalID, attempt, ErrCorruptApplyRecord, err)
	}
	return &r, nil
}

// BeginApply starts a new apply attempt for proposalID: under the same
// ledger lock Append already uses (docs/executor.md -- "Concurrency: the
// same ledger lock, reused"), determines the next attempt number and this
// proposal's most recently *sealed* attempt's ID (Parent -- "" if none),
// writes an initial, empty, unsealed record to its deterministic attempt
// file, and returns it. The lock is held only for this decide-then-create
// sequence, not the caller's whole apply loop.
func (l *Ledger) BeginApply(proposalID string) (*ApplyRecord, error) {
	ctx := context.Background()
	release, err := l.store.Lock(ctx, ledgerLockTTL)
	if err != nil {
		return nil, fmt.Errorf("begin apply: %w", err)
	}
	defer release(ctx)

	attempts, err := l.ApplyAttempts(proposalID)
	if err != nil {
		return nil, fmt.Errorf("begin apply: %w", err)
	}

	var nextAttempt int64 = 1
	var parent string
	for _, a := range attempts {
		if a.Attempt >= nextAttempt {
			nextAttempt = a.Attempt + 1
		}
		if a.Sealed() {
			parent = a.ID
		}
	}

	rec := &ApplyRecord{
		SchemaVersion: ApplyRecordSchemaVersion,
		Proposal:      proposalID,
		Parent:        parent,
		Attempt:       nextAttempt,
	}
	if err := l.writeApplyFile(rec); err != nil {
		return nil, fmt.Errorf("begin apply: %w", err)
	}
	return rec, nil
}

// SaveApplyProgress durably persists rec's current in-progress content --
// called after every transition/resource update, per docs/executor.md's
// THE invariant: a state transition must be on disk before the risky
// operation it precedes is attempted. rec must not yet be sealed.
func (l *Ledger) SaveApplyProgress(rec *ApplyRecord) error {
	if rec.Sealed() {
		return errors.New("save apply progress: record is already sealed")
	}
	return l.writeApplyFile(rec)
}

// SealApply finalizes rec: sets its terminal Summary, computes its content
// hash (ApplyHash), sets ID, and durably persists the final result. Once
// sealed, a record is never edited again.
func (l *Ledger) SealApply(rec *ApplyRecord, summary ApplySummary) (*ApplyRecord, error) {
	if rec.Sealed() {
		return nil, errors.New("seal apply: record is already sealed")
	}
	rec.Summary = &summary
	hash, err := ApplyHash(rec)
	if err != nil {
		return nil, fmt.Errorf("seal apply: %w", err)
	}
	rec.ID = hash
	if err := l.writeApplyFile(rec); err != nil {
		return nil, fmt.Errorf("seal apply: %w", err)
	}
	return rec, nil
}

// writeApplyFile marshals rec and durably writes it via the store --
// durability itself (temp+fsync+rename for a local filesystem; a single
// atomic object PUT for a real object store) is each store's own
// responsibility, per LedgerStore.WriteApply's own contract, matching
// docs/executor.md's THE invariant either way.
func (l *Ledger) writeApplyFile(rec *ApplyRecord) error {
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("write apply record: marshal: %w", err)
	}
	if err := l.store.WriteApply(context.Background(), rec.Proposal, rec.Attempt, b); err != nil {
		return fmt.Errorf("write apply record: %w", err)
	}
	return nil
}

// ApplyAfter returns a copy of state with mod's After dot-path values
// substituted in -- the same substitution FoldState performs when folding
// a Modification onto reconstructed ledger truth, exposed so core/executor
// (UBI-26) can construct an ApplyResourceChange request's PlannedState
// directly from a drift_revert's already-concrete restore values, without
// a separate plan phase (docs/executor.md -- "Constructing PlannedState
// without planning").
//
// A path present in Before but absent from After is DELETED from the
// result, not left untouched -- found empirically, not assumed, while
// live-testing ubx ship end to end (UBI-26 session 3): diffAttributes
// records an attribute that exists in observed/drifted reality but not in
// the ledger's own recorded truth (e.g. a tag added out-of-band) with an
// entry in Before and none in After at all, since there is no ledger value
// to record for something the ledger never had. Reverting that means
// removing the attribute from live reality, which a pure dotSet-based
// substitution can never do on its own -- it only ever sets values,
// never un-sets one. Confirmed against a real scenario before fixing:
// shipping a drift_revert that should remove an added tag left the tag in
// place, "applied" reported cleanly with no error, silently wrong. A path
// present in both Before and After (an ordinary changed value) is
// unaffected -- After's dotSet already runs first and wins.
func ApplyAfter(state json.RawMessage, mod Modification) (json.RawMessage, error) {
	var current map[string]interface{}
	if err := json.Unmarshal(state, &current); err != nil {
		return nil, fmt.Errorf("apply after: decode state: %w", err)
	}
	for path, raw := range mod.After {
		var v interface{}
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("apply after: bad after[%q]: %w", path, err)
		}
		dotSet(current, path, v)
	}
	for path := range mod.Before {
		if _, ok := mod.After[path]; ok {
			continue // a genuine value change, already applied above
		}
		dotDelete(current, path)
	}
	b, err := json.Marshal(current)
	if err != nil {
		return nil, fmt.Errorf("apply after: %w", err)
	}
	return b, nil
}
