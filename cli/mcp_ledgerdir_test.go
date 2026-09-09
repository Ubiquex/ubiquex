package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ledger_dir, and the two ways it lied to a caller.
//
// Both were reported from a real Claude Desktop session against the
// live server, not found by reading this code.
//
// First: a wrong path came back as a successful empty result. core.Open
// is a pure constructor that stats nothing (core/ledger.go), so a
// ledger "opened" at a directory that does not exist walks an absent
// tree, finds no proposals, and reports total: 0 with no error. Three
// genuinely different situations produced byte-identical output -- a
// stack that tracks nothing, a directory that was never a ubx root, and
// a path that does not exist at all. The model hit this and said
// explicitly that it could not tell them apart. A person running
// `ubx status` at least sees their own cwd; an MCP caller supplied the
// path blind and has no second channel to check it against.
//
// Second: a leading tilde was not expanded. A model writes
// "~/stacks/payments" because that is how a human writes a path, and
// there is no shell in front of an MCP call to expand it, so Go read it
// as a relative directory literally named "~" -- which then resolved
// under the server's own cwd, found nothing, and returned the same
// successful empty result as everything else. Silently treating ~/x as
// a directory named "~" is the worst of the three available answers:
// expanding is right, refusing is at least honest, and this was
// neither.
//
// The two are one change because expansion is only safe once the first
// is fixed. If the server's home is not the home the model meant, an
// expanded path is still wrong; without the root check that wrongness
// comes back as another silent empty result, and the fix would have
// moved the ambiguity rather than removed it.

