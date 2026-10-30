package parser

import (
	"errors"
	"fmt"
	"sync"

	"martianoff/gala/internal/parser/galop"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"

	"github.com/antlr4-go/antlr/v4"
)

// errNoNative is what a nil *Native reports: the parse gave no tree, ANTLR's
// parser made it (see Parsed.Native), or the holder dropped the handle.
var errNoNative = errors.New("parser: no native tree for this parse")

// Native is a parse's tree in galop's native form (internal/parser/native),
// converted the first time it is asked for, with the bridge between it and
// the ANTLR tree. While the tree's consumers move to the native tree one by
// one, code on either side crosses over with Node and Ctx. A Native belongs
// to one parse and converts at most once. A nil *Native is valid and has no
// tree: Tree reports errNoNative, Ctx and Node find nothing.
type Native struct {
	once sync.Once
	root antlr.ParserRuleContext
	// stream and src are what the conversion reads. The ANTLR tree keeps its
	// tokens, their lexer and its input alive, but reaches the token stream
	// only through the recognition exceptions of error-recovered contexts, and
	// never the source text. So until the conversion the handle is what holds
	// the stream (its token slice, hidden tokens included) and the text.
	// Converting lets the stream go; the text stays, as the native tree keeps
	// it (its tokens point into it).
	stream *antlr.CommonTokenStream
	src    string

	tree *native.GalaTree
	err  error
	// ctxs is the ANTLR context of each native node, by node index.
	ctxs []antlr.ParserRuleContext
	// nodes, built on the first call to Node, maps a context to its node.
	nodesOnce sync.Once
	nodes     map[antlr.ParserRuleContext]int32
}

// newNative returns the native-tree handle of root, read from stream over
// src, or nil when there is no source-file tree.
func newNative(root antlr.Tree, stream *antlr.CommonTokenStream, src string) *Native {
	sf, ok := root.(*grammar.SourceFileContext)
	if !ok || sf == nil {
		return nil
	}
	return &Native{root: sf, stream: stream, src: src}
}

// Tree is the native tree, converted from the ANTLR tree on the first call,
// once, safely from any goroutine. An error says there is none: see
// errNoNative, or the conversion panicked (a bug in galop's converter); the
// caller then works on the ANTLR tree.
func (t *Native) Tree() (*native.GalaTree, error) {
	if t == nil {
		return nil, errNoNative
	}
	t.once.Do(t.convert)
	return t.tree, t.err
}

func (t *Native) convert() {
	defer func() {
		if r := recover(); r != nil {
			t.tree, t.ctxs, t.err = nil, nil, fmt.Errorf("parser: converting the tree to galop's native tree panicked: %v", r)
		}
		// The native tree keeps the text it was given; the handle needs
		// neither any more, and after a failure nothing does.
		t.stream, t.src = nil, ""
	}()
	if t.tree, t.ctxs = galop.ToNative(t.root, t.stream, t.src); t.tree == nil {
		t.err = errNoNative
	}
}

// Ctx is the ANTLR context of native node n, or nil when n is no node of this
// tree.
func (t *Native) Ctx(n native.GalaNodeRef) antlr.ParserRuleContext {
	if tree, err := t.Tree(); err != nil || !n.Ok() || n.Tree() != tree || n.Index() >= len(t.ctxs) {
		return nil
	}
	return t.ctxs[n.Index()]
}

// Node is the native node of ctx, or no node when ctx is not in the tree.
func (t *Native) Node(ctx antlr.ParserRuleContext) native.GalaNodeRef {
	tree, err := t.Tree()
	if err != nil {
		return native.GalaNodeRef{}
	}
	t.nodesOnce.Do(func() {
		t.nodes = make(map[antlr.ParserRuleContext]int32, len(t.ctxs))
		for i, c := range t.ctxs {
			t.nodes[c] = int32(i)
		}
	})
	if i, ok := t.nodes[ctx]; ok {
		return tree.Node(int(i))
	}
	return native.GalaNodeRef{}
}
