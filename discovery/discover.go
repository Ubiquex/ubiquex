package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
)

// TaggingAPI is the minimal surface Discover needs from AWS's Resource
// Groups Tagging API — deliberately shaped to match
// *resourcegroupstaggingapi.Client's own GetResources method signature
// exactly (same argument/return types), so the real SDK client already
// satisfies this interface with zero adapter code, the identical
// dependency-inversion discipline core.StateReader/core.EventLookup
// already establish for the tfplugin provider client and CloudTrail.
// A hermetic fake implements the same interface to script multi-page
// responses for the adversarial program's own pagination row — see
// discover_test.go.
type TaggingAPI interface {
	GetResources(ctx context.Context, params *resourcegroupstaggingapi.GetResourcesInput, optFns ...func(*resourcegroupstaggingapi.Options)) (*resourcegroupstaggingapi.GetResourcesOutput, error)
}

// DefaultLimit is the confirmation-gate threshold CheckLimit applies
// when Options.Limit is left at zero — matching --confirm-destroys' own
// "friction by default, not silently unbounded" posture for a different
// risk (docs/discovery.md's adversarial row 2).
const DefaultLimit = 100

// DiscoveredResource is one enumerated, tag-matched resource — identity
// only, exactly like a tfstate --all entry (docs/architecture.md's
// "state provides identity, never truth"): nothing here is the
// resource's actual current attributes, which only ever come from a
// real core.RunScan/ReadResource call downstream.
type DiscoveredResource struct {
	ARN           string
	TerraformType string            // "" if unclassified
	Tags          map[string]string // every real tag on the resource, from the tagging API's own response -- not just the ones matched by --tag
	Lookup        json.RawMessage   // nil if NotAdoptableReason is set

	// NotAdoptableReason is non-empty exactly when Lookup is nil —
	// docs/discovery.md's own "discovered, not yet adoptable" gap
	// reporting: this resource is always present in Discover's own
	// returned slice, never silently dropped, whether or not it could
	// be bridged to a lookup.
	NotAdoptableReason string
}

// Options is one Discover call's own scope.
type Options struct {
	// Tags is the primary scoping mechanism (docs/discovery.md's own
	// "tag-scoped filtering as the primary UX") — key to allowed-values;
	// an empty/nil Values slice for a key matches any value for that key
	// (the tagging API's own "no value given" semantics). Multiple keys
	// AND-combine (a resource must match every key); multiple values for
	// the same key OR-combine.
	Tags map[string][]string

	// Types is a --type allowlist of Terraform type names, client-side
	// (docs/discovery.md's own "one ARN-parsing pass answers both which
	// tier and does this match --type"). Empty means no restriction —
	// every tag-matched ARN is classified and reported, adoptable or not.
	Types []string
}

// Discover enumerates every resource matching opts.Tags via the AWS
// Resource Groups Tagging API, paginating to completion before
// returning anything (docs/discovery.md's own adversarial row 2: the
// full, real count must be known before any confirmation gate or
// per-resource read happens) — never calls ReadResource itself; that
// stays core.RunScan's own, completely unchanged job (see
// cli/scandiscover.go).
func Discover(ctx context.Context, api TaggingAPI, opts Options) ([]DiscoveredResource, error) {
	wantedClassKeys, err := classKeysFor(opts.Types)
	if err != nil {
		return nil, err
	}

	var results []DiscoveredResource
	var paginationToken *string
	for {
		out, err := api.GetResources(ctx, &resourcegroupstaggingapi.GetResourcesInput{
			TagFilters:      tagFiltersFor(opts.Tags),
			PaginationToken: paginationToken,
		})
		if err != nil {
			return nil, fmt.Errorf("discover: tagging API: %w", err)
		}

		for _, m := range out.ResourceTagMappingList {
			arnString := aws.ToString(m.ResourceARN)
			a, err := ParseARN(arnString)
			if err != nil {
				// A tagging-API result that isn't even a well-formed ARN
				// is a genuine anomaly, not a classification gap -- named
				// as its own distinct not-adoptable reason rather than
				// silently dropped or conflated with "no known lookup
				// shape."
				results = append(results, DiscoveredResource{
					ARN: arnString, Tags: tagsToMap(m.Tags),
					NotAdoptableReason: fmt.Sprintf("malformed ARN returned by the tagging API: %v", err),
				})
				continue
			}
			if wantedClassKeys != nil && !wantedClassKeys[a.classKey()] {
				// Not one of the requested --type's own ARN shapes --
				// a different resource that happens to share the tag,
				// never reported (it wasn't asked for at all, distinct
				// from "asked for and couldn't bridge").
				continue
			}

			results = append(results, classify(a, m))
		}

		if out.PaginationToken == nil || aws.ToString(out.PaginationToken) == "" {
			break
		}
		paginationToken = out.PaginationToken
	}

	// Deterministic order for reproducible output (matching every other
	// batch-producing command in this project) -- the tagging API's own
	// page order isn't documented as stable.
	sort.Slice(results, func(i, j int) bool { return results[i].ARN < results[j].ARN })
	return results, nil
}

// classify turns one tagging-API result into a DiscoveredResource,
// building its lookup if its (service, resourceTypePrefix) pair is
// classified — docs/discovery.md's own three-tier translation.
func classify(a ARN, m types.ResourceTagMapping) DiscoveredResource {
	d := DiscoveredResource{ARN: a.String(), Tags: tagsToMap(m.Tags)}
	spec, ok := ClassifyARN(a)
	if !ok {
		d.NotAdoptableReason = ErrNotYetAdoptable.Error()
		return d
	}
	d.TerraformType = spec.TerraformType
	lookup, err := BuildLookup(spec, a)
	if err != nil {
		d.NotAdoptableReason = err.Error()
		return d
	}
	d.Lookup = lookup
	return d
}

func tagsToMap(tags []types.Tag) map[string]string {
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		m[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return m
}

// tagFiltersFor converts Options.Tags into the tagging API's own
// TagFilters shape -- one filter per distinct key, every requested
// value for that key folded into one filter's own Values (the tagging
// API's own real, documented OR-within-key/AND-across-keys semantics,
// confirmed via `go doc`, not assumed).
func tagFiltersFor(tags map[string][]string) []types.TagFilter {
	if len(tags) == 0 {
		return nil
	}
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic request shape, easier to test/replay
	filters := make([]types.TagFilter, 0, len(keys))
	for _, k := range keys {
		filters = append(filters, types.TagFilter{Key: aws.String(k), Values: tags[k]})
	}
	return filters
}

// ErrConfirmationRequired means Discover found more matched resources
// than the configured limit, and the caller hasn't explicitly confirmed
// proceeding (--yes) -- docs/discovery.md's own adversarial row 2: "the
// matched count is printed and a --limit-gated confirmation is required
// before any per-resource ReadResource call runs," the same
// "friction by default" posture --confirm-destroys already established
// for a different risk.
var ErrConfirmationRequired = errors.New("matched resource count exceeds the limit -- pass --yes to proceed, or narrow --tag/--type/--region")

// CheckLimit is the confirmation gate itself -- a small, pure function
// so it's hermetically testable without needing thousands of real
// resources (docs/discovery.md's own adversarial row 2 names this gap
// honestly: real tagging-API pagination at that scale was never
// triggered live, only this gate's own logic is asserted here).
// limit <= 0 means DefaultLimit.
func CheckLimit(count, limit int, confirmed bool) error {
	if limit <= 0 {
		limit = DefaultLimit
	}
	if count > limit && !confirmed {
		return fmt.Errorf("%w (%d matched, limit %d)", ErrConfirmationRequired, count, limit)
	}
	return nil
}
