// Real wiring for the provider-onboarding pipeline's own second half:
// filling in every real field a source model left undescribed.
//
// Cost-driven redesign, explicitly interim (founder's own words: "the
// right long-term design once budget exists; this is a deliberate
// interim substitution"): the real, live Claude API path
// (sdk/codegen/describe, checkpoint 4) stays in the code, but is no
// longer how a real coverage run gets its descriptions by default --
// per-field API billing across four-plus providers, at real volume
// (hundreds to thousands of fields), has no budget today. The default
// path is now a real, checked-in, provider-authored data file
// (loadCheckedInDescriptions) this pipeline reads at codegen time,
// costing nothing to consume. Authoring it is a real, separate,
// human-in-the-loop (or Claude-Code-in-session, no API key) step: this
// file's own emitDescribeGaps produces the real, structured "what's
// still missing" list that step needs, batched across sessions given
// the real volume -- see cli/sdk.go's own --list-undescribed flag.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ubiquex/ubiquex/sdk/codegen/describe"
	"github.com/ubiquex/ubiquex/sdk/codegen/ir"
)

// descriptionCoverage is one provider's own real, honest tally --
// exactly the three real DescriptionSource states, counted, never
// estimated or extrapolated from a sample, plus Excluded (below).
type descriptionCoverage struct {
	Sourced    int // DescriptionSourceModel -- the real provider's own prose
	AIInferred int // DescriptionSourceAIInferred -- checked-in or live-generated, labeled visibly
	None       int // DescriptionSourceNone -- genuinely undescribed: abstained, or not yet authored

	// Excluded is real fields belonging to a resource this provider's own
	// config declared pathological via describe_exclude (describeexclude.go)
	// -- generated normally (codegen never sees this exclusion), but never
	// enriched, never counted toward None, never listed in a gap file.
	// Confirmed live, not assumed: AWS's real CloudFormation registry
	// includes AWS::QuickSight::Dashboard/Analysis/Template, three
	// resources whose real, deeply-nested visual/chart-configuration
	// schemas alone total 77,457 of the registry's 126,624 real fields at
	// a ~0.1% real sourced rate -- deeply-nested visualization
	// definitions nobody hand-authors, generating descriptions for them
	// is real, measured waste, not a hypothetical one. Reported
	// separately rather than silently folded into None so the coverage
	// report stays honest about what was actually attempted versus what
	// was deliberately never asked.
	Excluded int
}

func (c descriptionCoverage) total() int { return c.Sourced + c.AIInferred + c.None + c.Excluded }

// String renders a real, human-readable coverage line -- the exact shape
// the founder asked to see reported "per provider." The Excluded segment
// is only ever shown when non-zero, so a provider with no real
// describe_exclude entries renders exactly as it always has.
func (c descriptionCoverage) String() string {
	total := c.total()
	if total == 0 {
		return "0 fields"
	}
	s := fmt.Sprintf("%d fields: %d sourced (%.0f%%), %d AI-inferred (%.0f%%), %d none (%.0f%%)",
		total,
		c.Sourced, 100*float64(c.Sourced)/float64(total),
		c.AIInferred, 100*float64(c.AIInferred)/float64(total),
		c.None, 100*float64(c.None)/float64(total),
	)
	if c.Excluded > 0 {
		s += fmt.Sprintf(", %d excluded (%.0f%%, describe_exclude)", c.Excluded, 100*float64(c.Excluded)/float64(total))
	}
	return s
}

// enrichDescriptionsConcurrency bounds how many real, concurrent Claude
// API calls a single `ubx sdk gen --describe` run makes -- opt-in, not
// the default path (see this file's own top doc comment), but still a
// real, deliberate rate-limit-safety bound whenever it IS used.
const enrichDescriptionsConcurrency = 8

