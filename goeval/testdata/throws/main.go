package main

import sdk "github.com/ubiquex/ubx-sdk-go/runtime"

func main() {
	sdk.Main(sdk.Stack("payments", func() {
		sdk.Intent(sdk.IntentInfo{Summary: "about to panic"})
		panic("deliberate mid-evaluation panic")
	}))
}
