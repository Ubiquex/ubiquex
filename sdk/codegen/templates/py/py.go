// Package pytmpl is the Python template half of docs/sdk.md's own
// "Codegen design": ir.ResourceType (language-neutral, wire-name-only) ->
// idiomatic Python bindings. This is the only package in sdk/codegen
// that knows what "idiomatic Python" means -- @dataclass Config classes,
// a runtime FieldMap descriptor -- none of which leaks back into
// sdk/codegen/ir itself.
//
// Smaller field-naming problem than TS (camelCase) or Go (PascalCase):
// every real provider wire name this project has generated against is
// already lowercase-with-underscores -- already a valid, idiomatic
// Python identifier verbatim. The only real work here is guarding
// against a wire name that collides with a Python keyword (rare, but
// real -- e.g. a field literally named "class" or "in"), handled the
// same way generated protobuf/thrift Python bindings conventionally do:
// a trailing underscore.
//
// UBI-98's own per-provider-repo/per-service-package restructure,
// applied to Python in the session AFTER Go's own and TS's own
// (STATE.md): GeneratedFile (one flat file for a whole provider) is
// replaced by ResourceFile/PackageDoc/GeneratedRepo (one file per
// resource type, one __init__.py per service package -- Python's own
// real package marker, not a separate doc file, matching this
// codebase's own existing sdk/conformance/programs/py/generated/__init__.py
// precedent of using __init__.py for exactly this directory role). Like
// TS (see sdk/codegen/templates/ts's own package doc comment), Python
// modules give every FILE its own independent namespace, so UBI-96's
// original cross-resource collision bug (two different resource types'
// own declarations silently overwriting each other in ONE flat module,
// this package's own WORST failure mode of the three languages, no error
// at all) is now structurally impossible ACROSS files regardless of
// naming -- confirmed, not assumed, same as TS.
//
// A REAL Python-specific finding, checked explicitly rather than assumed
// safe by extrapolation from Go/TS: unlike Go (no collision) or TS (no
// restriction on directory names at all), Python service/module names
// MUST be valid Python identifiers to be importable via the standard
// `import x.y` dotted syntax this restructure's own generated code uses
// -- and "lambda" is BOTH a real Python keyword AND a real AWS service
// (aws_lambda_*, 20 real types), confirmed against the real
// hashicorp/aws@6.54.0 schema. See pyModuleIdent, below.
package pytmpl

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ubiquex/ubiquex/sdk/codegen/ir"
)

// pythonNamespaceRoot is the shared, real PEP 420 implicit namespace
// package every provider's Python bindings repo nests under -- "ubx",
// matching real, strong precedent for a multi-package namespace
// (google.cloud.*, azure.mgmt.*). GeneratedRepo deliberately never emits
// "ubx/__init__.py": a directory WITH an __init__.py is a regular
// package, ownable by exactly one distribution, which would make
// ubx-sdk-aws-py's own install permanently claim the "ubx" name and
// block ubx-sdk-google-py/-azure-py/-kubernetes-py from each
// independently contributing their own ubx.google/ubx.azure/
// ubx.kubernetes sibling later. "ubx/<ns>/__init__.py" (one level down,
// e.g. "ubx/aws/__init__.py") is a real, ordinary package and always
// gets one, same as every service package below it.
const pythonNamespaceRoot = "ubx"

