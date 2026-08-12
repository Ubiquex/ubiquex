package blueprint

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/goeval"
	"github.com/ubiquex/ubiquex/pyeval"
	"github.com/ubiquex/ubiquex/tseval"
)

// ExpandCalls expands every entry in intent.BlueprintCalls into real
// resources, appending them to intent.Resources and clearing
// BlueprintCalls -- the shared mechanism diagram/parse.go's own
// ubx_blueprint node and intentprovider's own md "Use blueprint X
// with..." recognition BOTH route through (UBI-74 Slice 5's own "three
// different SOURCES, one resolved-delta mechanism" design). Does
// nothing if intent.BlueprintCalls is already empty -- every OTHER
// intent/v1 producer (a hand-written file, an SDK program's own
// evaluated output) never sets it in the first place.
//
// Each call is invoked by literally RUNNING the target blueprint's own
// compiled function -- the SAME goeval/tseval/pyeval machinery `ubx
// resolve --from-code` already runs for a hand-written SDK program
// (UBI-74 Slice 2's own real invocation mechanism), never a second,
// parallel way to execute a blueprint. A synthesized, throwaway calling
// program (never the blueprint's own source -- always an ephemeral
// local copy, see invokeCall) imports the target function and calls it
// inside a real stack()/intent()/evaluate() cycle using the CALLING
// intent's own stack name, so every address/embedded-ref the call
// produces threads the real calling stack through exactly like a
// hand-written SDK program already does (docs/blueprint.md's own
// "Cross-medium calling" section has the full design).
func ExpandCalls(ctx context.Context, intent *resolver.IntentFile) error {
	if len(intent.BlueprintCalls) == 0 {
		return nil
	}

	seen := make(map[string]string, len(intent.Resources))
	for _, ri := range intent.Resources {
		seen[ri.Type+"."+ri.Name] = "resources[]"
	}

	// UBI-128: aggregated across every call in this document, keyed
	// "<CallName>:<outputKey>" -- rewriteBlueprintOutputRefs (outputs.go)
	// consumes this ONCE, below, after every call has been expanded (a
	// PROVISIONAL output $ref can legitimately be embedded in ANY
	// resource's own config, not only ones the SAME call produced).
	outputAddr := map[string]string{}

	for _, call := range intent.BlueprintCalls {
		resources, callOutputs, err := invokeCall(ctx, intent.Stack, call)
		if err != nil {
			return fmt.Errorf("blueprint call %q (%s): %w", call.Name, call.Blueprint, err)
		}
		for _, ri := range resources {
			addr := ri.Type + "." + ri.Name
			if owner, dup := seen[addr]; dup {
				return fmt.Errorf("blueprint call %q (%s): resource %s collides with %s -- rename one", call.Name, call.Blueprint, addr, owner)
			}
			seen[addr] = fmt.Sprintf("blueprint call %q", call.Name)
			intent.Resources = append(intent.Resources, ri)
		}
		if call.CallName != "" {
			for outputKey, real := range callOutputs {
				outputAddr[call.CallName+":"+outputKey] = real
			}
		}
	}
	intent.BlueprintCalls = nil

	if err := rewriteBlueprintOutputRefs(intent, outputAddr); err != nil {
		return fmt.Errorf("blueprint output reference: %w", err)
	}
	return nil
}

// callLanguagePreference is the fixed order ExpandCalls tries a built
// blueprint's own sibling language directories in -- TypeScript first
// (tseval needs no manifest/module-resolution setup at all beyond @ubx/sdk's
// own embedded import map, the most robust, zero-configuration path of
// the three), then Python (pyeval's own WASI sandbox needs the calling
// driver copied alongside the blueprint's own py/ files, but otherwise
// no real module-resolution fragility), then Go last (goeval's own
// GOPROXY=off means resolving github.com/ubiquex/ubx-sdk-go's own real
// v0.0.0 version -- confirmed live, a real resolvable version, not a
// placeholder needing a replace -- only succeeds without ANY network
// access if that exact version is already in the local Go module
// cache; true on any machine that has ever built or run a real Go SDK
// program against it before, per this project's own standing
// verification discipline, but a real, named reason to prefer the
// other two when available, not an arbitrary ordering). All three
// generators produce equivalent resolved deltas (UBI-74 Slice 4's own
// live-verified guarantee), so which one actually executes a given call
// is an implementation detail, never something a caller needs to
// control.
var callLanguagePreference = []string{"ts", "py", "go"}

