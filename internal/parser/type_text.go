package parser

import (
	"strings"

	"martianoff/gala/internal/parser/grammar"
)

// TypeText spells the type ctx parses as one string, the form
// transpiler.ParseType reads: `Array[int]`, `map[string]int`,
// `func(int, ...string) Option[int]`. It is the type's source text without
// whitespace, except that a function type's parameter names are left out.
// GetText drops the whitespace between a name and its type, so
// `func(msg string)` would read as one parameter of type `msgstring`.
func TypeText(ctx grammar.ITypeContext) string {
	var b strings.Builder
	writeTypeText(&b, ctx)
	return b.String()
}

// PtrElem returns the type argument of GALA's non-null pointer annotation
// `Ptr[T]` (or `std.Ptr[T]`), or nil when ctx is not one.
func PtrElem(ctx grammar.ITypeContext) grammar.ITypeContext {
	if ctx == nil || ctx.QualifiedIdentifier() == nil || ctx.TypeArguments() == nil {
		return nil
	}
	switch ctx.QualifiedIdentifier().GetText() {
	case "Ptr", "std.Ptr":
	default:
		return nil
	}
	list, ok := ctx.TypeArguments().TypeList().(*grammar.TypeListContext)
	if !ok || list == nil || len(list.AllType_()) != 1 {
		return nil
	}
	return list.Type_(0)
}

// PointerElem returns the element type of a pointer type annotation, `*T` or
// the non-null `Ptr[T]`, and true; for any other type it returns (ctx, false).
// A receiver is a pointer receiver exactly when its type is one of the two.
func PointerElem(ctx grammar.ITypeContext) (grammar.ITypeContext, bool) {
	if elem := PtrElem(ctx); elem != nil {
		return elem, true
	}
	if ctx != nil && ctx.GetStart() != nil && ctx.GetStart().GetText() == "*" && len(ctx.AllType_()) == 1 {
		return ctx.Type_(0), true
	}
	return ctx, false
}

// AliasText spells the target of `type X Y` as TypeText does. Its grammar is
// `identifier | type`: `type Millis int64` takes the identifier branch.
func AliasText(ctx grammar.ITypeAliasContext) string {
	if a, ok := ctx.(*grammar.TypeAliasContext); ok && a.Type_() != nil {
		return TypeText(a.Type_())
	}
	return ctx.GetText()
}

func writeTypeText(b *strings.Builder, ctx grammar.ITypeContext) {
	t, ok := ctx.(*grammar.TypeContext)
	if !ok || t == nil {
		if ctx != nil {
			b.WriteString(ctx.GetText())
		}
		return
	}
	start := t.GetStart().GetText()
	switch {
	case t.QualifiedIdentifier() != nil:
		b.WriteString(t.QualifiedIdentifier().GetText())
		if args := t.TypeArguments(); args != nil {
			b.WriteByte('[')
			if list, ok := args.TypeList().(*grammar.TypeListContext); ok && list != nil {
				for i, arg := range list.AllType_() {
					if i > 0 {
						b.WriteByte(',')
					}
					writeTypeText(b, arg)
				}
			}
			b.WriteByte(']')
		}
	case t.FUNC() != nil:
		b.WriteString("func")
		writeSignatureText(b, t.Signature())
	case start == "map":
		b.WriteString("map[")
		writeTypeText(b, t.Type_(0))
		b.WriteByte(']')
		writeTypeText(b, t.Type_(1))
	case start == "[":
		b.WriteString("[]")
		writeTypeText(b, t.Type_(0))
	case start == "*":
		b.WriteByte('*')
		writeTypeText(b, t.Type_(0))
	default:
		b.WriteString(t.GetText())
	}
}

// writeSignatureText writes a function type's `(params) result`. A parameter
// is a type, or a name and a type; a lone name is the type itself
// (`func(int)` parses its `int` as a parameter name with no type).
func writeSignatureText(b *strings.Builder, sig grammar.ISignatureContext) {
	s, ok := sig.(*grammar.SignatureContext)
	if !ok || s == nil {
		return
	}
	b.WriteByte('(')
	if params, ok := s.Parameters().(*grammar.ParametersContext); ok && params != nil {
		if list, ok := params.ParameterList().(*grammar.ParameterListContext); ok && list != nil {
			for i, p := range list.AllParameter() {
				param := p.(*grammar.ParameterContext)
				if i > 0 {
					b.WriteByte(',')
				}
				if param.ELLIPSIS() != nil {
					b.WriteString("...")
				}
				if param.Type_() != nil {
					writeTypeText(b, param.Type_())
				} else if param.Identifier() != nil {
					b.WriteString(param.Identifier().GetText())
				}
			}
		}
	}
	b.WriteByte(')')
	switch {
	case s.Type_() != nil:
		writeTypeText(b, s.Type_())
	case s.GoResults() != nil:
		b.WriteByte('(')
		if results, ok := s.GoResults().(*grammar.GoResultsContext); ok {
			for i, r := range results.AllType_() {
				if i > 0 {
					b.WriteByte(',')
				}
				writeTypeText(b, r)
			}
		}
		b.WriteByte(')')
	}
}
