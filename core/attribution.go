package core

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

// CloudTrailEvent is core's own minimal, provider-agnostic view of one
// CloudTrail management event (UBI-10) — core deliberately doesn't import
// an AWS SDK, the same dependency-inversion discipline as StateReader for
// the tfplugin provider client (UBI-7 follow-up): the cloudtrail package
// translates the real AWS SDK's response into this plain struct at the
// boundary.
type CloudTrailEvent struct {
	EventID        string
	EventName      string
	EventTime      time.Time
	ActorARN       string
	SourceIP       string
	SessionContext json.RawMessage // raw userIdentity.sessionContext, opaque; nil if absent (e.g. a long-lived IAM user, not an assumed role)
	RawRecord      []byte          // the full CloudTrailEvent JSON record, for ContentHash

	// Resources is every ResourceName/ARN CloudTrail associated with this
	// event. AttributeDrift's own defensive exact-match check reads this —
	// it never trusts a LookupEvents call's own filtering alone to have
	// matched the right resource (see the "different resource, similar
	// name" case this guards against).
	Resources []string
}

// EventLookup is core's own minimal view of "something that can search
// CloudTrail for management events referencing one resource identity value
// in a time window" — mirrors StateReader's inversion: core doesn't import
// an AWS SDK; the cloudtrail package's Client implements this for real,
// cli adapts it at the call site, tests use a fake directly (see
// attribution_test.go).
type EventLookup interface {
	LookupEvents(ctx context.Context, resourceID string, since, until time.Time) ([]CloudTrailEvent, error)
}

// Reasons for IntentSource.Reason on a "cloudtrail_unattributed"/
// "audit_unattributed" source — see docs/schema.md's CloudTrail
// attribution amendment (and its UBI-21 audit_unattributed amendment) for
// what each one means and why they're distinct.
const (
	ReasonNoMatchingEvent = "no_matching_event"
	ReasonDeliveryWindow  = "delivery_window"
	ReasonNotLogged       = "not_logged"

	// ReasonNotConfigured means the backend itself needs operator
	// configuration ubx has no way to derive on its own (UBI-22: which
	// EKS cluster/log group, unlike AWS's region or GCP's project, isn't
	// implied by anything already on hand) and that configuration is
	// absent. Distinct from ReasonNotLogged (attempted and denied/failed)
	// -- an operator reading `ubx why` should be able to tell "I haven't
	// set this up" apart from "I set it up and it's failing."
	ReasonNotConfigured = "not_configured"
)

// AttributionBackend names which platform-specific audit-log backend
// AttributeDrift is running against, and the handful of things that
// genuinely differ per platform (UBI-21, docs/architecture.md — GCP
// support): which IntentSource kinds to emit, and how long that
// backend's own event delivery can lag. core stays entirely
// backend-agnostic — cloudtrail/ and gcpaudit/ each own their own
// AttributionBackend value (cloudtrail.Backend, gcpaudit.Backend) rather
// than core hardcoding either platform's specifics.
type AttributionBackend struct {
	// Name identifies this backend for IntentSource.Backend on an
	// "audit_unattributed" source. Left empty for cloudtrail.Backend
	// deliberately — it predates this field and keeps emitting the
	// original "cloudtrail_unattributed" kind (UnattributedKind below),
	// which carries no Backend at all; only a newer backend needs the
	// field to disambiguate.
	Name string

	// SuccessKind is the IntentSource.Kind for a matched event —
	// "cloudtrail" for cloudtrail.Backend, "gcp_audit" for
	// gcpaudit.Backend.
	SuccessKind string

	// UnattributedKind is the IntentSource.Kind for a failed attribution
	// attempt — "cloudtrail_unattributed" for cloudtrail.Backend
	// (unchanged since UBI-10, no Backend field ever set), the
	// generalized "audit_unattributed" (Backend == Name) for every other
	// backend.
	UnattributedKind string

	// DeliveryLag is this backend's own documented/measured event
	// delivery latency (CloudTrail's is ~15 minutes, measured against a
	// real account in UBI-10) — a correlation window narrower than this
	// can't rule out "the event just hasn't propagated yet" the way a
	// wider window that still finds nothing can. Never assumed to
	// transfer from one backend to another without its own measurement.
	DeliveryLag time.Duration
}

