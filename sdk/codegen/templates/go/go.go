// Package gotmpl is the Go template half of docs/sdk.md's own "Codegen
// design": ir.ResourceType (language-neutral, wire-name-only) -> idiomatic
// Go bindings. This is the only package in sdk/codegen that knows what
// "idiomatic Go" means -- exported PascalCase struct fields, a runtime
// FieldMap descriptor -- none of which leaks back into sdk/codegen/ir
// itself. Named gotmpl, not go (sdk/codegen/templates/go is the import
// path; go itself is a reserved word and cannot be a package identifier).
//
// UBI-98's own per-provider-repo/per-service-package restructure: this
// package no longer renders one flat file covering an entire provider
// (UBI-96's own real failure mode -- a flat package can hold enough
// declarations to crash the Go compiler itself at real full-provider
// scale, "internal compiler error: NewBulk too big", confirmed against
// the real 1,682-type hashicorp/aws@6.54.0 schema and unrelated to
// naming -- see STATE.md and the UBI-98 Linear thread for the full
// account). GeneratedRepo renders a whole repo-shaped tree instead: one
// Go module per provider, one package per AWS-service boundary
// (ir.ServiceAndLocalName), one file per resource type within its own
// service package -- exactly the shape the founder's own comment on
// UBI-98 locked in (`ecr.Repository`, never
// `generated.AwsEcrRepository` -- the import path already encodes
// provider+service, so the redundant prefix is dropped from every type
// name).
package gotmpl

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ubiquex/ubiquex/sdk/codegen/ir"
)

