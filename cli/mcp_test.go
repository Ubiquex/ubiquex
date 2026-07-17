package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectMCPTestClient wires an in-memory (never a real stdio
// subprocess) client to a fresh ubx MCP server -- the same three tools
// `ubx mcp` registers, connected the way mcp.NewInMemoryTransports's own
// doc comment prescribes: server connects first, then client.
func connectMCPTestClient(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	server := newMCPServer()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	t.Cleanup(func() { serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "mcp-test", Version: "v0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { clientSession.Close() })
	return clientSession
}

// callTool is a small test convenience wrapping session.CallTool.
func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return res
}

func toolTextContent(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatalf("expected non-empty Content, got none")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected *mcp.TextContent, got %T", res.Content[0])
	}
	return tc.Text
}

// buildFixtureLedger adopts a resource into a fresh ledger directory via
// the real `ubx scan`/`ubx accept` CLI path (reusing TestMain's own
// fakeProviderBinary), so MCP tool tests exercise the exact same ledger
// shape a real onboarded resource would produce.
func buildFixtureLedger(t *testing.T) (ledgerDir string, addr, proposalID string) {
	t.Helper()
	ledgerDir = t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	adoptPath := filepath.Join(ledgerDir, "adopt.json")

	if _, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments",
		"--type", "fake_widget",
		"--name", "mcp-fixture",
		"--lookup", `{"name":"mcp-fixture","tags":{"env":"prod"}}`,
		"--ledger-dir", ledgerDir,
		"--out", adoptPath,
	); exitCode(err) != 1 {
		t.Fatalf("fixture scan (adopt): %v", err)
	}
	acceptOut, err := runUbx(t, env, "accept", adoptPath, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("fixture accept: %v", err)
	}
	return ledgerDir, "payments.fake_widget.mcp-fixture", mustExtractID(t, acceptOut)
}

func TestMCP_ListTools(t *testing.T) {
	session := connectMCPTestClient(t)
	res, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	want := map[string]bool{"ubx_why": true, "ubx_status": true, "ubx_scan": true}
	if len(res.Tools) != len(want) {
		t.Fatalf("got %d tools, want %d", len(res.Tools), len(want))
	}
	for _, tool := range res.Tools {
		if !want[tool.Name] {
			t.Errorf("unexpected tool %q", tool.Name)
		}
		if tool.Description == "" {
			t.Errorf("tool %q has no description", tool.Name)
		}
	}
	// Boundary by omission (docs/architecture.md -- MCP server): confirm
	// none of the mutating verbs are exposed, not just that the three
	// expected tools are present.
	for _, forbidden := range []string{"ubx_accept", "ubx_ship", "ubx_writeback", "ubx_revert_plan", "ubx_revert-plan"} {
		if want[forbidden] {
			t.Errorf("test setup bug: %q listed as wanted", forbidden)
		}
		for _, tool := range res.Tools {
			if tool.Name == forbidden {
				t.Errorf("forbidden mutating tool %q is exposed", forbidden)
			}
		}
	}
}

func TestMCP_Why_ResourceAddress(t *testing.T) {
	ledgerDir, addr, _ := buildFixtureLedger(t)
	session := connectMCPTestClient(t)

	res := callTool(t, session, "ubx_why", map[string]any{"query": addr, "ledger_dir": ledgerDir})
	if res.IsError {
		t.Fatalf("expected success, got error: %s", toolTextContent(t, res))
	}
	payload, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("expected a JSON object, got %T", res.StructuredContent)
	}
	chain, ok := payload["chain"].([]any)
	if !ok || len(chain) != 1 {
		t.Fatalf("expected a 1-entry chain, got: %v", payload)
	}
}

func TestMCP_Why_ProposalID(t *testing.T) {
	ledgerDir, _, proposalID := buildFixtureLedger(t)
	session := connectMCPTestClient(t)

	res := callTool(t, session, "ubx_why", map[string]any{"query": proposalID, "ledger_dir": ledgerDir})
	if res.IsError {
		t.Fatalf("expected success, got error: %s", toolTextContent(t, res))
	}
	payload, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("expected a JSON object, got %T", res.StructuredContent)
	}
	proposal, ok := payload["proposal"].(map[string]any)
	if !ok || proposal["id"] != proposalID {
		t.Fatalf("expected proposal.id = %q, got: %v", proposalID, payload)
	}
}

// TestMCP_Why_UnknownAddress is the adversarial "unknown address" case
// (UBI-25's own test list): ubx_why must surface this as an MCP tool
// error, not a silent empty/zero result.
func TestMCP_Why_UnknownAddress(t *testing.T) {
	ledgerDir, _, _ := buildFixtureLedger(t)
	session := connectMCPTestClient(t)

	res := callTool(t, session, "ubx_why", map[string]any{"query": "payments.fake_widget.does-not-exist", "ledger_dir": ledgerDir})
	if !res.IsError {
		t.Fatal("expected an error result for an unknown address")
	}
	if text := toolTextContent(t, res); text == "" {
		t.Fatal("expected a non-empty error message")
	}
}

// TestMCP_Why_NoLedger is the "no ledger" adversarial case: a ledger_dir
// that doesn't exist (or was never initialized) must surface as a tool
// error, not a panic or a silently-empty payload.
func TestMCP_Why_NoLedger(t *testing.T) {
	session := connectMCPTestClient(t)
	res := callTool(t, session, "ubx_why", map[string]any{
		"query":      "payments.fake_widget.anything",
		"ledger_dir": filepath.Join(t.TempDir(), "no-such-ledger"),
	})
	if !res.IsError {
		t.Fatal("expected an error result when the ledger doesn't exist")
	}
}