// GeneratedRepo renders every file for one provider's repo-shaped Python
// output tree: a pyproject.toml stub (name "ubx-sdk-<shortName>", the
// real, already-published package name, unchanged by UBI-138 -- only
// its source location within the combined repo moves), one __init__.py
// per derived service package (ServicePackageDoc: the shared provenance
// banner plus a re-export line per resource type in that service), and
// one <local>.py per resource type (ResourceFile) inside its own
// service package. The re-export is what makes the real, final import
// `from ubx.<ns>.<service> import <Pascal>, <Pascal>Config` -- never the
// redundant `from ubx.<ns>.<service>.<local> import ...` stutter a bare
// PackageDoc-only __init__.py would require. Returns a map of path,
// relative to the repo root using "/" always -> file content -- mirrors
// sdk/codegen/templates/go's own GeneratedRepo exactly in shape.
//
// UBI-138: every returned path is now rooted under "sdk/python/" -- the
// real Pulumi-precedent sibling-subdirectory structure (see
// sdk/codegen/templates/go's own GeneratedRepo doc comment for the full
// account). pyproject.toml's own `[tool.setuptools.packages.find]`
// `where = ["."]` is relative to pyproject.toml's own location, not the
// repo root, so moving pyproject.toml and the `ubx/` tree under
// "sdk/python/" TOGETHER, preserving their relative position to each other,
// needs no further packaging-config change -- confirmed by reasoning
// through setuptools' own discovery semantics, re-verified with a real
// build in this migration's own end-to-end proof, not assumed correct
// from the reasoning alone. The published package's own internal
// namespace (`ubx.aws.iam`, PEP 420, no `ubx/__init__.py`) is
// unaffected -- it's a property of what pyproject.toml packages, never
// of pyproject.toml's own path within the source repo.
func GeneratedRepo(shortName, source, version string, types []*ir.ResourceType) (map[string]string, error) {
	sorted := make([]*ir.ResourceType, len(types))
	copy(sorted, types)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].WireType < sorted[j].WireType })

	type entry struct {
		rt       *ir.ResourceType
		local    string
		pascal   string
		fileStem string
	}
	byService := map[string][]entry{}
	var services []string
	for _, rt := range sorted {
		service, local, err := ir.ServiceAndLocalNameForType(rt)
		if err != nil {
			return nil, fmt.Errorf("sdk/codegen/templates/py: %w", err)
		}
		service = pyModuleIdent(service)
		local = pyModuleIdent(local)
		pascal, err := pascalCase(local)
		if err != nil {
			return nil, fmt.Errorf("sdk/codegen/templates/py: %s: local name %q: %w", rt.WireType, local, err)
		}
		if _, ok := byService[service]; !ok {
			services = append(services, service)
		}
		// fileStem is deliberately separate from local: local (the FULL,
		// untruncated name) is what pascal above -- and ResourceFile's own
		// internal pascalCase(e.local) below -- derive the exported class
		// name from; only the on-disk module name and the `from .X
		// import` line get the length cap, never the public identifier.
		byService[service] = append(byService[service], entry{rt, local, pascal, ir.FileStem(local)})
	}
	sort.Strings(services)

	// UBI-106: every service package nests under a namespace directory
	// (e.g. aws/iam/, never iam/ at the repo root) -- fixes a real
	// repo-browsing problem (200+ service directories at hashicorp/aws's
	// own repo root sorted ahead of README.md by GitHub's file browser).
	// pyproject.toml stays at the true root. Unlike Go/TS, this namespace
	// segment itself must be a valid Python identifier (`import
	// aws.iam.role` requires "aws" to parse as one) -- pyShortNameIdent
	// guards it the same way pyModuleIdent already guards service/local
	// names, PLUS a hyphen guard neither of those needs (shortName's own
	// pyproject.toml package NAME conventionally keeps hyphens, e.g.
	// "ubx-sdk-aws" -- fine there, a TOML string, but not here). Not
	// exercised by "aws" itself (no hyphen, not a keyword), but a real,
	// universal constraint for any future provider whose shortName has
	// one (e.g. a hypothetical "google-beta") -- guarded unconditionally
	// now, before Google's own repos exist, the same defensive-not-yet-
	// confirmed posture pyModuleIdent's own leading-digit guard already
	// established.
	ns := pyShortNameIdent(shortName)
	root := "sdk/python/" + pythonNamespaceRoot + "/" + ns
	files := map[string]string{
		"sdk/python/pyproject.toml": pyprojectTOML(shortName, source, version),
		root + "/__init__.py":       PackageDoc(source, version),
	}
	for _, service := range services {
		entries := byService[service]

		// UBI-108's own real, live-verified collision class hashicorp/aws
		// never surfaced but hashicorp/google@7.40.0 did (3 real hits,
		// migration/center_report+center_report_config among them,
		// reconfirmed live against hashicorp/google@7.42.0 this session):
		// a sibling resource in this SAME service whose own wire name is
		// exactly "<this>_config" (a real, independent resource, not a
		// nested block) has a Pascal name identical to this resource's
		// own auto-derived "<Pascal>Config" class -- which Python's new
		// re-export aggregation (ServicePackageDoc) would silently shadow,
		// exactly the class fromImportRe exists to catch. Mirrors
		// sdk/codegen/templates/go's own GeneratedRepo fix exactly: the
		// COLLIDING resource's own Config class gets a disambiguating
		// trailing underscore, never the sibling (whose own identity is a
		// real, independent resource and shouldn't be mangled).
		siblingPascalNames := map[string]bool{}
		for _, e := range entries {
			siblingPascalNames[e.pascal] = true
		}

		names := make([]exportedName, len(entries))
		for i, e := range entries {
			names[i] = exportedName{local: e.fileStem, pascal: e.pascal, config: e.pascal + "Config"}
			if siblingPascalNames[e.pascal+"Config"] {
				names[i].config = e.pascal + "Config_"
			}
		}
		files[root+"/"+service+"/__init__.py"] = ServicePackageDoc(source, version, names)
		for i, e := range entries {
			configOverride := ""
			if names[i].config != e.pascal+"Config" {
				configOverride = names[i].config
			}
			content, err := ResourceFile(e.local, e.rt, configOverride)
			if err != nil {
				return nil, fmt.Errorf("sdk/codegen/templates/py: %s: %w", e.rt.WireType, err)
			}
			path := root + "/" + service + "/" + e.fileStem + ".py"
			if _, exists := files[path]; exists {
				return nil, fmt.Errorf("sdk/codegen/templates/py: %s: generated path %q collides with an earlier resource type in this service", e.rt.WireType, path)
			}
			files[path] = content
		}
	}
	return files, nil
}

