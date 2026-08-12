package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/intentprovider"
)

// fakeAdapter is a hermetic, fully deterministic fake -- no network, no
// real LLM. Proves the harness itself (Fixtures, Doc, Run) works
// end-to-end without depending on whether any real adapter is correct,
// per docs/intent-provider.md's own "Conformance suite" section: "a fake
// Adapter proves this harness itself works hermetically."
type fakeAdapter struct{ draft string }

func (f *fakeAdapter) Name() string { return "fake" }

func (f *fakeAdapter) Model() string { return "fake-model" }

// scriptedDrafts maps a fixture's own Stack to its own scripted
// response -- needed once a second fixture exists (UBI-63): each
// fixture names a distinct stack, so dispatching on req.Stack picks the
// right canned draft for whichever fixture Run is currently driving,
// rather than every fixture getting the same single draft back
// regardless of its own doc.
var scriptedDrafts = map[string]string{
	"payments":                  goodPaymentsDraft,
	"platform":                  goodPlatformDraft,
	"platform-iam-attach":       goodPlatformIAMAttachDraft,
	"platform-blueprint-output": goodPlatformBlueprintOutputDraft,
}

func (f *fakeAdapter) Draft(_ context.Context, req intentprovider.DraftRequest) (json.RawMessage, error) {
	if f.draft != "" {
		return json.RawMessage(f.draft), nil
	}
	draft, ok := scriptedDrafts[req.Stack]
	if !ok {
		return nil, fmt.Errorf("fakeAdapter: no scripted draft for stack %q", req.Stack)
	}
	return json.RawMessage(draft), nil
}

var _ intentprovider.Adapter = (*fakeAdapter)(nil)

const goodPaymentsDraft = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "payments",
  "intent": {
    "summary": "provision a database for payments, modeled on staging, smaller",
    "assumptions": [
      {"text": "chose db.t3.small, one step down from staging's db.t3.medium", "affects": ["aws_db_instance.payments-db.instance_class"]}
    ],
    "defaults": [],
    "questions": []
  },
  "resources": [
    {"type": "aws_db_instance", "name": "payments-db", "op": "create", "config": "{\"instance_class\":\"db.t3.small\"}"}
  ],
  "destroys": []
}`

// goodPlatformDraft is the SECOND fixture's own scripted response
// (UBI-63) -- deliberately uses the real, correct wire-shape {"$ref":
// {"to": "..."}} object in TWO places: directly, as the attachment's own
// "role" attribute, and JSON-embedded one level inside the inline
// policy's own config-string content, exactly the two positions
// checkPlatformCrossRefAndJSONEmbeddedRef looks for. Proves the harness
// itself accepts a CORRECT draft; TestCheck_RejectsLiteralRefString
// (below) proves it also rejects the exact broken shape this ticket
// found live.
const goodPlatformDraft = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "platform",
  "intent": {
    "summary": "an IAM role for CI, with an inline policy granting SQS send access",
    "assumptions": [],
    "defaults": [],
    "questions": []
  },
  "resources": [
    {"type": "aws_sqs_queue", "name": "ci-notifications", "op": "create", "config": "{\"name\":\"ci-notifications\"}"},
    {"type": "aws_iam_role", "name": "ci-runner", "op": "create", "config": "{\"name\":\"ci-runner\"}"},
    {"type": "aws_iam_role_policy", "name": "ci-runner-sqs", "op": "create", "config": "{\"role\":{\"$ref\":{\"to\":\"platform.aws_iam_role.ci-runner.name\"}},\"policy\":\"{\\\"Statement\\\":[{\\\"Effect\\\":\\\"Allow\\\",\\\"Action\\\":\\\"sqs:SendMessage\\\",\\\"Resource\\\":{\\\"$ref\\\":{\\\"to\\\":\\\"platform.aws_sqs_queue.ci-notifications.arn\\\"}}}]}\"}"}
  ],
  "destroys": []
}`

