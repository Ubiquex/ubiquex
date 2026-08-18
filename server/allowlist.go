// allowlist.go is UBI-166's own real fix: before this, an incoming
// webhook-triggered action (plan/re-plan/accept/ship) never checked
// Config.Repos membership at all -- any repository the underlying
// credential could reach (a GitHub App's own installation scope; a
// single static token for GitLab/Azure DevOps/Bitbucket Server, which
// has no narrower per-repo concept at all) got plan/ship attempted,
// silently defaulting to ledger_dir "." when unlisted. Only
// drift-watch (a real, incidental exception -- server/drift.go's own
// polling loop has no triggering event to derive "which repo" from at
// all) ever iterated Config.Repos as an actual scope limiter. That
// enforcement is unchanged and lives where it always has: every
// platform's own webhook handler refuses outright, loudly logged, the
// moment matchingRepoConfigs* returns zero entries for an event's own
// repository identity.
//
// What UBI-167 changed is only where the stack's own location comes
// from, never the allowlist itself. Config.Repos entries used to carry
// a manually-declared ledger_dir; a stack's real location within its
// repo is a property of the repo itself (its own .ubx/config), not
// something the central server config should have to know and keep in
// sync by hand. So candidates here are now the ledger dirs
// stackdiscovery.go actually FOUND in the already-allowed repo's own
// checkout, rather than the ones an operator declared. The resolution
// logic below -- deepest-matching-stack-wins against the event's own
// changed files, refusing outright rather than guessing -- is UBI-166's
// own, unchanged, and still fixes the independent "first Config.Repos
// entry silently wins" bug that investigation surfaced: a repository
// with two real, independently configured stacks could never be routed
// correctly before it, regardless of which stack's own files a given PR
// actually touched.
package server

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrNoStackDiscovered means the repository IS on Config.Repos (its
// allowlist membership was already confirmed by the caller) but its own
// checkout contains no .ubx/config anywhere at all, so there is no real
// stack for ubx server to act on.
//
// Deliberately a separate condition from "not on the allowlist", which
// never reaches this resolver: that refusal fires earlier, in each
// platform's own webhook handler, on its own distinct log line. Before
// UBI-167 the zero case here meant "unlisted repo" (candidates were
// Config.Repos entries); now candidates are auto-discovered stacks, so
// zero means something genuinely different and says so.
var ErrNoStackDiscovered = errors.New("no .ubx/config found anywhere in this repository's own checkout, so it declares no real stack for ubx server to act on")

// ErrAmbiguousStack means more than one real stack was discovered in
// this repository, and this specific event's own changed files don't
// resolve to exactly one of them -- refused outright, the same "never
// guess, either direction is a real defect" doctrine
// discoverProposalFile/isAuthorizedToShip already apply elsewhere in
// this package.
var ErrAmbiguousStack = errors.New("this event's own changed files do not resolve to exactly one discovered stack")

// resolveLedgerDir picks the one, real, correct ledger dir among
// candidates (every stack discoverStacks actually found in the repo's
// own checkout, each a real repo-relative directory containing a real
// .ubx/config) for a single webhook-triggered action.
//
// Zero candidates: the repo declares no stack at all --
// ErrNoStackDiscovered.
//
// Exactly one candidate: used directly; changedFiles is never
// inspected. Callers should skip fetching changedFiles entirely
// whenever they already know len(candidates) <= 1 -- see
// resolveLedgerDirLazy -- so the common, single-stack-per-repository
// case pays zero extra API cost from this change.
//
// More than one candidate: picks the candidate whose own ledger dir is
// the most specific (deepest) real path match against at least one of
// changedFiles -- "most specific stack wins," the same real convention
// path-based routing (nginx location blocks, HTTP path routers)
// already uses. Deliberately not a blanket "more than one match is
// always ambiguous" rule: a real, legitimate repo-root stack
// (.ubx/config at the repo root) coexisting with a real, nested
// subdirectory stack is a genuine, valid configuration, not a
// misconfiguration -- the nested, more specific stack wins for a
// changed file under its own subtree. Two or more candidates tied at
// the same deepest match, or zero candidates matching any changed file
// at all, are both refused outright as ErrAmbiguousStack.
func resolveLedgerDir(candidates []string, changedFiles []string) (string, error) {
	switch len(candidates) {
	case 0:
		return "", ErrNoStackDiscovered
	case 1:
		return candidates[0], nil
	}

	bestDepth := -1
	var best []string
	for _, c := range candidates {
		matched := false
		for _, f := range changedFiles {
			if pathUnderLedgerDir(f, c) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		depth := ledgerDirDepth(c)
		switch {
		case depth > bestDepth:
			bestDepth = depth
			best = []string{c}
		case depth == bestDepth:
			best = append(best, c)
		}
	}

	switch len(best) {
	case 1:
		return best[0], nil
	case 0:
		return "", fmt.Errorf("%w: none of this event's own changed files fall under any of the %d stacks discovered in this repository", ErrAmbiguousStack, len(candidates))
	default:
		return "", fmt.Errorf("%w: this event's own changed files touch %d equally-specific discovered stacks at once", ErrAmbiguousStack, len(best))
	}
}

// resolveLedgerDirLazy avoids calling fetchChangedFiles at all when
// candidates already resolves unambiguously (0 or 1 entries) -- the
// changed-files fetch is a real, extra API call only a genuinely
// multi-stack repository should ever pay for.
func resolveLedgerDirLazy(candidates []string, fetchChangedFiles func() ([]string, error)) (string, error) {
	if len(candidates) <= 1 {
		return resolveLedgerDir(candidates, nil)
	}
	changedFiles, err := fetchChangedFiles()
	if err != nil {
		return "", fmt.Errorf("list changed files to resolve discovered stack: %w", err)
	}
	return resolveLedgerDir(candidates, changedFiles)
}

// resolveStackIn is UBI-167's own real entry point, and the only one
// every platform's own webhook handler calls: discover the real stacks
// present in an already-allowlisted repository's own checkout, then
// resolve them through UBI-166's own unchanged deepest-matching-stack-
// wins logic above. repoDir must already be checked out at the event's
// own real ref -- discovery reads what that specific commit actually
// contains, never a stale working tree.
func resolveStackIn(repoDir string, fetchChangedFiles func() ([]string, error)) (string, error) {
	stacks, err := discoverStacks(repoDir)
	if err != nil {
		return "", err
	}
	return resolveLedgerDirLazy(stacks, fetchChangedFiles)
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
