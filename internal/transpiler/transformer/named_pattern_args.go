package transformer

import (
	"fmt"
	"slices"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// Named sub-patterns: in a pattern on a sealed variant or a struct, `Field = p`
// matches p against the field of that name, as a named argument fills it at
// construction. Positional sub-patterns come first; a field given neither way
// matches anything: `case Rect(Height = h)` is `case Rect(_, h)`.

// normalizePatternArgs returns args with each named sub-pattern moved to its
// field's position and an empty argument, which every pattern lowering
// skips, for each field left out. A list without named sub-patterns is
// returned as it is. Lowering, the variant arity check and exhaustiveness
// all read the normalized list.
func (t *galaASTTransformer) normalizePatternArgs(rawName string, args patArgs, matchedType transpiler.Type) (patArgs, error) {
	firstNamed := firstNamedPatArg(args)
	if firstNamed < 0 {
		return args, nil
	}
	return t.reorderNamedArgs(rawName, args, firstNamed, matchedType)
}

// firstNamedPatArg returns the index of the first named sub-pattern of args,
// or -1.
func firstNamedPatArg(args patArgs) int {
	return slices.IndexFunc(args.list, func(a argSrc) bool {
		name, _ := a.name()
		return name != ""
	})
}

// namedSubPatternError is GALA-E0073 at at.
func namedSubPatternError(at srcNode, msg, hint string) error {
	line, col := at.start()
	return galaerr.NewCodedSemanticError(galaerr.CodeInvalidNamedSubPattern, line, col, msg, hint)
}

// noFieldNamesError reports named sub-patterns on a pattern that has no
// fields to match by name.
func noFieldNamesError(args patArgs, firstNamed int, name, why string) error {
	_, at := args.list[firstNamed].name()
	return namedSubPatternError(at, fmt.Sprintf("'%s' cannot be matched by field name: %s", name, why),
		"write the sub-patterns by position")
}

// reorderNamedArgs builds the normalized list of normalizePatternArgs.
func (t *galaASTTransformer) reorderNamedArgs(rawName string, args patArgs, firstNamed int, matchedType transpiler.Type) (patArgs, error) {
	name := stripPackagePrefix(rawName)
	var fields []string
	if variant, _ := t.sealedVariantOfMatchedType(name, matchedType); variant != nil {
		fields = variant.FieldNames
	} else if meta := t.getTypeMeta(rawName); meta != nil && meta.IsOpaque {
		// An opaque-type pattern takes one sub-pattern and reports a named
		// one itself.
		return args, nil
	} else if fields = t.structPatternFields(rawName); fields == nil {
		why := "it is an extractor whose result has no field names"
		if parent := t.sealedParentOfVariant(rawName); parent != "" {
			why = fmt.Sprintf("it is a variant of %s, and the value matched is not a %s", parent, parent)
		} else if meta != nil && !meta.IsSealed && len(meta.FieldNames) > 0 {
			why = "its pattern calls its own Unapply or matches elements, not fields"
		}
		return patArgs{}, noFieldNamesError(args, firstNamed, name, why)
	}
	slots := make([]argSrc, len(fields))
	filled := make([]bool, len(fields))
	for i, arg := range args.list {
		field, at := arg.name()
		if field == "" {
			switch {
			case i > firstNamed:
				return patArgs{}, namedSubPatternError(arg.at(), "a positional sub-pattern cannot follow a named one",
					"put the positional sub-patterns first, or name this one too")
			case i >= len(slots):
				return patArgs{}, namedSubPatternError(arg.at(), fmt.Sprintf("'%s' has %d fields, but this is sub-pattern %d", name, len(fields), i+1),
					"remove the extra sub-pattern")
			}
			slots[i], filled[i] = arg, true
			continue
		}
		if !arg.pattern().ok() {
			return patArgs{}, namedSubPatternError(at, fmt.Sprintf("field '%s' is given a lambda, which is not a pattern", field),
				"match the field with a pattern, such as `_` or a name to bind")
		}
		idx := slices.Index(fields, field)
		switch {
		case idx < 0:
			return patArgs{}, namedSubPatternError(at, fmt.Sprintf("'%s' has no field '%s'", name, field),
				fmt.Sprintf("its fields are %s", strings.Join(fields, ", ")))
		case filled[idx]:
			return patArgs{}, namedSubPatternError(at, fmt.Sprintf("field '%s' of '%s' is matched twice", field, name), "match each field once")
		}
		slots[idx], filled[idx] = arg, true
	}
	// A slot left out stays the zero argSrc: no name and no pattern.
	return patArgs{list: slots, at: args.at}, nil
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

// sealedParentOfVariant returns the sealed type a variant companion named
// name extracts from, or "".
func (t *galaASTTransformer) sealedParentOfVariant(name string) string {
	meta := t.getTypeMeta(name)
	if meta == nil {
		return ""
	}
	unapply, ok := meta.Methods["Unapply"]
	if !ok || len(unapply.ParamTypes) != 1 || unapply.ParamTypes[0] == nil {
		return ""
	}
	parentName := unapply.ParamTypes[0].BaseName()
	if parent := t.getTypeMeta(parentName); parent != nil && parent.IsSealed {
		return stripPackagePrefix(parentName)
	}
	return ""
}
