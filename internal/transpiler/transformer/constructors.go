package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
)

// This file contains struct construction and literal transformation logic:
// lowerIdentifier, lowerParenthesized, transformCompositeLiteral, lowerLiteral.

// lowerIdentifier lowers name, a primary written at at: a binding, a type
// parameter, a type or function of the package, std's or another package's.
func (t *galaASTTransformer) lowerIdentifier(name string, at srcNode) (ast.Expr, error) {
	// A `break` / `continue` statement is lowered before it gets here
	// (see lowerLoopControl); reaching it means it is read as a value.
	if _, isLoopControl := loopControlToken(name); isLoopControl {
		return nil, t.loopControlAsValueError(at, name)
	}
	if err := t.underscoreBindingRead(name, at); err != nil {
		return nil, err
	}
	if err := t.placeholderInInterpolation(name, at); err != nil {
		return nil, err
	}
	ident := ast.NewIdent(name)
	s := t.bindingScope(name)
	if name == "_" && s != nil && s.placeholder != nil && !s.placeholder.take(ident, at) {
		// A `_` the placeholder lambda around it does not take, one in a
		// nested lambda or a case clause, is bound by nothing.
		s = nil
	}
	if name == "_" && s == nil {
		t.recordBlankRead(ident, at)
	}
	// First check if it's a local variable - if so, don't try to resolve as std type
	if s != nil {
		if s.parent == nil {
			if err := t.preludeDotImportAmbiguity(at, name, t.dotImportedVals[name], "value"); err != nil {
				return nil, err
			}
		}
		if _, boxed := t.valKind(name); boxed {
			t.markSourceRef(ident)
			return &ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X:   ident,
					Sel: ast.NewIdent(transpiler.MethodGet),
				},
			}, nil
		}
		t.markSourceRef(ident)
		if s.inout[name] {
			if err := t.checkInoutCapture(s, name, at, false, false); err != nil {
				return nil, err
			}
			return t.inoutRead(ident), nil
		}
		if s.parent == nil && t.isPackageVar(name) {
			t.markOperand(ident, operandPackageVar)
		}
		return ident, nil
	}

	// A type parameter of the enclosing generic declaration shadows every
	// name outside it — a package-level val (see bindingScope), std's
	// names, an import's — as in Go: `Some[Left](a)` in `func f[Left any]`
	// names the type parameter, not std's Left.
	if t.activeTypeParams[name] {
		return ident, nil
	}

	// A type or function the package declares shadows a std type or
	// companion of the same name (`Seq`, `Left`, `func Void()`). A
	// default lowered from another package names that package's
	// function, which is qualified below.
	ownFunc := t.packageDeclaresFunction(name)
	if t.packageDeclaresType(name) || ownFunc && !t.loweringForeignDefault() {
		t.markTopLevelRef(ident)
		return ident, nil
	}
	// Check if this identifier is a std package type (not a variable with std type)
	// Only check typeMetas directly to see if std.name exists as a type definition
	// NOTE: Direct access is intentional here - we need exact match, not resolution
	if _, isStdType := t.typeMetas[withStdPrefix(name)]; isStdType && !ownFunc {
		if err := t.preludeDotImportAmbiguity(at, name, t.dotImportedFunction(name), "function"); err != nil {
			return nil, err
		}
		return t.stdIdent(name), nil
	}
	// Check if it's a std function (from metadata)
	resolvedFunc := t.getFunction(name)
	if resolvedFunc != nil && resolvedFunc.Package == registry.StdPackageName {
		return t.stdIdent(name), nil
	}
	// Function from another GALA package (e.g., ArrayTabulate from
	// collection_immutable). Either keep the bare name (when the package
	// is dot-imported in this file) and mark the dot-import as used, or
	// qualify as `pkg.Name` and add a transitive import. Without this,
	// a sibling file's dot-import propagates the function's metadata into
	// our richAST so the lambda's parameter type is inferred correctly,
	// but the call site emits an unqualified `Name(...)` that the Go
	// compiler rejects with `undefined: Name`.
	if resolvedFunc != nil && resolvedFunc.Package != "" && resolvedFunc.Package != t.packageName {
		pkg := resolvedFunc.Package
		if t.importManager.IsDotImported(pkg) {
			t.markDotImportUsed(pkg)
			return ident, nil
		}
		alias := pkg
		if a, ok := t.packageQualifier(pkg); ok {
			alias = a
		}
		return &ast.SelectorExpr{
			X:   ast.NewIdent(alias),
			Sel: ast.NewIdent(name),
		}, nil
	}
	// Check if it's a known std exported function (defined in Go, not GALA)
	if registry.IsStdFunction(name) {
		return t.stdIdent(name), nil
	}
	return ident, nil
}

