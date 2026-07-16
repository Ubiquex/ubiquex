// Package conformance is UBI-9's per-resource-type conformance harness: a
// table-driven registry of provider resource types (docs/plan.md §M1-2,
// "top ~50 AWS resource types"; §M1-2 GCP resource type list, UBI-21) plus
// a reusable adopt→mutate→scan-diff test pattern that exercises
// core.RunScan/GenerateProposal against each type — the real provider
// where cheap/safe, fakeprovider fixtures otherwise.
//
// Registry entries are keyed by (provider source, type), not bare type
// name (UBI-21, docs/architecture.md — GCP support): a second provider
// makes "there's only one provider" an assumption worth naming rather
// than leaving implicit, even though today's aws_*/google_* type-name
// prefixes don't actually collide in practice.
//
// This is project-internal tooling, not shipped product code — it lives
// outside core/ and cli/ deliberately, since "does ubx handle this
// resource type correctly" is a test/coverage concern, not part of the
// trust core or CLI surface.
//
//go:generate go run ./gentool -out ../core/lookuphints/hints.go
package conformance

import "encoding/json"

// Safety classifies whether a type's conformance test may run against the
// real account (AWS or GCP, whichever this entry's Source is), or must
// stay on fakeprovider fixtures.
type Safety int

const (
	// FakeOnly means this type's conformance test never touches a real
	// account — the resource is too expensive (hourly-billed compute/DB/
	// network appliances) or too slow/risky to spin up and tear down just
	// to exercise a schema shape. A fakeprovider fixture stands in (see
	// provider/internal/fakeprovider's "conformance-v5"/"conformance-v6"
	// modes and conformance/fake_test.go).
	//
	// What "verified" means for a FakeOnly entry, precisely (UBI-9 batch
	// 3): IdentityFields and the attributes named in Notes are real —
	// checked against the actual provider's GetProviderSchema, which
	// needs no Configure call, no credentials, and no live API round trip
	// at all, so this is free and safe to do for every type regardless of
	// cost/risk. What is NOT verified for a FakeOnly type is the live
	// ReadResource *lookup* convention (e.g. whether a natural-key
	// duplicate alongside "id" is required, the way aws_iam_role/
	// aws_s3_bucket empirically turned out to need) — that can only be
	// checked by actually calling a real provider's ReadResource against
	// a real instance, which is exactly the cost/risk this type is
	// fake-only to avoid. The fake fixture instead proves ubx's own
	// scan/diff/fold pipeline (RunScan, GenerateProposal, FoldState,
	// diffAttributes) classifies new/drifted/unchanged and generates a
	// correct before/after diff for that type's real attribute shape —
	// "conformance means the same thing across both classes" in the
	// sense that both prove the pipeline correct, not in the sense that
	// both prove the same thing about live lookup semantics.
	FakeOnly Safety = iota

	// RealSafe means this type's conformance test may run against the
	// real account: the resource is free or negligible-cost, safe to
	// read (and, for the mutate step, safe to tag), and either already
	// exists in the account or is cheap enough to create/destroy per test
	// run.
	RealSafe
)

func (s Safety) String() string {
	if s == RealSafe {
		return "real-safe"
	}
	return "fake-only"
}

