package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// fakeEventLookup drives AttributeDrift's adversarial paths without any
// real AWS dependency. byResourceID maps a searched identity value to the
// events LookupEvents should return for it; a missing key means "no
// events for this candidate" (not an error).
type fakeEventLookup struct {
	byResourceID map[string][]CloudTrailEvent
	err          error
	calls        []string // records every resourceID searched, in order
}

func (f *fakeEventLookup) LookupEvents(_ context.Context, resourceID string, _, _ time.Time) ([]CloudTrailEvent, error) {
	f.calls = append(f.calls, resourceID)
	if f.err != nil {
		return nil, f.err
	}
	return f.byResourceID[resourceID], nil
}

// testAWSBackend mirrors cloudtrail.Backend's own value without importing
// package cloudtrail (which imports core -- an import cycle). Every
// existing test in this file is AWS-shaped, so this is the backend value
// they all pass; UBI-21's own new tests below pass a GCP-shaped one
// instead to exercise the generalized kind/backend-field behavior.
var testAWSBackend = AttributionBackend{
	SuccessKind:      "cloudtrail",
	UnattributedKind: "cloudtrail_unattributed",
	DeliveryLag:      15 * time.Minute,
}

// testGCPBackend mirrors gcp.Backend's own value, same reasoning as
// testAWSBackend above.
var testGCPBackend = AttributionBackend{
	Name:             "gcp_audit_logs",
	SuccessKind:      "gcp_audit",
	UnattributedKind: "audit_unattributed",
	DeliveryLag:      3 * time.Minute,
}

func wideWindow() (time.Time, time.Time) {
	until := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	since := until.Add(-24 * time.Hour)
	return since, until
}

func narrowWindow() (time.Time, time.Time) {
	until := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	since := until.Add(-1 * time.Minute)
	return since, until
}

func TestAttributeDrift_SingleMatch(t *testing.T) {
	observed := json.RawMessage(`{"id":"ubx-states","arn":"arn:aws:s3:::ubx-states"}`)
	evt := CloudTrailEvent{
		EventID:   "evt-1",
		EventName: "PutBucketTagging",
		EventTime: time.Date(2026, 7, 10, 11, 0, 0, 0, time.UTC),
		ActorARN:  "arn:aws:iam::839333509514:user/roozbeh",
		SourceIP:  "93.228.76.41",
		RawRecord: []byte(`{"eventID":"evt-1"}`),
		Resources: []string{"ubx-states"},
	}
	lookup := &fakeEventLookup{byResourceID: map[string][]CloudTrailEvent{"ubx-states": {evt}}}
	since, until := wideWindow()

	sources := AttributeDrift(context.Background(), lookup, Address{Stack: "s", Type: "aws_s3_bucket", Name: "ubx-states"}, observed, since, until, testAWSBackend)

	if len(sources) != 1 {
		t.Fatalf("len(sources) = %d, want 1", len(sources))
	}
	s := sources[0]
	if s.Kind != "cloudtrail" {
		t.Errorf("Kind = %q, want cloudtrail", s.Kind)
	}
	if s.EventID != "evt-1" || s.Ref != "evt-1" {
		t.Errorf("EventID/Ref = %q/%q, want evt-1/evt-1", s.EventID, s.Ref)
	}
	if s.ActorARN != "arn:aws:iam::839333509514:user/roozbeh" {
		t.Errorf("ActorARN = %q", s.ActorARN)
	}
	if s.EventTime != "2026-07-10T11:00:00Z" {
		t.Errorf("EventTime = %q", s.EventTime)
	}
	wantHash := "sha256:" + sha256Hex(evt.RawRecord)
	if s.ContentHash != wantHash {
		t.Errorf("ContentHash = %q, want %q", s.ContentHash, wantHash)
	}
	// Searched by "id" first and found a match there -- never needed to
	// try "arn".
	if got := lookup.calls; len(got) != 1 || got[0] != "ubx-states" {
		t.Errorf("calls = %v, want exactly [ubx-states]", got)
	}
}

