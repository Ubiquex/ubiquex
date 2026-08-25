// Package ts is the TypeScript template half of docs/sdk.md's own
// "Codegen design": ir.ResourceType (language-neutral, wire-name-only) ->
// idiomatic TypeScript bindings. This is the only package in
// sdk/codegen that knows what "idiomatic TypeScript" means -- camelCase
// property names, interface declarations, a runtime field-map descriptor
// -- none of which leaks back into sdk/codegen/ir itself.
//
// UBI-98's own per-provider-repo/per-service-package restructure,
// applied to TS in the session AFTER Go's own (STATE.md): GeneratedFile
// (one flat file for a whole provider) is replaced by
// ResourceFile/PackageDoc/GeneratedRepo (one file per resource type, one
// doc.ts per service directory). A real, load-bearing difference from Go
// found and verified this session, not assumed: ES modules give every
// FILE its own independent scope, so (unlike Go, where UBI-96's original
// cross-resource collision bug required the package-wide "_" join fix to
// avoid a hard `go build` redeclaration) two DIFFERENT resource types'
// own declarations can NEVER collide with each other here, regardless of
// directory or naming, the moment each type gets its own file -- this
// restructure closes that whole class of risk for TS as a structural side
// effect of moving to per-type files, not because of anything specific to
// the per-SERVICE-directory grouping itself (which is here for
// reviewability/the founder's own Pulumi-parity naming goal, matching
// Go's `ecr.Repository`, not for a compile-correctness reason the way
// Go's package split was).
package ts

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ubiquex/ubiquex/sdk/codegen/ir"
)

// GeneratedRepo renders every file for one provider's repo-shaped
// TypeScript output tree: a package.json stub (name "@ubx/sdk-<shortName>",
// the real, already-published package name, unchanged by UBI-138 --
// only its source location within the combined repo moves), one doc.ts
// per derived service directory (PackageDoc), and one <local>.ts per
// resource type (ResourceFile) inside its own service directory. Returns
// a map of path, relative to the repo root using "/" always (never the
// host OS separator) -> file content -- mirrors
// sdk/codegen/templates/go's own GeneratedRepo exactly in shape.
//
// UBI-138: every returned path is now rooted under "sdk/typescript/" --
// the real Pulumi-precedent sibling-subdirectory structure (see
// sdk/codegen/templates/go's own GeneratedRepo doc comment for the full
// account). Unlike Go, this is NOT a new module/package boundary in any
// TypeScript-specific sense (`deno.json`'s own import map -- hand/
// tooling-maintained, never this template's own output -- is what
// actually anchors "sdk/typescript/" as its own publishable root at
// publish time); package.json's own "name"/"version" fields are
// untouched by this move, only the file's own location is.
func GeneratedRepo(shortName, source, version string, types []*ir.ResourceType) (map[string]string, error) {
	sorted := make([]*ir.ResourceType, len(types))
	copy(sorted, types)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].WireType < sorted[j].WireType })

	type entry struct {
		rt    *ir.ResourceType
		local string
	}
	// pkgKey mirrors sdk/codegen/templates/go's own identical fix
	// (UBI-178 piece 4): namespace is "" for an ordinary resource, "data"
	// for a data source (ir.ServiceAndLocalNameForType's own doc
	// comment), keyed separately from service so a resource and its
	// same-named data source counterpart never land at the same path.
	type pkgKey struct{ namespace, service string }
	byService := map[pkgKey][]entry{}
	var pkgKeys []pkgKey
	for _, rt := range sorted {
		namespace, service, local, err := ir.ServiceAndLocalNameForType(rt)
		if err != nil {
			return nil, fmt.Errorf("sdk/codegen/templates/ts: %w", err)
		}
		key := pkgKey{namespace, service}
		if _, ok := byService[key]; !ok {
			pkgKeys = append(pkgKeys, key)
		}
		byService[key] = append(byService[key], entry{rt, local})
	}
	sort.Slice(pkgKeys, func(i, j int) bool {
		if pkgKeys[i].namespace != pkgKeys[j].namespace {
			return pkgKeys[i].namespace < pkgKeys[j].namespace
		}
		return pkgKeys[i].service < pkgKeys[j].service
	})

	files := map[string]string{
		"sdk/typescript/package.json": packageJSON(shortName, source, version),
	}
	// UBI-106: every service directory nests under shortName/ (e.g.
	// aws/iam/, never iam/ directly under sdk/typescript/) -- fixes a real
	// repo-browsing problem (200+ service directories at hashicorp/aws's
	// own repo root sorted ahead of README.md by GitHub's file browser).
	// package.json stays at sdk/typescript/'s own true root. ES module
	// resolution is purely path-based (confirmed live, UBI-98 session 2)
	// -- no identifier-validity guard needed for shortName as a path
	// segment, unlike Python's own dotted-import requirement below.
	for _, key := range pkgKeys {
		// dir is shortName/service for an ordinary resource, unchanged
		// from before UBI-178, and shortName/data/service for a data
		// source -- see sdk/codegen/templates/go's own identical dir
		// construction for the full account.
		dir := shortName
		if key.namespace != "" {
			dir += "/" + key.namespace
		}
		dir += "/" + key.service
		files["sdk/typescript/"+dir+"/doc.ts"] = PackageDoc(source, version)
		for _, e := range byService[key] {
			content, err := ResourceFile(e.local, e.rt)
			if err != nil {
				return nil, fmt.Errorf("sdk/codegen/templates/ts: %s: %w", e.rt.WireType, err)
			}
			path := "sdk/typescript/" + dir + "/" + ir.FileStem(e.local) + ".ts"
			if _, exists := files[path]; exists {
				// ir.FileStem's own hash-suffix collision odds are
				// astronomically low, never trusted alone -- a real path
				// collision here fails the generation outright rather
				// than silently letting one resource's file clobber
				// another's (the same `files["path"] = content` map
				// write that would otherwise just overwrite in place).
				return nil, fmt.Errorf("sdk/codegen/templates/ts: %s: generated path %q collides with an earlier resource type in this service", e.rt.WireType, path)
			}
			files[path] = content
		}
	}
	return files, nil
}

