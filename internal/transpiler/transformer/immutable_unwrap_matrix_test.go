package transformer_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Immutable auto-unwrap, across every spelling that reads a field
//
// A shorthand struct gives every field val semantics, so the field is stored as
// std.Immutable[T] and reading it has to go through .Get(). unwrapImmutable is
// the one function that does this, and its doc comment says every path that
// reads through the wrapper must route through it. Nothing checked that they
// do.
//
// They have not. Two fixes five weeks apart taught two more syntactic sites to
// unwrap: placeholder lambdas, then cross-file package-level vals. Both had the
// same shape — one spelling of an access unwrapped, an equivalent spelling did
// not, and the second silently produced a wrapped value. That is not an
// architecture problem, since the 34 call sites are genuine decision points
// about where a value is read. It is a coverage problem: a new spelling is a
// new site, and nothing fails when it is forgotten.
//
// So this asserts the contract itself rather than any one site. For each field
// type, the same read is written every way GALA allows, and the generated Go
// must contain no bare access to the field: every `x.Name` must be the receiver
// of a `.Get()`. A spelling that skips the unwrap fails here whether or not
// anyone thought to write an example for it.
//
// The check is on the generated AST rather than the program's output, so it
// covers spellings whose result is never printed, and it names the spelling
// that broke instead of a mismatched line of stdout.
func TestImmutableFieldUnwrapAcrossSpellings(t *testing.T) {
	// Each case is one way to read a field of a shorthand struct. They are
	// deliberately equivalent: whatever a spelling does, the others must do.
	spellings := []struct {
		name string
		// body is GALA statements inside main. The struct `Person(Name string,
		// Age int)` and a `people` Array are already in scope, along with a
		// single `alice`.
		body string
	}{
		{
			name: "explicit lambda over a collection",
			body: `val out = people.Map((p) => p.Name)
    Println(out.String())`,
		},
		{
			name: "placeholder lambda over a collection",
			body: `val out = people.Map(_.Name)
    Println(out.String())`,
		},
		{
			name: "direct field access on a val",
			body: `val out = alice.Name
    Println(out)`,
		},
		{
			name: "field access in string interpolation",
			body: `Println(s"name is ${alice.Name}")`,
		},
		{
			name: "field access in a match arm",
			body: `val out = alice match {
        case Person(n, _) => n
        case _ => "none"
    }
    Println(out)`,
		},
		{
			name: "field access inside a nested lambda",
			body: `val f = () => alice.Name
    Println(f())`,
		},
		{
			name: "field access as a call argument",
			body: `Println(alice.Name)`,
		},
		{
			name: "field access in a conditional",
			body: `val out = if (true) alice.Name else "none"
    Println(out)`,
		},
		{
			name: "field access through a block-bodied lambda",
			body: `val f = (p Person) => {
        val n = p.Name
        n
    }
    Println(f(alice))`,
		},
		{
			name: "field access in a comparison",
			body: `Println(if (alice.Name == "Alice") "yes" else "no")`,
		},
	}

	for _, sp := range spellings {
		t.Run(sp.name, func(t *testing.T) {
			src := fmt.Sprintf(`package main

import . "martianoff/gala/collection_immutable"

struct Person(Name string, Age int)

func main() {
    val alice = Person(Name = "Alice", Age = 30)
    val people = ArrayOf(alice, Person(Name = "Bob", Age = 25))
    %s
}
`, sp.body)

			trans, _ := newTranspilerWithTransformer()
			out, err := trans.Transpile(src, "unwrap_matrix.gala")
			require.NoError(t, err, "this spelling does not transpile:\n%s", src)

			bare := findBareImmutableFieldReads(t, stripGeneratedHeader(out), "Name")
			require.Empty(t, bare,
				"this spelling reads the Immutable field without unwrapping it, at %v.\n"+
					"Every read of a shorthand-struct field must go through .Get() — see "+
					"unwrapImmutable in expressions.go, which every such path is required to "+
					"route through. The equivalent spellings in this test do unwrap, so this "+
					"one produces a wrapped value where they produce a plain one.\n"+
					"--- GALA ---\n%s\n--- generated Go ---\n%s",
				bare, src, out)
		})
	}
}

// findBareImmutableFieldReads returns the positions in main where the named
// field is selected without being the receiver of a .Get() call.
//
// Only main is scanned. The transpiler also emits Copy, Equal and Unapply for
// the struct, and those legitimately handle the wrapper itself — Copy passes it
// to std.Copy, Unapply returns it as the extracted value. They are not reads of
// the underlying value and requiring .Get() there would be wrong.
//
// Within main, two more shapes are excluded. A field named inside a composite
// literal is a key being written, not a value read. A selector whose parent is
// `.Get()` is the unwrap itself, which is what we want to see.
func findBareImmutableFieldReads(t *testing.T, src, field string) []string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "generated.go", src, parser.AllErrors)
	require.NoError(t, err, "generated Go does not parse:\n%s", src)

	var mainBody ast.Node
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "main" && fn.Recv == nil {
			mainBody = fn.Body
		}
	}
	require.NotNil(t, mainBody, "generated Go has no func main:\n%s", src)

	unwrapped := map[*ast.SelectorExpr]bool{}
	written := map[*ast.SelectorExpr]bool{}

	ast.Inspect(mainBody, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CallExpr:
			// x.Name.Get() — the inner selector is the unwrap's receiver.
			if outer, ok := v.Fun.(*ast.SelectorExpr); ok && outer.Sel.Name == "Get" {
				if inner, ok := outer.X.(*ast.SelectorExpr); ok && inner.Sel.Name == field {
					unwrapped[inner] = true
				}
			}
		case *ast.CompositeLit:
			// Person{Name: ...} — a key, not a read.
			for _, elt := range v.Elts {
				if kv, ok := elt.(*ast.KeyValueExpr); ok {
					if sel, ok := kv.Key.(*ast.SelectorExpr); ok && sel.Sel.Name == field {
						written[sel] = true
					}
				}
			}
		}
		return true
	})

	var bare []string
	ast.Inspect(mainBody, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != field || unwrapped[sel] || written[sel] {
			return true
		}
		// A selector on a package qualifier (std.Name) is not a field read.
		if x, ok := sel.X.(*ast.Ident); ok && strings.Contains(x.Name, "/") {
			return true
		}
		bare = append(bare, fmt.Sprintf("line %d", fset.Position(sel.Pos()).Line))
		return true
	})
	return bare
}
