package core

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ubiquex/ubiquex-cli/provider"
)

// fakeProvider is an in-memory test double for provider.Provider — no
// subprocess, no gRPC, just enough to drive core.RunScan/VerifyFreshness
// through their adversarial paths deterministically.
type fakeProvider struct {
	schemaErr    error
	configureErr error
	readErr      error
	state        json.RawMessage // returned by ReadResource; nil/"null" simulates "unreadable"
}

func (f *fakeProvider) ProtocolVersion() int { return 6 }

func (f *fakeProvider) Schema(context.Context) (*provider.Schemas, error) {
	if f.schemaErr != nil {
		return nil, f.schemaErr
	}
	return &provider.Schemas{
		Provider: &provider.Schema{},
		Resources: map[string]*provider.Schema{
			"aws_s3_bucket": {},
		},
	}, nil
}

func (f *fakeProvider) Configure(context.Context, *provider.Schema, json.RawMessage) error {
	return f.configureErr
}

func (f *fakeProvider) ReadResource(context.Context, *provider.Schema, string, json.RawMessage) (json.RawMessage, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.state, nil
}

func testAddr() Address {
	return Address{Stack: "payments", Type: "aws_s3_bucket", Name: "ubx-states"}
}

func TestRunScan_New(t *testing.T) {
	l := Open(t.TempDir())
	fp := &fakeProvider{state: json.RawMessage(`{"id":"ubx-states","tags":{"env":"prod"}}`)}

	res, err := RunScan(context.Background(), fp, l, ScanRequest{Address: testAddr(), CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan: %v", err)
	}
	if res.Outcome != ScanNew {
		t.Fatalf("Outcome = %v, want ScanNew", res.Outcome)
	}
	if res.ObservedHash == "" {
		t.Fatal("ObservedHash is empty")
	}
}

func TestRunScan_UnchangedAfterAdoption(t *testing.T) {
	l := Open(t.TempDir())
	fp := &fakeProvider{state: json.RawMessage(`{"id":"ubx-states","tags":{"env":"prod"}}`)}
	addr := testAddr()

	res, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan: %v", err)
	}
	proposal, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal: %v", err)
	}
	if _, err := Accept(l, proposal); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	// Scan again -- nothing changed.
	res2, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan (2nd): %v", err)
	}
	if res2.Outcome != ScanUnchanged {
		t.Fatalf("Outcome = %v, want ScanUnchanged", res2.Outcome)
	}
}

func TestRunScan_DriftDetectedAfterAdoption(t *testing.T) {
	l := Open(t.TempDir())
	addr := testAddr()
	fp := &fakeProvider{state: json.RawMessage(`{"id":"ubx-states","tags":{"env":"prod"}}`)}

	res, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan: %v", err)
	}
	proposal, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal: %v", err)
	}
	if _, err := Accept(l, proposal); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	// Reality changes: someone edits the tag outside ubx.
	fp.state = json.RawMessage(`{"id":"ubx-states","tags":{"env":"staging"}}`)

	res2, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan (2nd): %v", err)
	}
	if res2.Outcome != ScanDrifted {
		t.Fatalf("Outcome = %v, want ScanDrifted", res2.Outcome)
	}

	driftProposal, err := GenerateProposal(l, "payments", res2)
	if err != nil {
		t.Fatalf("GenerateProposal (drift): %v", err)
	}
	if driftProposal.Kind != KindDriftAdopt {
		t.Fatalf("Kind = %q, want %q", driftProposal.Kind, KindDriftAdopt)
	}
	if len(driftProposal.Delta.Modifies) != 1 {
		t.Fatalf("got %d modifies entries, want 1", len(driftProposal.Delta.Modifies))
	}
	mod := driftProposal.Delta.Modifies[0]
	if string(mod.Before["tags.env"]) != `"prod"` {
		t.Fatalf("before[tags.env] = %s, want %q", mod.Before["tags.env"], "prod")
	}
	if string(mod.After["tags.env"]) != `"staging"` {
		t.Fatalf("after[tags.env] = %s, want %q", mod.After["tags.env"], "staging")
	}
	// "id" didn't change -- must not appear in the diff.
	if _, ok := mod.Before["id"]; ok {
		t.Fatalf("unchanged attribute %q leaked into the diff", "id")
	}

	if _, err := Accept(l, driftProposal); err != nil {
		t.Fatalf("Accept (drift): %v", err)
	}
}

func TestRunScan_ResourceUnreadable(t *testing.T) {
	l := Open(t.TempDir())
	fp := &fakeProvider{state: nil}

	_, err := RunScan(context.Background(), fp, l, ScanRequest{Address: testAddr(), CurrentState: json.RawMessage(`{}`)})
	if !errors.Is(err, ErrResourceUnreadable) {
		t.Fatalf("got %v, want ErrResourceUnreadable", err)
	}
}

