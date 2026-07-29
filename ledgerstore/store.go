// Package ledgerstore implements core.LedgerStore against any
// gocloud.dev/blob bucket -- s3://, gs://, and azblob:// all through this
// one code path (UBI-32 Arc B, docs/architecture.md — "LedgerStore
// interface"). Kept out of package core deliberately: core stays
// dependency-free (the same inversion StateReader/EventLookup already
// establish for audit/cloudtrail, audit/gcp, audit/k8s's own heavy SDK
// dependencies), and gocloud.dev/blob's own driver packages each pull in
// their respective cloud SDK.
package ledgerstore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"

	"github.com/ubiquex/ubiquex-cli/core"
)

// Key layout, all relative to whatever prefix the caller's own *blob.Bucket
// is already scoped to (see Open, below, for how a store URI's own
// path-style prefix and a stack name become that scope):
//
//	proposals/<id>.prop.json
//	applies/<id>.attempt-<n>.apply.json
//	heads/genesis            -- the first proposal's own ID
//	heads/<parent-id>        -- the proposal that comes right after parent-id
//	head-hint                -- best-effort cache, never load-bearing
//	lock                     -- the distributed lock object
//	salt                     -- the redaction salt
const (
	proposalsPrefix = "proposals/"
	appliesPrefix   = "applies/"
	headsPrefix     = "heads/"
	genesisEdgeName = "genesis"
	headHintKey     = "head-hint"
	lockKey         = "lock"
	saltKey         = "salt"
)

func proposalKey(id string) string { return proposalsPrefix + id + ".prop.json" }

func applyKey(proposalID string, attempt int64) string {
	return fmt.Sprintf("%s%s.attempt-%d.apply.json", appliesPrefix, proposalID, attempt)
}

var applyKeyPattern = regexp.MustCompile(`^applies/([0-9a-f]{64})\.attempt-([0-9]+)\.apply\.json$`)

func headEdgeKey(parent string) string {
	if parent == "" {
		return headsPrefix + genesisEdgeName
	}
	return headsPrefix + parent
}

// defaultLockTTL/lockWaitTimeout/lockRetryInterval are package vars, not
// constants, so tests can shrink them instead of a multi-second sleep for
// every contention/expiry test -- same convention core's own
// lockWaitTimeout/lockRetryInterval and cli's configSearchStartDir use.
// lockWaitTimeout/lockRetryInterval are deliberately more generous than
// core's own git-local defaults: every poll here is a real network round
// trip against a remote store, not a local file stat, so a tight retry
// budget tuned for local contention was found (live, against real S3) to
// time out on genuine, ordinary contention rather than actual failure.
var (
	defaultLockTTL    = 2 * time.Minute
	lockWaitTimeout   = 30 * time.Second
	lockRetryInterval = 250 * time.Millisecond
)

// Store implements core.LedgerStore against bucket. bucket is expected to
// already be scoped (via blob.PrefixedBucket, applied by Open below) to
// exactly one stack's own address -- Store itself has no concept of
// "stack" at all, matching docs/architecture.md's own addressing rule
// that a store never maps anything per stack itself.
type Store struct {
	bucket *blob.Bucket
}

// New wraps bucket as a core.LedgerStore. Exposed directly (as well as
// via Open) so a caller that already has its own *blob.Bucket -- a
// hermetic test's memblob bucket, say -- never has to round-trip through
// a URL string just to get one.
func New(bucket *blob.Bucket) *Store { return &Store{bucket: bucket} }

