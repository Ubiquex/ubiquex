package github

import "testing"

func TestParseProposalTrailer(t *testing.T) {
	const hash = "d6b1f8a2c3e4d5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d70"

	tests := []struct {
		name     string
		body     string
		wantHash string
		wantOK   bool
	}{
		{"alone on its own line", "ubx-proposal: " + hash, hash, true},
		{"amid other PR body text", "## Summary\n\nBumps the instance size.\n\nubx-proposal: " + hash + "\n\nCloses #12", hash, true},
		{"no trailer at all", "## Summary\n\nJust a normal PR, no trailer.", "", false},
		{"malformed hash (too short)", "ubx-proposal: abc123", "", false},
		{"wrong case hex", "ubx-proposal: " + "D6B1F8A2C3E4D5F60718293A4B5C6D7E8F90A1B2C3D4E5F60718293A4B5C6D70", "", false},
		{"empty body", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash, ok := ParseProposalTrailer(tt.body)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if hash != tt.wantHash {
				t.Fatalf("hash = %q, want %q", hash, tt.wantHash)
			}
		})
	}
}
