// Package gcp is UBI-21 Stage 2's concrete implementation of
// core.EventLookup against GCP's real Cloud Audit Logs (Cloud Logging's
// ListLogEntries API) — the GCP counterpart to package cloudtrail (UBI-10),
// same dependency-inversion discipline: core defines the plain-Go shape it
// needs (core.EventLookup), this package translates the real GCP client
// library's response into it at the boundary. core itself never imports a
// GCP SDK. Renamed from gcpaudit (UBI-37, the audit/ consolidation — see
// audit/azure's own doc comment for the full rationale) with zero
// behavior change: same interface, same dispatch.
//
// Scope, deliberately narrow, mirroring cloudtrail's own AWS scoping
// decision (docs/plan.md's UBI-10 rationale): **Admin Activity** audit
// logs only (log ID "cloudaudit.googleapis.com/activity") — the GCP
// equivalent of CloudTrail's "management events": always-on, cannot be
// disabled, no logging sink configuration required, and the closest match
// to "who changed this resource's configuration." Data Access audit logs
// (opt-in, and the GCP equivalent of CloudTrail's data events) are
// deliberately excluded, same reasoning as cloudtrail/'s own scope.
package gcp

import (
	"context"
	"fmt"
	"time"

	logging "cloud.google.com/go/logging/apiv2"
	"cloud.google.com/go/logging/apiv2/loggingpb"
	"google.golang.org/api/iterator"
	"google.golang.org/genproto/googleapis/cloud/audit"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/ubiquex/ubiquex-cli/core"
)

// auditLogTypeURL is the well-known type URL Cloud Audit Log entries use
// for their protoPayload -- documented by GCP itself (see
// loggingpb.LogEntry_ProtoPayload's own doc comment) and required to
// unmarshal the entry's Any payload back into an audit.AuditLog.
const auditLogTypeURL = "type.googleapis.com/google.cloud.audit.AuditLog"

// Backend is gcp's own core.AttributionBackend value: "gcp_audit"/
// "audit_unattributed" kinds (Name == "gcp_audit_logs", carried on
// "audit_unattributed" sources), and a delivery lag based on direct
// measurement against a real project (UBI-21 Stage 2), not GCP's own
// documentation the way CloudTrail's 15-minute figure was -- GCP doesn't
// publish an equivalent "up to N minutes" ceiling the way AWS does for
// LookupEvents. Measured: a Pub/Sub topic's CreateTopic admin-activity
// entry became queryable via ListLogEntries roughly 18 seconds after the
// API call returned (well under a minute). DeliveryLag is set well above
// that single measurement as a safety margin, not tuned tightly to it.
var Backend = core.AttributionBackend{
	Name:             "gcp_audit_logs",
	SuccessKind:      "gcp_audit",
	UnattributedKind: "audit_unattributed",
	DeliveryLag:      3 * time.Minute,
}

// Client implements core.EventLookup against the real GCP Cloud Logging
// API, scoped to Admin Activity audit log entries.
//
// KNOWN GAP, found during Stage 2 live verification, not silently
// papered over: which value a GCP service's own audit log entry uses for
// protoPayload.resourceName is not consistent across services the way
// core.AttributeDrift's identityCandidates (id/arn/name from the
// resource's own observed state) assumes. Confirmed empirically:
// Pub/Sub's CreateTopic entry names the resource as
// "projects/<PROJECT_ID>/topics/<name>" -- matching google_pubsub_topic's
// own observed "id" attribute exactly, so correlation works. Secret
// Manager's CreateSecret entry instead names it
// "projects/<PROJECT_NUMBER>/secrets/<name>" -- the numeric project
// number, which never appears anywhere in google_secret_manager_secret's
// own observed state, so identityCandidates can never produce a
// candidate that matches, and every secret's drift is currently
// unattributable via this backend (always "audit_unattributed"/
// "no_matching_event", indistinguishable from a genuine no-event case).
// This needs either a per-service resourceName-shape table (the GCP
// counterpart to identityCandidates' own AWS id/arn/name ordering) or a
// project-number lookup added as a candidate -- not implemented here,
// flagged rather than silently declared working. See STATE.md.
type Client struct {
	api     *logging.Client
	project string
}

// New creates a GCP Cloud Logging client scoped to project, using
// whatever Application Default Credentials the environment already
// provides (gcloud auth application-default login, a service account
// key via GOOGLE_APPLICATION_CREDENTIALS, or GCE/GKE workload identity —
// ubx doesn't reimplement credential discovery, same posture as
// cloudtrail.New's use of AWS's own config.LoadDefaultConfig).
func New(ctx context.Context, project string) (*Client, error) {
	api, err := logging.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcp: create logging client: %w", err)
	}
	return &Client{api: api, project: project}, nil
}

// Close releases the underlying gRPC connection.
func (c *Client) Close() error {
	return c.api.Close()
}

