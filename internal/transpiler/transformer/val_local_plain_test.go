package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLocalValIsPlainGoVariable pins the lowering of a local val: a plain Go
// variable, read by its bare name, never a std.Immutable box read through
// `.Get()`. GALA itself rejects every write to a val and taking its address,
// so a box would only add a wrapper call per read and inline cost to every
// function that declares one. A package-level val stays boxed: another
// package reads it through the same accessor.
func TestLocalValIsPlainGoVariable(t *testing.T) {
	trans := newTranspiler()
	tests := []struct {
		name     string
		input    string
		contains []string
		boxes    bool // the output boxes something other than a local val
	}{
		{
			name: "inferred val",
			input: `package main

func f() int {
    val x = 1
    x + 1
}`,
			contains: []string{"var x = 1\n", "return x + 1"},
		},
		{
			name: "typed val",
			input: `package main

func f() int64 {
    val x int64 = 1
    x
}`,
			contains: []string{"var x int64 = 1\n", "return x\n"},
		},
		{
			name: "several vals",
			input: `package main

func f() int {
    val a, b = 1, 2
    a + b
}`,
			contains: []string{"var a, b = 1, 2\n", "return a + b"},
		},
		{
			name: "several vals from one Go call",
			input: `package main

import "strconv"

func f() int {
    val n, err = strconv.Atoi("1")
    if err != nil {
        return 0
    }
    n
}`,
			contains: []string{"var n, err = strconv.Atoi(\"1\")\n", "if err != nil {", "return n\n"},
		},
		{
			name: "several vals from one Go call, one discarded",
			input: `package main

import "strconv"

func f() error {
    val _, err = strconv.Atoi("1")
    err
}`,
			contains: []string{"var _, err = strconv.Atoi(\"1\")\n", "return err\n"},
		},
		{
			name: "short declaration",
			input: `package main

func f() int {
    x := 3
    x * 2
}`,
			contains: []string{"x := 3\n", "return x * 2"},
		},
		{
			name: "tuple destructuring",
			input: `package main

func f() int {
    val (a, b) = (1, 2)
    a + b
}`,
			contains: []string{"= __tuple_1.V1.Get()\n", "= __tuple_1.V2.Get()\n", "return a + b"},
			boxes:    true, // the tuple literal boxes its fields
		},
		{
			name: "captured by a closure",
			input: `package main

func f() func() int {
    val x = 4
    () => x + 1
}`,
			contains: []string{"var x = 4\n", "return x + 1"},
		},
		{
			name: "bind takes the val as the callback parameter",
			input: `package main

func half(x int) Option[int] = if (x % 2 == 0) Some(x / 2) else None[int]()

func f(x int) Option[int] {
    bind a = half(x)
    Some(a + 1)
}`,
			contains: []string{"func(a int) std.Option[int] {", "Apply(a + 1)"},
		},
		{
			name: "package-level val stays boxed",
			input: `package main

val Limit = 10

func f() int {
    val x = Limit
    x
}`,
			contains: []string{"var Limit = std.NewImmutable(10)", "var x = Limit.Get()\n", "return x\n"},
			boxes:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			require.NoError(t, err)
			for _, want := range tt.contains {
				assert.Contains(t, got, want)
			}
			if !tt.boxes {
				assert.NotContains(t, got, "NewImmutable")
			}
		})
	}
}

// TestLocalValStaysImmutable checks that the plain lowering of a local val
// keeps every guarantee the box gave: a pointer-receiver method runs on a
// copy, so it cannot change the val, and a write or `&` is still rejected.
func TestLocalValStaysImmutable(t *testing.T) {
	trans := newTranspiler()
	const decls = `package main

struct Counter(var n int)

func (c *Counter) Bump() int {
    c.n = c.n + 1
    c.n
}

struct Holder(var c Counter)

`
	t.Run("pointer method on a val runs on a copy", func(t *testing.T) {
		got, err := trans.Transpile(decls+"func f() int {\n    val c = Counter(1)\n    c.Bump()\n}", "")
		require.NoError(t, err)
		assert.Contains(t, got, "return std.AddrOfCopy(c).Bump()")
	})
	t.Run("pointer method on a field of a val runs on a copy", func(t *testing.T) {
		got, err := trans.Transpile(decls+"func f() int {\n    val h = Holder(Counter(1))\n    h.c.Bump()\n}", "")
		require.NoError(t, err)
		assert.Contains(t, got, "return std.AddrOfCopy(h.c).Bump()")
	})
	t.Run("pointer method on a var addresses it", func(t *testing.T) {
		got, err := trans.Transpile(decls+"func f() int {\n    var c = Counter(1)\n    c.Bump()\n}", "")
		require.NoError(t, err)
		assert.Contains(t, got, "return c.Bump()")
	})
	t.Run("reassigning a val", func(t *testing.T) {
		_, err := trans.Transpile(decls+"func f() {\n    val c = Counter(1)\n    c = Counter(2)\n}", "")
		require.Error(t, err)
	})
	t.Run("writing a field through a val", func(t *testing.T) {
		_, err := trans.Transpile(decls+"func f() {\n    val h = Holder(Counter(1))\n    h.c.n = 2\n}", "")
		requireSemanticError(t, err, "cannot assign to h.c.n: it is reached through val h", "declare it `var h`")
	})
	t.Run("address of a val", func(t *testing.T) {
		_, err := trans.Transpile(decls+"func f() {\n    val c = Counter(1)\n    val p = &c\n    Println(p.n)\n}", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot take the address of c: c is a val")
	})
}
