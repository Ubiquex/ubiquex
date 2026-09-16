package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ubiquex/ubiquex/blueprint"
	"github.com/ubiquex/ubiquex/core"
)

// blueprintreconcile.go answers, after a restore, what the stack's own
// `blueprints` table would have to say for the next `ubx plan` to agree
// with the head just restored to.
//
// The trap it closes is that a restore succeeds and is then quietly
// undone. Restore rebuilds the recorded config; it never re-runs a
// blueprint and never reads the table. So a stack whose table still names
// v2, restored to a head that used v1, holds v1's shape in its ledger and
// v2's declaration in its config, and the next plan proposes moving it all
// back. Both steps are behaving correctly and the net effect is that
// nothing happened.
//
// # Why this reports rather than edits
//
// There is no single file to edit. The effective table is merged across a
// cascade of directories in four formats, and the cascade tracks per-key
// provenance naming the file that supplied each effective value.
//
// That provenance exists for a key that is already there, so changing or
// removing an entry has a known target. It does not exist for a key that
// is absent, because nothing supplied it, so ADDING an entry back has no
// file to add it to and nothing but a guess to choose one.
//
// Adding one back is the case that motivates this: a stack drops a
// blueprint, later restores to a head that used it, and the table needs an
// entry rather than a changed version. An editor handling the two cases
// provenance can answer and refusing the one it cannot would be the worst
// available shape, so this reports all three and edits none.

// blueprintUse is one blueprint name as a head actually used it.
type blueprintUse struct {
	Name string
	// Declarations are the distinct declared sources recorded for this
	// name at that head, sorted. Usually one. More than one is real
	// rather than a bug: a resource created under v1 and never
	// re-resolved when the table moved to v2 keeps naming v1, so the
	// name genuinely had two sources in play.
	Declarations []string
	// Undeclared counts resources recorded with this blueprint but with
	// no declaration at all, which is every blueprint source written
	// before UBI-282. Their content hash names the bytes and cannot be
	// reversed into a source.
	Undeclared int
	// Hashes are the content hashes recorded for this name.
	Hashes map[string]bool
	// CalledNames are the version-independent identities of the sources
	// this head recorded, derived the same way the lock derives its own
	// key. This is the primary pairing key: see reconcileBlueprints.
	CalledNames map[string]bool
}

// blueprintReconcile is the whole comparison, kept as data so it can be
// tested without parsing rendered text.
type blueprintReconcile struct {
	Differs  []blueprintDiff // in both, declaration disagrees
	Missing  []blueprintUse  // the head used it, the table has no entry
	Extra    []string        // the table declares it, the head never used it
	Conflict []blueprintUse  // the head used one name with several sources
	Unknown  []blueprintUse  // used, but recorded before declarations existed
	UsedAll  int             // how many distinct blueprints the head used
}

type blueprintDiff struct {
	Name     string
	Declared string // what the stack declares now
	Used     string // what the head actually used
	// FromLock records that the declaration was found in the stack lock
	// rather than the config table, which is how an inline HCL
	// declaration reaches this report. It changes the wording only: a
	// reader told "your table says" about a stack with no table would go
	// looking in the wrong file.
	FromLock bool
}

// Empty reports whether there is nothing at all to say.
func (r blueprintReconcile) Empty() bool {
	return len(r.Differs) == 0 && len(r.Missing) == 0 && len(r.Extra) == 0 &&
		len(r.Conflict) == 0 && len(r.Unknown) == 0
}

// blueprintNameFromRef pulls the blueprint name off a provenance ref,
// which is always "<name>:sha256:<hex>" (blueprint.ExpandCalls' own
// stamping). Same split `ubx why` already does to render one.
func blueprintNameFromRef(ref string) string {
	name, _, ok := strings.Cut(ref, ":")
	if !ok {
		return ref
	}
	return name
}

// blueprintHashFromRef pulls the content hash off a provenance ref,
// which is always "<name>:sha256:<hex>". The part after the first colon
// is exactly the "sha256:<hex>" form a lock entry records, so the two
// are directly comparable.
func blueprintHashFromRef(ref string) string {
	_, hash, ok := strings.Cut(ref, ":")
	if !ok {
		return ""
	}
	return hash
}

