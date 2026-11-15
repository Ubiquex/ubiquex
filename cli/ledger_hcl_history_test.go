package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// hclCallEntry is a ledger entry in exactly the shape a blueprint call made
// through a .ubx.hcl file left behind, before UBI-301 removed that medium.
//
// Written by hand rather than produced, because nothing can produce one
// any more. The shape is what the removed path wrote: blueprint.ExpandCalls
// expanded the call into plain creates and cleared blueprint_calls, so
// nothing in the entry says "HCL". What it carries is each create's
// provenance, a "blueprint" IntentSource with the content-addressed ref,
// the declared source as written in the .ubx.hcl block, and the arguments
// the call was made with (UBI-282, UBI-287). The ID is filled in from the
// content hash by the test.
const hclCallEntry = `{
  "schema_version": 2,
  "id": "ID",
  "stack": "demo",
  "parent": "",
  "kind": "change",
  "intent": {"summary": "platform from demo.ubx.hcl"},
  "delta": {
    "creates": [
      {
        "stack": "demo", "type": "fake_widget", "name": "primary",
        "config": {"name": "widget-from-hcl"},
        "provider": {"source": "registry.terraform.io/ubiquex/fake", "version": "0.0.1"},
        "sources": [{
          "kind": "blueprint",
          "ref": "platform:sha256:9b1e5a3f0c2d4e6f8a1b3c5d7e9f0a2b4c6d8e0f1a3b5c7d9e1f3a5b7c9d1e3f",
          "declared_source": "/srv/blueprints/platform",
          "declared_args": {"primary_name": "widget-from-hcl"}
        }]
      },
      {
        "stack": "demo", "type": "fake_widget", "name": "mirror",
        "config": {"name": "widget-from-hcl-mirror"},
        "depends_on": ["demo.fake_widget.primary"],
        "provider": {"source": "registry.terraform.io/ubiquex/fake", "version": "0.0.1"},
        "sources": [{
          "kind": "blueprint",
          "ref": "platform:sha256:9b1e5a3f0c2d4e6f8a1b3c5d7e9f0a2b4c6d8e0f1a3b5c7d9e1f3a5b7c9d1e3f",
          "declared_source": "/srv/blueprints/platform",
          "declared_args": {"primary_name": "widget-from-hcl"}
        }]
      }
    ]
  },
  "resolution": {"resolved_at": "2026-09-14T10:00:00Z"},
  "cost_delta": {"monthly_usd": 0},
  "blast_radius": {"creates": 2, "modifies": 0, "destroys": 0},
  "acceptance": {"method": "local", "approvers": ["roozbeh"], "accepted_at": "2026-09-14T10:05:00Z"},
  "status": "accepted"
}`

// writeHCLCallLedger lays the entry down the way the git directory store
// keeps it: ledger/proposals/<id>.prop.json, and the head in
// .ubx/ledger.lock.
func writeHCLCallLedger(t *testing.T) (ledgerDir, id string) {
	t.Helper()
	var p core.Proposal
	if err := json.Unmarshal([]byte(hclCallEntry), &p); err != nil {
		t.Fatal(err)
	}
	id, err := core.Hash(&p)
	if err != nil {
		t.Fatal(err)
	}
	ledgerDir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(ledgerDir, "ledger", "proposals"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ledgerDir, ".ubx"), 0o755); err != nil {
		t.Fatal(err)
	}
	entry := strings.Replace(hclCallEntry, `"id": "ID"`, `"id": "`+id+`"`, 1)
	writeFile(t, filepath.Join(ledgerDir, "ledger", "proposals", id+".prop.json"), entry)
	writeFile(t, filepath.Join(ledgerDir, ".ubx", "ledger.lock"), id+"\n")

	// The ship, recorded through the engine's own apply-record API. Apply
	// records are sealed by hash and verify checks them, and nothing about
	// their format is part of what UBI-301 removes, so they are written by
	// the code that owns them rather than forged by hand. Only the
	// proposal, the part whose producer is gone, is hand-written.
	l := core.Open(ledgerDir)
	rec, err := l.BeginApply(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"primary", "mirror"} {
		rec.Resources = append(rec.Resources, &core.ResourceApply{
			Address: core.Address{Stack: "demo", Type: "fake_widget", Name: name},
			Transitions: []core.Transition{
				{State: "pending", At: "2026-09-14T10:06:00Z"},
				{State: "in_flight", At: "2026-09-14T10:06:00Z"},
				{State: "applied", At: "2026-09-14T10:06:01Z"},
			},
			ProviderResult: json.RawMessage(`{"id":"` + name + `-id","name":"widget-from-hcl"}`),
			Lookup:         json.RawMessage(`{"id":"` + name + `-id"}`),
		})
	}
	if _, err := l.SealApply(rec, core.ApplySummary{
		Outcome: "applied", StartedAt: "2026-09-14T10:06:00Z", FinishedAt: "2026-09-14T10:06:01Z", ResourcesApplied: 2,
	}); err != nil {
		t.Fatal(err)
	}
	return ledgerDir, id
}

// TestLedger_HCLBlueprintCallHistoryStillReads is UBI-301's own criterion:
// a stack can no longer be authored in .ubx.hcl, but a ledger holding what
// one produced is history, and still reads, verifies and explains itself.
func TestLedger_HCLBlueprintCallHistoryStillReads(t *testing.T) {
	ledgerDir, id := writeHCLCallLedger(t)

	l := core.Open(ledgerDir)
	p, err := l.Read(id)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(p.Delta.Creates) != 2 {
		t.Fatalf("creates = %d, want 2", len(p.Delta.Creates))
	}
	res, err := core.VerifyChain(l)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !res.ChainIntact {
		t.Fatalf("a .ubx.hcl-era ledger no longer verifies: %+v", res.Findings)
	}
	render, err := core.RenderProposal(p)
	if err != nil {
		t.Fatalf("RenderProposal: %v", err)
	}
	for _, want := range []string{
		`sources[0].declared_source = "/srv/blueprints/platform"`,
		`sources[0].declared_args.primary_name = "widget-from-hcl"`,
		`sources[0].kind = "blueprint"`,
	} {
		if !strings.Contains(string(render), want) {
			t.Fatalf("the render of a .ubx.hcl-era entry lost %q:\n%s", want, render)
		}
	}

	out, err := runUbx(t, nil, "verify", "--ledger-dir", ledgerDir)
	if err != nil || !strings.Contains(out, "chain: intact") {
		t.Fatalf("ubx verify: %v\n%s", err, out)
	}

	out, err = runUbx(t, nil, "why", "demo.fake_widget.primary", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx why: %v\n%s", err, out)
	}
	if !strings.Contains(out, "source: blueprint platform:sha256:") {
		t.Fatalf("ubx why no longer shows the blueprint provenance a .ubx.hcl call recorded:\n%s", out)
	}

	out, err = runUbx(t, nil, "why", id, "--ledger-dir", ledgerDir)
	if err != nil || !strings.Contains(out, "platform from demo.ubx.hcl") {
		t.Fatalf("ubx why <id>: %v\n%s", err, out)
	}

	out, err = runUbx(t, nil, "history", "--stack", "demo", "--ledger-dir", ledgerDir)
	if err != nil || !strings.Contains(out, id[:12]) {
		t.Fatalf("ubx history: %v\n%s", err, out)
	}
}
