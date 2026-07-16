package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

func redactedHash(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	inner, ok := m["$redacted"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected a $redacted marker, got: %s", raw)
	}
	hash, ok := inner["sha256"].(string)
	if !ok {
		t.Fatalf("expected $redacted.sha256 to be a string, got: %s", raw)
	}
	return hash
}

func TestRedact_TopLevelSensitiveAttribute(t *testing.T) {
	block := Block{Attributes: []Attribute{
		{Name: "id", Sensitive: false},
		{Name: "password", Sensitive: true},
	}}
	observed := json.RawMessage(`{"id":"widget-1","password":"hunter2"}`)

	out, err := Redact(block, []byte("salt"), observed)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if string(out) == string(observed) {
		t.Fatal("expected redaction to change the output")
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("decode redacted output: %v", err)
	}
	if decoded["id"] != "widget-1" {
		t.Fatalf("expected non-sensitive id to survive unchanged, got: %v", decoded["id"])
	}
	redactedHash(t, mustMarshal(t, decoded["password"]))
	if containsPlaintext(out, "hunter2") {
		t.Fatal("redacted output must never contain the real secret material")
	}
}

func TestRedact_NestedSingleBlock(t *testing.T) {
	block := Block{NestedBlocks: []NestedBlock{
		{TypeName: "auth", Nesting: NestingSingle, Block: Block{Attributes: []Attribute{
			{Name: "password", Sensitive: true},
			{Name: "username", Sensitive: false},
		}}},
	}}
	observed := json.RawMessage(`{"auth":{"password":"s3cr3t","username":"alice"}}`)

	out, err := Redact(block, []byte("salt"), observed)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if containsPlaintext(out, "s3cr3t") {
		t.Fatal("nested single-block sensitive value leaked")
	}
	var decoded map[string]interface{}
	json.Unmarshal(out, &decoded)
	auth := decoded["auth"].(map[string]interface{})
	if auth["username"] != "alice" {
		t.Fatalf("expected non-sensitive nested attribute to survive, got: %v", auth["username"])
	}
	redactedHash(t, mustMarshal(t, auth["password"]))
}

func TestRedact_NestedListBlock(t *testing.T) {
	block := Block{NestedBlocks: []NestedBlock{
		{TypeName: "credential", Nesting: NestingList, Block: Block{Attributes: []Attribute{
			{Name: "secret", Sensitive: true},
		}}},
	}}
	observed := json.RawMessage(`{"credential":[{"secret":"one"},{"secret":"two"}]}`)

	out, err := Redact(block, []byte("salt"), observed)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if containsPlaintext(out, "one") || containsPlaintext(out, "two") {
		t.Fatal("list-nested sensitive values leaked")
	}
	var decoded map[string]interface{}
	json.Unmarshal(out, &decoded)
	items := decoded["credential"].([]interface{})
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	h1 := redactedHash(t, mustMarshal(t, items[0].(map[string]interface{})["secret"]))
	h2 := redactedHash(t, mustMarshal(t, items[1].(map[string]interface{})["secret"]))
	if h1 == h2 {
		t.Fatal("expected different secret values to redact to different hashes")
	}
}

func TestRedact_NestedSetBlock(t *testing.T) {
	block := Block{NestedBlocks: []NestedBlock{
		{TypeName: "credential", Nesting: NestingSet, Block: Block{Attributes: []Attribute{
			{Name: "secret", Sensitive: true},
		}}},
	}}
	observed := json.RawMessage(`{"credential":[{"secret":"one"}]}`)

	out, err := Redact(block, []byte("salt"), observed)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if containsPlaintext(out, "one") {
		t.Fatal("set-nested sensitive value leaked")
	}
}

func TestRedact_NestedMapBlock(t *testing.T) {
	block := Block{NestedBlocks: []NestedBlock{
		{TypeName: "profile", Nesting: NestingMap, Block: Block{Attributes: []Attribute{
			{Name: "password", Sensitive: true},
		}}},
	}}
	observed := json.RawMessage(`{"profile":{"prod":{"password":"prod-secret"},"staging":{"password":"staging-secret"}}}`)

	out, err := Redact(block, []byte("salt"), observed)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if containsPlaintext(out, "prod-secret") || containsPlaintext(out, "staging-secret") {
		t.Fatal("map-nested sensitive values leaked")
	}
	var decoded map[string]interface{}
	json.Unmarshal(out, &decoded)
	profile := decoded["profile"].(map[string]interface{})
	prodHash := redactedHash(t, mustMarshal(t, profile["prod"].(map[string]interface{})["password"]))
	stagingHash := redactedHash(t, mustMarshal(t, profile["staging"].(map[string]interface{})["password"]))
	if prodHash == stagingHash {
		t.Fatal("expected different map-nested secrets to redact to different hashes")
	}
}