func TestAttributeDrift_MultipleMatches_NewestFirst(t *testing.T) {
	observed := json.RawMessage(`{"id":"my-role","name":"my-role"}`)
	older := CloudTrailEvent{
		EventID: "evt-old", EventName: "UntagRole",
		EventTime: time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC),
		RawRecord: []byte(`{}`), Resources: []string{"my-role"},
	}
	newer := CloudTrailEvent{
		EventID: "evt-new", EventName: "TagRole",
		EventTime: time.Date(2026, 7, 10, 11, 0, 0, 0, time.UTC),
		RawRecord: []byte(`{}`), Resources: []string{"my-role"},
	}
	lookup := &fakeEventLookup{byResourceID: map[string][]CloudTrailEvent{
		"my-role": {older, newer}, // deliberately out of order
	}}
	since, until := wideWindow()

	sources := AttributeDrift(context.Background(), lookup, Address{Stack: "s", Type: "aws_iam_role", Name: "my-role"}, observed, since, until, testAWSBackend)

	if len(sources) != 2 {
		t.Fatalf("len(sources) = %d, want 2 (both events attached, never guessing one)", len(sources))
	}
	if sources[0].EventID != "evt-new" || sources[1].EventID != "evt-old" {
		t.Errorf("order = [%s, %s], want [evt-new, evt-old] (newest first)", sources[0].EventID, sources[1].EventID)
	}
}

func TestAttributeDrift_NoMatchingEvent(t *testing.T) {
	observed := json.RawMessage(`{"id":"my-queue"}`)
	lookup := &fakeEventLookup{byResourceID: map[string][]CloudTrailEvent{}}
	since, until := wideWindow()

	sources := AttributeDrift(context.Background(), lookup, Address{Stack: "s", Type: "aws_sqs_queue", Name: "my-queue"}, observed, since, until, testAWSBackend)

	if len(sources) != 1 || sources[0].Kind != "cloudtrail_unattributed" || sources[0].Reason != ReasonNoMatchingEvent {
		t.Fatalf("sources = %+v, want one cloudtrail_unattributed/no_matching_event", sources)
	}
}

func TestAttributeDrift_DeliveryWindow(t *testing.T) {
	observed := json.RawMessage(`{"id":"my-queue"}`)
	lookup := &fakeEventLookup{byResourceID: map[string][]CloudTrailEvent{}}
	since, until := narrowWindow() // < cloudTrailDeliveryLag

	sources := AttributeDrift(context.Background(), lookup, Address{Stack: "s", Type: "aws_sqs_queue", Name: "my-queue"}, observed, since, until, testAWSBackend)

	if len(sources) != 1 || sources[0].Kind != "cloudtrail_unattributed" || sources[0].Reason != ReasonDeliveryWindow {
		t.Fatalf("sources = %+v, want one cloudtrail_unattributed/delivery_window", sources)
	}
}

func TestAttributeDrift_NotLogged_APIError(t *testing.T) {
	observed := json.RawMessage(`{"id":"my-bucket"}`)
	lookup := &fakeEventLookup{err: errors.New("AccessDenied: not authorized to perform cloudtrail:LookupEvents")}
	since, until := wideWindow()

	sources := AttributeDrift(context.Background(), lookup, Address{Stack: "s", Type: "aws_s3_bucket", Name: "my-bucket"}, observed, since, until, testAWSBackend)

	if len(sources) != 1 || sources[0].Kind != "cloudtrail_unattributed" || sources[0].Reason != ReasonNotLogged {
		t.Fatalf("sources = %+v, want one cloudtrail_unattributed/not_logged", sources)
	}
}

func TestAttributeDrift_NotLogged_NoTrailVisibility(t *testing.T) {
	// A distinct failure mode from IAM-denied access above (e.g. CloudTrail
	// event history disabled in this account/region) -- same reason
	// category either way: ubx has no visibility into whether an event
	// exists, as opposed to having searched and found nothing.
	observed := json.RawMessage(`{"id":"my-bucket"}`)
	lookup := &fakeEventLookup{err: errors.New("cloudtrail: event history is not enabled for this region")}
	since, until := wideWindow()

	sources := AttributeDrift(context.Background(), lookup, Address{Stack: "s", Type: "aws_s3_bucket", Name: "my-bucket"}, observed, since, until, testAWSBackend)

	if len(sources) != 1 || sources[0].Kind != "cloudtrail_unattributed" || sources[0].Reason != ReasonNotLogged {
		t.Fatalf("sources = %+v, want one cloudtrail_unattributed/not_logged", sources)
	}
}

