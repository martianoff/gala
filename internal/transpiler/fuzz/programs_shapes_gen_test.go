package fuzz_test

import (
	"fmt"
	"strings"
)

// programs_shapes_gen_test.go adds to FuzzPrograms' generator the statement
// shapes behind past bugs that its matches and calls could not build. Each
// shape models what it generates, as the rest of the generator does, so the
// program's output is known:
//
//   - an if-expression followed by a match, in a val, an argument and a
//     function's tail;
//   - a tail-recursive function whose self call has a match applied to it,
//     or whose if-expression has one;
//   - a val declared in an if-expression branch, a match arm or a lambda that
//     shadows an outer val of another type;
//   - statements before the value of an if-expression branch: a
//     statement-position match, a `return` in a match arm, and an unused
//     value, which must be rejected;
//   - package-level vals whose type the analyzer infers from the initializer
//     (an operand named like an exponent, a signed or prefixed literal, a
//     trailing match), read across the package boundary where an int is
//     needed;
//   - a struct type declared inside a function and built with call syntax;
//   - domain functions named like std types, called as statements and
//     values.

// shapes are the statement shapes, each adding to main.
var shapes = []func(*progGen){
	(*progGen).ifTrailingMatch,
	(*progGen).tailRecursion,
	(*progGen).branchShadow,
	(*progGen).branchStatements,
	(*progGen).packageVal,
	(*progGen).localStruct,
	(*progGen).stdShadow,
}

// canReject reports whether a generator may still emit an invalid
// statement. A program holds at most one, so the diagnostic it must be
// rejected with is required even if the transpiler stops at the first error.
func (g *progGen) canReject() bool { return g.rejection == nil }

// reject records that the program must be rejected with r. Callers check
// canReject first.
func (g *progGen) reject(r rejection) {
	if g.rejection != nil {
		panic("a second invalid statement in one program")
	}
	g.rejection = &r
}

// helper declares main's top-level function name, with source src, once.
func (g *progGen) helper(name, src string) string {
	if !g.helpers[name] {
		g.helpers[name] = true
		g.funcs = append(g.funcs, src)
	}
	return name
}

// printLine adds `Println(expr)` to main, which must print want.
func (g *progGen) printLine(expr, want string) {
	g.body = append(g.body, "Println("+expr+")")
	g.out = append(g.out, want)
}

// domainExtras is what the shapes added to the domain's declarations.
func (g *progGen) domainExtras() string {
	var b strings.Builder
	for _, v := range g.pkgVals {
		b.WriteString("\n" + v + "\n")
	}
	for _, name := range stdShadowNames {
		if src, ok := g.shadows[name]; ok {
			b.WriteString("\n" + src + "\n")
		}
	}
	return b.String()
}

// trailingMatch is the match `match { case k => 100; case z => z + 1 }`
// the trailing-match shapes apply, and its model.
func (g *progGen) trailingMatch(ind string) (src string, eval func(int) int) {
	k, z := g.pick(6), g.name("z")
	src = fmt.Sprintf("match {\n%s    case %d => 100\n%s    case %s => %s + 1\n%s}", ind, k, ind, z, z, ind)
	return src, func(v int) int {
		if v == k {
			return 100
		}
		return v + 1
	}
}