// packageJSON renders the repo-shaped tree's own manifest stub -- a real,
// consumer-ready `npm install`able shape (were it ever published; it
// isn't, matching @ubx/sdk's own "not yet published" posture, docs/sdk.md),
// not consulted by anything this codebase's own hermetic evaluator or
// `deno check` verification actually reads (Deno resolves "@ubx/sdk" via
// its own import map, never package.json/node_modules resolution).
func packageJSON(shortName, source, version string) string {
	return fmt.Sprintf(`{
  "name": "@ubx/sdk-%s",
  "version": "0.0.0",
  "description": "Generated TypeScript bindings for %s@%s (ubx sdk gen). DO NOT EDIT.",
  "type": "module",
  "dependencies": {
    "@ubx/sdk": "0.0.0"
  }
}
`, shortName, source, version)
}

// PackageDoc renders one service directory's shared doc.ts: the
// "generated, do not edit, committed to git" banner (docs/sdk.md's own
// "ubx sdk gen: local, pinned, offline-after-generation") plus
// __ubxSourceProvenance -- declared exactly once per directory, here,
// rather than repeated (or, worse, redeclared -- a real `SyntaxError:
// Identifier '...' has already been declared` if two files in the same
// directory each tried) in every resource type's own file.
func PackageDoc(source, version string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// Code generated by `ubx sdk gen` from %s@%s. DO NOT EDIT.\n", source, version)
	b.WriteString("// This file is committed to git like any other reviewable generated code\n")
	b.WriteString("// (docs/sdk.md — \"ubx sdk gen: local, pinned, offline-after-generation\") --\n")
	b.WriteString("// re-run `ubx sdk gen` to regenerate after a provider version bump.\n")
	fmt.Fprintf(&b, "export const __ubxSourceProvenance = { source: %q, version: %q } as const;\n", source, version)
	return b.String()
}

