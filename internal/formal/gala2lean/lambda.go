package gala2lean

import (
	"fmt"
	"strings"

	"martianoff/gala/internal/formal/coreexport"
)

// Lambdas and closures. A lambda is a Lean `fun` returning an action. GALA
// captures by reference (docs/FORMAL_SEMANTICS.MD, "Decisions"), so a `var`
// a lambda refers to lives in a reference cell in the program state (a
// "box"): the lambda and the code around it read and write the same cell.
// Each type of boxed variable gets a heap, an array in the state, and three
// helpers: _gala_newN allocates a cell, _gala_getN reads it, _gala_setN
// writes it. A `val` is captured by value, which is the same thing.

// boxKey names one variable a `var` declaration binds.
type boxKey struct {
	decl coreexport.Node
	name string
}

// funcTypeLean is GALA function type t in Lean: `A → B → M R`, with Unit
// for a function without parameters or without a result.
func (g *gen) funcTypeLean(t *coreexport.TypeFunc) string {
	var parts []string
	for _, p := range t.Params {
		parts = append(parts, g.leanType(p))
	}
	if len(parts) == 0 {
		parts = []string{"Unit"}
	}
	res := "Unit"
	if t.Result != nil {
		res = g.leanType(t.Result)
	}
	return "(" + strings.Join(parts, " → ") + " → _gala_M " + res + ")"
}

// funcResult is the result type R of Lean function type `(A → _gala_M R)`
// as funcTypeLean writes it, "" for any other type.
func funcResult(t string) string {
	if r := parseLean(t).result; r != nil {
		return r.String()
	}
	return ""
}

// funcDeclType is the Lean type of top-level function f as a value, "" when
// a type is outside the subset or f has inout parameters (its definition
// returns their final values too).
func (g *gen) funcDeclType(f *coreexport.Func) string {
	t := paramsType(f.Params, f.Result)
	if t == nil || len(f.TypeParams) > 0 || hasInout(f.Params) || isVariadic(f.Params) {
		return ""
	}
	return g.safeType(func() string { return g.funcTypeLean(t) })
}

// lambdaType is the Lean type of lambda l when its parameter types are
// written out, else "". A result it cannot tell is `_`: the type is then
// only good for knowing that the value is a function.
func (g *gen) lambdaType(l *coreexport.Lambda, sc *scope) string {
	t := paramsType(l.Params, l.Result)
	if t == nil {
		return ""
	}
	ls := newScope(sc, 0)
	for i, pn := range l.Params {
		ls.bind(pn.(*coreexport.Param).Name, false, g.safeType(func() string { return g.leanType(t.Params[i]) }))
	}
	ft := g.safeType(func() string { return g.funcTypeLean(t) })
	if l.Result != nil || ft == "" {
		return ft
	}
	res := "_"
	switch b := l.Body.(type) {
	case *coreexport.Block:
		if r := g.blockValueType(b, ls); r != "" {
			// Its trailing expression's value (docs/GALA.MD: a block's
			// value is its trailing expression).
			res = r
		} else if !returnsValue(b) && (len(b.Stmts) == 0 || !isValueIf(b.Stmts[len(b.Stmts)-1])) {
			// A trailing `if`/`else` with block branches carries a value;
			// its type is left to Lean.
			res = "Unit"
		}
	default:
		if r := g.typeOf(b, ls); r != "" {
			res = r
		}
	}
	return strings.TrimSuffix(ft, "Unit)") + res + ")"
}