// GeneratedRepo renders every file for one provider's repo-shaped Go
// output tree: a go.mod stub for module
// "github.com/ubiquex/ubx-sdk-<shortName>/go" (shortName is the caller's
// own concern -- cli/sdk.go derives it from the declared provider source,
// e.g. "hashicorp/aws" -> "aws" -- this package only ever renders Go
// source, never parses a provider source string), one doc.go per derived
// service package (PackageDoc -- the shared banner + SourceProvenance var
// every file in that package would otherwise need to redeclare, which Go
// forbids), and one <local>.go per resource type (ResourceFile) inside
// its own service package's own subdirectory.
//
// UBI-138: every returned path is now rooted under "sdk/go/" -- Go,
// TypeScript, and Python each publish independently from their own
// sibling subdirectory of one combined per-provider repo
// (ubx-sdk-<shortName>), the real Pulumi precedent UBI-138 cites
// (pulumi-aws's own sdk/go/, sdk/python/, sdk/nodejs/). This makes
// "sdk/go/" itself the real Go module boundary: module path
// "github.com/ubiquex/ubx-sdk-<shortName>/sdk/go", a genuine
// SUBDIRECTORY Go module (go.mod lives at "sdk/go/go.mod", never bare
// "go.mod"), requiring path-prefixed tags at publish time (e.g.
// "sdk/go/v0.3.1", never a bare "v0.3.1" -- confirmed against the Go modules reference
// before this change, not assumed). The shared runtime module
// (github.com/ubiquex/ubx-sdk-go, a wholly separate repo, UBI-139's own
// later scope) is untouched by this -- every generated file's own
// "github.com/ubiquex/ubx-sdk-go/runtime" import stays exactly as it was.
//
// Returns a map of path, relative to the repo root using "/" always
// (never the host OS separator -- a logical path, not a filesystem one;
// the caller decides how to actually write it to disk) -> file content.
// types is grouped by ir.ServiceAndLocalName(rt.WireType)'s own service
// return value, then sorted by WireType within each service (defensively,
// even if the caller already passed a sorted slice -- codegen output must
// never depend on caller-supplied ordering, determinism is a feature).
func GeneratedRepo(shortName, source, version string, types []*ir.ResourceType) (map[string]string, error) {
	sorted := make([]*ir.ResourceType, len(types))
	copy(sorted, types)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].WireType < sorted[j].WireType })

	type entry struct {
		rt    *ir.ResourceType
		local string
	}
	byService := map[string][]entry{}
	var services []string
	for _, rt := range sorted {
		service, local, err := ir.ServiceAndLocalName(rt.WireType)
		if err != nil {
			return nil, fmt.Errorf("sdk/codegen/templates/go: %w", err)
		}
		service = goPackageIdent(service)
		if _, ok := byService[service]; !ok {
			services = append(services, service)
		}
		byService[service] = append(byService[service], entry{rt, local})
	}
	sort.Strings(services)

	files := map[string]string{
		"sdk/go/go.mod": fmt.Sprintf("module github.com/ubiquex/ubx-sdk-%s/sdk/go\n\ngo 1.23\n\nrequire github.com/ubiquex/ubx-sdk-go v0.0.0\n", shortName),
	}
	// UBI-106: every service package nests under shortName/ (e.g.
	// aws/iam/, never iam/ directly under sdk/go/) -- a real repo-browsing
	// finding, not a design nicety: a bare-root layout put 200+ service
	// directories directly at hashicorp/aws's own repo root, alphabetically
	// sorted ahead of README.md by GitHub's own file browser, burying it
	// below the fold. go.mod itself stays at sdk/go/'s own true root (a Go
	// module's own go.mod is never optional-location) -- only service
	// packages move. Directory names need no Go-identifier validity (only
	// Go PACKAGE declarations do), so shortName needs no guarding here the
	// way pyModuleIdent guards it for Python's own dotted-import
	// requirement, below.
	for _, service := range services {
		files["sdk/go/"+shortName+"/"+service+"/doc.go"] = PackageDoc(service, source, version)

		// UBI-108: a real, live-verified collision class hashicorp/aws
		// never surfaced -- a sibling resource in this SAME service
		// package whose own wire name is exactly "<other>_config" (a
		// real, independent resource, not a nested block) declares a
		// binding var identical to some sibling's own auto-derived
		// "<Name>Config" struct name. Checked package-wide, before
		// rendering any one file, the same "spans a whole directory, not
		// one file" scope UBI-96's own duplicate check already needs for
		// Go specifically (ResourceFile's own doc comment has the full
		// account, including the 3 real hits this found).
		siblingPascalNames := map[string]bool{}
		for _, e := range byService[service] {
			pn, err := pascalCase(e.local)
			if err != nil {
				return nil, fmt.Errorf("sdk/codegen/templates/go: %s: local name %q: %w", e.rt.WireType, e.local, err)
			}
			siblingPascalNames[pn] = true
		}
		for _, e := range byService[service] {
			pn, err := pascalCase(e.local)
			if err != nil {
				return nil, fmt.Errorf("sdk/codegen/templates/go: %s: local name %q: %w", e.rt.WireType, e.local, err)
			}
			configOverride := ""
			if siblingPascalNames[pn+"Config"] {
				configOverride = pn + "Config_"
			}
			content, err := ResourceFile(service, e.local, e.rt, configOverride)
			if err != nil {
				return nil, fmt.Errorf("sdk/codegen/templates/go: %s: %w", e.rt.WireType, err)
			}
			// UBI-151: a real, live-verified Go-toolchain quirk, NOT a
			// naming collision like the *Config case above -- Go treats
			// ANY file whose own name ends in "_test.go" as test-only and
			// permanently excludes it from `go build`/`go doc`/any real
			// import, regardless of content. A resource whose own local
			// name happens to end in "_test" (a real, wire-derived name,
			// e.g. google_network_management_connectivity_test,
			// azurerm_application_insights_web_test) produced exactly
			// that filename -- 3 real, confirmed-live instances across
			// this whole codebase's 4 real provider corpora before this
			// fix (google, azurerm x2; aws and kubernetes confirmed
			// clean). Scoped to the FILENAME only, on purpose -- the
			// exported Go identifier/type name (via ResourceFile's own
			// e.local above, untouched) is correct today and does not
			// need to change, only the map key a real consumer's
			// `go build` never sees. Same trailing-underscore convention
			// the *Config collision above already uses.
			fileLocal := e.local
			if strings.HasSuffix(fileLocal, "_test") {
				fileLocal += "_"
			}
			files["sdk/go/"+shortName+"/"+service+"/"+fileLocal+".go"] = content
		}
	}
	return files, nil
}

