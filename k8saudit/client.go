// Package k8saudit is UBI-22's concrete implementation of core.EventLookup
// against EKS control-plane audit logs delivered to CloudWatch Logs — the
// third platform-specific backend behind core.EventLookup, alongside
// package cloudtrail (UBI-10, AWS management events) and package gcpaudit
// (UBI-21, GCP Cloud Audit Logs). Same dependency-inversion discipline:
// core defines the plain-Go shape it needs (core.EventLookup), this
// package translates Kubernetes' own audit.k8s.io/v1 Event schema into it
// at the boundary. core itself never imports an AWS SDK or knows anything
// about Kubernetes.
//
// Scope, deliberately narrow (docs/architecture.md — Kubernetes support):
// this attributes drift on Kubernetes/Helm-managed objects (Deployments,
// Secrets, helm_release, ...) using the cluster's own control-plane audit
// log — it says nothing about, and never touches, CloudTrail's own view
// of EKS as an AWS resource (aws_eks_cluster's own drift stays exactly as
// CloudTrail-attributable as any other AWS resource; see
// cli/attribution.go's dispatch-by-ProviderSource reasoning).
//
// "ONE configured backend in v1": which cluster, and which CloudWatch log
// group, is not derivable from anything ubx already has on hand the way
// AWS's region or GCP's project is — this requires explicit,
// optional .ubx/config ([k8s_audit]) configuration; see cli/config.go and
// cli/attribution.go for the not_configured fallback when it's absent.
package k8saudit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	sdkcwl "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"

	"github.com/ubiquex/ubiquex-cli/core"
)

// Backend is k8saudit's own core.AttributionBackend value: "k8s_audit"/
// "audit_unattributed" kinds (Name == "k8s_audit_logs", carried on
// "audit_unattributed" sources same as gcpaudit's own Name/Backend
// pattern).
//
// DeliveryLag is a documented, conservative PLACEHOLDER, not a measured
// value — stated honestly rather than presented as if it were measured
// the way CloudTrail's (UBI-10) and GCP's (UBI-21) own figures are. See
// STATE.md for whether this session's own Stage 2 (an EKS cluster with
// control-plane logging enabled) had the chance to measure it directly;
// if it did, this value and comment should already reflect that instead
// of this placeholder framing.
var Backend = core.AttributionBackend{
	Name:             "k8s_audit_logs",
	SuccessKind:      "k8s_audit",
	UnattributedKind: "audit_unattributed",
	DeliveryLag:      5 * time.Minute,
}

// Client implements core.EventLookup against EKS control-plane audit logs
// in CloudWatch Logs.
//
// KNOWN GAP, checked and flagged rather than assumed clean (mirroring
// gcpaudit's own Pub/Sub-vs-Secret-Manager precedent, UBI-21): which
// identity value a Kubernetes audit event's objectRef can be matched
// against is unverified until this project's own Stage 2 live-cluster
// work — core.AttributeDrift's identityCandidates only offers a
// kubernetes_*-typed resource's top-level "id" (name/namespace/uid live
// nested inside metadata[0], see docs/architecture.md's "Kubernetes
// support" §1). LookupEvents compensates defensively by offering every
// plausible Resources shape (bare name, namespace/name, uid) rather than
// picking one -- a mitigation, not a confirmed fix.
type Client struct {
	api      *sdkcwl.Client
	logGroup string
}

// LogGroupForCluster derives EKS's own default control-plane log group
// naming convention for cluster, used when .ubx/config's [k8s_audit]
// table doesn't set an explicit log_group override.
func LogGroupForCluster(cluster string) string {
	return "/aws/eks/" + cluster + "/cluster"
}

// New loads AWS credentials/config the standard way (config.LoadDefaultConfig,
// same as cloudtrail.New — ubx doesn't reimplement credential discovery)
// and returns a Client scoped to region, searching logGroup.
func New(ctx context.Context, region, logGroup string) (*Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("k8saudit: load AWS config: %w", err)
	}
	return &Client{api: sdkcwl.NewFromConfig(cfg), logGroup: logGroup}, nil
}

