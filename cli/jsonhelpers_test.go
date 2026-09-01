package cli

import (
	"encoding/json"
	"os"
	"testing"
)

// jsonQuote quotes s as a JSON string literal suitable for splicing
// directly into a JSON source snippet built via string concatenation in
// a test fixture (a bare JSON-quoted string is also valid D2 quoted-
// string syntax, which is why this helper predates UBI-224's removal of
// the diagram medium -- kept here since several surviving SDK-medium
// fixtures still build JSON snippets this same way).
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func writeResolverIntentFile(t *testing.T, path string, intent any) {
	t.Helper()
	b, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}