// pyShortNameIdent guards shortName for use as the namespace directory
// every service package nests under -- see GeneratedRepo's own comment
// above for why this needs a hyphen guard pyModuleIdent doesn't.
func pyShortNameIdent(shortName string) string {
	return pyModuleIdent(strings.ReplaceAll(shortName, "-", "_"))
}

// pyModuleIdent guards a derived service or local module name against
// Python's own real identifier rules -- required here in a way neither
// Go (a real language keyword collision, "default"/"main") nor TS (no
// restriction at all, verified live this session) needed identically:
// Python's own `import x.y` dotted syntax, and a directory acting as a
// real package, both require every path segment to be a valid Python
// identifier. Two real, distinct guards: (1) a Python keyword (checked
// against pythonKeywords, the same table pythonIdentifier already
// established for a colliding FIELD name -- "lambda" is the one real,
// live-verified hit: aws_lambda_* is a real 20-type AWS service, and
// "lambda" is a real Python keyword, so `import lambda.function` would
// be a SyntaxError); (2) a leading digit (a real, universal Python
// grammar rule -- `import 3thing` is invalid -- not confirmed to occur
// in hashicorp/aws@6.54.0's own real schema this session, but guarded
// unconditionally anyway since it's a hard language fact, not a
// speculative future-provider concern the way Go's own
// internal/vendor/testdated defensive guard was). Both resolved the same
// way pythonIdentifier already resolves a keyword-colliding field name:
// a trailing underscore.
func pyModuleIdent(name string) string {
	if pythonKeywords[name] {
		return name + "_"
	}
	if name != "" && name[0] >= '0' && name[0] <= '9' {
		return "_" + name
	}
	return name
}

// pyprojectTOML renders the repo-shaped tree's own manifest stub -- a
// real, consumer-ready shape, not consulted by anything this codebase's
// own hermetic verification actually reads (a real import resolves via
// PYTHONPATH directly against the on-disk tree, the same mechanism
// cli/sdk_test.go's own assertPyFileImports already uses). `ubx_sdk`
// pins to the real published PyPI package (UBI-107,
// https://pypi.org/project/ubx-sdk/) -- caret-equivalent range, matching
// @ubx/sdk's own `^0.1.0` JSR pin. The [build-system]/
// [tool.setuptools.packages.find] tables are what make "ubx" resolve as
// a real PEP 420 implicit namespace package rather than an ordinary one
// on a real `pip install` -- `namespaces = true` is the load-bearing
// line; `include` scopes discovery to this distribution's own "ubx*"
// tree instead of scanning the whole repo (dist/, .github/, ...).
func pyprojectTOML(shortName, source, version string) string {
	return fmt.Sprintf(`[project]
name = "ubx-sdk-%s"
version = "0.0.0"
description = "Generated Python bindings for %s@%s (ubx sdk gen). DO NOT EDIT."
dependencies = ["ubx_sdk>=0.1.0,<0.2.0"]

[build-system]
requires = ["setuptools>=61"]
build-backend = "setuptools.build_meta"

[tool.setuptools.packages.find]
where = ["."]
include = ["%s*"]
namespaces = true
`, shortName, source, version, pythonNamespaceRoot)
}