// pickCallLanguage returns the first language in callLanguagePreference
// whose own built sibling directory exists under dir.
func pickCallLanguage(dir string) (string, error) {
	for _, lang := range callLanguagePreference {
		if info, err := os.Stat(filepath.Join(dir, lang)); err == nil && info.IsDir() {
			return lang, nil
		}
	}
	return "", fmt.Errorf("%s has no built go/ts/py package -- run `ubx blueprint build` first", dir)
}

// resolvedArg is one declared param, paired with the call's own raw
// string value (Args is always string-valued regardless of the param's
// real type -- IntentFile.BlueprintCall's own doc comment has the full
// account of why) -- Given is false for a params: default entry the
// call simply didn't override.
type resolvedArg struct {
	Param Param
	Raw   string
	Given bool
}

// resolveCallArgs pairs blueprint's own declared params (in their
// Ubxfile declaration order) with call's own given values, hard-failing
// on a missing REQUIRED param -- never silently defaulted, matching
// this codebase's own "hard error, never silent" posture for every
// other missing-required-value case.
func resolveCallArgs(params []Param, given map[string]string) ([]resolvedArg, error) {
	out := make([]resolvedArg, 0, len(params))
	for _, p := range params {
		raw, ok := given[p.Name]
		if !ok && p.Required {
			return nil, fmt.Errorf("missing required param %q", p.Name)
		}
		out = append(out, resolvedArg{Param: p, Raw: raw, Given: ok})
	}
	return out, nil
}

// parseCrossRefArg parses a ParamCrossRef call-site argument's own raw
// value, "@<stack>.<type>.<name>.<attr-path>" -- diagram/crossref.go's
// own "@" grammar for a reference node's label (parseCrossRefLabel),
// extended one segment further: an explicit attribute path is always
// required here, unlike a diagram reference node's own topology-only
// "@<stack>.<type>.<name>" form (that path never produces a real $cross
// marker at all -- diagram/parse.go's own "Cross-stack edges" note). A
// ParamCrossRef value is always embedded directly into a resource's own
// config as one concrete attribute's value, and core/resolver/refs.go's
// own splitRefTarget already hard-requires exactly this 4-part shape
// for $cross's "to" field -- there is no real "the whole resource,
// no attribute" shape $cross could ever resolve to, so this is a real
// constraint already enforced downstream, surfaced here as a real,
// named parse error instead of an opaque resolve-time failure later.
func parseCrossRefArg(raw string) (stackName, to string, err error) {
	if !strings.HasPrefix(raw, "@") {
		return "", "", fmt.Errorf("%q must start with \"@\" -- expected \"@<stack>.<type>.<name>.<attr-path>\"", raw)
	}
	addr := strings.TrimPrefix(raw, "@")
	parts := strings.SplitN(addr, ".", 4)
	if len(parts) != 4 || parts[0] == "" || parts[1] == "" || parts[2] == "" || parts[3] == "" {
		return "", "", fmt.Errorf("%q is not a well-formed \"@<stack>.<type>.<name>.<attr-path>\" reference", raw)
	}
	return parts[0], addr, nil
}