// blockValueType is the Lean type of the value block b ends with, as far as
// it is plain to see: its trailing expression's type, with each `val` or
// `var` before it bound to its value's type (a destructuring one to the
// parts of a tuple type); "" when not plain to see. A type read wrong here
// is one Lean then rejects, never a different meaning.
func (g *gen) blockValueType(b *coreexport.Block, sc *scope) string {
	if len(b.Stmts) == 0 || !isExpr(b.Stmts[len(b.Stmts)-1]) {
		return ""
	}
	bs := newScope(sc, sc.level)
	for _, st := range b.Stmts[:len(b.Stmts)-1] {
		var names []string
		var values []coreexport.Node
		var typ coreexport.Node
		destructure := false
		switch d := st.(type) {
		case *coreexport.Val:
			names, values, destructure, typ = d.Names, d.Values, d.Destructure, d.Type
		case *coreexport.Var:
			names, values, destructure, typ = d.Names, d.Values, d.Destructure, d.Type
		default:
			continue
		}
		declared := g.optTypeSafe(typ)
		if len(values) != 1 || len(names) > 1 && !destructure {
			// `var x int`, or `val a, b = 1, "s"`: the declared type, else each
			// name's own value's.
			for i, n := range names {
				t := declared
				if t == "" && len(values) == len(names) {
					t = g.safeType(func() string { return g.typeOf(values[i], bs) })
				}
				bs.bind(n, false, t)
			}
			continue
		}
		t := declared
		if t == "" {
			t = g.safeType(func() string { return g.typeOf(values[0], bs) })
		}
		if !destructure {
			for _, n := range names {
				bs.bind(n, false, t)
			}
			continue
		}
		p := parseLean(t)
		for i, n := range names {
			et := ""
			if (p.head == "T2" || p.head == "T3" || p.head == "T4") && len(p.args) == len(names) {
				et = p.args[i].String()
			}
			bs.bind(n, false, et)
		}
	}
	return g.safeType(func() string { return g.typeOf(b.Stmts[len(b.Stmts)-1], bs) })
}

// safeType is f's type, or "" when f stops at a construct outside the
// subset (the translation reports it where it matters).
func (g *gen) safeType(f func() string) string {
	t, _ := attempt(f)
	return t
}

// exprExpect translates e where a value of type expect (nil when unknown) is
// wanted: a lambda takes its parameter and result types from it, and an
// expression passed for a `func() T` parameter becomes a thunk (a by-name
// argument).
func (g *gen) exprExpect(e coreexport.Node, sc *scope, expect coreexport.Node) term {
	g.placeholderArg(e, expect)
	ft, isFunc := expect.(*coreexport.TypeFunc)
	if l, ok := e.(*coreexport.Lambda); ok {
		if !isFunc {
			ft = nil
		}
		return g.lambda(l, sc, ft)
	}
	if isFunc && len(ft.Params) == 0 {
		t := g.typeOf(e, sc)
		if !strings.Contains(t, "→") {
			if t == "" && !g.isFuncValue(e, sc) {
				unsup(posOf(e), "by-name-arg")
			}
			if t != "" {
				// A by-name argument: run when the callee calls it.
				v := g.expr(e, sc.term())
				return term{code: "(fun (_ : Unit) => " + v.mon() + ")", pure: true}
			}
		}
	}
	return g.expr(e, sc)
}

// placeholderArg stops the translation of e, an argument for a slot of
// type expect (nil when unknown), when it reads a parameter of a
// placeholder lambda around it (placeholders.go) and the slot is not known
// to want something other than a function: the transformer would make a
// lambda of e, and the `_` would be its parameter, not the outer lambda's.
// That lambda itself is the argument it was made for.
func (g *gen) placeholderArg(e coreexport.Node, expect coreexport.Node) {
	if l, ok := e.(*coreexport.Lambda); g.inPlaceholder == 0 || ok && g.placeholders[l] {
		return
	}
	if !g.nonFuncSlot(expect) && hasPlaceholderParam(e) {
		unsup(posOf(e), "placeholder-lambda")
	}
}

// nonFuncSlot reports whether t is a type that is plainly not a function
// type: a basic type, a declared struct or sealed type, or one of std's
// generic types, none of them a type parameter in scope.
func (g *gen) nonFuncSlot(t coreexport.Node) bool {
	n, ok := t.(*coreexport.TypeName)
	if !ok || n.Package != "" {
		return false
	}
	if _, param := g.tparams[n.Name]; param {
		return false
	}
	switch n.Name {
	case "int", "int64", "string", "bool", "any":
		return true
	}
	_, std := stdGeneric[n.Name]
	return g.declaredType(n.Name) || std || hashTypes[n.Name]
}

