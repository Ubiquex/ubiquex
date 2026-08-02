package resolver

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/ubiquex/ubiquex/core"
)

// awsSchema/helmSchema are two disjoint fakeSchemas -- neither owns the
// other's types -- standing in for two genuinely different declared
// providers (docs/architecture.md §Multi-provider stacks). k8sSchema owns
// neither aws_db_instance nor helm_release -- used as a third declared
// provider that's never an owner, for the hint-names-a-real-but-wrong
// provider case.

func awsSchema() *fakeSchema {
	return &fakeSchema{
		types: map[string]bool{"aws_db_instance": true, "aws_vpc": true},
		computed: map[string]bool{
			"aws_db_instance.id":       true,
			"aws_db_instance.endpoint": true,
		},
	}
}

func helmSchema() *fakeSchema {
	return &fakeSchema{
		types:    map[string]bool{"helm_release": true},
		computed: map[string]bool{"helm_release.id": true},
	}
}

func k8sSchema() *fakeSchema {
	return &fakeSchema{types: map[string]bool{"kubernetes_namespace": true}}
}

// ambiguousSchema advertises the same type name a sibling provider also
// advertises -- docs/multi-provider-adversarial.md row 1's own fixture: a
// forked/vendored provider whose schema happens to claim a type another
// declared provider also claims.
func ambiguousSchema() *fakeSchema {
	return &fakeSchema{types: map[string]bool{"example_widget": true}}
}

func twoProviders() []DeclaredProvider {
	return []DeclaredProvider{
		{Source: "hashicorp/aws", Version: "6.60.0", Schema: awsSchema()},
		{Source: "hashicorp/helm", Version: "3.0.2", Schema: helmSchema()},
	}
}

// --- happy path: type -> provider inference records the winner ----------

