package sdk

import (
	"encoding/json"
	"strings"
	"testing"
)

var widgetBinding = ResourceBinding{
	WireType: "fake_widget",
	Fields: FieldMap{
		"Name":  {WireName: "name"},
		"Count": {WireName: "count"},
		"Tags":  {WireName: "tags"},
		"Owner": {WireName: "owner_ref"},
		"Timeouts": {
			WireName: "timeouts",
			Kind:     "object",
			Fields: FieldMap{
				"Create": {WireName: "create"},
			},
		},
	},
}

type widgetConfig struct {
	Name     any
	Count    any
	Tags     any
	Owner    any
	Timeouts any
}

type widgetTimeouts struct {
	Create any
}

func evalJSON(t *testing.T, def *StackDefinition) map[string]any {
	t.Helper()
	doc, err := def.Evaluate()
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func TestBasicResourceRoundTrip(t *testing.T) {
	def := Stack("demo", func() {
		Intent(IntentInfo{Summary: "a summary"})
		Resource(widgetBinding, "widget-a", widgetConfig{
			Name:  "hello",
			Count: 3,
		})
	})

	out := evalJSON(t, def)
	if out["schema_version"] != float64(1) {
		t.Fatalf("schema_version: %v", out["schema_version"])
	}
	if out["kind"] != "ubx:intent/v1" {
		t.Fatalf("kind: %v", out["kind"])
	}
	resources := out["resources"].([]any)
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
	res := resources[0].(map[string]any)
	if res["type"] != "fake_widget" || res["name"] != "widget-a" || res["op"] != "create" {
		t.Fatalf("unexpected resource shape: %v", res)
	}
	cfg := res["config"].(map[string]any)
	if cfg["name"] != "hello" || cfg["count"] != float64(3) {
		t.Fatalf("unexpected config: %v", cfg)
	}
	if _, present := cfg["owner_ref"]; present {
		t.Fatalf("unset field leaked into config: %v", cfg)
	}
}

func TestComputedReferenceSerializesToRef(t *testing.T) {
	def := Stack("demo", func() {
		Intent(IntentInfo{Summary: "s"})
		first := Resource(widgetBinding, "a", widgetConfig{Name: "a"})
		Resource(widgetBinding, "b", widgetConfig{Owner: first.Field("id")})
	})

	out := evalJSON(t, def)
	resources := out["resources"].([]any)
	second := resources[1].(map[string]any)
	cfg := second["config"].(map[string]any)
	ownerRef := cfg["owner_ref"].(map[string]any)
	ref := ownerRef["$ref"].(map[string]any)
	if ref["to"] != "demo.fake_widget.a.id" {
		t.Fatalf("unexpected $ref: %v", ref)
	}
}

func TestSecretAndCrossMarkers(t *testing.T) {
	def := Stack("demo", func() {
		Intent(IntentInfo{Summary: "s"})
		Resource(widgetBinding, "a", widgetConfig{
			Name:  Secret("vault", "path/to/secret"),
			Owner: Cross("../networking", "networking.fake_widget.shared"),
		})
	})

	out := evalJSON(t, def)
	cfg := out["resources"].([]any)[0].(map[string]any)["config"].(map[string]any)
	secret := cfg["name"].(map[string]any)["$secret"].(map[string]any)
	if secret["backend"] != "vault" || secret["path"] != "path/to/secret" {
		t.Fatalf("unexpected $secret: %v", secret)
	}
	cross := cfg["owner_ref"].(map[string]any)["$cross"].(map[string]any)
	if cross["ledger_dir"] != "../networking" || cross["to"] != "networking.fake_widget.shared" {
		t.Fatalf("unexpected $cross: %v", cross)
	}
}

func TestNestedObjectField(t *testing.T) {
	def := Stack("demo", func() {
		Intent(IntentInfo{Summary: "s"})
		Resource(widgetBinding, "a", widgetConfig{
			Name:     "a",
			Timeouts: widgetTimeouts{Create: "20m"},
		})
	})

	out := evalJSON(t, def)
	cfg := out["resources"].([]any)[0].(map[string]any)["config"].(map[string]any)
	timeouts := cfg["timeouts"].(map[string]any)
	if timeouts["create"] != "20m" {
		t.Fatalf("unexpected nested object: %v", timeouts)
	}
}

func TestOpaqueMapField(t *testing.T) {
	def := Stack("demo", func() {
		Intent(IntentInfo{Summary: "s"})
		Resource(widgetBinding, "a", widgetConfig{
			Name: "a",
			Tags: map[string]any{"env": "prod", "owner": "roozbeh"},
		})
	})

	out := evalJSON(t, def)
	cfg := out["resources"].([]any)[0].(map[string]any)["config"].(map[string]any)
	tags := cfg["tags"].(map[string]any)
	if tags["env"] != "prod" || tags["owner"] != "roozbeh" {
		t.Fatalf("unexpected tags: %v", tags)
	}
}

func TestMissingIntentIsHardFailure(t *testing.T) {
	def := Stack("demo", func() {
		Resource(widgetBinding, "a", widgetConfig{Name: "a"})
	})
	_, err := def.Evaluate()
	if err == nil || !strings.Contains(err.Error(), "Intent() was never called") {
		t.Fatalf("expected missing-intent error, got %v", err)
	}
}

func TestDuplicateResourceAddressIsHardFailure(t *testing.T) {
	def := Stack("demo", func() {
		Intent(IntentInfo{Summary: "s"})
		Resource(widgetBinding, "a", widgetConfig{Name: "1"})
		Resource(widgetBinding, "a", widgetConfig{Name: "2"})
	})
	_, err := def.Evaluate()
	if err == nil || !strings.Contains(err.Error(), "duplicate resource") {
		t.Fatalf("expected duplicate-resource error, got %v", err)
	}
}

func TestUnrecognizedConfigFieldIsHardFailure(t *testing.T) {
	type badConfig struct {
		NotARealField any
	}
	def := Stack("demo", func() {
		Intent(IntentInfo{Summary: "s"})
		Resource(widgetBinding, "a", badConfig{NotARealField: "x"})
	})
	_, err := def.Evaluate()
	if err == nil || !strings.Contains(err.Error(), "unrecognized config field") {
		t.Fatalf("expected unrecognized-field error, got %v", err)
	}
}

func TestFunctionInConfigIsHardFailure(t *testing.T) {
	def := Stack("demo", func() {
		Intent(IntentInfo{Summary: "s"})
		Resource(widgetBinding, "a", widgetConfig{Name: func() {}})
	})
	_, err := def.Evaluate()
	if err == nil || !strings.Contains(err.Error(), "cannot appear in a resource's own config") {
		t.Fatalf("expected function-rejection error, got %v", err)
	}
}

func TestResourceOutsideStackIsHardFailure(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil || !strings.Contains(r.(string), "called outside of an active Stack") {
			t.Fatalf("expected panic about calling outside Stack(), got %v", r)
		}
	}()
	Resource(widgetBinding, "a", widgetConfig{Name: "a"})
}
