package resolver

// UBI-224: Turn and Dialogue survive the removal of chat as an
// authoring medium because explaining an existing proposal is not
// authoring. A proposal accepted before this change may still carry a
// core.IntentSource with Kind == core.SourceKindDialogue, and ubx why
// still needs a real type to unmarshal the referenced dialogues/<hash>.dlg.json
// file into and render. Living in core/resolver rather than core itself
// only because Dialogue.Draft needs *IntentFile, which core cannot
// import without a cycle (resolver already imports core, not the other
// way around). The drafting-time-only Transcript method that used to
// live on Dialogue is gone with cli/chat.go itself; nothing here writes
// a new dialogue file anymore, only reads one that already exists.

// Turn is one user turn in a captured dialogue -- always the redacted
// text, per-turn, at capture. Kept only so an already-captured Dialogue
// still unmarshals; nothing appends a new Turn anymore.
type Turn struct {
	Text string `json:"text"`
	At   string `json:"at"` // RFC3339
}

// Dialogue is the whole captured conversation behind a chat-drafted
// intent/v1 draft, written to dialogues/<hash>.dlg.json exactly once by
// the chat medium this project no longer supports authoring through.
// Draft is the FINAL intent/v1 draft the dialogue produced, embedded
// verbatim, deliberately the pre-provenance version (see the historical
// design record in docs/intent-provider.md for why).
type Dialogue struct {
	SchemaVersion int64       `json:"schema_version"`
	Stack         string      `json:"stack"`
	Adapter       string      `json:"adapter"`
	Model         string      `json:"model"`
	StartedAt     string      `json:"started_at"`
	Turns         []Turn      `json:"turns"`
	Draft         *IntentFile `json:"draft"`
}
