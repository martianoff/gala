package parser

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"martianoff/gala/internal/parser/native"
)

// checkNativeText checks NativeText on every node of tree whose span is one
// token, where it takes its own path: it must be the node's GetText. The
// parser golden holds every helper's output on every node of its inputs.
func checkNativeText(tree *native.GalaTree) string {
	for i := range tree.Nodes {
		n := tree.Node(i)
		if n.StopIndex() != n.StartIndex() {
			continue
		}
		if want, got := n.GetText(), NativeText(n); got != want {
			return fmt.Sprintf("node %d (%s): GetText is %q, NativeText %q", i, native.GalaRuleNames[n.Rule()], want, got)
		}
	}
	return ""
}

// nativeHelperInputs spell types every way the helpers tell apart. GALA
// names are ASCII, so the non-ASCII text is in comments and strings, between
// a type's tokens too, where the native tree's token texts, cut from the
// source by byte offsets, must still be whole (and positions count code
// points). One input has CRLF line ends and a function type split across
// lines; the last is broken.
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

// TestNativeTypeHelpers checks the native helpers on nativeHelperInputs
// (checkNativeText), and that the inputs reach the cases they are for.
func TestNativeTypeHelpers(t *testing.T) {
	var typeTexts, aliasTexts []string
	ptrElems, pointerElems := 0, 0
	for k, input := range nativeHelperInputs {
		p := parseGalop(t, input)
		broken := k == len(nativeHelperInputs)-1
		if (len(p.Errors) > 0) != broken {
			t.Fatalf("input %d: errors %v, want them only for the last input", k, p.Errors)
		}
		tree, err := p.RecoveredNative().Tree()
		if err != nil {
			t.Fatal(err)
		}
		if msg := checkNativeText(tree); msg != "" {
			t.Fatalf("input %d: %s", k, msg)
		}
		if broken {
			// The counts below are of the inputs that parse: what error
			// recovery makes of this one is the parser's to decide. That it
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

// TestNativeTextConjuredToken: in an error-recovered tree, a node that spans
// one stream token can also hold a conjured one (a package clause whose
// `package` keyword is missing or misspelt), and NativeText includes its
// text, as GetText does.
func TestNativeTextConjuredToken(t *testing.T) {
	for _, src := range []string{
		"main\n\nval x = 1\n",
		"packe main\n\nval x = 1\n",
		"packag\n\nval x = 1\n",
	} {
		tree, err := parseGalop(t, src).RecoveredNative().Tree()
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		if len(tree.Conjured) == 0 {
			t.Fatalf("%q: the recovered tree conjures no token", src)
		}
		if msg := checkNativeText(tree); msg != "" {
			t.Errorf("%q: %s", src, msg)
		}
	}
}

// TestNativeTextEOFChild: in an error-recovered tree, a node whose span is
// one token can also hold the EOF token, which the recovery adds as a child
// but leaves out of the span. NativeText includes its text, as GetText does.
func TestNativeTextEOFChild(t *testing.T) {
	for _, src := range []string{
		"package\n",
		"package",
		"s\"${a}\"",
		"val\n",
	} {
		tree, err := parseGalop(t, src).RecoveredNative().Tree()
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		root := tree.Root()
		if root.StopIndex() != root.StartIndex() {
			t.Fatalf("%q: the recovered source file spans tokens %d to %d, not one", src, root.StartIndex(), root.StopIndex())
		}
		if got, want := NativeText(root), root.GetText(); got != want || !strings.HasSuffix(got, "<EOF>") {
			t.Errorf("%q: NativeText is %q, GetText %q", src, got, want)
		}
		if msg := checkNativeText(tree); msg != "" {
			t.Errorf("%q: %s", src, msg)
		}
	}
}

// TestNativeDocKeyOfGalopParse checks NativeDocKeyOf on a tree galop's native
// parser makes from galop's native lexer: after non-ASCII text, a
// declaration's column still counts code points.
func TestNativeDocKeyOfGalopParse(t *testing.T) {
	src := "package main\n\n/* ünï ✓ */ type A int\n/* 日本 */ func f(s string) int = 1\n"
	root, err := native.ParseSourceFile(native.GalaLex(src))
	if err != nil {
		t.Fatal(err)
	}
	parsed := root.Tree()
	var keys []DocKey
	for i := range parsed.Nodes {
		keys = append(keys, NativeDocKeyOf(parsed.Node(i)))
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
	p := parseGalop(t, "package main\n\ntype F func(a Ptr[int], b ...*int, c std.Ptr[Ptr[int]]) (Map[string, int], error)\n")
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
