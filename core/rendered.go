package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
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
// approver saw (UBI-294): render p, hash the bytes, store them
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

// Outcomes of VerifyLoopbackAcceptance. Each one names a different
// answer, and "could not check" is never folded into "checked and wrong".
const (
	// LoopbackVerified: the stored bytes hash to RenderedHash, and the
	// proposal re-renders to exactly those bytes.
	LoopbackVerified = "verified"
	// LoopbackNoRenderedHash: a loopback acceptance that names no render.
	// Every loopback acceptance AcceptFromLoopback writes carries one, so
	// this record did not come from it.
	LoopbackNoRenderedHash = "no_rendered_hash"
	// LoopbackRenderedMissing: the store holds nothing under RenderedHash.
	// The record claims the approver saw something that can no longer be
	// produced. A failure, not an inconclusive check: the evidence the
	// record rests on is gone.
	LoopbackRenderedMissing = "rendered_missing"
	// LoopbackRenderedHashMismatch: the stored bytes do not hash to
	// RenderedHash. They were altered or corrupted after storing.
	LoopbackRenderedHashMismatch = "rendered_hash_mismatch"
	// LoopbackRerenderDiffers: the stored bytes verify, the renderer is the
	// same version that produced them, and the proposal renders to
	// something else.
	LoopbackRerenderDiffers = "rerender_differs"
	// LoopbackRendererChanged: the stored bytes verify against their hash,
	// but a different renderer version produced them, so this binary cannot
	// reproduce them. What the approver saw is still on record and intact;
	// only the re-render step cannot be done. Inconclusive.
	LoopbackRendererChanged = "renderer_version_changed"
)

// LoopbackCheck is VerifyLoopbackAcceptance's result. Outcome is one of the
// Loopback* constants; Detail is a sentence for a human.
type LoopbackCheck struct {
	Outcome string
	Detail  string
}

// Failed reports whether the check found a problem, as opposed to passing
// or being unable to finish.
func (c LoopbackCheck) Failed() bool {
	return c.Outcome != LoopbackVerified && c.Outcome != LoopbackRendererChanged
}

// Inconclusive reports whether the check could not finish.
func (c LoopbackCheck) Inconclusive() bool { return c.Outcome == LoopbackRendererChanged }

// VerifyLoopbackAcceptance re-checks a loopback acceptance from the ledger
// alone (UBI-298): read the stored render by RenderedHash, confirm the
// bytes hash to it, re-render p, and confirm the result is byte-identical.
//
// What a pass proves: the bytes on record are the bytes the approval was
// bound to, and they are a faithful render of this exact proposal. What it
// does not prove: who approved. A loopback acceptance attests that someone
// at the machine clicked, and its approver is "loopback:presence" for that
// reason.
//
// err is only for a store that could not be read at all. Every finding
// about the record comes back as an Outcome.
func VerifyLoopbackAcceptance(l *Ledger, p *Proposal) (LoopbackCheck, error) {
	if p.Acceptance == nil || p.Acceptance.RenderedHash == "" {
		return LoopbackCheck{LoopbackNoRenderedHash,
			"this loopback acceptance records no rendered_hash, so there is nothing to show what the approver saw"}, nil
	}
	want := p.Acceptance.RenderedHash
	if !IsHash(want) {
		return LoopbackCheck{LoopbackRenderedHashMismatch,
			fmt.Sprintf("rendered_hash %q is not a 64-character hex hash", want)}, nil
	}
	stored, err := l.store.ReadRendered(context.Background(), want)
	if errors.Is(err, ErrStoreObjectNotFound) {
		return LoopbackCheck{LoopbackRenderedMissing,
			"the ledger store holds no render under rendered_hash " + want + "; what the approver saw cannot be shown"}, nil
	}
	if err != nil {
		return LoopbackCheck{}, fmt.Errorf("read rendered %s: %w", want, err)
	}
	if got := HashRendered(stored); got != want {
		return LoopbackCheck{LoopbackRenderedHashMismatch,
			"the stored render hashes to " + got + ", not to its rendered_hash " + want + "; it was altered after storing"}, nil
	}
	again, err := RenderProposal(p)
	if err != nil {
		return LoopbackCheck{LoopbackRerenderDiffers, "the proposal cannot be re-rendered: " + err.Error()}, nil
	}
	if bytes.Equal(again, stored) {
		return LoopbackCheck{LoopbackVerified,
			"the stored render is intact and is exactly what this proposal renders to; it attests presence at the machine, not identity"}, nil
	}
	storedVersion, _, _ := strings.Cut(string(stored), "\n")
	if storedVersion != RendererVersion {
		return LoopbackCheck{LoopbackRendererChanged,
			fmt.Sprintf("the stored render is intact, but %q produced it and this binary renders %q, so it cannot be reproduced here", storedVersion, RendererVersion)}, nil
	}
	return LoopbackCheck{LoopbackRerenderDiffers,
		"the stored render is intact but this proposal renders to different bytes with the same renderer version"}, nil
}
