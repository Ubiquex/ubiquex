package main

import (
	"fmt"
	"os"

	sdk "github.com/ubiquex/ubx-sdk-go/runtime"
)

var widget = sdk.ResourceBinding{
	WireType: "fake_widget",
	Fields:   sdk.FieldMap{"Name": {WireName: "name"}},
}

type widgetConfig struct {
	Name any
}

// Env-var visibility is a real, expected exception -- the sandbox
// (sandbox-exec/bwrap) is a filesystem/network policy, not an
// environment-variable filter (docs/sdk.md). This program proves that
// distinction directly: it does NOT fail, it succeeds with an empty
// value, because the evaluator scrubs the environment separately
// (cmd.Env), not via the sandbox profile itself.
func main() {
	sdk.Main(sdk.Stack("payments", func() {
		sdk.Intent(sdk.IntentInfo{Summary: "env should be scrubbed, not merely sandbox-denied"})
		sdk.Resource(widget, "primary", widgetConfig{Name: fmt.Sprintf("home=%q", os.Getenv("HOME"))})
	}))
}
