package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
)

// This file contains struct construction and literal transformation logic extracted from expressions.go
// Functions: transformPrimary, transformCompositeLiteral, transformLiteral

func (t *galaASTTransformer) transformPrimary(ctx *grammar.PrimaryContext) (ast.Expr, error) {
	if ctx.Identifier() != nil {
		name := ctx.Identifier().GetText()
		// A `break` / `continue` statement is lowered before it gets here
		// (see lowerLoopControl); reaching it means it is read as a value.
		if _, isLoopControl := loopControlToken(name); isLoopControl {
			return nil, t.loopControlAsValueError(ctx, name)
		}
		ident := ast.NewIdent(name)
		// First check if it's a local variable - if so, don't try to resolve as std type
		if t.isVal(name) || t.isVar(name) {
			if t.isVal(name) {
				return &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   ident,
						Sel: ast.NewIdent(transpiler.MethodGet),
					},
				}, nil
			}
			return ident, nil
		}

		// A type the package declares shadows a std type or companion of the
		// same name (`Seq`, `Left`).
		if t.packageDeclaresType(name) {
			return ident, nil
		}
		// Check if this identifier is a std package type (not a variable with std type)
		// Only check typeMetas directly to see if std.name exists as a type definition
		// NOTE: Direct access is intentional here - we need exact match, not resolution
		if _, isStdType := t.typeMetas[withStdPrefix(name)]; isStdType {
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
	if ctx.Literal() != nil {
		return t.transformLiteral(ctx.Literal().(*grammar.LiteralContext))
	}
	// Handle composite literal (e.g., map[K]V{}, struct{}{})
	if ctx.CompositeLiteral() != nil {
		return t.transformCompositeLiteral(ctx.CompositeLiteral().(*grammar.CompositeLiteralContext))
	}
	if tupleListCtx := ctx.TupleExpressionList(); tupleListCtx != nil {
		el := tupleListCtx.(*grammar.TupleExpressionListContext)
		// When a tuple literal flows into a Tuple-shaped enclosing context
		// (e.g., the return position of a function declared to return
		// `Tuple[int, Cmd[Msg]]`, or a call argument expecting
		// `Tuple[string, error]`), thread each element's expected type so
		// that nested sealed-variant constructors like `NoCmd()` infer
		// their type parameters from the tuple's slot, and so the
		// synthesized composite literal carries concrete element types
		// rather than collapsing to `any` when an element's standalone
		// type happens to resolve to nil/any.
		elemExprs := el.AllExpression()
		if len(elemExprs) > 1 {
			perElemExpected := t.tupleElementExpectedTypes(len(elemExprs))
			exprs, err := t.transformTupleElementExpressions(elemExprs, perElemExpected)
			if err != nil {
				return nil, err
			}
			return t.transformTupleLiteralWithExpected(exprs, perElemExpected, ctx.GetStart().GetLine(), ctx.GetStart().GetColumn())
		}
		exprs := make([]ast.Expr, 0, len(elemExprs))
		for _, eCtx := range elemExprs {
			expr, err := t.transformExpression(eCtx)
			if err != nil {
				return nil, err
			}
			exprs = append(exprs, expr)
		}
		if len(exprs) == 1 {
			return &ast.ParenExpr{X: exprs[0]}, nil
		}
		return t.transformTupleLiteral(exprs, ctx.GetStart().GetLine(), ctx.GetStart().GetColumn())
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
		ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
		"empty parenthesized expression \"()\" cannot be used as a value",
		"use a real statement (e.g. `Println(\"…\")`) or remove the arm if it cannot occur",
	)
}

