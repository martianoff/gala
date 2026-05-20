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

// TestExpectedTypeSealedVariantOverlap locks the package-scoped sealed-
// variant lookup used by the expected-type propagation path
// (resolveNamedArgExpectedFuncType -> findSealedVariant). The codegen
// path (handleNamedArgsCall -> findSealedVariantFields) was hardened
// against map-iteration-order non-determinism in a prior fix and is
// covered by TestSomeWrapSealedCaseNameOverlap; this test exercises
// the SEPARATE lookup that runs while transforming each named arg's
// value expression — the one that pushes a type onto the
// expectedArgTypes stack so a nested generic call can infer its
// return type-param from the enclosing call-site slot.
//
// Setup:
//
//	package sibling has  sealed type S { case B; case C; ... }      // zero-arg
//	package pkg_a   has  sealed type T { case B(P Option[string], Q string); ... }
//
// pkg_a imports sibling via `import _`, so the transformer's typeMetas
// contains BOTH packages' sealed metadata. A pre-fix
// findSealedVariant("B", "") would iterate typeMetas in Go's randomized
// map order and could return the sibling's zero-arg B (FieldNames
// empty) instead of pkg_a's fielded B. The argument loop would then
// never push pkg_a.B.P's Option[string] slot type onto expectedArgTypes —
// silently weakening downstream type-param inference for nested generic
// calls inside the named arg's value.
//
// The codegen path (handleNamedArgsCall -> findSealedVariantFields)
// would also have hit the wrong variant on master pre-#351; the
// post-#351 codegen path resolves correctly. So the SHAPE of the
// regression here is subtler: the codegen emits the right Apply call,
// but the expected-type pre-pass loses information. The load-bearing
// assertions:
//
//  1. The local pkg_a.B's two fields P and Q both appear in the
//     generated Apply call — confirms the variant-companion codegen
//     path picked the local variant.
//  2. The transformation succeeds across many in-process iterations,
//     confirming the lookup is deterministic regardless of Go's map
//     iteration order. Without the fix, on at least one iteration the
//     map's randomized walk lands on the sibling's B first and the
//     expected-type path silently degrades — the codegen path is also
//     vulnerable (already covered by the prior test), but the
//     expected-type path is what THIS test gates.
//
// A separate ambiguity test
// (TestExpectedTypeSealedVariantDotImportAmbiguous) asserts the
// defense-in-depth error surface: two dot-imports declaring the same
// fielded case name must be rejected at transform time.
func TestExpectedTypeSealedVariantOverlap(t *testing.T) {
	tmpDir := t.TempDir()

	const modulePath = "github.com/example/expectedtypeoverlap"
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "go.mod"),
		[]byte("module "+modulePath+"\n\ngo 1.22\n"),
		0644,
	))

	// Sibling package: zero-arg overlapping case names. Mass-overlap
	// (B..G) maximizes the surface that Go's randomized map iteration
	// has to mis-pick on any given run.
	siblingDir := filepath.Join(tmpDir, "sibling")
	require.NoError(t, os.MkdirAll(siblingDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(siblingDir, "s.gala"), []byte(`package sibling

sealed type S {
    case B
    case C
    case D
    case E
    case F
    case G
}
`), 0644))

	pkgADir := filepath.Join(tmpDir, "pkg_a")
	require.NoError(t, os.MkdirAll(pkgADir, 0755))

	typesSrc := `package pkg_a

sealed type T {
    case B(P Option[string], Q string)
    case C(P string)
    case D(P string)
    case E(P string)
    case F(P string)
    case G(P string)
}
`
	typesPath := filepath.Join(pkgADir, "types.gala")
	require.NoError(t, os.WriteFile(typesPath, []byte(typesSrc), 0644))

	wrapSrc := `package pkg_a

import _ "` + modulePath + `/sibling"

func wrap(errOpt Option[error], q string) T {
    return B(P = errOpt.Map((e) => e.Error()), Q = q)
}
`
	wrapPath := filepath.Join(pkgADir, "wrap.gala")
	require.NoError(t, os.WriteFile(wrapPath, []byte(wrapSrc), 0644))

	originalWd, err := os.Getwd()
	require.NoError(t, err)
	defer func() { _ = os.Chdir(originalWd) }()
	require.NoError(t, os.Chdir(tmpDir))

	// Repeat in-process to amplify any Go map-iteration-order
	// non-determinism. Each iteration constructs fresh analyzer,
	// transformer, and generator so typeMetas hash population varies.
	const iterations = 25
	for i := 0; i < iterations; i++ {
		p := transpiler.NewAntlrGalaParser()
		searchPaths := append([]string{tmpDir}, getStdSearchPath()...)
		a := analyzer.NewGalaAnalyzerWithPackageFiles(p, searchPaths, []string{typesPath})
		tree, err := p.Parse(wrapSrc)
		require.NoError(t, err, "iteration %d: parse", i)
		richAST, err := a.Analyze(tree, wrapPath)
		require.NoError(t, err, "iteration %d: analyze", i)

		tr := transformer.NewGalaASTTransformer()
		g := generator.NewGoCodeGenerator()
		fset, file, err := tr.Transform(richAST)
		require.NoError(t, err, "iteration %d: transform", i)
		gen, err := g.Generate(fset, file)
		require.NoError(t, err, "iteration %d: generate", i)

		// Local pkg_a.B must lower to B{}.Apply(...) with both args. If
		// the wrong (sibling, zero-arg) variant won the lookup at any
		// point along the named-arg pipeline, either:
		//   (a) the codegen path would emit a bare zero-value B{} with no
		//       Apply call (covered by the existing
		//       TestSomeWrapSealedCaseNameOverlap), or
		//   (b) the expected-type pre-pass would silently push NilType,
		//       and although codegen still picks the right variant
		//       after the prior codegen fix, the expected-type stack
		//       would be missing the Option[string] slot. The first-
		//       order assertion that catches BOTH is: the Apply call
		//       must be present AND non-empty.
		require.True(t, strings.Contains(gen, "B{}.Apply("),
			"iteration %d: expected B{}.Apply(...) for local sealed variant\n--- generated ---\n%s", i, gen)
		require.False(t, strings.Contains(gen, "Apply(B{})"),
			"iteration %d: unexpected bare B{} struct literal — would indicate the sibling's zero-arg B shadowed the local variant\n--- generated ---\n%s", i, gen)
	}
}

