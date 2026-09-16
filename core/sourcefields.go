package core

import (
	"fmt"
	"sort"
	"strings"
)

// sourcefields.go refuses an IntentSource carrying a field its kind does
// not use.
//
// # Why this exists instead of a shape
//
// IntentSource is sixteen fields: three general and thirteen belonging to
// one kind or another. Nothing said which was which except a doc comment,
// and nothing at all checked it, so a cloudtrail field on a blueprint
// source was accepted in silence.
//
// The obvious fix is nesting each group under its kind, and it was
// considered and rejected for two reasons. The existing thirteen cannot
// move: this is hashed content, so renaming or nesting one changes the
// canonical bytes of every proposal already written. And nesting buys
// nothing on the cost it appears to address, since an older binary drops
// an unknown key and mis-hashes whether that key is one field or an
// object containing four.
//
// So the case for nesting was legibility, and a rule is a more direct
// answer to legibility than a shape is. It also covers the thirteen
// fields that no nesting could reach, which is the half that matters,
// since those are the ones already written down.
//
// # What it deliberately does not do
//
// It does not require a kind to populate its fields. A cloudtrail source
// with no event id is incomplete, not malformed, and attribution
// legitimately produces partial records. This refuses only the reverse: a
// field set by a kind that has no use for it, which is always either a
// mistake or a misunderstanding of the type.

// sourceFieldsByKind names the kind-specific fields each IntentSource
// kind may carry. A kind absent from this map may carry none of them.
//
// Kind, Ref and ContentHash are general and belong to every kind, so they
// are not listed.
var sourceFieldsByKind = map[string][]string{
	"promotion":  {"base"},
	"blueprint":  {"declaration", "declared_source", "declared_rev", "declared_path", "declared_args", "withheld_args"},
	"cloudtrail": {"event_id", "event_name", "event_time", "actor_arn", "source_ip", "session_context"},
	// GCP reuses the same fields rather than declaring parallel ones, a
	// decision core.IntentSource's own doc comment records: both backends
	// implement one interface and produce one event shape. ActorARN
	// carries a principal email there, and SessionContext is always
	// empty, which is incompleteness rather than a violation.
	"gcp_audit":               {"event_id", "event_name", "event_time", "actor_arn", "source_ip", "session_context"},
	"cloudtrail_unattributed": {"reason", "backend"},
	"audit_unattributed":      {"reason", "backend"},
}

// setSourceFields lists the kind-specific fields this source actually
// carries, in a stable order.
func setSourceFields(s IntentSource) []string {
	var set []string
	add := func(name string, isSet bool) {
		if isSet {
			set = append(set, name)
		}
	}
	add("base", s.Base != "")
	add("declaration", s.Declaration != "")
	add("declared_source", s.DeclaredSource != "")
	add("declared_rev", s.DeclaredRev != "")
	add("declared_path", s.DeclaredPath != "")
	add("declared_args", len(s.DeclaredArgs) > 0)
	add("withheld_args", len(s.WithheldArgs) > 0)
	add("event_id", s.EventID != "")
	add("event_name", s.EventName != "")
	add("event_time", s.EventTime != "")
	add("actor_arn", s.ActorARN != "")
	add("source_ip", s.SourceIP != "")
	add("session_context", len(s.SessionContext) > 0)
	add("reason", s.Reason != "")
	add("backend", s.Backend != "")
	sort.Strings(set)
	return set
}

// validateSourceFields refuses any source in p carrying a field its kind
// does not use.
func validateSourceFields(p *Proposal) error {
	check := func(where string, sources []IntentSource) error {
		for _, s := range sources {
			allowed := map[string]bool{}
			for _, f := range sourceFieldsByKind[s.Kind] {
				allowed[f] = true
			}
			var stray []string
			for _, f := range setSourceFields(s) {
				if !allowed[f] {
					stray = append(stray, f)
				}
			}
			if len(stray) > 0 {
				return fmt.Errorf("%s: source kind %q carries %s, which %s",
					where, s.Kind, strings.Join(stray, ", "), noUseFor(s.Kind))
			}
		}
		return nil
	}
	if err := check("intent.sources", p.Intent.Sources); err != nil {
		return err
	}
	for i := range p.Delta.Modifies {
		if err := check(fmt.Sprintf("delta.modifies[%d].sources", i), p.Delta.Modifies[i].Sources); err != nil {
			return err
		}
	}
	for i := range p.Delta.Destroys {
		if err := check(fmt.Sprintf("delta.destroys[%d].sources", i), p.Delta.Destroys[i].Sources); err != nil {
			return err
		}
	}
	// Creates are not checked here. Delta.Creates is []json.RawMessage
	// and its sources are opaque bytes this never decodes, deliberately:
	// decoding them to validate would be the one place in the system that
	// reads a create's node shape, and the reason they stay opaque is
	// that nothing should.
	return nil
}

// noUseFor renders the tail of the message, naming what the kind may
// carry so the reader does not have to go and look.
func noUseFor(kind string) string {
	allowed := sourceFieldsByKind[kind]
	if len(allowed) == 0 {
		return "uses no kind-specific fields at all (only kind, ref and content_hash)"
	}
	return "does not use: it carries " + strings.Join(allowed, ", ")
}
