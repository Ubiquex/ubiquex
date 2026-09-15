package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// TestWriteBlueprintDeclaration covers the three answers this can give,
// which are genuinely different and must not be collapsed: the table
// declared it, the call named it inline, or nothing recorded one.
func TestWriteBlueprintDeclaration(t *testing.T) {
	cases := []struct {
		name   string
		src    core.IntentSource
		want   []string
		absent []string
	}{
		{
			name: "declared, git, with a parsed rev and path",
			src: core.IntentSource{
				Declaration:    "git+https://github.com/ubiquex/bps.git#ref=v2.1.0&path=ci",
				DeclaredSource: "https://github.com/ubiquex/bps.git",
				DeclaredRev:    "v2.1.0",
				DeclaredPath:   "ci",
			},
			// The verbatim declaration already spells out both, so the
			// parsed clause is suppressed rather than repeated.
			want:   []string{"declared as", "github.com/ubiquex/bps.git#ref=v2.1.0&path=ci"},
			absent: []string{" at v2.1.0", ", path ci"},
		},
		{
			name: "declared, oci, tag embedded so no rev clause",
			src: core.IntentSource{
				Declaration:    "oci://ghcr.io/ubx-blueprints/ts-bp:v0.1.0",
				DeclaredSource: "oci://ghcr.io/ubx-blueprints/ts-bp:v0.1.0",
			},
			want:   []string{"declared as oci://ghcr.io/ubx-blueprints/ts-bp:v0.1.0"},
			absent: []string{" at ", "path "},
		},
		{
			name: "called directly is not a table entry and must not read like one",
			src:  core.IntentSource{DeclaredSource: "./blueprints/ci-platform"},
			want: []string{"called directly: ./blueprints/ci-platform"},
			// The distinction the ledger records, preserved in what a
			// person reads: no table said this.
			absent: []string{"declared as"},
		},
		{
			name: "pre-UBI-282 proposal says so rather than printing nothing",
			src:  core.IntentSource{Kind: "blueprint", Ref: "ci:sha256:aaa"},
			want: []string{"no declaration recorded"},
		},
		{
			name:   "a path of . is noise, not information",
			src:    core.IntentSource{Declaration: "git+https://x/y.git", DeclaredPath: "."},
			absent: []string{"path ."},
		},
		{
			// The case the redundancy exists for: the verbatim string does
			// not say which rev it resolved to, so the parse is the only
			// place a reader can see it.
			name: "a parse not evident from the string is still shown",
			src: core.IntentSource{
				Declaration:    "git+https://github.com/ubiquex/bps.git",
				DeclaredSource: "https://github.com/ubiquex/bps.git",
				DeclaredRev:    "v2.1.0",
			},
			want: []string{"at v2.1.0"},
		},
		{
			name: "a path clause alone does not start with a stray comma",
			src: core.IntentSource{
				Declaration:  "git+https://github.com/ubiquex/bps.git",
				DeclaredPath: "ci",
			},
			want:   []string{"path ci"},
			absent: []string{".git, path"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			writeBlueprintDeclaration(&buf, "  ", c.src)
			got := buf.String()
			if got == "" {
				t.Fatal("every case must render something: silence reads as 'this had no declaration'")
			}
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("output does not contain %q:\n%s", w, got)
				}
			}
			for _, a := range c.absent {
				if strings.Contains(got, a) {
					t.Errorf("output should not contain %q:\n%s", a, got)
				}
			}
		})
	}
}
