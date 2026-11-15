package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

// TestAcceptFromLoopback_RoundTrip: stage, accept, read back. The record
// names the render, the render is retrievable and verified, and it is a
// render of the proposal the ledger holds.
func TestAcceptFromLoopback_RoundTrip(t *testing.T) {
	l := Open(t.TempDir())
	p := sampleProposal()

	renderedHash, shown, err := l.StageRender(p)
	if err != nil {
		t.Fatalf("StageRender: %v", err)
	}
	if HashRendered(shown) != renderedHash {
		t.Fatalf("shown bytes hash to %s, staged as %s", HashRendered(shown), renderedHash)
	}

	accepted, err := AcceptFromLoopback(l, p, renderedHash)
	if err != nil {
		t.Fatalf("AcceptFromLoopback: %v", err)
	}

	read, err := l.Read(accepted.ID)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	a := read.Acceptance
	if a == nil {
		t.Fatal("no acceptance on the ledger entry")
	}
	if a.Method != MethodLoopback {
		t.Errorf("Method = %q, want %q", a.Method, MethodLoopback)
	}
	if len(a.Approvers) != 1 || a.Approvers[0] != LoopbackApprover {
		t.Errorf("Approvers = %v, want exactly [%s]", a.Approvers, LoopbackApprover)
	}
	if a.RenderedHash != renderedHash {
		t.Errorf("RenderedHash = %q, want %q", a.RenderedHash, renderedHash)
	}
	if a.AcceptedAt == "" {
		t.Error("AcceptedAt empty")
	}

	stored, err := l.ReadRendered(a.RenderedHash)
	if err != nil {
		t.Fatalf("ReadRendered: %v", err)
	}
	if !bytes.Equal(stored, shown) {
		t.Fatalf("stored render differs from what was shown")
	}
	again, err := RenderProposal(read)
	if err != nil {
		t.Fatalf("RenderProposal(read back): %v", err)
	}
	if !bytes.Equal(again, stored) {
		t.Fatalf("the ledger entry does not re-render to the stored bytes:\n%s\nvs\n%s", again, stored)
	}

	res, err := VerifyChain(l)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !res.ChainIntact {
		t.Fatalf("chain not intact after a loopback acceptance: %+v", res.Findings)
	}
}

// TestAcceptFromLoopback_NoOSUsername: the loopback record carries no
// identity. Whatever the OS user is, it is not in the record.
func TestAcceptFromLoopback_NoOSUsername(t *testing.T) {
	l := Open(t.TempDir())
	p := sampleProposal()
	h, _, err := l.StageRender(p)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := AcceptFromLoopback(l, p, h)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(accepted.Acceptance)
	if err != nil {
		t.Fatal(err)
	}
	if u := localApprover(); u != "unknown" && strings.Contains(string(raw), u) {
		t.Fatalf("loopback acceptance contains the OS username %q: %s", u, raw)
	}
}

// TestAcceptance_RenderedHashExcludedFromContentHash: setting RenderedHash
// never moves the proposal's hash, and a loopback-accepted proposal's ID
// is the hash of the proposal as it was before acceptance.
func TestAcceptance_RenderedHashExcludedFromContentHash(t *testing.T) {
	p := sampleProposal()
	before, err := Hash(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Acceptance = &Acceptance{Method: MethodLoopback, Approvers: []string{LoopbackApprover}, RenderedHash: strings.Repeat("a", 64)}
	after, err := Hash(p)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("RenderedHash changed the content hash: %s -> %s", before, after)
	}

	l := Open(t.TempDir())
	q := sampleProposal()
	h, _, err := l.StageRender(q)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := AcceptFromLoopback(l, q, h)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.ID != before {
		t.Fatalf("accepted ID %s, want the pre-acceptance hash %s", accepted.ID, before)
	}
}

