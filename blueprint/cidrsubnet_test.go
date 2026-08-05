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
)

// cidrsubnetIntent is a small, hand-built resolved draft (UBI-125's
// tfconvert would build the identical shape mechanically) exercising a
// for_each resource whose own cidr_block Config field is a real
// {"$fn":{"name":"cidrsubnet",...}} marker referencing the loop's own
// synthetic index token -- the exact case a converted
// count=length(var.azs)/cidrsubnet(var.cidr, 8, count.index) module
// produces (tfconvert/tfconvert_test.go's own TestConvert_Cidrsubnet_Marker).
const cidrsubnetIntent = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "vpc-cidr",
  "intent": {"summary": "Subnets with cidrsubnet-derived CIDRs"},
  "resources": [
    {
      "type": "aws_subnet",
      "name": "private-{azs}",
      "op": "create",
      "for_each": "azs",
      "config": {
        "availability_zone": "{azs}",
        "cidr_block": {"$fn": {"name": "cidrsubnet", "args": ["{cidr}", 8, "{azs_index}"]}}
      }
    }
  ]
}`

func cidrsubnetUbxfile() *Ubxfile {
	return &Ubxfile{
		Dir:  "testdata",
		Lang: "go",
		Params: []Param{
			{Name: "cidr", Type: ParamString, Required: true},
			{Name: "azs", Type: ParamListString, Required: true},
		},
	}
}

func TestGenerateGo_Cidrsubnet_EmitsHelperOnlyWhenUsed(t *testing.T) {
	intent := mustIntentFile(t, cidrsubnetIntent)
	files, err := GenerateGo("vpc-cidr", cidrsubnetUbxfile(), intent)
	if err != nil {
		t.Fatalf("GenerateGo: %v", err)
	}
	if _, ok := files["go/cidrsubnet.go"]; !ok {
		t.Fatalf("expected go/cidrsubnet.go to be emitted, got files: %v", keysOf(files))
	}
	if !strings.Contains(files["go/vpccidr.go"], "cidrsubnet(cidr, 8, azsIndex)") {
		t.Fatalf("vpccidr.go missing the expected cidrsubnet(...) call:\n%s", files["go/vpccidr.go"])
	}

	// An ordinary blueprint (no $fn marker anywhere) must never carry the
	// helper -- confirmed directly, not just assumed from the "only when
	// used" doc comment.
	plainFiles, err := GenerateGo("vpc-subnets", vpcSubnetsUbxfile(), mustIntentFile(t, vpcSubnetsIntent))
	if err != nil {
		t.Fatalf("GenerateGo (plain): %v", err)
	}
	if _, ok := plainFiles["go/cidrsubnet.go"]; ok {
		t.Fatalf("an ordinary for_each blueprint (no cidrsubnet) must not carry go/cidrsubnet.go")
	}
}

// TestGenerateGo_Cidrsubnet_RuntimeUsable is this ticket's own required
// "produces the correct value, not just compiles" proof: a real `go run`
// of the generated package, called with a real VPC CIDR and three
// availability zones, must produce the SAME per-instance cidr_block
// values Terraform's own documented cidrsubnet() semantics would (also
// confirmed independently, by hand, against a standalone port of the
// same algorithm before this test existed).
func TestGenerateGo_Cidrsubnet_RuntimeUsable(t *testing.T) {
	requireGoToolchain(t)
	sdkGoRoot := sdkGoModuleRoot(t)

	intent := mustIntentFile(t, cidrsubnetIntent)
	files, err := GenerateGo("vpc-cidr", cidrsubnetUbxfile(), intent)
	if err != nil {
		t.Fatalf("GenerateGo: %v", err)
	}

	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "vpccidr")
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
		"require github.com/ubiquex/ubx-sdk-go v0.0.0\nrequire vpccidr v0.0.0\n\n" +
		"replace github.com/ubiquex/ubx-sdk-go => " + sdkGoRoot + "\n" +
		"replace vpccidr => " + pkgDir + "\n"
	if err := os.WriteFile(filepath.Join(stackDir, "go.mod"), []byte(stackGoMod), 0o644); err != nil {
		t.Fatal(err)
	}
	mainSrc := `package main

import (
	vpccidr "vpccidr"
	sdk "github.com/ubiquex/ubx-sdk-go/runtime"
)

func main() {
	sdk.Main(sdk.Stack("vpc-cidr", func() {
		sdk.Intent(sdk.IntentInfo{Summary: "x"})
		vpccidr.VpcCidr("10.0.0.0/16", []string{"eu-central-1a", "eu-central-1b", "eu-central-1c"})
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

	var doc struct {
		Resources []struct {
			Name   string `json:"name"`
			Config struct {
				CidrBlock string `json:"cidr_block"`
			} `json:"config"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("parse emitted intent/v1: %v\noutput: %s", err, out)
	}
	want := map[string]string{
		"private-eu-central-1a": "10.0.0.0/24",
		"private-eu-central-1b": "10.0.1.0/24",
		"private-eu-central-1c": "10.0.2.0/24",
	}
	if len(doc.Resources) != 3 {
		t.Fatalf("expected 3 resources, got %d: %s", len(doc.Resources), out)
	}
	for _, r := range doc.Resources {
		if want[r.Name] != r.Config.CidrBlock {
			t.Fatalf("resource %q: cidr_block = %q, want %q", r.Name, r.Config.CidrBlock, want[r.Name])
		}
	}
}

func TestGenerateTS_Cidrsubnet_Refused(t *testing.T) {
	intent := mustIntentFile(t, cidrsubnetIntent)
	_, err := GenerateTS("vpc-cidr", cidrsubnetUbxfile(), intent)
	if err == nil || !strings.Contains(err.Error(), "only supported in Go so far") {
		t.Fatalf("expected a named Go-only refusal, got: %v", err)
	}
}

func TestGeneratePython_Cidrsubnet_Refused(t *testing.T) {
	intent := mustIntentFile(t, cidrsubnetIntent)
	_, err := GeneratePython("vpc-cidr", cidrsubnetUbxfile(), intent)
	if err == nil || !strings.Contains(err.Error(), "only supported in Go so far") {
		t.Fatalf("expected a named Go-only refusal, got: %v", err)
	}
}

// TestCidrsubnetGoSource_KnownExamples confirms the ported algorithm
// itself against HashiCorp's own documented cidrsubnet() examples,
// independent of the blueprint-codegen integration above -- isolates
// "is the port correct" from "is it wired in correctly".
func TestCidrsubnetGoSource_KnownExamples(t *testing.T) {
	dir := t.TempDir()
	// fmt.Sprintf(cidrsubnetGoSource, "main") is the EXACT same rendering
	// GenerateGo itself uses (gogen.go) -- a complete, valid "package
	// main" file (imports already included) -- append a driver main()
	// reusing that same already-imported "fmt", rather than hand-
	// reconstructing the file another way.
	src := fmt.Sprintf(cidrsubnetGoSource, "main") + `
func main() {
	fmt.Println(cidrsubnet("10.0.0.0/16", 8, 2))
	fmt.Println(cidrsubnet("10.1.2.0/24", 4, 15))
	fmt.Println(cidrsubnet("10.0.0.0/16", 8, 0))
}
`
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	requireGoToolchain(t)
	out, err := exec.Command("go", "run", path).Output()
	if err != nil {
		t.Fatalf("go run: %v", err)
	}
	want := "10.0.2.0/24\n10.1.2.240/28\n10.0.0.0/24\n"
	if string(out) != want {
		t.Fatalf("cidrsubnet output = %q, want %q", out, want)
	}
}
