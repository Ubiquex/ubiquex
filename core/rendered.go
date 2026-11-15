package core

import (
	"context"
	"errors"
	"fmt"
)

// ErrRenderedHashMismatch means bytes stored as a render do not hash to
// the name they are stored under. Stored evidence that fails its own hash
// proves nothing, so it is never returned as if it were good.
var ErrRenderedHashMismatch = errors.New("stored rendered bytes do not hash to their own name")

// ErrRenderNotStored means an acceptance would name a render that is not
// in the ledger store. Refused rather than recorded: an acceptance pointing
// at bytes nobody kept proves nothing about what was seen.
var ErrRenderNotStored = errors.New("rendered bytes not stored")

// ErrRenderMismatch means the proposal being accepted does not render to
// the bytes the approver was shown. Either the proposal changed after the
// page was rendered, or the renderer did (a new RendererVersion between
// showing and clicking). Either way the human approved something other
// than this, and the approval is refused, never carried across.
var ErrRenderMismatch = errors.New("proposal does not render to the approved bytes")

// HashRendered is the rendered_hash of b: plain SHA-256, hex, of exactly
// the bytes shown. Deliberately not domain-prefixed the way proposal
// hashes are (hashDomainPrefix): rendered bytes are stored under their own
// namespace, so there is no other kind of object to confuse them with, and
// a plain hash means anyone holding the bytes can check the record with
// sha256sum and nothing from this codebase.
func HashRendered(b []byte) string { return sha256Hex(b) }

// StageRender is the first half of an approval that records what the
// approver saw (UBX-230): render p, hash the bytes, store them
// if absent, then read them back and return the STORED bytes for the
// caller to show. Showing what was read back, rather than what was just
// produced, means what the human sees is by construction what the ledger
// keeps.
//
// The second half is AcceptFromLoopback, after the human clicks. The order
// is the point: bytes are stored before any acceptance names them, so a
// crash between the two leaves an orphan render (harmless, and shared by
// any later identical render) and never an acceptance pointing at nothing.
func (l *Ledger) StageRender(p *Proposal) (renderedHash string, stored []byte, err error) {
	rendered, err := RenderProposal(p)
	if err != nil {
		return "", nil, fmt.Errorf("stage render: %w", err)
	}
	hash := HashRendered(rendered)
	ctx := context.Background()
	if err := l.store.WriteRenderedIfAbsent(ctx, hash, rendered); err != nil && !errors.Is(err, ErrStoreObjectExists) {
		return "", nil, fmt.Errorf("stage render: %w", err)
	}
	stored, err = l.ReadRendered(hash)
	if err != nil {
		return "", nil, fmt.Errorf("stage render: %w", err)
	}
	return hash, stored, nil
}

// ReadRendered returns the rendered bytes stored under hash, verified
// against it. ErrStoreObjectNotFound (wrapped) if nothing is stored there;
// ErrRenderedHashMismatch if what is stored does not hash to its name.
func (l *Ledger) ReadRendered(hash string) ([]byte, error) {
	if !IsHash(hash) {
		return nil, fmt.Errorf("read rendered: %q is not a 64-character hex hash", hash)
	}
	b, err := l.store.ReadRendered(context.Background(), hash)
	if err != nil {
		return nil, fmt.Errorf("read rendered %s: %w", hash, err)
	}
	if got := HashRendered(b); got != hash {
		return nil, fmt.Errorf("read rendered %s: %w (hashes to %s)", hash, ErrRenderedHashMismatch, got)
	}
	return b, nil
}
