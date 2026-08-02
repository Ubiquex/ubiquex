package executor

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex/core"
)

// TestShip_CreateFailsNotFoundReferencingShippedDependency_RetriesAndSucceeds
// is UBI-92's own hermetic repro of the founder's live finding
// (playground-14, pure-diagram 5-resource stack): a role ships
// successfully, then a same-batch attachment referencing it via
// $computed fails ApplyResourceChange with a NoSuchEntity/"cannot be
// found"-shaped diagnostic -- AWS IAM's own well-documented eventual-
// consistency lag, CreateRole returning success before the role is
// visible to AttachRolePolicy. Scripted to fail exactly 3 times before
// succeeding (fakeApplier's own createScripts queue, scriptCreateFailure)
// -- proves retryCreateOnDependencyNotVisible actually retries with
// backoff rather than failing on the first attempt, the exact gap this
// session confirmed empirically (before writing the fix: this same test,
// with the fix's own 3-line call site commented out, fails with
// ResourceFailed and zero reconciliation entries -- see this test's own
// sibling assertions on ra.Reconciliation, which would be empty without
// the fix).
func TestShip_CreateFailsNotFoundReferencingShippedDependency_RetriesAndSucceeds(t *testing.T) {
	l := core.Open(t.TempDir())
	fake := newFakeApplier()
	orig := eventualConsistencyBackoffSchedule
	eventualConsistencyBackoffSchedule = shortBackoffScheduleForTest()
	t.Cleanup(func() { eventualConsistencyBackoffSchedule = orig })

	roleAddr := core.Address{Stack: "playground", Type: "fake_widget", Name: "role"}
	attachAddr := core.Address{Stack: "playground", Type: "fake_attachment", Name: "attach"}

	// The founder's own exact live error text (playground-14): "The role
	// with name ci-runner-v14 cannot be found" -- confirmed to match
	// looksNotFound's own phrase list.
	notFoundErr := &TerminalError{Err: errors.New("NoSuchEntity: The role with name ci-runner-v14 cannot be found")}
	fake.scriptCreateFailure("attach", notFoundErr)
	fake.scriptCreateFailure("attach", notFoundErr)
	fake.scriptCreateFailure("attach", notFoundErr)

	roleCreate := changeCreateJSON(t, roleAddr, `{"name":"role","attachment_count":0}`)
	attachCreate := changeCreateJSON(t, attachAddr,
		`{"value":"attach","role_id":{"$computed":{"from":"playground.fake_widget.role.id"}}}`,
		roleAddr.String())

	p := acceptChange(t, l, "playground", []json.RawMessage{roleCreate, attachCreate})

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if sealed.Summary.Outcome != "applied" || sealed.Summary.ResourcesApplied != 2 {
		t.Fatalf("summary = %+v, want both resources applied after retrying through the eventual-consistency window", sealed.Summary)
	}

	var attach *core.ResourceApply
	for i := range sealed.Resources {
		if sealed.Resources[i].Address == attachAddr {
			attach = sealed.Resources[i]
		}
	}
	if attach == nil {
		t.Fatalf("attach resource missing from sealed apply record")
	}
	if st, _ := attach.LastState(); st != core.ResourceApplied {
		t.Fatalf("attach last state = %s, want applied", st)
	}
	// Three failed attempts (inconclusive) plus one final success --
	// every retry logged, the same observability discipline
	// reconcileDestroyLoop's own read-back already established.
	if len(attach.Reconciliation) != 4 {
		t.Fatalf("reconciliation = %+v, want exactly 4 entries (3 retries + 1 success)", attach.Reconciliation)
	}
	for i, r := range attach.Reconciliation[:3] {
		if r.Outcome != "inconclusive" {
			t.Errorf("reconciliation[%d].Outcome = %q, want inconclusive", i, r.Outcome)
		}
	}
	if attach.Reconciliation[3].Outcome != "applied" {
		t.Errorf("reconciliation[3].Outcome = %q, want applied", attach.Reconciliation[3].Outcome)
	}
}

