// Package graph provides dependency graph construction and analysis.
package graph

import (
	"martianoff/gala/internal/depman/mod"
	"martianoff/gala/internal/depman/version"
)

// Node represents a module in the dependency graph.
type Node struct {
	Path     string          // Module path (e.g., "github.com/user/pkg")
	Version  version.Version // Resolved version
	Direct   bool            // True if this is a direct dependency
	Module   *mod.File       // Parsed gala.mod of this module (may be nil)
	Children []*Edge         // Outgoing edges to dependencies
	Parents  []*Edge         // Incoming edges from dependents
}

// Edge represents a dependency relationship between two modules.
type Edge struct {
	From       *Node              // The dependent module
	To         *Node              // The dependency module
	Constraint version.Constraint // Version constraint from require directive
	Indirect   bool               // True if this is an indirect dependency
}

// Graph represents the complete dependency graph for a module.
type Graph struct {
	Root  *Node            // The root module (the current project)
	Nodes map[string]*Node // All nodes indexed by module path
}

// NewGraph creates an empty dependency graph with the given root module.
func NewGraph(rootPath string) *Graph {
	root := &Node{
		Path:   rootPath,
		Direct: false, // Root is not a dependency
	}
	return &Graph{
		Root: root,
		Nodes: map[string]*Node{
			rootPath: root,
		},
	}
}

// AddNode adds a node to the graph if it doesn't exist.
// Returns the existing or newly created node.
func (g *Graph) AddNode(path string, ver version.Version, direct bool) *Node {
	if existing, ok := g.Nodes[path]; ok {
		// Update if this version is newer
		if ver.GreaterThan(existing.Version) {
			existing.Version = ver
		}
		if direct {
			existing.Direct = true
		}
		return existing
	}

	node := &Node{
		Path:    path,
		Version: ver,
		Direct:  direct,
	}
	g.Nodes[path] = node
	return node
}

// AddEdge adds a dependency edge between two nodes.
func (g *Graph) AddEdge(from, to *Node, constraint version.Constraint, indirect bool) *Edge {
	edge := &Edge{
		From:       from,
		To:         to,
		Constraint: constraint,
		Indirect:   indirect,
	}
	from.Children = append(from.Children, edge)
	to.Parents = append(to.Parents, edge)
	return edge
}

// GetNode returns the node for a module path, or nil if not found.
func (g *Graph) GetNode(path string) *Node {
	return g.Nodes[path]
}

// DirectDependencies returns all direct dependencies of the root module.
func (g *Graph) DirectDependencies() []*Node {
	var direct []*Node
	for _, node := range g.Nodes {
		if node.Direct && node != g.Root {
			direct = append(direct, node)
		}
	}
	return direct
}

// AllDependencies returns all dependencies (direct and indirect).
func (g *Graph) AllDependencies() []*Node {
	var deps []*Node
	for _, node := range g.Nodes {
		if node != g.Root {
			deps = append(deps, node)
		}
	}
	return deps
}

// IndirectDependencies returns all indirect dependencies.
func (g *Graph) IndirectDependencies() []*Node {
	var indirect []*Node
	for _, node := range g.Nodes {
		if !node.Direct && node != g.Root {
			indirect = append(indirect, node)
		}
	}
	return indirect
}
