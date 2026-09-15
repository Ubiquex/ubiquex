package blueprint

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/tseval"
)

// invokedeps.go makes an HCL blueprint call a peer of the other calling
// paths rather than a parallel implementation of half of one.
//
// # What it was
//
// invokeCall pulled each called blueprint into os.MkdirTemp, hashed the
// pulled copy fresh, evaluated it with the PLAIN evaluator, and threw
// the directory away. It never touched resolveDeclaredBlueprints, the
// content store, or the stack lock, and an `ubx resolve` of an HCL
// document created no .ubx directory at all.
//
// Two consequences, and the second is the larger one.
//
// A blueprint with any third-party dependency could not be called from
// HCL, in any of the three languages, even though it shipped its own
// pin. The plain evaluators know nothing about a blueprint's own
// declaration, so none of the resolution built for the declaration path
// applied:
//
//	TypeScript  Import "left-pad" not a dependency and not in import map
//	Go          github.com/ubiquex/ubx-sdk-go@v0.6.0: module lookup
//	            disabled by GOPROXY=off
//	Python      ModuleNotFoundError: No module named 'idna'
//
// And a mutable tag was followed blindly. Every other calling path
// records what a declaration resolved to and refuses when the content
// moves underneath it; the most declarative one was the only one with no
// such record, which is the asymmetry someone meets when their tag moves
// rather than when they read the design.
//
// # Remote sources are locked; a local path is not
//
// The split is principled rather than a compromise, and it is the same
// line isLocalSource already draws for a declared dependency.
//
// The lock exists because a REFERENCE can be repointed under you: an OCI
// tag moved, a git branch advanced, a registry serving different bytes
// than it served yesterday. A local path is not a reference to anything
// that can move. It names a directory, and whatever is in that directory
// is what you are calling, by definition.
//
// It also matters that a local blueprint is usually one the author is
// editing. The documented HCL example calls `../blueprints/postgres`,
// and locking that would turn every edit into a refusal demanding
// --update-lock. The declaration path accepts that friction because a
// declared dependency is normally something published. A blueprint
// called by relative path from the stack beside it is not.
//
// So a local call keeps today's behaviour exactly: pulled into a
// throwaway directory, hashed fresh, no packaging required, no lock
// entry. It also means the edit-then-resolve loop, which is the whole
// point of calling a blueprint by path, keeps working.
//
// # What changed for an existing HCL stack
//
// A stack calling a remote blueprint and having no .ubx/blueprints.lock
// will get one written on its next plan, and from then on a repointed
// tag is refused with the lock's own two messages rather than silently
// followed.
//
// That is correct behaviour arriving, not a regression, but it arrives
// without anyone opting in, so it is said plainly here, in the user
// docs, and in the refusal itself.
//
// # What this does NOT fix
//
// A Python blueprint's own third-party dependencies still do not resolve
// (UBI-265). That gap is in both calling paths, not this one, and
// closing it here would have meant inventing an answer Python does not
// have anywhere else.

// callDeclaration turns one HCL blueprint call into the same Declaration
// shape every other calling path resolves.
func callDeclaration(call resolver.BlueprintCall) Declaration {
	// Source verbatim: Pull takes an HCL call's source exactly as
	// written, and anything reshaped here would be classified by one rule
	// and fetched by another.
	return Declaration{
		Name:   blueprintNameFromCall(call),
		URL:    call.Blueprint,
		Source: call.Blueprint,
		Ref:    call.Ref,
		Path:   call.Path,
	}
}

// callIsLocal reports whether a call names something on this disk rather
// than a reference that can be repointed under it.
//
// Pull's own dispatch, deliberately, rather than PyDependency's
// isLocalSource. That one asks whether a requirements.txt URL carries a
// scheme, which is the right question for the input space it was written
// for and the wrong one here: an HCL call's "file:///path" has a scheme
// and is handled by Pull as a GIT source, since os.Stat fails on a URL
// and the git branch takes everything that reaches it. Classifying by
// scheme would have called that local and then fetched it as git.
//
// So the rule is Pull's: oci:// is remote, anything os.Stat finds is
// local, everything else is git.
func callIsLocal(call resolver.BlueprintCall) bool {
	if strings.HasPrefix(call.Blueprint, "oci://") {
		return false
	}
	_, err := os.Stat(call.Blueprint)
	return err == nil
}

