package transformer

import (
	"strings"
	"testing"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/infer"

	"github.com/stretchr/testify/require"
)

// The function-derived half of the Hindley-Milner environment is converted
// once per file rather than once per expression inference, and shared by
// pointer between runs. These tests pin the two things that makes safe: the
// conversion is actually reused, and every state the conversion reads
// invalidates it — derived, where the state has a change token, and announced
// at the site, where it does not.

// funcTypeEnvFixture builds a transformer with a scope holding `x` and a
// function table shaped like a real package's.
func funcTypeEnvFixture(t testing.TB) *galaASTTransformer {
	t.Helper()
	tr := NewGalaASTTransformer().(*galaASTTransformer)
	tr.packageName = "main"
	tr.importManager = NewImportManager()
	tr.typeMetas = map[string]*transpiler.TypeMetadata{
		"a.Thing": {Name: "Thing"},
	}
	tr.typeAliases = map[string]transpiler.Type{}
	tr.functions = map[string]*transpiler.FunctionMetadata{
		"plain": {
			ParamTypes: []transpiler.Type{transpiler.BasicType{Name: "string"}},
			ReturnType: transpiler.BasicType{Name: "int"},
			TypeParams: nil,
		},
		"generic": {
			// A type parameter arrives as a plain name, which is what
			// substituteTypeParams rewrites into a fresh variable.
			ParamTypes: []transpiler.Type{transpiler.BasicType{Name: "T"}},
			ReturnType: transpiler.BasicType{Name: "T"},
			TypeParams: []string{"T"},
		},
	}
	tr.pushScope()
	tr.currentScope.valTypes["x"] = transpiler.BasicType{Name: "string"}
	return tr
}

func TestFunctionTypeEnvIsReusedUntilInvalidated(t *testing.T) {
	tr := funcTypeEnvFixture(t)

	first := tr.functionTypeEnv()
	second := tr.functionTypeEnv()

	// Same map: the whole point is that the signature conversion, the
	// generic-parameter substitution and the scheme allocations happen once.
	require.True(t, sameTypeEnv(first, second), "function environment was rebuilt on an unchanged transformer")

	// Reuse must not change what a caller observes.
	require.Equal(t, first["plain"], second["plain"])
	require.Equal(t, first["generic"], second["generic"])

	// Each state the conversion reads that has no change token of its own must
	// invalidate it. The import manager is absent from this table on purpose:
	// it carries a revision, and TestFunctionTypeEnvFollowsImportChanges
	// covers that.
	for name, invalidate := range map[string]func(){
		"typeMetas":   func() { tr.typeMetas["a.Other"] = &transpiler.TypeMetadata{Name: "Other"}; tr.invalidateTypeEnv() },
		"typeAliases": func() { tr.typeAliases["Alias"] = transpiler.BasicType{Name: "int"}; tr.invalidateTypeEnv() },
		"functions":   func() { tr.functions["added"] = &transpiler.FunctionMetadata{}; tr.invalidateTypeEnv() },
	} {
		before := tr.functionTypeEnv()
		invalidate()
		after := tr.functionTypeEnv()
		require.False(t, sameTypeEnv(before, after), "%s did not invalidate the function environment", name)
	}
}

