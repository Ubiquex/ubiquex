package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

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
	Declared string // what the table says now
	Used     string // what the head actually used
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
				u = &blueprintUse{Name: name}
				used[name] = u
				seen[name] = map[string]bool{}
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

// reconcileBlueprints compares a head's own usage against the table the
// stack declares right now.
func reconcileBlueprints(l *core.Ledger, headID, stack string, table map[string]string) (blueprintReconcile, error) {
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

	for _, n := range names {
		u := used[n]
		declared, inTable := table[n]
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
		case declared != u.Declarations[0]:
			r.Differs = append(r.Differs, blueprintDiff{Name: n, Declared: declared, Used: u.Declarations[0]})
		}
	}

	for n := range table {
		if used[n] == nil {
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
		fmt.Fprintf(w, "  %s  table says %s, this head used %s\n", padStyled(st.Yellow(d.Name), nameCol), d.Declared, d.Used)
	}
	for _, m := range r.Missing {
		if len(m.Declarations) > 0 {
			fmt.Fprintf(w, "  %s  this head used %s, your table has no entry\n", padStyled(st.Yellow(m.Name), nameCol), m.Declarations[0])
			continue
		}
		fmt.Fprintf(w, "  %s  this head used it, your table has no entry (source not recorded, see below)\n", padStyled(st.Yellow(m.Name), nameCol))
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