// TestAcceptFromLoopback_RefusesUnstoredRender: the write order is
// enforced, not just documented. A hash nobody stored is refused and the
// ledger does not move.
func TestAcceptFromLoopback_RefusesUnstoredRender(t *testing.T) {
	l := Open(t.TempDir())
	p := sampleProposal()
	rendered, err := RenderProposal(p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = AcceptFromLoopback(l, p, HashRendered(rendered))
	if !errors.Is(err, ErrRenderNotStored) {
		t.Fatalf("got %v, want ErrRenderNotStored", err)
	}
	assertHeadEmpty(t, l)
}

// TestAcceptFromLoopback_RefusesAnotherProposalsRender: approval given
// while looking at one proposal cannot be attached to a different one.
func TestAcceptFromLoopback_RefusesAnotherProposalsRender(t *testing.T) {
	l := Open(t.TempDir())
	shownProposal := sampleProposal()
	h, _, err := l.StageRender(shownProposal)
	if err != nil {
		t.Fatal(err)
	}
	other := sampleProposal()
	other.Delta.Creates = []json.RawMessage{
		json.RawMessage(`{"stack":"payments","type":"aws_db_instance","name":"payments-db","config":{"engine":"mysql"}}`),
	}
	_, err = AcceptFromLoopback(l, other, h)
	if !errors.Is(err, ErrRenderMismatch) {
		t.Fatalf("got %v, want ErrRenderMismatch", err)
	}
	assertHeadEmpty(t, l)
}

// TestAcceptFromLoopback_RefusesTamperedRender: stored bytes that no
// longer hash to their name are not evidence, and are never served or
// accepted against.
func TestAcceptFromLoopback_RefusesTamperedRender(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)
	p := sampleProposal()
	h, _, err := l.StageRender(p)
	if err != nil {
		t.Fatal(err)
	}
	path := newGitLedgerStore(dir).renderedPath(h)
	if err := os.WriteFile(path, []byte("ubx-render proposal/v1\n\nnothing to see\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ReadRendered(h); !errors.Is(err, ErrRenderedHashMismatch) {
		t.Fatalf("ReadRendered on tampered bytes: got %v, want ErrRenderedHashMismatch", err)
	}
	if _, err := AcceptFromLoopback(l, p, h); !errors.Is(err, ErrRenderedHashMismatch) {
		t.Fatalf("AcceptFromLoopback on tampered bytes: got %v, want ErrRenderedHashMismatch", err)
	}
	assertHeadEmpty(t, l)
}

// TestStageRender_IdempotentAndCreateOnly: staging the same proposal twice
// is a no-op the second time, and the store never overwrites a render.
func TestStageRender_IdempotentAndCreateOnly(t *testing.T) {
	dir := t.TempDir()
	l := Open(dir)
	p := sampleProposal()
	h1, b1, err := l.StageRender(p)
	if err != nil {
		t.Fatal(err)
	}
	h2, b2, err := l.StageRender(p)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 || !bytes.Equal(b1, b2) {
		t.Fatalf("second stage differs: %s vs %s", h1, h2)
	}
	store := newGitLedgerStore(dir)
	err = store.WriteRenderedIfAbsent(context.Background(), h1, []byte("different"))
	if !errors.Is(err, ErrStoreObjectExists) {
		t.Fatalf("overwrite of a stored render: got %v, want ErrStoreObjectExists", err)
	}
	if got, _ := l.ReadRendered(h1); !bytes.Equal(got, b1) {
		t.Fatal("stored render changed after a refused overwrite")
	}
	entries, err := os.ReadDir(store.renderedDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("rendered/ holds %d entries, want 1 (no temp files left behind)", len(entries))
	}
}

// TestLedger_ReadsEntryWrittenBeforeRenderedHash: a ledger entry in the
// exact shape written before UBI-294 (no rendered_hash key at all) reads,
// verifies, and keeps its meaning.
func TestLedger_ReadsEntryWrittenBeforeRenderedHash(t *testing.T) {
	const preChange = `{"schema_version":2,"id":"ID","stack":"payments","parent":"","kind":"change",` +
		`"intent":{"summary":"postgres for payments","sources":[{"kind":"dialogue","ref":"d-99f2","content_hash":"sha256:abc"}]},` +
		`"delta":{"creates":[{"stack":"payments","type":"aws_db_instance","name":"payments-db","config":{"engine":"postgres"}}]},` +
		`"resolution":{"resolved_at":"2026-07-10T00:00:00Z"},"cost_delta":{"monthly_usd":59},` +
		`"blast_radius":{"creates":1,"modifies":0,"destroys":0},` +
		`"acceptance":{"method":"local","approvers":["roozbeh"],"accepted_at":"2026-09-01T10:00:00Z"},"status":"accepted"}`

	var p Proposal
	if err := json.Unmarshal([]byte(preChange), &p); err != nil {
		t.Fatal(err)
	}
	id, err := Hash(&p)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(strings.Replace(preChange, `"id":"ID"`, `"id":"`+id+`"`, 1))

	dir := t.TempDir()
	store := newGitLedgerStore(dir)
	ctx := context.Background()
	if err := store.WriteProposalIfAbsent(ctx, id, raw); err != nil {
		t.Fatal(err)
	}
	if err := store.AdvanceHead(ctx, "", id); err != nil {
		t.Fatal(err)
	}

	l := Open(dir)
	read, err := l.Read(id)
	if err != nil {
		t.Fatalf("Read pre-change entry: %v", err)
	}
	if read.Acceptance == nil || read.Acceptance.Method != "local" || read.Acceptance.RenderedHash != "" {
		t.Fatalf("pre-change acceptance read back as %+v", read.Acceptance)
	}
	if len(read.Acceptance.Approvers) != 1 || read.Acceptance.Approvers[0] != "roozbeh" {
		t.Fatalf("pre-change approvers changed meaning: %v", read.Acceptance.Approvers)
	}
	res, err := VerifyChain(l)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !res.ChainIntact {
		t.Fatalf("pre-change ledger no longer verifies: %+v", res.Findings)
	}
}

// TestAccept_LocalUnchanged: the local tier still records the OS user and
// never a rendered hash.
func TestAccept_LocalUnchanged(t *testing.T) {
	l := Open(t.TempDir())
	accepted, err := Accept(l, sampleProposal())
	if err != nil {
		t.Fatal(err)
	}
	a := accepted.Acceptance
	if a.Method != "local" || a.RenderedHash != "" {
		t.Fatalf("local acceptance changed: %+v", a)
	}
	if len(a.Approvers) != 1 || a.Approvers[0] != localApprover() {
		t.Fatalf("local approvers = %v, want [%s]", a.Approvers, localApprover())
	}
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "rendered_hash") {
		t.Fatalf("local acceptance serialises a rendered_hash key: %s", raw)
	}
}

func assertHeadEmpty(t *testing.T, l *Ledger) {
	t.Helper()
	head, err := l.Head()
	if err != nil {
		t.Fatal(err)
	}
	if head != "" {
		t.Fatalf("ledger head moved to %s on a refused acceptance", head)
	}
}
