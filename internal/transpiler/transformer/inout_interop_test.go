package transformer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInoutGoWrapper pins how an exported inout function or method is
// reached from Go (docs/GO_INTEROP.MD, "inout functions and methods"): Go
// calls a wrapper with the declared Go signature that lends the GALA body a
// copy of each inout argument and copies it back, and GALA calls the body,
// GALA_<Func> or GALA_<Type>_<Method>, directly. A function value is the
// wrapper. An unexported inout function is split too: Go beside it may call it.
func TestInoutGoWrapper(t *testing.T) {
	trans := newDefaultsTranspiler()
	src := `package main

struct Account(var Balance int)

func (inout a Account) Deposit(amount int) int {
    a.Balance = a.Balance + amount
    a.Balance
}

func Transfer(inout from Account, inout to Account, amount int) {
    from.Balance = from.Balance - amount
    to.Balance = to.Balance + amount
}

func Twice[T any](inout x T, f func(T) T) {
    x = f(f(x))
}

func local(inout a Account) {
    a.Balance = 0
}

func (a Account) Merge(inout b Account) {
    b.Balance = b.Balance + a.Balance
}

func main() {
    var a = Account(1)
    var b = Account(2)
    a.Deposit(3)
    Transfer(&a, &b, 1)
    var n = 1
    Twice(&n, (v) => v + 1)
    local(&a)
    a.Merge(&b)
    val f = Transfer
    f(&a, &b, 1)
    Println(a.Balance, b.Balance, n)
}
`
	out, err := trans.Transpile(src, "inout_wrapper_test.gala")
	require.NoError(t, err)
	for _, want := range []string{
		"func (a *Account) Deposit(amount int) int {",
		"_gala_in_a := *a",
		"_gala_r0 := GALA_Account_Deposit(&_gala_in_a, amount)",
		"*a = _gala_in_a",
		"func GALA_Account_Deposit(a *Account, amount int) int {",
		"func Transfer(from *Account, to *Account, amount int) {",
		"GALA_Transfer(&_gala_in_from, &_gala_in_to, amount)",
		"func GALA_Transfer(from *Account, to *Account, amount int) {",
		"GALA_Twice[T](&_gala_in_x, f)",
		"GALA_Account_Deposit(&a, 3)",
		"GALA_Transfer(&a, &b, 1)",
		"GALA_Twice(&n, ",
		"GALA_local(&a)",
		"func local(a *Account) {",
		"func (a Account) Merge(b *Account) {",
		"GALA_Account_Merge(a, &b)",
		"var f = Transfer",
	} {
		assert.Contains(t, out, want)
	}
	assert.Contains(t, out, "func GALA_local(a *Account) {")
	// The wrapper checks for nil once; the body, which only GALA calls,
	// does not check again.
	assert.Equal(t, 1, strings.Count(out, `panic("Transfer: parameter from is a Ptr[Account], but the caller passed nil")`))
}

