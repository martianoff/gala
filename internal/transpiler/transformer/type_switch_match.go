package transformer

import (
	"go/ast"
	"go/token"
	"go/types"
	"regexp"
	"strings"

	"martianoff/gala/internal/transpiler"
)

// Type-switch dispatch for type patterns
//
// A type pattern `case x: T` lowers to `x, ok := std.As[T](obj)`. As takes an
// `any`, so a subject of a type parameter is boxed for every arm it is tested
// against, and As's parameter escapes (it may be returned, or unwrapped
// through a method call), so each of those boxes is a heap allocation. A
// match over the dynamic type of a type parameter or interface value whose
// arms are type patterns is dispatched by one Go type switch instead, in
// front of the unchanged if-else chain of its arms:
//
//	var _tmp_1 int
//	var _tmp_2 Hashable
//	_tmp_3 := 0
//	switch _tmp_4 := any(obj).(type) {
//	case std.ImmutableUnwrapper, std.Transparent:
//		var _tmp_5 bool
//		if _tmp_1, _tmp_5 = std.As[int](obj); _tmp_5 {
//			_tmp_3 = 1
//		} else if _tmp_2, _tmp_5 = std.As[Hashable](obj); _tmp_5 {
//			_tmp_3 = 2
//		}
//	case int:
//		_tmp_1, _tmp_3 = _tmp_4, 1
//	case Hashable:
//		_tmp_2, _tmp_3 = any(obj).(Hashable), 2
//	}
//	{ i, _tmp_6 := _tmp_1, _tmp_3 == 1; if _tmp_6 { ... } else { ... } }
//
// The switch finds the first type arm the subject matches, and its value;
// each type arm of the chain then reads `arm == k` and that value in place of
// its As call. Every other arm, and every guard, runs in the chain as before,
// in order, so the switch only replaces As calls, which have no effects.
//
// std.As looks through std's transparent wrappers (an ImmutableUnwrapper, a
// recovered panic's error): a value of one goes to the switch's first case,
// which runs the arms' As calls in order exactly as the chain did. Any other
// value has no wrapper to look through, so As is the plain Go assertion and
// the first case of the switch to match is the first arm As would match.
//
// The subject is boxed once, for the switch, and that box does not escape: a
// case of a concrete type copies the value out of it, and a case of an
// interface type, whose value may escape, boxes the subject afresh. A value of
// a primitive type is matched without an allocation.
//
// A match is dispatched this way only when the switch says exactly what the
// As calls did: its subject is a type parameter or an interface, every arm is
// an unguarded type pattern of a type that names no type parameter (no two of
// them the same type), a `_` or a binding of the whole subject, and the arms
// were lowered to the shape above. Any other match keeps its As calls.

// typeArm is one unguarded type pattern of a match the type switch dispatches:
// its As call's define in the chain and the type it tests.
type typeArm struct {
	define   *ast.AssignStmt
	typeExpr ast.Expr
	iface    bool
}

