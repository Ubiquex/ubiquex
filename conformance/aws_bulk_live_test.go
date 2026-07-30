package conformance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/core"
)

// TestBulkLiveProbes_AWSFreeTierBatch is UBI-50's own first real
// bulk-live-tier run (docs/conformance-harness.md — "Live tier...
// cost-aware: free/cheap types bulk-run"). Batch policy, decided before
// anything was created: free-tier types ONLY, and only the four
// create+destroy-per-run types this project's own existing conformance
// suite already established as safe, self-contained, and disposable
// (aws_sqs_queue, aws_sns_topic, aws_iam_policy, aws_iam_user) —
// deliberately excluding the three ADOPT-a-pre-existing-resource types
// (aws_s3_bucket/aws_iam_role/aws_vpc, TestConformance_AWSS3Bucket and
// siblings, above) from this bulk run: those mutate real, pre-existing
// account infrastructure (the account's own default VPC, a real
// AWS-created IAM role) rather than a throwaway resource created for
// this run alone, a real distinction worth keeping even though both
// classes are equally free. No priced type is included in this batch at
// all — the sanctioned default; anything priced would require stopping
// to ask first, per this session's own explicit instruction, and
// nothing here does.
//
// Runs all three non-destroy live probes (identity-shape, sensitive-echo,
// drift-detectability) against each of the four types — never probe 3
// (destroy honesty stays hermetic-only this session, a separate,
// explicit decision — see destroy_probe.go's own doc comment and
// STATE.md). Every created resource is destroyed in its own t.Cleanup;
// a final sweep (TestSweep_AWSBulkLiveRun_NoLeaks, below) proves nothing
// was left behind after the whole batch completes.
func TestBulkLiveProbes_AWSFreeTierBatch(t *testing.T) {
	RequireLive(t)
	providerPath := realAWSProviderPath(t)
	regionConfig := MustMarshal(map[string]string{"region": "us-east-1"})

	t.Run("aws_sqs_queue", func(t *testing.T) {
		name := uniqueName("ubx-conformance-bulk-sqs")
		queueURL := runAWSOutput(t, "sqs", "create-queue", "--queue-name", name, "--query", "QueueUrl", "--output", "text")
		t.Cleanup(func() { runAWS(t, "sqs", "delete-queue", "--queue-url", queueURL) })

		cfg := LiveReadProbeConfig{
			ProviderPath: providerPath, Source: "hashicorp/aws", Version: awsProviderVersion,
			Stack: "conformance", Address: core.Address{Stack: "conformance", Type: "aws_sqs_queue", Name: name},
			ProviderConfig: regionConfig,
		}
		lookup := MustMarshal(map[string]string{"id": queueURL})
		identityFinding, identityErr := ProbeIdentityShapeLive(context.Background(), cfg, lookup, lookup)
		requireNoFinding(t, "identity-shape", identityFinding, identityErr)

		marker := "ubx-marker-" + name
		runAWS(t, "sqs", "tag-queue", "--queue-url", queueURL, "--tags", "ubx-marker="+marker)
		t.Cleanup(func() { runAWS(t, "sqs", "untag-queue", "--queue-url", queueURL, "--tag-keys", "ubx-marker") })
		// tags_all included alongside tags -- the same real AWS dual-tag
		// convention (tags_all = tags merged with provider-level
		// default_tags) found and corrected for aws_sns_topic in the
		// prior session; confirmed here to apply to aws_sqs_queue too,
		// not assumed.
		echoFinding, echoErr := ProbeSensitiveEchoLive(context.Background(), cfg, lookup, marker, "tags", "tags_all")
		requireNoFinding(t, "sensitive-echo", echoFinding, echoErr)

		requireNoDriftFinding(t, cfg, lookup)
	})

	t.Run("aws_sns_topic", func(t *testing.T) {
		name := uniqueName("ubx-conformance-bulk-sns")
		arn := runAWSOutput(t, "sns", "create-topic", "--name", name, "--query", "TopicArn", "--output", "text")
		t.Cleanup(func() { runAWS(t, "sns", "delete-topic", "--topic-arn", arn) })

		cfg := LiveReadProbeConfig{
			ProviderPath: providerPath, Source: "hashicorp/aws", Version: awsProviderVersion,
			Stack: "conformance", Address: core.Address{Stack: "conformance", Type: "aws_sns_topic", Name: name},
			ProviderConfig: regionConfig,
		}
		lookup := MustMarshal(map[string]string{"id": arn})
		identityFinding, identityErr := ProbeIdentityShapeLive(context.Background(), cfg, lookup, lookup)
		requireNoFinding(t, "identity-shape", identityFinding, identityErr)

		marker := "ubx-marker-" + name
		runAWS(t, "sns", "tag-resource", "--resource-arn", arn, "--tags", "Key=ubx-marker,Value="+marker)
		t.Cleanup(func() { runAWS(t, "sns", "untag-resource", "--resource-arn", arn, "--tag-keys", "ubx-marker") })
		echoFinding, echoErr := ProbeSensitiveEchoLive(context.Background(), cfg, lookup, marker, "tags", "tags_all")
		requireNoFinding(t, "sensitive-echo", echoFinding, echoErr)

		requireNoDriftFinding(t, cfg, lookup)
	})

	t.Run("aws_iam_policy", func(t *testing.T) {
		name := uniqueName("ubx-conformance-bulk-policy")
		doc := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`
		arn := runAWSOutput(t, "iam", "create-policy", "--policy-name", name, "--policy-document", doc, "--query", "Policy.Arn", "--output", "text")
		t.Cleanup(func() { runAWS(t, "iam", "delete-policy", "--policy-arn", arn) })

		cfg := LiveReadProbeConfig{
			ProviderPath: providerPath, Source: "hashicorp/aws", Version: awsProviderVersion,
			Stack: "conformance", Address: core.Address{Stack: "conformance", Type: "aws_iam_policy", Name: name},
			ProviderConfig: regionConfig,
		}
		lookup := MustMarshal(map[string]string{"id": arn})
		identityFinding, identityErr := ProbeIdentityShapeLive(context.Background(), cfg, lookup, lookup)
		requireNoFinding(t, "identity-shape", identityFinding, identityErr)

		marker := "ubx-marker-" + name
		runAWS(t, "iam", "tag-policy", "--policy-arn", arn, "--tags", "Key=ubx-marker,Value="+marker)
		t.Cleanup(func() { runAWS(t, "iam", "untag-policy", "--policy-arn", arn, "--tag-keys", "ubx-marker") })
		echoFinding, echoErr := ProbeSensitiveEchoLive(context.Background(), cfg, lookup, marker, "tags", "tags_all")
		requireNoFinding(t, "sensitive-echo", echoFinding, echoErr)

		requireNoDriftFinding(t, cfg, lookup)
	})

	t.Run("aws_iam_user", func(t *testing.T) {
		name := uniqueName("ubx-conformance-bulk-user")
		runAWS(t, "iam", "create-user", "--user-name", name)
		t.Cleanup(func() { runAWS(t, "iam", "delete-user", "--user-name", name) })

		cfg := LiveReadProbeConfig{
			ProviderPath: providerPath, Source: "hashicorp/aws", Version: awsProviderVersion,
			Stack: "conformance", Address: core.Address{Stack: "conformance", Type: "aws_iam_user", Name: name},
			ProviderConfig: regionConfig,
		}
		lookupMinimal := MustMarshal(map[string]string{"id": name})
		lookupFull := MustMarshal(map[string]string{"id": name, "name": name})
		// A real, live check of something the registry's own Lookup
		// convention always supplies both fields for, but never actually
		// tested minimal-vs-full against: does "id" alone genuinely
		// suffice, or was "name" load-bearing all along?
		identityFinding, identityErr := ProbeIdentityShapeLive(context.Background(), cfg, lookupMinimal, lookupFull)
		requireNoFinding(t, "identity-shape", identityFinding, identityErr)

		marker := "ubx-marker-" + name
		runAWS(t, "iam", "tag-user", "--user-name", name, "--tags", "Key=ubx-marker,Value="+marker)
		t.Cleanup(func() { runAWS(t, "iam", "untag-user", "--user-name", name, "--tag-keys", "ubx-marker") })
		echoFinding, echoErr := ProbeSensitiveEchoLive(context.Background(), cfg, lookupFull, marker, "tags", "tags_all")
		requireNoFinding(t, "sensitive-echo", echoFinding, echoErr)

		requireNoDriftFinding(t, cfg, lookupFull)
	})
}

// requireNoFinding fails t if err != nil or finding != nil, logging the
// finding's own Detail on failure — shared by every subtest above.
func requireNoFinding(t *testing.T, probe string, finding *Finding, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s probe: %v", probe, err)
	}
	if finding != nil {
		t.Fatalf("%s probe: unexpected finding: %+v", probe, finding)
	}
}

// requireNoDriftFinding runs ProbeDriftLive against a fresh temp ledger
// (drift detection needs its own ledger state, unlike the read-only
// identity/sensitive-echo probes).
func requireNoDriftFinding(t *testing.T, cfg LiveReadProbeConfig, lookup json.RawMessage) {
	t.Helper()
	l := core.Open(t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	finding, err := ProbeDriftLive(ctx, l, cfg, lookup)
	requireNoFinding(t, "drift-detectability", finding, err)
}

// TestSweep_AWSBulkLiveRun_NoLeaks is the required sweep-verification
// step (docs/conformance-harness.md's own adversarial row 3): confirms,
// after the whole batch above runs and every subtest's own t.Cleanup
// fires, that nothing matching this run's own "ubx-conformance-bulk-*"
// naming convention remains live in the account. Run manually, after
// TestBulkLiveProbes_AWSFreeTierBatch, as part of this session's own
// live-tier verification -- not wired as a Go test dependency (Go tests
// don't guarantee cross-test ordering), documented instead in
// STATE.md/docs/conformance-harness.md alongside the real sweep output.
func TestSweep_AWSBulkLiveRun_NoLeaks(t *testing.T) {
	RequireLive(t)
	out := runAWSOutput(t, "sqs", "list-queues", "--queue-name-prefix", "ubx-conformance-bulk", "--output", "text")
	if out != "" && out != "None" {
		t.Fatalf("leaked SQS queue(s): %s", out)
	}
	out = runAWSOutput(t, "sns", "list-topics", "--query", "Topics[?contains(TopicArn, 'ubx-conformance-bulk')].TopicArn", "--output", "text")
	if out != "" && out != "None" {
		t.Fatalf("leaked SNS topic(s): %s", out)
	}
	out = runAWSOutput(t, "iam", "list-policies", "--scope", "Local", "--query", "Policies[?contains(PolicyName, 'ubx-conformance-bulk')].Arn", "--output", "text")
	if out != "" && out != "None" {
		t.Fatalf("leaked IAM polic(ies): %s", out)
	}
	out = runAWSOutput(t, "iam", "list-users", "--query", "Users[?contains(UserName, 'ubx-conformance-bulk')].UserName", "--output", "text")
	if out != "" && out != "None" {
		t.Fatalf("leaked IAM user(s): %s", out)
	}
}