// ResourceFile renders one TypeScript module for a single resource type.
// localWireName is ir.ServiceAndLocalName's own second return value for
// rt.WireType (e.g. "repository" for "aws_ecr_repository") -- used ONLY
// to derive this file's own exported identifiers (the founder's own
// locked naming scheme, applied identically to Go: `Repository`, never
// `AwsEcrRepository`, since the directory already encodes provider+
// service). rt.WireType itself is rendered into the runtime
// ResourceBinding literal completely unchanged.
func ResourceFile(localWireName string, rt *ir.ResourceType) (string, error) {
	pascalName, err := pascalCase(localWireName)
	if err != nil {
		return "", fmt.Errorf("%s: local name %q: %w", rt.WireType, localWireName, err)
	}

	r := &resourceRenderer{pascalName: pascalName}
	for _, f := range rt.Fields {
		if err := r.collectNestedInterfaces(f, pascalName); err != nil {
			return "", err
		}
	}

	// The runtime descriptor's own body is built into a SEPARATE buffer
	// first, before anything else is written to the file -- mirrors
	// sdk/codegen/templates/go's own ResourceFile exactly: r.fieldSpecLiteral
	// (called below) can hoist a new shared FieldMap const as a side
	// effect (r.fieldMapDecls), which must be written to the file BEFORE
	// this descriptor references it by name.
	// UBI-178 piece 4's own real, live-found gap -- see
	// sdk/codegen/templates/go's own identical ResourceFile fix for the
	// full account: @ubx/sdk's own DataSourceBinding<TLookup, TAttrs> is
	// a deliberately distinct interface from ResourceBinding<TConfig,
	// TAttrs>, and data() (TS's own real top-level function) is typed to
	// take DataSourceBinding specifically -- every data source this
	// template generated before this fix declared its binding const as
	// ResourceBinding regardless, which deno check/tsc refuses against a
	// real data(...) call site.
	bindingType := "ResourceBinding"
	if rt.IsDataSource {
		bindingType = "DataSourceBinding"
	}
	var descriptor strings.Builder
	fmt.Fprintf(&descriptor, "export const %s: %s<%sConfig, %sAttrs> = {\n", pascalName, bindingType, pascalName, pascalName)
	fmt.Fprintf(&descriptor, "  wireType: %q,\n", rt.WireType)
	descriptor.WriteString("  fields: {\n")
	for _, f := range rt.Fields {
		if !fieldIsSettable(f) {
			continue
		}
		idiomatic, err := camelCase(f.WireName)
		if err != nil {
			return "", err
		}
		spec, err := r.fieldSpecLiteral(f, 2)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&descriptor, "    %s: %s,\n", idiomatic, spec)
	}
	descriptor.WriteString("  },\n")
	descriptor.WriteString("};\n")

	var b strings.Builder
	fmt.Fprintf(&b, "// Code generated by `ubx sdk gen` from %s. DO NOT EDIT.\n", rt.WireType)
	fmt.Fprintf(&b, "import type { Computed, FieldMap, %s } from \"@ubx/sdk\";\n\n", bindingType)

	for _, decl := range r.nestedDecls {
		b.WriteString(decl)
		b.WriteString("\n")
	}
	for _, decl := range r.fieldMapDecls {
		b.WriteString(decl)
		b.WriteString("\n")
	}

	// Config interface: settable fields only (Required or Optional --
	// see fieldIsSettable's own doc comment for the Computed-only
	// exclusion this implements).
	fmt.Fprintf(&b, "export interface %sConfig {\n", pascalName)
	for _, f := range rt.Fields {
		if !fieldIsSettable(f) {
			continue
		}
		idiomatic, err := camelCase(f.WireName)
		if err != nil {
			return "", err
		}
		valueType, err := r.tsValueType(f.Type, pascalName, f.WireName)
		if err != nil {
			return "", err
		}
		b.WriteString(fieldDocComment(f, "  "))
		b.WriteString(configFieldLine(idiomatic, f.Required, valueType))
	}
	b.WriteString("}\n\n")

	// Attrs interface: every field, plain value types -- Computed<T>
	// wrapping happens once, generically, in the runtime's own Computed<T>
	// type, not per-field here.
	fmt.Fprintf(&b, "export interface %sAttrs {\n", pascalName)
	for _, f := range rt.Fields {
		idiomatic, err := camelCase(f.WireName)
		if err != nil {
			return "", err
		}
		valueType, err := r.tsValueType(f.Type, pascalName, f.WireName)
		if err != nil {
			return "", err
		}
		b.WriteString(fieldDocComment(f, "  "))
		fmt.Fprintf(&b, "  %s: %s;\n", idiomatic, valueType)
	}
	b.WriteString("}\n\n")

	// The runtime descriptor -- wireType carries the REAL, full wire type
	// string, always, regardless of what the exported identifiers above
	// dropped.
	b.WriteString(descriptor.String())

	return b.String(), nil
}