// The import manager moves a revision on every mutation of its entry set, and
// the cache is stamped with the revision it was built at, so a site that adds,
// renames or drops an import cannot leave it stale by forgetting to say so.
// Nothing below announces anything: each case mutates the manager and reads the
// environment back, so removing the revision from either side of the compare,
// or the bump from a mutator, turns a case into a failure.
func TestFunctionTypeEnvFollowsImportChanges(t *testing.T) {
	// A signature that mentions an unqualified type, so the conversion has to
	// resolve the name against the import set.
	withThing := func(t *testing.T) *galaASTTransformer {
		t.Helper()
		tr := funcTypeEnvFixture(t)
		tr.functions["takesThing"] = &transpiler.FunctionMetadata{
			ParamTypes: []transpiler.Type{transpiler.NamedType{Name: "Thing"}},
			ReturnType: transpiler.BasicType{Name: "int"},
		}
		tr.invalidateTypeEnv()
		return tr
	}
	thingArg := func(t *testing.T, env infer.TypeEnv) string {
		t.Helper()
		return asTypeConst(t, asTypeApp(t, env["takesThing"].Type).Args[0]).Name
	}

	t.Run("an added import becomes visible", func(t *testing.T) {
		tr := withThing(t)
		require.Equal(t, "Thing", thingArg(t, tr.functionTypeEnv()),
			"setup: nothing is imported yet, so the name cannot resolve")

		tr.importManager.Add("example.com/a", "", false, "a")

		after := tr.functionTypeEnv()
		require.Equal(t, "a.Thing", thingArg(t, after),
			"the environment still holds the answer from before the import existed")
	})

	t.Run("a removed import stops resolving", func(t *testing.T) {
		tr := withThing(t)
		tr.importManager.Add("example.com/a", "", false, "a")
		require.Equal(t, "a.Thing", thingArg(t, tr.functionTypeEnv()), "setup: the import should resolve")

		entry, _ := tr.importManager.GetByPath("example.com/a")
		tr.importManager.removeEntry(entry)

		require.Equal(t, "Thing", thingArg(t, tr.functionTypeEnv()),
			"the environment still resolves a name against an import that is gone")
	})

	t.Run("a renamed import is visible", func(t *testing.T) {
		tr := withThing(t)
		tr.importManager.Add("example.com/a", "", false, "a")
		tr.typeMetas["aa.Thing"] = &transpiler.TypeMetadata{Name: "Thing"}
		require.Equal(t, "a.Thing", thingArg(t, tr.functionTypeEnv()), "setup: the pre-rename name should resolve")

		tr.importManager.UpdateActualPackageName("example.com/a", "aa")

		require.Equal(t, "aa.Thing", thingArg(t, tr.functionTypeEnv()),
			"the environment still resolves against the pre-rename package name")
	})

	t.Run("an unchanged import set keeps the environment", func(t *testing.T) {
		tr := withThing(t)
		tr.importManager.Add("example.com/a", "", false, "a")
		first := tr.functionTypeEnv()
		require.True(t, sameTypeEnv(first, tr.functionTypeEnv()),
			"setup: a second read with no import change should reuse the cache")
	})
}

// The state the conversion reads is written in four places during a transform,
// not at its entry. The import block is covered by the derived mechanism
// (above), so the three below announce the change themselves, and each of
// those is the only thing standing between a mid-walk write and a stale
// environment. So each case here drives the real site — the function the
// traversal actually calls, with a node parsed from source — and then reads the
// environment back, with no invalidation call of its own. Deleting the
// invalidateTypeEnv from any of them leaves the environment cached and fails
// the case that names it.
func TestFunctionTypeEnvIsRebuiltByTheSitesThatInvalidateIt(t *testing.T) {
	// prime builds the cache and returns the environment it produced, having
	// checked that it really is cached: a case that passed because the
	// environment was never reused would prove nothing.
	prime := func(t *testing.T) (*galaASTTransformer, infer.TypeEnv) {
		t.Helper()
		tr := funcTypeEnvFixture(t)
		env := tr.functionTypeEnv()
		require.True(t, sameTypeEnv(env, tr.functionTypeEnv()), "setup: the environment was not cached")
		return tr, env
	}
	// requireRebuilt asserts the next caller is handed a different environment.
	requireRebuilt := func(t *testing.T, tr *galaASTTransformer, before infer.TypeEnv, site string) {
		t.Helper()
		require.False(t, sameTypeEnv(before, tr.functionTypeEnv()),
			"%s did not invalidate the cached function environment", site)
	}

	t.Run("transformImportDeclaration", func(t *testing.T) {
		tr, before := prime(t)
		_, err := tr.transformImportDeclaration(importDeclarationContext(t, `import "example.com/a"`))
		require.NoError(t, err)
		requireRebuilt(t, tr, before, "transformImportDeclaration")
	})

	t.Run("the type alias branch of transformTypeDeclaration", func(t *testing.T) {
		tr, before := prime(t)
		_, err := tr.transformTypeDeclaration(typeDeclarationContext(t, "type Coord int"))
		require.NoError(t, err)
		require.Contains(t, tr.typeAliases, "Coord", "setup: the alias was not registered")
		requireRebuilt(t, tr, before, "the type alias branch of transformTypeDeclaration")
	})

	t.Run("registerStructMetaTypeMeta", func(t *testing.T) {
		tr, before := prime(t)
		tr.registerStructMetaTypeMeta("_StructMeta_Person", "main.Person")
		require.Contains(t, tr.typeMetas, "_StructMeta_Person", "setup: the metadata was not registered")
		requireRebuilt(t, tr, before, "registerStructMetaTypeMeta")
	})

	t.Run("registerEmbeddedFSMetadata", func(t *testing.T) {
		tr, before := prime(t)
		tr.registerEmbeddedFSMetadata()
		require.Contains(t, tr.typeMetas, transpiler.TypeEmbeddedFS, "setup: the metadata was not registered")
		requireRebuilt(t, tr, before, "registerEmbeddedFSMetadata")
	})
}

