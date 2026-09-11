package blueprint

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/blueprint/spec"
)

// extractpy_test.go runs against the real pinned WASI interpreter, like
// pyeval's own tests do. Parsing is the entire risk here, so a fake
// parser would test nothing worth testing.

func requireWasmtime(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("wasmtime"); err != nil {
		t.Skip("wasmtime not in PATH")
	}
}

// writePyBlueprint writes a one-file Python blueprint and returns its
// directory.
func writePyBlueprint(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ubx_aws_sqs.py"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const pyHappy = `from dataclasses import dataclass
from typing import Optional
import ubx_sdk as sdk


@dataclass
class Config:
    name: str
    subnet_ids: list[str]
    vpc: sdk.CrossMarker
    target_arn: Optional[str] = None
    retention: Optional[int] = None
    enabled: Optional[bool] = None
    ports: Optional[list[int]] = None


@dataclass
class Outputs:
    queue_url: sdk.Computed
    queue_name: sdk.Computed


def ubx_aws_sqs(cfg: Config) -> Outputs:
    return Outputs(queue_url=None, queue_name=None)
`

func TestExtractPy_HappyPath(t *testing.T) {
	requireWasmtime(t)
	dir := writePyBlueprint(t, pyHappy)
	s, err := ExtractPy(context.Background(), dir, "ubx-aws-sqs")
	if err != nil {
		t.Fatal(err)
	}
	if s.SchemaVersion != SchemaVersion || s.Name != "ubx-aws-sqs" {
		t.Errorf("version/name: got %d/%q", s.SchemaVersion, s.Name)
	}
	if s.Entrypoint.Language != "py" || s.Entrypoint.Function != "ubx_aws_sqs" {
		t.Errorf("entrypoint: %+v", s.Entrypoint)
	}
	if s.Entrypoint.PyModule != "ubx_aws_sqs" {
		t.Errorf("py_module = %q, want the module name a caller imports", s.Entrypoint.PyModule)
	}
	if s.Entrypoint.GoModule != "" || s.Entrypoint.TSEntry != "" {
		t.Errorf("only the language's own specifier is set, got go=%q ts=%q", s.Entrypoint.GoModule, s.Entrypoint.TSEntry)
	}

	// Required first, then optional. That is not a style choice: see
	// TestExtractPy_RequiredAfterOptionalIsRefused.
	want := []SchemaParam{
		{Name: "name", SourceName: "name", Type: spec.ParamString, Required: true},
		{Name: "subnet_ids", SourceName: "subnet_ids", Type: spec.ParamListString, Required: true},
		{Name: "vpc", SourceName: "vpc", Type: spec.ParamCrossRef, Required: true},
		{Name: "target_arn", SourceName: "target_arn", Type: spec.ParamString, Required: false},
		{Name: "retention", SourceName: "retention", Type: spec.ParamNumber, Required: false},
		{Name: "enabled", SourceName: "enabled", Type: spec.ParamBool, Required: false},
		{Name: "ports", SourceName: "ports", Type: spec.ParamListNumber, Required: false},
	}
	if len(s.Params) != len(want) {
		t.Fatalf("got %d params, want %d: %+v", len(s.Params), len(want), s.Params)
	}
	for i, w := range want {
		if s.Params[i] != w {
			t.Errorf("param %d: got %+v, want %+v", i, s.Params[i], w)
		}
	}

	wantOut := []SchemaOutput{
		{Name: "queue_url", SourceName: "queue_url"},
		{Name: "queue_name", SourceName: "queue_name"},
	}
	for i, w := range wantOut {
		if s.Outputs[i] != w {
			t.Errorf("output %d: got %+v, want %+v", i, s.Outputs[i], w)
		}
	}
	if len(s.Derivation.Assumptions) != 0 {
		t.Errorf("assumptions = %v, want none: Python states int outright", s.Derivation.Assumptions)
	}
}

// PEP 604's `X | None` is the same thing as Optional[X] and an author
// may write either, so both have to read the same.
func TestExtractPy_BothOptionalSpellingsAgree(t *testing.T) {
	requireWasmtime(t)
	dir := writePyBlueprint(t, `from dataclasses import dataclass
from typing import Optional


@dataclass
class Config:
    required: str
    typing_form: Optional[str] = None
    operator_form: str | None = None


def bp(cfg: Config) -> None:
    pass
`)
	s, err := ExtractPy(context.Background(), dir, "bp")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range s.Params[1:] {
		if p.Required || p.Type != spec.ParamString {
			t.Errorf("%s: got required=%v type=%v, want optional string", p.Name, p.Required, p.Type)
		}
	}
	if !s.Params[0].Required {
		t.Error("a bare annotation is required")
	}
}

// `from __future__ import annotations` turns every annotation into a
// string at parse time. A reader that did not parse them back would see
// "'Optional[str]'" and report an unknown type, which is exactly the
// class of silent wrongness that made CPython's own ast the right
// parser to use.
func TestExtractPy_FutureAnnotationsStillRead(t *testing.T) {
	requireWasmtime(t)
	dir := writePyBlueprint(t, `from __future__ import annotations
from dataclasses import dataclass
from typing import Optional


@dataclass
class Config:
    name: str
    retention: Optional[int] = None


def bp(cfg: Config) -> None:
    pass
`)
	s, err := ExtractPy(context.Background(), dir, "bp")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Params) != 2 || s.Params[0].Type != spec.ParamString || s.Params[1].Type != spec.ParamNumber {
		t.Fatalf("params: %+v", s.Params)
	}
	if s.Params[1].Required {
		t.Error("Optional[int] is optional whether or not annotations are strings")
	}
}

// Python is the one language offering TWO optionality signals, and they
// are independent. A field with a default but no Optional would be
// reported required while the function treats it as optional, so it is
// refused with the fix named.
func TestExtractPy_DefaultWithoutOptionalIsRefused(t *testing.T) {
	requireWasmtime(t)
	dir := writePyBlueprint(t, `from dataclasses import dataclass


@dataclass
class Config:
    enabled: bool = True


def bp(cfg: Config) -> None:
    pass
`)
	_, err := ExtractPy(context.Background(), dir, "bp")
	if err == nil {
		t.Fatal("want a refusal: a default without Optional is ambiguous")
	}
	if !strings.Contains(err.Error(), "Optional[bool]") {
		t.Errorf("error should name the fix, got: %v", err)
	}
}

// The mirror case: Optional with no default cannot actually be omitted
// by a caller, so calling it optional would be a lie.
func TestExtractPy_OptionalWithoutDefaultIsRefused(t *testing.T) {
	requireWasmtime(t)
	dir := writePyBlueprint(t, `from dataclasses import dataclass
from typing import Optional


@dataclass
class Config:
    retention: Optional[int]


def bp(cfg: Config) -> None:
    pass
`)
	_, err := ExtractPy(context.Background(), dir, "bp")
	if err == nil {
		t.Fatal("want a refusal: Optional with no default is not omittable")
	}
	if !strings.Contains(err.Error(), "= None") {
		t.Errorf("error should name the fix, got: %v", err)
	}
}

func TestExtractPy_ParamsKeepDeclarationOrder(t *testing.T) {
	requireWasmtime(t)
	dir := writePyBlueprint(t, `from dataclasses import dataclass


@dataclass
class Config:
    zebra: str
    alpha: str
    middle: str


def bp(cfg: Config) -> None:
    pass
`)
	s, err := ExtractPy(context.Background(), dir, "bp")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"zebra", "alpha", "middle"}
	for i, w := range want {
		if s.Params[i].Name != w {
			t.Fatalf("param order: got %+v, want %v (declaration order, not sorted)", s.Params, want)
		}
	}
}

// A Python author writing camelCase gets the same treatment as the
// other two languages: the wire name is snake_case and the identifier
// is carried beside it.
func TestExtractPy_SchemaCarriesTheSourceIdentifier(t *testing.T) {
	requireWasmtime(t)
	dir := writePyBlueprint(t, `from dataclasses import dataclass
from typing import Optional


@dataclass
class Config:
    targetARN: Optional[str] = None


def bp(cfg: Config) -> None:
    pass
`)
	s, err := ExtractPy(context.Background(), dir, "bp")
	if err != nil {
		t.Fatal(err)
	}
	if s.Params[0].Name != "target_arn" || s.Params[0].SourceName != "targetARN" {
		t.Errorf("got %+v, want target_arn/targetARN", s.Params[0])
	}
}

func TestExtractPy_NoReturnAnnotationHasNoOutputs(t *testing.T) {
	requireWasmtime(t)
	dir := writePyBlueprint(t, `from dataclasses import dataclass


@dataclass
class Config:
    name: str


def bp(cfg: Config):
    pass
`)
	s, err := ExtractPy(context.Background(), dir, "bp")
	if err != nil {
		t.Fatal(err)
	}
	if s.Entrypoint.OutputsType != "" || len(s.Outputs) != 0 {
		t.Errorf("want no outputs, got type=%q outputs=%+v", s.Entrypoint.OutputsType, s.Outputs)
	}
}

func TestExtractPy_NonDataclassConfigIsRefused(t *testing.T) {
	requireWasmtime(t)
	dir := writePyBlueprint(t, `class Config:
    name: str


def bp(cfg: Config) -> None:
    pass
`)
	_, err := ExtractPy(context.Background(), dir, "bp")
	if err == nil {
		t.Fatal("want a refusal for a Config that is not a dataclass")
	}
	if !strings.Contains(err.Error(), "dataclass") {
		t.Errorf("error should say what a Config has to be, got: %v", err)
	}
}

// A parameterized decorator is still a dataclass: frozen and slots
// change nothing this reads.
func TestExtractPy_ParameterizedDataclassDecoratorAccepted(t *testing.T) {
	requireWasmtime(t)
	dir := writePyBlueprint(t, `from dataclasses import dataclass


@dataclass(frozen=True)
class Config:
    name: str


def bp(cfg: Config) -> None:
    pass
`)
	if _, err := ExtractPy(context.Background(), dir, "bp"); err != nil {
		t.Fatalf("@dataclass(frozen=True) is a dataclass: %v", err)
	}
}

func TestExtractPy_TwoCandidateFunctionsAreRefused(t *testing.T) {
	requireWasmtime(t)
	dir := writePyBlueprint(t, `from dataclasses import dataclass


@dataclass
class Config:
    name: str


def one(cfg: Config) -> None:
    pass


def two(cfg: Config) -> None:
    pass
`)
	_, err := ExtractPy(context.Background(), dir, "bp")
	if err == nil {
		t.Fatal("want a refusal for an ambiguous entrypoint")
	}
	if !strings.Contains(err.Error(), "one, two") {
		t.Errorf("error should name both candidates, got: %v", err)
	}
}

// A leading underscore is Python's own "not public", so it is how an
// author keeps a one-parameter helper beside the blueprint. The Go and
// TypeScript extractors read export-ness for the same purpose.
func TestExtractPy_UnderscoreHelperIsNotACandidate(t *testing.T) {
	requireWasmtime(t)
	dir := writePyBlueprint(t, `from dataclasses import dataclass


@dataclass
class Config:
    name: str


def _helper(cfg: Config) -> None:
    pass


def bp(cfg: Config) -> None:
    pass
`)
	s, err := ExtractPy(context.Background(), dir, "bp")
	if err != nil {
		t.Fatal(err)
	}
	if s.Entrypoint.Function != "bp" {
		t.Errorf("entrypoint = %q, want bp", s.Entrypoint.Function)
	}
}

func TestExtractPy_UnknownParamTypeIsRefused(t *testing.T) {
	requireWasmtime(t)
	dir := writePyBlueprint(t, `from dataclasses import dataclass


@dataclass
class Config:
    tags: dict[str, str]


def bp(cfg: Config) -> None:
    pass
`)
	_, err := ExtractPy(context.Background(), dir, "bp")
	if err == nil {
		t.Fatal("want a refusal for a type outside the vocabulary")
	}
	if !strings.Contains(err.Error(), "not a param type") {
		t.Errorf("error should name the vocabulary, got: %v", err)
	}
}

func TestExtractPy_NonComputedOutputIsRefused(t *testing.T) {
	requireWasmtime(t)
	dir := writePyBlueprint(t, `from dataclasses import dataclass


@dataclass
class Config:
    name: str


@dataclass
class Outputs:
    queue_url: str


def bp(cfg: Config) -> Outputs:
    return Outputs(queue_url="")
`)
	_, err := ExtractPy(context.Background(), dir, "bp")
	if err == nil {
		t.Fatal("want a refusal for an output that is not a Computed")
	}
	if !strings.Contains(err.Error(), "Computed") {
		t.Errorf("error should say what an output has to be, got: %v", err)
	}
}

// An async entrypoint parses fine and would fail at call time, so it is
// refused at extraction where the message can name the reason.
func TestExtractPy_AsyncEntrypointIsRefused(t *testing.T) {
	requireWasmtime(t)
	dir := writePyBlueprint(t, `from dataclasses import dataclass


@dataclass
class Config:
    name: str


async def bp(cfg: Config) -> None:
    pass
`)
	_, err := ExtractPy(context.Background(), dir, "bp")
	if err == nil {
		t.Fatal("want a refusal for an async entrypoint")
	}
	if !strings.Contains(err.Error(), "async") {
		t.Errorf("error should say why, got: %v", err)
	}
}

// The property the TypeScript extractor does NOT have: ast.parse
// resolves no imports, so a blueprint importing a package that is not
// installed still yields its schema.
func TestExtractPy_UninstalledImportStillExtracts(t *testing.T) {
	requireWasmtime(t)
	dir := writePyBlueprint(t, `from dataclasses import dataclass
from ubx_aws_not_installed import Queue
import ubx_sdk as sdk


@dataclass
class Config:
    name: str


@dataclass
class Outputs:
    queue_url: sdk.Computed


def bp(cfg: Config) -> Outputs:
    return Outputs(queue_url=Queue(name=cfg.name).url)
`)
	s, err := ExtractPy(context.Background(), dir, "bp")
	if err != nil {
		t.Fatalf("parsing resolves no imports, so an uninstalled one is irrelevant: %v", err)
	}
	if len(s.Params) != 1 || len(s.Outputs) != 1 {
		t.Errorf("params=%+v outputs=%+v", s.Params, s.Outputs)
	}
}

// The one thing Python cannot express that the other two can. A Go
// struct and a TypeScript interface may interleave required and
// optional fields freely; a Python dataclass may not, because a field
// with no default cannot follow one with a default. Found by writing
// this extractor: the happy-path fixture originally interleaved them,
// extracted cleanly, and was not a module Python could have imported,
// since ast.parse accepts what @dataclass later rejects.
//
// So the refusal happens here, where the message can name the reason,
// rather than as a TypeError the first time anything imports it.
func TestExtractPy_RequiredAfterOptionalIsRefused(t *testing.T) {
	requireWasmtime(t)
	dir := writePyBlueprint(t, `from dataclasses import dataclass
from typing import Optional


@dataclass
class Config:
    retention: Optional[int] = None
    name: str


def bp(cfg: Config) -> None:
    pass
`)
	_, err := ExtractPy(context.Background(), dir, "bp")
	if err == nil {
		t.Fatal("want a refusal: Python cannot declare a required field after an optional one")
	}
	if !strings.Contains(err.Error(), "required param") {
		t.Errorf("error should name the constraint, got: %v", err)
	}
}