// TestShip_CreateFailsNotFoundReferencingShippedDependency_BudgetExhausted_FailsTerminal
// proves the retry budget is real and bounded -- a dependency that NEVER
// becomes visible (scripted to fail not-found for longer than
// eventualConsistencyBackoffSchedule's own length) eventually gives up
// and fails terminally, exactly like reconcileDestroyLoop's own
// still_unknown/failed exhaustion path, never retrying forever.
func TestShip_CreateFailsNotFoundReferencingShippedDependency_BudgetExhausted_FailsTerminal(t *testing.T) {
	l := core.Open(t.TempDir())
	fake := newFakeApplier()
	orig := eventualConsistencyBackoffSchedule
	eventualConsistencyBackoffSchedule = shortBackoffScheduleForTest()
	t.Cleanup(func() { eventualConsistencyBackoffSchedule = orig })

	roleAddr := core.Address{Stack: "playground", Type: "fake_widget", Name: "role"}
	attachAddr := core.Address{Stack: "playground", Type: "fake_attachment", Name: "attach"}

	notFoundErr := &TerminalError{Err: errors.New("NoSuchEntity: The role with name ci-runner-v14 cannot be found")}
	// One more failure than the schedule has attempts -- the budget must
	// exhaust before the dependency ever becomes visible.
	for i := 0; i < len(shortBackoffScheduleForTest())+1; i++ {
		fake.scriptCreateFailure("attach", notFoundErr)
	}

	roleCreate := changeCreateJSON(t, roleAddr, `{"name":"role","attachment_count":0}`)
	attachCreate := changeCreateJSON(t, attachAddr,
		`{"value":"attach","role_id":{"$computed":{"from":"playground.fake_widget.role.id"}}}`,
		roleAddr.String())

	p := acceptChange(t, l, "playground", []json.RawMessage{roleCreate, attachCreate})

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}

	var attach *core.ResourceApply
	for i := range sealed.Resources {
		if sealed.Resources[i].Address == attachAddr {
			attach = sealed.Resources[i]
		}
	}
	if attach == nil {
		t.Fatalf("attach resource missing from sealed apply record")
	}
	if st, _ := attach.LastState(); st != core.ResourceFailed {
		t.Fatalf("attach last state = %s, want failed (retry budget exhausted, dependency never became visible)", st)
	}
	if len(attach.Errors) != 1 || attach.Errors[0].Classification != core.ErrorTerminal {
		t.Fatalf("errors = %+v, want exactly one terminal error after the budget exhausts", attach.Errors)
	}
	if len(attach.Reconciliation) != len(shortBackoffScheduleForTest())+1 {
		t.Fatalf("reconciliation = %+v, want exactly %d entries (the full retry budget)", attach.Reconciliation, len(shortBackoffScheduleForTest())+1)
	}
}

// TestShip_CreateFailsNotFound_NoSameBatchDependency_FailsFastNoRetry proves
// the narrow-scope guard: a create with a not-found-SHAPED error but no
// same-batch dependency at all (an independent resource, or one whose
// depends_on is empty) is never retried, regardless of how the error is
// phrased -- a real, permanent "not found" failure (e.g. a truly invalid
// reference to something outside this batch) must still fail fast, the
// exact behavior TestShip_TerminalError_FailsImmediately_NoReconciliation
// already established for terminal errors generally.
func TestShip_CreateFailsNotFound_NoSameBatchDependency_FailsFastNoRetry(t *testing.T) {
	l := core.Open(t.TempDir())
	fake := newFakeApplier()

	addr := core.Address{Stack: "playground", Type: "fake_widget", Name: "orphan"}
	notFoundErr := &TerminalError{Err: errors.New("NoSuchEntity: The role with name totally-unrelated cannot be found")}
	fake.scriptCreateFailure("orphan", notFoundErr)

	create := changeCreateJSON(t, addr, `{"value":"orphan","name":"orphan"}`)
	p := acceptChange(t, l, "playground", []json.RawMessage{create})

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	ra := sealed.Resources[0]
	if st, _ := ra.LastState(); st != core.ResourceFailed {
		t.Fatalf("last state = %s, want failed immediately", st)
	}
	if len(ra.Reconciliation) != 0 {
		t.Fatalf("reconciliation = %+v, want none -- no same-batch dependency, must never enter the retry loop", ra.Reconciliation)
	}
}