// underscoreBindingRead is GALA-E0139 when name, a primary written at at, is
// a `_` whose innermost binding is a `_` lambda parameter, typed pattern or
// `bind _ = m` clause (`(_) => _ + 1`, `case _: int => _`): such a `_` names
// no value, and Go has no value to read for it. A placeholder lambda's `_` is
// its parameter.
func (t *galaASTTransformer) underscoreBindingRead(name string, at srcNode) error {
	if name != "_" {
		return nil
	}
	if s := t.bindingScope(name); s == nil || s.placeholder != nil {
		return nil
	}
	r := at.rng()
	return spanErrorAt(galaerr.CodeUnderscoreBindingRead, r, r,
		"`_` reads a `_` binding, which names no value",
		"bind a name instead of `_` and read that name; for example `(x) => x + 1` or `case n: int => n`")
}

// underscoreWriteRead is GALA-E0139 for op, an increment, decrement or
// compound assignment of `_` written at at (`_++`, `_ += ...`): each reads
// `_` before writing it, and `_` names no value. Only `=` writes to `_`, as
// a discard.
func underscoreWriteRead(at srcNode, op string) error {
	r := at.rng()
	return spanErrorAt(galaerr.CodeUnderscoreBindingRead, r, r,
		fmt.Sprintf("`%s` reads `_`, which names no value", op),
		"use a named variable; only `=` writes to `_`, and that discards the value")
}

// lowerParenthesized lowers `(elems)` written at at: a tuple literal of two
// or more elements, a grouping of one, and `()`, which is no value.
func (t *galaASTTransformer) lowerParenthesized(elems []exprSrc, at srcNode) (ast.Expr, error) {
	line, col := at.start()
	if len(elems) > 0 {
		// When a tuple literal flows into a Tuple-shaped enclosing context
		// (e.g., the return position of a function declared to return
		// `Tuple[int, Cmd[Msg]]`, or a call argument expecting
		// `Tuple[string, error]`), thread each element's expected type so
		// that nested sealed-variant constructors like `NoCmd()` infer
		// their type parameters from the tuple's slot, and so the
		// synthesized composite literal carries concrete element types
		// rather than collapsing to `any` when an element's standalone
		// type happens to resolve to nil/any.
		if len(elems) > 1 {
			perElemExpected := t.tupleElementExpectedTypes(len(elems))
			exprs, err := t.transformTupleElementExpressions(elems, perElemExpected)
			if err != nil {
				return nil, err
			}
			return t.transformTupleLiteralWithExpected(exprs, perElemExpected, line, col)
		}
		expr, err := t.lowerExpr(elems[0])
		if err != nil {
			return nil, err
		}
		return &ast.ParenExpr{X: expr}, nil
	}
	// Empty parenthesized expression `()` — the grammar admits it because
	// `'(' expressionList? ')'` makes the inner list optional, but GALA
	// has no unit/void literal we can lower to a Go expression. Surface
	// as GALA-E0019 with a hint pointing at the common offender (void
	// match-arm shorthand). Without this guard the call returned nil, nil
	// and downstream code crashed Go's printer with `ast.Walk: unexpected
	// node type <nil>` far from the source span.
	return nil, galaerr.NewCodedSemanticError(
		galaerr.CodeEmptyParenExpression,
		line, col,
		"empty parenthesized expression \"()\" cannot be used as a value",
		"use a real statement (e.g. `Println(\"…\")`) or remove the arm if it cannot occur",
	)
}

// tupleElementExpectedTypes returns the per-element expected types for a
// tuple literal of the given arity: those of the slot the literal itself
// fills, the top of `expectedArgTypes` — a call argument, val declaration,
// tuple element, function or lambda result, pushed by lowerAgainst for the
// literal that is the whole value (see consumesSlotType). This drives
// bidirectional inference for `f((a, b))` where `f`'s parameter is
// `Tuple[T1, T2]`. Returns nil if that slot is not
// a tuple of this arity. When it is, the entry is consumed off the stack so
// that nested expressions inside this tuple do not pick it up again (B1
// contract).
func (t *galaASTTransformer) tupleElementExpectedTypes(arity int) []transpiler.Type {
	if pending := t.expectedArgTypes.peek(); pending != nil && !pending.IsNil() {
		if gen, ok := pending.(transpiler.GenericType); ok &&
			t.isTupleTypeName(gen.Base.String()) && len(gen.Params) == arity {
			t.expectedArgTypes.consume()
			return gen.Params
		}
	}
	return nil
}

// transformTupleElementExpressions transforms each element of a tuple literal
// against its corresponding expected type (see lowerAgainst), so that
// sealed-variant constructors nested directly inside an element can resolve
// their type arguments from the surrounding tuple slot.
func (t *galaASTTransformer) transformTupleElementExpressions(
	elemExprs []exprSrc,
	perElemExpected []transpiler.Type,
) ([]ast.Expr, error) {
	out := make([]ast.Expr, 0, len(elemExprs))
	for i, e := range elemExprs {
		var expected transpiler.Type
		if i < len(perElemExpected) {
			expected = perElemExpected[i]
		}
		expr, err := t.lowerAgainst(e, typedSlot(expected), true)
		if err != nil {
			return nil, err
		}
		// A `nil` element is of its slot element's type: `(n, nil)` filling
		// a Tuple[int, error] holds a nil error. Its wrapper names that type
		// all the same (transformTupleLiteralWithExpected).
		if expected != nil {
			t.typeNilAs(expr, expected)
		}
		out = append(out, expr)
	}
	return out, nil
}

