package blueprint

import (
	"fmt"
	"sort"

	"github.com/ubiquex/ubiquex/core"
)

// lockdrop.go says what a dropped declaration left behind.
//
// # Why here
//
// Removing a blueprint call from a stack removes the lock entry and
// nothing else. The resources that blueprint produced stay in the ledger,
// stay real, and are now declared by nothing, because a destroy is never
// inferred from a declaration going missing.
//
// That rule is right and permanent. What was wrong is that the moment it
// bites produced this and nothing else:
//
//	dropped rev-bp2 from blueprints.lock: stack "rev2" no longer declares it
//	Plan  rev2 · from stack.ubx.hcl
//	delta: +0 ~0 -0
//
// A line saying a declaration went, followed by a line saying there are
// no changes, which together read as confirmation that nothing is
// outstanding. The queue that blueprint made is still running and still
// being billed.
//
// Every other mechanism that could have said so is structurally unable
// to. The divergence marker and the reconciliation report both reason
// about resources a plan proposes something about, and these are in no
// plan, so the resource with the weakest protection got the least
// warning (UBI-291).
//
// This is the one moment where that is not true. The prune has just
// decided which blueprint went, and the ledger already records which
// resources each blueprint produced. Both halves are in hand here and
// nowhere else, so the warning belongs here rather than in a document
// someone reads at a different time.
//
// # What it deliberately does not do
//
// It does not remove anything, propose anything, or fail. Inferring
// destruction from a missing declaration is the boundary this respects,
// so the most it can honestly do is say what exists and is now unclaimed.

// orphanedByDrop lists the live addresses in stack that the named
// blueprints produced, keyed by the name the lock used for each.
//
// Best-effort by construction: a stack with no ledger yet, or a ledger
// that cannot be read, yields nothing rather than an error. A warning
// that could fail a plan would be a worse trade than a warning that
// occasionally cannot be given.
func orphanedByDrop(ledgerDir, stack string, dropped []string) map[string][]core.Address {
	if len(dropped) == 0 || ledgerDir == "" {
		return nil
	}
	l := core.Open(ledgerDir)
	entries, err := l.Addresses(stack, false)
	if err != nil || len(entries) == 0 {
		return nil
	}
	want := make(map[string]bool, len(dropped))
	for _, n := range dropped {
		want[n] = true
	}

	out := map[string][]core.Address{}
	for _, e := range entries {
		sources, found, err := l.FoldSources(e.Address)
		if err != nil || !found {
			continue
		}
		for _, s := range sources {
			if s.Kind != "blueprint" {
				continue
			}
			// The lock is keyed by the name the CALL used, and a
			// provenance ref carries the blueprint's own packaged name,
			// which can differ. The declared source is what reconciles
			// them, by the same derivation the lock keys itself by.
			name := ""
			if s.Declaration != "" {
				name = NameFromSource(s.Declaration, s.DeclaredPath)
			}
			if !want[name] {
				continue
			}
			out[name] = append(out[name], e.Address)
		}
	}
	for name := range out {
		addrs := out[name]
		sort.Slice(addrs, func(i, j int) bool { return addrs[i].String() < addrs[j].String() })
		out[name] = addrs
	}
	return out
}

// dropNotes renders the drop itself and, where the ledger can say so,
// what it left behind.
func dropNotes(ledgerDir, stack string, dropped []string) []string {
	orphans := orphanedByDrop(ledgerDir, stack, dropped)
	notes := make([]string, 0, len(dropped))
	for _, name := range dropped {
		note := fmt.Sprintf("dropped %s from %s: stack %q no longer declares it", name, StackLockFileName, stack)
		addrs := orphans[name]
		if len(addrs) == 0 {
			notes = append(notes, note)
			continue
		}
		noun := "resource"
		verb := "is"
		if len(addrs) > 1 {
			noun, verb = "resources", "are"
		}
		note += fmt.Sprintf("\n  %d %s it produced %s still in the ledger and now declared by nothing:", len(addrs), noun, verb)
		for _, a := range addrs {
			note += "\n    " + a.String()
		}
		// The three real choices, with doing nothing named as one of
		// them, because it is the default and it is a decision.
		note += "\n  Nothing will change or remove them. `ubx terminate` takes an address;" +
			"\n  declaring the blueprint again brings them back under management;" +
			"\n  leaving them keeps them running and unmanaged."
		notes = append(notes, note)
	}
	return notes
}
