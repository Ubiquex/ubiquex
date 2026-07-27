package intentprovider

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/core/resolver"
)

// Source kinds for docs/schema.md's own "Amendment: intent-provider
// drafts" (UBI-41) -- see core.IntentSource's own doc comment for the
// full kind list.
const (
	SourceKindDocument       = "document"
	SourceKindIntentProvider = "intent_provider"
)

// PopulateSources appends the two new UBI-41 intent.sources entries to
// draft's own Intent.Sources -- ubx's own authority, never the model's
// (docs/intent-provider.md's own "The mechanism" section): an Adapter has
// no way to correctly compute a content_hash or reliably know its own
// identity string, so DraftWithRetry never asks it to.
//
// docHash is the RAW, unredacted source document's own content hash --
// never the redacted copy actually transmitted to the adapter (see
// docs/intent-provider.md's own "Secret material in a doc" section for
// why these are two deliberately distinct byte sequences). rawOutput is
// the adapter's own final, validated raw response (DraftWithRetry's own
// second return value) -- hashed here as the intent_provider source's
// own tamper-evident audit content, evidence of provenance, never an
// enforced binding (a human editing the draft file afterward is
// legitimate and doesn't move this hash).
func PopulateSources(draft *resolver.IntentFile, docPath, docHash, adapterName, model string, rawOutput []byte) {
	draft.Intent.Sources = append(draft.Intent.Sources,
		core.IntentSource{
			Kind:        SourceKindDocument,
			Ref:         docPath,
			ContentHash: docHash,
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

// HashDocument hashes raw (the RAW, unredacted source document -- see
// PopulateSources's own doc comment) into the same "sha256:<hex>" form
// used for the "document" source's own ContentHash -- exported so a
// caller (the md pipeline session's own `ubx propose --from-doc`) never
// needs to reimplement this convention itself.
func HashDocument(raw []byte) string { return sha256Hex(raw) }