// renderArgLiteral renders one resolved arg's own raw string value into
// lang's own literal syntax for p.Type -- string/number/bool coercion
// against the target blueprint's own declared param type, deferred
// entirely to this point (neither the diagram medium nor the md medium
// has this type information available when the call is first
// extracted -- see resolver.BlueprintCall's own doc comment).
//
// UBI-129: a list-typed param's own raw value is the SAME kind of plain
// string every other param's Args entry already is -- diagram/parse.go's
// own generic "every non-reserved child attribute is one Arg, verbatim
// raw text" capture and the md medium's own identical Args convention
// both need ZERO changes to carry a comma-separated list value (e.g.
// "eu-central-1a, eu-central-1b, eu-central-1c") -- this is the ONE
// place that raw text is actually split and rendered as a real
// language-native list literal, splitListArg below.
//
// UBI-134: a ParamCrossRef param's own raw value is, identically, the
// SAME plain "@<stack>.<type>.<name>.<attr-path>" string every medium's
// Args capture already carries verbatim -- rendered here as a real
// sdk.CrossStack(...)/crossStack(...)/sdk.cross_stack(...) construction
// (matching each language's own real, already-exported $cross
// constructor -- sdk/go/runtime, sdk/ts/runtime, sdk/py -- never a raw
// map/dict literal reimplementing the wire shape by hand a fourth time),
// never treated as an ordinary string literal.
func renderArgLiteral(lang string, p Param, raw string) (string, error) {
	switch p.Type {
	case ParamCrossRef:
		stackName, to, err := parseCrossRefArg(raw)
		if err != nil {
			return "", fmt.Errorf("param %q: %w", p.Name, err)
		}
		switch lang {
		case "go":
			return fmt.Sprintf("sdk.CrossStack(%s, %s)", strconv.Quote(stackName), strconv.Quote(to)), nil
		case "py":
			return fmt.Sprintf("sdk.cross_stack(%s, %s)", jsonStringLiteral(stackName), jsonStringLiteral(to)), nil
		default: // "ts"
			return fmt.Sprintf("crossStack(%s, %s)", jsonStringLiteral(stackName), jsonStringLiteral(to)), nil
		}
	case ParamString:
		if lang == "go" {
			return fmt.Sprintf("%q", raw), nil
		}
		return jsonStringLiteral(raw), nil
	case ParamNumber:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return "", fmt.Errorf("param %q: %q is not a valid number: %w", p.Name, raw, err)
		}
		return numberLiteral(f), nil
	case ParamBool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return "", fmt.Errorf("param %q: %q is not a valid bool: %w", p.Name, raw, err)
		}
		if lang == "py" {
			if b {
				return "True", nil
			}
			return "False", nil
		}
		return strconv.FormatBool(b), nil
	case ParamListString:
		items := splitListArg(raw)
		rendered := make([]string, len(items))
		for i, it := range items {
			if lang == "go" {
				rendered[i] = fmt.Sprintf("%q", it)
			} else {
				rendered[i] = jsonStringLiteral(it)
			}
		}
		if lang == "go" {
			return fmt.Sprintf("[]string{%s}", strings.Join(rendered, ", ")), nil
		}
		return fmt.Sprintf("[%s]", strings.Join(rendered, ", ")), nil
	case ParamListNumber:
		items := splitListArg(raw)
		rendered := make([]string, len(items))
		for i, it := range items {
			f, err := strconv.ParseFloat(it, 64)
			if err != nil {
				return "", fmt.Errorf("param %q: element %q is not a valid number: %w", p.Name, it, err)
			}
			rendered[i] = numberLiteral(f)
		}
		if lang == "go" {
			return fmt.Sprintf("[]int{%s}", strings.Join(rendered, ", ")), nil
		}
		return fmt.Sprintf("[%s]", strings.Join(rendered, ", ")), nil
	default:
		return "", fmt.Errorf("param %q: unrecognized type %q", p.Name, p.Type)
	}
}

// splitListArg splits a list-typed param's own raw comma-separated call-
// site value (the SAME plain string diagram/md both already capture
// verbatim, e.g. "eu-central-1a, eu-central-1b, eu-central-1c") into its
// own trimmed elements -- tolerant of the author writing "a,b,c" or
// "a, b, c" interchangeably. An empty/whitespace-only raw value yields
// an empty (never nil-vs-empty-ambiguous) slice, rendering as a real,
// legal empty list literal in every language.
func splitListArg(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}
	return out
}

