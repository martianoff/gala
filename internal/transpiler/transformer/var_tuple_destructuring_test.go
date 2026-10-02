package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `var (a, b) = tuple` lowers like its `val` twin, except that each name is a
// plain Go variable holding the component's value, so it can be reassigned.
// The generated-Go oracle type-checks every output, so the reassignments below
// are checked against the variables' Go types too.
func TestVarTupleDestructuring(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		contains []string
	}{
		{
			name: "explicit Tuple constructor",
			body: `
func main() {
    var (a, b) = Tuple[int, int](V1 = 1, V2 = 2)
    a = a + b
    Println(a)
    Println(b)
}
`,
			contains: []string{"__tuple_1.V1.Get()", "__tuple_1.V2.Get()", "a = a + b"},
		},
		{
			name: "tuple literal of mixed component types",
			body: `
func main() {
    var (n, s) = (1, "x")
    n += 1
    s = s + "y"
    Println(s"$n $s")
}
`,
			contains: []string{"__tuple_1.V1.Get()", "__tuple_1.V2.Get()"},
		},
		{
			name: "function result",
			body: `
func divmod(a int, b int) Tuple[int, int] = (a / b, a % b)

func main() {
    var (q, r) = divmod(17, 5)
    q = q * 10
    Println(q + r)
}
`,
			contains: []string{"__tuple_1 = divmod(17, 5)", "__tuple_1.V1.Get()"},
		},
		{
			name: "package-level bindings reassigned from a function",
			body: `
var (hits, misses) = (0, 0)

func hit() {
    hits = hits + 1
}

func main() {
    hit()
    Println(s"$hits $misses")
}
`,
			contains: []string{"__tuple_1.V1.Get()", "hits = hits + 1"},
		},
		{
			name: "val destructuring keeps the Immutable field",
			body: `
func main() {
    val (a, b) = (1, 2)
    Println(a + b)
}
`,
			contains: []string{"__tuple_1.V1\n", "__tuple_1.V2\n"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newBindTranspiler().Transpile("package main\n"+tt.body, "")
			require.NoError(t, err)
			for _, want := range tt.contains {
				assert.Contains(t, got, want)
			}
		})
	}
}

// A `val` destructuring still binds immutable names.
func TestValTupleDestructuringStaysImmutable(t *testing.T) {
	_, err := newBindTranspiler().Transpile(`package main

func main() {
    val (a, b) = (1, 2)
    a = 3
    Println(a + b)
}
`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot assign to immutable variable a")
}

// A destructured name is checked against the reserved source-map marker
// prefix like any other declared name, and a failing Go call is named with the
// keyword the author wrote.
func TestTupleDestructuringChecks(t *testing.T) {
	for _, kw := range []string{"val", "var"} {
		_, err := newBindTranspiler().Transpile("package main\n\nfunc main() {\n    "+kw+" (__gala_line_7, b) = (1, 2)\n    Println(b)\n}\n", "")
		require.Error(t, err, kw)
		assert.Contains(t, err.Error(), "reserved", kw)

		_, err = newBindTranspiler().Transpile("package main\n\nimport \"strconv\"\n\nfunc main() {\n    "+kw+" (n, e) = strconv.Atoi(\"1\")\n    Println(n)\n}\n", "")
		require.Error(t, err, kw)
		assert.Contains(t, err.Error(), "cannot be destructured with `"+kw+" (...)`", kw)
	}
}
