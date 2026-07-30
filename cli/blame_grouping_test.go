package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestBlame_HidesZeroDefaultsBehindAll is UBI-61's own founder-test
// finding: "empty/null/zero-default attributes collapsed by default
// behind --all -- 24 lines of ""/null/false drowns the 3 lines that
// matter." A fake_widget adopted with no tags at all decodes tags as an
// empty map (provider.internal.fakeprovider's own decodeWidgetState) --
// a real, zero-value provider default alongside id/name's own real
// values in the identical group.
func TestBlame_HidesZeroDefaultsBehindAll(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	addr := "payments.fake_widget.widget-defaults"

	adoptPath := filepath.Join(ledgerDir, "adopt.json")
	scanOut, err := runUbx(t, env, "scan",
		"--provider", fakeProviderBinary,
		"--stack", "payments", "--type", "fake_widget", "--name", "widget-defaults",
		"--lookup", `{"name":"widget-defaults"}`,
		"--ledger-dir", ledgerDir,
		"--out", adoptPath,
	)
	requireExitCode(t, err, 1, scanOut)
	if _, err := runUbx(t, env, "accept", adoptPath, "--ledger-dir", ledgerDir); err != nil {
		t.Fatalf("ubx accept: %v", err)
	}

	out, err := runUbx(t, nil, "blame", addr, "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx blame: %v\noutput: %s", err, out)
	}
	if strings.Contains(out, "tags: {}") {
		t.Fatalf("expected the zero-default tags attribute hidden by default, got: %s", out)
	}
	if !strings.Contains(out, "provider default(s) hidden -- --all to show") {
		t.Fatalf("expected a hidden-defaults trailer, got: %s", out)
	}
	if !strings.Contains(out, `name: "widget-defaults"`) {
		t.Fatalf("expected the real name attribute still shown, got: %s", out)
	}

	allOut, err := runUbx(t, nil, "blame", addr, "--ledger-dir", ledgerDir, "--all")
	if err != nil {
		t.Fatalf("ubx blame --all: %v\noutput: %s", err, allOut)
	}
	if !strings.Contains(allOut, "tags: {}") {
		t.Fatalf("expected --all to reveal the zero-default tags attribute, got: %s", allOut)
	}
	if strings.Contains(allOut, "hidden -- --all to show") {
		t.Fatalf("--all must not still report anything hidden, got: %s", allOut)
	}
}