// wrapImmutableFieldValue builds the `std.NewImmutable(value)` wrapper that
// backs an immutable (`val`) struct field. typeArgs maps the struct's
// type-parameter names to the type arguments of the literal being built, so a
// field declared with a type parameter resolves to the type it is instantiated
// with at this construction site (`Box[int64](0)` → NewImmutable[int64]).
func (t *galaASTTransformer) wrapImmutableFieldValue(value ast.Expr, fieldType transpiler.Type, typeArgs map[string]ast.Expr) ast.Expr {
	var types typeSubstMap
	if len(typeArgs) > 0 {
		types = t.typeArgTypes(typeArgs)
	}
	return t.wrapImmutableFieldValueTypes(value, fieldType, types)
}

// wrapImmutableFieldValueTypes is wrapImmutableFieldValue with the type
// arguments already converted to types.
func (t *galaASTTransformer) wrapImmutableFieldValueTypes(value ast.Expr, fieldType transpiler.Type, typeArgs typeSubstMap) ast.Expr {
	return t.newImmutableFor(value, t.substituteTranspilerTypeParams(fieldType, typeArgs))
}

// newImmutableFor builds `std.NewImmutable(value)` for a value that lands in an
// `Immutable[target]` slot — a `val` struct field, a Copy override, a tuple
// element — naming the type argument explicitly whenever inference from the
// value alone would pick the wrong one. Struct construction, field defaults,
// Copy overrides and tuple literals all go through here, so the rule lives in
// one place. (liftToImmutableForArg always spells the type, so it needs none.)
//
// Go infers NewImmutable's type parameter from its argument, and an untyped
// constant argument collapses to its own default type (`0` → int, `1.5` →
// float64). A slot declared `int64` therefore receives an Immutable[int], which
// is not assignable to Immutable[int64] — even though the constant is perfectly
// representable and a plain Go assignment would have converted it. Spelling
// the type argument (`std.NewImmutable[int64](0)`) restores that conversion at
// the argument position.
//
// A bare `nil` has no type of its own at all, so Go cannot infer anything from
// it ("cannot infer T"); it always takes the slot's declared type
// (`std.NewImmutable[func(int) int](nil)`).
//
// target may be nil or unresolved, in which case the inferred form is kept.
func (t *galaASTTransformer) newImmutableFor(value ast.Expr, target transpiler.Type) ast.Expr {
	if typeArg := t.immutableTypeArg(value, target); typeArg != nil {
		return &ast.CallExpr{
			Fun:  &ast.IndexExpr{X: t.stdIdent(transpiler.FuncNewImmutable), Index: typeArg},
			Args: []ast.Expr{value},
		}
	}
	return &ast.CallExpr{
		Fun:  t.stdIdent(transpiler.FuncNewImmutable),
		Args: []ast.Expr{value},
	}
}

// immutableTypeArg returns the explicit NewImmutable type argument for value
// going into an Immutable[target], or nil when plain inference is already
// correct.
//
// Beyond `nil`, an untyped constant going into an opaque slot, a function
// value going into a Go named function type slot (`fs.WalkDirFunc`), any
// value going into an interface slot (`error`, `any`, `Shape`) and any value
// going into a slot of a Go type this file has no type information for, the
// rewrite is confined to untyped numeric constants going into a numeric slot —
// a predeclared numeric type, a GALA type declared over one (`type Millis int64`), or a Go named numeric type (`time.Duration`). That is
// exactly the set of values whose type Go would have taken from the
// destination but takes from the argument once the NewImmutable wrapper
// intervenes. A typed expression, a non-numeric slot, or a type parameter with
// no concrete instantiation keeps the inferred form.
func (t *galaASTTransformer) immutableTypeArg(value ast.Expr, target transpiler.Type) ast.Expr {
	if target == nil || transpiler.IsUnusable(target) {
		return nil
	}
	if id, isIdent := value.(*ast.Ident); isIdent && id.Name == "nil" {
		return t.typeToExpr(target)
	}
	// An untyped constant of any kind — `""`, `false`, `0`, an untyped bool —
	// takes an opaque slot's type, as a Go assignment would: its default type
	// (string, bool, int) is never the opaque type itself. A logical operator
	// over operands of the opaque type already has that type, so naming it
	// changes nothing there.
	if isUntypedConst(value) && t.opaqueMeta(target) != nil {
		return t.typeToExpr(target)
	}
	// A function value of an unnamed function type (a lambda, a declared
	// function) goes into a Go named function type slot (`fs.WalkDirFunc`) by
	// assignment, but not through NewImmutable's inferred type argument. So
	// does a value of a concrete type going into an interface slot (`error`,
	// `any`, `Shape`): Immutable[Square] is not an Immutable[Shape]. A Go type
	// this file has no type information for — a field of a struct another
	// package declares, of a Go package this file does not import — may be
	// either, so it is spelled too: the slot already has that type, so naming
	// it never changes what the value must be assignable to. A value of an
	// unnamed type goes into a slot of a Go type defined over it (`type NP
	// *T`, `type IDs []int`) the same way.
	if (t.isGoNamedFuncType(target) || t.isInterfaceType(target) || t.isGoTypeWithoutInfo(target) || t.isGoDefinedOverTypeLiteral(target)) &&
		!t.typeMentionsUnresolvedTypeParam(target) {
		return t.typeToExpr(target)
	}
	defaultName, ok := t.untypedNumericConstExprDefault(value)
	if !ok || !t.isNumericSlotType(target) {
		return nil
	}
	if basic, isBasic := target.(transpiler.BasicType); isBasic && basic.Name == defaultName {
		return nil
	}
	return t.typeToExpr(target)
}

