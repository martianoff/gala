package gala2lean

import (
	"fmt"
	"math/big"
	"strings"
	"unicode"
	"unicode/utf8"

	"martianoff/gala/internal/formal/coreexport"
)

// term is a translated expression: a plain Lean value (pure) or an action in
// the program's monad.
type term struct {
	code string
	pure bool
	// unit is true for an action whose result is Unit (a print, a call of a
	// function without a result), which a statement runs without discard.
	unit bool
	// item, when set, is the action as one do-item, for use directly in a
	// do block instead of the `(do item)` term in code.
	item string
}

// act is the action of do-item x.
func act(x string) term { return term{code: doTerm([]string{x}), item: x} }

// actUnit is the action of do-item x, with unit as its result is Unit.
func actUnit(x string, unit bool) term {
	t := act(x)
	t.unit = unit
	return t
}

// asItem is the action t as a do-item.
func (t term) asItem() string {
	if t.item != "" {
		return t.item
	}
	return t.mon()
}

// val is the term's value inside a do block: the action lifted with `←`.
func (t term) val() string {
	if t.pure {
		return t.code
	}
	return "(← " + t.code + ")"
}

// mon is the term as an action.
func (t term) mon() string {
	if t.pure {
		return "(pure " + t.code + ")"
	}
	return t.code
}

func allPure(ts ...term) bool {
	for _, t := range ts {
		if !t.pure {
			return false
		}
	}
	return true
}

// wrap builds a term from a Lean expression over the values of subterms: a
// plain value if they all are, else an action whose lifts run left to right.
func wrap(expr string, subs ...term) term {
	if allPure(subs...) {
		return term{code: expr, pure: true}
	}
	return act("pure " + expr)
}

func posOf(n coreexport.Node) coreexport.Pos {
	switch n := n.(type) {
	case *coreexport.Unsupported:
		return n.Pos
	}
	return metaPos(n)
}

// intLit is an int literal as an Int64. A constant outside int64 does not
// compile in Go, except 2^63 under a unary minus, which wraps to the same
// value Go gives -2^63.
func intLit(n *coreexport.Int) string {
	t := strings.ReplaceAll(strings.ToLower(n.Text), "_", "")
	v, ok := new(big.Int).SetString(t, 0)
	if !ok {
		unsup(n.Pos, "int-literal")
	}
	limit := new(big.Int).Lsh(big.NewInt(1), 63)
	if v.Cmp(limit) > 0 {
		unsup(n.Pos, "int-constant-overflow")
	}
	return "(" + v.String() + " : Int64)"
}

