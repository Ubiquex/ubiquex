package cli

import (
	"bytes"
	"os"
	"testing"
)

// withTerminal forces isTerminal to report v for the duration of the
// calling test -- the same package-var-swap pattern configSearchStartDir
// already established, needed because Go's testing framework can't fake
// a real terminal (or, per TestIsTerminal_RealCharDeviceIsNotEnough,
// even a real character device like /dev/null) any other way.
func withTerminal(t *testing.T, v bool) {
	t.Helper()
	orig := isTerminal
	isTerminal = func(any) bool { return v }
	t.Cleanup(func() { isTerminal = orig })
}

func TestColorEnabled_Matrix(t *testing.T) {
	root := NewRootCmd()
	root.SetOut(&bytes.Buffer{})

	t.Run("non-TTY, no NO_COLOR", func(t *testing.T) {
		withTerminal(t, false)
		if colorEnabled(root) {
			t.Fatal("expected color disabled for a non-terminal output stream")
		}
	})
	t.Run("TTY, no NO_COLOR", func(t *testing.T) {
		withTerminal(t, true)
		t.Setenv("NO_COLOR", "")
		if !colorEnabled(root) {
			t.Fatal("expected color enabled for a real terminal with NO_COLOR unset")
		}
	})
	t.Run("TTY, NO_COLOR set", func(t *testing.T) {
		withTerminal(t, true)
		t.Setenv("NO_COLOR", "1")
		if colorEnabled(root) {
			t.Fatal("expected color disabled whenever NO_COLOR is set, even on a real terminal")
		}
	})
}

func TestIsTerminal_RealCharDeviceIsNotEnough(t *testing.T) {
	// /dev/null is itself a character device on this platform (confirmed
	// via `stat`) -- an os.ModeCharDevice-only heuristic would wrongly
	// call it a terminal. This is why isTerminal uses golang.org/x/term's
	// real ioctl-based check instead.
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Skipf("no %s on this platform: %v", os.DevNull, err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Fatal("/dev/null must never be detected as an interactive terminal")
	}
}

func TestStyler_DisabledIsPassthrough(t *testing.T) {
	st := &styler{enabled: false}
	for _, got := range []string{st.Green("x"), st.Yellow("x"), st.Red("x"), st.Blue("x"), st.Purple("x"), st.Dim("x"), st.Bold("x")} {
		if got != "x" {
			t.Fatalf("disabled styler must return text unchanged, got %q", got)
		}
	}
}

func TestStyler_EnabledWrapsWithAnsiAndReset(t *testing.T) {
	st := &styler{enabled: true}
	got := st.Green("x")
	if got == "x" || got[len(got)-len(ansiReset):] != ansiReset {
		t.Fatalf("enabled styler should wrap text in an ANSI code and reset, got %q", got)
	}
}

func TestDisplayHash(t *testing.T) {
	full := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if got := displayHash(full, false); got != "0123456789ab…" {
		t.Fatalf("displayHash(short) = %q, want 12 chars + ellipsis", got)
	}
	if got := displayHash(full, true); got != full {
		t.Fatalf("displayHash(full) = %q, want the untouched full hash", got)
	}
	short := "abc123"
	if got := displayHash(short, false); got != short {
		t.Fatalf("displayHash of an already-short id should be untouched, got %q", got)
	}
}

func TestShortRef_NeverAddsAMarker(t *testing.T) {
	full := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	got := shortRef(full)
	if got != "0123456789ab" {
		t.Fatalf("shortRef = %q, want a bare 12-char prefix with no marker", got)
	}
	if len(got) != 12 {
		t.Fatalf("shortRef length = %d, want 12", len(got))
	}
}

func TestStylerRef_HonorsFullHashesButNeverAddsAMarker(t *testing.T) {
	full := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	short := (&styler{enabled: false, fullHashes: false}).Ref(full)
	if short != "0123456789ab" {
		t.Fatalf("Ref(short) = %q, want a bare 12-char prefix", short)
	}
	gotFull := (&styler{enabled: false, fullHashes: true}).Ref(full)
	if gotFull != full {
		t.Fatalf("Ref(full) = %q, want the untouched full hash", gotFull)
	}
}