func (s *Store) Exists(ctx context.Context) (bool, error) {
	iter := s.bucket.List(nil)
	_, err := iter.Next(ctx)
	if err == io.EOF {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) ReadProposal(ctx context.Context, id string) ([]byte, error) {
	b, err := s.bucket.ReadAll(ctx, proposalKey(id))
	if gcerrors.Code(err) == gcerrors.NotFound {
		return nil, core.ErrStoreObjectNotFound
	}
	return b, err
}

func (s *Store) WriteProposalIfAbsent(ctx context.Context, id string, data []byte) error {
	err := s.bucket.WriteAll(ctx, proposalKey(id), data, &blob.WriterOptions{IfNotExist: true})
	if gcerrors.Code(err) == gcerrors.FailedPrecondition {
		return core.ErrStoreObjectExists
	}
	return err
}

// Head resolves the current head: a best-effort cached hint, then
// following heads/ edges forward from there until one is missing (see
// docs/architecture.md's own "Head/AdvanceHead" section for why this is a
// real compare-and-swap and not just an optimistic overwrite, and why the
// hint bounds the common case rather than always walking from genesis).
func (s *Store) Head(ctx context.Context) (string, error) {
	start := ""
	hint, err := s.bucket.ReadAll(ctx, headHintKey)
	switch {
	case err == nil:
		h := strings.TrimSpace(string(hint))
		if h != "" && !core.IsHash(h) {
			return "", fmt.Errorf("%w: head-hint is not a 64-character hex hash", core.ErrCorruptLedgerHead)
		}
		start = h
	case gcerrors.Code(err) == gcerrors.NotFound:
		// no hint yet -- resolve from genesis
	default:
		return "", err
	}
	return s.resolveHeadFrom(ctx, start)
}

func (s *Store) resolveHeadFrom(ctx context.Context, start string) (string, error) {
	current := start
	for {
		next, found, err := s.readEdge(ctx, current)
		if err != nil {
			return "", err
		}
		if !found {
			return current, nil
		}
		current = next
	}
}

func (s *Store) readEdge(ctx context.Context, parent string) (child string, found bool, err error) {
	b, err := s.bucket.ReadAll(ctx, headEdgeKey(parent))
	if gcerrors.Code(err) == gcerrors.NotFound {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	child = strings.TrimSpace(string(b))
	if !core.IsHash(child) {
		return "", false, fmt.Errorf("%w: %s is not a 64-character hex hash", core.ErrCorruptLedgerHead, headEdgeKey(parent))
	}
	return child, true, nil
}

// AdvanceHead writes the immutable edge heads/<expectedPrev> -> newHead,
// using the store's own create-only write as the actual compare-and-swap:
// exactly one of any number of concurrent callers advancing from the same
// expectedPrev can ever win.
func (s *Store) AdvanceHead(ctx context.Context, expectedPrev, newHead string) error {
	err := s.bucket.WriteAll(ctx, headEdgeKey(expectedPrev), []byte(newHead), &blob.WriterOptions{IfNotExist: true})
	if gcerrors.Code(err) == gcerrors.FailedPrecondition {
		return fmt.Errorf("%w: expected %q", core.ErrStoreHeadMoved, expectedPrev)
	}
	if err != nil {
		return err
	}
	// Best-effort cache update -- never load-bearing; Head() re-derives
	// correctly even if this write is lost (a crash right here) or racing
	// with another advance's own hint update.
	_ = s.bucket.WriteAll(ctx, headHintKey, []byte(newHead), nil)
	return nil
}

func (s *Store) ReadApply(ctx context.Context, proposalID string, attempt int64) ([]byte, error) {
	b, err := s.bucket.ReadAll(ctx, applyKey(proposalID, attempt))
	if gcerrors.Code(err) == gcerrors.NotFound {
		return nil, core.ErrStoreObjectNotFound
	}
	return b, err
}

// WriteApply is a plain overwrite (not create-only) -- SaveApplyProgress
// calls this repeatedly for the same attempt as it progresses. A single
// object PUT is already atomic on every driver this package supports, so
// no separate temp+rename dance is needed the way the local-filesystem
// git store requires.
func (s *Store) WriteApply(ctx context.Context, proposalID string, attempt int64, data []byte) error {
	return s.bucket.WriteAll(ctx, applyKey(proposalID, attempt), data, nil)
}

func (s *Store) ListApplyAttempts(ctx context.Context, proposalID string) ([]int64, error) {
	prefix := appliesPrefix + proposalID + "."
	var attempts []int64
	iter := s.bucket.List(&blob.ListOptions{Prefix: prefix})
	for {
		obj, err := iter.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		m := applyKeyPattern.FindStringSubmatch(obj.Key)
		if m == nil || m[1] != proposalID {
			continue
		}
		n, err := strconv.ParseInt(m[2], 10, 64)
		if err != nil {
			continue
		}
		attempts = append(attempts, n)
	}
	return attempts, nil
}

// lockPayload is the lock object's own JSON content -- just enough to
// explain, and to decide, staleness: no PID, no hostname assumed
// meaningful across machines, only when this specific acquisition expires.
type lockPayload struct {
	ExpiresAt time.Time `json:"expires_at"`
}

// Lock acquires the distributed lock object, using the identical
// create-only primitive AdvanceHead uses. ttl (defaultLockTTL if zero) is
// the only staleness signal a remote store can have -- there is no
// process-liveness question a multi-machine lock can ask, unlike
// git-local's PID check (docs/architecture.md — "Locking: a TTL, not a
// PID"). An expired lock is reclaimed via a best-effort delete-then-retry:
// safe even if two callers both attempt the reclaim, since the actual
// winner is still decided by the create-only write underneath, not by
// which caller's delete lands first.
func (s *Store) Lock(ctx context.Context, ttl time.Duration) (release func(context.Context) error, err error) {
	if ttl <= 0 {
		ttl = defaultLockTTL
	}
	deadline := time.Now().Add(lockWaitTimeout)
	for {
		payload, marshalErr := json.Marshal(lockPayload{ExpiresAt: time.Now().Add(ttl)})
		if marshalErr != nil {
			return nil, marshalErr
		}
		writeErr := s.bucket.WriteAll(ctx, lockKey, payload, &blob.WriterOptions{IfNotExist: true})
		if writeErr == nil {
			return func(ctx context.Context) error { return s.bucket.Delete(ctx, lockKey) }, nil
		}
		if gcerrors.Code(writeErr) != gcerrors.FailedPrecondition {
			return nil, writeErr
		}

		// Contended. If the existing lock has expired, reclaim it (best
		// effort -- the next loop iteration's own create-only write is
		// what actually decides the winner, regardless of who deletes).
		if existing, readErr := s.bucket.ReadAll(ctx, lockKey); readErr == nil {
			var p lockPayload
			if json.Unmarshal(existing, &p) == nil && time.Now().After(p.ExpiresAt) {
				_ = s.bucket.Delete(ctx, lockKey)
				continue
			}
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w: lock object", core.ErrLedgerLocked)
		}
		time.Sleep(lockRetryInterval)
	}
}

func (s *Store) ReadSalt(ctx context.Context) ([]byte, bool, error) {
	b, err := s.bucket.ReadAll(ctx, saltKey)
	if gcerrors.Code(err) == gcerrors.NotFound {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

func (s *Store) WriteSaltIfAbsent(ctx context.Context, salt []byte) error {
	err := s.bucket.WriteAll(ctx, saltKey, salt, &blob.WriterOptions{IfNotExist: true})
	if gcerrors.Code(err) == gcerrors.FailedPrecondition {
		return core.ErrStoreObjectExists
	}
	return err
}

var _ core.LedgerStore = (*Store)(nil)
