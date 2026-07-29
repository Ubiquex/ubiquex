package core

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

// ReasonNoCreationVerbs means genesis attribution was never even
// attempted for this resource, because the caller has no known
// creation-verb event name(s) for its own type — distinct from
// ReasonNoMatchingEvent (attribution ran a real search and found
// nothing) and ReasonNotLogged (the search itself failed): an operator
// reading `ubx why` should be able to tell "this type isn't in the
// creation-verb table yet" apart from "attribution genuinely came up
// empty."
const ReasonNoCreationVerbs = "no_creation_verb_table"

// AttributeGenesis searches for a resource's own creation event — who
// created it, not who most recently changed it (AttributeDrift's own
// job, UBI-10/UBI-21). Reuses AttributeDrift's own identity-candidate
// search and defensive exact-match filtering unchanged (identityCandidates,
// filterExactMatch) — genesis attribution is the same mechanical search,
// pointed at a different question, not a second search mechanism.
//
// Two real differences from drift attribution:
//
//   - What's searched for: creationVerbs narrows matched events to a
//     specific creation-verb EventName (e.g. "CreateQueue") — a small,
//     curated per-type list the caller supplies (core stays entirely
//     type/backend-agnostic, the same reason AttributionBackend itself
//     is a parameter, not hardcoded here). An empty creationVerbs means
//     the caller has no known creation-verb table for this resource's
//     own type yet — genesis attribution is never even attempted,
//     recorded honestly as ReasonNoCreationVerbs rather than silently
//     skipped or conflated with a real, attempted-and-failed search.
//   - Which match wins: a resource is created exactly once, so among
//     genuinely matched creation-verb events, the OLDEST wins — the
//     actual moment of creation — never the newest (AttributeDrift's
//     own "newest first" convention is right for drift, wrong here). A
//     genuine re-create under the same identity (rare, but real) still
//     resolves to whichever event actually founded this resource's
//     current lineage.
//
// Has no drift-time anchor to work from at all — a discovered
// resource's own creation time is exactly the unknown this function
// exists to find — so the caller supplies [since, until] directly,
// typically as wide as the backend's own retention allows (CloudTrail's
// default management-event trail: 90 days).
func AttributeGenesis(ctx context.Context, lookup EventLookup, addr Address, observed json.RawMessage, since, until time.Time, backend AttributionBackend, creationVerbs []string) []IntentSource {
	if len(creationVerbs) == 0 {
		return []IntentSource{{Kind: backend.UnattributedKind, Reason: ReasonNoCreationVerbs, Backend: backend.Name}}
	}

	candidates := identityCandidates(observed, addr.Name)
	for _, candidate := range candidates {
		events, err := lookup.LookupEvents(ctx, candidate, since, until)
		if err != nil {
			return []IntentSource{{Kind: backend.UnattributedKind, Reason: ReasonNotLogged, Backend: backend.Name}}
		}
		matched := filterExactMatch(events, candidates)
		creation := filterByEventName(matched, creationVerbs)
		if len(creation) > 0 {
			return attributionSourcesOldestFirst(creation, backend.SuccessKind)
		}
	}

	if until.Sub(since) < backend.DeliveryLag {
		return []IntentSource{{Kind: backend.UnattributedKind, Reason: ReasonDeliveryWindow, Backend: backend.Name}}
	}
	return []IntentSource{{Kind: backend.UnattributedKind, Reason: ReasonNoMatchingEvent, Backend: backend.Name}}
}

// filterByEventName keeps only the events whose EventName is one of
// verbs — genesis attribution's own narrowing beyond AttributeDrift's
// identity-match alone: not just "an event referencing this resource,"
// but specifically one shaped like its own creation.
func filterByEventName(events []CloudTrailEvent, verbs []string) []CloudTrailEvent {
	want := map[string]bool{}
	for _, v := range verbs {
		want[v] = true
	}
	var out []CloudTrailEvent
	for _, e := range events {
		if want[e.EventName] {
			out = append(out, e)
		}
	}
	return out
}

// attributionSourcesOldestFirst is attributionSources' own genesis
// counterpart: identical shape, sorted oldest-first instead of
// newest-first (the actual moment of creation, per this function's own
// doc comment on AttributeGenesis).
func attributionSourcesOldestFirst(matched []CloudTrailEvent, kind string) []IntentSource {
	sort.Slice(matched, func(i, j int) bool {
		if !matched[i].EventTime.Equal(matched[j].EventTime) {
			return matched[i].EventTime.Before(matched[j].EventTime)
		}
		return matched[i].EventID < matched[j].EventID // deterministic tiebreak
	})
	sources := make([]IntentSource, 0, len(matched))
	for _, e := range matched {
		sources = append(sources, IntentSource{
			Kind:           kind,
			Ref:            e.EventID,
			ContentHash:    "sha256:" + sha256Hex(e.RawRecord),
			EventID:        e.EventID,
			EventName:      e.EventName,
			EventTime:      e.EventTime.UTC().Format(time.RFC3339),
			ActorARN:       e.ActorARN,
			SourceIP:       e.SourceIP,
			SessionContext: e.SessionContext,
		})
	}
	return sources
}