// configFieldLine renders one Config-shaped interface member line -- `?`
// when the field isn't required, always unioned with Computed<T> since
// either position may legally be assigned a live Computed<T> reference
// (a not-yet-resolved sibling resource's own attribute), the same
// acceptance a top-level Config field already needs. UBI-142: this
// SAME shape is needed both by the top-level %sConfig interface
// (ResourceFile, above) and by every nested interface tsValueType's own
// KindObject case declares (a nested block's own inner fields carry
// real per-field Required/Optional/Computed flags from the schema too,
// via ir.go's blockFields -- identical in kind to a top-level field's
// own, not a degenerate case). Both call sites route through this one
// function so they cannot independently drift out of sync again --
// exactly how UBI-142 happened the first time: the nested case was
// simply never given the top-level case's own treatment when it was
// added.
// fieldDocComment renders f.Description as a real TSDoc `/** ... */`
// line above the field, indented with pad -- mirrors
// sdk/codegen/templates/go's own fieldDocComment exactly (see that
// function's doc comment for why AI-inferred gets a visible suffix and
// DescriptionSourceNone gets no comment at all), using `/** */` rather
// than `//` since that's the convention every real TS editor/tool
// actually surfaces as hover documentation.
func fieldDocComment(f ir.Field, pad string) string {
	desc := oneLineDescription(f.Description)
	switch f.DescriptionSource {
	case ir.DescriptionSourceModel:
		return pad + "/** " + desc + " */\n"
	case ir.DescriptionSourceAIInferred:
		return pad + "/** " + desc + " (AI-inferred) */\n"
	default:
		return ""
	}
}

// oneLineDescription collapses internal newlines to spaces (a real
// TSDoc `/** */` comment can't itself contain a newline cleanly) and
// escapes a literal "*/" so a description can never prematurely close
// the comment it's rendered inside.
func oneLineDescription(s string) string {
	joined := strings.Join(strings.Fields(s), " ")
	return strings.ReplaceAll(joined, "*/", "* /")
}

func configFieldLine(idiomatic string, required bool, valueType string) string {
	optionalMark := ""
	if !required {
		optionalMark = "?"
	}
	return fmt.Sprintf("  %s%s: %s | Computed<%s>;\n", idiomatic, optionalMark, valueType, valueType)
}

// fieldIsSettable reports whether a field belongs in a Config interface
// at all -- true for Required or Optional fields (including a field that
// is BOTH Optional and Computed, a real, common schema shape: settable,
// but the provider fills it in if the program leaves it out), false only
// for a field that is Computed with neither Required nor Optional set
// (an output-only attribute like `id`/`arn` -- never legal as config
// input). A NestedBlock-derived field always has all three flags false
// (ir.go's own Field doc comment -- a real schema fact, not a gap in
// this package) and must NOT be excluded by that: `!f.Computed` alone
// already covers it (Computed is false for every nested block), which is
// why this is `Required || Optional || !Computed` rather than the
// simpler-looking `Required || Optional` that would silently drop every
// nested block from Config.
func fieldIsSettable(f ir.Field) bool {
	return f.Required || f.Optional || !f.Computed
}

