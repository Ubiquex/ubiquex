package core

import (
	"encoding/json"
	"errors"
	"testing"
)

func modificationFor(addr Address) Modification {
	return Modification{
		Target: addr,
		Before: map[string]json.RawMessage{"instance_class": json.RawMessage(`"db.t3.medium"`)},
		After:  map[string]json.RawMessage{"instance_class": json.RawMessage(`"db.t3.large"`)},
	}
}

func TestValidate_ModifiesRequiresResolutionInput(t *testing.T) {
	p := sampleProposal()
	addr := Address{Stack: "payments", Type: "aws_db_instance", Name: "payments-db"}
	p.Delta.Modifies = []Modification{modificationFor(addr)}
	// No resolution.inputs entry at all.

	err := Validate(p)
	if !errors.Is(err, ErrInvalidProposal) {
		t.Fatalf("got %v, want ErrInvalidProposal", err)
	}
}

func TestValidate_ModifiesRequiresNonEmptyObservedHash(t *testing.T) {
	p := sampleProposal()
	addr := Address{Stack: "payments", Type: "aws_db_instance", Name: "payments-db"}
	p.Delta.Modifies = []Modification{modificationFor(addr)}
	p.Resolution.Inputs = []ResolutionInput{
		{Kind: "live_state", Resource: addr.String(), ObservedHash: ""},
	}

	err := Validate(p)
	if !errors.Is(err, ErrInvalidProposal) {
		t.Fatalf("got %v, want ErrInvalidProposal", err)
	}
}

func TestValidate_ModifiesWithMatchingResolutionInputPasses(t *testing.T) {
	p := sampleProposal()
	addr := Address{Stack: "payments", Type: "aws_db_instance", Name: "payments-db"}
	p.Delta.Modifies = []Modification{modificationFor(addr)}
	p.Resolution.Inputs = []ResolutionInput{
		{Kind: "live_state", Resource: addr.String(), ObservedHash: "sha256:abc123"},
	}
	p.BlastRadius = BlastRadius{Modifies: 1}

	if err := Validate(p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_ModifiesResolutionInputWrongAddressRejected(t *testing.T) {
	p := sampleProposal()
	addr := Address{Stack: "payments", Type: "aws_db_instance", Name: "payments-db"}
	other := Address{Stack: "payments", Type: "aws_db_instance", Name: "some-other-db"}
	p.Delta.Modifies = []Modification{modificationFor(addr)}
	p.Resolution.Inputs = []ResolutionInput{
		{Kind: "live_state", Resource: other.String(), ObservedHash: "sha256:abc123"},
	}

	err := Validate(p)
	if !errors.Is(err, ErrInvalidProposal) {
		t.Fatalf("got %v, want ErrInvalidProposal", err)
	}
}

func TestValidate_AdoptionRequiresZeroBlastRadius(t *testing.T) {
	p := sampleProposal()
	p.Kind = KindAdoption
	p.BlastRadius = BlastRadius{Creates: 1}

	err := Validate(p)
	if !errors.Is(err, ErrInvalidProposal) {
		t.Fatalf("got %v, want ErrInvalidProposal", err)
	}
}

func TestValidate_AdoptionRejectsModifies(t *testing.T) {
	p := sampleProposal()
	p.Kind = KindAdoption
	p.BlastRadius = BlastRadius{}
	addr := Address{Stack: "payments", Type: "aws_db_instance", Name: "payments-db"}
	p.Delta.Modifies = []Modification{modificationFor(addr)}
	p.Resolution.Inputs = []ResolutionInput{
		{Kind: "live_state", Resource: addr.String(), ObservedHash: "sha256:abc123"},
	}

	err := Validate(p)
	if !errors.Is(err, ErrInvalidProposal) {
		t.Fatalf("got %v, want ErrInvalidProposal", err)
	}
}

func TestValidate_AdoptionRejectsDestroys(t *testing.T) {
	p := sampleProposal()
	p.Kind = KindAdoption
	p.BlastRadius = BlastRadius{}
	p.Delta.Destroys = []Address{{Stack: "payments", Type: "aws_db_instance", Name: "old-db"}}

	err := Validate(p)
	if !errors.Is(err, ErrInvalidProposal) {
		t.Fatalf("got %v, want ErrInvalidProposal", err)
	}
}

func TestValidate_AdoptionRecordOnlyPasses(t *testing.T) {
	p := sampleProposal()
	p.Kind = KindAdoption
	p.BlastRadius = BlastRadius{}
	// Creates may still be populated -- adoption records the resource into
	// the ledger, it just doesn't apply anything against the real world.

	if err := Validate(p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_NonAdoptionKindUnaffectedByAdoptionRule(t *testing.T) {
	p := sampleProposal()
	p.Kind = KindChange
	addr := Address{Stack: "payments", Type: "aws_db_instance", Name: "payments-db"}
	p.Delta.Modifies = []Modification{modificationFor(addr)}
	p.Delta.Destroys = []Address{{Stack: "payments", Type: "aws_db_instance", Name: "old-db"}}
	p.Resolution.Inputs = []ResolutionInput{
		{Kind: "live_state", Resource: addr.String(), ObservedHash: "sha256:abc123"},
	}
	p.BlastRadius = BlastRadius{Creates: 1, Modifies: 1, Destroys: 1}

	if err := Validate(p); err != nil {
		t.Fatalf("unexpected error for non-adoption kind: %v", err)
	}
}

func TestAccept_RejectsInvalidProposalBeforeHashing(t *testing.T) {
	l := Open(t.TempDir())
	p := sampleProposal()
	addr := Address{Stack: "payments", Type: "aws_db_instance", Name: "payments-db"}
	p.Delta.Modifies = []Modification{modificationFor(addr)}
	// Missing resolution.inputs entry for the modification.

	_, err := Accept(l, p)
	if !errors.Is(err, ErrInvalidProposal) {
		t.Fatalf("got %v, want ErrInvalidProposal", err)
	}
	if p.ID != "" {
		t.Fatalf("proposal should not have been assigned an ID after failing validation, got %q", p.ID)
	}
}
