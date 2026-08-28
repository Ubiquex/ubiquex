// Package dsfilter implements UBI-181's own five real data-source
// candidate exclusion rules -- watch paths, operation-status shapes,
// execution/event records, computed non-stored values, and high-volume
// reference duplication. Before this package existed, these five rules
// existed only as numbers in that Linear ticket (259/1/73/64/472,
// quoted by UBI-186 as "UBI-181's five-rule filter over the full
// corpus") -- no committed code anywhere in this repo, ubiquex, or
// ubiquex-docs implemented them, confirmed by a direct grep sweep
// across all three before writing this. UBI-186's own real
// DiscoverDataSources widening (smithy/datasource.go,
// discoverydoc/datasource.go, resourcemap/datasource.go) walked past
// them without reconciling against the ticket's own numbers, which is
// exactly how operation-poll and platform-reference-boilerplate
// candidates ended up in a real, generated corpus: a live count found
// Azure's own generated 2,129 data sources included 64
// operation/operation-status-named candidates and GCP's own 788
// included 113 operation-named and 145 generic-location-named
// candidates, none of it excluded by anything upstream of this
// package.
//
// Shared across all three real schema sources (Smithy/AWS,
// discoverydoc/GCP, resourcemap/Azure+Kubernetes+GitHub+Datadog)
// because the five concepts here are provider-agnostic even though
// each source's own real candidate shape (Smithy has no URL path at
// all; discoverydoc's path is a dotted resource-tree node; resourcemap's
// is a real REST path) differs -- Candidate is the deliberately narrow,
// source-agnostic signal each caller already has in hand, not a new
// unification of the three real candidate types.
package dsfilter

import "strings"

// Reason names which of the five rules excluded a candidate -- recorded
// in a Note at every real call site, never a silent drop, matching this
// codebase's own standing "skip, don't silently drop" discipline
// (identical in spirit to the "already claimed... skipped rather than
// disambiguated" Notes every DiscoverDataSources already emits).
type Reason string

const (
	ReasonWatchPath            Reason = "watch path -- a subscription/streaming endpoint, not a point-in-time lookup"
	ReasonOperationStatus      Reason = "operation-status shape -- async job/operation polling, not stored infrastructure data"
	ReasonExecutionOrEvent     Reason = "execution/event record -- a history/log entry, not current-state data"
	ReasonComputedValue        Reason = "computed, non-stored value -- a derived check/estimate, not a real stored lookup"
	ReasonReferenceDuplication Reason = "high-volume reference duplication -- generic platform reference data repeated near-identically across many services, not this service's own real data"
)

// Candidate is the minimal signal Excluded needs -- Noun is required
// (already snake_cased, already singularized, by the time a caller
// reaches this point in its own real discovery walk); Path,
// OperationName, and ResponseTypeName are each optional (empty string
// when a given source has nothing to offer -- Smithy has no real URL
// path, for instance) and only ever widen what a rule can catch, never
// narrow it.
type Candidate struct {
	// Noun is the candidate's own already-derived noun, e.g.
	// "operation", "target_type", "availability_zone".
	Noun string
	// Path is the candidate's own real path -- a REST path
	// ("/subscriptions/{id}/watch") for resourcemap, a dotted
	// resource-tree node ("projects.locations.operations") for
	// discoverydoc, empty for Smithy (which has no URL-path concept).
	Path string
	// OperationName is the candidate's own real, raw operation/method
	// name -- a Smithy shape's bare local name ("WatchNamespacedPod"),
	// an OpenAPI operationId, or a Discovery Document method key.
	OperationName string
	// ResponseTypeName is the candidate's own real, raw (not
	// snake_cased) response/output schema name, when known --
	// "TargetTypeListResult", "OperationStatus", empty when a source
	// has no named response type to offer.
	ResponseTypeName string
}

// Excluded reports whether c matches one of the five rules, and which.
func Excluded(c Candidate) (Reason, bool) {
	if isWatch(c) {
		return ReasonWatchPath, true
	}
	if isOperationStatus(c) {
		return ReasonOperationStatus, true
	}
	if hasToken(c.Noun, executionOrEventWords) {
		return ReasonExecutionOrEvent, true
	}
	if hasToken(c.Noun, computedValueWords) {
		return ReasonComputedValue, true
	}
	if hasToken(c.Noun, referenceDuplicationWords) {
		return ReasonReferenceDuplication, true
	}
	return "", false
}

