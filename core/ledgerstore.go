package core

import (
	"context"
	"errors"
	"time"
)

// LedgerStore is the storage abstraction extracted from core.Ledger's own
// implicit filesystem operations (UBI-32 Arc B, docs/architecture.md —
// "LedgerStore interface"): everything Ledger needs from wherever its
// bytes actually live, and nothing more. Confirmed against the real
// pre-existing code before this was written, not designed from the
// docs/architecture.md sketch alone — every chain-walking read
// (Chain/Fleet/ProposalsForAddress/LastObservedHash/FoldState) goes
// through Head()+Read() repeatedly, never a directory listing; only
// ApplyAttempts needs one. That absence of a "list every proposal"
// requirement is deliberate and load-bearing: it's the one operation
// object stores are worst at making cheap and strongly consistent, and
// this interface never needs it.
//
// core stays dependency-free (same inversion as StateReader/EventLookup,
// see core/accept.go/core/attribution.go): this file defines the
// interface and the sentinel errors implementations return; the
// git-directory implementation (gitledgerstore.go, stdlib only) lives
// here too, but a gocloud.dev/blob-backed implementation lives in its own
// sibling package (ledgerstore/), the same split audit/cloudtrail, audit/gcp,
// audit/k8s already establish for heavy SDK dependencies.
//
// Every method is byte-level: JSON marshal/unmarshal, and therefore
// ErrCorruptLedgerEntry/ErrCorruptLedgerHead/ErrCorruptApplyRecord's own
// decoding, stay exactly where they already lived in core/ledger.go and
// core/apply.go. A store only ever moves bytes; corrupted-content
// detection is identical regardless of which store produced them.
type LedgerStore interface {
	// Exists reports whether this store has ever been used at all --
	// core.Ledger.Exists's own distinction between "no store here" and "a
	// real store that's simply never recorded a given address."
	Exists(ctx context.Context) (bool, error)

	// ReadProposal reads one proposal's raw bytes by ID.
	// ErrStoreObjectNotFound if no such object exists.
	ReadProposal(ctx context.Context, id string) ([]byte, error)

	// WriteProposalIfAbsent writes data at id, but only if nothing is
	// already there -- ErrStoreObjectExists if it is. This create-only
	// semantic is what makes proposal immutability a store-level
	// guarantee rather than something core.Ledger.Append alone enforces
	// via a racy stat-then-write.
	WriteProposalIfAbsent(ctx context.Context, id string, data []byte) error

	// Head returns the current ledger head hash, or "" for a genesis
	// (never-appended-to) ledger.
	Head(ctx context.Context) (string, error)

	// AdvanceHead moves the head from expectedPrev to newHead --
	// ErrStoreHeadMoved if the store's own current head is no longer
	// expectedPrev (a compare-and-swap, not an unconditional overwrite;
	// see docs/architecture.md's own "Head/AdvanceHead" section for the
	// git-directory implementation's own zero-behavior-change reasoning
	// and the blob implementation's immutable-edge CAS design).
	AdvanceHead(ctx context.Context, expectedPrev, newHead string) error

	// ReadApply reads one specific (proposalID, attempt) apply record's
	// raw bytes. ErrStoreObjectNotFound if no such attempt exists.
	ReadApply(ctx context.Context, proposalID string, attempt int64) ([]byte, error)

	// WriteApply durably writes (proposalID, attempt)'s current content --
	// deliberately NOT create-only: SaveApplyProgress calls this
	// repeatedly for the SAME attempt as it progresses (docs/executor.md's
	// own THE invariant), sealed only once SealApply writes its final
	// content. A store's own implementation is responsible for this being
	// durable-before-return (temp+fsync+rename for a local filesystem; a
	// single object PUT is already atomic for a real object store).
	WriteApply(ctx context.Context, proposalID string, attempt int64, data []byte) error

	// ListApplyAttempts returns every attempt number recorded for
	// proposalID so far, in no particular order (callers sort). Empty,
	// nil slice if none exist -- never an error for "no attempts yet."
	ListApplyAttempts(ctx context.Context, proposalID string) ([]int64, error)

	// Lock acquires the ledger-mutating-operation lock (Append, via
	// Accept/AcceptFromMerge/BeginApply), blocking with the store's own
	// retry/backoff policy up to its own idea of a wait window, and
	// returns a release function. ttl is a hint a remote store uses to
	// bound how long a lock is honored before being treated as abandoned
	// (docs/architecture.md's own "Locking: a TTL, not a PID" section) --
	// the git-directory store, which can ask "is the holder's process
	// still alive," ignores it and keeps its own existing PID-liveness
	// staleness check unchanged.
	Lock(ctx context.Context, ttl time.Duration) (release func(context.Context) error, err error)

	// ReadSalt returns this store's redaction salt, and whether one has
	// ever been generated. Never generates one itself -- see
	// WriteSaltIfAbsent.
	ReadSalt(ctx context.Context) ([]byte, bool, error)

	// WriteSaltIfAbsent persists salt, but only if no salt exists yet --
	// ErrStoreObjectExists if one was already written (by this call or a
	// racing one; the caller re-reads via ReadSalt to get the winner's
	// bytes, see core.Ledger.Salt).
	WriteSaltIfAbsent(ctx context.Context, salt []byte) error
}

var (
	// ErrStoreObjectNotFound is a LedgerStore implementation's own "no
	// such object" signal -- translated by core.Ledger's own methods into
	// the existing, more specific ErrProposalNotFound/ErrApplyRecordNotFound.
	ErrStoreObjectNotFound = errors.New("ledger store: object not found")

	// ErrStoreObjectExists is a create-only write's own conflict signal.
	ErrStoreObjectExists = errors.New("ledger store: object already exists")

	// ErrStoreHeadMoved is AdvanceHead's own compare-and-swap failure
	// signal -- translated by core.Ledger.Append into the existing
	// ErrParentMismatch.
	ErrStoreHeadMoved = errors.New("ledger store: head moved during compare-and-swap")
)