// argLiteralOrDefault renders a's own given value if the call provided
// one, or (for a non-required param the call left unset) the SAME
// already-proven default-literal renderer each language's own codegen
// uses (defaultLiteral/tsDefaultLiteral/pyDefaultLiteral -- gogen.go/
// tsgen.go/pygen.go) -- reused directly here rather than reimplemented a
// fourth time.
func argLiteralOrDefault(lang string, a resolvedArg) (string, error) {
	if a.Given {
		return renderArgLiteral(lang, a.Param, a.Raw)
	}
	switch lang {
	case "go":
		return defaultLiteral(a.Param)
	case "ts":
		return tsDefaultLiteral(a.Param)
	case "py":
		return pyDefaultLiteral(a.Param)
	default:
		return "", fmt.Errorf("unrecognized language %q", lang)
	}
}

// blueprintNameFromCall derives the blueprint's own build-time directory
// basename from a BlueprintCall -- needed because a call site only ever
// knows a REFERENCE (a path, or a git URL+path), never the exact name
// `ubx blueprint build` itself used to derive the exported function/
// package identifiers already baked into the already-built go/ts/py
// output. A real, named limitation: this is a best-effort convention
// match (the git repo/subdirectory's own basename, or the local path's
// own basename), not something ubx independently verifies against the
// blueprint's own build-time record -- a blueprint whose own directory
// was renamed after building would derive the wrong identifier here and
// fail with a clear "function not found"-shaped error at invocation
// time, not a silent wrong call. Slice 6's own provenance tagging is a
// natural place to close this gap for real (recording the blueprint's
// own build-time name in blueprint.lock.json), not attempted here.
func blueprintNameFromCall(call resolver.BlueprintCall) string {
	if call.Path != "" && call.Path != "." {
		return filepath.Base(call.Path)
	}
	trimmed := strings.TrimSuffix(strings.TrimSuffix(call.Blueprint, "/"), ".git")
	return filepath.Base(trimmed)
}

