package cli

// known_dependents: where the cross-stack orphan check gets its
// neighbours from.
//
// Resolving a destroy walks each named neighbour ledger for a
// cross_stack_pin naming the address being destroyed, and refuses if it
// finds one (core/resolver/destroys.go). The neighbours have to be named
// by hand, because the producing stack has no index of who has pinned
// against it: pins are recorded in the CONSUMING stack's ledger, so the
// stack being asked to destroy something cannot enumerate its own
// dependents. docs/resolver.md's UBI-30 amendment chose an explicit list
// over inventing a registry, and docs/schema.md still carries the
// missing piece ("cross-stack workspace index format") as an open
// question.
//
// Until this existed, that list was `--known-dependent` and nothing
// else: a repeatable flag, remembered per invocation, by a human, at the
// exact moment they were destroying something. Naming zero neighbours is
// recorded honestly as status "not_performed" rather than passed off as
// a real check, but an honest record of a check nobody ran is not the
// same as running it.
//
// The set of stacks that depend on yours is a stable property of your
// stack, not of one command run, so it belongs in .ubx/config next to
// every other stable property. The flag still works and still adds to
// the configured list rather than replacing it: a neighbour that exists
// only for this one destroy is exactly what a flag is for, and silently
// dropping the configured ones when a flag appears would turn "add one
// more place to check" into "check only here", which is the wrong
// direction for a safety check to fail in.

// knownDependentsFor merges .ubx/config's own known_dependents list with
// any --known-dependent values, config first, flags appended, duplicates
// dropped.
//
// Deduplication is by exact string, deliberately. Two spellings of the
// same directory (a relative path and its absolute form, say) are not
// recognised as one, so that neighbour is simply opened and walked
// twice. That costs a redundant read and changes no answer, which is the
// right way for this to be wrong: collapsing paths that only look
// different would mean guessing that two strings name one ledger, and a
// wrong guess there drops a neighbour from a safety check.
func knownDependentsFor(cfg *Config, flagValues []string) []string {
	if cfg == nil {
		return append([]string(nil), flagValues...)
	}
	merged := make([]string, 0, len(cfg.KnownDependents)+len(flagValues))
	seen := map[string]bool{}
	for _, list := range [][]string{cfg.KnownDependents, flagValues} {
		for _, dir := range list {
			if dir == "" || seen[dir] {
				continue
			}
			seen[dir] = true
			merged = append(merged, dir)
		}
	}
	return merged
}
