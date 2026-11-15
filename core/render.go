package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
)

// RendererVersion is the first line of every RenderProposal output. A
// reader holding stored rendered bytes learns which renderer produced them
// from the bytes themselves, without trusting whatever record points at
// them. Any change to RenderProposal's output, however small, changes this
// line: two renders carrying the same version line must have come from the
// same rules.
const RendererVersion = "ubx-render proposal/v1"

// RenderProposal renders a proposal for a human deciding whether to accept
// it (UBX-232). Once an acceptance records the hash of these bytes
// (UBX-230), the renderer is part of the trust chain, so the output is a
// pure function of the proposal's hashed content:
//
//   - It renders exactly what Hash covers (canonicalProposalBytes: the
//     proposal minus id, acceptance and status), decoded from canonical
//     JSON, so object keys arrive sorted and delta elements arrive in
//     canonical (stack, type, name) order. No map iteration order, clock or
//     environment reaches the output.
//   - It renders every value the proposal holds. Nothing is summarised,
//     truncated or redacted. Secrets are references by construction
//     (docs/schema.md, value encoding), so a $secret marker renders as the
//     reference it is. A render showing less than the ledger stores would
//     have a human approving a summary while the record holds the real
//     thing. Fields this renderer has no layout for still render, in the
//     last section, so a field added to the proposal later can never be
//     silently missing from what an approver saw.
//   - The change itself comes first and reads on its own. The author's
//     account (intent) comes after it, labelled as the author's and
//     unchecked, because the author writes it and can write anything there.
//   - Every value is JSON-quoted and non-printable runes are escaped, so a
//     value containing a newline, or a bidirectional-override character,
//     cannot forge a line or a section of the render.
//
// The proposal's own hash is part of the output, so the render names the
// exact proposal it is a view of.
//
// The result runs through DoubleRun: two runs that disagree byte for byte
// are a hard failure, never a render.
func RenderProposal(p *Proposal) ([]byte, error) {
	if p == nil {
		return nil, errors.New("render: nil proposal")
	}
	hash, err := Hash(p)
	if err != nil {
		return nil, fmt.Errorf("render: %w", err)
	}
	out, err := DoubleRun(func() ([]byte, error) {
		return renderProposalOnce(p, hash)
	})
	if err != nil {
		return nil, fmt.Errorf("render: %w", err)
	}
	return out, nil
}

func renderProposalOnce(p *Proposal, hash string) ([]byte, error) {
	canon, err := canonicalProposalBytes(p)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(canon))
	dec.UseNumber()
	var doc map[string]interface{}
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode canonical proposal: %w", err)
	}

	var r renderBuf
	r.line(RendererVersion)
	r.blank()

	r.line("proposal        " + hash)
	r.line("stack           " + renderHeaderValue(takeKey(doc, "stack")))
	r.line("kind            " + renderHeaderValue(takeKey(doc, "kind")))
	parent, hasParent := doc["parent"]
	delete(doc, "parent")
	if s, ok := parent.(string); hasParent && ok && s == "" {
		r.line("parent          (none: the first proposal on this stack)")
	} else {
		r.line("parent          " + renderHeaderValue(parent))
	}
	r.line("schema_version  " + renderHeaderValue(takeKey(doc, "schema_version")))
	r.blank()

	var creates, modifies, destroys []interface{}
	if delta, ok := doc["delta"].(map[string]interface{}); ok {
		creates, _ = takeArray(delta, "creates")
		modifies, _ = takeArray(delta, "modifies")
		destroys, _ = takeArray(delta, "destroys")
		if len(delta) == 0 {
			delete(doc, "delta")
		}
	}
	r.line(fmt.Sprintf("This change creates %d, modifies %d and destroys %d resource(s).",
		len(creates), len(modifies), len(destroys)))

	if len(creates) > 0 {
		r.blank()
		r.line(fmt.Sprintf("== CREATE (%d) ==", len(creates)))
		for _, el := range creates {
			r.blank()
			renderCreate(&r, el)
		}
	}
	if len(modifies) > 0 {
		r.blank()
		r.line(fmt.Sprintf("== MODIFY (%d) ==", len(modifies)))
		for _, el := range modifies {
			r.blank()
			renderModify(&r, el)
		}
	}
	if len(destroys) > 0 {
		r.blank()
		r.line(fmt.Sprintf("== DESTROY (%d) ==", len(destroys)))
		for _, el := range destroys {
			r.blank()
			renderDestroy(&r, el)
		}
	}

	if intent, ok := doc["intent"]; ok {
		delete(doc, "intent")
		r.blank()
		r.line("== THE AUTHOR'S ACCOUNT, NOT CHECKED ==")
		r.blank()
		r.line("The author of this proposal wrote everything in this section.")
		r.line("Nothing here was checked against the change above.")
		r.blank()
		for _, l := range flatten("intent", intent) {
			r.line(l)
		}
	}

	if len(doc) > 0 {
		r.blank()
		r.line("== EVERYTHING ELSE IN THE PROPOSAL ==")
		r.blank()
		for _, k := range sortedKeys(doc) {
			for _, l := range flatten(pathSegment("", k), doc[k]) {
				r.line(l)
			}
		}
	}
	return r.buf.Bytes(), nil
}

