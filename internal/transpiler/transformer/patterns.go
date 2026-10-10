package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"sort"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// This file contains pattern transformation logic extracted from match.go
// Functions related to pattern matching, extractors, and type extraction

// blankAssignTempVar appends `_ = varName` to suppress Go's "declared and not used" error
// for internal temporary variables (_tmp_*). User-facing pattern variables are checked
// for usage by transformCaseClauseWithType and produce a GALA compiler error if unused.
func blankAssignTempVar(stmts []ast.Stmt, varName string) []ast.Stmt {
	return append(stmts, &ast.AssignStmt{
		Lhs: []ast.Expr{ast.NewIdent("_")},
		Tok: token.ASSIGN,
		Rhs: []ast.Expr{ast.NewIdent(varName)},
	})
}

func (t *galaASTTransformer) transformPattern(patCtx grammar.IPatternContext, objExpr ast.Expr) (ast.Expr, []ast.Stmt, error) {
	return t.transformPatternWithType(patCtx, objExpr, nil)
}

func (t *galaASTTransformer) transformPatternWithType(patCtx grammar.IPatternContext, objExpr ast.Expr, matchedType transpiler.Type) (ast.Expr, []ast.Stmt, error) {
	if isWildcard(patCtx.GetText()) {
		return ast.NewIdent("true"), nil, nil
	}

	switch ctx := patCtx.(type) {
	case *grammar.ExpressionPatternContext:
		return t.transformExpressionPatternWithType(ctx.Expression(), objExpr, matchedType)
	case *grammar.TypedPatternContext:
		return t.transformTypedPattern(ctx, objExpr, matchedType)
	case *grammar.RestPatternContext:
		// Rest pattern like "rest..." or "_..." - these should only appear in argument lists
		// If we get here, it's an error (rest patterns must be part of a sequence pattern)
		return nil, nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), "rest pattern (...) can only be used as the last argument in a sequence pattern like Array(first, second, rest...)")
	default:
		return nil, nil, galaerr.NewCodedSemanticError(
			galaerr.CodeUnknownPatternType,
			patCtx.GetStart().GetLine(), patCtx.GetStart().GetColumn(),
			fmt.Sprintf("unrecognized pattern syntax (internal type %T)", patCtx),
			"this usually means a new grammar rule is missing transformer support; please report at github.com/martianoff/gala/issues",
		)
	}
}

func (t *galaASTTransformer) transformExpressionPattern(patExprCtx grammar.IExpressionContext, objExpr ast.Expr) (ast.Expr, []ast.Stmt, error) {
	return t.transformExpressionPatternWithType(patExprCtx, objExpr, nil)
}

func (t *galaASTTransformer) transformExpressionPatternWithType(patExprCtx grammar.IExpressionContext, objExpr ast.Expr, matchedType transpiler.Type) (ast.Expr, []ast.Stmt, error) {
	text := patExprCtx.GetText()
	if isWildcard(text) {
		return ast.NewIdent("true"), nil, nil
	}

	if strings.Contains(text, "|") {
		if alts, err := t.patternAlternatives(patExprCtx); err != nil {
			return nil, nil, err
		} else if alts != nil {
			return t.transformAlternativePattern(alts, objExpr, matchedType)
		}
	}

	// Tuple pattern with parentheses syntax: (a, b, c) => Tuple3(a, b, c)
	if p := t.getPrimaryFromExpression(patExprCtx); p != nil {
		if exprList := p.TupleExpressionList(); exprList != nil {
			if el, ok := exprList.(*grammar.TupleExpressionListContext); ok {
				exprs := el.AllExpression()
				if len(exprs) >= 2 {
					// This is a tuple pattern (a, b, c) - transform to TupleN pattern
					return t.transformTuplePattern(exprs, objExpr, matchedType)
				}
			}
		}
	}

	// Package-qualified constructor pattern: pkg.Ctor(args) or pkg.Ctor[T](args) — e.g. `case acp.Acked()`
	// or `case acp.OutcomeResult(x)`. Sealed-case companions and structs from a
	// qualified import are registered under their qualified name (e.g. "acp.Acked"),
	// so resolve that name and route through the same extractor/struct logic. This
	// MUST come before the unqualified call-pattern check, whose two-suffix branch
	// assumes `Ctor[T](...)` and would otherwise leave this shape to fall through to
	// a (wrong) simple binding of the package identifier.
	if pkgPrimaryExpr, ctorName, qArgList, qTypeArgs, ok := t.getQualifiedCallPattern(patExprCtx); ok {
		pkgAst, err := t.transformPrimaryExpr(pkgPrimaryExpr)
		if err != nil {
			return nil, nil, err
		}
		if pkgIdent, isIdent := pkgAst.(*ast.Ident); isIdent && t.importManager.IsPackage(pkgIdent.Name) {
			// `case pkg.R(x)` where R is an imported extractor val/var.
			if b, ok := t.lookupBinding(pkgIdent.Name, ctorName); ok && qTypeArgs == nil {
				if expr, stmts, handled, err := t.tryBindingExtractorPattern(b, qArgList, objExpr, matchedType, patExprCtx); handled {
					return expr, stmts, err
				}
			}
			pkgName := pkgIdent.Name
			if actual, ok := t.importManager.ResolveAlias(pkgName); ok {
				pkgName = actual
			}
			rawName := pkgName + "." + ctorName
			return t.transformConstructorCallPattern(rawName, qArgList, qTypeArgs, objExpr, matchedType, patExprCtx)
		}
	}

	// Extractor - check for call patterns like Left(n), Some(x), IntStack(first, second, _...) etc.
	// This check must come BEFORE the simple binding check because a pattern like `Foo(x)`
	// has a primary with identifier `Foo`, but it's not a simple binding.
	// Also handles generic patterns with explicit type arguments like Unwrap[int](v).
	if primaryExprCtx, argList, explicitTypeArgs := t.getCallPatternWithTypeArgsFromExpression(patExprCtx); primaryExprCtx != nil {
		patternExpr, err := t.transformPrimaryExpr(primaryExprCtx)
		if err != nil {
			return nil, nil, err
		}

		// If it's a type name, determine how to match it.
		// First try to get the name from the transformed AST.
		rawName := t.getBaseTypeName(patternExpr)

		// If rawName is empty, the identifier may have been wrapped in .Get() (val variable).
		// Fall back to extracting the identifier directly from the grammar context.
		if rawName == "" {
			if p := primaryExprCtx.Primary(); p != nil {
				if pc, ok := p.(*grammar.PrimaryContext); ok && pc.Identifier() != nil {
					rawName = pc.Identifier().GetText()
				}
			}
		}

		return t.transformConstructorCallPattern(rawName, argList, explicitTypeArgs, objExpr, matchedType, patExprCtx)
	}

	// Simple Binding - bind variable with the matched type
	// This check comes after the extractor check because extractors like `Foo(x)` have a primary
	// with an identifier, but they're not simple bindings.
	return t.transformSimpleBindingOrLiteral(patExprCtx, objExpr, matchedType)
}

// transformConstructorCallPattern lowers a call-shaped pattern whose constructor
// resolves to rawName (which may be unqualified like "Some" or qualified like
// "acp.Acked"). It handles direct-Unapply extractors, sequence patterns, tuple
// and struct field matches, and instance extractors, in that order.
func (t *galaASTTransformer) transformConstructorCallPattern(rawName string, argList *grammar.ArgumentListContext, explicitTypeArgs *grammar.ExpressionListContext, objExpr ast.Expr, matchedType transpiler.Type, patExprCtx grammar.IExpressionContext) (ast.Expr, []ast.Stmt, error) {
	// A type parameter of the enclosing declaration shadows every extractor,
	// variant and struct of its name, and is none of them itself.
	if t.typeParamValue(rawName) {
		return nil, nil, t.typeParamMisuseError(patExprCtx, rawName, "cannot be matched as a pattern")
	}
	// An extractor's explicit type arguments (`case Unwrap[Circle](v)`) are
	// always types, so a sealed variant among them is GALA-E0061.
	if explicitTypeArgs != nil {
		for _, arg := range explicitTypeArgs.AllExpression() {
			if t.mayNameVariantType(arg) {
				if err := t.variantTypeArgError(arg); err != nil {
					return nil, nil, err
				}
			}
		}
	}
	// Check if we can use direct Unapply call (no reflection)
	// This applies to any extractor with an Unapply method - both generic and non-generic
	// For generic extractors like Cons[T], Some[T], we infer type params from the matched type
	// For non-generic extractors like Even, we just call Even{}.Unapply(x) directly
	// Requires Unapply to return bool or Option[T] - otherwise fall back to reflection
	if meta := t.getTypeMeta(rawName); meta != nil {
		if unapplyMeta, hasUnapply := meta.Methods["Unapply"]; hasUnapply {
			var inferredTypes []transpiler.Type
			if len(meta.TypeParams) > 0 {
				// Check if explicit type arguments were provided (e.g., Unwrap[int](v))
				if explicitTypeArgs != nil && len(explicitTypeArgs.AllExpression()) > 0 {
					// Use explicit type arguments instead of inferring
					for _, typeExpr := range explicitTypeArgs.AllExpression() {
						typeAst, err := t.transformExpression(typeExpr)
						if err != nil {
							return nil, nil, err
						}
						inferredTypes = append(inferredTypes, t.resolveType(t.getBaseTypeName(typeAst)))
					}
					if len(inferredTypes) != len(meta.TypeParams) {
						return nil, nil, galaerr.NewSemanticErrorAt(patExprCtx.GetStart().GetLine(), patExprCtx.GetStart().GetColumn(),
							fmt.Sprintf("extractor '%s' expects %d type parameters, got %d", rawName, len(meta.TypeParams), len(inferredTypes)))
					}
				} else {
					// Infer type parameters from the matched type
					inferredTypes = t.inferExtractorTypeParams(meta, matchedType)
					if len(inferredTypes) != len(meta.TypeParams) {
						return nil, nil, galaerr.NewSemanticErrorAt(patExprCtx.GetStart().GetLine(), patExprCtx.GetStart().GetColumn(),
							fmt.Sprintf("cannot infer type parameters for extractor '%s'. Ensure the Unapply method's parameter type matches the matched type", rawName))
					}
				}
			}
			// Check if return type is supported (bool or Option[T])
			returnType := t.substituteConcreteTypes(unapplyMeta.ReturnType, meta.TypeParams, inferredTypes)
			if !t.isDirectUnapplyReturnType(returnType) {
				return nil, nil, galaerr.NewSemanticErrorAt(patExprCtx.GetStart().GetLine(), patExprCtx.GetStart().GetColumn(),
					fmt.Sprintf("extractor '%s' must have Unapply returning bool or Option[T], got '%s'. Use Option[T] for extractors or bool for guard patterns. Unapply(any) any is not allowed",
						rawName, returnType.String()))
			}
			// Use direct Unapply call - no reflection needed!
			return t.generateDirectUnapplyPattern(rawName, meta, inferredTypes, unapplyMeta, objExpr, argList, matchedType)
		}
	}

	// An opaque type unwraps to its single underlying value: `case UserID(n)`.
	// No Unapply is generated for it; the pattern lowers to a conversion.
	if meta := t.opaqueMetaByName(rawName); meta != nil {
		return t.generateOpaquePattern(meta, rawName, argList, explicitTypeArgs, objExpr, matchedType, patExprCtx)
	}

	// Check if this is a sequence pattern (e.g., Array(first, second, rest...) or Array(a, b, c))
	// This handles Seq types like Array and List with element extraction.
	// Must be checked BEFORE struct field match, since Array/List are also structs
	// but their pattern arguments represent elements, not struct fields.
	if t.isSeqType(matchedType) {
		return t.generateSeqPatternMatch(objExpr, argList, matchedType)
	}
	if t.hasRestPattern(argList) {
		return nil, nil, galaerr.NewSemanticErrorAt(patExprCtx.GetStart().GetLine(), patExprCtx.GetStart().GetColumn(),
			fmt.Sprintf("rest pattern (...) requires a sequence type (Array, List, or type implementing Seq). Got '%s'", matchedType.String()))
	}

	// Check if this is a direct struct match for tuples (pattern type equals container type)
	// This handles cases like (a, b) matching against Tuple[A, B]
	if t.isDirectStructMatch(rawName, matchedType) {
		return t.generateDirectTupleStructMatch(objExpr, argList, matchedType)
	}

	// Check if this is a non-generic struct pattern match (e.g., Person(name, age))
	// Use direct field access for known structs
	resolvedStructName := t.resolveStructTypeName(rawName)
	if fields, ok := t.structFields[resolvedStructName]; ok && len(fields) > 0 {
		return t.generateDirectStructFieldMatch(objExpr, argList, explicitTypeArgs, fields, resolvedStructName, matchedType, patExprCtx)
	}

	// Check if rawName is a variable whose type has an Unapply method (instance extractor).
	// This enables patterns like: val r = regex.MustCompile("..."); x match { case r(groups) => ... }
	// where `r` is a variable of a type that defines Unapply.
	b, bound := t.lookupBinding("", rawName)
	if !bound {
		b = binding{name: rawName, typ: t.getType(rawName)}
	}
	if expr, stmts, handled, err := t.tryBindingExtractorPattern(b, argList, objExpr, matchedType, patExprCtx); handled {
		return expr, stmts, err
	}

	// Extractor not found or doesn't have Unapply method.
	// B7: suggest near-matches from companion objects / registered types.
	suggestion := t.suggestExtractorName(rawName)
	msg := fmt.Sprintf("extractor '%s' must define an Unapply method. For generic extractors use: func (e Extractor[T]) Unapply(v ContainerType[T]) Option[T]. For guard patterns use: func (e Extractor) Unapply(v ConcreteType) bool",
		rawName)
	hint := ""
	if suggestion != "" {
		hint = fmt.Sprintf("did you mean '%s'?", suggestion)
	}
	return nil, nil, galaerr.NewCodedSemanticError(
		galaerr.CodeMissingUnapply,
		patExprCtx.GetStart().GetLine(), patExprCtx.GetStart().GetColumn(),
		msg, hint)
}