// isGoDefinedOverTypeLiteral reports whether typ is a type Go defines over
// an unnamed type (`type NP *T`, `type IDs []int`, a map, a channel or a
// function type), which a value of that unnamed type is assignable to;
// through GALA and Go aliases of one.
func (t *galaASTTransformer) isGoDefinedOverTypeLiteral(typ transpiler.Type) bool {
	if transpiler.IsPrimitiveType(typ.BaseName()) {
		return false
	}
	typ, _ = t.unaliasedType(typ, nil)
	switch t.goDefinedUnderlying(typ, nil).(type) {
	case transpiler.PointerType, transpiler.ArrayType, transpiler.MapType, transpiler.ChanType, transpiler.FuncType:
		return true
	}
	return false
}

// isNumericSlotType reports whether typ is a numeric type an untyped numeric
// constant converts to: a predeclared numeric type, a GALA type declared over
// one (following alias chains), or a package-qualified Go named type whose
// underlying type is numeric.
func (t *galaASTTransformer) isNumericSlotType(typ transpiler.Type) bool {
	// The hop bound stops a declaration chain that refers back to itself.
	for hop := 0; hop < 16; hop++ {
		// An untyped constant fits an opaque type over a numeric type, as it
		// fits a Go defined type.
		if u, ok := t.opaqueUnderlying(typ); ok {
			typ = u
			continue
		}
		var bareName string
		switch ty := typ.(type) {
		case transpiler.BasicType:
			// A bare declared name (`Millis`) parses as a BasicType too.
			bareName = ty.Name
		case transpiler.NamedType:
			if u, ok := t.goNamedUnderlying(ty); ok {
				typ = u
				continue
			}
			if ty.Package != "" && ty.Package != t.packageName {
				resolved := t.followAliasChain(ty)
				if resolved.BaseName() == ty.BaseName() {
					return false
				}
				typ = resolved
				continue
			}
			// This package's own declarations are keyed by their bare name.
			bareName = ty.Name
		default:
			return false
		}
		if isNumericPrimitive(bareName) {
			return true
		}
		if next, ok := t.typeAliases[bareName]; ok && !next.IsNil() {
			typ = next
			continue
		}
		// Declared later in this file than the site being lowered.
		if target, ok := t.fileTypeDeclTargets[bareName]; ok {
			typ = target
			continue
		}
		return false
	}
	return false
}

// structTypeArgSubst pairs a struct's type-parameter names with the type
// arguments carried by the literal's type expression (`Box[int64]` → T: int64).
// It returns nil when the literal is not an instantiation or the arity does not
// line up, in which case fields typed with a type parameter stay uninstantiated.
// A literal spelled with an alias (`Flipped[int64, string]` for `type
// Flipped[V any, K comparable] Entry[K, V]`, or `IntPair` for `type IntPair
// Pair[int]`) carries the arguments of the struct type the alias names
// (`Entry[string, int64]`, `Pair[int]`).
func (t *galaASTTransformer) structTypeArgSubst(typeExpr ast.Expr, resolvedTypeName string) map[string]ast.Expr {
	base, indices := splitCallFunTypeArgs(typeExpr)
	if _, name := extractTypeNameFromExpr(base); name != "" {
		if _, isAlias := t.lookupTypeAlias(name); isAlias {
			if named, ok := t.followAliasChain(t.astTypeToTranspilerType(typeExpr)).(transpiler.GenericType); ok {
				indices = make([]ast.Expr, len(named.Params))
				for i, p := range named.Params {
					indices[i] = t.typeToExpr(p)
				}
			}
		}
	}
	if len(indices) == 0 {
		return nil
	}
	typeMeta := t.getTypeMeta(resolvedTypeName)
	if typeMeta == nil || len(typeMeta.TypeParams) != len(indices) {
		return nil
	}
	subst := make(map[string]ast.Expr, len(indices))
	for i, tp := range typeMeta.TypeParams {
		subst[tp] = indices[i]
	}
	return subst
}