// blueprintsUsedAt collects what a head's own live resources record about
// the blueprints that produced them.
//
// This is deliberately NOT "the table as it was at that head". The ledger
// records what produced each resource, not what was declared, and the two
// differ in both directions: a blueprint declared but never called leaves
// no trace at all, and a resource left un-re-resolved keeps naming an
// older source than the table held. What can be recovered is usage, so
// usage is what this returns and what the report compares against.
func blueprintsUsedAt(l *core.Ledger, headID, stack string) (map[string]*blueprintUse, error) {
	entries, err := l.AddressesAt(headID, stack, false)
	if err != nil {
		return nil, err
	}
	used := map[string]*blueprintUse{}
	seen := map[string]map[string]bool{}
	for _, e := range entries {
		sources, found, err := l.FoldSourcesAt(headID, e.Address)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		for _, s := range sources {
			if s.Kind != "blueprint" {
				continue
			}
			name := blueprintNameFromRef(s.Ref)
			u := used[name]
			if u == nil {
				u = &blueprintUse{Name: name, Hashes: map[string]bool{}, CalledNames: map[string]bool{}}
				used[name] = u
				seen[name] = map[string]bool{}
			}
			if h := blueprintHashFromRef(s.Ref); h != "" {
				u.Hashes[h] = true
			}
			// The same derivation the lock keys itself by, applied to
			// what this head recorded, so the two sides meet on a value
			// neither a version bump nor a rename of the packaged name
			// disturbs.
			if s.Declaration != "" {
				if cn := blueprint.NameFromSource(s.Declaration, s.DeclaredPath); cn != "" {
					u.CalledNames[cn] = true
				}
			}
			// A call that named its source inline records no
			// declaration, deliberately (UBI-282): there is no table
			// entry behind it, so it is not a table entry to reconcile.
			// Counting it as undeclared would invent a table row nobody
			// ever wrote.
			if s.Declaration == "" {
				if s.DeclaredSource == "" {
					u.Undeclared++
				}
				continue
			}
			if !seen[name][s.Declaration] {
				seen[name][s.Declaration] = true
				u.Declarations = append(u.Declarations, s.Declaration)
			}
		}
	}
	for _, u := range used {
		sort.Strings(u.Declarations)
	}
	return used, nil
}

// declaredSource is one blueprint this stack declares, and where the
// declaration was found.
type declaredSource struct {
	Source string
	// ContentHash is what this declaration resolved to, known only from
	// the lock: the config table records a source and no hash.
	ContentHash string
	// FromLock is true when the config table had no entry and the stack
	// lock did, which is what an HCL stack declaring inline looks like
	// from here.
	FromLock bool
}

// stackDeclarations is everything this stack declares, from both places a
// declaration can live.
//
// The config table is not the only declaration site, which this report
// originally assumed and was wrong about. An HCL stack declares a
// blueprint inline on the block itself, with source/version/path
// attributes and no table entry at all, and `ubx restore` never sees that
// file: it is handed a head, not a document.
//
// What it can see is .ubx/blueprints.lock, which records what every
// remote call resolved to, keyed by stack and name, regardless of where
// the declaration was written. So the lock is what makes an inline
// declaration visible here.
//
// Reading only the table meant an HCL stack declaring inline had EVERY
// blueprint reported as a missing entry, advising a reader to add
// something already present. That is the one line this report cannot be
// wrong about without being actively misleading, since "add an entry" is
// advice someone acts on.
//
// A local inline call is absent from both and is correctly never
// reported: it records no declaration in the first place, because a path
// is not a reference that can be repointed and there is no table entry
// behind it to reconcile.
func stackDeclarations(table map[string]string, lock *blueprint.StackLock, stack string) map[string]declaredSource {
	out := make(map[string]declaredSource, len(table))
	for name, src := range table {
		out[name] = declaredSource{Source: src}
	}
	if lock == nil {
		return out
	}
	for name, entry := range lock.Stacks[stack] {
		if _, inTable := out[name]; inTable {
			// The table is the declaration site a reader edits, so it
			// stays authoritative for what to compare against. The lock
			// only fills in names the table does not mention.
			continue
		}
		out[name] = declaredSource{Source: entry.Source, ContentHash: entry.ContentHash, FromLock: true}
	}
	return out
}

