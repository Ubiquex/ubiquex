package describe

import (
	"strings"
	"testing"
)

func TestBuildUserPrompt_IncludesOnlyRealSignal(t *testing.T) {
	field := FieldContext{
		Name:          "instance_class",
		Type:          "string",
		Required:      true,
		Enum:          []string{"db.t3.micro", "db.t3.small"},
		Constraints:   []string{"pattern: ^db\\."},
		ParentContext: "aws_rds_instance",
	}
	prompt := buildUserPrompt(field)

	for _, want := range []string{
		"Field name: instance_class",
		"Type: string",
		"db.t3.micro, db.t3.small",
		"pattern: ^db\\.",
		"aws_rds_instance",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q, got:\n%s", want, prompt)
		}
	}
}

func TestBuildUserPrompt_EmptySignalHonestlyLabeled(t *testing.T) {
	field := FieldContext{Name: "id", Type: "string", Computed: true}
	prompt := buildUserPrompt(field)

	if !strings.Contains(prompt, "Enum values: (none declared)") {
		t.Errorf("expected an honest '(none declared)' marker for enum, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Constraints: (none declared)") {
		t.Errorf("expected an honest '(none declared)' marker for constraints, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Parent resource/operation: (none given)") {
		t.Errorf("expected an honest '(none given)' marker for parent context, got:\n%s", prompt)
	}
}

func TestResultJSONSchema_ForcesAbstentionAsFirstClassField(t *testing.T) {
	required, ok := resultJSONSchema["required"].([]string)
	if !ok {
		t.Fatalf("required = %v, want []string", resultJSONSchema["required"])
	}
	want := map[string]bool{"abstained": true, "description": true, "reason": true}
	if len(required) != len(want) {
		t.Fatalf("required = %v, want exactly %v", required, want)
	}
	for _, r := range required {
		if !want[r] {
			t.Errorf("unexpected required field %q", r)
		}
	}
}

func TestNew_DefaultModel(t *testing.T) {
	g := New(Config{})
	if g.model != DefaultModel {
		t.Fatalf("model = %q, want %q", g.model, DefaultModel)
	}
}

func TestNew_ExplicitModel(t *testing.T) {
	g := New(Config{Model: "claude-haiku-4-5-20251001"})
	if g.model != "claude-haiku-4-5-20251001" {
		t.Fatalf("model = %q, want the explicit override", g.model)
	}
}
