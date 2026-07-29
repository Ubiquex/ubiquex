package discovery

import (
	"sort"
	"strings"
)

// StackGroup is one --suggest-stacks report entry — a suggestion, never
// an assignment (docs/discovery.md's own "stack-grouping inference,
// human-reviewable, never auto-assigned," directly following UBI-18's
// own established "a Terraform module path is a hint, never a silent
// stack split" precedent). Suggestion is "" for the ungrouped bucket —
// resources missing GroupTag entirely, or whose own trailing ARN
// segment has no separator a naming-prefix heuristic can key off of.
type StackGroup struct {
	Suggestion string
	Resources  []DiscoveredResource
}

// SuggestStacks groups resources for a read-only preview report —
// nothing here writes a proposal or assigns a stack; `--stack <name>`
// stays required, unchanged, to actually author anything. Grouping key,
// in order of preference:
//
//  1. groupTag's own value, if non-empty on a resource (an operator-
//     named tag key — e.g. "Project"/"Stack"/"Environment" — never
//     guessed; pass "" to skip straight to the naming heuristic).
//  2. A naming-prefix heuristic over the resource's own trailing ARN
//     segment (split on the first "-"/"_"/"." separator) — a much
//     weaker signal than a real tag, used only when no grouping tag is
//     configured at all.
//
// A resource matching neither lands in the "" (ungrouped) bucket,
// itself a real, visible line in the report rather than silently
// dropped.
func SuggestStacks(resources []DiscoveredResource, groupTag string) []StackGroup {
	order := []string{}
	groups := map[string][]DiscoveredResource{}
	for _, r := range resources {
		key := suggestionKey(r, groupTag)
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], r)
	}

	out := make([]StackGroup, 0, len(order))
	for _, key := range sortedGroupKeys(order) {
		out = append(out, StackGroup{Suggestion: key, Resources: groups[key]})
	}
	return out
}

func suggestionKey(r DiscoveredResource, groupTag string) string {
	if groupTag != "" {
		if v, ok := r.Tags[groupTag]; ok && v != "" {
			return v
		}
		return ""
	}
	a, err := ParseARN(r.ARN)
	if err != nil {
		return ""
	}
	return namingPrefix(a.ResourceID)
}

// namingPrefix derives a naming-based grouping key from a resource id by
// splitting on the first conventional separator — "payments-queue-1" ->
// "payments", "networking_vpc" -> "networking". Returns "" (ungrouped)
// if id has no such separator at all — never a guess from the whole,
// possibly-unique id.
func namingPrefix(id string) string {
	if idx := strings.IndexAny(id, "-_."); idx > 0 {
		return id[:idx]
	}
	return ""
}

// sortedGroupKeys puts the ungrouped ("") bucket last, real suggestions
// alphabetically first — the report's own most useful reading order
// (the groups worth acting on, before the leftovers).
func sortedGroupKeys(keys []string) []string {
	var named []string
	ungrouped := false
	for _, k := range keys {
		if k == "" {
			ungrouped = true
		} else {
			named = append(named, k)
		}
	}
	sort.Strings(named)
	if ungrouped {
		named = append(named, "")
	}
	return named
}
