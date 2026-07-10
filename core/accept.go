package core

import (
	"errors"
	"fmt"
	"os/user"
	"time"
)

// ErrAlreadyAccepted means Accept was called on a proposal that already
// has an ID or acceptance data — it looks like it went through this path
// once already.
var ErrAlreadyAccepted = errors.New("proposal already accepted")

// Accept computes p's canonical hash, fills in ID/Status/Acceptance — the
// "local" acceptance tier (docs/architecture.md: "Acceptance = PR merge
// binding ... or local `ubx accept`; optional hardened cryptographic
// signing tier later" — this is that local tier: it records who/how/when,
// not a cryptographic signature) — and appends the result to l.
//
// p must be draft-shaped: ID and Acceptance unset. p.Parent must already be
// set to the ledger's current head (callers are expected to have read
// Head() themselves before resolving/authoring p — Accept doesn't fill
// Parent in for them, since silently doing so would hide a stale-parent
// bug rather than surfacing it as ErrParentMismatch).
func Accept(l *Ledger, p *Proposal) (*Proposal, error) {
	if p.ID != "" || p.Acceptance != nil {
		return nil, fmt.Errorf("accept: %w", ErrAlreadyAccepted)
	}

	hash, err := Hash(p)
	if err != nil {
		return nil, fmt.Errorf("accept: %w", err)
	}

	p.ID = hash
	p.Status = StatusAccepted
	p.Acceptance = &Acceptance{
		Method:     "local",
		Approvers:  []string{localApprover()},
		AcceptedAt: time.Now().UTC().Format(time.RFC3339),
	}

	if err := l.Append(p); err != nil {
		return nil, fmt.Errorf("accept: %w", err)
	}
	return p, nil
}

func localApprover() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "unknown"
}
