package intentprovider

import "regexp"

// redactionPattern is one secret-shaped pattern Redact scans for. Name is
// what gets reported in a finding (never the matched text itself -- see
// Redact's own doc comment).
type redactionPattern struct {
	name string
	re   *regexp.Regexp
}

// redactionPatterns is the best-effort, defense-in-depth pattern set
// docs/intent-provider.md's own "Secret material in a doc" section names:
// cloud-provider access-key formats, PEM private-key blocks, common
// Bearer/API-key-prefixed tokens, and a generic high-entropy
// heuristic anchored near a secret-suggestive word. Explicitly NOT
// claimed complete (docs/intent-provider-adversarial.md's own "What this
// table doesn't yet cover" names this directly) -- a genuinely novel
// secret format this set doesn't recognize is not caught by this
// mechanism.
var redactionPatterns = []redactionPattern{
	{name: "aws-access-key-id", re: regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{name: "anthropic-api-key", re: regexp.MustCompile(`\bsk-ant-[A-Za-z0-9\-_]{20,}\b`)},
	{name: "openai-api-key", re: regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}\b`)},
	{name: "bearer-token", re: regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9\-_.]{20,}`)},
	{name: "pem-private-key-block", re: regexp.MustCompile(`(?s)-----BEGIN (?:RSA |EC |OPENSSH |DSA |)PRIVATE KEY-----.*?-----END (?:RSA |EC |OPENSSH |DSA |)PRIVATE KEY-----`)},
	// A generic "<key/secret/password/token>-shaped word, then a
	// separator, then a long opaque-looking value" heuristic -- the
	// intentionally broad catch-all for whatever the named patterns
	// above miss. Deliberately anchored on the label, not just a bare
	// long string, to keep the false-positive rate down (a long random
	// identifier that isn't labeled as a credential is not this
	// pattern's job to catch).
	{name: "labeled-credential", re: regexp.MustCompile(`(?i)\b(?:api[_-]?key|secret|password|passwd|token)\b\s*[:=]\s*['"]?[A-Za-z0-9\-_/+.=]{12,}['"]?`)},
}

// Redact scans raw for secret-shaped patterns and replaces every match
// with a "[REDACTED:<pattern-name>]" placeholder, run on a document's raw
// content before any network call ever leaves this machine
// (docs/intent-provider.md's own "Secret material in a doc" section).
// Returns the redacted copy and the distinct pattern names matched (never
// the matched text) -- callers use this list only to report that
// something was redacted and what kind, never to recover what was
// caught.
//
// raw is never mutated; the returned slice is always a fresh copy, even
// when nothing matched (so a caller can always safely use the returned
// value as "what gets transmitted," never needing to branch on whether
// redaction changed anything).
func Redact(raw []byte) (redacted []byte, findings []string) {
	out := append([]byte(nil), raw...)
	seen := map[string]bool{}
	for _, p := range redactionPatterns {
		if !p.re.Match(out) {
			continue
		}
		out = p.re.ReplaceAll(out, []byte("[REDACTED:"+p.name+"]"))
		if !seen[p.name] {
			seen[p.name] = true
			findings = append(findings, p.name)
		}
	}
	return out, findings
}
