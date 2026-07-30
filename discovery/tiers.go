package discovery

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Tier classifies how a Terraform resource type's own provider lookup
// shape relates to its AWS ARN — docs/discovery.md's own "three real
// tiers, empirically confirmed" section, checked directly against
// conformance.Registry's live-verified entries rather than assumed.
type Tier int

const (
	// TierUnknown means this (service, resource-type-prefix) pair isn't
	// in the tier table at all — BuildLookup reports "not yet
	// adoptable," never guesses.
	TierUnknown Tier = iota

	// TierA: the provider's own lookup id IS the ARN itself.
	// Confirmed: aws_iam_policy (conformance.Registry's own Notes:
	// "id IS the ARN... lookup only needs {\"id\": \"<policy-arn>\"}").
	TierA

	// TierB: the provider's own lookup id is the ARN's own trailing
	// ResourceID segment, optionally duplicated into one or more
	// additional fields. Confirmed: aws_vpc (id = the trailing vpc-*
	// id); aws_iam_role/aws_iam_user (id AND name both = the trailing
	// name segment — Registry: "name alone reads back null, empirically
	// confirmed"); aws_s3_bucket (id AND bucket both = the trailing
	// bucket name).
	TierB

	// TierC: the provider's own lookup id is constructed from the ARN's
	// own components (service/region/account/resource-id), not a
	// substring of the ARN. Confirmed live this session: aws_sqs_queue's
	// own real queue URL
	// (https://sqs.us-east-1.amazonaws.com/839333509514/ubx-discovery-...),
	// built from exactly the ARN's own region/account/resource-id, no
	// extra API round trip needed.
	TierC
)

// typeSpec is one tier-table entry — everything BuildLookup/Discover
// need to know about one Terraform resource type's own relationship to
// its AWS ARN shape.
type typeSpec struct {
	// TerraformType is this entry's own Terraform resource type name
	// (e.g. "aws_sqs_queue") — carried here (not just as the map key)
	// so a caller holding a *typeSpec from the reverse (service,
	// resourceTypePrefix) index can still report which Terraform type
	// it resolved to.
	TerraformType string
	Tier          Tier
	// AugmentFields (Tier B only) names additional lookup fields to set
	// to the same trailing ResourceID value core/lookuphints and
	// stateimport.BuildLookup already separately maintain this exact fact
	// for (docs/discovery.md's own "recommendation, not built this
	// session" — this table is discovery's own fourth copy, honestly
	// named as such rather than silently pretended unified).
	AugmentFields []string
	// Construct (Tier C only) builds the lookup id from the parsed ARN.
	Construct func(a ARN) (id string, err error)

	// CreationVerbs names the CloudTrail EventName(s) that mean "this
	// resource was just created" (e.g. "CreateQueue") — the genesis-
	// attribution bonus's own small, curated per-type knowledge
	// (session 3, docs/discovery.md's "attribution bonus"), reusing
	// this same per-type table rather than a fifth separately-
	// maintained one. Empty means genesis attribution isn't attempted
	// for this type at all yet (core.AttributeGenesis's own
	// ReasonNoCreationVerbs, never silently skipped without a reason).
	CreationVerbs []string
}

// tierTable is keyed by (service, resourceTypePrefix) — see ARN.classKey
// — seeded from this arc's own session-1 empirical findings
// (docs/discovery.md). Deliberately small: every entry here is either
// live-verified in conformance.Registry (RealSafe/Implemented) or
// confirmed directly this session (aws_sqs_queue) — this table never
// guesses a tier from a type's name or category alone. CreationVerbs are
// AWS's own well-documented API operation names (CloudTrail's EventName
// always matches the API operation verbatim) — real, not guessed.
var tierTable = map[string]typeSpec{
	"iam:policy": {TerraformType: "aws_iam_policy", Tier: TierA, CreationVerbs: []string{"CreatePolicy"}},
	"ec2:vpc":    {TerraformType: "aws_vpc", Tier: TierB, CreationVerbs: []string{"CreateVpc"}},
	"iam:role":   {TerraformType: "aws_iam_role", Tier: TierB, AugmentFields: []string{"name"}, CreationVerbs: []string{"CreateRole"}},
	"iam:user":   {TerraformType: "aws_iam_user", Tier: TierB, AugmentFields: []string{"name"}, CreationVerbs: []string{"CreateUser"}},
	"s3:":        {TerraformType: "aws_s3_bucket", Tier: TierB, AugmentFields: []string{"bucket"}, CreationVerbs: []string{"CreateBucket"}},
	"sqs:":       {TerraformType: "aws_sqs_queue", Tier: TierC, Construct: sqsQueueURL, CreationVerbs: []string{"CreateQueue"}},
}

