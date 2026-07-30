package conformance

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/provider"
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

// runAWSOutput is runAWS but returns stdout (trimmed) — for commands whose
// result (a queue URL, an ARN, ...) the test needs to use afterward.
func runAWSOutput(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("aws", args...).Output()
	if err != nil {
		t.Fatalf("aws %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// uniqueName gives each create+destroy-per-run conformance fixture (batch
// 2 on: types with no existing real resource to adopt, unlike batch 1's
// aws_s3_bucket/aws_iam_role/aws_vpc) a name that won't collide across
// repeated or concurrent runs.
func uniqueName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
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

// TestConformance_AWSSQSQueue creates a throwaway queue (free — SQS has no
// per-queue monthly charge, only per-request), tests it, and deletes it.
// Batch 2's first create+destroy-per-run type, unlike batch 1's
// adopt-something-that-already-exists pattern.
func TestConformance_AWSSQSQueue(t *testing.T) {
	RequireLive(t)
	providerPath := realAWSProviderPath(t)
	name := uniqueName("ubx-conformance-sqs")

	queueURL := runAWSOutput(t, "sqs", "create-queue", "--queue-name", name, "--query", "QueueUrl", "--output", "text")
	t.Cleanup(func() { runAWS(t, "sqs", "delete-queue", "--queue-url", queueURL) })

	RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
		ProviderPath:   providerPath,
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "aws_sqs_queue", Name: name},
		Lookup:         MustMarshal(map[string]string{"id": queueURL}),
		ProviderConfig: MustMarshal(map[string]string{"region": "us-east-1"}),
		Mutate: func(t *testing.T) {
			runAWS(t, "sqs", "tag-queue", "--queue-url", queueURL, "--tags", "ubx-conformance=sqs")
			t.Cleanup(func() { runAWS(t, "sqs", "untag-queue", "--queue-url", queueURL, "--tag-keys", "ubx-conformance") })
		},
	})
}

// TestConformance_AWSSNSTopic creates a throwaway topic (free), tests it,
// and deletes it.
func TestConformance_AWSSNSTopic(t *testing.T) {
	RequireLive(t)
	providerPath := realAWSProviderPath(t)
	name := uniqueName("ubx-conformance-sns")

	arn := runAWSOutput(t, "sns", "create-topic", "--name", name, "--query", "TopicArn", "--output", "text")
	t.Cleanup(func() { runAWS(t, "sns", "delete-topic", "--topic-arn", arn) })

	RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
		ProviderPath:   providerPath,
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "aws_sns_topic", Name: name},
		Lookup:         MustMarshal(map[string]string{"id": arn}),
		ProviderConfig: MustMarshal(map[string]string{"region": "us-east-1"}),
		Mutate: func(t *testing.T) {
			runAWS(t, "sns", "tag-resource", "--resource-arn", arn, "--tags", "Key=ubx-conformance,Value=sns")
			t.Cleanup(func() { runAWS(t, "sns", "untag-resource", "--resource-arn", arn, "--tag-keys", "ubx-conformance") })
		},
	})
}

// TestConformance_AWSIAMPolicy creates a throwaway managed policy (free),
// tests it, and deletes it.
func TestConformance_AWSIAMPolicy(t *testing.T) {
	RequireLive(t)
	providerPath := realAWSProviderPath(t)
	name := uniqueName("ubx-conformance-policy")
	doc := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`

	arn := runAWSOutput(t, "iam", "create-policy", "--policy-name", name,
		"--policy-document", doc, "--query", "Policy.Arn", "--output", "text")
	t.Cleanup(func() { runAWS(t, "iam", "delete-policy", "--policy-arn", arn) })

	RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
		ProviderPath:   providerPath,
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "aws_iam_policy", Name: name},
		Lookup:         MustMarshal(map[string]string{"id": arn}),
		ProviderConfig: MustMarshal(map[string]string{"region": "us-east-1"}),
		Mutate: func(t *testing.T) {
			runAWS(t, "iam", "tag-policy", "--policy-arn", arn, "--tags", "Key=ubx-conformance,Value=policy")
			t.Cleanup(func() { runAWS(t, "iam", "untag-policy", "--policy-arn", arn, "--tag-keys", "ubx-conformance") })
		},
	})
}

// TestConformance_AWSIAMUser creates a throwaway IAM user (free), tests
// it, and deletes it.
func TestConformance_AWSIAMUser(t *testing.T) {
	RequireLive(t)
	providerPath := realAWSProviderPath(t)
	name := uniqueName("ubx-conformance-user")

	runAWS(t, "iam", "create-user", "--user-name", name)
	t.Cleanup(func() { runAWS(t, "iam", "delete-user", "--user-name", name) })

	RunAdoptMutateScanDiff(t, AdoptMutateScanDiffConfig{
		ProviderPath:   providerPath,
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "aws_iam_user", Name: name},
		Lookup:         MustMarshal(map[string]string{"id": name, "name": name}),
		ProviderConfig: MustMarshal(map[string]string{"region": "us-east-1"}),
		Mutate: func(t *testing.T) {
			runAWS(t, "iam", "tag-user", "--user-name", name, "--tags", "Key=ubx-conformance,Value=user")
			t.Cleanup(func() { runAWS(t, "iam", "untag-user", "--user-name", name, "--tag-keys", "ubx-conformance") })
		},
	})
}
