package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/require"
)

// transpileGenericSealed compiles src through the full pipeline.
func transpileGenericSealed(t *testing.T, src string) (string, error) {
	t.Helper()
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return transpiler.NewGalaToGoTranspiler(p, a, tr, g).Transpile(src, "main.gala")
}

// TestGenericSealedTypeIsRecognizedAsSealed covers a whole class of checks that
// silently stood down for a generic sealed type declared in `main` (or `test`).
//
// Every one of them keys off `getTypeMeta(matchedType.BaseName())`. The matched
// type came from the variant constructor's `Apply` return type, which the
// analyzer built as `NamedType{Package: "main", Name: "Maybe"}` — base name
// `main.Maybe`. Types in `main`/`test` are registered under their BARE name, so
// that lookup matched nothing and every caller concluded "not a sealed type".
//
// The visible symptom was exhaustiveness: a match listing every variant was
// rejected with GALA-E0003 ("must have a default case"). But the same miss also
// disabled GALA-E0002 and GALA-E0004, so each is pinned here — a fix that only
// silenced E0003 (say, by treating an unresolvable subject as exhaustive) would
// pass the first case and fail the rest.
func TestGenericSealedTypeIsRecognizedAsSealed(t *testing.T) {
	const maybeDecl = `package main

sealed type Maybe[T any] {
    case Just(V T)
    case Nothing
}

`

	t.Run("exhaustive match needs no default", func(t *testing.T) {
		out, err := transpileGenericSealed(t, maybeDecl+`func main() {
    val m = Just(42)
    val r = m match {
        case Just(v)   => s"just $v"
        case Nothing() => "nothing"
    }
    Println(r)
}`)
		require.NoError(t, err)
		// An exhaustive sealed match lowers with a synthetic unreachable
		// default rather than a user-supplied one.
		require.Contains(t, out, `panic("unreachable")`)
	})

	// The BARE zero-field spelling (`case Nothing` rather than `case Nothing()`)
	// is deliberately not covered here. It is a separate defect with its own
	// fix: a bare identifier naming a zero-field variant of a GENERIC parent
	// falls through to a variable binding. The two compose — that spelling over
	// an inferred generic subject needs both fixes — but pinning it from this
	// side would make this test fail for a reason it does not own.

	t.Run("missing variant reports E0002, not E0003", func(t *testing.T) {
		_, err := transpileGenericSealed(t, maybeDecl+`func main() {
    val m = Just(42)
    val r = m match {
        case Just(v) => s"just $v"
    }
    Println(r)
}`)
		require.Error(t, err)
		var se *galaerr.SemanticError
		require.ErrorAs(t, err, &se)
		require.Equal(t, galaerr.CodeNonExhaustiveMatch, se.Code,
			"a recognized sealed subject must name the missing variant, "+
				"not fall back to the generic missing-default error")
		require.Contains(t, se.Msg, "Nothing")
	})

	t.Run("variant arity is validated", func(t *testing.T) {
		_, err := transpileGenericSealed(t, `package main

sealed type Pair[T any] {
    case Both(A T, B T)
    case Neither
}

func main() {
    val m = Both(1, 2)
    val r = m match {
        case Both(a)   => s"both $a"
        case Neither() => "neither"
    }
    Println(r)
}`)
		require.Error(t, err)
		var se *galaerr.SemanticError
		require.ErrorAs(t, err, &se)
		require.Equal(t, galaerr.CodeVariantArityMismatch, se.Code)
	})

	t.Run("non-generic control", func(t *testing.T) {
		_, err := transpileGenericSealed(t, `package main

sealed type Sh {
    case Circle(R float64)
    case Empty
}

func main() {
    val m = Circle(1.0)
    val r = m match {
        case Circle(v) => s"circle $v"
        case Empty()   => "empty"
    }
    Println(r)
}`)
		require.NoError(t, err)
	})

	t.Run("std Option control", func(t *testing.T) {
		_, err := transpileGenericSealed(t, `package main

func main() {
    val m = Some(42)
    val r = m match {
        case Some(v) => s"some $v"
        case None()  => "none"
    }
    Println(r)
}`)
		require.NoError(t, err)
	})
}

// TestGenericSealedTypeInNamedPackage is the control that answers "why did
// std.Option work when a structurally identical user type did not".
//
// Nothing special-cases std. `std` is simply a NAMED package, so the qualified
// base name its Apply return type carries (`std.Option`) is also the key that
// type is registered under, and the lookup hit. A user-declared generic sealed
// type in any named package behaves identically — the defect was scoped to
// `main`/`test`, whose types are registered unqualified.
//
// This case also guards the fix's blast radius in the other direction: the
// qualifier must still be emitted for named packages, or cross-package sealed
// resolution would break instead.
func TestGenericSealedTypeInNamedPackage(t *testing.T) {
	searchRoot := t.TempDir()
	pkgDir := filepath.Join(searchRoot, "boxpkg")
	require.NoError(t, os.MkdirAll(pkgDir, 0o755))

	libSrc := `package boxpkg

sealed type Box[T any] {
    case Full(V T)
    case Empty
}

func MakeFull(v int) Box[int] = Full(v)
`
	require.NoError(t, os.WriteFile(filepath.Join(pkgDir, "box.gala"), []byte(libSrc), 0o600))

	mainDir := filepath.Join(searchRoot, "main")
	require.NoError(t, os.MkdirAll(mainDir, 0o755))
	mainSrc := `package main

import . "martianoff/gala/boxpkg"

func main() {
    val b = MakeFull(7)
    val r = b match {
        case Full(v) => s"full $v"
        case Empty() => "empty"
    }
    Println(r)
}
`
	mainPath := filepath.Join(mainDir, "main.gala")
	require.NoError(t, os.WriteFile(mainPath, []byte(mainSrc), 0o600))

	out, err := newDocGuardTranspilerWithPaths(searchRoot).Transpile(mainSrc, mainPath)
	require.NoError(t, err)
	require.Contains(t, out, `panic("unreachable")`,
		"a named-package generic sealed type must also be recognized as exhaustive")
}
