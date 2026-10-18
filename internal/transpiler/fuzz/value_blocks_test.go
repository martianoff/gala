package fuzz_test

import (
	"fmt"
	"go/importer"
	"go/token"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/gooracle"
)

// FuzzValueBlocks generates whole programs from the fuzz input and checks
// them end to end, against a stronger property than the other targets: a
// program the transpiler accepts must yield Go that type-checks, not only Go
// that parses.
//
// The programs nest the constructs whose value is a block — if-expression
// branches, match arms, lambda and partial-function bodies, a trailing
// if/else — and put `bind` chains, local declarations, `return`s and loops
// in them, in valid and invalid combinations. Bugs there tend to produce Go
// that parses but does not compile (a branch returning `nil` for an `int`, a
// FlatMap callback of the wrong type, a void call used as a value), which
// only a type-check catches.
func FuzzValueBlocks(f *testing.F) {
	// The seed pass replays a fixed sample of generated programs, so that
	// `bazel test` covers the generator's whole range on every run.
	rng := rand.New(rand.NewSource(1))
	for range valueBlockSeeds {
		seed := make([]byte, 96)
		rng.Read(seed)
		f.Add(seed)
	}
	warmUp(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		checkTypeChecks(t, generateValueBlockProgram(data))
	})
}

// valueBlockSeeds is how many generated programs the seed pass replays.
const valueBlockSeeds = 120

// checkTypeChecks transpiles src and asserts the shared properties, and that
// accepted output type-checks.
func checkTypeChecks(t *testing.T, src string) {
	t.Helper()
	res := transpile(t, src)
	if checkOutcome(t, src, res) == nil {
		return
	}
	imp := oracleImporter(t)
	if imp == nil {
		return
	}
	tc := imp.TypeCheck("generated.go", res.Go)
	if tc.Skipped != "" {
		t.Fatalf("generated Go could not be type-checked: %s\ninput:\n%s", tc.Skipped, src)
	}
	if len(tc.Errors) > 0 {
		t.Fatalf("the transpiler accepted the program, but the Go it generated does not type-check:\n  %s\ninput:\n%s\ngenerated:\n%s",
			strings.Join(tc.Errors, "\n  "), src, res.Go)
	}
}

var (
	importerOnce sync.Once
	oracleImp    *gooracle.Importer
)

// oracleImporter returns the importer that type-checks generated Go: the Go
// standard library from the SDK, GALA packages transpiled from source. It is
// nil when no Go SDK is available.
func oracleImporter(tb testing.TB) *gooracle.Importer {
	tb.Helper()
	importerOnce.Do(func() {
		if !analyzer.GoImporterAvailable() {
			return
		}
		tr := newTranspiler(tb)
		oracleImp = gooracle.NewImporter(gooracle.ImporterConfig{
			Go:         importer.ForCompiler(token.NewFileSet(), "source", nil),
			ModulePath: "martianoff/gala",
			Root:       repoRoot(tb),
			Transpile:  tr.Transpile,
		})
	})
	if oracleImp == nil {
		tb.Skip("no Go SDK: generated Go cannot be type-checked")
	}
	return oracleImp
}

// valueGen turns fuzz bytes into a GALA program. Each choice consumes one
// byte; once the input runs out every choice is the first, simplest option,
// so generation always terminates.
type valueGen struct {
	data []byte
	pos  int
	// monad: the generated functions return Option[int]; otherwise int.
	monad bool
	ints  []string // int names in scope
	vals  []string // result-typed names in scope
	funcs []string // lambdas of result type in scope, taking an int
	pfs   []string // partial functions in scope, from int to the result type
	ret   bool     // a `return` of the result type may be written (see withReturn)
	next  int
}

func generateValueBlockProgram(data []byte) string {
	g := &valueGen{data: data, ret: true}
	g.monad = g.pick(2) == 0
	result := g.resultType()
	g.ints = []string{"n"}
	var b strings.Builder
	b.WriteString("package main\n\n")
	fmt.Fprintf(&b, "func f(n int) %s %s\n\n", result, g.block(0, 3, true))
	b.WriteString("func main() {\n    Println(f(0))\n    Println(f(1))\n    Println(f(3))\n    Println(f(8))\n}\n")
	return b.String()
}

