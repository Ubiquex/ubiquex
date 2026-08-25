package ts

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/sdk/codegen/ir"
)

// tsFixtureSource is a hermetic, real-shaped stand-in for @ubx/sdk's own
// resource()/data() surface -- signatures confirmed live against the
// real, separate ubx-sdk-typescript repo before writing this (this
// monorepo's own sdk/ts submodule is pinned to a pre-DataSourceBinding
// commit -- confirmed no "DataSourceBinding" or "data(" anywhere in it
// -- so the existing checkGeneratedRepo/sdkTSRuntimeIndexPath helpers in
// this package cannot exercise a real data-source consumer at all; this
// fixture exists so this test does not depend on either the stale
// submodule or a sibling checkout at a hardcoded path).
const tsFixtureSource = `export type FieldSpec =
  | string
  | { readonly wireName: string; readonly kind: "object" | "list" | "set" | "map"; readonly fields: FieldMap };
export type FieldMap = Record<string, FieldSpec>;
export type Computed<T> = T;

export interface ResourceBinding<TConfig = unknown, TAttrs = unknown> {
  readonly wireType: string;
  readonly fields: FieldMap;
  readonly __config?: TConfig;
  readonly __attrs?: TAttrs;
}

export interface DataSourceBinding<TLookup = unknown, TAttrs = unknown> {
  readonly wireType: string;
  readonly fields: FieldMap;
  readonly __lookup?: TLookup;
  readonly __attrs?: TAttrs;
}

export function resource<TConfig, TAttrs>(
  binding: ResourceBinding<TConfig, TAttrs>,
  name: string,
  config: TConfig,
): Computed<TAttrs> {
  return {} as Computed<TAttrs>;
}

export function data<TLookup, TAttrs>(
  binding: DataSourceBinding<TLookup, TAttrs>,
  name: string,
  lookup: TLookup,
): Computed<TAttrs> {
  return {} as Computed<TAttrs>;
}
`

// writeTSConsumerFixture writes files (a GeneratedRepo result) plus
// consumerSrc (a real consumer program) and tsFixtureSource into dir,
// with a deno.json import map pointing "@ubx/sdk" at the hermetic
// fixture -- checkGeneratedRepo's own real pattern, minus its dependency
// on the stale submodule / a sibling checkout.
func writeTSConsumerFixture(t *testing.T, dir string, files map[string]string, consumerPath, consumerSrc string) []string {
	t.Helper()

	var tsFiles []string
	for path, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if filepath.Ext(full) == ".ts" {
			tsFiles = append(tsFiles, full)
		}
	}

	fixturePath := filepath.Join(dir, "ubx-sdk-fixture.ts")
	if err := os.WriteFile(fixturePath, []byte(tsFixtureSource), 0o644); err != nil {
		t.Fatal(err)
	}

	importMap := fmt.Sprintf(`{"imports":{"@ubx/sdk":%q}}`, fixturePath)
	if err := os.WriteFile(filepath.Join(dir, "deno.json"), []byte(importMap), 0o644); err != nil {
		t.Fatal(err)
	}

	full := filepath.Join(dir, filepath.FromSlash(consumerPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(consumerSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	tsFiles = append(tsFiles, full)

	return tsFiles
}

func runDenoCheck(t *testing.T, dir string, tsFiles []string) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	args := append([]string{"check", "--no-remote"}, tsFiles...)
	cmd := exec.CommandContext(ctx, "deno", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// TestConsumer_ResourceAndDataSource_RealCompile is TestConsumer's own Go
// analog (sdk/codegen/templates/go/consumer_test.go's own doc comment
// has the full account of why this exists at all): a real consumer
// program calling resource()/data() against generated bindings, checked
// with the real TypeScript compiler via `deno check`, never just a
// string match on the generated source.
func TestConsumer_ResourceAndDataSource_RealCompile(t *testing.T) {
	requireDeno(t)

	resource := rt("aws_instance", scalarField("id", ir.ScalarString, false, false, true, false))
	resource.RealNamespace = "ec2"

	dataSource := rt("aws_instance", scalarField("id", ir.ScalarString, true, false, false, false))
	dataSource.RealNamespace = "ec2"
	dataSource.IsDataSource = true

	files, err := GeneratedRepo("aws", "hashicorp/aws", "6.54.0", []*ir.ResourceType{resource, dataSource})
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}

	consumer := `import { resource, data } from "@ubx/sdk";
import { Instance, InstanceConfig } from "./sdk/typescript/aws/ec2/instance.ts";
import { Instance as DataInstance, InstanceConfig as DataInstanceConfig } from "./sdk/typescript/aws/data/ec2/instance.ts";

resource(Instance, "example", {} as InstanceConfig);
data(DataInstance, "example", {} as DataInstanceConfig);
`

	dir := t.TempDir()
	tsFiles := writeTSConsumerFixture(t, dir, files, "consumer.ts", consumer)

	out, ok := runDenoCheck(t, dir, tsFiles)
	if !ok {
		t.Fatalf("real consumer program failed `deno check` against the generated resource + data source bindings:\n%s", out)
	}
}

// TestConsumer_WrongBindingKind_TypeScriptDoesNotCatchIt is a real,
// confirmed, negative finding, not a hypothetical: unlike Go (nominal
// types -- see the Go template's own consumer_test.go,
// TestConsumer_WrongBindingKind_FailsToCompile), TypeScript's structural
// typing does NOT reject a ResourceBinding passed where a
// DataSourceBinding is expected -- both interfaces have the identical
// required shape ({wireType, fields}) and differ only in two
// differently-named OPTIONAL phantom properties (__config/__attrs vs
// __lookup/__attrs), which structural compatibility ignores when
// absent (confirmed live: a `deno check` run against exactly this swap
// passes clean). This is recorded here, as a passing test with an
// explanatory name, rather than silently assumed -- the real,
// load-bearing defense against this bug class in the TypeScript
// template is the generated-source string assertion in ts_test.go
// (mustContain "DataSourceBinding", mustNotContain "ResourceBinding"),
// not the type checker.
func TestConsumer_WrongBindingKind_TypeScriptDoesNotCatchIt(t *testing.T) {
	requireDeno(t)

	resource := rt("aws_instance", scalarField("id", ir.ScalarString, false, false, true, false))
	resource.RealNamespace = "ec2"

	files, err := GeneratedRepo("aws", "hashicorp/aws", "6.54.0", []*ir.ResourceType{resource})
	if err != nil {
		t.Fatalf("GeneratedRepo: %v", err)
	}

	// Deliberately wrong: Instance is a ResourceBinding, but this passes
	// it to data(), which declares its own parameter as DataSourceBinding.
	consumer := `import { data } from "@ubx/sdk";
import { Instance, InstanceConfig } from "./sdk/typescript/aws/ec2/instance.ts";

data(Instance, "example", {} as InstanceConfig);
`

	dir := t.TempDir()
	tsFiles := writeTSConsumerFixture(t, dir, files, "consumer.ts", consumer)

	out, ok := runDenoCheck(t, dir, tsFiles)
	if !ok {
		t.Fatalf("expected TypeScript's structural typing to accept this ResourceBinding/DataSourceBinding swap (a real, confirmed gap -- see this test's own doc comment), but deno check rejected it instead:\n%s\nIf @ubx/sdk's own runtime has since been changed to nominally distinguish these two types, this test should be updated to expect a failure, and the ts_test.go string assertions revisited as no longer the only defense.", out)
	}
}
