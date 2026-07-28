package sdkeval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStampDocumentSource_AppendsDocumentEntry(t *testing.T) {
	dir := t.TempDir()
	entryPath := filepath.Join(dir, "payments.ts")
	content := []byte("// a fake entry file, only its bytes matter here\n")
	if err := os.WriteFile(entryPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	raw := []byte(`{"schema_version":1,"kind":"ubx:intent/v1","stack":"payments","intent":{"summary":"x"},"resources":[]}`)
	stamped, err := stampDocumentSource(raw, entryPath)
	if err != nil {
		t.Fatalf("stampDocumentSource: %v", err)
	}

	var doc struct {
		Intent struct {
			Sources []struct {
				Kind        string `json:"kind"`
				Ref         string `json:"ref"`
				ContentHash string `json:"content_hash"`
			} `json:"sources"`
		} `json:"intent"`
	}
	if err := json.Unmarshal(stamped, &doc); err != nil {
		t.Fatalf("unmarshal stamped output: %v", err)
	}
	if len(doc.Intent.Sources) != 1 {
		t.Fatalf("len(sources) = %d, want 1", len(doc.Intent.Sources))
	}
	src := doc.Intent.Sources[0]
	if src.Kind != "document" {
		t.Fatalf("Kind = %q, want \"document\"", src.Kind)
	}
	if src.Ref != "payments.ts" {
		t.Fatalf("Ref = %q, want \"payments.ts\"", src.Ref)
	}
	sum := sha256.Sum256(content)
	want := "sha256:" + hex.EncodeToString(sum[:])
	if src.ContentHash != want {
		t.Fatalf("ContentHash = %q, want %q", src.ContentHash, want)
	}
}

func TestStampDocumentSource_AppendsRatherThanOverwrites(t *testing.T) {
	dir := t.TempDir()
	entryPath := filepath.Join(dir, "payments.ts")
	if err := os.WriteFile(entryPath, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	raw := []byte(`{"schema_version":1,"kind":"ubx:intent/v1","stack":"payments",
		"intent":{"summary":"x","sources":[{"kind":"manual_edit","ref":"PR #1"}]},"resources":[]}`)
	stamped, err := stampDocumentSource(raw, entryPath)
	if err != nil {
		t.Fatalf("stampDocumentSource: %v", err)
	}

	var doc struct {
		Intent struct {
			Sources []map[string]interface{} `json:"sources"`
		} `json:"intent"`
	}
	if err := json.Unmarshal(stamped, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc.Intent.Sources) != 2 {
		t.Fatalf("len(sources) = %d, want 2 (the program's own + the harness's stamp)", len(doc.Intent.Sources))
	}
	if doc.Intent.Sources[0]["kind"] != "manual_edit" {
		t.Fatalf("expected the program's own source to survive first: %+v", doc.Intent.Sources[0])
	}
	if doc.Intent.Sources[1]["kind"] != "document" {
		t.Fatalf("expected the harness's own stamp appended second: %+v", doc.Intent.Sources[1])
	}
}

func TestStampDocumentSource_MissingEntryFile_Errors(t *testing.T) {
	raw := []byte(`{"schema_version":1,"kind":"ubx:intent/v1","stack":"payments","intent":{"summary":"x"},"resources":[]}`)
	if _, err := stampDocumentSource(raw, "/nonexistent/path/payments.ts"); err == nil {
		t.Fatal("stampDocumentSource: got nil error for a nonexistent entry file, want an error")
	}
}

func TestStampDocumentSource_NoIntentObject_Errors(t *testing.T) {
	dir := t.TempDir()
	entryPath := filepath.Join(dir, "payments.ts")
	if err := os.WriteFile(entryPath, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"schema_version":1,"kind":"ubx:intent/v1","stack":"payments","resources":[]}`)
	if _, err := stampDocumentSource(raw, entryPath); err == nil {
		t.Fatal("stampDocumentSource: got nil error for a document with no intent object, want an error")
	}
}