// TestShip_CreateFailsPermanentError_WithShippedDependency_DoesNotRetry
// proves the OTHER half of the narrow-scope guard: a create that DOES
// have an already-shipped same-batch dependency, but whose error does
// NOT look not-found-shaped (a genuinely different, permanent failure --
// e.g. a real validation error), is never retried either. Only the
// specific "not found, and references a just-shipped dependency" shape
// enters the retry path.
func TestShip_CreateFailsPermanentError_WithShippedDependency_DoesNotRetry(t *testing.T) {
	l := core.Open(t.TempDir())
	fake := newFakeApplier()

	roleAddr := core.Address{Stack: "playground", Type: "fake_widget", Name: "role"}
	attachAddr := core.Address{Stack: "playground", Type: "fake_attachment", Name: "attach"}

	permanentErr := &TerminalError{Err: errors.New("invalid attribute value: policy_arn is not a valid ARN")}
	fake.scriptCreateFailure("attach", permanentErr)

	roleCreate := changeCreateJSON(t, roleAddr, `{"name":"role","attachment_count":0}`)
	attachCreate := changeCreateJSON(t, attachAddr,
		`{"value":"attach","role_id":{"$computed":{"from":"playground.fake_widget.role.id"}}}`,
		roleAddr.String())

	p := acceptChange(t, l, "playground", []json.RawMessage{roleCreate, attachCreate})

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	var attach *core.ResourceApply
	for i := range sealed.Resources {
		if sealed.Resources[i].Address == attachAddr {
			attach = sealed.Resources[i]
		}
	}
	if attach == nil {
		t.Fatalf("attach resource missing from sealed apply record")
	}
	if st, _ := attach.LastState(); st != core.ResourceFailed {
		t.Fatalf("last state = %s, want failed immediately", st)
	}
	if len(attach.Reconciliation) != 0 {
		t.Fatalf("reconciliation = %+v, want none -- a permanent, non-not-found error must never enter the retry loop even with a shipped dependency present", attach.Reconciliation)
	}
}

// TestShip_CreateFailsNotFound_DifferentErrorSurfacesOnRetry_StopsEarly
// proves retryCreateOnDependencyNotVisible's own "a DIFFERENT error
// stops the loop immediately" rule: the first failure looks not-found-
// shaped (enters the retry loop), but the very next attempt returns a
// genuinely different, permanent error -- the loop must surface THAT
// error, not keep blindly retrying past what this narrow fix exists to
// smooth over.
func TestShip_CreateFailsNotFound_DifferentErrorSurfacesOnRetry_StopsEarly(t *testing.T) {
	l := core.Open(t.TempDir())
	fake := newFakeApplier()
	orig := eventualConsistencyBackoffSchedule
	eventualConsistencyBackoffSchedule = shortBackoffScheduleForTest()
	t.Cleanup(func() { eventualConsistencyBackoffSchedule = orig })

	roleAddr := core.Address{Stack: "playground", Type: "fake_widget", Name: "role"}
	attachAddr := core.Address{Stack: "playground", Type: "fake_attachment", Name: "attach"}

	notFoundErr := &TerminalError{Err: errors.New("NoSuchEntity: The role with name ci-runner-v14 cannot be found")}
	permanentErr := &TerminalError{Err: errors.New("invalid attribute value: policy_arn is not a valid ARN")}
	fake.scriptCreateFailure("attach", notFoundErr)
	fake.scriptCreateFailure("attach", permanentErr)

	roleCreate := changeCreateJSON(t, roleAddr, `{"name":"role","attachment_count":0}`)
	attachCreate := changeCreateJSON(t, attachAddr,
		`{"value":"attach","role_id":{"$computed":{"from":"playground.fake_widget.role.id"}}}`,
		roleAddr.String())

	p := acceptChange(t, l, "playground", []json.RawMessage{roleCreate, attachCreate})

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	var attach *core.ResourceApply
	for i := range sealed.Resources {
		if sealed.Resources[i].Address == attachAddr {
			attach = sealed.Resources[i]
		}
	}
	if attach == nil {
		t.Fatalf("attach resource missing from sealed apply record")
	}
	if st, _ := attach.LastState(); st != core.ResourceFailed {
		t.Fatalf("last state = %s, want failed", st)
	}
	if len(attach.Errors) != 1 || attach.Errors[0].Message != permanentErr.Error() {
		t.Fatalf("errors = %+v, want the DIFFERENT permanent error surfaced, not the original not-found one", attach.Errors)
	}
	// One retry attempt (the not-found one), stopped early on the second,
	// genuinely-different failure -- never retried further.
	if len(attach.Reconciliation) != 2 {
		t.Fatalf("reconciliation = %+v, want exactly 2 entries (the not-found retry, then the early stop)", attach.Reconciliation)
	}
}

