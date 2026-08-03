module github.com/ubiquex/ubiquex/sdk/conformance/programs/go

go 1.26.3

require (
	github.com/ubiquex/ubx-sdk-aws v0.0.0
	github.com/ubiquex/ubx-sdk-go v0.0.0
)

replace github.com/ubiquex/ubx-sdk-go => ../../../go

// UBI-98: generated/hashicorp-aws is now its own repo-shaped module
// (own go.mod -- generateGoProviderRepo's own output), not a plain
// subpackage of this module -- a nested go.mod is a real module
// boundary Go itself enforces, so importing it needs a require+replace
// exactly like any other external dependency would, same as
// github.com/ubiquex/ubx-sdk-go's own replace above (neither is
// published yet -- docs/sdk.md's own "No runtime package is published
// yet" section, now joined by "no generated bindings repo is published
// yet" either).
replace github.com/ubiquex/ubx-sdk-aws => ./generated/hashicorp-aws
