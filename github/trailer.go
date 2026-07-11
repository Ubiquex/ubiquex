package github

import "regexp"

// proposalTrailerPattern matches the "ubx-proposal: <hash>" line
// docs/schema.md's pr_merge acceptance amendment pins as the convention a
// PR body must carry: a full 64-hex-char proposal hash, alone on its own
// line (case-sensitive lowercase hex, matching how core.Hash renders it).
var proposalTrailerPattern = regexp.MustCompile(`(?m)^ubx-proposal:\s*([0-9a-f]{64})\s*$`)

// ParseProposalTrailer extracts the claimed proposal hash from a PR body.
// ok is false if no line matches the convention at all — a PR merged
// without ever having had the trailer, which AcceptFromMerge treats as a
// hard failure (there is nothing to derive acceptance from).
func ParseProposalTrailer(body string) (hash string, ok bool) {
	m := proposalTrailerPattern.FindStringSubmatch(body)
	if m == nil {
		return "", false
	}
	return m[1], true
}