// resourceRenderer accumulates one resource type's own nested interface
// declarations (KindObject fields, at any depth), keyed by a
// deterministic name derived from the full field path from the
// resource's own local PascalCase name (UBI-98: the caller now passes
// this package's own per-type localWireName-derived pascalName, never
// the full wire-type-derived one UBI-96 used, and every declaration this
// renderer emits lives in exactly one file -- ES modules give each file
// its own independent scope, so cross-RESOURCE collision is structurally
// impossible here regardless of naming, unlike Go; see this package's
// own doc comment for the full account). Within ONE resource's own tree,
// the identical UBI-96 "_" join (pathPrefix + "_" + fieldPascal) still
// applies so two DIFFERENT nested paths in the same file never share a
// bare name.
//
// A SEPARATE problem, found live this session verifying a synthetic
// reproduction of Go's own real aws_wafv2_web_acl_rule finding (STATE.md,
// UBI-98's Go session) against the SAME real schema for TS: AWS's own
// recursive schema shapes physically repeat an IDENTICAL nested block
// shape at every depth level a real provider schema can express
// recursion to at all. Confirmed empirically NOT to crash `deno check`
// the way it crashed the Go compiler (aws_wafv2_web_acl_rule alone --
// 16.7MB/~253,000 lines of naively-unrolled TS -- type-checked clean in
// ~2 real seconds) -- so this dedup is NOT required to avoid a crash for
// TS the way it was for Go. Applied anyway, for the same underlying
// reason it was worth doing for Go even setting the crash aside: a
// single generated file that large is still a real reviewability/git-
// diff/repo-bloat problem (docs/sdk.md's own explicit "committed to git
// like any other reviewable generated code" design goal), and the fix is
// unconditionally safe here for the identical reason it was safe in Go:
// confirmed by reading sdk/ts/runtime's own serializeConfig before
// relying on this, which reads a config value purely via `Object.entries`
// against the runtime FieldMap VALUE, never against a nested interface's
// own declared TYPE NAME (TS types are erased at runtime entirely) --
// two structurally-identical shapes sharing one interface name changes
// nothing about runtime behavior. seenShapes closes the (cosmetic)
// interface-declaration half; seenFieldMapVars/fieldMapDecls (see
// objectFieldMapRef, below) closes the OTHER half, which -- exactly as
// in Go -- is not cosmetic: the runtime FieldMap literal
// ResourceBinding.fields is built from IS what the evaluator actually
// reads, so hoisting a repeated shape into one shared top-level `const`
// and referencing it by name is what actually shrinks the real output
// (aws_wafv2_web_acl_rule: confirmed dropping from 16.7MB to a small
// fraction of that after this fix, mirroring Go's own ~150x reduction).
type resourceRenderer struct {
	pascalName       string
	nestedDecls      []string
	seenNames        map[string]bool
	seenShapes       map[string]string
	seenFieldMapVars map[string]string
	fieldMapDecls    []string
}

func (r *resourceRenderer) collectNestedInterfaces(f ir.Field, pathPrefix string) error {
	_, err := r.tsValueType(f.Type, pathPrefix, f.WireName)
	return err
}

// tsValueType returns the TS type text for one field's own TypeRef,
// recording a new nested interface declaration (once per unique
// STRUCTURAL SHAPE, not merely once per unique path -- see
// resourceRenderer's own doc comment) as a side effect whenever it
// encounters a KindObject.
func (r *resourceRenderer) tsValueType(t ir.TypeRef, pathPrefix, wireName string) (string, error) {
	switch t.Kind {
	case ir.KindScalar:
		switch t.Scalar {
		case ir.ScalarString:
			return "string", nil
		case ir.ScalarNumber:
			return "number", nil
		case ir.ScalarBool:
			return "boolean", nil
		case ir.ScalarDynamic:
			return "unknown", nil
		default:
			return "", fmt.Errorf("field %q: unrecognized scalar kind %v", wireName, t.Scalar)
		}
	case ir.KindList, ir.KindSet:
		// JSON (and therefore intent/v1) has no Set shape distinct from
		// an array -- List and Set both render as T[], the same
		// collapse provider/ctyvalue.go's own JSON-level encoding
		// already makes (both decode as a JSON array).
		el, err := r.tsValueType(*t.Element, pathPrefix, wireName)
		if err != nil {
			return "", err
		}
		return el + "[]", nil
	case ir.KindMap:
		el, err := r.tsValueType(*t.Element, pathPrefix, wireName)
		if err != nil {
			return "", err
		}
		return "Record<string, " + el + ">", nil
	case ir.KindObject:
		shape, err := nestedShapeSignature(t)
		if err != nil {
			return "", err
		}
		if r.seenShapes == nil {
			r.seenShapes = map[string]string{}
		}
		if existing, ok := r.seenShapes[shape]; ok {
			// An earlier position in this resource's own tree already
			// declared an interface for this EXACT shape -- share it
			// rather than recursing again to emit a byte-identical
			// duplicate under a different path-derived name.
			return existing, nil
		}

		fieldPascal, err := pascalCase(wireName)
		if err != nil {
			return "", err
		}
		name := pathPrefix + "_" + fieldPascal
		if r.seenNames == nil {
			r.seenNames = map[string]bool{}
		}
		if r.seenNames[name] {
			return "", fmt.Errorf("field %q: internal error: nested interface name %q already used for a DIFFERENT shape (path collision, not a shape repeat)", wireName, name)
		}
		r.seenNames[name] = true
		r.seenShapes[shape] = name

		var body strings.Builder
		fmt.Fprintf(&body, "export interface %s {\n", name)
		for _, inner := range t.Object {
			innerIdiomatic, err := camelCase(inner.WireName)
			if err != nil {
				return "", err
			}
			innerType, err := r.tsValueType(inner.Type, name, inner.WireName)
			if err != nil {
				return "", err
			}
			body.WriteString(fieldDocComment(inner, "  "))
			body.WriteString(configFieldLine(innerIdiomatic, inner.Required, innerType))
		}
		body.WriteString("}\n")
		r.nestedDecls = append(r.nestedDecls, body.String())
		return name, nil
	default:
		return "", fmt.Errorf("field %q: unrecognized type kind %v", wireName, t.Kind)
	}
}

