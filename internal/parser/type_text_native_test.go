package parser

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"

	"github.com/antlr4-go/antlr/v4"
)

// checkNativeHelpers compares, on context ctx of a parse and its native node
// node, each function of type_text.go that applies to ctx with its native
// version (type_text_native.go), returning what differs or "". checkNative
// runs it on every node of the trees it checks, so TestEnginesAgree runs it on
// every GALA source of the repository; checkNative compares DocKeyOf with
// NativeDocKeyOf itself, as its check of where each node starts.
func checkNativeHelpers(n *Native, ctx antlr.ParserRuleContext, node native.GalaNodeRef) string {
	// NativeText takes its own path for a node of one token; any other is
	// the node's GetText.
	if node.StopIndex() == node.StartIndex() {
		if want, got := ctx.GetText(), NativeText(node); got != want {
			return fmt.Sprintf("GetText is %q, NativeText %q", want, got)
		}
	}
	// A helper's nil context is no node, as Node finds it.
	switch c := ctx.(type) {
	case *grammar.TypeContext:
		typ := native.TypeNode{GalaNodeRef: node}
		if want, got := TypeText(c), NativeTypeText(typ); got != want {
			return fmt.Sprintf("TypeText is %q, NativeTypeText %q", want, got)
		}
		if want, got := n.Node(PtrElem(c)), NativePtrElem(typ).GalaNodeRef; got != want {
			return fmt.Sprintf("PtrElem is node %d, NativePtrElem node %d", want.Index(), got.Index())
		}
		wantElem, wantOk := PointerElem(c)
		gotElem, gotOk := NativePointerElem(typ)
		if want := n.Node(wantElem); gotElem.GalaNodeRef != want || gotOk != wantOk {
			return fmt.Sprintf("PointerElem is (node %d, %t), NativePointerElem (node %d, %t)", want.Index(), wantOk, gotElem.Index(), gotOk)
		}
	case *grammar.TypeAliasContext:
		if want, got := AliasText(c), NativeAliasText(native.TypeAliasNode{GalaNodeRef: node}); got != want {
			return fmt.Sprintf("AliasText is %q, NativeAliasText %q", want, got)
		}
	}
	return ""
}

// nativeHelperInputs spell types every way the helpers tell apart. GALA
// names are ASCII, so the non-ASCII text is in comments and strings, between
// a type's tokens too, where the native tree's token texts, cut from the
// source by byte offsets, must still be ANTLR's (which counts code points).
// One input has CRLF line ends and a function type split across lines; the
// last is broken.
var nativeHelperInputs = []string{
	`package main

// Ünïcödé — a doc comment ✓
type Node struct {
    name string // nämé
    next Ptr[/* ünï */ Node]
    prev * /* ✓ */ Node
    tags map[string]Array[Node]
    raw []byte
    cb func(msg string, rest ...int) Option[/* 日本 */ int]
    g func(int, string) (int, error)
}

type Millis int64

type Pair Tuple[Node, std.Ptr[int]]

func (u Ptr[Node]) Name() string = u.name + "ä"

func (u *Node) Other(f func() func(a int) map[Node]int) int = 1
`,
	"package main\r\n\r\n/* ünïcödé\r\n   ✓ */\r\ntype Fn func(\r\n    a int, // ä\r\n    rest ...Ptr[Node]\r\n) (Millis, error)\r\n\r\ntype Wrapped std.Ptr[Fn]\r\n",
	// Error-recovered trees: a made-up ']' (`<missing ']'>`), a skipped ')'
	// in a type of none of TypeText's forms, types cut short.
	"package main\n\ntype Conjured map[string int\n\ntype Skipped *)\n\ntype Broken func(a int, ) map[\n\ntype Half Ptr[\n",
}

