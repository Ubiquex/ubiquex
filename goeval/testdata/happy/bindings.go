package main

import sdk "github.com/ubiquex/ubx-sdk-go/runtime"

var FakeWidget = sdk.ResourceBinding{
	WireType: "fake_widget",
	Fields: sdk.FieldMap{
		"Name": {WireName: "name"},
	},
}

type FakeWidgetConfig struct {
	Name any
}
