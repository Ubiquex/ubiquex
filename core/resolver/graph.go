package resolver

import (
	"fmt"
	"strings"
)

// topoSort orders nodes (canonical address strings) so every node appears
// after every node its own edges point at -- docs/resolver.md's own
// "dependency graph" section: v1 XCL's single-stack resource graph never
// actually detected cycles (only its separate workspace-level multi-stack
// one did, via a path/inStack DFS reporting the full cycle on first
// detection). This is that real fix, one level down: the same DFS
// pattern, applied to one proposal's own resource graph instead of a
// workspace's stack graph.
//
// edgesOf(node) returns the OTHER batch nodes that node depends on (an
// edge node -> dep means "node must be ordered after dep"). nodes is
// walked in the order given for DFS root selection -- the only remaining
// source of ordering in this function; callers must pass a stable order
// (resolver.go passes the intent file's own resource order, itself fixed
// by the input JSON's own array order) so the result is deterministic
// across runs of the same input, which is exactly what core.DoubleRun
// checks.
func topoSort(nodes []string, edgesOf func(node string) []string) ([]string, error) {
	const (
		unvisited = iota
		inProgress
		done
	)
	state := make(map[string]int, len(nodes))
	var order []string
	var path []string

	var visit func(node string) error
	visit = func(node string) error {
		switch state[node] {
		case done:
			return nil
		case inProgress:
			cycleStart := 0
			for i, p := range path {
				if p == node {
					cycleStart = i
					break
				}
			}
			cycle := append(append([]string{}, path[cycleStart:]...), node)
			return fmt.Errorf("%w: %s", ErrCycleDetected, strings.Join(cycle, " -> "))
		}
		state[node] = inProgress
		path = append(path, node)
		for _, dep := range edgesOf(node) {
			if err := visit(dep); err != nil {
				return err
			}
		}
		path = path[:len(path)-1]
		state[node] = done
		order = append(order, node)
		return nil
	}

	for _, n := range nodes {
		if err := visit(n); err != nil {
			return nil, err
		}
	}
	return order, nil
}