// typeSwitchDispatch returns the statements that dispatch a match over the
// dynamic type of its subject with one type switch, and rewrites the As calls
// of its type arms to read what the switch found; nil, leaving the arms as
// they are, when the match is not one the switch can dispatch (see above).
// arms are the lowered arms of caseClauses; isDefault and isBinding classify
// them as buildMatchExpressionFromClauses did.
func (t *galaASTTransformer) typeSwitchDispatch(caseClauses []caseSrc, arms []matchArm, isDefault, isBinding []bool, paramName string, matchedType transpiler.Type) []ast.Stmt {
	if transpiler.IsUnusable(matchedType) || !t.holdsAnyType(matchedType) {
		return nil
	}
	var typeArms []typeArm
	// The switch's first case lists the transparent types; no arm may list
	// one again.
	seen := map[string]bool{}
	for _, typ := range t.transparentTypes(false) {
		seen[types.ExprString(typ)] = true
	}
	for i, cc := range caseClauses {
		if isDefault[i] || isBinding[i] || isWildcard(cc.pattern().text()) {
			continue
		}
		if cc.pattern().kind() != patKindTyped || cc.hasGuard() {
			return nil
		}
		arm, ok := t.typePatternArm(arms[i].clause, paramName)
		if !ok {
			return nil
		}
		typ := t.astTypeToTranspilerType(arm.typeExpr)
		if transpiler.IsUnusable(typ) || t.hasTypeParams(typ) {
			return nil
		}
		// Go rejects a type switch that lists one type twice, under any of
		// its names, so such a match keeps its As calls.
		keys := []string{goTypeSpelling(types.ExprString(arm.typeExpr)), goTypeSpelling(t.followAliasChain(typ).String())}
		if seen[keys[0]] || seen[keys[1]] {
			return nil
		}
		seen[keys[0]], seen[keys[1]] = true, true
		arm.iface = t.isInterfaceType(typ)
		typeArms = append(typeArms, arm)
	}
	if len(typeArms) == 0 {
		return nil
	}
	return t.lowerTypeSwitchDispatch(typeArms, paramName)
}

// typePatternArm reads a lowered unguarded type pattern, `{ x, ok :=
// std.As[T](obj); if ok { ... } }`, as a typeArm.
func (t *galaASTTransformer) typePatternArm(clause ast.Stmt, paramName string) (typeArm, bool) {
	block, ok := clause.(*ast.BlockStmt)
	if !ok || len(block.List) != 2 {
		return typeArm{}, false
	}
	define, ok := block.List[0].(*ast.AssignStmt)
	if !ok || define.Tok != token.DEFINE || len(define.Lhs) != 2 || len(define.Rhs) != 1 {
		return typeArm{}, false
	}
	okIdent, ok := define.Lhs[1].(*ast.Ident)
	if !ok {
		return typeArm{}, false
	}
	ifStmt, ok := block.List[1].(*ast.IfStmt)
	if !ok || ifStmt.Init != nil {
		return typeArm{}, false
	}
	if cond, ok := ifStmt.Cond.(*ast.Ident); !ok || cond.Name != okIdent.Name {
		return typeArm{}, false
	}
	call, ok := define.Rhs[0].(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return typeArm{}, false
	}
	if subject, ok := call.Args[0].(*ast.Ident); !ok || subject.Name != paramName {
		return typeArm{}, false
	}
	index, ok := call.Fun.(*ast.IndexExpr)
	if !ok || !t.isStdHelper(index.X, "As") {
		return typeArm{}, false
	}
	return typeArm{define: define, typeExpr: index.Index}, true
}

// isStdHelper reports whether expr is the reference stdIdent(name) builds.
func (t *galaASTTransformer) isStdHelper(expr ast.Expr, name string) bool {
	return types.ExprString(expr) == types.ExprString(t.stdHelperIdent(name))
}

// transparentTypes are the types of the values std.As looks through, the
// first case of a dispatching type switch: an ImmutableUnwrapper, and
// std.Transparent, which std's wrappers (a recovered panic's error among
// them) implement. The references mark the std import as needed only when
// emit is set.
func (t *galaASTTransformer) transparentTypes(emit bool) []ast.Expr {
	ref := t.stdHelperIdent
	if emit {
		ref = t.stdIdent
	}
	return []ast.Expr{ref("ImmutableUnwrapper"), ref("Transparent")}
}

// goTypeSpelling is the spelling of a Go type with the predeclared aliases in
// it (`byte`, `rune`, `interface{}`), at any depth, replaced by the types they
// stand for: `[]byte` and `[]uint8` are one type.
func goTypeSpelling(typ string) string {
	typ = strings.ReplaceAll(typ, "interface{}", "any")
	return predeclaredAlias.ReplaceAllStringFunc(typ, func(m string) string {
		lead, name := m[:len(m)-4], m[len(m)-4:]
		return lead + canonicalScalar(name)
	})
}

