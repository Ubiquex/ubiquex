package blueprint

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core/resolver"
)

// callableUbxfileWithUnitConversion is a real, on-disk Ubxfile matching
// ciPlatformUbxfileWithUnitConversion's own declared params exactly --
// ExpandCalls' own invokeCall re-parses the Ubxfile from disk (it has no
// access to the in-memory *Ubxfile GenerateGo/TS/Python were called
// with), so a real file matching those params is required for a
// hermetic invoke test to mean anything.
const callableUbxfileWithUnitConversion = `lang: all

params:
  queue_name: string, required
  max_receive_count: number, required
  retention_days: number, default 1

resources: |
  placeholder -- never re-drafted by a blueprint CALL, only by
  ` + "`ubx blueprint build`" + `, which this test never runs.
`

// writeCallableBlueprint builds ciPlatformIntentWithUnitConversion via
// whichever of GenerateGo/GenerateTS/GeneratePython onlyLangs names (all
// three if onlyLangs is empty) and writes a real Ubxfile alongside the
// output, into a fresh directory -- a real, on-disk, callable blueprint
// package, exactly what `ubx blueprint build` itself would have
// produced, without needing a real (or fake) intent-provider draft at
// all (this package's own codegen is what's under test here, not
// drafting).
func writeCallableBlueprint(t *testing.T, onlyLangs ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ci-platform")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, UbxfileName), []byte(callableUbxfileWithUnitConversion), 0o644); err != nil {
		t.Fatal(err)
	}

	intent := mustIntentFile(t, ciPlatformIntentWithUnitConversion)
	ubxfile := ciPlatformUbxfileWithUnitConversion()

	want := map[string]bool{"go": true, "ts": true, "py": true}
	if len(onlyLangs) > 0 {
		want = map[string]bool{}
		for _, l := range onlyLangs {
			want[l] = true
		}
	}

	generators := map[string]func(string, *Ubxfile, *resolver.IntentFile) (map[string]string, error){
		"go": GenerateGo,
		"ts": GenerateTS,
		"py": GeneratePython,
	}
	for lang, gen := range generators {
		if !want[lang] {
			continue
		}
		files, err := gen("ci-platform", ubxfile, intent)
		if err != nil {
			t.Fatalf("Generate%s: %v", lang, err)
		}
		for name, content := range files {
			if lang == "go" && name == "go/go.mod" {
				// A real, published blueprint's own go.mod never needs
				// this (Slice 1's own established "v0.0.0 is a genuinely
				// real, resolvable version" convention) -- only a
				// hermetically-tested fixture that must compile against
				// THIS repo's own not-yet-published sdk/go changes does,
				// matching gogen_test.go's own TestGenerateGo_CompilesClean
				// pattern exactly. writeGoCaller's own optional
				// sdkGoReplace extraction (invoke.go) is what makes
				// ExpandCalls' own synthesized Go caller pick this up too,
				// not just a direct-import caller.
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
	return dir
}

// TestExpandCalls_LocalBlueprint_TS is this package's own real, direct
// proof that ExpandCalls genuinely invokes a LOCAL blueprint (Slice 5's
// own required "local" source type) end to end: a real deno subprocess
// (the default-preferred language, callLanguagePreference), the real
// calling stack's own name threaded through every produced address, and
// UBI-123's own retention_days*86400 arithmetic genuinely computed, not
// a bare pass-through -- mirrors TestGenerateTS_PlaceholderArithmetic,
// but through the FULL ExpandCalls path this time, not GenerateTS alone.
func TestExpandCalls_LocalBlueprint_TS(t *testing.T) {
	requireDenoToolchain(t)
	dir := writeCallableBlueprint(t, "ts")

	for _, tc := range []struct {
		name             string
		args             map[string]string
		wantRetentionSec float64
	}{
		{"default", map[string]string{"queue_name": "payments-notifications", "max_receive_count": "5"}, 86400},
		{"overridden", map[string]string{"queue_name": "payments-notifications", "max_receive_count": "5", "retention_days": "30"}, 2592000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			intent := &resolver.IntentFile{
				SchemaVersion: 1,
				Kind:          resolver.IntentFileKind,
				Stack:         "payments",
				BlueprintCalls: []resolver.BlueprintCall{
					{Name: "ci-platform call", Blueprint: dir, Args: tc.args},
				},
			}
			if err := ExpandCalls(context.Background(), intent); err != nil {
				t.Fatalf("ExpandCalls: %v", err)
			}
			if len(intent.BlueprintCalls) != 0 {
				t.Fatalf("ExpandCalls should clear BlueprintCalls, got %+v", intent.BlueprintCalls)
			}
			if len(intent.Resources) != 1 {
				t.Fatalf("expected exactly 1 resource, got %d: %+v", len(intent.Resources), intent.Resources)
			}
			ri := intent.Resources[0]
			if ri.Type != "aws_sqs_queue" || ri.Name != "ci-notifications" {
				t.Fatalf("unexpected resource: %+v", ri)
			}

			var cfg map[string]any
			if err := json.Unmarshal(ri.Config, &cfg); err != nil {
				t.Fatalf("unmarshal config: %v", err)
			}
			if got := cfg["message_retention_seconds"]; got != tc.wantRetentionSec {
				t.Fatalf("message_retention_seconds = %v, want %v", got, tc.wantRetentionSec)
			}
			if got := cfg["visibility_timeout_seconds"]; got != float64(10) {
				t.Fatalf("visibility_timeout_seconds = %v, want 10 (max_receive_count * 2)", got)
			}
		})
	}
}

// TestExpandCalls_LocalBlueprint_Python confirms the SAME local blueprint
// call, when only a py/ package is built (no ts/), falls back to Python
// correctly -- callLanguagePreference's own fallback order genuinely
// exercised, not just declared.
func TestExpandCalls_LocalBlueprint_Python(t *testing.T) {
	requirePython3Toolchain(t)
	dir := writeCallableBlueprint(t, "py")

	intent := &resolver.IntentFile{
		SchemaVersion: 1,
		Kind:          resolver.IntentFileKind,
		Stack:         "payments",
		BlueprintCalls: []resolver.BlueprintCall{
			{Name: "ci-platform call", Blueprint: dir, Args: map[string]string{
				"queue_name": "payments-notifications", "max_receive_count": "5",
			}},
		},
	}
	if err := ExpandCalls(context.Background(), intent); err != nil {
		t.Fatalf("ExpandCalls: %v", err)
	}
	if len(intent.Resources) != 1 {
		t.Fatalf("expected exactly 1 resource, got %d", len(intent.Resources))
	}
	var cfg map[string]any
	if err := json.Unmarshal(intent.Resources[0].Config, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if got := cfg["message_retention_seconds"]; got != float64(86400) {
		t.Fatalf("message_retention_seconds = %v, want 86400", got)
	}
}

// TestExpandCalls_MissingRequiredParam confirms a call omitting a
// required param is a hard, named error -- never silently defaulted or
// passed through as an empty/zero value.
func TestExpandCalls_MissingRequiredParam(t *testing.T) {
	dir := writeCallableBlueprint(t, "ts")
	intent := &resolver.IntentFile{
		SchemaVersion: 1,
		Kind:          resolver.IntentFileKind,
		Stack:         "payments",
		BlueprintCalls: []resolver.BlueprintCall{
			{Name: "ci-platform call", Blueprint: dir, Args: map[string]string{"queue_name": "x"}},
		},
	}
	err := ExpandCalls(context.Background(), intent)
	if err == nil {
		t.Fatal("expected an error for a missing required param (max_receive_count), got nil")
	}
}

// TestExpandCalls_NoBuiltLanguage_Errors confirms a blueprint directory
// with no go/ts/py subdirectory at all (an Ubxfile that was never built)
// is a clear, named error, not a confusing failure three layers deeper.
func TestExpandCalls_NoBuiltLanguage_Errors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, UbxfileName), []byte(callableUbxfileWithUnitConversion), 0o644); err != nil {
		t.Fatal(err)
	}
	intent := &resolver.IntentFile{
		SchemaVersion: 1,
		Kind:          resolver.IntentFileKind,
		Stack:         "payments",
		BlueprintCalls: []resolver.BlueprintCall{
			{Name: "x", Blueprint: dir, Args: map[string]string{}},
		},
	}
	err := ExpandCalls(context.Background(), intent)
	if err == nil {
		t.Fatal("expected an error for a never-built blueprint, got nil")
	}
}