// TestNativeTypeHelpers checks the native helpers on nativeHelperInputs,
// with checkNative, and that the inputs reach the cases they are for.
func TestNativeTypeHelpers(t *testing.T) {
	var typeTexts, aliasTexts []string
	ptrElems, pointerElems := 0, 0
	for k, input := range nativeHelperInputs {
		p := parseConverted(input)
		broken := k == len(nativeHelperInputs)-1
		if (len(p.Errors) > 0) != broken {
			t.Fatalf("input %d: errors %v, want them only for the last input", k, p.Errors)
		}
		if msg := checkNative(p, treeText(p.Tree)); msg != "" {
			t.Fatalf("input %d: %s", k, msg)
		}
		tree, err := p.Native.Tree()
		if err != nil {
			t.Fatal(err)
		}
		if broken {
			// The counts below are of the inputs that parse: what error
			// recovery makes of this one is the engines' to decide. That it
			// reaches what only recovery makes: a conjured token, an error
			// token and a type of none of TypeText's forms (its default).
			defaults := 0
			tree.Root().Walk(func(n native.GalaNodeRef) bool {
				typ := native.TypeNode{GalaNodeRef: n}
				if n.Kind() == native.GalaKIND_type && !typ.QualifiedIdentifier().Ok() && !typ.FUNC().Ok() &&
					!slices.Contains([]string{"map", "[", "*"}, NativeStartText(n)) {
					defaults++
				}
				return true
			})
			if len(tree.Conjured) == 0 || len(tree.ErrorKids) == 0 || defaults == 0 {
				t.Errorf("the broken input has %d conjured tokens, %d error tokens and %d types of no form, want some of each",
					len(tree.Conjured), len(tree.ErrorKids), defaults)
			}
			continue
		}
		tree.Root().Walk(func(n native.GalaNodeRef) bool {
			switch n.Kind() {
			case native.GalaKIND_type:
				typ := native.TypeNode{GalaNodeRef: n}
				typeTexts = append(typeTexts, NativeTypeText(typ))
				if NativePtrElem(typ).Ok() {
					ptrElems++
				}
				if _, ok := NativePointerElem(typ); ok {
					pointerElems++
				}
			case native.GalaKIND_typeAlias:
				aliasTexts = append(aliasTexts, NativeAliasText(native.TypeAliasNode{GalaNodeRef: n}))
			}
			return true
		})
	}
	for _, want := range []string{
		"Ptr[Node]", "*Node", "map[string]Array[Node]", "[]byte",
		"func(string,...int)Option[int]", "func(int,string)(int,error)",
		"Tuple[Node,std.Ptr[int]]", "func()func(int)map[Node]int",
		"func(int,...Ptr[Node])(Millis,error)", "std.Ptr[Fn]",
	} {
		if !slices.Contains(typeTexts, want) {
			t.Errorf("no type spells %q; the types: %s", want, strings.Join(typeTexts, " "))
		}
	}
	for _, want := range []string{"int64", "Tuple[Node,std.Ptr[int]]", "std.Ptr[Fn]"} {
		if !slices.Contains(aliasTexts, want) {
			t.Errorf("no alias spells %q; the aliases: %s", want, strings.Join(aliasTexts, " "))
		}
	}
	// Ptr[Node] three times, std.Ptr[int], std.Ptr[Fn]; pointerElems adds
	// *Node twice.
	if ptrElems != 5 || pointerElems != 7 {
		t.Errorf("%d Ptr types and %d pointer types, want 5 and 7", ptrElems, pointerElems)
	}
}

// TestNativeDocKeyOfGalopParse checks NativeDocKeyOf on a tree galop's native
// parser makes itself, from galop's native lexer, rather than one converted
// from ANTLR's (whose tokens carry ANTLR's lines and columns): after
// non-ASCII text, a declaration's column still counts code points, as
// DocKeyOf's does, and every node starts where the converted tree's does.
func TestNativeDocKeyOfGalopParse(t *testing.T) {
	src := "package main\n\n/* ünï ✓ */ type A int\n/* 日本 */ func f(s string) int = 1\n"
	root, err := native.ParseSourceFile(native.GalaLex(src))
	if err != nil {
		t.Fatal(err)
	}
	p := parseWithGalop(src)
	converted, err := p.Native.Tree()
	if err != nil {
		t.Fatal(err)
	}
	parsed := root.Tree()
	if len(parsed.Nodes) != len(converted.Nodes) {
		t.Fatalf("galop's parse has %d nodes, the converted tree %d", len(parsed.Nodes), len(converted.Nodes))
	}
	var keys []DocKey
	for i := range parsed.Nodes {
		got, want := NativeDocKeyOf(parsed.Node(i)), NativeDocKeyOf(converted.Node(i))
		if got != want {
			t.Errorf("node %d (%s): galop's parse starts it at %v, the converted tree at %v",
				i, native.GalaRuleNames[parsed.Node(i).Rule()], got, want)
		}
		keys = append(keys, got)
	}
	// `type` after "/* ünï ✓ */ ", 12 code points and 16 bytes; `func` after
	// "/* 日本 */ ", 9 code points and 13 bytes.
	for _, want := range []DocKey{{Line: 3, Column: 12}, {Line: 4, Column: 9}} {
		if !slices.Contains(keys, want) {
			t.Errorf("no node starts at %v; the starts: %v", want, keys)
		}
	}
}

// TestNativeHelpersAllocate: NativePtrElem and NativePointerElem allocate
// nothing, on Ptr types, pointers, other generics and a func type with
// parameters and Go results; NativeTypeText spells the func type.
func TestNativeHelpersAllocate(t *testing.T) {
	p := parseWithGalop("package main\n\ntype F func(a Ptr[int], b ...*int, c std.Ptr[Ptr[int]]) (Map[string, int], error)\n")
	tree, err := p.Native.Tree()
	if err != nil {
		t.Fatal(err)
	}
	var types []native.TypeNode
	tree.Root().Walk(func(n native.GalaNodeRef) bool {
		if n.Kind() == native.GalaKIND_type {
			types = append(types, native.TypeNode{GalaNodeRef: n})
		}
		return true
	})
	if allocs := testing.AllocsPerRun(10, func() {
		for _, typ := range types {
			NativePtrElem(typ)
			NativePointerElem(typ)
		}
	}); allocs != 0 {
		t.Errorf("NativePtrElem and NativePointerElem allocate %v times", allocs)
	}
	// Walk visits parents first: types[0] is F's func type.
	if got := NativeTypeText(types[0]); got != "func(Ptr[int],...*int,std.Ptr[Ptr[int]])(Map[string,int],error)" {
		t.Fatalf("NativeTypeText is %q", got)
	}
}