// PackageDoc renders one service package's own __init__.py -- Python's
// real package marker (required for the directory to be a regular,
// dotted-importable package), doing double duty here as the shared
// "generated, do not edit, committed to git" banner
// (docs/sdk.md's own "ubx sdk gen: local, pinned,
// offline-after-generation") plus SOURCE_PROVENANCE, declared exactly
// once per package -- never repeated (or, worse, silently redefined
// with no error at all, Python's own worst-of-three failure mode UBI-96
// first found) in every resource type's own file. Also the namespace
// root's own "ubx/<ns>/__init__.py" (unlike "ubx/__init__.py" itself,
// which GeneratedRepo deliberately never emits -- see
// pythonNamespaceRoot's own doc comment).
func PackageDoc(source, version string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Code generated by `ubx sdk gen` from %s@%s. DO NOT EDIT.\n", source, version)
	b.WriteString("# This file is committed to git like any other reviewable generated code\n")
	b.WriteString("# (docs/sdk.md — \"ubx sdk gen: local, pinned, offline-after-generation\") --\n")
	b.WriteString("# re-run `ubx sdk gen` to regenerate after a provider version bump.\n")
	fmt.Fprintf(&b, "SOURCE_PROVENANCE = {%q: %q, %q: %q}\n", "source", source, "version", version)
	return b.String()
}

// exportedName is one resource type's own re-export identity within a
// service package's __init__.py -- local (the module/file basename,
// already passed through pyModuleIdent), pascal (that same name's
// PascalCase class-name form, identical to what ResourceFile derives
// internally for the SAME resource, since both start from the same
// post-pyModuleIdent local name), and config (the Config dataclass's own
// name -- normally pascal+"Config", but a disambiguated pascal+"Config_"
// when GeneratedRepo detects the sibling-collision class its own doc
// comment describes).
type exportedName struct {
	local  string
	pascal string
	config string
}

// ServicePackageDoc renders one service package's own __init__.py:
// PackageDoc's existing provenance banner, plus one re-export line per
// resource type in this service -- `from .<local> import <Pascal>,
// <Pascal>Config` -- so the real, final import a consumer writes is
// `from ubx.<ns>.<service> import <Pascal>, <Pascal>Config`, never the
// redundant `from ubx.<ns>.<service>.<local> import ...` stutter the
// pre-restructure layout required. names must already be in a
// deterministic order (GeneratedRepo passes them in the same
// WireType-sorted order byService itself was built in).
//
// A real, new collision class this re-export aggregation introduces,
// structurally analogous to Go's own single-package-namespace problem
// (STATE.md's ubx-sdk-google-go `*_config` finding) even though Python
// still gives every FILE its own independent namespace: two DIFFERENT
// resource types in the SAME service whose local names pascalCase to
// the SAME identifier would silently shadow each other in this shared
// __init__.py. CheckNoDuplicateDeclarations' own fromImportRe now
// checks every re-export line for exactly this, uniformly with every
// other module-level declaration it already checks.
func ServicePackageDoc(source, version string, names []exportedName) string {
	var b strings.Builder
	b.WriteString(PackageDoc(source, version))
	if len(names) > 0 {
		b.WriteString("\n")
	}
	for _, n := range names {
		fmt.Fprintf(&b, "from .%s import %s, %s\n", n.local, n.pascal, n.config)
	}
	return b.String()
}

