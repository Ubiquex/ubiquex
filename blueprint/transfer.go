package blueprint

// transfer.go carries byte-level progress out of an OCI push or pull,
// and nothing else. Rendering it is the CLI's job; this package only
// reports numbers.
//
// ORAS does not offer this. oras.CopyOptions exposes PreCopy, PostCopy,
// OnCopySkipped and OnMounted, all of which fire once per DESCRIPTOR:
// they can say "a 3.4 MB blob is starting" and "it finished", never how
// far through it is. Nothing in oras-go v2.6.2 reports bytes as they
// move.
//
// So the bytes are counted where they actually pass through a reader,
// by wrapping a content store's own Push with one that counts what it
// reads. The wrapped store is ALWAYS our own local file.Store, never
// the remote repository, and that is load-bearing rather than
// incidental: oras.Copy type-asserts its arguments for
// registry.Mounter, registry.ReferencePusher and
// registry.ReferenceFetcher, and wrapping a remote.Repository in a
// plain struct would hide all three and silently turn off
// cross-repository blob mounting and push-by-reference. A file.Store
// implements none of them (verified directly, not assumed), so wrapping
// it loses nothing.
//
// Push copies local -> remote, so the local store is the SOURCE and its
// Fetch is what gets wrapped. Pull copies remote -> local, so the local
// store is the DESTINATION and its Push is what gets wrapped. Either
// way the same bytes are counted, one hop from the network.

import (
	"context"
	"io"
	"sync/atomic"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
)

// TransferProgress is called as bytes move. total is 0 until the size
// is known, which for a pull is only once the manifest names the layer.
//
// Called from whichever goroutine oras.Copy is currently copying on, so
// an implementation has to be safe to call from any of them.
type TransferProgress func(transferred, total int64)

// TransferOption configures a transferring Pull or Push. Variadic so
// every existing caller, none of which wants progress, keeps compiling
// and keeps behaving identically.
type TransferOption func(*transferOptions)

type transferOptions struct {
	onProgress TransferProgress
}

// WithProgress reports bytes as they move. A nil callback is accepted
// and ignored, so a caller can pass one through unconditionally.
func WithProgress(fn TransferProgress) TransferOption {
	return func(o *transferOptions) { o.onProgress = fn }
}

func applyTransferOptions(opts []TransferOption) transferOptions {
	var o transferOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

// byteCounter accumulates bytes across every blob in one transfer and
// reports the running total against an expected size.
//
// A transfer moves more than the payload: a manifest and a config blob
// go along with it, together a few hundred bytes against a tarball
// measured in megabytes. They are counted too. Excluding them would
// mean deciding which descriptor is "the real one" from inside a
// counter, and a percentage that ends at 99.98 is worse than one that
// includes every byte actually moved.
type byteCounter struct {
	transferred atomic.Int64
	total       atomic.Int64
	onProgress  TransferProgress
}

func newByteCounter(total int64, onProgress TransferProgress) *byteCounter {
	c := &byteCounter{onProgress: onProgress}
	c.total.Store(total)
	return c
}

// addTotal records one more expected descriptor size, learned
// mid-transfer. That is a pull's situation: the layer's size arrives
// with the manifest, which is itself the first thing fetched, so the
// expected total is only knowable as descriptors are announced.
//
// An atomic add rather than a load-then-store, because oras announces
// descriptors from its own copy goroutines and two announcements can
// overlap. Reading the total and writing back a sum would lose one.
func (c *byteCounter) addTotal(n int64) {
	if n > 0 {
		c.total.Add(n)
	}
}

func (c *byteCounter) add(n int64) {
	if n <= 0 {
		return
	}
	moved := c.transferred.Add(n)
	if c.onProgress != nil {
		c.onProgress(moved, c.total.Load())
	}
}

// Transferred is what actually moved, which a failure reports so a
// reader knows where it stopped.
func (c *byteCounter) Transferred() int64 { return c.transferred.Load() }

// countingReader reports every byte read from the stream oras is
// copying.
type countingReader struct {
	r io.Reader
	c *byteCounter
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.c.add(int64(n))
	return n, err
}

// countingStore wraps a local content store so both directions of a
// copy can be measured with one type. Only the method on the side the
// bytes flow through does any counting; the rest delegate untouched.
//
// Deliberately implements content.Storage and the tag methods by
// delegation rather than embedding, so adding a method to one of oras's
// interfaces is a compile error here rather than a silently
// unimplemented optimisation.
type countingStore struct {
	inner   countableStore
	counter *byteCounter
	// countPush measures the destination side of a copy (a pull), and
	// countFetch the source side (a push).
	countPush  bool
	countFetch bool
}

// countableStore is the subset of a local file.Store this wraps.
type countableStore interface {
	content.Storage
	Tag(ctx context.Context, desc ocispec.Descriptor, reference string) error
	Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error)
}

func (s *countingStore) Push(ctx context.Context, expected ocispec.Descriptor, r io.Reader) error {
	if s.countPush {
		r = &countingReader{r: r, c: s.counter}
	}
	return s.inner.Push(ctx, expected, r)
}

func (s *countingStore) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	rc, err := s.inner.Fetch(ctx, target)
	if err != nil || !s.countFetch {
		return rc, err
	}
	return &countingReadCloser{ReadCloser: rc, c: s.counter}, nil
}

func (s *countingStore) Exists(ctx context.Context, target ocispec.Descriptor) (bool, error) {
	return s.inner.Exists(ctx, target)
}

func (s *countingStore) Tag(ctx context.Context, desc ocispec.Descriptor, reference string) error {
	return s.inner.Tag(ctx, desc, reference)
}

func (s *countingStore) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	return s.inner.Resolve(ctx, reference)
}

type countingReadCloser struct {
	io.ReadCloser
	c *byteCounter
}

func (r *countingReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	r.c.add(int64(n))
	return n, err
}
