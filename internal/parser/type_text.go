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

func writeTypeText(b *strings.Builder, ctx grammar.ITypeContext) {
	t, ok := ctx.(*grammar.TypeContext)
	if !ok || t == nil {
		if ctx != nil {
			b.WriteString(ctx.GetText())
		}
		return
	}
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
	case t.GetStart().GetText() == "map":
		b.WriteString("map[")
		writeTypeText(b, t.Type_(0))
		b.WriteByte(']')
		writeTypeText(b, t.Type_(1))
	case t.GetStart().GetText() == "[":
		b.WriteString("[]")
		writeTypeText(b, t.Type_(0))
	case t.GetStart().GetText() == "*":
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
