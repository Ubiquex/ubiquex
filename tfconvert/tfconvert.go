// Package tfconvert implements `ubx blueprint convert --from-terraform`
// (UBI-125): deterministic, no-AI translation of a real Terraform module's
// HCL directly into a blueprint (docs/blueprint.md's own "Terraform module
// conversion" section has the full design). Named for the CLI verb it
// implements one-to-one (CLAUDE.md's package-naming convention --
// writeback/ implements `ubx writeback` the same way), using "tf" (not
// "terraform", not "hcl") to match this project's own established
// abbreviation for the same external system elsewhere (provider/tfplugin5).
//
// The core insight this package leans on (recorded on UBI-125's own
// ticket): HCL's own expression primitives -- a `var.X` reference, a
// `resource_type.name.attr` reference, a string template mixing literal
// text with either -- already correspond EXACTLY to the wire primitives a
// resolved blueprint draft already uses (blueprint/decode.go's own
// `{param_name}` token grammar and `{"$ref":{"to":...}}` marker). Where
// they DO correspond, translation is genuinely mechanical, no LLM
// involved, matching this project's own established discipline for
// schema-to-code translation (CLAUDE.md, sdk/codegen). Where they don't --
// a conditional expression, an unported built-in function, a data source,
// a complex `locals` chain -- this package never guesses: it records a
// core.Question (the SAME mechanism the intent provider already uses for
// genuine ambiguity, reused verbatim, not reinvented) and declines the
// one affected attribute/resource/output, continuing with everything else
// mechanically translatable rather than failing the whole conversion
// opaquely (mirroring writeback/writeback.go's own established "decline
// per-attribute, not the whole document" precedent).
//
// Convert stops at the SAME point `ubx blueprint build`'s own AI draft
// stops at -- a Params/Outputs/*resolver.IntentFile triple ready for
// blueprint.GenerateGo/TS/Python -- but reaches it without ever calling
// the intent-provider pipeline. See the package's own "Ubxfile
// resources: after conversion" doc comment on Result below for the one
// real, deliberate consequence of skipping that pipeline entirely.
package tfconvert

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/ubiquex/ubiquex/blueprint"
	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
)

// RequiredProvider is one provider entry a module's own `terraform {
// required_providers { ... } }` block declares -- surfaced for item 7's
// own "confirm this resolves against the SAME provider version the
// original module targeted" conformance check, never itself consulted
// during translation (a resource's own wire attribute names are read
// directly from the HCL, not derived from a schema fetch, mirroring
// `ubx blueprint build`'s own "no provider schema fetch, deliberately"
// precedent, docs/blueprint.md).
type RequiredProvider struct {
	LocalName string // the block's own attribute name, e.g. "aws"
	Source    string // e.g. "hashicorp/aws"
	Version   string // the module's own raw version constraint, e.g. "~> 5.68" -- never parsed/resolved here
}

// Result is everything Convert deterministically derived from a real
// Terraform module directory.
//
// Ubxfile resources: after conversion -- a real, deliberate design
// boundary, not an oversight: Convert's own Intent is already a fully
// resolved draft (a real `*resolver.IntentFile`, ready for
// blueprint.GenerateGo/TS/Python directly), never assembled from prose an
// intent-provider adapter would re-derive. `ubx blueprint convert` writes
// an Ubxfile whose resources: field holds Summary below -- a short,
// deterministic, human-readable description of what was converted, for a
// human reading the directory -- but that text is documentation ONLY. If
// a later `ubx blueprint build` is run against a converted directory, it
// re-drafts resources: through the intent-provider pipeline (AI) exactly
// like any other Ubxfile, and there is no guarantee that re-draft
// reproduces this package's own deterministic translation byte-for-byte
// -- the whole point of `convert` is to skip that step, so ITS OWN output
// (the go/ts/py packages `ubx blueprint convert` writes directly,
// alongside the Ubxfile) is the source of truth for a converted
// blueprint, not a seed for a later AI rebuild. Named here explicitly,
// per this project's own "never contradict a doc silently" discipline
// (CLAUDE.md), because it's a genuine, load-bearing tension between two
// features (`build`'s "resources: is always re-drafted" contract and
// `convert`'s own "no AI, ever" contract) rather than a corner cut.
type Result struct {
	Params            []blueprint.Param
	Outputs           []blueprint.Output
	Intent            *resolver.IntentFile
	Questions         []core.Question
	Summary           string
	RequiredProviders []RequiredProvider

	// SkippedResources names every resource block Convert declined to
	// translate at all (an unrecognized count/for_each shape -- see
	// resources.go's own convertResource) -- surfaced separately from
	// Questions purely for CLI reporting convenience (every skipped
	// resource also has a real core.Question recorded in Intent.Intent.
	// Questions, this is not additional information, just an index into
	// it a caller doesn't have to re-derive by re-scanning Questions).
	SkippedResources []string
}

