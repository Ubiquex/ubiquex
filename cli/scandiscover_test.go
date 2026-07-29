package cli

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"

	"github.com/ubiquex/ubiquex-cli/core"
	"github.com/ubiquex/ubiquex-cli/discovery"
)

// fakeCLITaggingAPI is a hermetic, fully scripted discovery.TaggingAPI
// for CLI-level `ubx scan --discover` tests -- one page of results per
// entry in pages, served in order; PaginationToken set on every page but
// the last, so a multi-entry pages slice exercises real pagination.
type fakeCLITaggingAPI struct {
	pages [][]types.ResourceTagMapping
	call  int
}

func (f *fakeCLITaggingAPI) GetResources(_ context.Context, _ *resourcegroupstaggingapi.GetResourcesInput, _ ...func(*resourcegroupstaggingapi.Options)) (*resourcegroupstaggingapi.GetResourcesOutput, error) {
	if f.call >= len(f.pages) {
		return &resourcegroupstaggingapi.GetResourcesOutput{}, nil
	}
	page := f.pages[f.call]
	f.call++
	out := &resourcegroupstaggingapi.GetResourcesOutput{ResourceTagMappingList: page}
	if f.call < len(f.pages) {
		out.PaginationToken = aws.String("next")
	}
	return out, nil
}