// sealedVariantOfMatchedType reports whether name is declared as a `case`
// variant of the sealed type currently being matched. It returns the variant
// declaration together with the parent sealed type's metadata, or (nil, nil)
// when the matched type is unusable, is not sealed, or declares no such
// variant. This is deliberately scoped to the matched type: a bare identifier
// that happens to share a name with some unrelated variant elsewhere in the
// program is an ordinary binding. An alias of a sealed type (`type F frame`)
// is matched as the type it names. An unexported variant of a sealed type
// declared in another package is out of reach (see visibleHere): there the
// name is an ordinary binding too.
func (t *galaASTTransformer) sealedVariantOfMatchedType(name string, matchedType transpiler.Type) (*transpiler.SealedVariant, *transpiler.TypeMetadata) {
	if matchedType == nil || transpiler.IsUnusable(matchedType) {
		return nil, nil
	}
	meta := t.getTypeMeta(t.followAliasChain(matchedType).BaseName())
	if meta == nil || !meta.IsSealed || !t.visibleHere(name, meta) {
		return nil, nil
	}
	for i := range meta.SealedVariants {
		if meta.SealedVariants[i].Name == name {
			return &meta.SealedVariants[i], meta
		}
	}
	return nil, nil
}

// bareVariantBindingError reports a bare identifier in pattern position that
// names a field-bearing variant of the type being matched. Such a pattern is
// parsed as a fresh variable binding, which matches every value — so the arm
// silently becomes a catch-all and the variant it appears to test is never
// distinguished. Rejecting it cannot break a program that was not already
// misleading.
func bareVariantBindingError(name string, variant *transpiler.SealedVariant, parent *transpiler.TypeMetadata, line, col int) error {
	placeholders := make([]string, len(variant.FieldNames))
	for i := range placeholders {
		placeholders[i] = "_"
	}
	return galaerr.NewCodedSemanticError(
		galaerr.CodeBareVariantBinding,
		line, col,
		fmt.Sprintf("`%s` is a variant of sealed type %q, so `case %s` binds a new variable and matches every value instead of testing for %s",
			name, parent.Name, name, name),
		fmt.Sprintf("write `case %s(%s)` to match the variant, or rename the binding if you meant to capture the whole value",
			name, strings.Join(placeholders, ", ")),
	)
}

// isStableIdentifierPattern reports whether a bare identifier in a pattern is a
// stable identifier, compared with == rather than bound (as in Scala): a
// capitalized name of a value in scope that was not bound earlier in this same
// pattern, or of a const/var in a hand-written Go file of the package.
func (t *galaASTTransformer) isStableIdentifierPattern(name string) bool {
	return t.isStableIdentifierIn(name, t.bindingScope(name))
}

// isStableIdentifierIn is isStableIdentifierPattern for a name already
// resolved to s (nil when unbound).
func (t *galaASTTransformer) isStableIdentifierIn(name string, s *scope) bool {
	if !ast.IsExported(name) {
		return false
	}
	if s != nil {
		return !t.boundInCurrentPattern(s)
	}
	// Same-package Go declarations are keyed by the package name, which an
	// imported package can share whatever alias it is imported under (GALA's
	// `fs` importing Go's `io/fs`). The key then cannot tell the two apart, so
	// the name binds.
	if t.goTypeInfo == nil || t.packageName == "" {
		return false
	}
	for _, imp := range t.importManager.All() {
		if imp.PkgName == t.packageName {
			return false
		}
	}
	qualName := t.packageName + "." + name
	if _, ok := t.goTypeInfo.Constants[qualName]; ok {
		return true
	}
	_, ok := t.goTypeInfo.Variables[qualName]
	return ok
}

// isPatternBinding reports whether a sub-pattern's text, matched against a
// value of matchedType, is a plain name that binds a new variable — rather
// than a stable identifier compared by ==, or a name that tests the value
// (see bareNameTests).
func (t *galaASTTransformer) isPatternBinding(text string, matchedType transpiler.Type) bool {
	return t.isSimpleIdentifier(text) && !t.isStableIdentifierPattern(text) && !t.bareNameTests(text, matchedType)
}

// isBindingPatternOf reports whether a whole case pattern is a binding that
// catches every value of matchedType: a plain name (isBindingPattern) that
// does not test the value (see bareNameTests).
func (t *galaASTTransformer) isBindingPatternOf(text string, matchedType transpiler.Type) bool {
	return isBindingPattern(text) && !t.bareNameTests(text, matchedType)
}

// bareNameTests reports whether a bare name in pattern position tests the
// value it is matched against instead of binding it: the name is a variant of
// the matched sealed type, or a standalone zero-field extractor (see
// transformSimpleBindingOrLiteral, which lowers both). It is decided by what
// the name resolves to, never by its capitalization — `case endFrame =>`
// against a sealed type that declares `case endFrame()` matches that variant.
// A variant of some OTHER sealed type is not a test of a value whose type is
// known: the name binds, as sealedVariantOfMatchedType documents.
func (t *galaASTTransformer) bareNameTests(name string, matchedType transpiler.Type) bool {
	if variant, _ := t.sealedVariantOfMatchedType(name, matchedType); variant != nil {
		return true
	}
	meta, _ := t.bareExtractor(name, matchedType)
	return meta != nil
}

// bareExtractor returns the zero-field extractor (see zeroFieldExtractor) a
// bare pattern name lowers to when it is not a variant of the matched type:
// a standalone guard extractor, or — when the subject's type is unknown — any
// zero-field variant, whose Unapply then decides. A zero-field variant of a
// sealed type other than the known matched type is not one: the name binds.
func (t *galaASTTransformer) bareExtractor(name string, matchedType transpiler.Type) (*transpiler.TypeMetadata, *transpiler.MethodMetadata) {
	meta, unapplyMeta := t.zeroFieldExtractor(name)
	if meta == nil || !t.visibleHere(name, meta) ||
		(!transpiler.IsUnusable(matchedType) && t.findSealedParentForVariant(name, "") != nil) {
		return nil, nil
	}
	return meta, unapplyMeta
}

// visibleHere reports whether name, a variant or type that meta's package
// declares, can be referred to from the package being compiled: it is
// exported, or that package is this one. Go's export rule, not a naming
// heuristic — `concurrent.Future`'s unexported `fut` case is invisible to
// another package, where `case FutureCmd(fut)` binds a name.
func (t *galaASTTransformer) visibleHere(name string, meta *transpiler.TypeMetadata) bool {
	return visibleFrom(name, meta, t.packageName)
}

// visibleFrom is visibleHere for code in package fromPackage.
func visibleFrom(name string, meta *transpiler.TypeMetadata, fromPackage string) bool {
	return token.IsExported(name) || meta.Package == "" || meta.Package == fromPackage
}

// zeroFieldExtractor returns the type a bare pattern name names, and its
// Unapply, when that type is a zero-field extractor: its Unapply returns bool
// (a guard extractor) and it takes no type parameters — precisely the shape
// generated for a zero-field sealed variant. Otherwise it returns nils.
func (t *galaASTTransformer) zeroFieldExtractor(name string) (*transpiler.TypeMetadata, *transpiler.MethodMetadata) {
	meta := t.getTypeMeta(name)
	if meta == nil || len(meta.TypeParams) > 0 {
		return nil, nil
	}
	unapplyMeta, ok := meta.Methods["Unapply"]
	if !ok {
		return nil, nil
	}
	if basic, ok := unapplyMeta.ReturnType.(transpiler.BasicType); !ok || basic.Name != "bool" {
		return nil, nil
	}
	return meta, unapplyMeta
}

// boundInCurrentPattern reports whether s, the scope a name resolved to, is
// the scope of the case arm whose pattern is being lowered — i.e. the name was
// bound earlier in this same pattern.
func (t *galaASTTransformer) boundInCurrentPattern(s *scope) bool {
	return s == t.currentScope && s.caseArm
}

// patternIdentifier returns the name when a pattern is a bare identifier, and
// "" for anything else — a literal, a qualified name such as `math.MaxInt8`,
// or an operator expression — all of which compare by equality.
func patternIdentifier(ctx grammar.IExpressionContext) string {
	postfix := LeadingPostfixExpr(ctx, true)
	if postfix == nil || postfix.PostfixSuffix(0) != nil || postfix.CaseClause(0) != nil {
		return ""
	}
	if p := PrimaryOf(postfix); p != nil && p.Identifier() != nil {
		return p.Identifier().GetText()
	}
	return ""
}

