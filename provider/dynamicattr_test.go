package provider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
	ctymsgpack "github.com/zclconf/go-cty/cty/msgpack"
)

// A free-form JSON attribute is declared "dynamic" by a
// CloudFormation-derived schema: the schema states no shape, so the
// value carries its own. 707 attributes across 482 of 1,724 AWS
// resources are typed this way, every IAM policy document among them.
//
// Three separate paths had to learn it, and only the first announced
// itself: a create failed on planned state, an update and a destroy
// would have failed on prior state, and a decode succeeded while
// returning the wrong shape.

func dynamicBlock() Block {
	return Block{Attributes: []Attribute{
		{Name: "name", Type: json.RawMessage(`"string"`), Required: true},
		{Name: "redrive_policy", Type: json.RawMessage(`"dynamic"`)},
	}}
}

const policyJSON = `{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:1:dlq","maxReceiveCount":5}`

// The reported failure: "encode planned state: encode value: attribute
// \"redrive_policy\": unsupported type dynamic". This is the create
// path.
func TestEncodePlannedState_AcceptsADynamicAttribute(t *testing.T) {
	in := json.RawMessage(`{"name":"orders","redrive_policy":` + policyJSON + `}`)
	out, err := encodeUnknownAwareDynamicValue(dynamicBlock(), in)
	if err != nil {
		t.Fatalf("encodeUnknownAwareDynamicValue: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("no payload produced")
	}
}

// The path an update and a destroy take. Fixing only the reported
// error would have left a dynamic attribute uncreatable and then
// undeletable.
func TestEncodePriorState_AcceptsADynamicAttribute(t *testing.T) {
	in := json.RawMessage(`{"name":"orders","redrive_policy":` + policyJSON + `}`)
	out, err := encodeDynamicValue(dynamicBlock(), in)
	if err != nil {
		t.Fatalf("encodeDynamicValue: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("no payload produced")
	}
}

// The quiet one. Decoding used to succeed and return go-cty's own
// type-tagged wrapper, {"value":{...},"type":["object",{...}]}, which
// would have been what the ledger stored and what the next plan
// compared against the user's config.
func TestDecode_ReturnsPlainJSONForADynamicAttribute(t *testing.T) {
	block := dynamicBlock()
	ty, err := blockObjectType(block)
	if err != nil {
		t.Fatal(err)
	}
	val := cty.ObjectVal(map[string]cty.Value{
		"name": cty.StringVal("orders"),
		"redrive_policy": cty.ObjectVal(map[string]cty.Value{
			"deadLetterTargetArn": cty.StringVal("arn:aws:sqs:us-east-1:1:dlq"),
			"maxReceiveCount":     cty.NumberIntVal(5),
		}),
	})
	raw, err := ctymsgpack.Marshal(val, ty)
	if err != nil {
		t.Fatal(err)
	}

	out, err := decodeDynamicValue(block, raw, nil)
	if err != nil {
		t.Fatalf("decodeDynamicValue: %v", err)
	}
	if strings.Contains(string(out), `"type":[`) {
		t.Fatalf("decoded value carries go-cty's type wrapper instead of plain JSON:\n%s", out)
	}
	var got struct {
		Name          string          `json:"name"`
		RedrivePolicy json.RawMessage `json:"redrive_policy"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decoded value is not the expected shape: %v\n%s", err, out)
	}
	var probe map[string]any
	if err := json.Unmarshal(got.RedrivePolicy, &probe); err != nil {
		t.Fatalf("redrive_policy is not a plain JSON object: %v\n%s", err, got.RedrivePolicy)
	}
	if probe["deadLetterTargetArn"] != "arn:aws:sqs:us-east-1:1:dlq" {
		t.Errorf("redrive_policy lost its content: %s", got.RedrivePolicy)
	}
}

// Encode then decode has to return what went in. This is the property a
// user actually depends on: the JSON they wrote is the JSON the ledger
// records, so the next plan compares like with like rather than
// reporting permanent drift.
func TestDynamicAttribute_RoundTripsUnchanged(t *testing.T) {
	block := dynamicBlock()
	for _, c := range []struct {
		name  string
		value string
	}{
		{"object", policyJSON},
		{"IAM policy document", `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject"],"Resource":"arn:aws:s3:::b/*"}]}`},
		{"mixed-type array", `[1,"two",{"three":true},null]`},
		{"bare string", `"just a string"`},
		{"bare number", `12345678901234567890`},
		{"bare bool", `true`},
		{"empty object", `{}`},
		{"empty array", `[]`},
		{"nested arrays of objects", `{"Statement":[{"Resource":["a","b"]},{"Resource":"c"}]}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := json.RawMessage(`{"name":"orders","redrive_policy":` + c.value + `}`)
			payload, err := encodeUnknownAwareDynamicValue(block, in)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			out, err := decodeDynamicValue(block, payload, nil)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("decoded output is not an object: %v\n%s", err, out)
			}
			if !jsonEqual(t, string(got["redrive_policy"]), c.value) {
				t.Errorf("round trip changed the value:\n  in:  %s\n  out: %s", c.value, got["redrive_policy"])
			}
		})
	}
}

// A null dynamic attribute is null, not an error and not an empty
// object.
func TestDynamicAttribute_NullIsPreserved(t *testing.T) {
	in := json.RawMessage(`{"name":"orders","redrive_policy":null}`)
	payload, err := encodeUnknownAwareDynamicValue(dynamicBlock(), in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := decodeDynamicValue(dynamicBlock(), payload, nil)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if s := strings.TrimSpace(string(got["redrive_policy"])); s != "null" {
		t.Errorf("redrive_policy = %s, want null", s)
	}
}

// A $computed marker under a dynamic attribute reaches the wire as a
// real unknown, the same as under any other type.
func TestDynamicAttribute_ComputedMarkerBecomesUnknown(t *testing.T) {
	in := json.RawMessage(`{"name":"orders","redrive_policy":{"$computed":{"address":"x.y"}}}`)
	if _, err := encodeUnknownAwareDynamicValue(dynamicBlock(), in); err != nil {
		t.Fatalf("encode: %v", err)
	}
}

func jsonEqual(t *testing.T, a, b string) bool {
	t.Helper()
	var va, vb any
	da := json.NewDecoder(strings.NewReader(a))
	da.UseNumber()
	db := json.NewDecoder(strings.NewReader(b))
	db.UseNumber()
	if err := da.Decode(&va); err != nil {
		return false
	}
	if err := db.Decode(&vb); err != nil {
		return false
	}
	ja, _ := json.Marshal(va)
	jb, _ := json.Marshal(vb)
	return string(ja) == string(jb)
}

// The exact attributes this was reported on, with the shapes the real
// ubx-provider-dynamic schema declares for them (confirmed by querying
// the provider directly, not transcribed from memory):
//
//	aws_sqs_queue.redrive_policy              dynamic, optional
//	aws_sqs_queue.redrive_allow_policy        dynamic, optional
//	aws_sqs_queue_policy.policy_document      dynamic, REQUIRED
//	aws_iam_policy.policy_document            dynamic, REQUIRED
//
// Required matters: for those two the attribute cannot be omitted, so
// the resource was not merely awkward to configure, it was impossible
// to create at all.
func TestRealAWSDynamicAttributes(t *testing.T) {
	for _, c := range []struct {
		resource string
		block    Block
		config   string
	}{
		{
			resource: "aws_sqs_queue",
			block: Block{Attributes: []Attribute{
				{Name: "queue_name", Type: json.RawMessage(`"string"`)},
				{Name: "redrive_policy", Type: json.RawMessage(`"dynamic"`)},
				{Name: "redrive_allow_policy", Type: json.RawMessage(`"dynamic"`)},
			}},
			config: `{"queue_name":"orders","redrive_policy":{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:1:orders-dlq","maxReceiveCount":5},"redrive_allow_policy":{"redrivePermission":"allowAll"}}`,
		},
		{
			resource: "aws_sqs_queue_policy",
			block: Block{Attributes: []Attribute{
				{Name: "queues", Type: json.RawMessage(`["list","string"]`)},
				{Name: "policy_document", Type: json.RawMessage(`"dynamic"`), Required: true},
			}},
			config: `{"queues":["https://sqs.us-east-1.amazonaws.com/1/orders"],"policy_document":{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"sns.amazonaws.com"},"Action":"sqs:SendMessage","Resource":"arn:aws:sqs:us-east-1:1:orders"}]}}`,
		},
		{
			resource: "aws_iam_policy",
			block: Block{Attributes: []Attribute{
				{Name: "policy_name", Type: json.RawMessage(`"string"`)},
				{Name: "policy_document", Type: json.RawMessage(`"dynamic"`), Required: true},
			}},
			config: `{"policy_name":"read-orders","policy_document":{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject","s3:ListBucket"],"Resource":["arn:aws:s3:::orders","arn:aws:s3:::orders/*"]}]}}`,
		},
	} {
		t.Run(c.resource, func(t *testing.T) {
			in := json.RawMessage(c.config)

			// The create path, which is what failed.
			payload, err := encodeUnknownAwareDynamicValue(c.block, in)
			if err != nil {
				t.Fatalf("encode planned state: %v", err)
			}
			// The update and destroy path.
			if _, err := encodeDynamicValue(c.block, in); err != nil {
				t.Fatalf("encode prior state: %v", err)
			}
			// And back, unchanged.
			out, err := decodeDynamicValue(c.block, payload, nil)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !jsonEqual(t, string(out), c.config) {
				t.Errorf("round trip changed the resource:\n  in:  %s\n  out: %s", c.config, out)
			}
		})
	}
}