// isFuncValue reports whether e plainly names a function value: a declared
// function.
func (g *gen) isFuncValue(e coreexport.Node, sc *scope) bool {
	n, ok := e.(*coreexport.Name)
	return ok && sc.lookup(n.Name) == nil && g.funcs[n.Name] != nil
}

// lambda translates a lambda; expect is the function type the context wants,
// or nil.
func (g *gen) lambda(l *coreexport.Lambda, sc *scope, expect *coreexport.TypeFunc) term {
	if g.placeholders[l] {
		if expect == nil || len(expect.Params) != len(l.Params) {
			// The transformer makes a lambda of `_` only for a function
			// type, of one parameter per `_` (placeholders.go).
			unsup(l.Pos, "placeholder-lambda")
		}
		// Its parameters may be read in nested call arguments only where
		// no inner lambda takes them (placeholderArg).
		g.inPlaceholder++
		defer func() { g.inPlaceholder-- }()
	}
	ls := newScope(sc, 0)
	var ps []string
	for i, pn := range l.Params {
		p, ok := pn.(*coreexport.Param)
		if !ok {
			unsupAt(pn)
		}
		if p.Default != nil {
			unsup(p.Pos, "param-default")
		}
		if p.Inout {
			unsup(p.Pos, "inout-lambda-param")
		}
		t := p.Type
		if t == nil && expect != nil && i < len(expect.Params) {
			t = expect.Params[i]
		}
		if t == nil {
			unsup(p.Pos, "untyped-lambda-param")
		}
		lt := g.leanType(t)
		name := ident(p.Name)
		if p.Name == "_" {
			name = "_"
		}
		ps = append(ps, fmt.Sprintf("(%s : %s)", name, lt))
		ls.bind(p.Name, false, lt).gtype = t
	}
	if expect != nil && len(expect.Params) != len(l.Params) {
		unsup(l.Pos, "lambda-arity")
	}
	if len(ps) == 0 {
		ps = []string{"(_ : Unit)"}
	}
	// The result: declared, wanted by the context, or, for a block body,
	// void unless it returns a value.
	res := "_"
	switch {
	case l.Result != nil:
		res = g.leanType(l.Result)
	case expect != nil && expect.Result == nil:
		res = "Unit"
	case expect != nil:
		res = g.leanType(expect.Result)
	default:
		if b, ok := l.Body.(*coreexport.Block); ok && !returnsValue(b) {
			res = "Unit"
		}
	}
	saved := g.fn
	g.fn = &fnCtx{result: res}
	var items []string
	switch b := l.Body.(type) {
	case *coreexport.Block:
		items = g.fnBlock(b, ls)
	default:
		if res == "Unit" {
			items = g.unitItems(b, ls)
		} else {
			items = g.valueItems(b, ls)
		}
	}
	g.fn = saved
	return term{code: fmt.Sprintf("(fun %s => (do\n%s : _gala_M %s))", strings.Join(ps, " "), indentLines(items, 2), res), pure: true}
}

// returnsValue reports whether block b has a `return` with a value of its
// own (not one of a nested lambda's).
func returnsValue(b *coreexport.Block) bool {
	found := false
	var visit func(n coreexport.Node)
	visit = func(n coreexport.Node) {
		switch n := n.(type) {
		case *coreexport.Lambda:
			return
		case *coreexport.Return:
			if n.Value != nil {
				found = true
			}
		}
		walkChildren(n, visit)
	}
	visit(b)
	return found
}

// heap is the index of the heap holding boxed variables of Lean type t,
// added on first use.
func (g *gen) heap(t string, pos coreexport.Pos) int {
	g.stateType(t, pos)
	for i, h := range g.heaps {
		if h == t {
			return i
		}
	}
	g.heaps = append(g.heaps, t)
	return len(g.heaps) - 1
}

