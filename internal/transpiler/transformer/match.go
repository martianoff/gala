package transformer

import (
	"errors"
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

// containsUserReturnInClauses reports whether any user-written `return X`, or
// bare `return`, appears inside the if-else clauses or default body of a
// lowered match.
func (t *galaASTTransformer) containsUserReturnInClauses(clauses []ast.Stmt, defaultBody []ast.Stmt) bool {
	for _, c := range clauses {
		if t.stmtContainsUserReturn(c) {
			return true
		}
	}
	return t.containsUserReturnStmt(defaultBody)
}

// buildMatchBodyForInline builds the if-else chain for an inlined statement-
// position match. Unlike buildMatchBody, it does NOT call stripReturnStatements
// (which would corrupt user returns) or fixupReturnStatements (only useful
// for the IIFE's value channel). Synthesized arm-tail returns are removed so
// they do not erroneously exit the enclosing function with a discarded value.
func (t *galaASTTransformer) buildMatchBodyForInline(clauses []ast.Stmt, defaultBody []ast.Stmt) []ast.Stmt {
	return t.stripSynthesizedArmReturns(chainMatchClauses(clauses, defaultBody))
}

// chainMatchClauses chains the lowered arms of a match into one if-else
// chain, the default arm's body as its final else.
func chainMatchClauses(clauses []ast.Stmt, defaultBody []ast.Stmt) []ast.Stmt {
	var rootIf ast.Stmt
	var currentIf *ast.IfStmt
	for _, clause := range clauses {
		if rootIf == nil {
			rootIf = clause
			currentIf = findLeafIf(clause)
		} else if currentIf != nil {
			currentIf.Else = clause
			currentIf = findLeafIf(clause)
		}
	}
	if rootIf == nil {
		return defaultBody
	}
	if len(defaultBody) > 0 && currentIf != nil {
		currentIf.Else = &ast.BlockStmt{List: defaultBody}
	}
	return []ast.Stmt{rootIf}
}

// buildInlinedMatchBlock wraps the inlined match body in a Go block that
// binds the subject to paramName, mirroring the IIFE's parameter binding so
// the if-else chain (which references paramName) stays well-formed. The block
// also contains a fresh scope so the binding does not leak.
func (t *galaASTTransformer) buildInlinedMatchBlock(expr ast.Expr, paramName string, matchedType transpiler.Type, body []ast.Stmt) *ast.BlockStmt {
	stmts := make([]ast.Stmt, 0, len(body)+1)
	// The subject is evaluated either way. When no arm reads it (`case _ =>`
	// only), binding it would be an unused Go variable, so it is discarded.
	bind := &ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent("_")}, Tok: token.ASSIGN, Rhs: []ast.Expr{expr}}
	if readsName(body, paramName) {
		bind.Lhs[0], bind.Tok = ast.NewIdent(paramName), token.DEFINE
	}
	stmts = append(stmts, bind)
	stmts = append(stmts, body...)
	return &ast.BlockStmt{List: stmts}
}

// readsName reports whether stmts read the identifier name. The name a `:=`
// declares is not a read: a nested inlined match declares its own `obj`.
func readsName(stmts []ast.Stmt, name string) bool {
	found := false
	var visit func(ast.Node) bool
	visit = func(n ast.Node) bool {
		if found {
			return false
		}
		switch x := n.(type) {
		case *ast.AssignStmt:
			if x.Tok == token.DEFINE {
				for _, r := range x.Rhs {
					ast.Inspect(r, visit)
				}
				return false
			}
		case *ast.Ident:
			found = x.Name == name
		}
		return true
	}
	for _, s := range stmts {
		ast.Inspect(s, visit)
	}
	return found
}

// extractVariantName extracts the variant/constructor name from a case pattern text.
// E.g. "Circle(r)" → "Circle", "Point()" → "Point", "Debug" → "Debug",
// "endFrame" → "endFrame". The name is only a candidate: callers check it
// against the variants of a sealed type, so its capitalization plays no part
// — a lowercase variant is as much a variant as a capitalized one. Callers
// leave out patterns that bind (see isBindingPatternOf).
func extractVariantName(patternText string) string {
	idx := strings.Index(patternText, "(")
	var name string
	if idx < 0 {
		// No call suffix — bare identifier form like `Debug`.
		name = patternText
	} else if idx == 0 {
		return ""
	} else {
		name = patternText[:idx]
	}
	// Strip a package qualifier: `event.Tick` → `Tick`. Sealed variants are
	// recorded by their bare case name, so a package-qualified case pattern
	// (from a qualified import, e.g. `case event.Tick(n)`) must collapse to the
	// same bare name for the exhaustiveness and arity checks to recognize it.
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		name = name[dot+1:]
	}
	// Reject anything that is not a plain ASCII identifier — a literal (`42`,
	// `"x"`), an operator expression, a typed pattern (`x: int`) — with the
	// same alphabet isBindingPattern and isSimpleIdentifier use.
	if name == "" || (name[0] >= '0' && name[0] <= '9') {
		return ""
	}
	for _, ch := range name {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_') {
			return ""
		}
	}
	return name
}

// armMatchesEverything reports whether a lowered case clause (see
// transformCaseClauseWithType) tests nothing: its condition is the constant
// `true`. Used for tuple arms, where it reads the lowering itself, so an
// element that is a lowercase sealed variant, a zero-field extractor or a
// literal counts exactly as it runs. A guard makes the condition a
// conjunction, never the bare constant. (extractBindingDefault answers the
// same question but builds the default body, and an empty arm reads as nil.)
func armMatchesEverything(clause ast.Stmt) bool {
	if block, ok := clause.(*ast.BlockStmt); ok && len(block.List) > 0 {
		clause = block.List[len(block.List)-1]
	}
	ifStmt, ok := clause.(*ast.IfStmt)
	return ok && isLiteralTrue(ifStmt.Cond)
}

// isTuplePatternOfSubjectArity reports whether a case pattern is the
// parenthesized tuple syntax `(p1, …, pn)` and the subject is a Tuple of the
// same arity. A tuple pattern shorter than its subject also lowers to an
// unconditional clause (it reads only the first elements), so the arity has to
// match before such an arm may close the match.
func (t *galaASTTransformer) isTuplePatternOfSubjectArity(pat grammar.IPatternContext, matchedType transpiler.Type) bool {
	exprPat, ok := pat.(*grammar.ExpressionPatternContext)
	if !ok {
		return false
	}
	p := t.getPrimaryFromExpression(exprPat.Expression())
	if p == nil || p.TupleExpressionList() == nil {
		return false
	}
	genType, ok := matchedType.(transpiler.GenericType)
	if !ok || genType.Base == nil || !isTupleTypeName(stripStdPrefix(genType.Base.BaseName())) {
		return false
	}
	return len(p.TupleExpressionList().AllExpression()) == len(genType.Params)
}

// unreachableDefaultBody is the synthetic `panic("unreachable")` else-branch
// that closes a match whose arms already cover every value.
func unreachableDefaultBody() []ast.Stmt {
	return []ast.Stmt{
		&ast.ExprStmt{X: &ast.CallExpr{
			Fun:  ast.NewIdent("panic"),
			Args: []ast.Expr{&ast.BasicLit{Kind: token.STRING, Value: `"unreachable"`}},
		}},
	}
}

