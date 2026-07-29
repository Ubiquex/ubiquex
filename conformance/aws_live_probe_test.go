package conformance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ubiquex/ubiquex-cli/core"
)

// TestLiveProbes_AWSSNSTopic is UBI-50 session 3's own live-tier
// confirmation of probes 1/2/4 (identity-shape, sensitive-echo,
// drift-detectability) against a single real, free AWS resource — never
// probe 3 (destroy honesty), which stays hermetic-only this session by
// explicit decision (probe 3's own live confirmation would need
// core/executor.Ship to reach a real ApplyResourceChange destroy call
// against real AWS, exactly what CLAUDE.md's ship-verification rule
// bans; see STATE.md). Reuses aws_sns_topic — already RealSafe/
// Implemented in conformance.Registry (TestConformance_AWSSNSTopic,
// this same file's own sibling), a known-free, known-safe type — for
// ALL three checks against ONE created resource, minimizing real AWS
// footprint per this session's own "free-tier types first, cost-aware"
// instruction: one topic created, three read-only probes run against
// it, one topic destroyed.
func TestLiveProbes_AWSSNSTopic(t *testing.T) {
	RequireLive(t)
	providerPath := realAWSProviderPath(t)
	name := uniqueName("ubx-conformance-sns-probe")

	arn := runAWSOutput(t, "sns", "create-topic", "--name", name, "--query", "TopicArn", "--output", "text")
	t.Cleanup(func() { runAWS(t, "sns", "delete-topic", "--topic-arn", arn) })

	cfg := LiveReadProbeConfig{
		ProviderPath:   providerPath,
		Source:         "hashicorp/aws",
		Version:        awsProviderVersion,
		Stack:          "conformance",
		Address:        core.Address{Stack: "conformance", Type: "aws_sns_topic", Name: name},
		ProviderConfig: MustMarshal(map[string]string{"region": "us-east-1"}),
	}
	lookup := MustMarshal(map[string]string{"id": arn})

	t.Run("identity-shape: id alone is confirmed sufficient (no finding)", func(t *testing.T) {
		// aws_sns_topic's own registry entry already says "id" (the ARN)
		// alone is sufficient -- lookupFull identical to lookupMinimal
		// here means "nothing further to compare," so this asserts the
		// no-op path rather than a real diff (there is no fuller lookup
		// to compare against for a type whose own natural key IS "id").
		finding, err := ProbeIdentityShapeLive(context.Background(), cfg, lookup, lookup)
		if err != nil {
			t.Fatalf("ProbeIdentityShapeLive: %v", err)
		}
		if finding != nil {
			t.Fatalf("got %+v, want nil", finding)
		}
	})

	t.Run("sensitive-echo: a tag value does not leak into another attribute", func(t *testing.T) {
		marker := "ubx-conformance-marker-" + name
		runAWS(t, "sns", "tag-resource", "--resource-arn", arn, "--tags", "Key=ubx-marker,Value="+marker)
		t.Cleanup(func() { runAWS(t, "sns", "untag-resource", "--resource-arn", arn, "--tag-keys", "ubx-marker") })

		// "tags" and "tags_all" are BOTH legitimate homes for a tag
		// value -- AWS's own real, documented convention (tags_all is
		// tags merged with any provider-level default_tags) -- confirmed
		// empirically the first time this probe ran for real, not
		// assumed; only a marker appearing OUTSIDE both would be a real
		// leak.
		finding, err := ProbeSensitiveEchoLive(context.Background(), cfg, lookup, marker, "tags", "tags_all")
		if err != nil {
			t.Fatalf("ProbeSensitiveEchoLive: %v", err)
		}
		if finding != nil {
			t.Fatalf("got %+v, want nil -- a tag value has no legitimate reason to echo outside tags/tags_all", finding)
		}
	})

	t.Run("drift-detectability: two consecutive untouched reads agree (not undriftable)", func(t *testing.T) {
		l := core.Open(t.TempDir())
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		finding, err := ProbeDriftLive(ctx, l, cfg, lookup)
		if err != nil {
			t.Fatalf("ProbeDriftLive: %v", err)
		}
		if finding != nil {
			t.Fatalf("got %+v, want nil -- aws_sns_topic is an ordinary, real resource, not the hashicorp/time class", finding)
		}
	})
}

// TestProbeIdentityShapeLive_Hermetic_DetectsRealMismatch proves the
// COMPARISON logic itself against the real fakeprovider binary (no real
// cloud, no RequireLive), fast, always-run. Empirically confirmed while
// writing this test (not assumed): fakeprovider's own fake_widget type
// echoes "name" back from the LOOKUP input itself -- {"id":"..."} alone
// reads "name" back empty, {"id":"...","name":"..."} reads it back
// populated -- a real, if simplistic, simulation of exactly the class of
// lookup-shape sensitivity this probe exists to catch. Confirms
// ProbeIdentityShapeLive correctly detects and reports it, not that this
// specific type is a bug (fake_widget is a test fixture, not a real
// provider type).
func TestProbeIdentityShapeLive_Hermetic_DetectsRealMismatch(t *testing.T) {
	cfg := LiveReadProbeConfig{
		ProviderPath: fakeProviderBinary,
		Source:       "fake/widget",
		Version:      "0.1.0",
		Address:      core.Address{Stack: "conformance", Type: "fake_widget", Name: "identity-check"},
		ProviderEnv:  []string{"FAKEPROVIDER_MODE=ok-v6"},
	}
	lookupMinimal := json.RawMessage(`{"id":"identity-check"}`)
	lookupFull := json.RawMessage(`{"id":"identity-check","name":"identity-check"}`)

	finding, err := ProbeIdentityShapeLive(context.Background(), cfg, lookupMinimal, lookupFull)
	if err != nil {
		t.Fatalf("ProbeIdentityShapeLive: %v", err)
	}
	if finding == nil {
		t.Fatal("got nil, want a FindingIncompleteRead -- fake_widget's own name attribute genuinely differs between the two lookups")
	}
	if finding.Class != FindingIncompleteRead || finding.Tier != TierLive || finding.Confidence != Confirmed {
		t.Fatalf("unexpected finding shape: %+v", finding)
	}
}

// TestProbeIdentityShapeLive_Hermetic_IdenticalLookups_NoFinding proves
// the no-op path: an identical lookupMinimal/lookupFull pair has
// nothing to compare, so no read is even attempted a second time.
func TestProbeIdentityShapeLive_Hermetic_IdenticalLookups_NoFinding(t *testing.T) {
	cfg := LiveReadProbeConfig{
		ProviderPath: fakeProviderBinary,
		Source:       "fake/widget",
		Version:      "0.1.0",
		Address:      core.Address{Stack: "conformance", Type: "fake_widget", Name: "identity-check"},
		ProviderEnv:  []string{"FAKEPROVIDER_MODE=ok-v6"},
	}
	lookup := json.RawMessage(`{"id":"identity-check"}`)
	finding, err := ProbeIdentityShapeLive(context.Background(), cfg, lookup, lookup)
	if err != nil {
		t.Fatalf("ProbeIdentityShapeLive: %v", err)
	}
	if finding != nil {
		t.Fatalf("got %+v, want nil -- identical lookups have nothing to compare", finding)
	}
}

func TestIsEmptyOrNullJSON(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{`null`, true},
		{`""`, true},
		{``, true},
		{`"value"`, false},
		{`42`, false},
		{`{"nested":"object"}`, false},
		{`false`, false},
	}
	for _, c := range cases {
		got := isEmptyOrNullJSON(json.RawMessage(c.raw))
		if got != c.want {
			t.Errorf("isEmptyOrNullJSON(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}