func (g *valueGen) pick(n int) int {
	if g.pos >= len(g.data) {
		return 0
	}
	c := int(g.data[g.pos]) % n
	g.pos++
	return c
}

func (g *valueGen) name(prefix string) string {
	g.next++
	return fmt.Sprintf("%s%d", prefix, g.next)
}

func (g *valueGen) resultType() string {
	if g.monad {
		return "Option[int]"
	}
	return "int"
}

func (g *valueGen) intExpr() string {
	k := g.pick(9)
	v := g.ints[g.pick(len(g.ints))]
	switch g.pick(3) {
	case 0:
		return fmt.Sprintf("%s + %d", v, k)
	case 1:
		return fmt.Sprintf("%s * %d", v, k)
	}
	return fmt.Sprint(k)
}

func (g *valueGen) cond() string {
	v := g.ints[g.pick(len(g.ints))]
	ops := []string{">", "==", "<"}
	return fmt.Sprintf("%s %s %d", v, ops[g.pick(len(ops))], g.pick(9))
}

// leaf is a value of the result type with no nested block.
func (g *valueGen) leaf() string {
	if len(g.vals) > 0 && g.pick(4) == 0 {
		return g.vals[g.pick(len(g.vals))]
	}
	if !g.monad {
		return g.intExpr()
	}
	if g.pick(3) == 0 {
		return "None()"
	}
	return "Some(" + g.intExpr() + ")"
}

// monadValue is a value of the monad to `bind` over.
func (g *valueGen) monadValue() string {
	switch g.pick(3) {
	case 0:
		return fmt.Sprintf("if (%s) Some(%s) else None()", g.cond(), g.intExpr())
	case 1:
		return "None[int]()"
	}
	return "Some(" + g.intExpr() + ")"
}

// value is an expression of the result type at indentation ind; depth
// bounds its nesting.
func (g *valueGen) value(ind, depth int) string {
	if depth == 0 {
		return g.leaf()
	}
	in := strings.Repeat("    ", ind)
	switch g.pick(8) {
	case 0:
		return fmt.Sprintf("if (%s) %s else %s", g.cond(), g.value(ind, depth-1), g.value(ind, depth-1))
	case 1:
		return fmt.Sprintf("if (%s) %s else %s", g.cond(), g.block(ind, depth-1, true), g.block(ind, depth-1, true))
	case 2:
		v := g.ints[g.pick(len(g.ints))]
		return fmt.Sprintf("%s match {\n%s    case 1 => %s\n%s    case 2 => %s\n%s    case _ => %s\n%s}",
			v, in, g.value(ind+1, depth-1), in, g.block(ind+1, depth-1, true), in, g.block(ind+1, depth-1, true), in)
	case 3:
		if len(g.funcs) > 0 {
			return fmt.Sprintf("%s(%s)", g.funcs[g.pick(len(g.funcs))], g.intExpr())
		}
	case 4:
		if len(g.pfs) > 0 {
			fallback := "0"
			if g.monad {
				fallback = "None()"
			}
			return fmt.Sprintf("%s(%s).GetOrElse(%s)", g.pfs[g.pick(len(g.pfs))], g.intExpr(), fallback)
		}
	}
	return g.leaf()
}

// block is a `{ ... }` block. When value is set its value is of the result
// type; otherwise it is the body of an if statement or a loop. It mostly
// generates valid blocks; now and then a broken one, which must be rejected
// with a diagnostic.
func (g *valueGen) block(ind, depth int, value bool) string {
	saved := [4]int{len(g.ints), len(g.vals), len(g.funcs), len(g.pfs)}
	defer func() {
		g.ints, g.vals, g.funcs, g.pfs = g.ints[:saved[0]], g.vals[:saved[1]], g.funcs[:saved[2]], g.pfs[:saved[3]]
	}()
	in := strings.Repeat("    ", ind+1)
	var b strings.Builder
	b.WriteString("{\n")
	for range g.pick(4) {
		b.WriteString(in + g.statement(ind+1, depth) + "\n")
	}
	if value {
		if tail := g.tail(ind+1, depth); tail != "" {
			b.WriteString(in + tail + "\n")
		}
	}
	b.WriteString(strings.Repeat("    ", ind) + "}")
	return b.String()
}