// isExhaustiveMatch checks if a set of case patterns exhaustively covers all possible
// values of the matched type. Supports booleans (true/false) and sealed types.
// Returns (isExhaustive type, isExhaustive, missingCases).
// First return is false when the matched type is not an exhaustive type at all.
func (t *galaASTTransformer) isExhaustiveMatch(matchedType transpiler.Type, patternTexts []string) (bool, bool, []string) {
	// Check boolean exhaustiveness first
	if bt, ok := matchedType.(transpiler.BasicType); ok && bt.Name == "bool" {
		hasTrue, hasFalse := false, false
		for _, pat := range patternTexts {
			if pat == "true" {
				hasTrue = true
			}
			if pat == "false" {
				hasFalse = true
			}
		}
		var missing []string
		if !hasTrue {
			missing = append(missing, "true")
		}
		if !hasFalse {
			missing = append(missing, "false")
		}
		return true, len(missing) == 0, missing
	}
	// Fall through to sealed type check
	return t.isSealedExhaustive(matchedType, patternTexts)
}

// isSealedExhaustive checks if a set of case patterns exhaustively covers all variants
// of a sealed type. Returns (isSealed, isExhaustive, missingVariants).
// isSealed is false when the matched type is not a sealed type at all.
func (t *galaASTTransformer) isSealedExhaustive(matchedType transpiler.Type, patternTexts []string) (bool, bool, []string) {
	baseName := matchedType.BaseName()
	meta := t.getTypeMeta(baseName)
	if meta == nil || !meta.IsSealed || len(meta.SealedVariants) == 0 {
		return false, false, nil
	}

	covered := make(map[string]bool)
	for _, pat := range patternTexts {
		if name := extractVariantName(pat); name != "" {
			covered[name] = true
		}
	}

	var missing []string
	for _, v := range meta.SealedVariants {
		if !covered[v.Name] {
			missing = append(missing, v.Name)
		}
	}

	return true, len(missing) == 0, missing
}

// isNoReturnCallExpr reports whether expr is a call to a Go builtin/function
// that does not return a value (e.g., `panic(...)`). Such a call cannot be
// wrapped in a `return <expr>` statement — Go rejects `return panic(...)` as
// "no value used as value". When such a call appears at the tail of a match
// arm, the transformer must emit it as a bare statement and rely on Go's
// terminating-statement analysis (panic does not fall through) so the
// surrounding IIFE still type-checks.
//
// The diverging calls are Go's builtin `panic` and the go_builtins `Panic`
// wrapper (see isPanicWrapperCall). Any other function or method named
// `Panic` is an ordinary call that may well return a value.
func (t *galaASTTransformer) isNoReturnCallExpr(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "panic" {
		// The Go builtin. GALA source cannot spell it (GALA-E0035), so it is
		// always the transpiler's own lowering of the wrapper.
		return true
	}
	return t.isPanicWrapperCall(call)
}

// isPanicWrapperCall reports whether call calls the go_builtins `Panic`
// wrapper, which std and user code use in place of the forbidden builtin. A
// bare `Panic` is the wrapper (reached by dot-import, directly or through
// std) unless it names a GALA function or a local binding. A qualified
// `q.Panic` is the wrapper only when q is an import of the go_builtins
// package — whatever its alias — and not a value: `e.Panic(x)` on a receiver
// is an ordinary method call.
func (t *galaASTTransformer) isPanicWrapperCall(call *ast.CallExpr) bool {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name == "Panic" && t.getFunction(fun.Name) == nil && t.getType(fun.Name).IsNil()
	case *ast.SelectorExpr:
		q, ok := fun.X.(*ast.Ident)
		if !ok || fun.Sel.Name != "Panic" || !t.getType(q.Name).IsNil() {
			return false
		}
		entry, ok := t.importManager.GetByAlias(q.Name)
		return ok && !entry.IsDot && entry.PkgName == goBuiltinsPkgName
	}
	return false
}

// goBuiltinsPkgName is the package name of martianoff/gala/go_builtins, whose
// `Panic` wrapper diverges.
const goBuiltinsPkgName = "go_builtins"

// lowerMatchArmTailExpr classifies a tail expression in a match arm and
// returns the statement that should appear in its place along with the
// type that should feed common-result-type inference. (B8 — single source
// of truth for the value-vs-no-return decision that PR #209 / #214 / #260
// have each had to re-derive in slightly different ways.)
//
//   - For a no-return call (e.g. `panic("…")`), return a bare ExprStmt and
//     NilType. Wrapping in `return panic(…)` would produce invalid Go;
//     NilType keeps the arm out of common-type inference.
//   - For any other expression, return a `return <expr>` statement and
//     report the expression's inferred type.
func (t *galaASTTransformer) lowerMatchArmTailExpr(expr ast.Expr) (ast.Stmt, transpiler.Type) {
	if t.isNoReturnCallExpr(expr) {
		return &ast.ExprStmt{X: t.lowerPanicWrapperToBuiltin(expr)}, transpiler.NilType{}
	}
	return t.markSynthesizedArmReturn(&ast.ReturnStmt{Results: []ast.Expr{expr}}), t.inferResultType(expr)
}

// lowerPanicWrapperToBuiltin rewrites a call to the go_builtins.Panic wrapper
// (bare `Panic` via dot-import, or qualified `go_builtins.Panic`) into a call
// to Go's builtin `panic`. It is applied at no-return TAIL positions (match-arm
// tails) where Go's terminating-statement analysis must see a construct it
// recognizes as diverging: a bare `Panic(x)` statement is an ordinary void call
// that falls through, so the enclosing match IIFE would fail with "missing
// return", whereas `panic(x)` terminates. This lowering is the generated-Go
// counterpart of the `.Size()` sugar's `len()` emission — GALA source never
// spells bare `panic`, but the transpiler emits it where Go requires it. A bare
// Go `panic(...)` (already the builtin) passes through unchanged.
func (t *galaASTTransformer) lowerPanicWrapperToBuiltin(expr ast.Expr) ast.Expr {
	call, ok := expr.(*ast.CallExpr)
	if !ok || !t.isPanicWrapperCall(call) {
		return expr
	}
	return &ast.CallExpr{Fun: ast.NewIdent("panic"), Args: call.Args, Ellipsis: call.Ellipsis}
}

// markSynthesizedArmReturn records that ret was synthesized by the match-arm
// tail-expression lowering (i.e., it was an `ExprStmt{X}` rewritten into
// `ReturnStmt{Results: [X]}` to feed the match IIFE's value channel). User-
// written `return X` statements are NOT marked. This distinction lets the
// statement-position-with-user-return inliner strip only the synthesized
// returns and leave user returns intact so they exit the enclosing function.
func (t *galaASTTransformer) markSynthesizedArmReturn(ret *ast.ReturnStmt) *ast.ReturnStmt {
	if ret == nil {
		return nil
	}
	if t.synthesizedReturns == nil {
		t.synthesizedReturns = make(map[*ast.ReturnStmt]bool)
	}
	t.synthesizedReturns[ret] = true
	return ret
}

// armReturn is the promoteTrailingValue hook for a match arm body: it marks
// each synthesized return so stripSynthesizedArmReturns can undo them when the
// arm turns out to be void.
func (t *galaASTTransformer) armReturn(ret *ast.ReturnStmt) ast.Stmt {
	return t.markSynthesizedArmReturn(ret)
}

// isSynthesizedArmReturn reports whether ret was created by the match-arm
// tail synthesizer (see markSynthesizedArmReturn).
func (t *galaASTTransformer) isSynthesizedArmReturn(ret *ast.ReturnStmt) bool {
	if ret == nil || t.synthesizedReturns == nil {
		return false
	}
	return t.synthesizedReturns[ret]
}

// containsUserReturnStmt reports whether stmts contain a bare `return`, or a
// `return X` that was NOT synthesized by the match-arm tail lowering.
// Such a return represents user intent to exit the enclosing function.
// Like containsBareReturn, this skips constructs that establish their own
// return scope (func literals).
func (t *galaASTTransformer) containsUserReturnStmt(stmts []ast.Stmt) bool {
	for _, s := range stmts {
		if t.stmtContainsUserReturn(s) {
			return true
		}
	}
	return false
}

