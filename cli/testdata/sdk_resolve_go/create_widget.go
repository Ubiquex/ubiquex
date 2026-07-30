// Mirrors cli/testdata/sdk_resolve/create_widget.ts exactly, in Go.
package main

import sdk "github.com/ubiquex/ubx-sdk-go/runtime"

func main() {
	sdk.Main(sdk.Stack("payments", func() {
		sdk.Intent(sdk.IntentInfo{Summary: "add widget1, authored in Go"})
		sdk.Resource(FakeWidget, "widget1", FakeWidgetConfig{
			Name: "widget1",
			Tags: map[string]any{"env": "prod"},
		})
	}))
}
