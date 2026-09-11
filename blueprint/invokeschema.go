package blueprint

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// invokeschema.go calls a blueprint that is CODE, described by a
// derived blueprint.schema.json, rather than one built from an Ubxfile.
//
// The three writers here are the schema-model counterparts of
// invoke.go's own writeGoCaller/writeTSCaller/writePyCaller, not
// adaptations of them, because almost nothing they relied on survives
// the model change:
//
//   - A built blueprint had go/, ts/ and py/ subdirectories. A
//     blueprint that is code has one language and its own root IS the
//     package.
//   - A built blueprint's function and package names were DERIVED from
//     the blueprint's name (packageIdent/pascalCase), so an author had
//     no say in them. The schema records the real ones, so a
//     hand-written blueprint can call its function whatever it likes.
//   - Go arguments were positional with trailing functional options,
//     which is why invoke.go has to reorder them (requiredFirstOrder,
//     UBI-149). A config struct has named fields, so ordering stops
//     mattering entirely and the whole class of ordering bug goes with
//     it.
//
// The schema's SourceName is what makes this possible at all: a config
// literal names fields by their real identifiers, and no amount of
// case-converting the wire name recovers "TargetARN" from "target_arn".

// writeGoSchemaCaller writes a throwaway Go program that calls a
// code-model Go blueprint.
//
// An optional param is a pointer field, so a given one is wrapped in
// sdk.Ptr and an un-given one is omitted from the literal entirely.
// Omission is the whole point rather than a tidiness: the runtime skips
// a nil pointer when serializing, so an unset optional never reaches
// the provider as an explicit null, which is what made a real AWS apply
// fail on fifo_queue.
func writeGoSchemaCaller(scratch, blueprintDir, stackName, summary string, s *Schema, args []resolvedArg) (string, error) {
	if s.Entrypoint.GoModule == "" {
		return "", fmt.Errorf("blueprint schema has no go_module -- a Go blueprint is imported by module path")
	}

	absBlueprint, err := filepath.Abs(blueprintDir)
	if err != nil {
		return "", err
	}
	blueprintGoMod, err := os.ReadFile(filepath.Join(absBlueprint, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read blueprint go.mod: %w", err)
	}
	sdkGoRequire, err := extractRequireLine(string(blueprintGoMod), "github.com/ubiquex/ubx-sdk-go")
	if err != nil {
		return "", err
	}
	// Same reasoning as invoke.go's own: a test fixture carrying a local
	// replace has to resolve the identical sdk/go copy rather than
	// silently falling back to a published one.
	sdkGoReplace := extractReplaceLine(string(blueprintGoMod), "github.com/ubiquex/ubx-sdk-go")
	if sdkGoReplace != "" {
		sdkGoReplace += "\n"
	}

	fields, err := schemaFieldLiterals(s, args, "go", func(sourceName, lit string, required bool) string {
		if required {
			return fmt.Sprintf("\t\t\t%s: %s,", sourceName, lit)
		}
		return fmt.Sprintf("\t\t\t%s: sdk.Ptr(%s),", sourceName, lit)
	})
	if err != nil {
		return "", err
	}

	entryDir := filepath.Join(scratch, "callerprog")
	if err := os.MkdirAll(entryDir, 0o755); err != nil {
		return "", err
	}

	goMod := fmt.Sprintf("module ubx-blueprint-caller\n\ngo 1.23\n\n%s\nrequire %s v0.0.0\n\nreplace %s => %s\n%s",
		sdkGoRequire, s.Entrypoint.GoModule, s.Entrypoint.GoModule, absBlueprint, sdkGoReplace)
	if err := os.WriteFile(filepath.Join(entryDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		return "", err
	}

	body := ""
	if len(fields) > 0 {
		body = "\n" + strings.Join(fields, "\n") + "\n\t\t"
	}
	src := fmt.Sprintf(`package main

import (
	bp %s
	sdk "github.com/ubiquex/ubx-sdk-go/runtime"
)

func main() {
	sdk.Main(sdk.Stack(%s, func() {
		sdk.Intent(sdk.IntentInfo{Summary: %s})
		bp.%s(bp.%s{%s})
	}))
}
`, strconv.Quote(s.Entrypoint.GoModule), strconv.Quote(stackName), strconv.Quote(summary),
		s.Entrypoint.Function, s.Entrypoint.ConfigType, body)

	entry := filepath.Join(entryDir, "main.go")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		return "", err
	}
	return entry, nil
}

// writeTSSchemaCaller writes a throwaway TypeScript program that calls
// a code-model TypeScript blueprint.
//
// The entry is imported by relative path, which is what ts_entry
// records and what a TypeScript caller actually writes. An un-given
// optional is omitted from the object literal rather than set to
// undefined, for the same reason the Go writer omits a nil pointer.
func writeTSSchemaCaller(scratch, blueprintDir, stackName, summary string, s *Schema, args []resolvedArg) (string, error) {
	if s.Entrypoint.TSEntry == "" {
		return "", fmt.Errorf("blueprint schema has no ts_entry -- a TypeScript blueprint is imported by path")
	}
	absEntry, err := filepath.Abs(filepath.Join(blueprintDir, filepath.FromSlash(s.Entrypoint.TSEntry)))
	if err != nil {
		return "", err
	}

	fields, err := schemaFieldLiterals(s, args, "ts", func(sourceName, lit string, required bool) string {
		return fmt.Sprintf("    %s: %s,", sourceName, lit)
	})
	if err != nil {
		return "", err
	}

	// crossStack is imported only when a param actually uses it, so a
	// blueprint with no cross-stack param does not carry an unused
	// import into a program the evaluator type-checks.
	sdkImports := "intent, stack"
	for _, a := range args {
		if a.Param.Type == ParamCrossRef && (a.Given || a.Param.Required) {
			sdkImports = "crossStack, intent, stack"
			break
		}
	}

	body := ""
	if len(fields) > 0 {
		body = "\n" + strings.Join(fields, "\n") + "\n  "
	}
	src := fmt.Sprintf(`import { %s } from "@ubx/sdk";
import { %s } from %s;

stack(%s, () => {
  intent({ summary: %s });
  %s({%s});
});
`, sdkImports, s.Entrypoint.Function, jsonStringLiteral(absEntry),
		jsonStringLiteral(stackName), jsonStringLiteral(summary), s.Entrypoint.Function, body)

	entry := filepath.Join(scratch, "caller.ts")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		return "", err
	}
	return entry, nil
}

