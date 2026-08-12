package conformance

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/core"
)

// UBI-58: probe 3's own real-cloud confirmation, deliberately left open by
// docs/conformance-harness.md's own "Amendment (session 4, closing)" --
// probe 3 stayed hermetic-only through that arc, its live confirmation
// named explicitly as "this arc's one deliberately-open item... a future
// session picking this up should treat it as a fresh, standalone
// decision." This file is that fresh decision, made and executed. Every
// test here reaches a real ApplyResourceChange destroy call against a
// real cloud provider through core/executor.Ship (via ProbeDestroyHonesty)
// -- exactly the class of action CLAUDE.md's own ship-verification rule
// names and bans ("always, no exceptions"). Confirmed directly with the
// founder before this file was written, not inferred or assumed from the
// ticket's own text alone.

// TestLiveProbeDestroyHonesty_AWS_SNSTopic and its SQS sibling below are
// the honest-case confirmation the ticket asked for: a real, free,
// sacrificial AWS resource, created and destroyed through probe 3's own
// real ProbeDestroyHonesty path (never a shortcut), with the "actually
// gone" claim independently re-checked against real `aws` CLI ground
// truth -- not just probe 3's own self-reported nil Finding.
func TestLiveProbeDestroyHonesty_AWS_SNSTopic(t *testing.T) {
	RequireLive(t)
	providerPath := realAWSProviderPath(t)
	name := uniqueName("ubx-conformance-destroy-probe-sns")

	arn := runAWSOutput(t, "sns", "create-topic", "--name", name, "--query", "TopicArn", "--output", "text")
	t.Logf("created real SNS topic: %s", arn)
	// Safety-net cleanup only -- the whole point of this test is that
	// ProbeDestroyHonesty's own real ship call destroys it; this exists so
	// an unrelated failure part-way through never leaves the topic behind.
	t.Cleanup(func() { exec.Command("aws", "sns", "delete-topic", "--topic-arn", arn).Run() })

	l := core.Open(t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	finding, err := ProbeDestroyHonesty(ctx, l, DestroyProbeConfig{
		ProviderPath:   providerPath,
		Source:         "hashicorp/aws",
		Version:        awsProviderVersion,
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "aws_sns_topic", Name: name},
		Lookup:         MustMarshal(map[string]string{"id": arn}),
		ProviderConfig: MustMarshal(map[string]string{"region": "us-east-1"}),
		Timeout:        90 * time.Second,
	})
	if err != nil {
		t.Fatalf("ProbeDestroyHonesty: %v", err)
	}
	if finding != nil {
		t.Fatalf("got a FindingDestroyLie against real AWS SNS, want nil (aws_sns_topic destroys honestly): %+v", finding)
	}
	t.Logf("ProbeDestroyHonesty: nil finding (honest destroy) for %s", arn)

	// Independent, second confirmation -- real `aws` CLI ground truth, not
	// just trusting probe 3's own nil verdict.
	out, getErr := exec.Command("aws", "sns", "get-topic-attributes", "--topic-arn", arn).CombinedOutput()
	t.Logf("independent confirmation, aws sns get-topic-attributes --topic-arn %s:\n%s", arn, out)
	if getErr == nil {
		t.Fatalf("aws sns get-topic-attributes still succeeds after a claimed-honest destroy -- topic %s is NOT actually gone:\n%s", arn, out)
	}
	if !strings.Contains(string(out), "NotFound") {
		t.Fatalf("aws sns get-topic-attributes failed for an unexpected reason (want NotFound): %s", out)
	}
}

func TestLiveProbeDestroyHonesty_AWS_SQSQueue(t *testing.T) {
	RequireLive(t)
	providerPath := realAWSProviderPath(t)
	name := uniqueName("ubx-conformance-destroy-probe-sqs")

	url := runAWSOutput(t, "sqs", "create-queue", "--queue-name", name, "--query", "QueueUrl", "--output", "text")
	t.Logf("created real SQS queue: %s", url)
	t.Cleanup(func() { exec.Command("aws", "sqs", "delete-queue", "--queue-url", url).Run() })

	l := core.Open(t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	finding, err := ProbeDestroyHonesty(ctx, l, DestroyProbeConfig{
		ProviderPath:   providerPath,
		Source:         "hashicorp/aws",
		Version:        awsProviderVersion,
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "aws_sqs_queue", Name: name},
		Lookup:         MustMarshal(map[string]string{"id": url}),
		ProviderConfig: MustMarshal(map[string]string{"region": "us-east-1"}),
		Timeout:        90 * time.Second,
	})
	if err != nil {
		t.Fatalf("ProbeDestroyHonesty: %v", err)
	}
	if finding != nil {
		t.Fatalf("got a FindingDestroyLie against real AWS SQS, want nil (aws_sqs_queue destroys honestly): %+v", finding)
	}
	t.Logf("ProbeDestroyHonesty: nil finding (honest destroy) for %s", url)

	out, getErr := exec.Command("aws", "sqs", "get-queue-attributes", "--queue-url", url).CombinedOutput()
	t.Logf("independent confirmation, aws sqs get-queue-attributes --queue-url %s:\n%s", url, out)
	if getErr == nil {
		t.Fatalf("aws sqs get-queue-attributes still succeeds after a claimed-honest destroy -- queue %s is NOT actually gone:\n%s", url, out)
	}
	if !strings.Contains(string(out), "NonExistentQueue") {
		t.Fatalf("aws sqs get-queue-attributes failed for an unexpected reason (want NonExistentQueue): %s", out)
	}
}

