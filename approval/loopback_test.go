package approval

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

func nothingPending(context.Context, string, string) (*Pending, error) { return nil, ErrNotPending }

func TestNewLoopback_RefusesNonLoopback(t *testing.T) {
	for _, listen := range []string{"0.0.0.0:0", "[::]:0", "192.0.2.1:0", "localhost:0", "example.com:80"} {
		if l, err := NewLoopback(listen, nothingPending, nil); err == nil {
			l.Close()
			t.Errorf("%s: served on a non-loopback address", listen)
		}
	}
}

func TestLoopback_Request(t *testing.T) {
	l, err := NewLoopback("127.0.0.1:0", nothingPending, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	hash := strings.Repeat("c", 64)
	h, err := l.Request("pay ments", hash)
	if err != nil {
		t.Fatal(err)
	}
	if h.Method != core.MethodLoopback || h.URL != l.URL()+"/approve/pay%20ments/"+hash {
		t.Fatalf("handle = %+v", h)
	}
	if _, err := l.Request("payments", "abc"); err == nil {
		t.Error("a short hash was accepted")
	}
	if _, err := l.Request("", hash); err == nil {
		t.Error("an empty stack was accepted")
	}
}

func TestLoopback_UnknownPathsAndMethods(t *testing.T) {
	l, err := NewLoopback("127.0.0.1:0", nothingPending, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	for path, want := range map[string]int{
		"/":                     http.StatusNotFound,
		"/approve/payments/abc": http.StatusNotFound,
		"/approve/payments/" + strings.Repeat("c", 64): http.StatusNotFound, // nothing pending
	} {
		resp, err := http.Get(l.URL() + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s: %d, want %d", path, resp.StatusCode, want)
		}
	}
	req, _ := http.NewRequest(http.MethodDelete, l.URL()+"/approve/payments/"+strings.Repeat("c", 64), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("DELETE: %d, want 405", resp.StatusCode)
	}
}

// TestLoopback_ReferrerPolicyKeepsOrigin pins the policy that lets a real
// browser's form POST carry its Origin. Under "no-referrer" browsers send
// "Origin: null" on that POST, and every real approval was refused.
func TestLoopback_ReferrerPolicyKeepsOrigin(t *testing.T) {
	l, err := NewLoopback("127.0.0.1:0", nothingPending, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	resp, err := http.Get(l.URL() + "/approve/payments/" + strings.Repeat("c", 64))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Referrer-Policy"); got != "same-origin" {
		t.Fatalf("Referrer-Policy = %q, want same-origin", got)
	}
}
