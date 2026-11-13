package parser

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"

	"github.com/antlr4-go/antlr/v4"
)

// errNoNative is what a nil *Native reports: the parse gave no tree, it
// reported syntax errors (see Parsed.Native), or the holder dropped the
// handle.
var errNoNative = errors.New("parser: no native tree for this parse")

// Native is a parse's tree in galop's native form (internal/parser/native).
// galop's native parser builds it directly (nativeHandle); a tree of ANTLR's
// parser, with ANTLR's engine or after a galop panic, is converted the
// first time it is asked for (newNative). A Native belongs to one parse and
// converts at most once. A nil *Native is valid and has no tree: Tree reports
// errNoNative.
type Native struct {
	once sync.Once
	// converted is set once Tree has its tree (see Converted).
	converted atomic.Bool
	// trimmed is TreeOnly's trim of the tree (trimSpare), done once.
	trimmed sync.Once
	// root, stream and src are what the conversion reads, with toNative,
	// galop's converter. The ANTLR tree keeps its tokens, their lexer and its
	// input alive, but reaches the token stream only through the recognition
	// exceptions of error-recovered contexts, and never the source text. So
	// until the conversion the handle is what holds the stream (its token
	// slice, hidden tokens included) and the text. Converting lets all three
	// go; the text stays, as the native tree keeps it (its tokens point into
	// it).
	root     antlr.ParserRuleContext
	stream   *antlr.CommonTokenStream
	src      string
	toNative func(antlr.ParserRuleContext, *antlr.CommonTokenStream, string) *native.GalaTree

	tree *native.GalaTree
	err  error
}

// newNative returns the native-tree handle of root, an ANTLR tree read from
// stream over src, converted by toNative on the first call to Tree, or nil
// when there is no source-file tree.
func newNative(root antlr.Tree, stream *antlr.CommonTokenStream, src string,
	toNative func(antlr.ParserRuleContext, *antlr.CommonTokenStream, string) *native.GalaTree) *Native {
	sf, ok := root.(*grammar.SourceFileContext)
	if !ok || sf == nil {
		return nil
	}
	return &Native{root: sf, stream: stream, src: src, toNative: toNative}
}

// nativeHandle returns the handle of tree, which galop's native parser built.
func nativeHandle(tree *native.GalaTree) *Native {
	t := &Native{tree: tree}
	t.once.Do(func() {})
	t.converted.Store(true)
	return t
}

// Tree is the native tree: galop's native parse, or ANTLR's tree converted on
// the first call, once, safely from any goroutine. An error says there is
// none: see errNoNative, or the conversion panicked (a bug in galop's
// converter).
func (t *Native) Tree() (*native.GalaTree, error) {
	if t == nil {
		return nil, errNoNative
	}
	t.once.Do(t.convert)
	return t.tree, t.err
}

// Converted reports whether the tree is there already: always for galop's
// native parse, and for ANTLR's tree once Tree converted it. A nil *Native
// has not converted.
func (t *Native) Converted() bool {
	return t != nil && t.converted.Load()
}

// Root is the root of the native tree, converting it if nothing has yet; no
// node and false when there is none (see Tree).
func (t *Native) Root() (native.GalaNodeRef, bool) {
	tree, err := t.Tree()
	if err != nil {
		return native.GalaNodeRef{}, false
	}
	return tree.Root(), true
}

// TreeOnly is t once its tree is there (converting it if nothing has yet),
// nil when there is none: the handle for a holder that keeps it long. A
// converted handle holds nothing of ANTLR's parse (its conversion drops the
// ANTLR tree, the token stream and the text), and the first call cuts the
// spare capacity off the tree's slices (trimSpare), so the holder keeps
// neither. That replaces the slices under the tree's readers: call it before
// the tree is read on any other goroutine. Later calls change nothing.
func (t *Native) TreeOnly() *Native {
	tree, err := t.Tree()
	if err != nil {
		return nil
	}
	t.trimmed.Do(func() { trimSpare(tree) })
	return t
}

// trimSpare cuts tree's slices to their length where much of them is spare
// capacity. The lexer, the parser and the converter size the token, node and
// child slices by estimates of the input (and grow them by doubling past
// them), which overshoot by a fifth to a third. A kept handle outlives its
// parse (the analyzer's parsed-file cache keeps every file it parsed for the
// life of a build worker), so the spare is cut where a handle is kept
// (TreeOnly), not on every parse. The tokens and hidden tokens are the
// tree's *native.GalaTokens', which nothing but the tree holds once its
// parse returned.
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

func (t *Native) convert() {
	defer func() {
		if r := recover(); r != nil {
			t.tree, t.err = nil, fmt.Errorf("parser: converting the tree to galop's native tree panicked: %v", r)
		}
		// The native tree keeps the text it was given; the handle needs no
		// ANTLR tree, stream, text or converter any more, and after a
		// failure nothing does.
		t.root, t.stream, t.src, t.toNative = nil, nil, "", nil
		t.converted.Store(true)
	}()
	// The converter is given the text as ANTLR read it (antlrText).
	if t.tree = t.toNative(t.root, t.stream, antlrText(t.src)); t.tree == nil {
		t.err = errNoNative
	}
}
