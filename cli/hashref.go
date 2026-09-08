package cli

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ubiquex/ubiquex/core"
)

// Resolving a proposal reference the way git resolves a commit.
//
// Every receipt and every `ubx history` line prints a 12-character
// truncated hash. Six commands then refused it and demanded all 64:
// `why`, `alias set` and `restore` with a validation error, and
// `promote`, `revert-plan` and `writeback` with "proposal not found in
// ledger", which is worse because the proposal does exist. `ship` alone
// accepted a prefix, because it happened to resolve through
// resolvePlanHash, which had always done prefix matching over
// .ubx/plans/. So this was an inconsistency inside the tool rather than
// a rule anyone had decided.
//
// Any unambiguous prefix resolves. Not "exactly 12": the display length
// is a presentation choice that could change, and a reader pasting from
// a log should never have to count characters to find out whether they
// pasted enough.
//
// Ambiguity refuses and names the candidates, rather than picking the
// first match. Two proposals sharing a prefix is rare and a silent
// choice between them would be unrecoverable.

// ErrRefAmbiguous means a prefix matched more than one proposal.
var ErrRefAmbiguous = errors.New("ambiguous proposal reference")

// ErrRefNotFound means a reference matched no proposal at all.
var ErrRefNotFound = errors.New("no such proposal")

// ErrRefTooShort means a prefix was below minRefLen. Its own error
// rather than a not-found, because the two need different advice: one
// says give more characters, the other says this proposal does not
// exist, and telling a reader the second when the first is true sends
// them looking for a missing proposal.
var ErrRefTooShort = errors.New("proposal reference too short")

// minRefLen is the shortest prefix accepted at all.
//
// Four is git's own floor and the reasoning carries: below that a prefix
// is likelier to be a typo that happens to match than a reference
// someone meant, and an accidental match on a destructive command is not
// a failure mode worth leaving open. Shorter is refused as too short
// rather than silently resolved.
const minRefLen = 4

// resolveProposalPrefix expands an unambiguous prefix to a full proposal
// ID by walking the ledger's own chain.
//
// Ambiguity refuses and names the candidates rather than taking the
// first match: two proposals sharing a prefix is rare, and a silent
// choice between them would be unrecoverable.
//
// Git-local only, deliberately. A remote [ledger] store addresses one
// chain per stack behind a context and credentials this function has
// neither of, and every command that opens one already requires --stack
// and works from full hashes. A remote store falls through to the
// caller's own not-found message, exactly as today, rather than this
// pretending to have searched.
func resolveProposalPrefix(ledgerDir, ref string) (string, error) {
	if len(ref) < minRefLen {
		return "", fmt.Errorf("%w: %q is only %d character(s) -- give at least %d", ErrRefTooShort, ref, len(ref), minRefLen)
	}
	chain, err := core.Open(ledgerDir).Chain()
	if err != nil {
		return "", err
	}
	var matches []string
	for _, p := range chain {
		if strings.HasPrefix(p.ID, ref) {
			matches = append(matches, p.ID)
		}
	}
	sort.Strings(matches)
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("%w: %s", ErrRefNotFound, ref)
	default:
		short := make([]string, len(matches))
		for i, m := range matches {
			short[i] = shortRef(m)
		}
		return "", fmt.Errorf("%w: %q matches %d proposals (%s) -- give more characters",
			ErrRefAmbiguous, ref, len(matches), strings.Join(short, ", "))
	}
}

// isHexRef reports whether ref could be a hash or a prefix of one.
// Anything carrying a non-hex character is a name, not a reference, and
// that distinction is what keeps the two error messages honest: `alias
// set` used to report a real hash as a missing alias name, which sent
// the reader looking for an alias they had never created.
func isHexRef(ref string) bool {
	if ref == "" {
		return false
	}
	for _, r := range ref {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}
