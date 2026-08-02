package provider

import (
	"encoding/json"
	"testing"
)

// awsIAMRoleLikeBlock, awsECRRepositoryLikeBlock, awsSQSQueueLikeBlock
// model the exact real-schema shapes UBI-66's own live repro was found
// against (a subset of each real type's attributes, enough to prove the
// point -- not a full schema dump).
func awsIAMRoleLikeBlock() Block {
	return Block{Attributes: []Attribute{
		{Name: "id", Type: json.RawMessage(`"string"`), Computed: true},
		{Name: "name", Type: json.RawMessage(`"string"`), Required: true},
		{Name: "assume_role_policy", Type: json.RawMessage(`"string"`), Required: true},
		{Name: "arn", Type: json.RawMessage(`"string"`), Computed: true},
	}}
}

func awsECRRepositoryLikeBlock() Block {
	return Block{Attributes: []Attribute{
		{Name: "id", Type: json.RawMessage(`"string"`), Computed: true},
		{Name: "name", Type: json.RawMessage(`"string"`), Required: true},
		{Name: "arn", Type: json.RawMessage(`"string"`), Computed: true},
	}}
}

func awsSQSQueueLikeBlock() Block {
	return Block{Attributes: []Attribute{
		{Name: "id", Type: json.RawMessage(`"string"`), Computed: true},
		{Name: "name", Type: json.RawMessage(`"string"`), Required: true},
		{Name: "arn", Type: json.RawMessage(`"string"`), Computed: true},
	}}
}

// TestUnknownConfigKeys_RealLiveRepro is UBI-66's own regression test:
// the exact four hallucinated keys a real live run (Claude Haiku)
// produced -- none of them real, all plausible-sounding -- each must be
// caught, each with the exact real attribute name suggested.
func TestUnknownConfigKeys_RealLiveRepro(t *testing.T) {
	cases := []struct {
		name    string
		block   Block
		config  map[string]interface{}
		wantKey string
		wantSug string
	}{
		{"ecr repository_name->name", awsECRRepositoryLikeBlock(), map[string]interface{}{"repository_name": "my-repo"}, "repository_name", "name"},
		{"iam role_name->name", awsIAMRoleLikeBlock(), map[string]interface{}{"role_name": "ci-runner"}, "role_name", "name"},
		{"iam assume_role_policy_document->assume_role_policy", awsIAMRoleLikeBlock(), map[string]interface{}{"assume_role_policy_document": "{}"}, "assume_role_policy_document", "assume_role_policy"},
		{"sqs queue_name->name", awsSQSQueueLikeBlock(), map[string]interface{}{"queue_name": "ci-notifications"}, "queue_name", "name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			issues := UnknownConfigKeys(c.block, c.config)
			if len(issues) != 1 {
				t.Fatalf("issues = %+v, want exactly 1", issues)
			}
			if issues[0].Key != c.wantKey || issues[0].Path != c.wantKey {
				t.Errorf("issue key/path = %q/%q, want %q/%q", issues[0].Key, issues[0].Path, c.wantKey, c.wantKey)
			}
			if issues[0].Suggestion != c.wantSug {
				t.Errorf("suggestion = %q, want %q", issues[0].Suggestion, c.wantSug)
			}
		})
	}
}

// TestUnknownConfigKeys_ResourceWithThreeWrongKeys is UBI-66's own
// hermetic acceptance criterion, verbatim: "a drafted resource with 3
// wrong keys refused with 3 distinct teaching errors."
func TestUnknownConfigKeys_ResourceWithThreeWrongKeys(t *testing.T) {
	block := awsIAMRoleLikeBlock()
	config := map[string]interface{}{
		"role_name":                   "ci-runner",
		"assume_role_policy_document": "{}",
		"role_arn":                    "arn:aws:iam::123:role/ci-runner",
	}
	issues := UnknownConfigKeys(block, config)
	if len(issues) != 3 {
		t.Fatalf("issues = %+v, want exactly 3", issues)
	}
	byKey := make(map[string]ConfigKeyIssue, len(issues))
	for _, iss := range issues {
		byKey[iss.Key] = iss
	}
	if byKey["role_name"].Suggestion != "name" {
		t.Errorf("role_name suggestion = %q, want %q", byKey["role_name"].Suggestion, "name")
	}
	if byKey["assume_role_policy_document"].Suggestion != "assume_role_policy" {
		t.Errorf("assume_role_policy_document suggestion = %q, want %q", byKey["assume_role_policy_document"].Suggestion, "assume_role_policy")
	}
	if byKey["role_arn"].Suggestion != "arn" {
		t.Errorf("role_arn suggestion = %q, want %q", byKey["role_arn"].Suggestion, "arn")
	}
}

