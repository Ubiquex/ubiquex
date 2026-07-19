package cli

import (
	"strings"
	"testing"
)

// UBI-32: the MCP surface (`ubx_why`/`ubx_status`/`ubx_scan`) now reads
// `.ubx/config`'s own `[ledger]` table exactly like the CLI's own --json
// paths do -- a real, if narrow, divergence this session found:
// computeWhyJSON/computeStatusJSON/computeScanJSON had quietly stopped
// being literally shared code with cli/why.go/status.go/scan.go's own
// RunE the moment those grew their own openLedgerForStack call
// (UBI-32, an earlier session), leaving these three MCP-only functions
// as the last unwired git-local-only readers.

func TestMCP_Why_RemoteStore_ProposalID_RequiresStack(t *testing.T) {
	storeName, _ := remoteStoreFixture(t)
	remoteConfigDir(t, storeName)
	session := connectMCPTestClient(t)

	res := callTool(t, session, "ubx_why", map[string]any{
		"query":      "3ff1769e2c4d2cadc8e492cce9ae5b465b9263b3f39a5aae946617ae8fc028c0",
		"ledger_dir": ".",
	})
	if !res.IsError {
		t.Fatal("expected an error -- a bare proposal-id query against a remote store needs a stack")
	}
	if text := toolTextContent(t, res); !strings.Contains(text, "--stack") {
		t.Fatalf("expected the error to name --stack, got: %s", text)
	}
}

func TestMCP_Why_RemoteStore_ProposalID_WithStack_ReadsFromStore(t *testing.T) {
	storeName, bucket := remoteStoreFixture(t)
	remoteConfigDir(t, storeName)

	// Accept a real proposal directly against the shared bucket first --
	// ubx_why's own job is reading it back, not creating it.
	repoDir, sha := gitRepoWithProposal(t, "proposal.json", draftProposalJSON)
	apiBase := fakeGitHubServer(t, "ubx-proposal: 3ac4ad60be63780420495d6f6dc16e06c206ad5960fa0a664facce27e73ec6ae", []map[string]any{})
	t.Setenv("UBX_GITHUB_API_BASE_URL", apiBase)
	if _, err := runUbx(t, nil, "accept",
		"--from-merge", sha,
		"--repo-dir", repoDir,
		"--proposal-file", "proposal.json",
		"--github-repo", "acme/infra",
		"--ledger-dir", ".",
	); err != nil {
		t.Fatalf("seed accept: %v", err)
	}
	_ = bucket

	session := connectMCPTestClient(t)
	res := callTool(t, session, "ubx_why", map[string]any{
		"query":      "3ac4ad60be63780420495d6f6dc16e06c206ad5960fa0a664facce27e73ec6ae",
		"stack":      "payments",
		"ledger_dir": ".",
	})
	if res.IsError {
		t.Fatalf("expected success, got error: %s", toolTextContent(t, res))
	}
	payload, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("expected a JSON object, got %T", res.StructuredContent)
	}
	proposal, ok := payload["proposal"].(map[string]any)
	if !ok || proposal["id"] != "3ac4ad60be63780420495d6f6dc16e06c206ad5960fa0a664facce27e73ec6ae" {
		t.Fatalf("expected the proposal read back from the remote store, got: %v", payload)
	}
}

func TestMCP_Status_RemoteStore_RequiresStack(t *testing.T) {
	storeName, _ := remoteStoreFixture(t)
	remoteConfigDir(t, storeName)
	session := connectMCPTestClient(t)

	res := callTool(t, session, "ubx_status", map[string]any{"ledger_dir": "."})
	if !res.IsError {
		t.Fatal("expected an error -- a remote store has no single fleet to enumerate without a stack")
	}
	if text := toolTextContent(t, res); !strings.Contains(text, "--stack") {
		t.Fatalf("expected the error to name --stack, got: %s", text)
	}
}

func TestMCP_Scan_RemoteStore_WritesToConfiguredStore(t *testing.T) {
	t.Setenv("FAKEPROVIDER_MODE", "ok-v6")
	storeName, bucket := remoteStoreFixture(t)
	remoteConfigDir(t, storeName)
	session := connectMCPTestClient(t)

	res := callTool(t, session, "ubx_scan", map[string]any{
		"stack":         "payments",
		"type":          "fake_widget",
		"name":          "mcp-remote-fixture",
		"lookup":        `{"name":"mcp-remote-fixture","tags":{"env":"prod"}}`,
		"provider_path": fakeProviderBinary,
		"ledger_dir":    ".",
	})
	if res.IsError {
		t.Fatalf("expected success, got error: %s", toolTextContent(t, res))
	}
	payload := res.StructuredContent.(map[string]any)
	if payload["outcome"] != "new" {
		t.Fatalf("expected outcome=new, got: %v", payload)
	}
	// ubx_scan itself never writes to the ledger (it's read-only), but it
	// does read the salt from the configured store -- confirming this
	// reached the shared bucket at all, not git-local, proves the fix.
	if bucketIsEmpty(t, bucket) {
		t.Fatal("expected ubx_scan to have touched the configured remote store (its own salt object), not git-local")
	}
}
