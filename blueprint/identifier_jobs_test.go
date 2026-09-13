package blueprint

import (
	"strings"
	"testing"

	gotemplate "github.com/ubiquex/ubiquex/sdk/codegen/templates/go"
)

// identifier_jobs_test.go pins the distinction UBI-264 settled: there
// are two jobs here, not one function with an inconsistency.
//
// It was filed as a divergence, on the reading that two structurally
// identical pascalCase implementations disagreed about whether a hyphen
// is legal. Tracing the callers showed otherwise. The hyphen tolerance
// is load-bearing for input that is hyphenated throughout this
// repository's own fixtures, and none of it is a wire name.

// An authored name is chosen by a person, and hyphens are ordinary in
// one. Every value here appears hyphenated in real fixtures or real
// published artifacts.
func TestPascalCaseAuthored_AcceptsHyphens(t *testing.T) {
	cases := map[string]string{
		"ubx-aws-sqs":           "UbxAwsSqs",      // a blueprint's own name
		"pipeline-events":       "PipelineEvents", // a resource slug
		"attach-ci-runner-v14":  "AttachCiRunnerV14",
		"retention_days":        "RetentionDays", // a param name
		"message_retention_sec": "MessageRetentionSec",
	}
	for in, want := range cases {
		got, err := pascalCaseAuthored(in)
		if err != nil {
			t.Errorf("%q: %v -- hyphenated authored names are ordinary, and refusing one breaks every hyphenated resource slug", in, err)
			continue
		}
		if got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

// A wire name is chosen by a provider, and no real provider schema this
// project has generated against contains a hyphen. Accepting one would
// mean inventing an identifier for input that cannot occur, and doing
// it differently from the SDK a caller imports alongside.
func TestPascalCaseWire_RefusesHyphens(t *testing.T) {
	if _, err := pascalCaseWire("foo-bar"); err == nil {
		t.Fatal("a hyphen in a wire name has to be refused, the way sdk codegen refuses it")
	}
	got, err := pascalCaseWire("message_retention_seconds")
	if err != nil {
		t.Fatal(err)
	}
	if got != "MessageRetentionSeconds" {
		t.Errorf("got %q", got)
	}
}

// The point of having two: they must agree wherever a real provider
// field name can actually reach both. If they ever disagree here, a
// blueprint's own generated binding and the published SDK would give
// two different Go names for one provider field.
func TestWireConverters_AgreeWithTheSDKsOwn(t *testing.T) {
	realFieldNames := []string{
		"name",
		"message_retention_seconds",
		"kms_master_key_id",
		"fifo_queue",
		"content_based_deduplication",
		"redrive_allow_policy",
		"visibility_timeout_seconds",
		"id",
		"arn",
	}
	for _, wire := range realFieldNames {
		mine, myErr := pascalCaseWire(wire)
		theirs, theirErr := gotemplate.PascalCaseWireName(wire)
		if (myErr == nil) != (theirErr == nil) {
			t.Errorf("%q: one converter accepted and the other refused (mine=%v, sdk=%v)", wire, myErr, theirErr)
			continue
		}
		if mine != theirs {
			t.Errorf("%q -> blueprint %q, sdk codegen %q -- the same provider field would get two different Go names", wire, mine, theirs)
		}
	}
}

// And they must agree on refusal too, not only on success. Accepting
// what the other rejects is exactly the shape this was filed as.
func TestWireConverters_AgreeOnWhatIsIllegal(t *testing.T) {
	illegal := []string{"foo-bar", "Foo_Bar", "foo bar", "foo.bar", ""}
	for _, wire := range illegal {
		_, myErr := pascalCaseWire(wire)
		_, theirErr := gotemplate.PascalCaseWireName(wire)
		if (myErr == nil) != (theirErr == nil) {
			t.Errorf("%q: blueprint err=%v, sdk codegen err=%v -- one accepts what the other refuses", wire, myErr, theirErr)
		}
	}
}

// The authored converter still refuses genuinely bad input. Leniency
// about hyphens is not leniency generally.
func TestPascalCaseAuthored_StillRefusesRubbish(t *testing.T) {
	for _, bad := range []string{"", "Foo", "foo bar", "foo.bar", "{param}"} {
		if _, err := pascalCaseAuthored(bad); err == nil {
			t.Errorf("%q was accepted; the authored converter accepts hyphens, not anything", bad)
		} else if bad == "{param}" && !strings.Contains(err.Error(), "unsupported character") {
			t.Errorf("%q: %v", bad, err)
		}
	}
}