// predeclaredAlias matches `byte` or `rune` as a whole, unqualified name, with
// the character before it.
var predeclaredAlias = regexp.MustCompile(`(^|[^.\w])(byte|rune)\b`)

// lowerTypeSwitchDispatch builds the dispatch of typeArms, and rewrites each
// arm's As call to read it (see typeSwitchDispatch).
func (t *galaASTTransformer) lowerTypeSwitchDispatch(typeArms []typeArm, paramName string) []ast.Stmt {
	// The subject is converted to any even when its type is any: an inlined
	// match binds it as its own Go type, which a type switch may not take.
	subject := func() ast.Expr {
		return &ast.CallExpr{Fun: ast.NewIdent("any"), Args: []ast.Expr{ast.NewIdent(paramName)}}
	}
	armName := t.nextTempVar()
	switchedName := t.nextTempVar()
	okName := t.nextTempVar()
	stmts := make([]ast.Stmt, 0, len(typeArms)+2)
	values := make([]string, len(typeArms))
	for k, arm := range typeArms {
		values[k] = t.nextTempVar()
		stmts = append(stmts, seqVarDecl(values[k], arm.typeExpr))
	}
	stmts = append(stmts, &ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent(armName)}, Tok: token.DEFINE, Rhs: []ast.Expr{intLit(0)}})

	// The first case: the As calls, in order, for a value As looks through.
	var asChain ast.Stmt
	for k := len(typeArms) - 1; k >= 0; k-- {
		asChain = &ast.IfStmt{
			Init: &ast.AssignStmt{
				Lhs: []ast.Expr{ast.NewIdent(values[k]), ast.NewIdent(okName)},
				Tok: token.ASSIGN,
				Rhs: []ast.Expr{t.stdAsCall(typeArms[k].typeExpr, ast.NewIdent(paramName))},
			},
			Cond: ast.NewIdent(okName),
			Body: &ast.BlockStmt{List: []ast.Stmt{
				&ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent(armName)}, Tok: token.ASSIGN, Rhs: []ast.Expr{intLit(k + 1)}},
			}},
			Else: asChain,
		}
	}
	cases := []ast.Stmt{&ast.CaseClause{List: t.transparentTypes(true), Body: []ast.Stmt{
		seqVarDecl(okName, ast.NewIdent("bool")),
		asChain,
	}}}

	// A case per type arm. A concrete value is copied out of the switch's
	// box; an interface value boxes the subject afresh, so the switch's box
	// never escapes.
	bindsSwitched := false
	for k, arm := range typeArms {
		var value ast.Expr
		if arm.iface {
			value = &ast.TypeAssertExpr{X: subject(), Type: arm.typeExpr}
		} else {
			value = ast.NewIdent(switchedName)
			bindsSwitched = true
		}
		cases = append(cases, &ast.CaseClause{List: []ast.Expr{arm.typeExpr}, Body: []ast.Stmt{
			&ast.AssignStmt{
				Lhs: []ast.Expr{ast.NewIdent(values[k]), ast.NewIdent(armName)},
				Tok: token.ASSIGN,
				Rhs: []ast.Expr{value, intLit(k + 1)},
			},
		}})
		// The chain's As call becomes the switch's answer for this arm.
		arm.define.Rhs = []ast.Expr{
			ast.NewIdent(values[k]),
			&ast.BinaryExpr{X: ast.NewIdent(armName), Op: token.EQL, Y: intLit(k + 1)},
		}
	}

	var assign ast.Stmt = &ast.ExprStmt{X: &ast.TypeAssertExpr{X: subject()}}
	if bindsSwitched {
		assign = &ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent(switchedName)}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.TypeAssertExpr{X: subject()}}}
	}
	return append(stmts, &ast.TypeSwitchStmt{Assign: assign, Body: &ast.BlockStmt{List: cases}})
}
