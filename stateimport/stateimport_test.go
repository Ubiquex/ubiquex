package stateimport

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestParse_Basic(t *testing.T) {
	data := []byte(`{
		"version": 4,
		"resources": [
			{
				"mode": "managed",
				"type": "aws_s3_bucket",
				"name": "example",
				"instances": [
					{"attributes": {"id": "my-bucket", "bucket": "my-bucket"}}
				]
			}
		]
	}`)
	got, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d resources, want 1", len(got))
	}
	r := got[0]
	if r.Type != "aws_s3_bucket" || r.Name != "example" || r.Module != "" {
		t.Fatalf("got %+v, want type=aws_s3_bucket name=example module=\"\"", r)
	}
	if string(r.Attributes["id"]) != `"my-bucket"` {
		t.Fatalf("attributes[id] = %s, want %q", r.Attributes["id"], "my-bucket")
	}
}

func TestParse_IgnoresDataSourcesAndUnknownModes(t *testing.T) {
	data := []byte(`{
		"version": 4,
		"resources": [
			{"mode": "data", "type": "aws_ami", "name": "ubuntu", "instances": [{"attributes": {"id": "ami-1"}}]},
			{"mode": "managed", "type": "aws_instance", "name": "web", "instances": [{"attributes": {"id": "i-1"}}]}
		]
	}`)
	got, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d resources, want 1 (data source must be dropped)", len(got))
	}
	if got[0].Type != "aws_instance" {
		t.Fatalf("got %+v, want the managed aws_instance only", got[0])
	}
}

func TestParse_IgnoresOutputsField(t *testing.T) {
	data := []byte(`{
		"version": 4,
		"outputs": {"vpc_id": {"value": "vpc-123", "type": "string"}},
		"resources": [
			{"mode": "managed", "type": "aws_vpc", "name": "main", "instances": [{"attributes": {"id": "vpc-123"}}]}
		]
	}`)
	got, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d resources, want 1", len(got))
	}
}

func TestParse_NestedModules(t *testing.T) {
	data := []byte(`{
		"version": 4,
		"resources": [
			{"mode": "managed", "type": "aws_instance", "name": "web", "instances": [{"attributes": {"id": "root"}}]},
			{"mode": "managed", "type": "aws_instance", "name": "web", "module": "module.network", "instances": [{"attributes": {"id": "nested"}}]},
			{"mode": "managed", "type": "aws_instance", "name": "web", "module": "module.network.module.subnet", "instances": [{"attributes": {"id": "deep"}}]}
		]
	}`)
	got, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d resources, want 3", len(got))
	}
	wantModules := []string{"", "network", "network.subnet"}
	for i, want := range wantModules {
		if got[i].Module != want {
			t.Errorf("resources[%d].Module = %q, want %q", i, got[i].Module, want)
		}
	}
}

func TestParse_CountInstances(t *testing.T) {
	data := []byte(`{
		"version": 4,
		"resources": [
			{
				"mode": "managed", "type": "aws_instance", "name": "web",
				"instances": [
					{"index_key": 0, "attributes": {"id": "i-0"}},
					{"index_key": 1, "attributes": {"id": "i-1"}}
				]
			}
		]
	}`)
	got, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d resources, want 2", len(got))
	}
	if got[0].Name != "web[0]" || got[1].Name != "web[1]" {
		t.Fatalf("got names %q, %q, want web[0], web[1]", got[0].Name, got[1].Name)
	}
}

func TestParse_ForEachInstances(t *testing.T) {
	data := []byte(`{
		"version": 4,
		"resources": [
			{
				"mode": "managed", "type": "aws_instance", "name": "web",
				"instances": [
					{"index_key": "us-east-1", "attributes": {"id": "i-a"}},
					{"index_key": "us-west-2", "attributes": {"id": "i-b"}}
				]
			}
		]
	}`)
	got, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d resources, want 2", len(got))
	}
	want := []string{`web["us-east-1"]`, `web["us-west-2"]`}
	for i, w := range want {
		if got[i].Name != w {
			t.Errorf("resources[%d].Name = %q, want %q", i, got[i].Name, w)
		}
	}
}

func TestParse_NoInstances_ProducesNoResources(t *testing.T) {
	data := []byte(`{
		"version": 4,
		"resources": [
			{"mode": "managed", "type": "aws_instance", "name": "web", "instances": []}
		]
	}`)
	got, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d resources, want 0", len(got))
	}
}

