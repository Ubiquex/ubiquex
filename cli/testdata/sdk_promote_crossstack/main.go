// A real SDK program whose own resolved value genuinely depends on
// WHICH ledger it's resolved against -- sdk.CrossStack (UBI-134) names
// the "networking" neighbor by STACK, resolved at resolve time against
// whichever ledger's own [ledger] base store resolver.Resolve actually
// runs with, never a value baked in at evaluation time. Used by
// TestPromote_SDKAuthoredSource_ContextAwareDrafting_DiffersByTarget
// (promote_test.go) to prove the SAME unchanged program, re-run by
// "ubx promote" against two different targets, genuinely resolves
// differently per target -- never a frozen, copied value.
package main

import sdk "github.com/ubiquex/ubx-sdk-go/runtime"

func main() {
	sdk.Main(sdk.Stack("payments", func() {
		sdk.Intent(sdk.IntentInfo{Summary: "widget referencing networking's own vpc, by stack name"})
		sdk.Resource(FakeWidget, "widget1", FakeWidgetConfig{
			Name: "widget1",
			Tags: map[string]any{
				"vpc_id": sdk.CrossStack("networking", "networking.aws_vpc.main.id"),
			},
		})
	}))
}
