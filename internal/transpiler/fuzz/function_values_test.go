package fuzz_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// FuzzFunctionValues generates programs whose values are functions — of
// random, nested function types, including the bare `func()` — and checks
// that every one is accepted and yields Go that type-checks.
//
// The other generators build values of a handful of fixed types (int,
// Option[int], a small domain), so the transpiler's own types are only ever
// spelled back into Go for those. Here a value of any generated type flows
// through every construct that has to write its type out — an if-expression,
// a match, a block branch, a lambda with and without declared types, a
// generic call, Some/None, a top-level function used as a value — and is
// then used: called with generated arguments, unwrapped, printed. A type the
// transpiler spells wrongly, or builds a malformed Go node for, fails the
// type-check or panics the printer.
//
// Every generated program is valid GALA, so a rejection is a failure too.
func FuzzFunctionValues(f *testing.F) {
	rng := rand.New(rand.NewSource(3))
	for range functionValueSeeds {
		seed := make([]byte, 128)
		rng.Read(seed)
		f.Add(seed)
	}
	warmUp(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		src := generateFunctionValueProgram(data)
		res := transpile(t, src)
		if checkOutcome(t, src, res) == nil {
			t.Fatalf("the transpiler rejected a valid program: %v\ninput:\n%s", res.Err, src)
		}
		requireTypeChecks(t, src, res.Go)
	})
}

// functionValueSeeds is how many generated programs the seed pass replays.
const functionValueSeeds = 150

// fkind is the shape of a generated type.
type fkind int

const (
	fInt fkind = iota
	fString
	fBool
	fFunc
	fOpt
)

// ftype is a generated type. A function's nil result is no result.
type ftype struct {
	kind   fkind
	params []*ftype // fFunc
	result *ftype   // fFunc; nil: returns nothing
	elem   *ftype   // fOpt
}

var intType = &ftype{kind: fInt}

func (t *ftype) String() string {
	switch t.kind {
	case fString:
		return "string"
	case fBool:
		return "bool"
	case fOpt:
		return "Option[" + t.elem.String() + "]"
	case fFunc:
		ps := make([]string, len(t.params))
		for i, p := range t.params {
			ps[i] = p.String()
		}
		s := "func(" + strings.Join(ps, ", ") + ")"
		if t.result != nil {
			s += " " + t.result.String()
		}
		return s
	}
	return "int"
}

// hasFunc reports whether t is or contains a function type.
func (t *ftype) hasFunc() bool {
	switch t.kind {
	case fFunc:
		return true
	case fOpt:
		return t.elem.hasFunc()
	}
	return false
}

// fnVar is a name in scope and its type.
type fnVar struct {
	name string
	typ  *ftype
}

// fnGen turns fuzz bytes into a program. Each choice consumes one byte; once
// the input runs out every choice is the first, simplest option, so
// generation always terminates.
type fnGen struct {
	data  []byte
	pos   int
	scope []fnVar
	next  int
}

func (g *fnGen) pick(n int) int {
	if g.pos >= len(g.data) {
		return 0
	}
	c := int(g.data[g.pos]) % n
	g.pos++
	return c
}

func (g *fnGen) name(prefix string) string {
	g.next++
	return fmt.Sprintf("%s%d", prefix, g.next)
}

// scoped runs gen with the scope restored afterwards.
func (g *fnGen) scoped(gen func() string) string {
	saved := len(g.scope)
	defer func() { g.scope = g.scope[:saved] }()
	return gen()
}

// declare puts a fresh name of type t in scope.
func (g *fnGen) declare(prefix string, t *ftype) fnVar {
	v := fnVar{g.name(prefix), t}
	g.scope = append(g.scope, v)
	return v
}

// where lists the names in scope whose type satisfies ok.
func (g *fnGen) where(ok func(*ftype) bool) []fnVar {
	var out []fnVar
	for _, v := range g.scope {
		if ok(v.typ) {
			out = append(out, v)
		}
	}
	return out
}

func (g *fnGen) ofType(t *ftype) []fnVar {
	want := t.String()
	return g.where(func(x *ftype) bool { return x.String() == want })
}

func (g *fnGen) returning(t *ftype) []fnVar {
	want := t.String()
	return g.where(func(x *ftype) bool { return x.kind == fFunc && x.result != nil && x.result.String() == want })
}

func (g *fnGen) unitFuncs() []fnVar {
	return g.where(func(x *ftype) bool { return x.kind == fFunc && x.result == nil })
}

func indent(n int) string { return strings.Repeat("    ", n) }

