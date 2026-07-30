package intentprovider

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
)

// Source kinds for docs/schema.md's own "Amendment: intent-provider
// drafts" (UBI-41) and "Amendment: the chat medium" (UBI-46) -- see
// core.IntentSource's own doc comment for the full kind list.
const (
	SourceKindDocument       = "document"
	SourceKindDialogue       = "dialogue"
	SourceKindIntentProvider = "intent_provider"
)

// PopulateSources appends two intent.sources entries to draft's own
// Intent.Sources -- ubx's own authority, never the model's
// (docs/intent-provider.md's own "The mechanism" section): an Adapter has
// no way to correctly compute a content_hash or reliably know its own
// identity string, so DraftWithRetry never asks it to.
//
// sourceKind/ref/hash name the authoring medium behind this draft --
// SourceKindDocument with a doc path and HashDocument's own hash of the
// RAW file (never the redacted copy actually transmitted, see
// docs/intent-provider.md's own "Secret material in a doc" section) for
// `ubx propose --from-doc`; SourceKindDialogue with the captured
// dialogues/<hash>.dlg.json path and its own file hash for `ubx chat`
// (docs/intent-provider.md's own "Amendment: the chat medium," UBI-46).
// rawOutput is the adapter's own final, validated raw response
// (DraftWithRetry's own second return value) -- hashed here as the
// intent_provider source's own tamper-evident audit content, evidence of
// provenance, never an enforced binding (a human editing the draft file
// afterward is legitimate and doesn't move this hash).
func PopulateSources(draft *resolver.IntentFile, sourceKind, ref, hash, adapterName, model string, rawOutput []byte) {
	draft.Intent.Sources = append(draft.Intent.Sources,
		core.IntentSource{
			Kind:        sourceKind,
			Ref:         ref,
			ContentHash: hash,
		},
		core.IntentSource{
			Kind:        SourceKindIntentProvider,
			Ref:         adapterName + ":" + model,
			ContentHash: sha256Hex(rawOutput),
		},
	)
}

// sha256Hex hashes b into the "sha256:<hex>" form docs/schema.md's own
// content_hash convention uses everywhere else in this codebase.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// HashDocument hashes raw into the same "sha256:<hex>" form every
// intent.sources content_hash in this codebase uses -- exported so a
// caller never needs to reimplement this convention itself. Despite the
// name (`ubx propose --from-doc`'s own original caller, hashing the RAW,
// unredacted source document -- see PopulateSources's own doc comment),
// it's a generic content hash: `ubx chat` reuses it unchanged to hash a
// captured dialogue file's own bytes.
func HashDocument(raw []byte) string { return sha256Hex(raw) }
