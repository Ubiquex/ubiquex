package claude

import (
	"net/http"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/ubiquex/ubiquex/intentprovider"
)

func TestNew_DefaultsModel(t *testing.T) {
	a := New(Config{})
	if a.model != DefaultModel {
		t.Errorf("model = %q, want default %q", a.model, DefaultModel)
	}
	if a.Name() != "claude" {
		t.Errorf("Name() = %q, want \"claude\"", a.Name())
	}
}

func TestNew_HonorsExplicitModel(t *testing.T) {
	a := New(Config{Model: "claude-sonnet-5"})
	if a.model != "claude-sonnet-5" {
		t.Errorf("model = %q, want \"claude-sonnet-5\"", a.model)
	}
}

// TestEffortSupported_ExcludesHaikuFamily is UBI-63 session 2's own
// regression test for a real bug found live: a stack config explicitly
// pinning "claude-haiku-4-5-20251001" got a real, structured 400 back
// from the real API -- "This model does not support the effort
// parameter" -- since Draft used to send OutputConfig.Effort
// unconditionally for every model. A substring check on "haiku"
// (case-insensitive), not an exhaustive allowlist, so a future Haiku
// point release is still correctly excluded.
func TestEffortSupported_ExcludesHaikuFamily(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"claude-haiku-4-5-20251001", false},
		{"claude-3-5-haiku-20241022", false},
		{"CLAUDE-HAIKU-4-5-20251001", false}, // case-insensitive
		{"claude-opus-4-8", true},
		{"claude-sonnet-5", true},
		{"claude-fable-5", true},
	}
	for _, tt := range tests {
		if got := effortSupported(tt.model); got != tt.want {
			t.Errorf("effortSupported(%q) = %v, want %v", tt.model, got, tt.want)
		}
	}
}

func TestAdapter_ImplementsIntentproviderAdapter(t *testing.T) {
	var _ intentprovider.Adapter = New(Config{})
}

// TestClassifyError_NamesDistinctFailures is
// docs/intent-provider-adversarial.md row 6's own required outcome: a
// bad/missing key must never be reported identically to a network
// timeout or another API error -- each sub-case gets a distinguishable
// message.
func TestClassifyError_NamesDistinctFailures(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantSubstr string
	}{
		{
			name:       "401 unauthorized",
			err:        &anthropic.Error{StatusCode: http.StatusUnauthorized},
			wantSubstr: "authentication failed",
		},
		{
			name:       "403 forbidden",
			err:        &anthropic.Error{StatusCode: http.StatusForbidden},
			wantSubstr: "authentication failed",
		},
		{
			name:       "429 rate limited",
			err:        &anthropic.Error{StatusCode: http.StatusTooManyRequests},
			wantSubstr: "rate limited",
		},
		{
			name:       "500 server error",
			err:        &anthropic.Error{StatusCode: http.StatusInternalServerError},
			wantSubstr: "API error (500)",
		},
		{
			name:       "plain network error",
			err:        errPlain("connection refused"),
			wantSubstr: "network/connection",
		},
		{
			// A real gap found live (see classifyError's own doc comment):
			// with no credential resolvable at all, the SDK never reaches
			// the server, so there's no *anthropic.Error to branch on --
			// confirmed by actually running the live test with
			// UBX_TEST_SLOW=1 and no credentials in this environment.
			name:       "no credentials resolvable",
			err:        errPlain("no Anthropic credentials found. The SDK tried these sources in order:\n  1. ANTHROPIC_API_KEY env var: not set"),
			wantSubstr: "no credentials resolvable",
		},
	}

	seen := map[string]bool{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyError(tt.err)
			if got == nil {
				t.Fatal("classifyError returned nil")
			}
			if !strings.Contains(got.Error(), tt.wantSubstr) {
				t.Errorf("classifyError(%v) = %q, want it to contain %q", tt.err, got.Error(), tt.wantSubstr)
			}
			seen[got.Error()] = true
		})
	}
	if len(seen) != len(tests) {
		t.Error("two distinct failure sub-cases produced an identical message -- classifyError must distinguish every one")
	}
}

func TestRetryPrompt_IncludesEveryError(t *testing.T) {
	got := retryPrompt([]string{"intent.summary: must not be empty", "resources[0].op: bad value"})
	for _, want := range []string{"intent.summary: must not be empty", "resources[0].op: bad value"} {
		if !strings.Contains(got, want) {
			t.Errorf("retryPrompt output missing %q:\n%s", want, got)
		}
	}
}

type errPlain string

func (e errPlain) Error() string { return string(e) }