// TestUnknownConfigKeys_Deterministic proves issues come back in a fixed
// (sorted-by-key) order across repeat calls -- load-bearing for
// core.DoubleRun, which resolves everything twice and hard-fails on any
// byte divergence between the two runs.
func TestUnknownConfigKeys_Deterministic(t *testing.T) {
	block := awsIAMRoleLikeBlock()
	config := map[string]interface{}{
		"role_name":                   "ci-runner",
		"assume_role_policy_document": "{}",
		"role_arn":                    "arn:aws:iam::123:role/ci-runner",
	}
	first := UnknownConfigKeys(block, config)
	for i := 0; i < 10; i++ {
		got := UnknownConfigKeys(block, config)
		if len(got) != len(first) {
			t.Fatalf("run %d: len = %d, want %d", i, len(got), len(first))
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("run %d: issue[%d] = %+v, want %+v", i, j, got[j], first[j])
			}
		}
	}
	// Sorted by key: "assume_role_policy_document" < "role_arn" < "role_name".
	want := []string{"assume_role_policy_document", "role_arn", "role_name"}
	for i, w := range want {
		if first[i].Key != w {
			t.Fatalf("issue[%d].Key = %q, want %q (order not stable)", i, first[i].Key, w)
		}
	}
}

// TestUnknownConfigKeys_NoCloseMatch proves a genuinely unrelated bad key
// gets no suggestion at all -- a wrong suggestion is worse than none.
func TestUnknownConfigKeys_NoCloseMatch(t *testing.T) {
	block := awsIAMRoleLikeBlock()
	issues := UnknownConfigKeys(block, map[string]interface{}{"totally_unrelated_xyz": "value"})
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want exactly 1", issues)
	}
	if issues[0].Suggestion != "" {
		t.Errorf("suggestion = %q, want none", issues[0].Suggestion)
	}
}

// TestUnknownConfigKeys_RecognizedKeysProduceNoIssues is the positive
// twin: a config using only real keys reports nothing, even when it
// happens to share substrings with other real keys (name/arn/id all
// exist on the same schema here).
func TestUnknownConfigKeys_RecognizedKeysProduceNoIssues(t *testing.T) {
	block := awsIAMRoleLikeBlock()
	issues := UnknownConfigKeys(block, map[string]interface{}{
		"name":               "ci-runner",
		"assume_role_policy": "{}",
	})
	if len(issues) != 0 {
		t.Fatalf("issues = %+v, want none", issues)
	}
}

// TestUnknownConfigKeys_NestedBlock_PathPrefixed proves a bad key one
// level inside a NestedBlock is found too, with its own path prefixed by
// the block's own name -- not just top-level attributes.
func TestUnknownConfigKeys_NestedBlock_PathPrefixed(t *testing.T) {
	block := Block{
		Attributes: []Attribute{{Name: "name", Type: json.RawMessage(`"string"`), Required: true}},
		NestedBlocks: []NestedBlock{
			{TypeName: "image_scanning_configuration", Nesting: NestingList, MaxItems: 1, Block: Block{
				Attributes: []Attribute{{Name: "scan_on_push", Type: json.RawMessage(`"bool"`), Required: true}},
			}},
		},
	}
	// Bare-object shorthand (UBI-63 bug 2's own convention) with a wrong
	// key inside it.
	config := map[string]interface{}{
		"name": "my-repo",
		"image_scanning_configuration": map[string]interface{}{
			"scan_push": true,
		},
	}
	issues := UnknownConfigKeys(block, config)
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want exactly 1", issues)
	}
	if issues[0].Path != "image_scanning_configuration.scan_push" {
		t.Errorf("path = %q, want %q", issues[0].Path, "image_scanning_configuration.scan_push")
	}
	if issues[0].Suggestion != "scan_on_push" {
		t.Errorf("suggestion = %q, want %q", issues[0].Suggestion, "scan_on_push")
	}
}