func (t *galaASTTransformer) stmtContainsUserReturn(stmt ast.Stmt) bool {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		if len(s.Results) == 0 {
			// A source `return` in a void function: the bare returns the
			// match lowering adds for void arms are added after this check
			// and inside the match's function literal, which this walk does
			// not enter. (In a match whose value is used a bare return is
			// rejected by validateNoBareReturnsInValueMatch.)
			return true
		}
		return !t.isSynthesizedArmReturn(s)
	case *ast.BlockStmt:
		return t.containsUserReturnStmt(s.List)
	case *ast.IfStmt:
		if s.Body != nil && t.containsUserReturnStmt(s.Body.List) {
			return true
		}
		if s.Else != nil && t.stmtContainsUserReturn(s.Else) {
			return true
		}
		return false
	case *ast.ForStmt:
		if s.Body != nil {
			return t.containsUserReturnStmt(s.Body.List)
		}
	case *ast.RangeStmt:
		if s.Body != nil {
			return t.containsUserReturnStmt(s.Body.List)
		}
	case *ast.SwitchStmt:
		if s.Body != nil {
			return t.containsUserReturnStmt(s.Body.List)
		}
	case *ast.TypeSwitchStmt:
		if s.Body != nil {
			return t.containsUserReturnStmt(s.Body.List)
		}
	case *ast.CaseClause:
		return t.containsUserReturnStmt(s.Body)
	case *ast.LabeledStmt:
		return t.stmtContainsUserReturn(s.Stmt)
	}
	return false
}

// stripSynthesizedArmReturns rewrites synthesized arm-tail `return X` statements
// inside a statement-position match body that is being inlined (so user-written
// `return X` statements are NOT touched and continue to escape the enclosing
// function). For a synthesized `return X`:
//   - If X is a valid Go expression statement (e.g., a call), replace with `X`
//     so the side effect runs even when the value is discarded.
//   - Otherwise, drop the statement entirely (the value was the only payload).
//
// User-written returns are left untouched. Recurses through BlockStmt and IfStmt
// (the only structural nodes the match lowering produces); other compound
// statements are returned unchanged.
func (t *galaASTTransformer) stripSynthesizedArmReturns(stmts []ast.Stmt) []ast.Stmt {
	out := make([]ast.Stmt, 0, len(stmts))
	for _, s := range stmts {
		switch n := s.(type) {
		case *ast.ReturnStmt:
			if len(n.Results) > 0 && t.isSynthesizedArmReturn(n) {
				if isValidGoExprStatement(n.Results[0]) {
					out = append(out, &ast.ExprStmt{X: n.Results[0]})
				}
				// Drop entirely if not a valid expression statement; the value
				// was the only payload and is being discarded.
				continue
			}
			out = append(out, n)
		case *ast.BlockStmt:
			out = append(out, &ast.BlockStmt{List: t.stripSynthesizedArmReturns(n.List)})
		case *ast.IfStmt:
			newIf := &ast.IfStmt{Init: n.Init, Cond: n.Cond}
			if n.Body != nil {
				newIf.Body = &ast.BlockStmt{List: t.stripSynthesizedArmReturns(n.Body.List)}
			}
			if n.Else != nil {
				switch e := n.Else.(type) {
				case *ast.BlockStmt:
					newIf.Else = &ast.BlockStmt{List: t.stripSynthesizedArmReturns(e.List)}
				case *ast.IfStmt:
					stripped := t.stripSynthesizedArmReturns([]ast.Stmt{e})
					if len(stripped) == 1 {
						newIf.Else = stripped[0]
					} else {
						newIf.Else = &ast.BlockStmt{List: stripped}
					}
				default:
					newIf.Else = n.Else
				}
			}
			out = append(out, newIf)
		default:
			out = append(out, s)
		}
	}
	return out
}

// containsBareReturn reports whether any statement in stmts (recursively) is a
// `return` with no results. It is used to detect "bare return" inside a branch
// of a match-as-value expression, which would generate invalid Go because the
// generated IIFE has a non-void return type.
//
// Only structural containers are traversed (blocks, if/else, the top-level
// case body). Constructs that establish their own return scope (func literals,
// nested IIFEs) are NOT traversed, since a bare return inside a nested lambda
// exits that lambda, not the enclosing match IIFE.
func containsBareReturn(stmts []ast.Stmt) bool {
	for _, s := range stmts {
		if stmtContainsBareReturn(s) {
			return true
		}
	}
	return false
}

func stmtContainsBareReturn(stmt ast.Stmt) bool {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		return len(s.Results) == 0
	case *ast.BlockStmt:
		return containsBareReturn(s.List)
	case *ast.IfStmt:
		if s.Body != nil && containsBareReturn(s.Body.List) {
			return true
		}
		if s.Else != nil && stmtContainsBareReturn(s.Else) {
			return true
		}
		return false
	case *ast.LabeledStmt:
		return stmtContainsBareReturn(s.Stmt)
	}
	return false
}

// validateNoBareReturnsInValueMatch rejects a match expression whose result is
// used as a value (non-void) but whose case bodies contain a bare `return`.
// Such a construct would emit Go like:
//
//	func(obj T) string { ...; return /* no value */ }(subject)
//
// which fails to compile with "not enough return values". The user intent is
// ambiguous between "exit the outer function" and "exit the match IIFE", so we
// reject it and point at explicit rewrites in docs/errors/GALA-E0015.md.
//
// Called after case bodies have been transformed and the common result type
// has been inferred; no-ops when resultType is void/nil.
func (t *galaASTTransformer) validateNoBareReturnsInValueMatch(
	clauses []ast.Stmt,
	defaultBody []ast.Stmt,
	resultType transpiler.Type,
	startLine, startCol int,
) error {
	if transpiler.IsUnusable(resultType) {
		return nil
	}
	if resultType != nil && resultType.IsVoid() {
		return nil
	}
	offender := false
	for _, c := range clauses {
		if stmtContainsBareReturn(c) {
			offender = true
			break
		}
	}
	if !offender && containsBareReturn(defaultBody) {
		offender = true
	}
	if !offender {
		return nil
	}
	return galaerr.NewCodedSemanticError(
		galaerr.CodeBareReturnInValueMatch,
		startLine, startCol,
		"bare `return` inside a match branch whose result is used as a value",
		"the match is wrapped in a function that must return "+resultType.String()+
			"; initialize a `val` with the match first (a `return` in it then leaves the function), restructure to early-exit before the match, or use combinators like .Recover / .GetOrElse. See docs/errors/GALA-E0015.md",
	)
}

// validateSealedVariantArity checks that each sealed-variant extractor pattern
// binds the same number of fields as the variant declares. Each argument of
// the pattern counts as one field whatever its shape: a binding, `_`, a
// literal, a nested constructor, or a parenthesized tuple pattern such as the
// `(n, s)` in `Some((n, s))`. The count comes from the parse tree, so commas
// inside nested patterns or string literals are never miscounted. Patterns
// that do not target a known sealed variant are skipped. The error points at
// the offending pattern.
func (t *galaASTTransformer) validateSealedVariantArity(matchedType transpiler.Type, patterns []grammar.IPatternContext) error {
	if transpiler.IsUnusable(matchedType) {
		return nil
	}
	meta := t.getTypeMeta(matchedType.BaseName())
	if meta == nil || !meta.IsSealed || len(meta.SealedVariants) == 0 {
		return nil
	}
	variantByName := make(map[string]*transpiler.SealedVariant, len(meta.SealedVariants))
	for i := range meta.SealedVariants {
		v := &meta.SealedVariants[i]
		variantByName[v.Name] = v
	}
	for _, pat := range patterns {
		exprPat, ok := pat.(*grammar.ExpressionPatternContext)
		if !ok {
			continue
		}
		name, argList, isCall := t.patternCallShape(exprPat.Expression())
		if !isCall {
			continue
		}
		variant, ok := variantByName[name]
		if !ok {
			continue
		}
		got := 0
		if argList != nil {
			got = len(argList.AllArgument())
		}
		want := len(variant.FieldNames)
		if got != want {
			return galaerr.NewCodedSemanticError(
				galaerr.CodeVariantArityMismatch,
				pat.GetStart().GetLine(),
				pat.GetStart().GetColumn(),
				fmt.Sprintf("sealed variant %q pattern binds %d field(s) but declares %d",
					name, got, want),
				"use `_` for unused fields",
			)
		}
	}
	return nil
}

