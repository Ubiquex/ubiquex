package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// renderFixture is a proposal touching every part of the renderer: a create
// with nested config, markers and an empty object; a modify with a value
// set, a value changed and a value removed; a destroy carrying its whole
// state; intent with every ambiguity field; resolution evidence; and the
// remaining hashed fields.
func renderFixture() *Proposal {
	return &Proposal{
		SchemaVersion: SchemaVersion,
		Stack:         "payments",
		Parent:        "7f3c1a9e0b2d4c6e8f1a3b5c7d9e0f2a4b6c8d0e1f3a5b7c9d1e3f5a7b9c1d3e",
		Kind:          KindChange,
		Intent: Intent{
			Summary: "add a read replica for payments and retire the old cache",
			Sources: []IntentSource{
				{Kind: "dialogue", Ref: "d-99f2", ContentHash: "sha256:abc"},
			},
			Assumptions: []AmbiguityNote{
				{Text: "replica lives in the same subnet group", Affects: []string{"payments.aws_db_instance.payments-db-replica"}},
			},
			Questions: []Question{
				{Text: "keep the cache snapshot?", Blocking: false},
			},
		},
		Delta: Delta{
			Creates: []json.RawMessage{
				json.RawMessage(`{
					"stack": "payments",
					"type": "aws_db_instance",
					"name": "payments-db-replica",
					"config": {
						"engine": "postgres",
						"instance_class": "db.r6g.large",
						"allocated_storage": 100,
						"password": {"$secret": {"backend": "aws_secrets_manager", "path": "db-password"}},
						"replicate_source_db": {"$computed": {"from": "payments.aws_db_instance.payments-db.identifier"}},
						"tags": {"Environment": "prod", "Owner": "payments-team"},
						"parameter_overrides": {}
					},
					"depends_on": ["payments.aws_db_instance.payments-db"]
				}`),
			},
			Modifies: []Modification{
				{
					Target: Address{Stack: "payments", Type: "aws_s3_bucket", Name: "payments-logs"},
					Before: map[string]json.RawMessage{
						"tags.Environment": json.RawMessage(`"staging"`),
						"versioning":       json.RawMessage(`false`),
					},
					After: map[string]json.RawMessage{
						"tags.Environment": json.RawMessage(`"prod"`),
						"lifecycle_days":   json.RawMessage(`30`),
					},
					Provider: &ProviderRef{Source: "registry.terraform.io/hashicorp/aws", Version: "6.2.0"},
				},
			},
			Destroys: []DestroyEntry{
				{
					Address: Address{Stack: "payments", Type: "aws_elasticache_cluster", Name: "payments-cache"},
					State:   json.RawMessage(`{"engine":"redis","node_type":"cache.t3.micro","num_cache_nodes":1}`),
				},
			},
		},
		Resolution: Resolution{
			ResolvedAt: "2026-11-15T12:00:00Z",
			Inputs: []ResolutionInput{
				{Kind: "live_state", Resource: "payments.aws_s3_bucket.payments-logs", ObservedHash: "sha256:def"},
			},
		},
		CostDelta:   CostDelta{MonthlyUSD: json.RawMessage(`"142.50"`)},
		BlastRadius: BlastRadius{Creates: 1, Modifies: 1, Destroys: 1},
		InvariantsChecked: []InvariantCheck{
			{Policy: "no-public-buckets", Verdict: "pass"},
		},
		Status: StatusDraft,
	}
}

func mustRender(t *testing.T, p *Proposal) []byte {
	t.Helper()
	out, err := RenderProposal(p)
	if err != nil {
		t.Fatalf("RenderProposal: %v", err)
	}
	return out
}