// TypeSpec describes one provider resource type's place in the
// conformance suite.
type TypeSpec struct {
	// Source is the type's provider, in Terraform-source form (e.g.
	// "hashicorp/aws", "hashicorp/google") — part of this entry's
	// identity, not metadata about it (UBI-21, docs/architecture.md — GCP
	// support): Registry entries and core/lookuphints are both keyed by
	// (Source, Type), never bare Type alone.
	Source   string
	Type     string // e.g. "aws_s3_bucket", "google_storage_bucket"
	Category string // "compute" | "network" | "iam" | "storage" | "db" | "dns" | "messaging"
	Safety   Safety

	// IdentityFields lists which observed-state attribute(s) carry this
	// resource's stable identity — almost always "id", and "arn" wherever
	// the schema surfaces one. Recorded so lookup data can capture ARN (or
	// equivalent) up front — forward-compat for CloudTrail attribution
	// (UBI-10), which correlates by ARN, not by ubx's own address scheme.
	// Left nil/unverified for types this session hasn't actually
	// implemented yet — see Implemented.
	IdentityFields []string

	// Notes records quirks discovered by actually implementing this
	// type's conformance test: non-standard id semantics, lookup shape
	// surprises, anything that made the type "fight back". Populated only
	// once Implemented; a type that isn't implemented yet has no verified
	// notes to record.
	Notes string

	// LookupHint names this type's own natural-key attribute(s) -- the
	// ones a user coming from Terraform's own attribute names might
	// reach for alone instead of "id" (e.g. aws_s3_bucket's "bucket",
	// aws_iam_role/aws_iam_user's "name") -- empirically confirmed to
	// read back null on their own; "id" alone, by contrast, IS
	// sufficient for every one of these types (verified live,
	// conformance/lookuphints_live_test.go, not assumed from the Notes
	// prose). Left nil for every type where this confusion doesn't
	// apply, including types whose id happens to BE something
	// surprising like an ARN or URL (that's a "use the right value"
	// surprise, not a "you reached for the wrong key" one, and isn't
	// safe to promote as a shipped teaching-error hint -- see
	// core/lookuphints, UBI-20 workstream 3, which generates a shipped
	// table from exactly this field via go:generate).
	LookupHint []string

	// Implemented is true once this type has a real conformance test
	// (see aws_test.go and friends) backing up IdentityFields/Notes.
	// Session UBI-9-1 seeds the ~50-type list with Implemented=false
	// almost everywhere; "subsequent sessions work through the list in
	// batches" (STATE.md) flips these on one by one.
	Implemented bool
}