// defaultDescriptionsDir is where the onboarding pipeline's own
// checked-in description artifacts live -- a real, deliberate sibling of
// wherever THIS invocation's own .ubx/config resolved from (relative to
// the process's own CWD, exactly like .ubx/config itself), never a
// repo-root-relative path: `ubx sdk gen` against the central provider
// config is run FROM sdk/providers/ (where sdk/providers/.ubx/config
// itself lives, confirmed live this checkpoint -- a repo-root-relative
// default silently resolved to a nonexistent, doubled
// sdk/providers/sdk/providers/descriptions and produced zero AI-inferred
// fields despite a real, committed descriptions file existing, caught
// only by re-running and comparing the coverage report against what a
// real checked-in file should have changed). "descriptions", a plain
// sibling of .ubx/, is correct for both this pipeline's own real
// sdk/providers/ CWD and a generic per-project [thirdparty_providers]-only user
// running from their own repo root.
const defaultDescriptionsDir = "descriptions"

// describeJob is one real field FromSchema (and any prior enrichment
// step) left with DescriptionSourceNone -- resource/relPath together
// are the real, stable key both the checked-in descriptions file and
// the gap-list output use; ParentContext/Enum/Constraints in context
// are the real signal a description author (live API or a real,
// in-session Claude Code authoring pass) gets to work from.
type describeJob struct {
	field    *ir.Field
	resource string // rt.WireType
	relPath  string // dotted path relative to the resource root, e.g. "spec.replicas"
	context  describe.FieldContext
}

// gapFieldInfo is one real field's own recorded context in a gap-list
// output file -- exactly the real signal a description author (this
// session, or a future one) has to work with: name/type/required-optional-
// computed/parent-context always, enum/constraints when
// ubx-provider-dynamic's own --dump-signals mode found real data for
// this field (empty for a [thirdparty_providers] real-registry source, which has
// no OpenAPI/Smithy document to extract enum/constraints from at all --
// a real, separate, still-unaddressed limitation of THAT source, not
// this mechanism).
type gapFieldInfo struct {
	Type          string   `json:"type"`
	Required      bool     `json:"required,omitempty"`
	Optional      bool     `json:"optional,omitempty"`
	Computed      bool     `json:"computed,omitempty"`
	ParentContext string   `json:"parent_context"`
	Enum          []string `json:"enum,omitempty"`
	Constraints   []string `json:"constraints,omitempty"`
}

// checkedInDescriptions is one provider's own real, checked-in
// description artifact, loaded from sdk/providers/descriptions/<name>.json --
// resource WireType -> relPath -> the real description text an
// authoring pass (live API or a Claude Code session) wrote. A field
// simply ABSENT from this map is the real, first-class abstention
// outcome the founder's own instruction requires -- never a sentinel
// value, never a placeholder string.
type checkedInDescriptions map[string]map[string]string

// loadCheckedInDescriptions reads dir/<providerFileName>.json. Returns
// nil, nil (not an error) when no such file exists yet for this
// provider -- the real, honest, common state for any provider before
// its own first authoring pass; dir == "" also returns nil, nil
// (explicitly disabled, see --descriptions-dir's own flag doc).
func loadCheckedInDescriptions(dir, providerFileName string) (checkedInDescriptions, error) {
	if dir == "" {
		return nil, nil
	}
	path := filepath.Join(dir, providerFileName+".json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read checked-in descriptions %q: %w", path, err)
	}
	var out checkedInDescriptions
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse checked-in descriptions %q: %w", path, err)
	}
	return out, nil
}

func (c checkedInDescriptions) lookup(resource, relPath string) (string, bool) {
	if c == nil {
		return "", false
	}
	byField, ok := c[resource]
	if !ok {
		return "", false
	}
	desc, ok := byField[relPath]
	return desc, ok
}

// prune removes (resource, relPath) from c, deleting the resource's own
// entry entirely once it has no fields left -- keeps the checked-in
// artifact free of empty {} resource blocks a stale-entry removal would
// otherwise leave behind.
func (c checkedInDescriptions) prune(resource, relPath string) {
	byField, ok := c[resource]
	if !ok {
		return
	}
	delete(byField, relPath)
	if len(byField) == 0 {
		delete(c, resource)
	}
}