// notARoot is a real, readable directory that has nothing to do with
// ubx -- the "wrong path" case, distinct from a path that is simply
// absent.
func notARoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("unrelated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Every tool that takes ledger_dir refuses a path that is not a ubx
// root, rather than reporting it as an empty ledger. Covered per tool
// because each opens the ledger through its own compute*JSON function.
func TestMCP_LedgerDir_NotAUbxRootIsRefused(t *testing.T) {
	session := connectMCPTestClient(t)

	for _, dir := range []struct{ name, path string }{
		{"nonexistent", filepath.Join(t.TempDir(), "no-such-directory")},
		{"real directory, never a ubx root", notARoot(t)},
	} {
		t.Run(dir.name, func(t *testing.T) {
			for _, call := range []struct {
				tool string
				args map[string]any
			}{
				{"ubx_status", map[string]any{"ledger_dir": dir.path}},
				{"ubx_why", map[string]any{"query": "payments.fake_widget.anything", "ledger_dir": dir.path}},
				{"ubx_scan", map[string]any{
					"stack": "payments", "type": "fake_widget", "name": "x",
					"lookup": `{"name":"x"}`, "provider_path": fakeProviderBinary,
					"ledger_dir": dir.path,
				}},
			} {
				res := callTool(t, session, call.tool, call.args)
				if !res.IsError {
					t.Fatalf("%s reported success for a path that is not a ubx root: %s", call.tool, toolTextContent(t, res))
				}
				text := toolTextContent(t, res)
				if !strings.Contains(text, "not a ubx root") {
					t.Errorf("%s: expected the error to say the path is not a ubx root, got: %s", call.tool, text)
				}
				if !strings.Contains(text, dir.path) {
					t.Errorf("%s: the error must name the path it rejected, so the caller can see what it actually looked at, got: %s", call.tool, text)
				}
			}
		})
	}
}

// The point of the whole change: a stack that genuinely tracks nothing
// still answers, and now means something. `ubx init` writes
// .ubx/config.hcl and no ledger/ at all (ledger/ is created lazily on
// the first accept), so this is the exact shape a freshly initialized
// stack has, and it must stay distinguishable from a wrong path rather
// than being swept up by the check.
func TestMCP_LedgerDir_FreshlyInitializedRootReportsZero(t *testing.T) {
	dir := ubxRoot(t)
	if _, err := os.Stat(filepath.Join(dir, "ledger")); !os.IsNotExist(err) {
		t.Fatalf("precondition failed: a freshly initialized root has no ledger/ yet, stat said: %v", err)
	}

	session := connectMCPTestClient(t)
	res := callTool(t, session, "ubx_status", map[string]any{"ledger_dir": dir})
	if res.IsError {
		t.Fatalf("a freshly initialized stack must report zero resources, not an error: %s", toolTextContent(t, res))
	}
	summary := res.StructuredContent.(map[string]any)["summary"].(map[string]any)
	if summary["total"] != float64(0) {
		t.Fatalf("expected total: 0, got: %v", summary)
	}
}

// The discriminator is `.ubx/`, not `.ubx/` plus a config file.
//
// A ledger built by `ubx accept --ledger-dir <dir>` is real and fully
// working, and holds .ubx/ledger.lock, .ubx/salt and ledger/ but NO
// config file of its own -- verified against what buildFixtureLedger
// actually leaves on disk, which is ubx's own code creating it. A check
// that required a config file would refuse this, so it checks for the
// directory. Both of ubx's legitimate zero-or-more shapes have .ubx/;
// only one of them has ledger/, and only the other has a config.
func TestMCP_LedgerDir_AcceptedLedgerWithoutConfigIsARoot(t *testing.T) {
	ledgerDir, addr, _ := buildFixtureLedger(t)
	if pickConfigFile(ledgerDir) != "" {
		t.Fatalf("precondition failed: this fixture is meant to have no config file of its own, found %q", pickConfigFile(ledgerDir))
	}
	if _, err := os.Stat(filepath.Join(ledgerDir, ".ubx")); err != nil {
		t.Fatalf("precondition failed: a ledger built by ubx accept must have .ubx/: %v", err)
	}

	session := connectMCPTestClient(t)
	res := callTool(t, session, "ubx_why", map[string]any{"query": addr, "ledger_dir": ledgerDir})
	if res.IsError {
		t.Fatalf("a real ledger with no config file of its own was refused: %s", toolTextContent(t, res))
	}
}

// With ledger_dir omitted the server falls back to its own cwd, which
// the caller cannot see. If that is not a root, the error has to name
// it -- otherwise the model is told "not a ubx root" about a path it
// never supplied and cannot inspect.
func TestMCP_LedgerDir_DefaultNamesTheServersOwnCwd(t *testing.T) {
	session := connectMCPTestClient(t)
	res := callTool(t, session, "ubx_status", map[string]any{})
	if !res.IsError {
		t.Skip("the test binary's own cwd is a ubx root, so the default cannot be exercised here")
	}
	text := toolTextContent(t, res)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, cwd) {
		t.Fatalf("the error must name the server's own current directory, since the caller cannot see it; got: %s", text)
	}
	if !strings.Contains(text, "ledger_dir") {
		t.Fatalf("the error must point at the parameter that fixes it, got: %s", text)
	}
}

// A leading tilde resolves against the server's home and finds the real
// ledger there. Proved end to end through a real fixture ledger rather
// than by unit-testing the expansion alone: what matters is that
// "~/payments" and the absolute path return the same resource.
func TestMCP_LedgerDir_TildeExpandsToTheRealLedger(t *testing.T) {
	home := t.TempDir()
	orig := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = orig })

	stackDir := filepath.Join(home, "payments")
	if err := os.MkdirAll(stackDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_, addr, _ := buildFixtureLedgerAt(t, stackDir)

	session := connectMCPTestClient(t)
	res := callTool(t, session, "ubx_status", map[string]any{"ledger_dir": "~/payments"})
	if res.IsError {
		t.Fatalf("a tilde path was not expanded: %s", toolTextContent(t, res))
	}
	payload := res.StructuredContent.(map[string]any)
	resources, _ := payload["resources"].([]any)
	if len(resources) != 1 {
		t.Fatalf("expected the one fixture resource through the tilde path, got: %v", payload)
	}

	// Same answer as the absolute path, which is the actual claim.
	absRes := callTool(t, session, "ubx_status", map[string]any{"ledger_dir": stackDir})
	absResources, _ := absRes.StructuredContent.(map[string]any)["resources"].([]any)
	if len(absResources) != len(resources) {
		t.Fatalf("tilde and absolute paths disagreed: %d vs %d", len(resources), len(absResources))
	}

	whyRes := callTool(t, session, "ubx_why", map[string]any{"query": addr, "ledger_dir": "~/payments"})
	if whyRes.IsError {
		t.Fatalf("ubx_why did not expand a tilde path: %s", toolTextContent(t, whyRes))
	}
}