// tupleElementExpectedTypes returns the per-element expected types for a
// tuple literal of the given arity: those of the slot the literal itself
// fills, the top of `expectedArgTypes` — a call argument, val declaration or
// tuple element (lowerAgainst pushes an argSlot's type), or a function or
// lambda result when the literal is the whole result value (see
// consumesSlotType). This drives bidirectional inference for `f((a, b))`
// where `f`'s parameter is `Tuple[T1, T2]`. Returns nil if that slot is not
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
	elemExprs []grammar.IExpressionContext,
	perElemExpected []transpiler.Type,
) ([]ast.Expr, error) {
	out := make([]ast.Expr, 0, len(elemExprs))
	for i, eCtx := range elemExprs {
		var expected transpiler.Type
		if i < len(perElemExpected) {
			expected = perElemExpected[i]
		}
		expr, err := t.lowerAgainst(eCtx, argSlot(expected), true)
		if err != nil {
			return nil, err
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
	target := fieldType
	if len(typeArgs) > 0 && !transpiler.IsUnusable(fieldType) {
		target = t.substituteInType(fieldType, t.typeArgTypes(typeArgs))
	}
	return t.newImmutableFor(value, target)
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
// Beyond `nil`, the rewrite is confined to untyped numeric constants going into
// a numeric slot — a predeclared numeric type, a GALA type declared over one
// (`type Millis int64`), or a Go named numeric type (`time.Duration`). That is
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
	// assignment, but not through NewImmutable's inferred type argument.
	if t.isGoNamedFuncType(target) && !t.typeMentionsUnresolvedTypeParam(target) {
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
func (t *galaASTTransformer) structTypeArgSubst(typeExpr ast.Expr, resolvedTypeName string) map[string]ast.Expr {
	var indices []ast.Expr
	switch te := typeExpr.(type) {
	case *ast.IndexExpr:
		indices = []ast.Expr{te.Index}
	case *ast.IndexListExpr:
		indices = te.Indices
	default:
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

func (t *galaASTTransformer) transformCompositeLiteral(ctx *grammar.CompositeLiteralContext) (ast.Expr, error) {
	// Transform the type
	typeExpr, err := t.transformType(ctx.Type_())
	if err != nil {
		return nil, err
	}

	// Reject slice literals - users should use collection_immutable.Array, collection_mutable.Array, or go_interop.SliceOf() or go_interop.SliceEmpty() for Go interop
	if _, isArray := typeExpr.(*ast.ArrayType); isArray {
		return nil, galaerr.NewCodedSemanticError(
			galaerr.CodeSliceLiteralNotSupported,
			ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
			"slice literals are not a first-class GALA construct",
			"use collection_immutable.Array or collection_mutable.Array for type-safe collections, or go_interop.SliceOf()/SliceEmpty() for Go interop",
		)
	}

	// Reject map literals - users should use collection_immutable.HashMap, collection_mutable.HashMap or go_interop.MapEmpty() for Go interop
	if _, isMap := typeExpr.(*ast.MapType); isMap {
		return nil, galaerr.NewCodedSemanticError(
			galaerr.CodeMapLiteralNotSupported,
			ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
			"map literals are not a first-class GALA construct",
			"use collection_immutable.HashMap or collection_mutable.HashMap for type-safe maps, or go_interop.MapEmpty()/MapPut() for Go interop",
		)
	}

	// Look up struct field immutability flags so we can wrap immutable field values
	// with NewImmutable() during Go-style struct construction (e.g., Foo{items: x}).
	typeName := t.getBaseTypeName(typeExpr)
	resolvedTypeName := t.resolveStructTypeName(typeName)
	immutFlags := t.structImmutFields[resolvedTypeName]
	fields := t.structFields[resolvedTypeName]
	fieldTypes := t.structFieldTypes[resolvedTypeName]
	typeArgSubst := t.structTypeArgSubst(typeExpr, resolvedTypeName)
	// Build a map from field name to its index for quick lookup
	fieldIndex := make(map[string]int, len(fields))
	for i, f := range fields {
		fieldIndex[f] = i
	}

	// Transform the elements
	var elts []ast.Expr
	if ctx.ElementList() != nil {
		elemList := ctx.ElementList().(*grammar.ElementListContext)
		for _, keyedElem := range elemList.AllKeyedElement() {
			kv := keyedElem.(*grammar.KeyedElementContext)
			exprs := kv.AllExpression()
			if len(exprs) == 2 {
				// Key-value pair
				key, err := t.transformExpression(exprs[0].(*grammar.ExpressionContext))
				if err != nil {
					return nil, err
				}
				value, err := t.transformExpression(exprs[1].(*grammar.ExpressionContext))
				if err != nil {
					return nil, err
				}
				// Wrap value with NewImmutable() if the field is immutable (val, not var)
				if keyIdent, ok := key.(*ast.Ident); ok {
					if idx, found := fieldIndex[keyIdent.Name]; found {
						if immutFlags != nil && idx < len(immutFlags) && immutFlags[idx] {
							// Reject nil assignment to immutable (val) fields — use Option[T] instead
							if ident, isIdent := value.(*ast.Ident); isIdent && ident.Name == "nil" {
								return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), fmt.Sprintf(
									"cannot assign nil to immutable field '%s' — use Option[T] with None() for optional values, or 'var %s' to make it mutable",
									keyIdent.Name, keyIdent.Name))
							}
							value = t.wrapImmutableFieldValue(value, fieldTypes[keyIdent.Name], typeArgSubst)
						}
					}
				}
				elts = append(elts, &ast.KeyValueExpr{Key: key, Value: value})
			} else if len(exprs) == 1 {
				// Value only
				value, err := t.transformExpression(exprs[0].(*grammar.ExpressionContext))
				if err != nil {
					return nil, err
				}
				elts = append(elts, value)
			}
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
func (t *galaASTTransformer) checkIntLiteral(node antlr.TerminalNode) error {
	text := node.GetText()
	if len(text) < 2 || text[0] != '0' || strings.IndexFunc(text, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return nil
	}
	bad := strings.IndexAny(text, "89")
	if bad < 0 {
		return nil
	}
	tok := node.GetSymbol()
	err := galaerr.NewSyntaxError(tok.GetLine(), tok.GetColumn()+bad,
		fmt.Sprintf("invalid digit %q in octal literal %s (a leading 0 makes an integer literal octal; drop the leading zeros for a decimal number)", text[bad], text))
	err.FilePath = t.filePath
	return err
}

func (t *galaASTTransformer) transformLiteral(ctx *grammar.LiteralContext) (ast.Expr, error) {
	if ctx.INT_LIT() != nil {
		if err := t.checkIntLiteral(ctx.INT_LIT()); err != nil {
			return nil, err
		}
		return &ast.BasicLit{Kind: token.INT, Value: ctx.INT_LIT().GetText()}, nil
	}
	if ctx.FLOAT_LIT() != nil {
		return &ast.BasicLit{Kind: token.FLOAT, Value: ctx.FLOAT_LIT().GetText()}, nil
	}
	// A literal's raw source text is copied verbatim into the generated Go
	// literal, so its escape sequences must be ones Go accepts — GALA's lexer
	// admits `\` followed by any character. Backtick raw strings have no
	// escapes and are not scanned.
	if ctx.STRING() != nil {
		if err := t.checkLiteralEscapes(ctx.STRING(), escapeKindString); err != nil {
			return nil, err
		}
		return &ast.BasicLit{Kind: token.STRING, Value: ctx.STRING().GetText()}, nil
	}
	if ctx.CHAR_LIT() != nil {
		if err := t.checkLiteralEscapes(ctx.CHAR_LIT(), escapeKindRune); err != nil {
			return nil, err
		}
		return &ast.BasicLit{Kind: token.CHAR, Value: ctx.CHAR_LIT().GetText()}, nil
	}
	if ctx.RAW_STRING() != nil {
		return &ast.BasicLit{Kind: token.STRING, Value: ctx.RAW_STRING().GetText()}, nil
	}
	if ctx.INTERPOLATED_STRING() != nil {
		if err := t.checkLiteralEscapes(ctx.INTERPOLATED_STRING(), escapeKindInterp); err != nil {
			return nil, err
		}
		return t.transformInterpolatedString(ctx.INTERPOLATED_STRING())
	}
	if ctx.FORMAT_STRING() != nil {
		if err := t.checkLiteralEscapes(ctx.FORMAT_STRING(), escapeKindFormat); err != nil {
			return nil, err
		}
		return t.transformFormatString(ctx.FORMAT_STRING())
	}
	if ctx.GetText() == "true" || ctx.GetText() == "false" {
		return ast.NewIdent(ctx.GetText()), nil
	}
	if ctx.GetText() == "nil" {
		return ast.NewIdent("nil"), nil
	}
	return nil, nil
}