// ResourceFile renders one Python module for a single resource type.
// localWireName is ir.ServiceAndLocalName's own second return value for
// rt.WireType (already passed through pyModuleIdent by GeneratedRepo) --
// used ONLY to derive this file's own module name and (PascalCased)
// class names (the founder's own locked naming scheme, applied
// identically to Go/TS: `Repository`, never `AwsEcrRepository`, since
// the package already encodes provider+service). rt.WireType itself is
// rendered into the runtime ResourceBinding call completely unchanged.
//
// configTypeNameOverride mirrors sdk/codegen/templates/go's own
// ResourceFile parameter of the same name exactly (see GeneratedRepo's
// own doc comment for the real, live-verified collision class this
// resolves): empty string means "no collision, use the default
// <Pascal>Config"; a non-empty override (always <Pascal>Config_, a
// trailing underscore) is used instead.
func ResourceFile(localWireName string, rt *ir.ResourceType, configTypeNameOverride string) (string, error) {
	pascalName, err := pascalCase(localWireName)
	if err != nil {
		return "", fmt.Errorf("%s: local name %q: %w", rt.WireType, localWireName, err)
	}
	configTypeName := pascalName + "Config"
	if configTypeNameOverride != "" {
		configTypeName = configTypeNameOverride
	}

	// Real, deliberate, checkpoint-10 change: mirrors
	// sdk/codegen/templates/go's own identical fix exactly -- nested
	// dataclasses are now collected for EVERY real field, not just
	// settable ones, so Attrs (below) has a real declared type for a
	// computed-only field's own nested shape too.
	r := &resourceRenderer{pascalName: pascalName}
	for _, f := range rt.Fields {
		if err := r.collectNestedDataclasses(f, pascalName); err != nil {
			return "", err
		}
	}

	// The runtime descriptor's own body is built into a SEPARATE buffer
	// first -- mirrors sdk/codegen/templates/go and .../ts's own
	// ResourceFile exactly: r.fieldSpecLiteral (called below) can hoist a
	// new shared FieldMap dict as a side effect (r.fieldMapDecls), which
	// must be written to the file BEFORE this descriptor references it
	// by name.
	var descriptor strings.Builder
	fmt.Fprintf(&descriptor, "%s = ubx.ResourceBinding(\n", pascalName)
	fmt.Fprintf(&descriptor, "    wire_type=%q,\n", rt.WireType)
	descriptor.WriteString("    fields={\n")
	for _, f := range rt.Fields {
		if !fieldIsSettable(f) {
			continue
		}
		idiomatic, err := pythonIdentifier(f.WireName)
		if err != nil {
			return "", err
		}
		spec, err := r.fieldSpecLiteral(f, 2)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&descriptor, "        %q: %s,\n", idiomatic, spec)
	}
	descriptor.WriteString("    },\n")
	descriptor.WriteString(")\n")

	var b strings.Builder
	fmt.Fprintf(&b, "# Code generated by `ubx sdk gen` from %s. DO NOT EDIT.\n", rt.WireType)
	b.WriteString("from __future__ import annotations\n\n")
	b.WriteString("import dataclasses\n")
	b.WriteString("from typing import Any\n\n")
	b.WriteString("import ubx_sdk as ubx\n\n")

	for _, decl := range r.nestedDecls {
		b.WriteString(decl)
		b.WriteString("\n")
	}
	for _, decl := range r.fieldMapDecls {
		b.WriteString(decl)
		b.WriteString("\n")
	}

	// Config dataclass: settable fields only (same fieldIsSettable rule
	// sdk/codegen/templates/ts and .../go use).
	fmt.Fprintf(&b, "@dataclasses.dataclass\nclass %s:\n", configTypeName)
	var anyField bool
	for _, f := range rt.Fields {
		if !fieldIsSettable(f) {
			continue
		}
		idiomatic, err := pythonIdentifier(f.WireName)
		if err != nil {
			return "", err
		}
		anyField = true
		b.WriteString(fieldDocComment(f, "    "))
		fmt.Fprintf(&b, "    %s: Any = None\n", idiomatic)
	}
	if !anyField {
		b.WriteString("    pass\n")
	}
	b.WriteString("\n")

	// Attrs dataclass: EVERY real field, settable or computed-only --
	// mirrors sdk/codegen/templates/go's own identical XxxAttrs struct
	// (see that function's own doc comment for the full real finding
	// and scope: a real, declared type giving every field's own real
	// description a visible home, not yet wired to any real runtime
	// computed-attribute-reading mechanism).
	attrsTypeName := pascalName + "Attrs"
	fmt.Fprintf(&b, "@dataclasses.dataclass\nclass %s:\n", attrsTypeName)
	var anyAttrField bool
	for _, f := range rt.Fields {
		idiomatic, err := pythonIdentifier(f.WireName)
		if err != nil {
			return "", err
		}
		anyAttrField = true
		b.WriteString(fieldDocComment(f, "    "))
		fmt.Fprintf(&b, "    %s: Any = None\n", idiomatic)
	}
	if !anyAttrField {
		b.WriteString("    pass\n")
	}
	b.WriteString("\n")

	// The runtime descriptor -- wire_type carries the REAL, full wire
	// type string, always, regardless of what the class name above
	// dropped.
	b.WriteString(descriptor.String())

	return b.String(), nil
}