// A bare "~" is the home directory itself, not a directory named "~".
func TestMCP_LedgerDir_BareTildeIsHome(t *testing.T) {
	home := t.TempDir()
	orig := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = orig })
	buildFixtureLedgerAt(t, home)

	session := connectMCPTestClient(t)
	res := callTool(t, session, "ubx_status", map[string]any{"ledger_dir": "~"})
	if res.IsError {
		t.Fatalf("a bare ~ was not treated as the home directory: %s", toolTextContent(t, res))
	}
}

// ~user is refused rather than guessed at. Resolving another user's
// home is not portably available here, and a wrong guess yields a
// real-looking absolute path, which is the exact failure mode this
// change exists to remove.
func TestMCP_LedgerDir_TildeUserIsRefused(t *testing.T) {
	session := connectMCPTestClient(t)
	res := callTool(t, session, "ubx_status", map[string]any{"ledger_dir": "~someoneelse/stacks/payments"})
	if !res.IsError {
		t.Fatal("expected ~user to be refused")
	}
	text := toolTextContent(t, res)
	if !strings.Contains(text, "~user") {
		t.Fatalf("expected the error to explain that ~user is what it cannot resolve, got: %s", text)
	}
	if strings.Contains(text, "not a ubx root") {
		t.Fatalf("~user must be refused as unresolvable, not silently expanded and then reported as a bad path: %s", text)
	}
}

func TestExpandTilde(t *testing.T) {
	home := t.TempDir()
	orig := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = orig })

	for _, tc := range []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "", want: ""},
		{in: ".", want: "."},
		{in: "/abs/path", want: "/abs/path"},
		{in: "relative/path", want: "relative/path"},
		{in: "~", want: home},
		{in: "~/", want: home},
		{in: "~/stacks/payments", want: filepath.Join(home, "stacks/payments")},
		// Only a LEADING tilde is special: "~" anywhere else is an
		// ordinary, legal character in a path.
		{in: "stacks/~backup", want: "stacks/~backup"},
		{in: "~someoneelse/stacks", wantErr: true},
		{in: "~root", wantErr: true},
	} {
		got, err := expandTilde(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("expandTilde(%q) = %q, want an error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("expandTilde(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("expandTilde(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The blueprint tools take caller-supplied paths too, and a model
// writes "~" in them for the same reason. list_blueprints is the
// checkable one: it reports what it actually found.
func TestMCP_Blueprint_RootDirExpandsTilde(t *testing.T) {
	home := t.TempDir()
	orig := userHomeDir
	userHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userHomeDir = orig })

	bpDir := filepath.Join(home, "blueprints", "queue")
	if err := os.MkdirAll(bpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ubxfile, err := assembleUbxfileYAML("go", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := assembleResourcesJSON("payments", "one queue", `[]`)
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"Ubxfile": ubxfile, "resources.json": resources} {
		if err := os.WriteFile(filepath.Join(bpDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	session := connectMCPTestClient(t)
	res := callTool(t, session, "list_blueprints", map[string]any{"root_dir": "~/blueprints"})
	if res.IsError {
		t.Fatalf("list_blueprints did not expand a tilde root_dir: %s", toolTextContent(t, res))
	}
	found, _ := res.StructuredContent.(map[string]any)["blueprints"].([]any)
	if len(found) != 1 {
		t.Fatalf("expected the one blueprint under the expanded path, got: %v", res.StructuredContent)
	}
}