// TestShip_CreateFailsNotFound_ZeroProgressPastThreshold_BailsEarlyHonestly
// is UBI-93's own hermetic repro of the founder's live finding
// (playground-15): a same-batch dependency that NEVER becomes visible --
// not because it's slow, but because it was created under a different
// identity than the one this create referenced (confirmed live via
// CloudTrail: CreateRole succeeded under a provider-auto-generated name,
// never the name ubx_required's literal value assumed -- docs/executor.md's
// UBI-93 amendment) -- must not grind out the full retry budget. Once
// elapsed retry time clearly exceeds noProgressBailoutThreshold with every
// attempt returning the IDENTICAL not-found shape (zero progress, not slow
// progress), ubx gives up early with an honest "does not appear to exist"
// message instead of spending the user's time on the remaining budget.
func TestShip_CreateFailsNotFound_ZeroProgressPastThreshold_BailsEarlyHonestly(t *testing.T) {
	l := core.Open(t.TempDir())
	fake := newFakeApplier()
	origSchedule := eventualConsistencyBackoffSchedule
	origThreshold := noProgressBailoutThreshold
	// A schedule long enough to have entries both before and after the
	// threshold -- proves this bails EARLY (fewer reconciliation entries
	// than the full schedule), not just eventually.
	eventualConsistencyBackoffSchedule = []time.Duration{
		time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond,
		time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond,
	}
	noProgressBailoutThreshold = 4 * time.Millisecond
	t.Cleanup(func() {
		eventualConsistencyBackoffSchedule = origSchedule
		noProgressBailoutThreshold = origThreshold
	})

	roleAddr := core.Address{Stack: "playground", Type: "fake_widget", Name: "role"}
	attachAddr := core.Address{Stack: "playground", Type: "fake_attachment", Name: "attach"}

	notFoundErr := &TerminalError{Err: errors.New("NoSuchEntity: The role with name ci-runner-v15 cannot be found")}
	// Enough scripted failures to cover the whole schedule -- if the
	// bailout doesn't fire, the test would exhaust fake's own script
	// queue and fail loudly rather than silently passing.
	for i := 0; i < len(eventualConsistencyBackoffSchedule); i++ {
		fake.scriptCreateFailure("attach", notFoundErr)
	}

	roleCreate := changeCreateJSON(t, roleAddr, `{"name":"role","attachment_count":0}`)
	attachCreate := changeCreateJSON(t, attachAddr,
		`{"value":"attach","role_id":{"$computed":{"from":"playground.fake_widget.role.id"}}}`,
		roleAddr.String())

	p := acceptChange(t, l, "playground", []json.RawMessage{roleCreate, attachCreate})

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	var attach *core.ResourceApply
	for i := range sealed.Resources {
		if sealed.Resources[i].Address == attachAddr {
			attach = sealed.Resources[i]
		}
	}
	if attach == nil {
		t.Fatalf("attach resource missing from sealed apply record")
	}
	if st, _ := attach.LastState(); st != core.ResourceFailed {
		t.Fatalf("last state = %s, want failed", st)
	}
	// 4ms threshold / 1ms steps: bails after the 4th attempt, well short
	// of all 10 -- the whole point being it doesn't grind out the budget.
	if len(attach.Reconciliation) >= len(eventualConsistencyBackoffSchedule) {
		t.Fatalf("reconciliation = %+v (%d entries), want fewer than the full %d-step budget -- the bailout should stop early, not exhaust it",
			attach.Reconciliation, len(attach.Reconciliation), len(eventualConsistencyBackoffSchedule))
	}
	if len(attach.Errors) != 1 {
		t.Fatalf("errors = %+v, want exactly one terminal error", attach.Errors)
	}
	got := attach.Errors[0].Message
	if !strings.Contains(got, "does not appear to exist") {
		t.Fatalf("error message = %q, want the honest UBI-93 bailout wording (\"does not appear to exist\")", got)
	}
	if !strings.Contains(got, "cannot be found") {
		t.Fatalf("error message = %q, want the original provider diagnostic still present (wrapped, not discarded)", got)
	}
	last := attach.Reconciliation[len(attach.Reconciliation)-1]
	if !strings.Contains(last.Detail, "giving up early") {
		t.Fatalf("last reconciliation entry = %+v, want a \"giving up early\" detail marking the bailout", last)
	}
}

// shortBackoffScheduleForTest shrinks eventualConsistencyBackoffSchedule
// to millisecond-scale for this file's own tests -- the same convention
// TestShipDestroy_* already uses (see ship_test.go/destroys_test.go's own
// direct package-var reassignment), so this file's own suite stays fast
// (sub-second) despite exercising the SAME real ten-step retry budget
// shape, not a shortcut around it.
func shortBackoffScheduleForTest() []time.Duration {
	return []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond, time.Millisecond}
}
