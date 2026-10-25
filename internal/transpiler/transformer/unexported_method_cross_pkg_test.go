package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unexportedMethodFixture is a module whose `shapes` package declares a
// sealed type (which gets private is<Variant> predicates), a struct with an
// exported and an unexported method, and uses them itself.
func unexportedMethodFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/unexp\n\ngala dev\n")
	write("shapes/shapes.gala", `package shapes

sealed type Shape {
    case Circle(R int)
    case Square(S int, Label string)
    case Dot()
}

func IsRound(s Shape) bool = s.isCircle() || s.isDot()

struct Box(W int)

func (b Box) Area() int = b.W * b.W

func (b Box) half() int = b.W / 2

func (b Box) Half() int = b.half()
`)
	return root
}

// TestUnexportedMethodCrossPackage: a lower-case method of another package's
// GALA type — a sealed type's is<Variant> predicate, or a declared one — is
// rejected with GALA-E0081 instead of reaching `go build` as "cannot refer to
// unexported method". Exported methods, and the declaring package's own use of
// the private ones, are unaffected.
func TestUnexportedMethodCrossPackage(t *testing.T) {
	root := unexportedMethodFixture(t)
	const header = "package main\n\nimport \"example.com/unexp/shapes\"\n\n"

	rejected := []struct{ name, body, msg, hint string }{
		{
			name: "zero-field variant predicate",
			body: "func f(s shapes.Shape) bool = s.isDot()\n\nfunc main() { Println(f(shapes.Dot())) }",
			msg:  "isDot is an unexported method of shapes.Shape, so only package shapes can call it",
			hint: "`case shapes.Dot() => true`",
		},
		{
			name: "variant predicate with fields",
			body: "func f(s shapes.Shape) bool = s.isSquare()\n\nfunc main() { Println(f(shapes.Dot())) }",
			msg:  "isSquare is an unexported method of shapes.Shape",
			hint: "`case shapes.Square(_, _) => true`",
		},
		{
			name: "predicate in a statement body",
			body: "func main() {\n    val s = shapes.Circle(1)\n    if s.isCircle() {\n        Println(\"round\")\n    }\n}",
			msg:  "isCircle is an unexported method of shapes.Shape",
			hint: "`case shapes.Circle(_) => true`",
		},
		{
			name: "declared unexported method",
			body: "func main() { Println(shapes.Box(4).half()) }",
			msg:  "half is an unexported method of shapes.Box",
			hint: "call an exported method (only the capitalized methods of Box are visible outside package shapes)",
		},
	}
	for _, tc := range rejected {
		t.Run("rejected/"+tc.name, func(t *testing.T) {
			_, err := transpileCrossPkg(t, root, header+tc.body)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0081")
			assert.Contains(t, err.Error(), tc.msg)
			assert.Contains(t, err.Error(), tc.hint)
		})
	}

	allowed := []struct{ name, body string }{
		{"exported methods", "func main() { Println(shapes.Box(4).Area(), shapes.Box(4).Half()) }"},
		{"exported function using the predicates", "func main() { Println(shapes.IsRound(shapes.Dot())) }"},
		{"qualified match instead of a predicate", "func f(s shapes.Shape) bool = s match {\n    case shapes.Dot() => true\n    case _ => false\n}\n\nfunc main() { Println(f(shapes.Dot())) }"},
	}
	for _, tc := range allowed {
		t.Run("allowed/"+tc.name, func(t *testing.T) {
			_, err := transpileCrossPkg(t, root, header+tc.body)
			require.NoError(t, err)
		})
	}
}

// TestUnexportedMethodSamePackage: a package calls its own sealed type's
// is<Variant> predicates and its own lower-case methods freely.
func TestUnexportedMethodSamePackage(t *testing.T) {
	root := unexportedMethodFixture(t)
	_, err := transpileCrossPkg(t, root, `package main

sealed type Light {
    case On()
    case Off()
}

struct Meter(N int)

func (m Meter) twice() int = m.N * 2

func main() {
    Println(On().isOn(), Off().isOn(), Meter(2).twice())
}`)
	require.NoError(t, err)
}