// transformSimpleBindingOrLiteral handles the two remaining pattern shapes once a
// pattern is known not to be a tuple/extractor/constructor call: a bare identifier
// binds a variable (with a zero-field sealed-variant shortcut), and anything else
// — a literal, a qualified name, or a stable identifier (see
// isStableIdentifierPattern) — is compared for equality.
func (t *galaASTTransformer) transformSimpleBindingOrLiteral(patExprCtx grammar.IExpressionContext, objExpr ast.Expr, matchedType transpiler.Type) (ast.Expr, []ast.Stmt, error) {
	if name := patternIdentifier(patExprCtx); name != "" {

		// A bare identifier that names a variant of the type being matched is
		// never a binding — binding it would turn the arm into a catch-all that
		// silently swallows every other variant.
		if variant, parent := t.sealedVariantOfMatchedType(name, matchedType); variant != nil {
			if len(variant.FieldNames) > 0 {
				return nil, nil, bareVariantBindingError(name, variant, parent,
					patExprCtx.GetStart().GetLine(), patExprCtx.GetStart().GetColumn())
			}
			// Zero-field variant: `case None` means exactly `case None()`. Route
			// through the constructor-call path so generic parents (Option[T],
			// Maybe[T], …) get their type arguments inferred from the matched type.
			// The call-shaped spelling reaches that path with the companion's
			// canonical name (`std.None`), because it transforms the primary
			// expression first; resolve the bare name the same way so the emitted
			// extractor carries its package qualifier.
			ctorName := name
			if _, resolved := t.getTypeMetaResolved(name); resolved != "" {
				ctorName = resolved
			}
			return t.transformConstructorCallPattern(ctorName, nil, nil, objExpr, matchedType, patExprCtx)
		}

		// The identifier may still refer to a zero-field extractor that is not a
		// variant of the matched type — e.g. a standalone guard extractor, or a
		// sealed variant matched against a subject whose type could not be
		// inferred. Writing `case Debug =>` stays equivalent to `case Debug() =>`.
		if meta, unapplyMeta := t.bareExtractor(name, matchedType); meta != nil {
			return t.generateDirectUnapplyPattern(name, meta, nil, unapplyMeta, objExpr, nil, matchedType)
		}

		// A name bound earlier in this same pattern is neither a fresh binding
		// (Go would reject the redeclaration) nor a value to compare against.
		s := t.bindingScope(name)
		if s != nil && t.boundInCurrentPattern(s) {
			err := galaerr.NewSemanticErrorAt(patExprCtx.GetStart().GetLine(), patExprCtx.GetStart().GetColumn(),
				fmt.Sprintf("'%s' is bound more than once in this pattern", name))
			err.Hint = "bind each part to its own name and compare them in a guard: `case (a, b) if a == b =>`"
			return nil, nil, err
		}

		if !t.isStableIdentifierIn(name, s) {
			t.currentScope.vals[name] = false // Treat as var to avoid .Get() wrapping
			// Set the type of the bound variable to the matched type
			if matchedType != nil && !matchedType.IsNil() {
				t.currentScope.valTypes[name] = matchedType
			} else {
				// Type is unknown, explicitly set to any so type inference works correctly
				t.currentScope.valTypes[name] = transpiler.BasicType{Name: "any"}
			}
			// A subject declared `any` binds as `any`: that is its type. A
			// binding that has to be hoisted out of a guard with an unknown
			// subject type takes the bound temp's declared type or is rejected
			// (see hoistPatternDecls).
			var bindingType ast.Expr
			if matchedType != nil && matchedType.IsAny() {
				bindingType = ast.NewIdent("any")
			} else if matchedType != nil {
				bindingType = t.knownTypeExpr(matchedType)
			}
			assign := t.patternDefine([]string{name}, []ast.Expr{bindingType}, objExpr)
			return ast.NewIdent("true"), []ast.Stmt{assign}, nil
		}
	}

	// Literal or other - use direct equality comparison
	patExpr, err := t.transformExpression(patExprCtx)
	if err != nil {
		return nil, nil, err
	}
	cond := &ast.BinaryExpr{
		X:  objExpr,
		Op: token.EQL,
		Y:  patExpr,
	}
	return cond, nil, nil
}

// transformTypedPattern lowers `name: Type`. matchedType is the subject's type
// as the enclosing pattern knows it, nil when it does not; objExpr may be a
// temp whose type cannot be read back, as with a value an extractor took out.
func (t *galaASTTransformer) transformTypedPattern(ctx *grammar.TypedPatternContext, objExpr ast.Expr, matchedType transpiler.Type) (ast.Expr, []ast.Stmt, error) {
	name := ctx.Identifier().GetText()
	typeExpr, err := t.transformType(ctx.Type_())
	if err != nil {
		return nil, nil, err
	}

	// Check if this is a wildcard generic pattern (e.g., Wrap[_] -> Wrap[any])
	// Only use interface-based matching when objExpr has a concrete generic type,
	// not when it's 'any' (because we need field access which requires concrete type)
	if baseName, isWildcard := t.isWildcardGenericType(typeExpr); isWildcard {
		objType := matchedType
		if transpiler.IsUnusable(objType) {
			objType = t.getExprTypeName(objExpr)
		}
		// Only use interface check if the object has a concrete generic type (not any/interface)
		if !transpiler.IsUnusableOrAny(objType) {
			return t.transformWildcardTypedPattern(name, baseName, objExpr, objType)
		}
	}

	typeName := t.resolveType(t.getBaseTypeName(typeExpr))
	if qName := t.lookupTypeName(typeName.String()); !qName.IsNil() {
		typeName = qName
	}
	// If the type expression is a pointer (*T), wrap the resolved type
	// in PointerType so the pattern variable has the correct pointer type.
	if _, isPtr := typeExpr.(*ast.StarExpr); isPtr {
		if _, alreadyPtr := typeName.(transpiler.PointerType); !alreadyPtr {
			typeName = transpiler.PointerType{Elem: typeName}
		}
	}
	t.addVar(name, typeName)

	okName := t.nextTempVar()

	// v, ok := std.As[T](obj)
	assign := t.patternDefine([]string{name, okName}, []ast.Expr{typeExpr, ast.NewIdent("bool")}, t.stdAsCall(typeExpr, objExpr))

	return ast.NewIdent(okName), []ast.Stmt{assign}, nil
}

// isWildcardGenericType checks if typeExpr is a generic type with wildcard (any) type parameters.
// Returns the base type name and true if it's a wildcard generic pattern.
func (t *galaASTTransformer) isWildcardGenericType(typeExpr ast.Expr) (string, bool) {
	// Check for IndexExpr: Wrap[any]
	if idx, ok := typeExpr.(*ast.IndexExpr); ok {
		if ident, ok := idx.Index.(*ast.Ident); ok && ident.Name == "any" {
			if baseIdent, ok := idx.X.(*ast.Ident); ok {
				return baseIdent.Name, true
			}
			if sel, ok := idx.X.(*ast.SelectorExpr); ok {
				return sel.Sel.Name, true
			}
		}
	}
	// Check for IndexListExpr: Wrap[any, any]
	if idx, ok := typeExpr.(*ast.IndexListExpr); ok {
		hasAny := false
		for _, index := range idx.Indices {
			if ident, ok := index.(*ast.Ident); ok && ident.Name == "any" {
				hasAny = true
				break
			}
		}
		if hasAny {
			if baseIdent, ok := idx.X.(*ast.Ident); ok {
				return baseIdent.Name, true
			}
			if sel, ok := idx.X.(*ast.SelectorExpr); ok {
				return sel.Sel.Name, true
			}
		}
	}
	return "", false
}

// transformWildcardTypedPattern generates code for wildcard generic patterns like w: Wrap[_].
// Instead of using As[Wrap[any]], it uses the marker interface check. The
// binding keeps the subject's type, objType.
func (t *galaASTTransformer) transformWildcardTypedPattern(name, baseName string, objExpr ast.Expr, objType transpiler.Type) (ast.Expr, []ast.Stmt, error) {
	interfaceName := baseName + "Instance"
	// Use the actual generated interface name if it was renamed to avoid collision
	if t.instanceInterfaceNames != nil {
		if actual, ok := t.instanceInterfaceNames[baseName]; ok {
			interfaceName = actual
		}
	}
	methodName := "Is" + baseName

	// The variable keeps its original type from objExpr
	// We just need to verify it's an instance of the generic type
	t.addVar(name, objType)

	okName := t.nextTempVar()
	instName := t.nextTempVar()

	// inst, ok := std.As[WrapInstance](obj) — like a type pattern, it looks
	// through std's transparent wrappers.
	assign1 := t.patternDefine([]string{instName, okName}, []ast.Expr{ast.NewIdent(interfaceName), ast.NewIdent("bool")},
		t.stdAsCall(ast.NewIdent(interfaceName), objExpr))

	// name := obj (keep original concrete type)
	assign2 := t.patternDefine([]string{name}, []ast.Expr{t.knownTypeExpr(objType)}, objExpr)

	// Condition: ok && inst.IsWrap()
	cond := &ast.BinaryExpr{
		X:  ast.NewIdent(okName),
		Op: token.LAND,
		Y: &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   ast.NewIdent(instName),
				Sel: ast.NewIdent(methodName),
			},
		},
	}

	return cond, []ast.Stmt{assign1, assign2}, nil
}

// isDirectStructMatch checks if the pattern type directly matches the container type
// AND the matched type is a generic type with type parameters.
// For example, Tuple pattern matching against Tuple[A, B] is a direct match.
// This is different from:
// - Companion objects like Some matching Option[T]
// - Non-generic struct matching (like Person matching Person) which should use UnapplyFull
func (t *galaASTTransformer) isDirectStructMatch(patternTypeName string, matchedType transpiler.Type) bool {
	if transpiler.IsUnusable(matchedType) {
		return false
	}

	// Only consider generic types for direct struct matching
	// Non-generic structs should use UnapplyFull with their own Unapply method
	genType, ok := matchedType.(transpiler.GenericType)
	if !ok || len(genType.Params) == 0 {
		return false
	}

	containerBaseName := genType.Base.BaseName()

	// Normalize names by removing package prefixes
	normalizedPattern := stripStdPrefix(patternTypeName)

	normalizedContainer := stripStdPrefix(containerBaseName)

	// Check for exact match. Only a tuple is read positionally (V1, V2, …);
	// any other declared generic struct is matched through its own fields by
	// generateDirectStructFieldMatch, so `Box(md, in)` against Box[int] reads
	// Md and In rather than tuple accessors typed by Box's type arguments.
	if normalizedPattern == normalizedContainer {
		return t.isTupleType(normalizedContainer) || len(t.structFields[t.resolveStructTypeName(patternTypeName)]) == 0
	}

	// Check for tuple pattern matching with parentheses syntax
	// When using (a, b, c) pattern, the patternTypeName might not be set,
	// but we want to match against TupleN types
	if t.isTupleType(normalizedContainer) {
		return true
	}

	return false
}

// isTupleType checks if a type name is a Tuple type (Tuple, Tuple3, ..., Tuple10).
// Thin wrapper around isTupleTypeName (B2) — kept as a method so existing
// receiver-style call sites stay readable.
func (t *galaASTTransformer) isTupleType(typeName string) bool {
	return isTupleTypeName(typeName)
}

