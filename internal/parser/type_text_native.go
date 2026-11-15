package parser

import (
	"iter"
	"strings"

	"martianoff/gala/internal/parser/native"
)

// The functions here read what the transpiler needs off galop's native tree:
// a type's spelling (NativeTypeText, NativeAliasText), its pointer element
// (NativePtrElem, NativePointerElem), where a declaration starts
// (NativeDocKeyOf), and a node's text and children.
//
// No node gives the empty result. They read children through the rule
// handles and NativeKids, never through Typed, which builds a handle per
// node, and walk a list in one pass without allocating: the All accessors
// allocate, and the indexed ones rescan the children from the first.

// NativeTypeText spells the type t as one string, the form
// transpiler.ParseType reads: `Array[int]`, `map[string]int`,
// `func(int, ...string) Option[int]`. It is the type's source text without
// whitespace, except that a function type's parameter names are left out:
// GetText drops the whitespace between a name and its type, so
// `func(msg string)` would read as one parameter of type `msgstring`.
func NativeTypeText(t native.TypeNode) string {
	var b strings.Builder
	writeNativeTypeText(&b, t)
	return b.String()
}

// PtrTypeName is the name of GALA's non-null pointer marker, `Ptr[T]`.
const PtrTypeName = "Ptr"

// IsPtrTypeName reports whether name, the base of a generic type `name[T]`,
// names the non-null pointer marker: `Ptr` unqualified or as `std.Ptr`.
func IsPtrTypeName(name string) bool {
	return name == PtrTypeName || name == "std."+PtrTypeName
}

// NativePtrElem is the type argument of GALA's non-null pointer annotation
// `Ptr[T]` or `std.Ptr[T]`, or no node when t is not one.
func NativePtrElem(t native.TypeNode) native.TypeNode {
	if !t.Ok() {
		return native.TypeNode{}
	}
	qid := t.QualifiedIdentifier()
	elem, ok := OnlyNativeKid(t.TypeArguments().TypeList().GalaNodeRef, native.GalaKIND_type)
	if !qid.Ok() || !ok || !isNativePtrName(qid) {
		return native.TypeNode{}
	}
	return native.TypeNode{GalaNodeRef: elem}
}

// isNativePtrName is IsPtrTypeName(qid.GetText()) without building the text
// for the common names. GetText runs qid's token texts together; the source
// from its first token to its last also has what lies between them. Where
// that source is `Ptr` or `std.Ptr` the tokens are exactly those, and where
// it does not end with `Ptr` neither does the last token, which a made-up
// token cannot be (its text, "<missing ...>", is in no name either).
func isNativePtrName(qid native.QualifiedIdentifierNode) bool {
	switch src := qid.SourceText(); {
	case IsPtrTypeName(src):
		return true
	case !strings.HasSuffix(src, PtrTypeName):
		return false
	}
	return IsPtrTypeName(qid.GetText())
}

// NativePointerElem is the element type of a pointer type annotation, `*T`
// or the non-null `Ptr[T]`, and true; for any other type it is (t, false). A
// receiver is a pointer receiver exactly when its type is one of the two.
func NativePointerElem(t native.TypeNode) (native.TypeNode, bool) {
	if elem := NativePtrElem(t); elem.Ok() {
		return elem, true
	}
	if t.Ok() && NativeStartText(t.GalaNodeRef) == "*" {
		if elem, ok := OnlyNativeKid(t.GalaNodeRef, native.GalaKIND_type); ok {
			return native.TypeNode{GalaNodeRef: elem}, true
		}
	}
	return t, false
}

// NativeAliasText spells the target of `type X Y` as NativeTypeText does. Its
// grammar is `identifier | type`: `type Millis int64` takes the identifier
// branch.
func NativeAliasText(a native.TypeAliasNode) string {
	if !a.Ok() {
		return ""
	}
	if typ := a.Type_(); typ.Ok() {
		return NativeTypeText(typ)
	}
	return a.GetText()
}

// NativeDocKeyOf is where node n's first token starts: the key of the
// declaration n is (see DocKey). A node that spans no token has no first
// token (the parser starts it at the token after the ones before it, which
// may be past the last), so it, like no node, gives the zero key; a
// declaration always spans tokens.
func NativeDocKeyOf(n native.GalaNodeRef) DocKey {
	if !n.Ok() || n.StopIndex() < n.StartIndex() {
		return DocKey{}
	}
	start := n.Start()
	return DocKey{Line: int(start.Line), Column: int(start.Column)}
}

// NativeStartText is the text of the token n starts at, which for a node that
// spans no token is the token after it. Past the last token it is "": that
// token is none of the ones writeNativeTypeText and NativePointerElem look
// for.
func NativeStartText(n native.GalaNodeRef) string {
	tree, i := n.Tree(), n.StartIndex()
	if i >= len(tree.Tokens) {
		return ""
	}
	return tree.Text(i)
}