// goodPlatformIAMAttachDraft is the THIRD fixture's own scripted
// response (UBI-65) -- the standalone-policy shape the founder's
// exact platform-iam-attach.md doc requires: a separate aws_iam_policy
// resource plus a separate aws_iam_role_policy_attachment resource,
// never a single aws_iam_role_policy (inline) resource. Proves the
// harness accepts the CORRECT shape;
// TestScanForIAMPolicyShape_RejectsInlinePolicy (below) proves it also
// rejects the exact miss a real live Haiku run produced before UBI-65's
// prompt fix existed.
const goodPlatformIAMAttachDraft = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "platform-iam-attach",
  "intent": {
    "summary": "CI platform: ECR repo, SQS queue, IAM role, a standalone policy attached to that role",
    "assumptions": [],
    "defaults": [],
    "questions": []
  },
  "resources": [
    {"type": "aws_ecr_repository", "name": "ci-artifacts", "op": "create", "config": "{\"name\":\"ci-artifacts\",\"image_tag_mutability\":\"IMMUTABLE\",\"image_scanning_configuration\":{\"scan_on_push\":true}}"},
    {"type": "aws_sqs_queue", "name": "ci-notifications", "op": "create", "config": "{\"name\":\"ci-notifications\",\"message_retention_seconds\":86400}"},
    {"type": "aws_iam_role", "name": "ci-runner", "op": "create", "config": "{\"name\":\"ci-runner\",\"assume_role_policy\":\"{\\\"Version\\\":\\\"2012-10-17\\\",\\\"Statement\\\":[{\\\"Effect\\\":\\\"Allow\\\",\\\"Principal\\\":{\\\"Service\\\":\\\"ec2.amazonaws.com\\\"},\\\"Action\\\":\\\"sts:AssumeRole\\\"}]}\"}"},
    {"type": "aws_iam_policy", "name": "ci-runner-access", "op": "create", "config": "{\"name\":\"ci-runner-access\",\"policy\":\"{\\\"Version\\\":\\\"2012-10-17\\\",\\\"Statement\\\":[{\\\"Effect\\\":\\\"Allow\\\",\\\"Action\\\":[\\\"ecr:BatchCheckLayerAvailability\\\",\\\"ecr:GetDownloadUrlForLayer\\\",\\\"ecr:BatchGetImage\\\",\\\"ecr:PutImage\\\",\\\"ecr:InitiateLayerUpload\\\",\\\"ecr:UploadLayerPart\\\",\\\"ecr:CompleteLayerUpload\\\"],\\\"Resource\\\":{\\\"$ref\\\":{\\\"to\\\":\\\"platform-iam-attach.aws_ecr_repository.ci-artifacts.arn\\\"}}},{\\\"Effect\\\":\\\"Allow\\\",\\\"Action\\\":\\\"sqs:SendMessage\\\",\\\"Resource\\\":{\\\"$ref\\\":{\\\"to\\\":\\\"platform-iam-attach.aws_sqs_queue.ci-notifications.arn\\\"}}}]}\"}"},
    {"type": "aws_iam_role_policy_attachment", "name": "ci-runner-access-attach", "op": "create", "config": "{\"role\":{\"$ref\":{\"to\":\"platform-iam-attach.aws_iam_role.ci-runner.name\"}},\"policy_arn\":{\"$ref\":{\"to\":\"platform-iam-attach.aws_iam_policy.ci-runner-access.arn\"}}}"}
  ],
  "destroys": []
}`

// goodPlatformBlueprintOutputDraft is the FOURTH fixture's own scripted
// response (UBI-128): the fixture doc names a blueprint call with an
// explicit "as 'platform'" alias, then references that alias's own
// repo_arn output from a later, unrelated sentence -- the scripted draft
// carries the real wire shape both halves of that grammar are supposed
// to produce: "call_name":"platform" on the blueprint_calls[] entry, and
// a real, nested {"$ref":{"to":"$blueprint_output:platform:repo_arn"}}
// object one level inside the policy's own JSON-encoded string content
// (the identical "JSON-embedded ref" shape goodPlatformDraft already
// exercises for an ordinary resource-to-resource reference, just with a
// blueprint-output "to" value instead of a <stack>.<type>.<name>.<attr>
// address).
const goodPlatformBlueprintOutputDraft = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "platform-blueprint-output",
  "intent": {
    "summary": "call the ci-platform blueprint as 'platform', attach a downstream policy using its own repo_arn output",
    "assumptions": [],
    "defaults": [],
    "questions": []
  },
  "resources": [
    {"type": "aws_iam_role_policy", "name": "downstream-access", "op": "create", "config": "{\"role\":\"downstream-role\",\"policy\":\"{\\\"Statement\\\":[{\\\"Effect\\\":\\\"Allow\\\",\\\"Action\\\":\\\"s3:GetObject\\\",\\\"Resource\\\":{\\\"$ref\\\":{\\\"to\\\":\\\"$blueprint_output:platform:repo_arn\\\"}}}]}\"}"}
  ],
  "destroys": [],
  "blueprint_calls": [
    {"name": "ci-platform call", "call_name": "platform", "blueprint": "ci-platform", "ref": "", "path": "", "args": "{\"repo_name\":\"payments-ci-artifacts\",\"queue_name\":\"payments-notifications\"}"}
  ]
}`