// TestInoutInterfaceImpl pins GALA-E0116: a value fills an interface that
// declares an inout method only when its type implements that method with
// an inout receiver, or is a Go type whose method is on the no-retain
// allow-list.
func TestInoutInterfaceImpl(t *testing.T) {
	trans := newDefaultsTranspiler()
	src := func(body string) string {
		return `package main

import (
    "bytes"
    "strings"
)

type Stepper interface {
    inout Step() int
}

type Sink interface {
    inout WriteString(s string) (int, error)
}

type Plain interface {
    Step() int
}

struct Good(var n int)

func (inout g Good) Step() int {
    g.n = g.n + 1
    g.n
}

struct Leaky(var n int)

func (l Ptr[Leaky]) Step() int = l.n

func run(s Stepper) int = s.Step()

func main() {
    ` + body + `
}
`
	}
	_, err := trans.Transpile(src("Println(run(&Good(0)))\n    var s Sink = &bytes.Buffer{}\n    s.WriteString(\"x\")"), "inout_iface_test.gala")
	require.NoError(t, err)

	rejected := []struct{ name, body, want string }{
		{"a GALA method without inout", "Println(run(&Leaky(0)))", "Stepper declares Step inout, and Leaky.Step has no inout receiver"},
		{"a Go method not on the allow-list", "var s Sink = &strings.Builder{}\n    s.WriteString(\"x\")", "its method WriteString is Go code"},
		{"through an interface without inout", "var p Plain = &Leaky(0)\n    var s Stepper = p\n    Println(run(s))", "Stepper declares Step inout, and Plain.Step has no inout receiver"},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(src(tc.body), "inout_iface_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0116")
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestInoutInterfaceConstraint pins GALA-E0116 at a type argument: a type
// parameter constrained by an interface that declares an inout method is
// instantiated only with a type implementing it with an inout receiver, as
// a value filling the interface must: a generic function's, struct's or
// method's, written or inferred. Go's own constraint check cannot tell
// an inout receiver from a Ptr receiver. A type parameter is judged by its
// constraint.
func TestInoutInterfaceConstraint(t *testing.T) {
	trans := newDefaultsTranspiler()
	src := func(decl, body string) string {
		return `package main

import "fmt"

type Stepper interface {
    inout Step() int
}

type Shower interface {
    inout String() string
}

func show[T Shower](s T) string = s.String()

func describe(s fmt.Stringer) string = s.String()

struct Slack(var n int)

func (s Slack) Step() int = s.n

type Plain interface {
    Step() int
}

struct Good(var n int)

func (inout g Good) Step() int {
    g.n = g.n + 1
    g.n
}

struct Leaky(var n int)

func (l Ptr[Leaky]) Step() int = l.n

func run(s Stepper) int = s.Step()

func runG[T Stepper](s T) int = s.Step()

func viaStepper[T Stepper](s T) int = runG(s)

struct Box[T Stepper](s T)

func (b Box[T]) run() int = b.s.Step()

struct Holder(n int)

func (h Holder) apply[T Stepper](s T) int = s.Step() + h.n

` + decl + `

func main() {
    ` + body + `
}
`
	}
	_, err := trans.Transpile(src("", "Println(runG(&Good(0)), viaStepper(&Good(1)), runG[Ptr[Good]](&Good(2)), Box(&Good(3)).run(), Holder(1).apply(&Good(4)))"), "inout_constraint_test.gala")
	require.NoError(t, err)

	rejected := []struct{ name, decl, body, want string }{
		{"an inferred type argument without inout", "", "Println(runG(&Leaky(0)))",
			"Ptr[Leaky], the type argument for T of runG, does not satisfy its constraint Stepper: Stepper declares Step inout, and Leaky.Step has no inout receiver"},
		{"a generic struct built with a type argument without inout", "", "Println(Box(&Leaky(0)).run())",
			"Ptr[Leaky], the type argument for T of Box, does not satisfy its constraint Stepper"},
		{"a written type argument of a generic type", "func keep(b Box[Ptr[Leaky]]) int = b.run()", "Println(1)",
			"the type argument for T of Box, does not satisfy its constraint Stepper"},
		{"a generic method's type argument without inout", "", "Println(Holder(1).apply(&Leaky(0)))",
			"the type argument for T of apply, does not satisfy its constraint Stepper"},
		{"an explicit type argument without inout", "", "Println(runG[Ptr[Leaky]](&Leaky(0)))",
			"the type argument for T of runG, does not satisfy its constraint Stepper"},
		{"a value-receiver method through a written type argument", "", "Println(runG[Ptr[Slack]](&Slack(0)))",
			"Stepper declares Step inout, and Slack.Step has no inout receiver"},
		{"a generic function filling a function-typed parameter", "func apply(f func(Ptr[Leaky]) int, p Ptr[Leaky]) int = f(p)", "Println(apply(runG, &Leaky(0)))",
			"the type argument for T of runG, does not satisfy its constraint Stepper"},
		{"a type parameter constrained by a Go interface", "func viaGo[T fmt.Stringer](s T) string = show(s)", "Println(1)",
			"the type argument for T of show, does not satisfy its constraint Shower"},
		{"a type parameter constrained without inout", "func viaPlain[T Plain](s T) int = runG(s)", "Println(1)",
			"Stepper declares Step inout, and Plain.Step has no inout receiver"},
		{"a type parameter filling an inout interface slot", "func toSlot[T Plain](s T) int = run(s)", "Println(1)",
			"Stepper declares Step inout, and Plain.Step has no inout receiver"},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(src(tc.decl, tc.body), "inout_constraint_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0116")
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestInoutGoWrapperNoCopy pins that an exported inout parameter of a type
// that must not be copied is GALA-E0111: Go reaches it through a copy.
func TestInoutGoWrapperNoCopy(t *testing.T) {
	trans := newDefaultsTranspiler()
	src := "package main\n\nimport \"strings\"\n\nfunc Fill(inout b strings.Builder) {\n    b.Grow(1)\n}\n\nfunc main() {\n    var b = strings.Builder{}\n    Fill(&b)\n    Println(b.Len())\n}\n"
	_, err := trans.Transpile(src, "inout_nocopy_test.gala")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0111")
	assert.Contains(t, err.Error(), "exported Fill cannot lend b to Go code through a copy")
}

// TestInoutTypePattern pins GALA-E0116 at a typed pattern: Go's dynamic
// type test cannot tell an inout receiver from a Ptr receiver, so a pattern
// may test for an interface declaring inout methods only on a value whose
// static type already guarantees them.
func TestInoutTypePattern(t *testing.T) {
	trans := newDefaultsTranspiler()
	src := func(decl, body string) string {
		return `package main

type Stepper interface {
    inout Step() int
}

type Walker interface {
    inout Step() int
    Name() string
}

type Plain interface {
    Step() int
}

struct Good(var n int)

func (inout g Good) Step() int {
    g.n = g.n + 1
    g.n
}

func (g Good) Name() string = "good"

struct Leaky(var n int)

func (l Ptr[Leaky]) Step() int = l.n

` + decl + `

func main() {
    ` + body + `
}
`
	}
	accepted := []string{
		"val w Walker = &Good(0)\n    w match {\n        case s: Stepper => Println(s.Step())\n        case _ => Println(0)\n    }",
		"val g = &Good(0)\n    g match {\n        case s: Stepper => Println(s.Step())\n        case _ => Println(0)\n    }",
		"val x any = &Leaky(0)\n    x match {\n        case p: Plain => Println(p.Step())\n        case _ => Println(0)\n    }",
	}
	for _, body := range accepted {
		_, err := trans.Transpile(src("", body), "inout_pattern_test.gala")
		require.NoError(t, err, body)
	}

	rejected := []struct{ name, decl, body, want string }{
		{"a value of type any", "", "val x any = &Leaky(3)\n    x match {\n        case s: Stepper => Println(s.Step())\n        case _ => Println(0)\n    }",
			"a typed pattern cannot test an any for Stepper"},
		{"an interface without inout", "", "val p Plain = &Good(0)\n    p match {\n        case s: Stepper => Println(s.Step())\n        case _ => Println(0)\n    }",
			"a typed pattern cannot test a Plain for Stepper"},
		{"a concrete type without inout", "", "val l = &Leaky(1)\n    l match {\n        case s: Stepper => Println(s.Step())\n        case _ => Println(0)\n    }",
			"Stepper declares Step inout, and Leaky.Step has no inout receiver"},
		{"a value of an unconstrained type parameter", "func pick[T any](x T) int = x match {\n    case s: Stepper => s.Step()\n    case _ => 0\n}", "Println(pick(&Leaky(1)))",
			"a typed pattern cannot test a T (a type parameter that may hold any type) for Stepper"},
		{"a value of type error", "func pickErr(e error) int = e match {\n    case s: Stepper => s.Step()\n    case _ => 0\n}", "Println(1)",
			"a typed pattern cannot test a error for Stepper"},	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(src(tc.decl, tc.body), "inout_pattern_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0116")
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}
