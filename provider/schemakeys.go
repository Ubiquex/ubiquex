package provider

import "sort"

// ConfigKeyIssue is one config key that matches no real Attribute or
// NestedBlock name in the schema Block it was found under (UBI-66).
type ConfigKeyIssue struct {
	// Path is the dot-notation path to the offending key, from the
	// resource's own top-level config (e.g.
	// "encryption_configuration.repository_name" for a key one level
	// inside a NestedBlock).
	Path string
	// Key is the offending key itself -- Path's own last segment,
	// available separately so a caller doesn't need to re-parse Path.
	Key string
	// Suggestion is the closest real key at the same schema level, ""
	// when nothing is close enough to be worth suggesting.
	Suggestion string
}

// knownKeySet returns the set of real key names -- Attribute and
// NestedBlock names alike -- block's own schema recognizes at its own
// level. Shared by encodeBlockValue's own fail-fast, single-error check
// (ctyvalue.go) and UnknownConfigKeys' exhaustive, suggestion-carrying
// walk below, so "what counts as a known key at this level" has exactly
// one implementation the two checks can never silently diverge on.
func knownKeySet(block Block) map[string]bool {
	known := make(map[string]bool, len(block.Attributes)+len(block.NestedBlocks))
	for _, a := range block.Attributes {
		known[a.Name] = true
	}
	for _, nb := range block.NestedBlocks {
		known[nb.TypeName] = true
	}
	return known
}

// knownKeyNames is knownKeySet's own ordered sibling -- closestKey needs
// an actual list of candidates to compare against, not just membership.
// Order follows block.Attributes then block.NestedBlocks, i.e. the real
// schema's own declared order (never a Go map's iteration order), so a
// tie between two equally-close suggestions resolves the same way on
// every run -- load-bearing for core.DoubleRun, which resolveOnce's own
// caller runs this against twice.
func knownKeyNames(block Block) []string {
	names := make([]string, 0, len(block.Attributes)+len(block.NestedBlocks))
	for _, a := range block.Attributes {
		names = append(names, a.Name)
	}
	for _, nb := range block.NestedBlocks {
		names = append(names, nb.TypeName)
	}
	return names
}

// UnknownConfigKeys walks config against block recursively -- attributes
// and nested blocks alike, the identical recognition rules encodeBlockValue
// enforces at encode time (ship time) -- and collects EVERY unrecognized
// key found anywhere in the tree, each paired with a fuzzy-match
// suggestion when a real key at that same level is close enough.
//
// Exists for resolve-time validation (UBI-66, core/resolver's own
// SchemaInspector.UnknownConfigKeys): a real live bug (a model drafting
// "repository_name"/"role_name"/"assume_role_policy_document"/
// "queue_name" -- none of them real, all plausible-sounding) reached a
// real ApplyResourceChange call before anything caught it.
// encodeBlockValue's own ErrUnrecognizedConfigKey check (ctyvalue.go)
// already catches this, but only at encode time -- ship time, arbitrarily
// late, one resource at a time, first-error-wins, no suggestions. This
// exists to run much earlier (resolve time, before any resource in a
// batch has shipped), collecting every mistake across the whole
// resource in one pass instead of stopping at the first, with an
// actionable "did you mean" for each.
//
// Only NestedBlocks are ever recursed into -- a plain Attribute's own
// Type is always primitive/list/set/map, never a further object shape
// with its own named keys to check (see encodeGenericValue's own doc
// comment for why; a map-typed attribute's own keys, e.g. "tags", are
// caller-chosen tag names, never schema-checkable).
func UnknownConfigKeys(block Block, config map[string]interface{}) []ConfigKeyIssue {
	return unknownConfigKeysAt(block, config, "")
}

func unknownConfigKeysAt(block Block, raw interface{}, prefix string) []ConfigKeyIssue {
	m, ok := raw.(map[string]interface{})
	if !ok {
		return nil
	}
	known := knownKeyNames(block)
	knownSet := make(map[string]bool, len(known))
	for _, k := range known {
		knownSet[k] = true
	}

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var issues []ConfigKeyIssue
	for _, k := range keys {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if !knownSet[k] {
			issues = append(issues, ConfigKeyIssue{Path: path, Key: k, Suggestion: closestKey(k, known)})
			continue
		}
		for _, nb := range block.NestedBlocks {
			if nb.TypeName == k {
				issues = append(issues, unknownConfigKeysWithinNested(nb, m[k], path)...)
			}
		}
	}
	return issues
}