// fieldIsSettable mirrors sdk/codegen/templates/ts and .../go's own
// function of the same name exactly.
func fieldIsSettable(f ir.Field) bool {
	return f.Required || f.Optional || !f.Computed
}

// fieldDocComment renders f.Description as a real, plain "# ..." comment
// line above the field, indented with pad -- Python has no per-field
// docstring convention a dataclass attribute actually supports at
// runtime, so a comment line is the honest, visible choice (mirrors
// sdk/codegen/templates/go's own fieldDocComment exactly; see that
// function's doc comment for why AI-inferred gets a visible suffix and
// DescriptionSourceNone gets no comment at all).
func fieldDocComment(f ir.Field, pad string) string {
	desc := oneLineDescription(f.Description)
	switch f.DescriptionSource {
	case ir.DescriptionSourceModel:
		return pad + "# " + desc + "\n"
	case ir.DescriptionSourceAIInferred:
		return pad + "# " + desc + " (AI-inferred)\n"
	default:
		return ""
	}
}

func oneLineDescription(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// resourceRenderer accumulates one resource type's own nested Config
// dataclass declarations (KindObject fields, at any depth), keyed by a
// deterministic name derived from the full field path from the
// resource's own local PascalCase name (UBI-98: the caller now passes
// this package's own per-type localWireName-derived pascalName, never
// the full wire-type-derived one UBI-96 used, and every declaration this
// renderer emits lives in exactly one file -- Python modules give each
// file its own independent namespace, so cross-RESOURCE collision is
// structurally impossible here regardless of naming, unlike Go; see this
// package's own doc comment for the full account). Within ONE resource's
// own tree, the identical UBI-96 "_" join (pathPrefix + "_" + fieldPascal)
// still applies so two DIFFERENT nested paths in the same file never
// share a bare class name.
//
// A SEPARATE problem, found live this session against the SAME real
// schema Go's own aws_wafv2_web_acl_rule finding came from (STATE.md,
// UBI-98's Go session): AWS's own recursive schema shapes physically
// repeat an IDENTICAL nested block shape at every depth level a real
// provider schema can express recursion to at all. Confirmed empirically
// NOT to crash a real Python import the way it crashed the Go compiler
// (aws_wafv2_web_acl_rule alone -- 21.2MB/~258,000 lines, 21,026
// dataclasses, of naively-unrolled Python -- imported clean in ~4.4 real
// seconds) -- so this dedup is NOT required to avoid a crash for Python
// either, exactly like TS. Applied anyway, for the identical reason: a
// single generated file that large is still a real reviewability/git-
// diff/repo-bloat problem, and the fix is unconditionally safe here for
// the identical reason it was safe in Go/TS: confirmed by reading
// sdk/py/ubx_sdk's own runtime serializer before relying on this (it
// reads a Config value's own dataclass FIELD NAMES via
// dataclasses.fields()/getattr, matched against the runtime FieldMap
// dict's own keys, never against a nested dataclass's own declared CLASS
// NAME) -- two structurally-identical shapes sharing one class name
// changes nothing about runtime behavior. seenShapes closes the
// (cosmetic) dataclass-declaration half; seenFieldMapVars/fieldMapDecls
// (see objectFieldMapRef, below) closes the OTHER half, which -- exactly
// as in Go/TS -- is not cosmetic: the runtime fields={...} dict
// ResourceBinding is built from IS what the evaluator actually reads.
type resourceRenderer struct {
	pascalName       string
	nestedDecls      []string
	seenNames        map[string]bool
	seenShapes       map[string]string
	seenFieldMapVars map[string]string
	fieldMapDecls    []string
}

func (r *resourceRenderer) collectNestedDataclasses(f ir.Field, pathPrefix string) error {
	_, err := r.pyFieldMeta(f.Type, pathPrefix, f.WireName)
	return err
}

// pyFieldMeta records a new nested Config dataclass declaration (once
// per unique STRUCTURAL SHAPE, not merely once per unique path -- see
// resourceRenderer's own doc comment) as a side effect whenever it
// encounters a KindObject.
func (r *resourceRenderer) pyFieldMeta(t ir.TypeRef, pathPrefix, wireName string) (string, error) {
	switch t.Kind {
	case ir.KindScalar:
		return "", nil
	case ir.KindList, ir.KindSet, ir.KindMap:
		return r.pyFieldMeta(*t.Element, pathPrefix, wireName)
	case ir.KindObject:
		shape, err := nestedShapeSignature(t)
		if err != nil {
			return "", err
		}
		if r.seenShapes == nil {
			r.seenShapes = map[string]string{}
		}
		if existing, ok := r.seenShapes[shape]; ok {
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
			return "", fmt.Errorf("field %q: internal error: nested dataclass name %q already used for a DIFFERENT shape (path collision, not a shape repeat)", wireName, name)
		}
		r.seenNames[name] = true
		r.seenShapes[shape] = name

		var body strings.Builder
		fmt.Fprintf(&body, "@dataclasses.dataclass\nclass %s:\n", name)
		var anyField bool
		for _, inner := range t.Object {
			innerIdiomatic, err := pythonIdentifier(inner.WireName)
			if err != nil {
				return "", err
			}
			if _, err := r.pyFieldMeta(inner.Type, name, inner.WireName); err != nil {
				return "", err
			}
			anyField = true
			body.WriteString(fieldDocComment(inner, "    "))
			fmt.Fprintf(&body, "    %s: Any = None\n", innerIdiomatic)
		}
		if !anyField {
			body.WriteString("    pass\n")
		}
		r.nestedDecls = append(r.nestedDecls, body.String())
		return name, nil
	default:
		return "", fmt.Errorf("field %q: unrecognized type kind %v", wireName, t.Kind)
	}
}

// fieldSpecLiteral renders one settable field's own runtime
// ubx.FieldSpec(...) call literal, mirroring sdk/codegen/templates/go
// and .../ts's own fieldSpecLiteral exactly. Now a resourceRenderer
// method: the object/collection-of-object branches route through
// r.objectFieldMapRef, which may hoist a shared module-level FieldMap
// dict as a side effect.
func (r *resourceRenderer) fieldSpecLiteral(f ir.Field, indent int) (string, error) {
	pad := strings.Repeat("    ", indent)
	innerPad := strings.Repeat("    ", indent+1)
	switch f.Type.Kind {
	case ir.KindScalar:
		return fmt.Sprintf("ubx.FieldSpec(wire_name=%q)", f.WireName), nil
	case ir.KindList, ir.KindSet, ir.KindMap:
		if f.Type.Element.Kind != ir.KindObject {
			return fmt.Sprintf("ubx.FieldSpec(wire_name=%q)", f.WireName), nil
		}
		kind := map[ir.TypeKind]string{ir.KindList: "list", ir.KindSet: "set", ir.KindMap: "map"}[f.Type.Kind]
		fieldsRef, err := r.objectFieldMapRef(f.Type.Element.Object)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("ubx.FieldSpec(\n%swire_name=%q,\n%skind=%q,\n%sfields=%s,\n%s)",
			innerPad, f.WireName, innerPad, kind, innerPad, fieldsRef, pad), nil
	case ir.KindObject:
		fieldsRef, err := r.objectFieldMapRef(f.Type.Object)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("ubx.FieldSpec(\n%swire_name=%q,\n%skind=%q,\n%sfields=%s,\n%s)",
			innerPad, f.WireName, innerPad, "object", innerPad, fieldsRef, pad), nil
	default:
		return "", fmt.Errorf("field %q: unrecognized type kind %v", f.WireName, f.Type.Kind)
	}
}

// objectFieldMapRef returns an EXPRESSION for one nested object's own
// fields dict -- either a reference to an already-hoisted shared
// module-level variable (a shape this resource's own tree has already
// rendered once, anywhere in it) or, the first time a given shape is
// seen, a fresh inline dict literal that ALSO gets hoisted into a new
// shared variable (recorded in r.fieldMapDecls) so every LATER
// occurrence of the identical shape references it instead of
// re-inlining. Keyed by nestedShapeSignature -- the same structural
// fingerprint r.pyFieldMeta already uses for the (cosmetic) nested
// dataclass declarations, deliberately: the base name for a new shared
// variable reuses whatever name pyFieldMeta already minted for the
// identical shape during ResourceFile's own earlier
// collectNestedDataclasses pre-pass. A single leading underscore marks
// it as module-internal (PEP 8's own convention), never part of this
// file's public/reviewed API surface (Config/the resource's own
// ResourceBinding).
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
	varName := "_" + base + "Fields"
	r.seenFieldMapVars[shape] = varName

	lit, err := r.objectFieldMapLiteral(fields, 0)
	if err != nil {
		return "", err
	}
	r.fieldMapDecls = append(r.fieldMapDecls, fmt.Sprintf("%s = %s\n", varName, lit))
	return varName, nil
}

