package blueprint

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"oras.land/oras-go/v2/content/oci"
)

// The progress callback is the whole point of transfer.go, and ORAS
// offers nothing like it, so these prove the counting against oras-go's
// own real local OCI store rather than a fake.

func TestPushToTarget_ReportsBytesNeverExceedingTheTotal(t *testing.T) {
	ctx := context.Background()

	dir := writeSampleBuiltBlueprint(t)
	tarPath := filepath.Join(t.TempDir(), "ci-platform-v1.tar.gz")
	m, err := Package(ctx, dir, tarPath)
	if err != nil {
		t.Fatalf("Package: %v", err)
	}
	info, err := os.Stat(tarPath)
	if err != nil {
		t.Fatal(err)
	}

	target, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var calls int
	var lastMoved, lastTotal int64
	progress := func(moved, total int64) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		lastMoved, lastTotal = moved, total
	}

	if err := pushToTarget(ctx, tarPath, m, target, "v1", WithProgress(progress)); err != nil {
		t.Fatalf("pushToTarget: %v", err)
	}

	if calls == 0 {
		t.Fatal("progress was never reported")
	}
	// A transfer moves the manifest and config blobs as well as the
	// payload, so both numbers exceed the tarball. What matters is that
	// they agree about what is being counted.
	if lastTotal < info.Size() {
		t.Errorf("total = %d, want at least the tarball's own size %d", lastTotal, info.Size())
	}
	if lastMoved < info.Size() {
		t.Errorf("moved = %d, want at least the tarball's own %d bytes", lastMoved, info.Size())
	}
	// The regression this guards: the total used to be the tarball's
	// size alone while the counter counted every byte, so the running
	// line read "748 B of 737 B" and the bar sat pinned at 100 percent
	// before it had finished.
	if lastMoved > lastTotal {
		t.Errorf("moved %d exceeds the announced total %d, so the bar would read over 100%%", lastMoved, lastTotal)
	}
}

func TestPullFromTarget_ReportsBytesWithATotalLearnedFromTheManifest(t *testing.T) {
	ctx := context.Background()

	dir := writeSampleBuiltBlueprint(t)
	tarPath := filepath.Join(t.TempDir(), "ci-platform-v1.tar.gz")
	m, err := Package(ctx, dir, tarPath)
	if err != nil {
		t.Fatalf("Package: %v", err)
	}
	info, err := os.Stat(tarPath)
	if err != nil {
		t.Fatal(err)
	}

	target, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := pushToTarget(ctx, tarPath, m, target, "v1"); err != nil {
		t.Fatalf("pushToTarget: %v", err)
	}

	var mu sync.Mutex
	var calls int
	var lastMoved, lastTotal int64
	progress := func(moved, total int64) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		lastMoved, lastTotal = moved, total
	}

	dest := filepath.Join(t.TempDir(), "pulled")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := pullFromTarget(ctx, target, "v1", dest, WithProgress(progress)); err != nil {
		t.Fatalf("pullFromTarget: %v", err)
	}

	if calls == 0 {
		t.Fatal("progress was never reported")
	}
	// A pull learns its total from the manifest, which arrives after the
	// copy has started, so the interesting property is that it ends up
	// covering the payload rather than being known up front.
	if lastTotal < info.Size() {
		t.Errorf("total = %d, want at least the tarball's own size %d", lastTotal, info.Size())
	}
	if lastMoved < info.Size() {
		t.Errorf("moved = %d, want at least the tarball's own %d bytes", lastMoved, info.Size())
	}
	if lastMoved > lastTotal {
		t.Errorf("moved %d exceeds the announced total %d, so the bar would read over 100%%", lastMoved, lastTotal)
	}
}

// A transfer with no progress option must behave exactly as before,
// which is what keeps every existing caller unaffected.
func TestTransfer_NoProgressOptionLeavesTheStoresUnwrapped(t *testing.T) {
	ctx := context.Background()

	dir := writeSampleBuiltBlueprint(t)
	tarPath := filepath.Join(t.TempDir(), "ci-platform-v1.tar.gz")
	m, err := Package(ctx, dir, tarPath)
	if err != nil {
		t.Fatalf("Package: %v", err)
	}
	target, err := oci.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := pushToTarget(ctx, tarPath, m, target, "v1"); err != nil {
		t.Fatalf("pushToTarget without progress: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "pulled")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := pullFromTarget(ctx, target, "v1", dest); err != nil {
		t.Fatalf("pullFromTarget without progress: %v", err)
	}
	if _, err := Verify(dest); err != nil {
		t.Fatalf("Verify(pulled): %v", err)
	}
}

// A nil callback is accepted and ignored, so a caller can pass one
// through unconditionally rather than branching.
func TestWithProgress_NilCallbackIsInert(t *testing.T) {
	o := applyTransferOptions([]TransferOption{WithProgress(nil), nil})
	if o.onProgress != nil {
		t.Fatal("a nil callback was retained")
	}
}
