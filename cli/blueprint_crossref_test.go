package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gocloud.dev/blob"
	"gocloud.dev/blob/memblob"

	"github.com/ubiquex/ubiquex/blueprint"
	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
	"github.com/ubiquex/ubiquex/diagram"
	"github.com/ubiquex/ubiquex/ledgerstore"
)

// UBI-134: a real ParamCrossRef parameter, called with a real
// "@<stack>.<type>.<name>.<attr-path>" argument through all three
// calling mediums, each resolving via a real `ubx plan` run against a
// real stack-mode $cross neighbor (never git-local -- resolveCross's own
// "stack" mode requires a real base store, core/resolver/
// crossstack_addressing_test.go's own TestResolve_CrossStack_
// ByStackName_NoBaseIsGitLocal_Refused confirms this directly).

// crossRefBlueprintIntent is the fixture blueprint's own build-time
// resolved intent -- one resource, one cross_ref param threaded through
// {vpc_id} into a nested "tags" attribute (fake_widget's own schema has
// no top-level free-form string field -- provider/internal/fakeprovider's
// own real schema only has id/name/tags -- so, like blueprintCallIntent's
// own "peer_id" precedent, this goes inside tags, never a top-level
// attribute the schema doesn't recognize).
const crossRefBlueprintIntent = `{
  "schema_version": 1,
  "kind": "ubx:intent/v1",
  "stack": "platform",
  "intent": {"summary": "cross_ref blueprint call fixture"},
  "resources": [
    {
      "type": "fake_widget",
      "name": "primary",
      "op": "create",
      "config": {"name": "primary-widget", "tags": {"vpc_id": "{vpc_id}"}}
    }
  ]
}`

func crossRefBlueprintUbxfile() *blueprint.Ubxfile {
	return &blueprint.Ubxfile{
		Dir:  "testdata",
		Lang: "all",
		Params: []blueprint.Param{
			{Name: "vpc_id", Type: blueprint.ParamCrossRef, Required: true},
		},
	}
}

const crossRefBlueprintUbxfileContent = "lang: all\n\nparams:\n  vpc_id: cross_ref, required\n\nresources: |\n  placeholder -- never re-drafted by a blueprint CALL, only by\n  `ubx blueprint build`, which this test never runs.\n"