// objectFieldMapLiteral renders an INLINE FieldMap dict literal for one
// nested object's own fields -- every field of a nested object is
// "settable" from the config's own point of view (mirrors
// sdk/codegen/templates/go and .../ts's own objectFieldMapLiteral).
// Called from exactly one place now (objectFieldMapRef, the first time a
// given shape is seen).
func (r *resourceRenderer) objectFieldMapLiteral(fields []ir.Field, indent int) (string, error) {
	pad := strings.Repeat("    ", indent)
	innerPad := strings.Repeat("    ", indent+1)
	var b strings.Builder
	b.WriteString("{\n")
	for _, f := range fields {
		idiomatic, err := pythonIdentifier(f.WireName)
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

// nestedShapeSignature computes a canonical, structure-only fingerprint
// of one TypeRef's own recursive shape -- mirrors
// sdk/codegen/templates/go and .../ts's own function of the same name
// exactly (kept as an independent copy in each template package,
// matching this project's own established "no per-language identifier
// convention lives in ir" posture, sdk/codegen/ir's own Field doc
// comment).
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

// pythonIdentifier turns a provider wire name (snake_case, verbatim from
// a real schema -- e.g. "instance_class") into a Config dataclass's own
// field name. Unlike TS's camelCase or Go's PascalCase conversion, this
// is the identity function for every real wire name this project has
// ever generated against -- already lowercase-with-underscores, already
// a valid Python identifier -- except for the one real edge case a wire
// name could hit that TS/Go never need to guard against: colliding with
// a Python keyword (e.g. a field literally named "class" or "in"),
// resolved with a trailing underscore, the same convention generated
// protobuf/thrift Python bindings already use.
func pythonIdentifier(wireName string) (string, error) {
	parts, err := splitWireName(wireName)
	if err != nil {
		return "", err
	}
	joined := strings.Join(parts, "_")
	if pythonKeywords[joined] {
		return joined + "_", nil
	}
	return joined, nil
}

// pascalCase converts a provider wire name into a Python class-name
// identifier (PascalCase, the conventional Python class-naming style,
// PEP 8) -- used only for generated dataclass/ResourceBinding names,
// never for field names (see pythonIdentifier, above).
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

// pythonKeywords is the lowercase-only subset of Python 3's reserved-word
// set (keyword.kwlist) that a real wire name -- or, as of UBI-98, a real
// derived service/local module name -- could ever actually collide
// with -- splitWireName only ever accepts lowercase ascii, so the
// capitalized reserved words (False/None/True) can never match a real
// wire name and are omitted rather than listed misleadingly.
var pythonKeywords = map[string]bool{
	"and": true, "as": true, "assert": true, "async": true, "await": true,
	"break": true, "class": true, "continue": true, "def": true, "del": true,
	"elif": true, "else": true, "except": true, "finally": true, "for": true,
	"from": true, "global": true, "if": true, "import": true, "in": true,
	"is": true, "lambda": true, "nonlocal": true, "not": true, "or": true,
	"pass": true, "raise": true, "return": true, "try": true, "while": true,
	"with": true, "yield": true,
}
