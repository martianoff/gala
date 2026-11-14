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