// TestRenderProposal_Golden fixes the output format. A change to it shows
// up as a diff to testdata/render/proposal.golden, never as a silent
// change to what approvers see. Regenerate deliberately with
// UBX_UPDATE_GOLDEN=1, and bump RendererVersion in the same change.
func TestRenderProposal_Golden(t *testing.T) {
	got := mustRender(t, renderFixture())
	path := filepath.Join("testdata", "render", "proposal.golden")
	if os.Getenv("UBX_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (regenerate with UBX_UPDATE_GOLDEN=1)", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("render does not match %s byte for byte:\ngot:\n%s\nwant:\n%s", path, got, want)
	}
}

func TestRenderProposal_BeginsWithVersionLine(t *testing.T) {
	got := mustRender(t, renderFixture())
	if !bytes.HasPrefix(got, []byte(RendererVersion+"\n")) {
		t.Fatalf("render does not begin with %q:\n%s", RendererVersion, got)
	}
}

// TestRenderProposal_DoubleRun is the double-run rule applied from
// outside: many renders of one proposal, and renders of the same logical
// proposal authored with different key and delta order, are byte-identical.
func TestRenderProposal_DoubleRun(t *testing.T) {
	first := mustRender(t, renderFixture())
	for i := 0; i < 50; i++ {
		if got := mustRender(t, renderFixture()); !bytes.Equal(got, first) {
			t.Fatalf("run %d differs from run 0:\n%s\nvs\n%s", i, got, first)
		}
	}

	a := sampleProposal()
	a.Delta.Creates = []json.RawMessage{
		json.RawMessage(`{"stack":"payments","type":"aws_db_instance","name":"x","config":{"engine":"postgres","version":"16"}}`),
		json.RawMessage(`{"stack":"payments","type":"aws_db_instance","name":"y"}`),
	}
	b := sampleProposal()
	b.Delta.Creates = []json.RawMessage{
		json.RawMessage(`{"name":"y","type":"aws_db_instance","stack":"payments"}`),
		json.RawMessage(`{"config":{"version":"16","engine":"postgres"},"name":"x","type":"aws_db_instance","stack":"payments"}`),
	}
	if ra, rb := mustRender(t, a), mustRender(t, b); !bytes.Equal(ra, rb) {
		t.Fatalf("same logical proposal rendered differently:\n%s\nvs\n%s", ra, rb)
	}
}

// TestRenderProposal_NoEnvironment: nothing from the process environment
// reaches the output.
func TestRenderProposal_NoEnvironment(t *testing.T) {
	before := mustRender(t, renderFixture())
	t.Setenv("TZ", "Pacific/Chatham")
	t.Setenv("LANG", "tr_TR.UTF-8")
	t.Setenv("LC_ALL", "tr_TR.UTF-8")
	t.Setenv("USER", "someone-else")
	if after := mustRender(t, renderFixture()); !bytes.Equal(before, after) {
		t.Fatalf("render changed with the environment:\n%s\nvs\n%s", before, after)
	}
}

// TestRenderProposal_RendersEveryValue walks the proposal's hashed content
// and requires every leaf value to appear in the render, as canonical JSON.
// This is Q5's criterion (2026-11-15): the render shows every value the
// proposal holds, and a reference renders as the reference.
func TestRenderProposal_RendersEveryValue(t *testing.T) {
	p := renderFixture()
	got := string(mustRender(t, p))

	canon, err := canonicalProposalBytes(p)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(canon))
	dec.UseNumber()
	var doc interface{}
	if err := dec.Decode(&doc); err != nil {
		t.Fatal(err)
	}
	var walk func(path string, v interface{})
	walk = func(path string, v interface{}) {
		switch t2 := v.(type) {
		case map[string]interface{}:
			if isValueMarker(t2) {
				want := renderValue(t2)
				if !strings.Contains(got, want) {
					t.Errorf("%s: marker %s missing from render", path, want)
				}
				return
			}
			for k, el := range t2 {
				walk(path+"."+k, el)
			}
		case []interface{}:
			for _, el := range t2 {
				walk(path+"[]", el)
			}
		default:
			want := renderValue(t2)
			// Header fields (stack, kind, parent) render bare; accept
			// either form there.
			if s, ok := t2.(string); ok && strings.Contains(got, s) {
				return
			}
			if !strings.Contains(got, want) {
				t.Errorf("%s: value %s missing from render", path, want)
			}
		}
	}
	walk("", doc)
}