func TestRun_FakeAdapterPasses(t *testing.T) {
	Run(t, &fakeAdapter{})
}

// TestScanForRefShapes_RejectsLiteralRefString is UBI-63 bug 1(c)'s own
// regression test for the conformance gap itself:
// checkPlatformCrossRefAndJSONEmbeddedRef's own scanning logic must
// flag the exact broken shape found live ("$ref:<address>.<path>" as a
// plain string, both directly and JSON-embedded one level inside a
// policy-shaped config string) -- proving the harness would have
// caught this bug, not just that a fixed adapter now passes it.
// Asserts against scanForRefShapes directly (not through a fixture's
// own Check, which takes a *testing.T -- not designed to be faked
// outside the real test framework) so this test itself stays a normal,
// safe test rather than needing to construct one.
func TestScanForRefShapes_RejectsLiteralRefString(t *testing.T) {
	resources := []resolver.ResourceIntent{
		{Type: "aws_sqs_queue", Name: "ci-notifications", Op: resolver.OpCreate, Config: json.RawMessage(`{"name":"ci-notifications"}`)},
		{Type: "aws_iam_role", Name: "ci-runner", Op: resolver.OpCreate, Config: json.RawMessage(`{"name":"ci-runner"}`)},
		{Type: "aws_iam_role_policy", Name: "ci-runner-sqs", Op: resolver.OpCreate, Config: json.RawMessage(`{"role":"$ref:platform.aws_iam_role.ci-runner.name","policy":"{\"Resource\":\"$ref:platform.aws_sqs_queue.ci-notifications.arn\"}"}`)},
	}
	sawReal, sawBroken, err := scanForRefShapes(resources)
	if err != nil {
		t.Fatalf("scanForRefShapes: %v", err)
	}
	if !sawBroken {
		t.Error("expected scanForRefShapes to flag the literal \"$ref:\" string, found none")
	}
	if sawReal {
		t.Error("expected no real {\"$ref\": {\"to\": \"...\"}} object in this deliberately-broken draft, found one")
	}
}

// TestScanForRefShapes_AcceptsRealRefObjects is the positive twin,
// proving the SAME scanning logic recognizes the correct shape (both
// directly and JSON-embedded) as real -- goodPlatformDraft's own exact
// content, decoded, rather than re-declared here.
func TestScanForRefShapes_AcceptsRealRefObjects(t *testing.T) {
	draft, _, err := intentprovider.DraftWithRetry(context.Background(), &fakeAdapter{draft: goodPlatformDraft}, "platform", []byte("irrelevant"), nil, nil)
	if err != nil {
		t.Fatalf("DraftWithRetry: %v", err)
	}
	sawReal, sawBroken, err := scanForRefShapes(draft.Resources)
	if err != nil {
		t.Fatalf("scanForRefShapes: %v", err)
	}
	if sawBroken {
		t.Error("expected no broken \"$ref:\" string in goodPlatformDraft, found one")
	}
	if !sawReal {
		t.Error("expected a real {\"$ref\": {\"to\": \"...\"}} object in goodPlatformDraft, found none")
	}
}