// patternCallShape reads a call-shaped pattern from its parse tree: a
// constructor name, optionally qualified by a selector chain and optionally
// followed by type arguments, then a call — `Ctor(...)`, `Ctor[T](...)`,
// `pkg.Ctor(...)`, `pkg.Ctor[T](...)`, `a.b.Ctor(...)`. It returns the bare
// constructor name (the last selector, or the primary identifier), the call's
// argument list (nil for an empty call), and whether the pattern has that
// shape at all.
func (t *galaASTTransformer) patternCallShape(expr grammar.IExpressionContext) (name string, argList *grammar.ArgumentListContext, ok bool) {
	postfix := t.getSinglePostfixExpr(expr)
	if postfix == nil {
		return "", nil, false
	}
	primary := PrimaryOf(postfix)
	if primary == nil || primary.Identifier() == nil {
		return "", nil, false
	}
	name = primary.Identifier().GetText()

	suffixes := postfix.AllPostfixSuffix()
	if len(suffixes) == 0 {
		return "", nil, false
	}
	call := suffixes[len(suffixes)-1].(*grammar.PostfixSuffixContext)
	if suffixOpener(call) != "(" {
		return "", nil, false
	}
	prefix := suffixes[:len(suffixes)-1]
	// Optional type arguments right before the call.
	if n := len(prefix); n > 0 && suffixOpener(prefix[n-1].(*grammar.PostfixSuffixContext)) == "[" {
		prefix = prefix[:n-1]
	}
	// Everything else is a selector chain; its last member is the name.
	for _, s := range prefix {
		sel := s.(*grammar.PostfixSuffixContext)
		if sel.Identifier() == nil {
			return "", nil, false
		}
		name = sel.Identifier().GetText()
	}
	if al := call.ArgumentList(); al != nil {
		argList = al.(*grammar.ArgumentListContext)
	}
	return name, argList, true
}

// suffixOpener returns the token a postfix suffix starts with: ".", "(" or "[".
func suffixOpener(s *grammar.PostfixSuffixContext) string {
	if s.GetChildCount() == 0 {
		return ""
	}
	return s.GetChild(0).(antlr.ParseTree).GetText()
}

// inferMatchedTypeFromCases attempts to infer the sealed parent type from case pattern names.
// When the match subject type cannot be inferred from the expression itself, we look at the
// case patterns (e.g., Some/None → Option, Success/Failure → Try, Left/Right → Either)
// and resolve the parent sealed type from companion object metadata.
func (t *galaASTTransformer) inferMatchedTypeFromCases(caseClauses []grammar.ICaseClauseContext) transpiler.Type {
	for _, cc := range caseClauses {
		ccCtx := cc.(*grammar.CaseClauseContext)
		patCtx := ccCtx.Pattern()
		if patCtx == nil {
			continue
		}
		patternText := patCtx.GetText()
		if isWildcard(patternText) || t.isBindingPatternOf(patternText, nil) {
			continue
		}
		variantName := extractVariantName(patternText)
		if variantName == "" {
			continue
		}

		// Look up the variant in companion objects to find the parent sealed type
		companion := t.lookupCompanion(variantName)
		if companion == nil {
			continue
		}

		// Resolve the parent sealed type from companion metadata.
		// companion.TargetType may already include the package prefix (e.g., "std.Try"
		// from NamedType.BaseName()), so we must NOT blindly prepend companion.Package.
		meta := t.getTypeMeta(companion.TargetType)
		if meta == nil && companion.Package != "" {
			// Only add package prefix if TargetType doesn't already contain one
			if !strings.Contains(companion.TargetType, ".") {
				meta = t.getTypeMeta(companion.Package + "." + companion.TargetType)
			}
		}
		if meta == nil {
			// Last resort: strip any package prefix and try bare name
			base := companion.TargetType
			if idx := strings.LastIndex(base, "."); idx >= 0 {
				base = base[idx+1:]
			}
			meta = t.getTypeMeta(base)
		}
		if meta == nil || !meta.IsSealed {
			continue
		}

		// Build the type. For generic sealed types (Option[T], Try[T], Either[A,B]),
		// we use 'any' as the type parameter since we can't infer the concrete type
		// from the case patterns alone.
		var baseType transpiler.Type
		if companion.Package != "" {
			// Strip package prefix from TargetType if it already contains one
			// (e.g., "std.Try" -> "Try") to avoid double prefix "std.std.Try"
			typeName := companion.TargetType
			if strings.HasPrefix(typeName, companion.Package+".") {
				typeName = typeName[len(companion.Package)+1:]
			}
			baseType = transpiler.NamedType{Package: companion.Package, Name: typeName}
		} else {
			baseType = transpiler.BasicType{Name: companion.TargetType}
		}

		if len(meta.TypeParams) > 0 {
			params := make([]transpiler.Type, len(meta.TypeParams))
			for i := range meta.TypeParams {
				params[i] = transpiler.BasicType{Name: "any"}
			}
			return transpiler.GenericType{Base: baseType, Params: params}
		}
		return baseType
	}
	return nil
}

// lookupCompanion searches for a companion object by name, checking both
// fully-qualified and unqualified names across all registered companions.
func (t *galaASTTransformer) lookupCompanion(name string) *transpiler.CompanionObjectMetadata {
	// Try exact match first
	if c, ok := t.companionObjects[name]; ok {
		return c
	}
	// The package's own companion (a library keys it `pkg.Name`) shadows std's.
	if c, ok := t.companionObjects[t.packageName+"."+name]; ok && !strings.Contains(name, ".") {
		return c
	}
	// Try with std prefix
	stdName := registry.StdPackageName + "." + name
	if c, ok := t.companionObjects[stdName]; ok {
		return c
	}
	// Search all companion objects for a matching base name
	for key, c := range t.companionObjects {
		// Check if key ends with ".Name" (e.g., "std.Some" matches "Some")
		if idx := strings.LastIndex(key, "."); idx >= 0 {
			if key[idx+1:] == name {
				return c
			}
		}
	}
	return nil
}

// buildMatchBody chains case clauses into an if-else chain with default body,
// and applies void stripping or return fixup based on result type.
func (t *galaASTTransformer) buildMatchBody(clauses []ast.Stmt, defaultBody []ast.Stmt, resultType transpiler.Type) []ast.Stmt {
	body := chainMatchClauses(clauses, defaultBody)

	isVoid := resultType != nil && resultType.IsVoid()
	if isVoid {
		body = t.stripReturnStatements(body)
	} else if resultType != nil && !resultType.IsNil() && !resultType.IsAny() {
		t.fixupReturnStatements(body, resultType)
	}

	return body
}