// TestUnknownConfigKeys_NestedBlock_ArrayShape proves the array (not
// bare-object) shape for a List/Set-nested block is walked per element
// too.
func TestUnknownConfigKeys_NestedBlock_ArrayShape(t *testing.T) {
	block := Block{
		NestedBlocks: []NestedBlock{
			{TypeName: "rule", Nesting: NestingSet, Block: Block{
				Attributes: []Attribute{{Name: "action", Type: json.RawMessage(`"string"`), Required: true}},
			}},
		},
	}
	config := map[string]interface{}{
		"rule": []interface{}{
			map[string]interface{}{"action": "allow"},
			map[string]interface{}{"actions": "deny"},
		},
	}
	issues := UnknownConfigKeys(block, config)
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want exactly 1", issues)
	}
	if issues[0].Path != "rule.actions" || issues[0].Suggestion != "action" {
		t.Errorf("issue = %+v, want path %q suggestion %q", issues[0], "rule.actions", "action")
	}
}

// TestUnknownConfigKeys_MapAttributeNotRecursedInto proves a map-typed
// Attribute's own keys (e.g. "tags") are never schema-checked -- those
// are caller-chosen names, not part of the schema, unlike a NestedBlock's
// own fixed attribute set.
func TestUnknownConfigKeys_MapAttributeNotRecursedInto(t *testing.T) {
	block := Block{Attributes: []Attribute{
		{Name: "tags", Type: json.RawMessage(`["map","string"]`), Optional: true},
	}}
	config := map[string]interface{}{
		"tags": map[string]interface{}{"AnyKeyAtAll": "value", "totally_unrelated_xyz": "value"},
	}
	issues := UnknownConfigKeys(block, config)
	if len(issues) != 0 {
		t.Fatalf("issues = %+v, want none -- map attribute values are never schema-checked", issues)
	}
}

func TestUnknownConfigKeys_NilOrNonObjectConfig(t *testing.T) {
	block := awsIAMRoleLikeBlock()
	if issues := UnknownConfigKeys(block, nil); len(issues) != 0 {
		t.Errorf("nil config: issues = %+v, want none", issues)
	}
}

// TestMissingRequiredKeys_RealLiveRepro is UBI-90's own regression test,
// UnknownConfigKeys_RealLiveRepro's mirror image: aws_iam_role/
// aws_ecr_repository, each missing the exact Required attribute the
// founder's own live playground-13 incident found absent.
func TestMissingRequiredKeys_RealLiveRepro(t *testing.T) {
	cases := []struct {
		name     string
		block    Block
		config   map[string]interface{}
		wantPath string
	}{
		{"iam role missing assume_role_policy", awsIAMRoleLikeBlock(), map[string]interface{}{"name": "ci-runner"}, "assume_role_policy"},
		{"ecr repository missing name", awsECRRepositoryLikeBlock(), map[string]interface{}{}, "name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			issues := MissingRequiredKeys(c.block, c.config)
			if len(issues) != 1 {
				t.Fatalf("issues = %+v, want exactly 1", issues)
			}
			if issues[0].Key != c.wantPath || issues[0].Path != c.wantPath {
				t.Errorf("issue key/path = %q/%q, want %q/%q", issues[0].Key, issues[0].Path, c.wantPath, c.wantPath)
			}
		})
	}
}

// TestMissingRequiredKeys_MultipleMissingOnOneResource mirrors
// TestUnknownConfigKeys_ResourceWithThreeWrongKeys: every Required
// attribute genuinely absent is reported, not just the first.
func TestMissingRequiredKeys_MultipleMissingOnOneResource(t *testing.T) {
	block := awsIAMRoleLikeBlock() // name + assume_role_policy both Required
	issues := MissingRequiredKeys(block, map[string]interface{}{})
	if len(issues) != 2 {
		t.Fatalf("issues = %+v, want exactly 2", issues)
	}
	byKey := make(map[string]bool, len(issues))
	for _, iss := range issues {
		byKey[iss.Key] = true
	}
	if !byKey["name"] || !byKey["assume_role_policy"] {
		t.Errorf("issues = %+v, want both name and assume_role_policy", issues)
	}
}