// AttributeDrift attempts audit-log attribution (via backend) for a drift
// that's already been detected and recorded, returning the IntentSource
// entries to append to that drift_adopt proposal's Intent.Sources — one
// backend.SuccessKind source per matched event (newest event_time first;
// never guessing a single cause when several real events could be
// responsible), or exactly one backend.UnattributedKind source recording
// why attribution didn't produce a match.
//
// Best-effort by construction (docs/schema.md's amendment, UBI-10): this
// never returns an error. Any failure to attribute — denied credentials,
// no matching event, too recent for the backend to have delivered it yet
// — is itself recorded as evidence, not surfaced as a caller-visible
// failure that could block generating or accepting the underlying
// proposal. Callers that don't want attribution at all (fake-only
// conformance tests, environments with no cloud credentials) simply don't
// call this — GenerateProposal never requires it, and the proposal it
// already built is perfectly valid without it.
//
// Which value(s) to search by come from identityCandidates, derived from
// the resource's own Observed state (id/arn/name) rather than a static
// per-type table: which attribute a backend's own resource-matching
// actually recognizes varies by service (and by platform — see
// gcpaudit/client.go's own doc comment on GCP's scheme-less resource
// paths), and was confirmed empirically (not assumed) to be "id" for
// aws_s3_bucket/aws_iam_role/aws_vpc — searching by the full ARN returned
// nothing at all even for genuinely matching, real events (see STATE.md's
// UBI-10 surprise entry). id is tried first for that reason, but arn/name
// are still tried as fallbacks rather than trusting one field to
// generalize to every type.
func AttributeDrift(ctx context.Context, lookup EventLookup, addr Address, observed json.RawMessage, since, until time.Time, backend AttributionBackend) []IntentSource {
	candidates := identityCandidates(observed, addr.Name)

	for _, candidate := range candidates {
		events, err := lookup.LookupEvents(ctx, candidate, since, until)
		if err != nil {
			// The API call itself failed -- retrying with a different
			// candidate value won't change that (same credentials, same
			// call). We have no visibility into whether an event exists
			// at all, as opposed to having searched and found nothing.
			return []IntentSource{{Kind: backend.UnattributedKind, Reason: ReasonNotLogged, Backend: backend.Name}}
		}
		if matched := filterExactMatch(events, candidates); len(matched) > 0 {
			return attributionSources(matched, backend.SuccessKind)
		}
	}

	if until.Sub(since) < backend.DeliveryLag {
		return []IntentSource{{Kind: backend.UnattributedKind, Reason: ReasonDeliveryWindow, Backend: backend.Name}}
	}
	return []IntentSource{{Kind: backend.UnattributedKind, Reason: ReasonNoMatchingEvent, Backend: backend.Name}}
}

// identityCandidates returns the resource's own observed identity values to
// search CloudTrail by, in the order to try them: "id", then "arn", then
// "name" if present and not already tried (duplicates and empty values are
// dropped). fallback (addr.Name) is appended last as a final attempt if
// none of the observed attributes yield anything — Observed should always
// have an "id" in practice, but this keeps AttributeDrift from having
// nothing at all to search by in a malformed/partial observation.
func identityCandidates(observed json.RawMessage, fallback string) []string {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(observed, &m) // best-effort; a decode failure just means no candidates from Observed

	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, key := range []string{"id", "arn", "name"} {
		if raw, ok := m[key]; ok {
			var s string
			if json.Unmarshal(raw, &s) == nil {
				add(s)
			}
		}
	}
	add(fallback)
	return out
}

// filterExactMatch keeps only the events whose Resources list contains at
// least one of candidates, exactly. This is the defense against "an event
// for a different resource with a similar name" — CloudTrail's own
// ResourceName lookup attribute is trusted to filter server-side, but
// AttributeDrift never assumes that alone is sufficient (nor does it trust
// a test fake's fabricated response without checking); only an event that
// genuinely references one of this resource's own known identity values
// counts as a match.
func filterExactMatch(events []CloudTrailEvent, candidates []string) []CloudTrailEvent {
	want := map[string]bool{}
	for _, c := range candidates {
		want[c] = true
	}
	var out []CloudTrailEvent
	for _, e := range events {
		for _, r := range e.Resources {
			if want[r] {
				out = append(out, e)
				break
			}
		}
	}
	return out
}

// attributionSources converts matched events into IntentSource entries,
// newest event_time first — docs/schema.md's amendment: "ubx never guesses
// which of several candidate events caused the drift." kind is the
// backend-specific success kind (backend.SuccessKind).
func attributionSources(matched []CloudTrailEvent, kind string) []IntentSource {
	sort.Slice(matched, func(i, j int) bool {
		if !matched[i].EventTime.Equal(matched[j].EventTime) {
			return matched[i].EventTime.After(matched[j].EventTime)
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
