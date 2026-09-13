package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/blueprint"
	"github.com/ubiquex/ubiquex/blueprint/spec"
)

// sampleDescription is a blueprint with everything the renderer has a
// branch for: a required param, an optional one, an output, and source
// names that differ from the wire names.
func sampleDescription() *blueprint.Description {
	return &blueprint.Description{
		Name:   "widget-bp",
		Lang:   "go",
		Source: "schema",
		Params: []spec.Param{
			{Name: "queue_name", Type: spec.ParamType("string"), Required: true},
			{Name: "retention_days", Type: spec.ParamType("number")},
		},
		Outputs: []spec.Output{{Name: "queue_url"}},
		Schema: &blueprint.Schema{
			SchemaVersion: blueprint.SchemaVersion,
			Name:          "widget-bp",
			Entrypoint: blueprint.Entrypoint{
				Language: "go", GoModule: "github.com/ubx-blueprints/widget-bp",
				GoPackage: "widgets", Function: "BuildWidget",
				ConfigType: "Config", OutputsType: "Outputs",
			},
			Params: []blueprint.SchemaParam{
				{Name: "queue_name", SourceName: "QueueName"},
				{Name: "retention_days", SourceName: "RetentionDays"},
			},
			Outputs: []blueprint.SchemaOutput{{Name: "queue_url", SourceName: "QueueURL"}},
		},
	}
}

// describe now renders in the shape every read command shares: a
// "Title  subject · facts" header and "▸ " sections, so a fifth surface
// cannot drift into a format of its own.
func TestRenderBlueprintDescription_UsesTheReadCommandShape(t *testing.T) {
	var buf bytes.Buffer
	renderBlueprintDescription(&buf, plainStyler(), sampleDescription())
	out := buf.String()

	if !strings.HasPrefix(out, "Blueprint  widget-bp · go · described by schema\n") {
		t.Errorf("header is not the shared shape:\n%s", out)
	}
	for _, want := range []string{"▸ entrypoint", "▸ params · 2", "▸ outputs · 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing section %q:\n%s", want, out)
		}
	}
	// The identifier as the author spelled it, which a caller needs and
	// cannot reconstruct from the wire name.
	if !strings.Contains(out, "[QueueName]") || !strings.Contains(out, "[QueueURL]") {
		t.Errorf("source names are missing:\n%s", out)
	}
}

// A section with nothing in it says so on its own line rather than
// carrying a "· 0" in its header, which reads as a fact worth noting.
func TestRenderBlueprintDescription_EmptySectionsHaveNoCount(t *testing.T) {
	d := sampleDescription()
	d.Params = nil
	d.Outputs = nil

	var buf bytes.Buffer
	renderBlueprintDescription(&buf, plainStyler(), d)
	out := buf.String()

	if strings.Contains(out, "· 0") {
		t.Errorf("an empty section carries a zero count:\n%s", out)
	}
	if strings.Count(out, "(none)") != 2 {
		t.Errorf("empty sections do not say (none):\n%s", out)
	}
}

// The palette, checked where it matters: a param name is yellow,
// required is green, a type is dim, the blueprint's identity is blue.
func TestRenderBlueprintDescription_Palette(t *testing.T) {
	var buf bytes.Buffer
	renderBlueprintDescription(&buf, styled(), sampleDescription())
	out := buf.String()

	for _, c := range []struct{ code, what string }{
		{ansiBlue + "widget-bp", "the blueprint's own name is not blue"},
		{ansiYellow + "queue_name", "a param name is not yellow"},
		{ansiGreen + "required", "required is not green"},
		{ansiDim + "string", "a type is not dim"},
		{ansiDim + "optional", "optional is not dim"},
	} {
		if !strings.Contains(out, c.code) {
			t.Error(c.what)
		}
	}
}

// Off a TTY, or with NO_COLOR set, the identical content renders with
// no escape sequences at all.
func TestRenderBlueprintDescription_DegradesToPlain(t *testing.T) {
	var colored, plain bytes.Buffer
	renderBlueprintDescription(&colored, styled(), sampleDescription())
	renderBlueprintDescription(&plain, plainStyler(), sampleDescription())

	if strings.Contains(plain.String(), "\x1b[") {
		t.Errorf("plain output carries escape sequences:\n%q", plain.String())
	}
	if stripANSI(colored.String()) != plain.String() {
		t.Error("color changes the content, not just the styling")
	}
}

