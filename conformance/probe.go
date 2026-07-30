// This file is UBI-50 session 2's own first slice: the schema-driven
// probe generator and its hermetic tier, designed in
// docs/conformance-harness.md before any of this existed. It generalizes
// Registry's 154 hand-written TypeSpec entries into a machine that can
// produce a verdict for every type of every provider's own real schema
// — hermetic-tier only here (probes 1/2/4's own schema-only halves;
// probe 3, destroy honesty, has no hermetic half at all, named
// explicitly in docs/conformance-harness.md, and isn't touched by this
// file). Live-tier confirmation (the part that actually creates,
// mutates, and destroys real resources) is later work — see that
// document's own "Execution tiers" section.
package conformance

import (
	"sort"
	"strings"

	"github.com/ubiquex/ubiquex/provider"
)

// FindingClass names one of the four lie-classes docs/conformance-harness.md's
// own "Failure taxonomy" section defines. Exactly these four exist —
// this is a closed set, not open for a probe to invent a fifth kind
// without a matching design amendment first.
type FindingClass string

const (
	FindingIncompleteRead     FindingClass = "incomplete-read"
	FindingSensitiveUnderflag FindingClass = "sensitive-underflag"
	FindingDestroyLie         FindingClass = "destroy-lie"
	FindingUndriftable        FindingClass = "undriftable"
)

// Tier names which execution tier produced a Finding. TierHermetic is
// the only tier this file's own probes ever produce — schema-only,
// free, no cloud, no credentials, the same standard every existing
// FakeOnly TypeSpec.IdentityFields already holds to. TierLive is named
// here so Finding.Tier's own zero value is never silently confused with
// a real hermetic result once live-tier probes exist; nothing in this
// file ever sets it.
type Tier string

const (
	TierHermetic Tier = "hermetic"
	TierLive     Tier = "live"
)

// Confidence distinguishes a hermetic CANDIDATE — worth a human's (or a
// future live probe's) attention, never itself a claim of certainty,
// docs/conformance-harness.md's own explicit honesty requirement for the
// sensitive-echo and drift-detectability probes — from a hermetic
// CONFIRMED finding, the rare case a schema alone can settle completely
// (probe 1's own "no identity candidate exists in the schema at all"
// case). A live-tier probe (not built here) is what turns most
// Candidate findings into a real Confirmed verdict, one way or the
// other.
type Confidence string

const (
	Candidate Confidence = "candidate"
	Confirmed Confidence = "confirmed"
)

// Finding is one probe's own generated verdict for one (Source,
// Version, Type) — the executable unit of docs/conformance-harness.md's
// own "(source, version, type, verb)" generated registry format. Verb
// is "read" for every lie-class this file's hermetic probes can ever
// produce (incomplete-read, sensitive-underflag, and undriftable are
// all questions about what a ReadResource call returns or fails to);
// "destroy" is reserved for the destroy-honesty class, which has no
// hermetic half and is never emitted by anything in this file — see
// FindingDestroyLie's own doc comment.
//
// Deliberately NOT a replacement for, or a write into,
// conformance.Registry's own TypeSpec — that table's hand-verified
// Notes/IdentityFields/LookupHint stay exactly as they are, layered on
// TOP of whatever a Finding says for the same (Source, Type), per
// docs/conformance-harness.md's own "Layering, never replacing"
// section. Reconciling a Finding against an existing TypeSpec entry is
// future work (session 3+), not attempted here.
type Finding struct {
	Source     string
	Version    string
	Type       string
	Verb       string
	Class      FindingClass
	Tier       Tier
	Confidence Confidence
	Detail     string
}

// identityCandidateAttrs are the attribute names conformance.Registry's
// own 154 hand-written TypeSpec.IdentityFields entries have, historically,
// actually recorded across every platform this project has onboarded:
// "id" (always), "self_link" (GCP's own convention), "arn" (AWS's),
// "name" (every platform's own natural key). Deliberately broader than
// core.AttributeDrift's own identityCandidates (core/attribution.go),
// which tries only id/arn/name, in that specific order, for a narrower
// and different purpose (CloudTrail-style event correlation) — this
// probe's own job is "does this type's schema even offer a recognized
// identity shape at all," matching how IdentityFields has actually been
// populated by hand, not how attribution happens to search.
var identityCandidateAttrs = []string{"id", "self_link", "arn", "name"}

