// Package cloudtrail is UBI-10's concrete implementation of core.EventLookup
// against the real AWS CloudTrail LookupEvents API — the only place in this
// codebase that imports an AWS SDK directly (aws-sdk-go-v2). core itself
// never does (see core/attribution.go's EventLookup interface, the same
// dependency-inversion discipline as core.StateReader for the tfplugin
// provider client, UBI-7 follow-up): core defines the plain-Go shape it
// needs, this package translates the real SDK's response into it at the
// boundary.
//
// This is deliberately not part of package provider: provider/ speaks the
// tfplugin gRPC protocol to Terraform provider binaries, an entirely
// different wire protocol and concern from calling an AWS management API
// directly. Scope is intentionally narrow — LookupEvents (management
// events only, ~90-day default retention), no trail configuration, no
// CloudTrail Lake — per docs/plan.md's UBI-10 scoping.
package cloudtrail

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	sdkcloudtrail "github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"

	"github.com/ubiquex/ubiquex-cli/core"
)

// Client implements core.EventLookup against the real AWS CloudTrail API.
type Client struct {
	api *sdkcloudtrail.Client
}

// New loads AWS credentials/config the standard way (environment,
// ~/.aws/credentials, SSO, instance role — whatever config.LoadDefaultConfig
// already resolves; ubx doesn't reimplement credential discovery) and
// returns a Client scoped to region.
func New(ctx context.Context, region string) (*Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("cloudtrail: load AWS config: %w", err)
	}
	return &Client{api: sdkcloudtrail.NewFromConfig(cfg)}, nil
}

// LookupEvents implements core.EventLookup: searches CloudTrail's
// LookupEvents for management events whose ResourceName lookup attribute
// matches resourceID, within [since, until], paginating through every
// page of results.
//
// resourceID is whatever value the caller wants CloudTrail's ResourceName
// attribute matched against — which observed attribute (id/arn/name) that
// should be varies by AWS service (see core.AttributeDrift's doc comment);
// this Client has no opinion on that, it just performs the one lookup it's
// asked to.
func (c *Client) LookupEvents(ctx context.Context, resourceID string, since, until time.Time) ([]core.CloudTrailEvent, error) {
	var events []core.CloudTrailEvent
	var nextToken *string
	for {
		out, err := c.api.LookupEvents(ctx, &sdkcloudtrail.LookupEventsInput{
			StartTime: aws.Time(since),
			EndTime:   aws.Time(until),
			LookupAttributes: []types.LookupAttribute{
				{AttributeKey: types.LookupAttributeKeyResourceName, AttributeValue: aws.String(resourceID)},
			},
			NextToken: nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("cloudtrail: lookup events for %q: %w", resourceID, err)
		}
		for _, e := range out.Events {
			parsed, ok := parseEvent(e)
			if !ok {
				continue // malformed/unparseable record -- skip it rather than fail the whole lookup
			}
			events = append(events, parsed)
		}
		if out.NextToken == nil {
			return events, nil
		}
		nextToken = out.NextToken
	}
}

// rawRecord is the subset of a CloudTrailEvent JSON record (the
// "CloudTrailEvent" field's own JSON-encoded string, not the flat Event
// struct — the flat struct has EventId/EventName/EventTime/Username but
// not the actor's ARN, source IP, or session context, which only live
// inside this nested record) that parseEvent needs.
type rawRecord struct {
	EventID      string `json:"eventID"`
	EventName    string `json:"eventName"`
	EventTime    string `json:"eventTime"` // RFC3339, UTC ("Z")
	SourceIP     string `json:"sourceIPAddress"`
	UserIdentity struct {
		ARN            string          `json:"arn"`
		SessionContext json.RawMessage `json:"sessionContext,omitempty"`
	} `json:"userIdentity"`
	Resources []struct {
		ARN          string `json:"ARN"`
		ResourceName string `json:"resourceName"`
	} `json:"resources"`
}

// parseEvent translates one SDK types.Event (plus its nested CloudTrailEvent
// JSON record) into core.CloudTrailEvent. ok is false if the record can't be
// parsed at all (CloudTrailEvent empty, or not valid JSON) — the caller
// skips it rather than failing the whole LookupEvents call over one bad
// record.
func parseEvent(e types.Event) (core.CloudTrailEvent, bool) {
	raw := aws.ToString(e.CloudTrailEvent)
	if raw == "" {
		return core.CloudTrailEvent{}, false
	}
	var rec rawRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return core.CloudTrailEvent{}, false
	}

	// Resources: prefer the flat, SDK-parsed Event.Resources (ResourceName
	// is what CloudTrail's own ResourceName lookup attribute matches
	// against — confirmed empirically to be the value that works for
	// aws_s3_bucket/aws_iam_role/aws_vpc, see STATE.md), but also fold in
	// the raw record's resources[].ARN, since some services expose the ARN
	// there even when the flat ResourceName field is the short name (or
	// vice versa) -- AttributeDrift's own exact-match filtering decides
	// which of these actually matches this resource, not this parser.
	var resources []string
	for _, r := range e.Resources {
		if name := aws.ToString(r.ResourceName); name != "" {
			resources = append(resources, name)
		}
	}
	for _, r := range rec.Resources {
		if r.ARN != "" {
			resources = append(resources, r.ARN)
		}
		if r.ResourceName != "" {
			resources = append(resources, r.ResourceName)
		}
	}

	eventTime, err := time.Parse(time.RFC3339, rec.EventTime)
	if err != nil {
		// Fall back to the SDK's own parsed EventTime if the nested
		// record's own eventTime string is somehow missing/malformed.
		if e.EventTime != nil {
			eventTime = *e.EventTime
		}
	}

	return core.CloudTrailEvent{
		EventID:        rec.EventID,
		EventName:      rec.EventName,
		EventTime:      eventTime,
		ActorARN:       rec.UserIdentity.ARN,
		SourceIP:       rec.SourceIP,
		SessionContext: rec.UserIdentity.SessionContext,
		RawRecord:      []byte(raw),
		Resources:      resources,
	}, true
}
