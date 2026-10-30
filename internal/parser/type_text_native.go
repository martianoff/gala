package parser

import (
	"strings"

	"martianoff/gala/internal/parser/native"
)

// Each function here is the native-tree version of the function in
// type_text.go or docs.go with the name less its Native prefix: given the
// native node of a context (Native.Node), it returns what that function
// returns for the context. They read children through the rule handles, never
// through Typed, which builds a handle per node, and walk lists by index
// rather than through the All accessors, which allocate. In builds without
// galop's parser every node is no node (see internal/parser/nativeoff), and
// they return what they return for none.

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
	qid, list := t.QualifiedIdentifier(), t.TypeArguments().TypeList()
	elem := list.Type_(0)
	// The name last: reading it builds a string.
	if !qid.Ok() || !elem.Ok() || list.Type_(1).Ok() || !IsPtrTypeName(qid.GetText()) {
		return native.TypeNode{}
	}
	return elem
}

// NativePointerElem is PointerElem for the native node of a type: the element
// type of `*T` or `Ptr[T]` and true, or (t, false).
func NativePointerElem(t native.TypeNode) (native.TypeNode, bool) {
	if elem := NativePtrElem(t); elem.Ok() {
		return elem, true
	}
	if t.Ok() && nativeStartText(t.GalaNodeRef) == "*" {
		if elem := t.Type_(0); elem.Ok() && !t.Type_(1).Ok() {
			return elem, true
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

// NativeDocKeyOf is DocKeyOf for the first token of node n, the key of the
// declaration n is; the zero key for no node.
func NativeDocKeyOf(n native.GalaNodeRef) DocKey {
	if !n.Ok() {
		return DocKey{}
	}
	start := n.Start()
	return DocKey{Line: int(start.Line), Column: int(start.Column)}
}

// nativeStartText is the text of n's first token, as ANTLR's GetStart gives
// it: for a node that spans no token, the token after it.
func nativeStartText(n native.GalaNodeRef) string {
	return n.Tree().Text(n.StartIndex())
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
			list := args.TypeList()
			for i := 0; list.Type_(i).Ok(); i++ {
				if i > 0 {
					b.WriteByte(',')
				}
				writeNativeTypeText(b, list.Type_(i))
			}
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
	list := s.Parameters().ParameterList()
	for i := 0; list.Parameter(i).Ok(); i++ {
		param := list.Parameter(i)
		if i > 0 {
			b.WriteByte(',')
		}
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
		for i := 0; results.Type_(i).Ok(); i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			writeNativeTypeText(b, results.Type_(i))
		}
		b.WriteByte(')')
	}
}
