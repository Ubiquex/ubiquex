package core

import (
	"encoding/json"
	"testing"
)

func TestIsRedactedValue(t *testing.T) {
	cases := []struct {
		name string
		raw  json.RawMessage
		want bool
	}{
		{"redacted marker", json.RawMessage(`{"$redacted":{"sha256":"abc"}}`), true},
		{"plain string", json.RawMessage(`"hunter2"`), false},
		{"plain object", json.RawMessage(`{"name":"widget"}`), false},
		{"redacted key with extra sibling", json.RawMessage(`{"$redacted":{"sha256":"abc"},"extra":1}`), false},
		{"redacted key with non-object value", json.RawMessage(`{"$redacted":"abc"}`), false},
		{"null", json.RawMessage(`null`), false},
		{"array", json.RawMessage(`[1,2,3]`), false},
		{"malformed json", json.RawMessage(`{not json`), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRedactedValue(tc.raw); got != tc.want {
				t.Errorf("IsRedactedValue(%s) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestCountRedacted(t *testing.T) {
	cases := []struct {
		name string
		raw  json.RawMessage
		want int
	}{
		{"none", json.RawMessage(`{"name":"widget"}`), 0},
		{"one top-level", json.RawMessage(`{"password":{"$redacted":{"sha256":"a"}}}`), 1},
		{
			"nested in object",
			json.RawMessage(`{"auth":{"password":{"$redacted":{"sha256":"a"}},"username":"alice"}}`),
			1,
		},
		{
			"nested in array",
			json.RawMessage(`{"credentials":[{"secret":{"$redacted":{"sha256":"a"}}},{"secret":{"$redacted":{"sha256":"b"}}}]}`),
			2,
		},
		{
			"multiple at different levels",
			json.RawMessage(`{"top_secret":{"$redacted":{"sha256":"a"}},"nested":{"deep_secret":{"$redacted":{"sha256":"b"}}}}`),
			2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CountRedacted(tc.raw); got != tc.want {
				t.Errorf("CountRedacted(%s) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}