func renderCreate(r *renderBuf, el interface{}) {
	obj, ok := el.(map[string]interface{})
	if !ok {
		r.line("+ create (an element that is not an object)")
		r.indented(flatten("element", el))
		return
	}
	r.line("+ create " + takeAddress(obj, ""))
	r.indented(flattenObject(obj))
}

func renderModify(r *renderBuf, el interface{}) {
	obj, ok := el.(map[string]interface{})
	if !ok {
		r.line("~ modify (an element that is not an object)")
		r.indented(flatten("element", el))
		return
	}
	r.line("~ modify " + takeAddress(obj, "target"))

	before, beforeOK := obj["before"].(map[string]interface{})
	after, afterOK := obj["after"].(map[string]interface{})
	_, beforePresent := obj["before"]
	_, afterPresent := obj["after"]
	// before/after are rendered as a diff only when each is either an
	// object or absent. Anything else is malformed, and falls through to
	// the plain flattening below so it still renders.
	if (beforeOK || !beforePresent) && (afterOK || !afterPresent) {
		delete(obj, "before")
		delete(obj, "after")
		keys := map[string]bool{}
		for k := range before {
			keys[k] = true
		}
		for k := range after {
			keys[k] = true
		}
		names := make([]string, 0, len(keys))
		for k := range keys {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			r.line("    " + renderAttrPath(k))
			r.line("      before  " + renderSide(before, k))
			r.line("      after   " + renderSide(after, k))
		}
	}
	r.indented(flattenObject(obj))
}

func renderDestroy(r *renderBuf, el interface{}) {
	obj, ok := el.(map[string]interface{})
	if !ok {
		r.line("- destroy (an element that is not an object)")
		r.indented(flatten("element", el))
		return
	}
	r.line("- destroy " + takeAddress(obj, "address"))
	r.indented(flattenObject(obj))
}

// takeAddress removes and renders a delta element's address: the element's
// own stack/type/name when nested is "", or the object under nested
// otherwise (modifies use "target", destroys "address"). An address that is
// missing or not three strings is left in place, so it still renders as
// data below the header.
func takeAddress(obj map[string]interface{}, nested string) string {
	src := obj
	if nested != "" {
		inner, ok := obj[nested].(map[string]interface{})
		if !ok || len(inner) != 3 {
			return "(no well-formed address)"
		}
		src = inner
	}
	stack, ok1 := src["stack"].(string)
	typ, ok2 := src["type"].(string)
	name, ok3 := src["name"].(string)
	if !ok1 || !ok2 || !ok3 {
		return "(no well-formed address)"
	}
	if nested != "" {
		delete(obj, nested)
	} else {
		delete(obj, "stack")
		delete(obj, "type")
		delete(obj, "name")
	}
	return renderAddressPart(stack) + "." + renderAddressPart(typ) + "." + renderAddressPart(name)
}

func renderSide(m map[string]interface{}, k string) string {
	v, ok := m[k]
	if !ok {
		return "(not set)"
	}
	return renderValue(v)
}

// flattenObject renders every remaining key of obj, sorted, as path = value
// lines.
func flattenObject(obj map[string]interface{}) []string {
	var out []string
	for _, k := range sortedKeys(obj) {
		out = append(out, flatten(pathSegment("", k), obj[k])...)
	}
	return out
}

