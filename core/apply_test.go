package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// testProposalID stands in for a real proposal ID (always a 64-hex-char
// SHA-256 digest in production) -- the apply-attempt filename pattern
// requires exactly that shape (mirroring hexHash64's own convention for
// ledger heads), so these hermetic tests use a realistic-looking one
// rather than an arbitrary short placeholder.
const testProposalID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestApplyHash_DeterministicRegardlessOfResourceOrder(t *testing.T) {
	base := func(order []string) *ApplyRecord {
		rec := &ApplyRecord{SchemaVersion: ApplyRecordSchemaVersion, Proposal: "p1", Parent: "", Attempt: 1}
		for _, n := range order {
			rec.Resources = append(rec.Resources, &ResourceApply{
				Address:     Address{Stack: "payments", Type: "fake_widget", Name: n},
				Transitions: []Transition{{State: ResourceApplied, At: "2026-07-17T00:00:00Z"}},
			})
		}
		rec.Summary = &ApplySummary{Outcome: "applied", StartedAt: "2026-07-17T00:00:00Z", FinishedAt: "2026-07-17T00:00:01Z", ResourcesApplied: int64(len(order))}
		return rec
	}
	h1, err := ApplyHash(base([]string{"a", "b"}))
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	h2, err := ApplyHash(base([]string{"b", "a"}))
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("hash depends on Resources slice order: %s vs %s", h1, h2)
	}
}

func TestApplyHash_ChangesWithContent(t *testing.T) {
	rec := &ApplyRecord{SchemaVersion: ApplyRecordSchemaVersion, Proposal: "p1", Attempt: 1,
		Summary: &ApplySummary{Outcome: "applied"}}
	h1, err := ApplyHash(rec)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	rec.Summary.Outcome = "failed"
	h2, err := ApplyHash(rec)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if h1 == h2 {
		t.Fatal("hash did not change when content changed")
	}
}

func TestApplyHash_ExcludesIDOnly(t *testing.T) {
	rec := &ApplyRecord{SchemaVersion: ApplyRecordSchemaVersion, Proposal: "p1", Attempt: 1,
		Summary: &ApplySummary{Outcome: "applied"}}
	h1, err := ApplyHash(rec)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	rec.ID = "irrelevant-should-not-affect-hash"
	h2, err := ApplyHash(rec)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if h1 != h2 {
		t.Fatal("hash changed based on the ID field, which must be excluded")
	}
}

func TestBeginApply_FirstAttempt_NoParent(t *testing.T) {
	l := Open(t.TempDir())
	rec, err := l.BeginApply(testProposalID)
	if err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	if rec.Attempt != 1 || rec.Parent != "" {
		t.Fatalf("attempt=%d parent=%q, want 1/\"\"", rec.Attempt, rec.Parent)
	}
	if rec.Sealed() {
		t.Fatal("a freshly begun attempt must not be sealed")
	}
}

func TestBeginApply_SecondAttempt_ChainsToPriorSealedID(t *testing.T) {
	l := Open(t.TempDir())
	rec1, err := l.BeginApply(testProposalID)
	if err != nil {
		t.Fatalf("begin apply 1: %v", err)
	}
	sealed1, err := l.SealApply(rec1, ApplySummary{Outcome: "applied", StartedAt: "t0", FinishedAt: "t1"})
	if err != nil {
		t.Fatalf("seal 1: %v", err)
	}
	if sealed1.ID == "" {
		t.Fatal("sealed record has no ID")
	}

	rec2, err := l.BeginApply(testProposalID)
	if err != nil {
		t.Fatalf("begin apply 2: %v", err)
	}
	if rec2.Attempt != 2 {
		t.Fatalf("attempt = %d, want 2", rec2.Attempt)
	}
	if rec2.Parent != sealed1.ID {
		t.Fatalf("parent = %q, want %q (chained to the prior sealed attempt)", rec2.Parent, sealed1.ID)
	}
}

func TestBeginApply_UnsealedPriorAttempt_NeverBecomesParent(t *testing.T) {
	l := Open(t.TempDir())
	rec1, err := l.BeginApply(testProposalID)
	if err != nil {
		t.Fatalf("begin apply 1: %v", err)
	}
	// Simulate a crash: attempt 1 is saved but never sealed.
	rec1.Resources = append(rec1.Resources, &ResourceApply{Address: Address{Stack: "s", Type: "t", Name: "n"}})
	if err := l.SaveApplyProgress(rec1); err != nil {
		t.Fatalf("save progress: %v", err)
	}

	rec2, err := l.BeginApply(testProposalID)
	if err != nil {
		t.Fatalf("begin apply 2: %v", err)
	}
	if rec2.Attempt != 2 {
		t.Fatalf("attempt = %d, want 2 (attempt numbering advances even over an unsealed prior attempt)", rec2.Attempt)
	}
	if rec2.Parent != "" {
		t.Fatalf("parent = %q, want \"\" (an unsealed attempt has no ID to chain to)", rec2.Parent)
	}
}