// fieldSpecLiteral renders one settable field's own runtime FieldSpec
// literal (sdk/ts/runtime's own FieldSpec type: a plain wire-name string
// for a scalar or a list/set/map-of-scalar field -- no recursion needed
// -- or an object literal naming wireName/kind/fields for anything that
// needs recursive wire-name mapping at evaluation time). Now a
// resourceRenderer method: the object/collection-of-object branches
// route through r.objectFieldMapRef, which may hoist a shared top-level
// FieldMap const as a side effect (see resourceRenderer's own doc
// comment for why this, not just the nested interface declarations, is
// what actually bounds output size for a deeply recursive real schema).
func (r *resourceRenderer) fieldSpecLiteral(f ir.Field, indent int) (string, error) {
	pad := strings.Repeat("  ", indent)
	innerPad := strings.Repeat("  ", indent+1)
	switch f.Type.Kind {
	case ir.KindScalar:
		return fmt.Sprintf("%q", f.WireName), nil
	case ir.KindList, ir.KindSet, ir.KindMap:
		if f.Type.Element.Kind != ir.KindObject {
			// A collection of scalars -- no per-element wire-name mapping
			// needed, same plain-string leaf as a bare scalar field.
			return fmt.Sprintf("%q", f.WireName), nil
		}
		kind := map[ir.TypeKind]string{ir.KindList: "list", ir.KindSet: "set", ir.KindMap: "map"}[f.Type.Kind]
		fieldsRef, err := r.objectFieldMapRef(f.Type.Element.Object)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("{\n%swireName: %q,\n%skind: %q,\n%sfields: %s,\n%s}",
			innerPad, f.WireName, innerPad, kind, innerPad, fieldsRef, pad), nil
	case ir.KindObject:
		fieldsRef, err := r.objectFieldMapRef(f.Type.Object)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("{\n%swireName: %q,\n%skind: %q,\n%sfields: %s,\n%s}",
			innerPad, f.WireName, innerPad, "object", innerPad, fieldsRef, pad), nil
	default:
		return "", fmt.Errorf("field %q: unrecognized type kind %v", f.WireName, f.Type.Kind)
	}
}

// objectFieldMapRef returns an EXPRESSION for one nested object's own
// FieldMap -- either a reference to an already-hoisted shared top-level
// const (a shape this resource's own tree has already rendered once,
// anywhere in it) or, the first time a given shape is seen, a fresh
// inline object literal that ALSO gets hoisted into a new shared const
// (recorded in r.fieldMapDecls) so every LATER occurrence of the
// identical shape references it instead of re-inlining. Keyed by
// nestedShapeSignature -- the same structural fingerprint r.tsValueType
// already uses for the (cosmetic) nested interface declarations,
// deliberately: the base name for a new shared const reuses whatever
// name tsValueType already minted for the identical shape during
// ResourceFile's own earlier collectNestedInterfaces pre-pass (that pass
// always runs first and recurses through every field's full tree,
// settable or not, so r.seenShapes is already fully populated for every
// shape this method will ever be asked about) -- the fallback branch
// below is defensive, not expected to ever actually trigger.
func (r *resourceRenderer) objectFieldMapRef(fields []ir.Field) (string, error) {
	shape, err := nestedShapeSignature(ir.TypeRef{Kind: ir.KindObject, Object: fields})
	if err != nil {
		return "", err
	}
	if r.seenFieldMapVars == nil {
		r.seenFieldMapVars = map[string]string{}
	}
	if existing, ok := r.seenFieldMapVars[shape]; ok {
		return existing, nil
	}

	base := r.seenShapes[shape]
	if base == "" {
		base = fmt.Sprintf("%s_shape%d", r.pascalName, len(r.seenFieldMapVars))
	}
	constName := base + "Fields"
	r.seenFieldMapVars[shape] = constName

	lit, err := r.objectFieldMapLiteral(fields, 0)
	if err != nil {
		return "", err
	}
	r.fieldMapDecls = append(r.fieldMapDecls, fmt.Sprintf("const %s: FieldMap = %s;\n", constName, lit))
	return constName, nil
}

