package blueprint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// stacklock.go is the stack side of blueprint identity: which blueprint
// content a stack resolved against, recorded where a fresh clone can
// read it.
//
// The declaration says WHERE and the lock says WHAT. That split did not
// exist before this file. A stack declared a blueprint by URL and
// nothing recorded what that URL produced, so:
//
//   - A fresh clone could not reconstruct the blueprint. The pulled
//     directory was untracked working state with no convention about
//     whether it was committed.
//   - Two people could resolve the same stack against different content
//     from the same declaration, both successfully, with nothing
//     comparing them.
//   - blueprint.lock.json, which travels INSIDE a blueprint, records
//     content identity and no origin at all, so a pulled directory
//     cannot say which reference produced it. Only the consuming stack
//     can record that link, and until now it did not.
//
// The ledger was never blind: a resolved proposal records
// sources[].ref as "<name>:<content_hash>", hashed and signed. ubx
// recorded the answer and had no way to state the question. This file
// is the question.
//
// Not to be confused with blueprint.lock.json despite the similar job.
// That one is a blueprint's own manifest and travels with it. This one
// belongs to the consuming stack, names the blueprints that stack
// depends on, and is called blueprints.lock precisely so the two are
// not confused in an error message.

// StackLockFileName is the lock file's name under the ledger dir's own
// .ubx/ directory, alongside config.
//
// Committed, unlike everything else ubx writes under .ubx/. salt is
// gitignored because it is secret, plans/ and ledger.lock are working
// state, and config is committed because it is the stack's own
// declaration. This belongs with config: an uncommitted lock records
// nothing a fresh clone can use, which is the entire point of having
// one.
const StackLockFileName = "blueprints.lock"

// StackLockSchemaVersion is this file format's own version, so a future
// shape change is a real, detectable migration rather than a confusing
// parse failure.
const StackLockSchemaVersion = 1

// ErrLockMissingEntry means a blueprint is declared but the lock has no
// entry for it, which is the ordinary "someone added a declaration and
// did not update the lock" case.
var ErrLockMissingEntry = errors.New("blueprint is declared but not locked")

// LockEntry is one blueprint's resolved identity for one stack.
type LockEntry struct {
	// Source is the declaration this entry resolved from, verbatim.
	// Recorded as well as the hash because blueprint.lock.json carries
	// no origin, so without this nothing anywhere links a content hash
	// back to the reference that produced it.
	Source string `json:"source"`

	// ContentHash is the pulled blueprint's own verified content hash,
	// "sha256:<hex>", the same identity the ledger records in
	// sources[].ref and the same one `ubx blueprint verify` checks.
	ContentHash string `json:"content_hash"`
}

// StackLock is .ubx/blueprints.lock's own decoded shape.
//
// Keyed by stack first, because .ubx/plans/ is already explicitly shared
// by every stack using one ledger dir (that sharing is what makes a bare
// `ubx ship` have to choose between stacks at all). One directory holding
// several stacks is a real, supported situation, so this is stack-aware
// from its first version rather than growing a stack dimension later,
// which would be a format migration on a file people already have
// committed.
type StackLock struct {
	SchemaVersion int `json:"schema_version"`

	// existed records whether the file was actually on disk, as opposed
	// to this being the empty value LoadStackLock returns for a stack
	// that has none. It decides whether verification is enforced at all:
	// see Existed.
	existed bool

	// Stacks maps stack name -> blueprint name -> resolved identity.
	Stacks map[string]map[string]LockEntry `json:"stacks"`
}

func stackLockPath(ledgerDir string) string {
	return filepath.Join(ledgerDir, ".ubx", StackLockFileName)
}