// braced is head followed by a `{ ... }` block of lines at indentation ind.
func braced(head string, ind int, lines ...string) string {
	var b strings.Builder
	b.WriteString(head + "{\n")
	for _, l := range lines {
		b.WriteString(indent(ind+1) + l + "\n")
	}
	b.WriteString(indent(ind) + "}")
	return b.String()
}

// genType is a type nested at most depth deep. Function types are the
// point, so they come up most often.
func (g *fnGen) genType(depth int) *ftype {
	if depth == 0 {
		return &ftype{kind: []fkind{fInt, fString, fBool}[g.pick(3)]}
	}
	switch g.pick(6) {
	case 0, 1, 2:
		t := &ftype{kind: fFunc}
		for range g.pick(3) {
			t.params = append(t.params, g.genType(depth-1))
		}
		if g.pick(3) != 0 {
			t.result = g.genType(depth - 1)
		}
		return t
	case 3:
		return &ftype{kind: fOpt, elem: g.genType(depth - 1)}
	}
	return g.genType(0)
}

func (g *fnGen) intExpr() string {
	ints := g.ofType(intType)
	k := g.pick(9)
	if len(ints) > 0 && g.pick(2) == 0 {
		return fmt.Sprintf("%s + %d", ints[g.pick(len(ints))].name, k)
	}
	return fmt.Sprint(k)
}

func (g *fnGen) cond() string {
	ops := []string{">", "==", "<"}
	return fmt.Sprintf("n %s %d", ops[g.pick(len(ops))], g.pick(5))
}

// call is a call of f with generated arguments. A lambda argument is now and
// then written without parameter types, which the call's signature gives.
func (g *fnGen) call(f fnVar, ind, depth int) string {
	args := make([]string, len(f.typ.params))
	for i, p := range f.typ.params {
		if p.kind == fFunc && g.pick(3) == 0 {
			args[i] = g.lambda(p, ind, depth, false)
		} else {
			args[i] = g.expr(p, ind, depth)
		}
	}
	return f.name + "(" + strings.Join(args, ", ") + ")"
}

// expr is an expression of type t at indentation ind; depth bounds its
// nesting.
func (g *fnGen) expr(t *ftype, ind, depth int) string {
	if depth > 0 {
		switch g.pick(10) {
		case 0:
			if vs := g.ofType(t); len(vs) > 0 {
				return vs[g.pick(len(vs))].name
			}
		case 1:
			return fmt.Sprintf("if (%s) %s else %s", g.cond(), g.expr(t, ind, depth-1), g.expr(t, ind, depth-1))
		case 2:
			return fmt.Sprintf("if (%s) %s else %s", g.cond(), g.block(t, ind, depth-1), g.block(t, ind, depth-1))
		case 3:
			return braced("n match ", ind,
				"case 1 => "+g.expr(t, ind+1, depth-1),
				"case _ => "+g.expr(t, ind+1, depth-1))
		case 4:
			if fs := g.returning(t); len(fs) > 0 {
				return g.call(fs[g.pick(len(fs))], ind, depth-1)
			}
		case 5:
			return "id(" + g.expr(t, ind, depth-1) + ")"
		case 6:
			return fmt.Sprintf("(%s).GetOrElse(%s)", g.expr(&ftype{kind: fOpt, elem: t}, ind, depth-1), g.expr(t, ind, depth-1))
		}
	}
	return g.literal(t, ind, depth)
}

// block is a `{ ... }` block whose value is of type t.
func (g *fnGen) block(t *ftype, ind, depth int) string {
	return g.scoped(func() string {
		var lines []string
		for range g.pick(3) {
			lines = append(lines, g.local(ind+1, depth))
		}
		lines = append(lines, g.expr(t, ind+1, depth))
		return braced("", ind, lines...)
	})
}

// local declares a val in a block and puts it in scope.
func (g *fnGen) local(ind, depth int) string {
	t := intType
	if depth > 0 {
		t = g.genType(g.pick(2) + 1)
	}
	value := g.expr(t, ind, depth)
	return fmt.Sprintf("val %s = %s", g.declare("v", t).name, value)
}

// literal is a value of type t built directly: a constant, Some/None, or a
// lambda.
func (g *fnGen) literal(t *ftype, ind, depth int) string {
	switch t.kind {
	case fString:
		if g.pick(2) == 0 {
			return `s"n=$n"`
		}
		return `"s"`
	case fBool:
		return g.cond()
	case fOpt:
		// Explicit type arguments in an expression are expressions, so a
		// function type cannot be spelled there: None[func()]() does not
		// parse.
		if g.pick(3) == 0 && !t.elem.hasFunc() {
			return "None[" + t.elem.String() + "]()"
		}
		return "Some(" + g.expr(t.elem, ind, depth) + ")"
	case fFunc:
		// A lambda's body runs to the end of the expression, so one inside
		// a larger expression is parenthesized, as in an if-expression's
		// branches.
		return "(" + g.lambda(t, ind, depth, true) + ")"
	}
	return g.intExpr()
}

