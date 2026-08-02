// UBI-90's own live SDK-medium repro fixture: a "buggy" Go SDK program
// that simply never sets Name (fake_widget's own schema-Required
// attribute) -- sdk/go/runtime's own serializeOpaque deliberately omits a
// zero-value `any` field from the wire config entirely (see its own
// "not set -- omitted, matching TS's 'key not present at all'" comment),
// so this produces exactly the same "key entirely absent" shape a
// diagram-authored resource's always-empty config does. Mirrors
// cli/testdata/sdk_resolve_go/create_widget.go, minus the one line that
// sets Name.
package main

import sdk "github.com/ubiquex/ubx-sdk-go/runtime"

func main() {
	sdk.Main(sdk.Stack("payments", func() {
		sdk.Intent(sdk.IntentInfo{Summary: "widget1, missing its own required name (buggy SDK program)"})
		sdk.Resource(FakeWidget, "widget1", FakeWidgetConfig{
			Tags: map[string]any{"env": "prod"},
		})
	}))
}