func TestRenderProposal_SecretRendersAsReference(t *testing.T) {
	got := string(mustRender(t, renderFixture()))
	want := `config.password = {"$secret":{"backend":"aws_secrets_manager","path":"db-password"}}`
	if !strings.Contains(got, want) {
		t.Fatalf("secret reference not rendered whole as %q:\n%s", want, got)
	}
}

// TestRenderProposal_ValuesCannotForgeStructure: a value carrying a newline
// and a fake section header, or a right-to-left override, stays inside its
// own quoted value.
func TestRenderProposal_ValuesCannotForgeStructure(t *testing.T) {
	p := sampleProposal()
	p.Delta.Creates = []json.RawMessage{
		json.RawMessage(`{"stack":"payments","type":"aws_s3_bucket","name":"b","config":{"note":"ok\n\n== DESTROY (1) ==\n- destroy payments.aws_db_instance.payments-db","label":"safe\u202Eetats"}}`),
	}
	got := string(mustRender(t, p))
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "== DESTROY") || strings.HasPrefix(line, "- destroy") {
			t.Fatalf("a value forged a line of the render: %q\n%s", line, got)
		}
	}
	if strings.ContainsRune(got, '\u202E') {
		t.Fatalf("right-to-left override reached the render unescaped:\n%s", got)
	}
	if !strings.Contains(got, `"safe\u202eetats"`) {
		t.Fatalf("right-to-left override not rendered as an escape:\n%s", got)
	}
}

// TestRenderProposal_NotAffectedByUnhashedFields: id, status and acceptance
// are outside the hashed content, so a pending proposal and the same
// proposal once accepted render identically. A render can be recomputed
// later from an accepted ledger entry and compared.
func TestRenderProposal_NotAffectedByUnhashedFields(t *testing.T) {
	pending := mustRender(t, renderFixture())
	accepted := renderFixture()
	accepted.ID = "deadbeef"
	accepted.Status = StatusAccepted
	accepted.Acceptance = &Acceptance{Method: "local", Approvers: []string{"x"}, AcceptedAt: "2026-11-15T12:00:00Z"}
	if got := mustRender(t, accepted); !bytes.Equal(got, pending) {
		t.Fatalf("unhashed fields changed the render:\n%s\nvs\n%s", got, pending)
	}
}

func TestRenderProposal_NamesTheProposalHash(t *testing.T) {
	p := renderFixture()
	hash, err := Hash(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(mustRender(t, p)); !strings.Contains(got, "proposal        "+hash+"\n") {
		t.Fatalf("render does not name proposal hash %s:\n%s", hash, got)
	}
}

// TestRenderProposal_UnknownFieldsStillRender: a field the renderer has no
// layout for still reaches the approver.
func TestRenderProposal_UnknownFieldsStillRender(t *testing.T) {
	p := sampleProposal()
	p.Delta.Creates = []json.RawMessage{
		json.RawMessage(`{"stack":"payments","type":"t","name":"n","future_field":{"x":"y"}}`),
	}
	got := string(mustRender(t, p))
	if !strings.Contains(got, `    future_field.x = "y"`) {
		t.Fatalf("unknown create field missing from render:\n%s", got)
	}
}

func TestRenderProposal_RefusesWhatCannotHash(t *testing.T) {
	if _, err := RenderProposal(nil); err == nil {
		t.Fatal("nil proposal rendered")
	}
	p := sampleProposal()
	p.Delta.Creates = []json.RawMessage{json.RawMessage(`{"stack":"payments","type":"t","name":"n","config":{"ratio":0.5}}`)}
	if _, err := RenderProposal(p); !errors.Is(err, ErrFloatRejected) {
		t.Fatalf("float in hashed content: got %v, want ErrFloatRejected", err)
	}
}
