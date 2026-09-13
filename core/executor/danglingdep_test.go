package executor

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// A delta entry whose depends_on names an address the proposal does not
// carry used to panic: byAddr[key] returns a nil *changeNode and
// .dependsOn dereferences it inside topoSortAddresses' own closure.
//
// One real way to reach it is an unchanged dependency that produces no
// delta entry while a dependent still carries the edge (UBI-267), but
// the point of the check is that ANY cause gets a sentence naming the
// resource and the dependency rather than a stack trace.
func TestChangeNodesOf_DanglingDependencyRefusesInsteadOfPanicking(t *testing.T) {
	create := map[string]interface{}{
		"stack": "bp2",
		"type":  "aws_sqs_queue",
		"name":  "queue",
		"config": map[string]interface{}{
			"redrive_policy": map[string]interface{}{
				"$computed": map[string]interface{}{"from": "bp2.aws_sqs_queue.dlq.arn"},
			},
		},
		"depends_on": []string{"bp2.aws_sqs_queue.dlq"},
	}
	raw, err := json.Marshal(create)
	if err != nil {
		t.Fatal(err)
	}
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         "bp2",
		Kind:          core.KindChange,
		Delta:         core.Delta{Creates: []json.RawMessage{raw}},
	}

	nodes, err := changeNodesOf(p)
	if err == nil {
		t.Fatalf("a dangling dependency was accepted, %d node(s) returned", len(nodes))
	}
	if !errors.Is(err, ErrDanglingDependency) {
		t.Fatalf("err = %v, want ErrDanglingDependency", err)
	}
	// The message has to name both ends, which is the whole reason this
	// is not a panic.
	for _, want := range []string{"bp2.aws_sqs_queue.queue", "bp2.aws_sqs_queue.dlq"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %v", want, err)
		}
	}
}

// A proposal whose edges all resolve is unaffected.
func TestChangeNodesOf_ResolvableDependencyIsOrderedNormally(t *testing.T) {
	node := func(name string, deps ...string) json.RawMessage {
		m := map[string]interface{}{"stack": "bp2", "type": "aws_sqs_queue", "name": name}
		if len(deps) > 0 {
			m["depends_on"] = deps
		}
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	p := &core.Proposal{
		SchemaVersion: core.SchemaVersion,
		Stack:         "bp2",
		Kind:          core.KindChange,
		Delta: core.Delta{Creates: []json.RawMessage{
			node("queue", "bp2.aws_sqs_queue.dlq"),
			node("dlq"),
		}},
	}

	nodes, err := changeNodesOf(p)
	if err != nil {
		t.Fatalf("changeNodesOf: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("want 2 nodes, got %d", len(nodes))
	}
	if nodes[0].addr.Name != "dlq" {
		t.Errorf("the dependency is not ordered first: %s then %s", nodes[0].addr, nodes[1].addr)
	}
}
