package transformer

import (
	"fmt"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// Named sub-patterns: in a pattern on a sealed variant or a struct, `Field = p`
// matches p against the field of that name, as a named argument fills it at
// construction. Positional sub-patterns come first; a field given neither way
// matches anything: `case Rect(Height = h)` is `case Rect(_, h)`.

// normalizePatternArgs returns argList with each named sub-pattern moved to
// its field's position and an empty argument, which every pattern lowering
// skips, for each field left out. A list without named sub-patterns is
// returned as it is.
func (t *galaASTTransformer) normalizePatternArgs(rawName string, argList *grammar.ArgumentListContext, matchedType transpiler.Type) (*grammar.ArgumentListContext, error) {
	if argList == nil {
		return nil, nil
	}
	args := argList.AllArgument()
	firstNamed := -1
	for i, a := range args {
		if a.(*grammar.ArgumentContext).Identifier() != nil {
			firstNamed = i
			break
		}
	}
	if firstNamed < 0 {
		return argList, nil
	}
	name := stripPackagePrefix(rawName)
	if meta := t.getTypeMeta(rawName); meta != nil && meta.IsOpaque {
		// An opaque-type pattern takes one sub-pattern and reports a named
		// one itself.
		return argList, nil
	}
	fields, ok := t.patternFieldNames(rawName, matchedType)
	if !ok {
		at := args[firstNamed].(*grammar.ArgumentContext).Identifier().GetStart()
		return nil, galaerr.NewCodedSemanticError(galaerr.CodeInvalidNamedSubPattern, at.GetLine(), at.GetColumn(),
			fmt.Sprintf("'%s' has no fields to match by name: named sub-patterns need a sealed variant or a struct", name),
			"write the sub-patterns by position")
	}
	slots := make([]grammar.IArgumentContext, len(fields))
	for i, a := range args {
		arg := a.(*grammar.ArgumentContext)
		if arg.Identifier() == nil {
			if i > firstNamed {
				at := arg.GetStart()
				return nil, galaerr.NewCodedSemanticError(galaerr.CodeInvalidNamedSubPattern, at.GetLine(), at.GetColumn(),
					"a positional sub-pattern cannot follow a named one",
					"put the positional sub-patterns first, or name this one too")
			}
			if i >= len(slots) {
				at := arg.GetStart()
				return nil, galaerr.NewCodedSemanticError(galaerr.CodeInvalidNamedSubPattern, at.GetLine(), at.GetColumn(),
					fmt.Sprintf("'%s' has %d fields, but this is sub-pattern %d", name, len(fields), i+1),
					"remove the extra sub-pattern")
			}
			slots[i] = arg
			continue
		}
		field := arg.Identifier().GetText()
		at := arg.Identifier().GetStart()
		idx := -1
		for j, f := range fields {
			if f == field {
				idx = j
			}
		}
		if idx < 0 {
			return nil, galaerr.NewCodedSemanticError(galaerr.CodeInvalidNamedSubPattern, at.GetLine(), at.GetColumn(),
				fmt.Sprintf("'%s' has no field '%s'", name, field),
				fmt.Sprintf("its fields are %s", strings.Join(fields, ", ")))
		}
		if slots[idx] != nil {
			return nil, galaerr.NewCodedSemanticError(galaerr.CodeInvalidNamedSubPattern, at.GetLine(), at.GetColumn(),
				fmt.Sprintf("field '%s' of '%s' is matched twice", field, name),
				"match each field once")
		}
		slots[idx] = arg
	}
	parent, _ := argList.GetParent().(antlr.ParserRuleContext)
	out := grammar.NewArgumentListContext(argList.GetParser(), parent, -1)
	out.SetStart(argList.GetStart())
	out.SetStop(argList.GetStop())
	for _, s := range slots {
		if s == nil {
			s = grammar.NewEmptyArgumentContext()
		}
		out.AddChild(s)
	}
	return out, nil
}

// patternFieldNames returns the field names, in order, of what a pattern
// named rawName matches: a sealed variant of matchedType or a struct whose
// pattern reads its fields. ok is false for anything else, such as an
// extractor with its own Unapply.
func (t *galaASTTransformer) patternFieldNames(rawName string, matchedType transpiler.Type) ([]string, bool) {
	if variant, _ := t.sealedVariantOfMatchedType(stripPackagePrefix(rawName), matchedType); variant != nil {
		return variant.FieldNames, true
	}
	meta := t.getTypeMeta(rawName)
	if meta == nil || meta.IsSealed || meta.IsOpaque || len(meta.FieldNames) == 0 {
		return nil, false
	}
	if _, _, hasUnapply := t.userDefinedMethodFlags(rawName); hasUnapply {
		return nil, false
	}
	return meta.FieldNames, true
}