// PackageDoc renders one service package's shared doc.go: the "generated,
// do not edit, committed to git" banner (docs/sdk.md's own "ubx sdk gen:
// local, pinned, offline-after-generation") plus SourceProvenance --
// declared exactly ONCE per package here, never per resource-type file,
// since Go allows a package-level identifier exactly one declaration
// across every file in a directory combined (the same namespace
// UBI-96's own CheckNoDuplicateDeclarations already treats as spanning
// the whole package, not a single file).
func PackageDoc(pkgName, source, version string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// Code generated by `ubx sdk gen` from %s@%s. DO NOT EDIT.\n", source, version)
	b.WriteString("// This file is committed to git like any other reviewable generated code\n")
	b.WriteString("// (docs/sdk.md — \"ubx sdk gen: local, pinned, offline-after-generation\") --\n")
	b.WriteString("// re-run `ubx sdk gen` to regenerate after a provider version bump.\n")
	fmt.Fprintf(&b, "package %s\n\n", pkgName)
	fmt.Fprintf(&b, "var SourceProvenance = struct {\n\tSource  string\n\tVersion string\n}{Source: %q, Version: %q}\n", source, version)
	return b.String()
}

// ResourceFile renders one Go source file for a single resource type,
// scoped to package pkgName (the derived AWS-service boundary, e.g.
// "ecr" -- ir.ServiceAndLocalName's own first return value for
// rt.WireType). localWireName is that same call's own second return
// value (e.g. "repository" for "aws_ecr_repository") -- used ONLY to
// derive this file's own Go identifiers (the founder's own locked
// naming scheme: `ecr.Repository`, never `ecr.AwsEcrRepository`, since
// the import path already encodes provider+service). rt.WireType itself
// is rendered into the runtime ResourceBinding literal completely
// unchanged -- the real, full wire type string a provider actually
// expects over the wire, never shortened; only the GO IDENTIFIER drops
// the redundant prefix, never the wire-protocol value.
//
// configTypeNameOverride is UBI-108's own real, live-verified finding on
// hashicorp/google@7.40.0 (3 real hits: spanner/instance+instance_config,
// workstations/workstation+workstation_config,
// migration/center_report+center_report_config) -- a SIBLING resource in
// the SAME service package whose own wire name is exactly
// "<this resource>_config" (a real, independent top-level resource, not
// a nested block -- distinct from UBI-96's own nested-block-name
// collision) declares a binding var named exactly what this resource's
// own auto-derived "<Name>Config" struct would be. GeneratedRepo checks
// every sibling up front (a package-wide concern, same reason UBI-96's
// own duplicate check spans a whole directory) and passes a
// disambiguated override here when it detects one; empty string means
// "no collision, use the default `<Name>Config`" -- every existing
// caller/test that predates this finding passes "" unchanged.
func ResourceFile(pkgName, localWireName string, rt *ir.ResourceType, configTypeNameOverride string) (string, error) {
	pascalName, err := pascalCase(localWireName)
	if err != nil {
		return "", fmt.Errorf("%s: local name %q: %w", rt.WireType, localWireName, err)
	}
	configTypeName := pascalName + "Config"
	if configTypeNameOverride != "" {
		configTypeName = configTypeNameOverride
	}

	r := &resourceRenderer{pascalName: pascalName}
	for _, f := range rt.Fields {
		if !fieldIsSettable(f) {
			continue
		}
		if err := r.collectNestedStructs(f, pascalName); err != nil {
			return "", err
		}
	}

	// The runtime descriptor's own body is built into a SEPARATE buffer
	// first, deliberately, before anything else is written to the file --
	// r.fieldSpecLiteral (called below) can hoist a new shared
	// ubx.FieldMap var as a side effect (r.fieldMapDecls -- see
	// resourceRenderer's own doc comment for why this half, not just the
	// nested struct declarations above, is what actually bounds output
	// size for a deeply recursive real schema), and that declaration must
	// be written to the file BEFORE this descriptor references it by
	// name for the generated file to read top-to-bottom sensibly (Go
	// itself has no declare-before-use ordering requirement within one
	// package, but every other declaration here follows that order
	// anyway).
	var descriptor strings.Builder
	fmt.Fprintf(&descriptor, "var %s = ubx.ResourceBinding{\n", pascalName)
	fmt.Fprintf(&descriptor, "\tWireType: %q,\n", rt.WireType)
	descriptor.WriteString("\tFields: ubx.FieldMap{\n")
	for _, f := range rt.Fields {
		if !fieldIsSettable(f) {
			continue
		}
		idiomatic, err := pascalCase(f.WireName)
		if err != nil {
			return "", err
		}
		spec, err := r.fieldSpecLiteral(f, 2)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&descriptor, "\t\t%q: %s,\n", idiomatic, spec)
	}
	descriptor.WriteString("\t},\n")
	descriptor.WriteString("}\n")

	var b strings.Builder
	fmt.Fprintf(&b, "// Code generated by `ubx sdk gen` from %s. DO NOT EDIT.\n", rt.WireType)
	fmt.Fprintf(&b, "package %s\n\n", pkgName)
	b.WriteString("import ubx \"github.com/ubiquex/ubx-sdk-go/runtime\"\n\n")

	for _, decl := range r.nestedDecls {
		b.WriteString(decl)
		b.WriteString("\n")
	}
	for _, decl := range r.fieldMapDecls {
		b.WriteString(decl)
		b.WriteString("\n")
	}

	// Config struct: settable fields only (a NestedBlock-derived field has
	// Required/Optional/Computed all false and must still be settable,
	// hence `!Computed` rather than `Required || Optional` alone --
	// fieldIsSettable's own doc comment).
	fmt.Fprintf(&b, "type %s struct {\n", configTypeName)
	for _, f := range rt.Fields {
		if !fieldIsSettable(f) {
			continue
		}
		idiomatic, err := pascalCase(f.WireName)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "\t%s any\n", idiomatic)
	}
	b.WriteString("}\n\n")

	// The runtime descriptor -- WireType carries the REAL, full wire type
	// string, always, regardless of what the Go identifier above dropped.
	b.WriteString(descriptor.String())

	return b.String(), nil
}