// identityCandidateSuffixes is a real, session-2-added refinement, found
// by triaging the 134 AWS types the exact-match check alone flagged
// Confirmed: a real, live check against hashicorp/aws 6.54.0 showed 108
// of those 134 actually DO carry a plausible identity attribute — just
// type-prefixed rather than exactly "arn"/"id"/"name"
// (aws_bedrock_guardrail_version's own "guardrail_arn",
// aws_iam_group_policies_exclusive's own "group_name", and similar).
// This is real AWS provider convention, not an edge case: a
// type-prefixed identity attribute is, if anything, the MORE common
// shape once account-scoped/"exclusive"-set/composite-association types
// are counted. A suffix match is weaker evidence than an exact
// canonical name (attributeNamesPresent's own check, above) — it can
// also be a genuine FOREIGN KEY to a different resource
// (aws_fis_target_account_configuration's own "account_id" names the
// AWS account, not this resource's own identity) — so it never
// downgrades a type to "clean," only to a weaker Candidate signal
// naming the specific attribute a live-tier probe should try first.
var identityCandidateSuffixes = []string{"_arn", "_id", "_name"}

// probeIdentityShape is the hermetic half of lie-class 1
// (docs/conformance-harness.md — "Identity-shape / incomplete-read"). It
// can only ever confirm one thing: whether ANY recognized identity
// attribute exists in the schema at all. If one does, sufficiency
// (whether "id" alone, or some combination, actually round-trips a
// complete ReadResource) is explicitly a live-tier-only question — see
// the design doc's own "cannot prove sufficiency" line — so this
// function returns no Finding at all for the common exact-match case,
// never a false "confirmed clean."
//
// Three-way outcome, not two — refined this session after triaging the
// real AWS results: an EXACT canonical name present (the common case)
// produces no Finding at all; NO exact name but a plausible
// type-prefixed one (identityCandidateSuffixes) produces a Candidate
// finding naming it, since a live-tier probe should try it before
// concluding failure, but the schema alone can't promise it's really
// this resource's own identity rather than a foreign-key reference;
// NEITHER produces Confirmed — a schema offering nothing at all,
// canonical or type-prefixed, that core.RunScan/core.AttributeDrift
// could ever plausibly search by, is the one thing schema-only
// inspection can settle completely. Real triage split, hashicorp/aws
// 6.54.0 (see docs/conformance-harness.md's own session-3 amendment):
// of 134 types originally flagged Confirmed by the exact-match check
// alone, 114 actually have a plausible suffix-matched candidate (108
// type-prefixed *_arn/*_id, 6 "_exclusive"-shaped *_name parent keys);
// the remaining ~20 are real, structural singleton/composite/
// account-scoped config resources with no per-instance identity
// attribute of any shape — a live-tier policy decision (below, and in
// docs/conformance-harness.md), not a probe bug.
func probeIdentityShape(source, version, typeName string, block provider.Block) []Finding {
	if len(attributeNamesPresent(block, identityCandidateAttrs)) > 0 {
		return nil
	}
	if suffixed := attributeNamesWithSuffix(block, identityCandidateSuffixes); len(suffixed) > 0 {
		return []Finding{{
			Source: source, Version: version, Type: typeName, Verb: "read",
			Class: FindingIncompleteRead, Tier: TierHermetic, Confidence: Candidate,
			Detail: "no canonical identity attribute (id/self_link/arn/name) present, but " + strings.Join(suffixed, ",") +
				" is a plausible type-prefixed candidate -- a live-tier probe should try it before concluding this type is unreadable; schema alone can't confirm it's self-identity rather than a foreign-key reference",
		}}
	}
	return []Finding{{
		Source: source, Version: version, Type: typeName, Verb: "read",
		Class: FindingIncompleteRead, Tier: TierHermetic, Confidence: Confirmed,
		Detail: "no recognized identity attribute -- canonical (id/self_link/arn/name) or type-prefixed (*_arn/*_id/*_name) -- found anywhere in this type's own schema; ReadResource has no known lookup shape to try at all, likely a singleton/composite/account-scoped resource needing a different resolution model entirely (see docs/conformance-harness.md)",
	}}
}

// suspectKeywords is the exact corpus this project's own manual
// sensitive-attribute audits (UBI-22/24, UBI-37) already used by hand —
// reused verbatim, not reinvented, so the hermetic probe reproduces
// what a human already did, not a fresh guess at what might matter.
var suspectKeywords = []string{
	"notes", "manifest", "output", "log", "message", "content", "template",
	"script", "yaml", "json", "result", "rendered", "secret", "password",
	"key", "token", "credential", "connection", "cert", "private",
}