// generateDirectTupleStructMatch generates direct field access code for tuple patterns.
// Instead of using reflection-based UnapplyTupleN, this generates direct access like:
//
//	a := obj.V1.Get()
//	b := obj.V2.Get()
//
// The condition is always true since the type already matches.
func (t *galaASTTransformer) generateDirectTupleStructMatch(objExpr ast.Expr, argList *grammar.ArgumentListContext, matchedType transpiler.Type) (ast.Expr, []ast.Stmt, error) {
	if argList == nil {
		return ast.NewIdent("true"), nil, nil
	}

	args := argList.AllArgument()
	if len(args) == 0 {
		return ast.NewIdent("true"), nil, nil
	}

	var stmts []ast.Stmt
	var conds []ast.Expr

	// Extract element types from matched type if available
	var elementTypes []transpiler.Type
	if genType, ok := matchedType.(transpiler.GenericType); ok {
		elementTypes = genType.Params
	}

	// Generate bindings for each pattern argument using direct field access
	for i, argCtx := range args {
		arg := argCtx.(*grammar.ArgumentContext)
		if arg.Pattern() == nil {
			continue
		}
		patternText := arg.Pattern().GetText()

		if isWildcard(patternText) {
			continue
		}

		// Determine the type for this element
		var elemType transpiler.Type = transpiler.NilType{} // unknown, never erased to any
		if i < len(elementTypes) {
			elemType = elementTypes[i]
		}

		// Generate direct field access: objExpr.V{i+1}.Get()
		fieldName := fmt.Sprintf("V%d", i+1)
		elemExpr := &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X: &ast.SelectorExpr{
					X:   objExpr,
					Sel: ast.NewIdent(fieldName),
				},
				Sel: ast.NewIdent("Get"),
			},
		}

		// A binding, a nested pattern or a typed pattern, all lowered by the
		// general dispatcher.
		nestedCond, nestedStmts, err := t.transformPatternWithType(arg.Pattern(), elemExpr, elemType)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, nestedStmts...)
		if ident, ok := nestedCond.(*ast.Ident); !ok || ident.Name != "true" {
			conds = append(conds, nestedCond)
		}
	}

	t.needsStdImport = true

	// Combine all conditions
	if len(conds) == 0 {
		return ast.NewIdent("true"), stmts, nil
	}

	finalCond := conds[0]
	for i := 1; i < len(conds); i++ {
		finalCond = &ast.BinaryExpr{
			X:  finalCond,
			Op: token.LAND,
			Y:  conds[i],
		}
	}
	return finalCond, stmts, nil
}

// generateDirectStructFieldMatch generates direct field access code for struct patterns.
// For example, Person(name, age) matching against Person{Name: "Alice", Age: 25}
// generates: name := obj.Name.Get(); age := obj.Age.Get() — `.Get()` only for
// non-`var` fields, which are stored as Immutable[T].
func (t *galaASTTransformer) generateDirectStructFieldMatch(objExpr ast.Expr, argList *grammar.ArgumentListContext, explicitTypeArgs *grammar.ExpressionListContext, fields []string, structName string, matchedType transpiler.Type, patExprCtx grammar.IExpressionContext) (ast.Expr, []ast.Stmt, error) {
	var stmts []ast.Stmt
	var conds []ast.Expr

	// When the match subject is statically an interface — `any`, `error`, or
	// another GALA or Go interface — or a type parameter, the struct's fields
	// are not directly reachable: Go requires a type assertion first. Insert
	// `castVar, ok := std.As[Struct](obj)` and gate the arm on `ok`; subsequent
	// field access reads from castVar. A concretely-typed subject keeps reading
	// fields straight off objExpr.
	baseExpr := objExpr
	assertFrom, err := t.structPatternAssertSubject(objExpr, structName, matchedType, patExprCtx)
	if err != nil {
		return nil, nil, err
	}
	if assertFrom != nil {
		// A generic struct can only be asserted to one of its instantiations,
		// which an interface subject cannot supply: the pattern must name the
		// type arguments (`case Box[int](v, tag)`). They then type the fields too.
		assertType, instantiated, err := t.structPatternAssertType(structName, explicitTypeArgs, patExprCtx)
		if err != nil {
			return nil, nil, err
		}
		if instantiated != nil {
			matchedType = instantiated
		}
		var stmt ast.Stmt
		var ok ast.Expr
		baseExpr, stmt, ok = t.assertPatternSubject(assertFrom, assertType)
		stmts = append(stmts, stmt)
		conds = append(conds, ok)
	}

	var args []grammar.IArgumentContext
	if argList != nil {
		args = argList.AllArgument()
	}
	if len(args) == 0 {
		return combineStructMatchConds(conds, stmts)
	}

	if len(args) > len(fields) {
		return nil, nil, galaerr.NewSemanticErrorAt(argList.GetStart().GetLine(), argList.GetStart().GetColumn(), fmt.Sprintf("struct '%s' has %d fields but pattern has %d arguments", structName, len(fields), len(args)))
	}

	// Get field types if available. They are the declared types, so for a
	// generic struct they mention its type parameters: substitute the matched
	// type's arguments (Box[int] turns `Md Mode[T]` into Mode[int]).
	fieldTypes := t.structFieldTypes[structName]
	immutFlags := t.structImmutFields[structName]
	substituteFieldType := func(ft transpiler.Type) transpiler.Type { return ft }
	if meta := t.getTypeMeta(structName); meta != nil && len(meta.TypeParams) > 0 {
		subject := matchedType
		if ptr, ok := subject.(transpiler.PointerType); ok {
			subject = ptr.Elem
		}
		if gen, ok := subject.(transpiler.GenericType); ok && len(gen.Params) == len(meta.TypeParams) {
			substituteFieldType = func(ft transpiler.Type) transpiler.Type {
				return t.substituteConcreteTypes(ft, meta.TypeParams, gen.Params)
			}
		}
	}

	// Generate bindings for each pattern argument using direct field access
	for i, argCtx := range args {
		arg := argCtx.(*grammar.ArgumentContext)
		if arg.Pattern() == nil {
			continue
		}
		patternText := arg.Pattern().GetText()

		if isWildcard(patternText) {
			continue
		}

		// Get the field name and type
		fieldName := fields[i]
		var fieldType transpiler.Type = transpiler.NilType{} // unknown, never erased to any
		if fieldTypes != nil {
			if ft, ok := fieldTypes[fieldName]; ok {
				fieldType = substituteFieldType(ft)
			}
		}

		// baseExpr.FieldName, unwrapped with .Get() only for a non-`var`
		// (Immutable[T]) field; a `var` field is a plain Go field.
		// baseExpr is the type-asserted castVar for interface subjects, else objExpr.
		// Metadata synthesized from Go source records an immutable field's
		// type as Immutable[T]; the unwrapped read is a T.
		isImmut := i < len(immutFlags) && immutFlags[i]
		elemExpr := buildFieldAccess(baseExpr, fieldName, isImmut)
		if isImmut {
			fieldType = unwrapGalaType(fieldType)
		}

		// A binding (`name := obj.Field`, `.Get()` added when immutable), a
		// nested pattern such as `Circle(r)` or a typed pattern such as
		// `e: Tagged[_]`, all lowered by the general dispatcher against the
		// field's type.
		nestedCond, nestedStmts, err := t.transformPatternWithType(arg.Pattern(), elemExpr, fieldType)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, nestedStmts...)
		if ident, ok := nestedCond.(*ast.Ident); !ok || ident.Name != "true" {
			conds = append(conds, nestedCond)
		}
	}

	t.needsStdImport = true

	return combineStructMatchConds(conds, stmts)
}

// combineStructMatchConds ANDs a struct pattern's accumulated conditions into a
// single expression (or the literal `true` when there are none) and returns it
// alongside the pattern's binding statements.
func combineStructMatchConds(conds []ast.Expr, stmts []ast.Stmt) (ast.Expr, []ast.Stmt, error) {
	if len(conds) == 0 {
		return ast.NewIdent("true"), stmts, nil
	}
	finalCond := conds[0]
	for i := 1; i < len(conds); i++ {
		finalCond = &ast.BinaryExpr{X: finalCond, Op: token.LAND, Y: conds[i]}
	}
	return finalCond, stmts, nil
}

// structPatternAssertSubject returns the expression a struct pattern asserts
// to the struct before reading its fields, or nil when the subject is of a
// concrete type whose fields are read directly. An interface subject is
// asserted as it is, once the struct is known to implement the interface — a
// struct that does not could never be held by it, so the arm is an error
// rather than one that silently never matches. A value of a type parameter is
// asserted through `any`.
func (t *galaASTTransformer) structPatternAssertSubject(objExpr ast.Expr, structName string, matchedType transpiler.Type, patExprCtx grammar.IExpressionContext) (ast.Expr, error) {
	if matchedType == nil {
		return nil, nil
	}
	subjectType := t.followAliasChain(matchedType)
	if t.isTypeParamSubject(subjectType) {
		return &ast.CallExpr{Fun: ast.NewIdent("any"), Args: []ast.Expr{objExpr}}, nil
	}
	if !t.isInterfaceType(subjectType) {
		return nil, nil
	}
	// Only a struct declared in GALA has its whole method set in metadata.
	meta := t.getTypeMeta(structName)
	if meta == nil || !strings.HasSuffix(meta.DefinedIn, ".gala") {
		return objExpr, nil
	}
	required, _ := t.interfaceMethodNames(subjectType)
	reason := ""
	if missing := t.missingInterfaceMethods(meta, required); len(missing) > 0 {
		reason = "missing " + strings.Join(missing, ", ")
	} else if ptr := pointerReceiverMethods(meta, required); len(ptr) > 0 {
		reason = strings.Join(ptr, ", ") + " has a pointer receiver"
	}
	if reason != "" {
		at := patExprCtx.GetStart()
		name := stripPackagePrefix(structName)
		return nil, galaerr.NewSemanticErrorAt(at.GetLine(), at.GetColumn(),
			fmt.Sprintf("%s does not implement %s (%s), so a value of type %s never holds one and the pattern %s(...) never matches",
				name, matchedType.String(), reason, matchedType.String(), name))
	}
	return objExpr, nil
}

// pointerReceiverMethods returns the methods of required that meta's type
// declares with a pointer receiver, sorted: a value of the type lacks them.
func pointerReceiverMethods(meta *transpiler.TypeMetadata, required []string) []string {
	var ptr []string
	for _, m := range required {
		if mm := meta.Methods[m]; mm != nil && mm.PointerReceiver {
			ptr = append(ptr, m)
		}
	}
	sort.Strings(ptr)
	return ptr
}

