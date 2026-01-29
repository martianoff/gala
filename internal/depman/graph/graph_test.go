package graph

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/depman/version"
)

func TestNewGraph(t *testing.T) {
	g := NewGraph("github.com/user/myproject")

	assert.NotNil(t, g.Root)
	assert.Equal(t, "github.com/user/myproject", g.Root.Path)
	assert.Len(t, g.Nodes, 1)
}

func TestGraph_AddNode(t *testing.T) {
	g := NewGraph("root")
	v1, _ := version.Parse("v1.0.0")
	v2, _ := version.Parse("v1.1.0")

	// Add first node
	node1 := g.AddNode("dep1", v1, true)
	assert.Equal(t, "dep1", node1.Path)
	assert.Equal(t, v1, node1.Version)
	assert.True(t, node1.Direct)
	assert.Len(t, g.Nodes, 2)

	// Add same node with higher version
	node2 := g.AddNode("dep1", v2, false)
	assert.Same(t, node1, node2)       // Same node returned
	assert.Equal(t, v2, node2.Version) // Version updated
	assert.True(t, node2.Direct)       // Direct flag preserved
	assert.Len(t, g.Nodes, 2)          // No new node added
}

func TestGraph_AddEdge(t *testing.T) {
	g := NewGraph("root")
	v1, _ := version.Parse("v1.0.0")

	dep := g.AddNode("dep1", v1, true)
	constraint, _ := version.ParseConstraint("^1.0.0")
	edge := g.AddEdge(g.Root, dep, constraint, false)

	assert.Equal(t, g.Root, edge.From)
	assert.Equal(t, dep, edge.To)
	assert.False(t, edge.Indirect)
	assert.Len(t, g.Root.Children, 1)
	assert.Len(t, dep.Parents, 1)
}

func TestGraph_Dependencies(t *testing.T) {
	g := NewGraph("root")
	v1, _ := version.Parse("v1.0.0")

	// Add direct and indirect dependencies
	direct1 := g.AddNode("direct1", v1, true)
	direct2 := g.AddNode("direct2", v1, true)
	indirect1 := g.AddNode("indirect1", v1, false)

	g.AddEdge(g.Root, direct1, nil, false)
	g.AddEdge(g.Root, direct2, nil, false)
	g.AddEdge(direct1, indirect1, nil, true)

	// Test DirectDependencies
	directDeps := g.DirectDependencies()
	assert.Len(t, directDeps, 2)

	// Test IndirectDependencies
	indirectDeps := g.IndirectDependencies()
	assert.Len(t, indirectDeps, 1)
	assert.Equal(t, "indirect1", indirectDeps[0].Path)

	// Test AllDependencies
	allDeps := g.AllDependencies()
	assert.Len(t, allDeps, 3)
}

func TestGraph_DetectCycles_NoCycle(t *testing.T) {
	g := NewGraph("root")
	v1, _ := version.Parse("v1.0.0")

	// Linear dependency: root -> a -> b -> c
	a := g.AddNode("a", v1, true)
	b := g.AddNode("b", v1, false)
	c := g.AddNode("c", v1, false)

	g.AddEdge(g.Root, a, nil, false)
	g.AddEdge(a, b, nil, true)
	g.AddEdge(b, c, nil, true)

	err := g.DetectCycles()
	assert.NoError(t, err)
}

func TestGraph_DetectCycles_WithCycle(t *testing.T) {
	g := NewGraph("root")
	v1, _ := version.Parse("v1.0.0")

	// Cycle: root -> a -> b -> a
	a := g.AddNode("a", v1, true)
	b := g.AddNode("b", v1, false)

	g.AddEdge(g.Root, a, nil, false)
	g.AddEdge(a, b, nil, true)
	g.AddEdge(b, a, nil, true) // Creates cycle

	err := g.DetectCycles()
	assert.Error(t, err)

	cycleErr, ok := err.(*CycleError)
	require.True(t, ok)
	assert.Contains(t, cycleErr.Cycle, "a")
	assert.Contains(t, cycleErr.Cycle, "b")
}

