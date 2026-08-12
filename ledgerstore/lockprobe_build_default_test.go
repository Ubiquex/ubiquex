//go:build !cloudblob

package ledgerstore

// lockprobeBuildArgs: see the cloudblob-tagged sibling of this file --
// nothing extra needed in the default build, matching this test binary's
// own (untagged) build config.
var lockprobeBuildArgs []string
