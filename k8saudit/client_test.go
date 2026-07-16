package k8saudit

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

func TestLogGroupForCluster(t *testing.T) {
	got := LogGroupForCluster("my-cluster")
	want := "/aws/eks/my-cluster/cluster"
	if got != want {
		t.Fatalf("LogGroupForCluster(%q) = %q, want %q", "my-cluster", got, want)
	}
}

func TestParseEvent_NamespacedObject(t *testing.T) {
	msg := `{
		"auditID": "abc-123",
		"verb": "update",
		"user": {"username": "alice@example.com"},
		"sourceIPs": ["10.0.0.5"],
		"objectRef": {"resource": "secrets", "namespace": "default", "name": "my-secret", "uid": "uid-1"},
		"requestReceivedTimestamp": "2026-07-17T10:00:00.123456Z"
	}`
	e := types.FilteredLogEvent{Message: aws.String(msg)}

	ev, ok := parseEvent(e)
	if !ok {
		t.Fatal("expected parseEvent to succeed")
	}
	if ev.EventID != "abc-123" {
		t.Errorf("EventID = %q, want abc-123", ev.EventID)
	}
	if ev.EventName != "update" {
		t.Errorf("EventName = %q, want update", ev.EventName)
	}
	if ev.ActorARN != "alice@example.com" {
		t.Errorf("ActorARN = %q, want alice@example.com", ev.ActorARN)
	}
	if ev.SourceIP != "10.0.0.5" {
		t.Errorf("SourceIP = %q, want 10.0.0.5", ev.SourceIP)
	}
	wantTime := time.Date(2026, 7, 17, 10, 0, 0, 123456000, time.UTC)
	if !ev.EventTime.Equal(wantTime) {
		t.Errorf("EventTime = %v, want %v", ev.EventTime, wantTime)
	}
	// Every plausible identity shape must be offered (the defensive
	// mitigation for the unverified id-format gap, docs/architecture.md).
	wantResources := map[string]bool{"my-secret": true, "default/my-secret": true, "uid-1": true}
	if len(ev.Resources) != len(wantResources) {
		t.Fatalf("Resources = %v, want exactly %v", ev.Resources, wantResources)
	}
	for _, r := range ev.Resources {
		if !wantResources[r] {
			t.Errorf("unexpected resource candidate %q", r)
		}
	}
}

func TestParseEvent_ClusterScopedObject_NoNamespaceCandidate(t *testing.T) {
	msg := `{
		"auditID": "abc-456",
		"verb": "create",
		"user": {"username": "bob@example.com"},
		"objectRef": {"resource": "namespaces", "name": "prod", "uid": "uid-2"},
		"requestReceivedTimestamp": "2026-07-17T10:00:00Z"
	}`
	e := types.FilteredLogEvent{Message: aws.String(msg)}

	ev, ok := parseEvent(e)
	if !ok {
		t.Fatal("expected parseEvent to succeed")
	}
	for _, r := range ev.Resources {
		if r == "/prod" || r == "prod/prod" {
			t.Fatalf("cluster-scoped object must not produce a namespace/name candidate, got %v", ev.Resources)
		}
	}
	wantResources := map[string]bool{"prod": true, "uid-2": true}
	if len(ev.Resources) != len(wantResources) {
		t.Fatalf("Resources = %v, want exactly %v", ev.Resources, wantResources)
	}
}

func TestParseEvent_FallsBackToIngestionTimestampOnBadRequestTimestamp(t *testing.T) {
	msg := `{"auditID": "x", "verb": "get", "objectRef": {"name": "n"}, "requestReceivedTimestamp": "not-a-timestamp"}`
	fallback := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e := types.FilteredLogEvent{Message: aws.String(msg), Timestamp: aws.Int64(fallback.UnixMilli())}

	ev, ok := parseEvent(e)
	if !ok {
		t.Fatal("expected parseEvent to succeed despite a bad requestReceivedTimestamp")
	}
	if !ev.EventTime.Equal(fallback) {
		t.Fatalf("EventTime = %v, want fallback %v", ev.EventTime, fallback)
	}
}

func TestParseEvent_EmptyMessageIsUnparseable(t *testing.T) {
	if _, ok := parseEvent(types.FilteredLogEvent{}); ok {
		t.Fatal("expected an empty Message to be unparseable")
	}
}

func TestParseEvent_MalformedJSONIsUnparseable(t *testing.T) {
	e := types.FilteredLogEvent{Message: aws.String("not json")}
	if _, ok := parseEvent(e); ok {
		t.Fatal("expected malformed JSON to be unparseable")
	}
}

func TestParseEvent_RawRecordIsTheOriginalMessage(t *testing.T) {
	msg := `{"auditID": "x", "verb": "get", "objectRef": {"name": "n"}, "requestReceivedTimestamp": "2026-07-17T10:00:00Z"}`
	e := types.FilteredLogEvent{Message: aws.String(msg)}
	ev, ok := parseEvent(e)
	if !ok {
		t.Fatal("expected parseEvent to succeed")
	}
	if string(ev.RawRecord) != msg {
		t.Fatalf("RawRecord = %q, want %q", ev.RawRecord, msg)
	}
}