// LookupEvents implements core.EventLookup: searches this project's Admin
// Activity audit log for entries whose protoPayload.resourceName matches
// resourceID, within [since, until], paginating through every page of
// results.
//
// resourceID is whatever value the caller wants matched against
// protoPayload.resourceName -- GCP's audit log entries record this as a
// scheme-less resource path (e.g.
// "projects/P/topics/T", not a bare id or name) -- which exact shape that
// takes was not assumed here; see core.AttributeDrift's own
// identityCandidates, which already tries a resource's id/arn/name in
// turn and is unmodified by this package (this Client has no opinion on
// which identity value should be tried, same posture as cloudtrail.Client).
func (c *Client) LookupEvents(ctx context.Context, resourceID string, since, until time.Time) ([]core.CloudTrailEvent, error) {
	filter := fmt.Sprintf(
		`logName="projects/%s/logs/cloudaudit.googleapis.com%%2Factivity" AND `+
			`protoPayload.resourceName="%s" AND `+
			`timestamp>="%s" AND timestamp<="%s"`,
		c.project, resourceID, since.UTC().Format(time.RFC3339), until.UTC().Format(time.RFC3339))

	it := c.api.ListLogEntries(ctx, &loggingpb.ListLogEntriesRequest{
		ResourceNames: []string{"projects/" + c.project},
		Filter:        filter,
	})

	var events []core.CloudTrailEvent
	for {
		entry, err := it.Next()
		if err == iterator.Done {
			return events, nil
		}
		if err != nil {
			return nil, fmt.Errorf("gcp: list log entries for %q: %w", resourceID, err)
		}
		parsed, ok := parseEntry(entry)
		if !ok {
			continue // malformed/unparseable entry -- skip it rather than fail the whole lookup
		}
		events = append(events, parsed)
	}
}

// parseEntry translates one loggingpb.LogEntry (whose payload must be a
// google.cloud.audit.AuditLog proto, the shape every Cloud Audit Log entry
// uses) into core.CloudTrailEvent. ok is false if the entry's payload
// isn't a parseable AuditLog at all -- the caller skips it rather than
// failing the whole LookupEvents call over one bad record, same
// discipline as cloudtrail.parseEvent.
//
// core.CloudTrailEvent's field names are AWS-shaped (ActorARN, in
// particular) -- reused here rather than introduced as a new,
// GCP-specific struct because core.EventLookup's existing shape genuinely
// held up (see this package's own doc comment and docs/architecture.md's
// "GCP support" section): ActorARN carries the GCP principal email
// instead of an AWS ARN, EventID is Cloud Logging's InsertId, and
// Resources carries the single protoPayload.resourceName GCP itself
// reports (a scheme-less path, not an ARN) -- callers correlate against
// whatever identity value core.AttributeDrift tries, not against a field
// name that presumes AWS's own identity shape.
func parseEntry(entry *loggingpb.LogEntry) (core.CloudTrailEvent, bool) {
	protoPayload, ok := entry.GetPayload().(*loggingpb.LogEntry_ProtoPayload)
	if !ok || protoPayload.ProtoPayload == nil {
		return core.CloudTrailEvent{}, false
	}
	auditLog, ok := unmarshalAuditLog(protoPayload.ProtoPayload)
	if !ok {
		return core.CloudTrailEvent{}, false
	}

	var actorEmail, callerIP string
	if auditLog.GetAuthenticationInfo() != nil {
		actorEmail = auditLog.GetAuthenticationInfo().GetPrincipalEmail()
	}
	if auditLog.GetRequestMetadata() != nil {
		callerIP = auditLog.GetRequestMetadata().GetCallerIp()
	}

	var eventTime time.Time
	if ts := entry.GetTimestamp(); ts != nil {
		eventTime = ts.AsTime()
	}

	var resources []string
	if r := auditLog.GetResourceName(); r != "" {
		resources = append(resources, r)
	}

	return core.CloudTrailEvent{
		EventID:   entry.GetInsertId(),
		EventName: auditLog.GetMethodName(),
		EventTime: eventTime,
		ActorARN:  actorEmail,
		SourceIP:  callerIP,
		RawRecord: []byte(entry.String()),
		Resources: resources,
	}, true
}

// unmarshalAuditLog unwraps a LogEntry's protoPayload Any into an
// audit.AuditLog, refusing anything that doesn't carry the well-known
// Cloud Audit Log type URL -- a LogEntry with some other proto payload
// (a small handful of Google services use protoPayload for non-audit
// data, per the field's own doc comment) is not a parsing bug, just not
// an audit log entry at all.
func unmarshalAuditLog(payload *anypb.Any) (*audit.AuditLog, bool) {
	if payload.GetTypeUrl() != auditLogTypeURL {
		return nil, false
	}
	var log audit.AuditLog
	if err := payload.UnmarshalTo(&log); err != nil {
		return nil, false
	}
	return &log, true
}