func TestParse_MalformedJSON(t *testing.T) {
	_, err := Parse([]byte(`{"version": 4, "resources": [`))
	if !errors.Is(err, ErrMalformedState) {
		t.Fatalf("got %v, want ErrMalformedState", err)
	}
}

func TestParse_Truncated(t *testing.T) {
	full := `{"version": 4, "resources": [{"mode": "managed", "type": "aws_instance", "name": "web", "instances": [{"attributes": {"id": "i-1"}}]}]}`
	truncated := full[:len(full)-20]
	_, err := Parse([]byte(truncated))
	if !errors.Is(err, ErrMalformedState) {
		t.Fatalf("got %v, want ErrMalformedState", err)
	}
}

func TestParse_UnsupportedVersion(t *testing.T) {
	_, err := Parse([]byte(`{"version": 3, "resources": []}`))
	if !errors.Is(err, ErrMalformedState) {
		t.Fatalf("got %v, want ErrMalformedState", err)
	}
	if !strings.Contains(err.Error(), "unsupported state version 3") {
		t.Fatalf("expected the version named in the error, got: %v", err)
	}
}

// TestParse_HugeState verifies a synthetic 1000-resource state file parses
// correctly and completely -- docs/architecture.md's "Scale: bounded
// memory, not streaming" accepts one json.Unmarshal of the whole file at
// foundational-slice scale; this proves that scale is handled correctly,
// not just assumed fast enough.
func TestParse_HugeState(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"version": 4, "resources": [`)
	const n = 1000
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"mode":"managed","type":"aws_instance","name":"web%d","instances":[{"attributes":{"id":"i-%d"}}]}`, i, i)
	}
	b.WriteString(`]}`)

	got, err := Parse([]byte(b.String()))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != n {
		t.Fatalf("got %d resources, want %d", len(got), n)
	}
	seen := map[string]bool{}
	for _, r := range got {
		if seen[r.Name] {
			t.Fatalf("duplicate name %q in parsed output", r.Name)
		}
		seen[r.Name] = true
	}
}

func TestBuildLookup_Default(t *testing.T) {
	attrs := map[string]json.RawMessage{"id": json.RawMessage(`"i-123"`)}
	got, err := BuildLookup("aws_instance", attrs)
	if err != nil {
		t.Fatalf("BuildLookup: %v", err)
	}
	if string(got) != `{"id":"i-123"}` {
		t.Fatalf("got %s, want {\"id\":\"i-123\"}", got)
	}
}

func TestBuildLookup_S3BucketAugmented(t *testing.T) {
	attrs := map[string]json.RawMessage{
		"id":     json.RawMessage(`"my-bucket"`),
		"bucket": json.RawMessage(`"my-bucket"`),
		"arn":    json.RawMessage(`"arn:aws:s3:::my-bucket"`),
	}
	got, err := BuildLookup("aws_s3_bucket", attrs)
	if err != nil {
		t.Fatalf("BuildLookup: %v", err)
	}
	var m map[string]string
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatal(err)
	}
	if m["id"] != "my-bucket" || m["bucket"] != "my-bucket" {
		t.Fatalf("got %v, want id and bucket both set, arn absent", m)
	}
	if _, ok := m["arn"]; ok {
		t.Fatalf("got %v, arn must not be duplicated into the lookup", m)
	}
}

func TestBuildLookup_IAMRoleAugmented(t *testing.T) {
	attrs := map[string]json.RawMessage{
		"id":   json.RawMessage(`"my-role"`),
		"name": json.RawMessage(`"my-role"`),
		"arn":  json.RawMessage(`"arn:aws:iam::123:role/my-role"`),
	}
	got, err := BuildLookup("aws_iam_role", attrs)
	if err != nil {
		t.Fatalf("BuildLookup: %v", err)
	}
	var m map[string]string
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatal(err)
	}
	if m["id"] != "my-role" || m["name"] != "my-role" {
		t.Fatalf("got %v, want id and name both set", m)
	}
}

func TestBuildLookup_NoID(t *testing.T) {
	attrs := map[string]json.RawMessage{"name": json.RawMessage(`"no-id-here"`)}
	_, err := BuildLookup("aws_instance", attrs)
	if !errors.Is(err, ErrNoIdentity) {
		t.Fatalf("got %v, want ErrNoIdentity", err)
	}
}

func TestBuildLookup_NullID(t *testing.T) {
	attrs := map[string]json.RawMessage{"id": json.RawMessage(`null`)}
	_, err := BuildLookup("aws_instance", attrs)
	if !errors.Is(err, ErrNoIdentity) {
		t.Fatalf("got %v, want ErrNoIdentity", err)
	}
}
