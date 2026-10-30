package parser

import (
	"iter"
	"strings"

	"martianoff/gala/internal/parser/native"
)

// The functions here read galop's native tree as type_text.go and docs.go
// read ANTLR's:
//
//   - NativeTypeText, NativePtrElem, NativePointerElem and NativeAliasText
//     take the native node of a context (Native.Node) and return what TypeText,
//     PtrElem, PointerElem and AliasText return for the context, a returned
//     context as its native node.
//   - NativeDocKeyOf takes a node where DocKeyOf takes a token: it is DocKeyOf
//     of the node's first token.
//
// No node gives the empty result. They read children through the rule
// handles and nativeKids, never through Typed, which builds a handle per
// node, and walk a list in one pass without allocating: the All accessors
// allocate, and the indexed ones rescan the children from the first. In builds
// without galop's parser every node is no node (see internal/parser/nativeoff).

// NativeTypeText is TypeText for the native node of a type.
func NativeTypeText(t native.TypeNode) string {
	var b strings.Builder
	writeNativeTypeText(&b, t)
	return b.String()
}

// NativePtrElem is PtrElem for the native node of a type: the type argument
// of `Ptr[T]` or `std.Ptr[T]`, or no node.
func NativePtrElem(t native.TypeNode) native.TypeNode {
	if !t.Ok() {
		return native.TypeNode{}
	}
	qid := t.QualifiedIdentifier()
	elem, ok := onlyNativeKid(t.TypeArguments().TypeList().GalaNodeRef, native.GalaKIND_type)
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

// NativePointerElem is PointerElem for the native node of a type: the element
// type of `*T` or `Ptr[T]` and true, or (t, false).
func NativePointerElem(t native.TypeNode) (native.TypeNode, bool) {
	if elem := NativePtrElem(t); elem.Ok() {
		return elem, true
	}
	if t.Ok() && nativeStartText(t.GalaNodeRef) == "*" {
		if elem, ok := onlyNativeKid(t.GalaNodeRef, native.GalaKIND_type); ok {
			return native.TypeNode{GalaNodeRef: elem}, true
		}
	}
	return t, false
}

// NativeAliasText is AliasText for the native node of a typeAlias.
func NativeAliasText(a native.TypeAliasNode) string {
	if !a.Ok() {
		return ""
	}
	if typ := a.Type_(); typ.Ok() {
		return NativeTypeText(typ)
	}
	return a.GetText()
}

// NativeDocKeyOf is DocKeyOf of node n's first token: the key of the
// declaration n is. A node that spans no token has no first token (the
// converter starts it at the token after the ones before it, which may be
// past the last), so it, like no node, gives the zero key; a declaration
// always spans tokens.
func NativeDocKeyOf(n native.GalaNodeRef) DocKey {
	if !n.Ok() || n.StopIndex() < n.StartIndex() {
		return DocKey{}
	}
	start := n.Start()
	return DocKey{Line: int(start.Line), Column: int(start.Column)}
}

// nativeStartText has no ANTLR counterpart: it is the text of the token
// ANTLR's GetStart gives for n's context, which for a node that spans no
// token is the token after it. Past the last token it is "": that token is
// none of the ones writeNativeTypeText and NativePointerElem look for.
func nativeStartText(n native.GalaNodeRef) string {
	tree, i := n.Tree(), n.StartIndex()
	if i >= len(tree.Tokens) {
		return ""
	}
	return tree.Text(i)
}

// nativeKids is n's child nodes of kind, in order, in one pass over its
// children.
func nativeKids(n native.GalaNodeRef, kind native.GalaKind) iter.Seq[native.GalaNodeRef] {
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

// onlyNativeKid is n's child node of kind and true when it has exactly one.
func onlyNativeKid(n native.GalaNodeRef, kind native.GalaKind) (native.GalaNodeRef, bool) {
	var only native.GalaNodeRef
	count := 0
	for c := range nativeKids(n, kind) {
		if count++; count > 1 {
			return native.GalaNodeRef{}, false
		}
		only = c
	}
	return only, count == 1
}

// writeNativeTypeList writes n's child types as writeTypeText writes a list
// of types: separated by commas.
func writeNativeTypeList(b *strings.Builder, n native.GalaNodeRef) {
	first := true
	for typ := range nativeKids(n, native.GalaKIND_type) {
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
	start := nativeStartText(t.GalaNodeRef)
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

// writeNativeSignatureText is writeSignatureText for the native node of a
// signature.
func writeNativeSignatureText(b *strings.Builder, s native.SignatureNode) {
	if !s.Ok() {
		return
	}
	b.WriteByte('(')
	first := true
	for p := range nativeKids(s.Parameters().ParameterList().GalaNodeRef, native.GalaKIND_parameter) {
		if !first {
			b.WriteByte(',')
		}
		first = false
		param := native.ParameterNode{GalaNodeRef: p}
		if param.ELLIPSIS().Ok() {
			b.WriteString("...")
		}
		if typ := param.Type_(); typ.Ok() {
			writeNativeTypeText(b, typ)
		} else if id := param.Identifier(); id.Ok() {
			b.WriteString(id.GetText())
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
