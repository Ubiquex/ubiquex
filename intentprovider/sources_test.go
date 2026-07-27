package intentprovider

import (
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex-cli/core/resolver"
)

func TestPopulateSources(t *testing.T) {
	draft := &resolver.IntentFile{Stack: "payments"}
	docHash := HashDocument([]byte("# Payments database\n\nProvision it."))

	PopulateSources(draft, SourceKindDocument, "payments.md", docHash, "claude", "claude-opus-4-8", []byte(validPaymentsDraft))

	if len(draft.Intent.Sources) != 2 {
		t.Fatalf("Sources = %+v, want exactly 2 entries", draft.Intent.Sources)
	}

	doc := draft.Intent.Sources[0]
	if doc.Kind != SourceKindDocument || doc.Ref != "payments.md" || doc.ContentHash != docHash {
		t.Errorf("document source = %+v, want kind=%q ref=%q hash=%q", doc, SourceKindDocument, "payments.md", docHash)
	}

	provider := draft.Intent.Sources[1]
	if provider.Kind != SourceKindIntentProvider {
		t.Errorf("provider source Kind = %q, want %q", provider.Kind, SourceKindIntentProvider)
	}
	if provider.Ref != "claude:claude-opus-4-8" {
		t.Errorf("provider source Ref = %q, want %q", provider.Ref, "claude:claude-opus-4-8")
	}
	if !strings.HasPrefix(provider.ContentHash, "sha256:") {
		t.Errorf("provider source ContentHash = %q, want a sha256: prefix", provider.ContentHash)
	}
}

func TestHashDocument_Deterministic(t *testing.T) {
	a := HashDocument([]byte("same content"))
	b := HashDocument([]byte("same content"))
	if a != b {
		t.Errorf("HashDocument not deterministic: %q != %q", a, b)
	}
	if HashDocument([]byte("different")) == a {
		t.Error("HashDocument produced the same hash for different content")
	}
}
