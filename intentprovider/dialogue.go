package intentprovider

import (
	"fmt"
	"strings"

	"github.com/ubiquex/ubiquex/core/resolver"
)

// Turn is one user turn in a captured dialogue -- always the REDACTED
// text, per-turn, at capture (docs/intent-provider.md's own "Amendment:
// the chat medium," UBI-46 -- redaction never happens post-hoc; by the
// time a Turn exists at all, its own Text has already been through
// Redact). There is no separate "raw" turn text preserved anywhere,
// unlike `ubx propose --from-doc`'s own document/redacted-copy split --
// a chat turn has no independent on-disk original to keep a tamper-
// evidence hash of; the redacted text IS the authoritative captured
// record, named explicitly as a real, deliberate divergence from the
// `--from-doc` path, not an oversight.
type Turn struct {
	Text string `json:"text"`
	At   string `json:"at"` // RFC3339
}

// Dialogue is the whole captured conversation behind a chat-drafted
// intent/v1 draft -- docs/schema.md's own founding `IntentSource.Kind ==
// "dialogue"`, drafted at this project's very first design session,
// given a real producer for the first time here. Written to
// dialogues/<hash>.dlg.json exactly once, atomically, only at explicit
// finalization (`ubx chat`'s own `/save`) -- never incrementally per
// turn, which is what makes "no orphan capture file from an abandoned
// session" true by construction rather than by a separate cleanup step:
// if the process never reaches finalization, nothing was ever written.
//
// Draft is the FINAL intent/v1 draft this dialogue produced, embedded
// verbatim -- but deliberately the PRE-provenance version, with an empty
// Intent.Sources. This isn't an oversight: the draft this project
// actually hands back to the caller (written to --out/stdout) is a
// separate copy whose own Intent.Sources names THIS dialogue file's own
// content hash -- embedding that same provenance-bearing copy inside the
// file being hashed would be circular (the file's hash would depend on
// its own hash). Two distinct objects for two distinct purposes: the
// embedded Draft is "what was actually drafted, verbatim, for audit";
// the emitted one is "what a human reviews and resolves next."
type Dialogue struct {
	SchemaVersion int64                `json:"schema_version"`
	Stack         string               `json:"stack"`
	Adapter       string               `json:"adapter"`
	Model         string               `json:"model"`
	StartedAt     string               `json:"started_at"`
	Turns         []Turn               `json:"turns"`
	Draft         *resolver.IntentFile `json:"draft"`
}

// Transcript formats d's own turns into the DraftRequest.Content bytes
// handed to Adapter.Draft -- the exact, and only, extension point chat
// needs from the intent-provider interface. No change to Adapter,
// DraftRequest, or DraftWithRetry was needed to build this (confirmed by
// writing this function, not assumed from the design doc's own
// "rides nearly free" claim): a chat turn is just a different way of
// producing Content bytes than reading a file whole, and DraftWithRetry
// itself is completely transcript-shape-agnostic. Numbered explicitly
// ("[Turn N]") so a later, contradicting turn can be identified as later
// by the model without it having to infer ordering from prose alone --
// docs/intent-provider-adversarial.md's own "contradictory turns" row.
func (d *Dialogue) Transcript() []byte {
	var b strings.Builder
	for i, t := range d.Turns {
		fmt.Fprintf(&b, "[Turn %d]: %s\n\n", i+1, t.Text)
	}
	return []byte(b.String())
}
