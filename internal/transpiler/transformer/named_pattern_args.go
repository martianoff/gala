package transformer

import (
	"fmt"
	"slices"
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

// normalizedArgs is the result of normalizePatternArgs for one argument list.
type normalizedArgs struct {
	list *grammar.ArgumentListContext
	err  error
}

// normalizePatternArgs returns argList with each named sub-pattern moved to
// its field's position and an empty argument, which every pattern lowering
// skips, for each field left out. A list without named sub-patterns is
// returned as it is. The result is computed once per list: lowering, the
// variant arity check and exhaustiveness all read it.
func (t *galaASTTransformer) normalizePatternArgs(rawName string, argList *grammar.ArgumentListContext, matchedType transpiler.Type) (*grammar.ArgumentListContext, error) {
	firstNamed := firstNamedArg(argList)
	if firstNamed < 0 {
		return argList, nil
	}
	if r, ok := t.namedArgs[argList]; ok {
		return r.list, r.err
	}
	list, err := t.reorderNamedArgs(rawName, argList, firstNamed, matchedType)
	if t.namedArgs == nil {
		t.namedArgs = make(map[*grammar.ArgumentListContext]normalizedArgs)
	}
	t.namedArgs[argList] = normalizedArgs{list: list, err: err}
	return list, err
}

// firstNamedArg returns the index of the first named sub-pattern of argList,
// or -1.
func firstNamedArg(argList *grammar.ArgumentListContext) int {
	if argList == nil {
		return -1
	}
	return slices.IndexFunc(argList.AllArgument(), func(a grammar.IArgumentContext) bool {
		return a.(*grammar.ArgumentContext).Identifier() != nil
	})
}

// noFieldNamesError reports named sub-patterns on a pattern whose extractor
// result has no field names.
func noFieldNamesError(argList *grammar.ArgumentListContext, firstNamed int, name, why string) error {
	at := argList.Argument(firstNamed).(*grammar.ArgumentContext).Identifier().GetStart()
	return galaerr.NewCodedSemanticError(galaerr.CodeInvalidNamedSubPattern, at.GetLine(), at.GetColumn(),
		fmt.Sprintf("'%s' cannot be matched by field name: %s", name, why),
		"write the sub-patterns by position")
}

// reorderNamedArgs builds the normalized list of normalizePatternArgs.
func (t *galaASTTransformer) reorderNamedArgs(rawName string, argList *grammar.ArgumentListContext, firstNamed int, matchedType transpiler.Type) (*grammar.ArgumentListContext, error) {
	fail := func(at antlr.Token, msg, hint string) error {
		return galaerr.NewCodedSemanticError(galaerr.CodeInvalidNamedSubPattern, at.GetLine(), at.GetColumn(), msg, hint)
	}
	name := stripPackagePrefix(rawName)
	var fields []string
	if variant, _ := t.sealedVariantOfMatchedType(name, matchedType); variant != nil {
		fields = variant.FieldNames
	} else if meta := t.getTypeMeta(rawName); meta != nil && meta.IsOpaque {
		// An opaque-type pattern takes one sub-pattern and reports a named
		// one itself.
		return argList, nil
	} else if fields = t.structPatternFields(rawName); fields == nil {
		why := "it is an extractor whose result has no field names"
		if meta != nil && !meta.IsSealed && len(meta.FieldNames) > 0 {
			why = "its pattern calls its own Unapply or matches elements, not fields"
		}
		return nil, noFieldNamesError(argList, firstNamed, name, why)
	}
	slots := make([]grammar.IArgumentContext, len(fields))
	for i, a := range argList.AllArgument() {
		arg := a.(*grammar.ArgumentContext)
		if arg.Identifier() == nil {
			switch {
			case i > firstNamed:
				return nil, fail(arg.GetStart(), "a positional sub-pattern cannot follow a named one",
					"put the positional sub-patterns first, or name this one too")
			case i >= len(slots):
				return nil, fail(arg.GetStart(), fmt.Sprintf("'%s' has %d fields, but this is sub-pattern %d", name, len(fields), i+1),
					"remove the extra sub-pattern")
			}
			slots[i] = arg
			continue
		}
		field := arg.Identifier().GetText()
		at := arg.Identifier().GetStart()
		idx := slices.Index(fields, field)
		switch {
		case idx < 0:
			return nil, fail(at, fmt.Sprintf("'%s' has no field '%s'", name, field),
				fmt.Sprintf("its fields are %s", strings.Join(fields, ", ")))
		case slots[idx] != nil:
			return nil, fail(at, fmt.Sprintf("field '%s' of '%s' is matched twice", field, name), "match each field once")
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

// structPatternFields returns the field names, in order, of the struct named
// name when a pattern on it reads its fields, or nil: for a sealed or opaque
// type, a sequence (whose pattern matches elements), or a struct with a
// hand-written Unapply.
func (t *galaASTTransformer) structPatternFields(name string) []string {
	meta := t.getTypeMeta(name)
	if meta == nil || meta.IsSealed || meta.IsOpaque || len(meta.FieldNames) == 0 {
		return nil
	}
	if t.isSeqType(transpiler.BasicType{Name: name}) {
		return nil
	}
	if _, _, hasUnapply := t.userDefinedMethodFlags(name); hasUnapply {
		return nil
	}
	return meta.FieldNames
}
