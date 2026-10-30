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

// This file is built only with galop's parser (see BUILD.bazel): it reads the
// native tree.

// checkNativeHelpers compares, on context ctx of a parse and its native node
// node, each function of type_text.go and docs.go that applies to ctx with its
// native version (type_text_native.go), returning what differs or "".
// checkNative runs it on every node of the trees it checks, so TestEnginesAgree
// runs it on every GALA source of the repository.
func checkNativeHelpers(n *Native, ctx antlr.ParserRuleContext, node native.GalaNodeRef) string {
	if want, got := DocKeyOf(ctx.GetStart()), NativeDocKeyOf(node); got != want {
		return fmt.Sprintf("DocKeyOf is %v, NativeDocKeyOf %v", want, got)
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
// a type's tokens too: native offsets count bytes, ANTLR's code points. One
// input has CRLF line ends and a function type split across lines.
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
	// Error-recovered trees: types cut short.
	"package main\n\ntype Broken func(a int, ) map[\n\ntype Half Ptr[\n",
}

// TestNativeTypeHelpers checks the native helpers on nativeHelperInputs,
// with checkNative, and that the inputs reach the cases they are for.
func TestNativeTypeHelpers(t *testing.T) {
	var typeTexts, aliasTexts []string
	ptrElems, pointerElems := 0, 0
	for k, input := range nativeHelperInputs {
		p := NewAntlrGalaParser().ParseFile(input)
		broken := k == len(nativeHelperInputs)-1
		if (len(p.Errors) > 0) != broken {
			t.Fatalf("input %d: errors %v, want them only for the last input", k, p.Errors)
		}
		if msg := checkNative(p, treeText(p.Tree)); msg != "" {
			t.Fatalf("input %d: %s", k, msg)
		}
		if broken {
			// What error recovery makes of it is the engines' to decide;
			// checkNative has compared the helpers on it.
			continue
		}
		tree, err := p.Native.Tree()
		if err != nil {
			t.Fatal(err)
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