// TestMissingRequiredKeys_ComputedAndOptionalNeverFlagged proves only
// Required attributes are ever checked -- a Computed attribute (never
// something a caller sets) and an absent Optional one are both silent.
func TestMissingRequiredKeys_ComputedAndOptionalNeverFlagged(t *testing.T) {
	block := Block{Attributes: []Attribute{
		{Name: "id", Type: json.RawMessage(`"string"`), Computed: true},
		{Name: "description", Type: json.RawMessage(`"string"`), Optional: true},
		{Name: "name", Type: json.RawMessage(`"string"`), Required: true},
	}}
	issues := MissingRequiredKeys(block, map[string]interface{}{"name": "my-repo"})
	if len(issues) != 0 {
		t.Fatalf("issues = %+v, want none -- id/description are never Required", issues)
	}
}

// TestMissingRequiredKeys_RecognizedKeysProduceNoIssues is the positive
// twin: every Required attribute present reports nothing.
func TestMissingRequiredKeys_RecognizedKeysProduceNoIssues(t *testing.T) {
	block := awsIAMRoleLikeBlock()
	issues := MissingRequiredKeys(block, map[string]interface{}{
		"name":               "ci-runner",
		"assume_role_policy": "{}",
	})
	if len(issues) != 0 {
		t.Fatalf("issues = %+v, want none", issues)
	}
}

// TestMissingRequiredKeys_PresentButNull proves an explicit JSON null
// still counts as "present" -- this check only ever asks "does the key
// exist at all," the identical `!present` question encodeBlockValue's own
// Required check asks at encode time; a key present with a null value is
// a genuinely different, already-existing gap (ctyvalue.go's own encode
// path sends it through unchanged, matching `case !present:` never firing
// for it either) -- not something this presence-only check expands scope
// to also catch.
func TestMissingRequiredKeys_PresentButNull(t *testing.T) {
	block := awsIAMRoleLikeBlock()
	issues := MissingRequiredKeys(block, map[string]interface{}{
		"name":               "ci-runner",
		"assume_role_policy": nil,
	})
	if len(issues) != 0 {
		t.Fatalf("issues = %+v, want none -- an explicit null is present, not missing", issues)
	}
}

// TestMissingRequiredKeys_NestedBlock_PathPrefixed mirrors
// TestUnknownConfigKeys_NestedBlock_PathPrefixed: a Required attribute
// missing one level inside a PRESENT nested block is found too, path
// prefixed by the block's own name.
func TestMissingRequiredKeys_NestedBlock_PathPrefixed(t *testing.T) {
	block := Block{
		Attributes: []Attribute{{Name: "name", Type: json.RawMessage(`"string"`), Required: true}},
		NestedBlocks: []NestedBlock{
			{TypeName: "image_scanning_configuration", Nesting: NestingList, MaxItems: 1, Block: Block{
				Attributes: []Attribute{{Name: "scan_on_push", Type: json.RawMessage(`"bool"`), Required: true}},
			}},
		},
	}
	// Bare-object shorthand, present but missing its own Required attribute.
	config := map[string]interface{}{
		"name":                         "my-repo",
		"image_scanning_configuration": map[string]interface{}{},
	}
	issues := MissingRequiredKeys(block, config)
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want exactly 1", issues)
	}
	if issues[0].Path != "image_scanning_configuration.scan_on_push" {
		t.Errorf("path = %q, want %q", issues[0].Path, "image_scanning_configuration.scan_on_push")
	}
}