// structPatternAssertType returns the type a struct pattern asserts an interface
// or type-parameter subject to. A non-generic struct asserts to itself. A
// generic struct needs one of its instantiations, so the pattern must spell
// the type arguments (`case Box[int](...)`); the instantiated type is
// returned as well so the fields can be typed by it. Without them the pattern
// is rejected: Go cannot assert to an uninstantiated generic type.
func (t *galaASTTransformer) structPatternAssertType(structName string, explicitTypeArgs *grammar.ExpressionListContext, patExprCtx grammar.IExpressionContext) (ast.Expr, transpiler.Type, error) {
	meta := t.getTypeMeta(structName)
	if meta == nil || len(meta.TypeParams) == 0 {
		return t.ident(structName), nil, nil
	}
	var typeArgExprs []grammar.IExpressionContext
	if explicitTypeArgs != nil {
		typeArgExprs = explicitTypeArgs.AllExpression()
	}
	if len(typeArgExprs) != len(meta.TypeParams) {
		kind := "struct"
		if meta.IsOpaque {
			kind = "opaque type"
		}
		return nil, nil, galaerr.NewSemanticErrorAt(patExprCtx.GetStart().GetLine(), patExprCtx.GetStart().GetColumn(),
			fmt.Sprintf("cannot match generic %s '%s' against a value of an interface or type-parameter type without its %d type argument(s): Go can only type-assert to an instantiated type. Write the type arguments in the pattern, e.g. `case %s[%s](...)`",
				kind, stripPackagePrefix(structName), len(meta.TypeParams), stripPackagePrefix(structName), strings.Join(meta.TypeParams, ", ")))
	}
	goArgs := make([]ast.Expr, len(typeArgExprs))
	typeArgs := make([]transpiler.Type, len(typeArgExprs))
	for i, e := range typeArgExprs {
		typeAst, err := t.transformExpression(e)
		if err != nil {
			return nil, nil, err
		}
		goArgs[i] = typeAst
		typeArgs[i] = t.astTypeToTranspilerType(typeAst)
	}
	return withTypeArgs(t.ident(structName), goArgs), transpiler.GenericType{Base: transpiler.BasicType{Name: structName}, Params: typeArgs}, nil
}

// hasRestPattern checks if any argument in the argument list is a rest pattern (ends with ...).
func (t *galaASTTransformer) hasRestPattern(argList *grammar.ArgumentListContext) bool {
	if argList == nil {
		return false
	}
	for _, argCtx := range argList.AllArgument() {
		arg := argCtx.(*grammar.ArgumentContext)
		if pat := arg.Pattern(); pat != nil {
			if _, ok := pat.(*grammar.RestPatternContext); ok {
				return true
			}
		}
	}
	return false
}

// isSeqType checks if a type implements the Seq interface (has Size, Get, SeqDrop methods).
func (t *galaASTTransformer) isSeqType(typ transpiler.Type) bool {
	if transpiler.IsUnusable(typ) {
		return false
	}

	// Unwrap pointer types for mutable collections
	if ptrType, ok := typ.(transpiler.PointerType); ok {
		typ = ptrType.Elem
	}

	// Get the base type name
	var baseName string
	if genType, ok := typ.(transpiler.GenericType); ok {
		baseName = genType.Base.BaseName()
	} else if basicType, ok := typ.(transpiler.BasicType); ok {
		baseName = basicType.Name
	} else {
		return false
	}

	// Check if it's a known Seq type (includes both immutable and mutable collections)
	switch baseName {
	case "Array", "collection_immutable.Array", "collection_mutable.Array",
		"List", "collection_immutable.List", "collection_mutable.List":
		return true
	}

	// Check if the type has the required methods
	if meta := t.getTypeMeta(baseName); meta != nil {
		_, hasSize := meta.Methods["Size"]
		_, hasGet := meta.Methods["Get"]
		_, hasSeqDrop := meta.Methods["SeqDrop"]
		return hasSize && hasGet && hasSeqDrop
	}

	return false
}

// getSeqElementType extracts the element type from a Seq type like Array[int] or List[string].
// Also handles pointer types like *Array[int] for mutable collections.
func (t *galaASTTransformer) getSeqElementType(typ transpiler.Type) transpiler.Type {
	// Unwrap pointer types for mutable collections
	if ptrType, ok := typ.(transpiler.PointerType); ok {
		typ = ptrType.Elem
	}
	if genType, ok := typ.(transpiler.GenericType); ok {
		if len(genType.Params) > 0 {
			return genType.Params[0]
		}
	}
	return transpiler.BasicType{Name: "any"}
}

// emitRestBinding appends the var-decl + guarded-assign for a named rest
// pattern (e.g., `case Array(head, rest...) =>` — rest is the binding name).
// Extracted from generateSeqPatternMatch as part of A2 cont.
func (t *galaASTTransformer) emitRestBinding(
	restPatternName string,
	matchedType transpiler.Type,
	objExpr ast.Expr,
	nonRestCount int,
	varDecls []ast.Stmt,
	guardedAssigns []ast.Stmt,
) ([]ast.Stmt, []ast.Stmt) {
	t.currentScope.vals[restPatternName] = false
	t.currentScope.valTypes[restPatternName] = matchedType

	// var restPatternName MatchedType
	varDecl := &ast.DeclStmt{
		Decl: &ast.GenDecl{
			Tok: token.VAR,
			Specs: []ast.Spec{
				&ast.ValueSpec{
					Names: []*ast.Ident{ast.NewIdent(restPatternName)},
					Type:  t.typeToExpr(matchedType),
				},
			},
		},
	}
	varDecls = append(varDecls, varDecl)

	// restPatternName = obj.SeqDrop(n).(MatchedType)
	guardedAssigns = append(guardedAssigns, &ast.AssignStmt{
		Lhs: []ast.Expr{ast.NewIdent(restPatternName)},
		Tok: token.ASSIGN,
		Rhs: []ast.Expr{
			&ast.TypeAssertExpr{
				X: &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   objExpr,
						Sel: ast.NewIdent("SeqDrop"),
					},
					Args: []ast.Expr{&ast.BasicLit{Kind: token.INT, Value: fmt.Sprintf("%d", nonRestCount)}},
				},
				Type: t.typeToExpr(matchedType),
			},
		},
	})
	return varDecls, guardedAssigns
}

// assembleSeqPatternGuardBlock wraps the accumulated guardedAssigns in an
// `if sizeCheckName { ... }` block and appends it to stmts. No-op when there
// are no guarded assigns. Extracted as part of A2 cont.
func assembleSeqPatternGuardBlock(stmts []ast.Stmt, sizeCheckName string, guardedAssigns []ast.Stmt) []ast.Stmt {
	if len(guardedAssigns) == 0 {
		return stmts
	}
	return append(stmts, &ast.IfStmt{
		Cond: ast.NewIdent(sizeCheckName),
		Body: &ast.BlockStmt{List: guardedAssigns},
	})
}

// scanSeqPatternArgs walks a seq-pattern's argument list, locating the rest
// pattern (if any) and returning its index, its binding name (or "" for a
// wildcard/anonymous rest), and the count of non-rest arguments. Extracted
// from generateSeqPatternMatch as part of A2.
func scanSeqPatternArgs(args []grammar.IArgumentContext) (restIndex int, restName string, nonRestCount int) {
	restIndex = -1
	for i, argCtx := range args {
		arg := argCtx.(*grammar.ArgumentContext)
		pat := arg.Pattern()
		if pat == nil {
			nonRestCount++
			continue
		}
		if restPat, ok := pat.(*grammar.RestPatternContext); ok {
			restIndex = i
			exprText := restPat.Expression().GetText()
			if !isWildcard(exprText) {
				restName = exprText
			}
		} else {
			nonRestCount++
		}
	}
	return
}

// generateSeqPatternMatch generates code for sequence pattern matching with rest patterns.
// It orchestrates the extracted helpers scanSeqPatternArgs, emitSizeCheck,
// emitNonRestBindings, and emitRestBinding.
//
// For example, Array(first, Some(n), rest...) matching against
// Array[Option[int]] generates:
//
//	_tmp_ok := obj.Size() >= 2
//	var _tmp_1 Option[int]
//	var first Option[int]
//	var _tmp_2 Option[int]
//	var n int  // ...and the other names Some(n) declares
//	var rest Array[Option[int]]
//	if _tmp_ok {
//	    _tmp_1 = obj.Get(0)
//	    first = _tmp_1
//	    _tmp_2 = obj.Get(1)
//	    ... Some(n) lowered against _tmp_2, assigning n ...
//	    rest = obj.SeqDrop(2).(Array[Option[int]])
//	}
//	if _tmp_ok && <Some(n) condition> { ... body }
func (t *galaASTTransformer) generateSeqPatternMatch(objExpr ast.Expr, argList *grammar.ArgumentListContext, matchedType transpiler.Type) (ast.Expr, []ast.Stmt, error) {
	if argList == nil {
		return ast.NewIdent("true"), nil, nil
	}

	args := argList.AllArgument()
	if len(args) == 0 {
		return ast.NewIdent("true"), nil, nil
	}

	restPatternIndex, restPatternName, nonRestCount := scanSeqPatternArgs(args)

	// Rest pattern must be the last argument.
	if restPatternIndex >= 0 && restPatternIndex != len(args)-1 {
		return nil, nil, galaerr.NewSemanticErrorAt(argList.GetStart().GetLine(), argList.GetStart().GetColumn(), "rest pattern (...) must be the last argument in a sequence pattern")
	}

	// Emit the size check and seed the statement+condition accumulators.
	sizeCheckStmt, sizeCheckName := t.emitSizeCheck(objExpr, nonRestCount)
	stmts := []ast.Stmt{sizeCheckStmt}
	conds := []ast.Expr{ast.NewIdent(sizeCheckName)}

	// Determine the element type for non-rest arg bindings.
	elemType := t.getSeqElementType(matchedType)
	elemTypeExpr := t.typeToExpr(elemType)
	if elemTypeExpr == nil {
		elemTypeExpr = ast.NewIdent("any")
	}

	// Emit bindings for the non-rest arguments.
	varDecls, guardedAssigns, extraConds, err := t.emitNonRestBindings(args, objExpr, elemType, elemTypeExpr)
	if err != nil {
		return nil, nil, err
	}
	conds = append(conds, extraConds...)

	// Handle rest pattern if present and named.
	if restPatternName != "" {
		varDecls, guardedAssigns = t.emitRestBinding(restPatternName, matchedType, objExpr, nonRestCount, varDecls, guardedAssigns)
	}

	stmts = append(stmts, varDecls...)
	stmts = assembleSeqPatternGuardBlock(stmts, sizeCheckName, guardedAssigns)

	t.needsStdImport = true

	// Combine all conditions into a single && chain.
	if len(conds) == 0 {
		return ast.NewIdent("true"), stmts, nil
	}
	finalCond := conds[0]
	for i := 1; i < len(conds); i++ {
		finalCond = &ast.BinaryExpr{X: finalCond, Op: token.LAND, Y: conds[i]}
	}
	return finalCond, stmts, nil
}

// emitSizeCheck generates `_tmpN := obj.Size() >= nonRestCount` and returns
// both the statement and the temp variable name (so callers can reference it
// in the guarded block condition). Extracted from generateSeqPatternMatch as
// part of A2 cont.
func (t *galaASTTransformer) emitSizeCheck(objExpr ast.Expr, nonRestCount int) (ast.Stmt, string) {
	sizeCheckName := t.nextTempVar()
	stmt := t.patternDefine([]string{sizeCheckName}, []ast.Expr{ast.NewIdent("bool")}, &ast.BinaryExpr{
		X: &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   objExpr,
				Sel: ast.NewIdent("Size"),
			},
		},
		Op: token.GEQ,
		Y:  &ast.BasicLit{Kind: token.INT, Value: fmt.Sprintf("%d", nonRestCount)},
	})
	return stmt, sizeCheckName
}

