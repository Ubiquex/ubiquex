package discovery

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
)

// fakeTaggingAPI is a hermetic, fully scripted stand-in for the real AWS
// Resource Groups Tagging API -- pages is consumed one GetResources call
// at a time, in order, so a test can script real pagination (adversarial
// row 2) without needing thousands of real tagged resources.
type fakeTaggingAPI struct {
	pages     [][]types.ResourceTagMapping
	call      int
	lastInput *resourcegroupstaggingapi.GetResourcesInput
	err       error
}

func (f *fakeTaggingAPI) GetResources(_ context.Context, params *resourcegroupstaggingapi.GetResourcesInput, _ ...func(*resourcegroupstaggingapi.Options)) (*resourcegroupstaggingapi.GetResourcesOutput, error) {
	f.lastInput = params
	if f.err != nil {
		return nil, f.err
	}
	if f.call >= len(f.pages) {
		return &resourcegroupstaggingapi.GetResourcesOutput{}, nil
	}
	page := f.pages[f.call]
	f.call++
	out := &resourcegroupstaggingapi.GetResourcesOutput{ResourceTagMappingList: page}
	if f.call < len(f.pages) {
		out.PaginationToken = aws.String("next-page-token")
	}
	return out, nil
}

func mapping(arn string, tags map[string]string) types.ResourceTagMapping {
	var ts []types.Tag
	for k, v := range tags {
		ts = append(ts, types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return types.ResourceTagMapping{ResourceARN: aws.String(arn), Tags: ts}
}

// TestDiscover_Pagination_FollowedToCompletion is adversarial row 2's
// own pagination half: a scripted 3-page response, confirming Discover
// follows PaginationToken through every page before returning, never
// stopping at the first.
func TestDiscover_Pagination_FollowedToCompletion(t *testing.T) {
	api := &fakeTaggingAPI{pages: [][]types.ResourceTagMapping{
		{mapping("arn:aws:sqs:us-east-1:1:q1", nil)},
		{mapping("arn:aws:sqs:us-east-1:1:q2", nil)},
		{mapping("arn:aws:sqs:us-east-1:1:q3", nil)},
	}}
	got, err := Discover(context.Background(), api, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d resources, want 3 (all pages followed)", len(got))
	}
	if api.call != 3 {
		t.Fatalf("GetResources called %d times, want 3 (one per page)", api.call)
	}
}

// TestCheckLimit_ConfirmationGate is adversarial row 2's own gate half:
// exceeding the limit without --yes refuses; --yes or staying under the
// limit proceeds.
func TestCheckLimit_ConfirmationGate(t *testing.T) {
	if err := CheckLimit(150, 100, false); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("expected ErrConfirmationRequired for 150 > limit 100, got %v", err)
	}
	if err := CheckLimit(150, 100, true); err != nil {
		t.Fatalf("expected --yes (confirmed=true) to proceed regardless of count, got %v", err)
	}
	if err := CheckLimit(50, 100, false); err != nil {
		t.Fatalf("expected under-limit to proceed without confirmation, got %v", err)
	}
	if err := CheckLimit(150, 0, false); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("expected limit<=0 to fall back to DefaultLimit (%d), got %v", DefaultLimit, err)
	}
}

// TestDiscover_NoLookupShape_NeverSilentlySkipped is adversarial row 1:
// a discovered ARN with no tier-table entry still appears in the
// returned slice, marked not-adoptable, never dropped.
func TestDiscover_NoLookupShape_NeverSilentlySkipped(t *testing.T) {
	api := &fakeTaggingAPI{pages: [][]types.ResourceTagMapping{
		{
			mapping("arn:aws:sqs:us-east-1:1:known-queue", map[string]string{"env": "prod"}),
			mapping("arn:aws:dynamodb:us-east-1:1:table/unclassified-table", map[string]string{"env": "prod"}),
		},
	}}
	got, err := Discover(context.Background(), api, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected both resources present (one adoptable, one not), got %d", len(got))
	}
	var sawUnclassified bool
	for _, d := range got {
		if d.ARN == "arn:aws:dynamodb:us-east-1:1:table/unclassified-table" {
			sawUnclassified = true
			if d.Lookup != nil {
				t.Fatalf("expected nil Lookup for an unclassified type, got %s", d.Lookup)
			}
			if d.NotAdoptableReason == "" {
				t.Fatal("expected a non-empty NotAdoptableReason")
			}
			if d.Tags["env"] != "prod" {
				t.Fatalf("expected tags to still be carried through even when not adoptable, got %v", d.Tags)
			}
		}
	}
	if !sawUnclassified {
		t.Fatal("the unclassified resource must still appear in the results, never silently dropped")
	}
}