// probeSensitiveEcho is the hermetic half of lie-class 2
// (docs/conformance-harness.md — "Sensitive-flag audit vs. echo
// attributes"): every COMPUTED, non-Sensitive attribute (walked
// recursively through nested blocks, dot-path named) whose own name
// contains a suspectKeywords substring is flagged Candidate — never
// Confirmed, and the Detail string says so explicitly. This is a
// deliberate, load-bearing honesty constraint, not a hedge: this
// project's own real UBI-37 audit of 42 azurerm types found this exact
// keyword corpus matches mostly false positives on inspection
// (private_ip_address, login_server, public_key_openssh,
// administrator_login — an IP classification, a public hostname, PUBLIC
// key material, and a username, none of them secret material) against
// exactly one real gap (azurerm_linux_web_app's own app_settings, which
// the keyword scan doesn't even match — "app_settings" contains none of
// these keywords, confirming this hermetic check is neither complete
// nor precise, only a triage aid). Only a live-tier marker-string echo
// check (not built here) can mechanically PROVE a leak; this function
// never claims to.
func probeSensitiveEcho(source, version, typeName string, block provider.Block) []Finding {
	var findings []Finding
	walkAttributes(block, "", func(path string, attr provider.Attribute) {
		if attr.Sensitive || !attr.Computed {
			return
		}
		matched := matchKeywords(attr.Name, suspectKeywords)
		if len(matched) == 0 {
			return
		}
		findings = append(findings, Finding{
			Source: source, Version: version, Type: typeName, Verb: "read",
			Class: FindingSensitiveUnderflag, Tier: TierHermetic, Confidence: Candidate,
			Detail: path + ": computed, non-sensitive, name matches " + strings.Join(matched, ",") +
				" -- unconfirmed, human triage required (this project's own real audit history shows most keyword matches are false positives; see docs/conformance-harness.md's own sensitive-flag-audit section)",
		})
	})
	return findings
}

// probeDrift is the hermetic half of lie-class 4
// (docs/conformance-harness.md — "Drift-detectability (the
// hashicorp/time class)"): a type whose entire schema — walked
// recursively through nested blocks — has no Optional or Required
// attribute anywhere is flagged Candidate undriftable. core.dotSet/
// FoldState's own diff can only ever be meaningful where a user's own
// declared config is compared against a live read; a type with nothing
// a user could ever legitimately set has no config-vs-live comparison
// to make in the first place, only two different snapshots of the
// provider's own internal computation. Candidate, not Confirmed: the
// hermetic check alone cannot distinguish a genuinely undriftable type
// (hashicorp/time's own time_static, confirmed live during UBI-43 to
// return fresh, disconnected values on every read) from one that's
// simply entirely server-assigned but stable — only a live no-mutation
// rescan (not built here) can tell those apart.
func probeDrift(source, version, typeName string, block provider.Block) []Finding {
	if hasSettableAttribute(block) {
		return nil
	}
	return []Finding{{
		Source: source, Version: version, Type: typeName, Verb: "drift",
		Class: FindingUndriftable, Tier: TierHermetic, Confidence: Candidate,
		Detail: "every attribute in this type's own schema (including nested blocks) is Computed -- no Optional/Required attribute exists for FoldState's own diff to ever compare against a live read; candidate undriftable, unconfirmed without a live no-mutation rescan",
	}}
}

// attributeNamesPresent returns which of candidates actually appear as
// top-level attributes in block, sorted for determinism (CLAUDE.md's own
// standing "no map-iteration ordering" rule).
func attributeNamesPresent(block provider.Block, candidates []string) []string {
	want := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		want[c] = true
	}
	var present []string
	for _, a := range block.Attributes {
		if want[a.Name] {
			present = append(present, a.Name)
		}
	}
	sort.Strings(present)
	return present
}

// attributeNamesWithSuffix returns every top-level attribute name in
// block ending in one of suffixes, sorted for determinism — the weaker,
// type-prefixed-identity signal probeIdentityShape falls back to once
// no exact canonical name (attributeNamesPresent) is found. Top-level
// only, deliberately not recursive into nested blocks: an identity
// attribute for THIS resource is, by every real example this project
// has ever seen across every platform, always a flat, top-level field —
// never buried inside a nested config block.
func attributeNamesWithSuffix(block provider.Block, suffixes []string) []string {
	var matched []string
	for _, a := range block.Attributes {
		for _, suf := range suffixes {
			if strings.HasSuffix(a.Name, suf) {
				matched = append(matched, a.Name)
				break
			}
		}
	}
	sort.Strings(matched)
	return matched
}

