package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectMCPTestClientServing is connectMCPTestClient for a server started
// with --serve over roots.
func connectMCPTestClientServing(t *testing.T, roots ...string) *mcp.ClientSession {
	t.Helper()
	served, err := loadServedStacks(roots, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("loadServedStacks: %v", err)
	}
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := newMCPServer(served).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	t.Cleanup(func() { serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "mcp-test", Version: "v0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

// servedFixtureStack is a real ledger holding one adopted fake_widget,
// whose .ubx/config names its stack and points at the fake provider.
func servedFixtureStack(t *testing.T) (root, addr string) {
	t.Helper()
	root, addr, _ = buildFixtureLedger(t)
	writeConfig(t, root, `stack = "payments"

[provider]
path = "`+fakeProviderBinary+`"
`)
	t.Setenv("FAKEPROVIDER_MODE", "ok-v6")
	return root, addr
}

func callToolJSON(t *testing.T, session *mcp.ClientSession, name string, args map[string]any, into any) {
	t.Helper()
	res := callTool(t, session, name, args)
	text := toolTextContent(t, res)
	if res.IsError {
		t.Fatalf("%s returned an error: %s", name, text)
	}
	if err := json.Unmarshal([]byte(text), into); err != nil {
		t.Fatalf("%s: unmarshal: %v\n%s", name, err, text)
	}
}

func callToolError(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res := callTool(t, session, name, args)
	text := toolTextContent(t, res)
	if !res.IsError {
		t.Fatalf("%s: expected an error, got: %s", name, text)
	}
	return text
}

func TestMCPServe_Stacks(t *testing.T) {
	root, addr := servedFixtureStack(t)
	session := connectMCPTestClientServing(t, root)

	var out stacksJSON
	callToolJSON(t, session, "stacks", map[string]any{}, &out)
	if len(out.Stacks) != 1 {
		t.Fatalf("want 1 served stack, got %+v", out.Stacks)
	}
	s := out.Stacks[0]
	if s.Name != "payments" || s.Root != root || len(s.Head) != 64 {
		t.Fatalf("unexpected stack entry: %+v", s)
	}
	if len(s.Resources) != 1 || s.Resources[0] != addr {
		t.Fatalf("resources = %v, want [%s]", s.Resources, addr)
	}
}

func TestMCPServe_NothingServed(t *testing.T) {
	session := connectMCPTestClient(t)
	var out stacksJSON
	callToolJSON(t, session, "stacks", map[string]any{}, &out)
	if len(out.Stacks) != 0 {
		t.Fatalf("want no stacks, got %+v", out.Stacks)
	}
	msg := callToolError(t, session, "state", map[string]any{"stack": "payments"})
	if !strings.Contains(msg, "--serve") {
		t.Fatalf("state on an unserved server should say how to serve a stack, got: %s", msg)
	}
}

// TestMCPServe_OnlyServedStacksAreVisible: the read tools address stacks by
// name only, and a name that was not served is refused with the served
// names, never resolved as a path.
func TestMCPServe_OnlyServedStacksAreVisible(t *testing.T) {
	root, _ := servedFixtureStack(t)
	other, _, _ := buildFixtureLedger(t)
	writeConfig(t, other, "stack = \"billing\"\n")
	session := connectMCPTestClientServing(t, root)

	for _, args := range []map[string]any{
		{"stack": "billing"},
		{"stack": other},
		{"stack": "../billing"},
	} {
		msg := callToolError(t, session, "state", args)
		if !strings.Contains(msg, "not served here") || !strings.Contains(msg, "payments") {
			t.Fatalf("state %v: want a not-served refusal naming payments, got: %s", args, msg)
		}
	}
}

func TestMCPServe_State(t *testing.T) {
	root, addr := servedFixtureStack(t)
	session := connectMCPTestClientServing(t, root)

	var list stateJSON
	callToolJSON(t, session, "state", map[string]any{"stack": "payments"}, &list)
	if list.Format != jsonFormatVersion || len(list.Resources) != 1 || list.State != nil {
		t.Fatalf("unexpected state listing: %+v", list)
	}

	var one stateJSON
	callToolJSON(t, session, "state", map[string]any{"stack": "payments", "address": addr}, &one)
	if one.Address != addr {
		t.Fatalf("address = %q, want %q", one.Address, addr)
	}
	var folded map[string]any
	if err := json.Unmarshal(one.State, &folded); err != nil {
		t.Fatalf("state is not a JSON object: %v (%s)", err, one.State)
	}
	if folded["name"] != "mcp-fixture" {
		t.Fatalf("folded state does not carry the adopted name: %s", one.State)
	}

	msg := callToolError(t, session, "state", map[string]any{"stack": "payments", "address": "payments.fake_widget.nope"})
	if !strings.Contains(msg, "no live resource") {
		t.Fatalf("unknown address: got %s", msg)
	}
	msg = callToolError(t, session, "state", map[string]any{"stack": "payments", "address": "billing.fake_widget.x"})
	if !strings.Contains(msg, "belongs to stack") {
		t.Fatalf("other stack's address: got %s", msg)
	}
}

func TestMCPServe_Schema(t *testing.T) {
	root, _ := servedFixtureStack(t)
	session := connectMCPTestClientServing(t, root)

	var list schemaListJSON
	callToolJSON(t, session, "schema", map[string]any{"stack": "payments"}, &list)
	if len(list.Providers) != 1 {
		t.Fatalf("want 1 provider, got %+v", list.Providers)
	}
	found := false
	for _, ty := range list.Providers[0].ResourceTypes {
		if ty == "fake_widget" {
			found = true
		}
	}
	if !found {
		t.Fatalf("fake_widget not among resource types: %v", list.Providers[0].ResourceTypes)
	}

	var one schemaTypeJSON
	callToolJSON(t, session, "schema", map[string]any{"stack": "payments", "type": "fake_widget"}, &one)
	if one.Type != "fake_widget" || len(one.Block.Attributes) == 0 {
		t.Fatalf("unexpected type schema: %+v", one)
	}
	names := map[string]bool{}
	for _, a := range one.Block.Attributes {
		names[a.Name] = true
	}
	if !names["name"] {
		t.Fatalf("fake_widget schema has no name attribute: %+v", one.Block.Attributes)
	}

	msg := callToolError(t, session, "schema", map[string]any{"stack": "payments", "type": "nope_widget"})
	if !strings.Contains(msg, "offers a resource type") && !strings.Contains(msg, "offers no resource type") {
		t.Fatalf("unknown type: got %s", msg)
	}
}

func TestMCPServe_RefusesBadRoots(t *testing.T) {
	noStack, _, _ := buildFixtureLedger(t)
	if _, err := loadServedStacks([]string{noStack}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "names no stack") {
		t.Fatalf("root without a stack name: got %v", err)
	}
	if _, err := loadServedStacks([]string{t.TempDir()}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "not a ubx root") {
		t.Fatalf("non-root: got %v", err)
	}
	a, _, _ := buildFixtureLedger(t)
	b, _, _ := buildFixtureLedger(t)
	writeConfig(t, a, "stack = \"payments\"\n")
	writeConfig(t, b, "stack = \"payments\"\n")
	if _, err := loadServedStacks([]string{a, b}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "named by both") {
		t.Fatalf("duplicate stack names: got %v", err)
	}
}