// enrichOptions controls which real enrichment mechanisms enrichDescriptions
// applies, in order: checkedIn first (free, the real default path), then
// gen (opt-in, real live API calls, --describe) for whatever's still
// left. gapsOut, when non-nil, receives every field STILL
// DescriptionSourceNone after both -- the real, structured "what's
// missing" list --list-undescribed writes out.
type enrichOptions struct {
	checkedIn checkedInDescriptions
	gen       *describe.Generator
	gapsOut   *map[string]map[string]gapFieldInfo
}

// enrichDescriptions walks every real field of every resource type in
// types (recursively, through List/Set/Map/Object nesting), applying
// opts' own real mechanisms in order, and returns the real, honest,
// counted coverage split, plus how many stale entries it pruned from
// opts.checkedIn (see that field's own doc comment). A field already
// carrying DescriptionSourceModel (the real provider's own prose) is
// never touched by any enrichment mechanism -- every mechanism here
// only ever fills a genuine gap, never overwrites or second-guesses a
// real, existing description. signalsByType carries
// ubx-provider-dynamic's own real, per-resource enum/constraint signal
// (nil for a [thirdparty_providers] real-registry source, which has no such data
// at all -- see gapFieldInfo's own doc comment).
func enrichDescriptions(ctx context.Context, providerName string, types []*ir.ResourceType, signalsByType map[string]map[string]*fieldSignal, opts enrichOptions) (descriptionCoverage, int, error) {
	jobs, stale := collectJobsAndStale(providerName, types, signalsByType, opts.checkedIn)

	var coverage descriptionCoverage
	for _, rt := range types {
		countExisting(rt.Fields, &coverage)
	}

	// Real, direct fix for the real staleness gap this checkpoint's own
	// verification found: the "sourced always wins" outcome above was
	// already correct BY CONSTRUCTION (collectJobs, below, only ever
	// builds a job for a DescriptionSourceNone field -- a field that
	// gained a real source description on THIS run never reaches the
	// checked-in lookup at all, so a stale entry could never overwrite
	// a real one). What was genuinely missing: nothing ever removed
	// that now-stale, now-permanently-unused entry from the checked-in
	// artifact itself -- it just sat there forever, inert, implying a
	// real AI-inference happened for a field that's actually sourced
	// now. Pruned here, in opts.checkedIn directly (the caller's own
	// loaded map, mutated in place) -- cli/sdk.go's own
	// writeGeneratedSDK is responsible for persisting the pruned result
	// back to disk when this count is non-zero.
	for _, s := range stale {
		opts.checkedIn.prune(s.resource, s.relPath)
	}

	var remaining []describeJob
	for _, j := range jobs {
		if desc, ok := opts.checkedIn.lookup(j.resource, j.relPath); ok {
			j.field.Description = desc
			j.field.DescriptionSource = ir.DescriptionSourceAIInferred
			coverage.AIInferred++
			continue
		}
		remaining = append(remaining, j)
	}
	jobs = remaining

	if opts.gen != nil && len(jobs) > 0 {
		var err error
		jobs, err = runLiveDescribe(ctx, opts.gen, jobs, &coverage)
		if err != nil {
			return coverage, len(stale), err
		}
	}

	if opts.gapsOut != nil {
		for _, j := range jobs {
			if (*opts.gapsOut)[j.resource] == nil {
				(*opts.gapsOut)[j.resource] = map[string]gapFieldInfo{}
			}
			(*opts.gapsOut)[j.resource][j.relPath] = gapFieldInfo{
				Type:          j.context.Type,
				Required:      j.context.Required,
				Optional:      j.context.Optional,
				Computed:      j.context.Computed,
				ParentContext: j.context.ParentContext,
				Enum:          j.context.Enum,
				Constraints:   j.context.Constraints,
			}
		}
	}
	coverage.None += len(jobs)

	return coverage, len(stale), nil
}

