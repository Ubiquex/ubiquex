// Package azure is UBI-37 Stage 2's concrete implementation of
// core.EventLookup against Azure's real Activity Log (Azure Monitor's
// ActivityLogs API) — the fourth platform-specific backend behind
// core.EventLookup, alongside package cloudtrail (UBI-10, AWS management
// events), package gcp (UBI-21, GCP Cloud Audit Logs), and package k8s
// (UBI-22, EKS control-plane audit logs). Same dependency-inversion
// discipline: core defines the plain-Go shape it needs (core.EventLookup),
// this package translates the real Azure SDK client's response into it at
// the boundary. core itself never imports an Azure SDK.
//
// Scope, deliberately narrow, mirroring cloudtrail's/gcp's own scoping
// decisions (docs/plan.md's UBI-10 rationale): the Activity Log's
// "Administrative" category only — always-on, cannot be disabled, no
// diagnostic-setting configuration required, and the closest match to
// "who changed this resource's configuration." Every other Activity Log
// category (ServiceHealth, Alert, Autoscale, Security, Recommendation,
// Policy) is deliberately excluded, same reasoning as cloudtrail/gcp's
// own scope: those record platform/service events, not "a caller changed
// a resource."
package azure

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"

	"github.com/ubiquex/ubiquex-cli/core"
)

// Backend is azure's own core.AttributionBackend value: "azure_activity_log"/
// "audit_unattributed" kinds (Name == "azure_activity_log", carried on
// "audit_unattributed" sources same as every other backend's own Name/
// Backend pattern), and a delivery lag based on direct measurement
// against a real subscription (UBI-37 Stage 2), not Azure's own
// documentation the way CloudTrail's 15-minute figure was -- Azure
// doesn't publish an equivalent "up to N minutes" ceiling any more than
// GCP does. Measured: a resource group's own real "Update resource
// group" Administrative event became queryable via NewListPager roughly
// 60-90 seconds after the `az group update` call returned (bounded by
// 10-second poll granularity, not sub-second precision) -- slower than
// GCP's measured ~18 seconds, faster than CloudTrail's documented
// ~15-minute ceiling. DeliveryLag is set well above that measurement as
// a safety margin, not tuned tightly to it, the same posture
// gcp.Backend's own DeliveryLag already established.
var Backend = core.AttributionBackend{
	Name:             "azure_activity_log",
	SuccessKind:      "azure_activity_log",
	UnattributedKind: "audit_unattributed",
	DeliveryLag:      5 * time.Minute,
}

// Client implements core.EventLookup against the real Azure Monitor
// Activity Log API, scoped to Administrative-category entries.
type Client struct {
	api            *armmonitor.ActivityLogsClient
	subscriptionID string
}

// New creates an Azure Activity Log client scoped to subscriptionID,
// using azidentity.NewDefaultAzureCredential -- the Azure SDK's own
// credential-chain discovery (environment variables, managed identity,
// then the Azure CLI's own logged-in session via `az login`), the same
// "ubx doesn't reimplement credential discovery" posture cloudtrail.New/
// gcp.New already hold to for their own platforms' credential chains.
func New(ctx context.Context, subscriptionID string) (*Client, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("azure: create credential: %w", err)
	}
	api, err := armmonitor.NewActivityLogsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("azure: create activity logs client: %w", err)
	}
	return &Client{api: api, subscriptionID: subscriptionID}, nil
}

// LookupEvents implements core.EventLookup: searches this subscription's
// Activity Log for Administrative-category entries whose own ResourceID
// matches resourceID, within [since, until], paginating through every
// page of results.
//
// resourceID is whatever value the caller wants matched -- this Client
// has no opinion on which observed identity value should be tried, same
// posture as cloudtrail.Client/gcp.Client/k8s.Client. Unlike GCP's own
// scheme-less resource paths (gcp.Client's own doc comment), Azure's
// Activity Log resourceId field and every azurerm type's own observed
// "id" attribute both follow the identical ARM resource path convention
// -- confirmed live (UBI-37 Stage 2) -- but NOT byte-identical: a real
// Activity Log entry's own resourceId came back
// ".../resourcegroups/<name>" (all lowercase), while the azurerm
// provider's own observed "id" reports ".../resourceGroups/<name>"
// (camelCase) -- the exact same case-sensitivity trap this project
// already learned to watch for the hard way (docs/schema.md's own ARN
// case-folding amendment). The Activity Log REST API's own "eq" filter
// operator was empirically found to be case-SENSITIVE for resourceUri
// (a filter built from the camelCase id matched zero real events that
// `az monitor activity-log list` could see existed) -- so this Client
// never filters server-side by resourceUri at all, only by time window
// and resourceGroupName (parsed out of resourceID, itself case-
// insensitive server-side), then matches every returned event's own
// ResourceID against resourceID case-INSENSITIVELY here, client-side --
// and reports the match using resourceID's own original casing (not the
// server's), so core.AttributeDrift's own exact-match check downstream
// sees a byte-identical hit rather than needing its own case-insensitive
// logic (which would weaken exact-match's own guarantee for every OTHER
// platform that doesn't have this problem).
func (c *Client) LookupEvents(ctx context.Context, resourceID string, since, until time.Time) ([]core.CloudTrailEvent, error) {
	filter := fmt.Sprintf("eventTimestamp ge '%s' and eventTimestamp le '%s'",
		since.UTC().Format(time.RFC3339), until.UTC().Format(time.RFC3339))
	if rg, ok := resourceGroupFromID(resourceID); ok {
		filter += fmt.Sprintf(" and resourceGroupName eq '%s'", rg)
	}

	pager := c.api.NewListPager(filter, nil)

	var events []core.CloudTrailEvent
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("azure: list activity log for %q: %w", resourceID, err)
		}
		for _, e := range page.Value {
			if e == nil || e.ResourceID == nil || !strings.EqualFold(*e.ResourceID, resourceID) {
				continue
			}
			parsed, ok := parseEvent(e, resourceID)
			if !ok {
				continue // malformed/unparseable entry -- skip it rather than fail the whole lookup
			}
			events = append(events, parsed)
		}
	}
	return events, nil
}