// Registry is the ~50-type list, docs/plan.md §M1-2's own copy of which is
// the canonical rationale/source of truth — this is the executable
// counterpart. Biased toward what real Terraform shops actually run:
// compute, network, IAM, storage, database, DNS, plus a handful of
// messaging/observability/secrets types that show up in nearly every real
// account too.
var Registry = []TypeSpec{
	// --- compute ---
	// All nine: real schema (GetProviderSchema) has id/arn/tags/tags_all;
	// fixture models exactly those four and mutates tags — see
	// conformance/fake_test.go's stdCase.
	{Type: "aws_instance", Source: "hashicorp/aws", Category: "compute", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags. Real schema also carries ami/instance_type/etc. (not modeled — see FakeOnly's doc comment on scope).",
		Implemented:    true},
	{Type: "aws_launch_template", Source: "hashicorp/aws", Category: "compute", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},
	{Type: "aws_autoscaling_group", Source: "hashicorp/aws", Category: "compute", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},
	{Type: "aws_ecs_cluster", Source: "hashicorp/aws", Category: "compute", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},
	{Type: "aws_ecs_service", Source: "hashicorp/aws", Category: "compute", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},
	{Type: "aws_ecs_task_definition", Source: "hashicorp/aws", Category: "compute", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags. Real schema's natural key is \"family\" (required), not modeled in the fixture.",
		Implemented:    true},
	{Type: "aws_eks_cluster", Source: "hashicorp/aws", Category: "compute", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},
	{Type: "aws_eks_node_group", Source: "hashicorp/aws", Category: "compute", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},
	{Type: "aws_lambda_function", Source: "hashicorp/aws", Category: "compute", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags. Real schema's natural key is \"function_name\" (required), not modeled in the fixture.",
		Implemented:    true},

	// --- network ---
	{
		Type: "aws_vpc", Source: "hashicorp/aws", Category: "network", Safety: RealSafe,
		IdentityFields: []string{"id", "arn"},
		Notes: "id is the vpc-* id (e.g. \"vpc-b75be9cd\"); that's also " +
			"what the lookup needs (\"id\"). arn is surfaced directly by " +
			"the schema too. Verified against the account's real default " +
			"VPC.",
		Implemented: true,
	},
	{Type: "aws_subnet", Source: "hashicorp/aws", Category: "network", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},
	{Type: "aws_route_table", Source: "hashicorp/aws", Category: "network", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},
	{
		Type: "aws_route_table_association", Source: "hashicorp/aws", Category: "network", Safety: FakeOnly,
		Notes: "PARKED, not hacked: real schema (GetProviderSchema) is " +
			"{gateway_id, id, region, route_table_id (required), " +
			"subnet_id} -- a pure join between a route table and " +
			"whichever of gateway_id/subnet_id it associates. Neither " +
			"optional field is a genuine in-place \"modify\" in AWS's own " +
			"semantics (changing what an association points to is a " +
			"replace, like aws_iam_role_policy_attachment below), so " +
			"there's no honest mutate step to drive adopt-mutate-scan-diff " +
			"with -- the same \"types that fight back\" shape as " +
			"aws_iam_group, just discovered via schema inspection instead " +
			"of a live API call.",
	},
	{Type: "aws_route", Source: "hashicorp/aws", Category: "network", Safety: FakeOnly,
		IdentityFields: []string{"id", "route_table_id"},
		Notes: "No arn/tags in the real schema. Fixture models " +
			"id/route_table_id/gateway_id and mutates gateway_id (a real " +
			"optional attribute -- which gateway a route points to -- " +
			"though in practice the real AWS provider often replaces " +
			"rather than in-place-modifies a route when its target " +
			"changes; noted here so the fixture's mutate step isn't " +
			"mistaken for a claim about real update-vs-replace behavior).",
		Implemented: true},
	{Type: "aws_internet_gateway", Source: "hashicorp/aws", Category: "network", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},
	{Type: "aws_nat_gateway", Source: "hashicorp/aws", Category: "network", Safety: FakeOnly,
		IdentityFields: []string{"id"},
		Notes:          "No arn in the real schema (unlike most network types). Fixture models id/tags/tags_all, mutates tags.",
		Implemented:    true},
	{Type: "aws_eip", Source: "hashicorp/aws", Category: "network", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},
	{Type: "aws_security_group", Source: "hashicorp/aws", Category: "network", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes: "The account's default security group would be a RealSafe " +
			"candidate (free, always exists) but its rules are shared, " +
			"live infrastructure other things may depend on -- tagging it " +
			"for a mutate-step test is a smaller blast radius than most " +
			"resources here, but still deferred rather than done " +
			"opportunistically. Covered generically here instead: fixture " +
			"models id/arn/tags/tags_all (real schema-verified), mutates " +
			"tags.",
		Implemented: true},
	{Type: "aws_security_group_rule", Source: "hashicorp/aws", Category: "network", Safety: FakeOnly,
		IdentityFields: []string{"id", "security_group_id"},
		Notes: "No arn/tags in the real schema (rules aren't independently " +
			"taggable, unlike the group they belong to). Fixture models " +
			"id/security_group_id/description and mutates description -- " +
			"a real, genuinely in-place-updatable optional attribute.",
		Implemented: true},
	{Type: "aws_lb", Source: "hashicorp/aws", Category: "network", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},
	{Type: "aws_lb_target_group", Source: "hashicorp/aws", Category: "network", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},
	{Type: "aws_lb_listener", Source: "hashicorp/aws", Category: "network", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},
	{Type: "aws_vpc_endpoint", Source: "hashicorp/aws", Category: "network", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},

	// --- iam ---
	{
		Type: "aws_iam_role", Source: "hashicorp/aws", Category: "iam", Safety: RealSafe,
		IdentityFields: []string{"id", "arn", "name"},
		Notes: "id and name are both the role name (not the ARN). Like " +
			"aws_s3_bucket, the lookup needs BOTH \"id\" and \"name\" set " +
			"({\"id\": \"<role-name>\", \"name\": \"<role-name>\"}) -- " +
			"\"name\" alone reads back null, empirically confirmed before " +
			"writing this note (not assumed from the S3 precedent). " +
			"Verified by adopting the account's real (pre-existing, " +
			"AWS-created) \"aws-codestar-service-role\".",
		LookupHint:  []string{"name"},
		Implemented: true,
	},
	{
		Type: "aws_iam_policy", Source: "hashicorp/aws", Category: "iam", Safety: RealSafe,
		IdentityFields: []string{"id", "arn"},
		Notes: "id IS the ARN (unlike role/user/group, which use the name) " +
			"-- lookup only needs {\"id\": \"<policy-arn>\"}. Verified by " +
			"creating a throwaway managed policy, testing it, and deleting " +
			"it (create+destroy per run, not an adopted pre-existing " +
			"resource like aws_iam_role).",
		Implemented: true,
	},
	{
		Type: "aws_iam_role_policy_attachment", Source: "hashicorp/aws", Category: "iam", Safety: FakeOnly,
		Notes: "PARKED, not hacked: real schema (GetProviderSchema) is " +
			"exactly {id, policy_arn (required), role (required)} -- a " +
			"pure join between a role and a policy, no optional or " +
			"computed field besides id at all. \"Changing\" which policy " +
			"is attached is a replace in AWS's own model, not an in-place " +
			"modify, so there's no honest mutate step -- same shape as " +
			"aws_route_table_association and aws_iam_group, discovered " +
			"via schema inspection this time rather than a live API call.",
	},
	{
		Type: "aws_iam_user", Source: "hashicorp/aws", Category: "iam", Safety: RealSafe,
		IdentityFields: []string{"id", "arn", "name"},
		Notes: "Same shape as aws_iam_role: id and name are both the user " +
			"name, and both must be set in the lookup (\"name\" alone reads " +
			"back null). Verified by creating a throwaway user, testing it, " +
			"and deleting it.",
		LookupHint:  []string{"name"},
		Implemented: true,
	},
	{
		Type: "aws_iam_group", Source: "hashicorp/aws", Category: "iam", Safety: FakeOnly,
		Notes: "PARKED, not hacked: IAM groups have no tagging API at all " +
			"(there is no \"aws iam tag-group\" -- confirmed empirically, " +
			"not assumed) and the aws_iam_group schema itself has nothing " +
			"else mutable-and-observable (path is immutable after create; " +
			"no tags field). The adopt half works fine ({\"id\": " +
			"\"<group-name>\", \"name\": \"<group-name>\"}, same shape as " +
			"role/user), but there is no real out-of-band mutation to test " +
			"drift detection against, so this stays fake-only until a " +
			"fakeprovider fixture stands in for the mutate step instead.",
	},
	{Type: "aws_iam_instance_profile", Source: "hashicorp/aws", Category: "iam", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn", "name"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},
	{Type: "aws_iam_openid_connect_provider", Source: "hashicorp/aws", Category: "iam", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn", "url"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},

	// --- storage ---
	{
		Type: "aws_s3_bucket", Source: "hashicorp/aws", Category: "storage", Safety: RealSafe,
		IdentityFields: []string{"id", "arn", "bucket"},
		Notes: "id and bucket are both the bucket name; lookup needs BOTH " +
			"set ({\"id\": \"<name>\", \"bucket\": \"<name>\"}) -- sending " +
			"only \"bucket\" reads back null (see STATE.md's 2026-07-10 " +
			"finding from Slice 1). Verified repeatedly across UBI-7/8/9 " +
			"against the real \"ubx-states\" bucket.",
		LookupHint:  []string{"bucket"},
		Implemented: true,
	},
	{Type: "aws_s3_bucket_policy", Source: "hashicorp/aws", Category: "storage", Safety: FakeOnly,
		IdentityFields: []string{"id", "bucket"},
		Notes: "No arn/tags (it's a sub-resource of the bucket, not " +
			"independently taggable). Fixture models id/bucket/policy and " +
			"mutates policy directly (the JSON policy document) -- the " +
			"actual real-world drift vector for this type.",
		Implemented: true},
	{Type: "aws_s3_bucket_versioning", Source: "hashicorp/aws", Category: "storage", Safety: FakeOnly,
		IdentityFields: []string{"id", "bucket"},
		Notes: "Real schema nests the mutable field inside a " +
			"versioning_configuration block (status: Enabled/Suspended); " +
			"the fixture models it as a flat \"status\" attribute instead " +
			"(id/bucket/status) since ubx's diff pipeline operates on " +
			"ReadResource's opaque JSON regardless of nesting -- what's " +
			"being conformance-tested here is the pipeline, not nested-" +
			"block wire fidelity (see the real, nested-block-modeling " +
			"provider/ctyvalue.go for where that already IS proven, " +
			"against a real provider).",
		Implemented: true},
	{Type: "aws_s3_bucket_public_access_block", Source: "hashicorp/aws", Category: "storage", Safety: FakeOnly,
		IdentityFields: []string{"id", "bucket"},
		Notes: "No arn/tags. Fixture models id/bucket/block_public_acls " +
			"and mutates block_public_acls -- a real, flat, optional " +
			"boolean attribute (modeled as a string \"true\"/\"false\", " +
			"see FakeOnly's doc comment on scalar type-fidelity not " +
			"mattering here).",
		Implemented: true},
	{Type: "aws_ebs_volume", Source: "hashicorp/aws", Category: "storage", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},
	{Type: "aws_efs_file_system", Source: "hashicorp/aws", Category: "storage", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},

	// --- database ---
	{Type: "aws_db_instance", Source: "hashicorp/aws", Category: "db", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},
	{Type: "aws_db_subnet_group", Source: "hashicorp/aws", Category: "db", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},
	{Type: "aws_rds_cluster", Source: "hashicorp/aws", Category: "db", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},
	{Type: "aws_elasticache_cluster", Source: "hashicorp/aws", Category: "db", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},
	{Type: "aws_dynamodb_table", Source: "hashicorp/aws", Category: "db", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},

	// --- dns / cdn / certs ---
	{Type: "aws_route53_zone", Source: "hashicorp/aws", Category: "dns", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes: "The account has no existing hosted zone, and creating one " +
			"solely for this suite would add a real recurring charge -- " +
			"stays FakeOnly until/unless a zone exists for another reason. " +
			"Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented: true},
	{Type: "aws_route53_record", Source: "hashicorp/aws", Category: "dns", Safety: FakeOnly,
		IdentityFields: []string{"id", "zone_id", "name"},
		Notes: "No arn/tags in the real schema (DNS records aren't " +
			"independently taggable). Fixture models id/zone_id/name/ttl " +
			"and mutates ttl -- a real, genuinely in-place-updatable " +
			"optional attribute (modeled as a string, see FakeOnly's doc " +
			"comment on scalar type-fidelity not mattering here).",
		Implemented: true},
	{Type: "aws_cloudfront_distribution", Source: "hashicorp/aws", Category: "dns", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},
	{Type: "aws_acm_certificate", Source: "hashicorp/aws", Category: "dns", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},

	// --- messaging / observability / secrets ---
	{
		Type: "aws_sqs_queue", Source: "hashicorp/aws", Category: "messaging", Safety: RealSafe,
		IdentityFields: []string{"id", "arn", "url"},
		Notes: "id IS the queue URL (not the ARN, though arn is also " +
			"surfaced) -- lookup only needs {\"id\": \"<queue-url>\"}. " +
			"Verified by creating a throwaway queue, testing it, and " +
			"deleting it. SQS has no per-queue monthly charge (pay per " +
			"request only), so create+destroy per run costs effectively " +
			"nothing.",
		Implemented: true,
	},
	{
		Type: "aws_sns_topic", Source: "hashicorp/aws", Category: "messaging", Safety: RealSafe,
		IdentityFields: []string{"id", "arn"},
		Notes: "id IS the topic ARN -- lookup only needs {\"id\": " +
			"\"<topic-arn>\"}, same pattern as aws_iam_policy. Verified by " +
			"creating a throwaway topic, testing it, and deleting it.",
		Implemented: true,
	},
	{Type: "aws_cloudwatch_log_group", Source: "hashicorp/aws", Category: "messaging", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},
	{Type: "aws_cloudwatch_metric_alarm", Source: "hashicorp/aws", Category: "messaging", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},
	{Type: "aws_secretsmanager_secret", Source: "hashicorp/aws", Category: "messaging", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},
	{Type: "aws_kms_key", Source: "hashicorp/aws", Category: "messaging", Safety: FakeOnly,
		IdentityFields: []string{"id", "arn"},
		Notes:          "Fixture models id/arn/tags/tags_all, mutates tags.",
		Implemented:    true},

	// --- hashicorp/google (UBI-21 Stage 1) ---
	//
	// Session bootstrapping, same posture as UBI-9 session 1's own AWS
	// seeding (see docs/plan.md §M1-2 GCP resource type list): every entry
	// below is Safety: FakeOnly, Implemented: false. IdentityFields come
	// from a real GetProviderSchema call against the acquired
	// hashicorp/google 7.40.0 binary (free, no credentials, no live GCP
	// API round trip -- see conformance/gcp_provider_test.go), not
	// guessed. Classifying which of these are cheap enough to be
	// RealSafe, writing fakeprovider fixtures, and finding any that "fight
	// back" the way aws_iam_group/aws_route_table_association did is
	// Stage 2 work, done against a real account rather than guessed here.
	{Type: "google_compute_instance", Source: "hashicorp/google", Category: "compute", Safety: FakeOnly,
		IdentityFields: []string{"id", "self_link", "name"}, Notes: gcpSeedNote},
	{Type: "google_compute_instance_template", Source: "hashicorp/google", Category: "compute", Safety: FakeOnly,
		IdentityFields: []string{"id", "self_link", "name"}, Notes: gcpSeedNote},
	{Type: "google_container_cluster", Source: "hashicorp/google", Category: "compute", Safety: FakeOnly,
		IdentityFields: []string{"id", "self_link", "name"}, Notes: gcpSeedNote},
	{Type: "google_cloudfunctions2_function", Source: "hashicorp/google", Category: "compute", Safety: FakeOnly,
		IdentityFields: []string{"id", "name"}, Notes: gcpSeedNote},
	{Type: "google_cloud_run_v2_service", Source: "hashicorp/google", Category: "compute", Safety: FakeOnly,
		IdentityFields: []string{"id", "name", "uid"}, Notes: gcpSeedNote},
	{Type: "google_cloud_run_v2_job", Source: "hashicorp/google", Category: "compute", Safety: FakeOnly,
		IdentityFields: []string{"id", "name", "uid"}, Notes: gcpSeedNote},

	{Type: "google_compute_network", Source: "hashicorp/google", Category: "network", Safety: FakeOnly,
		IdentityFields: []string{"id", "self_link", "name"}, Notes: gcpSeedNote},
	{Type: "google_compute_subnetwork", Source: "hashicorp/google", Category: "network", Safety: FakeOnly,
		IdentityFields: []string{"id", "self_link", "name"}, Notes: gcpSeedNote},
	{Type: "google_compute_route", Source: "hashicorp/google", Category: "network", Safety: FakeOnly,
		IdentityFields: []string{"id", "self_link", "name"}, Notes: gcpSeedNote},
	{Type: "google_compute_router", Source: "hashicorp/google", Category: "network", Safety: FakeOnly,
		IdentityFields: []string{"id", "self_link", "name"}, Notes: gcpSeedNote},
	{Type: "google_compute_router_nat", Source: "hashicorp/google", Category: "network", Safety: FakeOnly,
		IdentityFields: []string{"id", "name"}, Notes: gcpSeedNote},
	{Type: "google_compute_firewall", Source: "hashicorp/google", Category: "network", Safety: FakeOnly,
		IdentityFields: []string{"id", "self_link", "name"}, Notes: gcpSeedNote},
	{Type: "google_compute_address", Source: "hashicorp/google", Category: "network", Safety: FakeOnly,
		IdentityFields: []string{"id", "self_link", "name"}, Notes: gcpSeedNote},
	{Type: "google_compute_global_address", Source: "hashicorp/google", Category: "network", Safety: FakeOnly,
		IdentityFields: []string{"id", "self_link", "name"}, Notes: gcpSeedNote},
	{Type: "google_compute_forwarding_rule", Source: "hashicorp/google", Category: "network", Safety: FakeOnly,
		IdentityFields: []string{"id", "self_link", "name"}, Notes: gcpSeedNote},
	{Type: "google_compute_backend_service", Source: "hashicorp/google", Category: "network", Safety: FakeOnly,
		IdentityFields: []string{"id", "self_link", "name"}, Notes: gcpSeedNote},

	{
		Type: "google_service_account", Source: "hashicorp/google", Category: "iam", Safety: RealSafe,
		IdentityFields: []string{"id", "email", "name"},
		Notes: "id (the full \"projects/<project>/serviceAccounts/<email>\" path) alone " +
			"is sufficient -- no companion field needed, unlike google_storage_bucket/" +
			"google_pubsub_topic/google_secret_manager_secret below. Verified by creating " +
			"a throwaway service account, testing it, and deleting it. GCP IAM's own " +
			"read-after-write consistency lags the write itself -- a live displayName " +
			"update took up to several minutes to become visible to the provider's own " +
			"ReadResource call, well past what gcloud's own describe reported as already " +
			"consistent (see conformance/gcp_live_test.go's own polling wait). Not the " +
			"same shape of surprise CloudTrail's delivery lag was (a logging/audit system), " +
			"but the same genre: a real API's real consistency window, discovered by " +
			"actually hitting it, not assumed.",
		Implemented: true,
	},
	{Type: "google_service_account_key", Source: "hashicorp/google", Category: "iam", Safety: FakeOnly,
		IdentityFields: []string{"id", "name"}, Notes: gcpSeedNote},
	{Type: "google_project_iam_member", Source: "hashicorp/google", Category: "iam", Safety: FakeOnly,
		IdentityFields: []string{"id"}, Notes: gcpSeedNote},
	{Type: "google_project_iam_binding", Source: "hashicorp/google", Category: "iam", Safety: FakeOnly,
		IdentityFields: []string{"id"}, Notes: gcpSeedNote},
	{
		Type: "google_project_iam_custom_role", Source: "hashicorp/google", Category: "iam", Safety: RealSafe,
		IdentityFields: []string{"id", "name"},
		Notes: "id (the full \"projects/<project>/roles/<role_id>\" path) alone is " +
			"sufficient, same as google_service_account -- no companion field needed. " +
			"Verified by creating a throwaway custom role, testing it, and deleting it.",
		Implemented: true,
	},

	{
		Type: "google_storage_bucket", Source: "hashicorp/google", Category: "storage", Safety: RealSafe,
		IdentityFields: []string{"id", "self_link", "name"},
		Notes: "id and name are both the bucket name, and BOTH must be set together -- " +
			"unlike every AWS LookupHint entry, NEITHER alone is sufficient here. " +
			"\"id\" alone errors outright (\"Storage Bucket \\\"\\\": googleapi: Error " +
			"400\"); \"name\" alone reads back null (ErrResourceUnreadable) instead of " +
			"erroring -- two different failure shapes for the two different single-" +
			"field mistakes. Deliberately NOT given a LookupHint / promoted to " +
			"core/lookuphints: that mechanism's shipped message hardcodes 'make sure " +
			"\"id\" is included' (correct for every AWS entry, where id alone always " +
			"works and the OTHER field is the trap) -- applying it here would tell a " +
			"user whose lookup already has \"id\" (and is missing \"name\") to add the " +
			"one field they already have. Generalizing the hint mechanism to express " +
			"\"both required together\" distinctly from \"id always works alone\" is " +
			"follow-up work, not done under time pressure here (see STATE.md). " +
			"Verified by creating a throwaway bucket, testing it, and deleting it.",
		Implemented: true,
	},
	{Type: "google_storage_bucket_iam_member", Source: "hashicorp/google", Category: "storage", Safety: FakeOnly,
		IdentityFields: []string{"id"}, Notes: gcpSeedNote},
	{Type: "google_storage_bucket_object", Source: "hashicorp/google", Category: "storage", Safety: FakeOnly,
		IdentityFields: []string{"id", "self_link", "name"}, Notes: gcpSeedNote},
	{Type: "google_compute_disk", Source: "hashicorp/google", Category: "storage", Safety: FakeOnly,
		IdentityFields: []string{"id", "self_link", "name"}, Notes: gcpSeedNote},
	{Type: "google_filestore_instance", Source: "hashicorp/google", Category: "storage", Safety: FakeOnly,
		IdentityFields: []string{"id", "name"}, Notes: gcpSeedNote},

	{Type: "google_sql_database_instance", Source: "hashicorp/google", Category: "db", Safety: FakeOnly,
		IdentityFields: []string{"id", "self_link", "name"}, Notes: gcpSeedNote},
	{Type: "google_sql_database", Source: "hashicorp/google", Category: "db", Safety: FakeOnly,
		IdentityFields: []string{"id", "self_link", "name"}, Notes: gcpSeedNote},
	{Type: "google_sql_user", Source: "hashicorp/google", Category: "db", Safety: FakeOnly,
		IdentityFields: []string{"id", "name"}, Notes: gcpSeedNote},
	{Type: "google_spanner_instance", Source: "hashicorp/google", Category: "db", Safety: FakeOnly,
		IdentityFields: []string{"id", "name"}, Notes: gcpSeedNote},
	{Type: "google_firestore_database", Source: "hashicorp/google", Category: "db", Safety: FakeOnly,
		IdentityFields: []string{"id", "name", "uid"}, Notes: gcpSeedNote},

	{Type: "google_dns_managed_zone", Source: "hashicorp/google", Category: "dns", Safety: FakeOnly,
		IdentityFields: []string{"id", "name"}, Notes: gcpSeedNote},
	{Type: "google_dns_record_set", Source: "hashicorp/google", Category: "dns", Safety: FakeOnly,
		IdentityFields: []string{"id", "name"}, Notes: gcpSeedNote},
	{Type: "google_compute_ssl_certificate", Source: "hashicorp/google", Category: "dns", Safety: FakeOnly,
		IdentityFields: []string{"id", "self_link", "name"}, Notes: gcpSeedNote},

	{
		Type: "google_pubsub_topic", Source: "hashicorp/google", Category: "messaging", Safety: RealSafe,
		IdentityFields: []string{"id", "name"},
		Notes: "A materially more dangerous shape of lookup mistake than any AWS entry: " +
			"\"id\" alone (the full \"projects/<project>/topics/<name>\" path) does NOT " +
			"error and does NOT read back null -- ReadResource succeeds, but the " +
			"resource's own \"name\" attribute comes back as an empty string even " +
			"though the topic genuinely has one. core.ErrResourceUnreadable never " +
			"fires (nothing is nil/null), so this ledgers a real, silently-incomplete " +
			"proposal rather than erroring -- the teaching-error mechanism (UBI-20) " +
			"can't help here at all, since it only ever engages on an actual read " +
			"failure. \"id\" AND \"name\" together read back correctly. Deliberately " +
			"NOT given a LookupHint for the same reason as google_storage_bucket " +
			"above, plus this one: there is no error to attach a hint's message to in " +
			"the first place. See STATE.md for the fuller writeup of this gap. " +
			"Verified by creating a throwaway topic, testing it, and deleting it.",
		Implemented: true,
	},
	{Type: "google_pubsub_subscription", Source: "hashicorp/google", Category: "messaging", Safety: FakeOnly,
		IdentityFields: []string{"id", "name"}, Notes: gcpSeedNote},
	{Type: "google_logging_metric", Source: "hashicorp/google", Category: "messaging", Safety: FakeOnly,
		IdentityFields: []string{"id", "name"}, Notes: gcpSeedNote},
	{Type: "google_monitoring_alert_policy", Source: "hashicorp/google", Category: "messaging", Safety: FakeOnly,
		IdentityFields: []string{"id", "name"}, Notes: gcpSeedNote},
	{
		Type: "google_secret_manager_secret", Source: "hashicorp/google", Category: "messaging", Safety: RealSafe,
		IdentityFields: []string{"id", "name"},
		Notes: "Same silently-incomplete-read shape as google_pubsub_topic above: " +
			"\"id\" alone (the full \"projects/<project>/secrets/<name>\" path) reads " +
			"back successfully but with \"name\": \"\" and \"secret_id\": null -- no " +
			"error, ErrResourceUnreadable never fires. \"id\" and \"secret_id\" (this " +
			"type's own natural-key attribute, not \"name\") together read back " +
			"correctly. Deliberately not given a LookupHint, same reasoning as " +
			"google_pubsub_topic. Verified by creating a throwaway secret, testing " +
			"it, and deleting it.",
		Implemented: true,
	},
	{Type: "google_kms_crypto_key", Source: "hashicorp/google", Category: "messaging", Safety: FakeOnly,
		IdentityFields: []string{"id", "name"}, Notes: gcpSeedNote},
}

// gcpSeedNote is the shared "not yet worked through" note for UBI-21
// Stage 1's GCP entries -- honest about what's actually been verified
// (IdentityFields, against the real schema) versus what hasn't
// (everything else), the same "documented as parked, not silently
// skipped" discipline UBI-9's own PARKED entries follow, just for "not
// started yet" rather than "can't be conformance-tested at all."
const gcpSeedNote = "Seeded for UBI-21 Stage 1 (docs/plan.md §M1-2 GCP resource type list): " +
	"IdentityFields verified against the real hashicorp/google 7.40.0 schema " +
	"(GetProviderSchema, free, no credentials, no live GCP API call). Not yet " +
	"implemented -- no fakeprovider fixture, no live-account verification, " +
	"Safety/LookupHint not yet classified. Stage 2 (a real GCP account) works " +
	"through this list in batches, the same way UBI-9 worked through AWS's."

// ByType returns the registry entry for a (source, type) pair, or nil if
// that combination isn't tracked at all (UBI-21: keyed by source+type,
// not type alone — see TypeSpec.Source).
func ByType(source, t string) *TypeSpec {
	for i := range Registry {
		if Registry[i].Source == source && Registry[i].Type == t {
			return &Registry[i]
		}
	}
	return nil
}

// MustMarshal is a small test-fixture convenience: marshal v to JSON,
// panicking on error (only ever called with values the caller controls
// entirely, so a marshal error here means a fixture bug, not bad input).
func MustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