// walkAttributes visits every attribute in block, and recursively every
// attribute in every nested block, dot-path naming each one exactly the
// way provider/overrides.go's own SensitiveOverride.Path convention
// already does (a nested block's own TypeName joined by ".", matching
// Modification.Before/After's own dotted-path convention). Order is the
// schema's own attribute/nested-block order, not sorted — callers that
// need deterministic output sort their own accumulated results, the
// same posture attributeNamesPresent takes for its own narrower job.
func walkAttributes(block provider.Block, prefix string, fn func(path string, attr provider.Attribute)) {
	for _, a := range block.Attributes {
		path := a.Name
		if prefix != "" {
			path = prefix + "." + a.Name
		}
		fn(path, a)
	}
	for _, nb := range block.NestedBlocks {
		path := nb.TypeName
		if prefix != "" {
			path = prefix + "." + nb.TypeName
		}
		walkAttributes(nb.Block, path, fn)
	}
}

// hasSettableAttribute reports whether block, or any block nested
// inside it, has at least one Optional or Required attribute — "at
// least one thing a user could ever legitimately declare," walked
// recursively since a nested block itself carries no Optional/Required
// flag of its own in this project's own provider.NestedBlock shape
// (only its Nesting mode) — only the attributes inside it, at any
// depth, can ever be user-settable.
func hasSettableAttribute(block provider.Block) bool {
	for _, a := range block.Attributes {
		if a.Optional || a.Required {
			return true
		}
	}
	for _, nb := range block.NestedBlocks {
		if hasSettableAttribute(nb.Block) {
			return true
		}
	}
	return false
}

// matchKeywords returns every entry of keywords that appears as a
// case-insensitive substring of name, preserving keywords' own order.
func matchKeywords(name string, keywords []string) []string {
	lower := strings.ToLower(name)
	var matched []string
	for _, k := range keywords {
		if strings.Contains(lower, k) {
			matched = append(matched, k)
		}
	}
	return matched
}

// ProbeType runs every hermetic-tier probe (identity-shape,
// sensitive-echo, drift-detectability) against one resource type's own
// schema block, returning every Finding that actually has something to
// say — a type with a present identity attribute, no suspicious
// computed attribute names, and at least one settable attribute
// produces zero Findings, not three redundant "clean" entries. Findings
// are returned in a fixed, deterministic order (identity, then
// sensitive-echo entries in their own schema-walk order, then drift) —
// callers that aggregate across many types (ProbeSchema) impose their
// own cross-type ordering on top.
func ProbeType(source, version, typeName string, block provider.Block) []Finding {
	var findings []Finding
	findings = append(findings, probeIdentityShape(source, version, typeName, block)...)
	findings = append(findings, probeSensitiveEcho(source, version, typeName, block)...)
	findings = append(findings, probeDrift(source, version, typeName, block)...)
	return findings
}

// ProbeSchema runs ProbeType against every RESOURCE type in schemas —
// deliberately never DataSources, matching every existing
// conformance.Registry entry, all of which are resource types
// core.RunScan/GenerateProposal/Accept can actually act on; a data
// source is a read-only external reference the ledger never manages,
// out of scope for a lie-class that's fundamentally about "does ubx's
// own adopt/scan/destroy path tell the truth about this type." Findings
// are sorted by (Type, Class, Verb, Detail) for fully reproducible
// output regardless of Go's own randomized map iteration order over
// schemas.Resources — CLAUDE.md's own standing determinism rule, not
// an incidental nicety: two runs against the identical schema must
// produce byte-identical output, since this is exactly the property
// "rerun on version bump" (docs/conformance-harness.md) needs to diff
// against.
func ProbeSchema(source, version string, schemas *provider.Schemas) []Finding {
	var findings []Finding
	for typeName, schema := range schemas.Resources {
		if schema == nil {
			continue
		}
		findings = append(findings, ProbeType(source, version, typeName, schema.Block)...)
	}
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.Class != b.Class {
			return a.Class < b.Class
		}
		if a.Verb != b.Verb {
			return a.Verb < b.Verb
		}
		return a.Detail < b.Detail
	})
	return findings
}
