package pyeval

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// wasi_retry_test.go covers UBI-255's retry half: a single transient
// failure at the third-party release URL used to fail the whole
// acquisition, and therefore the whole test suite and any real Python
// evaluation.
//
// On 2026-09-09 that URL answered HTTP 500 and took main red as eight
// failures across two packages, none of which mentioned a download.

// The schedule is shortened for tests: the behaviour under test is which
// failures are retried, not how long the waits are.
func fastRetries(t *testing.T) {
	t.Helper()
	original := downloadRetrySchedule
	downloadRetrySchedule = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { downloadRetrySchedule = original })
}

// zipWithInterpreter builds the archive shape the real release has, so a
// successful attempt is genuinely successful rather than merely a 200.
func zipWithInterpreter(t *testing.T) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)
	f, err := w.Create("python.wasm")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("not a real interpreter, but a real file")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Create("lib/os.py"); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The exact shape of the outage: a 500, then success.
func TestDownload_RetriesATransientServerError(t *testing.T) {
	fastRetries(t)
	body := zipWithInterpreter(t)

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write(body)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "python-wasi")
	if err := downloadAndExtractWithRetry(context.Background(), srv.URL, dest); err != nil {
		t.Fatalf("a single 500 must not fail the acquisition: %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Errorf("attempts = %d, want 2 (one failure, one success)", got)
	}
	if err := verifyPythonWasiDir(dest); err != nil {
		t.Errorf("the retry succeeded but produced nothing usable: %v", err)
	}
}

// A wrong pin is not a blip. Retrying it three times only delays a clear
// answer, so a 404 fails immediately.
func TestDownload_DoesNotRetryA404(t *testing.T) {
	fastRetries(t)

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	err := downloadAndExtractWithRetry(context.Background(), srv.URL, filepath.Join(t.TempDir(), "d"))
	if err == nil {
		t.Fatal("want a failure for a URL that is not there")
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1: a 404 means the pinned version does not exist, which every retry will confirm identically", got)
	}
}

// Retries are bounded. A sustained outage has to end as a real failure
// rather than hanging the suite.
func TestDownload_GivesUpOnASustainedOutage(t *testing.T) {
	fastRetries(t)

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := downloadAndExtractWithRetry(context.Background(), srv.URL, filepath.Join(t.TempDir(), "d"))
	if err == nil {
		t.Fatal("want a failure once the retries are exhausted")
	}
	if want := int32(len(downloadRetrySchedule) + 1); attempts.Load() != want {
		t.Errorf("attempts = %d, want %d (the first try plus one per backoff)", attempts.Load(), want)
	}
	var status *downloadStatusError
	if !errors.As(err, &status) || status.code != 500 {
		t.Errorf("the failure should still carry the real status, got: %v", err)
	}
}

// Classification is on the status, not on the text of a message, so a
// reworded error cannot silently change what gets retried.
func TestIsTransientDownloadFailure_Classification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"500", &downloadStatusError{code: 500}, true},
		{"503", &downloadStatusError{code: 503}, true},
		{"429", &downloadStatusError{code: 429}, true},
		{"404", &downloadStatusError{code: 404}, false},
		{"403", &downloadStatusError{code: 403}, false},
		{"transport failure", errors.New("dial tcp: connection reset by peer"), true},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := isTransientDownloadFailure(c.err); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// PrefetchInterpreter is what CI calls. It must honour the same escape
// hatch a developer uses, so a prefetch step never downloads anything
// when the asset was supplied by hand.
func TestPrefetchInterpreter_HonoursTheDirectoryOverride(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "python.wasm"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(pythonWasiDirEnv, dir)

	got, err := PrefetchInterpreter(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Errorf("got %q, want the supplied directory %q", got, dir)
	}
}
