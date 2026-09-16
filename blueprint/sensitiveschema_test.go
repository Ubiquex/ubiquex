package blueprint

import (
	"context"
	"testing"
)

// The same logical blueprint in all three languages: one ordinary param
// and one carrying a credential, each marked with that language's own
// metadata mechanism.
//
// None of the three markers changes what the blueprint's own code
// receives, which is the property that made each the right choice for
// its language: a Go struct tag, a Python Annotated, and a TypeScript doc
// comment are all transparent to the value's type.

const sensitiveGoBlueprint = `package ubxawssqs

import sdk "github.com/ubiquex/ubx-sdk-go/runtime"

type Config struct {
	RepoName string
	APIToken string ` + "`ubx:\"sensitive\"`" + `
}

type Outputs struct {
	QueueUrl *sdk.Computed
}

func UbxAwsSqs(cfg Config) Outputs { return Outputs{} }
`

const sensitiveTSBlueprint = `import { Computed } from "@ubx/sdk";

export interface Config {
  repoName: string;
  /** @sensitive the token this deploys with */
  apiToken: string;
}

export interface Outputs {
  queueURL: Computed;
}

export function ubxAwsSqs(cfg: Config): Outputs {
  return { queueURL: null as unknown as Computed };
}
`

const sensitivePyBlueprint = `from dataclasses import dataclass
from typing import Annotated
import ubx_sdk as sdk


@dataclass
class Config:
    repo_name: str
    api_token: Annotated[str, "sensitive"]


def blueprint(c: Config):
    return None
`

// TestExtractGo_SensitiveStructTag: Go marks a param with a struct tag,
// which is the language's own metadata mechanism and leaves the field an
// ordinary string.
func TestExtractGo_SensitiveStructTag(t *testing.T) {
	s, err := ExtractGo(writeBlueprint(t, sensitiveGoBlueprint), "ubx-aws-sqs")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	assertSensitiveParams(t, "go", s)
}

func TestExtractTS_SensitiveJSDoc(t *testing.T) {
	requireDenoToolchain(t)
	s, err := ExtractTS(context.Background(), writeTSBlueprint(t, sensitiveTSBlueprint), "ubx-aws-sqs")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	assertSensitiveParams(t, "ts", s)
}

func TestExtractPy_SensitiveAnnotated(t *testing.T) {
	requirePython3Toolchain(t)
	s, err := ExtractPy(context.Background(), writePyBlueprint(t, sensitivePyBlueprint), "ubx-aws-sqs")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	assertSensitiveParams(t, "py", s)
}

func assertSensitiveParams(t *testing.T, lang string, s *Schema) {
	t.Helper()
	if s.SchemaVersion != SchemaVersion {
		t.Errorf("%s: schema_version = %d, want %d", lang, s.SchemaVersion, SchemaVersion)
	}
	byName := map[string]SchemaParam{}
	for _, p := range s.Params {
		byName[p.Name] = p
	}
	tok, ok := byName["api_token"]
	if !ok {
		t.Fatalf("%s: no api_token param in %+v", lang, s.Params)
	}
	if !tok.Sensitive {
		t.Errorf("%s: api_token was not marked sensitive", lang)
	}
	// The marker must be transparent: the param is still an ordinary
	// string, not some wrapper type the vocabulary had to learn.
	if tok.Type != "string" {
		t.Errorf("%s: api_token type = %q, want string: the marker must not change the declared type", lang, tok.Type)
	}
	if !tok.Required {
		t.Errorf("%s: api_token lost its required-ness", lang)
	}
	repo, ok := byName["repo_name"]
	if !ok {
		t.Fatalf("%s: no repo_name param", lang)
	}
	if repo.Sensitive {
		t.Errorf("%s: repo_name was marked sensitive without saying so", lang)
	}
}

