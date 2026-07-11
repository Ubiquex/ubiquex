package core

import "testing"

func TestParseAddress(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Address
		ok   bool
	}{
		{"simple", "payments.aws_db_instance.payments-db", Address{"payments", "aws_db_instance", "payments-db"}, true},
		{"name contains dots", "payments.aws_db_instance.payments.db.replica", Address{"payments", "aws_db_instance", "payments.db.replica"}, true},
		{"round-trips through String", "conformance.aws_s3_bucket.ubx-states", Address{"conformance", "aws_s3_bucket", "ubx-states"}, true},
		{"missing name", "payments.aws_db_instance", Address{}, false},
		{"missing type and name", "payments", Address{}, false},
		{"empty string", "", Address{}, false},
		{"empty stack", ".aws_db_instance.payments-db", Address{}, false},
		{"empty type", "payments..payments-db", Address{}, false},
		{"empty name", "payments.aws_db_instance.", Address{}, false},
		{"looks like a proposal id, not an address (no dots)", "d6b1f8a2c3e4d5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7", Address{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseAddress(tt.in)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseAddress_RoundTripsWithString(t *testing.T) {
	addr := Address{Stack: "payments", Type: "aws_db_instance", Name: "payments-db"}
	got, ok := ParseAddress(addr.String())
	if !ok {
		t.Fatal("expected ParseAddress to succeed on Address.String()'s own output")
	}
	if got != addr {
		t.Fatalf("got %+v, want %+v", got, addr)
	}
}