// invokeCall resolves, invokes, and returns the resources one
// BlueprintCall produces, plus (UBI-128) that call's own declared
// outputs, resolved into real addresses (resolveCallOutputs, outputs.go)
// now that the blueprint's real resource types/names are finally known.
// The blueprint is ALWAYS pulled into a fresh, ephemeral temp directory
// first (Pull, UBI-74 Slice 3's own mechanism, reused unconditionally
// for both local and git references) and that whole temp directory is
// removed before this function returns -- invoking a blueprint never
// mutates its own source, local or git, under any circumstance.
func invokeCall(ctx context.Context, callingStack string, call resolver.BlueprintCall) (resources []resolver.ResourceIntent, outputAddr map[string]string, err error) {
	scratch, err := os.MkdirTemp("", "ubx-blueprint-call-*")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(scratch)

	blueprintName := blueprintNameFromCall(call)
	dest := filepath.Join(scratch, blueprintName)
	if _, err := Pull(ctx, call.Blueprint, dest, call.Ref, call.Path); err != nil {
		return nil, nil, fmt.Errorf("pull: %w", err)
	}

	ubxfile, err := ParseUbxfile(dest)
	if err != nil {
		return nil, nil, fmt.Errorf("parse Ubxfile: %w", err)
	}

	// UBI-74 Slice 6: the provenance ref every resource this call
	// produces gets stamped with, below -- "<name>:<content_hash>",
	// reusing buildManifest (manifest.go, Slice 3's own `package`/
	// `verify` mechanism) rather than requiring the blueprint to have
	// already been explicitly `package`d (blueprint.lock.json need not
	// exist at all -- computed fresh over the pulled copy's own current
	// files every time, so this always works, not just for a packaged
	// blueprint). The FULL "sha256:<hex>" form is stored here, matching
	// every other content_hash in this codebase -- truncation to a
	// short, readable form is a presentation concern for `ubx why`/`ubx
	// render`, never baked into the stored ref itself.
	manifest, err := buildManifest(dest, blueprintName)
	if err != nil {
		return nil, nil, fmt.Errorf("compute blueprint content hash: %w", err)
	}
	blueprintRef := blueprintName + ":" + manifest.ContentHash

	lang, err := pickCallLanguage(dest)
	if err != nil {
		return nil, nil, err
	}

	args, err := resolveCallArgs(ubxfile.Params, call.Args)
	if err != nil {
		return nil, nil, err
	}

	summary := fmt.Sprintf("blueprint call: %s", call.Name)

	var entry string
	switch lang {
	case "ts":
		entry, err = writeTSCaller(scratch, dest, blueprintName, callingStack, summary, args)
	case "py":
		entry, err = writePyCaller(scratch, dest, blueprintName, callingStack, summary, args)
	case "go":
		entry, err = writeGoCaller(scratch, dest, blueprintName, callingStack, summary, args)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("prepare %s caller: %w", lang, err)
	}

	var raw []byte
	switch lang {
	case "ts":
		raw, err = tseval.Evaluate(ctx, entry)
	case "py":
		raw, err = pyeval.Evaluate(ctx, entry)
	case "go":
		raw, err = goeval.Evaluate(ctx, entry)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("evaluate (%s): %w", lang, err)
	}

	var result resolver.IntentFile
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, nil, fmt.Errorf("parse evaluated intent: %w", err)
	}

	// UBI-74 Slice 6: every resource THIS call produces is stamped with
	// the SAME blueprint ref -- identically regardless of which medium
	// (SDK/diagram/md) or which language (Go/TS/Python) actually
	// executed it, since blueprintRef describes the blueprint itself
	// (name + content hash), never the calling mechanism.
	//
	// UBI-126: for a Go call specifically, the evaluated program's own
	// resources may ALREADY carry a "blueprint"-kind source -- generated
	// code now calls sdk.PushBlueprintSource(rawName) around its own
	// body (sdk/go/runtime.go) so a DIRECT-import call gets provenance
	// too, and writeGoCaller's own synthesized program is just another
	// caller of that SAME generated code, so it fires here as well.
	// THAT tag only ever carries the blueprint's own bare, unsanitized
	// name (never a real content hash -- the compiled binary has no way
	// to compute its own on-disk directory's hash; see sdkprovenance.go's
	// own doc comment). This function already computed the REAL,
	// complete blueprintRef (name + content hash) externally, before
	// ever running the caller -- strictly more authoritative than
	// anything the evaluated program could produce -- so any existing
	// "blueprint"-kind entry is REPLACED here, never appended alongside
	// (a stray incomplete duplicate would otherwise shadow the real one
	// wherever a renderer reads sources[0], e.g. `ubx render`'s own
	// container label). A non-blueprint source (shouldn't exist today,
	// no evaluator emits one) is left untouched, matching
	// core.IntentSource's own established "multiple kinds coexist"
	// posture.
	for i := range result.Resources {
		kept := result.Resources[i].Sources[:0]
		for _, s := range result.Resources[i].Sources {
			if s.Kind != "blueprint" {
				kept = append(kept, s)
			}
		}
		result.Resources[i].Sources = append(kept, core.IntentSource{
			Kind: "blueprint",
			Ref:  blueprintRef,
		})
	}

	callOutputs, err := resolveCallOutputs(callingStack, ubxfile, result.Resources)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve outputs: %w", err)
	}
	return result.Resources, callOutputs, nil
}

