package graph

import (
	"fmt"

	"martianoff/gala/internal/depman/fetch"
	"martianoff/gala/internal/depman/mod"
	"martianoff/gala/internal/depman/version"
)

// Builder constructs a dependency graph by fetching and analyzing gala.mod files.
type Builder struct {
	cache   *fetch.Cache
	fetcher *fetch.GitFetcher
	visited map[string]bool // Track visited modules to avoid infinite loops
}

// NewBuilder creates a new graph builder with the given cache and fetcher.
func NewBuilder(cache *fetch.Cache, fetcher *fetch.GitFetcher) *Builder {
	return &Builder{
		cache:   cache,
		fetcher: fetcher,
		visited: make(map[string]bool),
	}
}

// Build constructs a complete dependency graph starting from the root gala.mod.
func (b *Builder) Build(rootMod *mod.File) (*Graph, error) {
	g := NewGraph(rootMod.Module.Path)
	g.Root.Module = rootMod

	// Process direct dependencies
	for _, req := range rootMod.Require {
		if err := b.processRequire(g, g.Root, req, false); err != nil {
			return nil, fmt.Errorf("failed to process %s: %w", req.Path, err)
		}
	}

	return g, nil
}

// BuildFromFile constructs a dependency graph from a gala.mod file path.
func (b *Builder) BuildFromFile(galaModPath string) (*Graph, error) {
	rootMod, err := mod.ParseFile(galaModPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", galaModPath, err)
	}
	return b.Build(rootMod)
}

// processRequire processes a single require directive and recursively processes its dependencies.
func (b *Builder) processRequire(g *Graph, parent *Node, req mod.Require, indirect bool) error {
	// Parse version
	ver, err := version.Parse(req.Version)
	if err != nil {
		return fmt.Errorf("invalid version %s: %w", req.Version, err)
	}

	// Check if already visited (with same or higher version)
	visitKey := fmt.Sprintf("%s@%s", req.Path, req.Version)
	if b.visited[visitKey] {
		// Already processed, just add edge if node exists
		if existing := g.GetNode(req.Path); existing != nil {
			constraint, _ := version.ParseConstraint(req.Version)
			g.AddEdge(parent, existing, constraint, indirect || req.Indirect)
		}
		return nil
	}
	b.visited[visitKey] = true

	// Add node
	isDirect := parent == g.Root && !req.Indirect
	node := g.AddNode(req.Path, ver, isDirect)

	// Add edge
	constraint, _ := version.ParseConstraint(req.Version)
	g.AddEdge(parent, node, constraint, indirect || req.Indirect)

	// Try to get the module's gala.mod to process transitive dependencies
	depMod, err := b.getModuleGalaMod(req.Path, req.Version)
	if err != nil {
		// Module might not have a gala.mod (leaf dependency)
		// This is not an error, just means no transitive deps
		return nil
	}
	node.Module = depMod

	// Process transitive dependencies
	for _, transReq := range depMod.Require {
		if err := b.processRequire(g, node, transReq, true); err != nil {
			return err
		}
	}

	return nil
}

// getModuleGalaMod retrieves the gala.mod for a module, fetching if necessary.
func (b *Builder) getModuleGalaMod(modulePath, ver string) (*mod.File, error) {
	// Try cache first
	if b.cache != nil {
		if galaMod, err := b.cache.GetGalaMod(modulePath, ver); err == nil {
			return galaMod, nil
		}
	}

	// If not in cache and we have a fetcher, try to fetch
	if b.fetcher != nil && b.cache != nil {
		_, _, err := b.fetcher.Fetch(modulePath, ver)
		if err != nil {
			return nil, err
		}
		return b.cache.GetGalaMod(modulePath, ver)
	}

	return nil, fmt.Errorf("module not found: %s@%s", modulePath, ver)
}

// Resolve applies Minimal Version Selection to resolve all dependency versions.
// This modifies the graph in place, updating node versions to the selected versions.
func (b *Builder) Resolve(g *Graph) error {
	// Collect all version requirements for each module
	requirements := make(map[string][]version.Version)

	for _, node := range g.Nodes {
		if node == g.Root {
			continue
		}
		requirements[node.Path] = append(requirements[node.Path], node.Version)

		// Also collect from incoming edges (constraints)
		for _, edge := range node.Parents {
			if edge.Constraint != nil {
				// For now, we just track the version; full MVS would evaluate constraints
				requirements[node.Path] = append(requirements[node.Path], node.Version)
			}
		}
	}

	// Apply MVS: select maximum of minimum required versions
	for path, versions := range requirements {
		if len(versions) == 0 {
			continue
		}

		// Find the maximum version (MVS selects the minimum version that satisfies all constraints)
		maxVer := versions[0]
		for _, v := range versions[1:] {
			if v.GreaterThan(maxVer) {
				maxVer = v
			}
		}

		// Update node version
		if node := g.GetNode(path); node != nil {
			node.Version = maxVer
		}
	}

	return nil
}
