package blueprint

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
)

func TestApplyOverrides_MergesIntoTargetConfig(t *testing.T) {
	intent := &resolver.IntentFile{
		Stack: "payments",
		Resources: []resolver.ResourceIntent{
			{Type: "aws_sqs_queue", Name: "pipeline-events", Op: resolver.OpCreate, Config: json.RawMessage(`{"name":"events","message_retention_seconds":86400}`)},
		},
		Overrides: []resolver.Override{
			{Address: "payments.aws_sqs_queue.pipeline-events", Config: map[string]json.RawMessage{
				"some_hardcoded_field": json.RawMessage(`"new_value"`),
			}},
		},
	}

	if err := ApplyOverrides(intent); err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if intent.Overrides != nil {
		t.Fatalf("expected Overrides cleared after apply, got %+v", intent.Overrides)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(intent.Resources[0].Config, &config); err != nil {
		t.Fatal(err)
	}
	if config["some_hardcoded_field"] != "new_value" {
		t.Fatalf("expected some_hardcoded_field overridden to new_value, got %v", config)
	}
	if config["name"] != "events" {
		t.Fatalf("expected pre-existing field untouched, got %v", config)
	}
}

func TestApplyOverrides_NoOverrides_NoOp(t *testing.T) {
	intent := &resolver.IntentFile{
		Stack: "payments",
		Resources: []resolver.ResourceIntent{
			{Type: "aws_sqs_queue", Name: "q", Op: resolver.OpCreate, Config: json.RawMessage(`{"name":"q"}`)},
		},
	}
	orig := string(intent.Resources[0].Config)
	if err := ApplyOverrides(intent); err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	if string(intent.Resources[0].Config) != orig {
		t.Fatalf("expected config untouched, got %s", intent.Resources[0].Config)
	}
}

func TestApplyOverrides_CrossStackTarget_Errors(t *testing.T) {
	intent := &resolver.IntentFile{
		Stack: "payments",
		Resources: []resolver.ResourceIntent{
			{Type: "aws_sqs_queue", Name: "q", Op: resolver.OpCreate, Config: json.RawMessage(`{}`)},
		},
		Overrides: []resolver.Override{
			{Address: "other-stack.aws_sqs_queue.q", Config: map[string]json.RawMessage{"x": json.RawMessage(`1`)}},
		},
	}
	err := ApplyOverrides(intent)
	if err == nil || !strings.Contains(err.Error(), "can only target its own calling stack") {
		t.Fatalf("expected a cross-stack refusal, got %v", err)
	}
}

func TestApplyOverrides_UnknownAddress_Errors(t *testing.T) {
	intent := &resolver.IntentFile{
		Stack: "payments",
		Resources: []resolver.ResourceIntent{
			{Type: "aws_sqs_queue", Name: "q", Op: resolver.OpCreate, Config: json.RawMessage(`{}`)},
		},
		Overrides: []resolver.Override{
			{Address: "payments.aws_sqs_queue.does-not-exist", Config: map[string]json.RawMessage{"x": json.RawMessage(`1`)}},
		},
	}
	err := ApplyOverrides(intent)
	if err == nil || !strings.Contains(err.Error(), "no such resource") {
		t.Fatalf("expected a no-such-resource refusal, got %v", err)
	}
}

func TestApplyOverrides_MalformedAddress_Errors(t *testing.T) {
	intent := &resolver.IntentFile{
		Stack: "payments",
		Resources: []resolver.ResourceIntent{
			{Type: "aws_sqs_queue", Name: "q", Op: resolver.OpCreate, Config: json.RawMessage(`{}`)},
		},
		Overrides: []resolver.Override{
			{Address: "not-a-valid-address", Config: map[string]json.RawMessage{"x": json.RawMessage(`1`)}},
		},
	}
	err := ApplyOverrides(intent)
	if err == nil || !strings.Contains(err.Error(), "not a valid") {
		t.Fatalf("expected a malformed-address refusal, got %v", err)
	}
}

// TestApplyOverrides_TargetsBlueprintProducedResource proves the ticket's
// own literal worked example: an override targeting a resource a
// blueprint call JUST produced (not a hand-authored one) -- the
// motivating case named in the design comment on UBI-86.
func TestApplyOverrides_TargetsBlueprintProducedResource(t *testing.T) {
	intent := &resolver.IntentFile{
		Stack: "payments",
		Resources: []resolver.ResourceIntent{
			{
				Type: "aws_sqs_queue", Name: "pipeline-events", Op: resolver.OpCreate,
				Config:  json.RawMessage(`{"name":"payments-notifications"}`),
				Sources: []core.IntentSource{{Kind: "blueprint", Ref: "ci-platform:sha256:abc"}},
			},
		},
		Overrides: []resolver.Override{
			{Address: "payments.aws_sqs_queue.pipeline-events", Config: map[string]json.RawMessage{
				"some_hardcoded_field": json.RawMessage(`"new_value"`),
			}},
		},
	}
	if err := ApplyOverrides(intent); err != nil {
		t.Fatalf("ApplyOverrides: %v", err)
	}
	var config map[string]interface{}
	if err := json.Unmarshal(intent.Resources[0].Config, &config); err != nil {
		t.Fatal(err)
	}
	if config["some_hardcoded_field"] != "new_value" {
		t.Fatalf("expected override applied to blueprint-produced resource, got %v", config)
	}
}
