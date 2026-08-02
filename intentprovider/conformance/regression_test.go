package conformance

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/intentprovider"
)

// badPlatformDraft mirrors the EXACT hallucinated attribute names a real
// live run (Claude Haiku, this exact platform.md fixture) produced before
// UBI-66's fix existed: "role_name"/"assume_role_policy_document" for
// aws_iam_role's real "name"/"assume_role_policy" attributes -- both
// plausible-sounding, neither real (confirmed empirically against
// hashicorp/aws's own live schema at the time). goodPlatformDraft
// (harness_test.go) is this same fixture's own correct counterpart.
const badPlatformDraft = `{
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
    {"type": "aws_iam_role", "name": "ci-runner", "op": "create", "config": "{\"role_name\":\"ci-runner\",\"assume_role_policy_document\":\"{}\"}"}
  ],
  "destroys": []
}`

// roleSchema is a hermetic resolver.SchemaInspector modeling
// aws_iam_role's/aws_sqs_queue's REAL attribute names (id, name,
// assume_role_policy, arn) -- enough to resolve badPlatformDraft and
// prove UBI-66's own resolve-time refusal. Deliberately no `provider`
// import here: this package's own boundary (see conformance.go's doc
// comment, "project-internal tooling") stays core/resolver +
// intentprovider only, the same discipline adapter.go documents for
// intentprovider itself -- a small hand-rolled fake, matching
// core/resolver's own test pattern, is enough to prove this integration
// point without reaching into `provider`.
type roleSchema struct{}

func (roleSchema) HasType(t string) bool {
	return t == "aws_iam_role" || t == "aws_sqs_queue"
}
func (roleSchema) IsComputed(t, path string) bool  { return path == "id" || path == "arn" }
func (roleSchema) IsSensitive(t, path string) bool { return false }

// realKeysByType is the "real schema" half of this fake -- deliberately
// separate from the suggestion table below, the same "known keys" vs.
// "what's the closest real key" split provider/schemakeys.go's own
// knownKeySet/closestKey keep separate.
var realKeysByType = map[string]map[string]bool{
	"aws_iam_role":  {"id": true, "name": true, "assume_role_policy": true, "arn": true},
	"aws_sqs_queue": {"id": true, "name": true, "arn": true},
}

// suggestionFor is a fixed lookup, not the real fuzzy-match algorithm --
// that algorithm is unit-tested directly, exhaustively, in
// provider/schemakeys_test.go (including this exact repro's own key
// pairs). This fake only needs to prove resolve-time wiring/refusal for
// the real, live-found repro, not re-verify fuzzy-match quality.
var suggestionFor = map[string]string{
	"role_name":                   "name",
	"assume_role_policy_document": "assume_role_policy",
}

func (roleSchema) UnknownConfigKeys(t string, config map[string]interface{}) []resolver.ConfigKeyIssue {
	known := realKeysByType[t]
	keys := make([]string, 0, len(config))
	for k := range config {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var issues []resolver.ConfigKeyIssue
	for _, k := range keys {
		if known[k] {
			continue
		}
		issues = append(issues, resolver.ConfigKeyIssue{Path: k, Suggestion: suggestionFor[k]})
	}
	return issues
}

// MissingRequiredKeys is an always-nil stub (UBI-90) -- this suite's own
// fixed drafts already carry every real attribute their own fixtures need;
// required-attribute validation is proven separately, hermetically, in
// core/resolver's own tests and diagram's own conformance suite.
func (roleSchema) MissingRequiredKeys(t string, config map[string]interface{}) []resolver.RequiredAttributeIssue {
	return nil
}

// TestPlatform_HallucinatedAttributeNames_PassesDraftValidation_ButRefusedByResolve
// is UBI-66's own permanent conformance regression case -- the original
// haiku + platform.md repro, kept runnable forever, hermetically (no
// network, no real Claude, no real AWS): a fakeAdapter scripted with the
// EXACT live hallucination.
//
// Two claims, both proven, matching UBI-66 point 4's own instruction to
// confirm empirically (not assume) why structured output didn't already
// catch this:
//
//  1. DraftWithRetry -- the real two-layer validation pipeline every
//     Adapter goes through (its own API-level JSON-schema constraint,
//     simulated here by a fake that skips straight to the ubx-side layer
//     the same way a real adapter's own successful API round trip would;
//     plus parseAndValidate, which always runs regardless) -- ACCEPTS
//     this draft cleanly. Confirmed, not assumed: schema.go's own
//     IntentDraftJSONSchema types a resource's "config" as an opaque JSON-
//     encoded STRING (structured-output schemas can't express an open-
//     ended object shape), so no per-type attribute-key constraint is
//     even expressible at that layer; validate.go's parseAndValidate is
//     deliberately ledger/provider-independent (see its own doc comment)
//     and never inspects config's own decoded keys at all. Neither layer
//     was ever positioned to catch this -- resolve is the first (and
//     only) point in the whole pipeline with both a real provider schema
//     AND a decoded config to check it against.
//  2. resolver.Resolve -- given the exact same draft, resolved against a
//     schema modeling aws_iam_role's REAL attribute names -- refuses,
//     naming both hallucinated keys with the correct real-key suggestion
//     each (UBI-66's own fix).
func TestPlatform_HallucinatedAttributeNames_PassesDraftValidation_ButRefusedByResolve(t *testing.T) {
	draft, _, err := intentprovider.DraftWithRetry(context.Background(), &fakeAdapter{draft: badPlatformDraft}, "platform", []byte("irrelevant"), nil)
	if err != nil {
		t.Fatalf("DraftWithRetry: %v -- expected this draft to validate cleanly (UBI-66 point 4: neither the API-level JSON schema nor parseAndValidate has any way to catch a wrong attribute KEY name)", err)
	}

	l := core.Open(t.TempDir())
	providers := []resolver.DeclaredProvider{{Source: "hashicorp/aws", Version: "6.54.0", Schema: roleSchema{}}}

	_, err = resolver.Resolve(l, providers, draft, nil)
	if err == nil {
		t.Fatal("resolve: expected refusal for the hallucinated attribute names, got nil error")
	}
	if !errors.Is(err, resolver.ErrUnknownConfigKey) {
		t.Fatalf("err = %v, want errors.Is resolver.ErrUnknownConfigKey", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, `"role_name" does not exist on aws_iam_role (did you mean "name"?)`) {
		t.Errorf("missing role_name teaching error: %s", msg)
	}
	if !strings.Contains(msg, `"assume_role_policy_document" does not exist on aws_iam_role (did you mean "assume_role_policy"?)`) {
		t.Errorf("missing assume_role_policy_document teaching error: %s", msg)
	}
}