// ifTrailingMatch: an if-expression followed by a match. After a block else
// branch the match applies to the if-expression's value; after a bare call
// it applies to the call alone.
func (g *progGen) ifTrailingMatch() {
	a, b, x, y := g.pick(6), g.pick(6), g.pick(6), g.pick(6)
	m, eval := g.trailingMatch("")
	thenSrc := fmt.Sprint(x)
	if g.pick(2) == 0 {
		thenSrc = fmt.Sprintf("{ %d }", x)
	}
	var src string
	var want int
	if g.pick(3) == 0 {
		// The match applies to the else branch's call only.
		id := g.helper("ident", "func ident(n int) int = n")
		src = fmt.Sprintf("if (%d > %d) %s else %s(%d) %s", a, b, thenSrc, id, y, m)
		want = eval(y)
		if a > b {
			want = x
		}
	} else {
		src = fmt.Sprintf("if (%d > %d) %s else { %d } %s", a, b, thenSrc, y, m)
		want = eval(y)
		if a > b {
			want = eval(x)
		}
	}
	switch g.pick(3) {
	case 0:
		// A val, with or without its type written.
		name, typ := g.name("r"), []string{" int", ""}[g.pick(2)]
		g.body = append(g.body, fmt.Sprintf("val %s%s = %s", name, typ, src))
		g.print(name, want)
	case 1:
		g.print(src, want)
	case 2:
		// A function's tail: the if-expression's condition reads the
		// parameter.
		name, p := g.name("t"), g.name("n")
		g.funcs = append(g.funcs, fmt.Sprintf("func %s(%s int) int = %s",
			name, p, strings.Replace(src, fmt.Sprintf("if (%d > ", a), fmt.Sprintf("if (%s > ", p), 1)))
		g.print(fmt.Sprintf("%s(%d)", name, a), want)
	}
}

// tailRecursion: a self-recursive function whose recursion a tail-call
// rewrite may turn into a loop, only where the self call's value is the
// function's.
func (g *progGen) tailRecursion() {
	name := g.name("loop")
	n, acc, step, zero := g.pick(5), g.pick(4), 1+g.pick(3), 50+g.pick(9)
	mm := func(r int) int {
		if r == 0 {
			return zero
		}
		return r * 2
	}
	m := fmt.Sprintf("match {\n    case 0 => %d\n    case r => r * 2\n}", zero)
	call := fmt.Sprintf("%s(n - 1, acc + %d)", name, step)
	var src string
	var f func(n, acc int) int
	// matchedCall is the model of a function whose self call has the match
	// applied: not a tail call.
	matchedCall := func(n, acc int) int {
		if n <= 0 {
			return acc
		}
		return mm(f(n-1, acc+step))
	}
	switch g.pick(4) {
	case 0:
		// An expression body: the match applies to the else branch's call.
		src = fmt.Sprintf("func %s(n int, acc int) int = if (n <= 0) acc else %s %s", name, call, m)
		f = matchedCall
	case 1:
		// The match applies to the if-expression: runs on every level.
		src = fmt.Sprintf("func %s(n int, acc int) int = if (n <= 0) { acc } else { %s } %s", name, call, m)
		f = func(n, acc int) int {
			v := acc
			if n > 0 {
				v = f(n-1, acc+step)
			}
			return mm(v)
		}
	case 2:
		// A block body whose trailing self call has a match applied.
		src = fmt.Sprintf("func %s(n int, acc int) int {\n    if (n <= 0) {\n        return acc\n    }\n    %s %s\n}",
			name, call, strings.ReplaceAll(m, "\n", "\n    "))
		f = matchedCall
	case 3:
		// A tail call proper.
		src = fmt.Sprintf("func %s(n int, acc int) int = if (n <= 0) acc else %s", name, call)
		f = func(n, acc int) int {
			if n <= 0 {
				return acc
			}
			return f(n-1, acc+step)
		}
	}
	g.funcs = append(g.funcs, src)
	g.print(fmt.Sprintf("%s(%d, %d)", name, n, acc), f(n, acc))
}