// badPlatformIAMAttachDraft mirrors the EXACT inline-policy shape a real
// live Claude Haiku run produced against this fixture's own doc before
// UBI-65's prompt fix existed -- 4 resources, an aws_iam_role_policy
// (inline) instead of a standalone aws_iam_policy + a separate
// aws_iam_role_policy_attachment.
const badPlatformIAMAttachDraft = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "platform-iam-attach",
  "intent": {
    "summary": "CI platform: ECR repo, SQS queue, IAM role with an inline policy",
    "assumptions": [],
    "defaults": [],
    "questions": []
  },
  "resources": [
    {"type": "aws_ecr_repository", "name": "ci-artifacts", "op": "create", "config": "{\"name\":\"ci-artifacts\"}"},
    {"type": "aws_sqs_queue", "name": "ci-notifications", "op": "create", "config": "{\"name\":\"ci-notifications\"}"},
    {"type": "aws_iam_role", "name": "ci-runner", "op": "create", "config": "{\"name\":\"ci-runner\"}"},
    {"type": "aws_iam_role_policy", "name": "ci-runner-access", "op": "create", "config": "{\"role\":{\"$ref\":{\"to\":\"platform-iam-attach.aws_iam_role.ci-runner.name\"}},\"policy\":\"{}\"}"}
  ],
  "destroys": []
}`

// TestScanForIAMPolicyShape_RejectsInlinePolicy is UBI-65's own
// regression test for the conformance gap itself: given the exact
// inline-shape draft a real live Haiku run produced, the check must flag
// the missing standalone policy, the missing attachment, AND the
// presence of the inline resource -- proving the harness would have
// caught this miss, not just that a fixed model now passes it.
func TestScanForIAMPolicyShape_RejectsInlinePolicy(t *testing.T) {
	draft, _, err := intentprovider.DraftWithRetry(context.Background(), &fakeAdapter{draft: badPlatformIAMAttachDraft}, "platform-iam-attach", []byte("irrelevant"), nil, nil)
	if err != nil {
		t.Fatalf("DraftWithRetry: %v", err)
	}
	sawStandalone, sawAttachment, sawInline := scanForIAMPolicyShape(draft.Resources)
	if sawStandalone {
		t.Error("expected no standalone aws_iam_policy in the deliberately-inline draft, found one")
	}
	if sawAttachment {
		t.Error("expected no aws_iam_role_policy_attachment in the deliberately-inline draft, found one")
	}
	if !sawInline {
		t.Error("expected scanForIAMPolicyShape to flag the inline aws_iam_role_policy, found none")
	}
}

// TestScanForIAMPolicyShape_AcceptsStandaloneShape is the positive twin,
// proving the same scanning logic recognizes the correct, standalone
// shape as real -- goodPlatformIAMAttachDraft's own exact content,
// decoded, rather than re-declared here.
func TestScanForIAMPolicyShape_AcceptsStandaloneShape(t *testing.T) {
	draft, _, err := intentprovider.DraftWithRetry(context.Background(), &fakeAdapter{draft: goodPlatformIAMAttachDraft}, "platform-iam-attach", []byte("irrelevant"), nil, nil)
	if err != nil {
		t.Fatalf("DraftWithRetry: %v", err)
	}
	sawStandalone, sawAttachment, sawInline := scanForIAMPolicyShape(draft.Resources)
	if !sawStandalone {
		t.Error("expected a standalone aws_iam_policy in goodPlatformIAMAttachDraft, found none")
	}
	if !sawAttachment {
		t.Error("expected an aws_iam_role_policy_attachment in goodPlatformIAMAttachDraft, found none")
	}
	if sawInline {
		t.Error("expected no inline aws_iam_role_policy in goodPlatformIAMAttachDraft, found one")
	}
}

// TestFixtures_DocFilesEmbedAndParse confirms every fixture's own doc
// file is actually embedded and readable -- a missing/misnamed embed
// path fails loudly here rather than surfacing as a confusing "file does
// not exist" only inside a specific t.Run.
func TestFixtures_DocFilesEmbedAndParse(t *testing.T) {
	if len(Fixtures) == 0 {
		t.Fatal("Fixtures is empty -- fixture #1 (the payments doc) must always be present")
	}
	for _, f := range Fixtures {
		doc := f.Doc(t)
		if len(doc) == 0 {
			t.Errorf("fixture %s: embedded doc is empty", f.Name)
		}
	}
}
