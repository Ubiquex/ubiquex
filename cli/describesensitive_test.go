package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/blueprint"
	"github.com/ubiquex/ubiquex/blueprint/spec"
)

func describeParams(t *testing.T, params []blueprint.Param) string {
	t.Helper()
	var buf bytes.Buffer
	// Through renderBlueprintDescription, the function `ubx blueprint
	// describe` actually calls, rather than the row helper. The same
	// entry-point failure has happened three times in this codebase and
	// is recorded on UBI-252: a marker that is not wired in is not a
	// marker, and the wiring is one line with no behaviour of its own.
	renderBlueprintDescription(&buf, &styler{}, &blueprint.Description{
		Name:          "ci-platform",
		Lang:          "go",
		Params:        params,
		DefaultsKnown: true,
	})
	return buf.String()
}

// TestDescribe_ShowsTheSensitiveMarker is the gap this closes.
//
// describe is what someone runs to see what a blueprint expects BEFORE
// calling it, which makes it the one place a caller finds out that an
// argument they are about to pass will not be recorded. The schema
// carried the flag and the rendering dropped it, so the caller learned
// nothing.
func TestDescribe_ShowsTheSensitiveMarker(t *testing.T) {
	out := describeParams(t, []blueprint.Param{
		{Name: "queue_name", Type: spec.ParamString, Required: true},
		{Name: "api_token", Type: spec.ParamString, Required: true, Sensitive: true},
	})

	if !strings.Contains(out, "sensitive") {
		t.Fatalf("describe does not show the marker at all:\n%s", out)
	}
	// On the right row, and not on the other one.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "queue_name") && strings.Contains(line, "sensitive") {
			t.Errorf("an ordinary param was marked sensitive:\n%s", line)
		}
	}
	if !strings.Contains(out, "required, sensitive") {
		t.Errorf("the marker does not read as an addition to required:\n%s", out)
	}
}

// TestDescribe_ExplainsWhatSensitiveMeans: a bare word on a row says
// what the parameter is called, not what it costs the caller. Two things
// are easy to assume and both are wrong, so both are stated.
func TestDescribe_ExplainsWhatSensitiveMeans(t *testing.T) {
	out := describeParams(t, []blueprint.Param{
		{Name: "api_token", Type: spec.ParamString, Required: true, Sensitive: true},
	})
	for _, want := range []string{
		"never written to the ledger", // what does not happen
		"Its name is",                 // and what does, since the argument does not vanish
		"recording only",              // and the boundary, since the name invites a broader reading
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the note does not say %q:\n%s", want, out)
		}
	}
}

// TestDescribe_NoNoteWhenNothingIsSensitive: a note explaining a marker
// nobody used is noise on every other blueprint forever.
func TestDescribe_NoNoteWhenNothingIsSensitive(t *testing.T) {
	out := describeParams(t, []blueprint.Param{
		{Name: "queue_name", Type: spec.ParamString, Required: true},
		{Name: "retention_days", Type: spec.ParamNumber, Default: 14},
	})
	if strings.Contains(out, "sensitive") {
		t.Errorf("a blueprint with no sensitive params mentions sensitivity:\n%s", out)
	}
}

// TestDescribe_EmptyParamsStillRenders guards the note's own guard: a
// blueprint with no params at all must not trip the scan.
func TestDescribe_EmptyParamsStillRenders(t *testing.T) {
	out := describeParams(t, nil)
	if !strings.Contains(out, "(none)") {
		t.Errorf("a blueprint with no params no longer renders:\n%s", out)
	}
	if strings.Contains(out, "sensitive") {
		t.Errorf("a blueprint with no params mentions sensitivity:\n%s", out)
	}
}