func TestAttributeDrift_SimilarNameDifferentResource_NotAttached(t *testing.T) {
	// The lookup returns an event, but it references a different bucket
	// that merely happens to share a prefix -- AttributeDrift must not
	// attach it just because the (fake, possibly-fuzzy) lookup handed it
	// back.
	observed := json.RawMessage(`{"id":"ubx-states"}`)
	decoy := CloudTrailEvent{
		EventID: "evt-decoy", EventName: "PutBucketTagging",
		EventTime: time.Date(2026, 7, 10, 11, 0, 0, 0, time.UTC),
		RawRecord: []byte(`{}`), Resources: []string{"ubx-states-2"}, // similar name, not an exact match
	}
	lookup := &fakeEventLookup{byResourceID: map[string][]CloudTrailEvent{"ubx-states": {decoy}}}
	since, until := wideWindow()

	sources := AttributeDrift(context.Background(), lookup, Address{Stack: "s", Type: "aws_s3_bucket", Name: "ubx-states"}, observed, since, until, testAWSBackend)

	if len(sources) != 1 || sources[0].Kind != "cloudtrail_unattributed" || sources[0].Reason != ReasonNoMatchingEvent {
		t.Fatalf("sources = %+v, want no_matching_event -- the decoy event must not be attached", sources)
	}
}

func TestAttributeDrift_FallsBackFromIDToARN(t *testing.T) {
	// "id" is tried first (see identityCandidates' doc comment on why),
	// but if it turns up nothing, "arn" is still tried before giving up --
	// this simulates a hypothetical type where CloudTrail's ResourceName
	// only recognizes the ARN, the opposite of aws_s3_bucket/aws_iam_role/
	// aws_vpc's empirically-confirmed behavior.
	observed := json.RawMessage(`{"id":"fn-name","arn":"arn:aws:lambda:us-east-1:1:function:fn-name"}`)
	evt := CloudTrailEvent{
		EventID: "evt-arn", EventName: "UpdateFunctionConfiguration20150331",
		EventTime: time.Date(2026, 7, 10, 11, 0, 0, 0, time.UTC),
		RawRecord: []byte(`{}`), Resources: []string{"arn:aws:lambda:us-east-1:1:function:fn-name"},
	}
	lookup := &fakeEventLookup{byResourceID: map[string][]CloudTrailEvent{
		// nothing under "fn-name"; only under the ARN
		"arn:aws:lambda:us-east-1:1:function:fn-name": {evt},
	}}
	since, until := wideWindow()

	sources := AttributeDrift(context.Background(), lookup, Address{Stack: "s", Type: "aws_lambda_function", Name: "fn-name"}, observed, since, until, testAWSBackend)

	if len(sources) != 1 || sources[0].Kind != "cloudtrail" || sources[0].EventID != "evt-arn" {
		t.Fatalf("sources = %+v, want the ARN-matched event attached", sources)
	}
	if got := lookup.calls; len(got) != 2 || got[0] != "fn-name" || got[1] != "arn:aws:lambda:us-east-1:1:function:fn-name" {
		t.Errorf("calls = %v, want [fn-name, then the ARN] -- id tried first, arn as fallback", got)
	}
}

