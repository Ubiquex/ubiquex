package tseval

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
// itself: {"kind": "document", "ref": "<basename>", "content_hash":
// "sha256:<hex of the RAW entry file's own bytes>"}.
//
// A single "document" entry -- not the separate "sdk"/"sdk_evaluator"
// kind pair docs/sdk.md's own slice 6 sketch originally proposed. Real,
// deliberate simplification, decided this session: code is
// self-describing and fully deterministic, with no analogous "which
// adapter drafted this" fact worth a second entry the way
// intent_provider's own kind names which LLM transcribed a document --
// there is no LLM in this path at all. "document" is the SAME kind the
// md medium already uses (docs/intent-provider.md) for exactly this
// role -- an authoring artifact is an authoring artifact, whether it's
// prose a human wrote or code a human wrote; only an LLM-transcription
// STEP needs its own second, adapter-naming entry, and this path has no
// such step. Reusing "document" here is what makes "three independent
// producers converge" (docs/sdk.md's own slice 7 framing) a real,
// checkable claim rather than three producers each inventing their own
// bespoke provenance vocabulary.
//
// The content hash is computed HERE, Go-side, over the entry file's own
// real bytes -- never inside the sandboxed evaluator, which has no fs
// access at all, by design (docs/sdk.md's own hermetic evaluator
// section). Matches docs/intent-provider.md's own established rule for
// the md medium's "document" source: "computed over the RAW, unredacted
// file," the same boundary reused here rather than reinvented.
//
// Appends to whatever intent.sources a program's own intent() call may
// already have set (sdk/ts/runtime) -- never overwrites: a program
// author's own explicitly-named sources are real content too, and the
// harness's own provenance stamp is additive to them, not a replacement.
func stampDocumentSource(raw []byte, entryFile string) ([]byte, error) {
	data, err := os.ReadFile(entryFile)
	if err != nil {
		return nil, fmt.Errorf("tseval: stamp source: read entry file: %w", err)
	}
	sum := sha256.Sum256(data)
	source := map[string]interface{}{
		"kind":         "document",
		"ref":          filepath.Base(entryFile),
		"content_hash": "sha256:" + hex.EncodeToString(sum[:]),
	}

	var generic map[string]interface{}
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("tseval: stamp source: decode evaluated output: %w", err)
	}
	intentObj, ok := generic["intent"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("tseval: stamp source: evaluated output has no intent object")
	}
	existing, _ := intentObj["sources"].([]interface{})
	intentObj["sources"] = append(existing, source)
	generic["intent"] = intentObj

	stamped, err := json.Marshal(generic)
	if err != nil {
		return nil, fmt.Errorf("tseval: stamp source: re-encode: %w", err)
	}
	return stamped, nil
}