// reconcileBlueprints compares a head's own usage against what the stack
// declares right now, from both declaration sites.
func reconcileBlueprints(l *core.Ledger, headID, stack string, declared map[string]declaredSource) (blueprintReconcile, error) {
	used, err := blueprintsUsedAt(l, headID, stack)
	if err != nil {
		return blueprintReconcile{}, err
	}
	r := blueprintReconcile{UsedAll: len(used)}

	names := make([]string, 0, len(used))
	for n := range used {
		names = append(names, n)
	}
	sort.Strings(names)

	// # How a head's usage is paired to a declaration
	//
	// Three keys, tried in order, because no single one works everywhere
	// and the first version of this shipped with the wrong one.
	//
	// This report originally looked a head's blueprint name up directly in
	// the declarations, which assumed the ledger's name and the
	// declaration's name come from the same authority. Nothing ever said
	// they did, and they do not.
	//
	// A ledger ref carries the blueprint's own PACKAGED name, from its
	// blueprint.lock.json. A lock entry is keyed by the name the CALL
	// used, derived from the last path segment of the source. For
	// "oci://ghcr.io/ubx-blueprints/rev-bp:v1.0.0" holding a blueprint
	// packaged as "bp", those are "bp" and "rev-bp", and the report named
	// one blueprint twice: missing under one name and unused under the
	// other. Both were the same artifact, same source, same bytes.
	//
	// Both differences are deliberate and both are right for their own
	// job. The packaged name answers "what is this blueprint" and belongs
	// in provenance. The called name answers "what did this stack ask
	// for" and belongs in a file a person edits. Forcing either to adopt
	// the other makes one of those answers wrong.
	//
	// 1. The packaged name, exact for a table-declared blueprint, because
	//    resolveOne REQUIRES the pulled blueprint's packaged name to match
	//    the declared one and errors otherwise. Those two cannot differ.
	//
	// 2. The version-independent identity of the declared source, derived
	//    by blueprint.NameFromSource, which is exactly how the lock
	//    derives its own key for an HCL call. This is the one that works
	//    when the names differ AND the version has moved.
	//
	// 3. The content hash, last, as a fallback for the same bytes
	//    published under two spellings.
	//
	// The hash was tried first and alone, and that was wrong in the case
	// this report exists for. Two versions of a blueprint are different
	// bytes by construction, so a version change is precisely when hash
	// pairing finds nothing, and both sides then fell back to names that
	// disagree. The result was one blueprint reported twice, as missing
	// and as unused, exactly when it should have been reported once, as a
	// version that moved.
	//
	// So the key has to be something a version change does NOT disturb,
	// and the identity of the source is that thing:
	// "oci://host/repo:v1" and "oci://host/repo:v2" derive one value.
	// The hash is the opposite of what was needed and is kept only as a
	// last resort, where it can still add a pair and can no longer be the
	// reason one is missed.
	declaredByHash := make(map[string]string, len(declared))
	declaredBySourceID := make(map[string]string, len(declared))
	for name, d := range declared {
		if d.ContentHash != "" {
			declaredByHash[d.ContentHash] = name
		}
		// A lock entry is already keyed by this value, so deriving it
		// again from the source is a no-op there and correct for a table
		// entry, whose key is a declared name rather than a derived one.
		if id := blueprint.NameFromSource(d.Source, ""); id != "" {
			declaredBySourceID[id] = name
		}
	}
	matched := make(map[string]bool, len(declared))

	for _, n := range names {
		u := used[n]
		declName := pairDeclaration(n, u, declared, declaredBySourceID, declaredByHash)
		decl, inTable := declared[declName]
		if inTable {
			matched[declName] = true
		}
		switch {
		case len(u.Declarations) > 1:
			// Reported rather than resolved: no single entry reproduces
			// resources that genuinely came from different sources, and
			// picking one silently would make the report wrong rather
			// than incomplete.
			r.Conflict = append(r.Conflict, *u)
		case len(u.Declarations) == 0:
			if u.Undeclared > 0 {
				r.Unknown = append(r.Unknown, *u)
			}
			if !inTable && u.Undeclared > 0 {
				r.Missing = append(r.Missing, *u)
			}
		case !inTable:
			r.Missing = append(r.Missing, *u)
		case decl.Source != u.Declarations[0]:
			r.Differs = append(r.Differs, blueprintDiff{Name: n, Declared: decl.Source, Used: u.Declarations[0], FromLock: decl.FromLock})
		}
	}

	for n := range declared {
		// Matched rather than used[n]: a declaration paired by hash is
		// not "never used" just because the ledger calls it something
		// else, which is exactly the false second line this fixes.
		if !matched[n] {
			r.Extra = append(r.Extra, n)
		}
	}
	sort.Strings(r.Extra)
	return r, nil
}