func TestRunScan_ResourceUnreadable_JSONNull(t *testing.T) {
	l := Open(t.TempDir())
	fp := &fakeProvider{state: json.RawMessage(`null`)}

	_, err := RunScan(context.Background(), fp, l, ScanRequest{Address: testAddr(), CurrentState: json.RawMessage(`{}`)})
	if !errors.Is(err, ErrResourceUnreadable) {
		t.Fatalf("got %v, want ErrResourceUnreadable", err)
	}
}

func TestRunScan_ProviderErrorOnSchema(t *testing.T) {
	l := Open(t.TempDir())
	wantErr := errors.New("schema fetch boom")
	fp := &fakeProvider{schemaErr: wantErr}

	_, err := RunScan(context.Background(), fp, l, ScanRequest{Address: testAddr(), CurrentState: json.RawMessage(`{}`)})
	if !errors.Is(err, wantErr) {
		t.Fatalf("got %v, want wrapped %v", err, wantErr)
	}
}

func TestRunScan_ProviderErrorOnConfigure(t *testing.T) {
	l := Open(t.TempDir())
	wantErr := errors.New("configure boom")
	fp := &fakeProvider{configureErr: wantErr}

	_, err := RunScan(context.Background(), fp, l, ScanRequest{Address: testAddr(), CurrentState: json.RawMessage(`{}`)})
	if !errors.Is(err, wantErr) {
		t.Fatalf("got %v, want wrapped %v", err, wantErr)
	}
}

func TestRunScan_ProviderErrorOnReadResource(t *testing.T) {
	l := Open(t.TempDir())
	wantErr := errors.New("read boom")
	fp := &fakeProvider{readErr: wantErr}

	_, err := RunScan(context.Background(), fp, l, ScanRequest{Address: testAddr(), CurrentState: json.RawMessage(`{}`)})
	if !errors.Is(err, wantErr) {
		t.Fatalf("got %v, want wrapped %v", err, wantErr)
	}
}

func TestRunScan_UnknownResourceType(t *testing.T) {
	l := Open(t.TempDir())
	fp := &fakeProvider{state: json.RawMessage(`{}`)}
	addr := Address{Stack: "payments", Type: "aws_totally_made_up", Name: "x"}

	_, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{}`)})
	if !errors.Is(err, ErrUnknownResourceType) {
		t.Fatalf("got %v, want ErrUnknownResourceType", err)
	}
}

func TestGenerateProposal_UnchangedRefused(t *testing.T) {
	l := Open(t.TempDir())
	res := &ScanResult{Address: testAddr(), Outcome: ScanUnchanged}
	if _, err := GenerateProposal(l, "payments", res); err == nil {
		t.Fatal("expected an error for an unchanged scan result, got nil")
	}
}

func TestVerifyFreshness_PassesWhenUnchanged(t *testing.T) {
	l := Open(t.TempDir())
	addr := testAddr()
	fp := &fakeProvider{state: json.RawMessage(`{"id":"ubx-states","tags":{"env":"prod"}}`)}

	res, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan: %v", err)
	}
	proposal, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal: %v", err)
	}

	if err := VerifyFreshness(context.Background(), fp, addr, nil, json.RawMessage(`{"id":"ubx-states"}`), proposal); err != nil {
		t.Fatalf("VerifyFreshness: %v", err)
	}
}

// TestVerifyFreshness_BlocksStaleAcceptance is the "drift-on-drift
// staleness" adversarial case: reality changes again between when scan
// generated the proposal and when accept would run.
func TestVerifyFreshness_BlocksStaleAcceptance(t *testing.T) {
	l := Open(t.TempDir())
	addr := testAddr()
	fp := &fakeProvider{state: json.RawMessage(`{"id":"ubx-states","tags":{"env":"prod"}}`)}

	res, err := RunScan(context.Background(), fp, l, ScanRequest{Address: addr, CurrentState: json.RawMessage(`{"id":"ubx-states"}`)})
	if err != nil {
		t.Fatalf("RunScan: %v", err)
	}
	proposal, err := GenerateProposal(l, "payments", res)
	if err != nil {
		t.Fatalf("GenerateProposal: %v", err)
	}

	// Reality changes again, after the proposal was generated but before
	// it's accepted.
	fp.state = json.RawMessage(`{"id":"ubx-states","tags":{"env":"staging"}}`)

	err = VerifyFreshness(context.Background(), fp, addr, nil, json.RawMessage(`{"id":"ubx-states"}`), proposal)
	if !errors.Is(err, ErrStaleObservation) {
		t.Fatalf("got %v, want ErrStaleObservation", err)
	}
}
