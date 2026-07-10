package core

import (
	"errors"
	"testing"
)

func TestDoubleRun_Success(t *testing.T) {
	calls := 0
	out, err := DoubleRun(func() ([]byte, error) {
		calls++
		return []byte("stable"), nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "stable" {
		t.Fatalf("got %q, want %q", out, "stable")
	}
	if calls != 2 {
		t.Fatalf("fn called %d times, want exactly 2", calls)
	}
}

func TestDoubleRun_MismatchDetected(t *testing.T) {
	calls := 0
	_, err := DoubleRun(func() ([]byte, error) {
		calls++
		return []byte{byte(calls)}, nil
	})
	if !errors.Is(err, ErrDoubleRunMismatch) {
		t.Fatalf("got %v, want ErrDoubleRunMismatch", err)
	}
}

func TestDoubleRun_FirstRunErrorPropagates(t *testing.T) {
	wantErr := errors.New("boom")
	_, err := DoubleRun(func() ([]byte, error) {
		return nil, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("got %v, want %v", err, wantErr)
	}
}