// inferResultType infers the type of an expression used as a case clause result
func (t *galaASTTransformer) inferResultType(expr ast.Expr) transpiler.Type {
	// Check for void IIFE (from nested void match expressions)
	// A void IIFE is a CallExpr where Fun is a FuncLit with no return type
	if call, ok := expr.(*ast.CallExpr); ok {
		if funcLit, ok := call.Fun.(*ast.FuncLit); ok {
			if funcLit.Type.Results == nil {
				return transpiler.VoidType{}
			}
		}
	}

	// Check if this is a call to a known multi-return function (like fmt.Printf, fmt.Println)
	// These should be treated as void for match statement purposes. A call
	// converted to a Try (see go_results.go) is judged by the call itself.
	if call, ok := t.rawGoCall(expr).(*ast.CallExpr); ok {
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if pkgIdent, ok := sel.X.(*ast.Ident); ok {
				// Check specifically for known multi-return functions
				pkgName := pkgIdent.Name
				funcName := sel.Sel.Name
				if t.isKnownMultiReturnFunction(pkgName, funcName) {
					return transpiler.VoidType{}
				}
			}
		}
	}

	// Try manual type extraction first
	typ := t.getExprTypeNameManual(expr)
	if typ != nil && !typ.IsNil() {
		return typ
	}
	// Fall back to HM inference
	typ, _ = t.inferExprType(expr)
	if typ != nil && !typ.IsNil() {
		return typ
	}
	return transpiler.NilType{}
}

// isKnownMultiReturnFunction checks if a function is known to return multiple values.
// These functions are used for side effects and their return values shouldn't be used in match expressions.
func (t *galaASTTransformer) isKnownMultiReturnFunction(pkgName, funcName string) bool {
	// Resolve package alias
	resolvedPkg := pkgName
	if actual, ok := t.importManager.ResolveAlias(pkgName); ok {
		resolvedPkg = actual
	}

	// List of known functions that return multiple values (usually (int, error) or similar)
	switch resolvedPkg {
	case "fmt":
		switch funcName {
		case "Print", "Printf", "Println",
			"Fprint", "Fprintf", "Fprintln",
			"Scan", "Scanf", "Scanln",
			"Fscan", "Fscanf", "Fscanln",
			"Sscan", "Sscanf", "Sscanln":
			return true
		}
	case "log":
		switch funcName {
		case "Print", "Printf", "Println",
			"Fatal", "Fatalf", "Fatalln",
			"Panic", "Panicf", "Panicln":
			return true
		}
	case "io":
		switch funcName {
		case "Copy", "CopyN", "CopyBuffer",
			"ReadFull", "ReadAtLeast",
			"WriteString":
			return true
		}
	}

	return false
}

// inferCommonResultType checks that all result types are compatible and returns the common type.
// ctx is optional and used for position info in error messages.
//
// discardValue is set when the match is in statement position — its value is
// discarded by the enclosing block, so the arms are pure side-effect dispatch
// and their result types need not unify. In that case the match lowers to a
// void IIFE (the result type is overwritten with VoidType by the caller), so a
// mismatch among arm types (e.g. one arm calling a string-returning method,
// another a bool-returning one) is not an error. Skipping the unification here
// is what lets `c match { case '"' => readString(); case 't' => readBool() }`
// stand as a statement.
func (t *galaASTTransformer) inferCommonResultType(types []transpiler.Type, patterns []string, ctx antlr.ParserRuleContext, discardValue bool) (transpiler.Type, error) {
	if len(types) == 0 {
		line, col := t.lastLine, t.lastCol
		if ctx != nil && ctx.GetStart() != nil {
			line, col = ctx.GetStart().GetLine(), ctx.GetStart().GetColumn()
		}
		return nil, galaerr.NewSemanticErrorAt(line, col, "match expression has no case branches")
	}

	// Value-discarding (statement-position) match: arms are side-effect
	// dispatch, so they need not share a type. Caller forces the lowered IIFE
	// to void.
	if discardValue {
		return transpiler.VoidType{}, nil
	}

	// Check if all branches are void (side-effect only, like fmt.Printf calls)
	allVoid := true
	for _, typ := range types {
		if !typ.IsVoid() {
			allVoid = false
			break
		}
	}
	if allVoid {
		return transpiler.VoidType{}, nil
	}

	// Find the first non-nil, non-type-parameter, non-void type as reference
	var refType transpiler.Type
	var refPattern string
	for i, typ := range types {
		if typ != nil && !typ.IsNil() {
			// Skip type parameters (like A, B, T, U) - they're not concrete types
			typeName := typ.String()
			if t.isTypeParameter(typeName) {
				continue
			}
			// Skip void types when looking for reference
			if typ.IsVoid() {
				continue
			}
			refType = typ
			refPattern = patterns[i]
			break
		}
	}

	if refType == nil {
		// Check if all non-void types are NilType (complete inference failure) vs type parameters
		hasTypeParam := false
		allNilOrVoid := true
		for _, typ := range types {
			if typ != nil && !typ.IsNil() {
				if !typ.IsVoid() {
					allNilOrVoid = false
					if t.isTypeParameter(typ.String()) {
						hasTypeParam = true
					}
				}
			}
		}

		if allNilOrVoid && !hasTypeParam {
			// Complete inference failure — no branch could be typed. The
			// match's type is then the type of the slot it fills, if any (see
			// branchingResultType); never the enclosing function's result
			// type, which is the type of the result value only. With no slot
			// type either, it is a dispatch-style match used purely for side
			// effects (all arms call void functions, recurse, or are empty
			// `{}` blocks, with none producing a typed value), which the
			// caller lowers to a void IIFE.
			t.traceType(nil, transpiler.NilType{}, "match-result-untyped-arms")
			return transpiler.NilType{}, nil
		}
		// B3: when every typed arm names the *same* type parameter AND that parameter
		// is in scope of the currently-transforming function, return it
		// directly. Without the in-scope guard the IIFE would be emitted
		// with a return type Go cannot resolve (the type param is bound at
		// the matched value's type, not at this function's signature) — so
		// we fall through to `any` for the out-of-scope case.
		var sharedTypeParam transpiler.Type
		for _, typ := range types {
			if transpiler.IsUnusable(typ) {
				continue
			}
			if typ.IsVoid() {
				continue
			}
			name := typ.String()
			if !t.isTypeParameter(name) {
				sharedTypeParam = nil
				break
			}
			if sharedTypeParam == nil {
				sharedTypeParam = typ
			} else if sharedTypeParam.String() != name {
				sharedTypeParam = nil
				break
			}
		}
		// Only a parameter the enclosing declaration binds may become the
		// result type. isActiveTypeParam also accepts a callee's unbound
		// placeholder (e.g. inside a non-generic function whose body matches
		// on a generic type), which would emit a type-param Go signature for
		// a function that doesn't declare it.
		if sharedTypeParam != nil && t.activeTypeParams[sharedTypeParam.String()] {
			t.traceType(nil, sharedTypeParam, "match-result-fallback-to-shared-type-param")
			return sharedTypeParam, nil
		}
		t.warnInference("match expression defaulting to 'any' return type (all branches are type parameters)")
		return transpiler.BasicType{Name: "any"}, nil
	}

	// Check all types are compatible with the reference type
	for i, typ := range types {
		if typ == nil {
			line, col := t.lastLine, t.lastCol
			if ctx != nil && ctx.GetStart() != nil {
				line, col = ctx.GetStart().GetLine(), ctx.GetStart().GetColumn()
			}
			return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf("cannot infer result type for '%s'. Please add explicit type annotation", patterns[i]))
		}
		// VoidType is compatible with any type (for mixed match where some branches are void)
		if typ.IsVoid() {
			continue
		}
		// NilType means inference failed for this branch — treat as compatible
		// when at least one other branch has a concrete type. The Go compiler
		// will catch any real type mismatch downstream.
		if typ.IsNil() {
			continue
		}
		// Note: NilType (from nil literal) is allowed and checked in typesCompatible
		if t.typesCompatible(refType, typ) {
			continue
		}
		// Sealed widening: when the only mismatch is a sealed-CASE in one arm
		// vs. its sealed-PARENT in another (possibly nested in a tuple slot),
		// widen both arms to the common parent type and continue. This lets a
		// match arm produce a sealed CASE value (e.g. `MsgCmd[AppMsg]`) while
		// a sibling arm produces the parent (`Cmd[AppMsg]`) without forcing
		// the user to add `: Cmd[AppMsg]` annotations. The widened parent
		// becomes the new reference type so subsequent arms unify against it.
		if widened, ok := t.unifyWithSealedWidening(refType, typ); ok {
			refType = widened
			continue
		}
		msg := fmt.Sprintf("type mismatch in match expression: '%s' returns '%s' but '%s' returns '%s'. All branches must return the same type",
			refPattern, refType.String(), patterns[i], typ.String())
		if ctx != nil {
			return nil, t.semanticErrorAt(ctx, msg)
		}
		return nil, galaerr.NewSemanticErrorAt(t.lastLine, t.lastCol, msg)
	}

	return refType, nil
}