// writeBlueprintReconcile renders the report onto a restore receipt.
//
// It always ends by naming what it compared and what it did not, because
// the thing it cannot check is the likelier cause of a divergence than
// anything it can. Arguments are not in the ledger in either language:
// expanding a blueprint call clears the call and records only the result.
// So a reader can match every line here and still get a different plan,
// and a report that implied otherwise would be worse than none.
func writeBlueprintReconcile(w io.Writer, st *styler, r blueprintReconcile) {
	if r.Empty() {
		if r.UsedAll > 0 {
			fmt.Fprintf(w, "\nblueprints: the %d this head used match your table\n", r.UsedAll)
			writeReconcileScope(w)
		}
		return
	}

	fmt.Fprintf(w, "\nblueprints: your table does not match what this head used\n")
	// One column for the names, so the lines read as a table rather than
	// as prose that happens to start with a name. padStyled counts runes
	// and ignores ANSI escapes, which plain width arithmetic does not.
	nameCol := reconcileNameWidth(r)
	for _, d := range r.Differs {
		// Named by where the declaration actually is. A reader whose
		// stack declares inline has no blueprints table, and telling them
		// "your table says" sends them to a file that does not mention
		// this blueprint at all.
		where := "your table says"
		if d.FromLock {
			where = "your stack declares"
		}
		fmt.Fprintf(w, "  %s  %s %s, this head used %s\n", padStyled(st.Yellow(d.Name), nameCol), where, d.Declared, d.Used)
	}
	for _, m := range r.Missing {
		if len(m.Declarations) > 0 {
			fmt.Fprintf(w, "  %s  this head used %s, and nothing this stack declares mentions it\n", padStyled(st.Yellow(m.Name), nameCol), m.Declarations[0])
			continue
		}
		fmt.Fprintf(w, "  %s  this head used it, and nothing this stack declares mentions it (source not recorded, see below)\n", padStyled(st.Yellow(m.Name), nameCol))
	}
	for _, e := range r.Extra {
		fmt.Fprintf(w, "  %s  your table declares it, this head never used it\n", padStyled(st.Yellow(e), nameCol))
	}
	for _, c := range r.Conflict {
		fmt.Fprintf(w, "  %s  this head used %d different sources for one name, so no single entry reproduces it:\n", padStyled(st.Yellow(c.Name), nameCol), len(c.Declarations))
		for _, d := range c.Declarations {
			fmt.Fprintf(w, "      %s\n", d)
		}
	}
	if len(r.Unknown) > 0 {
		fmt.Fprintln(w)
		for _, u := range r.Unknown {
			fmt.Fprintf(w, "  %s  used by %d resource(s) resolved before ubx recorded declarations, so what asked for it is not in the ledger\n", st.Yellow(u.Name), u.Undeclared)
		}
		fmt.Fprintf(w, "  a content hash names bytes and cannot be turned back into a source, so this is\n"+
			"  not a clean result: those entries are unchecked rather than confirmed correct.\n")
	}
	fmt.Fprintf(w, "\n  nothing is edited for you: the entry a dropped blueprint needs is the one\n"+
		"  the config cascade cannot say which file should own.\n\n")
	fmt.Fprintf(w, "  looked in: .ubx/config's blueprints table, and .ubx/blueprints.lock, which\n"+
		"  is where a blueprint declared inline on an HCL block shows up.\n")
	writeReconcileScope(w)
}

// reconcileNameWidth is the width of the name column, the longest name
// any line will print.
func reconcileNameWidth(r blueprintReconcile) int {
	w := 0
	note := func(name string) {
		if n := cellWidth(name); n > w {
			w = n
		}
	}
	for _, d := range r.Differs {
		note(d.Name)
	}
	for _, m := range r.Missing {
		note(m.Name)
	}
	for _, e := range r.Extra {
		note(e)
	}
	for _, c := range r.Conflict {
		note(c.Name)
	}
	for _, u := range r.Unknown {
		note(u.Name)
	}
	return w
}

// writeReconcileScope states the boundary of the check, always.
func writeReconcileScope(w io.Writer) {
	fmt.Fprintf(w, "  checked: blueprint names and declared sources.\n")
	fmt.Fprintf(w, "  not checked: the arguments each call was made with. Expanding a call records\n"+
		"  its result and not its arguments, so the same blueprint at the same version can\n"+
		"  still produce a different plan. Matching every line above does not guarantee the\n"+
		"  next plan agrees with this head.\n")
}

// pairDeclaration finds which declaration a head's usage corresponds to,
// returning the used name unchanged when nothing pairs.
//
// The order is the point. See reconcileBlueprints for why the source
// identity has to come before the content hash.
func pairDeclaration(usedName string, u *blueprintUse, declared map[string]declaredSource, bySourceID, byHash map[string]string) string {
	// 1. The packaged name, exact wherever a declaration states a name.
	if _, ok := declared[usedName]; ok {
		return usedName
	}
	// 2. The identity of the source, which survives a version change.
	for cn := range u.CalledNames {
		if dn, ok := bySourceID[cn]; ok {
			return dn
		}
		// A lock key IS this identity, so a direct hit counts too, for a
		// declaration whose own source no longer derives to it.
		if _, ok := declared[cn]; ok {
			return cn
		}
	}
	// 3. Same bytes under a different spelling.
	for h := range u.Hashes {
		if dn, ok := byHash[h]; ok {
			return dn
		}
	}
	return usedName
}