func TestSealApply_RejectsDoubleSeal(t *testing.T) {
	l := Open(t.TempDir())
	rec, err := l.BeginApply(testProposalID)
	if err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	sealed, err := l.SealApply(rec, ApplySummary{Outcome: "applied"})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := l.SealApply(sealed, ApplySummary{Outcome: "applied"}); err == nil {
		t.Fatal("expected an error sealing an already-sealed record")
	}
}

func TestSaveApplyProgress_RejectsAlreadySealed(t *testing.T) {
	l := Open(t.TempDir())
	rec, err := l.BeginApply(testProposalID)
	if err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	sealed, err := l.SealApply(rec, ApplySummary{Outcome: "applied"})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if err := l.SaveApplyProgress(sealed); err == nil {
		t.Fatal("expected an error saving progress on an already-sealed record")
	}
}

func TestApplyAttempts_OrderedOldestFirst_IncludingUnsealed(t *testing.T) {
	l := Open(t.TempDir())
	rec1, _ := l.BeginApply(testProposalID)
	sealed1, err := l.SealApply(rec1, ApplySummary{Outcome: "partially_applied"})
	if err != nil {
		t.Fatalf("seal 1: %v", err)
	}
	rec2, _ := l.BeginApply(testProposalID)
	rec2.Resources = append(rec2.Resources, &ResourceApply{Address: Address{Stack: "s", Type: "t", Name: "n"}})
	if err := l.SaveApplyProgress(rec2); err != nil {
		t.Fatalf("save progress 2: %v", err)
	}

	attempts, err := l.ApplyAttempts(testProposalID)
	if err != nil {
		t.Fatalf("apply attempts: %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(attempts))
	}
	if attempts[0].Attempt != 1 || !attempts[0].Sealed() {
		t.Fatalf("attempts[0] = %+v, want sealed attempt 1", attempts[0])
	}
	if attempts[1].Attempt != 2 || attempts[1].Sealed() {
		t.Fatalf("attempts[1] = %+v, want unsealed attempt 2", attempts[1])
	}
	if attempts[0].ID != sealed1.ID {
		t.Fatalf("attempts[0].ID = %q, want %q", attempts[0].ID, sealed1.ID)
	}
}

func TestApplyAttempts_NoAttemptsYet_ReturnsEmpty(t *testing.T) {
	l := Open(t.TempDir())
	attempts, err := l.ApplyAttempts("neverseen")
	if err != nil {
		t.Fatalf("apply attempts: %v", err)
	}
	if len(attempts) != 0 {
		t.Fatalf("attempts = %d, want 0", len(attempts))
	}
}

func TestApplyAttempts_CorruptFile_ReturnsErrCorruptApplyRecord(t *testing.T) {
	l := Open(t.TempDir())
	if _, err := l.BeginApply(testProposalID); err != nil {
		t.Fatalf("begin apply: %v", err)
	}
	path := filepath.Join(l.Dir(), "ledger", "applies", testProposalID+".attempt-1.apply.json")
	if err := os.WriteFile(path, []byte("{ not valid json"), 0o644); err != nil {
		t.Fatalf("corrupt file: %v", err)
	}
	if _, err := l.ApplyAttempts(testProposalID); !errors.Is(err, ErrCorruptApplyRecord) {
		t.Fatalf("err = %v, want ErrCorruptApplyRecord", err)
	}
}

func TestApplyAfter_SubstitutesDotPathsOntoState(t *testing.T) {
	state := json.RawMessage(`{"id":"x","instance_class":"db.t3.medium","tags":{"Environment":"staging"}}`)
	mod := Modification{
		After: map[string]json.RawMessage{
			"instance_class":   json.RawMessage(`"db.t3.large"`),
			"tags.Environment": json.RawMessage(`"prod"`),
		},
	}
	out, err := ApplyAfter(state, mod)
	if err != nil {
		t.Fatalf("apply after: %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got["instance_class"] != "db.t3.large" {
		t.Fatalf("instance_class = %v, want db.t3.large", got["instance_class"])
	}
	if got["id"] != "x" {
		t.Fatalf("id = %v, want untouched x", got["id"])
	}
	tags, ok := got["tags"].(map[string]interface{})
	if !ok || tags["Environment"] != "prod" {
		t.Fatalf("tags = %v, want Environment=prod", got["tags"])
	}
}