// fieldIsSettable mirrors sdk/codegen/templates/ts's own function of the
// same name exactly -- see that package's doc comment for why this is
// `Required || Optional || !Computed` rather than the simpler-looking
// `Required || Optional`.
func fieldIsSettable(f ir.Field) bool {
	return f.Required || f.Optional || !f.Computed
}

// resourceRenderer accumulates one resource type's own nested Config
// struct declarations (KindObject fields, at any depth), keyed by a
// deterministic name derived from the full field path from the
// resource's own local PascalCase name (UBI-98: the caller now passes
// this package's own per-type localWireName-derived pascalName, never
// the full wire-type-derived one UBI-96 used) -- collision-free WITHIN
// one resource (no two fields can share the same path), but that was
// never the whole story even under the old flat-package scheme: a bare
// "parentPascal+fieldPascal" concatenation (UBI-96's original scheme)
// could and did collide with a DIFFERENT resource's own bare
// pascalCase(wireType) name. Confirmed live against the real
// hashicorp/aws@6.54.0 provider (1,682 types): AWS's own convention of
// splitting a legacy inline nested block out into its own standalone
// resource (aws_s3_bucket's "logging" block vs. the separate
// aws_s3_bucket_logging resource; 6,278 such names total, every single
// one within the same AWS service -- so UBI-98's OWN per-service-package
// split, on its own, would NOT have fixed this; it was fixed separately,
// UBI-96, by the "_" join below). Under THIS package's new per-service
// scope, the identical proof still holds, now scoped to one service
// package rather than the whole provider: pascalCase never emits an
// underscore, so every nested name here is disjoint from every bare
// resource name in the same package (which has none), and two different
// resources' own nested trees within one service package can never
// collide either, since the first path segment up to the first inserted
// "_" is always that resource's own distinct local pascal name (verified
// collision-free per (service, local) pair across the real full schema --
// ir.ServiceAndLocalName's own doc comment).
//
// A THIRD, separate problem, found live this session verifying THIS
// restructure at true full-provider scale (not anticipated by UBI-98's
// own ticket text, which only ever discussed aggregate package size):
// AWS's own recursive schema shapes -- confirmed via
// aws_wafv2_web_acl_rule, the real, extreme case -- physically repeat an
// IDENTICAL nested block shape at every depth level a real provider
// schema can express recursion to at all (there is no true
// self-reference in a real tfplugin schema; "recursion" is always
// statically unrolled to some fixed depth, each level re-declaring the
// literal same block verbatim). Naively minting a NEW, uniquely-pathed
// struct declaration at every depth (the scheme above, alone) means a
// deeply-recursive real shape renders to genuinely enormous output --
// aws_wafv2_web_acl_rule ALONE, confirmed live, rendered to over 10MB /
// ~250,000 lines before this fix, enough by itself (independent of any
// sibling resource type sharing its package) to reproduce the exact same
// Go compiler "NewBulk too big" crash UBI-96 first found -- this time
// from a single resource type's own file, a problem UBI-98's
// per-service-package split could never fix on its own no matter how
// finely packages are split. seenShapes closes HALF of this: goFieldMeta
// now keys on a canonical STRUCTURAL signature of a KindObject's own
// recursive shape (nestedShapeSignature, below), not just its derived
// path/name -- two positions in the tree with the IDENTICAL shape share
// one already-emitted struct declaration instead of emitting a
// byte-identical duplicate under a different name. This half is
// unconditionally safe, not merely a size optimization with a
// correctness cost: nested struct declarations here are documentation
// only -- confirmed by reading sdk/go/runtime's own serializeConfig
// before relying on this, which walks a Config value purely by Go struct
// FIELD NAME via reflection, never by a nested value's own declared TYPE
// NAME -- so which Go type name two structurally-identical shapes happen
// to share changes nothing about runtime behavior.
//
// This alone was NOT enough, confirmed live, not assumed: deduplicating
// only the (cosmetic) struct declarations shrank aws_wafv2_web_acl_rule
// from >10MB to ~6.5MB, but the real full-provider `go build` still
// crashed identically on the wafv2/quicksight service packages --
// because fieldSpecLiteral/objectFieldMapLiteral (the runtime
// ubx.FieldMap{...} literal ResourceBinding.Fields is ACTUALLY built
// from, unlike the struct declarations above) were still naively
// re-inlining the ENTIRE recursive literal tree at every depth, with no
// dedup at all -- and THAT is what was actually driving the Go
// compiler's own internal "bulk" past its limit, not the struct
// declarations. seenFieldMapVars/fieldMapDecls closes the other half,
// identically in spirit: fieldSpecLiteral now hoists a repeated shape's
// ubx.FieldMap{...} literal into ONE shared top-level `var`
// (`<Name>Fields`) the first time it's seen, and every later occurrence
// of the identical shape emits a REFERENCE to that var instead of
// re-inlining the literal -- unlike the struct-declaration dedup, this
// one is NOT cosmetic: sdk/go/runtime's own ResourceBinding.Fields is
// exactly this value, read directly at evaluation time, so two
// structurally-identical shapes sharing one Go value (not just one type
// name) must be, and is, byte-for-byte equivalent to what the un-deduped
// literal would have evaluated to -- confirmed by this package's own
// TestResourceFile_RecursiveShape_TrueRepeat, which checks the field
// mapping still resolves correctly for both occurrences after sharing.
// seenNames (renamed from the prior seen field) still guards the same
// by-path uniqueness the doc comment above establishes, now used only as
// a fallback should a shape genuinely never repeat.
type resourceRenderer struct {
	pascalName       string
	nestedDecls      []string
	seenNames        map[string]bool
	seenShapes       map[string]string
	seenFieldMapVars map[string]string
	fieldMapDecls    []string
}

