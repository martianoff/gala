package gala2lean

import (
	"fmt"
	"strings"

	"martianoff/gala/internal/formal/coreexport"
)

// scope is the locals visible at a point. level counts the term-level `do`
// blocks around it: Lean's do-notation can assign a `let mut` and `return`
// from the function only at the level they belong to.
type scope struct {
	parent *scope
	vars   map[string]*local
	level  int
}

type local struct {
	mutable bool
	level   int
	// typ is its Lean type, "" when not known.
	typ string
}

func newScope(parent *scope, level int) *scope {
	return &scope{parent: parent, vars: map[string]*local{}, level: level}
}

// child is a nested scope at the same level (a branch or block that is a
// do-item of the enclosing do block).
func (s *scope) child() *scope { return newScope(s, s.level) }

// term is a nested scope inside a term-level `do` block.
func (s *scope) term() *scope { return newScope(s, s.level+1) }

func (s *scope) bind(x string, mutable bool, typ string) {
	s.vars[x] = &local{mutable: mutable, level: s.level, typ: typ}
}

func (s *scope) lookup(x string) *local {
	for c := s; c != nil; c = c.parent {
		if l, ok := c.vars[x]; ok {
			return l
		}
	}
	return nil
}

// indentLines renders do-items as lines at indentation n; an item's own
// continuation lines keep their indentation relative to it.
func indentLines(items []string, n int) string {
	pad := strings.Repeat(" ", n)
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = pad + strings.ReplaceAll(it, "\n", "\n"+pad)
	}
	return strings.Join(out, "\n")
}

// doTerm renders do-items as a term, `(do ...)`.
func doTerm(items []string) string {
	return "(do\n" + indentLines(items, 2) + ")"
}

func isExpr(n coreexport.Node) bool {
	switch n.(type) {
	case *coreexport.Val, *coreexport.Var, *coreexport.Func, *coreexport.Struct, *coreexport.If,
		*coreexport.Return, *coreexport.Assign, *coreexport.IncDec:
		return false
	}
	return true
}

// stmts translates a block's statements into do-items. With value, the last
// statement is an expression whose value the items end with; without, the
// items end with `pure ()`.
func (g *gen) stmts(list []coreexport.Node, sc *scope, value bool) []string {
	var items []string
	for i, st := range list {
		items = append(items, g.stmt(st, sc, value && i == len(list)-1)...)
	}
	if !value && !terminates(list) {
		items = append(items, "pure ()")
	}
	return items
}

// valueItems are do-items that end with the value of expression e, which
// may be a block, if or match whose arms are themselves do-items.
func (g *gen) valueItems(e coreexport.Node, sc *scope) []string {
	switch e := e.(type) {
	case *coreexport.Block:
		return g.stmts(e.Stmts, sc.child(), true)
	case *coreexport.Paren:
		return g.valueItems(e.Expr, sc)
	}
	return g.stmt(e, sc, true)
}

// unitItems are do-items that run e, a statement or expression, and end with
// `pure ()`.
func (g *gen) unitItems(e coreexport.Node, sc *scope) []string {
	if b, ok := e.(*coreexport.Block); ok {
		return g.stmts(b.Stmts, sc.child(), false)
	}
	items := g.stmt(e, sc, false)
	if terminates([]coreexport.Node{e}) {
		return items
	}
	return append(items, "pure ()")
}

