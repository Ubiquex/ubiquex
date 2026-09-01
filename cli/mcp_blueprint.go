// cli/mcp_blueprint.go is UBI-223's own MCP surface for blueprint
// authoring: five independent tools (draft_ubxfile, validate_ubxfile,
// build_blueprint, list_blueprints, describe_blueprint), each callable
// standalone, in whatever order a conversation takes -- never a fixed
// pipeline.
//
// push_blueprint is deliberately NOT one of them. Publishing a blueprint
// to a registry is the same kind of act ubx_why/ubx_status/ubx_scan's own
// package doc comment already draws the line around: irreversible, and
// the one step other stacks actually consume. The existing boundary-by-
// omission convention (docs/architecture.md -- "Boundary by omission:
// signatures and mutations are human acts") doesn't gate an irreversible
// action with a runtime check; it omits it from tool registration
// entirely. push_blueprint gets the identical treatment -- it stays a
// CLI-only verb, "ubx blueprint push", run by a human directly.
//
// No tool here writes to a repository, at all -- no opt-in either.
// build_blueprint already computes its own output as an in-memory
// map[string]string before ubx blueprint build's own CLI RunE ever
// touches disk (cli/blueprint.go's allFiles); this returns that same map
// as content instead, the same posture the three existing tools already
// hold ("it never writes to a live resource"). An earlier version of
// this tool had an opt-in out_dir field, mirroring ubx_scan's own `out`
// field (cli/mcp.go) -- removed on review: ubx_scan's out writes one
// inert proposal file nobody consumes until a human runs ubx accept on
// it by hand, while out_dir would have written the actual generated
// source tree, the real deliverable, directly into a caller-given
// directory -- exactly the write this design ruled out, not an
// equivalent-risk mirror of it. The calling agent already has its own
// file tools for the cases where writing the result to disk is
// genuinely wanted.
//
// draft_ubxfile does not invoke a second LLM. UBI-224 removed the
// intent-provider package this ticket's own "second LLM" question
// assumed still existed -- there is nothing left to invoke. The
// calling agent is the only intelligence in this loop; draft_ubxfile's
// own job is deterministic assembly (build a syntactically valid
// Ubxfile + resources.json from pieces the agent already decided), never
// interpretation of free text.
//
// validate_ubxfile and build_blueprint share ONE implementation for
// "is this Ubxfile structurally valid" -- blueprint.Validate
// (blueprint/ubxfile.go), the same parse-and-decode step every language
// generator already runs internally before its own codegen. Two
// implementations of the same validation is the shape that caused
// UBI-197 and UBI-233; this deliberately isn't a third.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ubiquex/ubiquex/blueprint"
)

// blueprintParamSpec/blueprintOutputSpec are draft_ubxfile's own input
// shape for params:/outputs: -- structured JSON the calling agent
// already decided, never free text this tool interprets. Assembled into
// the real "name: type, required"/"name: type, default X" grammar
// blueprint.ParseUbxfile expects (parseParamSpec, blueprint/ubxfile.go),
// so the agent never needs to know that grammar itself.
type blueprintParamSpec struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
	Default  any    `json:"default,omitempty"`
}

type blueprintOutputSpec struct {
	Name   string `json:"name"`
	Target string `json:"target"`
}

// assembleUbxfileYAML builds a real Ubxfile's own text from already-
// decided pieces -- mechanical string assembly, never interpretation.
// resourcesFile is the literal resources: value written (always
// "resources.json", the split form the real fixtures already use,
// docs/blueprint.md), never inline JSON -- keeping the two pieces
// separate is what makes build_blueprint/validate_ubxfile's own
// returned content reviewable as two distinct, readable files rather
// than one YAML blob with an embedded JSON block scalar.
func assembleUbxfileYAML(lang string, params []blueprintParamSpec, outputs []blueprintOutputSpec) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "lang: %s\n", lang)
	if len(params) > 0 {
		b.WriteString("params:\n")
		for _, p := range params {
			var spec string
			switch {
			case p.Required:
				spec = fmt.Sprintf("%s, required", p.Type)
			case p.Default != nil:
				spec = fmt.Sprintf("%s, default %v", p.Type, p.Default)
			default:
				return "", fmt.Errorf("param %q: needs either required=true or a default value", p.Name)
			}
			fmt.Fprintf(&b, "  %s: %s\n", p.Name, spec)
		}
	}
	b.WriteString("resources: resources.json\n")
	if len(outputs) > 0 {
		b.WriteString("outputs:\n")
		for _, o := range outputs {
			fmt.Fprintf(&b, "  %s: %s\n", o.Name, o.Target)
		}
	}
	return b.String(), nil
}