func TestMCP_Status_LedgerOnly(t *testing.T) {
	ledgerDir, _, _ := buildFixtureLedger(t)
	session := connectMCPTestClient(t)

	res := callTool(t, session, "ubx_status", map[string]any{"ledger_dir": ledgerDir})
	if res.IsError {
		t.Fatalf("expected success, got error: %s", toolTextContent(t, res))
	}
	payload, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("expected a JSON object, got %T", res.StructuredContent)
	}
	if payload["drift_checked"] != false {
		t.Errorf("expected drift_checked=false for a ledger-only status call, got: %v", payload["drift_checked"])
	}
	resources, ok := payload["resources"].([]any)
	if !ok || len(resources) != 1 {
		t.Fatalf("expected exactly one resource, got: %v", payload["resources"])
	}
}

// TestMCP_Status_DriftWithoutProvider is the "drift without provider"
// adversarial case: drift:true with no provider identity at all must
// surface ubx's own teaching-error message as a tool error, never
// silently fall back to ledger-only behavior.
func TestMCP_Status_DriftWithoutProvider(t *testing.T) {
	ledgerDir, _, _ := buildFixtureLedger(t)
	session := connectMCPTestClient(t)

	res := callTool(t, session, "ubx_status", map[string]any{"ledger_dir": ledgerDir, "drift": true})
	if !res.IsError {
		t.Fatal("expected an error result when drift is requested with no provider identity")
	}
	if text := toolTextContent(t, res); text == "" {
		t.Fatal("expected a non-empty error message naming the missing provider identity")
	}
}

func TestMCP_Status_Drift_Clean(t *testing.T) {
	ledgerDir, _, _ := buildFixtureLedger(t)
	session := connectMCPTestClient(t)

	res := callTool(t, session, "ubx_status", map[string]any{
		"ledger_dir":    ledgerDir,
		"drift":         true,
		"provider_path": fakeProviderBinary,
	})
	if res.IsError {
		t.Fatalf("expected success, got error: %s", toolTextContent(t, res))
	}
	payload := res.StructuredContent.(map[string]any)
	if payload["drift_checked"] != true {
		t.Fatalf("expected drift_checked=true, got: %v", payload)
	}
	summary := payload["summary"].(map[string]any)
	if summary["drifted"] != float64(0) {
		t.Fatalf("expected 0 drifted (fixture wasn't mutated), got: %v", summary)
	}
}

func TestMCP_Scan_New(t *testing.T) {
	t.Setenv("FAKEPROVIDER_MODE", "ok-v6")
	ledgerDir := t.TempDir()
	session := connectMCPTestClient(t)

	res := callTool(t, session, "ubx_scan", map[string]any{
		"stack":         "payments",
		"type":          "fake_widget",
		"name":          "scan-tool-fixture",
		"lookup":        `{"name":"scan-tool-fixture","tags":{"env":"prod"}}`,
		"provider_path": fakeProviderBinary,
		"ledger_dir":    ledgerDir,
	})
	if res.IsError {
		t.Fatalf("expected success, got error: %s", toolTextContent(t, res))
	}
	payload := res.StructuredContent.(map[string]any)
	if payload["outcome"] != "new" {
		t.Fatalf("expected outcome=new, got: %v", payload)
	}
	proposals, ok := payload["proposals"].([]any)
	if !ok || len(proposals) != 1 {
		t.Fatalf("expected exactly one generated proposal, got: %v", payload["proposals"])
	}
	// ubx_scan must never accept anything -- confirm nothing was
	// appended to the ledger by re-checking via ubx_status.
	statusRes := callTool(t, session, "ubx_status", map[string]any{"ledger_dir": ledgerDir})
	statusPayload := statusRes.StructuredContent.(map[string]any)
	if resources, _ := statusPayload["resources"].([]any); len(resources) != 0 {
		t.Fatalf("ubx_scan must never accept a proposal, but the ledger now has: %v", resources)
	}
}

func TestMCP_Scan_MissingRequiredFields(t *testing.T) {
	session := connectMCPTestClient(t)
	res := callTool(t, session, "ubx_scan", map[string]any{"stack": "payments"})
	if !res.IsError {
		t.Fatal("expected an error result when type/name are missing")
	}
}

// TestMCP_ToolDescriptions_MentionWhatTheyAnswer is a light content
// check on the "tool descriptions are the model's UX" design goal
// (docs/architecture.md -- MCP server) -- not exhaustive, just confirms
// each description actually explains the tool's purpose in plain
// language rather than being a bare mechanical restatement of its name.
func TestMCP_ToolDescriptions_MentionWhatTheyAnswer(t *testing.T) {
	session := connectMCPTestClient(t)
	res, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	minLen := 80
	for _, tool := range res.Tools {
		if len(tool.Description) < minLen {
			t.Errorf("tool %q description is only %d chars, expected a substantive explanation (>= %d)", tool.Name, len(tool.Description), minLen)
		}
	}
}

func TestMCP_OutputIsValidJSON(t *testing.T) {
	ledgerDir, addr, _ := buildFixtureLedger(t)
	session := connectMCPTestClient(t)
	res := callTool(t, session, "ubx_why", map[string]any{"query": addr, "ledger_dir": ledgerDir})
	var decoded map[string]any
	if err := json.Unmarshal([]byte(toolTextContent(t, res)), &decoded); err != nil {
		t.Fatalf("tool text content is not valid JSON: %v", err)
	}
	if decoded["format"] != float64(1) {
		t.Fatalf("expected format=1 (UBI-20's contract), got: %v", decoded["format"])
	}
}
