package intentprovider

import (
	"strings"
	"testing"
)

// TestRedact_CatchesEachPattern is docs/intent-provider-adversarial.md
// row 3's own required outcome, exercised per pattern -- three
// independent secret-shaped cases (an AWS-style access key ID, a PEM
// private key block, a Bearer-shaped token), matching the row's own
// required sub-cases.
func TestRedact_CatchesEachPattern(t *testing.T) {
	tests := []struct {
		name        string
		doc         string
		wantFinding string
	}{
		{
			name:        "AWS access key ID",
			doc:         "Use this key: AKIAIOSFODNN7EXAMPLE for the deploy role.",
			wantFinding: "aws-access-key-id",
		},
		{
			name: "PEM private key block",
			doc: "Here's the key:\n-----BEGIN RSA PRIVATE KEY-----\n" +
				"MIIEpAIBAAKCAQEAxyz1234567890abcdefghijklmnop\n" +
				"-----END RSA PRIVATE KEY-----\nthanks",
			wantFinding: "pem-private-key-block",
		},
		{
			name:        "Bearer token",
			doc:         "Authenticate with Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.abcdefghijklmnopqrstuvwxyz1234567890",
			wantFinding: "bearer-token",
		},
		{
			name:        "Anthropic API key",
			doc:         "export ANTHROPIC_API_KEY=sk-ant-api03-abcdefghijklmnopqrstuvwxyz",
			wantFinding: "anthropic-api-key",
		},
		{
			name:        "labeled credential",
			doc:         `password: "S0mething-Really-Long-And-Secret-12345"`,
			wantFinding: "labeled-credential",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redacted, findings := Redact([]byte(tt.doc))

			found := false
			for _, f := range findings {
				if f == tt.wantFinding {
					found = true
				}
			}
			if !found {
				t.Errorf("findings = %v, want %q among them", findings, tt.wantFinding)
			}
			if !strings.Contains(string(redacted), "[REDACTED:"+tt.wantFinding+"]") {
				t.Errorf("redacted output = %q, want it to contain a [REDACTED:%s] placeholder", redacted, tt.wantFinding)
			}
		})
	}
}

func TestRedact_NeverLeaksTheOriginalSecretText(t *testing.T) {
	secret := "AKIAIOSFODNN7EXAMPLE"
	doc := "The access key is " + secret + " -- keep it safe."

	redacted, findings := Redact([]byte(doc))

	if strings.Contains(string(redacted), secret) {
		t.Fatalf("redacted output still contains the original secret text: %q", redacted)
	}
	for _, f := range findings {
		if strings.Contains(f, secret) {
			t.Fatalf("a finding name itself leaked the secret text: %q", f)
		}
	}
}

func TestRedact_OrdinaryContentUnchanged(t *testing.T) {
	doc := "Provision a small Postgres database for the payments service."
	redacted, findings := Redact([]byte(doc))

	if string(redacted) != doc {
		t.Errorf("redacted = %q, want unchanged %q", redacted, doc)
	}
	if len(findings) != 0 {
		t.Errorf("findings = %v, want none for ordinary content", findings)
	}
}

func TestRedact_AlwaysReturnsAFreshCopy(t *testing.T) {
	raw := []byte("ordinary content, nothing secret here")
	redacted, _ := Redact(raw)

	redacted[0] = 'X'
	if raw[0] == 'X' {
		t.Error("Redact's returned slice aliases the input -- mutating it must never mutate raw")
	}
}
