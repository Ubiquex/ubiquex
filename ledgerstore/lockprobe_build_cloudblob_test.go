//go:build cloudblob

package ledgerstore

// lockprobeBuildArgs is buildLockprobe's own extra `go build` args --
// the spawned lockprobe subprocess must be built with the SAME tags as
// this test binary itself, or it silently lacks whatever drivers this
// build enabled (UBI-56: a real, live-caught bug -- the GCS/Azure live
// tests all failed with "no driver registered" the first time, because
// lockprobe was being built untagged even under a `-tags cloudblob` test
// run).
var lockprobeBuildArgs = []string{"-tags", "cloudblob"}