// runLiveDescribe calls gen.Describe for every real job, bounded to
// enrichDescriptionsConcurrency concurrent real API calls, returning
// whatever jobs the generator itself abstained on (still genuinely
// undescribed -- eligible for gapsOut, same as a field this whole
// mechanism never had a checked-in entry OR a live call for at all).
func runLiveDescribe(ctx context.Context, gen *describe.Generator, jobs []describeJob, coverage *descriptionCoverage) ([]describeJob, error) {
	sem := make(chan struct{}, enrichDescriptionsConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	var stillNone []describeJob

	for _, j := range jobs {
		wg.Add(1)
		go func(j describeJob) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			result, err := gen.Describe(ctx, j.context)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("describe %s.%s: %w", j.context.ParentContext, j.context.Name, err)
				}
				return
			}
			if result.Abstained {
				stillNone = append(stillNone, j)
				return
			}
			j.field.Description = result.Description
			j.field.DescriptionSource = ir.DescriptionSourceAIInferred
			coverage.AIInferred++
		}(j)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return stillNone, nil
}

// staleCheckedInEntry is one real (resource, relPath) checkedIn already
// covers whose corresponding field is now DescriptionSourceModel -- a
// real source description exists for it now, so the checked-in entry
// is stale, dead weight, never read again by lookup (which only ever
// consults a DescriptionSourceNone field).
type staleCheckedInEntry struct {
	resource string
	relPath  string
}

// collectJobsAndStale walks every field of every resource type in
// types, recursively through List/Set/Map/Object nesting (walkNested).
// For each DescriptionSourceNone field, it pairs it with its own real
// signal (looked up in signalsByType in exact lockstep with the
// field-tree walk) and its own dotted relPath, relative to the resource
// root -- the real, stable key both the checked-in descriptions file
// and the gap-list output share -- into a describeJob. For EVERY field,
// regardless of its own DescriptionSource, it also checks checkedIn for
// a now-stale entry (see staleCheckedInEntry's own doc comment) -- one
// real tree walk does both real jobs, rather than two.
func collectJobsAndStale(providerName string, types []*ir.ResourceType, signalsByType map[string]map[string]*fieldSignal, checkedIn checkedInDescriptions) ([]describeJob, []staleCheckedInEntry) {
	var jobs []describeJob
	var stale []staleCheckedInEntry
	var walk func(fields []ir.Field, resource, parentContext, relPath string, sigMap map[string]*fieldSignal)
	walk = func(fields []ir.Field, resource, parentContext, relPath string, sigMap map[string]*fieldSignal) {
		for i := range fields {
			f := &fields[i]
			fieldRelPath := joinFieldPath(relPath, f.WireName)
			var sig *fieldSignal
			if sigMap != nil {
				sig = sigMap[f.WireName]
			}
			switch f.DescriptionSource {
			case ir.DescriptionSourceNone:
				jobs = append(jobs, describeJob{
					field:    f,
					resource: resource,
					relPath:  fieldRelPath,
					context: describe.FieldContext{
						Name:          f.WireName,
						Type:          typeRefString(f.Type),
						Required:      f.Required,
						Optional:      f.Optional,
						Computed:      f.Computed,
						ParentContext: parentContext,
						Enum:          sig.enumStrings(),
						Constraints:   sig.constraintStrings(),
					},
				})
			case ir.DescriptionSourceModel:
				if _, ok := checkedIn.lookup(resource, fieldRelPath); ok {
					stale = append(stale, staleCheckedInEntry{resource: resource, relPath: fieldRelPath})
				}
			}
			var childSigs map[string]*fieldSignal
			if sig != nil {
				childSigs = sig.Nested
			}
			walkNested(f.Type, func(nested []ir.Field) {
				walk(nested, resource, parentContext+"."+f.WireName, fieldRelPath, childSigs)
			})
		}
	}
	for _, rt := range types {
		walk(rt.Fields, rt.WireType, providerName+"."+rt.WireType, "", signalsByType[rt.WireType])
	}
	return jobs, stale
}

func joinFieldPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "." + name
}

