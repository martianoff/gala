package transformer

import (
	"go/ast"
	"go/token"
	"strings"

	"github.com/antlr4-go/antlr/v4"

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

// interpolationPart represents a piece of an interpolated string.
type interpolationPart struct {
	isLiteral  bool   // true = literal text, false = expression
	text       string // literal text content (unescaped) or expression source
	formatSpec string // explicit format spec (f-strings only), e.g. "%04d"
}

// transformInterpolatedString handles s"..." string interpolation.
// Auto-infers format verbs from expression types.
func (t *galaASTTransformer) transformInterpolatedString(raw string) (ast.Expr, error) {
	// Strip s" prefix and " suffix
	content := raw[2 : len(raw)-1]
	parts := parseInterpolationParts(content)
	return t.buildSprintfCall(parts, false)
}

// transformFormatString handles f"..." format string interpolation.
// Uses explicit format specs when provided, falls back to auto-infer.
func (t *galaASTTransformer) transformFormatString(raw string) (ast.Expr, error) {
	// Strip f" prefix and " suffix
	content := raw[2 : len(raw)-1]
	parts := parseInterpolationParts(content)
	return t.buildSprintfCall(parts, true)
}

// buildSprintfCall generates a fmt.Sprintf call from interpolation parts.
// If there are no interpolation expressions, returns a plain string literal.
func (t *galaASTTransformer) buildSprintfCall(parts []interpolationPart, isFormatString bool) (ast.Expr, error) {
	// Check if there are any expression parts
	hasExprs := false
	for _, p := range parts {
		if !p.isLiteral {
			hasExprs = true
			break
		}
	}

	// No interpolation — return plain string literal
	if !hasExprs {
		var sb strings.Builder
		for _, p := range parts {
			sb.WriteString(p.text)
		}
		return &ast.BasicLit{Kind: token.STRING, Value: `"` + sb.String() + `"`}, nil
	}

	// Build format string and argument list
	var formatBuf strings.Builder
	var args []ast.Expr

	for _, p := range parts {
		if p.isLiteral {
			// Escape any % in literal text for Sprintf
			formatBuf.WriteString(strings.ReplaceAll(p.text, "%", "%%"))
			continue
		}

		// Parse expression and transform to Go AST
		goExpr, err := t.parseAndTransformExpr(p.text)
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

		if isFormatString && p.formatSpec != "" {
			verb = p.formatSpec
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

	return &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   ast.NewIdent("fmt"),
			Sel: ast.NewIdent("Sprintf"),
		},
		Args: allArgs,
	}, nil
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

// parseInterpolationParts splits interpolated string content into literal and expression parts.
// Handles: $identifier, ${expression}, $$ (literal $)
// For f-strings, also extracts %spec after expressions.
func parseInterpolationParts(content string) []interpolationPart {
	var parts []interpolationPart
	var literal strings.Builder
	i := 0

	for i < len(content) {
		if content[i] == '\\' && i+1 < len(content) {
			// Escape sequence — pass through as-is
			literal.WriteByte(content[i])
			literal.WriteByte(content[i+1])
			i += 2
			continue
		}

		if content[i] != '$' {
			literal.WriteByte(content[i])
			i++
			continue
		}

		// Found $
		if i+1 >= len(content) {
			literal.WriteByte('$')
			i++
			continue
		}

		next := content[i+1]

		// $$ → literal $
		if next == '$' {
			literal.WriteByte('$')
			i += 2
			continue
		}

		// ${expr} or ${expr}%spec
		if next == '{' {
			// Flush literal
			if literal.Len() > 0 {
				parts = append(parts, interpolationPart{isLiteral: true, text: literal.String()})
				literal.Reset()
			}

			// Find matching } with brace nesting
			braceDepth := 1
			j := i + 2
			for j < len(content) && braceDepth > 0 {
				if content[j] == '{' {
					braceDepth++
				} else if content[j] == '}' {
					braceDepth--
				} else if content[j] == '\\' && j+1 < len(content) {
					j++ // skip escaped char
				}
				if braceDepth > 0 {
					j++
				}
			}

			exprText := unescapeInterpolationExpr(content[i+2 : j])
			j++ // skip closing }

			// Check for format spec
			fmtSpec := ""
			if j < len(content) && content[j] == '%' {
				fmtSpec, j = extractFormatSpec(content, j)
			}

			parts = append(parts, interpolationPart{isLiteral: false, text: exprText, formatSpec: fmtSpec})
			i = j
			continue
		}

		// $identifier
		if isIdentStart(next) {
			// Flush literal
			if literal.Len() > 0 {
				parts = append(parts, interpolationPart{isLiteral: true, text: literal.String()})
				literal.Reset()
			}

			j := i + 1
			for j < len(content) && isIdentPart(content[j]) {
				j++
			}

			identName := content[i+1 : j]

			// Check for format spec
			fmtSpec := ""
			if j < len(content) && content[j] == '%' {
				fmtSpec, j = extractFormatSpec(content, j)
			}

			parts = append(parts, interpolationPart{isLiteral: false, text: identName, formatSpec: fmtSpec})
			i = j
			continue
		}

		// $ followed by something else — treat as literal $
		literal.WriteByte('$')
		i++
	}

	// Flush remaining literal
	if literal.Len() > 0 {
		parts = append(parts, interpolationPart{isLiteral: true, text: literal.String()})
	}

	return parts
}

// unescapeInterpolationExpr converts escaped quotes inside ${} expression blocks
// back to regular quotes so the expression can be re-parsed by the GALA parser.
func unescapeInterpolationExpr(expr string) string {
	return strings.ReplaceAll(expr, `\"`, `"`)
}

// extractFormatSpec extracts a Go printf format specifier starting at position i (which points to %).
// Returns the format spec string and the position after it.
func extractFormatSpec(content string, i int) (string, int) {
	if i >= len(content) || content[i] != '%' {
		return "", i
	}

	j := i + 1 // skip %

	// Skip flags: +, -, #, ' ', 0
	for j < len(content) {
		c := content[j]
		if c == '+' || c == '-' || c == '#' || c == ' ' || c == '0' {
			j++
		} else {
			break
		}
	}

	// Skip width: digits or *
	if j < len(content) && content[j] == '*' {
		j++
	} else {
		for j < len(content) && content[j] >= '0' && content[j] <= '9' {
			j++
		}
	}

	// Skip precision: .digits or .*
	if j < len(content) && content[j] == '.' {
		j++
		if j < len(content) && content[j] == '*' {
			j++
		} else {
			for j < len(content) && content[j] >= '0' && content[j] <= '9' {
				j++
			}
		}
	}

	// Verb character
	if j < len(content) && isFormatVerb(content[j]) {
		j++
		return content[i:j], j
	}

	// No valid verb found — not a format spec, treat % as literal
	return "", i
}

// isFormatVerb returns true if c is a valid Go printf verb character.
func isFormatVerb(c byte) bool {
	switch c {
	case 'b', 'c', 'd', 'e', 'E', 'f', 'F', 'g', 'G', 'o', 'O', 'p', 'q', 's', 't', 'T', 'U', 'v', 'x', 'X':
		return true
	}
	return false
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
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

