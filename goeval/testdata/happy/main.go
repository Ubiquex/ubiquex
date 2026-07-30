package main

import sdk "github.com/ubiquex/ubx-sdk-go/runtime"

func main() {
	sdk.Main(sdk.Stack("payments", func() {
		sdk.Intent(sdk.IntentInfo{Summary: "a real, well-behaved program for goeval's own happy-path test"})
		sdk.Resource(FakeWidget, "primary", FakeWidgetConfig{Name: "primary-widget"})
	}))
}