// branchShadow: a string val shadowed by an int val of the same name inside
// a nested scope, then read as the string again.
func (g *progGen) branchShadow() {
	s := g.name("s")
	g.body = append(g.body, fmt.Sprintf("val %s = \"outer\"", s))
	k, j := g.pick(6), g.pick(6)
	r := g.name("r")
	var want int
	switch g.pick(4) {
	case 0, 1:
		a, b := g.pick(6), g.pick(6)
		elseSrc := fmt.Sprintf("{ %d }", j)
		want = j
		if g.pick(2) == 0 {
			// Both branches declare it.
			elseSrc = fmt.Sprintf("{\n    val %s = %d\n    %s * 2\n}", s, j, s)
			want = j * 2
		}
		if a > b {
			want = k + 1
		}
		g.body = append(g.body, fmt.Sprintf("val %s = if (%d > %d) {\n    val %s = %d\n    %s + 1\n} else %s", r, a, b, s, k, s, elseSrc))
	case 2:
		x := g.pick(3)
		g.body = append(g.body, fmt.Sprintf("val %s = %d match {\n    case 1 => {\n        val %s = %d\n        %s + 1\n    }\n    case _ => %d\n}", r, x, s, k, s, j))
		want = j
		if x == 1 {
			want = k + 1
		}
	case 3:
		f := g.name("f")
		g.body = append(g.body, fmt.Sprintf("val %s = (n int) int => {\n    val %s = n\n    %s + 1\n}", f, s, s))
		g.body = append(g.body, fmt.Sprintf("val %s = %s(%d)", r, f, k))
		want = k + 1
	}
	g.print(r, want)
	g.printLine(s+" + \"!\"", "outer!")
}

// branchStatements: statements before the value of an if-expression block
// branch, lowered as in any block.
func (g *progGen) branchStatements() {
	t, k, x := g.pick(5), g.pick(5), g.pick(6)
	switch g.pick(5) {
	case 0, 1:
		// A statement-position match whose arm returns from the function.
		name := g.name("b")
		g.funcs = append(g.funcs, fmt.Sprintf(`func %s(n int) int {
    val v = if (n > %d) {
        n match {
            case %d => { return 7 }
            case _ => Println("other")
        }
        5
    } else 2
    v
}`, name, t, k))
		want := 2
		if x > t {
			if x == k {
				want = 7
			} else {
				g.out = append(g.out, "other")
				want = 5
			}
		}
		g.print(fmt.Sprintf("%s(%d)", name, x), want)
	case 2, 3:
		// A statement-position match in main, printing in its arms.
		a, b, w, z := g.pick(6), g.pick(6), g.pick(9), g.pick(9)
		v := g.name("v")
		g.body = append(g.body, fmt.Sprintf(`val %s = if (%d > %d) {
    Println("in")
    %d match {
        case %d => Println("hit")
        case _ => Println("miss")
    }
    %d
} else { %d }`, v, a, b, x, k, w, z))
		want := z
		if a > b {
			g.out = append(g.out, "in")
			if x == k {
				g.out = append(g.out, "hit")
			} else {
				g.out = append(g.out, "miss")
			}
			want = w
		}
		g.print(v, want)
	case 4:
		if !g.canReject() {
			g.print("0", 0)
			return
		}
		// A value evaluated but not used: a GALA diagnostic, not a Go
		// error. It has no code, so its message is checked.
		n, v := g.name("n"), g.name("v")
		g.body = append(g.body, fmt.Sprintf("val %s = %d", n, x))
		g.body = append(g.body, fmt.Sprintf("val %s = if (%s > %d) {\n    %s + 1\n    5\n} else { 2 }", v, n, t, n))
		g.print(v, 0)
		g.reject(rejection{text: "is evaluated but not used"})
	}
}

// signedLiterals are package-level initializers the analyzer types from
// their text, with their values. (GALA has no digit separators.)
var signedLiterals = []struct {
	src string
	n   int
}{
	{"-0x1F", -31}, {"-0b101", -5}, {"-0o17", -15}, {"0x2a", 42}, {"-7", -7}, {"0b11", 3},
}