// typeArgTypes converts the type-argument expressions of structTypeArgSubst to
// types, for substituting into a field's declared type.
func (t *galaASTTransformer) typeArgTypes(subst map[string]ast.Expr) map[string]transpiler.Type {
	out := make(map[string]transpiler.Type, len(subst))
	for name, arg := range subst {
		out[name] = t.astTypeToTranspilerType(arg)
	}
	return out
}

// untypedNumericConstDefault reports the Go default type of an untyped numeric
// constant expression (`0` → int, `1.5` → float64, `'a'` → rune) and false for
// anything that is not built purely from numeric literals. Constant arithmetic
// stays untyped in Go, so `60 * 1000` is classified like a bare literal.
func untypedNumericConstDefault(expr ast.Expr) (string, bool) {
	return untypedNumericConstDefaultWith(expr, nil)
}

// untypedNumericConstExprDefault is untypedNumericConstDefault that also knows
// the untyped constants Go packages declare: `math.MinInt8`, `math.Pi`, and
// constant expressions built from them (`math.MaxInt8 - 1`) are untyped in Go
// exactly as literals are, so they take the type of the slot they land in.
func (t *galaASTTransformer) untypedNumericConstExprDefault(expr ast.Expr) (string, bool) {
	return untypedNumericConstDefaultWith(expr, t.goUntypedNumericConstDefault)
}

// isUntypedConstArg reports whether expr, a lowered call argument, is an
// untyped constant: a literal, or one of a Go package (`math.MaxInt8`). Go
// infers a type parameter only such arguments bind as the constant's default
// type, whatever the slot the call fills.
func (t *galaASTTransformer) isUntypedConstArg(expr ast.Expr) bool {
	if isUntypedConstExpr(expr) {
		return true
	}
	_, ok := t.untypedNumericConstExprDefault(expr)
	return ok
}

// typedPartType is typ, the type of expr, a lowered argument, with every part
// an untyped constant gives left unknown (NilType): all of it for a constant,
// an element for a tuple literal holding one (`("a", 1)` is a
// `Tuple[string, ?]`). A type parameter only such parts bind takes the type
// of the slot the call fills, as a Go assignment converts a constant.
func (t *galaASTTransformer) typedPartType(expr ast.Expr, typ transpiler.Type) transpiler.Type {
	if t.isUntypedConstArg(expr) {
		return transpiler.NilType{}
	}
	lit, isLit := expr.(*ast.CompositeLit)
	gen, isGen := typ.(transpiler.GenericType)
	if !isLit || !isGen || !t.isTupleTypeName(gen.Base.String()) || len(lit.Elts) != len(gen.Params) {
		return typ
	}
	params := make([]transpiler.Type, len(gen.Params))
	for i, elt := range lit.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			elt = kv.Value
		}
		params[i] = t.typedPartType(newImmutableArg(elt), gen.Params[i])
	}
	return transpiler.GenericType{Base: gen.Base, Params: params}
}

// newImmutableArg is the value a `NewImmutable(value)` call wraps, or expr
// itself when it is no such call.
func newImmutableArg(expr ast.Expr) ast.Expr {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return expr
	}
	fun := call.Fun
	if idx, ok := fun.(*ast.IndexExpr); ok {
		fun = idx.X
	}
	var name string
	switch f := fun.(type) {
	case *ast.Ident:
		name = f.Name
	case *ast.SelectorExpr:
		name = f.Sel.Name
	}
	if name != transpiler.FuncNewImmutable {
		return expr
	}
	return call.Args[0]
}

// goUntypedNumericConstDefault reports the default type of a reference to an
// untyped numeric constant of an imported Go package (`math.MaxInt8` → int).
func (t *galaASTTransformer) goUntypedNumericConstDefault(expr ast.Expr) (string, bool) {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || t.goTypeInfo == nil {
		return "", false
	}
	qualifier, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	entry, isGala, ok := t.importForQualifier(qualifier.Name)
	if !ok || isGala {
		return "", false
	}
	key := entry.PkgName + "." + sel.Sel.Name
	if !t.goTypeInfo.UntypedConstants[key] {
		return "", false
	}
	basic, ok := t.goTypeInfo.Constants[key].(transpiler.BasicType)
	if !ok || numericConstRank(basic.Name) < 0 {
		return "", false
	}
	return basic.Name, true
}

// untypedNumericConstDefaultWith is the shared walk; named, when non-nil,
// classifies a leaf that is not a literal (a named constant reference).
func untypedNumericConstDefaultWith(expr ast.Expr, named func(ast.Expr) (string, bool)) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		switch e.Kind {
		case token.INT:
			return "int", true
		case token.FLOAT:
			return "float64", true
		case token.CHAR:
			return "rune", true
		}
	case *ast.ParenExpr:
		return untypedNumericConstDefaultWith(e.X, named)
	case *ast.UnaryExpr:
		switch e.Op {
		case token.ADD, token.SUB, token.XOR:
			return untypedNumericConstDefaultWith(e.X, named)
		}
	case *ast.BinaryExpr:
		switch e.Op {
		case token.SHL, token.SHR:
			// The shift count does not influence the result's default type.
			return untypedNumericConstDefaultWith(e.X, named)
		case token.ADD, token.SUB, token.MUL, token.QUO, token.REM,
			token.AND, token.OR, token.XOR, token.AND_NOT:
			left, okLeft := untypedNumericConstDefaultWith(e.X, named)
			right, okRight := untypedNumericConstDefaultWith(e.Y, named)
			if !okLeft || !okRight {
				return "", false
			}
			if numericConstRank(right) > numericConstRank(left) {
				return right, true
			}
			return left, true
		}
	case *ast.SelectorExpr:
		if named != nil {
			return named(e)
		}
	}
	return "", false
}

