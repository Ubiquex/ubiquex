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

// extractts_test.go runs against a real `deno doc` subprocess, like
// tseval's own tests do against a real `deno run`. A mocked one would
// test this file's own beliefs about Deno's output shape rather than
// Deno's actual output shape, and the shape is the entire risk here.

func requireDeno(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("deno"); err != nil {
		t.Skip("deno not in PATH")
	}
}

// writeTSBlueprint writes a one-file TypeScript blueprint and returns
// its directory.
func writeTSBlueprint(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blueprint.ts"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const tsHappy = `import { Computed, CrossMarker } from "@ubx/sdk";

export interface Config {
  name: string;
  targetARN?: string;
  retention?: number;
  enabled?: boolean;
  subnetIds: string[];
  ports?: number[];
  vpc: CrossMarker;
}

export interface Outputs {
  queueURL: Computed;
  queueName: Computed;
}

export function ubxAwsSqs(cfg: Config): Outputs {
  return { queueURL: null as unknown as Computed, queueName: null as unknown as Computed };
}
`

func TestExtractTS_HappyPath(t *testing.T) {
	requireDeno(t)
	dir := writeTSBlueprint(t, tsHappy)
	s, err := ExtractTS(context.Background(), dir, "ubx-aws-sqs")
	if err != nil {
		t.Fatal(err)
	}
	if s.SchemaVersion != SchemaVersion || s.Name != "ubx-aws-sqs" {
		t.Errorf("version/name: got %d/%q", s.SchemaVersion, s.Name)
	}
	if s.Entrypoint.Language != "ts" || s.Entrypoint.Function != "ubxAwsSqs" {
		t.Errorf("entrypoint: %+v", s.Entrypoint)
	}
	if s.Entrypoint.ConfigType != "Config" || s.Entrypoint.OutputsType != "Outputs" {
		t.Errorf("types: %+v", s.Entrypoint)
	}
	if s.Entrypoint.TSEntry != "blueprint.ts" {
		t.Errorf("ts_entry = %q, want the entry file relative to the blueprint root", s.Entrypoint.TSEntry)
	}
	if s.Entrypoint.GoModule != "" || s.Entrypoint.PyModule != "" {
		t.Errorf("only the language's own specifier is set, got go=%q py=%q", s.Entrypoint.GoModule, s.Entrypoint.PyModule)
	}

	want := []SchemaParam{
		{Name: "name", SourceName: "name", Type: spec.ParamString, Required: true},
		{Name: "target_arn", SourceName: "targetARN", Type: spec.ParamString, Required: false},
		{Name: "retention", SourceName: "retention", Type: spec.ParamNumber, Required: false},
		{Name: "enabled", SourceName: "enabled", Type: spec.ParamBool, Required: false},
		{Name: "subnet_ids", SourceName: "subnetIds", Type: spec.ParamListString, Required: true},
		{Name: "ports", SourceName: "ports", Type: spec.ParamListNumber, Required: false},
		{Name: "vpc", SourceName: "vpc", Type: spec.ParamCrossRef, Required: true},
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
		{Name: "queue_url", SourceName: "queueURL"},
		{Name: "queue_name", SourceName: "queueName"},
	}
	if len(s.Outputs) != len(wantOut) {
		t.Fatalf("got %d outputs, want %d: %+v", len(s.Outputs), len(wantOut), s.Outputs)
	}
	for i, w := range wantOut {
		if s.Outputs[i] != w {
			t.Errorf("output %d: got %+v, want %+v", i, s.Outputs[i], w)
		}
	}
}

// The declaration ORDER is the schema's own, because a derived
// positional wrapper depends on it and the source is the only ordering
// the three languages agree on.
func TestExtractTS_ParamsKeepDeclarationOrder(t *testing.T) {
	requireDeno(t)
	dir := writeTSBlueprint(t, `import { Computed } from "@ubx/sdk";
export interface Config { zebra: string; alpha: string; middle: string; }
export interface Outputs { id: Computed; }
export function bp(cfg: Config): Outputs { return { id: null as unknown as Computed }; }
`)
	s, err := ExtractTS(context.Background(), dir, "bp")
	if err != nil {
		t.Fatal(err)
	}
	got := []string{s.Params[0].Name, s.Params[1].Name, s.Params[2].Name}
	want := []string{"zebra", "alpha", "middle"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("param order: got %v, want %v (declaration order, not sorted)", got, want)
		}
	}
}

// The same finding the Go extractor forced: the snake_case name does
// not round-trip back to the identifier, so the schema carries both.
func TestExtractTS_SchemaCarriesTheSourceIdentifier(t *testing.T) {
	requireDeno(t)
	dir := writeTSBlueprint(t, `import { Computed } from "@ubx/sdk";
export interface Config { targetARN?: string; kmsKeyId?: string; httpEndpoint?: string; }
export interface Outputs { queueURL: Computed; }
export function bp(cfg: Config): Outputs { return { queueURL: null as unknown as Computed }; }
`)
	s, err := ExtractTS(context.Background(), dir, "bp")
	if err != nil {
		t.Fatal(err)
	}
	wantPairs := map[string]string{
		"target_arn":    "targetARN",
		"kms_key_id":    "kmsKeyId",
		"http_endpoint": "httpEndpoint",
	}
	for _, p := range s.Params {
		if want, ok := wantPairs[p.Name]; !ok || p.SourceName != want {
			t.Errorf("param %q: source_name = %q, want %q", p.Name, p.SourceName, want)
		}
	}
	if s.Outputs[0].Name != "queue_url" || s.Outputs[0].SourceName != "queueURL" {
		t.Errorf("output: got %+v, want queue_url/queueURL", s.Outputs[0])
	}
	// The reason both are carried: PascalCasing the wire name back gives
	// TargetArn, not TargetARN, and camelCasing gives targetArn. Neither
	// compiles against the real property.
	if got := snakeCase("targetARN"); got != "target_arn" {
		t.Fatalf("snakeCase(targetARN) = %q", got)
	}
}

// `?` is the ONLY optionality signal, matching Go's pointer and unlike
// Python, where a default is a second one.
func TestExtractTS_OptionalIsTheQuestionMark(t *testing.T) {
	requireDeno(t)
	dir := writeTSBlueprint(t, `import { Computed } from "@ubx/sdk";
export interface Config { required: string; optional?: string; }
export interface Outputs { id: Computed; }
export function bp(cfg: Config): Outputs { return { id: null as unknown as Computed }; }
`)
	s, err := ExtractTS(context.Background(), dir, "bp")
	if err != nil {
		t.Fatal(err)
	}
	if !s.Params[0].Required || s.Params[1].Required {
		t.Errorf("got required=%v,%v want true,false", s.Params[0].Required, s.Params[1].Required)
	}
	if s.Defaults.Derivable {
		t.Error("defaults are not derivable from a signature in any language")
	}
}

// A blueprint returning nothing is legal and has no outputs, so the
// author of one is not made to invent an empty interface.
func TestExtractTS_VoidReturnHasNoOutputs(t *testing.T) {
	requireDeno(t)
	dir := writeTSBlueprint(t, `export interface Config { name: string; }
export function bp(cfg: Config): void {}
`)
	s, err := ExtractTS(context.Background(), dir, "bp")
	if err != nil {
		t.Fatal(err)
	}
	if s.Entrypoint.OutputsType != "" {
		t.Errorf("outputs_type = %q, want empty", s.Entrypoint.OutputsType)
	}
	if len(s.Outputs) != 0 {
		t.Errorf("got %d outputs, want none", len(s.Outputs))
	}
}

// The assumption is recorded only when it is actually being made.
// TypeScript's number admits fractions and this vocabulary's "number"
// does not, so a blueprint with a number param asserts something its
// own types cannot express, and one without asserts nothing.
func TestExtractTS_NumberAssumptionRecordedOnlyWhenANumberIsUsed(t *testing.T) {
	requireDeno(t)
	withNumber := writeTSBlueprint(t, `export interface Config { retention: number; }
export function bp(cfg: Config): void {}
`)
	s, err := ExtractTS(context.Background(), withNumber, "bp")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Derivation.Assumptions) != 1 || s.Derivation.Assumptions[0] != TSNumberIsIntAssumption {
		t.Errorf("assumptions = %v, want the int assumption", s.Derivation.Assumptions)
	}

	withoutNumber := writeTSBlueprint(t, `export interface Config { name: string; }
export function bp(cfg: Config): void {}
`)
	s2, err := ExtractTS(context.Background(), withoutNumber, "bp")
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.Derivation.Assumptions) != 0 {
		t.Errorf("assumptions = %v, want none when no number param exists", s2.Derivation.Assumptions)
	}
}

// Deno reports where a type resolved FROM, which the Go extractor
// cannot see. A local interface named CrossMarker is a different type
// wearing the SDK's name, and the evaluator would not recognize it.
func TestExtractTS_CrossMarkerMustComeFromTheSDK(t *testing.T) {
	requireDeno(t)
	dir := writeTSBlueprint(t, `export interface CrossMarker { fake: true; }
export interface Config { vpc: CrossMarker; }
export function bp(cfg: Config): void {}
`)
	_, err := ExtractTS(context.Background(), dir, "bp")
	if err == nil {
		t.Fatal("want a refusal for a CrossMarker that is not the SDK's own")
	}
	if !strings.Contains(err.Error(), "@ubx/sdk") {
		t.Errorf("error should name the module the marker has to come from, got: %v", err)
	}
}

func TestExtractTS_UnknownParamTypeIsRefused(t *testing.T) {
	requireDeno(t)
	dir := writeTSBlueprint(t, `export interface Config { tags: Record<string, string>; }
export function bp(cfg: Config): void {}
`)
	_, err := ExtractTS(context.Background(), dir, "bp")
	if err == nil {
		t.Fatal("want a refusal for a type outside the vocabulary")
	}
	if !strings.Contains(err.Error(), "not a param type") {
		t.Errorf("error should name the vocabulary, got: %v", err)
	}
}

func TestExtractTS_TwoCandidateFunctionsAreRefused(t *testing.T) {
	requireDeno(t)
	dir := writeTSBlueprint(t, `export interface Config { name: string; }
export function one(cfg: Config): void {}
export function two(cfg: Config): void {}
`)
	_, err := ExtractTS(context.Background(), dir, "bp")
	if err == nil {
		t.Fatal("want a refusal for an ambiguous entrypoint")
	}
	if !strings.Contains(err.Error(), "one, two") {
		t.Errorf("error should name both candidates, got: %v", err)
	}
}

func TestExtractTS_NonComputedOutputIsRefused(t *testing.T) {
	requireDeno(t)
	dir := writeTSBlueprint(t, `export interface Config { name: string; }
export interface Outputs { queueUrl: string; }
export function bp(cfg: Config): Outputs { return { queueUrl: "" }; }
`)
	_, err := ExtractTS(context.Background(), dir, "bp")
	if err == nil {
		t.Fatal("want a refusal for an output that is not a Computed")
	}
	if !strings.Contains(err.Error(), "Computed") {
		t.Errorf("error should say what an output has to be, got: %v", err)
	}
}

// The Config interface may live beside the entrypoint rather than in
// it, which is why every file is read in one deno doc call.
func TestExtractTS_ConfigInAnotherFileResolves(t *testing.T) {
	requireDeno(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "types.ts"), []byte(`import { Computed } from "@ubx/sdk";
export interface Config { name: string; }
export interface Outputs { id: Computed; }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blueprint.ts"), []byte(`import { Config, Outputs } from "./types.ts";
import { Computed } from "@ubx/sdk";
export function bp(cfg: Config): Outputs { return { id: null as unknown as Computed }; }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ExtractTS(context.Background(), dir, "bp")
	if err != nil {
		t.Fatal(err)
	}
	if s.Entrypoint.TSEntry != "blueprint.ts" {
		t.Errorf("ts_entry = %q, want the file the FUNCTION is in, not the one its types are in", s.Entrypoint.TSEntry)
	}
	if len(s.Params) != 1 || s.Params[0].Name != "name" {
		t.Errorf("params: %+v", s.Params)
	}
}

// The one asymmetry worth pinning: deno doc resolves the whole module
// graph and emits nothing at all when any import is unresolvable, even
// one whose types the signature never mentions. The Go extractor reads
// an undownloaded tree happily. The refusal has to say so, since an
// author otherwise sees a schema failure for a dependency problem.
func TestExtractTS_UnresolvableImportSaysToInstallDependencies(t *testing.T) {
	requireDeno(t)
	dir := writeTSBlueprint(t, `import { Computed } from "@ubx/sdk";
import { Queue } from "@ubx/sdk-aws-not-installed";
export interface Config { name: string; }
export interface Outputs { id: Computed; }
export function bp(cfg: Config): Outputs { return { id: new Queue().id }; }
`)
	_, err := ExtractTS(context.Background(), dir, "bp")
	if err == nil {
		t.Fatal("want a refusal when an import cannot resolve")
	}
	if !strings.Contains(err.Error(), "dependencies") {
		t.Errorf("error should tell the author to install dependencies, got: %v", err)
	}
}
