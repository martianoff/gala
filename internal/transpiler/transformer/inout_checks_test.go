package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inoutCheckDecls declares what the ownership-check tests below lend.
const inoutCheckDecls = `
struct Counter(var n int)

struct Pair(var left Counter, var right Counter)

func (inout c Counter) inc() int {
    c.n = c.n + 1
    c.n
}

func (inout c Counter) apply(f func() int) {
    c.n = c.n + f()
}

func (c Ptr[Counter]) keep() Ptr[Counter] = c

func (c Ptr[Counter]) bumpPtr() {
    c.n = c.n + 1
}

func (c Ptr[Counter]) bumpTwice() {
    c.bumpPtr()
    c.bumpPtr()
}

func (c Ptr[Counter]) keepVia() int = c.keep().n

func (inout p Pair) settle(inout c Counter) {
    p.left.n = c.n
}

func bump(inout c Counter) {
    c.n = c.n + 1
}

func both(inout a Counter, inout b Counter) {
    a.n = b.n
}

func withFn(inout c Counter, f func() int) int = c.n + f()

func store(p Ptr[Counter]) int = p.n

func each(inout c Counter, f func(inout Counter)) = f(&c)

func risky() int = 1
`

// inoutCheckSource is a program with inoutCheckDecls, decl, and body as the
// body of main.
func inoutCheckSource(decl, body string) string {
	return "package main\n" + inoutCheckDecls + "\n" + decl + "\n\nfunc main() {\n    " + body + "\n}\n"
}