// NativeText is GetText of n without building a string where it can: a node
// of one token, an identifier's, is that token's text, a slice of the source.
//
// A node's span (StartIndex to StopIndex) does not always hold every token
// under it. In an error-recovered tree a node can hold a conjured token
// ("<missing 'package'>"), which sits past the stream's tokens, or the EOF
// token, which the recovery adds as a child but leaves out of the span. So a
// node takes the fast path only when it is a chain of single
// children down to the one token its span starts at.
func NativeText(n native.GalaNodeRef) string {
	if i := n.StartIndex(); n.StopIndex() == i {
		if j, ok := soleToken(n); ok && j == i {
			return n.Tree().Text(i)
		}
	}
	return n.GetText()
}

// soleToken is the index of the one token under n, and whether n holds
// exactly one: each node from n down has a single child, the last a token.
func soleToken(n native.GalaNodeRef) (int, bool) {
	for n.ChildCount() == 1 {
		c := n.Child(0)
		if c.IsToken() {
			return c.Terminal().Index(), !c.IsError()
		}
		n = c.Node()
	}
	return 0, false
}

// NativeKids is n's child nodes of kind, in order, in one pass over its
// children and without allocating (the All accessors allocate). The transpiler's
// native-tree readers walk lists with it.
func NativeKids(n native.GalaNodeRef, kind native.GalaKind) iter.Seq[native.GalaNodeRef] {
	return func(yield func(native.GalaNodeRef) bool) {
		if !n.Ok() {
			return
		}
		for i := range n.ChildCount() {
			if c := n.Child(i).Node(); c.Ok() && c.Kind() == kind && !yield(c) {
				return
			}
		}
	}
}

// OnlyNativeKid is n's child node of kind and true when it has exactly one.
func OnlyNativeKid(n native.GalaNodeRef, kind native.GalaKind) (native.GalaNodeRef, bool) {
	var only native.GalaNodeRef
	count := 0
	for c := range NativeKids(n, kind) {
		if count++; count > 1 {
			return native.GalaNodeRef{}, false
		}
		only = c
	}
	return only, count == 1
}

// writeNativeTypeList writes n's child types separated by commas.
func writeNativeTypeList(b *strings.Builder, n native.GalaNodeRef) {
	first := true
	for typ := range NativeKids(n, native.GalaKIND_type) {
		if !first {
			b.WriteByte(',')
		}
		first = false
		writeNativeTypeText(b, native.TypeNode{GalaNodeRef: typ})
	}
}

func writeNativeTypeText(b *strings.Builder, t native.TypeNode) {
	if !t.Ok() {
		return
	}
	start := NativeStartText(t.GalaNodeRef)
	switch qid := t.QualifiedIdentifier(); {
	case qid.Ok():
		b.WriteString(qid.GetText())
		if args := t.TypeArguments(); args.Ok() {
			b.WriteByte('[')
			writeNativeTypeList(b, args.TypeList().GalaNodeRef)
			b.WriteByte(']')
		}
	case t.FUNC().Ok():
		b.WriteString("func")
		writeNativeSignatureText(b, t.Signature())
	case start == "map":
		b.WriteString("map[")
		writeNativeTypeText(b, t.Type_(0))
		b.WriteByte(']')
		writeNativeTypeText(b, t.Type_(1))
	case start == "[":
		b.WriteString("[]")
		writeNativeTypeText(b, t.Type_(0))
	case start == "*":
		b.WriteByte('*')
		writeNativeTypeText(b, t.Type_(0))
	default:
		b.WriteString(t.GetText())
	}
}

// writeNativeSignatureText writes the function type signature s as
// `(params) result`. A parameter is a type, or a name and a type; a lone
// name is the type itself (`func(int)` parses its `int` as a parameter name
// with no type).
func writeNativeSignatureText(b *strings.Builder, s native.SignatureNode) {
	if !s.Ok() {
		return
	}
	b.WriteByte('(')
	first := true
	for p := range NativeKids(s.Parameters().ParameterList().GalaNodeRef, native.GalaKIND_parameter) {
		if !first {
			b.WriteByte(',')
		}
		first = false
		param := native.ParameterNode{GalaNodeRef: p}
		if param.ELLIPSIS().Ok() {
			b.WriteString("...")
		}
		// An `inout T` parameter is written `inout Ptr[T]`: the Ptr[T] it lowers
		// to, marked inout (transpiler.InoutPrefix).
		inout := param.INOUT().Ok()
		if inout {
			b.WriteString("inout " + PtrTypeName + "[")
		}
		if typ := param.Type_(); typ.Ok() {
			writeNativeTypeText(b, typ)
		} else if id := param.Identifier(); id.Ok() {
			b.WriteString(id.GetText())
		}
		if inout {
			b.WriteByte(']')
		}
	}
	b.WriteByte(')')
	switch typ, results := s.Type_(), s.GoResults(); {
	case typ.Ok():
		writeNativeTypeText(b, typ)
	case results.Ok():
		b.WriteByte('(')
		writeNativeTypeList(b, results.GalaNodeRef)
		b.WriteByte(')')
	}
}