// countExisting tallies every field NOT needing enrichment (already
// DescriptionSourceModel) into coverage -- collectJobs/enrichDescriptions
// only ever increment AIInferred/None for the fields they actually
// touch, so the real "sourced" count is collected separately, once, up
// front.
func countExisting(fields []ir.Field, coverage *descriptionCoverage) {
	for i := range fields {
		f := &fields[i]
		if f.DescriptionSource == ir.DescriptionSourceModel {
			coverage.Sourced++
		}
		walkNested(f.Type, func(nested []ir.Field) {
			countExisting(nested, coverage)
		})
	}
}

// walkNested calls fn with ref's own nested field slice, if it has one --
// KindObject directly, or a List/Set/Map whose own Element is itself
// KindObject. A scalar or a List/Set/Map of scalars has nothing nested
// to walk.
func walkNested(ref ir.TypeRef, fn func([]ir.Field)) {
	switch ref.Kind {
	case ir.KindObject:
		fn(ref.Object)
	case ir.KindList, ir.KindSet, ir.KindMap:
		if ref.Element != nil && ref.Element.Kind == ir.KindObject {
			fn(ref.Element.Object)
		}
	}
}

// typeRefString renders ref as a short, human-readable type description
// -- the real "Type" signal describe.FieldContext's own doc comment
// promises to send, deliberately independent of any per-language
// template's own type-name rendering (docs/sdk.md's own "no per-language
// convention belongs in a shared layer" rule, applied here too).
func typeRefString(ref ir.TypeRef) string {
	switch ref.Kind {
	case ir.KindScalar:
		switch ref.Scalar {
		case ir.ScalarString:
			return "string"
		case ir.ScalarNumber:
			return "number"
		case ir.ScalarBool:
			return "bool"
		case ir.ScalarDynamic:
			return "dynamic"
		default:
			return "unknown scalar"
		}
	case ir.KindList:
		return "list of " + typeRefString(derefOrInvalid(ref.Element))
	case ir.KindSet:
		return "set of " + typeRefString(derefOrInvalid(ref.Element))
	case ir.KindMap:
		return "map of " + typeRefString(derefOrInvalid(ref.Element))
	case ir.KindObject:
		return "object"
	default:
		return "unknown"
	}
}

func derefOrInvalid(ref *ir.TypeRef) ir.TypeRef {
	if ref == nil {
		return ir.TypeRef{Kind: ir.KindInvalid}
	}
	return *ref
}

// formatCoverageReport renders the real, per-provider coverage lines the
// founder asked to see -- called once, after every declared source
// (both [thirdparty_providers] and [dynamic_providers.<name>]) has been enriched.
func formatCoverageReport(perProvider map[string]descriptionCoverage, order []string) string {
	var b strings.Builder
	b.WriteString("real description coverage:\n")
	for _, name := range order {
		c, ok := perProvider[name]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "  %s: %s\n", name, c)
	}
	return b.String()
}

// writeCheckedInDescriptions writes checkedIn back to
// dir/<providerFileName>.json -- the identical real path
// loadCheckedInDescriptions reads from, matching writeGapFile's own
// determinism (encoding/json already sorts map keys on Marshal). Called
// only when enrichDescriptions actually pruned at least one real stale
// entry (cli/sdk.go's own call site) -- a normal run with nothing stale
// never touches this file at all.
func writeCheckedInDescriptions(dir, providerFileName string, checkedIn checkedInDescriptions) error {
	data, err := json.MarshalIndent(checkedIn, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, providerFileName+".json")
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// writeGapFile writes gaps (resource -> relPath -> gapFieldInfo) to
// dir/<providerFileName>.json, deterministically (Go's own
// encoding/json already sorts map keys on Marshal, so this needs no
// separate sort step of its own) -- the real, structured "fields lacking
// a source description" list the founder's own interim workflow calls
// for, sharing the identical (resource, relPath) key shape
// loadCheckedInDescriptions reads, so authoring a real description for
// a gap is a same-shaped-key insertion into the checked-in file, not a
// format translation.
func writeGapFile(dir, providerFileName string, gaps map[string]map[string]gapFieldInfo) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(gaps, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, providerFileName+".json")
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