func cliMapping(arn string, tags map[string]string) types.ResourceTagMapping {
	var ts []types.Tag
	for k, v := range tags {
		ts = append(ts, types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	return types.ResourceTagMapping{ResourceARN: aws.String(arn), Tags: ts}
}

// withDiscoveryTaggingAPI installs a fake discovery.TaggingAPI for the
// duration of one test, restoring the real production seam on cleanup --
// the same convention cli/accept_frommerge_remote_test.go's own
// remoteStoreFixture already establishes for openRemoteLedgerStore.
func withDiscoveryTaggingAPI(t *testing.T, api discovery.TaggingAPI) {
	t.Helper()
	orig := newDiscoveryTaggingAPI
	newDiscoveryTaggingAPI = func(ctx context.Context, region string) (discovery.TaggingAPI, error) { return api, nil }
	t.Cleanup(func() { newDiscoveryTaggingAPI = orig })
}

// fakeReadResourceFunc maps a lookup's own "id" field to a scripted
// outcome -- state to echo back, or an error, or nil (not-found).
type fakeReadResourceFunc func(typeName string, lookup json.RawMessage) (json.RawMessage, error)

type fakeDiscoveryStateReaderImpl struct {
	types map[string]bool
	read  fakeReadResourceFunc
}

func (f *fakeDiscoveryStateReaderImpl) Schema(context.Context) (any, map[string]any, error) {
	schemas := make(map[string]any, len(f.types))
	for t := range f.types {
		schemas[t] = struct{}{}
	}
	return struct{}{}, schemas, nil
}
func (f *fakeDiscoveryStateReaderImpl) Configure(context.Context, any, json.RawMessage) error {
	return nil
}
func (f *fakeDiscoveryStateReaderImpl) ReadResource(_ context.Context, _ any, typeName string, currentState json.RawMessage) (json.RawMessage, error) {
	return f.read(typeName, currentState)
}

// withDiscoveryStateReader installs a hand-written fake core.StateReader
// for the duration of one test -- never fakeprovider's own shared
// fake_widget fixture, whose schema doesn't include any of discovery's
// own real AWS tier-table types (aws_sqs_queue, aws_iam_policy, ...).
// Full, precise control over ReadResource's own per-scenario behavior,
// zero risk to the rest of this project's fakeprovider-based suite.
func withDiscoveryStateReader(t *testing.T, r *fakeDiscoveryStateReaderImpl) {
	t.Helper()
	orig := newDiscoveryStateReader
	newDiscoveryStateReader = func(ctx context.Context, providerPath, source, providerVersion string, salt []byte) (core.StateReader, func() error, string, error) {
		return r, func() error { return nil }, "", nil
	}
	t.Cleanup(func() { newDiscoveryStateReader = orig })
}

func echoingRead(typeName string, lookup json.RawMessage) (json.RawMessage, error) {
	var l map[string]string
	if err := json.Unmarshal(lookup, &l); err != nil {
		return nil, err
	}
	l["tags"] = ""
	b, _ := json.Marshal(l)
	return b, nil
}

func TestScanDiscover_RequiresTag(t *testing.T) {
	ledgerDir := t.TempDir()
	_, err := runUbx(t, nil, "scan", "--discover", "--stack", "payments", "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "--tag") {
		t.Fatalf("expected an error naming --tag, got: %v", err)
	}
}

func TestScanDiscover_MutuallyExclusiveWithSingleResourceFlags(t *testing.T) {
	ledgerDir := t.TempDir()
	_, err := runUbx(t, nil, "scan", "--discover", "--tag", "Env=prod", "--type", "aws_sqs_queue", "--stack", "payments", "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 2, "")
	if !strings.Contains(err.Error(), "--type") {
		t.Fatalf("expected an error naming the conflicting flag, got: %v", err)
	}
}

// TestScanDiscover_NoLookupShape_NotYetAdoptable is adversarial row 1 at
// the CLI level: a discovered resource with no tier-table entry is
// reported, never silently dropped, and never written as a proposal.
func TestScanDiscover_NoLookupShape_NotYetAdoptable(t *testing.T) {
	ledgerDir := t.TempDir()
	withDiscoveryTaggingAPI(t, &fakeCLITaggingAPI{pages: [][]types.ResourceTagMapping{
		{cliMapping("arn:aws:dynamodb:us-east-1:1:table/unclassified", map[string]string{"env": "prod"})},
	}})

	out, err := runUbx(t, nil, "scan", "--discover", "--tag", "env=prod", "--stack", "payments", "--ledger-dir", ledgerDir)
	requireExitCode(t, err, 1, out)
	if !strings.Contains(out, "not yet adoptable") {
		t.Fatalf("expected a 'not yet adoptable' line, got: %s", out)
	}
	if !strings.Contains(out, "0 adopted, 1 skipped") {
		t.Fatalf("expected 0 adopted, 1 skipped, got: %s", out)
	}
}

// TestScanDiscover_PaginationAndLimitGate is adversarial row 2 at the
// CLI level: multi-page results are followed to completion, the matched
// count is printed, and exceeding --limit without --yes refuses before
// any per-resource read.
func TestScanDiscover_PaginationAndLimitGate(t *testing.T) {
	ledgerDir := t.TempDir()
	pages := [][]types.ResourceTagMapping{
		{cliMapping("arn:aws:sqs:us-east-1:1:q1", nil)},
		{cliMapping("arn:aws:sqs:us-east-1:1:q2", nil)},
		{cliMapping("arn:aws:sqs:us-east-1:1:q3", nil)},
	}
	withDiscoveryTaggingAPI(t, &fakeCLITaggingAPI{pages: pages})

	out, err := runUbx(t, nil, "scan", "--discover", "--tag", "env=prod", "--stack", "payments", "--ledger-dir", ledgerDir, "--limit", "2")
	requireExitCode(t, err, 2, out)
	if !strings.Contains(out, "3 resource(s) matched, 3 adoptable") {
		t.Fatalf("expected the full, paginated count printed before refusing, got: %s", out)
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected the refusal to name --yes, got: %v", err)
	}

	// Same scope, --yes given: proceeds (each of the 3 queues fails at
	// the provider-read step in this test, since no state reader is
	// installed -- --limit's own gate is what's under test here, not
	// the read outcome).
	withDiscoveryTaggingAPI(t, &fakeCLITaggingAPI{pages: pages})
	withDiscoveryStateReader(t, &fakeDiscoveryStateReaderImpl{
		types: map[string]bool{"aws_sqs_queue": true},
		read:  echoingRead,
	})
	out2, err2 := runUbx(t, nil, "scan", "--discover", "--tag", "env=prod", "--stack", "payments", "--ledger-dir", ledgerDir, "--limit", "2", "--yes", "--provider", "unused")
	requireExitCode(t, err2, 0, out2)
	if !strings.Contains(out2, "3 adopted, 0 skipped") {
		t.Fatalf("expected --yes to proceed past the limit and adopt all 3, got: %s", out2)
	}
}

// TestScanDiscover_ProviderReadFailed_PermissionDeniedAndDeleted is
// adversarial rows 3 and 4 together at the CLI level: the tagging API's
// own broad enumeration succeeds for all three resources, but one's own
// ReadResource call fails (row 3: a type-specific permission denied) and
// another's returns a null/not-found read (row 4: deleted between list
// and read) -- both skipped with the identical "provider read failed"
// category runScanAll's own equivalent case already uses, and the batch
// continues to the third, which adopts successfully.
func TestScanDiscover_ProviderReadFailed_PermissionDeniedAndDeleted(t *testing.T) {
	ledgerDir := t.TempDir()
	withDiscoveryTaggingAPI(t, &fakeCLITaggingAPI{pages: [][]types.ResourceTagMapping{{
		cliMapping("arn:aws:sqs:us-east-1:1:denied-queue", nil),
		cliMapping("arn:aws:sqs:us-east-1:1:deleted-queue", nil),
		cliMapping("arn:aws:sqs:us-east-1:1:ok-queue", nil),
	}}})
	withDiscoveryStateReader(t, &fakeDiscoveryStateReaderImpl{
		types: map[string]bool{"aws_sqs_queue": true},
		read: func(typeName string, lookup json.RawMessage) (json.RawMessage, error) {
			switch {
			case strings.Contains(string(lookup), "denied-queue"):
				return nil, errors.New("AccessDeniedException: not authorized to perform sqs:GetQueueAttributes")
			case strings.Contains(string(lookup), "deleted-queue"):
				return nil, nil // genuinely not-found -- the same shape core.RunScan already treats as ErrResourceUnreadable
			default:
				return echoingRead(typeName, lookup)
			}
		},
	})

	out, err := runUbx(t, nil, "scan", "--discover", "--tag", "env=prod", "--stack", "payments", "--ledger-dir", ledgerDir, "--provider", "unused")
	requireExitCode(t, err, 1, out)
	if !strings.Contains(out, "1 adopted, 2 skipped") {
		t.Fatalf("expected 1 adopted (ok-queue), 2 skipped (denied+deleted), got: %s", out)
	}
	if strings.Count(out, "provider read failed") == 0 {
		t.Fatalf("expected both failures to share the 'provider read failed' category, got: %s", out)
	}
}

// TestScanDiscover_AlreadyAdopted_Idempotent is adversarial row 5: a
// rediscovered resource this ledger already knows about is skipped, not
// re-adopted -- core.RunScan's own existing, unmodified address-based
// classification, zero new logic.
func TestScanDiscover_AlreadyAdopted_Idempotent(t *testing.T) {
	ledgerDir := t.TempDir()
	outDir := filepath.Join(ledgerDir, "proposals")
	withDiscoveryTaggingAPI(t, &fakeCLITaggingAPI{pages: [][]types.ResourceTagMapping{
		{cliMapping("arn:aws:sqs:us-east-1:1:my-queue", nil)},
	}})
	withDiscoveryStateReader(t, &fakeDiscoveryStateReaderImpl{
		types: map[string]bool{"aws_sqs_queue": true},
		read:  echoingRead,
	})

	out1, err1 := runUbx(t, nil, "scan", "--discover", "--tag", "env=prod", "--stack", "payments", "--ledger-dir", ledgerDir, "--out-dir", outDir, "--provider", "unused")
	requireExitCode(t, err1, 0, out1)
	if !strings.Contains(out1, "1 adopted, 0 skipped") {
		t.Fatalf("expected the first run to adopt the queue, got: %s", out1)
	}

	// Discovery only ever generates -- exactly like --all --tfstate,
	// nothing here is "already adopted" until a real ubx accept records
	// it (record-only, blast-radius zero by construction).
	acceptOut, acceptErr := runUbx(t, nil, "accept", filepath.Join(outDir, "payments.aws_sqs_queue.my-queue.json"), "--ledger-dir", ledgerDir)
	if acceptErr != nil {
		t.Fatalf("ubx accept: %v\noutput: %s", acceptErr, acceptOut)
	}

	// Re-discover the identical, now-accepted resource.
	withDiscoveryTaggingAPI(t, &fakeCLITaggingAPI{pages: [][]types.ResourceTagMapping{
		{cliMapping("arn:aws:sqs:us-east-1:1:my-queue", nil)},
	}})
	withDiscoveryStateReader(t, &fakeDiscoveryStateReaderImpl{
		types: map[string]bool{"aws_sqs_queue": true},
		read:  echoingRead,
	})
	out2, err2 := runUbx(t, nil, "scan", "--discover", "--tag", "env=prod", "--stack", "payments", "--ledger-dir", ledgerDir, "--provider", "unused")
	requireExitCode(t, err2, 1, out2)
	if !strings.Contains(out2, "already known to the ledger") {
		t.Fatalf("expected the rediscovered resource to be skipped as already known, got: %s", out2)
	}
	if !strings.Contains(out2, "0 adopted, 1 skipped") {
		t.Fatalf("expected 0 adopted on rediscovery, got: %s", out2)
	}
}

func TestScanDiscover_HappyPath_WritesProposalsToOutDir(t *testing.T) {
	ledgerDir := t.TempDir()
	outDir := filepath.Join(ledgerDir, "proposals")
	withDiscoveryTaggingAPI(t, &fakeCLITaggingAPI{pages: [][]types.ResourceTagMapping{
		{
			cliMapping("arn:aws:sqs:us-east-1:1:queue-a", map[string]string{"Project": "payments"}),
			cliMapping("arn:aws:iam::1:policy/policy-a", map[string]string{"Project": "payments"}),
		},
	}})
	withDiscoveryStateReader(t, &fakeDiscoveryStateReaderImpl{
		types: map[string]bool{"aws_sqs_queue": true, "aws_iam_policy": true},
		read:  echoingRead,
	})

	out, err := runUbx(t, nil, "scan", "--discover", "--tag", "Project=payments", "--stack", "payments",
		"--ledger-dir", ledgerDir, "--out-dir", outDir, "--provider", "unused")
	requireExitCode(t, err, 0, out)
	if !strings.Contains(out, "2 adopted, 0 skipped") {
		t.Fatalf("expected both resources adopted, got: %s", out)
	}
	if !strings.Contains(out, "payments.aws_sqs_queue.queue-a") {
		t.Fatalf("expected the sqs queue's own address in the output, got: %s", out)
	}
	if !strings.Contains(out, "payments.aws_iam_policy.policy-a") {
		t.Fatalf("expected the iam policy's own address in the output, got: %s", out)
	}
}

func TestScanSuggestStacks_GroupsAndWritesNothing(t *testing.T) {
	ledgerDir := t.TempDir()
	withDiscoveryTaggingAPI(t, &fakeCLITaggingAPI{pages: [][]types.ResourceTagMapping{
		{
			cliMapping("arn:aws:sqs:us-east-1:1:queue-a", map[string]string{"Project": "payments"}),
			cliMapping("arn:aws:sqs:us-east-1:1:queue-b", map[string]string{"Project": "networking"}),
		},
	}})

	out, err := runUbx(t, nil, "scan", "--discover", "--suggest-stacks", "--tag", "env=prod", "--stack-tag", "Project", "--ledger-dir", ledgerDir)
	if err != nil {
		t.Fatalf("ubx scan --discover --suggest-stacks: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "networking: 1 resource(s)") || !strings.Contains(out, "payments: 1 resource(s)") {
		t.Fatalf("expected both suggested groups reported, got: %s", out)
	}
	if !strings.Contains(out, "nothing written") {
		t.Fatalf("expected the report to say nothing was written, got: %s", out)
	}
}
