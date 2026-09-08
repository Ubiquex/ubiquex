package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The six commands that rejected a short hash, all resolving it now.
//
// Every receipt and every `ubx history` line prints a 12-character
// truncated hash, and six commands then refused it. Three said so
// (`why`, `alias set`, `restore`); three reported "proposal not found in
// ledger" (`promote`, `revert-plan`, `writeback`), which is worse
// because the proposal does exist. `ship` alone accepted a prefix, so
// this was an inconsistency inside the tool rather than a rule.
//
// The founder found three by accident. The other three came from
// actually enumerating every command whose Use line takes a hash.
func TestShortHash_EverySurfaceResolvesAPrefix(t *testing.T) {
	ledgerDir := t.TempDir()
	env := []string{"FAKEPROVIDER_MODE=ok-v6"}
	adoptViaCLI(t, ledgerDir, "payments", "fake_widget", "w1", `{"name":"w1"}`, env)

	full := headOf(t, ledgerDir)
	short := full[:12]

	for _, verb := range []string{"why", "revert-plan", "writeback"} {
		t.Run(verb, func(t *testing.T) {
			_, err := runUbx(t, env, verb, short, "--ledger-dir", ledgerDir)
			// The command may still refuse for its own reasons (a
			// revert-plan wants a drift_revert), but it must never claim
			// the proposal does not exist, and must never call a real
			// hash invalid.
			if err != nil {
				msg := err.Error()
				for _, bad := range []string{"not found in ledger", "is not a proposal hash", "no alias"} {
					if strings.Contains(msg, bad) {
						t.Fatalf("%s rejected a real short hash: %v", verb, err)
					}
				}
			}
		})
	}

	t.Run("alias set", func(t *testing.T) {
		out, err := runUbx(t, env, "alias", "set", "v1", short, "--ledger-dir", ledgerDir, "--stack", "payments")
		if err != nil {
			t.Fatalf("alias set with a short hash: %v\noutput: %s", err, out)
		}
	})
}

// Ambiguity refuses and names the candidates, rather than taking the
// first match. Synthetic IDs, because two real proposals sharing four
// hex characters cannot be arranged on demand.
func TestShortHash_AmbiguousPrefixNamesCandidates(t *testing.T) {
	dir := t.TempDir()
	writeTwoProposalsSharingAPrefix(t, dir)

	_, err := resolveProposalPrefix(dir, "abcd")
	if err == nil {
		t.Fatal("expected an ambiguous prefix to refuse")
	}
	if !errors.Is(err, ErrRefAmbiguous) {
		t.Fatalf("expected ErrRefAmbiguous, got: %v", err)
	}
	if !strings.Contains(err.Error(), "matches 2 proposals") {
		t.Errorf("expected the count named, got: %v", err)
	}
	// Both candidates must appear, or the reader cannot act on it.
	for _, want := range []string{"abcd0", "abcd1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected candidate %s named, got: %v", want, err)
		}
	}
}

// A prefix long enough to be unique resolves, in the same ledger where a
// shorter one is ambiguous.
func TestShortHash_LongerPrefixDisambiguates(t *testing.T) {
	dir := t.TempDir()
	writeTwoProposalsSharingAPrefix(t, dir)

	got, err := resolveProposalPrefix(dir, "abcd0")
	if err != nil {
		t.Fatalf("a unique prefix should resolve: %v", err)
	}
	if !strings.HasPrefix(got, "abcd0") {
		t.Errorf("resolved to %s, which does not carry the prefix", got)
	}
}

// Below the floor is its own error, with its own advice. Reporting
// "no such proposal" for a two-character prefix would send the reader
// looking for a missing proposal when the fix is to type more.
func TestShortHash_BelowFloorSaysSoRatherThanNotFound(t *testing.T) {
	dir := t.TempDir()
	writeTwoProposalsSharingAPrefix(t, dir)

	_, err := resolveProposalPrefix(dir, "ab")
	if !errors.Is(err, ErrRefTooShort) {
		t.Fatalf("expected ErrRefTooShort, got: %v", err)
	}
	if errors.Is(err, ErrRefNotFound) {
		t.Error("a short prefix must not be reported as a missing proposal")
	}
	if !strings.Contains(err.Error(), "give at least 4") {
		t.Errorf("expected the floor named, got: %v", err)
	}
}

// A name carrying non-hex characters is an alias, not a reference, and
// the two must not be confused. `alias set` used to report a real hash
// as a missing alias name, which is the same confusion in reverse.
func TestIsHexRef(t *testing.T) {
	for _, ok := range []string{"abcd", "0123456789abcdef", "ABCD"} {
		if !isHexRef(ok) {
			t.Errorf("%q should read as a hex reference", ok)
		}
	}
	for _, no := range []string{"", "v1", "prod-head", "abcz", "abc.def"} {
		if isHexRef(no) {
			t.Errorf("%q should read as a name, not a hex reference", no)
		}
	}
}

func writeTwoProposalsSharingAPrefix(t *testing.T, dir string) {
	t.Helper()
	// Two proposals whose IDs share "abcd" but diverge at the fifth
	// character, chained so Chain() walks both.
	a := "abcd0" + strings.Repeat("0", 59)
	b := "abcd1" + strings.Repeat("0", 59)
	props := filepath.Join(dir, "ledger", "proposals")
	if err := os.MkdirAll(props, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(props, a+".prop.json"),
		`{"schema_version":2,"id":"`+a+`","stack":"payments","kind":"adoption","parent":"","intent":{"summary":"first"},"status":"accepted"}`)
	writeFile(t, filepath.Join(props, b+".prop.json"),
		`{"schema_version":2,"id":"`+b+`","stack":"payments","kind":"adoption","parent":"`+a+`","intent":{"summary":"second"},"status":"accepted"}`)
	if err := os.MkdirAll(filepath.Join(dir, ".ubx"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".ubx", "ledger.lock"), b)
}

func headOf(t *testing.T, ledgerDir string) string {
	t.Helper()
	out, err := runUbx(t, nil, "history", "--ledger-dir", ledgerDir, "--full-hashes", "--json")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	i := strings.Index(out, `"id": "`)
	if i < 0 {
		t.Fatalf("no proposal id in history output: %s", out)
	}
	rest := out[i+len(`"id": "`):]
	return rest[:strings.Index(rest, `"`)]
}
