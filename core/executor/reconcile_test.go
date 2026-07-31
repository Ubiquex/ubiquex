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