func (r *resourceRenderer) collectNestedStructs(f ir.Field, pathPrefix string) error {
	_, err := r.goFieldMeta(f.Type, pathPrefix, f.WireName)
	return err
}

// goFieldMeta records a new nested Config struct declaration (once per
// unique STRUCTURAL SHAPE, not merely once per unique path -- see
// resourceRenderer's own doc comment for why a shape-keyed cache is
// required, not just a size nicety) as a side effect whenever it
// encounters a KindObject. Unlike sdk/codegen/templates/ts's
// tsValueType, the return value itself is unused by callers (every
// Config field is typed any regardless of shape) -- this only exists to
// drive the nested-struct side effect walk.
func (r *resourceRenderer) goFieldMeta(t ir.TypeRef, pathPrefix, wireName string) (string, error) {
	switch t.Kind {
	case ir.KindScalar:
		return "", nil
	case ir.KindList, ir.KindSet, ir.KindMap:
		return r.goFieldMeta(*t.Element, pathPrefix, wireName)
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
			// declared a struct for this EXACT shape -- share it rather
			// than recursing again to emit a byte-identical duplicate
			// under a different path-derived name (this is what actually
			// bounds a deeply self-similar real schema, e.g.
			// aws_wafv2_web_acl_rule's own recursive statement tree, to a
			// small, finite number of distinct declarations).
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
		// A name collision here (same path, different shape -- can't
		// happen given the disjointness proof above, but guarded rather
		// than silently overwriting) is a real bug, not a shape repeat
		// (those are already handled above) -- surfaced loudly.
		if r.seenNames[name] {
			return "", fmt.Errorf("field %q: internal error: nested struct name %q already used for a DIFFERENT shape (path collision, not a shape repeat)", wireName, name)
		}
		r.seenNames[name] = true
		r.seenShapes[shape] = name

		var body strings.Builder
		fmt.Fprintf(&body, "type %s struct {\n", name)
		for _, inner := range t.Object {
			innerIdiomatic, err := pascalCase(inner.WireName)
			if err != nil {
				return "", err
			}
			if _, err := r.goFieldMeta(inner.Type, name, inner.WireName); err != nil {
				return "", err
			}
			fmt.Fprintf(&body, "\t%s any\n", innerIdiomatic)
		}
		body.WriteString("}\n")
		r.nestedDecls = append(r.nestedDecls, body.String())
		return name, nil
	default:
		return "", fmt.Errorf("field %q: unrecognized type kind %v", wireName, t.Kind)
	}
}

