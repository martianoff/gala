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

import . "martianoff/gala/collection_immutable"
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

// TestSomeWrapSealedCaseDotImportLookup verifies the deterministic
// dot-import fallback for sealed-variant constructors. When the
// current package does not declare the variant locally and exactly
// one dot-imported package does, the unqualified call site must
// resolve to that variant's Apply form.
//
// This locks the happy path of the dot-import fallback. The lookup
// must succeed reliably across runs; previously a map-iteration
// scan of every sealed type in every loaded package could pick the
// wrong one when names collided, or miss a deterministic resolution
// when only one package contained the variant.
func TestSomeWrapSealedCaseDotImportLookup(t *testing.T) {
	tmpDir := t.TempDir()
	const modulePath = "github.com/example/dotimportlookup"
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "go.mod"),
		[]byte("module "+modulePath+"\n\ngo 1.22\n"),
		0644,
	))

	// `other` declares the sealed type and its cases.
	otherDir := filepath.Join(tmpDir, "other")
	require.NoError(t, os.MkdirAll(otherDir, 0755))
	otherSrc := `package other

sealed type O {
    case W(P string, Q string)
}
`
	require.NoError(t, os.WriteFile(filepath.Join(otherDir, "o.gala"), []byte(otherSrc), 0644))

	// `host` dot-imports `other` and constructs W unqualified.
	hostDir := filepath.Join(tmpDir, "host")
	require.NoError(t, os.MkdirAll(hostDir, 0755))
	hostSrc := `package host

import . "` + modulePath + `/other"

func wrap(x string) Option[O] {
    return Some[O](W(P = x, Q = x))
}
`
	hostPath := filepath.Join(hostDir, "host.gala")
	require.NoError(t, os.WriteFile(hostPath, []byte(hostSrc), 0644))

	originalWd, err := os.Getwd()
	require.NoError(t, err)
	defer func() { _ = os.Chdir(originalWd) }()
	require.NoError(t, os.Chdir(tmpDir))

	p := transpiler.NewAntlrGalaParser()
	searchPaths := append([]string{tmpDir}, getStdSearchPath()...)
	a := analyzer.NewGalaAnalyzer(p, searchPaths)
	tree, err := p.Parse(hostSrc)
	require.NoError(t, err)
	richAST, err := a.Analyze(tree, hostPath)
	require.NoError(t, err)

	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	fset, file, err := tr.Transform(richAST)
	require.NoError(t, err)
	gen, err := g.Generate(fset, file)
	require.NoError(t, err)

	require.True(t, strings.Contains(gen, "W{}.Apply("),
		"expected generated code to contain W{}.Apply(...) for dot-imported variant\n--- generated ---\n%s", gen)
	require.False(t, strings.Contains(gen, "Apply(W{})"),
		"expected generated code to NOT contain bare Apply(W{}) (dropped args)\n--- generated ---\n%s", gen)
}

// TestSomeWrapSealedCaseDotImportAmbiguousIsError verifies that an
// unqualified sealed-variant constructor that matches a case in two
// or more dot-imported packages is rejected with a clear error
// rather than silently resolved by import order. Silent first-wins
// would re-introduce the same class of non-determinism the parent
// fix removed for the in-package overlap case; the strict error
// surface makes the user qualify the call site instead.
//
// The analyzer's existing dot-import symbol-collision detector
// surfaces this case as the primary diagnostic (it fires before the
// transformer's sealed-variant lookup is reached). This test locks
// that behavior: the user MUST see an error mentioning the variant
// name and both conflicting packages so they can qualify the call
// site. The transformer-level ambiguity guard added alongside this
// test is defense-in-depth for paths the analyzer detector might
// miss in future refactors.
func TestSomeWrapSealedCaseDotImportAmbiguousIsError(t *testing.T) {
	tmpDir := t.TempDir()
	const modulePath = "github.com/example/dotimportambig"
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "go.mod"),
		[]byte("module "+modulePath+"\n\ngo 1.22\n"),
		0644,
	))

	// Two dot-imported packages, each declaring its own sealed type
	// with a `case B`. Different field sets prevent accidental coincidence.
	aDir := filepath.Join(tmpDir, "pkga")
	require.NoError(t, os.MkdirAll(aDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(aDir, "a.gala"), []byte(`package pkga

sealed type A {
    case B(P string)
}
`), 0644))

	bDir := filepath.Join(tmpDir, "pkgb")
	require.NoError(t, os.MkdirAll(bDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(bDir, "b.gala"), []byte(`package pkgb

sealed type B2 {
    case B(Q string, R string)
}
`), 0644))

	hostDir := filepath.Join(tmpDir, "host")
	require.NoError(t, os.MkdirAll(hostDir, 0755))
	hostSrc := `package host

import . "` + modulePath + `/pkga"
import . "` + modulePath + `/pkgb"

func wrap(x string) Option[A] {
    return Some[A](B(P = x))
}
`
	hostPath := filepath.Join(hostDir, "host.gala")
	require.NoError(t, os.WriteFile(hostPath, []byte(hostSrc), 0644))

	originalWd, err := os.Getwd()
	require.NoError(t, err)
	defer func() { _ = os.Chdir(originalWd) }()
	require.NoError(t, os.Chdir(tmpDir))

	p := transpiler.NewAntlrGalaParser()
	searchPaths := append([]string{tmpDir}, getStdSearchPath()...)
	a := analyzer.NewGalaAnalyzer(p, searchPaths)
	tree, err := p.Parse(hostSrc)
	require.NoError(t, err)
	richAST, err := a.Analyze(tree, hostPath)
	require.NoError(t, err)

	tr := transformer.NewGalaASTTransformer()
	_, _, err = tr.Transform(richAST)
	require.Error(t, err, "expected transpile to fail with a dot-import ambiguity error")
	errMsg := err.Error()
	// The analyzer's dot-import symbol-collision detector reports it
	// as "dot-import symbol collision" (see imports.go). The exact
	// surface phrasing may evolve; assert on the load-bearing facts:
	// the variant name and the two conflicting packages, so the user
	// has enough information to qualify the call site.
	require.Contains(t, errMsg, "\"B\"", "error should name the variant: %s", errMsg)
	require.Contains(t, errMsg, "pkga", "error should name the first conflicting package: %s", errMsg)
	require.Contains(t, errMsg, "pkgb", "error should name the second conflicting package: %s", errMsg)
}