// TestInoutEscapeRejected pins GALA-E0113: an inout place does not outlive
// the call that lends it.
func TestInoutEscapeRejected(t *testing.T) {
	trans := newDefaultsTranspiler()
	tests := []struct{ name, decl, want string }{
		{"stored in a val", "func f(inout c Counter) {\n    val p = &c\n    Println(p.n)\n}",
			"&c escapes: a pointer to the inout place c may only be lent to an inout parameter"},
		{"returned", "func f(inout c Counter) Ptr[Counter] = &c",
			"&c escapes"},
		{"a field address stored", "func f(inout p Pair) {\n    val q = &p.left\n    Println(q.n)\n}",
			"&p escapes"},
		{"passed to a Ptr parameter", "func f(inout c Counter) int = store(&c)",
			"&c escapes: c is lent to a parameter that is not inout"},
		{"passed to Go", "func f(inout c Counter) = Println(&c)",
			"&c escapes: c is lent to a parameter that is not inout"},
		{"captured by a lambda", "func f(inout c Counter) func() int = () => c.n",
			"inout place c is captured by a lambda"},
		{"captured by a by-name argument", "func later(f func() int) int = f()\n\nfunc f(inout c Counter) int = later(c.n)",
			"inout place c is captured by a lambda"},
		{"a method value", "func f(inout c Counter) func() int = c.inc",
			"method value c.inc captures the inout place c"},
		{"a Ptr method that keeps its receiver", "func f(inout c Counter) = Println(c.keep().n)",
			"calling keep on the inout place c lends its address to a method that may keep it"},
		{"a lambda without inout in an inout slot", "func f(inout c Counter) = each(&c, (k) => Println(k.n))",
			"cannot fill a func(inout Counter)"},
		{"a function value taking a Ptr in an inout slot", "func g(p Ptr[Counter]) = Println(p.n)\n\nfunc f(inout c Counter) = each(&c, g)",
			"its parameter 1 is not inout"},
		{"stored by a struct constructor", "struct Box(p Ptr[Counter])\n\nfunc f(inout c Counter) Box = Box(&c)",
			"&c escapes: c is lent to a parameter that is not inout"},
		{"a field lent to a Ptr parameter", "func f(inout p Pair) int = store(&p.left)",
			"&p escapes: p is lent to a parameter that is not inout"},
		{"an inout lambda in a Ptr slot", "func run(f func(Ptr[Counter])) = f(&Counter(0))\n\nfunc f() = run((inout k) => k.inc())",
			"its parameter 1 is inout, but the slot's callers may pass a pointer they share"},
		{"a Ptr method of this package that lends its receiver to one that keeps it", "func f(inout c Counter) = Println(c.keepVia())",
			"calling keepVia on the inout place c lends its address"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(inoutCheckSource(tc.decl, "Println(1)"), "inout_checks_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0113")
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestInoutGoAllowList pins the no-retain allow-list: a Go pointer method
// lent an inout place is accepted only when the compiler's built-in list,
// checked against Go's escape analysis in CI, says it keeps no reference.
func TestInoutGoAllowList(t *testing.T) {
	trans := newDefaultsTranspiler()
	src := func(body string) string {
		return "package main\n\nimport \"bytes\"\n\nfunc fill(inout b bytes.Buffer) {\n    " + body + "\n}\n\nfunc main() {\n    var b = bytes.Buffer{}\n    fill(&b)\n    Println(b.Len())\n}\n"
	}
	_, err := trans.Transpile(src("b.WriteString(\"x\")\n    b.Truncate(0)"), "inout_allow_test.gala")
	require.NoError(t, err)

	_, err = trans.Transpile(src("Println(b.Bytes())"), "inout_allow_test.gala")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0113")
	assert.Contains(t, err.Error(), "calling Bytes on the inout place b lends its address")
}

// TestInoutOverlapRejected pins GALA-E0112: one call never lends one place
// twice.
func TestInoutOverlapRejected(t *testing.T) {
	trans := newDefaultsTranspiler()
	tests := []struct{ name, body, want string }{
		{"the same place twice", "var c = Counter(0)\n    both(&c, &c)", "&c and &c overlap"},
		{"a place and its field", "var p = Pair(Counter(0), Counter(0))\n    p.settle(&p.left)", "the receiver and &p.left overlap"},
		{"a lambda reading a lent place", "var c = Counter(0)\n    Println(withFn(&c, () => c.n))", "a lambda argument reads c, which the same call lends"},
		{"a lambda reading an inout receiver", "var c = Counter(0)\n    c.apply(() => c.n)", "a lambda argument reads c, which the same call lends"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(inoutCheckSource("", tc.body), "inout_checks_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0112")
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestInoutInTryRejected pins GALA-E0115: an inout place is not used inside
// Try, where a caught panic would leave it half updated.
func TestInoutInTryRejected(t *testing.T) {
	trans := newDefaultsTranspiler()
	for name, decl := range map[string]string{
		"a Try lambda":       "func f(inout c Counter) = Println(Try(() => { c.n = risky() ; c.n }))",
		"a by-name Try body": "func f(inout c Counter) = Println(Try(c.inc()))",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := trans.Transpile(inoutCheckSource(decl, "Println(1)"), "inout_checks_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0115")
			assert.Contains(t, err.Error(), "inout place c is used inside Try")
		})
	}
}

// TestInoutChecksAccepted pins what the ownership checks allow: lending on,
// disjoint fields, an inout receiver with an inout argument on another
// place, reads and copies, a Ptr method whose receiver is borrowed, and a
// function value whose parameter is inout.
func TestInoutChecksAccepted(t *testing.T) {
	trans := newDefaultsTranspiler()
	decl := `func lend(inout c Counter) {
    bump(&c)
    c.inc()
    val copy = c
    Println(copy.n)
}

func split(inout p Pair) {
    both(&p.left, &p.right)
}

func viaFn(inout c Counter) = each(&c, (inout k) => k.inc())

func viaPtr(inout c Counter) {
    c.bumpPtr()
    c.bumpTwice()
}`
	body := `var c = Counter(0)
    lend(&c)
    var p = Pair(Counter(1), Counter(2))
    split(&p)
    var d = Counter(5)
    p.settle(&d)
    viaFn(&c)
    viaPtr(&c)
    var other = Counter(1)
    Println(withFn(&c, () => other.n))
    Println(c.n, p.left.n, withFn(&c, () => 1))`
	_, err := trans.Transpile(inoutCheckSource(decl, body), "inout_checks_test.gala")
	require.NoError(t, err)
}