var watchWords = []string{"watch", "watches"}

// isWatch checks all three real signals a watch/streaming endpoint can
// surface under: a literal "watch" path segment (Kubernetes' own real
// `/watch/...` convention and query-style watch collections alike), a
// Watch-prefixed operation name (Kubernetes' own real
// `watchNamespacedPod`-style operationId convention), or a bare "watch"
// noun.
func isWatch(c Candidate) bool {
	if hasPathSegment(c.Path, "watch") || hasPathSegment(c.Path, "watches") {
		return true
	}
	if strings.HasPrefix(strings.ToLower(c.OperationName), "watch") {
		return true
	}
	return hasToken(c.Noun, watchWords)
}

var operationWords = []string{"operation", "operations", "operation_status", "long_running_operation", "async_operation"}

// isOperationStatus checks the noun (GCP/Azure's own real
// "projects.locations.operations"-shaped tree nodes, once snake_cased
// and singularized, land on exactly "operation") and, when a source
// offers one, the raw response type name (Azure's own real
// "OperationStatus"/"OperationStatusResult" response shapes) -- checked
// case-insensitively against the response type since that field is
// deliberately NOT pre-snake_cased the way Noun already is.
func isOperationStatus(c Candidate) bool {
	if hasToken(c.Noun, operationWords) {
		return true
	}
	return strings.Contains(strings.ToLower(c.ResponseTypeName), "operation")
}

var executionOrEventWords = []string{"execution", "event", "activity", "activity_log", "audit_log", "log_entry", "history"}

// computedValueWords deliberately uses "name_availability" (Azure and
// GCP's own real checkNameAvailability convention), not a bare
// "availability" -- a bare word would also match "availability_zone",
// which is genuinely rule 5's own high-volume reference duplication,
// not a computed check.
var computedValueWords = []string{"name_availability", "sku_availability", "estimate", "estimation", "quota", "usage", "cost", "validation", "capability", "capabilities", "eligibility", "compatibility"}

var referenceDuplicationWords = []string{"location", "region", "zone", "availability_zone"}

// hasToken reports whether noun equals phrase, or phrase appears in
// noun as a whole "_"-delimited component (so "chaos_availability_zone"
// matches "availability_zone", but "zonefile" does not spuriously match
// "zone") -- word-boundary matching on the already-snake_cased noun,
// not a bare substring test.
//
// Also tries noun with a trailing "s" stripped, and unchanged if it
// already has none: AWS's own real Smithy-sourced nouns are commonly
// still plural at this point (List-verb-stripped, e.g. "operations",
// "locations" -- Smithy's own real read-candidate discovery has no
// singularization step of its own, unlike discoverydoc/resourcemap's
// nouns, which are already singular by the time they reach here), and
// duplicating every phrase in both forms across all five word lists
// would be real, ongoing maintenance debt for the same real check this
// one trailing-"s" strip already covers.
func hasToken(noun string, phrases []string) bool {
	if matchesExact(noun, phrases) {
		return true
	}
	if singular := strings.TrimSuffix(noun, "s"); singular != noun {
		return matchesExact(singular, phrases)
	}
	return false
}

func matchesExact(noun string, phrases []string) bool {
	for _, phrase := range phrases {
		if noun == phrase ||
			strings.HasPrefix(noun, phrase+"_") ||
			strings.HasSuffix(noun, "_"+phrase) ||
			strings.Contains(noun, "_"+phrase+"_") {
			return true
		}
	}
	return false
}

// hasPathSegment reports whether path (either "/"-separated, a real
// REST path, or "."-separated, discoverydoc's own dotted resource-tree
// node path) contains segment as a whole, case-insensitive component.
func hasPathSegment(path, segment string) bool {
	if path == "" {
		return false
	}
	lower := strings.ToLower(path)
	segment = strings.ToLower(segment)
	for _, part := range strings.FieldsFunc(lower, func(r rune) bool { return r == '/' || r == '.' }) {
		part = strings.Trim(part, "{}")
		if part == segment {
			return true
		}
	}
	return false
}