// unifyWithSealedWidening attempts to unify two match arm result types by
// widening sealed-variant CASE positions to their declared sealed PARENT
// when the sibling arm carries the parent in the same position. Returns
// (widenedType, true) on success and (nil, false) when no widening can
// reconcile the two types.
//
// Handles three shapes:
//
//  1. Direct case ↔ parent: `MsgCmd[AppMsg]` and `Cmd[AppMsg]` widen to
//     `Cmd[AppMsg]`.
//  2. Generic container with mismatching params, where exactly one slot
//     differs and that slot is a sealed-case-vs-parent: `Tuple[A, MsgCmd[X]]`
//     and `Tuple[A, Cmd[X]]` widen to `Tuple[A, Cmd[X]]`.
//  3. Recursive descent for nested generics (e.g. `Array[Tuple[A, Cmd[X]]]`)
//     by reusing this helper on each parameter slot.
//
// The widening direction is fixed: when one operand is the case and the
// other is the parent, the parent wins. This mirrors the value semantics —
// every CASE value is a value of the PARENT type via the case's Apply
// method — so widening never loses information at the Go-level.
func (t *galaASTTransformer) unifyWithSealedWidening(a, b transpiler.Type) (transpiler.Type, bool) {
	if a == nil || b == nil {
		return nil, false
	}
	if t.typesCompatible(a, b) {
		return a, true
	}
	// Direct sealed case → parent widening (in either direction).
	if parent := t.sealedCaseParent(a); parent != nil && t.typesCompatible(parent, b) {
		return b, true
	}
	if parent := t.sealedCaseParent(b); parent != nil && t.typesCompatible(parent, a) {
		return a, true
	}
	// Same generic base, recurse into params. This covers the tuple-slot
	// case from the bug report: Tuple[A, MsgCmd[X]] vs Tuple[A, Cmd[X]].
	gen1, ok1 := a.(transpiler.GenericType)
	gen2, ok2 := b.(transpiler.GenericType)
	if !ok1 || !ok2 {
		return nil, false
	}
	if gen1.Base.String() != gen2.Base.String() && !t.typesCompatible(gen1.Base, gen2.Base) {
		return nil, false
	}
	if len(gen1.Params) != len(gen2.Params) {
		return nil, false
	}
	mergedParams := make([]transpiler.Type, len(gen1.Params))
	for i := range gen1.Params {
		merged, ok := t.unifyWithSealedWidening(gen1.Params[i], gen2.Params[i])
		if !ok {
			return nil, false
		}
		mergedParams[i] = merged
	}
	return transpiler.GenericType{Base: gen1.Base, Params: mergedParams}, true
}

// typesCompatible checks if two types are compatible (same type, both any, or type parameter with any)
func (t *galaASTTransformer) typesCompatible(t1, t2 transpiler.Type) bool {
	if t1 == nil || t2 == nil {
		return false
	}

	// NilType (from nil literal) is compatible with any type
	if t1.IsNil() || t2.IsNil() {
		return true
	}

	// Types are compatible if they have the same string representation
	if t1.String() == t2.String() {
		return true
	}

	// Check qualified vs unqualified equivalence: "Response" should match "server.Response"
	// when server is dot-imported, is the current package, or is a known imported package.
	// Type alias resolution can produce qualified names from any of these sources.
	s1, s2 := t1.String(), t2.String()
	if strings.Contains(s2, ".") && !strings.Contains(s1, ".") {
		// s2 is qualified (pkg.Type), s1 is bare (Type)
		dotIdx := strings.Index(s2, ".")
		pkg := s2[:dotIdx]
		bareName := s2[dotIdx+1:]
		if bareName == s1 {
			if t.importManager.IsDotImported(pkg) || pkg == t.packageName || t.importManager.IsPackage(pkg) {
				return true
			}
		}
	}
	if strings.Contains(s1, ".") && !strings.Contains(s2, ".") {
		// s1 is qualified (pkg.Type), s2 is bare (Type)
		dotIdx := strings.Index(s1, ".")
		pkg := s1[:dotIdx]
		bareName := s1[dotIdx+1:]
		if bareName == s2 {
			if t.importManager.IsDotImported(pkg) || pkg == t.packageName || t.importManager.IsPackage(pkg) {
				return true
			}
		}
	}

	// any is compatible with everything
	if t1.IsAny() || t2.IsAny() {
		return true
	}

	// Type parameters (like T, U, std.T, std.U) are compatible with any
	if t.isTypeParameter(t1.String()) || t.isTypeParameter(t2.String()) {
		return true
	}

	// Check generic types with same base but different parameters
	// e.g., Option[T] is compatible with Option[any] if T is a type parameter
	gen1, ok1 := t1.(transpiler.GenericType)
	gen2, ok2 := t2.(transpiler.GenericType)
	if ok1 && ok2 {
		// Same base type? Use typesCompatible for base to handle qualified vs unqualified names.
		basesMatch := gen1.Base.String() == gen2.Base.String() || t.typesCompatible(gen1.Base, gen2.Base)
		if basesMatch && len(gen1.Params) == len(gen2.Params) {
			allParamsCompatible := true
			for i := range gen1.Params {
				if !t.typesCompatible(gen1.Params[i], gen2.Params[i]) {
					allParamsCompatible = false
					break
				}
			}
			if allParamsCompatible {
				return true
			}
		}
	}

	return false
}

// isTypeParameter checks if a type name represents a type parameter (like T, U, std.T).
// Delegates to isActiveTypeParam for consistent type parameter detection.
func (t *galaASTTransformer) isTypeParameter(typeName string) bool {
	return t.isActiveTypeParam(typeName)
}

// typeHasUnresolvedParams checks if a type contains unresolved type parameters (like T, U, A, B).
// Delegates to hasTypeParams for consistent type parameter detection.
func (t *galaASTTransformer) typeHasUnresolvedParams(typ transpiler.Type) bool {
	return t.hasTypeParams(typ)
}

// isSimpleIdentifier checks if a string is a simple identifier (not underscore, not complex)
func (t *galaASTTransformer) isSimpleIdentifier(s string) bool {
	if s == "_" || s == "" {
		return false
	}
	// The literal keywords `true`, `false`, and `nil` lex as the `literal`
	// grammar rule, not `identifier`. In a constructor sub-pattern like
	// `case Cat(name, true)` they must be matched as literal-equality
	// conditions, never bound as variables — otherwise the extractor path
	// would create a binding named `true`/`false`/`nil` and later reject it
	// as an unused variable.
	if s == "true" || s == "false" || s == "nil" {
		return false
	}
	// Simple identifiers start with a letter and contain only letters, digits, or underscores
	for i, c := range s {
		if i == 0 {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_') {
				return false
			}
		} else {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
				return false
			}
		}
	}
	// Exclude patterns that contain parentheses, brackets, or colons (complex patterns)
	for _, c := range s {
		if c == '(' || c == ')' || c == '[' || c == ']' || c == ':' {
			return false
		}
	}
	return true
}