// writeTSCaller writes a throwaway TypeScript entry program into
// scratch that imports blueprintDir's own already-built ts/<pkg>.ts
// function (via its own absolute path -- Deno resolves an entry file's
// relative imports relative to the entry file itself, never the runner
// script that imports IT, so the blueprint's own ts/ directory never
// needs to be copied at all, unlike Python's own WASI sandbox
// constraint below) and calls it with every declared param, in
// requiredFirstOrder (ubxfile.go), using EVERY param's own resolved
// value (a given override, or its own declared default rendered as a
// literal) -- TypeScript function calls are positional-only, no native
// keyword-argument syntax, so a later param can't be overridden while
// an earlier defaulted one is silently skipped; passing all of them
// explicitly sidesteps that entirely.
//
// UBI-149: args arrives in raw Ubxfile declaration order
// (resolveCallArgs), but the generated function's own real signature
// (renderTSFunction, tsgen.go) is required-first, defaulted-trailing --
// TypeScript itself refuses to compile a required param after a
// defaulted one, so the signature can't stay in declaration order when
// the two are interleaved. Reordering args here, via the EXACT same
// requiredFirstOrder the signature itself uses, is what makes this
// positional call line up with that signature again -- previously this
// function passed args in raw declaration order regardless, silently
// threading values into the wrong slots whenever a required param
// followed a defaulted one (a real, reproduced bug: a retention_days
// value landed in a later vpc_id slot).
func writeTSCaller(scratch, blueprintDir, blueprintName, stackName, summary string, args []resolvedArg) (string, error) {
	pkgName, err := packageIdent(blueprintName)
	if err != nil {
		return "", err
	}
	funcName, err := camelCase(blueprintName)
	if err != nil {
		return "", err
	}

	tsFile, err := filepath.Abs(filepath.Join(blueprintDir, "ts", pkgName+".ts"))
	if err != nil {
		return "", err
	}

	byName := make(map[string]resolvedArg, len(args))
	params := make([]Param, 0, len(args))
	for _, a := range args {
		byName[a.Param.Name] = a
		params = append(params, a.Param)
	}
	orderedArgs := make([]resolvedArg, 0, len(args))
	for _, p := range requiredFirstOrder(params) {
		orderedArgs = append(orderedArgs, byName[p.Name])
	}

	argExprs := make([]string, 0, len(orderedArgs))
	needsCrossStack := false
	for _, a := range orderedArgs {
		expr, err := argLiteralOrDefault("ts", a)
		if err != nil {
			return "", err
		}
		argExprs = append(argExprs, expr)
		if a.Param.Type == ParamCrossRef {
			needsCrossStack = true
		}
	}

	// UBI-134: crossStack imported ONLY when at least one arg actually
	// needs it -- every existing blueprint call (zero ParamCrossRef
	// params) keeps generating the byte-identical import line it always
	// has, no unused-import Deno diagnostic introduced for the common
	// case.
	sdkImports := "intent, stack"
	if needsCrossStack {
		sdkImports = "crossStack, intent, stack"
	}

	src := fmt.Sprintf(`import { %s } from "@ubx/sdk";
import { %s } from %s;

export default stack(%s, () => {
  intent({ summary: %s });
  %s(%s);
});
`, sdkImports, funcName, jsonStringLiteral(tsFile), jsonStringLiteral(stackName), jsonStringLiteral(summary), funcName, strings.Join(argExprs, ", "))

	entry := filepath.Join(scratch, "caller.ts")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		return "", err
	}
	return entry, nil
}

// writePyCaller writes a throwaway Python entry program alongside a
// COPY of blueprintDir's own py/ package (never the original -- pyeval's
// own WASI sandbox mounts only the entry file's own directory, so the
// driver must sit next to bindings.py/<pkg>.py for `from <pkg> import
// ...` to resolve at all; copying protects the original from ever
// being mutated by this throwaway file, the same "always a copy, never
// the source" discipline goeval's own buildProgram already applies for
// Go). Uses Python's own native keyword arguments for every param that
// EITHER is required OR was explicitly overridden -- sidesteps the
// positional-ordering question TS's own writer has to solve differently,
// since Python (unlike TS) supports keyword arguments for any parameter
// regardless of its own default status.
func writePyCaller(scratch, blueprintDir, blueprintName, stackName, summary string, args []resolvedArg) (string, error) {
	pkgName, err := packageIdent(blueprintName)
	if err != nil {
		return "", err
	}
	funcName, err := pythonIdentifier(blueprintName)
	if err != nil {
		return "", err
	}

	pyScratch := filepath.Join(scratch, "py")
	if err := copyDir(filepath.Join(blueprintDir, "py"), pyScratch); err != nil {
		return "", fmt.Errorf("copy blueprint py/ package: %w", err)
	}

	argParts := make([]string, 0, len(args))
	for _, a := range args {
		if !a.Given {
			continue // a native default argument -- omit entirely, Python fills it in itself
		}
		cname, err := pythonIdentifier(a.Param.Name)
		if err != nil {
			return "", err
		}
		lit, err := renderArgLiteral("py", a.Param, a.Raw)
		if err != nil {
			return "", err
		}
		argParts = append(argParts, fmt.Sprintf("%s=%s", cname, lit))
	}

	src := fmt.Sprintf(`import ubx_sdk as sdk
from %s import %s


def describe():
    sdk.intent(%s)
    %s(%s)


if __name__ == "__main__":
    sdk.run(%s, describe)
`, pkgName, funcName, jsonStringLiteral(summary), funcName, strings.Join(argParts, ", "), jsonStringLiteral(stackName))

	entry := filepath.Join(pyScratch, "_ubx_call_driver.py")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		return "", err
	}
	return entry, nil
}

