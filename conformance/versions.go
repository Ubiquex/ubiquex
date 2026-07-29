package conformance

// PinnedProviderVersions is the single source of truth for which
// provider version this project's own conformance suite is currently
// pinned to, per source (UBI-8's own "explicit version pins only,
// reproducibility" rule) — shared by conformance/probegen's own bulk
// schema fetch (the generator behind findings_generated.go, see that
// file's own header) so there is one canonical place these five pins
// live, not scattered duplicates.
//
// NOTE, honestly: every individual live conformance test file
// (aws_live_test.go's own awsProviderVersion, gcp_provider_test.go's own
// gcpProviderVersion, and so on) still carries its OWN identical literal
// today — this map doesn't yet replace them (a real, deliberate scope
// line: refactoring four already-working test files' own constants to
// consume this map instead is a mechanical follow-up, not attempted
// under this session's own time budget). Bumping a provider version
// means updating BOTH this map and that file's own constant, kept in
// sync by hand for now — named honestly rather than silently assumed
// unified.
var PinnedProviderVersions = map[string]string{
	"hashicorp/aws":        "6.54.0",
	"hashicorp/google":     "7.40.0",
	"hashicorp/azurerm":    "5.0.0",
	"hashicorp/kubernetes": "2.35.1",
	"hashicorp/helm":       "2.17.0",
}