func TestColorEnabled_HonorsNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	cmd := newBlueprintDescribeCmd()
	cmd.SetOut(os.Stdout)
	if colorEnabled(cmd) {
		t.Error("NO_COLOR did not disable color")
	}
}

// --json is built and marshaled separately from anything the styler
// touches, so it stays byte identical whatever the terminal is.
func TestBlueprintDescribeJSON_IsUnaffectedByStyling(t *testing.T) {
	dir := t.TempDir()
	writeFileT(t, dir+"/blueprint.py", pySampleBlueprint)

	run := func(noColor bool) string {
		if noColor {
			t.Setenv("NO_COLOR", "1")
		} else {
			os.Unsetenv("NO_COLOR")
		}
		var buf bytes.Buffer
		cmd := newBlueprintDescribeCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{dir, "--json"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("describe --json: %v", err)
		}
		return buf.String()
	}

	a, b := run(false), run(true)
	if a != b {
		t.Errorf("--json differs with NO_COLOR:\n%q\nvs\n%q", a, b)
	}
	if strings.Contains(a, "\x1b[") {
		t.Errorf("--json carries escape sequences: %q", a)
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(a), &probe); err != nil {
		t.Fatalf("--json is not valid JSON: %v\n%s", err, a)
	}
}

// The receipt shape package, push, pull and verify share: one line,
// detail indented beneath.
func TestWriteBlueprintReceipt_Shape(t *testing.T) {
	var buf bytes.Buffer
	writeBlueprintReceipt(&buf, plainStyler(), "packaged", "widget-bp", "/tmp/out.tar.gz",
		"sha256:f413a8b35b5f235159ed7bb001a152cb04bdf19833c80244b7d157269ab0f2e6", 3, "")

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want one line plus one indented detail, got:\n%s", buf.String())
	}
	if lines[0] != "packaged widget-bp -> /tmp/out.tar.gz" {
		t.Errorf("first line = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "    ") {
		t.Errorf("detail is not indented: %q", lines[1])
	}
	// Full, not truncated: this hash is the value a consumer compares
	// against, and a truncated one cannot be compared.
	if !strings.Contains(lines[1], "sha256:f413a8b35b5f235159ed7bb001a152cb04bdf19833c80244b7d157269ab0f2e6") {
		t.Errorf("the content hash is truncated: %q", lines[1])
	}
	if strings.Contains(lines[1], "…") {
		t.Errorf("the content hash carries a truncation marker: %q", lines[1])
	}
}

func TestWriteBlueprintReceipt_NoHashPrintsNoDetail(t *testing.T) {
	var buf bytes.Buffer
	writeBlueprintReceipt(&buf, plainStyler(), "pulled", "./local-bp", "/tmp/dest", "", 0, "")
	if got := strings.Count(buf.String(), "\n"); got != 1 {
		t.Errorf("a receipt with no hash printed %d lines:\n%s", got, buf.String())
	}
}

// A local pull never transfers, so it gets no bar. A bar that fills
// instantly is noise pretending to be feedback.
func TestPullProgressFor_OnlyTheTransferringPaths(t *testing.T) {
	var buf bytes.Buffer
	for _, source := range []string{"/tmp/some-dir", "./bp.tar.gz", "https://github.com/org/repo"} {
		if pullProgressFor(source, &buf, plainStyler(), true, 80) != nil {
			t.Errorf("%s got a progress bar and does not transfer bytes this can count", source)
		}
	}
	if pullProgressFor("oci://ghcr.io/org/bp:v1", &buf, plainStyler(), true, 80) == nil {
		t.Error("an oci:// pull got no progress bar")
	}
}

func writeFileT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const pySampleBlueprint = `from dataclasses import dataclass

from ubx_sdk import Computed


@dataclass
class Config:
    queue_name: str


@dataclass
class Outputs:
    queue_url: Computed


def widget_bp(cfg: Config) -> Outputs:
    ...
`