// extractUserPatternVarNames walks pattern bindings AST and collects user-defined variable names.
// It skips internal temp vars (_tmp_* prefix) and blank identifiers (_).
func extractUserPatternVarNames(bindings []ast.Stmt) []string {
	var names []string
	for _, stmt := range bindings {
		extractUserVarsFromStmt(stmt, &names)
	}
	return names
}

func extractUserVarsFromStmt(stmt ast.Stmt, names *[]string) {
	addName := func(name string) {
		if name != "_" && !strings.HasPrefix(name, "_tmp_") {
			*names = append(*names, name)
		}
	}
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		if s.Tok == token.DEFINE {
			for _, lhs := range s.Lhs {
				if ident, ok := lhs.(*ast.Ident); ok {
					addName(ident.Name)
				}
			}
		}
	case *ast.DeclStmt:
		// A binding declared before a guard and assigned inside it (a
		// sequence element, a rest binding, a sub-pattern hoisted out of an
		// extractor's guard) is a `var`, not a `:=`.
		if gen, ok := s.Decl.(*ast.GenDecl); ok && gen.Tok == token.VAR {
			for _, spec := range gen.Specs {
				if vs, ok := spec.(*ast.ValueSpec); ok {
					for _, n := range vs.Names {
						addName(n.Name)
					}
				}
			}
		}
	case *ast.BlockStmt:
		for _, inner := range s.List {
			extractUserVarsFromStmt(inner, names)
		}
	case *ast.IfStmt:
		// Walk the body (guarded assignments may define vars inside if blocks)
		if s.Body != nil {
			for _, inner := range s.Body.List {
				extractUserVarsFromStmt(inner, names)
			}
		}
	}
}

// collectReferencedIdents walks Go AST nodes and collects all referenced identifier names.
//
// A match or if-expression lowered as statements is still a placeholder in
// nodes (see hoistedResult); the names its statements reference count too.
func (t *galaASTTransformer) collectReferencedIdents(nodes []ast.Node) map[string]bool {
	refs := make(map[string]bool)
	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok {
			refs[ident.Name] = true
			if hv, ok := t.hoisted[ident]; ok {
				for _, s := range hv.stmts {
					ast.Inspect(s, visit)
				}
			}
		}
		return true
	}
	for _, node := range nodes {
		if node != nil {
			ast.Inspect(node, visit)
		}
	}
	return refs
}

// transformCaseClauseWithType transforms a case clause and returns its result
// type. armSlot is the slot the match fills (see transformCaseBodyStmt).
func (t *galaASTTransformer) transformCaseClauseWithType(ctx *grammar.CaseClauseContext, paramName string, matchedType transpiler.Type, armSlot slot) (ast.Stmt, transpiler.Type, error) {
	t.pushScope()
	defer t.popScope()
	t.currentScope.caseArm = true

	patCtx := ctx.Pattern()
	cond, bindings, err := t.transformPatternWithType(patCtx, ast.NewIdent(paramName), matchedType)
	if err != nil {
		return nil, nil, err
	}

	// Transform guard expression separately so we can check variable references in it
	var guardExpr ast.Expr
	if ctx.GetGuard() != nil {
		guardExpr, err = t.transformExpression(ctx.GetGuard())
		if err != nil {
			return nil, nil, err
		}
		cond = &ast.BinaryExpr{
			X:  cond,
			Op: token.LAND,
			Y:  guardExpr,
		}
	}

	var body []ast.Stmt
	var resultType transpiler.Type

	if ctx.GetBodyBlock() != nil {
		// The case body's block last expression becomes the arm's value, so
		// it is value-consumed (not statement-position).
		b, err := t.transformValueBlock(ctx.GetBodyBlock().(*grammar.BlockContext), armSlot)
		if err != nil {
			return nil, nil, err
		}
		body = b.List
		// In GALA, a block used as an expression returns its last expression.
		// Convert the last expression statement to a return statement.
		if len(body) > 0 {
			lastStmt := body[len(body)-1]
			if lastStmt != nil {
				if exprStmt, ok := lastStmt.(*ast.ExprStmt); ok {
					// B8: classify and lower via the unified helper.
					stmt, typ := t.lowerMatchArmTailExpr(exprStmt.X)
					body[len(body)-1] = stmt
					resultType = typ
				} else if ret, ok := lastStmt.(*ast.ReturnStmt); ok && len(ret.Results) > 0 && armSlot.hoist == nil {
					// In a match lowered to a function literal, a `return`
					// yields the arm's value; in one lowered as statements it
					// leaves the function, and the arm has no value.
					resultType = t.inferResultType(ret.Results[0])
				} else if ifStmt, ok := lastStmt.(*ast.IfStmt); ok {
					// A trailing if/else is the arm's value too, carried by its
					// branches. Every branch yields the same type, so the first
					// one gives the arm's result type.
					if promoted, ok := t.promoteIfBranchValues(ifStmt, t.armReturn); ok {
						body[len(body)-1] = promoted
						if result := firstBranchResult(promoted); result != nil {
							resultType = t.inferResultType(result)
						}
					}
				}
			}
		}
		// If resultType is still nil, this is a void (side-effect) branch:
		// either empty block `{}` or last statement is an assignment/loop
		if resultType == nil {
			resultType = transpiler.VoidType{}
		}
	} else if ctx.GetBodyStmt() != nil {
		bodyStmts, bodyType, err := t.transformCaseBodyStmt(ctx.GetBodyStmt(), armSlot)
		if err != nil {
			return nil, nil, err
		}
		body = bodyStmts
		resultType = bodyType
	}

	// Check for unused pattern variables: user vars that appear in bindings but
	// are not referenced in the body or guard expression.
	userVars := extractUserPatternVarNames(bindings)
	if len(userVars) > 0 {
		// Collect identifiers referenced in body and guard
		var nodesToCheck []ast.Node
		for _, s := range body {
			nodesToCheck = append(nodesToCheck, s)
		}
		if guardExpr != nil {
			nodesToCheck = append(nodesToCheck, guardExpr)
		}
		refs := t.collectReferencedIdents(nodesToCheck)

		for _, varName := range userVars {
			if !refs[varName] {
				line := ctx.GetStart().GetLine()
				col := ctx.GetStart().GetColumn()
				return nil, nil, galaerr.NewSemanticErrorAt(line, col,
					fmt.Sprintf("unused variable '%s' in match branch — use '_' to discard this value", varName))
			}
		}
	}

	bodyBlock := &ast.BlockStmt{List: body}

	ifStmt := &ast.IfStmt{
		Cond: cond,
		Body: bodyBlock,
	}

	if len(bindings) > 0 {
		return &ast.BlockStmt{
			List: append(bindings, ifStmt),
		}, resultType, nil
	}

	return ifStmt, resultType, nil
}

// matchArm is one lowered case clause of a match: a regular arm's clause, or
// the default arm's body. hasResult reports whether the arm contributes
// resultType to the match's result type.
type matchArm struct {
	clause      ast.Stmt
	defaultBody []ast.Stmt
	resultType  transpiler.Type
	hasResult   bool
}