// TestMissingRequiredKeys_AbsentNestedBlockNeverFlagged proves an ABSENT
// nested block is never itself flagged -- mirrors encodeNestedBlockValue's
// own leniency (an absent List/Set block encodes as an empty collection,
// no error; NestedBlock has no MinItems modeled in this codebase at all).
func TestMissingRequiredKeys_AbsentNestedBlockNeverFlagged(t *testing.T) {
	block := Block{
		Attributes: []Attribute{{Name: "name", Type: json.RawMessage(`"string"`), Required: true}},
		NestedBlocks: []NestedBlock{
			{TypeName: "image_scanning_configuration", Nesting: NestingList, MaxItems: 1, Block: Block{
				Attributes: []Attribute{{Name: "scan_on_push", Type: json.RawMessage(`"bool"`), Required: true}},
			}},
		},
	}
	issues := MissingRequiredKeys(block, map[string]interface{}{"name": "my-repo"})
	if len(issues) != 0 {
		t.Fatalf("issues = %+v, want none -- an absent nested block is never itself Required", issues)
	}
}

// TestMissingRequiredKeys_NestedBlock_ArrayShape mirrors
// TestUnknownConfigKeys_NestedBlock_ArrayShape: the array (not bare-object)
// shape for a List/Set-nested block is walked per element too.
func TestMissingRequiredKeys_NestedBlock_ArrayShape(t *testing.T) {
	block := Block{
		NestedBlocks: []NestedBlock{
			{TypeName: "rule", Nesting: NestingSet, Block: Block{
				Attributes: []Attribute{{Name: "action", Type: json.RawMessage(`"string"`), Required: true}},
			}},
		},
	}
	config := map[string]interface{}{
		"rule": []interface{}{
			map[string]interface{}{"action": "allow"},
			map[string]interface{}{},
		},
	}
	issues := MissingRequiredKeys(block, config)
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want exactly 1", issues)
	}
	if issues[0].Path != "rule.action" {
		t.Errorf("path = %q, want %q", issues[0].Path, "rule.action")
	}
}

// TestMissingRequiredKeys_NilOrNonObjectConfig is a DELIBERATE asymmetry
// from TestUnknownConfigKeys_NilOrNonObjectConfig, not an inconsistency:
// UnknownConfigKeys asks "are there any BAD keys" (a nil config trivially
// has none); MissingRequiredKeys asks "are all GOOD keys present" (a nil
// config is missing every one of them, genuinely). The higher-level
// adapter (cli/schemainspector.go's schemaInspectorAdapter) still
// short-circuits a nil config to "no issues" before ever calling this,
// mirroring UnknownConfigKeys' own adapter-level guard -- that's a
// deliberate "a different, already-existing check catches this shape
// mismatch instead" boundary, not this function's own concern; this test
// covers the raw building block's own honest behavior in isolation.
func TestMissingRequiredKeys_NilOrNonObjectConfig(t *testing.T) {
	block := awsIAMRoleLikeBlock()
	if issues := MissingRequiredKeys(block, nil); len(issues) != 2 {
		t.Errorf("nil config: issues = %+v, want 2 (both name and assume_role_policy genuinely absent)", issues)
	}
}

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"kitten", "sitting", 3},
		{"assume_role_policy", "asssume_role_policy", 1},
		{"name", "name", 0},
	}
	for _, c := range cases {
		if got := levenshtein(c.a, c.b); got != c.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestClosestKey_TypoFallback proves a genuine same-length-ish typo (no
// candidate has a substring relationship) is still caught, via the
// edit-distance fallback tier.
func TestClosestKey_TypoFallback(t *testing.T) {
	got := closestKey("asssume_role_policy", []string{"name", "assume_role_policy", "arn"})
	if got != "assume_role_policy" {
		t.Errorf("closestKey = %q, want %q", got, "assume_role_policy")
	}
}

// TestClosestKey_SubstringBeatsSimilarLengthTypo is the exact failure
// mode a naive blended edit-distance score would fall into: a same-
// length, similar-prefix candidate ("repository_url") must NOT outrank a
// genuine substring match ("name") just because it happens to be a
// smaller raw edit distance from a long key.
func TestClosestKey_SubstringBeatsSimilarLengthTypo(t *testing.T) {
	got := closestKey("repository_name", []string{"name", "repository_url", "arn", "id"})
	if got != "name" {
		t.Errorf("closestKey = %q, want %q", got, "name")
	}
}