// writePySchemaCaller writes a throwaway Python program that calls a
// code-model Python blueprint.
//
// The blueprint is COPIED next to the driver rather than imported in
// place, matching invoke.go's own py writer: pyeval's sandbox mounts
// the entry file's own directory, and copying also keeps the original
// beyond reach of anything this throwaway does.
func writePySchemaCaller(scratch, blueprintDir, stackName, summary string, s *Schema, args []resolvedArg) (string, error) {
	if s.Entrypoint.PyModule == "" {
		return "", fmt.Errorf("blueprint schema has no py_module -- a Python blueprint is imported by module name")
	}

	pyScratch := filepath.Join(scratch, "py")
	if err := copyDir(blueprintDir, pyScratch); err != nil {
		return "", fmt.Errorf("copy blueprint source: %w", err)
	}

	fields, err := schemaFieldLiterals(s, args, "py", func(sourceName, lit string, required bool) string {
		return fmt.Sprintf("%s=%s", sourceName, lit)
	})
	if err != nil {
		return "", err
	}

	imports := s.Entrypoint.Function
	if s.Entrypoint.ConfigType != "" {
		imports += ", " + s.Entrypoint.ConfigType
	}
	src := fmt.Sprintf(`import ubx_sdk as sdk
from %s import %s


def describe():
    sdk.intent(%s)
    %s(%s(%s))


if __name__ == "__main__":
    sdk.run(%s, describe)
`, s.Entrypoint.PyModule, imports, jsonStringLiteral(summary),
		s.Entrypoint.Function, s.Entrypoint.ConfigType, strings.Join(fields, ", "),
		jsonStringLiteral(stackName))

	entry := filepath.Join(pyScratch, "_ubx_call_driver.py")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		return "", err
	}
	return entry, nil
}

// schemaFieldLiterals renders one config-literal field per argument
// that is required or actually given, in the schema's own declaration
// order, using each language's own spelling.
//
// An un-given optional is skipped rather than rendered, in every
// language. That is the same decision three times over, made here once
// so it cannot be made differently in three files: an optional the
// caller did not set has to be genuinely absent, not present-and-empty,
// or a provider that rejects an attribute outright fails an apply that
// should have succeeded.
func schemaFieldLiterals(s *Schema, args []resolvedArg, lang string, render func(sourceName, lit string, required bool) string) ([]string, error) {
	bySchemaName := make(map[string]resolvedArg, len(args))
	for _, a := range args {
		bySchemaName[a.Param.Name] = a
	}

	out := make([]string, 0, len(s.Params))
	for _, sp := range s.Params {
		a, ok := bySchemaName[sp.Name]
		if !ok {
			if sp.Required {
				return nil, fmt.Errorf("missing required param %q", sp.Name)
			}
			continue
		}
		if !sp.Required && !a.Given {
			continue
		}
		lit, err := renderArgLiteral(lang, a.Param, a.Raw)
		if err != nil {
			return nil, err
		}
		if sp.SourceName == "" {
			return nil, fmt.Errorf("param %q has no source_name in the blueprint's own schema -- a config literal needs the real identifier", sp.Name)
		}
		out = append(out, render(sp.SourceName, lit, sp.Required))
	}
	return out, nil
}