// lambda is a lambda of function type t. With typed unset its parameters are
// written without types, for a position whose expected type gives them.
func (g *fnGen) lambda(t *ftype, ind, depth int, typed bool) string {
	return g.scoped(func() string {
		params := make([]fnVar, len(t.params))
		ps := make([]string, len(t.params))
		for i, p := range t.params {
			params[i] = g.declare("a", p)
			ps[i] = params[i].name
			if typed {
				ps[i] += " " + p.String()
			}
		}
		head := "(" + strings.Join(ps, ", ") + ")"
		if typed && t.result != nil && g.pick(3) == 0 {
			head += " " + t.result.String()
		}
		if t.result == nil {
			switch g.pick(4) {
			case 1:
				return head + " => Println(" + g.intExpr() + ")"
			case 2:
				var uses []string
				for _, v := range params {
					uses = append(uses, g.use(v, ind+1, depth))
				}
				return braced(head+" => ", ind, uses...)
			case 3:
				if fs := g.unitFuncs(); len(fs) > 0 && depth > 0 {
					return head + " => " + g.call(fs[g.pick(len(fs))], ind, depth-1)
				}
			}
			return head + " => {}"
		}
		if depth > 0 && g.pick(3) == 0 {
			return braced(head+" => ", ind,
				g.local(ind+1, depth-1),
				"return "+g.expr(t.result, ind+1, depth-1))
		}
		return head + " => " + g.expr(t.result, ind, depth-1)
	})
}

// use is a statement that uses v: prints it, calls it, or unwraps it.
func (g *fnGen) use(v fnVar, ind, depth int) string {
	switch v.typ.kind {
	case fFunc:
		c := g.call(v, ind, depth)
		if v.typ.result == nil {
			return c
		}
		return g.scoped(func() string {
			r := g.declare("r", v.typ.result)
			return fmt.Sprintf("val %s = %s\n%s%s", r.name, c, indent(ind), g.use(r, ind, depth))
		})
	case fOpt:
		return g.scoped(func() string {
			x := g.declare("x", v.typ.elem)
			return braced(v.name+".ForEach(("+x.name+") => ", ind, g.use(x, ind+1, depth)) + ")"
		})
	}
	return "Println(" + v.name + ")"
}

// helper declares a top-level function of a generated signature. It is
// then in scope everywhere, so it is also passed and returned as a value.
// Its first parameter is an int n, like run's, so its body can branch. The
// caller resets the scope around it.
func (g *fnGen) helper() (string, fnVar) {
	t := g.genType(2)
	if t.kind != fFunc {
		t = &ftype{kind: fFunc, params: []*ftype{t}}
	}
	t.params = append([]*ftype{intType}, t.params...)
	h := fnVar{g.name("h"), t}
	g.scope = append(g.scope, fnVar{"n", intType})
	ps := []string{"n int"}
	params := make([]fnVar, 0, len(t.params)-1)
	for _, p := range t.params[1:] {
		v := g.declare("p", p)
		params = append(params, v)
		ps = append(ps, v.name+" "+p.String())
	}
	head := "func " + h.name + "(" + strings.Join(ps, ", ") + ")"
	if t.result != nil {
		return head + " " + t.result.String() + " " + g.block(t.result, 0, 2), h
	}
	var uses []string
	for _, v := range params {
		uses = append(uses, g.use(v, 1, 1))
	}
	return braced(head+" ", 0, uses...), h
}

func generateFunctionValueProgram(data []byte) string {
	g := &fnGen{data: data}
	var b strings.Builder
	b.WriteString("package main\n\n")
	b.WriteString("func id[T any](x T) T = x\n\n")
	var helpers []fnVar
	for range g.pick(3) {
		g.scope = append([]fnVar(nil), helpers...)
		src, h := g.helper()
		b.WriteString(src + "\n\n")
		helpers = append(helpers, h)
	}
	g.scope = append([]fnVar{{"n", intType}}, helpers...)
	b.WriteString("func run(n int) {\n")
	for range g.pick(3) + 1 {
		t := g.genType(2)
		value := g.expr(t, 1, 3)
		v := g.declare("v", t)
		fmt.Fprintf(&b, "    val %s = %s\n", v.name, value)
		b.WriteString("    " + g.use(v, 1, 2) + "\n")
	}
	b.WriteString("}\n\nfunc main() {\n    run(0)\n    run(1)\n    run(3)\n}\n")
	return b.String()
}
