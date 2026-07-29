package discovery

import (
	"encoding/json"
	"errors"
	"testing"
)

// TestARN_StringRoundTrip guards Tier A's own correctness: BuildLookup
// reconstructs the ARN via a.String() rather than carrying the original
// string separately, so this round trip must be byte-exact.
func TestARN_StringRoundTrip(t *testing.T) {
	cases := []string{
		"arn:aws:sqs:us-east-1:839333509514:ubx-discovery-1785357707",
		"arn:aws:iam::839333509514:policy/my-policy",
		"arn:aws:s3:::my-bucket",
	}
	for _, want := range cases {
		a, err := ParseARN(want)
		if err != nil {
			t.Fatal(err)
		}
		if got := a.String(); got != want {
			t.Fatalf("round trip: ParseARN(%q).String() = %q", want, got)
		}
	}
}

func TestBuildLookup_TierA_IAMPolicy(t *testing.T) {
	a, err := ParseARN("arn:aws:iam::839333509514:policy/my-policy")
	if err != nil {
		t.Fatal(err)
	}
	spec, ok := ClassifyARN(a)
	if !ok || spec.TerraformType != "aws_iam_policy" || spec.Tier != TierA {
		t.Fatalf("ClassifyARN = %+v, %v", spec, ok)
	}
	lookup, err := BuildLookup(spec, a)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(lookup, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"id": "arn:aws:iam::839333509514:policy/my-policy"}
	if got["id"] != want["id"] || len(got) != 1 {
		t.Fatalf("lookup = %v, want %v", got, want)
	}
}

func TestBuildLookup_TierB(t *testing.T) {
	cases := []struct {
		name string
		arn  string
		want map[string]string
	}{
		{
			name: "aws_vpc -- id alone, the trailing vpc-* id",
			arn:  "arn:aws:ec2:us-east-1:839333509514:vpc/vpc-b75be9cd",
			want: map[string]string{"id": "vpc-b75be9cd"},
		},
		{
			name: "aws_iam_role -- id AND name both the trailing name segment",
			arn:  "arn:aws:iam::839333509514:role/my-role",
			want: map[string]string{"id": "my-role", "name": "my-role"},
		},
		{
			name: "aws_iam_user -- id AND name both the trailing name segment",
			arn:  "arn:aws:iam::839333509514:user/my-user",
			want: map[string]string{"id": "my-user", "name": "my-user"},
		},
		{
			name: "aws_s3_bucket -- id AND bucket both the bucket name",
			arn:  "arn:aws:s3:::my-bucket",
			want: map[string]string{"id": "my-bucket", "bucket": "my-bucket"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, err := ParseARN(c.arn)
			if err != nil {
				t.Fatal(err)
			}
			spec, ok := ClassifyARN(a)
			if !ok || spec.Tier != TierB {
				t.Fatalf("ClassifyARN(%q) = %+v, %v -- want ok, TierB", c.arn, spec, ok)
			}
			lookup, err := BuildLookup(spec, a)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]string
			if err := json.Unmarshal(lookup, &got); err != nil {
				t.Fatal(err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("lookup = %v, want %v", got, c.want)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Fatalf("lookup[%q] = %q, want %q (full: %v)", k, got[k], v, got)
				}
			}
		})
	}
}

// TestBuildLookup_TierC_SQSQueueURL is this arc's own live-confirmed
// worked example: an SQS ARN's own real lookup id is the queue URL, not
// the ARN, confirmed against a real, throwaway queue this session
// (docs/discovery.md).
func TestBuildLookup_TierC_SQSQueueURL(t *testing.T) {
	a, err := ParseARN("arn:aws:sqs:us-east-1:839333509514:ubx-discovery-1785357707")
	if err != nil {
		t.Fatal(err)
	}
	spec, ok := ClassifyARN(a)
	if !ok || spec.TerraformType != "aws_sqs_queue" || spec.Tier != TierC {
		t.Fatalf("ClassifyARN = %+v, %v", spec, ok)
	}
	lookup, err := BuildLookup(spec, a)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(lookup, &got); err != nil {
		t.Fatal(err)
	}
	want := "https://sqs.us-east-1.amazonaws.com/839333509514/ubx-discovery-1785357707"
	if got["id"] != want {
		t.Fatalf("lookup id = %q, want %q (the real queue URL confirmed live this session)", got["id"], want)
	}
}

// TestClassifyARN_Unclassified_NotYetAdoptable is adversarial row 1: a
// type with no known tier surfaces as ErrNotYetAdoptable, never a
// fabricated lookup.
func TestClassifyARN_Unclassified_NotYetAdoptable(t *testing.T) {
	a, err := ParseARN("arn:aws:dynamodb:us-east-1:839333509514:table/my-table")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ClassifyARN(a); ok {
		t.Fatal("expected ClassifyARN to report unclassified for a type not in tierTable")
	}
}

func TestBuildLookup_TierC_ConstructorMissing(t *testing.T) {
	a, err := ParseARN("arn:aws:sqs:us-east-1:839333509514:q")
	if err != nil {
		t.Fatal(err)
	}
	spec := typeSpec{TerraformType: "aws_sqs_queue", Tier: TierC} // Construct deliberately nil
	_, err = BuildLookup(spec, a)
	if !errors.Is(err, ErrNotYetAdoptable) {
		t.Fatalf("expected ErrNotYetAdoptable for a Tier C spec with no constructor, got %v", err)
	}
}