// hclModule is every relevant block collected from every *.tf file in one
// module directory, before any translation happens -- a plain data
// carrier, not itself an evaluator.
type hclModule struct {
	dir       string
	variables []*hclsyntax.Block
	resources []*hclsyntax.Block
	outputs   []*hclsyntax.Block
	locals    []*hclsyntax.Block // each is one "locals { ... }" block; Terraform allows more than one per module
	providers []*hclsyntax.Block // "terraform" blocks (required_providers lives nested inside)
	src       map[string][]byte  // filename -> raw file content, for exprText's own error-message rendering
}

// Convert deterministically translates the real Terraform module at dir
// (every top-level *.tf file, non-recursive -- Terraform's own single-
// directory-per-module convention, matching writeback.FindAndApply's own
// glob precedent) into a Result. Never returns an error for an
// individual unsupported HCL construct -- that becomes a core.Question
// instead (this package's own central design decision, see the package
// doc comment) -- Convert's own error return is reserved for something a
// human must fix before conversion can proceed AT ALL (the directory
// doesn't parse as valid HCL, a variable/resource/output block is
// malformed beyond what a Question can describe usefully).
//
// blueprintName is the FINAL blueprint's own name (the --out directory's
// own basename, `ubx blueprint convert`'s own caller) -- baked into
// every $ref address Convert produces and into the returned Intent's own
// Stack field, exactly matching draftBlueprint's identical convention
// (cli/blueprint.go: "stack" is the built directory's basename, never
// the source module's own directory name) -- decodeBlueprint hard-
// requires an address's own stack segment to equal IntentFile.Stack
// exactly (blueprint/decode.go's resolveAddress), so this must be the
// directory blueprint.GenerateGo/TS/Python will actually be invoked
// with, not wherever the original .tf module happens to live on disk.
func Convert(dir, blueprintName string) (*Result, error) {
	mod, err := loadModule(dir)
	if err != nil {
		return nil, err
	}

	stack := blueprintName
	c := &converter{
		mod:           mod,
		stack:         stack,
		resourceAddrs: map[string]bool{},
		resourceInfo:  map[string]resourceInfo{},
		localCache:    map[string]localResult{},
	}

	for _, rb := range mod.resources {
		if len(rb.Labels) != 2 {
			return nil, fmt.Errorf("tfconvert: %s: resource block wants exactly 2 labels (type, name), got %d", rb.TypeRange.Filename, len(rb.Labels))
		}
		c.resourceAddrs[rb.Labels[0]+"."+rb.Labels[1]] = true
	}

	if err := c.convertParams(); err != nil {
		return nil, err
	}
	c.convertProviders()
	c.convertResources()
	c.convertOutputs()

	intent := &resolver.IntentFile{
		SchemaVersion: 1,
		Kind:          resolver.IntentFileKind,
		Stack:         stack,
		Intent: core.Intent{
			Summary:   c.summary(),
			Questions: c.questions,
		},
		Resources: c.resources,
	}

	return &Result{
		Params:            c.params,
		Outputs:           c.outputs,
		Intent:            intent,
		Questions:         c.questions,
		Summary:           c.summary(),
		RequiredProviders: c.providers,
		SkippedResources:  c.skipped,
	}, nil
}