// sourceFileContext parses a one-declaration GALA file and returns its context,
// so a test can hand a real traversal function the node it would be given.
func sourceFileContext(t *testing.T, decl string) *grammar.SourceFileContext {
	t.Helper()
	tree, _, err := transpiler.NewAntlrGalaParser().Parse("package main\n\n" + decl + "\n")
	if err != nil {
		t.Fatalf("parsing %q: %v", decl, err)
	}
	sourceFile, ok := any(tree).(*grammar.SourceFileContext)
	if !ok {
		t.Fatalf("parse tree is %T, want *grammar.SourceFileContext", tree)
	}
	return sourceFile
}

func importDeclarationContext(t *testing.T, decl string) *grammar.ImportDeclarationContext {
	t.Helper()
	imports := sourceFileContext(t, decl).AllImportDeclaration()
	if len(imports) != 1 {
		t.Fatalf("%q yielded %d import declarations, want 1", decl, len(imports))
	}
	return imports[0].(*grammar.ImportDeclarationContext)
}

func typeDeclarationContext(t *testing.T, decl string) *grammar.TypeDeclarationContext {
	t.Helper()
	for _, top := range sourceFileContext(t, decl).AllTopLevelDeclaration() {
		if typeDecl, ok := top.TypeDeclaration().(*grammar.TypeDeclarationContext); ok {
			return typeDecl
		}
	}
	t.Fatalf("%q yielded no type declaration", decl)
	return nil
}

func TestFunctionTypeEnvRebuildsWhenScopeShadowsANormalizedName(t *testing.T) {
	tr := funcTypeEnvFixture(t)
	// A signature that mentions an unqualified type, so its conversion has to
	// resolve the name — and getType consults the scope chain first.
	tr.functions["takesThing"] = &transpiler.FunctionMetadata{
		ParamTypes: []transpiler.Type{transpiler.NamedType{Name: "Thing"}},
		ReturnType: transpiler.BasicType{Name: "int"},
	}
	tr.invalidateTypeEnv()

	first := tr.functionTypeEnv()
	// Nothing in scope is named Thing, so the conversion answered from
	// package metadata and a local binding could still change that answer.
	require.Contains(t, tr.funcTypeEnvNames, "Thing")
	unshadowed := asTypeConst(t, asTypeApp(t, first["takesThing"].Type).Args[0]).Name

	// Shadow the name with a local binding of a different type. The cached
	// conversion was produced with the other answer, so it must not be reused.
	tr.currentScope.valTypes["Thing"] = transpiler.BasicType{Name: "bool"}
	rebuilt := tr.functionTypeEnv()
	require.False(t, sameScheme(first["takesThing"], rebuilt["takesThing"]),
		"a local binding shadowing a normalized type name did not force a rebuild")
	require.Equal(t, "bool", asTypeConst(t, asTypeApp(t, rebuilt["takesThing"].Type).Args[0]).Name,
		"the rebuilt environment did not pick up the shadowing binding, which was %q", unshadowed)
}

// A build that happened while a local shadowed a normalized type name must
// not survive the shadow: the next caller, back in a clean scope, would
// otherwise inherit the local's type in every signature mentioning the name.
// Before this cache existed the environment was rebuilt per call, so this is
// the case the cache has to be careful about rather than the common one.
func TestFunctionTypeEnvNotReusedAfterShadowEnds(t *testing.T) {
	sig := &transpiler.FunctionMetadata{
		ParamTypes: []transpiler.Type{transpiler.NamedType{Name: "Thing"}},
		ReturnType: transpiler.BasicType{Name: "int"},
	}

	// The answer an unshadowed scope gives.
	clean := funcTypeEnvFixture(t)
	clean.functions["takesThing"] = sig
	clean.invalidateTypeEnv()
	want := asTypeConst(t, asTypeApp(t, clean.functionTypeEnv()["takesThing"].Type).Args[0]).Name

	// The same transformer, but the first build happens under a shadow.
	tr := funcTypeEnvFixture(t)
	tr.functions["takesThing"] = sig
	tr.invalidateTypeEnv()
	tr.pushScope()
	tr.currentScope.valTypes["Thing"] = transpiler.BasicType{Name: "bool"}
	shadowed := asTypeConst(t, asTypeApp(t, tr.functionTypeEnv()["takesThing"].Type).Args[0]).Name
	require.Equal(t, "bool", shadowed,
		"the shadowed build should answer from the local binding")
	tr.popScope()

	got := asTypeConst(t, asTypeApp(t, tr.functionTypeEnv()["takesThing"].Type).Args[0]).Name
	require.Equal(t, want, got,
		"an environment built under a shadow was reused after the shadow was popped")
}