// TestSchema_SensitiveIsIdenticalAcrossLanguages is what makes the
// cross-language guarantee real rather than asserted.
//
// A blueprint whose sensitivity depended on the language it was written
// in would be a trap dressed as a feature: the same logical blueprint,
// ported from Python to TypeScript, would silently stop protecting a
// credential, and nothing in either version would show the difference.
//
// So the rule is all three or none. If a language ever cannot express
// this, the honest response is to stop carrying sensitivity in the schema
// entirely rather than ship it for two out of three, and this test
// failing is what would say so.
func TestSchema_SensitiveIsIdenticalAcrossLanguages(t *testing.T) {
	requireDenoToolchain(t)
	requirePython3Toolchain(t)

	goSchema, err := ExtractGo(writeBlueprint(t, sensitiveGoBlueprint), "ubx-aws-sqs")
	if err != nil {
		t.Fatalf("go: %v", err)
	}
	tsSchema, err := ExtractTS(context.Background(), writeTSBlueprint(t, sensitiveTSBlueprint), "ubx-aws-sqs")
	if err != nil {
		t.Fatalf("ts: %v", err)
	}
	pySchema, err := ExtractPy(context.Background(), writePyBlueprint(t, sensitivePyBlueprint), "ubx-aws-sqs")
	if err != nil {
		t.Fatalf("py: %v", err)
	}

	// Compared on the language-neutral fields only. SourceName is
	// deliberately excluded: APIToken, apiToken and api_token are the
	// same parameter written in three languages, and requiring those to
	// match would be asserting that the languages are the same rather
	// than that the schemas are.
	type neutral struct {
		Name      string
		Type      string
		Required  bool
		Sensitive bool
	}
	project := func(s *Schema) []neutral {
		out := make([]neutral, 0, len(s.Params))
		for _, p := range s.Params {
			out = append(out, neutral{Name: p.Name, Type: string(p.Type), Required: p.Required, Sensitive: p.Sensitive})
		}
		return out
	}

	want := project(goSchema)
	if len(want) != 2 {
		t.Fatalf("fixture drift: expected 2 params, got %+v", want)
	}
	for _, other := range []struct {
		lang string
		s    *Schema
	}{{"ts", tsSchema}, {"py", pySchema}} {
		got := project(other.s)
		if len(got) != len(want) {
			t.Fatalf("%s has %d params, go has %d: %+v vs %+v", other.lang, len(got), len(want), got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s param %d differs from go:\n  go: %+v\n  %s: %+v\n"+
					"the same blueprint must protect the same credential in every language it is written in",
					other.lang, i, want[i], other.lang, got[i])
			}
		}
	}
}

// TestDescribe_SensitiveReachesEveryConsumer covers the join point.
//
// Both blueprint kinds converge on a Description, and consumers read that
// rather than a Schema. A code blueprint arrives via the derived schema;
// an Ubxfile blueprint never gets a derived schema at all and is read
// straight from its Ubxfile. The flag has to survive both routes or it
// protects only half the blueprints there are.
func TestDescribe_SensitiveReachesEveryConsumer(t *testing.T) {
	t.Run("code blueprint, via the derived schema", func(t *testing.T) {
		dir := writeBlueprint(t, sensitiveGoBlueprint)
		s, err := ExtractGo(dir, "ubx-aws-sqs")
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if err := WriteSchema(dir, s); err != nil {
			t.Fatalf("write schema: %v", err)
		}
		desc, err := Describe(dir)
		if err != nil {
			t.Fatalf("describe: %v", err)
		}
		assertDescribedSensitive(t, desc)
	})

	t.Run("Ubxfile blueprint, read straight from the Ubxfile", func(t *testing.T) {
		dir := writeUbxfile(t, "lang: go\nparams:\n  repo_name: string, required\n  api_token: string, required, sensitive\nresources: hello\n")
		desc, err := Describe(dir)
		if err != nil {
			t.Fatalf("describe: %v", err)
		}
		assertDescribedSensitive(t, desc)
	})
}

func assertDescribedSensitive(t *testing.T, desc *Description) {
	t.Helper()
	var seen bool
	for _, p := range desc.Params {
		switch p.Name {
		case "api_token":
			seen = true
			if !p.Sensitive {
				t.Error("api_token reached a consumer without its sensitivity")
			}
		case "repo_name":
			if p.Sensitive {
				t.Error("repo_name gained a sensitivity nobody declared")
			}
		}
	}
	if !seen {
		t.Fatalf("api_token is missing from %+v", desc.Params)
	}
}

// TestSchemaVersion_IsPinnedToALiteral exists because the obvious
// assertion is a tautology.
//
// Comparing a produced schema's version against the SchemaVersion
// constant compares the constant with itself: change the constant and
// both sides move, so the test passes while the wire format silently
// shifts under every consumer. Found by reverting the bump and watching
// the suite stay green.
//
// The version is a wire fact, so it is pinned to a number. Changing it
// must be a deliberate edit here, with whatever consumer story that
// implies, rather than a one-character change nothing notices.
//
// 2 is UBI-289's: it is when SchemaParam.Sensitive appeared, and it is
// the version an older ubx refuses. That refusal is the mechanism, not a
// side effect: a binary that cannot tell which parameters carry
// credentials has no safe way to record what a blueprint was called
// with.
func TestSchemaVersion_IsPinnedToALiteral(t *testing.T) {
	if SchemaVersion != 2 {
		t.Fatalf("SchemaVersion is %d, not 2 -- if that is deliberate, every consumer of blueprint.schema.json "+
			"has to be considered, since describe.go refuses a version it does not recognise", SchemaVersion)
	}
	s, err := ExtractGo(writeBlueprint(t, sensitiveGoBlueprint), "ubx-aws-sqs")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if s.SchemaVersion != 2 {
		t.Errorf("a freshly derived schema declares version %d, want 2", s.SchemaVersion)
	}
}
