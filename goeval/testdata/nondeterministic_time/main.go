package main

import (
	"fmt"
	"time"

	sdk "github.com/ubiquex/ubx-sdk-go/runtime"
)

var widget = sdk.ResourceBinding{
	WireType: "fake_widget",
	Fields:   sdk.FieldMap{"Name": {WireName: "name"}},
}

type widgetConfig struct {
	Name any
}

// Compiled Go has no monkey-patchable Date.now()-equivalent to eagerly
// guard against (docs/sdk.md's own "The determinism story turns out
// simpler than TypeScript's") -- core.DoubleRun alone is the whole
// backstop, and this program's job is to prove it actually catches a
// real nondeterministic call, not just a hypothetical one.
func main() {
	sdk.Main(sdk.Stack("payments", func() {
		sdk.Intent(sdk.IntentInfo{Summary: "leaks time.Now() into config -- must be caught by DoubleRun"})
		sdk.Resource(widget, "primary", widgetConfig{Name: fmt.Sprintf("widget-%d", time.Now().UnixNano())})
	}))
}