// stmt translates one statement. With value it is the last statement of a
// sequence whose value is needed.
func (g *gen) stmt(st coreexport.Node, sc *scope, value bool) []string {
	switch st := st.(type) {
	case *coreexport.Val:
		return g.local(st.Names, st.Destructure, st.Type, st.Values, false, st.Pos, sc)
	case *coreexport.Var:
		return g.local(st.Names, st.Destructure, st.Type, st.Values, true, st.Pos, sc)
	case *coreexport.Assign:
		return g.assign(st, sc)
	case *coreexport.IncDec:
		op := map[string]string{"++": "+", "--": "-"}[st.Op]
		return g.assignTo(st.Target, func(cur string) string { return fmt.Sprintf("(%s %s 1)", cur, op) }, sc, st.Pos)
	case *coreexport.If:
		return []string{g.ifStmt(st, sc)}
	case *coreexport.Return:
		if sc.level != 0 {
			unsup(st.Pos, "return-inside-expression")
		}
		if st.Value == nil {
			return []string{"return ()"}
		}
		return []string{"return " + g.expr(st.Value, sc).val()}
	case *coreexport.Func:
		unsup(st.Pos, "local-func")
	case *coreexport.Struct:
		unsup(st.Pos, "local-struct")
	case *coreexport.Unsupported:
		unsup(st.Pos, "%s", st.What)
	case *coreexport.Match:
		return g.matchItems(st, sc, value)
	case *coreexport.IfExpr:
		c := g.expr(st.Cond, sc)
		var th, el []string
		if value {
			th, el = g.valueItems(st.Then, sc.child()), g.valueItems(st.Else, sc.child())
		} else {
			th, el = g.unitItems(st.Then, sc.child()), g.unitItems(st.Else, sc.child())
		}
		return []string{"if " + c.val() + " then\n" + indentLines(th, 2) + "\nelse\n" + indentLines(el, 2)}
	case *coreexport.Block:
		return []string{"do\n" + indentLines(g.stmts(st.Stmts, sc.child(), value), 2)}
	case *coreexport.Paren:
		return g.stmt(st.Expr, sc, value)
	}
	t := g.expr(st, sc)
	switch {
	case value:
		return []string{t.asItem()}
	case t.pure:
		return nil // a value without effects, unused
	case t.unit:
		return []string{t.asItem()}
	}
	return []string{"discard " + t.code}
}

// terminates reports whether running list always ends in a `return`, so that
// nothing may follow it in Lean (which rejects dead code).
func terminates(list []coreexport.Node) bool {
	if len(list) == 0 {
		return false
	}
	switch s := list[len(list)-1].(type) {
	case *coreexport.Return:
		return true
	case *coreexport.Block:
		return terminates(s.Stmts)
	case *coreexport.If:
		return s.Else != nil && terminates([]coreexport.Node{s.Then}) && terminates([]coreexport.Node{s.Else})
	}
	return false
}

// local translates a local val or var.
func (g *gen) local(names []string, destructure bool, typ coreexport.Node, values []coreexport.Node, mutable bool, pos coreexport.Pos, sc *scope) []string {
	kw := "let "
	if mutable {
		kw = "let mut "
	}
	if destructure {
		if len(values) != 1 {
			unsup(pos, "destructure-count")
		}
		if len(names) > 4 {
			unsup(pos, "tuple-arity-%d", len(names))
		}
		v := g.expr(values[0], sc)
		ps := make([]string, len(names))
		for i, n := range names {
			ps[i] = ident(n)
			if n == "_" {
				ps[i] = "_"
			}
		}
		for _, n := range names {
			if n != "_" {
				sc.bind(n, mutable, "")
			}
		}
		return []string{fmt.Sprintf("%s(T%d.mk %s) := %s", kw, len(names), strings.Join(ps, " "), v.val())}
	}
	if len(values) != 0 && len(values) != len(names) {
		unsup(pos, "declaration-count-mismatch")
	}
	ann := ""
	if typ != nil {
		ann = " : " + g.leanType(typ)
	}
	// Go evaluates every right-hand side before binding any name.
	vals := make([]string, len(names))
	for i := range names {
		if len(values) == 0 {
			if typ == nil {
				unsup(pos, "untyped-declaration")
			}
			vals[i] = g.zero(typ)
		} else {
			vals[i] = g.expr(values[i], sc).val()
		}
	}
	var items []string
	if len(names) > 1 {
		tmps := make([]string, len(names))
		for i := range names {
			tmps[i] = g.freshName("v")
			items = append(items, fmt.Sprintf("let %s%s := %s", tmps[i], ann, vals[i]))
		}
		vals = tmps
	}
	for i, n := range names {
		if n == "_" {
			if len(names) == 1 {
				items = append(items, fmt.Sprintf("let _%s := %s", ann, vals[i]))
			}
			continue
		}
		items = append(items, fmt.Sprintf("%s%s%s := %s", kw, ident(n), ann, vals[i]))
	}
	for i, n := range names {
		if n == "_" {
			continue
		}
		t := strings.TrimPrefix(ann, " : ")
		if t == "" && len(values) > 0 {
			t = g.exprType(values[i], sc, map[string]bool{})
		}
		sc.bind(n, mutable, t)
	}
	return items
}