// TestAttributeDrift_GCPBackend_SingleMatch is UBI-21's own coverage of
// the generalized backend parameter: a matched event under
// testGCPBackend must emit "gcp_audit" (not "cloudtrail"), reusing
// ActorARN for the GCP principal email (see audit/gcp/client.go's own
// doc comment on why).
func TestAttributeDrift_GCPBackend_SingleMatch(t *testing.T) {
	observed := json.RawMessage(`{"id":"projects/p/topics/t"}`)
	evt := CloudTrailEvent{
		EventID:   "insert-id-1",
		EventName: "google.pubsub.v1.Publisher.UpdateTopic",
		EventTime: time.Date(2026, 7, 10, 11, 0, 0, 0, time.UTC),
		ActorARN:  "someone@example.com",
		RawRecord: []byte(`{}`),
		Resources: []string{"projects/p/topics/t"},
	}
	lookup := &fakeEventLookup{byResourceID: map[string][]CloudTrailEvent{"projects/p/topics/t": {evt}}}
	since, until := wideWindow()

	sources := AttributeDrift(context.Background(), lookup, Address{Stack: "s", Type: "google_pubsub_topic", Name: "t"}, observed, since, until, testGCPBackend)

	if len(sources) != 1 {
		t.Fatalf("len(sources) = %d, want 1", len(sources))
	}
	s := sources[0]
	if s.Kind != "gcp_audit" {
		t.Errorf("Kind = %q, want gcp_audit", s.Kind)
	}
	if s.ActorARN != "someone@example.com" {
		t.Errorf("ActorARN = %q, want the GCP principal email", s.ActorARN)
	}
	if s.Backend != "" {
		t.Errorf("Backend = %q, want empty -- Backend is only set on the unattributed kind", s.Backend)
	}
}

// TestAttributeDrift_GCPBackend_Unattributed_CarriesBackendField confirms
// the generalized "audit_unattributed" kind carries Backend ==
// testGCPBackend.Name, unlike "cloudtrail_unattributed" (which never
// carries one).
func TestAttributeDrift_GCPBackend_Unattributed_CarriesBackendField(t *testing.T) {
	observed := json.RawMessage(`{"id":"projects/p/topics/t"}`)
	lookup := &fakeEventLookup{byResourceID: map[string][]CloudTrailEvent{}}
	since, until := wideWindow()

	sources := AttributeDrift(context.Background(), lookup, Address{Stack: "s", Type: "google_pubsub_topic", Name: "t"}, observed, since, until, testGCPBackend)

	if len(sources) != 1 {
		t.Fatalf("len(sources) = %d, want 1", len(sources))
	}
	s := sources[0]
	if s.Kind != "audit_unattributed" {
		t.Errorf("Kind = %q, want audit_unattributed", s.Kind)
	}
	if s.Backend != "gcp_audit_logs" {
		t.Errorf("Backend = %q, want gcp_audit_logs", s.Backend)
	}
	if s.Reason != ReasonNoMatchingEvent {
		t.Errorf("Reason = %q, want %q", s.Reason, ReasonNoMatchingEvent)
	}
}

// TestAttributeDrift_AWSBackend_Unattributed_NeverCarriesBackendField is
// the back-compat guarantee docs/schema.md's audit_unattributed amendment
// makes explicit: cloudtrail_unattributed sources never gain a Backend
// value, preserving every existing ledger entry's exact shape.
func TestAttributeDrift_AWSBackend_Unattributed_NeverCarriesBackendField(t *testing.T) {
	observed := json.RawMessage(`{"id":"my-queue"}`)
	lookup := &fakeEventLookup{byResourceID: map[string][]CloudTrailEvent{}}
	since, until := wideWindow()

	sources := AttributeDrift(context.Background(), lookup, Address{Stack: "s", Type: "aws_sqs_queue", Name: "my-queue"}, observed, since, until, testAWSBackend)

	if len(sources) != 1 || sources[0].Kind != "cloudtrail_unattributed" {
		t.Fatalf("sources = %+v, want one cloudtrail_unattributed", sources)
	}
	if sources[0].Backend != "" {
		t.Errorf("Backend = %q, want empty for cloudtrail_unattributed", sources[0].Backend)
	}
}

func TestIdentityCandidates(t *testing.T) {
	tests := []struct {
		name     string
		observed json.RawMessage
		fallback string
		want     []string
	}{
		{"id and arn both present", json.RawMessage(`{"id":"i","arn":"a"}`), "fallback", []string{"i", "a", "fallback"}},
		{"id equals arn, deduped", json.RawMessage(`{"id":"same","arn":"same"}`), "fallback", []string{"same", "fallback"}},
		{"only arn present", json.RawMessage(`{"arn":"a"}`), "fallback", []string{"a", "fallback"}},
		{"nothing present, falls back", json.RawMessage(`{}`), "fallback", []string{"fallback"}},
		{"malformed observed, falls back", json.RawMessage(`not json`), "fallback", []string{"fallback"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := identityCandidates(tt.observed, tt.fallback)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}