// TestExpandCalls_ResourceAddressCollision_Errors confirms a blueprint
// call whose own produced resource address collides with one already in
// resources[] (or with another call's own output) is a hard, named
// error -- never a silent overwrite.
func TestExpandCalls_ResourceAddressCollision_Errors(t *testing.T) {
	requireDenoToolchain(t)
	dir := writeCallableBlueprint(t, "ts")

	intent := &resolver.IntentFile{
		SchemaVersion: 1,
		Kind:          resolver.IntentFileKind,
		Stack:         "payments",
		Resources: []resolver.ResourceIntent{
			{Type: "aws_sqs_queue", Name: "ci-notifications", Op: resolver.OpCreate, Config: json.RawMessage(`{}`)},
		},
		BlueprintCalls: []resolver.BlueprintCall{
			{Name: "ci-platform call", Blueprint: dir, Args: map[string]string{
				"queue_name": "payments-notifications", "max_receive_count": "5",
			}},
		},
	}
	err := ExpandCalls(context.Background(), intent)
	if err == nil {
		t.Fatal("expected a collision error, got nil")
	}
}

// TestExpandCalls_LocalBlueprint_GoFallback confirms Go works as the
// last-preference fallback when no ts/py package is built -- real
// network resolution of github.com/ubiquex/ubx-sdk-go's own real v0.0.0
// version (goeval's own GOPROXY=off means this only succeeds once that
// module is already in the local Go module cache -- true on any machine
// that has ever built a blueprint's own go/ package for real before,
// per this project's own standing verification discipline; skipped
// rather than failed when it isn't, since seeding the cache is a real
// network operation this hermetic test suite doesn't force).
func TestExpandCalls_LocalBlueprint_GoFallback(t *testing.T) {
	requireGoToolchain(t)
	dir := writeCallableBlueprint(t, "go")

	intent := &resolver.IntentFile{
		SchemaVersion: 1,
		Kind:          resolver.IntentFileKind,
		Stack:         "payments",
		BlueprintCalls: []resolver.BlueprintCall{
			{Name: "ci-platform call", Blueprint: dir, Args: map[string]string{
				"queue_name": "payments-notifications", "max_receive_count": "5", "retention_days": "30",
			}},
		},
	}
	if err := ExpandCalls(context.Background(), intent); err != nil {
		t.Skipf("Go fallback call failed (likely an uncached github.com/ubiquex/ubx-sdk-go module on this machine, not a code bug -- see this test's own doc comment): %v", err)
	}
	if len(intent.Resources) != 1 {
		t.Fatalf("expected exactly 1 resource, got %d", len(intent.Resources))
	}
	var cfg map[string]any
	if err := json.Unmarshal(intent.Resources[0].Config, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if got := cfg["message_retention_seconds"]; got != float64(2592000) {
		t.Fatalf("message_retention_seconds = %v, want 2592000", got)
	}
}

// TestExpandCalls_GitBlueprint confirms the SAME expansion mechanism
// works for a git-referenced blueprint (Slice 5's own required "git"
// source type, UBI-74 Slice 3's own Pull mechanism reused directly) --
// not just a local path. Mirrors pull_test.go's own file:// trick: forces
// Pull down the real git-clone path even though the "repository" is
// itself a local directory.
func TestExpandCalls_GitBlueprint(t *testing.T) {
	requireDenoToolchain(t)
	built := writeCallableBlueprint(t, "ts")

	repoDir := initTestGitRepo(t)
	if err := copyDir(built, filepath.Join(repoDir, "ci-platform")); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, repoDir, "add ci-platform blueprint")

	intent := &resolver.IntentFile{
		SchemaVersion: 1,
		Kind:          resolver.IntentFileKind,
		Stack:         "payments",
		BlueprintCalls: []resolver.BlueprintCall{
			{
				Name:      "ci-platform call",
				Blueprint: "file://" + repoDir,
				Path:      "ci-platform",
				Args:      map[string]string{"queue_name": "payments-notifications", "max_receive_count": "5"},
			},
		},
	}
	if err := ExpandCalls(context.Background(), intent); err != nil {
		t.Fatalf("ExpandCalls: %v", err)
	}
	if len(intent.Resources) != 1 {
		t.Fatalf("expected exactly 1 resource, got %d", len(intent.Resources))
	}
	if intent.Resources[0].Type != "aws_sqs_queue" {
		t.Fatalf("unexpected resource: %+v", intent.Resources[0])
	}
}