func (g *gen) ifStmt(st *coreexport.If, sc *scope) string {
	c := g.expr(st.Cond, sc)
	then := g.unitItems(st.Then, sc.child())
	s := "if " + c.val() + " then\n" + indentLines(then, 2)
	switch el := st.Else.(type) {
	case nil:
	case *coreexport.If:
		s += "\nelse\n" + indentLines([]string{g.ifStmt(el, sc.child())}, 2)
	default:
		s += "\nelse\n" + indentLines(g.unitItems(el, sc.child()), 2)
	}
	return s
}

func (g *gen) assign(st *coreexport.Assign, sc *scope) []string {
	if len(st.Targets) == 1 && len(st.Values) == 1 {
		v := g.expr(st.Values[0], sc)
		// The right-hand side runs first, so that the stored variable is
		// read after any change it makes to it.
		var items []string
		if !v.pure {
			tmp := g.freshName("r")
			items = append(items, "let "+tmp+" := "+v.val())
			v = term{code: tmp, pure: true}
		}
		return append(items, g.assignTo(st.Targets[0], func(cur string) string {
			switch st.Op {
			case "=":
				return v.val()
			case "+=":
				return fmt.Sprintf("(gadd %s %s)", cur, v.val())
			case "-=", "*=":
				return fmt.Sprintf("(%s %s %s)", cur, st.Op[:1], v.val())
			case "/=":
				return fmt.Sprintf("(← gdiv %s %s)", cur, v.val())
			}
			unsup(st.Pos, "assign-%s", st.Op)
			return ""
		}, sc, st.Pos)...)
	}
	if st.Op != "=" || len(st.Targets) != len(st.Values) {
		unsup(st.Pos, "multi-assign")
	}
	// Go: every right-hand side first, then the stores.
	var items []string
	tmps := make([]string, len(st.Values))
	for i, v := range st.Values {
		tmps[i] = g.freshName("t")
		items = append(items, "let "+tmps[i]+" := "+g.expr(v, sc).val())
	}
	for i, tg := range st.Targets {
		tmp := tmps[i]
		items = append(items, g.assignTo(tg, func(string) string { return tmp }, sc, st.Pos)...)
	}
	return items
}

// assignTo stores f(current value) into target t: a local var, a
// package-level variable, or a field of one (at any depth).
func (g *gen) assignTo(t coreexport.Node, f func(cur string) string, sc *scope, pos coreexport.Pos) []string {
	// The path of fields from the variable to the target.
	var path []string
	root := t
	for {
		if p, ok := root.(*coreexport.Paren); ok {
			root = p.Expr
			continue
		}
		s, ok := root.(*coreexport.Select)
		if !ok {
			break
		}
		path = append([]string{s.Name}, path...)
		root = s.Target
	}
	n, ok := root.(*coreexport.Name)
	if !ok {
		unsup(pos, "assign-target-%s", root.Kind())
	}
	// update rebuilds cur with the field path set to f's value.
	var update func(cur string, path []string) string
	update = func(cur string, path []string) string {
		if len(path) == 0 {
			return f(cur)
		}
		fld := ident(path[0])
		return fmt.Sprintf("{ %s with %s := %s }", cur, fld, update("("+cur+")."+fld, path[1:]))
	}
	if l := sc.lookup(n.Name); l != nil {
		if !l.mutable {
			unsup(pos, "assign-to-val")
		}
		if l.level != sc.level {
			unsup(pos, "var-mutated-inside-expression")
		}
		x := ident(n.Name)
		return []string{x + " := " + update(x, path)}
	}
	if gv := g.globalBy[n.Name]; gv != nil {
		tmp := g.freshName("g")
		x := ident(n.Name)
		return []string{
			"let " + tmp + " := " + update("(← get)."+x, path),
			fmt.Sprintf("modify fun _gala_st => { _gala_st with %s := %s }", x, tmp),
		}
	}
	unsup(pos, "assign-target")
	return nil
}