// unknownConfigKeysWithinNested mirrors encodeNestedBlockValue's own
// Single/List/Set/Map nesting shapes (including its own "a bare object is
// always accepted as one-element shorthand for a List/Set block"
// convention, UBI-63 bug 2) -- recursing unknownConfigKeysAt per element
// so a bad key nested two or more levels deep is found too, not just at
// the resource's own top level.
func unknownConfigKeysWithinNested(nb NestedBlock, raw interface{}, path string) []ConfigKeyIssue {
	switch nb.Nesting {
	case NestingList, NestingSet:
		if arr, ok := raw.([]interface{}); ok {
			var issues []ConfigKeyIssue
			for _, item := range arr {
				issues = append(issues, unknownConfigKeysAt(nb.Block, item, path)...)
			}
			return issues
		}
		// Bare-object shorthand for a single element -- same convention
		// encodeNestedBlockValue accepts regardless of MaxItems.
		return unknownConfigKeysAt(nb.Block, raw, path)
	case NestingMap:
		m, ok := raw.(map[string]interface{})
		if !ok {
			return nil
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var issues []ConfigKeyIssue
		for _, k := range keys {
			issues = append(issues, unknownConfigKeysAt(nb.Block, m[k], path+"."+k)...)
		}
		return issues
	default: // Single, Group
		return unknownConfigKeysAt(nb.Block, raw, path)
	}
}

// closestKey returns the candidate in candidates most likely to be what
// key actually meant, or "" if nothing is close enough to be worth
// suggesting -- a wrong suggestion is worse than none. candidates is
// already in the schema's own declared order (knownKeyNames), so ties
// resolve deterministically (first-seen strictly-greater wins).
//
// Two tiers, checked in strict priority order -- NOT combined into one
// blended score. A hallucinated key is very often the real one plus an
// extra prefix/suffix token ("repository_name" for "name",
// "assume_role_policy_document" for "assume_role_policy" -- both real,
// live examples this was built against), which is a LARGE edit distance
// relative to string length; a blended score lets an unrelated
// similar-length candidate (e.g. "repository_url" vs "repository_name",
// same 11-character shared prefix, small edit distance) outscore the
// genuinely-intended "name", which is exactly backwards. So: prefer any
// substring-containment candidate outright, regardless of edit distance;
// only fall back to a pure edit-distance typo check when no candidate
// has that relationship at all.
func closestKey(key string, candidates []string) string {
	if best := closestBySubstring(key, candidates); best != "" {
		return best
	}
	return closestByEditDistance(key, candidates)
}

// closestBySubstring finds the candidate closest to key via substring
// containment (either direction), preferring the highest
// shorter-string/longer-string length ratio -- the most complete overlap
// -- among candidates that qualify. Guards against noise two ways: the
// shorter side must be at least 3 characters (a coincidental 1-2
// character overlap, e.g. "id" appearing inside an unrelated longer
// word, is not a meaningful signal), and the ratio itself must clear a
// low bar (0.2) so a tiny fragment of a long, otherwise-unrelated
// candidate doesn't qualify either.
func closestBySubstring(key string, candidates []string) string {
	const minShorterLen = 3
	const minRatio = 0.2
	best := ""
	bestRatio := 0.0
	for _, c := range candidates {
		shorter, longer := c, key
		if len(key) < len(c) {
			shorter, longer = key, c
		}
		if len(shorter) < minShorterLen || !containsSubstring(longer, shorter) {
			continue
		}
		ratio := float64(len(shorter)) / float64(len(longer))
		if ratio < minRatio {
			continue
		}
		if ratio > bestRatio {
			bestRatio = ratio
			best = c
		}
	}
	return best
}

// closestByEditDistance falls back to a plain Levenshtein ratio -- for a
// genuine typo of a same-length-ish key (e.g. "asssume_role_policy" for
// "assume_role_policy"), where no candidate has a substring relationship
// to key at all.
func closestByEditDistance(key string, candidates []string) string {
	const threshold = 0.75
	best := ""
	bestScore := 0.0
	for _, c := range candidates {
		maxLen := len(key)
		if len(c) > maxLen {
			maxLen = len(c)
		}
		if maxLen == 0 {
			continue
		}
		score := 1 - float64(levenshtein(key, c))/float64(maxLen)
		if score > bestScore {
			bestScore = score
			best = c
		}
	}
	if bestScore < threshold {
		return ""
	}
	return best
}

func containsSubstring(haystack, needle string) bool {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// levenshtein returns the classic byte-wise edit distance between a and
// b -- provider schema key names are always plain ASCII snake_case, so
// this never needs to be rune-aware.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			min := del
			if ins < min {
				min = ins
			}
			if sub < min {
				min = sub
			}
			curr[j] = min
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}