func TestGraph_TopologicalSort(t *testing.T) {
	g := NewGraph("root")
	v1, _ := version.Parse("v1.0.0")

	// Diamond dependency: root -> a, b; a -> c; b -> c
	a := g.AddNode("a", v1, true)
	b := g.AddNode("b", v1, true)
	c := g.AddNode("c", v1, false)

	g.AddEdge(g.Root, a, nil, false)
	g.AddEdge(g.Root, b, nil, false)
	g.AddEdge(a, c, nil, true)
	g.AddEdge(b, c, nil, true)

	sorted, err := g.TopologicalSort()
	require.NoError(t, err)

	// c should come before a and b, root should come last
	cIndex := -1
	aIndex := -1
	bIndex := -1
	rootIndex := -1
	for i, node := range sorted {
		switch node.Path {
		case "c":
			cIndex = i
		case "a":
			aIndex = i
		case "b":
			bIndex = i
		case "root":
			rootIndex = i
		}
	}

	assert.True(t, cIndex < aIndex, "c should come before a")
	assert.True(t, cIndex < bIndex, "c should come before b")
	assert.True(t, aIndex < rootIndex, "a should come before root")
	assert.True(t, bIndex < rootIndex, "b should come before root")
}

func TestMVS_Resolve(t *testing.T) {
	mvs := NewMVS()

	v100, _ := version.Parse("v1.0.0")
	v110, _ := version.Parse("v1.1.0")
	v120, _ := version.Parse("v1.2.0")

	// Module A is required at v1.0.0 and v1.2.0
	mvs.AddRequirement("github.com/example/a", v100)
	mvs.AddRequirement("github.com/example/a", v120)

	// Module B is required at v1.1.0 only
	mvs.AddRequirement("github.com/example/b", v110)

	selected := mvs.Resolve()

	// MVS should select the maximum of required versions
	assert.Equal(t, v120, selected["github.com/example/a"])
	assert.Equal(t, v110, selected["github.com/example/b"])
}

func TestMVS_BuildList(t *testing.T) {
	mvs := NewMVS()

	v100, _ := version.Parse("v1.0.0")
	v200, _ := version.Parse("v2.0.0")

	mvs.AddRequirement("github.com/example/b", v200)
	mvs.AddRequirement("github.com/example/a", v100)
	mvs.Resolve()

	list := mvs.BuildList()

	assert.Len(t, list, 2)
	// Should be sorted by path
	assert.Equal(t, "github.com/example/a", list[0].Path)
	assert.Equal(t, "github.com/example/b", list[1].Path)
}

func TestMVS_Upgrade(t *testing.T) {
	mvs := NewMVS()

	v100, _ := version.Parse("v1.0.0")
	v200, _ := version.Parse("v2.0.0")

	mvs.AddRequirement("github.com/example/a", v100)
	mvs.Resolve()

	// Upgrade to v2.0.0
	selected := mvs.Upgrade("github.com/example/a", v200)

	assert.Equal(t, v200, selected["github.com/example/a"])
}

func TestMVS_Downgrade(t *testing.T) {
	mvs := NewMVS()

	v100, _ := version.Parse("v1.0.0")
	v200, _ := version.Parse("v2.0.0")

	// Another module requires v2.0.0
	mvs.AddRequirement("github.com/example/a", v200)
	mvs.Resolve()

	// Try to downgrade to v1.0.0 - should fail
	err := mvs.Downgrade("github.com/example/a", v100)
	assert.Error(t, err)
}

func TestMVS_Conflicts(t *testing.T) {
	mvs := NewMVS()

	v100, _ := version.Parse("v1.0.0")
	v200, _ := version.Parse("v2.0.0")

	// Multiple versions required for same module
	mvs.AddRequirement("github.com/example/a", v100)
	mvs.AddRequirement("github.com/example/a", v200)
	mvs.Resolve()

	conflicts := mvs.Conflicts()
	assert.Len(t, conflicts, 1)
	assert.Contains(t, conflicts[0], "github.com/example/a")
}