// LookupEvents implements core.EventLookup: searches logGroup's audit log
// stream(s) via CloudWatch Logs' FilterLogEvents for entries containing
// resourceID as a literal term, within [since, until], paginating through
// every page of results.
//
// resourceID is whatever value the caller wants matched -- this Client has
// no opinion on which observed identity value (id, in practice, for
// kubernetes_* types today) should be tried, same posture as
// cloudtrail.Client/gcpaudit.Client. The filter pattern is a simple quoted
// term match (CloudWatch Logs' own "contains this text" syntax) -- safe
// unescaped for any real Kubernetes object identity value, since
// Kubernetes names/namespaces/uids are restricted to DNS-1123-safe
// characters (lowercase alphanumeric and hyphens) that never require
// quoting/escaping in the first place.
func (c *Client) LookupEvents(ctx context.Context, resourceID string, since, until time.Time) ([]core.CloudTrailEvent, error) {
	filterPattern := `"` + resourceID + `"`

	var events []core.CloudTrailEvent
	var nextToken *string
	for {
		out, err := c.api.FilterLogEvents(ctx, &sdkcwl.FilterLogEventsInput{
			LogGroupName:  aws.String(c.logGroup),
			FilterPattern: aws.String(filterPattern),
			StartTime:     aws.Int64(since.UnixMilli()),
			EndTime:       aws.Int64(until.UnixMilli()),
			NextToken:     nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("k8saudit: filter log events for %q: %w", resourceID, err)
		}
		for _, e := range out.Events {
			parsed, ok := parseEvent(e)
			if !ok {
				continue // malformed/unparseable log line -- skip it rather than fail the whole lookup
			}
			events = append(events, parsed)
		}
		if out.NextToken == nil {
			return events, nil
		}
		nextToken = out.NextToken
	}
}

// auditEvent is the subset of Kubernetes' own audit.k8s.io/v1 Event schema
// (https://kubernetes.io/docs/tasks/debug/debug-cluster/audit/) parseEvent
// needs -- one JSON object per CloudWatch Logs line, exactly as the EKS
// control plane writes it.
type auditEvent struct {
	AuditID string `json:"auditID"`
	Verb    string `json:"verb"`
	User    struct {
		Username string `json:"username"`
	} `json:"user"`
	SourceIPs []string `json:"sourceIPs"`
	ObjectRef struct {
		Resource  string `json:"resource"`
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
		UID       string `json:"uid"`
	} `json:"objectRef"`
	RequestReceivedTimestamp string `json:"requestReceivedTimestamp"` // RFC3339 (nanosecond precision)
}

// parseEvent translates one CloudWatch Logs FilteredLogEvent (whose
// Message is one Kubernetes audit event, JSON-encoded) into
// core.CloudTrailEvent. ok is false if Message isn't parseable JSON at
// all -- the caller skips it rather than failing the whole LookupEvents
// call over one bad line, same discipline as cloudtrail.parseEvent/
// gcpaudit.parseEntry.
//
// core.CloudTrailEvent's field names are AWS-shaped (ActorARN, in
// particular) -- reused here rather than introduced as a new,
// Kubernetes-specific struct, the same choice UBI-21 made for GCP:
// ActorARN carries the acting user's own username (Kubernetes'
// authentication layer reports whatever identity string it was
// configured with -- an IAM principal via EKS's own aws-auth mapping, an
// OIDC subject, or a service account -- never literally an ARN),
// EventName carries the audit event's verb (Kubernetes' own vocabulary --
// "create"/"update"/"patch"/"delete" -- not an AWS/GCP-shaped method
// name), SessionContext is always absent (no Kubernetes equivalent).
func parseEvent(e types.FilteredLogEvent) (core.CloudTrailEvent, bool) {
	msg := aws.ToString(e.Message)
	if msg == "" {
		return core.CloudTrailEvent{}, false
	}
	var ev auditEvent
	if err := json.Unmarshal([]byte(msg), &ev); err != nil {
		return core.CloudTrailEvent{}, false
	}

	eventTime, err := time.Parse(time.RFC3339Nano, ev.RequestReceivedTimestamp)
	if err != nil {
		if ts := aws.ToInt64(e.Timestamp); ts != 0 {
			eventTime = time.UnixMilli(ts).UTC()
		}
	}

	var sourceIP string
	if len(ev.SourceIPs) > 0 {
		sourceIP = ev.SourceIPs[0]
	}

	// Resources: every plausible identity shape this event's objectRef
	// could be matched against -- see this package's own doc comment on
	// the unverified id-format gap. Offering all three (rather than
	// picking the one AttributeDrift's identityCandidates is assumed to
	// use) is the defensive mitigation for that gap.
	var resources []string
	if ev.ObjectRef.Name != "" {
		resources = append(resources, ev.ObjectRef.Name)
		if ev.ObjectRef.Namespace != "" {
			resources = append(resources, ev.ObjectRef.Namespace+"/"+ev.ObjectRef.Name)
		}
	}
	if ev.ObjectRef.UID != "" {
		resources = append(resources, ev.ObjectRef.UID)
	}

	return core.CloudTrailEvent{
		EventID:   ev.AuditID,
		EventName: ev.Verb,
		EventTime: eventTime,
		ActorARN:  ev.User.Username,
		SourceIP:  sourceIP,
		RawRecord: []byte(msg),
		Resources: resources,
	}, true
}
