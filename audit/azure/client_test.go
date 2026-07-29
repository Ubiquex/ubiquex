package azure

import (
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
)

func TestResourceGroupFromID(t *testing.T) {
	cases := []struct {
		id     string
		wantRG string
		wantOK bool
	}{
		{
			id:     "/subscriptions/315b4ae0-7c86-41f1-b0a0-ac735e8b4683/resourceGroups/ubx-conformance",
			wantRG: "ubx-conformance", wantOK: true,
		},
		{
			id:     "/subscriptions/315b4ae0-7c86-41f1-b0a0-ac735e8b4683/resourceGroups/ubx-conformance/providers/Microsoft.Storage/storageAccounts/ubxsa1",
			wantRG: "ubx-conformance", wantOK: true,
		},
		{
			// Real-world casing (UBI-37 Stage 2's own finding): Activity
			// Log's own resourceId comes back lowercase; this must still
			// extract correctly.
			id:     "/subscriptions/315b4ae0-7c86-41f1-b0a0-ac735e8b4683/resourcegroups/ubx-conformance",
			wantRG: "ubx-conformance", wantOK: true,
		},
		{
			id:     "some-bare-name",
			wantRG: "", wantOK: false,
		},
		{
			id:     "",
			wantRG: "", wantOK: false,
		},
	}
	for _, c := range cases {
		rg, ok := resourceGroupFromID(c.id)
		if rg != c.wantRG || ok != c.wantOK {
			t.Errorf("resourceGroupFromID(%q) = (%q, %v), want (%q, %v)", c.id, rg, ok, c.wantRG, c.wantOK)
		}
	}
}

func strPtr(s string) *string { return &s }

func TestParseEvent(t *testing.T) {
	ts := time.Date(2026, 7, 29, 17, 38, 53, 0, time.UTC)
	e := &armmonitor.EventData{
		EventDataID:    strPtr("57fdfc27-8c5d-40ec-9142-0a39fef00151"),
		EventTimestamp: &ts,
		Caller:         strPtr("me@roozbeh.net"),
		ResourceID:     strPtr("/subscriptions/sub/resourcegroups/ubx-conformance"), // real, lowercase server casing
		OperationName:  &armmonitor.LocalizableString{Value: strPtr("Microsoft.Resources/subscriptions/resourcegroups/write"), LocalizedValue: strPtr("Update resource group")},
		HTTPRequest:    &armmonitor.HTTPRequestInfo{ClientIPAddress: strPtr("203.0.113.5")},
	}

	// matchedCandidate is the CALLER's own original casing -- LookupEvents
	// already confirmed a case-insensitive match before calling parseEvent.
	matchedCandidate := "/subscriptions/sub/resourceGroups/ubx-conformance"
	got, ok := parseEvent(e, matchedCandidate)
	if !ok {
		t.Fatal("parseEvent returned ok=false for a well-formed event")
	}
	if got.EventID != "57fdfc27-8c5d-40ec-9142-0a39fef00151" {
		t.Errorf("EventID = %q", got.EventID)
	}
	if got.ActorARN != "me@roozbeh.net" {
		t.Errorf("ActorARN = %q, want the real caller email", got.ActorARN)
	}
	if got.EventName != "Update resource group" {
		t.Errorf("EventName = %q, want the localized OperationName", got.EventName)
	}
	if got.SourceIP != "203.0.113.5" {
		t.Errorf("SourceIP = %q", got.SourceIP)
	}
	if !got.EventTime.Equal(ts) {
		t.Errorf("EventTime = %v, want %v", got.EventTime, ts)
	}
	if len(got.Resources) != 1 || got.Resources[0] != matchedCandidate {
		t.Errorf("Resources = %v, want [%q] (the caller's own original casing, not the server's)", got.Resources, matchedCandidate)
	}
}

func TestParseEvent_NoEventDataID_NotOK(t *testing.T) {
	e := &armmonitor.EventData{}
	if _, ok := parseEvent(e, "whatever"); ok {
		t.Fatal("parseEvent should return ok=false for an entry with no EventDataID")
	}
}