// TestLiveProbeDestroyHonesty_GCP_PubSubTopic_UBI44Reproduction attempts
// the real UBI-44 lying-destroy reproduction against real GCP -- gated
// UBX_TEST_SLOW=1 to match destroy_probe_test.go's own hermetic sibling
// (a real lie pays core/executor's own ~64s production retry budget, and
// real GCP round trips make even the honest branch slower than typical).
//
// Deliberately uses the SAME incomplete lookup UBI-44's own real,
// historical reproduction used -- "id" alone (the full
// "projects/<project>/topics/<name>" path), never "name" -- because
// that's what an ordinary `ubx ship` destroy's own recorded lookup
// always looks like for this type in real use (core.RunScan's adopt
// path stores whatever Lookup the caller supplied; ubx's own universal
// convention is "id" alone unless a LookupHint says otherwise, and
// google_pubsub_topic deliberately has none, per registry.go's own
// entry). Using the complete {"id","name"} lookup here would prove
// nothing about the real bug this test exists to re-confirm.
//
// This test does not hard-assert which branch (lie vs. genuinely honest)
// occurs -- STATE.md's own account already establishes the underlying
// name-completeness gap was a deliberate, still-open follow-up, not
// something this session is fixing, so either outcome is a real,
// honestly-reportable result. What IS hard-asserted is INTERNAL
// CONSISTENCY between probe 3's own verdict and two independent, real
// ground-truth checks (gcloud describe, Cloud Audit Logs) -- a
// contradiction between what probe 3 reports and what actually happened
// would itself be a real probe-3 bug, worth failing loudly on.
func TestLiveProbeDestroyHonesty_GCP_PubSubTopic_UBI44Reproduction(t *testing.T) {
	RequireLive(t)
	if os.Getenv("UBX_TEST_SLOW") == "" {
		t.Skip("set UBX_TEST_SLOW=1 to run this real-cloud UBI-44 reproduction attempt -- a real lie pays core/executor's own ~64s production retry budget")
	}
	providerPath := realGCPProviderPath(t)
	name := uniqueName("ubx-conformance-destroy-probe-pubsub")
	fullID := "projects/" + gcpProject + "/topics/" + name

	runGCloud(t, "pubsub", "topics", "create", name)
	t.Logf("created real pubsub topic: %s", fullID)
	// Safety-net cleanup regardless of outcome -- UBI-58's own sweep
	// requirement: this specific probe run's resources must never be left
	// behind, unlike UBI-56's deliberately-standing infrastructure.
	t.Cleanup(func() {
		exec.Command("gcloud", "pubsub", "topics", "delete", name, "--project", gcpProject, "--quiet").Run()
	})

	beforeShip := time.Now().UTC()

	l := core.Open(t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	finding, err := ProbeDestroyHonesty(ctx, l, DestroyProbeConfig{
		ProviderPath: providerPath,
		Source:       "hashicorp/google",
		Version:      gcpProviderVersion,
		Stack:        "conformance",
		Address:      core.Address{Stack: "conformance", Type: "google_pubsub_topic", Name: name},
		// Deliberately incomplete -- see doc comment above.
		Lookup:         MustMarshal(map[string]string{"id": fullID}),
		ProviderConfig: gcpProviderConfig(),
		Timeout:        90 * time.Second,
	})
	if err != nil {
		t.Fatalf("ProbeDestroyHonesty: %v", err)
	}

	stillPresent := gcpPubSubTopicPresent(t, name)
	deleteTopicCalls := gcpCountDeleteTopicAuditLogEntries(t, fullID, beforeShip)

	switch {
	case finding != nil:
		// Probe 3 itself confirmed the lie live, for the first time,
		// closing the loop UBI-44's own filing left open. Ground truth
		// MUST agree, or the finding itself would be wrong.
		t.Logf("UBI-44 reproduced LIVE via probe 3 itself: %+v", finding)
		if !stillPresent {
			t.Fatalf("probe 3 reported FindingDestroyLie, but gcloud pubsub topics describe shows the topic is genuinely gone -- the finding contradicts real ground truth")
		}
		if finding.Class != FindingDestroyLie || finding.Tier != TierLive || finding.Confidence != Confirmed {
			t.Fatalf("unexpected finding shape: %+v", finding)
		}
		t.Logf("independent confirmation: gcloud describe shows the topic still present; %d real DeleteTopic audit log call(s) found (0 expected, matching UBI-44's own historical signature of a provider claiming success without ever making the real delete call)", deleteTopicCalls)
	case stillPresent:
		// No finding, but the topic is still there -- either an ordinary,
		// honestly-reported failure (fine, not a lie), or probe 3 MISSED a
		// real lie (a genuine bug, worth failing loudly on). Cloud Audit
		// Logs are the tiebreaker: zero real DeleteTopic calls with a
		// still-present topic and no finding is exactly UBI-44's own
		// signature, uncaught.
		if deleteTopicCalls == 0 {
			t.Fatalf("topic still present, zero real DeleteTopic audit log calls, but probe 3 reported no FindingDestroyLie -- this is UBI-44's own exact signature, MISSED by probe 3 (a real bug in ProbeDestroyHonesty/classifyDestroyOutcome, not a clean result)")
		}
		t.Logf("topic still present after an honest, non-lying destroy failure (%d real DeleteTopic call(s) were made) -- not the UBI-44 shape, not a probe-3 bug", deleteTopicCalls)
	default:
		// Topic genuinely gone -- the historical lookup-completeness gap
		// didn't reproduce this run (schema/provider behavior may have
		// changed since UBI-44 was first found). Report honestly, not
		// forced into looking like a reproduction.
		t.Logf("google_pubsub_topic destroyed honestly and completely this run (topic confirmed gone via gcloud, %d real DeleteTopic call(s)) -- UBI-44's own lookup-completeness gap did NOT reproduce live this time", deleteTopicCalls)
	}
}

// gcpPubSubTopicPresent reports whether name still exists, via a real
// `gcloud pubsub topics describe` call -- the same independent
// ground-truth check STATE.md's own UBI-44 account already established
// (`gcloud pubsub topics describe ...`).
func gcpPubSubTopicPresent(t *testing.T, name string) bool {
	t.Helper()
	out, err := exec.Command("gcloud", "pubsub", "topics", "describe", name, "--project", gcpProject, "--quiet").CombinedOutput()
	t.Logf("gcloud pubsub topics describe %s --project %s:\n%s", name, gcpProject, out)
	return err == nil
}

// gcpCountDeleteTopicAuditLogEntries queries real Cloud Audit Logs for
// google.pubsub.v1.Publisher.DeleteTopic calls against fullTopicID since
// since -- the same real, independent verification mechanism STATE.md's
// own UBI-44 account used ("confirmed via Cloud Audit Logs showing zero
// real DeleteTopic calls"). Audit log propagation genuinely lags a few
// seconds behind the real API call itself (empirically the reason every
// other gcloud-based wait helper in this package polls rather than
// reading once) -- retried for up to 30s rather than read once and
// trusted immediately.
func gcpCountDeleteTopicAuditLogEntries(t *testing.T, fullTopicID string, since time.Time) int {
	t.Helper()
	filter := `logName="projects/` + gcpProject + `/logs/cloudaudit.googleapis.com%2Factivity" AND ` +
		`protoPayload.methodName="google.pubsub.v1.Publisher.DeleteTopic" AND ` +
		`protoPayload.resourceName="` + fullTopicID + `" AND ` +
		`timestamp>="` + since.Format(time.RFC3339) + `"`

	t.Logf("gcloud logging read %q --project %s", filter, gcpProject)
	deadline := time.Now().Add(30 * time.Second)
	for {
		out, err := exec.Command("gcloud", "logging", "read", filter,
			"--project", gcpProject, "--format=value(protoPayload.methodName)").Output()
		if err == nil {
			lines := strings.TrimSpace(string(out))
			if lines != "" {
				t.Logf("gcloud logging read result: %s", lines)
				return len(strings.Split(lines, "\n"))
			}
		}
		if time.Now().After(deadline) {
			return 0
		}
		time.Sleep(3 * time.Second)
	}
}
