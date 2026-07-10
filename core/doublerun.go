package core

import (
	"bytes"
	"errors"
)

// ErrDoubleRunMismatch means an evaluator produced two different byte
// sequences across two runs of what's supposed to be the same deterministic
// computation — a hard failure per docs/schema.md's double-run rule.
var ErrDoubleRunMismatch = errors.New("double-run mismatch: evaluator produced different bytes across two runs")

// DoubleRun runs fn twice and requires byte-identical results. Any
// evaluator that feeds hashed content (canonicalizers, and later resolvers,
// IR renderers, ...) should go through this rather than trusting a single
// run, so nondeterminism (map iteration order, uninitialized state,
// environment/clock leakage, ...) gets caught as a hard failure at propose
// time instead of surfacing later as a silently unstable hash.
func DoubleRun(fn func() ([]byte, error)) ([]byte, error) {
	a, err := fn()
	if err != nil {
		return nil, err
	}
	b, err := fn()
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(a, b) {
		return nil, ErrDoubleRunMismatch
	}
	return a, nil
}
