package claude_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/intentprovider"
	"github.com/ubiquex/ubiquex/intentprovider/claude"
)

// UBI-81 v1's own real, end-to-end motivating case, run against the real
// Claude API (requireSlowLive, adapter_live_test.go): "if this is a
// production environment, enable KMS encryption and 14-day retention,
// else default encryption and 1-day retention" -- against a stack whose
// own config actually resolves the branch (TestReadStackConfig_
// ProductionStackName_ResolvesConditionalAsNamedDefault) and against one
// that genuinely doesn't (TestReadStackConfig_NoEnvironmentSignal_
// StillBlocksOnQuestion) -- proving the read_stack_config tool changes
// the OUTCOME (a named default instead of a blocking question) exactly
// when real context resolves the ambiguity, and changes NOTHING when it
// doesn't.

const stackConfigConditionalDoc = "# Payments logs bucket\n\n" +
	"Create an S3 bucket called payments-logs. If this is a production " +
	"environment, enable KMS encryption and 14-day log retention. " +
	"Otherwise, use default (AES256) encryption and 1-day log retention.\n"

// TestReadStackConfig_ProductionStackName_ResolvesConditionalAsNamedDefault
// is the happy path: a real stack config whose own name plainly signals
// production. The real Claude API is expected to call read_stack_config,
// see "payments-prod", resolve the branch to KMS + 14-day retention, and
// record that resolution as a real intent.defaults entry -- never a
// blocking question for this condition.
func TestReadStackConfig_ProductionStackName_ResolvesConditionalAsNamedDefault(t *testing.T) {
	requireSlowLive(t)

	a := claude.New(claude.Config{})
	stackConfig, err := json.Marshal(map[string]string{
		"stack":           "payments-prod",
		"provider_source": "hashicorp/aws",
	})
	if err != nil {
		t.Fatal(err)
	}

	draft, raw, err := intentprovider.DraftWithRetry(context.Background(), a, "payments-prod", []byte(stackConfigConditionalDoc), nil, stackConfig)
	if err != nil {
		t.Fatalf("DraftWithRetry against the real Claude API: %v\nraw: %s", err, raw)
	}
	if len(draft.Resources) == 0 {
		t.Fatalf("expected at least one resource in the real draft, got none\nraw: %s", raw)
	}

	var config string
	for _, r := range draft.Resources {
		config += " " + string(r.Config)
	}
	lower := strings.ToLower(config)
	if !strings.Contains(lower, "kms") {
		t.Errorf("expected the resolved config to contain KMS encryption (the production branch), got: %s", config)
	}
	if !strings.Contains(config, "14") {
		t.Errorf("expected the resolved config to contain 14-day retention (the production branch), got: %s", config)
	}

	var blocking []string
	for _, q := range draft.Intent.Questions {
		if q.Blocking {
			blocking = append(blocking, q.Text)
		}
	}
	if len(blocking) > 0 {
		t.Errorf("expected the environment-tier condition to resolve via real context, not block -- got blocking question(s): %v", blocking)
	}

	if len(draft.Intent.Defaults) == 0 && len(draft.Intent.Assumptions) == 0 {
		t.Error("expected the context-derived encryption/retention resolution to be recorded as a named default or assumption, got neither")
	}
	var named string
	for _, d := range draft.Intent.Defaults {
		named += " " + d.Text
	}
	for _, a := range draft.Intent.Assumptions {
		named += " " + a.Text
	}
	if !strings.Contains(strings.ToLower(named), "production") && !strings.Contains(named, "payments-prod") {
		t.Errorf("expected the named default/assumption to name the real reasoning (production/payments-prod), got: %q", named)
	}
}

// TestReadStackConfig_NoEnvironmentSignal_StillBlocksOnQuestion is the
// required fallback proof: an identical conditional document, but a
// stack config with genuinely no production/staging signal at all. The
// real Claude API is still expected to call read_stack_config (nothing
// about the tool itself changes), see that its answer doesn't resolve
// the condition, and still record a real, blocking question -- exactly
// the pre-UBI-81 behavior, never a silent guess just because a tool now
// exists to call.
func TestReadStackConfig_NoEnvironmentSignal_StillBlocksOnQuestion(t *testing.T) {
	requireSlowLive(t)

	a := claude.New(claude.Config{})
	stackConfig, err := json.Marshal(map[string]string{
		"stack":           "widgets-api",
		"provider_source": "hashicorp/aws",
	})
	if err != nil {
		t.Fatal(err)
	}

	draft, raw, err := intentprovider.DraftWithRetry(context.Background(), a, "widgets-api", []byte(stackConfigConditionalDoc), nil, stackConfig)
	if err != nil {
		t.Fatalf("DraftWithRetry against the real Claude API: %v\nraw: %s", err, raw)
	}

	var blocking []string
	for _, q := range draft.Intent.Questions {
		if q.Blocking {
			blocking = append(blocking, q.Text)
		}
	}
	if len(blocking) == 0 {
		t.Fatalf("expected a real blocking question -- neither the document nor the stack config resolves whether this is production -- got none. Resources: %s\nDefaults: %v\nAssumptions: %v",
			mustMarshalResourceConfigs(t, draft), draft.Intent.Defaults, draft.Intent.Assumptions)
	}
}

func mustMarshalResourceConfigs(t *testing.T, draft *resolver.IntentFile) string {
	t.Helper()
	var s string
	for _, r := range draft.Resources {
		s += string(r.Config) + " "
	}
	return s
}