// numericConstRank orders the untyped numeric constant kinds by how they
// combine: mixing two kinds in a constant expression yields the wider one.
func numericConstRank(name string) int {
	switch name {
	case "int":
		return 0
	case "rune":
		return 1
	case "float64":
		return 2
	}
	return -1
}

// isNumericPrimitive reports whether name is a predeclared Go numeric type —
// the destinations an untyped numeric constant can convert to implicitly.
func isNumericPrimitive(name string) bool {
	switch name {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
		"float32", "float64", "byte", "rune":
		return true
	}
	return false
}

// compositeSrc is a composite literal node, `T{...}`.
type compositeSrc struct {
	node native.GalaNodeRef
}

// at is where c is written.
func (c compositeSrc) at() srcNode { return srcNode{node: c.node} }

// typ is the type c constructs.
func (c compositeSrc) typ() typeSrc {
	return nativeType(native.CompositeLiteralNode{GalaNodeRef: c.node}.Type_())
}

// keyedElem is an element of a composite literal: its key, the zero exprSrc
// when it has none, and its value.
type keyedElem struct{ key, value exprSrc }

// elems is c's elements, in order.
func (c compositeSrc) elems() []keyedElem {
	var out []keyedElem
	list := native.CompositeLiteralNode{GalaNodeRef: c.node}.ElementList()
	for kv := range galaparser.NativeKids(list.GalaNodeRef, native.GalaKIND_keyedElement) {
		switch exprs := nativeExprs(kv); len(exprs) {
		case 1:
			out = append(out, keyedElem{value: exprs[0].exprSrc})
		case 2:
			out = append(out, keyedElem{key: exprs[0].exprSrc, value: exprs[1].exprSrc})
		}
	}
	return out
}

// compositeStruct is what a composite literal knows of the struct it builds:
// a GALA struct's fields, their declared types and the type parameters the
// literal leaves unbound, or the Go struct its type names, directly, through
// an alias or a dot import.
type compositeStruct struct {
	// isStruct: the type is known to be a struct, so a bare-name key is a
	// field name, in the struct's own namespace: `Pair{Left: 1}` names the
	// field Left, though a bare `Left` elsewhere is std's Either constructor.
	// Any other literal, such as a named Go map with expression keys
	// (`url.Values{k: v}`), lowers its keys as expressions.
	isStruct   bool
	fields     []string
	fieldTypes map[string]transpiler.Type
	// immutable names the GALA `val` fields.
	immutable map[string]bool
	// typeParamsKnown: the GALA struct's type parameters are known (it has
	// type metadata), so unbound lists every one the literal leaves unbound.
	typeParamsKnown bool
	unbound         []string
	typeArgSub      map[string]ast.Expr
	typeArgs        typeSubstMap // typeArgSub as types, on first use
	typeArgsDone    bool
	goStruct        *transpiler.GoTypeData
	// order is the struct's fields in declaration order, for a literal
	// written without keys; nil when it is not known to be complete.
	order []string
}

// compositeStructOf is what a literal of typeExpr knows of its struct. The Go
// type info is consulted only for a type GALA declares no fields for.
func (t *galaASTTransformer) compositeStructOf(typeExpr ast.Expr, resolvedTypeName string, meta *transpiler.TypeMetadata, typeArgSubst map[string]ast.Expr) *compositeStruct {
	s := &compositeStruct{
		fields:          t.structFields[resolvedTypeName],
		fieldTypes:      t.structFieldTypes[resolvedTypeName],
		typeParamsKnown: meta != nil,
		typeArgSub:      typeArgSubst,
	}
	for i, immutable := range t.structImmutFields[resolvedTypeName] {
		if immutable && i < len(s.fields) {
			if s.immutable == nil {
				s.immutable = make(map[string]bool)
			}
			s.immutable[s.fields[i]] = true
		}
	}
	if meta != nil {
		for _, tp := range meta.TypeParams {
			if _, bound := typeArgSubst[tp]; !bound {
				s.unbound = append(s.unbound, tp)
			}
		}
	}
	s.isStruct = len(s.fields) > 0 || (meta != nil && len(meta.Fields) > 0)
	if s.isStruct {
		// A sealed type's fields are the merged payloads of its cases, in no
		// one case's order.
		if meta == nil || !meta.IsSealed {
			s.order = s.fields
		}
		return s
	}
	if td, _ := t.goTypeDataThroughAliases(t.astTypeToTranspilerType(typeExpr)); td != nil && td.Kind == "struct" {
		s.goStruct, s.isStruct, s.order = td, true, td.FieldOrder
	}
	return s
}

