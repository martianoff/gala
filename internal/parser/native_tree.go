package parser

import (
	"errors"
	"slices"
	"sync"

	"martianoff/gala/internal/parser/native"
)

// errNoNative is what a nil *Native reports: the parse gave no tree, it
// reported syntax errors (see Parsed.Native), or the holder dropped the
// handle.
var errNoNative = errors.New("parser: no native tree for this parse")

// Native is a parse's tree in galop's native form (internal/parser/native),
// which galop's native parser builds (nativeHandle). A nil *Native is valid
// and has no tree: Tree reports errNoNative.
type Native struct {
	tree *native.GalaTree
	// trimmed is TreeOnly's trim of the tree (trimSpare), done once.
	trimmed sync.Once
}

// nativeHandle returns the handle of tree, which galop's native parser built.
func nativeHandle(tree *native.GalaTree) *Native {
	return &Native{tree: tree}
}

// Tree is the native tree. An error says there is none: see errNoNative.
func (t *Native) Tree() (*native.GalaTree, error) {
	if t == nil || t.tree == nil {
		return nil, errNoNative
	}
	return t.tree, nil
}

// Root is the root of the native tree; no node and false when there is none
// (see Tree).
func (t *Native) Root() (native.GalaNodeRef, bool) {
	tree, err := t.Tree()
	if err != nil {
		return native.GalaNodeRef{}, false
	}
	return tree.Root(), true
}

// TreeOnly is t when it has a tree, nil when it has none: the handle for a
// holder that keeps it long. The first call cuts the spare capacity off the
// tree's slices (trimSpare), so the holder does not keep it. That replaces
// the slices under the tree's readers: call it before the tree is read on any
// other goroutine. Later calls change nothing.
func (t *Native) TreeOnly() *Native {
	tree, err := t.Tree()
	if err != nil {
		return nil
	}
	t.trimmed.Do(func() { trimSpare(tree) })
	return t
}

// trimSpare cuts tree's slices to their length where much of them is spare
// capacity. The lexer and the parser size the token, node and child slices
// by estimates of the input (and grow them by doubling past them), which
// overshoot by a fifth to a third. A kept handle outlives its parse (the
// analyzer's parsed-file cache keeps every file it parsed for the life of a
// build worker), so the spare is cut where a handle is kept (TreeOnly), not
// on every parse. The tokens and hidden tokens are the tree's
// *native.GalaTokens', which nothing but the tree holds once its parse
// returned. Trimming keeps each slice's length, so Texts and HiddenTexts (set
// for a source that is not valid UTF-8, and made at their exact length) stay
// index-aligned with Tokens and Hidden.
func trimSpare(tree *native.GalaTree) {
	tree.Nodes = trimmed(tree.Nodes)
	tree.Kids = trimmed(tree.Kids)
	tree.Tokens = trimmed(tree.Tokens)
	tree.Hidden = trimmed(tree.Hidden)
	tree.TokenLabels = trimmed(tree.TokenLabels)
	tree.ErrorKids = trimmed(tree.ErrorKids)
	tree.Conjured = trimmed(tree.Conjured)
	tree.ConjuredText = trimmed(tree.ConjuredText)
}

// trimmed is s cut to its length, or s when less than an eighth of it is
// spare: copying it would cost more than the spare holds.
func trimmed[S ~[]E, E any](s S) S {
	if cap(s)-len(s) <= len(s)/8 {
		return s
	}
	return slices.Clone(s)
}
