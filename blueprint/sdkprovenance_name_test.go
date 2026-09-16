package blueprint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
)

// sdkprovenance_name_test.go covers UBI-257's second defect: a
// blueprint's identity was taken from the directory a consumer happened
// to put it in, not from what the blueprint calls itself.

// writeManifestedBlueprint writes a blueprint root whose recorded name
// deliberately differs from its directory name, which is exactly what
// `ubx blueprint pull <source> <dest>` produces when dest is named
// anything else.
func writeManifestedBlueprint(t *testing.T, parent, dirName, recordedName string) string {
	t.Helper()
	root := filepath.Join(parent, dirName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, UbxfileName), []byte("lang: go\n\nparams:\n  name: string, required\n\nresources: |\n  placeholder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"name":           recordedName,
		"files":          map[string]any{},
		"content_hash":   "sha256:deadbeef",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ManifestFileName), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// The name a blueprint calls itself travels with its bytes. The
// directory does not.
func TestBlueprintNameAt_PrefersTheRecordedName(t *testing.T) {
	root := writeManifestedBlueprint(t, t.TempDir(), "pulled-into-this-directory", "ubx-aws-sqs")
	if got := blueprintNameAt(root); got != "ubx-aws-sqs" {
		t.Errorf("got %q, want the name the blueprint recorded for itself", got)
	}
}

// An unpackaged working directory has no lock file yet, and used to
// work off the basename. It still does.
func TestBlueprintNameAt_FallsBackToTheDirectory(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "my-blueprint")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := blueprintNameAt(root); got != "my-blueprint" {
		t.Errorf("got %q, want the directory name when nothing was recorded", got)
	}
}

// The same bytes pulled into two different directories have to produce
// the SAME identity. Before this, they produced two, so the ledger's
// record of which blueprint made a resource depended on where the
// person who ran it happened to put the files.
func TestBlueprintNameAt_IdentityDoesNotDependOnPlacement(t *testing.T) {
	parent := t.TempDir()
	a := writeManifestedBlueprint(t, parent, "somewhere", "ubx-aws-sqs")
	b := writeManifestedBlueprint(t, parent, "somewhere-else", "ubx-aws-sqs")
	if blueprintNameAt(a) != blueprintNameAt(b) {
		t.Errorf("identity depends on the directory: %q vs %q", blueprintNameAt(a), blueprintNameAt(b))
	}
}

// The old error asserted the LAYOUT was wrong ("an Ubxfile-bearing
// parent of a go.mod'd package") when the layout was usually correct
// and only the name differed. That is what sent the ticket's author
// inspecting a structure with nothing wrong with it.
func TestApplyBlueprintRefs_ErrorNamesWhatWasFound(t *testing.T) {
	intent := &resolver.IntentFile{
		Stack: "payments",
		Resources: []resolver.ResourceIntent{{
			Type: "aws_sqs_queue", Name: "orders",
			Sources: []core.IntentSource{{Kind: "blueprint", Ref: "ubx-aws-sqs"}},
		}},
	}
	found := map[string]BlueprintProvenance{"something-else": {Ref: "something-else:sha256:abc"}}

	err := applyBlueprintRefs(intent, found, "no imported Go module resolves to one")
	if err == nil {
		t.Fatal("want a refusal for a name that does not resolve")
	}
	msg := err.Error()
	if !strings.Contains(msg, "something-else") {
		t.Errorf("the error has to name what it DID find, which is usually the diagnosis:\n%s", msg)
	}
	if !strings.Contains(msg, "blueprint.lock.json records") {
		t.Errorf("the error should point at where a blueprint's name comes from:\n%s", msg)
	}
}

// An empty result says something different from a near-miss, and the
// message should not imply a near-miss that does not exist.
func TestApplyBlueprintRefs_ErrorWhenNothingWasFoundAtAll(t *testing.T) {
	intent := &resolver.IntentFile{
		Stack: "payments",
		Resources: []resolver.ResourceIntent{{
			Type: "aws_sqs_queue", Name: "orders",
			Sources: []core.IntentSource{{Kind: "blueprint", Ref: "ubx-aws-sqs"}},
		}},
	}
	err := applyBlueprintRefs(intent, map[string]BlueprintProvenance{}, "no imported Go module resolves to one")
	if err == nil {
		t.Fatal("want a refusal")
	}
	if !strings.Contains(err.Error(), "No blueprint was found at all") {
		t.Errorf("an empty result should say so plainly:\n%s", err.Error())
	}
}