// positionalField is the field the i-th of n elements of a literal written
// without keys fills, "" when it is not known. Go fills every field by
// position or none, so the n elements of a valid literal name a struct's n
// fields. A Go struct's FieldOrder lists only the fields visible here, not
// its unexported ones, so when it is shorter than n it is incomplete and an
// index into it could name the wrong field: no element is given one.
func (s *compositeStruct) positionalField(i, n int) string {
	if n == len(s.order) {
		return s.order[i]
	}
	return ""
}

// compositeTypeArgs is the literal's written type arguments as types.
func (t *galaASTTransformer) compositeTypeArgs(s *compositeStruct) typeSubstMap {
	if !s.typeArgsDone && len(s.typeArgSub) > 0 {
		s.typeArgs, s.typeArgsDone = t.typeArgTypes(s.typeArgSub), true
	}
	return s.typeArgs
}

// fieldSlot is the type the value of field name fills in the literal, as for
// the same field passed as a named argument (resolveNamedArgExpectedFuncType):
// a lambda written there takes its parameter types from it, and a tuple
// literal or generic call its type arguments. A GALA struct's field has its
// declared type, with the literal's type arguments substituted
// (structFieldSlot); one still naming a type parameter the literal leaves
// unbound gives none, since a `T{…}` literal infers no type argument from its
// fields. A Go struct's field gives its function type (goStructFuncField).
// Nil when there is none.
func (t *galaASTTransformer) fieldSlot(s *compositeStruct, name string) transpiler.Type {
	if ft := s.fieldTypes[name]; !transpiler.IsUnusable(ft) {
		if !s.typeParamsKnown {
			return nil
		}
		return t.structFieldSlot(ft, t.compositeTypeArgs(s), s.unbound, false)
	}
	if fn, ok := t.goStructFuncField(s.goStruct, name); ok {
		return fn
	}
	return nil
}

// lowerFieldValue lowers value, the value of field name ("" when unknown) of
// a literal of s, against the field's slot (see fieldSlot), and wraps it in
// NewImmutable when the field is a GALA `val` field. line and col place the
// literal.
func (t *galaASTTransformer) lowerFieldValue(s *compositeStruct, name string, value exprSrc, line, col int) (ast.Expr, error) {
	var fieldType transpiler.Type
	if name != "" {
		fieldType = t.fieldSlot(s, name)
	}
	expr, err := t.lowerAgainst(value, typedSlot(fieldType), false)
	if err != nil || name == "" {
		return expr, err
	}
	if !s.immutable[name] {
		return expr, nil
	}
	// Reject nil assignment to immutable (val) fields — use Option[T] instead
	if ident, isIdent := expr.(*ast.Ident); isIdent && ident.Name == "nil" {
		return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf(
			"cannot assign nil to immutable field '%s' — use Option[T] with None() for optional values, or 'var %s' to make it mutable",
			name, name))
	}
	return t.wrapImmutableFieldValueTypes(expr, s.fieldTypes[name], t.compositeTypeArgs(s)), nil
}

