// Package discovery is UBI-45's own cloud-side discovery mechanism:
// enumerating real AWS resources via the Resource Groups Tagging API
// (chosen empirically over AWS Config and the tfplugin ListResource RPC,
// see docs/discovery.md's own "mechanism decision" section) and bridging
// each returned ARN into the provider lookup shape core.RunScan already
// needs — the identity bridge, this arc's actual hard problem. Discovery
// only ever produces identity; a resource's recorded content still comes
// from the exact same, completely unmodified core.RunScan/core.
// GenerateProposal pipeline UBI-7/UBI-18 already built (docs/
// architecture.md's "state provides identity, never truth" principle,
// applied to a new identity source).
package discovery

import (
	"errors"
	"fmt"
	"strings"
)

// ErrMalformedARN means a string doesn't have the minimum
// "arn:partition:service:region:account:resource" shape (6 colon-
// separated fields) — genuinely malformed input, not a recognized-but-
// unclassified type.
var ErrMalformedARN = errors.New("malformed ARN")

// ARN is a parsed AWS Amazon Resource Name — only the fields the
// identity bridge actually needs, nothing else.
type ARN struct {
	Partition string // almost always "aws"
	Service   string // e.g. "sqs", "iam", "s3", "ec2"
	Region    string // empty for global services (iam, s3)
	Account   string
	Resource  string // everything after the 6th colon, raw and unsplit

	// ResourceTypePrefix/ResourceID are Resource split on its first '/'
	// (Terraform/AWS's own "<resource-type>/<resource-id>" ARN
	// convention — e.g. "role/my-role" -> "role", "my-role"). A
	// colon-form ARN with no '/' at all (e.g. an SQS queue's own
	// "arn:aws:sqs:region:account:queue-name") has an empty
	// ResourceTypePrefix and ResourceID equal to the whole Resource —
	// this is the "bare-colon-form" shape docs/discovery.md's own
	// tier table keys off of exactly like ResourceTypePrefix present.
	ResourceTypePrefix string
	ResourceID         string
}

// ParseARN parses s into its component fields. Only the minimum "arn:
// partition:service:region:account:resource" shape is validated —
// service-specific resource-format quirks are the tier table's own
// problem (discovery/tiers.go), not this parser's.
func ParseARN(s string) (ARN, error) {
	fields := strings.SplitN(s, ":", 6)
	if len(fields) != 6 || fields[0] != "arn" {
		return ARN{}, fmt.Errorf("%w: %q", ErrMalformedARN, s)
	}
	a := ARN{
		Partition: fields[1],
		Service:   fields[2],
		Region:    fields[3],
		Account:   fields[4],
		Resource:  fields[5],
	}
	if typePrefix, id, ok := strings.Cut(a.Resource, "/"); ok {
		a.ResourceTypePrefix = typePrefix
		a.ResourceID = id
	} else {
		a.ResourceID = a.Resource
	}
	return a, nil
}

// String renders a back into its canonical "arn:..." form — used only
// by tests to round-trip; discovery's own real code always carries the
// original string alongside a parsed ARN rather than reconstructing it.
func (a ARN) String() string {
	return fmt.Sprintf("arn:%s:%s:%s:%s:%s", a.Partition, a.Service, a.Region, a.Account, a.Resource)
}

// classKey is the tier table's own lookup key — (service,
// resourceTypePrefix), matching docs/discovery.md's own finding that an
// ARN's resource-type prefix (when present) already disambiguates
// same-service Terraform types (aws_iam_role/aws_iam_user/aws_iam_policy
// all share the "iam" service but have distinct "role"/"user"/"policy"
// prefixes) without needing a --type allowlist at all; only a bare-
// colon-form ARN (empty prefix) risks a genuine collision if a future
// type is added sharing one service with no prefix -- named honestly in
// tiers.go, not hidden.
func (a ARN) classKey() string {
	return a.Service + ":" + a.ResourceTypePrefix
}