// assembleResourcesJSON wraps an already-decided resources array (the
// calling agent's own JSON, one object per resource matching
// resolver.ResourceIntent's wire shape) into the full ubx:intent/v1
// envelope blueprint.Validate expects -- the SAME wrapping "ubx resolve
// --from-code --out resources.json" already produces, assembled here
// instead of resolved, since a blueprint's own resources: is pre-
// resolved by the time it's authored (blueprint/ubxfile.go's own
// Resources doc comment).
func assembleResourcesJSON(stack, summary, resourcesArrayJSON string) (string, error) {
	var resources json.RawMessage
	trimmed := strings.TrimSpace(resourcesArrayJSON)
	if trimmed == "" {
		trimmed = "[]"
	}
	resources = json.RawMessage(trimmed)
	if !json.Valid(resources) {
		return "", fmt.Errorf("resources is not valid JSON")
	}
	envelope := map[string]any{
		"schema_version": 1,
		"kind":           "ubx:intent/v1",
		"stack":          stack,
		"intent":         map[string]any{"summary": summary},
		"resources":      resources,
	}
	out, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return "", fmt.Errorf("assemble resources.json: %w", err)
	}
	return string(out), nil
}

// writeTempBlueprintDir writes ubxfileYAML + resourcesJSON into a fresh
// OS temp directory (os.MkdirTemp, never the caller's own working tree
// or repository) and returns it plus a cleanup func -- how every MCP
// tool below validates/builds inline content through the exact same
// on-disk code path (blueprint.ParseUbxfile, blueprint.Validate) a real
// checked-in Ubxfile uses, without ever touching a real repo. This is
// the one place content is written to disk at all in this file, and
// it's gone (os.RemoveAll, deferred by every caller) before the tool
// call returns.
func writeTempBlueprintDir(ubxfileYAML, resourcesJSON string) (dir string, cleanup func(), err error) {
	dir, err = os.MkdirTemp("", "ubx-mcp-blueprint-*")
	if err != nil {
		return "", nil, fmt.Errorf("create scratch directory: %w", err)
	}
	cleanup = func() { os.RemoveAll(dir) }
	if err := os.WriteFile(filepath.Join(dir, blueprint.UbxfileName), []byte(ubxfileYAML), 0o644); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("write Ubxfile: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "resources.json"), []byte(resourcesJSON), 0o644); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("write resources.json: %w", err)
	}
	return dir, cleanup, nil
}

// resolveBlueprintDir is shared by validate_ubxfile and build_blueprint:
// either dir already names a real, existing directory with a real
// Ubxfile (the "I already have one checked in" flow), or ubxfileYAML/
// resourcesJSON carry inline content not yet saved anywhere (the "I just
// drafted this" flow) -- exactly one of the two is expected. Inline
// content is written to a throwaway temp directory (never the caller's
// repo) so both flows converge on the identical blueprint.Validate call
// below them.
func resolveBlueprintDir(dir, ubxfileYAML, resourcesJSON string) (resolvedDir string, cleanup func(), err error) {
	if dir != "" {
		return dir, func() {}, nil
	}
	if strings.TrimSpace(ubxfileYAML) == "" {
		return "", nil, fmt.Errorf("either dir (an existing Ubxfile directory) or ubxfile (inline Ubxfile text) is required")
	}
	if strings.TrimSpace(resourcesJSON) == "" {
		return "", nil, fmt.Errorf("resources is required alongside inline ubxfile content")
	}
	return writeTempBlueprintDir(ubxfileYAML, resourcesJSON)
}

func paramsToSpecs(params []blueprint.Param) []map[string]any {
	out := make([]map[string]any, 0, len(params))
	for _, p := range params {
		entry := map[string]any{"name": p.Name, "type": string(p.Type), "required": p.Required}
		if !p.Required {
			entry["default"] = p.Default
		}
		out = append(out, entry)
	}
	return out
}

