package blueprint

import (
	"strings"
	"testing"
)

// TestParseUbxfile_SensitiveParam covers the declaration itself: a
// trailing clause on top of required, never a replacement for it, so a
// parameter always still says whether it must be given.
func TestParseUbxfile_SensitiveParam(t *testing.T) {
	for _, spec := range []string{"string, required, sensitive", "string, required,sensitive"} {
		t.Run(spec, func(t *testing.T) {
			u, err := ParseUbxfile(writeUbxfile(t, "lang: go\nparams:\n  api_token: "+spec+"\nresources: hello\n"))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(u.Params) != 1 {
				t.Fatalf("params = %+v", u.Params)
			}
			p := u.Params[0]
			if !p.Sensitive {
				t.Error("the param was not marked sensitive")
			}
			if !p.Required {
				t.Error("sensitive must not silently replace required")
			}
			if p.Type != ParamString {
				t.Errorf("type = %q", p.Type)
			}
		})
	}
}

// TestParseUbxfile_SensitiveParamCannotHaveADefault is the refusal that
// matters most, and it is a refusal rather than a warning on purpose.
//
// An Ubxfile ships inside the packaged blueprint, so a default IS
// distributed. A sensitive parameter with one hands the credential to
// everyone who pulls the blueprint, and it leaks without anything being
// run at all. That is strictly worse than the ledger case the flag exists
// for, so it cannot be left to a reader's judgement.
func TestParseUbxfile_SensitiveParamCannotHaveADefault(t *testing.T) {
	_, err := ParseUbxfile(writeUbxfile(t, "lang: go\nparams:\n  api_token: string, default \"hunter2\", sensitive\nresources: hello\n"))
	if err == nil {
		t.Fatal("a sensitive param with a default must be refused: the default ships inside the artifact")
	}
	for _, want := range []string{"sensitive", "default", "ships inside the packaged blueprint"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not explain itself, missing %q: %v", want, err)
		}
	}
}

// TestParseUbxfile_SensitiveIsNotAReplacementForRequired: "string,
// sensitive" leaves the parameter saying nothing about whether it must be
// given, and guessing "required" from the presence of sensitive would be
// inventing a declaration the author did not write.
func TestParseUbxfile_SensitiveIsNotAReplacementForRequired(t *testing.T) {
	_, err := ParseUbxfile(writeUbxfile(t, "lang: go\nparams:\n  api_token: string, sensitive\nresources: hello\n"))
	if err == nil {
		t.Fatal("expected an error: sensitive says nothing about whether the param must be given")
	}
	if !strings.Contains(err.Error(), "required, sensitive") {
		t.Errorf("the error does not show the correct spelling: %v", err)
	}
}

// TestParseUbxfile_OrdinaryParamsAreUnaffected: every blueprint written
// before this existed must parse identically, with Sensitive false.
func TestParseUbxfile_OrdinaryParamsAreUnaffected(t *testing.T) {
	u, err := ParseUbxfile(writeUbxfile(t, "lang: go\nparams:\n  repo_name: string, required\n  retries: number, default 3\nresources: hello\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, p := range u.Params {
		if p.Sensitive {
			t.Errorf("%s was marked sensitive without saying so", p.Name)
		}
	}
	if len(u.Params) != 2 || !u.Params[0].Required || u.Params[1].Default == nil {
		t.Fatalf("ordinary params did not parse as before: %+v", u.Params)
	}
}
