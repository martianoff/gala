package transformer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/require"
)

// TestSomeWrapSealedCaseNameOverlap locks the lowering of
// `Some[T](Case(named args))` when a sibling package declares its own
// sealed type whose case names overlap with the local one.
//
// Setup:
//
//	package sibling has  sealed type S { case B; case C; ...  }  // zero-arg
//	package pkg_a   has  sealed type T { case B(P, Q Option, R); ... } // fielded
//
// pkg_a imports sibling. Inside pkg_a, `Some[T](B(P = x, ...))` must
// lower to `Some[T]{}.Apply(B{}.Apply(x, ...))` so the argument has the
// parent sealed type T. The defect was that the transformer's
// sealed-variant field lookup iterated every sealed metadata across
// every package without a package filter and returned the first match.
// When the sibling's zero-arg `B` happened to be found first (Go map
// iteration order is non-deterministic), the returned fields slice
// was nil — the variant-companion branch short-circuited and the call
// fell through to the plain struct-literal path, which emits a bare
// `B{}` zero-value (dropping `.Apply(args)` and all the named args).
//
// The Go compiler then rejects:
//
//	cannot use B{} (value of struct type B) as T value in argument to
//	Some[T]{}.Apply
//
// The fix scopes the sealed-parent lookup to the variant's own
// package, so the local declaration always wins.
func TestSomeWrapSealedCaseNameOverlap(t *testing.T) {
	tmpDir := t.TempDir()

	// go.mod at the workspace root so the resolver treats the
	// subdirectories as packages of one module.
	const modulePath = "github.com/example/casenameoverlap"
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "go.mod"),
		[]byte("module "+modulePath+"\n\ngo 1.22\n"),
		0644,
	))

	// Sibling package: a sealed type whose case names overlap with
	// pkg_a's T cases — all zero-arg.
	siblingDir := filepath.Join(tmpDir, "sibling")
	require.NoError(t, os.MkdirAll(siblingDir, 0755))
	siblingSrc := `package sibling

sealed type S {
    case B
    case C
    case D
    case E
    case F
    case G
}
`
	require.NoError(t, os.WriteFile(filepath.Join(siblingDir, "s.gala"), []byte(siblingSrc), 0644))

	// pkg_a: types.gala declares T; wrap.gala imports the sibling and
	// constructs Some[T](Case(named args)) for every T case.
	pkgADir := filepath.Join(tmpDir, "pkg_a")
	require.NoError(t, os.MkdirAll(pkgADir, 0755))

	typesSrc := `package pkg_a

sealed type T {
    case A(P string, Q string)
    case B(P string, Q Option[string], R string)
    case C(P string)
    case D(P string)
    case E(P string)
    case F(P string)
    case G(P string)
    case H(P Array[string])
}
`
	typesPath := filepath.Join(pkgADir, "types.gala")
	require.NoError(t, os.WriteFile(typesPath, []byte(typesSrc), 0644))

	wrapSrc := `package pkg_a

import _ "` + modulePath + `/sibling"

func wrap(tag string, x string) Option[T] {
    if (tag == "a") { return Some[T](A(P = x, Q = x)) }
    if (tag == "b") { return Some[T](B(P = x, Q = None[string](), R = x)) }
    if (tag == "c") { return Some[T](C(P = x)) }
    if (tag == "d") { return Some[T](D(P = x)) }
    if (tag == "e") { return Some[T](E(P = x)) }
    if (tag == "f") { return Some[T](F(P = x)) }
    if (tag == "g") { return Some[T](G(P = x)) }
    if (tag == "h") { return Some[T](H(P = EmptyArray[string]())) }
    return None[T]()
}
`
	wrapPath := filepath.Join(pkgADir, "wrap.gala")
	require.NoError(t, os.WriteFile(wrapPath, []byte(wrapSrc), 0644))

	// Transpile with the workspace root on the search path so the
	// resolver finds the sibling package, and types.gala registered as
	// a package-sibling so the analyzer knows about T's variants.
	originalWd, err := os.Getwd()
	require.NoError(t, err)
	defer func() { _ = os.Chdir(originalWd) }()
	require.NoError(t, os.Chdir(tmpDir))

	p := transpiler.NewAntlrGalaParser()
	searchPaths := append([]string{tmpDir}, getStdSearchPath()...)
	a := analyzer.NewGalaAnalyzerWithPackageFiles(p, searchPaths, []string{typesPath})
	tree, err := p.Parse(wrapSrc)
	require.NoError(t, err)
	richAST, err := a.Analyze(tree, wrapPath)
	require.NoError(t, err)

	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	fset, file, err := tr.Transform(richAST)
	require.NoError(t, err)
	gen, err := g.Generate(fset, file)
	require.NoError(t, err)

	// Each case must lower to `<Case>{}.Apply(...)` so the result has
	// type T — Some[T]{}.Apply accepts T, not the bare case struct.
	for _, c := range []string{"A", "B", "C", "D", "E", "F", "G", "H"} {
		applyFrag := c + "{}.Apply("
		require.True(t, strings.Contains(gen, applyFrag),
			"expected generated code to contain %q (Apply form for case %s)\n--- generated ---\n%s",
			applyFrag, c, gen)
	}

	// And must NOT emit the bare zero-value `Some[T]{}.Apply(<Case>{})` —
	// that drops the Apply call and all constructor args, and Go
	// rejects it because <Case>{} is not a T.
	for _, c := range []string{"A", "B", "C", "D", "E", "F", "G", "H"} {
		bareFrag := "Some[T]{}.Apply(" + c + "{})"
		require.False(t, strings.Contains(gen, bareFrag),
			"expected generated code to NOT contain bare %q (zero-value case struct dropped from Apply call)\n--- generated ---\n%s",
			bareFrag, gen)
	}
}
