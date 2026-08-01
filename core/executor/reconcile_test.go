package executor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// TestShip_ReconcileSameBatchEffects_AttachmentMutatesRole is UBI-63
// session 3's own hermetic repro of the founder's live finding: shipping
// a 2-resource change (a role, then an attachment that depends on it and
// references the role's own real id via $computed) leaves the
// attachment's real, factual side effect on the role's own observable
// state (attachment_count) reflected on the role's own ReadResource --
// exactly the aws_iam_role_policy_attachment/aws_iam_role pattern found
// live. Without reconcileSameBatchEffects, the role's own recorded
// baseline is pinned to its apply-time snapshot (attachment_count: 0),
// taken before the attachment ever existed -- the very next scan would
// see attachment_count: 0 -> 1 and report it as drift, even though
// nothing outside this ship touched anything.
func TestShip_ReconcileSameBatchEffects_AttachmentMutatesRole(t *testing.T) {
	l := core.Open(t.TempDir())
	fake := newFakeApplier()

	roleAddr := core.Address{Stack: "payments", Type: "fake_widget", Name: "role"}
	attachAddr := core.Address{Stack: "payments", Type: "fake_attachment", Name: "attach"}

	roleCreate := changeCreateJSON(t, roleAddr, `{"name":"role","attachment_count":0}`)
	attachCreate := changeCreateJSON(t, attachAddr,
		`{"value":"attach","role_id":{"$computed":{"from":"payments.fake_widget.role.id"}}}`,
		roleAddr.String())

	p := acceptChange(t, l, "payments", []json.RawMessage{roleCreate, attachCreate})

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", p)
	if err != nil {
		t.Fatalf("ship: %v", err)
	}
	if sealed.Summary.Outcome != "applied" || sealed.Summary.ResourcesApplied != 2 {
		t.Fatalf("summary = %+v", sealed.Summary)
	}

	var roleLookup json.RawMessage
	for _, ra := range sealed.Resources {
		if ra.Address == roleAddr {
			roleLookup = ra.Lookup
		}
	}
	if len(roleLookup) == 0 {
		t.Fatalf("role's own recorded lookup missing from the sealed apply record")
	}

	// The ledger's own recorded truth for the role must already reflect
	// the attachment's real effect -- not the role's own stale,
	// apply-time-only snapshot.
	state, found, err := l.FoldState(roleAddr)
	if err != nil {
		t.Fatalf("fold state: %v", err)
	}
	if !found {
		t.Fatalf("role not found in ledger after ship")
	}
	var m map[string]interface{}
	if err := json.Unmarshal(state, &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m["attachment_count"] != float64(1) {
		t.Fatalf("recorded attachment_count = %v, want 1 -- reconcileSameBatchEffects should have folded the attachment's own real effect forward", m["attachment_count"])
	}

	// The whole point: the very next scan against the real world must
	// show zero drift, since the recorded baseline now already matches
	// what the attachment's own apply really did.
	res, err := core.RunScan(context.Background(), fake, l, core.ScanRequest{
		Address:      roleAddr,
		CurrentState: roleLookup,
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if res.Outcome != core.ScanUnchanged {
		t.Fatalf("outcome = %v, want ScanUnchanged -- attachment_count: 0 -> 1 must already be recorded, not still-pending drift", res.Outcome)
	}
}

// TestShipDestroy_SameBatchDependentDestroyed_NotRefused is UBI-63
// session 6's own hermetic proof of the destroy-side mirror of the test
// above: ship a 3-resource create (role, policy, and an attachment
// referencing both via $computed) exactly like a real
// aws_iam_role_policy_attachment, then terminate all 3 together in ONE
// destroy proposal. The attachment must destroy first (role/policy's own
// DestroyEntry.DependsOn names it, docs/resolver.md's own orphan-
// protection reverse edge set) -- and its real destroy-time effect
// (reverting attachment_count back to 0 on both) is exactly the kind of
// real, factual drift shipDestroyNode's own freshness precheck would
// otherwise refuse as "signed away" state, since the accepted destroy
// proposal's own entry.State still carries attachment_count: 1 for
// role/policy (recorded before the attachment's own destroy in this
// SAME batch). sameBatchDependentsDestroyed exists precisely so this
// doesn't stall: the whole batch must destroy cleanly, zero refusals.
func TestShipDestroy_SameBatchDependentDestroyed_NotRefused(t *testing.T) {
	l := core.Open(t.TempDir())
	fake := newFakeApplier()

	roleAddr := core.Address{Stack: "payments", Type: "fake_widget", Name: "role"}
	policyAddr := core.Address{Stack: "payments", Type: "fake_widget", Name: "policy"}
	attachAddr := core.Address{Stack: "payments", Type: "fake_attachment", Name: "attach"}

	roleCreate := changeCreateJSON(t, roleAddr, `{"name":"role","attachment_count":0}`)
	policyCreate := changeCreateJSON(t, policyAddr, `{"name":"policy","attachment_count":0}`)
	attachCreate := changeCreateJSON(t, attachAddr,
		`{"value":"attach","role_id":{"$computed":{"from":"payments.fake_widget.role.id"}},"policy_id":{"$computed":{"from":"payments.fake_widget.policy.id"}}}`,
		roleAddr.String(), policyAddr.String())

	createSealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "",
		acceptChange(t, l, "payments", []json.RawMessage{roleCreate, policyCreate, attachCreate}))
	if err != nil {
		t.Fatalf("ship create: %v", err)
	}
	if createSealed.Summary.Outcome != "applied" || createSealed.Summary.ResourcesApplied != 3 {
		t.Fatalf("create summary = %+v", createSealed.Summary)
	}

	// Confirm the create-side effect landed on BOTH role and policy
	// (session 3 already proved this for role alone) -- the destroy-side
	// test below is only meaningful if both start at attachment_count: 1.
	for _, addr := range []core.Address{roleAddr, policyAddr} {
		state, found, err := l.FoldState(addr)
		if err != nil || !found {
			t.Fatalf("fold state %s: found=%v err=%v", addr, found, err)
		}
		var m map[string]interface{}
		if err := json.Unmarshal(state, &m); err != nil {
			t.Fatalf("decode %s: %v", addr, err)
		}
		if m["attachment_count"] != float64(1) {
			t.Fatalf("%s recorded attachment_count = %v, want 1 before the destroy leg begins", addr, m["attachment_count"])
		}
	}

	// Each resource's own real lookup (its auto-assigned "created-N" id,
	// NOT addr.String() -- unlike adoptFake's own convention, a genuine
	// create's id is assigned by the fake applier itself, session 3's
	// own AttachmentMutatesRole test extracts it the same way) --
	// destroyTargetInput's own lookupJSON(addr) default would point at a
	// nonexistent id here, silently misreporting "already absent"
	// instead of exercising a real destroy at all.
	realLookupFor := func(addr core.Address) json.RawMessage {
		for _, ra := range createSealed.Resources {
			if ra.Address == addr {
				return ra.Lookup
			}
		}
		t.Fatalf("%s: no recorded lookup in the create's own sealed apply record", addr)
		return nil
	}

	// Build the destroy proposal directly (core/executor stays
	// independently testable from core/resolver, the same posture every
	// other destroy fixture in this package already takes) -- role and
	// policy each DependsOn the attachment (must be destroyed first);
	// the attachment itself has no destroy-side dependency.
	var destroys []core.DestroyEntry
	var inputs []core.ResolutionInput
	for _, addr := range []core.Address{roleAddr, policyAddr, attachAddr} {
		state, found, err := l.FoldState(addr)
		if err != nil || !found {
			t.Fatalf("fold state %s: found=%v err=%v", addr, found, err)
		}
		var dependsOn []string
		if addr != attachAddr {
			dependsOn = []string{attachAddr.String()}
		}
		destroys = append(destroys, core.DestroyEntry{Address: addr, State: state, DependsOn: dependsOn})
		hash, err := core.ObservedHash(state)
		if err != nil {
			t.Fatalf("observed hash %s: %v", addr, err)
		}
		inputs = append(inputs, core.ResolutionInput{Kind: "destroy_target", Resource: addr.String(), ObservedHash: hash, Lookup: realLookupFor(addr)})
	}
	destroyProposal := acceptDestroy(t, l, "payments", destroys, inputs)

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", destroyProposal)
	if err != nil {
		t.Fatalf("ship destroy: %v", err)
	}
	if sealed.Summary.Outcome != "applied" || sealed.Summary.ResourcesFailed != 0 {
		t.Fatalf("destroy summary = %+v, want applied with zero refusals", sealed.Summary)
	}
	for _, ra := range sealed.Resources {
		for _, e := range ra.Errors {
			t.Fatalf("%s: unexpected error %q (classification %s)", ra.Address, e.Message, e.Classification)
		}
		for _, r := range ra.Reconciliation {
			if r.Outcome == "present_drifted" {
				t.Fatalf("%s: refused as drifted -- sameBatchDependentsDestroyed should have explained the attachment's own destroy-time effect", ra.Address)
			}
		}
	}
	for _, addr := range []core.Address{roleAddr, policyAddr, attachAddr} {
		var lookup map[string]string
		if err := json.Unmarshal(realLookupFor(addr), &lookup); err != nil {
			t.Fatalf("decode lookup %s: %v", addr, err)
		}
		if _, exists := fake.resources[lookup["id"]]; exists {
			t.Fatalf("%s (id %s) still present after what should have been a clean destroy", addr, lookup["id"])
		}
	}
}

// TestShipDestroy_FiveResourcePlatformShape_NotRefused is UBI-63 session
// 6's own fresh, fake-simulated reproduction of the founder's exact
// platform.md topology that started this whole arc: ECR + SQS + IAM role
// + policy + attachment, 5 resources, shipped and then terminated
// together as ONE batch. Unlike the 3-resource test above, this proves
// two things at once: (1) the same-batch-dependent fix scales to the
// real shape, not just the minimal repro, and (2) it doesn't overreach --
// the two resources with NO relationship to the attachment (repo/queue,
// standing in for aws_ecr_repository/aws_sqs_queue) have
// sameBatchDependentsDestroyed=false and must destroy via the ordinary
// present-and-matching path, not the same-batch-dependent bypass.
func TestShipDestroy_FiveResourcePlatformShape_NotRefused(t *testing.T) {
	l := core.Open(t.TempDir())
	fake := newFakeApplier()

	roleAddr := core.Address{Stack: "platform", Type: "fake_widget", Name: "ci-runner"}
	policyAddr := core.Address{Stack: "platform", Type: "fake_widget", Name: "ci-runner-access"}
	attachAddr := core.Address{Stack: "platform", Type: "fake_attachment", Name: "ci-runner-access-attachment"}
	repoAddr := core.Address{Stack: "platform", Type: "fake_widget", Name: "ci-artifacts"}
	queueAddr := core.Address{Stack: "platform", Type: "fake_widget", Name: "ci-notifications"}

	roleCreate := changeCreateJSON(t, roleAddr, `{"name":"ci-runner","attachment_count":0}`)
	policyCreate := changeCreateJSON(t, policyAddr, `{"name":"ci-runner-access","attachment_count":0}`)
	attachCreate := changeCreateJSON(t, attachAddr,
		`{"value":"attach","role_id":{"$computed":{"from":"platform.fake_widget.ci-runner.id"}},"policy_id":{"$computed":{"from":"platform.fake_widget.ci-runner-access.id"}}}`,
		roleAddr.String(), policyAddr.String())
	repoCreate := changeCreateJSON(t, repoAddr, `{"name":"ci-artifacts"}`)
	queueCreate := changeCreateJSON(t, queueAddr, `{"name":"ci-notifications"}`)

	createSealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "",
		acceptChange(t, l, "platform", []json.RawMessage{roleCreate, policyCreate, attachCreate, repoCreate, queueCreate}))
	if err != nil {
		t.Fatalf("ship create: %v", err)
	}
	if createSealed.Summary.Outcome != "applied" || createSealed.Summary.ResourcesApplied != 5 {
		t.Fatalf("create summary = %+v", createSealed.Summary)
	}

	realLookupFor := func(addr core.Address) json.RawMessage {
		for _, ra := range createSealed.Resources {
			if ra.Address == addr {
				return ra.Lookup
			}
		}
		t.Fatalf("%s: no recorded lookup in the create's own sealed apply record", addr)
		return nil
	}

	allAddrs := []core.Address{attachAddr, roleAddr, policyAddr, repoAddr, queueAddr}
	dependents := map[core.Address][]string{
		roleAddr:   {attachAddr.String()},
		policyAddr: {attachAddr.String()},
		// repoAddr/queueAddr: no relationship to the attachment at all --
		// this is the "doesn't overreach" half of the proof.
	}

	var destroys []core.DestroyEntry
	var inputs []core.ResolutionInput
	for _, addr := range allAddrs {
		state, found, err := l.FoldState(addr)
		if err != nil || !found {
			t.Fatalf("fold state %s: found=%v err=%v", addr, found, err)
		}
		destroys = append(destroys, core.DestroyEntry{Address: addr, State: state, DependsOn: dependents[addr]})
		hash, err := core.ObservedHash(state)
		if err != nil {
			t.Fatalf("observed hash %s: %v", addr, err)
		}
		inputs = append(inputs, core.ResolutionInput{Kind: "destroy_target", Resource: addr.String(), ObservedHash: hash, Lookup: realLookupFor(addr)})
	}
	destroyProposal := acceptDestroy(t, l, "platform", destroys, inputs)

	sealed, err := Ship(context.Background(), l, SingleApplierPool(fake, nil), "", destroyProposal)
	if err != nil {
		t.Fatalf("ship destroy: %v", err)
	}
	if sealed.Summary.Outcome != "applied" || sealed.Summary.ResourcesFailed != 0 {
		t.Fatalf("destroy summary = %+v, want applied with zero refusals", sealed.Summary)
	}
	for _, ra := range sealed.Resources {
		for _, e := range ra.Errors {
			t.Fatalf("%s: unexpected error %q (classification %s)", ra.Address, e.Message, e.Classification)
		}
		for _, r := range ra.Reconciliation {
			if r.Outcome == "present_drifted" {
				t.Fatalf("%s: refused as drifted", ra.Address)
			}
		}
	}
	for _, addr := range allAddrs {
		var lookup map[string]string
		if err := json.Unmarshal(realLookupFor(addr), &lookup); err != nil {
			t.Fatalf("decode lookup %s: %v", addr, err)
		}
		if _, exists := fake.resources[lookup["id"]]; exists {
			t.Fatalf("%s (id %s) still present after what should have been a clean destroy", addr, lookup["id"])
		}
	}
}