// packageVal: a package-level val of the domain, read where an int is
// needed, so a wrongly inferred type fails the type-check.
func (g *progGen) packageVal() {
	var name string
	var n int
	switch g.pick(3) {
	case 0:
		// An operand named like an exponent: E, EE, ...
		e := strings.Repeat("E", len(g.pkgVals)+1)
		k, j := g.pick(6), g.pick(6)
		name = g.name("F")
		g.pkgVals = append(g.pkgVals, fmt.Sprintf("val %s = %d", e, k), fmt.Sprintf("val %s = %s + %d", name, e, j))
		n = k + j
	case 1:
		lit := signedLiterals[g.pick(len(signedLiterals))]
		name = g.name("H")
		g.pkgVals = append(g.pkgVals, fmt.Sprintf("val %s = %s", name, lit.src))
		n = lit.n
	case 2:
		// A trailing match: an int, or a string read as one.
		x, k, a, b := g.pick(4), g.pick(4), g.pick(9), g.pick(9)
		name = g.name("M")
		if g.pick(2) == 0 {
			s := g.name("S")
			g.pkgVals = append(g.pkgVals, fmt.Sprintf("val %s = %d match {\n    case %d => \"hit\"\n    case _ => \"miss\"\n}", s, x, k))
			want := "miss"
			if x == k {
				want = "hit"
			}
			g.printLine(g.q+s+" + \"!\"", want+"!")
		}
		g.pkgVals = append(g.pkgVals, fmt.Sprintf("val %s = %d match {\n    case %d => %d\n    case _ => %d\n}", name, x, k, a, b))
		n = b
		if x == k {
			n = a
		}
	}
	g.print(fmt.Sprintf("%sArea(%sCircle(%s%s))", g.q, g.q, g.q, name), n*3)
}

// localStruct: a struct type declared inside a function, built with call
// syntax.
func (g *progGen) localStruct() {
	fn, typ := g.name("ls"), g.name("Inner")
	a, b := g.pick(6), g.pick(6)
	var ctor string
	switch g.pick(3) {
	case 0:
		ctor = fmt.Sprintf("%s(%d, %d)", typ, a, b)
	case 1:
		ctor = fmt.Sprintf("%s(B = %d, A = %d)", typ, b, a)
	case 2:
		// A subset of the fields: the rest take their zero value.
		ctor = fmt.Sprintf("%s(%d)", typ, a)
		b = 0
	}
	g.funcs = append(g.funcs, fmt.Sprintf("func %s() int {\n    type %s struct {\n        A int\n        B int\n    }\n    val i = %s\n    i.A * 10 + i.B\n}", fn, typ, ctor))
	g.print(fn+"()", a*10+b)
}

// stdShadowNames are std types the domain may declare a function named like.
var stdShadowNames = []string{"Void", "Ordered", "Hashable"}

// stdShadow: a domain function named like a std type, which the domain's
// name must resolve to. Not generated where the function and std's type meet
// in one scope: when main dot-imports the domain, or when std is
// dot-imported, as the domain's file (main's or the library's) then does too.
func (g *progGen) stdShadow() {
	name := stdShadowNames[g.pick(len(stdShadowNames))]
	if _, declared := g.shadows[name]; declared || g.style == dotImported || g.stdDot {
		g.print("1", 1)
		return
	}
	k := g.pick(9)
	switch name {
	case "Void":
		g.shadows[name] = "func Void() = Println(\"void\")"
		g.body = append(g.body, g.q+"Void()")
		g.out = append(g.out, "void")
	case "Ordered":
		g.shadows[name] = "func Ordered(n int) int = n * 10"
		if g.pick(2) == 0 {
			h := g.name("h")
			g.body = append(g.body, fmt.Sprintf("val %s = %sOrdered", h, g.q))
			g.print(fmt.Sprintf("%s(%d)", h, k), k*10)
		} else {
			g.print(fmt.Sprintf("%sOrdered(%d)", g.q, k), k*10)
		}
	case "Hashable":
		g.shadows[name] = "func Hashable(n int) = Println(s\"hash $n\")"
		g.body = append(g.body, fmt.Sprintf("%sHashable(%d)", g.q, k))
		g.out = append(g.out, fmt.Sprintf("hash %d", k))
	}
}
