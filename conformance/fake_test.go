package conformance

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex-cli/core"
)

// fakeProviderBinary is built once (see TestMain) and reused by every
// FakeOnly conformance case below — the fixture's schema/behavior is driven
// per-case entirely through env vars (see provider/internal/fakeprovider's
// package doc comment), never through a different binary.
var fakeProviderBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ubx-conformance-fakeprovider")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	fakeProviderBinary = filepath.Join(dir, "fakeprovider")
	build := exec.Command("go", "build", "-o", fakeProviderBinary, "../provider/internal/fakeprovider")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic("building fakeprovider fixture: " + err.Error())
	}

	os.Exit(m.Run())
}

// fakeConformanceCase describes one FakeOnly type's fixture configuration:
// which attributes (a real, schema-verified subset — see cmd/schemadump and
// conformance/registry.go's Notes for each type) the fixture should serve,
// and which one Mutate changes to simulate an out-of-band drift.
type fakeConformanceCase struct {
	Type        string
	Attrs       []string
	MutateAttr  string
	MutateValue string
}

// stdAttrs/stdMutate* model the overwhelmingly common real shape found via
// cmd/schemadump across every remaining FakeOnly type: id + arn identity,
// tags/tags_all as the mutable, observable field — the same "someone tagged
// it in the console" drift scenario the RealSafe types (batches 1-2)
// exercise for real, just simulated here instead of live.
var stdAttrs = []string{"id", "arn", "tags", "tags_all"}

const (
	stdMutateAttr  = "tags"
	stdMutateValue = "ubx-conformance=fake"
)

func stdCase(t string) fakeConformanceCase {
	return fakeConformanceCase{Type: t, Attrs: stdAttrs, MutateAttr: stdMutateAttr, MutateValue: stdMutateValue}
}

// fakeConformanceCases is UBI-9 batch 3's fake-only worklist: every
// FakeOnly, Implemented type in conformance/registry.go that isn't parked.
// Special-shaped types (no arn/tags in their real schema) get their own
// entry instead of stdCase — see the matching Notes in registry.go for why.
var fakeConformanceCases = []fakeConformanceCase{
	// compute
	stdCase("aws_instance"),
	stdCase("aws_launch_template"),
	stdCase("aws_autoscaling_group"),
	stdCase("aws_ecs_cluster"),
	stdCase("aws_ecs_service"),
	stdCase("aws_ecs_task_definition"),
	stdCase("aws_eks_cluster"),
	stdCase("aws_eks_node_group"),
	stdCase("aws_lambda_function"),

	// network
	stdCase("aws_subnet"),
	stdCase("aws_route_table"),
	{Type: "aws_route", Attrs: []string{"id", "route_table_id", "gateway_id"}, MutateAttr: "gateway_id", MutateValue: "igw-mutated"},
	stdCase("aws_internet_gateway"),
	{Type: "aws_nat_gateway", Attrs: []string{"id", "tags", "tags_all"}, MutateAttr: stdMutateAttr, MutateValue: stdMutateValue},
	stdCase("aws_eip"),
	stdCase("aws_security_group"),
	{Type: "aws_security_group_rule", Attrs: []string{"id", "security_group_id", "description"}, MutateAttr: "description", MutateValue: "mutated by conformance test"},
	stdCase("aws_lb"),
	stdCase("aws_lb_target_group"),
	stdCase("aws_lb_listener"),
	stdCase("aws_vpc_endpoint"),

	// iam
	stdCase("aws_iam_instance_profile"),
	stdCase("aws_iam_openid_connect_provider"),

	// storage
	{Type: "aws_s3_bucket_policy", Attrs: []string{"id", "bucket", "policy"}, MutateAttr: "policy", MutateValue: `{"Version":"2012-10-17","Statement":[]}`},
	{Type: "aws_s3_bucket_versioning", Attrs: []string{"id", "bucket", "status"}, MutateAttr: "status", MutateValue: "Suspended"},
	{Type: "aws_s3_bucket_public_access_block", Attrs: []string{"id", "bucket", "block_public_acls"}, MutateAttr: "block_public_acls", MutateValue: "false"},
	stdCase("aws_ebs_volume"),
	stdCase("aws_efs_file_system"),

	// db
	stdCase("aws_db_instance"),
	stdCase("aws_db_subnet_group"),
	stdCase("aws_rds_cluster"),
	stdCase("aws_elasticache_cluster"),
	stdCase("aws_dynamodb_table"),

	// dns / cdn / certs
	stdCase("aws_route53_zone"),
	{Type: "aws_route53_record", Attrs: []string{"id", "zone_id", "name", "ttl"}, MutateAttr: "ttl", MutateValue: "600"},
	stdCase("aws_cloudfront_distribution"),
	stdCase("aws_acm_certificate"),

	// messaging / observability / secrets
	stdCase("aws_cloudwatch_log_group"),
	stdCase("aws_cloudwatch_metric_alarm"),
	stdCase("aws_secretsmanager_secret"),
	stdCase("aws_kms_key"),
}