// nestedShapeSignature computes a canonical, structure-only fingerprint
// of one TypeRef's own recursive shape (field wire names, in
// schema-declared order, each with its own recursive signature) --
// deliberately ignores path/naming entirely, so two positions in a
// resource's own nested-block tree that describe the EXACT same real
// shape (AWS's own recursive schemas repeat an identical block verbatim
// at every depth level they support -- see resourceRenderer's own doc
// comment for the live-verified aws_wafv2_web_acl_rule case this exists
// to bound) produce the identical signature and can safely share one
// already-emitted Go struct declaration.
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

// fieldSpecLiteral renders one settable field's own runtime
// ubx.FieldSpec literal, mirroring sdk/codegen/templates/ts's own
// fieldSpecLiteral exactly (see that function's doc comment for the
// scalar-vs-object-vs-collection-of-object distinction) -- now a
// resourceRenderer method, not a free function: the object/
// collection-of-object branches route through r.objectFieldMapRef,
// which may hoist a shared top-level ubx.FieldMap var as a side effect
// (see resourceRenderer's own doc comment for why this, not just the
// nested struct declarations, is what actually bounds output size for a
// deeply recursive real schema).
func (r *resourceRenderer) fieldSpecLiteral(f ir.Field, indent int) (string, error) {
	pad := strings.Repeat("\t", indent)
	innerPad := strings.Repeat("\t", indent+1)
	switch f.Type.Kind {
	case ir.KindScalar:
		return fmt.Sprintf("ubx.FieldSpec{WireName: %q}", f.WireName), nil
	case ir.KindList, ir.KindSet, ir.KindMap:
		if f.Type.Element.Kind != ir.KindObject {
			return fmt.Sprintf("ubx.FieldSpec{WireName: %q}", f.WireName), nil
		}
		kind := map[ir.TypeKind]string{ir.KindList: "list", ir.KindSet: "set", ir.KindMap: "map"}[f.Type.Kind]
		fieldsRef, err := r.objectFieldMapRef(f.Type.Element.Object)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("ubx.FieldSpec{\n%sWireName: %q,\n%sKind: %q,\n%sFields: %s,\n%s}",
			innerPad, f.WireName, innerPad, kind, innerPad, fieldsRef, pad), nil
	case ir.KindObject:
		fieldsRef, err := r.objectFieldMapRef(f.Type.Object)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("ubx.FieldSpec{\n%sWireName: %q,\n%sKind: %q,\n%sFields: %s,\n%s}",
			innerPad, f.WireName, innerPad, "object", innerPad, fieldsRef, pad), nil
	default:
		return "", fmt.Errorf("field %q: unrecognized type kind %v", f.WireName, f.Type.Kind)
	}
}

// objectFieldMapRef returns an EXPRESSION for one nested object's own
// ubx.FieldMap -- either a reference to an already-hoisted shared
// top-level var (a shape this resource's own tree has already rendered
// once, anywhere in it) or, the first time a given shape is seen, a
// fresh inline ubx.FieldMap{...} literal that ALSO gets hoisted into a
// new shared var (recorded in r.fieldMapDecls) so every LATER occurrence
// of the identical shape references it instead of re-inlining. Keyed by
// nestedShapeSignature -- the exact same structural fingerprint
// r.goFieldMeta already uses for the (cosmetic) nested struct
// declarations, deliberately: the base name for a new shared var reuses
// whatever name goFieldMeta already minted for the identical shape
// during ResourceFile's own earlier collectNestedStructs pre-pass (that
// pass always runs first and recurses through every settable field's
// full tree, so r.seenShapes is already fully populated for every shape
// this method will ever be asked about) -- the fallback branch below is
// defensive, not expected to ever actually trigger.
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
	varName := base + "Fields"
	// Reserve the name before rendering the literal body -- a shape that
	// (recursively) contains itself is not possible in a real, finite
	// provider schema (there is no true self-reference in a tfplugin
	// schema, only static unrolling to a fixed depth -- goFieldMeta's own
	// doc comment), but reserving first rather than after costs nothing
	// and avoids relying on that invariant holding forever.
	r.seenFieldMapVars[shape] = varName

	lit, err := r.objectFieldMapLiteral(fields, 1)
	if err != nil {
		return "", err
	}
	r.fieldMapDecls = append(r.fieldMapDecls, fmt.Sprintf("var %s = %s\n", varName, lit))
	return varName, nil
}