func outputsToSpecs(outputs []blueprint.Output) []map[string]any {
	out := make([]map[string]any, 0, len(outputs))
	for _, o := range outputs {
		out = append(out, map[string]any{"name": o.Name, "target": o.Target})
	}
	return out
}

// --- draft_ubxfile ---

type draftUbxfileInput struct {
	Lang      string                `json:"lang" jsonschema:"target language(s) for build_blueprint later: \"go\", \"ts\", \"py\", or \"all\""`
	Stack     string                `json:"stack" jsonschema:"the stack every resource below belongs to"`
	Summary   string                `json:"summary" jsonschema:"one-line intent.summary describing what this blueprint does"`
	Resources string                `json:"resources" jsonschema:"a JSON array of resource objects, e.g. [{\"type\":\"aws_ecr_repository\",\"name\":\"artifacts\",\"op\":\"create\",\"config\":{\"name\":\"{repo_name}\"}}] -- already decided by you from the conversation; this tool assembles it into a valid Ubxfile, it does not interpret intent itself. Use {param_name} tokens directly in config values to reference a declared param (docs: param interpolation)"`
	Params    []blueprintParamSpec  `json:"params,omitempty" jsonschema:"params: declarations this blueprint takes, in the order they should appear"`
	Outputs   []blueprintOutputSpec `json:"outputs,omitempty" jsonschema:"outputs: declarations, each target a \"<resource-slug>.<attribute>\" naming one of the resources above by its own slug"`
}

func registerDraftUbxfileTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "draft_ubxfile",
		Description: "Assemble an Ubxfile from resources/params/outputs you've already decided from the conversation " +
			"-- mechanical construction, not drafting: this tool has no model of its own and never interprets free-form " +
			"intent. Returns the Ubxfile text and its resources.json content as strings; nothing is written to disk. " +
			"The result is validated internally before returning (the same check validate_ubxfile runs), so you get an " +
			"immediate valid/invalid signal without a second call. Reach for this once you know which resources a " +
			"blueprint should describe; call validate_ubxfile again after hand-editing the result, and build_blueprint " +
			"when ready to compile.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in draftUbxfileInput) (*mcp.CallToolResult, any, error) {
		if in.Lang == "" || in.Stack == "" || in.Resources == "" {
			return nil, nil, fmt.Errorf("draft_ubxfile: lang, stack, and resources are all required")
		}
		ubxfileYAML, err := assembleUbxfileYAML(in.Lang, in.Params, in.Outputs)
		if err != nil {
			return nil, nil, fmt.Errorf("draft_ubxfile: %w", err)
		}
		resourcesJSON, err := assembleResourcesJSON(in.Stack, in.Summary, in.Resources)
		if err != nil {
			return nil, nil, fmt.Errorf("draft_ubxfile: %w", err)
		}

		result := map[string]any{
			"ubxfile":        ubxfileYAML,
			"resources_json": resourcesJSON,
		}

		dir, cleanup, err := writeTempBlueprintDir(ubxfileYAML, resourcesJSON)
		if err != nil {
			result["valid"] = false
			result["validation_error"] = err.Error()
			return nil, result, nil
		}
		defer cleanup()
		_, draft, verr := blueprint.Validate(dir)
		if verr != nil {
			result["valid"] = false
			result["validation_error"] = verr.Error()
			return nil, result, nil
		}
		result["valid"] = true
		result["resource_count"] = len(draft.Resources)
		return nil, result, nil
	})
}

// --- validate_ubxfile ---

type validateUbxfileInput struct {
	Dir       string `json:"dir,omitempty" jsonschema:"an existing directory containing a real Ubxfile -- mutually exclusive with ubxfile/resources"`
	Ubxfile   string `json:"ubxfile,omitempty" jsonschema:"inline Ubxfile text not yet saved anywhere, e.g. draft_ubxfile's own output -- requires resources alongside it, mutually exclusive with dir"`
	Resources string `json:"resources,omitempty" jsonschema:"inline resources.json text, paired with ubxfile"`
}

func registerValidateUbxfileTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "validate_ubxfile",
		Description: "Check whether an Ubxfile is structurally valid before building anything -- parses it, unmarshals " +
			"resources: as a pre-resolved intent/v1 document, and runs the same language-neutral consistency checks " +
			"(every $ref/depends_on resolves, for_each names a real list param, output targets resolve to real " +
			"resource slugs, ...) every language's own codegen already runs internally. Cheap and side-effect-free: " +
			"never compiles anything. Pass either dir (an existing Ubxfile directory) or ubxfile+resources (content not " +
			"yet saved anywhere). Returns valid=false with a real error message rather than failing the tool call, " +
			"since an invalid draft is an expected, ordinary outcome to reason about, not an exception.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in validateUbxfileInput) (*mcp.CallToolResult, any, error) {
		dir, cleanup, err := resolveBlueprintDir(in.Dir, in.Ubxfile, in.Resources)
		if err != nil {
			return nil, nil, fmt.Errorf("validate_ubxfile: %w", err)
		}
		defer cleanup()

		ubxfile, draft, verr := blueprint.Validate(dir)
		if verr != nil {
			return nil, map[string]any{"valid": false, "error": verr.Error()}, nil
		}
		return nil, map[string]any{
			"valid":          true,
			"stack":          draft.Stack,
			"resource_count": len(draft.Resources),
			"params":         paramsToSpecs(ubxfile.Params),
			"outputs":        outputsToSpecs(ubxfile.Outputs),
		}, nil
	})
}

// --- build_blueprint ---

type buildBlueprintInput struct {
	Dir       string `json:"dir,omitempty" jsonschema:"an existing directory containing a real Ubxfile -- mutually exclusive with ubxfile/resources"`
	Ubxfile   string `json:"ubxfile,omitempty" jsonschema:"inline Ubxfile text not yet saved anywhere -- requires resources alongside it, mutually exclusive with dir"`
	Resources string `json:"resources,omitempty" jsonschema:"inline resources.json text, paired with ubxfile"`
	Lang      string `json:"lang,omitempty" jsonschema:"target language(s): go, ts, py, or all -- default: the Ubxfile's own declared lang:"`
}

func registerBuildBlueprintTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "build_blueprint",
		Description: "Compile a valid Ubxfile into real, compilable Go/TypeScript/Python SDK package source -- the " +
			"exact same codegen \"ubx blueprint build\" runs. Pass either dir (an existing Ubxfile directory) or " +
			"ubxfile+resources (content not yet saved anywhere, e.g. straight from draft_ubxfile). Returns every " +
			"generated file's own path and content inline; never writes to disk. Fails with the real validation " +
			"error if the Ubxfile isn't valid -- call validate_ubxfile first if you want to check without paying " +
			"for codegen.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in buildBlueprintInput) (*mcp.CallToolResult, any, error) {
		dir, cleanup, err := resolveBlueprintDir(in.Dir, in.Ubxfile, in.Resources)
		if err != nil {
			return nil, nil, fmt.Errorf("build_blueprint: %w", err)
		}
		defer cleanup()

		ubxfile, draft, verr := blueprint.Validate(dir)
		if verr != nil {
			return nil, nil, fmt.Errorf("build_blueprint: %w", verr)
		}

		langInput := in.Lang
		if langInput == "" {
			langInput = ubxfile.Lang
		}
		langs, err := parseLangFlag(langInput)
		if err != nil {
			return nil, nil, fmt.Errorf("build_blueprint: %w", err)
		}

		blueprintName := filepath.Base(dir)
		if in.Dir != "" {
			blueprintName = filepath.Base(in.Dir)
		}

		allFiles := map[string]string{}
		for _, l := range langs {
			files, err := blueprintGenerators[l](blueprintName, ubxfile, draft)
			if err != nil {
				return nil, nil, fmt.Errorf("build_blueprint (%s): %w", l, err)
			}
			for name, content := range files {
				allFiles[name] = content
			}
		}

		result := map[string]any{
			"files":          allFiles,
			"resource_count": len(draft.Resources),
			"languages":      langs,
		}

		return nil, result, nil
	})
}

// --- list_blueprints ---

type listBlueprintsInput struct {
	RootDir string `json:"root_dir,omitempty" jsonschema:"directory tree to search for blueprint sources (default: the server's own current directory)"`
}

func registerListBlueprintsTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "list_blueprints",
		Description: "Find blueprint source directories (a real Ubxfile) anywhere under root_dir. There is no " +
			"registry to query yet (blueprint.Pull's own doc comment: Strata itself isn't built), so this reports " +
			"what's real today -- Ubxfile-rooted directories found by walking the filesystem, each parsed far enough " +
			"to report its own params and resource count. A directory whose Ubxfile fails to parse is still listed, " +
			"with its own error, rather than silently skipped. Reach for this to answer \"what blueprints exist in " +
			"this checkout\"; use describe_blueprint for one already-known ref (a git URL, an oci:// reference, a " +
			"tarball) instead.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listBlueprintsInput) (*mcp.CallToolResult, any, error) {
		root := orDot(in.RootDir)
		absRoot, err := filepath.Abs(root)
		if err != nil {
			return nil, nil, fmt.Errorf("list_blueprints: %w", err)
		}

		var found []map[string]any
		err = filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				return nil
			}
			if d.Name() == "node_modules" || d.Name() == ".git" {
				return filepath.SkipDir
			}
			if _, statErr := os.Stat(filepath.Join(path, blueprint.UbxfileName)); statErr != nil {
				return nil
			}
			entry := map[string]any{"dir": path}
			ubxfile, draft, verr := blueprint.Validate(path)
			if verr != nil {
				entry["valid"] = false
				entry["error"] = verr.Error()
				found = append(found, entry)
				return nil
			}
			entry["valid"] = true
			entry["name"] = filepath.Base(path)
			entry["lang"] = ubxfile.Lang
			entry["params"] = paramsToSpecs(ubxfile.Params)
			entry["resource_count"] = len(draft.Resources)
			found = append(found, entry)
			return nil
		})
		if err != nil {
			return nil, nil, fmt.Errorf("list_blueprints: %w", err)
		}
		return nil, map[string]any{"blueprints": found}, nil
	})
}

// --- describe_blueprint ---

type describeBlueprintInput struct {
	Source string `json:"source" jsonschema:"a blueprint reference: a local directory, a bare tarball file (\"ubx blueprint package\" output), a git repository URL, or an \"oci://registry/repo:tag\" reference -- the same four forms \"ubx blueprint pull\" accepts"`
	Ref    string `json:"ref,omitempty" jsonschema:"git ref (branch/tag/commit) -- git sources only, default the repo's own default branch"`
	Path   string `json:"path,omitempty" jsonschema:"path within the git repo to the blueprint -- git sources only, default \".\""`
}

func registerDescribeBlueprintTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "describe_blueprint",
		Description: "Resolve one already-known blueprint reference (a local path, a tarball file, a git repo+ref, " +
			"or an oci:// artifact) and report what it is -- name, declared params, resource count, outputs, and its " +
			"own content hash if it was packaged (\"ubx blueprint package\"). Pulls into a throwaway scratch " +
			"directory, never the caller's own working tree, and cleans up afterward -- read-only, no lasting side " +
			"effect. Reach for this before pulling a blueprint for real, to confirm it's the one you mean.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in describeBlueprintInput) (*mcp.CallToolResult, any, error) {
		if in.Source == "" {
			return nil, nil, fmt.Errorf("describe_blueprint: source is required")
		}
		scratch, err := os.MkdirTemp("", "ubx-mcp-describe-*")
		if err != nil {
			return nil, nil, fmt.Errorf("describe_blueprint: %w", err)
		}
		defer os.RemoveAll(scratch)
		dest := filepath.Join(scratch, "blueprint")

		if _, err := blueprint.Pull(ctx, in.Source, dest, in.Ref, in.Path); err != nil {
			return nil, nil, fmt.Errorf("describe_blueprint: %w", err)
		}

		result := map[string]any{"source": in.Source}
		if manifest, err := blueprint.Verify(dest); err == nil {
			result["name"] = manifest.Name
			result["content_hash"] = manifest.ContentHash
			result["file_count"] = len(manifest.Files)
			result["packaged"] = true
		} else {
			result["packaged"] = false
		}
		if ubxfile, draft, err := blueprint.Validate(dest); err == nil {
			if result["name"] == nil {
				result["name"] = filepath.Base(dest)
			}
			result["valid"] = true
			result["lang"] = ubxfile.Lang
			result["stack"] = draft.Stack
			result["params"] = paramsToSpecs(ubxfile.Params)
			result["outputs"] = outputsToSpecs(ubxfile.Outputs)
			result["resource_count"] = len(draft.Resources)
		} else {
			result["valid"] = false
			result["validation_error"] = err.Error()
		}
		return nil, result, nil
	})
}
