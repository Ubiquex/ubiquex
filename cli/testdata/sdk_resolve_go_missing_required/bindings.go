// A tiny, hand-written fake binding, mirroring fake_widget's real schema
// (provider/internal/fakeprovider) -- standing in for a real `ubx sdk
// gen`-generated one, since this fixture is about `ubx resolve
// --from-code`'s own CLI wiring, not codegen (already covered by
// sdk/codegen/templates/go's own tests). Mirrors
// cli/testdata/sdk_resolve/bindings.ts exactly, in Go.
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