// objectFieldMapLiteral renders an INLINE ubx.FieldMap literal for one
// nested object's own fields -- every field of a nested object is
// "settable" from the config's own point of view (mirrors
// sdk/codegen/templates/ts's own objectFieldMapLiteral). Called from
// exactly one place now (objectFieldMapRef, the first time a given shape
// is seen) -- every repeat occurrence of an already-rendered shape short-
// circuits to a var reference before ever reaching this method again.
func (r *resourceRenderer) objectFieldMapLiteral(fields []ir.Field, indent int) (string, error) {
	pad := strings.Repeat("\t", indent)
	innerPad := strings.Repeat("\t", indent+1)
	var b strings.Builder
	b.WriteString("ubx.FieldMap{\n")
	for _, f := range fields {
		idiomatic, err := pascalCase(f.WireName)
		if err != nil {
			return "", err
		}
		spec, err := r.fieldSpecLiteral(f, indent+1)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s%q: %s,\n", innerPad, idiomatic, spec)
	}
	fmt.Fprintf(&b, "%s}", pad)
	return b.String(), nil
}

// goPackageIdent guards a derived service name (ir.ServiceAndLocalName's
// own first return value) against names that are either a Go syntax
// keyword or otherwise special to the go TOOL itself, not just the
// language grammar. Two real, live-verified findings this session, not
// hypotheticals: `aws_default_vpc` and five sibling real
// hashicorp/aws@6.54.0 types derive service "default" (a Go keyword --
// `package default` is a syntax error); `aws_main_route_table_association`
// derives service "main" (not a keyword -- syntactically legal -- but
// `go build`/`go run` treat a package literally named "main" as a
// command, requiring a `func main()`; a generated bindings package has
// none, so `go build ./...` fails with "function main is undeclared in
// the main package" -- confirmed live, the exact real full-provider
// repro). Checked exhaustively against all 258 real derived service
// names from the real full schema -- "default" and "main" are the ONLY
// two that actually collide with anything in goReservedPackageNames.
// "internal"/"vendor"/"testdata" are included defensively (real,
// well-known go-tool-special directory names -- "internal" restricts
// importability, "vendor" is the vendoring convention, "testdata" is
// silently EXCLUDED from `go build ./...`'s own package discovery
// entirely, which would be a silent, not loud, failure mode) even though
// none of them occur in hashicorp/aws@6.54.0's own real schema -- not
// verified against a real occurrence this session, unlike "default"/
// "main", but a future, different provider (gcp, azure, kubernetes)
// could realistically use one of these as a genuine service slug.
// Resolved the same way sdk/codegen/templates/py's own pythonKeywords
// already resolves a wire name colliding with a Python keyword: a
// trailing underscore, never a silent rename to some other word that
// would break the "wire type's own service token, mechanically"
// derivation rule ir.ServiceAndLocalName's doc comment establishes.
func goPackageIdent(service string) string {
	if goReservedPackageNames[service] {
		return service + "_"
	}
	return service
}

var goReservedPackageNames = map[string]bool{
	// Go language keywords -- a package clause naming one of these is a
	// syntax error.
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
	// Not language keywords -- special to the go TOOL itself instead.
	"main": true, "internal": true, "vendor": true, "testdata": true,
}

// pascalCase converts a provider wire name (snake_case, verbatim from a
// real schema -- e.g. "instance_class") into Go's own idiomatic exported
// identifier casing ("InstanceClass"). Mirrors
// sdk/codegen/templates/ts's own pascalCase/splitWireName exactly,
// including the "never a best-effort coercion" refusal on anything
// outside plain ASCII snake_case (docs/sdk.md's own row 5).
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