func TestResolve_TwoProviders_TypeInference_RecordsWinner(t *testing.T) {
	l := core.Open(t.TempDir())
	intent := intentFile("payments",
		ri("aws_db_instance", "main", OpCreate, `{"instance_class":"db.t3.medium"}`),
		ri("helm_release", "app", OpCreate, `{"chart":"nginx"}`),
	)

	p, err := Resolve(l, twoProviders(), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(p.Delta.Creates) != 2 {
		t.Fatalf("creates = %d, want 2", len(p.Delta.Creates))
	}
	byName := map[string]map[string]interface{}{}
	for _, raw := range p.Delta.Creates {
		var node map[string]interface{}
		if err := json.Unmarshal(raw, &node); err != nil {
			t.Fatalf("decode create node: %v", err)
		}
		byName[node["name"].(string)] = node
	}
	dbProv, ok := byName["main"]["provider"].(map[string]interface{})
	if !ok || dbProv["source"] != "hashicorp/aws" || dbProv["version"] != "6.60.0" {
		t.Fatalf("aws_db_instance.main provider = %v, want hashicorp/aws@6.60.0", byName["main"]["provider"])
	}
	helmProv, ok := byName["app"]["provider"].(map[string]interface{})
	if !ok || helmProv["source"] != "hashicorp/helm" || helmProv["version"] != "3.0.2" {
		t.Fatalf("helm_release.app provider = %v, want hashicorp/helm@3.0.2", byName["app"]["provider"])
	}
	if err := core.Validate(p); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestResolve_Modify_RecordsProvider(t *testing.T) {
	l := core.Open(t.TempDir())
	addr := core.Address{Stack: "payments", Type: "aws_db_instance", Name: "main"}
	seedLedger(t, l, addr, `{"id":"main-1","instance_class":"db.t3.small"}`)

	intent := intentFile("payments",
		ri("aws_db_instance", "main", OpModify, `{"instance_class":"db.t3.large"}`),
	)
	p, err := Resolve(l, twoProviders(), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(p.Delta.Modifies) != 1 {
		t.Fatalf("modifies = %d, want 1", len(p.Delta.Modifies))
	}
	prov := p.Delta.Modifies[0].Provider
	if prov == nil || prov.Source != "hashicorp/aws" || prov.Version != "6.60.0" {
		t.Fatalf("modify provider = %+v, want hashicorp/aws@6.60.0", prov)
	}
}

// --- docs/multi-provider-adversarial.md row 1: ambiguous type, no hint ---

func TestResolve_AmbiguousType_NoHint_Refused(t *testing.T) {
	l := core.Open(t.TempDir())
	providers := []DeclaredProvider{
		{Source: "hashicorp/example", Version: "1.0.0", Schema: ambiguousSchema()},
		{Source: "acme/example", Version: "2.0.0", Schema: ambiguousSchema()},
	}
	intent := intentFile("payments",
		ri("example_widget", "thing", OpCreate, `{}`),
	)
	_, err := Resolve(l, providers, intent, nil)
	if !errors.Is(err, ErrAmbiguousType) {
		t.Fatalf("err = %v, want ErrAmbiguousType", err)
	}
}

// --- row 2: ambiguous type, resolved via an explicit hint -----------------

func TestResolve_AmbiguousType_WithHint_Resolves(t *testing.T) {
	l := core.Open(t.TempDir())
	providers := []DeclaredProvider{
		{Source: "hashicorp/example", Version: "1.0.0", Schema: ambiguousSchema()},
		{Source: "acme/example", Version: "2.0.0", Schema: ambiguousSchema()},
	}
	intent := intentFile("payments",
		ResourceIntent{
			Type: "example_widget", Name: "thing", Op: OpCreate,
			Config:   json.RawMessage(`{}`),
			Provider: &ProviderHint{Source: "acme/example"},
		},
	)
	p, err := Resolve(l, providers, intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var node map[string]interface{}
	json.Unmarshal(p.Delta.Creates[0], &node)
	prov, ok := node["provider"].(map[string]interface{})
	if !ok || prov["source"] != "acme/example" || prov["version"] != "2.0.0" {
		t.Fatalf("provider = %v, want acme/example@2.0.0 (the hinted source)", node["provider"])
	}
}

func TestResolve_AmbiguousType_HintNamesUndeclaredSource_Refused(t *testing.T) {
	l := core.Open(t.TempDir())
	providers := []DeclaredProvider{
		{Source: "hashicorp/example", Version: "1.0.0", Schema: ambiguousSchema()},
		{Source: "acme/example", Version: "2.0.0", Schema: ambiguousSchema()},
	}
	intent := intentFile("payments",
		ResourceIntent{
			Type: "example_widget", Name: "thing", Op: OpCreate,
			Config:   json.RawMessage(`{}`),
			Provider: &ProviderHint{Source: "never/declared"},
		},
	)
	_, err := Resolve(l, providers, intent, nil)
	if !errors.Is(err, ErrProviderHintUnknown) {
		t.Fatalf("err = %v, want ErrProviderHintUnknown", err)
	}
}

// The hint names a real declared provider -- just not one that actually
// owns this type. The hint selects among real owners, it never
// manufactures ownership.
func TestResolve_AmbiguousType_HintNamesRealProviderThatDoesNotOwnType_Refused(t *testing.T) {
	l := core.Open(t.TempDir())
	providers := []DeclaredProvider{
		{Source: "hashicorp/example", Version: "1.0.0", Schema: ambiguousSchema()},
		{Source: "acme/example", Version: "2.0.0", Schema: ambiguousSchema()},
		{Source: "hashicorp/kubernetes", Version: "2.35.1", Schema: k8sSchema()},
	}
	intent := intentFile("payments",
		ResourceIntent{
			Type: "example_widget", Name: "thing", Op: OpCreate,
			Config:   json.RawMessage(`{}`),
			Provider: &ProviderHint{Source: "hashicorp/kubernetes"},
		},
	)
	_, err := Resolve(l, providers, intent, nil)
	if !errors.Is(err, ErrProviderHintDoesNotOwnType) {
		t.Fatalf("err = %v, want ErrProviderHintDoesNotOwnType", err)
	}
}

// --- row 3: unowned type ---------------------------------------------------

func TestResolve_UnownedType_Refused(t *testing.T) {
	l := core.Open(t.TempDir())
	intent := intentFile("payments",
		ri("gcp_bucket", "main", OpCreate, `{}`),
	)
	_, err := Resolve(l, twoProviders(), intent, nil)
	if !errors.Is(err, ErrUnknownType) {
		t.Fatalf("err = %v, want ErrUnknownType", err)
	}
}

// --- row 5: a cross-provider $ref chain -----------------------------------

// TestResolve_CrossProviderRef_ComputedSubstitution proves refs.go's own
// fix (docs/resolver.md's amendment): a $ref's IsComputed check must
// consult the REFERENCED sibling's own provider schema, not the
// referencing entry's -- helmSchema (the referencing entry's own provider
// here) doesn't even know aws_db_instance exists at all; if resolveRef
// used the wrong schema this would panic or silently misclassify instead
// of correctly marking $computed.
func TestResolve_CrossProviderRef_ComputedSubstitution(t *testing.T) {
	l := core.Open(t.TempDir())
	intent := intentFile("payments",
		ri("aws_db_instance", "main", OpCreate, `{"instance_class":"db.t3.medium"}`),
		ri("helm_release", "app", OpCreate, `{"values":{"db_endpoint":{"$ref":{"to":"payments.aws_db_instance.main.endpoint"}}}}`),
	)
	p, err := Resolve(l, twoProviders(), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var helmNode map[string]interface{}
	for _, raw := range p.Delta.Creates {
		var node map[string]interface{}
		json.Unmarshal(raw, &node)
		if node["name"] == "app" {
			helmNode = node
		}
	}
	if helmNode == nil {
		t.Fatal("helm_release.app not found in resolved creates")
	}
	values := helmNode["config"].(map[string]interface{})["values"].(map[string]interface{})
	endpoint, ok := values["db_endpoint"].(map[string]interface{})
	computed, ok2 := endpoint["$computed"].(map[string]interface{})
	if !ok || !ok2 {
		t.Fatalf("db_endpoint = %v, want a $computed marker (aws_db_instance.endpoint is Computed per its own provider's schema)", values["db_endpoint"])
	}
	if computed["from"] != "payments.aws_db_instance.main.endpoint" {
		t.Fatalf("$computed.from = %v", computed["from"])
	}
	dependsOn, _ := helmNode["depends_on"].([]interface{})
	if len(dependsOn) != 1 || dependsOn[0] != "payments.aws_db_instance.main" {
		t.Fatalf("depends_on = %v, want [payments.aws_db_instance.main]", dependsOn)
	}
}

// --- destroys infer their provider fresh, not from history -----------------

func TestResolve_Destroy_InfersProviderFresh(t *testing.T) {
	l := core.Open(t.TempDir())
	addr := core.Address{Stack: "payments", Type: "helm_release", Name: "app"}
	seedLedgerWithLookup(t, l, addr, `{"chart":"nginx"}`, `{"id":"app"}`)

	intent := &IntentFile{
		SchemaVersion: 1, Kind: IntentFileKind, Stack: "payments",
		Intent: core.Intent{Summary: "decommission app"}, Destroys: []string{addr.String()},
	}
	p, err := Resolve(l, twoProviders(), intent, nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(p.Delta.Destroys) != 1 {
		t.Fatalf("destroys = %d, want 1", len(p.Delta.Destroys))
	}
	prov := p.Delta.Destroys[0].Provider
	if prov == nil || prov.Source != "hashicorp/helm" || prov.Version != "3.0.2" {
		t.Fatalf("destroy provider = %+v, want hashicorp/helm@3.0.2", prov)
	}
}

func TestResolve_Destroy_UnownedType_Refused(t *testing.T) {
	l := core.Open(t.TempDir())
	addr := core.Address{Stack: "payments", Type: "gcp_bucket", Name: "main"}
	seedLedgerWithLookup(t, l, addr, `{}`, `{"id":"main"}`)

	intent := &IntentFile{
		SchemaVersion: 1, Kind: IntentFileKind, Stack: "payments",
		Intent: core.Intent{Summary: "decommission main"}, Destroys: []string{addr.String()},
	}
	_, err := Resolve(l, twoProviders(), intent, nil)
	if !errors.Is(err, ErrUnknownType) {
		t.Fatalf("err = %v, want ErrUnknownType (the declared providers changed since this resource was created -- no longer owned by anything declared)", err)
	}
}