// heapFields are the state fields of the heaps; heapHelpers their helpers.
func (g *gen) heapFields() string {
	var b strings.Builder
	for i, t := range g.heaps {
		fmt.Fprintf(&b, "  _gala_heap%d : Array %s := #[]\n", i, t)
	}
	return b.String()
}

func (g *gen) heapHelpers() string {
	var b strings.Builder
	for i, t := range g.heaps {
		fmt.Fprintf(&b, "/-- A new cell for a boxed variable of type %s. -/\n", t)
		fmt.Fprintf(&b, "def _gala_new%d (v : %s) : _gala_M Nat := do\n  let s ← get\n  set { s with _gala_heap%d := s._gala_heap%d.push v }\n  pure s._gala_heap%d.size\n\n", i, t, i, i, i)
		fmt.Fprintf(&b, "def _gala_get%d (r : Nat) : _gala_M %s := do\n  pure ((← get)._gala_heap%d.getD r default)\n\n", i, t, i)
		fmt.Fprintf(&b, "def _gala_set%d (r : Nat) (v : %s) : _gala_M Unit :=\n  modify fun s => { s with _gala_heap%d := s._gala_heap%d.set! r v }\n\n", i, t, i, i)
	}
	return b.String()
}

// boxDecl is a declaration a name refers to, while finding boxes.
type boxDecl struct {
	key     boxKey
	mutable bool
	depth   int // how many lambdas enclose the declaration
}

// findBoxes marks every `var` that a lambda refers to, reading or writing
// it, from inside the lambda.
func (g *gen) findBoxes(p *coreexport.Program) {
	g.boxed = map[boxKey]bool{}
	depth := 0
	var scopes []map[string]*boxDecl
	push := func() { scopes = append(scopes, map[string]*boxDecl{}) }
	pop := func() { scopes = scopes[:len(scopes)-1] }
	declare := func(decl coreexport.Node, name string, mutable bool) {
		scopes[len(scopes)-1][name] = &boxDecl{boxKey{decl, name}, mutable, depth}
	}
	use := func(name string) {
		for i := len(scopes) - 1; i >= 0; i-- {
			if d, ok := scopes[i][name]; ok {
				if d.mutable && d.depth < depth {
					g.boxed[d.key] = true
				}
				return
			}
		}
	}
	var visit func(n coreexport.Node)
	visit = func(n coreexport.Node) {
		switch n := n.(type) {
		case nil:
			return
		case *coreexport.Name:
			use(n.Name)
			return
		case *coreexport.Select:
			visit(n.Target)
			return
		case *coreexport.Block:
			push()
			for _, st := range n.Stmts {
				visit(st)
			}
			pop()
			return
		case *coreexport.Val:
			for _, v := range n.Values {
				visit(v)
			}
			for _, x := range n.Names {
				declare(n, x, false)
			}
			return
		case *coreexport.Var:
			for _, v := range n.Values {
				visit(v)
			}
			for _, x := range n.Names {
				declare(n, x, true)
			}
			return
		case *coreexport.Func:
			push()
			for _, pn := range n.Params {
				if p, ok := pn.(*coreexport.Param); ok {
					visit(p.Default)
					declare(p, p.Name, false)
				}
			}
			visit(n.Body)
			pop()
			return
		case *coreexport.Lambda:
			depth++
			push()
			for _, pn := range n.Params {
				if p, ok := pn.(*coreexport.Param); ok {
					declare(p, p.Name, false)
				}
			}
			visit(n.Body)
			pop()
			depth--
			return
		case *coreexport.Case:
			push()
			binders := map[string]bool{}
			g.patBinders(n.Pattern, binders)
			for x := range binders {
				declare(n, x, false)
			}
			visit(n.Pattern)
			visit(n.Guard)
			visit(n.Body)
			pop()
			return
		}
		walkChildren(n, visit)
	}
	push()
	for _, f := range p.Files {
		visit(f)
	}
}