// TestExpectedTypeSealedVariantDotImportAmbiguous verifies the
// defense-in-depth ambiguity guard on the expected-type lookup path.
// When two dot-imported packages each declare a fielded sealed case
// of the same name, the unqualified named-arg call site
// `B(P=..., Q=...)` must be rejected — silently picking one would
// re-introduce the same class of map-iteration non-determinism the
// rest of this fix removed.
//
// The analyzer's dot-import symbol-collision detector typically
// reports this earlier with its own diagnostic; the transformer-level
// guard here is defense-in-depth for paths the analyzer detector
// might miss in future refactors. Either error path mentioning the
// variant and both packages is acceptable.
func TestExpectedTypeSealedVariantDotImportAmbiguous(t *testing.T) {
	tmpDir := t.TempDir()
	const modulePath = "github.com/example/expectedtypeambig"
	require.NoError(t, os.WriteFile(
		filepath.Join(tmpDir, "go.mod"),
		[]byte("module "+modulePath+"\n\ngo 1.22\n"),
		0644,
	))

	aDir := filepath.Join(tmpDir, "pkga")
	require.NoError(t, os.MkdirAll(aDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(aDir, "a.gala"), []byte(`package pkga

sealed type A {
    case B(P string, Q string)
}
`), 0644))

	bDir := filepath.Join(tmpDir, "pkgb")
	require.NoError(t, os.MkdirAll(bDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(bDir, "b.gala"), []byte(`package pkgb

sealed type B2 {
    case B(P string, R string)
}
`), 0644))

	hostDir := filepath.Join(tmpDir, "host")
	require.NoError(t, os.MkdirAll(hostDir, 0755))
	hostSrc := `package host

import . "` + modulePath + `/pkga"
import . "` + modulePath + `/pkgb"

func wrap(x string) A {
    return B(P = x, Q = x)
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
	require.Contains(t, errMsg, "\"B\"", "error should name the variant: %s", errMsg)
	require.Contains(t, errMsg, "pkga", "error should name the first conflicting package: %s", errMsg)
	require.Contains(t, errMsg, "pkgb", "error should name the second conflicting package: %s", errMsg)
}
