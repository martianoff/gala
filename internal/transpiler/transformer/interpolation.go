package transformer

import (
	"go/ast"
	"go/token"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/interpolation"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// rewriteBuiltinPrintFuncs rewrites bare Println/Print calls to fmt.Println/fmt.Print.
// This enables calling Println/Print without importing fmt.
func (t *galaASTTransformer) rewriteBuiltinPrintFuncs(base ast.Expr) ast.Expr {
	id, ok := base.(*ast.Ident)
	if !ok {
		return base
	}
	switch id.Name {
	case "Println", "Print":
		// Don't rewrite if it's a local variable or function parameter
		if t.isVal(id.Name) || t.isVar(id.Name) {
			return base
		}
		t.needsFmtImport = true
		return &ast.SelectorExpr{
			X:   ast.NewIdent("fmt"),
			Sel: ast.NewIdent(id.Name),
		}
	}
	return base
}

// transformInterpolatedString handles s"..." string interpolation.
// Auto-infers format verbs from expression types.
func (t *galaASTTransformer) transformInterpolatedString(raw string) (ast.Expr, error) {
	// Strip s" prefix and " suffix
	content := raw[2 : len(raw)-1]
	parts := interpolation.Split(content)
	return t.buildSprintfCall(parts, false)
}

// transformFormatString handles f"..." format string interpolation.
// Uses explicit format specs when provided, falls back to auto-infer.
func (t *galaASTTransformer) transformFormatString(raw string) (ast.Expr, error) {
	// Strip f" prefix and " suffix
	content := raw[2 : len(raw)-1]
	parts := interpolation.Split(content)
	return t.buildSprintfCall(parts, true)
}

// buildSprintfCall generates a fmt.Sprintf call from interpolation parts.
// If there are no interpolation expressions, returns a plain string literal.
func (t *galaASTTransformer) buildSprintfCall(parts []interpolation.Part, isFormatString bool) (ast.Expr, error) {
	// Check if there are any expression parts
	hasExprs := false
	for _, p := range parts {
		if !p.IsLiteral {
			hasExprs = true
			break
		}
	}

	// No interpolation — return plain string literal
	if !hasExprs {
		var sb strings.Builder
		for _, p := range parts {
			sb.WriteString(p.Text)
		}
		return &ast.BasicLit{Kind: token.STRING, Value: `"` + sb.String() + `"`}, nil
	}

	// Build format string and argument list
	var formatBuf strings.Builder
	var args []ast.Expr

	for _, p := range parts {
		if p.IsLiteral {
			// Escape any % in literal text for Sprintf
			formatBuf.WriteString(strings.ReplaceAll(p.Text, "%", "%%"))
			continue
		}

		// Parse expression and transform to Go AST
		goExpr, err := t.parseAndTransformExpr(p.Text)
		if err != nil {
			return nil, err
		}

		// Determine format verb, unwrapping Immutable[T] if needed
		var verb string
		typ := t.getExprTypeNameManual(goExpr)

		// If the expression type is Immutable[T], auto-unwrap with .Get()
		if t.isImmutableType(typ) {
			goExpr = &ast.CallExpr{
				Fun: &ast.SelectorExpr{X: goExpr, Sel: ast.NewIdent("Get")},
			}
			// Use the inner type for format verb selection
			if gt, ok := typ.(transpiler.GenericType); ok && len(gt.Params) > 0 {
				typ = gt.Params[0]
			}
		}

		if isFormatString && p.FormatSpec != "" {
			verb = p.FormatSpec
		} else {
			verb = formatVerbForType(typ)
		}

		formatBuf.WriteString(verb)
		args = append(args, goExpr)
	}

	t.needsFmtImport = true

	allArgs := make([]ast.Expr, 0, 1+len(args))
	allArgs = append(allArgs, &ast.BasicLit{
		Kind:  token.STRING,
		Value: `"` + formatBuf.String() + `"`,
	})
	allArgs = append(allArgs, args...)

	call := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   ast.NewIdent("fmt"),
			Sel: ast.NewIdent("Sprintf"),
		},
		Args: allArgs,
	}
	// Tag the resulting CallExpr with type `string` so downstream type
	// inference (getExprTypeNameManual / getExprTypeName) recognises it
	// without re-deriving the return type from `fmt.Sprintf`. Without this,
	// when the Go SDK is unavailable for type info, an if-expression whose
	// arms are s-string interpolations would widen its IIFE return type to
	// `func() any` and break callers that expect a concrete `string`.
	t.exprTypeCache[call] = transpiler.BasicType{Name: "string"}
	return call, nil
}

// parseAndTransformExpr parses a GALA expression string and transforms it to Go AST.
func (t *galaASTTransformer) parseAndTransformExpr(exprText string) (ast.Expr, error) {
	is := antlr.NewInputStream(exprText)
	lexer := grammar.NewgalaLexer(is)
	stream := antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel)
	p := grammar.NewgalaParser(stream)

	// Suppress error output
	lexer.RemoveErrorListeners()
	p.RemoveErrorListeners()

	exprCtx := p.Expression()
	return t.transformExpression(exprCtx.(*grammar.ExpressionContext))
}

// formatVerbForType returns the Go printf format verb for a GALA type.
func formatVerbForType(typ transpiler.Type) string {
	switch t := typ.(type) {
	case transpiler.BasicType:
		return formatVerbForBasicType(t.Name)
	case transpiler.NamedType:
		return formatVerbForBasicType(t.Name)
	default:
		return "%v"
	}
}

func formatVerbForBasicType(name string) string {
	switch name {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "byte":
		return "%d"
	case "float32", "float64":
		return "%g"
	case "string":
		return "%s"
	case "bool":
		return "%t"
	case "rune":
		return "%c"
	default:
		return "%v"
	}
}

