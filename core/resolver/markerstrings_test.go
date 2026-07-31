package resolver

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// TestResolve_SuspiciousMarkerStringLiteral_HardRefusal is UBI-63 bug 1's
// own regression test: a config value that looks like an attempt at a
// $ref marker, but is a plain string ("$ref:<address>.<path>") rather
// than the real wire-shape object, must be a hard resolve-time error --
// exactly the shape the md-medium's own (since-fixed) system prompt
// instruction was found live to produce, and exactly the shape that
// would otherwise ship a broken literal straight to a real provider.
func TestResolve_SuspiciousMarkerStringLiteral_HardRefusal(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpCreate, `{"vpc_cidr":"$ref:payments.aws_vpc.main.cidr_block"}`),
	)

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrMarkerStringLiteral) {
		t.Fatalf("err = %v, want ErrMarkerStringLiteral", err)
	}
}

// TestResolve_SuspiciousMarkerStringLiteral_EveryMarkerKey proves the
// refusal isn't specific to $ref -- any of the five recognized marker
// keys, mis-encoded as a string prefix, is refused the same way.
func TestResolve_SuspiciousMarkerStringLiteral_EveryMarkerKey(t *testing.T) {
	for _, key := range []string{"$ref", "$cross", "$secret", "$computed", "$ephemeral"} {
		t.Run(key, func(t *testing.T) {
			l := core.Open(t.TempDir())
			schema := newFakeSchema()
			cfgJSON, err := json.Marshal(map[string]string{"attr": key + ":payments.aws_vpc.main.cidr_block"})
			if err != nil {
				t.Fatal(err)
			}
			intent := intentFile("payments", ri("aws_vpc", "v", OpCreate, string(cfgJSON)))
			_, err = Resolve(l, singleProvider(schema), intent, nil)
			if !errors.Is(err, ErrMarkerStringLiteral) {
				t.Fatalf("err = %v, want ErrMarkerStringLiteral", err)
			}
		})
	}
}

// TestResolve_JSONEmbeddedRef_ResolvesAndOrdersDependency is UBI-63 bug
// 1's own "JSON-embedded refs" acceptance test: a config attribute whose
// value is a JSON-encoded STRING (an IAM-policy-shaped document, the
// real repro's own scenario) with a $ref embedded at a leaf inside it
// must (a) resolve to the real, concrete value, re-serialized back into
// the same string form, and (b) contribute a real dependency-graph edge
// -- proving scanRefEdges's own JSON-embedded walk, not just
// resolveValue's, actually runs.
func TestResolve_JSONEmbeddedRef_ResolvesAndOrdersDependency(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	policy := `{"policy":"{\"Effect\":\"Allow\",\"Resource\":{\"$ref\":{\"to\":\"payments.aws_vpc.main.cidr_block\"}}}"}`
	intent := intentFile("payments",
		// Deliberately out of dependency order in the input.
		ri("aws_db_instance", "policy-holder", OpCreate, policy),
		ri("aws_vpc", "main", OpCreate, `{"cidr_block":"10.0.0.0/16"}`),
	)

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var first, second map[string]interface{}
	json.Unmarshal(p.Delta.Creates[0], &first)
	json.Unmarshal(p.Delta.Creates[1], &second)
	if first["name"] != "main" || second["name"] != "policy-holder" {
		t.Fatalf("execution order = %v, %v -- want main before policy-holder (JSON-embedded dependency)", first["name"], second["name"])
	}

	cfg := second["config"].(map[string]interface{})
	policyStr, ok := cfg["policy"].(string)
	if !ok {
		t.Fatalf("expected policy to stay a string, got %T", cfg["policy"])
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(policyStr), &decoded); err != nil {
		t.Fatalf("re-encoded policy isn't valid JSON: %v: %s", err, policyStr)
	}
	if decoded["Resource"] != "10.0.0.0/16" {
		t.Fatalf("expected the embedded $ref resolved to the real literal, got: %v", decoded["Resource"])
	}

	dependsOn, _ := second["depends_on"].([]interface{})
	if len(dependsOn) != 1 || dependsOn[0] != "payments.aws_vpc.main" {
		t.Fatalf("depends_on = %v, want [payments.aws_vpc.main] (the JSON-embedded ref's own dependency edge)", dependsOn)
	}
}