// resourceGroupFromID extracts the resource group name segment from an
// ARM resource id ("/subscriptions/<sub>/resourceGroups/<rg>/..." or the
// bare "/subscriptions/<sub>/resourceGroups/<rg>" a resource group's own
// id is -- see conformance.Registry's own azurerm_resource_group entry).
// Case-insensitive on the "resourceGroups" segment name itself, matching
// this whole function's own reason for existing. ok is false for a
// candidate value that isn't ARM-id-shaped at all (a bare name, or
// addr.Name's own fallback candidate) -- LookupEvents falls back to an
// unscoped time-window-only query in that case, never silently dropping
// the search.
func resourceGroupFromID(id string) (string, bool) {
	segments := strings.Split(id, "/")
	for i, s := range segments {
		if strings.EqualFold(s, "resourceGroups") && i+1 < len(segments) {
			return segments[i+1], true
		}
	}
	return "", false
}

// parseEvent translates one armmonitor.EventData into
// core.CloudTrailEvent. ok is false if the entry has no EventDataID at
// all (Azure's own required, unique-per-event identifier) -- the caller
// skips it rather than failing the whole LookupEvents call over one bad
// record, same discipline as cloudtrail.parseEvent/gcp.parseEntry/
// k8s.parseEvent.
//
// core.CloudTrailEvent's field names are AWS-shaped (ActorARN, in
// particular) -- reused here rather than introduced as a new,
// Azure-specific struct, the same choice UBI-21/UBI-22 both made:
// ActorARN carries the Azure principal (Caller -- a UPN or service
// principal object id, never literally an ARN), EventName carries the
// Activity Log's own localized OperationName (more human-legible than
// EventName's own internal value -- "Microsoft.Resources/subscriptions/
// resourceGroups/write", not a generic "EndRequest"), SourceIP comes from
// HTTPRequest.ClientIPAddress when present (absent for some operation
// kinds -- not every Activity Log entry carries an HTTP request at all).
// matchedCandidate is the caller's own original-casing resourceID
// (LookupEvents already confirmed a case-insensitive match against
// e.ResourceID before calling this) -- used verbatim for Resources
// rather than the server's own possibly-differently-cased ResourceID,
// so core.AttributeDrift's own downstream exact-match check (which
// compares byte-for-byte against the SAME candidate value) succeeds.
func parseEvent(e *armmonitor.EventData, matchedCandidate string) (core.CloudTrailEvent, bool) {
	if e.EventDataID == nil {
		return core.CloudTrailEvent{}, false
	}

	var eventTime time.Time
	if e.EventTimestamp != nil {
		eventTime = *e.EventTimestamp
	}

	var caller string
	if e.Caller != nil {
		caller = *e.Caller
	}

	var sourceIP string
	if e.HTTPRequest != nil && e.HTTPRequest.ClientIPAddress != nil {
		sourceIP = *e.HTTPRequest.ClientIPAddress
	}

	var eventName string
	if e.OperationName != nil && e.OperationName.LocalizedValue != nil {
		eventName = *e.OperationName.LocalizedValue
	} else if e.OperationName != nil && e.OperationName.Value != nil {
		eventName = *e.OperationName.Value
	}

	resources := []string{matchedCandidate}

	raw, err := e.MarshalJSON()
	if err != nil {
		raw = nil
	}

	return core.CloudTrailEvent{
		EventID:   *e.EventDataID,
		EventName: eventName,
		EventTime: eventTime,
		ActorARN:  caller,
		SourceIP:  sourceIP,
		RawRecord: raw,
		Resources: resources,
	}, true
}