// lowerDefaultMatchArm lowers the default arm of a match (a wildcard, or the
// catch-all binding pattern, binds) against armSlot. Its body's trailing value, or
// the value its trailing if/else carries, is the arm's value.
func (t *galaASTTransformer) lowerDefaultMatchArm(ctx *grammar.CaseClauseContext, paramName string, matchedType transpiler.Type, binds bool, armSlot slot) (matchArm, error) {
	var arm matchArm
	// For binding patterns, register the variable and add assignment
	var bindingStmts []ast.Stmt
	if binds {
		patternText := ctx.Pattern().GetText()
		t.currentScope.vals[patternText] = false
		if matchedType != nil && !matchedType.IsNil() {
			t.currentScope.valTypes[patternText] = matchedType
		}
		bindingStmts = append(bindingStmts, &ast.AssignStmt{
			Lhs: []ast.Expr{ast.NewIdent(patternText)},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{ast.NewIdent(paramName)},
		})
	}

	if ctx.GetBodyBlock() != nil {
		// The default arm's block-body last expression becomes the
		// arm's value, so it is value-consumed.
		b, err := t.transformValueBlock(ctx.GetBodyBlock().(*grammar.BlockContext), armSlot)
		if err != nil {
			return matchArm{}, err
		}
		arm.defaultBody = append(bindingStmts, b.List...)
		if len(b.List) > 0 {
			last := len(arm.defaultBody) - 1
			switch lastStmt := b.List[len(b.List)-1].(type) {
			case *ast.ReturnStmt:
				// See transformCaseClauseWithType.
				if len(lastStmt.Results) > 0 && armSlot.hoist == nil {
					arm.resultType, arm.hasResult = t.inferResultType(lastStmt.Results[0]), true
				}
			case *ast.ExprStmt:
				// Block's last expression statement becomes the return value
				arm.defaultBody[last] = t.markSynthesizedArmReturn(&ast.ReturnStmt{Results: []ast.Expr{lastStmt.X}})
				arm.resultType, arm.hasResult = t.inferResultType(lastStmt.X), true
			case *ast.IfStmt:
				// A trailing if/else is the arm's value too, carried by
				// its branches. Every branch yields the same type, so
				// the first one gives the arm's result type.
				if promoted, ok := t.promoteIfBranchValues(lastStmt, t.armReturn); ok {
					arm.defaultBody[last] = promoted
					if result := firstBranchResult(promoted); result != nil {
						arm.resultType, arm.hasResult = t.inferResultType(result), true
					}
				}
			}
		}
	} else if ctx.GetBodyStmt() != nil {
		bodyStmts, bodyType, err := t.transformCaseBodyStmt(ctx.GetBodyStmt(), armSlot)
		if err != nil {
			return matchArm{}, err
		}
		arm.defaultBody = append(bindingStmts, bodyStmts...)
		arm.resultType, arm.hasResult = bodyType, true
	}
	return arm, nil
}

// lowerBranches lowers the n branches of a match or if-expression filling slot
// s: lower(i, s) lowers branch i against s, records it, and returns its value
// type (nil when it has none).
//
// siblingTyped is set for a construct whose value is used but whose slot has
// no type. Its type is then the one its branches unify to, and a branch that
// cannot be typed on its own takes it from its siblings: a bare `None()` arm
// next to `Some(v)` is `None[T]`, and `Phantom()` next to `Phantom[int]()` is
// `Phantom[int]`. A branch whose construction or generic call has no type
// argument fails its first lowering (isUninferredTypeArgError) and is lowered
// again against the type the other branches unify to, in either order. When
// they unify to no settled type, the first such error is reported; when the
// construction still has no type argument against it (it is not the branch's
// value, as in `val d = None()` inside the branch), so is that error. Any
// other error is reported as it is.
func (t *galaASTTransformer) lowerBranches(n int, s slot, siblingTyped bool, lower func(i int, s slot) (transpiler.Type, error)) error {
	var types []transpiler.Type
	var retry []int
	var firstErr error
	for i := range n {
		typ, err := lower(i, s)
		switch {
		case err != nil && siblingTyped && isUninferredTypeArgError(err):
			retry = append(retry, i)
			if firstErr == nil {
				firstErr = err
			}
		case err != nil:
			return err
		case typ != nil:
			types = append(types, typ)
		}
	}
	if len(retry) == 0 {
		return nil
	}
	common := t.siblingsType(types)
	if common == nil {
		return firstErr
	}
	for _, i := range retry {
		restore := t.enterReturnSlot(returnSlot{typ: common})
		_, err := lower(i, typedSlot(common))
		restore()
		if err != nil {
			return err
		}
	}
	return nil
}

// isUninferredTypeArgError reports whether err says a type argument has no
// source: GALA-E0018 (a sealed variant constructor) or GALA-E0067 (a generic
// struct, companion Apply or generic function call) found none in its
// arguments or the slot it fills. Another slot can still give it one — unless
// an argument's own type is unknown (the GALA-E0067 that carries a hint, see
// uninferredCallTypeArgError), which no slot fixes.
func isUninferredTypeArgError(err error) bool {
	var se *galaerr.SemanticError
	if !errors.As(err, &se) {
		return false
	}
	return se.Code == galaerr.CodeSealedVariantUninferred || se.Code == galaerr.CodeUninferredTypeArgument && se.Hint == ""
}

// siblingsType is the settled type the value types of a construct's typed
// branches unify to, or nil.
func (t *galaASTTransformer) siblingsType(types []transpiler.Type) transpiler.Type {
	if len(types) == 0 {
		return nil
	}
	common, err := t.inferCommonResultType(types, make([]string, len(types)), nil, false)
	if err != nil || transpiler.IsUnusable(common) || !t.isSettledType(common) {
		return nil
	}
	return common
}

// transformCaseBodyStmt transforms a simpleStatement case body.
// Returns (stmts, resultType, error) where stmts are the Go statements for the body,
// and resultType is the type (VoidType for assignments/incDec, or the expression type).
// armSlot is the slot the match fills (zero when none); an
// expression body is lowered against it (see lowerAgainst).
func (t *galaASTTransformer) transformCaseBodyStmt(ctx grammar.ISimpleStatementContext, armSlot slot) ([]ast.Stmt, transpiler.Type, error) {
	if exprCtx := ctx.Expression(); exprCtx != nil {
		// `case x => break` runs loop control; the arm has no value.
		if bs, ok := t.lowerLoopControl(exprCtx); ok {
			return []ast.Stmt{bs}, transpiler.VoidType{}, nil
		}
		// In a statement match the arm's value is discarded, so its body is a
		// statement, exactly like the tail of a braced arm: a nested match or
		// if-expression runs as a statement (its own arms may hold loop
		// control), and a plain value is evaluated but not used.
		if armSlot.discarded {
			stmt, err := t.lowerExpressionStatement(exprCtx, functionDiscardHint)
			if err != nil {
				return nil, nil, err
			}
			return []ast.Stmt{stmt}, transpiler.VoidType{}, nil
		}
		// Otherwise the body is the arm's value: wrap it in a return.
		if err := t.checkForbiddenStatementKeyword(exprCtx); err != nil {
			return nil, nil, err
		}
		expr, err := t.lowerAgainst(exprCtx, armSlot, true)
		if err != nil {
			return nil, nil, err
		}
		// B8: route through the unified value-vs-no-return classifier so
		// every match-arm-tail decision agrees.
		stmt, typ := t.lowerMatchArmTailExpr(expr)
		return []ast.Stmt{stmt}, typ, nil
	}
	// Otherwise it's a side-effect statement (assignment, incDec, shortVarDecl)
	stmt, err := t.transformSimpleStatement(ctx)
	if err != nil {
		return nil, nil, err
	}
	return []ast.Stmt{stmt}, transpiler.VoidType{}, nil
}

// Pattern transformation functions moved to patterns.go