// Under GALA_TRACE_TYPES the environment is neither memoized nor cached, so
// trace output keeps the per-occurrence multiplicity it had before the cache.
func TestFunctionTypeEnvIsNeitherMemoizedNorCachedUnderTracing(t *testing.T) {
	tr := funcTypeEnvFixture(t)
	tr.functions["takesThing"] = &transpiler.FunctionMetadata{
		ParamTypes: []transpiler.Type{transpiler.NamedType{Name: "Thing"}},
		ReturnType: transpiler.BasicType{Name: "int"},
	}
	tr.invalidateTypeEnv()
	tr.traceTypeResolution = true

	first := tr.functionTypeEnv()
	afterFirst := countTraceName(tr, "Thing")
	second := tr.functionTypeEnv()
	afterSecond := countTraceName(tr, "Thing")

	require.False(t, sameTypeEnv(first, second),
		"tracing must rebuild the environment so each resolution is recorded")
	require.Nil(t, tr.funcTypeEnv,
		"an environment built under tracing must not be stored for reuse")
	require.Equal(t, 1, afterFirst, "one call should trace one resolution of Thing")
	require.Equal(t, 2, afterSecond,
		"the second call recorded no resolution, so traces lose per-occurrence multiplicity")
}

func TestFunctionTypeEnvStaysReusedWithoutShadowing(t *testing.T) {
	tr := funcTypeEnvFixture(t)
	tr.invalidateTypeEnv()
	tr.functionTypeEnv()

	// A scope full of ordinary local names, none of which any signature
	// mentions, must not cost the cache its reuse. This is the hot path:
	// buildTypeEnv runs once per expression inference.
	tr.pushScope()
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		tr.currentScope.valTypes[name] = transpiler.BasicType{Name: "int"}
	}
	before := tr.functionTypeEnv()
	require.True(t, sameTypeEnv(before, tr.functionTypeEnv()))
}

func TestBuildTypeEnvLetsFunctionNamesWinOverLocalBindings(t *testing.T) {
	tr := funcTypeEnvFixture(t)
	// A local binding that collides with a function name. The function half
	// is written second and used to overwrite the scope half; that order is
	// load-bearing, because the two halves are now built separately and
	// merged.
	tr.currentScope.valTypes["plain"] = transpiler.BasicType{Name: "bool"}
	tr.invalidateTypeEnv()

	env := tr.buildTypeEnv()
	// plain: (string) int from the function table, not bool from the scope.
	require.True(t, sameScheme(tr.funcTypeEnv["plain"], env["plain"]),
		"a function name must win over a same-named local binding")
	require.Equal(t, "string", asTypeConst(t, asTypeApp(t, tr.funcTypeEnv["plain"].Type).Args[0]).Name)
}

func TestBuildTypeEnvKeepsDistinctScopesDistinct(t *testing.T) {
	tr := funcTypeEnvFixture(t)

	outer := tr.buildTypeEnv()
	outer["only-outer"] = &infer.Scheme{Type: &infer.TypeConst{Name: "marker"}}

	tr.pushScope()
	tr.currentScope.valTypes["x"] = transpiler.BasicType{Name: "int"}
	inner := tr.buildTypeEnv()

	// The scope half is rebuilt per call, so a binding added after the
	// cached function half was built is visible, and the outer-only entry
	// does not leak in.
	require.Equal(t, "int", asTypeConst(t, inner["x"].Type).Name,
		"the innermost binding must win")
	require.NotContains(t, inner, "only-outer")

	// The function half is shared between the two calls, unchanged.
	require.True(t, sameScheme(tr.funcTypeEnv["plain"], outer["plain"]))
}