// emitNonRestBindings walks the seq pattern's non-rest arguments and generates
// the per-element bindings: a var declaration for each bound variable plus a
// guarded assignment that reads `obj.Get(i)` (or `std.As[T](obj.Get(i))` for
// typed patterns). Returns:
//
//	varDecls       — zero-value declarations outside the guard block
//	guardedAssigns — assignments that run only when the size check passes
//	extraConds     — additional boolean conditions introduced by typed/nested
//	                 patterns (e.g., the `ok` result of a type assertion)
//
// Extracted from generateSeqPatternMatch as part of A2 cont.
func (t *galaASTTransformer) emitNonRestBindings(
	args []grammar.IArgumentContext,
	objExpr ast.Expr,
	elemType transpiler.Type,
	elemTypeExpr ast.Expr,
) (varDecls []ast.Stmt, guardedAssigns []ast.Stmt, extraConds []ast.Expr, err error) {
	argIndex := 0
	for _, argCtx := range args {
		arg := argCtx.(*grammar.ArgumentContext)
		patCtx := arg.Pattern()
		if patCtx == nil {
			argIndex++
			continue
		}
		// Rest patterns are handled by emitRestBinding; skip here.
		if _, ok := patCtx.(*grammar.RestPatternContext); ok {
			continue
		}
		patternText := patCtx.GetText()
		if isWildcard(patternText) {
			argIndex++
			continue
		}

		// A binding (`head`), a nested pattern (`Circle(r)`) or a typed
		// pattern (`x: int`, `e: Tagged[_]`): read the element into a temp,
		// then lower the sub-pattern against it through the general
		// dispatcher. Everything runs inside the size guard, so a too-short
		// sequence never reads an element or runs a sub-pattern (a field read
		// through a nil pointer, a user Unapply) on a zero value; the names the
		// sub-pattern declares are hoisted before the guard so the arm and its
		// condition see them.
		tempName := t.nextTempVar()
		varDecls = append(varDecls, seqVarDecl(tempName, elemTypeExpr))
		guardedAssigns = append(guardedAssigns, seqGetAssign(tempName, objExpr, argIndex))
		nestedCond, nestedStmts, nerr := t.transformPatternWithType(patCtx, ast.NewIdent(tempName), elemType)
		if nerr != nil {
			return nil, nil, nil, nerr
		}
		decls, guarded, herr := t.hoistPatternDecls(nestedStmts, tempName, elemTypeExpr)
		if herr != nil {
			return nil, nil, nil, herr
		}
		varDecls = append(varDecls, decls...)
		guardedAssigns = append(guardedAssigns, guarded...)
		if ident, ok := nestedCond.(*ast.Ident); !ok || ident.Name != "true" {
			extraConds = append(extraConds, nestedCond)
		}
		argIndex++
	}
	return varDecls, guardedAssigns, extraConds, nil
}

// seqVarDecl builds `var name T` (`var name T = value` with a value) as a
// DeclStmt: for seq-pattern bindings (emitNonRestBindings) and the variables
// of a construct lowered as statements (hoisted_value.go).
func seqVarDecl(name string, typeExpr ast.Expr, value ...ast.Expr) ast.Stmt {
	return &ast.DeclStmt{
		Decl: &ast.GenDecl{
			Tok: token.VAR,
			Specs: []ast.Spec{
				&ast.ValueSpec{
					Names:  []*ast.Ident{ast.NewIdent(name)},
					Type:   typeExpr,
					Values: value,
				},
			},
		},
	}
}

// seqGetAssign builds `name = obj.Get(i)` as an AssignStmt for seq-pattern
// guarded bindings. Helper used by emitNonRestBindings.
func seqGetAssign(name string, objExpr ast.Expr, index int) ast.Stmt {
	return &ast.AssignStmt{
		Lhs: []ast.Expr{ast.NewIdent(name)},
		Tok: token.ASSIGN,
		Rhs: []ast.Expr{
			&ast.CallExpr{
				Fun:  &ast.SelectorExpr{X: objExpr, Sel: ast.NewIdent("Get")},
				Args: []ast.Expr{&ast.BasicLit{Kind: token.INT, Value: fmt.Sprintf("%d", index)}},
			},
		},
	}
}

// transformTuplePattern transforms a tuple pattern like (a, b, c) into direct field access.
// Instead of using reflection-based UnapplyTupleN, this generates direct access to V1, V2, etc.
func (t *galaASTTransformer) transformTuplePattern(patternExprs []grammar.IExpressionContext, objExpr ast.Expr, matchedType transpiler.Type) (ast.Expr, []ast.Stmt, error) {
	n := len(patternExprs)
	if n < 2 || n > 10 {
		if n > 0 {
			return nil, nil, galaerr.NewSemanticErrorAt(patternExprs[0].GetStart().GetLine(), patternExprs[0].GetStart().GetColumn(), fmt.Sprintf("tuple patterns must have 2-10 elements, got %d", n))
		}
		return nil, nil, galaerr.NewSemanticErrorAt(t.lastLine, t.lastCol, fmt.Sprintf("tuple patterns must have 2-10 elements, got %d", n))
	}

	var stmts []ast.Stmt
	var combinedCond ast.Expr

	// Extract element types from matched type if available
	var elementTypes []transpiler.Type
	if genType, ok := matchedType.(transpiler.GenericType); ok {
		elementTypes = genType.Params
	}

	// Generate bindings for each pattern element using direct field access.
	// Conditions from nested element patterns are AND-combined; bindings from
	// every element are collected so that `(true, code)` binds `code` even
	// when an earlier element contributes a literal-equality condition.
	for i, patExpr := range patternExprs {
		patText := patExpr.GetText()
		if isWildcard(patText) {
			continue
		}

		// Determine the type for this element
		var elemType transpiler.Type = transpiler.NilType{} // unknown, never erased to any
		if i < len(elementTypes) {
			elemType = elementTypes[i]
		}

		// Generate direct field access: objExpr.V{i+1}.Get()
		// Tuple fields are V1, V2, V3, etc.
		fieldName := fmt.Sprintf("V%d", i+1)
		elemExpr := &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X: &ast.SelectorExpr{
					X:   objExpr,
					Sel: ast.NewIdent(fieldName),
				},
				Sel: ast.NewIdent("Get"),
			},
		}

		// A binding or a nested pattern, both lowered recursively. Bindings
		// must be appended BEFORE we accumulate the condition so any later
		// element pattern (e.g. `code` in `(true, code)`) still gets bound.
		nestedCond, nestedStmts, err := t.transformExpressionPatternWithType(patExpr, elemExpr, elemType)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, nestedStmts...)

		if ident, ok := nestedCond.(*ast.Ident); !ok || ident.Name != "true" {
			t.needsStdImport = true
			if combinedCond == nil {
				combinedCond = nestedCond
			} else {
				combinedCond = &ast.BinaryExpr{X: combinedCond, Op: token.LAND, Y: nestedCond}
			}
		}
	}

	t.needsStdImport = true
	if combinedCond != nil {
		return combinedCond, stmts, nil
	}
	// All patterns are simple bindings or wildcards, condition is always true
	return ast.NewIdent("true"), stmts, nil
}

// inferExtractorTypeParams attempts to infer type parameters for a generic extractor
// by examining its Unapply method's first parameter type and matching it against
// the type of the expression being matched.
// For example, if Cons[T] has Unapply(l List[T]) and we're matching against List[int],
// this function will return [int] to instantiate Cons[int].
func (t *galaASTTransformer) inferExtractorTypeParams(extractorMeta *transpiler.TypeMetadata, matchedType transpiler.Type) []transpiler.Type {
	if extractorMeta == nil || len(extractorMeta.TypeParams) == 0 {
		return nil
	}

	// Get the Unapply method
	unapplyMeta, ok := extractorMeta.Methods["Unapply"]
	if !ok || len(unapplyMeta.ParamTypes) == 0 {
		return nil
	}

	// Get the first parameter type (the type we're matching against)
	unapplyParamType := unapplyMeta.ParamTypes[0]
	if transpiler.IsUnusable(unapplyParamType) {
		return nil
	}

	// Try to unify the parameter type with the matched type to infer type parameters
	// For example: unify List[T] with List[int] -> {T: int}
	substitution := make(map[string]transpiler.Type)
	if !t.unifyTypes(unapplyParamType, matchedType, extractorMeta.TypeParams, substitution) {
		return nil
	}

	// Build the result in the order of the extractor's type parameters
	result := make([]transpiler.Type, len(extractorMeta.TypeParams))
	for i, paramName := range extractorMeta.TypeParams {
		if inferredType, ok := substitution[paramName]; ok {
			result[i] = inferredType
		} else {
			// If we couldn't infer a type parameter, return nil to fall back to 'any'
			return nil
		}
	}

	return result
}

// stripPackagePrefix removes package prefixes from type names for comparison.
// For example, "std.Either" becomes "Either", "main.User" becomes "User".
func stripPackagePrefix(name string) string {
	_, bare := splitPackageQualifier(name)
	return bare
}

// splitPackageQualifier splits a possibly-qualified type name at its last "."
// into its package qualifier and bare name: "std.None" → ("std", "None"),
// "User" → ("", "User"). Sealed-variant lookups follow the last-dot convention
// (the bare case name is the metadata key, the prefix is the package scope), so
// callers that need both halves should use this rather than re-deriving the
// split inline.
func splitPackageQualifier(name string) (pkg, bare string) {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			return name[:i], name[i+1:]
		}
	}
	return "", name
}

// unifyTypes attempts to unify two types and extract type parameter substitutions.
// Delegates to unifyForInference for consistent unification logic across the codebase.
func (t *galaASTTransformer) unifyTypes(pattern, concrete transpiler.Type, typeParams []string, substitution map[string]transpiler.Type) bool {
	return t.unifyForInference(pattern, concrete, typeParams, substitution)
}

// unwrapOptionType unwraps Option[X] to return X
func (t *galaASTTransformer) unwrapOptionType(typ transpiler.Type) transpiler.Type {
	genType, ok := typ.(transpiler.GenericType)
	if !ok {
		return transpiler.NilType{}
	}
	baseName := genType.Base.BaseName()
	if baseName == "Option" || baseName == "std.Option" {
		if len(genType.Params) > 0 {
			return genType.Params[0]
		}
	}
	return transpiler.NilType{}
}

// isDirectUnapplyReturnType returns true if the return type of an Unapply method
// can be handled directly without reflection. Supported types are:
// - bool (guard pattern)
// - Option[T] (extractor pattern)
func (t *galaASTTransformer) isDirectUnapplyReturnType(typ transpiler.Type) bool {
	if transpiler.IsUnusable(typ) {
		return false
	}
	// Check for bool
	if basic, ok := typ.(transpiler.BasicType); ok && basic.Name == "bool" {
		return true
	}
	// Check for Option[T]
	if genType, ok := typ.(transpiler.GenericType); ok {
		baseName := genType.Base.BaseName()
		if baseName == "Option" || baseName == "std.Option" {
			return true
		}
	}
	return false
}

// wrapWithTypeAssertion wraps an expression with a type assertion.
// For example, wraps `std.GetSafe(res, 0)` to `std.GetSafe(res, 0).(int)`
func (t *galaASTTransformer) wrapWithTypeAssertion(expr ast.Expr, typ transpiler.Type) ast.Expr {
	typeExpr := t.typeToExpr(typ)
	if typeExpr == nil {
		return expr
	}

	return &ast.TypeAssertExpr{
		X:    expr,
		Type: typeExpr,
	}
}

// fixupReturnStatements traverses statements and adds type assertions to return statements
// that return 'any' values when the expected result type is concrete.
func (t *galaASTTransformer) fixupReturnStatements(stmts []ast.Stmt, resultType transpiler.Type) {
	for _, stmt := range stmts {
		t.fixupReturnStatement(stmt, resultType)
	}
}