// LoadStackLock reads ledgerDir's own lock file. A missing file is not
// an error and yields an empty lock: a stack that has never resolved a
// blueprint legitimately has none, and the first `ubx plan` writes it.
func LoadStackLock(ledgerDir string) (*StackLock, error) {
	path := stackLockPath(ledgerDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &StackLock{SchemaVersion: StackLockSchemaVersion, Stacks: map[string]map[string]LockEntry{}}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var l StackLock
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if l.SchemaVersion != StackLockSchemaVersion {
		return nil, fmt.Errorf("%s declares schema_version %d, and this ubx understands %d -- the file was written by a different version of ubx",
			path, l.SchemaVersion, StackLockSchemaVersion)
	}
	if l.Stacks == nil {
		l.Stacks = map[string]map[string]LockEntry{}
	}
	l.existed = true
	return &l, nil
}

// Existed reports whether a lock file was actually present.
//
// This is what makes the pin opt-in rather than a breaking change. A
// stack with no lock file has not adopted the mechanism, and refusing
// its `ubx resolve` would break every stack UBI-130 already serves the
// first time they ran it. No file means no enforcement, exactly as
// npm does not refuse without a package-lock.json.
//
// Once the file exists the stack HAS adopted it, and a declared
// blueprint missing from it is a real error: someone added a
// declaration without planning, and accepting that silently would make
// the lock describe less than the stack depends on.
func (l *StackLock) Existed() bool { return l != nil && l.existed }

// Save writes the lock file, with stable key ordering so a commit diff
// shows what actually changed rather than a reordered map.
//
// encoding/json already sorts map keys, so the ordering comes for free
// and this comment exists to say that it is relied upon rather than
// incidental.
func (l *StackLock) Save(ledgerDir string) error {
	l.SchemaVersion = StackLockSchemaVersion
	path := stackLockPath(ledgerDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// Entry returns the locked identity for one blueprint in one stack.
func (l *StackLock) Entry(stack, name string) (LockEntry, bool) {
	if l == nil {
		return LockEntry{}, false
	}
	e, ok := l.Stacks[stack][name]
	return e, ok
}

// Set records a resolved identity, creating the stack's own map on
// first use.
func (l *StackLock) Set(stack, name string, e LockEntry) {
	if l.Stacks == nil {
		l.Stacks = map[string]map[string]LockEntry{}
	}
	if l.Stacks[stack] == nil {
		l.Stacks[stack] = map[string]LockEntry{}
	}
	l.Stacks[stack][name] = e
}

// Prune removes entries for blueprints stack no longer declares, and
// reports what it removed.
//
// Returned rather than logged so the caller can name them in a receipt:
// a lock entry disappearing is a real change to what a stack depends on
// and should be visible in the same place the additions are.
func (l *StackLock) Prune(stack string, declared map[string]string) []string {
	entries := l.Stacks[stack]
	if entries == nil {
		return nil
	}
	var removed []string
	for name := range entries {
		if _, still := declared[name]; !still {
			removed = append(removed, name)
			delete(entries, name)
		}
	}
	sort.Strings(removed)
	if len(entries) == 0 {
		delete(l.Stacks, stack)
	}
	return removed
}

// CheckLocked compares a freshly resolved blueprint against what the
// lock records, and refuses on any disagreement.
//
// Refusing rather than warning, and never updating silently, because
// this is the whole mechanism: a lock that quietly accepts different
// content records nothing. `--update-lock` is the explicit path.
//
// The two mismatches get different messages on purpose, because they
// mean genuinely different things and the right response differs:
//
//   - The SOURCE moved. Someone edited the declaration and the lock is
//     simply behind. Ordinary, and the fix is to update.
//   - The CONTENT moved while the source stayed identical. Either a
//     mutable tag was repointed upstream, which is expected drift worth
//     looking at deliberately, or the registry served something other
//     than what it served before, which is not. Saying "run --update-lock"
//     as the first suggestion here would be wrong: the first thing to do
//     is find out why it changed.
func CheckLocked(stack, name string, locked LockEntry, gotSource, gotHash string) error {
	if locked.Source != gotSource {
		return fmt.Errorf("blueprint %q in stack %q is locked to %s but is now declared as %s -- the declaration changed and %s is behind; re-run with --update-lock to record the new source",
			name, stack, locked.Source, gotSource, StackLockFileName)
	}
	if locked.ContentHash != gotHash {
		return fmt.Errorf("blueprint %q in stack %q resolved to %s, but %s locks it to %s, from the same source %s.\n"+
			"The declaration did not change, so the content behind it did. Either a mutable tag was repointed, or the registry is serving different bytes than it served before.\n"+
			"Find out which before accepting it: `ubx blueprint pull %s <dir>` and compare. Re-run with --update-lock only once you know why it moved",
			name, stack, gotHash, StackLockFileName, locked.ContentHash, locked.Source, gotSource)
	}
	return nil
}

// LockMode is what a command does with the lock file.
type LockMode int

const (
	// LockOff involves the lock file not at all. The zero value on
	// purpose: every caller that has not opted in, including every
	// existing test, behaves exactly as it did before this file existed.
	LockOff LockMode = iota

	// LockVerify checks a resolved blueprint against the lock and
	// refuses on disagreement, writing nothing. `ubx resolve`, which
	// writes nothing today and should not start.
	LockVerify

	// LockWrite verifies like LockVerify and then records what it
	// resolved, filling in entries the lock does not have yet. `ubx
	// plan`, which already writes to .ubx/plans/.
	LockWrite

	// LockUpdate accepts whatever resolves and records it, which is the
	// only way past a mismatch. `--update-lock`, always explicit.
	LockUpdate
)

// LockPolicy is the ambient lock context for one command invocation:
// which stack is being resolved, where its lock lives, what the config
// declares, and what to do about it.
//
// Carried on the context rather than threaded through
// EvaluatePythonWithDeps -> ResolvePyDependencies and the two more call
// sites the TypeScript and Go translators will add, for the same reason
// executor.WithProgress is: every one of those already takes a ctx, so
// this adds no signature churn to a call graph that has nothing else to
// do with locking. An invocation that never sets one gets LockOff.
type LockPolicy struct {
	LedgerDir string
	Stack     string

	// Declared is .ubx/config's own [blueprints] table, name -> source.
	// The single declaration site: a code-local declaration would mean
	// two parsers and a precedence rule, and a static scan cannot see a
	// declaration inside a conditional anyway.
	Declared map[string]string

	Mode LockMode
}

type lockPolicyKey struct{}

// WithLockPolicy attaches p to ctx for the blueprint resolution that
// runs under it.
func WithLockPolicy(ctx context.Context, p LockPolicy) context.Context {
	return context.WithValue(ctx, lockPolicyKey{}, p)
}

// lockPolicyFrom returns ctx's own policy, or a LockOff zero value.
func lockPolicyFrom(ctx context.Context) LockPolicy {
	p, _ := ctx.Value(lockPolicyKey{}).(LockPolicy)
	return p
}

// mergeDeclaredBlueprints combines .ubx/config's own [blueprints] table
// with requirements.txt's entries, and reports which names the table
// superseded.
//
// [blueprints] wins. requirements.txt is kept because removing it would
// break every stack UBI-130 already serves, and because pip's "<name> @
// <url>" is a real, working declaration that a Python CI pipeline reads
// with no extra tooling. But it is Python-only, and the table is the one
// declaration site that works for all three languages, so the table is
// the path forward and the conflict resolves its way.
//
// The supersession is REPORTED rather than silent. Two tables that can
// disagree about the same name is the exact shape that produced a
// warning naming a config table a stack did not have, earlier this same
// week; the lesson taken from that one is that the quiet case is the
// expensive one.
func mergeDeclaredBlueprints(table map[string]string, fromRequirements []PyDependency) (merged []PyDependency, superseded []string) {
	byName := map[string]bool{}
	names := make([]string, 0, len(table))
	for n := range table {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		// The table's value is parsed with the same function
		// requirements.txt's right-hand side is, so the four forms
		// `ubx blueprint pull` accepts (oci://, git+<transport>:// with
		// an optional @ref and #subdirectory=, file://, a bare local
		// path) mean exactly the same thing in both places. A second
		// parser would be a second set of accepted spellings.
		src, ref, path, err := parsePyRequirementURL(table[n])
		if err != nil {
			// Deliberately not fatal here: this function merges, and the
			// resolution below reports a bad source with the dependency
			// name and URL already attached. Carrying the raw value
			// through lets that happen rather than producing a second,
			// context-free error from a merge step.
			src = table[n]
		}
		merged = append(merged, PyDependency{Name: n, URL: table[n], Source: src, Ref: ref, Path: path})
		byName[n] = true
	}
	for _, d := range fromRequirements {
		if byName[d.Name] {
			superseded = append(superseded, d.Name)
			continue
		}
		merged = append(merged, d)
	}
	sort.Strings(superseded)
	return merged, superseded
}
