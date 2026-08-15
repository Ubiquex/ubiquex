// allowlist.go is UBI-166's own real fix: before this, an incoming
// webhook-triggered action (plan/re-plan/accept/ship) never checked
// Config.Repos membership at all -- any repository the underlying
// credential could reach (a GitHub App's own installation scope; a
// single static token for GitLab/Azure DevOps/Bitbucket Server, which
// has no narrower per-repo concept at all) got plan/ship attempted,
// silently defaulting to ledger_dir "." when unlisted. Only
// drift-watch (a real, incidental exception -- server/drift.go's own
// polling loop has no triggering event to derive "which repo" from at
// all) ever iterated Config.Repos as an actual scope limiter. This
// file makes Config.Repos a real, enforced allowlist for every
// platform's own webhook-triggered path too, and fixes the
// independent "first Config.Repos entry silently wins" bug the same
// investigation surfaced: a repository with two real, independently
// configured stacks (two Config.Repos entries, same owner/repo, each
// its own LedgerDir) could never be routed correctly before this --
// only the first-listed entry was ever consulted, for every event on
// that repository, regardless of which stack's own files a given PR
// actually touched.
package server

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrRepoNotAllowed means the platform+repo identity a real webhook
// event named has no Config.Repos entry at all -- UBI-166's own real
// allowlist enforcement. ubx server never automates plan/accept/ship
// against a repository the operator didn't explicitly list, no matter
// how broadly the underlying credential can actually reach it.
var ErrRepoNotAllowed = errors.New("repository is not listed in ubx server's own Config.Repos allowlist (UBI-166)")

// ErrAmbiguousStack means more than one Config.Repos entry exists for
// the same platform+repo identity, and this specific event's own
// changed files don't resolve to exactly one of them -- refused
// outright, the same "never guess, either direction is a real defect"
// doctrine discoverProposalFile/isAuthorizedToShip already apply
// elsewhere in this package.
var ErrAmbiguousStack = errors.New("this event's own changed files do not resolve to exactly one configured stack")

// resolveLedgerDir picks the one, real, correct LedgerDir among
// candidates (every Config.Repos entry already matched on platform +
// repo identity) for a single webhook-triggered action.
//
// Zero candidates: the repo isn't configured for ubx server automation
// at all -- ErrRepoNotAllowed.
//
// Exactly one candidate: used directly; changedFiles is never
// inspected. Callers should skip fetching changedFiles entirely
// whenever they already know len(candidates) <= 1 -- see
// resolveLedgerDirLazy -- so the common, single-stack-per-repository
// case pays zero extra API cost from this change.
//
// More than one candidate: picks the candidate whose own LedgerDir is
// the most specific (deepest) real path match against at least one of
// changedFiles -- "most specific configured stack wins," the same
// real convention path-based routing (nginx location blocks, HTTP
// path routers) already uses. Deliberately not a blanket "more than
// one match is always ambiguous" rule: a real, legitimate repo-root
// stack (ledger_dir ".") coexisting with a real, nested subdirectory
// stack is a genuine, valid configuration, not a misconfiguration --
// the nested, more specific stack wins for a changed file under its
// own subtree. Two or more candidates tied at the same deepest match,
// or zero candidates matching any changed file at all, are both
// refused outright as ErrAmbiguousStack.
func resolveLedgerDir(candidates []RepoConfig, changedFiles []string) (string, error) {
	switch len(candidates) {
	case 0:
		return "", ErrRepoNotAllowed
	case 1:
		return candidates[0].LedgerDir, nil
	}

	bestDepth := -1
	var best []RepoConfig
	for _, c := range candidates {
		matched := false
		for _, f := range changedFiles {
			if pathUnderLedgerDir(f, c.LedgerDir) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		depth := ledgerDirDepth(c.LedgerDir)
		switch {
		case depth > bestDepth:
			bestDepth = depth
			best = []RepoConfig{c}
		case depth == bestDepth:
			best = append(best, c)
		}
	}

	switch len(best) {
	case 1:
		return best[0].LedgerDir, nil
	case 0:
		return "", fmt.Errorf("%w: none of this event's own changed files fall under any of the %d stacks configured for this repository", ErrAmbiguousStack, len(candidates))
	default:
		return "", fmt.Errorf("%w: this event's own changed files touch %d equally-specific configured stacks at once", ErrAmbiguousStack, len(best))
	}
}

// resolveLedgerDirLazy avoids calling fetchChangedFiles at all when
// candidates already resolves unambiguously (0 or 1 entries) -- the
// changed-files fetch is a real, extra API call only a genuinely
// multi-stack repository configuration should ever pay for.
func resolveLedgerDirLazy(candidates []RepoConfig, fetchChangedFiles func() ([]string, error)) (string, error) {
	if len(candidates) <= 1 {
		return resolveLedgerDir(candidates, nil)
	}
	changedFiles, err := fetchChangedFiles()
	if err != nil {
		return "", fmt.Errorf("list changed files to resolve configured stack: %w", err)
	}
	return resolveLedgerDir(candidates, changedFiles)
}

// pathUnderLedgerDir reports whether changedFile (a repo-relative
// path, as every platform's own changed-files API already returns)
// falls under ledgerDir's own real subtree -- "." (the repo-root
// stack) matches every real path, by definition.
func pathUnderLedgerDir(changedFile, ledgerDir string) bool {
	ledgerDir = filepath.Clean(ledgerDir)
	changedFile = filepath.Clean(strings.TrimPrefix(changedFile, "/"))
	if ledgerDir == "." {
		return true
	}
	rel, err := filepath.Rel(ledgerDir, changedFile)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ledgerDirDepth is resolveLedgerDir's own real "most specific wins"
// measure -- real path segment count, not raw string length (two
// differently-named but equal-length paths must never accidentally
// tie/outrank each other based on spelling alone).
func ledgerDirDepth(ledgerDir string) int {
	ledgerDir = filepath.Clean(ledgerDir)
	if ledgerDir == "." {
		return 0
	}
	return strings.Count(ledgerDir, string(filepath.Separator)) + 1
}