// transformCompositeLiteral lowers the composite literal c.
func (t *galaASTTransformer) transformCompositeLiteral(c compositeSrc) (ast.Expr, error) {
	t.nativeShape(c.node, "composite")
	// Transform the type
	typeExpr, err := t.lowerType(c.typ())
	if err != nil {
		return nil, err
	}
	line, col := c.at().start()

	// Reject slice literals - users should use collection_immutable.Array, collection_mutable.Array, or go_interop.SliceOf() or go_interop.SliceEmpty() for Go interop
	if _, isArray := typeExpr.(*ast.ArrayType); isArray {
		return nil, galaerr.NewCodedSemanticError(
			galaerr.CodeSliceLiteralNotSupported,
			line, col,
			"slice literals are not a first-class GALA construct",
			"use collection_immutable.Array or collection_mutable.Array for type-safe collections, or go_interop.SliceOf()/SliceEmpty() for Go interop",
		)
	}

	// Reject map literals - users should use collection_immutable.HashMap, collection_mutable.HashMap or go_interop.MapEmpty() for Go interop
	if _, isMap := typeExpr.(*ast.MapType); isMap {
		return nil, galaerr.NewCodedSemanticError(
			galaerr.CodeMapLiteralNotSupported,
			line, col,
			"map literals are not a first-class GALA construct",
			"use collection_immutable.HashMap or collection_mutable.HashMap for type-safe maps, or go_interop.MapEmpty()/MapPut() for Go interop",
		)
	}

	// What the literal knows of its struct: its fields' types, which are
	// immutable (wrapped in NewImmutable, see lowerFieldValue) and their order.
	typeName := t.getBaseTypeName(typeExpr)
	resolvedTypeName := t.resolveStructTypeName(typeName)
	typeArgSubst := t.structTypeArgSubst(typeExpr, resolvedTypeName)
	meta := t.getTypeMeta(resolvedTypeName)
	target := t.compositeStructOf(typeExpr, resolvedTypeName, meta, typeArgSubst)

	// Transform the elements
	var elts []ast.Expr
	given := make(map[string]bool) // the fields a keyed literal names
	keyed := false
	elems := c.elems()
	for i, el := range elems {
		if el.key.ok() {
			// Key-value pair
			name := ""
			if target.isStruct {
				name = el.key.bareIdent()
			}
			var key ast.Expr
			if name != "" {
				key = ast.NewIdent(name)
			} else if key, err = t.lowerExpr(el.key); err != nil {
				return nil, err
			}
			value, err := t.lowerFieldValue(target, name, el.value, line, col)
			if err != nil {
				return nil, err
			}
			keyed = true
			if name != "" {
				given[name] = true
			}
			elts = append(elts, &ast.KeyValueExpr{Key: key, Value: value})
		} else {
			// Value only: it fills the i-th field.
			value, err := t.lowerFieldValue(target, target.positionalField(i, len(elems)), el.value, line, col)
			if err != nil {
				return nil, err
			}
			elts = append(elts, value)
		}
	}

	if meta != nil {
		// A sealed type's literal names none of its cases: it is the type's
		// default value.
		if meta.IsSealed && len(elts) == 0 {
			val, err := t.defaultValue(t.astTypeToTranspilerType(typeExpr), line, col, map[string]bool{})
			if err != nil || val != nil {
				return val, err
			}
		}
		// The fields a keyed literal leaves out take their declared defaults,
		// or their types' default values (see fillOmittedStructFields).
		if len(target.fields) > 0 && !meta.IsSealed && (len(elts) == 0 || keyed) {
			omitted, err := withoutCallFrame(t, func() ([]ast.Expr, error) {
				return t.fillOmittedStructFields(typeName, resolvedTypeName, target.fields,
					func(_ int, name string) bool { return given[name] },
					typeArgSubst, line, col, false, nil)
			})
			if err != nil {
				return nil, err
			}
			elts = append(elts, omitted...)
		}
	}

	return &ast.CompositeLit{
		Type: typeExpr,
		Elts: elts,
	}, nil
}

// checkIntLiteral rejects the one INT_LIT spelling the lexer admits that Go
// does not: a leading zero followed by an 8 or 9. The grammar's plain-decimal
// alternative is `[0-9]+`, but a leading 0 makes the literal octal in Go (the
// classic `0644` form GALA keeps), so `08` or `0129` used to reach the
// generated Go verbatim and fail to parse there — an internal transpiler
// error instead of a diagnostic at the literal.
func (t *galaASTTransformer) checkIntLiteral(tok litTok) error {
	text := tok.text
	if len(text) < 2 || text[0] != '0' || strings.IndexFunc(text, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return nil
	}
	bad := strings.IndexAny(text, "89")
	if bad < 0 {
		return nil
	}
	err := galaerr.NewSyntaxError(tok.line, tok.col+bad,
		fmt.Sprintf("invalid digit %q in octal literal %s (a leading 0 makes an integer literal octal; drop the leading zeros for a decimal number)", text[bad], text))
	err.FilePath = t.filePath
	return err
}

// lowerLiteral lowers the literal l.
func (t *galaASTTransformer) lowerLiteral(l literalSrc) (ast.Expr, error) {
	// A literal's raw source text is copied verbatim into the generated Go
	// literal, so its escape sequences must be ones Go accepts — GALA's lexer
	// admits `\` followed by any character. Backtick raw strings have no
	// escapes and are not scanned.
	switch l.kind {
	case litInt:
		if err := t.checkIntLiteral(l.tok); err != nil {
			return nil, err
		}
		return &ast.BasicLit{Kind: token.INT, Value: l.tok.text}, nil
	case litFloat:
		return &ast.BasicLit{Kind: token.FLOAT, Value: l.tok.text}, nil
	case litString:
		if err := t.checkLiteralEscapes(l.tok, escapeKindString); err != nil {
			return nil, err
		}
		return &ast.BasicLit{Kind: token.STRING, Value: l.tok.text}, nil
	case litChar:
		if err := t.checkLiteralEscapes(l.tok, escapeKindRune); err != nil {
			return nil, err
		}
		return &ast.BasicLit{Kind: token.CHAR, Value: l.tok.text}, nil
	case litRawString:
		return &ast.BasicLit{Kind: token.STRING, Value: l.tok.text}, nil
	case litInterpolated:
		if err := t.checkLiteralEscapes(l.tok, escapeKindInterp); err != nil {
			return nil, err
		}
		return t.transformInterpolatedString(l.tok)
	case litFormat:
		if err := t.checkLiteralEscapes(l.tok, escapeKindFormat); err != nil {
			return nil, err
		}
		return t.transformFormatString(l.tok)
	case litWord:
		// `true`, `false` or `nil`.
		return ast.NewIdent(l.tok.text), nil
	}
	return nil, nil
}
