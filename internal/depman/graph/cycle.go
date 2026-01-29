package graph

import (
	"fmt"
	"strings"
)

// CycleError represents a dependency cycle in the graph.
type CycleError struct {
	Cycle []string // Module paths forming the cycle
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("dependency cycle detected: %s", strings.Join(e.Cycle, " -> "))
}

// DetectCycles checks for cycles in the dependency graph.
// Returns nil if no cycles are found, or a CycleError describing the first cycle found.
func (g *Graph) DetectCycles() error {
	// Track visit state: 0 = unvisited, 1 = in progress, 2 = done
	state := make(map[string]int)
	// Track the path for cycle reporting
	path := make([]string, 0)

	var visit func(node *Node) error
	visit = func(node *Node) error {
		if state[node.Path] == 2 {
			// Already fully processed
			return nil
		}
		if state[node.Path] == 1 {
			// Found a cycle - build the cycle path
			cycleStart := -1
			for i, p := range path {
				if p == node.Path {
					cycleStart = i
					break
				}
			}
			if cycleStart >= 0 {
				cycle := append(path[cycleStart:], node.Path)
				return &CycleError{Cycle: cycle}
			}
			return &CycleError{Cycle: []string{node.Path}}
		}

		// Mark as in progress
		state[node.Path] = 1
		path = append(path, node.Path)

		// Visit all children
		for _, edge := range node.Children {
			if err := visit(edge.To); err != nil {
				return err
			}
		}

		// Mark as done and remove from path
		state[node.Path] = 2
		path = path[:len(path)-1]

		return nil
	}

	// Start from root
	return visit(g.Root)
}

// FindAllCycles finds all cycles in the graph.
// This is more expensive than DetectCycles but provides complete information.
func (g *Graph) FindAllCycles() [][]string {
	var cycles [][]string
	visited := make(map[string]bool)
	recStack := make(map[string]bool)
	path := make([]string, 0)

	var dfs func(node *Node)
	dfs = func(node *Node) {
		visited[node.Path] = true
		recStack[node.Path] = true
		path = append(path, node.Path)

		for _, edge := range node.Children {
			child := edge.To
			if !visited[child.Path] {
				dfs(child)
			} else if recStack[child.Path] {
				// Found a cycle
				cycleStart := -1
				for i, p := range path {
					if p == child.Path {
						cycleStart = i
						break
					}
				}
				if cycleStart >= 0 {
					cycle := make([]string, len(path)-cycleStart+1)
					copy(cycle, path[cycleStart:])
					cycle[len(cycle)-1] = child.Path
					cycles = append(cycles, cycle)
				}
			}
		}

		path = path[:len(path)-1]
		recStack[node.Path] = false
	}

	// Visit all nodes to find cycles in disconnected components
	for _, node := range g.Nodes {
		if !visited[node.Path] {
			dfs(node)
		}
	}

	return cycles
}

// TopologicalSort returns nodes in topological order (dependencies before dependents).
// Returns an error if a cycle is detected.
func (g *Graph) TopologicalSort() ([]*Node, error) {
	if err := g.DetectCycles(); err != nil {
		return nil, err
	}

	var result []*Node
	visited := make(map[string]bool)

	var visit func(node *Node)
	visit = func(node *Node) {
		if visited[node.Path] {
			return
		}
		visited[node.Path] = true

		// Visit dependencies first
		for _, edge := range node.Children {
			visit(edge.To)
		}

		result = append(result, node)
	}

	// Start from root
	visit(g.Root)

	// Post-order DFS already gives dependencies before dependents
	return result, nil
}