// assertAnyIdent is expr asserted to typ when expr is a name of type `any`,
// and expr otherwise.
func (t *galaASTTransformer) assertAnyIdent(expr ast.Expr, typ transpiler.Type) ast.Expr {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return expr
	}
	if vt := t.getType(ident.Name); vt != nil && vt.IsAny() {
		return &ast.TypeAssertExpr{X: expr, Type: t.typeToExpr(typ)}
	}
	return expr
}

func (t *galaASTTransformer) fixupReturnStatement(stmt ast.Stmt, resultType transpiler.Type) {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		for i, result := range s.Results {
			s.Results[i] = t.assertAnyIdent(result, resultType)
		}
	case *ast.IfStmt:
		// Recursively process if body and else clause
		if s.Body != nil {
			t.fixupReturnStatements(s.Body.List, resultType)
		}
		if s.Else != nil {
			if block, ok := s.Else.(*ast.BlockStmt); ok {
				t.fixupReturnStatements(block.List, resultType)
			} else {
				t.fixupReturnStatement(s.Else, resultType)
			}
		}
	case *ast.BlockStmt:
		t.fixupReturnStatements(s.List, resultType)
	}
}

// isValidGoExprStatement reports whether expr can legally appear as a Go
// expression statement. Go restricts expression statements to function/method
// calls, channel receives (`<-ch`), and a few builtins it forwards as calls.
// A bare literal, identifier, selector, binary expression, or type assertion
// is rejected by the Go compiler ("X evaluated but not used"). We use this
// when lowering a value-producing arm into a void context: pure expressions
// have no observable effect with their value discarded, so they can be
// dropped instead of emitted as invalid Go.
func isValidGoExprStatement(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.CallExpr:
		return true
	case *ast.UnaryExpr:
		// Channel receive used for side effects (`<-ch`).
		return e.Op == token.ARROW
	case *ast.ParenExpr:
		return isValidGoExprStatement(e.X)
	default:
		return false
	}
}

// stripReturnStatements converts return statements to expression statements + empty returns for void match.
// This is used when a match is used purely for side effects (like fmt.Printf calls).
// We keep empty returns to ensure early exit after each case branch.
func (t *galaASTTransformer) stripReturnStatements(stmts []ast.Stmt) []ast.Stmt {
	result := make([]ast.Stmt, 0, len(stmts))
	for _, stmt := range stmts {
		result = append(result, t.stripReturnStatement(stmt))
	}
	return result
}

func (t *galaASTTransformer) stripReturnStatement(stmt ast.Stmt) ast.Stmt {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		// Convert "return expr" to "expr; return" (execute the expression, then return with no value).
		// In void context the expression's value is discarded; if the expression has no
		// observable side effects (e.g. a bare literal, identifier, or selector left over from
		// a value-producing match arm), Go would reject it as an "expression evaluated but not
		// used" expression statement. Drop such expressions and keep just the empty return.
		if len(s.Results) > 0 {
			list := []ast.Stmt{}
			if isValidGoExprStatement(s.Results[0]) {
				list = append(list, &ast.ExprStmt{X: s.Results[0]})
			}
			list = append(list, &ast.ReturnStmt{}) // Empty return for early exit
			return &ast.BlockStmt{List: list}
		}
		// Keep empty returns as-is
		return s
	case *ast.IfStmt:
		// Recursively process if body and else clause
		newStmt := &ast.IfStmt{
			Init: s.Init,
			Cond: s.Cond,
		}
		if s.Body != nil {
			newStmt.Body = &ast.BlockStmt{List: t.stripReturnStatements(s.Body.List)}
		}
		if s.Else != nil {
			if block, ok := s.Else.(*ast.BlockStmt); ok {
				newStmt.Else = &ast.BlockStmt{List: t.stripReturnStatements(block.List)}
			} else {
				newStmt.Else = t.stripReturnStatement(s.Else)
			}
		}
		return newStmt
	case *ast.BlockStmt:
		return &ast.BlockStmt{List: t.stripReturnStatements(s.List)}
	default:
		return stmt
	}
}

// generateDirectUnapplyPattern generates reflection-free code for generic extractors.
// Instead of using std.UnapplyFull (which uses reflection), this generates direct method calls:
//
//	_tmp_opt := Cons[int]{}.Unapply(list)
//	_tmp_ok := _tmp_opt.IsDefined()
//	var _tmp_tuple std.Tuple[int, List[int]]
//	if _tmp_ok {
//	    _tmp_tuple = _tmp_opt.Get()
//	}
//	head := _tmp_tuple.V1
//	tail := _tmp_tuple.V2
//	if _tmp_ok { ... body }
//
// This eliminates reflection from: UnapplyFull, UnapplyTuple, GetSafe, and As.
// The .Get() is guarded by IsDefined() to prevent panics.
func (t *galaASTTransformer) generateDirectUnapplyPattern(
	extractorName string,
	extractorMeta *transpiler.TypeMetadata,
	inferredTypes []transpiler.Type,
	unapplyMeta *transpiler.MethodMetadata,
	objExpr ast.Expr,
	argList *grammar.ArgumentListContext,
	matchedType transpiler.Type,
) (ast.Expr, []ast.Stmt, error) {

	var allBindings []ast.Stmt
	var conds []ast.Expr

	// Build the extractor type expression with inferred type parameters
	// e.g., Cons[int]{}
	extractorTypeExpr := t.ident(extractorName)
	if len(inferredTypes) == 1 {
		extractorTypeExpr = &ast.IndexExpr{X: extractorTypeExpr, Index: t.typeToExpr(inferredTypes[0])}
	} else if len(inferredTypes) > 1 {
		indices := make([]ast.Expr, len(inferredTypes))
		for i, tp := range inferredTypes {
			indices[i] = t.typeToExpr(tp)
		}
		extractorTypeExpr = &ast.IndexListExpr{X: extractorTypeExpr, Indices: indices}
	}

	// Get the return type of Unapply and substitute type parameters
	// e.g., Option[Tuple[T, List[T]]] -> Option[Tuple[int, List[int]]]
	returnType := t.substituteConcreteTypes(unapplyMeta.ReturnType, extractorMeta.TypeParams, inferredTypes)

	// Check if Unapply returns bool (guard pattern) or Option[T] (extractor pattern)
	isBoolReturn := false
	if basic, ok := returnType.(transpiler.BasicType); ok && basic.Name == "bool" {
		isBoolReturn = true
	}

	// Generate: _tmp_result := Extractor[T]{}.Unapply(obj)
	resultName := t.nextTempVar()
	unapplyCall := t.patternDefine([]string{resultName}, []ast.Expr{t.knownTypeExpr(returnType)}, &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   &ast.CompositeLit{Type: extractorTypeExpr},
			Sel: ast.NewIdent("Unapply"),
		},
		Args: []ast.Expr{objExpr},
	})
	allBindings = append(allBindings, unapplyCall)

	var okName string
	var innerType transpiler.Type

	if isBoolReturn {
		// For bool-returning extractors, the result IS the condition
		// No inner value to extract
		okName = resultName
		conds = append(conds, ast.NewIdent(okName))
		innerType = transpiler.NilType{}
	} else {
		// For Option-returning extractors, check IsDefined and extract inner value
		// Generate: _tmp_ok := _tmp_result.IsDefined()
		okName = t.nextTempVar()
		isDefinedAssign := t.patternDefine([]string{okName}, []ast.Expr{ast.NewIdent("bool")}, &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   ast.NewIdent(resultName),
				Sel: ast.NewIdent("IsDefined"),
			},
		})
		allBindings = append(allBindings, isDefinedAssign)
		conds = append(conds, ast.NewIdent(okName))

		// Unwrap Option to get the inner type
		innerType = t.unwrapOptionType(returnType)
	}

	// Handle the pattern arguments
	if argList != nil && len(argList.AllArgument()) > 0 && !isBoolReturn {
		numArgs := len(argList.AllArgument())

		// Check if the inner type is a Tuple that needs expansion
		isTupleResult := false
		var tupleParamTypes []transpiler.Type
		if genType, ok := innerType.(transpiler.GenericType); ok {
			baseName := genType.Base.BaseName()
			if t.isTupleTypeName(baseName) || baseName == "Tuple" || baseName == "std.Tuple" {
				isTupleResult = true
				tupleParamTypes = genType.Params
			}
		}

		// Generate a guarded .Get() call:
		// var _tmp_inner InnerType
		// if _tmp_ok { _tmp_inner = _tmp_result.Get() }
		innerName := t.nextTempVar()

		// Declare the variable with its type
		innerTypeExpr := t.typeToExpr(innerType)
		if innerTypeExpr == nil {
			innerTypeExpr = ast.NewIdent("any")
		}

		varDecl := &ast.DeclStmt{
			Decl: &ast.GenDecl{
				Tok: token.VAR,
				Specs: []ast.Spec{
					&ast.ValueSpec{
						Names: []*ast.Ident{ast.NewIdent(innerName)},
						Type:  innerTypeExpr,
					},
				},
			},
		}
		allBindings = append(allBindings, varDecl)

		// Generate: if _tmp_ok { _tmp_inner = _tmp_result.Get() }
		guardedGet := &ast.IfStmt{
			Cond: ast.NewIdent(okName),
			Body: &ast.BlockStmt{
				List: []ast.Stmt{
					&ast.AssignStmt{
						Lhs: []ast.Expr{ast.NewIdent(innerName)},
						Tok: token.ASSIGN,
						Rhs: []ast.Expr{
							&ast.CallExpr{
								Fun: &ast.SelectorExpr{
									X:   ast.NewIdent(resultName),
									Sel: ast.NewIdent("Get"),
								},
							},
						},
					},
				},
			},
		}
		guardIdx := len(allBindings)
		allBindings = append(allBindings, guardedGet)
		// Suppress "declared and not used" for inner temp var in case all pattern args are wildcards
		allBindings = blankAssignTempVar(allBindings, innerName)

		// Declarations a nested sub-pattern hoists out of the guard; they
		// are spliced in just before it once every argument is lowered.
		var hoisted []ast.Stmt

		// For each argument pattern, generate direct field access
		for i, argCtx := range argList.AllArgument() {
			arg := argCtx.(*grammar.ArgumentContext)
			if arg.Pattern() == nil {
				continue
			}
			patternText := arg.Pattern().GetText()

			if isWildcard(patternText) {
				continue
			}

			// Determine the type and access expression for this element
			var elemType transpiler.Type
			var elemExpr ast.Expr

			if isTupleResult && numArgs > 1 && numArgs == len(tupleParamTypes) {
				// Implicit tuple expansion: Cons(head, tail) -> access tuple.V1, tuple.V2
				if i < len(tupleParamTypes) {
					elemType = tupleParamTypes[i]
				}
				// Access tuple field directly: _tmp_inner.V1.Get(), _tmp_inner.V2.Get(), etc.
				// Note: Tuple fields are stored as Immutable[T], so we need to call .Get() to unwrap
				fieldName := fmt.Sprintf("V%d", i+1)
				elemExpr = &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X: &ast.SelectorExpr{
							X:   ast.NewIdent(innerName),
							Sel: ast.NewIdent(fieldName),
						},
						Sel: ast.NewIdent("Get"),
					},
				}
			} else if isTupleResult && numArgs == 1 {
				// Explicit Tuple pattern: Cons(Tuple(head, tail)) -> return full tuple
				elemType = innerType
				elemExpr = ast.NewIdent(innerName)
			} else {
				// Single value extraction (not a tuple)
				elemType = innerType
				elemExpr = ast.NewIdent(innerName)
			}

			// Check if this is a simple identifier binding
			if t.isPatternBinding(patternText, elemType) {
				varName := patternText
				t.currentScope.vals[varName] = false
				if elemType != nil && !elemType.IsNil() {
					t.currentScope.valTypes[varName] = elemType
				} else {
					t.currentScope.valTypes[varName] = transpiler.BasicType{Name: "any"}
				}

				// Generate: varName := elemExpr
				allBindings = append(allBindings, t.patternDefine([]string{varName}, []ast.Expr{t.knownTypeExpr(elemType)}, elemExpr))
			} else {
				// Handle nested patterns recursively. The sub-pattern reads
				// the payload, which is only set when the extractor matched,
				// so it runs inside the guard; its bindings are declared
				// before the guard so the arm sees them.
				subCond, subBindings, err := t.transformPatternWithType(arg.Pattern(), elemExpr, elemType)
				if err != nil {
					return nil, nil, err
				}
				decls, guarded, err := t.hoistPatternDecls(subBindings, innerName, innerTypeExpr)
				if err != nil {
					return nil, nil, err
				}
				hoisted = append(hoisted, decls...)
				guardedGet.Body.List = append(guardedGet.Body.List, guarded...)
				// Add sub-condition to the list of conditions to check
				if subCond != nil {
					// Check if subCond is just "true" - if so, skip it
					if ident, ok := subCond.(*ast.Ident); !ok || ident.Name != "true" {
						conds = append(conds, subCond)
					}
				}
			}
		}
		allBindings = spliceStmts(allBindings, guardIdx, hoisted)
	}

	// Build final condition by ANDing all conditions
	if len(conds) == 0 {
		return ast.NewIdent("true"), allBindings, nil
	}
	var finalCond ast.Expr = conds[0]
	for i := 1; i < len(conds); i++ {
		finalCond = &ast.BinaryExpr{
			X:  finalCond,
			Op: token.LAND,
			Y:  conds[i],
		}
	}

	return finalCond, allBindings, nil
}