// TestDiscover_CreationVerbsCarriedThrough confirms the genesis-
// attribution bonus's own per-type knowledge rides along with every
// classified, adoptable resource (never on an unclassified one).
func TestDiscover_CreationVerbsCarriedThrough(t *testing.T) {
	api := &fakeTaggingAPI{pages: [][]types.ResourceTagMapping{
		{
			mapping("arn:aws:sqs:us-east-1:1:my-queue", nil),
			mapping("arn:aws:dynamodb:us-east-1:1:table/unclassified", nil),
		},
	}}
	got, err := Discover(context.Background(), api, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range got {
		switch d.ARN {
		case "arn:aws:sqs:us-east-1:1:my-queue":
			if len(d.CreationVerbs) != 1 || d.CreationVerbs[0] != "CreateQueue" {
				t.Fatalf("expected aws_sqs_queue's own CreationVerbs [CreateQueue], got %v", d.CreationVerbs)
			}
		case "arn:aws:dynamodb:us-east-1:1:table/unclassified":
			if len(d.CreationVerbs) != 0 {
				t.Fatalf("expected no CreationVerbs for an unclassified type, got %v", d.CreationVerbs)
			}
		}
	}
}

func TestDiscover_TypeAllowlist_ClientSideFiltering(t *testing.T) {
	api := &fakeTaggingAPI{pages: [][]types.ResourceTagMapping{
		{
			mapping("arn:aws:sqs:us-east-1:1:my-queue", nil),
			mapping("arn:aws:iam::1:role/my-role", nil),
		},
	}}
	got, err := Discover(context.Background(), api, Options{Types: []string{"aws_sqs_queue"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].TerraformType != "aws_sqs_queue" {
		t.Fatalf("expected --type to exclude the non-matching iam:role resource entirely, got %+v", got)
	}
}

func TestDiscover_TypeAllowlist_UnknownType_Errors(t *testing.T) {
	api := &fakeTaggingAPI{}
	_, err := Discover(context.Background(), api, Options{Types: []string{"aws_dynamodb_table"}})
	if !errors.Is(err, ErrUnknownDiscoveryType) {
		t.Fatalf("expected ErrUnknownDiscoveryType for a --type discovery has no tier for, got %v", err)
	}
}

func TestDiscover_TagFilters_KeyValueMapping(t *testing.T) {
	api := &fakeTaggingAPI{pages: [][]types.ResourceTagMapping{{}}}
	_, err := Discover(context.Background(), api, Options{Tags: map[string][]string{
		"Project": {"payments"},
		"Env":     {"prod", "staging"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(api.lastInput.TagFilters) != 2 {
		t.Fatalf("expected 2 tag filters (one per key), got %d: %+v", len(api.lastInput.TagFilters), api.lastInput.TagFilters)
	}
	for _, f := range api.lastInput.TagFilters {
		switch aws.ToString(f.Key) {
		case "Project":
			if len(f.Values) != 1 || f.Values[0] != "payments" {
				t.Fatalf("Project filter values = %v, want [payments]", f.Values)
			}
		case "Env":
			if len(f.Values) != 2 {
				t.Fatalf("Env filter values = %v, want 2 values (OR-combined within key)", f.Values)
			}
		default:
			t.Fatalf("unexpected filter key %q", aws.ToString(f.Key))
		}
	}
}

// TestDiscover_TaggingAPIError_PermissionDenied confirms a whole-call
// tagging-API failure (e.g. AccessDeniedException) surfaces as a plain,
// wrapped error -- the caller (cli/scandiscover.go) decides how this
// maps to the CLI's own exit-code contract; Discover itself never
// swallows it.
func TestDiscover_TaggingAPIError_PermissionDenied(t *testing.T) {
	api := &fakeTaggingAPI{err: errors.New("AccessDeniedException: not authorized to perform tag:GetResources")}
	_, err := Discover(context.Background(), api, Options{})
	if err == nil {
		t.Fatal("expected an error")
	}
}

// TestDiscover_MalformedARN_SurfacesAsNotAdoptable is a real, honest
// defensive case: if the tagging API ever returned something that
// somehow isn't a well-formed ARN, that's reported, not silently
// dropped or a fatal error for the whole batch.
func TestDiscover_MalformedARN_SurfacesAsNotAdoptable(t *testing.T) {
	api := &fakeTaggingAPI{pages: [][]types.ResourceTagMapping{
		{mapping("not-a-real-arn", nil)},
	}}
	got, err := Discover(context.Background(), api, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].NotAdoptableReason == "" {
		t.Fatalf("expected the malformed ARN to surface as its own not-adoptable entry, got %+v", got)
	}
}