// withReturn generates with g.ret, whether a `return` of the result type may
// be written, set to ret: true in the function and its lambdas (which share
// its result type), false in a partial function.
func (g *valueGen) withReturn(ret bool, gen func() string) string {
	saved := g.ret
	g.ret = ret
	defer func() { g.ret = saved }()
	return gen()
}

// statement is a statement of a block, before its tail.
func (g *valueGen) statement(ind, depth int) string {
	in := strings.Repeat("    ", ind)
	switch g.pick(11) {
	case 0:
		name := g.name("v")
		s := fmt.Sprintf("val %s = %s", name, g.intExpr())
		g.ints = append(g.ints, name)
		return s
	case 1:
		return "Println(" + g.intExpr() + ")"
	case 2:
		if g.monad {
			name := g.name("b")
			s := fmt.Sprintf("bind %s = %s", name, g.monadValue())
			g.ints = append(g.ints, name)
			return s
		}
	case 3:
		if g.ret && depth > 0 {
			return fmt.Sprintf("if (%s) {\n%s    return %s\n%s}", g.cond(), in, g.value(ind+1, depth-1), in)
		}
	case 4:
		if depth > 0 {
			name, param := g.name("g"), g.name("k")
			result := " " + g.resultType()
			if g.pick(4) == 0 {
				result = "" // a lambda that declares no result type
			}
			g.ints = append(g.ints, param)
			body := g.withReturn(true, func() string { return g.block(ind, depth-1, true) })
			g.ints = g.ints[:len(g.ints)-1]
			g.funcs = append(g.funcs, name)
			return fmt.Sprintf("val %s = (%s int)%s => %s", name, param, result, body)
		}
	case 5:
		if depth > 0 {
			name := g.name("p")
			s := g.withReturn(false, func() string {
				return fmt.Sprintf("val %s = { case 1 => %s case 2 => %s }", name, g.value(ind, depth-1), g.block(ind, depth-1, true))
			})
			g.pfs = append(g.pfs, name)
			return s
		}
	case 6:
		if depth > 0 {
			name := g.name("r")
			s := fmt.Sprintf("val %s = %s", name, g.value(ind, depth))
			g.vals = append(g.vals, name)
			return s
		}
	case 7:
		if depth > 0 {
			// A statement-position if, whose body may itself be invalid.
			return fmt.Sprintf("if (%s) %s", g.cond(), g.block(ind, depth-1, false))
		}
	case 8:
		if depth > 0 {
			return fmt.Sprintf("for i := 0; i < 2; i++ %s", g.block(ind, depth-1, false))
		}
	case 9:
		if g.monad && g.pick(3) == 0 {
			// A `bind` whose annotation may not match the bound value.
			name := g.name("b")
			typ := []string{"int", "string"}[g.pick(2)]
			s := fmt.Sprintf("bind %s %s = %s", name, typ, g.monadValue())
			g.ints = append(g.ints, name)
			return s
		}
	}
	name := g.name("v")
	g.ints = append(g.ints, name)
	return fmt.Sprintf("val %s = %s", name, g.intExpr())
}

// tail is the last statement of a value block: usually its value, sometimes
// a trailing if/else chain, and now and then something with no value.
func (g *valueGen) tail(ind, depth int) string {
	switch g.pick(12) {
	case 0, 1:
		if depth > 0 {
			return fmt.Sprintf("if (%s) %s else %s", g.cond(), g.block(ind, depth-1, true), g.block(ind, depth-1, true))
		}
	case 2:
		if depth > 0 {
			return fmt.Sprintf("if (%s) %s else if (%s) %s else %s", g.cond(), g.block(ind, depth-1, true),
				g.cond(), g.block(ind, depth-1, true), g.block(ind, depth-1, true))
		}
	case 3:
		if g.ret {
			return "return " + g.value(ind, depth)
		}
	case 4:
		// No value: these must be rejected wherever the value is used.
		switch g.pick(5) {
		case 0:
			return fmt.Sprintf("val %s = %s", g.name("z"), g.intExpr())
		case 1:
			return "Println(" + g.intExpr() + ")"
		case 2:
			if g.monad {
				return fmt.Sprintf("bind %s = %s", g.name("z"), g.monadValue())
			}
		case 3:
			if depth > 0 {
				return fmt.Sprintf("if (%s) %s", g.cond(), g.block(ind, depth-1, true))
			}
		case 4:
			return "" // an empty block
		}
	}
	return g.value(ind, depth)
}
