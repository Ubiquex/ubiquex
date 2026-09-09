package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ledger_dir supplies the config, not just the ledger.
//
// Every tool taking ledger_dir describes it as the stack's root
// directory, "the directory holding its .ubx/". The .ubx/ half was not
// honoured: each handler called LoadConfig, which starts at
// os.Getwd() and walks upward (configcascade.go), so the ledger opened
// at ledger_dir while the config came from wherever the server process
// happened to be started. A call naming one stack ran under another
// stack's provider identity, ledger store and stack default, with no
// signal of any kind that it had.
//
// Worse than either of the two defects fixed alongside it: a wrong path
// at least produced a wrong-looking answer once it was refused, whereas
// this produced a plausible answer computed against the wrong config.
// For a Claude Desktop server the cwd is whatever the launcher chose,
// which the model cannot see at all.

// remoteStoreRoot writes a stack root whose .ubx/config names a remote
// ledger store, and returns it.
//
// Remote is the observable: openLedgerForStack refuses a bare
// remote-store open without a stack (cli/ledgeropen.go), so which
// config a call actually resolved is visible in whether that one
// specific refusal appears.
//
// The store name comes from remoteStoreFixture, which redirects the
// openRemoteLedgerStore seam at an in-memory bucket. A literal
// "s3://..." here would be a real bucket address: an earlier draft of
// this file used one and a test run made a genuine AWS GetObject with
// the machine's own credentials. Every remote open in these tests must
// be unable to leave the process even when the code under test is
// broken, since a test for "did it open the WRONG ledger" is exactly
// the one that opens it.
func remoteStoreRoot(t *testing.T) string {
	t.Helper()
	storeName, _ := remoteStoreFixture(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".ubx"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "ledger = { store = \"" + storeName + "\" }\n"
	if err := os.WriteFile(filepath.Join(dir, ".ubx", "config.hcl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const remoteStoreRefusal = "--stack is required to open a remote ledger store"

// The core claim: with the server's own cwd configured for a remote
// store and ledger_dir naming a plain git ledger root, the call reads
// the git ledger it was given.
func TestMCP_ConfigComesFromLedgerDirNotTheServersCwd(t *testing.T) {
	// The fixture ledger is built first, on purpose: buildFixtureLedger
	// runs real ubx commands that read the cascade themselves, so seeding
	// it after the cwd was pointed at a remote store would have the
	// fixture write into that store rather than into its own directory.
	stackRoot, addr, _ := buildFixtureLedger(t)

	cwdRoot := remoteStoreRoot(t)
	orig := configSearchStartDir
	configSearchStartDir = func() (string, error) { return cwdRoot, nil }
	t.Cleanup(func() { configSearchStartDir = orig })

	session := connectMCPTestClient(t)
	res := callTool(t, session, "ubx_status", map[string]any{"ledger_dir": stackRoot})
	if res.IsError {
		text := toolTextContent(t, res)
		if strings.Contains(text, remoteStoreRefusal) {
			t.Fatalf("the call used the server cwd's own remote-store config instead of the config at the ledger_dir it was given: %s", text)
		}
		t.Fatalf("unexpected error: %s", text)
	}
	resources, _ := res.StructuredContent.(map[string]any)["resources"].([]any)
	if len(resources) != 1 {
		t.Fatalf("expected the one resource in the named stack's own ledger, got: %v", res.StructuredContent)
	}

	whyRes := callTool(t, session, "ubx_why", map[string]any{"query": addr, "ledger_dir": stackRoot})
	if whyRes.IsError {
		t.Fatalf("ubx_why did not read the config at its own ledger_dir: %s", toolTextContent(t, whyRes))
	}
}

// With ledger_dir omitted, nothing changes: the stack root is the
// server's own working directory and its config is what the call runs
// under, exactly as before. This is the "does the fix break anyone
// relying on the current cascade" guard, and the default is the path
// every existing deployment already uses.
//
// Exercised through configSearchStartDir rather than by changing the
// process directory, because that var IS where the default comes from
// (mcpRootDir) and in production it is os.Getwd. Routing the default
// through the seam instead of a raw os.Getwd is deliberate: it keeps
// one notion of "where this process looks by default", and keeps the
// MCP handlers inside the hermeticity pin TestMain sets on that var
// (cli/scan_test.go), which a raw os.Getwd would step outside of.
func TestMCP_ConfigDefaultsToTheServersOwnCwd(t *testing.T) {
	cwdRoot := remoteStoreRoot(t)
	orig := configSearchStartDir
	configSearchStartDir = func() (string, error) { return cwdRoot, nil }
	t.Cleanup(func() { configSearchStartDir = orig })

	session := connectMCPTestClient(t)
	res := callTool(t, session, "ubx_status", map[string]any{})
	if !res.IsError {
		t.Fatalf("expected the cwd's own remote-store config to be honoured with no ledger_dir given, got: %s", toolTextContent(t, res))
	}
	if text := toolTextContent(t, res); !strings.Contains(text, remoteStoreRefusal) {
		t.Fatalf("the default did not resolve the server's own cwd config, got: %s", text)
	}
}

// The cascade still walks UPWARD from ledger_dir, so a config in a
// parent directory is found exactly as it is from cwd. This is what
// makes the fix a redirection of where the walk starts rather than a
// narrowing of what it can see -- and it is the direct guard on
// resolveLedgerDir returning an absolute path, since filepath.Dir(".")
// is "." and a relative start would stop at the first directory.
func TestMCP_ConfigCascadesUpwardFromLedgerDir(t *testing.T) {
	parent := remoteStoreRoot(t)
	child := filepath.Join(parent, "stacks", "payments")
	if err := os.MkdirAll(filepath.Join(child, ".ubx"), 0o755); err != nil {
		t.Fatal(err)
	}

	session := connectMCPTestClient(t)
	res := callTool(t, session, "ubx_status", map[string]any{"ledger_dir": child})
	if !res.IsError {
		t.Fatalf("expected the parent directory's config to apply, got: %s", toolTextContent(t, res))
	}
	if text := toolTextContent(t, res); !strings.Contains(text, remoteStoreRefusal) {
		t.Fatalf("the cascade did not walk upward from ledger_dir, so a parent's config was missed: %s", text)
	}
}

// A ledger_dir whose own config differs from a parent's still wins:
// nearest-directory-wins is the cascade's existing rule and this change
// does not alter it, it only changes where "nearest" is measured from.
func TestMCP_ConfigAtLedgerDirBeatsItsParent(t *testing.T) {
	parent := remoteStoreRoot(t)
	child := filepath.Join(parent, "stacks", "payments")
	if err := os.MkdirAll(filepath.Join(child, ".ubx"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, ".ubx", "config.hcl"), []byte("ledger = { store = \"git\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	session := connectMCPTestClient(t)
	res := callTool(t, session, "ubx_status", map[string]any{"ledger_dir": child})
	if res.IsError {
		t.Fatalf("the nearer config declaring a git store was not honoured: %s", toolTextContent(t, res))
	}
	summary := res.StructuredContent.(map[string]any)["summary"].(map[string]any)
	if summary["total"] != float64(0) {
		t.Fatalf("expected an empty git ledger at the child root, got: %v", summary)
	}
}
