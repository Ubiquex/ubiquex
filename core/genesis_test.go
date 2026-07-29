package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestAttributeGenesis_MatchedCreationEvent(t *testing.T) {
	observed := json.RawMessage(`{"id": "my-queue-id"}`)
	lookup := &fakeEventLookup{byResourceID: map[string][]CloudTrailEvent{
		"my-queue-id": {
			{EventID: "e1", EventName: "TagQueue", EventTime: mustParseTime(t, "2026-01-01T00:00:00Z"), Resources: []string{"my-queue-id"}},
			{EventID: "e2", EventName: "CreateQueue", EventTime: mustParseTime(t, "2025-06-01T00:00:00Z"), ActorARN: "arn:aws:iam::1:user/alice", Resources: []string{"my-queue-id"}},
		},
	}}
	since := mustParseTime(t, "2025-01-01T00:00:00Z")
	until := mustParseTime(t, "2026-02-01T00:00:00Z")

	sources := AttributeGenesis(context.Background(), lookup, Address{Stack: "payments", Type: "aws_sqs_queue", Name: "q"}, observed, since, until, testAWSBackend, []string{"CreateQueue"})
	if len(sources) != 1 {
		t.Fatalf("got %d sources, want 1 (only the creation-verb event, not TagQueue), got %+v", len(sources), sources)
	}
	if sources[0].Kind != "cloudtrail" || sources[0].EventName != "CreateQueue" || sources[0].ActorARN != "arn:aws:iam::1:user/alice" {
		t.Fatalf("source = %+v, want the CreateQueue event attributed to alice", sources[0])
	}
}

// TestAttributeGenesis_MultipleCreationEvents_OldestWins is the real
// difference from AttributeDrift's own "newest first" convention: a
// resource is created exactly once, so among genuinely matched
// creation-verb events, the OLDEST is the one that actually founded this
// resource's own lineage.
func TestAttributeGenesis_MultipleCreationEvents_OldestWins(t *testing.T) {
	observed := json.RawMessage(`{"id": "my-queue-id"}`)
	lookup := &fakeEventLookup{byResourceID: map[string][]CloudTrailEvent{
		"my-queue-id": {
			{EventID: "newer", EventName: "CreateQueue", EventTime: mustParseTime(t, "2026-01-01T00:00:00Z"), ActorARN: "arn:aws:iam::1:user/bob", Resources: []string{"my-queue-id"}},
			{EventID: "older", EventName: "CreateQueue", EventTime: mustParseTime(t, "2025-01-01T00:00:00Z"), ActorARN: "arn:aws:iam::1:user/alice", Resources: []string{"my-queue-id"}},
		},
	}}
	sources := AttributeGenesis(context.Background(), lookup, Address{Type: "aws_sqs_queue", Name: "q"}, observed,
		mustParseTime(t, "2024-01-01T00:00:00Z"), mustParseTime(t, "2026-06-01T00:00:00Z"), testAWSBackend, []string{"CreateQueue"})
	if len(sources) != 2 {
		t.Fatalf("expected both matches returned (never guessing a single cause), got %d", len(sources))
	}
	if sources[0].ActorARN != "arn:aws:iam::1:user/alice" || sources[0].EventID != "older" {
		t.Fatalf("sources[0] = %+v, want the OLDEST event first (alice's), not the newest", sources[0])
	}
	if sources[1].EventID != "newer" {
		t.Fatalf("sources[1] = %+v, want the newer event second", sources[1])
	}
}

func TestAttributeGenesis_NonCreationEventsIgnored_NoMatch(t *testing.T) {
	observed := json.RawMessage(`{"id": "my-queue-id"}`)
	lookup := &fakeEventLookup{byResourceID: map[string][]CloudTrailEvent{
		"my-queue-id": {
			{EventID: "e1", EventName: "TagQueue", EventTime: mustParseTime(t, "2026-01-01T00:00:00Z"), Resources: []string{"my-queue-id"}},
		},
	}}
	since := mustParseTime(t, "2025-01-01T00:00:00Z")
	until := mustParseTime(t, "2026-02-01T00:00:00Z")
	sources := AttributeGenesis(context.Background(), lookup, Address{Type: "aws_sqs_queue", Name: "q"}, observed, since, until, testAWSBackend, []string{"CreateQueue"})
	if len(sources) != 1 || sources[0].Kind != testAWSBackend.UnattributedKind || sources[0].Reason != ReasonNoMatchingEvent {
		t.Fatalf("expected an unattributed/no_matching_event outcome (TagQueue isn't a creation verb), got %+v", sources)
	}
}

func TestAttributeGenesis_EmptyCreationVerbs_NeverEvenSearches(t *testing.T) {
	lookup := &fakeEventLookup{byResourceID: map[string][]CloudTrailEvent{}}
	sources := AttributeGenesis(context.Background(), lookup, Address{Type: "aws_dynamodb_table", Name: "t"}, json.RawMessage(`{"id":"x"}`),
		time.Now().Add(-time.Hour), time.Now(), testAWSBackend, nil)
	if len(sources) != 1 || sources[0].Reason != ReasonNoCreationVerbs {
		t.Fatalf("expected ReasonNoCreationVerbs when the caller has no known creation-verb table, got %+v", sources)
	}
	if len(lookup.calls) != 0 {
		t.Fatalf("expected LookupEvents never to be called at all when creationVerbs is empty, got %d calls", len(lookup.calls))
	}
}

func TestAttributeGenesis_LookupError_NotLogged(t *testing.T) {
	lookup := &fakeEventLookup{err: errors.New("AccessDenied: not authorized to perform cloudtrail:LookupEvents")}
	sources := AttributeGenesis(context.Background(), lookup, Address{Type: "aws_sqs_queue", Name: "q"}, json.RawMessage(`{"id":"x"}`),
		time.Now().Add(-time.Hour), time.Now(), testAWSBackend, []string{"CreateQueue"})
	if len(sources) != 1 || sources[0].Reason != ReasonNotLogged {
		t.Fatalf("expected ReasonNotLogged on a lookup error, got %+v", sources)
	}
}

func TestAttributeGenesis_WithinDeliveryLag_DeliveryWindow(t *testing.T) {
	lookup := &fakeEventLookup{byResourceID: map[string][]CloudTrailEvent{}}
	now := mustParseTime(t, "2026-01-01T00:10:00Z")
	since := now.Add(-5 * time.Minute) // narrower than testAWSBackend's own 15-minute DeliveryLag
	sources := AttributeGenesis(context.Background(), lookup, Address{Type: "aws_sqs_queue", Name: "q"}, json.RawMessage(`{"id":"x"}`),
		since, now, testAWSBackend, []string{"CreateQueue"})
	if len(sources) != 1 || sources[0].Reason != ReasonDeliveryWindow {
		t.Fatalf("expected ReasonDeliveryWindow when the search window is narrower than the backend's own delivery lag, got %+v", sources)
	}
}

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}
