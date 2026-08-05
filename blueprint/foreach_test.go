package blueprint

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/core/resolver"
)

// vpcSubnetsUbxfile is UBI-129's own worked example (docs/blueprint.md's
// own "List-typed parameters + iteration" section): a single for_each
// resource iterating over a declared list(string) param, using both
// synthetic tokens (the bare {availability_zones} per-element value in
// availability_zone/the resource's own name, and {availability_zones_index}
// in cidr_block).
func vpcSubnetsUbxfile() *Ubxfile {
	return &Ubxfile{
		Dir:  "testdata",
		Lang: "go",
		Params: []Param{
			{Name: "vpc_id", Type: ParamString, Required: true},
			{Name: "availability_zones", Type: ParamListString, Required: true},
		},
	}
}

const vpcSubnetsIntent = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "vpc-subnets",
  "intent": {"summary": "Subnets per AZ"},
  "resources": [
    {
      "type": "aws_subnet",
      "name": "subnet-{availability_zones}",
      "op": "create",
      "for_each": "availability_zones",
      "config": {
        "vpc_id": "{vpc_id}",
        "availability_zone": "{availability_zones}",
        "cidr_block": "10.0.{availability_zones_index}.0/24"
      }
    }
  ]
}`

func TestGenerateGo_ForEach_SignatureAndReturn(t *testing.T) {
	intent := mustIntentFile(t, vpcSubnetsIntent)
	files, err := GenerateGo("vpc-subnets", vpcSubnetsUbxfile(), intent)
	if err != nil {
		t.Fatalf("GenerateGo: %v", err)
	}
	fn := files["go/vpcsubnets.go"]

	if !strings.Contains(fn, "func VpcSubnets(vpcId string, availabilityZones []string) []*sdk.Computed {") {
		t.Fatalf("vpcsubnets.go missing the []*sdk.Computed-returning signature:\n%s", fn)
	}
	if !strings.Contains(fn, "var subnetList []*sdk.Computed") {
		t.Fatalf("vpcsubnets.go missing the accumulator slice declaration:\n%s", fn)
	}
	if !strings.Contains(fn, "for availabilityZonesIndex, availabilityZonesValue := range availabilityZones {") {
		t.Fatalf("vpcsubnets.go missing the real for loop over availabilityZones:\n%s", fn)
	}
	if !strings.Contains(fn, `item := sdk.Resource(Subnet, fmt.Sprintf("subnet-%v", availabilityZonesValue), SubnetConfig{`) {
		t.Fatalf("vpcsubnets.go missing the per-instance sdk.Resource() call with a templated name:\n%s", fn)
	}
	if !strings.Contains(fn, "AvailabilityZone: availabilityZonesValue,") {
		t.Fatalf("vpcsubnets.go missing the per-element availability_zone field:\n%s", fn)
	}
	if !strings.Contains(fn, `CidrBlock: fmt.Sprintf("10.0.%v.0/24", availabilityZonesIndex),`) {
		t.Fatalf("vpcsubnets.go missing the index-derived cidr_block field:\n%s", fn)
	}
	if !strings.Contains(fn, "subnetList = append(subnetList, item)") {
		t.Fatalf("vpcsubnets.go missing the accumulator append:\n%s", fn)
	}
	if !strings.Contains(fn, "return subnetList") {
		t.Fatalf("vpcsubnets.go missing the final return of the accumulated list:\n%s", fn)
	}
}

func TestGenerateGo_ForEach_CompilesClean(t *testing.T) {
	requireGoToolchain(t)

	intent := mustIntentFile(t, vpcSubnetsIntent)
	files, err := GenerateGo("vpc-subnets", vpcSubnetsUbxfile(), intent)
	if err != nil {
		t.Fatalf("GenerateGo: %v", err)
	}

	dir := t.TempDir()
	for name, content := range files {
		rel := strings.TrimPrefix(name, "go/")
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sdkGoRoot := sdkGoModuleRoot(t)
	goMod := files["go/go.mod"] + "\nreplace github.com/ubiquex/ubx-sdk-go => " + sdkGoRoot + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build of the generated for_each blueprint package failed:\n%s", out)
	}
}

// TestGenerateGo_ForEach_RuntimeUsable is UBI-129's own required "produces
// the correct N resources with correct per-instance values" proof: a real
// go run (real sdk/go runtime, not a mock) calls the generated function
// with three availability zones and confirms the emitted intent/v1 has
// exactly three aws_subnet resources, each with its own distinct name,
// its own correct availability_zone, and its own index-derived cidr_block.
func TestGenerateGo_ForEach_RuntimeUsable(t *testing.T) {
	requireGoToolchain(t)
	sdkGoRoot := sdkGoModuleRoot(t)

	intent := mustIntentFile(t, vpcSubnetsIntent)
	files, err := GenerateGo("vpc-subnets", vpcSubnetsUbxfile(), intent)
	if err != nil {
		t.Fatalf("GenerateGo: %v", err)
	}

	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "vpcsubnets")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		rel := strings.TrimPrefix(name, "go/")
		if rel == "go.mod" {
			content += "\nreplace github.com/ubiquex/ubx-sdk-go => " + sdkGoRoot + "\n"
		}
		if err := os.WriteFile(filepath.Join(pkgDir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	stackDir := filepath.Join(dir, "stack")
	if err := os.MkdirAll(stackDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stackGoMod := "module stack\n\ngo 1.23\n\n" +
		"require github.com/ubiquex/ubx-sdk-go v0.0.0\nrequire vpcsubnets v0.0.0\n\n" +
		"replace github.com/ubiquex/ubx-sdk-go => " + sdkGoRoot + "\n" +
		"replace vpcsubnets => " + pkgDir + "\n"
	if err := os.WriteFile(filepath.Join(stackDir, "go.mod"), []byte(stackGoMod), 0o644); err != nil {
		t.Fatal(err)
	}
	mainSrc := `package main

import (
	vpcsubnets "vpcsubnets"
	sdk "github.com/ubiquex/ubx-sdk-go/runtime"
)

func main() {
	sdk.Main(sdk.Stack("vpc-subnets", func() {
		sdk.Intent(sdk.IntentInfo{Summary: "x"})
		vpcsubnets.VpcSubnets("vpc-123", []string{"eu-central-1a", "eu-central-1b", "eu-central-1c"})
	}))
}
`
	if err := os.WriteFile(filepath.Join(stackDir, "main.go"), []byte(mainSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = stackDir
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOFLAGS=-mod=mod")
	out, err := cmd.Output()
	if err != nil {
		var stderr []byte
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		t.Fatalf("go run the calling stack failed: %v\nstdout: %s\nstderr: %s", err, out, stderr)
	}

	assertVpcSubnetsDoc(t, out)
}

// --- Validation error paths (UBI-129) ---

func TestGenerateGo_ForEach_MultipleForEachRejected(t *testing.T) {
	intent := mustIntentFile(t, `{
  "schema_version": 1, "kind": "ubx:intent/v1", "stack": "x",
  "intent": {"summary": "x"},
  "resources": [
    {"type": "aws_subnet", "name": "a-{availability_zones}", "op": "create", "for_each": "availability_zones",
     "config": {"availability_zone": "{availability_zones}"}},
    {"type": "aws_subnet", "name": "b-{availability_zones}", "op": "create", "for_each": "availability_zones",
     "config": {"availability_zone": "{availability_zones}"}}
  ]
}`)
	uf := vpcSubnetsUbxfile()
	if _, err := GenerateGo("vpc-subnets", uf, intent); err == nil || !strings.Contains(err.Error(), "only one for_each resource") {
		t.Fatalf("expected a multiple-for_each rejection, got: %v", err)
	}
}

func TestGenerateGo_ForEach_NonListParamRejected(t *testing.T) {
	intent := mustIntentFile(t, `{
  "schema_version": 1, "kind": "ubx:intent/v1", "stack": "x",
  "intent": {"summary": "x"},
  "resources": [
    {"type": "aws_subnet", "name": "a-{vpc_id}", "op": "create", "for_each": "vpc_id",
     "config": {"availability_zone": "{vpc_id}"}}
  ]
}`)
	uf := vpcSubnetsUbxfile()
	if _, err := GenerateGo("vpc-subnets", uf, intent); err == nil || !strings.Contains(err.Error(), "must name a list(string)/list(number) param") {
		t.Fatalf("expected a non-list-param for_each rejection, got: %v", err)
	}
}

func TestGenerateGo_ForEach_UndeclaredParamRejected(t *testing.T) {
	intent := mustIntentFile(t, `{
  "schema_version": 1, "kind": "ubx:intent/v1", "stack": "x",
  "intent": {"summary": "x"},
  "resources": [
    {"type": "aws_subnet", "name": "a-{nope}", "op": "create", "for_each": "nope",
     "config": {"availability_zone": "{nope}"}}
  ]
}`)
	uf := vpcSubnetsUbxfile()
	if _, err := GenerateGo("vpc-subnets", uf, intent); err == nil || !strings.Contains(err.Error(), "names no declared param") {
		t.Fatalf("expected an undeclared-param for_each rejection, got: %v", err)
	}
}

func TestGenerateGo_ForEach_OutputsConflictRejected(t *testing.T) {
	intent := mustIntentFile(t, vpcSubnetsIntent)
	uf := vpcSubnetsUbxfile()
	uf.Outputs = []Output{{Name: "x", Target: "subnet-{availability_zones}.arn"}}
	if _, err := GenerateGo("vpc-subnets", uf, intent); err == nil || !strings.Contains(err.Error(), "cannot also declare outputs") {
		t.Fatalf("expected a for_each+outputs conflict rejection, got: %v", err)
	}
}

func TestGenerateGo_ForEach_SiblingRefRejected(t *testing.T) {
	intent := mustIntentFile(t, `{
  "schema_version": 1, "kind": "ubx:intent/v1", "stack": "vpc-subnets",
  "intent": {"summary": "x"},
  "resources": [
    {"type": "aws_subnet", "name": "subnet-{availability_zones}", "op": "create", "for_each": "availability_zones",
     "config": {"availability_zone": "{availability_zones}"}},
    {"type": "aws_route_table_association", "name": "assoc", "op": "create",
     "config": {"subnet_id": {"$ref": {"to": "vpc-subnets.aws_subnet.subnet-{availability_zones}.id"}}}}
  ]
}`)
	uf := vpcSubnetsUbxfile()
	if _, err := GenerateGo("vpc-subnets", uf, intent); err == nil || !strings.Contains(err.Error(), "cannot be targeted by a sibling") {
		t.Fatalf("expected a sibling-$ref-to-for_each rejection, got: %v", err)
	}
}

func TestGenerateGo_ForEach_BareTokenOutsideForEachRejected(t *testing.T) {
	intent := mustIntentFile(t, `{
  "schema_version": 1, "kind": "ubx:intent/v1", "stack": "vpc-subnets",
  "intent": {"summary": "x"},
  "resources": [
    {"type": "aws_subnet", "name": "subnet-{availability_zones}", "op": "create", "for_each": "availability_zones",
     "config": {"availability_zone": "{availability_zones}"}},
    {"type": "aws_vpc", "name": "vpc", "op": "create",
     "config": {"zones": "{availability_zones}"}}
  ]
}`)
	uf := vpcSubnetsUbxfile()
	if _, err := GenerateGo("vpc-subnets", uf, intent); err == nil || !strings.Contains(err.Error(), "only valid inside its own for_each resource") {
		t.Fatalf("expected a bare-list-token-outside-for_each rejection, got: %v", err)
	}
}

// TestGenerateGo_ForEach_BareTokenNoForEachDeclaredRejected covers the
// OTHER path to the same refusal: a list-typed param is declared but NO
// resource in the whole document sets for_each at all (g.blueprint.ForEach
// is nil), so the bare-token check falls all the way through to the
// ordinary paramRef lookup, which must still refuse it (never silently
// treat a list param as an ordinary scalar reference).
func TestGenerateGo_ForEach_BareTokenNoForEachDeclaredRejected(t *testing.T) {
	intent := mustIntentFile(t, `{
  "schema_version": 1, "kind": "ubx:intent/v1", "stack": "vpc-subnets",
  "intent": {"summary": "x"},
  "resources": [
    {"type": "aws_vpc", "name": "vpc", "op": "create",
     "config": {"zones": "{availability_zones}"}}
  ]
}`)
	uf := vpcSubnetsUbxfile()
	if _, err := GenerateGo("vpc-subnets", uf, intent); err == nil || !strings.Contains(err.Error(), "can only be referenced inside its own for_each resource") {
		t.Fatalf("expected a bare-list-token-with-no-for_each-at-all rejection, got: %v", err)
	}
}

func TestGenerateGo_ForEach_IndexTokenOutsideForEachRejected(t *testing.T) {
	intent := mustIntentFile(t, `{
  "schema_version": 1, "kind": "ubx:intent/v1", "stack": "vpc-subnets",
  "intent": {"summary": "x"},
  "resources": [
    {"type": "aws_subnet", "name": "subnet-{availability_zones}", "op": "create", "for_each": "availability_zones",
     "config": {"availability_zone": "{availability_zones}"}},
    {"type": "aws_vpc", "name": "vpc", "op": "create",
     "config": {"note": "index {availability_zones_index}"}}
  ]
}`)
	uf := vpcSubnetsUbxfile()
	if _, err := GenerateGo("vpc-subnets", uf, intent); err == nil || !strings.Contains(err.Error(), "only valid inside") {
		t.Fatalf("expected an index-token-outside-for_each rejection, got: %v", err)
	}
}

// --- TypeScript (UBI-129) ---

func TestGenerateTS_ForEach_SignatureAndReturn(t *testing.T) {
	intent := mustIntentFile(t, vpcSubnetsIntent)
	files, err := GenerateTS("vpc-subnets", vpcSubnetsUbxfile(), intent)
	if err != nil {
		t.Fatalf("GenerateTS: %v", err)
	}
	fn := files["ts/vpcsubnets.ts"]

	if !strings.Contains(fn, "export function vpcSubnets(vpcId: string, availabilityZones: string[]): any[] {") {
		t.Fatalf("vpcsubnets.ts missing the any[]-returning signature:\n%s", fn)
	}
	if !strings.Contains(fn, "const subnetList: any[] = [];") {
		t.Fatalf("vpcsubnets.ts missing the accumulator array declaration:\n%s", fn)
	}
	if !strings.Contains(fn, "availabilityZones.forEach((availabilityZonesValue, availabilityZonesIndex) => {") {
		t.Fatalf("vpcsubnets.ts missing the real forEach loop over availabilityZones:\n%s", fn)
	}
	if !strings.Contains(fn, "const item = resource(Subnet, `subnet-${availabilityZonesValue}`, {") {
		t.Fatalf("vpcsubnets.ts missing the per-instance resource() call with a templated name:\n%s", fn)
	}
	if !strings.Contains(fn, "availabilityZone: availabilityZonesValue,") {
		t.Fatalf("vpcsubnets.ts missing the per-element availabilityZone field:\n%s", fn)
	}
	if !strings.Contains(fn, "cidrBlock: `10.0.${availabilityZonesIndex}.0/24`,") {
		t.Fatalf("vpcsubnets.ts missing the index-derived cidrBlock field:\n%s", fn)
	}
	if !strings.Contains(fn, "subnetList.push(item);") {
		t.Fatalf("vpcsubnets.ts missing the accumulator push:\n%s", fn)
	}
	if !strings.Contains(fn, "return subnetList;") {
		t.Fatalf("vpcsubnets.ts missing the final return of the accumulated list:\n%s", fn)
	}
}

func TestGenerateTS_ForEach_CompilesClean(t *testing.T) {
	requireDenoToolchain(t)

	intent := mustIntentFile(t, vpcSubnetsIntent)
	files, err := GenerateTS("vpc-subnets", vpcSubnetsUbxfile(), intent)
	if err != nil {
		t.Fatalf("GenerateTS: %v", err)
	}

	dir := t.TempDir()
	var tsFiles []string
	for name, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		tsFiles = append(tsFiles, full)
	}

	importMap := fmt.Sprintf(`{"imports":{"@ubx/sdk":%q}}`, sdkTSRuntimeIndexPath(t))
	if err := os.WriteFile(filepath.Join(dir, "deno.json"), []byte(importMap), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	args := append([]string{"check", "--no-remote"}, tsFiles...)
	cmd := exec.CommandContext(ctx, "deno", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("deno check of the generated for_each blueprint module failed:\n%s", out)
	}
}

// TestGenerateTS_ForEach_RuntimeUsable mirrors TestGenerateGo_ForEach_RuntimeUsable
// for the TS medium: a real `deno run` driver calls the generated
// function with three availability zones and confirms three distinct
// aws_subnet resources with correct per-instance values.
func TestGenerateTS_ForEach_RuntimeUsable(t *testing.T) {
	requireDenoToolchain(t)

	intent := mustIntentFile(t, vpcSubnetsIntent)
	files, err := GenerateTS("vpc-subnets", vpcSubnetsUbxfile(), intent)
	if err != nil {
		t.Fatalf("GenerateTS: %v", err)
	}

	dir := t.TempDir()
	for name, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	driver := `import { intent, stack } from "@ubx/sdk";
import { vpcSubnets } from "./ts/vpcsubnets.ts";

const s = stack("vpc-subnets", () => {
  intent({ summary: "x" });
  vpcSubnets("vpc-123", ["eu-central-1a", "eu-central-1b", "eu-central-1c"]);
});
console.log(JSON.stringify(s.evaluate()));
`
	if err := os.WriteFile(filepath.Join(dir, "driver.ts"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}

	importMap := fmt.Sprintf(`{"imports":{"@ubx/sdk":%q}}`, sdkTSRuntimeIndexPath(t))
	if err := os.WriteFile(filepath.Join(dir, "deno.json"), []byte(importMap), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "deno", "run", "--no-remote", "--allow-read", "driver.ts")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("deno run failed: %v\nstderr: %s", err, ee.Stderr)
		}
		t.Fatalf("deno run failed: %v", err)
	}

	assertVpcSubnetsDoc(t, out)
}

// --- Python (UBI-129) ---

func TestGeneratePython_ForEach_SignatureAndReturn(t *testing.T) {
	intent := mustIntentFile(t, vpcSubnetsIntent)
	files, err := GeneratePython("vpc-subnets", vpcSubnetsUbxfile(), intent)
	if err != nil {
		t.Fatalf("GeneratePython: %v", err)
	}
	fn := files["py/vpcsubnets.py"]

	if !strings.Contains(fn, "def vpc_subnets(vpc_id: str, availability_zones: list[str]) -> list[Any]:") {
		t.Fatalf("vpcsubnets.py missing the list[Any]-returning signature:\n%s", fn)
	}
	if !strings.Contains(fn, "subnet_list: list[Any] = []") {
		t.Fatalf("vpcsubnets.py missing the accumulator list declaration:\n%s", fn)
	}
	if !strings.Contains(fn, "for availability_zones_index, availability_zones_value in enumerate(availability_zones):") {
		t.Fatalf("vpcsubnets.py missing the real enumerate loop over availability_zones:\n%s", fn)
	}
	if !strings.Contains(fn, `item = sdk.resource(Subnet, f"subnet-{availability_zones_value}", SubnetConfig(`) {
		t.Fatalf("vpcsubnets.py missing the per-instance sdk.resource() call with a templated name:\n%s", fn)
	}
	if !strings.Contains(fn, "availability_zone=availability_zones_value,") {
		t.Fatalf("vpcsubnets.py missing the per-element availability_zone field:\n%s", fn)
	}
	if !strings.Contains(fn, `cidr_block=f"10.0.{availability_zones_index}.0/24",`) {
		t.Fatalf("vpcsubnets.py missing the index-derived cidr_block field:\n%s", fn)
	}
	if !strings.Contains(fn, "subnet_list.append(item)") {
		t.Fatalf("vpcsubnets.py missing the accumulator append:\n%s", fn)
	}
	if !strings.Contains(fn, "return subnet_list") {
		t.Fatalf("vpcsubnets.py missing the final return of the accumulated list:\n%s", fn)
	}
}

// TestGeneratePython_ForEach_RuntimeUsable mirrors
// TestGenerateGo_ForEach_RuntimeUsable for the Python medium: a real
// python3 driver calls the generated function with three availability
// zones and confirms three distinct aws_subnet resources with correct
// per-instance values.
func TestGeneratePython_ForEach_RuntimeUsable(t *testing.T) {
	requirePython3Toolchain(t)

	intent := mustIntentFile(t, vpcSubnetsIntent)
	files, err := GeneratePython("vpc-subnets", vpcSubnetsUbxfile(), intent)
	if err != nil {
		t.Fatalf("GeneratePython: %v", err)
	}

	dir := t.TempDir()
	pyDir := filepath.Join(dir, "py")
	if err := os.MkdirAll(pyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		rel := strings.TrimPrefix(name, "py/")
		if err := os.WriteFile(filepath.Join(pyDir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	driver := `import json

import ubx_sdk as sdk
from vpcsubnets import vpc_subnets


def describe():
    sdk.intent("x")
    vpc_subnets("vpc-123", ["eu-central-1a", "eu-central-1b", "eu-central-1c"])


doc = sdk.stack("vpc-subnets", describe).evaluate()
print(json.dumps(doc))
`
	driverPath := filepath.Join(pyDir, "_driver.py")
	if err := os.WriteFile(driverPath, []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", driverPath)
	cmd.Env = []string{"PYTHONPATH=" + sdkPyModuleRoot(t) + string(os.PathListSeparator) + pyDir}
	out, err := cmd.Output()
	if err != nil {
		var stderr []byte
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		t.Fatalf("python3 run failed: %v\nstdout: %s\nstderr: %s", err, out, stderr)
	}

	assertVpcSubnetsDoc(t, out)
}

// assertVpcSubnetsDoc is the shared JSON-shape assertion
// TestGenerateGo/TS/Python_ForEach_RuntimeUsable all apply to their own
// emitted intent/v1 document -- exactly 3 aws_subnet resources, each
// with its own distinct name, correct availability_zone, and
// index-derived cidr_block, proving all three languages' own generated
// for loops are behaviorally equivalent, not just independently
// plausible.
func assertVpcSubnetsDoc(t *testing.T, out []byte) {
	t.Helper()
	var doc struct {
		Resources []struct {
			Type   string `json:"type"`
			Name   string `json:"name"`
			Config struct {
				VpcID            string `json:"vpc_id"`
				AvailabilityZone string `json:"availability_zone"`
				CidrBlock        string `json:"cidr_block"`
			} `json:"config"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("parse emitted intent/v1: %v\noutput: %s", err, out)
	}
	if len(doc.Resources) != 3 {
		t.Fatalf("expected exactly 3 aws_subnet resources, got %d: %s", len(doc.Resources), out)
	}
	want := []struct{ name, az, cidr string }{
		{"subnet-eu-central-1a", "eu-central-1a", "10.0.0.0/24"},
		{"subnet-eu-central-1b", "eu-central-1b", "10.0.1.0/24"},
		{"subnet-eu-central-1c", "eu-central-1c", "10.0.2.0/24"},
	}
	for i, w := range want {
		r := doc.Resources[i]
		if r.Type != "aws_subnet" {
			t.Fatalf("resource[%d]: want type aws_subnet, got %q", i, r.Type)
		}
		if r.Name != w.name {
			t.Fatalf("resource[%d]: want name %q, got %q", i, w.name, r.Name)
		}
		if r.Config.VpcID != "vpc-123" {
			t.Fatalf("resource[%d]: want vpc_id %q, got %q", i, "vpc-123", r.Config.VpcID)
		}
		if r.Config.AvailabilityZone != w.az {
			t.Fatalf("resource[%d]: want availability_zone %q, got %q", i, w.az, r.Config.AvailabilityZone)
		}
		if r.Config.CidrBlock != w.cidr {
			t.Fatalf("resource[%d]: want cidr_block %q, got %q", i, w.cidr, r.Config.CidrBlock)
		}
	}
}

// --- Cross-medium calling: a comma-separated list arg (UBI-129) ---

// vpcSubnetsCallableUbxfile is a real, on-disk Ubxfile matching
// vpcSubnetsUbxfile's own declared params exactly -- ExpandCalls' own
// invokeCall re-parses the Ubxfile from disk (mirrors invoke_test.go's
// own callableUbxfileWithUnitConversion/writeCallableBlueprint
// precedent).
const vpcSubnetsCallableUbxfile = `lang: all

params:
  vpc_id: string, required
  availability_zones: list(string), required

resources: |
  placeholder -- never re-drafted by a blueprint CALL, only by
  ` + "`ubx blueprint build`" + `, which this test never runs.
`

// writeCallableVpcSubnetsBlueprint mirrors invoke_test.go's own
// writeCallableBlueprint, for the vpc-subnets for_each fixture instead
// of ci-platform.
func writeCallableVpcSubnetsBlueprint(t *testing.T, onlyLangs ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "vpc-subnets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, UbxfileName), []byte(vpcSubnetsCallableUbxfile), 0o644); err != nil {
		t.Fatal(err)
	}

	intent := mustIntentFile(t, vpcSubnetsIntent)
	ubxfile := vpcSubnetsUbxfile()

	want := map[string]bool{"go": true, "ts": true, "py": true}
	if len(onlyLangs) > 0 {
		want = map[string]bool{}
		for _, l := range onlyLangs {
			want[l] = true
		}
	}

	if want["go"] {
		files, err := GenerateGo("vpc-subnets", ubxfile, intent)
		if err != nil {
			t.Fatalf("GenerateGo: %v", err)
		}
		writeGeneratedFiles(t, dir, files, true)
	}
	if want["ts"] {
		files, err := GenerateTS("vpc-subnets", ubxfile, intent)
		if err != nil {
			t.Fatalf("GenerateTS: %v", err)
		}
		writeGeneratedFiles(t, dir, files, false)
	}
	if want["py"] {
		files, err := GeneratePython("vpc-subnets", ubxfile, intent)
		if err != nil {
			t.Fatalf("GeneratePython: %v", err)
		}
		writeGeneratedFiles(t, dir, files, false)
	}
	return dir
}

func writeGeneratedFiles(t *testing.T, dir string, files map[string]string, isGo bool) {
	t.Helper()
	for name, content := range files {
		if isGo && name == "go/go.mod" {
			content += "\nreplace github.com/ubiquex/ubx-sdk-go => " + sdkGoModuleRoot(t) + "\n"
		}
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestExpandCalls_ForEach_CommaSeparatedListArg is UBI-129's own required
// cross-medium proof: a diagram/md call's own Args map (identical shape
// either way) carries a list-typed param's value as ONE comma-separated
// string -- diagram/parse.go's own generic attribute capture and the md
// medium's own identical Args convention need zero changes to produce
// this (see renderArgLiteral's own doc comment) -- and ExpandCalls,
// running the REAL compiled TS function via the exact SAME mechanism
// Slice 5 already proved for scalar params, produces the correct 3
// aws_subnet resources with correct per-instance values, exactly like
// calling from the SDK with a real array literal directly.
func TestExpandCalls_ForEach_CommaSeparatedListArg(t *testing.T) {
	requireDenoToolchain(t)
	dir := writeCallableVpcSubnetsBlueprint(t, "ts")

	intent := &resolver.IntentFile{
		SchemaVersion: 1,
		Kind:          resolver.IntentFileKind,
		Stack:         "payments",
		BlueprintCalls: []resolver.BlueprintCall{
			{
				Name:      "vpc subnets call",
				Blueprint: dir,
				Args: map[string]string{
					"vpc_id":             "vpc-123",
					"availability_zones": "eu-central-1a, eu-central-1b, eu-central-1c",
				},
			},
		},
	}
	if err := ExpandCalls(context.Background(), intent); err != nil {
		t.Fatalf("ExpandCalls: %v", err)
	}
	if len(intent.BlueprintCalls) != 0 {
		t.Fatalf("ExpandCalls should clear BlueprintCalls, got %+v", intent.BlueprintCalls)
	}
	if len(intent.Resources) != 3 {
		t.Fatalf("expected exactly 3 aws_subnet resources, got %d: %+v", len(intent.Resources), intent.Resources)
	}

	wantAZ := []string{"eu-central-1a", "eu-central-1b", "eu-central-1c"}
	wantCIDR := []string{"10.0.0.0/24", "10.0.1.0/24", "10.0.2.0/24"}
	for i, ri := range intent.Resources {
		if ri.Type != "aws_subnet" {
			t.Fatalf("resource[%d]: want type aws_subnet, got %q", i, ri.Type)
		}
		if ri.Name != "subnet-"+wantAZ[i] {
			t.Fatalf("resource[%d]: want name %q, got %q", i, "subnet-"+wantAZ[i], ri.Name)
		}
		var cfg map[string]any
		if err := json.Unmarshal(ri.Config, &cfg); err != nil {
			t.Fatalf("resource[%d]: unmarshal config: %v", i, err)
		}
		if cfg["vpc_id"] != "vpc-123" {
			t.Fatalf("resource[%d]: vpc_id = %v, want %q", i, cfg["vpc_id"], "vpc-123")
		}
		if cfg["availability_zone"] != wantAZ[i] {
			t.Fatalf("resource[%d]: availability_zone = %v, want %q", i, cfg["availability_zone"], wantAZ[i])
		}
		if cfg["cidr_block"] != wantCIDR[i] {
			t.Fatalf("resource[%d]: cidr_block = %v, want %q", i, cfg["cidr_block"], wantCIDR[i])
		}
	}
}

// widgetListIntent/widgetListUbxfile are a real, live-found regression
// fixture (UBI-129): a for_each resource that references ONLY the
// per-element VALUE, never the index -- unlike vpcSubnetsIntent above
// (which uses both), this is what first surfaced a real Go compile bug
// live (`ubx resolve --from-code` against a real, hermetic fakeprovider
// stack): Go hard-refuses to compile a declared-but-unused range
// variable, and the generated loop header unconditionally declared
// BOTH the index and value variables regardless of whether the
// resource's own fields/name actually referenced the index at all.
const widgetListIntent = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "widget-list",
  "intent": {"summary": "One fake_widget per name"},
  "resources": [
    {
      "type": "fake_widget",
      "name": "widget-{widget_names}",
      "op": "create",
      "for_each": "widget_names",
      "config": {
        "name": "{widget_names}"
      }
    }
  ]
}`

func widgetListUbxfile() *Ubxfile {
	return &Ubxfile{
		Dir:  "testdata",
		Lang: "go",
		Params: []Param{
			{Name: "widget_names", Type: ParamListString, Required: true},
		},
	}
}

// TestGenerateGo_ForEach_IndexUnused_CompilesClean is the regression
// test for the real bug above: the generated loop header must use Go's
// own blank identifier ("_") for the index variable when nothing
// references {widget_names_index}, never an unconditionally-declared
// (and therefore unused) one.
func TestGenerateGo_ForEach_IndexUnused_CompilesClean(t *testing.T) {
	requireGoToolchain(t)

	intent := mustIntentFile(t, widgetListIntent)
	files, err := GenerateGo("widget-list", widgetListUbxfile(), intent)
	if err != nil {
		t.Fatalf("GenerateGo: %v", err)
	}
	fn := files["go/widgetlist.go"]
	if !strings.Contains(fn, "for _, widgetNamesValue := range widgetNames {") {
		t.Fatalf("widgetlist.go should use \"_\" for the unused index variable:\n%s", fn)
	}

	dir := t.TempDir()
	for name, content := range files {
		rel := strings.TrimPrefix(name, "go/")
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sdkGoRoot := sdkGoModuleRoot(t)
	goMod := files["go/go.mod"] + "\nreplace github.com/ubiquex/ubx-sdk-go => " + sdkGoRoot + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOFLAGS=-mod=mod")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build of the index-unused for_each blueprint package failed:\n%s", out)
	}
}

// TestGenerateGo_ForEach_NameMustVary_Rejected is the regression test
// for the second real issue caught alongside the one above: a for_each
// resource whose own Name never references either synthetic token would
// call sdk.Resource() with the IDENTICAL name on every iteration --
// refused at decode time, not left to fail confusingly at call time.
func TestGenerateGo_ForEach_NameMustVary_Rejected(t *testing.T) {
	intent := mustIntentFile(t, `{
  "schema_version": 1, "kind": "ubx:intent/v1", "stack": "widget-list",
  "intent": {"summary": "x"},
  "resources": [
    {"type": "fake_widget", "name": "widget", "op": "create", "for_each": "widget_names",
     "config": {"name": "{widget_names}"}}
  ]
}`)
	uf := widgetListUbxfile()
	if _, err := GenerateGo("widget-list", uf, intent); err == nil || !strings.Contains(err.Error(), "never references") {
		t.Fatalf("expected a name-must-vary rejection, got: %v", err)
	}
}
