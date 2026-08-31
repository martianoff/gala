package transformer

import (
	"fmt"
	"go/ast"
	"go/token"

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
			if a, ok := t.importManager.GetAlias(pkg); ok {
				alias = a
			}
			if path, ok := t.importManager.GetPath(pkg); ok {
				t.importManager.AddTransitive(path, alias)
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
// tuple literal of the given arity, computed from the most-specific available
// outer context. Checks two sources, most-specific first:
//
//  1. The top of `expectedArgTypes` — the slot type at the immediately
//     enclosing call argument / val-decl / tuple-element position. This
//     drives bidirectional inference for the call-site case
//     (`f((a, b))` where `f`'s parameter is `Tuple[T1, T2]`).
//  2. `currentFuncReturnType` — the enclosing function's declared return
//     type, used when the tuple literal is the value at a function return.
//
// Returns nil if no Tuple-shaped expected type is available. When (1)
// matches, the entry is consumed off the stack so that nested expressions
// inside this tuple do not pick it up again (B1 contract).
func (t *galaASTTransformer) tupleElementExpectedTypes(arity int) []transpiler.Type {
	if pending := t.expectedArgTypes.peek(); pending != nil && !pending.IsNil() {
		if gen, ok := pending.(transpiler.GenericType); ok &&
			t.isTupleTypeName(gen.Base.String()) && len(gen.Params) == arity {
			t.expectedArgTypes.consume()
			return gen.Params
		}
	}
	candidates := []transpiler.Type{t.currentFuncReturnType}
	for _, cand := range candidates {
		if transpiler.IsUnusable(cand) {
			continue
		}
		gen, ok := cand.(transpiler.GenericType)
		if !ok {
			continue
		}
		if !t.isTupleTypeName(gen.Base.String()) {
			continue
		}
		if len(gen.Params) != arity {
			continue
		}
		return gen.Params
	}
	return nil
}

// transformTupleElementExpressions transforms each element of a tuple literal
// with its corresponding expected type pushed onto expectedArgTypes, so that
// sealed-variant constructors nested directly inside an element can resolve
// their type arguments from the surrounding tuple slot (B1).
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
		if expected != nil && !expected.IsNil() {
			release := t.expectedArgTypes.push(expected)
			expr, err := t.transformExpression(eCtx)
			release()
			if err != nil {
				return nil, err
			}
			out = append(out, expr)
			continue
		}
		expr, err := t.transformExpression(eCtx)
		if err != nil {
			return nil, err
		}
		out = append(out, expr)
	}
	return out, nil
}

// wrapImmutableFieldValue builds the `std.NewImmutable(value)` wrapper that
// backs an immutable (`val`) struct field, naming the type argument explicitly
// when the value alone would infer the wrong one.
//
// Go infers NewImmutable's type parameter from its argument, and an untyped
// constant argument collapses to its own default type (`0` → int, `1.5` →
// float64). A field declared `int64` therefore receives an Immutable[int],
// which is not assignable to Immutable[int64] — even though the constant is
// perfectly representable and a plain Go field assignment would have converted
// it. Spelling the type argument (`std.NewImmutable[int64](0)`) restores that
// conversion at the argument position.
//
// typeArgs maps the struct's type-parameter names to the type arguments of the
// literal being built, so a field declared with a type parameter resolves to the
// type it is instantiated with at this construction site.
func (t *galaASTTransformer) wrapImmutableFieldValue(value ast.Expr, fieldType transpiler.Type, typeArgs map[string]ast.Expr) ast.Expr {
	if typeArg := immutableFieldTypeArg(value, fieldType, typeArgs); typeArg != nil {
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

// immutableFieldTypeArg returns the explicit NewImmutable type argument for a
// field whose declared type differs from the default type of an untyped
// constant value, or nil when plain inference is already correct.
//
// The rewrite is deliberately confined to untyped numeric constants going into
// a field whose type resolves to a predeclared numeric type: that is exactly the
// set of values whose type Go would have taken from the destination but takes
// from the argument once the NewImmutable wrapper intervenes. Anything else (a
// typed expression, a named or generic field type, a type parameter with no
// concrete instantiation here) keeps the inferred form, so no construction that
// compiles today changes shape.
func immutableFieldTypeArg(value ast.Expr, fieldType transpiler.Type, typeArgs map[string]ast.Expr) ast.Expr {
	if transpiler.IsUnusable(fieldType) {
		return nil
	}
	var fieldName string
	switch ft := fieldType.(type) {
	case transpiler.BasicType:
		fieldName = ft.Name
	case transpiler.NamedType:
		if ft.Package != "" {
			return nil
		}
		fieldName = ft.Name
	default:
		return nil
	}
	// A field declared with one of the struct's type parameters takes the type
	// it is instantiated with here (`Box[int64](0)` → NewImmutable[int64]).
	if arg, isTypeParam := typeArgs[fieldName]; isTypeParam {
		argIdent, ok := arg.(*ast.Ident)
		if !ok {
			return nil
		}
		fieldName = argIdent.Name
	}
	if !isNumericPrimitive(fieldName) {
		return nil
	}
	defaultName, ok := untypedNumericConstDefault(value)
	if !ok || defaultName == fieldName {
		return nil
	}
	return ast.NewIdent(fieldName)
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

// untypedNumericConstDefault reports the Go default type of an untyped numeric
// constant expression (`0` → int, `1.5` → float64, `'a'` → rune) and false for
// anything that is not built purely from numeric literals. Constant arithmetic
// stays untyped in Go, so `60 * 1000` is classified like a bare literal.
func untypedNumericConstDefault(expr ast.Expr) (string, bool) {
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
		return untypedNumericConstDefault(e.X)
	case *ast.UnaryExpr:
		switch e.Op {
		case token.ADD, token.SUB, token.XOR:
			return untypedNumericConstDefault(e.X)
		}
	case *ast.BinaryExpr:
		switch e.Op {
		case token.SHL, token.SHR:
			// The shift count does not influence the result's default type.
			return untypedNumericConstDefault(e.X)
		case token.ADD, token.SUB, token.MUL, token.QUO, token.REM,
			token.AND, token.OR, token.XOR, token.AND_NOT:
			left, okLeft := untypedNumericConstDefault(e.X)
			right, okRight := untypedNumericConstDefault(e.Y)
			if !okLeft || !okRight {
				return "", false
			}
			if numericConstRank(right) > numericConstRank(left) {
				return right, true
			}
			return left, true
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

func (t *galaASTTransformer) transformLiteral(ctx *grammar.LiteralContext) (ast.Expr, error) {
	if ctx.INT_LIT() != nil {
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