// TestConformance_FakeOnly runs the identical adopt→mutate→scan-diff
// sequence RealSafe types run (see aws_live_test.go), against the
// fakeprovider fixture instead of the real account — no UBX_CONFORMANCE_LIVE
// gate needed, since nothing here ever touches real AWS. Table-driven for
// the same reason conformance/registry.go itself is: ~40 near-identical
// per-type tests are a data problem, not 40 hand-written Go functions.
func TestConformance_FakeOnly(t *testing.T) {
	for _, tc := range fakeConformanceCases {
		t.Run(tc.Type, func(t *testing.T) {
			RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
				ProviderPath: fakeProviderBinary,
				Stack:        "conformance",
				Address:      core.Address{Stack: "conformance", Type: tc.Type, Name: "fixture"},
				Lookup:       MustMarshal(map[string]string{"id": tc.Type + "-fixture"}),
				ProviderEnv: []string{
					"FAKEPROVIDER_MODE=conformance-v6",
					"FAKEPROVIDER_RESOURCE_TYPE=" + tc.Type,
					"FAKEPROVIDER_ATTRS=" + strings.Join(tc.Attrs, ","),
				},
				Mutate: func(t *testing.T) {
					t.Setenv("FAKEPROVIDER_MUTATE_ATTR", tc.MutateAttr)
					t.Setenv("FAKEPROVIDER_MUTATE_VALUE", tc.MutateValue)
				},
			})
		})
	}
}

// TestFakeConformanceCases_MatchRegistry cross-checks the worklist above
// against conformance/registry.go: every case here must correspond to a
// FakeOnly, Implemented registry entry (so the fixture and the registry
// never silently drift apart), and every FakeOnly Implemented entry must
// have a case here (so nothing claims fixture-verified without actually
// being exercised by a test).
func TestFakeConformanceCases_MatchRegistry(t *testing.T) {
	cased := make(map[string]bool, len(fakeConformanceCases))
	for _, tc := range fakeConformanceCases {
		cased[tc.Type] = true
		spec := ByType(tc.Type)
		if spec == nil {
			t.Errorf("fakeConformanceCases has %q, not in Registry at all", tc.Type)
			continue
		}
		if spec.Safety != FakeOnly {
			t.Errorf("%s: fakeConformanceCases has it, but Registry says Safety=%v", tc.Type, spec.Safety)
		}
		if !spec.Implemented {
			t.Errorf("%s: fakeConformanceCases has it, but Registry says Implemented=false", tc.Type)
		}
	}
	for _, spec := range Registry {
		if spec.Safety == FakeOnly && spec.Implemented && !cased[spec.Type] {
			t.Errorf("%s: Registry says FakeOnly+Implemented, but no fakeConformanceCases entry exercises it", spec.Type)
		}
	}
}