// writeCrossRefBlueprintPackage builds crossRefBlueprintIntent via BOTH
// GenerateGo (the direct-import SDK leg) and GenerateTS (the diagram/md
// legs -- ExpandCalls prefers ts/ when present), writing a real Ubxfile
// alongside both into one real on-disk blueprint package -- the SAME
// dual-build shape writeBlueprintCrossMediumPackage (blueprint_cross_
// medium_test.go) already established for UBI-74 Slice 5.
func writeCrossRefBlueprintPackage(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "platform")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, blueprint.UbxfileName), []byte(crossRefBlueprintUbxfileContent), 0o644); err != nil {
		t.Fatal(err)
	}

	var intent resolver.IntentFile
	if err := json.Unmarshal([]byte(crossRefBlueprintIntent), &intent); err != nil {
		t.Fatalf("parse crossRefBlueprintIntent: %v", err)
	}
	ubxfile := crossRefBlueprintUbxfile()

	sdkGoRoot := blueprintCallSdkGoRoot(t)
	goFiles, err := blueprint.GenerateGo("platform", ubxfile, &intent)
	if err != nil {
		t.Fatalf("GenerateGo: %v", err)
	}
	for name, content := range goFiles {
		if name == "go/go.mod" {
			content += "\nreplace github.com/ubiquex/ubx-sdk-go => " + sdkGoRoot + "\n"
		}
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tsFiles, err := blueprint.GenerateTS("platform", ubxfile, &intent)
	if err != nil {
		t.Fatalf("GenerateTS: %v", err)
	}
	for name, content := range tsFiles {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

// writeCrossRefGoCallingStack writes a plain SDK program -- no blueprint
// machinery of its own -- that imports pkgDir/go (the locally-built
// blueprint package) via a real Go local-path replace directive and
// calls its function with a real sdk.CrossStack(...) argument, proving
// ParamCrossRef's own GoType() == "sdk.CrossMarker" decision compiles
// and threads through a real direct-import call, not just ExpandCalls'
// own synthesized caller.
func writeCrossRefGoCallingStack(t *testing.T, dir, pkgGoDir, callArgs string) string {
	t.Helper()
	stackDir := filepath.Join(dir, "stack")
	if err := os.MkdirAll(stackDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sdkGoRoot := blueprintCallSdkGoRoot(t)
	goMod := "module crossref-call-fixture\n\ngo 1.23\n\n" +
		"require github.com/ubiquex/ubx-sdk-go v0.0.0\n" +
		"require platform v0.0.0\n\n" +
		"replace github.com/ubiquex/ubx-sdk-go => " + sdkGoRoot + "\n" +
		"replace platform => " + pkgGoDir + "\n"
	if err := os.WriteFile(filepath.Join(stackDir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	entry := filepath.Join(stackDir, "create_platform.go")
	src := "package main\n\n" +
		"import (\n" +
		"\tplatform \"platform\"\n" +
		"\tsdk \"github.com/ubiquex/ubx-sdk-go/runtime\"\n" +
		")\n\n" +
		"func main() {\n" +
		"\tsdk.Main(sdk.Stack(\"payments\", func() {\n" +
		"\t\tsdk.Intent(sdk.IntentInfo{Summary: \"payments, via a direct-import cross_ref blueprint call\"})\n" +
		"\t\tplatform.Platform(" + callArgs + ")\n" +
		"\t}))\n" +
		"}\n"
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return entry
}

// crossRefStoreFixture wires cfg.Ledger.Store at storeName and installs
// BOTH openRemoteLedgerStore (cli/ledgeropen.go's own seam -- the
// CALLING stack's own ledger open, via openLedgerForStack) AND
// core.RegisterRemoteLedgerOpener (core/openref.go -- the NEIGHBOR
// stack's own lookup, core/resolver/refs.go's resolveCross "stack" mode
// reaches this directly via core.OpenRef, never through
// openRemoteLedgerStore at all) onto ONE shared bucket, prefixed per
// stack -- registerFakeRemoteOpener's own convention
// (core/resolver/crossstack_addressing_test.go), extended here to also
// cover the CLI-level seam so two independently-addressed stacks
// (payments, networking) genuinely share one base without their own
// chains colliding. core.RegisterRemoteLedgerOpener is restored to its
// own real, production callback (cli/ledgeropen.go's init()) on cleanup
// -- it's a package-level global, not test-scoped, so leaving the fake
// installed would leak into every other test in this package's run.
func crossRefStoreFixture(t *testing.T) (storeName string, shared *blob.Bucket) {
	t.Helper()
	shared = memblob.OpenBucket(nil)
	t.Cleanup(func() { shared.Close() })

	prefixedOpener := func(ctx context.Context, ref string) (*ledgerstore.Store, func() error, error) {
		trimmed := strings.TrimSuffix(ref, "/")
		parts := strings.Split(trimmed, "/")
		stack := parts[len(parts)-1]
		return ledgerstore.New(blob.PrefixedBucket(shared, stack+"/")), func() error { return nil }, nil
	}

	origLedgerSeam := openRemoteLedgerStore
	openRemoteLedgerStore = prefixedOpener
	t.Cleanup(func() { openRemoteLedgerStore = origLedgerSeam })

	core.RegisterRemoteLedgerOpener(func(ctx context.Context, ref string) (core.LedgerStore, func() error, error) {
		return prefixedOpener(ctx, ref)
	})
	t.Cleanup(func() {
		core.RegisterRemoteLedgerOpener(func(ctx context.Context, storeURI string) (core.LedgerStore, func() error, error) {
			return ledgerstore.Open(ctx, storeURI)
		})
	})

	return "mem://acme-ledger", shared
}

// seedCrossRefNeighborNetworkingStack seeds a real "networking.aws_vpc.main"
// resource, folded immediately via the SAME adoption-shaped hand-built
// proposal pattern why_pinchain_test.go's own seedLedgerWithPinForTest
// established (UBI-57) -- reused directly here (pin args left empty: a
// plain seed, no cross_stack_pin of its own).
func seedCrossRefNeighborNetworkingStack(t *testing.T, shared *blob.Bucket) {
	t.Helper()
	neighborStore := ledgerstore.New(blob.PrefixedBucket(shared, "networking/"))
	neighbor := core.OpenStore(neighborStore)
	seedLedgerWithPinForTest(t, neighbor, core.Address{Stack: "networking", Type: "aws_vpc", Name: "main"}, `{"id":"vpc-123"}`, "", "", "")
}

// crossRefResolvedTagsVpcID extracts the resolved proposal's own single
// create's tags.vpc_id value -- the concrete value $cross's "stack" mode
// must have substituted in place of the raw "@networking.aws_vpc.main.id"
// call argument.
func crossRefResolvedTagsVpcID(t *testing.T, resolvedPath string) string {
	t.Helper()
	raw, err := os.ReadFile(resolvedPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Delta struct {
			Creates []json.RawMessage `json:"creates"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse resolved proposal: %v\nfile: %s", err, raw)
	}
	if len(doc.Delta.Creates) != 1 {
		t.Fatalf("expected exactly 1 create, got %d: %s", len(doc.Delta.Creates), raw)
	}
	var node struct {
		Config struct {
			Tags map[string]string `json:"tags"`
		} `json:"config"`
	}
	if err := json.Unmarshal(doc.Delta.Creates[0], &node); err != nil {
		t.Fatalf("parse create node: %v\nraw: %s", err, doc.Delta.Creates[0])
	}
	return node.Config.Tags["vpc_id"]
}

// TestBlueprintCall_CrossRefParam_Diagram_ResolvesRealCrossStackValue is
// UBI-134's own diagram-medium proof: a real .d2 ubx_blueprint node's
// own "vpc_id" attribute, "@networking.aws_vpc.main.id" -- captured
// verbatim by diagram/parse.go's own generic Args capture (never diagram/
// crossref.go's own separate reference-node "@" grammar, which is for a
// DIFFERENT node class entirely) -- rendered by renderArgLiteral into a
// real crossStack(...) construction in the synthesized TS caller, then
// resolved through the identical, unmodified core/resolver.Resolve every
// other entry point uses. The resolved config must carry the real
// "vpc-123" id, not the raw "@..." string.
func TestBlueprintCall_CrossRefParam_Diagram_ResolvesRealCrossStackValue(t *testing.T) {
	requireHermeticSandbox(t)
	requireDeno(t)
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	storeName, shared := crossRefStoreFixture(t)
	remoteConfigDir(t, storeName)
	seedCrossRefNeighborNetworkingStack(t, shared)

	pkgDir := writeCrossRefBlueprintPackage(t)

	d2Src := `
platform: "platform call" {
  class: ubx_blueprint
  blueprint: ` + jsonQuote(pkgDir) + `
  vpc_id: "@networking.aws_vpc.main.id"
}
`
	diagramIntent, err := diagram.Parse("crossref.d2", strings.NewReader(d2Src), "payments", nil, diagram.Options{})
	if err != nil {
		t.Fatalf("diagram.Parse: %v", err)
	}
	if len(diagramIntent.BlueprintCalls) != 1 {
		t.Fatalf("expected exactly 1 blueprint call from the diagram, got %d", len(diagramIntent.BlueprintCalls))
	}
	diagramIntent.Intent.Summary = "payments, via a diagram cross_ref blueprint call"
	diagramPath := filepath.Join(t.TempDir(), "diagram-draft.json")
	writeResolverIntentFile(t, diagramPath, diagramIntent)

	ledgerDir := t.TempDir()
	resolvedPath := filepath.Join(t.TempDir(), "resolved.json")
	out, err := runUbx(t, env, "plan", diagramPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--out", resolvedPath, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx plan (diagram leg): %v\noutput: %s", err, out)
	}

	if got := crossRefResolvedTagsVpcID(t, resolvedPath); got != "vpc-123" {
		t.Fatalf("resolved tags.vpc_id = %q, want the real neighbor value \"vpc-123\"", got)
	}
}

// TestBlueprintCall_CrossRefParam_MD_ResolvesRealCrossStackValue is
// UBI-134's own md-medium proof: a real blueprint_calls args entry
// (JSON-decoded string->string, intentprovider/validate.go, no special
// handling for an "@..." value -- confirmed directly, not assumed)
// carrying the identical raw "@networking.aws_vpc.main.id" value,
// resolving to the same real concrete value through `ubx plan --from-doc`.
func TestBlueprintCall_CrossRefParam_MD_ResolvesRealCrossStackValue(t *testing.T) {
	requireHermeticSandbox(t)
	requireDeno(t)
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	storeName, shared := crossRefStoreFixture(t)
	remoteConfigDir(t, storeName)
	seedCrossRefNeighborNetworkingStack(t, shared)

	pkgDir := writeCrossRefBlueprintPackage(t)

	mdArgs, err := json.Marshal(map[string]string{"vpc_id": "@networking.aws_vpc.main.id"})
	if err != nil {
		t.Fatal(err)
	}
	mdDraftJSON := `{
	  "schema_version": 1,
	  "kind": "ubx:intent/v1",
	  "stack": "payments",
	  "intent": {"summary": "payments, via an md cross_ref blueprint call", "assumptions": [], "defaults": [], "questions": []},
	  "resources": [],
	  "destroys": [],
	  "blueprint_calls": [{"name": "platform call", "blueprint": ` + jsonQuote(pkgDir) + `, "ref": "", "path": "", "args": ` + jsonQuote(string(mdArgs)) + `}]
	}`
	withBuildIntentAdapter(t, &fakeIntentAdapter{draft: mdDraftJSON})

	docPath := filepath.Join(t.TempDir(), "platform.md")
	writeFile(t, docPath, "Use blueprint platform with: vpc_id = @networking.aws_vpc.main.id")

	ledgerDir := t.TempDir()
	resolvedPath := filepath.Join(t.TempDir(), "resolved.json")
	out, err := runUbx(t, env, "plan", "--from-doc", docPath, "--stack", "payments", "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--out", resolvedPath, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx plan --from-doc (md leg): %v\noutput: %s", err, out)
	}

	if got := crossRefResolvedTagsVpcID(t, resolvedPath); got != "vpc-123" {
		t.Fatalf("resolved tags.vpc_id = %q, want the real neighbor value \"vpc-123\"", got)
	}
}

// TestBlueprintCall_CrossRefParam_SDKGo_ResolvesRealCrossStackValue is
// UBI-134's own direct-import SDK-medium proof: a hand-written Go
// program imports the built blueprint's OWN package directly (never
// through ExpandCalls' own synthesized caller at all) and passes
// sdk.CrossStack("networking", "networking.aws_vpc.main.id") as the real
// argument -- proving ParamCrossRef's own GoType() == "sdk.CrossMarker"
// decision produces a real, correctly-typed exported function signature
// that a hand-written caller can use exactly like any other SDK call.
func TestBlueprintCall_CrossRefParam_SDKGo_ResolvesRealCrossStackValue(t *testing.T) {
	requireHermeticSandbox(t)
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	storeName, shared := crossRefStoreFixture(t)
	remoteConfigDir(t, storeName)
	seedCrossRefNeighborNetworkingStack(t, shared)

	dir := t.TempDir()
	pkgDir := writeCrossRefBlueprintPackage(t)
	entry := writeCrossRefGoCallingStack(t, dir, filepath.Join(pkgDir, "go"), `sdk.CrossStack("networking", "networking.aws_vpc.main.id")`)

	ledgerDir := t.TempDir()
	resolvedPath := filepath.Join(t.TempDir(), "resolved.json")
	out, err := runUbx(t, env, "plan", "--from-code", entry, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--out", resolvedPath, "--timeout", "60s")
	if err != nil {
		t.Fatalf("ubx plan --from-code (SDK Go leg): %v\noutput: %s", err, out)
	}

	if got := crossRefResolvedTagsVpcID(t, resolvedPath); got != "vpc-123" {
		t.Fatalf("resolved tags.vpc_id = %q, want the real neighbor value \"vpc-123\"", got)
	}
}

// TestBlueprintCall_CrossRefParam_MalformedRef_Refused is UBI-134's own
// required refusal case: a call-site value for a ParamCrossRef param
// that isn't a real, well-formed "@<stack>.<type>.<name>.<attr-path>"
// reference must be a real, named parse error (parseCrossRefArg,
// blueprint/invoke.go) -- never silently accepted as an ordinary string
// literal, matching this project's "never a silent pass-through"
// standard (docs/resolver-adversarial.md).
func TestBlueprintCall_CrossRefParam_MalformedRef_Refused(t *testing.T) {
	requireHermeticSandbox(t)
	requireDeno(t)
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}

	storeName, shared := crossRefStoreFixture(t)
	remoteConfigDir(t, storeName)
	seedCrossRefNeighborNetworkingStack(t, shared)

	pkgDir := writeCrossRefBlueprintPackage(t)

	d2Src := `
platform: "platform call" {
  class: ubx_blueprint
  blueprint: ` + jsonQuote(pkgDir) + `
  vpc_id: "not-a-valid-reference"
}
`
	diagramIntent, err := diagram.Parse("crossref-malformed.d2", strings.NewReader(d2Src), "payments", nil, diagram.Options{})
	if err != nil {
		t.Fatalf("diagram.Parse: %v", err)
	}
	diagramIntent.Intent.Summary = "payments, via a malformed cross_ref blueprint call"
	diagramPath := filepath.Join(t.TempDir(), "diagram-draft.json")
	writeResolverIntentFile(t, diagramPath, diagramIntent)

	ledgerDir := t.TempDir()
	out, err := runUbx(t, env, "plan", diagramPath, "--provider", fakeProviderBinary, "--ledger-dir", ledgerDir, "--timeout", "60s")
	if err == nil {
		t.Fatalf("expected ubx plan to refuse a malformed cross_ref argument, got success:\n%s", out)
	}
	if !strings.Contains(err.Error(), "@<stack>.<type>.<name>.<attr-path>") {
		t.Fatalf("expected a real, named parse error naming the malformed reference, got: %v\noutput: %s", err, out)
	}
}
