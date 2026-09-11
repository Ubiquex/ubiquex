package pyeval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// declarations.go reads a Python file's own module-level declarations
// without running it, for `ubx blueprint package`'s schema extraction
// (blueprint/extractpy.go, docs/blueprint.md's own "Blueprint schema").
//
// It parses with CPython's own `ast` module rather than anything
// hand-written. Python's grammar is significant-whitespace, its
// annotations can be strings, and `from __future__ import annotations`
// makes them strings wholesale; a hand-rolled reader would be wrong in
// exactly the cases a real blueprint hits, and would be wrong silently.
//
// `ast.parse` compiles to a syntax tree and stops. It runs no module
// code, executes no decorator, imports nothing. So this reads an
// undownloaded, uninstalled tree happily, which is the property the Go
// extractor has and the TypeScript one does not.
//
// The interpreter is the pinned WASI CPython the evaluator already
// pins, not the host's own python3. The schema this produces is hashed
// into the blueprint package, so which parser read the source is not an
// implementation detail: a signature this interpreter cannot parse is a
// signature it could not have run either, and an ambient python3 would
// make the packaged schema depend on whatever happened to be installed.

// declarationScript is the extractor. It reads one source file and
// prints a JSON description of its module-level functions and classes.
//
// Deliberately flat and dumb: it reports what is written, unresolved,
// and every judgement about what is a valid blueprint is made in Go
// (blueprint/extractpy.go) alongside the same judgements for the other
// two languages. Splitting those across a Go file and an embedded
// Python string is how three extractors stop agreeing.
const declarationScript = `import ast, json, sys

src = open("/prog/subject.py", encoding="utf-8").read()
tree = ast.parse(src, filename="subject.py")

def ann(node):
    # Unparsed back to source text. An annotation is matched on its
    # spelling by the caller, and ast.unparse normalizes the spelling
    # (whitespace, line continuations, redundant parens) so the caller
    # never has to.
    if node is None:
        return None
    if isinstance(node, ast.Constant) and isinstance(node.value, str):
        # A string annotation, either written as one or made one by
        # "from __future__ import annotations". Parsed once more so it
        # reaches the caller as the type it names rather than as a
        # quoted string.
        try:
            return ast.unparse(ast.parse(node.value, mode="eval").body)
        except SyntaxError:
            return node.value
    return ast.unparse(node)

classes = []
for node in tree.body:
    if not isinstance(node, ast.ClassDef):
        continue
    fields = []
    for stmt in node.body:
        if isinstance(stmt, ast.AnnAssign) and isinstance(stmt.target, ast.Name):
            fields.append({
                "name": stmt.target.id,
                "annotation": ann(stmt.annotation),
                "has_default": stmt.value is not None,
            })
    classes.append({
        "name": node.name,
        "decorators": [ann(d) for d in node.decorator_list],
        "fields": fields,
    })

functions = []
for node in tree.body:
    if not isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
        continue
    a = node.args
    positional = a.posonlyargs + a.args
    ndefaults = len(a.defaults)
    params = []
    for i, arg in enumerate(positional):
        params.append({
            "name": arg.arg,
            "annotation": ann(arg.annotation),
            "has_default": i >= len(positional) - ndefaults,
        })
    for arg, default in zip(a.kwonlyargs, a.kw_defaults):
        params.append({
            "name": arg.arg,
            "annotation": ann(arg.annotation),
            "has_default": default is not None,
        })
    functions.append({
        "name": node.name,
        "is_async": isinstance(node, ast.AsyncFunctionDef),
        "params": params,
        "has_varargs": a.vararg is not None or a.kwarg is not None,
        "returns": ann(node.returns),
    })

json.dump({"classes": classes, "functions": functions}, sys.stdout, sort_keys=True)
`

// PyModule is one parsed Python source file, as declarationScript
// reports it.
type PyModule struct {
	Classes   []PyClass    `json:"classes"`
	Functions []PyFunction `json:"functions"`
}

// PyClass is one module-level class and its annotated fields.
type PyClass struct {
	Name       string    `json:"name"`
	Decorators []string  `json:"decorators"`
	Fields     []PyField `json:"fields"`
}

// PyField is one annotated class attribute, in declaration order.
type PyField struct {
	Name string `json:"name"`
	// Annotation is the type as written, normalized by ast.unparse, and
	// nil for an attribute with no annotation at all.
	Annotation *string `json:"annotation"`
	// HasDefault is true for `retention: int = 30`. It says a default
	// EXISTS, never what it is: see blueprint.Defaults for why the value
	// is deliberately not carried.
	HasDefault bool `json:"has_default"`
}

// PyFunction is one module-level function.
type PyFunction struct {
	Name    string    `json:"name"`
	IsAsync bool      `json:"is_async"`
	Params  []PyParam `json:"params"`
	// HasVarargs is true for *args or **kwargs, which a blueprint
	// entrypoint cannot have: a caller binding named arguments has
	// nothing to bind them to.
	HasVarargs bool    `json:"has_varargs"`
	Returns    *string `json:"returns"`
}

// PyParam is one function parameter.
type PyParam struct {
	Name       string  `json:"name"`
	Annotation *string `json:"annotation"`
	HasDefault bool    `json:"has_default"`
}

// Declarations parses sourceFile with the pinned interpreter's own ast
// module and returns its module-level declarations.
//
// The file is COPIED into a scratch directory rather than mounted in
// place, matching the "always a copy, never the source" discipline
// invoke.go's own callers already follow: the sandbox's preopens are
// read-write, so mounting an author's own blueprint directory to read
// one file from it would put it within reach of a write.
func Declarations(ctx context.Context, sourceFile string) (*PyModule, error) {
	data, err := os.ReadFile(sourceFile)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", sourceFile, err)
	}

	scratch, err := os.MkdirTemp("", "ubx-pydecl-*")
	if err != nil {
		return nil, fmt.Errorf("read declarations of %s: %w", sourceFile, err)
	}
	defer os.RemoveAll(scratch)

	if err := os.WriteFile(filepath.Join(scratch, "subject.py"), data, 0o644); err != nil {
		return nil, fmt.Errorf("read declarations of %s: %w", sourceFile, err)
	}
	entry := filepath.Join(scratch, "declarations.py")
	if err := os.WriteFile(entry, []byte(declarationScript), 0o644); err != nil {
		return nil, fmt.Errorf("read declarations of %s: %w", sourceFile, err)
	}

	// runOnce, not Evaluate: no DoubleRun, and no intent-shape
	// validation. Both exist to police a program that RAN, and nothing
	// here runs the subject. ast.parse of a fixed string is already
	// deterministic.
	raw, err := runOnce(ctx, entry, nil)
	if err != nil {
		return nil, fmt.Errorf("read declarations of %s: %w", sourceFile, err)
	}

	var mod PyModule
	if err := json.Unmarshal(raw, &mod); err != nil {
		return nil, fmt.Errorf("read declarations of %s: %w", sourceFile, err)
	}
	return &mod, nil
}
