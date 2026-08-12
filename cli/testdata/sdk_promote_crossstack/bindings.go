// A tiny, hand-written fake binding, mirroring fake_widget's real schema
// (provider/internal/fakeprovider) -- standing in for a real `ubx sdk
// gen`-generated one, since this fixture is about `ubx promote`'s own
// SDK re-run path (TestPromote_SDKAuthoredSource_ContextAwareDrafting_
// DiffersByTarget, promote_test.go), not codegen. Mirrors
// cli/testdata/sdk_resolve_go/bindings.go exactly.
package main

import sdk "github.com/ubiquex/ubx-sdk-go/runtime"

var FakeWidget = sdk.ResourceBinding{
	WireType: "fake_widget",
	Fields: sdk.FieldMap{
		"Name": {WireName: "name"},
		"Tags": {WireName: "tags"},
	},
}

type FakeWidgetConfig struct {
	Name any
	Tags any
}
