package transformer

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"

	"martianoff/gala/galaerr"
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
	return &ast.SelectorExpr{
		X:   t.fmtPackage(),
		Sel: ast.NewIdent(base.(*ast.Ident).Name),
	}
}

// fmtAlias is the name generated code imports Go's fmt under when its own
// name is taken here, so that a call GALA lowers to fmt (Println, s"...")
// does not reach what took it.
const fmtAlias = localNamePrefix + "fmt"

// fmtPackage names Go's fmt package where generated code calls it, and makes
// the file import it under that name: the name the file imports fmt under, or
// fmt, unless a binding, type parameter, function or type has that name.
func (t *galaASTTransformer) fmtPackage() *ast.Ident {
	name := "fmt"
	imported := false
	if e, ok := t.importManager.GetByPath("fmt"); ok && e != nil && !e.IsDot && !e.Implicit() {
		name, imported = e.QualifierFor("fmt"), true
	}
	if t.nameTaken(name) {
		t.needsFmtAliasImport = true
		return ast.NewIdent(fmtAlias)
	}
	if !imported {
		t.needsFmtImport = true
	}
	return ast.NewIdent(name)
}

// nameTaken reports whether name, at this point of the file, means something
// other than a package: a binding in scope, a type parameter, or a function
// or type of the package.
func (t *galaASTTransformer) nameTaken(name string) bool {
	if _, _, bound := t.scopeLookup(name); bound {
		return true
	}
	return t.activeTypeParams[name] || t.isKnownFunctionName(name) || t.getTypeMeta(name) != nil
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
// It takes the literal's token rather than its text so the embedded
// expressions can be re-parsed at their real source position; see
// parseAndTransformExpr for why that matters.
func (t *galaASTTransformer) transformInterpolatedString(tok litTok) (ast.Expr, error) {
	// Strip s" prefix and " suffix
	content := tok.text[2 : len(tok.text)-1]
	parts := interpolation.Split(content, false)
	line, col := interpContentOrigin(tok)
	return t.buildSprintfCall(parts, false, content, line, col)
}

// transformFormatString handles f"..." format string interpolation.
// Uses explicit format specs when provided, falls back to auto-infer.
func (t *galaASTTransformer) transformFormatString(tok litTok) (ast.Expr, error) {
	// Strip f" prefix and " suffix
	content := tok.text[2 : len(tok.text)-1]
	parts := interpolation.Split(content, true)
	line, col := interpContentOrigin(tok)
	return t.buildSprintfCall(parts, true, content, line, col)
}

// interpContentOrigin returns the 1-based line and 0-based rune column at which
// an interpolated literal's CONTENT begins — that is, just past the two-rune
// `s"` / `f"` opener. A position-less token (line 0) yields (0, 0), which
// callers treat as "no position available" and fall back to unrebased
// parsing.
func interpContentOrigin(tok litTok) (int, int) {
	if tok.line == 0 {
		return 0, 0
	}
	return tok.line, tok.col + 2
}

// interpPartOrigin maps a part's byte Offset within content to an absolute
// source position, given where the content itself starts. Interpolated literals
// are single-line in practice, but a literal carrying a real newline is handled
// rather than silently mispositioned: each newline advances the line and resets
// the column to the content's own left margin of 0.
func interpPartOrigin(content string, offset, baseLine, baseCol int) (int, int) {
	if baseLine <= 0 {
		return baseLine, baseCol
	}
	return interpolation.Position(content, offset, baseLine, baseCol)
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

	// Build the format string, decoded, and the argument list. A literal
	// run's escapes are decoded before its `%`s are doubled, so an escape
	// that stands for `%` (`\x25`, `\045`, `\u0025`) is text, not a verb.
	var formatBuf strings.Builder
	var args []ast.Expr

	for _, p := range parts {
		if p.IsLiteral {
			text, err := decodeEscapes(p.Text)
			if err != nil {
				return nil, err
			}
			formatBuf.WriteString(strings.ReplaceAll(text, "%", "%%"))
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

		explicit := isFormatString && p.FormatSpec != ""
		// A value printed by its String: with no spec, or one fmt would
		// print it by String with (%v, %s, not %#v).
		if !explicit || p.PrintedByString() {
			goExpr, typ, err = t.printedValue(goExpr, typ, exprLine, exprCol)
			if err != nil {
				return nil, err
			}
		} else if strings.ContainsAny(p.FormatSpec[len(p.FormatSpec)-1:], "qxX") {
			// fmt calls String for %q, %x and %X too, so a generated String
			// copies the fields it prints as it does for %v.
			if err := t.checkPrintedFieldsCopy(typ, exprLine, exprCol); err != nil {
				return nil, err
			}
		}
		if explicit {
			verb = p.FormatSpec
		} else {
			verb = formatVerbForType(typ)
		}

		formatBuf.WriteString(verb)
		args = append(args, goExpr)
	}

	allArgs := make([]ast.Expr, 0, 1+len(args))
	allArgs = append(allArgs, &ast.BasicLit{
		Kind:  token.STRING,
		Value: strconv.Quote(formatBuf.String()),
	})
	allArgs = append(allArgs, args...)

	call := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   t.fmtPackage(),
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
//   - Errors must be REPORTED. A parse that recovered from a syntax error
//     silently once let `s"v=${x +}"` transpile to `v=%d`/`x` with the
//     dangling operator dropped and print `v=1` — invalid source producing a
//     working binary. The expression must also be the whole text: a token
//     left over after it is an error too.
//
//   - Positions must be ABSOLUTE. Parsing the bare expression text put every
//     token on line 1, so a diagnostic raised while transforming it (a bare
//     `len` inside an interpolation, say) pointed at line 1 of the file. The
//     lexer is started at the literal's own position instead, so every token
//     it emits carries a true absolute position.
//
// Starting the lexer there is O(1), where padding the input with the
// equivalent newlines and spaces would re-lex a prefix as long as the line
// number for EVERY embedded expression, which is quadratic-ish in file size
// across a file full of interpolations.
//
// line is 1-based and col 0-based; a line <= 0 means the caller had no
// position and the expression is parsed at 1:0. The parsing itself is
// parseExprAt, shared with declared default values.
func (t *galaASTTransformer) parseAndTransformExpr(exprText string, line, col int) (ast.Expr, error) {
	e, err := t.parseExprAt(exprText, line, col, "interpolated expression")
	if err != nil {
		return nil, err
	}
	// The analyzer checked the file's own parse tree, which holds this
	// expression only as string text.
	if err := t.checkGeneratedCodecNamesOf(e); err != nil {
		return nil, err
	}
	if err := t.checkVariantTypeNamesNative(e.node); err != nil {
		return nil, err
	}
	outer := t.interpolationScope
	t.interpolationScope = t.currentScope
	defer func() { t.interpolationScope = outer }()
	return t.lowerExpr(e)
}

// placeholderInInterpolation is GALA-E0137 when name, a primary written at
// at, is a `_` read in an interpolated expression that no placeholder lambda
// written inside the expression binds (`s"${xs.Map(_ * 2)}"` is fine). The
// innermost `_` decides: one a `_` parameter or typed pattern binds names no
// value to read, and an unbound one is never valid.
func (t *galaASTTransformer) placeholderInInterpolation(name string, at srcNode) error {
	if name != "_" || t.interpolationScope == nil {
		return nil
	}
	s := t.currentScope
	for ; s != nil && s != t.interpolationScope; s = s.parent {
		if _, ok := s.vals[name]; ok {
			break
		}
	}
	if s == t.interpolationScope || s == nil {
		// Not bound inside the string; outside it, a `_` parameter or typed
		// pattern may bind it, and a placeholder lambda does not reach in.
		s = t.bindingScope(name)
	} else if s.placeholder {
		return nil
	}
	r := at.rng()
	if s != nil && !s.placeholder {
		return spanErrorAt(galaerr.CodePlaceholderInInterpolation, r, r,
			"`_` inside an interpolated string reads a `_` binding, which names no value",
			"bind a name instead of `_` and read that name; for example `(x) => x + 1` or `case n: int => n`")
	}
	return spanErrorAt(galaerr.CodePlaceholderInInterpolation, r, r,
		"`_` inside an interpolated string is not a placeholder: a placeholder lambda takes only the `_` written outside the string",
		"write an explicit lambda; name its parameter and use the name inside the string: `(x) => s\"v${x}\"`")
}

// parseExprAt parses text as one expression whose first character sits at
// line/col of the file into a native tree of its own, made by galop's
// recovering parser as the file's is (parser.ParseExpressionCheckedAt). The
// lowering reads only the nodes it is given, so it lowers this tree as it
// lowers the file's. A panic in galop's lexer or parser is an internal error
// (GALA-E0017) at the text's position.
//
// Each text is parsed once per position per file (parsedExprs): a default is
// lowered at every use site, and an interpolation in it with it, but the tree
// is the same at each, so the caches keyed by its nodes (escapeCache, the
// hint stack's keys) hit.
func (t *galaASTTransformer) parseExprAt(text string, line, col int, what string) (exprSrc, error) {
	file := t.filePath
	if t.loweringDefault != nil && t.loweringDefault.src.file != "" {
		file = t.loweringDefault.src.file
	}
	key := parsedExprKey{file: file, text: text, what: what, line: line, col: col}
	if e, ok := t.parsedExprs[key]; ok {
		return e, nil
	}
	n, err := parser.ParseExpressionCheckedAt(text, line, col, what)
	if err != nil {
		return exprSrc{}, err
	}
	if t.onParsedExpr != nil {
		t.onParsedExpr(n.Tree(), what, file)
	}
	e := nativeExpr(n.GalaNodeRef)
	if t.parsedExprs == nil {
		t.parsedExprs = make(map[parsedExprKey]exprSrc)
	}
	t.parsedExprs[key] = e
	return e, nil
}

// parsedExprKey is an expression's text as parseExprAt parses it: written at
// line/col of file, and parsed as what (which a syntax error names).
type parsedExprKey struct {
	file, text, what string
	line, col        int
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