// flatten renders v as one "path = value" line per leaf. Objects descend by
// key, arrays by index. A marker object ($secret, $computed and the other
// single-key "$" envelopes from docs/schema.md's value encoding) is a leaf:
// it renders whole, as the reference it is, rather than as its parts.
// Empty objects and arrays render as {} and [], so their presence is not
// lost.
func flatten(prefix string, v interface{}) []string {
	switch t := v.(type) {
	case map[string]interface{}:
		if len(t) == 0 || isValueMarker(t) {
			return []string{prefix + " = " + renderValue(t)}
		}
		var out []string
		for _, k := range sortedKeys(t) {
			out = append(out, flatten(pathSegment(prefix, k), t[k])...)
		}
		return out
	case []interface{}:
		if len(t) == 0 {
			return []string{prefix + " = []"}
		}
		var out []string
		for i, el := range t {
			out = append(out, flatten(prefix+"["+strconv.Itoa(i)+"]", el)...)
		}
		return out
	default:
		return []string{prefix + " = " + renderValue(t)}
	}
}

func isValueMarker(m map[string]interface{}) bool {
	if len(m) != 1 {
		return false
	}
	for k := range m {
		return strings.HasPrefix(k, "$")
	}
	return false
}

var (
	plainKey       = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	plainAttrPath  = regexp.MustCompile(`^[A-Za-z0-9_.\[\]-]+$`)
	plainAddrPart  = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	plainHeaderTok = regexp.MustCompile(`^[A-Za-z0-9_.:/@+-]+$`)
)

// pathSegment appends key k to path prefix. A key made only of letters,
// digits, '_' and '-' is written bare; anything else is written as a
// quoted index, so a key containing a dot or a newline cannot read as a
// different path.
func pathSegment(prefix, k string) string {
	if plainKey.MatchString(k) {
		if prefix == "" {
			return k
		}
		return prefix + "." + k
	}
	return prefix + "[" + quoteForDisplay(k) + "]"
}

func renderAttrPath(k string) string {
	if plainAttrPath.MatchString(k) {
		return k
	}
	return quoteForDisplay(k)
}

func renderAddressPart(s string) string {
	if plainAddrPart.MatchString(s) {
		return s
	}
	return quoteForDisplay(s)
}

// renderHeaderValue renders a header field: a plain token bare, anything
// else as canonical JSON. A missing field renders as such rather than as an
// empty string.
func renderHeaderValue(v interface{}) string {
	if v == nil {
		return "(missing)"
	}
	if s, ok := v.(string); ok && plainHeaderTok.MatchString(s) {
		return s
	}
	return renderValue(v)
}

// renderValue renders a value as canonical JSON with non-printable runes
// escaped. Strings are always quoted, so "59" and 59 never look alike.
func renderValue(v interface{}) string {
	b, err := CanonicalJSON(v)
	if err != nil {
		// Unreachable for content that already hashed, since Hash runs the
		// same canonicalisation first. Rendered rather than dropped all the
		// same, so a failure here can never make a value disappear.
		return "(unrenderable: " + escapeNonPrintable(err.Error()) + ")"
	}
	return escapeNonPrintable(string(b))
}

func quoteForDisplay(s string) string {
	b, _ := json.Marshal(s) // a string always marshals
	return escapeNonPrintable(string(b))
}

// escapeNonPrintable rewrites every rune that is not printable as a JSON
// \u escape (a surrogate pair above the BMP), leaving the text valid JSON
// where it already was. encoding/json escapes control characters, but not
// format characters such as U+202E (right-to-left override) or the U+E00xx
// tag block, which can make a rendered value display as something other
// than what it is.
func escapeNonPrintable(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == ' ' || unicode.IsPrint(r) {
			b.WriteRune(r)
			continue
		}
		if r > 0xFFFF {
			hi, lo := utf16.EncodeRune(r)
			fmt.Fprintf(&b, `\u%04x\u%04x`, hi, lo)
			continue
		}
		fmt.Fprintf(&b, `\u%04x`, r)
	}
	return b.String()
}

func takeKey(m map[string]interface{}, k string) interface{} {
	v := m[k]
	delete(m, k)
	return v
}

// takeArray removes m[k] if it is an array and returns it. Anything else is
// left in place, to render as data.
func takeArray(m map[string]interface{}, k string) ([]interface{}, bool) {
	arr, ok := m[k].([]interface{})
	if ok {
		delete(m, k)
	}
	return arr, ok
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type renderBuf struct{ buf bytes.Buffer }

func (r *renderBuf) line(s string) {
	r.buf.WriteString(s)
	r.buf.WriteByte('\n')
}

func (r *renderBuf) blank() { r.buf.WriteByte('\n') }

func (r *renderBuf) indented(lines []string) {
	for _, l := range lines {
		r.line("    " + l)
	}
}
