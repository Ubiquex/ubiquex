package diagram

import (
	"encoding/json"
	"sort"

	"github.com/ubiquex/ubiquex/core"
	"github.com/ubiquex/ubiquex/core/resolver"
)

// topologyResource is one resources[] entry's own topology-relevant
// content -- type/name/op/depends_on, deliberately never config (a
// diagram never authors attribute values at all, the lossy-medium rule's
// own first half, so a diagram-sourced intent's own config is always
// empty anyway -- but Topology is written to answer "did the meaning
// change" for any resolver.IntentFile, not only a diagram-sourced one,
// so config is excluded on principle, not because it happens to be empty
// here).
type topologyResource struct {
	Type      string   `json:"type"`
	Name      string   `json:"name"`
	Op        string   `json:"op"`
	DependsOn []string `json:"depends_on,omitempty"`
}

// topologyView is Topology's own canonicalized shape: stack + resources,
// nothing else -- no intent.summary, no intent.sources, no ambiguity
// content (assumptions/defaults/questions). Deliberately a package-
// private type: callers get canonical bytes back, never this struct
// itself, so the exact field set stays free to evolve without becoming
// part of this package's own exported API surface.
type topologyView struct {
	Stack     string             `json:"stack"`
	Resources []topologyResource `json:"resources"`
}

// Topology returns intent's own canonical topology view (docs/diagram-
// medium.md's own "topology hash" concept, first given real code here,
// UBI-47 slice 5): core.CanonicalJSON over resources[] (type, name, op,
// depends_on) + stack, EXCLUDING intent.summary/sources/ambiguity
// content entirely. This is the "did the meaning change" question,
// deliberately distinct from a document's own content_hash (raw bytes,
// styling included, tamper-evidence): editing a diagram's own
// classes.*.style.* block changes its content_hash without changing
// this function's own output at all, styling never being topology-
// relevant to begin with.
//
// Sorted by (type, name) internally, independent of intent.Resources'
// own incoming order -- Parse's own output already comes pre-sorted
// this way (sortedLeaves walks the D2 graph in absID order), but
// Topology doesn't rely on that: a general "did the meaning change"
// comparison primitive shouldn't assume its caller happened to hand it
// resources in a stable order, especially once a resolved change
// proposal's own resources (a different producer entirely) starts
// getting compared through this same function.
func Topology(intent *resolver.IntentFile) ([]byte, error) {
	resources := make([]topologyResource, 0, len(intent.Resources))
	for _, r := range intent.Resources {
		resources = append(resources, topologyResource{
			Type:      r.Type,
			Name:      r.Name,
			Op:        r.Op,
			DependsOn: append([]string(nil), r.DependsOn...),
		})
	}
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Type != resources[j].Type {
			return resources[i].Type < resources[j].Type
		}
		return resources[i].Name < resources[j].Name
	})

	raw, err := json.Marshal(topologyView{Stack: intent.Stack, Resources: resources})
	if err != nil {
		return nil, err
	}
	var decoded interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return core.CanonicalJSON(decoded)
}