// tryBindingExtractorPattern matches against a val/var — `r`, or an imported
// `pkg.R` — whose type defines Unapply: `case r(groups) =>`. handled is false
// when the binding's type has no Unapply.
func (t *galaASTTransformer) tryBindingExtractorPattern(
	b binding,
	argList *grammar.ArgumentListContext,
	objExpr ast.Expr,
	matchedType transpiler.Type,
	patExprCtx grammar.IExpressionContext,
) (ast.Expr, []ast.Stmt, bool, error) {
	if b.typ == nil || b.typ.IsNil() {
		return nil, nil, false, nil
	}
	varTypeName := b.typ.BaseName()
	varMeta := t.getTypeMeta(varTypeName)
	if varMeta == nil {
		return nil, nil, false, nil
	}
	unapplyMeta, hasUnapply := varMeta.Methods["Unapply"]
	if !hasUnapply {
		return nil, nil, false, nil
	}
	if returnType := unapplyMeta.ReturnType; !t.isDirectUnapplyReturnType(returnType) {
		return nil, nil, true, galaerr.NewSemanticErrorAt(patExprCtx.GetStart().GetLine(), patExprCtx.GetStart().GetColumn(),
			fmt.Sprintf("extractor variable '%s' (type '%s') must have Unapply returning bool or Option[T], got '%s'",
				b, varTypeName, returnType.String()))
	}
	expr, stmts, err := t.generateVariableUnapplyPattern(b, varMeta, unapplyMeta, objExpr, argList, matchedType)
	return expr, stmts, true, err
}

// generateVariableUnapplyPattern generates a pattern match using a variable's Unapply method.
// Instead of Type{}.Unapply(obj), it generates variableName.Unapply(obj).
// This enables instance extractors like:
//
//	val dateRegex = regex.MustCompile("(\d{4})-(\d{2})-(\d{2})")
//	input match { case dateRegex(groups) => ... }
//
// which generates: _tmp := dateRegex.Unapply(input)
func (t *galaASTTransformer) generateVariableUnapplyPattern(
	b binding,
	varTypeMeta *transpiler.TypeMetadata,
	unapplyMeta *transpiler.MethodMetadata,
	objExpr ast.Expr,
	argList *grammar.ArgumentListContext,
	matchedType transpiler.Type,
) (ast.Expr, []ast.Stmt, error) {

	var allBindings []ast.Stmt
	var conds []ast.Expr

	// Get the return type of Unapply, substituting type params from the variable's concrete type.
	// e.g., if variable is JsonEncoder[Person] and Unapply returns Option[T], resolve T=Person → Option[Person]
	returnType := unapplyMeta.ReturnType
	// Unwrap Immutable[X] to get X (vals are wrapped)
	if genType, ok := unwrapGalaType(b.typ).(transpiler.GenericType); ok && len(varTypeMeta.TypeParams) > 0 {
		typeSubst := make(map[string]string)
		for i, tp := range varTypeMeta.TypeParams {
			if i < len(genType.Params) {
				typeSubst[tp] = genType.Params[i].String()
			}
		}
		if len(typeSubst) > 0 {
			returnType = t.substituteTranspilerTypeParams(returnType, typeSubst)
		}
	}

	// Check if Unapply returns bool (guard pattern) or Option[T] (extractor pattern)
	isBoolReturn := false
	if basic, ok := returnType.(transpiler.BasicType); ok && basic.Name == "bool" {
		isBoolReturn = true
	}

	// The receiver reads the variable, unwrapping a val's Immutable[T].
	receiverExpr := t.bindingRead(b)

	// Generate: _tmp_result := variableName[.Get()].Unapply(obj)
	resultName := t.nextTempVar()
	unapplyCall := t.patternDefine([]string{resultName}, []ast.Expr{t.knownTypeExpr(returnType)}, &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   receiverExpr,
			Sel: ast.NewIdent("Unapply"),
		},
		Args: []ast.Expr{objExpr},
	})
	allBindings = append(allBindings, unapplyCall)

	var okName string
	var innerType transpiler.Type

	if isBoolReturn {
		okName = resultName
		conds = append(conds, ast.NewIdent(okName))
		innerType = transpiler.NilType{}
	} else {
		// For Option-returning extractors, check IsDefined and extract inner value
		okName = t.nextTempVar()
		isDefinedAssign := t.patternDefine([]string{okName}, []ast.Expr{ast.NewIdent("bool")}, &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   ast.NewIdent(resultName),
				Sel: ast.NewIdent("IsDefined"),
			},
		})
		allBindings = append(allBindings, isDefinedAssign)
		conds = append(conds, ast.NewIdent(okName))

		// Unwrap Option to get the inner type
		innerType = t.unwrapOptionType(returnType)
	}

	// Handle the pattern arguments
	if argList != nil && len(argList.AllArgument()) > 0 && !isBoolReturn {
		numArgs := len(argList.AllArgument())

		// Check if the inner type is a Tuple that needs expansion
		isTupleResult := false
		var tupleParamTypes []transpiler.Type
		if genType, ok := innerType.(transpiler.GenericType); ok {
			baseName := genType.Base.BaseName()
			if t.isTupleTypeName(baseName) || baseName == "Tuple" || baseName == "std.Tuple" {
				isTupleResult = true
				tupleParamTypes = genType.Params
			}
		}

		// Generate a guarded .Get() call
		innerName := t.nextTempVar()

		innerTypeExpr := t.typeToExpr(innerType)
		if innerTypeExpr == nil {
			innerTypeExpr = ast.NewIdent("any")
		}

		varDecl := &ast.DeclStmt{
			Decl: &ast.GenDecl{
				Tok: token.VAR,
				Specs: []ast.Spec{
					&ast.ValueSpec{
						Names: []*ast.Ident{ast.NewIdent(innerName)},
						Type:  innerTypeExpr,
					},
				},
			},
		}
		allBindings = append(allBindings, varDecl)

		guardedGet := &ast.IfStmt{
			Cond: ast.NewIdent(okName),
			Body: &ast.BlockStmt{
				List: []ast.Stmt{
					&ast.AssignStmt{
						Lhs: []ast.Expr{ast.NewIdent(innerName)},
						Tok: token.ASSIGN,
						Rhs: []ast.Expr{
							&ast.CallExpr{
								Fun: &ast.SelectorExpr{
									X:   ast.NewIdent(resultName),
									Sel: ast.NewIdent("Get"),
								},
							},
						},
					},
				},
			},
		}
		guardIdx := len(allBindings)
		allBindings = append(allBindings, guardedGet)
		allBindings = blankAssignTempVar(allBindings, innerName)
		var hoisted []ast.Stmt

		for i, argCtx := range argList.AllArgument() {
			arg := argCtx.(*grammar.ArgumentContext)
			if arg.Pattern() == nil {
				continue
			}
			patternText := arg.Pattern().GetText()

			if isWildcard(patternText) {
				continue
			}

			var elemType transpiler.Type
			var elemExpr ast.Expr

			if isTupleResult && numArgs > 1 && numArgs == len(tupleParamTypes) {
				if i < len(tupleParamTypes) {
					elemType = tupleParamTypes[i]
				}
				fieldName := fmt.Sprintf("V%d", i+1)
				elemExpr = &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X: &ast.SelectorExpr{
							X:   ast.NewIdent(innerName),
							Sel: ast.NewIdent(fieldName),
						},
						Sel: ast.NewIdent("Get"),
					},
				}
			} else if isTupleResult && numArgs == 1 {
				elemType = innerType
				elemExpr = ast.NewIdent(innerName)
			} else {
				elemType = innerType
				elemExpr = ast.NewIdent(innerName)
			}

			if t.isPatternBinding(patternText, elemType) {
				varName := patternText
				t.currentScope.vals[varName] = false
				if elemType != nil && !elemType.IsNil() {
					t.currentScope.valTypes[varName] = elemType
				} else {
					t.currentScope.valTypes[varName] = transpiler.BasicType{Name: "any"}
				}

				allBindings = append(allBindings, t.patternDefine([]string{varName}, []ast.Expr{t.knownTypeExpr(elemType)}, elemExpr))
			} else {
				// As in generateDirectUnapplyPattern: run the sub-pattern
				// inside the guard, declare its bindings before it.
				subCond, subBindings, err := t.transformPatternWithType(arg.Pattern(), elemExpr, elemType)
				if err != nil {
					return nil, nil, err
				}
				decls, guarded, err := t.hoistPatternDecls(subBindings, innerName, innerTypeExpr)
				if err != nil {
					return nil, nil, err
				}
				hoisted = append(hoisted, decls...)
				guardedGet.Body.List = append(guardedGet.Body.List, guarded...)
				if subCond != nil {
					if ident, ok := subCond.(*ast.Ident); !ok || ident.Name != "true" {
						conds = append(conds, subCond)
					}
				}
			}
		}
		allBindings = spliceStmts(allBindings, guardIdx, hoisted)
	}

	// Build final condition by ANDing all conditions
	if len(conds) == 0 {
		return ast.NewIdent("true"), allBindings, nil
	}
	var finalCond ast.Expr = conds[0]
	for i := 1; i < len(conds); i++ {
		finalCond = &ast.BinaryExpr{
			X:  finalCond,
			Op: token.LAND,
			Y:  conds[i],
		}
	}

	return finalCond, allBindings, nil
}