// resolveCallBlueprints resolves every distinct blueprint an HCL
// document calls, through the lock and the content store, and returns
// them keyed by the call name that asked for each.
//
// Keyed by BLUEPRINT name in the lock, not by call name, because that is
// what every other path keys on and what the pulled manifest is checked
// against. Two calls to the same blueprint therefore share one lock
// entry, which is right: it is one blueprint, reached twice.
//
// Resolved once per distinct blueprint rather than once per call, so a
// document calling the same blueprint three times pulls it once.
func resolveCallBlueprints(ctx context.Context, calls []resolver.BlueprintCall) (map[string]ResolvedDep, []string, error) {
	if len(calls) == 0 {
		return nil, nil, nil
	}

	// Distinct REMOTE blueprints, in a deterministic order, with
	// same-name conflicts caught before anything is pulled. A local call
	// is resolved by invokeCall itself, unlocked and uncached, for the
	// reasons in this file's header.
	byName := map[string]Declaration{}
	var order []string
	for _, call := range calls {
		if callIsLocal(call) {
			continue
		}
		dep := callDeclaration(call)
		prev, seen := byName[dep.Name]
		if !seen {
			byName[dep.Name] = dep
			order = append(order, dep.Name)
			continue
		}
		if prev.URL != dep.URL || prev.Ref != dep.Ref || prev.Path != dep.Path {
			return nil, nil, fmt.Errorf("%s", conflictingCallSources(dep.Name, prev, dep))
		}
	}
	sort.Strings(order)

	policy := lockPolicyFrom(ctx)
	lock, err := loadLockForPolicy(policy)
	if err != nil {
		return nil, nil, err
	}

	resolvedByName := make(map[string]ResolvedDep, len(order))
	deps := make([]Declaration, 0, len(order))
	for _, name := range order {
		dep := byName[name]
		deps = append(deps, dep)

		locked, hasLock := lock.Entry(policy.Stack, dep.Name)
		expect := ""
		if hasLock && locked.Source == dep.URL && policy.Mode != LockUpdate {
			expect = locked.ContentHash
		}

		r, err := resolveOneAdoptingName(ctx, dep, expect)
		if err != nil {
			return nil, nil, fmt.Errorf("blueprint call %q (%s): %w", dep.Name, dep.URL, err)
		}
		if err := applyLockPolicyResolved(policy, lock, dep, r, hasLock, locked); err != nil {
			return nil, nil, err
		}
		// Keyed by the name the call derived, since that is what the
		// lookup below has. r.Dep.Name is the blueprint's own, which may
		// differ and is what the lock and the caller use.
		resolvedByName[dep.Name] = r
	}

	notes, err := finishLockPolicy(policy, lock, deps, nil)
	if err != nil {
		return nil, nil, err
	}

	// Re-keyed by call name, so invokeCall can look up without deriving
	// the blueprint name a second time and risking the two derivations
	// drifting apart.
	out := make(map[string]ResolvedDep, len(calls))
	for _, call := range calls {
		if callIsLocal(call) {
			continue
		}
		out[call.Name] = resolvedByName[blueprintNameFromCall(call)]
	}
	return out, notes, nil
}

// conflictingCallSources refuses two calls that name one blueprint and
// disagree about where it comes from.
//
// The lock records one content hash per blueprint name per stack, so two
// sources under one name have no representation in it. Refused rather
// than silently locking whichever resolved last, which would make the
// lock describe half the document.
//
// This is the one shape that worked before and does not now, since each
// call used to pull independently into its own temp directory. Named
// precisely for that reason.
func conflictingCallSources(name string, a, b Declaration) string {
	var s strings.Builder
	fmt.Fprintf(&s, "two blueprint calls name %q and disagree about where it comes from:\n", name)
	fmt.Fprintf(&s, "    %s\n", describeDeclarationSource(a))
	fmt.Fprintf(&s, "    %s\n", describeDeclarationSource(b))
	s.WriteString("  A blueprint's name comes from its source, and the lock records one content hash per name per\n")
	s.WriteString("  stack, so two sources under one name cannot both be recorded. Calling the same blueprint twice is\n")
	s.WriteString("  fine; calling two different ones that derive the same name is not.\n")
	s.WriteString("  Give them distinct names by publishing them under distinct paths, or call one of them from a\n")
	s.WriteString("  separate stack.")
	return s.String()
}

func describeDeclarationSource(d Declaration) string {
	out := d.URL
	if d.Ref != "" {
		out += " @ " + d.Ref
	}
	if d.Path != "" && d.Path != "." {
		out += " (path " + d.Path + ")"
	}
	return out
}

// callEvalDir returns the directory a called blueprint is evaluated
// from, and the imports its own declaration contributes, having fetched
// whatever it needs first.
//
// The same machinery the declaration path uses, reached from the other
// calling path rather than reimplemented beside it. Everything
// language-specific about a blueprint's own dependencies already lives
// in tsdeps.go, tslock.go and godeps.go.
func callEvalDir(ctx context.Context, lang, blueprintDir, name, contentHash string) (dir string, imports map[string]string, err error) {
	dir = blueprintDir
	switch lang {
	case "ts":
		own, err := tsBlueprintImports(blueprintDir, name)
		if err != nil {
			return "", nil, err
		}
		// A mirror is keyed by content hash, which a local call does not
		// have one of: it is pulled into a throwaway directory that is
		// gone by the next run. Its dependencies are materialised beside
		// it there instead, with the same lifetime as the pull.
		if own.NeedsPrefetch {
			if contentHash != "" {
				mirror, err := materializeBlueprintDeps(ctx, blueprintDir, contentHash, name)
				if err != nil {
					return "", nil, err
				}
				dir = mirror
			} else if err := installBlueprintDepsInPlace(ctx, blueprintDir, name); err != nil {
				return "", nil, err
			}
		}
		return dir, own.Imports, nil
	case "go":
		if err := checkGoBlueprintPinned(blueprintDir, name); err != nil {
			return "", nil, err
		}
		if err := prefetchGoBlueprintDeps(ctx, blueprintDir, name); err != nil {
			return "", nil, err
		}
		return dir, nil, nil
	default:
		// Python: nothing to fetch here. Its blueprints' own third-party
		// dependencies are unresolved in every calling path (UBI-265),
		// and inventing an answer only for this one would make the two
		// paths disagree about what a Python blueprint can do.
		return dir, nil, nil
	}
}

// tsCallImports builds the import-map contribution for a called
// blueprint: its own declared imports, scoped to its own directory, with
// the embedded runtime pinned inside that scope.
//
// The specifier is the blueprint's own name rather than anything the
// caller wrote, because the synthesized caller imports it by that name.
func tsCallImports(specifier, dir, entryFile string, own map[string]string) []tseval.BlueprintImport {
	return []tseval.BlueprintImport{{
		Specifier: specifier,
		EntryFile: entryFile,
		Dir:       dir,
		Imports:   own,
	}}
}