// TestRedact_WholeValueRedactedRegardlessOfType confirms a Sensitive
// attribute whose own value is a whole list -- a real shape found via
// live schema introspection against hashicorp/aws
// (aws_elasticache_user.authentication_mode.passwords) -- redacts as one
// atomic unit, not per-element.
func TestRedact_WholeValueRedactedRegardlessOfType(t *testing.T) {
	block := Block{Attributes: []Attribute{
		{Name: "passwords", Sensitive: true},
	}}
	observed := json.RawMessage(`{"passwords":["pw1","pw2","pw3"]}`)

	out, err := Redact(block, []byte("salt"), observed)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	var decoded map[string]interface{}
	json.Unmarshal(out, &decoded)
	if _, isList := decoded["passwords"].([]interface{}); isList {
		t.Fatal("expected the whole list-typed sensitive attribute to be replaced with a single $redacted marker, not left as a list")
	}
	redactedHash(t, mustMarshal(t, decoded["passwords"]))
	if containsPlaintext(out, "pw1") || containsPlaintext(out, "pw2") || containsPlaintext(out, "pw3") {
		t.Fatal("list-typed sensitive attribute leaked an element")
	}
}

func TestRedact_NonSensitiveUntouched(t *testing.T) {
	block := Block{Attributes: []Attribute{{Name: "name", Sensitive: false}}}
	observed := json.RawMessage(`{"name":"widget-1"}`)
	out, err := Redact(block, []byte("salt"), observed)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	var decoded map[string]interface{}
	json.Unmarshal(out, &decoded)
	if decoded["name"] != "widget-1" {
		t.Fatalf("expected untouched value, got: %v", decoded["name"])
	}
}

func TestRedact_MissingAttributeSkippedGracefully(t *testing.T) {
	block := Block{Attributes: []Attribute{{Name: "password", Sensitive: true}}}
	observed := json.RawMessage(`{"id":"widget-1"}`)
	out, err := Redact(block, []byte("salt"), observed)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	var decoded map[string]interface{}
	json.Unmarshal(out, &decoded)
	if _, ok := decoded["password"]; ok {
		t.Fatal("expected no password key to be synthesized when the attribute is absent from observed state")
	}
}

// TestRedact_DeterministicSameSaltSameValue confirms drift detection over
// a redacted attribute keeps working: the same salt and the same real
// value must always redact to the same hash (unchanged -> no drift), and
// a different value must redact to a different hash (changed -> drift
// fires) -- docs/schema.md's $redacted amendment.
func TestRedact_DeterministicSameSaltSameValue(t *testing.T) {
	block := Block{Attributes: []Attribute{{Name: "password", Sensitive: true}}}
	salt := []byte("fixed-salt")

	out1, err := Redact(block, salt, json.RawMessage(`{"password":"hunter2"}`))
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	out2, err := Redact(block, salt, json.RawMessage(`{"password":"hunter2"}`))
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	var d1, d2 map[string]interface{}
	json.Unmarshal(out1, &d1)
	json.Unmarshal(out2, &d2)
	h1 := redactedHash(t, mustMarshal(t, d1["password"]))
	h2 := redactedHash(t, mustMarshal(t, d2["password"]))
	if h1 != h2 {
		t.Fatalf("expected same salt+value to produce the same hash, got %s vs %s", h1, h2)
	}

	out3, err := Redact(block, salt, json.RawMessage(`{"password":"different-value"}`))
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	var d3 map[string]interface{}
	json.Unmarshal(out3, &d3)
	h3 := redactedHash(t, mustMarshal(t, d3["password"]))
	if h1 == h3 {
		t.Fatal("expected a different real value to produce a different hash")
	}
}

func TestRedact_DifferentSaltDifferentHash(t *testing.T) {
	block := Block{Attributes: []Attribute{{Name: "password", Sensitive: true}}}
	observed := json.RawMessage(`{"password":"hunter2"}`)

	out1, err := Redact(block, []byte("salt-one"), observed)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	out2, err := Redact(block, []byte("salt-two"), observed)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	var d1, d2 map[string]interface{}
	json.Unmarshal(out1, &d1)
	json.Unmarshal(out2, &d2)
	h1 := redactedHash(t, mustMarshal(t, d1["password"]))
	h2 := redactedHash(t, mustMarshal(t, d2["password"]))
	if h1 == h2 {
		t.Fatal("expected different salts to produce different hashes for the same real value")
	}
}

func mustMarshal(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func containsPlaintext(out json.RawMessage, needle string) bool {
	return strings.Contains(string(out), needle)
}