// writeGoCaller writes a throwaway Go calling program into scratch,
// requiring+replacing blueprintDir's own already-built go/ package by
// local path (goeval.buildProgram itself copies this ENTIRE module
// before building it, per its own doc comment -- "build from a
// throwaway copy... never the program author's own real directory" --
// so the blueprint's own go/ directory this replace points at is never
// touched either). Reuses whatever version the blueprint's own already-
// built go/go.mod declares for github.com/ubiquex/ubx-sdk-go, verbatim
// -- see this package's own doc comment on callLanguagePreference for
// the real, named condition this needs (the module already present in
// the local Go module cache, since goeval's own GOPROXY=off blocks a
// fresh network fetch). Required params stay positional
// (Go's own calling convention, unchanged since Slice 1); a
// params: default entry becomes a `With<Param>(...)` option, included
// ONLY when the call actually overrides it -- omitting an un-overridden
// one is correct and sufficient here, since Go's own compiled options
// struct already seeds the Ubxfile's own declared default.
//
// UBI-149: args is reordered via requiredFirstOrder (ubxfile.go) BEFORE
// this loop runs, for the same real reason writeTSCaller needs it --
// renderGoFunction's own generated signature is every required
// positional argument FIRST, contiguous, THEN the trailing "opts
// ...Option" (gogen.go), never interleaved with declaration order.
// Previously this looped over args in raw Ubxfile declaration order, so
// a params: default entry declared BETWEEN two required params produced
// a real Go compile error: a With<Param>(...) call landing positionally
// where the second required argument belongs, and vice versa -- the
// exact same root cause as writeTSCaller's own bug, just caught by the
// Go compiler instead of silently threading the wrong runtime value.
func writeGoCaller(scratch, blueprintDir, blueprintName, stackName, summary string, args []resolvedArg) (string, error) {
	pkgName, err := packageIdent(blueprintName)
	if err != nil {
		return "", err
	}
	funcName, err := pascalCase(blueprintName)
	if err != nil {
		return "", err
	}

	blueprintGoDir, err := filepath.Abs(filepath.Join(blueprintDir, "go"))
	if err != nil {
		return "", err
	}
	blueprintGoMod, err := os.ReadFile(filepath.Join(blueprintGoDir, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read blueprint go.mod: %w", err)
	}
	sdkGoRequire, err := extractRequireLine(string(blueprintGoMod), "github.com/ubiquex/ubx-sdk-go")
	if err != nil {
		return "", err
	}

	byName := make(map[string]resolvedArg, len(args))
	params := make([]Param, 0, len(args))
	for _, a := range args {
		byName[a.Param.Name] = a
		params = append(params, a.Param)
	}
	orderedArgs := make([]resolvedArg, 0, len(args))
	for _, p := range requiredFirstOrder(params) {
		orderedArgs = append(orderedArgs, byName[p.Name])
	}

	var callArgs []string
	for _, a := range orderedArgs {
		if a.Param.Required {
			lit, err := renderArgLiteral("go", a.Param, a.Raw)
			if err != nil {
				return "", err
			}
			callArgs = append(callArgs, lit)
			continue
		}
		if !a.Given {
			continue
		}
		wname, err := pascalCase(a.Param.Name)
		if err != nil {
			return "", err
		}
		lit, err := renderArgLiteral("go", a.Param, a.Raw)
		if err != nil {
			return "", err
		}
		callArgs = append(callArgs, fmt.Sprintf("%s.With%s(%s)", pkgName, wname, lit))
	}

	entryDir := filepath.Join(scratch, "callerprog")
	if err := os.MkdirAll(entryDir, 0o755); err != nil {
		return "", err
	}

	// sdkGoReplace mirrors sdkGoRequire's own "reuse whatever the
	// blueprint's own go.mod already declares, verbatim" reasoning,
	// extended to an OPTIONAL replace directive: a real, published
	// blueprint's own go.mod never has one (Slice 1's own established
	// convention -- v0.0.0 resolves from the real module proxy/cache,
	// no override needed), so this stays "" and the synthesized caller's
	// own behavior is completely unchanged from before this line existed.
	// A hermetically-tested blueprint fixture that DOES carry a local
	// `replace github.com/ubiquex/ubx-sdk-go => ...` (every real test
	// fixture in this codebase that builds a callable blueprint already
	// adds one, so its own package compiles against local sdk/go
	// unpublished changes) needs the synthesized caller to resolve the
	// IDENTICAL sdk-go copy, not silently fall back to a stale published
	// version -- without this, any real sdk/go runtime change (UBI-126's
	// own new PushBlueprintSource/PopBlueprintSource, say) would compile
	// fine against a direct-import caller (which already carries its own
	// local replace) but fail this synthesized-caller path specifically,
	// with a confusing "undefined: sdk.X" error pointing at generated
	// code that never changed.
	sdkGoReplace := extractReplaceLine(string(blueprintGoMod), "github.com/ubiquex/ubx-sdk-go")
	if sdkGoReplace != "" {
		sdkGoReplace += "\n"
	}

	goMod := fmt.Sprintf("module ubx-blueprint-caller\n\ngo 1.23\n\n%s\nrequire %s v0.0.0\n\nreplace %s => %s\n%s",
		sdkGoRequire, pkgName, pkgName, blueprintGoDir, sdkGoReplace)
	if err := os.WriteFile(filepath.Join(entryDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		return "", err
	}

	src := fmt.Sprintf(`package main

import (
	%s %s
	sdk "github.com/ubiquex/ubx-sdk-go/runtime"
)

func main() {
	sdk.Main(sdk.Stack(%s, func() {
		sdk.Intent(sdk.IntentInfo{Summary: %s})
		%s.%s(%s)
	}))
}
`, pkgName, strconv.Quote(pkgName), strconv.Quote(stackName), strconv.Quote(summary), pkgName, funcName, strings.Join(callArgs, ", "))

	entry := filepath.Join(entryDir, "main.go")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		return "", err
	}
	return entry, nil
}

// extractRequireLine returns the single top-level "require <modulePath>
// <version>" line from goMod's own text, verbatim -- goeval's own doc
// comment names this as the ordinary, idiomatic way any Go program
// declares its ubx-sdk-go dependency; GenerateGo's own emitted go.mod
// (files["go/go.mod"]) only ever has exactly one require, never a
// parenthesized multi-require block, so a single-line scan suffices.
func extractRequireLine(goMod, modulePath string) (string, error) {
	prefix := "require " + modulePath + " "
	for _, line := range strings.Split(goMod, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) {
			return trimmed, nil
		}
	}
	return "", fmt.Errorf("go.mod has no require line for %s", modulePath)
}

// extractReplaceLine is extractRequireLine's own optional sibling: a
// "replace <modulePath> => ..." line, verbatim, or "" if goMod has none
// -- a real, published blueprint's own go.mod never does (see
// writeGoCaller's own doc comment on sdkGoReplace for why this matters
// only for hermetically-tested fixtures that DO carry one), so unlike
// extractRequireLine, absence is never an error here.
func extractReplaceLine(goMod, modulePath string) string {
	prefix := "replace " + modulePath + " "
	for _, line := range strings.Split(goMod, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) {
			return trimmed
		}
	}
	return ""
}
