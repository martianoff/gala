package transformer

import (
	"go/ast"
	"go/token"
	"strings"
	"unicode/utf8"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/interpolation"
	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/transpiler"
)

// rewriteBuiltinPrintFuncs rewrites bare Println/Print calls to fmt.Println/fmt.Print.
// This enables calling Println/Print without importing fmt.
func (t *galaASTTransformer) rewriteBuiltinPrintFuncs(base ast.Expr) ast.Expr {
	if !t.isBuiltinPrint(base) {
		return base
	}
	t.needsFmtImport = true
	return &ast.SelectorExpr{
		X:   ast.NewIdent("fmt"),
		Sel: ast.NewIdent(base.(*ast.Ident).Name),
	}
}

// isBuiltinPrint reports whether base names GALA's Println or Print builtin —
// not a local variable or parameter of the same name.
func (t *galaASTTransformer) isBuiltinPrint(base ast.Expr) bool {
	id, ok := base.(*ast.Ident)
	return ok && (id.Name == "Println" || id.Name == "Print") && !t.isVal(id.Name) && !t.isVar(id.Name)
}

// transformInterpolatedString handles s"..." string interpolation.
// Auto-infers format verbs from expression types.
//
// It takes the literal's terminal node rather than its text so the embedded
// expressions can be re-parsed at their real source position; see
// parseAndTransformExpr for why that matters.
func (t *galaASTTransformer) transformInterpolatedString(node antlr.TerminalNode) (ast.Expr, error) {
	// Strip s" prefix and " suffix
	raw := node.GetText()
	content := raw[2 : len(raw)-1]
	parts := interpolation.Split(content)
	line, col := interpContentOrigin(node)
	return t.buildSprintfCall(parts, false, content, line, col)
}

// transformFormatString handles f"..." format string interpolation.
// Uses explicit format specs when provided, falls back to auto-infer.
func (t *galaASTTransformer) transformFormatString(node antlr.TerminalNode) (ast.Expr, error) {
	// Strip f" prefix and " suffix
	raw := node.GetText()
	content := raw[2 : len(raw)-1]
	parts := interpolation.Split(content)
	line, col := interpContentOrigin(node)
	return t.buildSprintfCall(parts, true, content, line, col)
}

// interpContentOrigin returns the 1-based line and 0-based rune column at which
// an interpolated literal's CONTENT begins — that is, just past the two-rune
// `s"` / `f"` opener. A nil or position-less node yields (0, 0), which callers
// treat as "no position available" and fall back to unrebased parsing.
func interpContentOrigin(node antlr.TerminalNode) (int, int) {
	if node == nil {
		return 0, 0
	}
	sym := node.GetSymbol()
	if sym == nil {
		return 0, 0
	}
	return sym.GetLine(), sym.GetColumn() + 2
}

// interpPartOrigin maps a part's byte Offset within content to an absolute
// source position, given where the content itself starts. Interpolated literals
// are single-line in practice, but a literal carrying a real newline is handled
// rather than silently mispositioned: each newline advances the line and resets
// the column to the content's own left margin of 0.
func interpPartOrigin(content string, offset, baseLine, baseCol int) (int, int) {
	if baseLine <= 0 || offset < 0 || offset > len(content) {
		return baseLine, baseCol
	}
	prefix := content[:offset]
	if nl := strings.LastIndexByte(prefix, '\n'); nl >= 0 {
		return baseLine + strings.Count(prefix, "\n"), utf8.RuneCountInString(prefix[nl+1:])
	}
	// Columns are counted in runes because that is what the diagnostic
	// renderer indexes by, while Offset is a byte index into the content.
	// RuneCountInString rather than len([]rune(...)) so counting does not
	// allocate a slice it immediately discards — this runs per embedded
	// expression, on every interpolated string compiled.
	return baseLine, baseCol + utf8.RuneCountInString(prefix)
}

// buildSprintfCall generates a fmt.Sprintf call from interpolation parts.
// If there are no interpolation expressions, returns a plain string literal.
func (t *galaASTTransformer) buildSprintfCall(parts []interpolation.Part, isFormatString bool, content string, baseLine, baseCol int) (ast.Expr, error) {
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

		// Parse expression and transform to Go AST, at its real position so
		// any diagnostic raised below points into the interpolation.
		exprLine, exprCol := interpPartOrigin(content, p.Offset, baseLine, baseCol)
		goExpr, err := t.parseAndTransformExpr(p.Text, exprLine, exprCol)
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
//
// The expression is re-parsed on its own, so two things have to be arranged
// deliberately that a whole-file parse gets for free:
//
//   - Errors must be REPORTED. The listeners are replaced, not merely removed:
//     stripping them without a substitute made ANTLR error-recover silently, so
//     `s"v=${x +}"` transpiled to `v=%d`/`x` with the dangling operator dropped
//     and printed `v=1` — invalid source producing a working binary.
//
//   - Positions must be ABSOLUTE. Parsing the bare expression text put every
//     token on line 1, so a diagnostic raised while transforming it (a bare
//     `len` inside an interpolation, say) pointed at line 1 of the file. ANTLR
//     has no setter for a token's position after the fact, but the lexer can be
//     SEEDED before it consumes anything: its ATN simulator tracks the current
//     line and column in exported fields, so starting it at the literal's own
//     position makes every token it emits carry a true absolute position.
//
// Seeding is what this does rather than padding the input with the equivalent
// newlines and spaces. Padding also works — whitespace is skipped, so the tree
// is identical either way — but it allocates and re-lexes a prefix as long as
// the line number for EVERY embedded expression, which is quadratic-ish in file
// size across a file full of interpolations. Seeding is O(1).
//
// line is 1-based and col 0-based, as ANTLR reports them; a line <= 0 means the
// caller had no position and the expression is parsed at 1:0. The parsing
// itself is parser.ParseExpressionAt, shared with declared default values.
func (t *galaASTTransformer) parseAndTransformExpr(exprText string, line, col int) (ast.Expr, error) {
	exprCtx, err := parser.ParseExpressionAt(exprText, line, col, "interpolated expression")
	if err != nil {
		return nil, err
	}
	// The analyzer checked the file's own parse tree, which holds this
	// expression only as string text.
	if err := CheckGeneratedCodecNames(exprCtx); err != nil {
		return nil, err
	}
	return t.transformExpression(exprCtx)
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