// strLit is a Lean string literal for s, which must be valid UTF-8 (a Lean
// String is).
func strLit(s string, pos coreexport.Pos) string {
	if !utf8.ValidString(s) {
		unsup(pos, "invalid-utf8-string")
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func (g *gen) expr(e coreexport.Node, sc *scope) term {
	switch e := e.(type) {
	case *coreexport.Unsupported:
		unsup(e.Pos, "%s", e.What)
	case *coreexport.Int:
		return term{code: intLit(e), pure: true}
	case *coreexport.Bool:
		if e.Value {
			return term{code: "true", pure: true}
		}
		return term{code: "false", pure: true}
	case *coreexport.String:
		return term{code: strLit(e.Value, e.Pos), pure: true}
	case *coreexport.Paren:
		return g.expr(e.Expr, sc)
	case *coreexport.Name:
		return g.name(e, sc)
	case *coreexport.Unary:
		o := g.expr(e.Operand, sc)
		switch e.Op {
		case "-":
			return wrap("(-"+o.val()+")", o)
		case "+":
			return o
		case "!":
			return wrap("(!"+o.val()+")", o)
		case "^":
			return wrap("(~~~"+o.val()+")", o)
		}
		unsup(e.Pos, "unary-%s", e.Op)
	case *coreexport.Binary:
		return g.binary(e, sc)
	case *coreexport.Tuple:
		if len(e.Elems) > 4 {
			unsup(e.Pos, "tuple-arity-%d", len(e.Elems))
		}
		ts := make([]term, len(e.Elems))
		parts := make([]string, len(e.Elems))
		for i, el := range e.Elems {
			ts[i] = g.expr(el, sc)
			parts[i] = ts[i].val()
		}
		return wrap(fmt.Sprintf("(T%d.mk %s)", len(e.Elems), strings.Join(parts, " ")), ts...)
	case *coreexport.Select:
		if g.variadicMethodNamed(e.Name) && g.isVariadicMethod(e.Target, e.Name, sc) {
			// A call of the method value would pass its arguments one by one.
			unsup(e.Pos, "variadic-func-value")
		}
		t := g.expr(e.Target, sc)
		return wrap("("+t.val()+")."+ident(e.Name), t)
	case *coreexport.Call:
		return g.call(e, sc)
	case *coreexport.IfExpr:
		c := g.expr(e.Cond, sc)
		th := g.branch(e.Then, sc)
		el := g.branch(e.Else, sc)
		if allPure(c, th, el) {
			return term{code: fmt.Sprintf("(if %s then %s else %s)", c.code, th.code, el.code), pure: true}
		}
		return act(fmt.Sprintf("if %s then %s else %s", c.val(), th.mon(), el.mon()))
	case *coreexport.Block:
		inner := sc.term()
		return term{code: doTerm(g.stmts(e.Stmts, inner.child(), true))}
	case *coreexport.Match:
		return term{code: doTerm(g.matchItems(e, sc.term(), true))}
	case *coreexport.Lambda:
		return g.lambda(e, sc, nil)
	case *coreexport.Interp:
		return g.interp(e, sc)
	case *coreexport.NamedArg:
		unsup(e.Pos, "named-arg")
	case *coreexport.Spread:
		// A spread into a declared variadic function, ArrayOf or ListOf is
		// translated there (variadic.go).
		unsup(e.Pos, "spread-arg")
	}
	unsup(posOf(e), "%s", e.Kind())
	return term{}
}

// branch is one branch of an if expression, a block or an expression.
func (g *gen) branch(e coreexport.Node, sc *scope) term {
	if b, ok := e.(*coreexport.Block); ok {
		return term{code: doTerm(g.stmts(b.Stmts, sc.term(), true))}
	}
	return g.expr(e, sc)
}

// name is an identifier used as a value.
func (g *gen) name(e *coreexport.Name, sc *scope) term {
	x := e.Name
	if l := sc.lookup(x); l != nil {
		if l.slice {
			// A variadic parameter is only spread or sized (variadic.go).
			unsup(e.Pos, "slice-value")
		}
		if l.box >= 0 {
			return term{code: fmt.Sprintf("(_gala_get%d %s)", l.box, ident(x))}
		}
		return term{code: ident(x), pure: true}
	}
	if g.globalBy[x] != nil {
		g.use(x)
		return term{code: fmt.Sprintf("((·.%s) <$> get)", stateField(x))}
	}
	if v := g.variants[x]; v != nil && len(v.fields) == 0 {
		g.use(v.sealed.Name)
		return term{code: ident(v.sealed.Name) + "." + ident(v.v.Name), pure: true}
	}
	if f := g.funcs[x]; f != nil {
		g.use(x)
		if len(f.TypeParams) > 0 {
			// Its type arguments would be left to Lean, from the context.
			unsup(e.Pos, "generic-func-value")
		}
		if hasInout(f.Params) {
			// Its values are of type func(inout T), outside the subset.
			unsup(e.Pos, "inout-func-value")
		}
		if isVariadic(f.Params) {
			// A call of the value would pass its arguments one by one.
			unsup(e.Pos, "variadic-func-value")
		}
		// A top-level function as a value.
		if len(f.Params) == 0 {
			return term{code: "(fun (_ : Unit) => " + g.funcRef(x) + ")", pure: true}
		}
		return term{code: g.funcRef(x), pure: true}
	}
	if g.structs[x] != nil || g.variants[x] != nil {
		unsup(e.Pos, "func-value")
	}
	if x == "None" {
		return term{code: stdCtors[x], pure: true}
	}
	if stdCtors[x] != "" || g.isStdFunc(x) {
		unsup(e.Pos, "func-value")
	}
	if strings.HasPrefix(x, noCollPrefix) {
		// A collections name in a file without their import (hideLibNames).
		unsup(e.Pos, "unresolved-name")
	}
	g.unresolved(e.Pos, "unresolved-name")
	return term{}
}

func (g *gen) binary(e *coreexport.Binary, sc *scope) term {
	if e.Op == "==" || e.Op == "!=" {
		// A type's own Equal decides `==` (docs/GALA.MD, "Equality").
		g.comparable(e.Left, sc)
	}
	l := g.expr(e.Left, sc)
	r := g.expr(e.Right, sc)
	switch e.Op {
	case "&&", "||":
		if r.pure {
			return wrap(fmt.Sprintf("(%s %s %s)", l.val(), e.Op, r.val()), l)
		}
		// The right operand runs only when it decides the result.
		if e.Op == "&&" {
			return act(fmt.Sprintf("if %s then %s else pure false", l.val(), r.mon()))
		}
		return act(fmt.Sprintf("if %s then pure true else %s", l.val(), r.mon()))
	case "+":
		// int or string.
		return wrap(fmt.Sprintf("(gadd %s %s)", l.val(), r.val()), l, r)
	case "-", "*":
		return wrap(fmt.Sprintf("(%s %s %s)", l.val(), e.Op, r.val()), l, r)
	case "|":
		return wrap(fmt.Sprintf("(%s ||| %s)", l.val(), r.val()), l, r)
	case "^":
		return wrap(fmt.Sprintf("(%s ^^^ %s)", l.val(), r.val()), l, r)
	case "&":
		return wrap(fmt.Sprintf("(%s &&& %s)", l.val(), r.val()), l, r)
	case "&^":
		return wrap(fmt.Sprintf("(%s &&& ~~~%s)", l.val(), r.val()), l, r)
	case "==":
		return wrap(fmt.Sprintf("(%s == %s)", l.val(), r.val()), l, r)
	case "!=":
		return wrap(fmt.Sprintf("(%s != %s)", l.val(), r.val()), l, r)
	case "<", "<=", ">", ">=":
		return wrap(fmt.Sprintf("(decide (%s %s %s))", l.val(), e.Op, r.val()), l, r)
	case "/", "%", "<<", ">>":
		fn := map[string]string{"/": "gdiv", "%": "gmod", "<<": "gshl", ">>": "gshr"}[e.Op]
		return act(fmt.Sprintf("%s %s %s", fn, l.val(), r.val()))
	}
	unsup(e.Pos, "binary-%s", e.Op)
	return term{}
}

// isUpper reports whether x starts with an upper-case letter.
func isUpper(x string) bool {
	r, _ := utf8.DecodeRuneInString(x)
	return unicode.IsUpper(r)
}
