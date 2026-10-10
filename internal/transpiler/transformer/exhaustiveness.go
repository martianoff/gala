package transformer

import (
	"go/types"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// Exhaustiveness of a match over a sealed type or bool, decided on a pattern
// matrix: one row per unguarded case, one column per value being matched. A
// variant counts as covered only when the patterns written for it cover every
// value of its fields together, so `case Some(0)` covers part of `Some`, while
// `Some(Some(_))` and `Some(None())` together cover all of `Option[Option[T]]`'s
// `Some`. A guarded case covers nothing: its guard may be false.

// covCell is one pattern of a row. nil matches every value; covRefutable
// matches some values and names no constructor.
type covCell = grammar.IExpressionContext

// covRefutable stands for a pattern that is neither a wildcard nor a
// constructor, such as a typed pattern of a narrower type.
var covRefutable covCell = grammar.NewEmptyExpressionContext()

// covCtor is one constructor of an enumerable type and its field types.
type covCtor struct {
	name       string
	fields     []transpiler.Type
	structural bool // a struct's single constructor, not a variant
}

// tupleCtorName names the single constructor of a tuple type.
const tupleCtorName = "()"

// coverage holds the constructors computed for each type during one check.
type coverage struct {
	t     *galaASTTransformer
	ctors map[string][]covCtor
}

// coverageOf decides whether the unguarded case patterns cover every value
// of matchedType. enumerable is false when matchedType is neither a sealed
// type nor bool. missing names each constructor that is not fully covered:
// `Rect` when no case matches it at all, `Some(...)` when cases match only
// some of its values. guardedMissing reports whether a guarded case names one
// of the missing constructors.
func (t *galaASTTransformer) coverageOf(matchedType transpiler.Type, clauses []*grammar.CaseClauseContext) (enumerable, exhaustive bool, missing []string, guardedMissing bool) {
	c := &coverage{t: t, ctors: map[string][]covCtor{}}
	ctors, ok := c.constructors(matchedType, false)
	if !ok {
		return false, false, nil, false
	}
	var rows, guarded [][]covCell
	for _, cc := range clauses {
		pat, ok := cc.Pattern().(*grammar.ExpressionPatternContext)
		if !ok {
			// A typed pattern at the top of a case covers no constructor.
			continue
		}
		if cc.GetGuard() != nil {
			guarded = append(guarded, []covCell{pat.Expression()})
		} else {
			rows = append(rows, []covCell{pat.Expression()})
		}
	}
	rows = c.expandAlternatives(rows)
	guarded = c.expandAlternatives(guarded)
	for _, ctor := range ctors {
		spec := c.specialize(rows, ctor, matchedType)
		if c.covers(spec, ctor.fields) {
			continue
		}
		if len(spec) == 0 {
			missing = append(missing, ctor.name)
		} else {
			missing = append(missing, ctor.name+"(...)")
		}
		for _, row := range guarded {
			if name, _ := c.constructorOf(row[0], matchedType); name == ctor.name {
				guardedMissing = true
				break
			}
		}
	}
	return true, len(missing) == 0, missing, guardedMissing
}

// covers reports whether rows match every vector of values of types.
func (c *coverage) covers(rows [][]covCell, types []transpiler.Type) bool {
	if len(types) == 0 {
		return len(rows) > 0
	}
	rows = c.expandAlternatives(rows)
	for _, row := range rows {
		if c.allWildcards(row, types) {
			return true
		}
	}
	head, rest := types[0], types[1:]
	if ctors, ok := c.constructors(head, true); ok && c.writesEvery(rows, ctors, head) {
		for _, ctor := range ctors {
			if !c.covers(c.specialize(rows, ctor, head), append(append([]transpiler.Type{}, ctor.fields...), rest...)) {
				return false
			}
		}
		return true
	}
	// A constructor no row names (or a type with none to enumerate) is
	// matched only by the rows that match anything here, and if those cover
	// the rest they cover every constructor: so they decide.
	var def [][]covCell
	for _, row := range rows {
		if c.isWildcard(row[0], head) {
			def = append(def, row[1:])
		}
	}
	return c.covers(def, rest)
}

// allWildcards reports whether every pattern of row matches every value.
func (c *coverage) allWildcards(row []covCell, types []transpiler.Type) bool {
	for i, cell := range row {
		if !c.isWildcard(cell, types[i]) {
			return false
		}
	}
	return true
}

// expandAlternatives replaces each row whose first pattern is an alternative
// pattern by one row per alternative.
func (c *coverage) expandAlternatives(rows [][]covCell) [][]covCell {
	var out [][]covCell
	for _, row := range rows {
		if row[0] == nil || row[0] == covRefutable {
			out = append(out, row)
			continue
		}
		for _, alt := range c.t.flatAlternatives(row[0]) {
			out = append(out, append([]covCell{alt}, row[1:]...))
		}
	}
	return out
}

// writesEvery reports whether, between them, the rows' first patterns name
// every constructor of typ.
func (c *coverage) writesEvery(rows [][]covCell, ctors []covCtor, typ transpiler.Type) bool {
	named := make(map[string]bool, len(ctors))
	for _, row := range rows {
		if name, _ := c.constructorOf(row[0], typ); name != "" {
			if named[name] = true; len(named) == len(ctors) {
				return true
			}
		}
	}
	return false
}

// specialize keeps the rows that can match constructor ctor, with ctor's
// fields in place of their first pattern. rows have their alternatives
// expanded.
func (c *coverage) specialize(rows [][]covCell, ctor covCtor, typ transpiler.Type) [][]covCell {
	var out [][]covCell
	for _, row := range rows {
		fields := make([]covCell, len(ctor.fields))
		if !c.isWildcard(row[0], typ) {
			name, args := c.constructorOf(row[0], typ)
			if name != ctor.name {
				continue
			}
			copy(fields, args)
		}
		out = append(out, append(fields, row[1:]...))
	}
	return out
}

// constructors returns the constructors of typ: the variants of a sealed
// type (with the subject's type arguments in their field types), true and
// false for bool, and, when nested is set, the one constructor of a tuple or
// a struct.
func (c *coverage) constructors(typ transpiler.Type, nested bool) ([]covCtor, bool) {
	if typ == nil || typ.IsNil() {
		return nil, false
	}
	typ = c.t.followAliasChain(typ)
	if bt, ok := typ.(transpiler.BasicType); ok && bt.Name == "bool" {
		return []covCtor{{name: "true"}, {name: "false"}}, true
	}
	if gen, ok := typ.(transpiler.GenericType); ok && gen.Base != nil && c.t.isTupleTypeName(gen.Base.BaseName()) {
		if !nested {
			return nil, false
		}
		return []covCtor{{name: tupleCtorName, fields: gen.Params}}, true
	}
	key := typ.String()
	ctors, ok := c.ctors[key]
	if !ok {
		ctors = c.typeConstructors(typ)
		c.ctors[key] = ctors
	}
	if len(ctors) == 1 && ctors[0].structural && !nested {
		return nil, false // a struct is enumerated only inside another pattern
	}
	return ctors, ctors != nil
}

// typeConstructors returns the variants of the sealed type typ, or the one constructor of a struct whose pattern reads its
// fields (see structPatternFields), or nil.
func (c *coverage) typeConstructors(typ transpiler.Type) []covCtor {
	meta := c.t.getTypeMeta(typ.BaseName())
	if meta == nil {
		return nil
	}
	var args []transpiler.Type
	if gen, ok := typ.(transpiler.GenericType); ok {
		args = gen.Params
	}
	if !meta.IsSealed {
		names := c.t.structPatternFields(typ.BaseName())
		if names == nil {
			return nil
		}
		fields := make([]transpiler.Type, len(names))
		for i, f := range names {
			// A field read through the pattern is its value, not the
			// Immutable that holds it.
			fields[i] = c.t.substituteConcreteTypes(unwrapGalaType(meta.Fields[f]), meta.TypeParams, args)
		}
		return []covCtor{{name: stripPackagePrefix(typ.BaseName()), fields: fields, structural: true}}
	}
	if len(meta.SealedVariants) == 0 {
		return nil
	}
	ctors := make([]covCtor, len(meta.SealedVariants))
	for i, v := range meta.SealedVariants {
		fields := make([]transpiler.Type, len(v.FieldTypes))
		for j, ft := range v.FieldTypes {
			fields[j] = c.t.substituteConcreteTypes(ft, meta.TypeParams, args)
		}
		ctors[i] = covCtor{name: v.Name, fields: fields}
	}
	return ctors
}

// isWildcard reports whether a pattern matches every value of typ: `_`, a
// binding, or a parenthesized one.
func (c *coverage) isWildcard(cell covCell, typ transpiler.Type) bool {
	switch {
	case cell == nil:
		return true
	case cell == covRefutable:
		return false
	}
	if inner := c.t.parenthesizedPattern(cell); inner != nil {
		return c.isWildcard(inner, typ)
	}
	if isWildcard(cell.GetText()) {
		return true
	}
	// A bare name binds unless it tests the value (a variant, a zero-field
	// extractor, a stable identifier), as transformSimpleBindingOrLiteral
	// lowers it; its capitalization plays no part.
	switch name := patternIdentifier(cell); name {
	case "", "true", "false", "nil", "iota":
		return false
	default:
		return !c.t.bareNameTests(name, typ) && !c.t.isStableIdentifierPattern(name)
	}
}

// constructorOf returns the constructor of typ a pattern names and the
// patterns of its fields (nil where a field matches anything), or "" when
// the pattern names none: a literal, a stable identifier, an extractor that
// is not a variant.
func (c *coverage) constructorOf(cell covCell, typ transpiler.Type) (string, []covCell) {
	if cell == nil || cell == covRefutable || typ == nil || typ.IsNil() {
		return "", nil
	}
	typ = c.t.followAliasChain(typ)
	if bt, ok := typ.(transpiler.BasicType); ok && bt.Name == "bool" {
		if text := cell.GetText(); text == "true" || text == "false" {
			return text, nil
		}
		return "", nil
	}
	ctors, ok := c.constructors(typ, true)
	if !ok {
		return "", nil
	}
	if ctors[0].name == tupleCtorName {
		list := c.t.parenthesizedList(cell)
		if list == nil || len(list.AllExpression()) != len(ctors[0].fields) {
			return "", nil
		}
		return tupleCtorName, list.AllExpression()
	}
	if inner := c.t.parenthesizedPattern(cell); inner != nil {
		return c.constructorOf(inner, typ)
	}
	if name := patternIdentifier(cell); name != "" {
		// `case None` is `case None()`.
		for _, ctor := range ctors {
			if ctor.name == name && len(ctor.fields) == 0 && c.t.bareNameTests(name, typ) {
				return name, nil
			}
		}
		return "", nil
	}
	name, argList, isCall := c.t.patternCallShape(cell)
	if !isCall {
		return "", nil
	}
	for _, ctor := range ctors {
		if ctor.name == name {
			argList, err := c.t.normalizePatternArgs(name, argList, typ)
			if err != nil {
				// Lowering reports the malformed sub-patterns.
				return "", nil
			}
			return name, c.fieldPatterns(argList, ctor)
		}
	}
	return "", nil
}

// fieldPatterns returns the patterns a variant pattern gives its fields, by
// position once named sub-patterns are normalized.
func (c *coverage) fieldPatterns(argList *grammar.ArgumentListContext, ctor covCtor) []covCell {
	fields := make([]covCell, len(ctor.fields))
	if argList == nil {
		return fields
	}
	for i, a := range argList.AllArgument() {
		if i >= len(fields) {
			break
		}
		switch pat := a.(*grammar.ArgumentContext).Pattern().(type) {
		case nil:
			// A field left out of named sub-patterns matches anything.
		case *grammar.ExpressionPatternContext:
			fields[i] = pat.Expression()
		case *grammar.TypedPatternContext:
			// `x: T` of the field's own type matches every value of it.
			if !c.typedPatternOfType(pat, ctor.fields[i]) {
				fields[i] = covRefutable
			}
		default:
			fields[i] = covRefutable
		}
	}
	return fields
}

// typedPatternOfType reports whether the typed pattern names exactly typ.
func (c *coverage) typedPatternOfType(pat *grammar.TypedPatternContext, typ transpiler.Type) bool {
	if transpiler.IsUnusable(typ) {
		return false
	}
	written, err := c.t.transformType(pat.Type_())
	if err != nil {
		return false
	}
	field := c.t.knownTypeExpr(typ)
	return field != nil && types.ExprString(written) == types.ExprString(field)
}