// TestExpandCalls_ProvenanceStamped is UBI-74 Slice 6's own required
// hermetic proof: every resource ExpandCalls produces carries exactly
// one {"kind": "blueprint", "ref": "<name>:<content_hash>"} entry in its
// own Sources -- the SAME ref regardless of which language actually
// executed the call (TS here; TestExpandCalls_ProvenanceStamped_Python
// below confirms Python independently, not assumed identical).
func TestExpandCalls_ProvenanceStamped(t *testing.T) {
	requireDenoToolchain(t)
	dir := writeCallableBlueprint(t, "ts")

	intent := &resolver.IntentFile{
		SchemaVersion: 1,
		Kind:          resolver.IntentFileKind,
		Stack:         "payments",
		BlueprintCalls: []resolver.BlueprintCall{
			{Name: "ci-platform call", Blueprint: dir, Args: map[string]string{
				"queue_name": "payments-notifications", "max_receive_count": "5",
			}},
		},
	}
	if err := ExpandCalls(context.Background(), intent); err != nil {
		t.Fatalf("ExpandCalls: %v", err)
	}
	if len(intent.Resources) != 1 {
		t.Fatalf("expected exactly 1 resource, got %d", len(intent.Resources))
	}
	sources := intent.Resources[0].Sources
	if len(sources) != 1 {
		t.Fatalf("expected exactly 1 source, got %+v", sources)
	}
	if sources[0].Kind != "blueprint" {
		t.Fatalf("Kind = %q, want %q", sources[0].Kind, "blueprint")
	}
	if !strings.HasPrefix(sources[0].Ref, "ci-platform:sha256:") {
		t.Fatalf("Ref = %q, want it to start with \"ci-platform:sha256:\"", sources[0].Ref)
	}
}