// TestResolve_JSONEmbeddedRef_ToComputedSibling_PersistsAsTemplate is
// UBI-63 session 2's own "deferred materialization" amendment: a
// JSON-embedded $ref to a sibling's own not-yet-applied Computed
// attribute (its real ARN, unknowable until a real create actually
// runs) is no longer a hard refusal -- same-batch resources referencing
// each other's ARNs inside IAM policy JSON (a role's inline policy
// naming a sibling queue's ARN, created together) is the flagship AWS
// pattern, and refusing it forced an unacceptable two-proposal
// workaround. The signed proposal instead carries a TEMPLATE: the
// $computed marker persists, re-serialized, inside the string -- still
// resolved enough to contribute a real dependency edge (so the executor
// ships the referenced resource first) -- and core/executor's own
// substituteComputed fills in the real value at ship time. See
// TestSubstituteComputed_JSONEmbedded* in core/executor for the ship-time
// half of this.
func TestResolve_JSONEmbeddedRef_ToComputedSibling_PersistsAsTemplate(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	policy := `{"policy":"{\"Effect\":\"Allow\",\"Resource\":{\"$ref\":{\"to\":\"payments.aws_db_instance.primary.arn\"}}}"}`
	intent := intentFile("payments",
		ri("aws_db_instance", "primary", OpCreate, `{"instance_class":"db.t3.medium"}`),
		ri("aws_db_instance", "policy-holder", OpCreate, policy),
	)

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	var first, second map[string]interface{}
	json.Unmarshal(p.Delta.Creates[0], &first)
	json.Unmarshal(p.Delta.Creates[1], &second)
	if first["name"] != "primary" || second["name"] != "policy-holder" {
		t.Fatalf("execution order = %v, %v -- want primary before policy-holder", first["name"], second["name"])
	}

	dependsOn, _ := second["depends_on"].([]interface{})
	if len(dependsOn) != 1 || dependsOn[0] != "payments.aws_db_instance.primary" {
		t.Fatalf("depends_on = %v, want [payments.aws_db_instance.primary] -- the JSON-embedded $computed still contributes a real dependency edge", dependsOn)
	}

	cfg := second["config"].(map[string]interface{})
	policyStr, ok := cfg["policy"].(string)
	if !ok {
		t.Fatalf("expected policy to stay a string template, got %T", cfg["policy"])
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(policyStr), &decoded); err != nil {
		t.Fatalf("re-encoded policy template isn't valid JSON: %v: %s", err, policyStr)
	}
	computed, ok := decoded["Resource"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected Resource to hold a $computed marker template, got %v", decoded["Resource"])
	}
	inner, ok := computed["$computed"].(map[string]interface{})
	if !ok || inner["from"] != "payments.aws_db_instance.primary.arn" {
		t.Fatalf("expected {\"$computed\":{\"from\":\"payments.aws_db_instance.primary.arn\"}}, got %v", computed)
	}
}

// TestResolve_JSONEmbeddedSecret_StillRefused proves $secret's own
// refusal survives the deferred-materialization amendment above --
// unlike $computed, a $secret marker has no defined redaction path once
// buried inside a JSON-encoded string attribute, so it stays a hard
// resolve-time error regardless. "policy" is deliberately flagged
// Sensitive in this test's own fake schema (a real provider's schema
// essentially never would be, for a JSON-embedding attribute like this
// -- but the check must still hold even in the case where IsSensitive
// alone wouldn't already have refused it, proving unsafeToEmbedSecret
// itself, not just the earlier IsSensitive gate, is doing real work).
func TestResolve_JSONEmbeddedSecret_StillRefused(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	schema.sensitive["aws_db_instance.policy"] = true
	policy := `{"policy":"{\"$secret\":{\"backend\":\"aws_secrets_manager\",\"path\":\"x\"}}"}`
	intent := intentFile("payments",
		ri("aws_db_instance", "db", OpCreate, policy),
	)

	_, err := Resolve(l, singleProvider(schema), intent, nil)
	if !errors.Is(err, ErrSecretEmbeddedInString) {
		t.Fatalf("err = %v, want ErrSecretEmbeddedInString", err)
	}
}

// TestResolve_OrdinaryJSONLookingString_UnchangedByteForByte proves the
// JSON-embedded-refs amendment is purely additive: a plain string that
// merely happens to parse as JSON, with no marker anywhere inside it (a
// hand-authored, already-concrete literal policy document, say), is
// left completely untouched -- not even re-serialized/reformatted.
func TestResolve_OrdinaryJSONLookingString_UnchangedByteForByte(t *testing.T) {
	l := core.Open(t.TempDir())
	schema := newFakeSchema()
	const literal = `{  "Effect":   "Allow", "Resource": "*" }` // deliberately odd whitespace
	cfgJSON, err := json.Marshal(map[string]string{"policy": literal})
	if err != nil {
		t.Fatal(err)
	}
	intent := intentFile("payments", ri("aws_vpc", "v", OpCreate, string(cfgJSON)))

	p, err := Resolve(l, singleProvider(schema), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var node map[string]interface{}
	json.Unmarshal(p.Delta.Creates[0], &node)
	cfg := node["config"].(map[string]interface{})
	if cfg["policy"] != literal {
		t.Fatalf("policy = %q, want the exact original literal %q byte-for-byte unchanged", cfg["policy"], literal)
	}
}