func TestCachedGenericSchemeInstantiatesFreshPerUse(t *testing.T) {
	tr := funcTypeEnvFixture(t)

	// A cached generic scheme is a template. Each use must instantiate it to
	// fresh variables, or the second use of a generic function in the same
	// expression would be forced to agree with the first.
	env := tr.buildTypeEnv()
	tr.addBuiltinsToEnv(env)

	firstUse, err := tr.inferer.Infer(env, &infer.App{
		Fn:  &infer.Var{Name: "generic"},
		Arg: &infer.Lit{Type: &infer.TypeConst{Name: "int"}},
	})
	require.NoError(t, err)

	secondUse, err := tr.inferer.Infer(env, &infer.App{
		Fn:  &infer.Var{Name: "generic"},
		Arg: &infer.Lit{Type: &infer.TypeConst{Name: "string"}},
	})
	require.NoError(t, err)

	require.Equal(t, "int", firstUse.String())
	require.Equal(t, "string", secondUse.String(),
		"a cached generic scheme leaked its quantified variable between uses")
}

func TestBuiltinsToEnvSharesOneSetOfSchemes(t *testing.T) {
	tr := NewGalaASTTransformer().(*galaASTTransformer)

	first := infer.TypeEnv{}
	second := infer.TypeEnv{}
	tr.addBuiltinsToEnv(first)
	tr.addBuiltinsToEnv(second)

	require.Len(t, first, 10)
	require.True(t, sameTypeEnv(first, second),
		"builtin operator schemes are rebuilt per call instead of shared")
}

// sameTypeEnv reports whether two environments hold the same schemes, compared
// by identity. Identity is the point: infer never writes through a Scheme, so
// sharing one is what makes reuse safe.
func sameTypeEnv(a, b infer.TypeEnv) bool {
	if len(a) != len(b) {
		return false
	}
	for name, scheme := range a {
		if b[name] != scheme {
			return false
		}
	}
	return true
}

// sameScheme is sameTypeEnv for a single entry.
func sameScheme(a, b *infer.Scheme) bool { return a == b }

// countTraceName counts recorded type-resolution events for one unqualified
// name. getType tags each event with how it answered, so the suffix is
// matched rather than the whole method string.
func countTraceName(tr *galaASTTransformer, name string) int {
	n := 0
	for _, entry := range tr.typeTraces {
		if strings.HasSuffix(entry.Method, ":"+name) {
			n++
		}
	}
	return n
}

// asTypeConst asserts that typ is a type constant and returns it, so the
// assertions above read as one line. The distinction between "the const we
// expected" and "some other type" is the whole test.
func asTypeApp(t *testing.T, typ infer.Type) *infer.TypeApp {
	t.Helper()
	a, ok := typ.(*infer.TypeApp)
	require.Truef(t, ok, "expected a type application, got %T (%s)", typ, typ)
	return a
}

func asTypeConst(t *testing.T, typ infer.Type) *infer.TypeConst {
	t.Helper()
	c, ok := typ.(*infer.TypeConst)
	require.Truef(t, ok, "expected a type constant, got %T (%s)", typ, typ)
	return c
}

// BenchmarkBuildTypeEnv measures the per-expression cost of assembling the
// inference environment. It includes the builtin insert because both call
// sites do it immediately afterwards, and because the map is sized to
// absorb it: benchmarking buildTypeEnv alone would charge that sizing as a
// regression while hiding the rehash it removes.
func BenchmarkBuildTypeEnv(b *testing.B) {
	tr := funcTypeEnvFixture(b)
	b.ReportAllocs()
	for b.Loop() {
		env := tr.buildTypeEnv()
		tr.addBuiltinsToEnv(env)
		if len(env) == 0 {
			b.Fatal("empty environment")
		}
	}
}

// BenchmarkFunctionTypeEnvUncached is the cost the cache removes: converting
// the function half from scratch, as buildTypeEnv did before it was cached.
func BenchmarkFunctionTypeEnvUncached(b *testing.B) {
	tr := funcTypeEnvFixture(b)
	b.ReportAllocs()
	for b.Loop() {
		tr.invalidateTypeEnv()
		if env := tr.functionTypeEnv(); len(env) == 0 {
			b.Fatal("empty environment")
		}
	}
}
