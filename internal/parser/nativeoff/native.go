// Package native, in this directory, stands in for internal/parser/native in
// builds without galop's parser (--//internal/parser:antlr, or nix with
// useGalop = false). It declares the types internal/parser's native-tree
// handle names; there is never a tree of them (see parser.Native).
package native

// GalaTree stands in for galop's native tree.
type GalaTree struct{}

// Node is no node.
func (t *GalaTree) Node(int) GalaNodeRef { return GalaNodeRef{} }

// GalaNodeRef stands in for a node of galop's native tree: always no node.
type GalaNodeRef struct{}

// Ok reports false: there are no nodes.
func (GalaNodeRef) Ok() bool { return false }

// Tree is nil.
func (GalaNodeRef) Tree() *GalaTree { return nil }

// Index is -1.
func (GalaNodeRef) Index() int { return -1 }
