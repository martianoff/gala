package parser

import "martianoff/gala/internal/parser/native"

// Misbridge converts n's tree and gives its first node of kind the root's
// context, another rule's, so that n's bridge no longer holds, as a bug in
// the converter would leave it. It reports whether the tree has such a node.
// It must run before n's first Bridged, which settles once.
func Misbridge(n *Native, kind native.GalaKind) bool {
	tree, err := n.Tree()
	if err != nil {
		return false
	}
	for i := range tree.Nodes {
		if tree.Node(i).Kind() == kind {
			n.ctxs[i] = n.ctxs[tree.Root().Index()]
			return true
		}
	}
	return false
}