// objectFieldMapLiteral renders an INLINE FieldMap object literal for
// one nested object's own fields -- every field of a nested object is
// "settable" from the config's own point of view (a program author
// writes the whole nested object literal; there's no separate
// Required/Optional gate at this level the way there is for a resource's
// own top-level Config, since NestedBlock-derived fields carry no such
// flags at all). Called from exactly one place now (objectFieldMapRef,
// the first time a given shape is seen).
func (r *resourceRenderer) objectFieldMapLiteral(fields []ir.Field, indent int) (string, error) {
	pad := strings.Repeat("  ", indent)
	innerPad := strings.Repeat("  ", indent+1)
	var b strings.Builder
	b.WriteString("{\n")
	for _, f := range fields {
		idiomatic, err := camelCase(f.WireName)
		if err != nil {
			return "", err
		}
		spec, err := r.fieldSpecLiteral(f, indent+1)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s%s: %s,\n", innerPad, idiomatic, spec)
	}
	fmt.Fprintf(&b, "%s}", pad)
	return b.String(), nil
}

// nestedShapeSignature computes a canonical, structure-only fingerprint
// of one TypeRef's own recursive shape (field wire names, in
// schema-declared order, each with its own recursive signature) --
// mirrors sdk/codegen/templates/go's own function of the same name
// exactly (kept as an independent copy, not shared via sdk/codegen/ir,
// matching this package's own established "no per-language identifier
// convention lives in ir" posture -- this is closer to a codegen
// IMPLEMENTATION DETAIL than a naming convention, but duplicating a few
// lines is cheaper and safer here than inventing a new cross-language
// shared-helper seam for one function, with no other consumer).
func nestedShapeSignature(t ir.TypeRef) (string, error) {
	switch t.Kind {
	case ir.KindScalar:
		return fmt.Sprintf("s%d", t.Scalar), nil
	case ir.KindList, ir.KindSet, ir.KindMap:
		el, err := nestedShapeSignature(*t.Element)
		if err != nil {
			return "", err
		}
		kindTag := map[ir.TypeKind]byte{ir.KindList: 'L', ir.KindSet: 'S', ir.KindMap: 'M'}[t.Kind]
		return fmt.Sprintf("%c<%s>", kindTag, el), nil
	case ir.KindObject:
		var b strings.Builder
		b.WriteString("O{")
		for _, f := range t.Object {
			sig, err := nestedShapeSignature(f.Type)
			if err != nil {
				return "", err
			}
			b.WriteString(f.WireName)
			b.WriteString(":")
			b.WriteString(sig)
			b.WriteString(";")
		}
		b.WriteString("}")
		return b.String(), nil
	default:
		return "", fmt.Errorf("unrecognized type kind %v", t.Kind)
	}
}

// pascalCase and camelCase both convert a provider wire name
// (snake_case, verbatim from a real schema -- e.g. "instance_class") into
// TypeScript's own idiomatic casing. Every real provider attribute name
// this codebase has seen (UBI-9's 51 AWS types, UBI-21's GCP types) is
// plain ASCII snake_case; both functions refuse anything else explicitly
// rather than silently producing a malformed identifier, matching this
// project's standing "never a best-effort coercion" rule (docs/sdk.md's
// own row 5).
func pascalCase(wireName string) (string, error) {
	parts, err := splitWireName(wireName)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	return b.String(), nil
}

func camelCase(wireName string) (string, error) {
	parts, err := splitWireName(wireName)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for i, p := range parts {
		if i == 0 {
			b.WriteString(strings.ToLower(p[:1]))
			b.WriteString(p[1:])
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	return b.String(), nil
}

func splitWireName(wireName string) ([]string, error) {
	if wireName == "" {
		return nil, fmt.Errorf("empty wire name")
	}
	parts := strings.Split(wireName, "_")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue // tolerate a leading/trailing/doubled underscore rather than hard-failing on it
		}
		for _, r := range p {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') {
				return nil, fmt.Errorf("wire name %q: unsupported character %q (only lowercase ascii + digits + underscore seen in any real provider schema this project has generated against)", wireName, r)
			}
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("wire name %q: no identifier characters", wireName)
	}
	return out, nil
}
