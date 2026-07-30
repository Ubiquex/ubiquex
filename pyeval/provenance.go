package pyeval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// stampDocumentSource injects one intent.sources entry into raw
// (already-evaluated, unstamped) intent/v1 JSON, naming entryFile
// itself -- mirrors sdkeval's and goeval's own stampDocumentSource
// exactly (same "document" kind, same Go-side-only hashing boundary,
// same additive-not-overwriting behavior); duplicated rather than
// shared for the same small-and-language-agnostic reasoning goeval's
// own provenance.go doc comment gives -- this is the third copy, and
// the real trigger to extract a shared package, deferred as a real,
// named follow-up rather than done reflexively mid-arc.
func stampDocumentSource(raw []byte, entryFile string) ([]byte, error) {
	data, err := os.ReadFile(entryFile)
	if err != nil {
		return nil, fmt.Errorf("pyeval: stamp source: read entry file: %w", err)
	}
	sum := sha256.Sum256(data)
	source := map[string]interface{}{
		"kind":         "document",
		"ref":          filepath.Base(entryFile),
		"content_hash": "sha256:" + hex.EncodeToString(sum[:]),
	}

	var generic map[string]interface{}
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("pyeval: stamp source: decode evaluated output: %w", err)
	}
	intentObj, ok := generic["intent"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("pyeval: stamp source: evaluated output has no intent object")
	}
	existing, _ := intentObj["sources"].([]interface{})
	intentObj["sources"] = append(existing, source)
	generic["intent"] = intentObj

	stamped, err := json.Marshal(generic)
	if err != nil {
		return nil, fmt.Errorf("pyeval: stamp source: re-encode: %w", err)
	}
	return stamped, nil
}