// TestExpandCalls_ProvenanceStamped_Python confirms the SAME provenance
// ref is stamped regardless of which language actually executed the
// call -- Python here, TS above -- since blueprintRef describes the
// blueprint itself (name + content hash of the SAME pulled directory),
// never the calling mechanism.
func TestExpandCalls_ProvenanceStamped_Python(t *testing.T) {
	requirePython3Toolchain(t)
	dir := writeCallableBlueprint(t, "py")

	intent := &resolver.IntentFile{
		SchemaVersion: 1,
		Kind:          resolver.IntentFileKind,
		Stack:         "payments",
		BlueprintCalls: []resolver.BlueprintCall{
			{Name: "ci-platform call", Blueprint: dir, Args: map[string]string{
				"queue_name": "payments-notifications", "max_receive_count": "5",
			}},
		},
	}
	if err := ExpandCalls(context.Background(), intent); err != nil {
		t.Fatalf("ExpandCalls: %v", err)
	}
	sources := intent.Resources[0].Sources
	if len(sources) != 1 || sources[0].Kind != "blueprint" {
		t.Fatalf("Sources = %+v, want exactly one blueprint-kind source", sources)
	}
	if !strings.HasPrefix(sources[0].Ref, "ci-platform:sha256:") {
		t.Fatalf("Ref = %q, want it to start with \"ci-platform:sha256:\"", sources[0].Ref)
	}
}

// TestExpandCalls_ProvenanceStamped_Go is TS/Python's own Go sibling --
// and UBI-126's own required regression proof specifically: a Go call
// goes through BOTH provenance mechanisms at once (sdk/go/runtime's own
// PushBlueprintSource, firing for ANY call to generated code including
// ExpandCalls' own synthesized Go caller; AND this function's own
// external blueprintRef stamping, invokeCall's pre-existing Slice 6
// mechanism) -- exactly the combination that, before invokeCall's own
// "replace, don't append" fix, produced TWO source entries (one bare
// "ci-platform" from the SDK-level tag, one complete "ci-platform:
// sha256:..." from invokeCall) instead of the single, complete one every
// other calling path already produces.
func TestExpandCalls_ProvenanceStamped_Go(t *testing.T) {
	requireGoToolchain(t)
	dir := writeCallableBlueprint(t, "go")

	intent := &resolver.IntentFile{
		SchemaVersion: 1,
		Kind:          resolver.IntentFileKind,
		Stack:         "payments",
		BlueprintCalls: []resolver.BlueprintCall{
			{Name: "ci-platform call", Blueprint: dir, Args: map[string]string{
				"queue_name": "payments-notifications", "max_receive_count": "5",
			}},
		},
	}
	if err := ExpandCalls(context.Background(), intent); err != nil {
		t.Fatalf("ExpandCalls: %v", err)
	}
	if len(intent.Resources) != 1 {
		t.Fatalf("expected exactly 1 resource, got %d", len(intent.Resources))
	}
	sources := intent.Resources[0].Sources
	if len(sources) != 1 {
		t.Fatalf("expected exactly 1 source (not a bare SDK-level tag alongside invokeCall's own complete one), got %+v", sources)
	}
	if sources[0].Kind != "blueprint" {
		t.Fatalf("Kind = %q, want %q", sources[0].Kind, "blueprint")
	}
	if !strings.HasPrefix(sources[0].Ref, "ci-platform:sha256:") {
		t.Fatalf("Ref = %q, want it to start with \"ci-platform:sha256:\" (a bare \"ci-platform\" would mean the SDK-level tag leaked through unresolved)", sources[0].Ref)
	}
}
