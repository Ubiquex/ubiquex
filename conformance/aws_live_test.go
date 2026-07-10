package conformance

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/provider"
)

// awsProviderVersion is pinned, not "latest" (see docs/architecture.md —
// provider acquisition, UBI-8: explicit version pins only). Bump
// deliberately, not automatically, if a newer AWS provider release needs
// conformance coverage.
const awsProviderVersion = "6.54.0"

// realAWSProviderPath acquires (or reuses the cached, already-verified)
// real AWS provider binary via provider.Acquire — dogfooding UBI-8 rather
// than a manually downloaded scratch binary.
func realAWSProviderPath(t *testing.T) string {
	t.Helper()
	src, err := provider.ParseSource("hashicorp/aws")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	result, err := provider.Acquire(ctx, src, awsProviderVersion)
	if err != nil {
		t.Fatalf("acquire real aws provider: %v", err)
	}
	return result.Path
}

func runAWS(t *testing.T, args ...string) {
	t.Helper()
	out, err := exec.Command("aws", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("aws %v: %v\n%s", args, err, out)
	}
}

// requireDefaultVPCID discovers the account's real default VPC. Every AWS
// account has one unless it's been deliberately deleted; skips (doesn't
// fail) if not, since that's an account-configuration fact, not a ubx bug.
func requireDefaultVPCID(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("aws", "ec2", "describe-vpcs",
		"--filters", "Name=isDefault,Values=true",
		"--query", "Vpcs[0].VpcId", "--output", "text").Output()
	if err != nil {
		t.Fatalf("discover default VPC: %v", err)
	}
	id := strings.TrimSpace(string(out))
	if id == "" || id == "None" {
		t.Skip("no default VPC in this account")
	}
	return id
}

// TestConformance_AWSS3Bucket adopts the real "ubx-states" bucket (see
// docs/plan.md, UBI-7/8's real-world verification) and re-confirms the
// same drift-detection flow through the conformance registry/harness
// rather than a one-off manual script.
func TestConformance_AWSS3Bucket(t *testing.T) {
	RequireLive(t)
	providerPath := realAWSProviderPath(t)
	const bucket = "ubx-states"

	RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
		ProviderPath:   providerPath,
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "aws_s3_bucket", Name: bucket},
		Lookup:         MustMarshal(map[string]string{"id": bucket, "bucket": bucket}),
		ProviderConfig: MustMarshal(map[string]string{"region": "us-east-1"}),
		Mutate: func(t *testing.T) {
			runAWS(t, "s3api", "put-bucket-tagging", "--bucket", bucket,
				"--tagging", "TagSet=[{Key=ubx-conformance,Value=s3}]")
			t.Cleanup(func() { runAWS(t, "s3api", "delete-bucket-tagging", "--bucket", bucket) })
		},
	})
}

// TestConformance_AWSIAMRole adopts the account's real, pre-existing
// (AWS-created) "aws-codestar-service-role" — read-only except for the
// tag mutation step, which is undone in cleanup.
func TestConformance_AWSIAMRole(t *testing.T) {
	RequireLive(t)
	providerPath := realAWSProviderPath(t)
	const role = "aws-codestar-service-role"

	RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
		ProviderPath:   providerPath,
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "aws_iam_role", Name: role},
		Lookup:         MustMarshal(map[string]string{"id": role, "name": role}),
		ProviderConfig: MustMarshal(map[string]string{"region": "us-east-1"}),
		Mutate: func(t *testing.T) {
			runAWS(t, "iam", "tag-role", "--role-name", role, "--tags", "Key=ubx-conformance,Value=iam")
			t.Cleanup(func() { runAWS(t, "iam", "untag-role", "--role-name", role, "--tag-keys", "ubx-conformance") })
		},
	})
}

// TestConformance_AWSVPC adopts the account's real default VPC.
func TestConformance_AWSVPC(t *testing.T) {
	RequireLive(t)
	providerPath := realAWSProviderPath(t)
	vpcID := requireDefaultVPCID(t)

	RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
		ProviderPath:   providerPath,
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "aws_vpc", Name: vpcID},
		Lookup:         MustMarshal(map[string]string{"id": vpcID}),
		ProviderConfig: MustMarshal(map[string]string{"region": "us-east-1"}),
		Mutate: func(t *testing.T) {
			runAWS(t, "ec2", "create-tags", "--resources", vpcID, "--tags", "Key=ubx-conformance,Value=vpc")
			t.Cleanup(func() { runAWS(t, "ec2", "delete-tags", "--resources", vpcID, "--tags", "Key=ubx-conformance") })
		},
	})
}