// sqsQueueURL builds an aws_sqs_queue's own real lookup id — the queue
// URL, not the ARN — from an SQS ARN's own region/account/resource-id
// components. Confirmed live this session against a real, throwaway
// queue: ARN "arn:aws:sqs:us-east-1:839333509514:ubx-discovery-..."
// produces exactly
// "https://sqs.us-east-1.amazonaws.com/839333509514/ubx-discovery-...",
// the real queue URL `aws sqs get-queue-attributes` accepted.
func sqsQueueURL(a ARN) (string, error) {
	if a.Region == "" || a.Account == "" || a.ResourceID == "" {
		return "", fmt.Errorf("sqs queue ARN missing region/account/resource-id: %s", a)
	}
	return fmt.Sprintf("https://sqs.%s.amazonaws.com/%s/%s", a.Region, a.Account, a.ResourceID), nil
}

// ErrNotYetAdoptable means a discovered ARN's (service, resource-type-
// prefix) pair isn't in tierTable at all, or its tier's own Construct
// function failed — docs/discovery.md's own "coverage gaps surface
// honestly" requirement: never silently dropped, always a named,
// distinct reason a caller can report and a future session can close
// for one more type.
var ErrNotYetAdoptable = fmt.Errorf("not yet adoptable: no known lookup shape for this type")

// BuildLookup translates a into the json.RawMessage lookup key
// core.RunScan's own ReadResource call needs, per its classified tier.
// terraformType is the TerraformType this ARN was resolved to (by
// Discover's own classification step, ClassifyARN) — needed here only
// to know which AugmentFields apply, since two Tier-B entries can want
// different augmented field names.
func BuildLookup(spec typeSpec, a ARN) (json.RawMessage, error) {
	switch spec.Tier {
	case TierA:
		return json.Marshal(map[string]string{"id": a.String()})
	case TierB:
		lookup := map[string]string{"id": a.ResourceID}
		for _, field := range spec.AugmentFields {
			lookup[field] = a.ResourceID
		}
		return json.Marshal(lookup)
	case TierC:
		if spec.Construct == nil {
			return nil, fmt.Errorf("%w: %s (tier C with no constructor)", ErrNotYetAdoptable, spec.TerraformType)
		}
		id, err := spec.Construct(a)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrNotYetAdoptable, spec.TerraformType, err)
		}
		return json.Marshal(map[string]string{"id": id})
	default:
		return nil, fmt.Errorf("%w: %s", ErrNotYetAdoptable, a.String())
	}
}

// ClassifyARN looks a up in tierTable by its own (service,
// resourceTypePrefix) key. ok is false if the pair isn't classified at
// all — the caller (Discover) reports this as "discovered, not yet
// adoptable," never guesses a Terraform type from the ARN's service
// name alone.
func ClassifyARN(a ARN) (spec typeSpec, ok bool) {
	spec, ok = tierTable[a.classKey()]
	return spec, ok
}

// terraformTypeToClassKey is tierTable's own reverse index, built once —
// lets Discover's own --type allowlist (Terraform type names) narrow
// which (service, resourceTypePrefix) ARNs to even consider, without a
// second hand-maintained table (docs/discovery.md's own "one ARN-parsing
// pass answers both 'which tier' and 'does this match --type'").
var terraformTypeToClassKey = func() map[string]string {
	m := make(map[string]string, len(tierTable))
	for classKey, spec := range tierTable {
		m[spec.TerraformType] = classKey
	}
	return m
}()

// ErrUnknownDiscoveryType means a --type allowlist entry names a
// Terraform type discovery has no tier-table entry for at all — a
// fail-fast, whole-request error (the operator asked for something
// discovery structurally can't classify yet), distinct from
// ErrNotYetAdoptable's own per-resource reporting for a type discovery
// DOES know about but couldn't build a lookup for on this specific ARN.
var ErrUnknownDiscoveryType = errors.New("discovery has no known ARN shape for this Terraform type yet")

// classKeysFor resolves a --type allowlist (Terraform type names) into
// the set of tier-table classKeys to restrict discovery to. Returns an
// error naming the first unrecognized type name, per ErrUnknownDiscoveryType.
func classKeysFor(types []string) (map[string]bool, error) {
	if len(types) == 0 {
		return nil, nil
	}
	keys := make(map[string]bool, len(types))
	for _, t := range types {
		classKey, ok := terraformTypeToClassKey[t]
		if !ok {
			return nil, fmt.Errorf("--type %q: %w", t, ErrUnknownDiscoveryType)
		}
		keys[classKey] = true
	}
	return keys, nil
}