// loadModule parses every top-level *.tf file in dir and buckets every
// block by type -- sorted by filename first, so two runs over the
// identical directory always walk blocks in the identical order
// regardless of the host filesystem's own directory-listing order
// (determinism, CLAUDE.md).
func loadModule(dir string) (*hclModule, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.tf"))
	if err != nil {
		return nil, fmt.Errorf("tfconvert: glob %s: %w", dir, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("tfconvert: no .tf files found in %s", dir)
	}
	sort.Strings(files)

	mod := &hclModule{dir: dir, src: map[string][]byte{}}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("tfconvert: read %s: %w", f, err)
		}
		mod.src[f] = src
		hf, diags := hclsyntax.ParseConfig(src, f, hcl.InitialPos)
		if diags.HasErrors() {
			return nil, fmt.Errorf("tfconvert: parse %s: %w", f, diags)
		}
		body, ok := hf.Body.(*hclsyntax.Body)
		if !ok {
			return nil, fmt.Errorf("tfconvert: %s: unexpected body type %T", f, hf.Body)
		}
		for _, block := range body.Blocks {
			switch block.Type {
			case "variable":
				mod.variables = append(mod.variables, block)
			case "resource":
				mod.resources = append(mod.resources, block)
			case "output":
				mod.outputs = append(mod.outputs, block)
			case "locals":
				mod.locals = append(mod.locals, block)
			case "terraform":
				mod.providers = append(mod.providers, block)
			// "module", "data", "provider" blocks are real, named,
			// out-of-scope for this ticket (docs/blueprint.md's own
			// "Terraform module conversion" section names each
			// explicitly) -- silently skipped at the LOADING stage,
			// never producing a Question here: a Question fires only
			// where a REFERENCE to one of these (e.g. a resource
			// attribute reading `data.aws_ami.x.id`) is actually
			// encountered during translation (expr.go), not merely for
			// a data/module/provider block existing unreferenced in the
			// module.
			default:
			}
		}
	}
	return mod, nil
}

// converter threads every piece of state one Convert call needs across
// params.go/resources.go/outputs.go/providers.go/expr.go -- a single
// unexported struct (mirroring blueprint's own goGenerator precedent,
// gogen.go) rather than passing six separate maps through every function.
type converter struct {
	mod   *hclModule
	stack string // this blueprint's own Stack -- the built directory's own basename, matching draftBlueprint's identical convention (cli/blueprint.go)

	params            []blueprint.Param
	paramType         map[string]blueprint.ParamType
	unsupportedParams map[string]bool // param name -> declared but dropped (unsupported type), so a later reference becomes its own Question

	resourceAddrs map[string]bool         // "type.name" -> declared in this module
	resourceInfo  map[string]resourceInfo // "type.name" -> translation outcome, populated by resources.go BEFORE any Config field is translated (so a forward OR backward cross-resource reference resolves identically)

	resources  []resolver.ResourceIntent
	skipped    []string
	hasForEach bool // true once any non-skipped resource carries a for_each (set during convertResources) -- decodeBlueprint's own permanent boundary is blanket, not per-output (blueprint/decode.go): a blueprint with ANY for_each resource can't declare outputs: AT ALL, even one targeting a different, ordinary resource

	outputs []blueprint.Output

	providers []RequiredProvider

	questions []core.Question

	localCache map[string]localResult
	localStack map[string]bool // cycle guard, "name" -> currently being resolved
}

func (c *converter) addQuestion(text string, affects ...string) {
	c.questions = append(c.questions, core.Question{Text: text, Affects: affects})
}

func (c *converter) summary() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Converted mechanically (ubx blueprint convert, no AI) from the Terraform module at %s. ", c.mod.dir))
	fmt.Fprintf(&b, "%d resource(s)", len(c.resources))
	if len(c.skipped) > 0 {
		fmt.Fprintf(&b, " (%d skipped -- see questions)", len(c.skipped))
	}
	b.WriteString(".")
	return b.String()
}
