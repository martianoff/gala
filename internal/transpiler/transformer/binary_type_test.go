package transformer

import (
	"go/ast"
	"go/token"
	"testing"

	"martianoff/gala/internal/transpiler"
)

// Go types a mixed arithmetic expression by its TYPED operand: `15 *
// time.Second` is a Duration, not an int. Inference took the left operand
// unconditionally, so every consumer of it — inlay hints, hover, downstream
// unification — saw the untyped constant's default instead.
func TestIsUntypedConstExpr(t *testing.T) {
	lit := func(kind token.Token, v string) ast.Expr { return &ast.BasicLit{Kind: kind, Value: v} }
	sel := &ast.SelectorExpr{X: ast.NewIdent("time"), Sel: ast.NewIdent("Second")}

	for _, tt := range []struct {
		name string
		expr ast.Expr
		want bool
	}{
		{"int literal", lit(token.INT, "15"), true},
		{"float literal", lit(token.FLOAT, "1.5"), true},
		{"string literal", lit(token.STRING, `"x"`), true},
		{"negated literal", &ast.UnaryExpr{Op: token.SUB, X: lit(token.INT, "3")}, true},
		{"parenthesized literal", &ast.ParenExpr{X: lit(token.INT, "3")}, true},
		{"literal arithmetic", &ast.BinaryExpr{X: lit(token.INT, "2"), Op: token.MUL, Y: lit(token.INT, "3")}, true},
		{"qualified constant", sel, false},
		{"identifier", ast.NewIdent("x"), false},
		{"call", &ast.CallExpr{Fun: ast.NewIdent("f")}, false},
		{"mixed arithmetic", &ast.BinaryExpr{X: lit(token.INT, "2"), Op: token.MUL, Y: sel}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := isUntypedConstExpr(tt.expr); got != tt.want {
				t.Errorf("isUntypedConstExpr = %v, want %v", got, tt.want)
			}
		})
	}
}

// A shift takes its LEFT operand's type whatever the count is: `1 << n` is an
// int, not whatever integer type n happens to be. The count here is a variable,
// which is the only shape where the wrong rule could show — a constant count
// would make the whole expression constant and settle it that way regardless.
func TestShiftKeepsLeftOperandType(t *testing.T) {
	tr := NewGalaASTTransformer().(*galaASTTransformer)
	lit := &ast.BasicLit{Kind: token.INT, Value: "1"}
	count := ast.NewIdent("n")

	for _, op := range []token.Token{token.SHL, token.SHR} {
		got := tr.arithmeticResultType(&ast.BinaryExpr{X: lit, Op: op, Y: count})
		if got.String() != "int" {
			t.Errorf("%s: got %s, want the left operand's int", op, got.String())
		}
	}
}

// An expression built purely from untyped constants has a default type of its
// own, and the kinds combine by width: `1 * 1.5` is a float64, not the left
// operand's int.
func TestUntypedConstantArithmeticCombinesKinds(t *testing.T) {
	tr := &galaASTTransformer{exprTypeCache: map[ast.Expr]transpiler.Type{}}
	got := tr.arithmeticResultType(&ast.BinaryExpr{
		X:  &ast.BasicLit{Kind: token.INT, Value: "1"},
		Op: token.MUL,
		Y:  &ast.BasicLit{Kind: token.FLOAT, Value: "1.5"},
	})
	if got.String() != "float64" {
		t.Errorf("got %s, want float64", got.String())
	}
}

// The typed operand only wins when it actually resolves. An unresolvable one
// must not cost the untyped constant its concrete default: both inference
// paths would otherwise degrade `2 * mystery()` from int to nothing at all —
// and getExprType's "nothing at all" is the `any` this transpiler must never
// emit into generated Go.
func TestUnresolvableOperandKeepsTheConstantsDefault(t *testing.T) {
	tr := NewGalaASTTransformer().(*galaASTTransformer)
	unresolvable := &ast.CallExpr{Fun: ast.NewIdent("mystery")}
	e := &ast.BinaryExpr{X: &ast.BasicLit{Kind: token.INT, Value: "2"}, Op: token.MUL, Y: unresolvable}

	if got := tr.arithmeticResultType(e); got.String() != "int" {
		t.Errorf("arithmeticResultType = %s, want the constant's int default", got.String())
	}
	got := tr.getExprType(e)
	if id, ok := got.(*ast.Ident); !ok || id.Name != "int" {
		t.Errorf("getExprType = %#v, want the constant's int default", got)
	}
}

func TestExprTypeCacheReset(t *testing.T) {
	tr := NewGalaASTTransformer().(*galaASTTransformer)
	oldExpr := ast.NewIdent("old")
	tr.exprTypeCache[oldExpr] = transpiler.BasicType{Name: "int"}
	tr.resetExprTypeCache()
	if len(tr.exprTypeCache) != 0 {
		t.Fatalf("cache has %d entries after reset", len(tr.exprTypeCache))
	}
	tr.exprTypeCache[ast.NewIdent("new")] = transpiler.BasicType{Name: "string"}
	tr.resetExprTypeCache()
	if len(tr.exprTypeCache) != 0 {
		t.Fatalf("cache has %d entries after second reset", len(tr.exprTypeCache))
	}
}

// An unqualified name resolves against the import set as it stood when the
// snapshot was taken, so a cache that is not tied to that set is a correctness
// dependency nothing holds up. Validity is derived instead of announced: the
// snapshot records the import manager's revision and is rebuilt when it moves.
//
// So every case below mutates the manager and reads the answer back, with no
// invalidation call of its own to forget. That is what makes them worth
// having: dropping the revision check, or the bump in any one mutator, turns
// the resolution into a miss. The subtests are the three mutators that can
// change what a name resolves to, plus the caching the whole thing exists for.
func TestCachedTypeResolverLifecycle(t *testing.T) {
	// fixture builds a transformer with no imports, so every qualified name in
	// the subtests resolves only through the import the subtest adds.
	fixture := func() (*galaASTTransformer, func(string) bool) {
		tr := NewGalaASTTransformer().(*galaASTTransformer)
		tr.packageName = "main"
		tr.importManager = NewImportManager()
		tr.typeMetas = make(map[string]*transpiler.TypeMetadata)
		return tr, func(name string) bool {
			_, ok := tr.typeMetas[name]
			return ok
		}
	}

	t.Run("an added import is visible", func(t *testing.T) {
		tr, exists := fixture()
		if _, ok := tr.tryResolveSimpleName("Thing", exists); ok {
			t.Fatal("resolved with nothing imported")
		}
		tr.importManager.Add("example.com/a", "", true, "a")
		tr.typeMetas["a.Thing"] = &transpiler.TypeMetadata{Name: "Thing"}
		if got, ok := tr.tryResolveSimpleName("Thing", exists); !ok || got != "a.Thing" {
			t.Fatalf("after Add = %q, %v; want a.Thing, true", got, ok)
		}
	})

	t.Run("a renamed import is visible", func(t *testing.T) {
		tr, exists := fixture()
		tr.importManager.Add("example.com/a", "", true, "a")
		tr.typeMetas["a.Thing"] = &transpiler.TypeMetadata{Name: "Thing"}
		if _, ok := tr.tryResolveSimpleName("Thing", exists); !ok {
			t.Fatal("setup: expected a.Thing to resolve")
		}
		// PkgName is what the resolver matches on, and the alias index still
		// says "a", so a rename is invisible to anything reading either index.
		tr.importManager.UpdateActualPackageName("example.com/a", "aa")
		tr.typeMetas["aa.Thing"] = &transpiler.TypeMetadata{Name: "Thing"}
		if got, ok := tr.tryResolveSimpleName("Thing", exists); !ok || got != "aa.Thing" {
			t.Fatalf("after rename = %q, %v; want aa.Thing, true", got, ok)
		}
	})

	t.Run("a removed import stops resolving", func(t *testing.T) {
		tr, exists := fixture()
		tr.importManager.Add("example.com/a", "", true, "a")
		tr.typeMetas["a.Thing"] = &transpiler.TypeMetadata{Name: "Thing"}
		if _, ok := tr.tryResolveSimpleName("Thing", exists); !ok {
			t.Fatal("setup: expected a.Thing to resolve")
		}
		// removeEntry is unexported and today reachable only from Add, which
		// would bump on its own account and prove nothing about this path.
		// Calling it here is the point: the mutator is what has to move the
		// revision, not whoever happens to call it. PruneUnused, the other
		// place a pruned import could plausibly have been removed from the
		// manager, rewrites the generated file and leaves the manager alone.
		entry, _ := tr.importManager.GetByPath("example.com/a")
		tr.importManager.removeEntry(entry)
		if got, ok := tr.tryResolveSimpleName("Thing", exists); ok {
			t.Fatalf("after removeEntry = %q, true; want a miss", got)
		}
	})

	t.Run("an unchanged import set keeps the snapshot", func(t *testing.T) {
		tr, exists := fixture()
		tr.importManager.Add("example.com/a", "", true, "a")
		tr.typeMetas["a.Thing"] = &transpiler.TypeMetadata{Name: "Thing"}
		tr.tryResolveSimpleName("Thing", exists)
		built := tr.cachedTypeResolver
		if built == nil {
			t.Fatal("no snapshot was cached")
		}
		tr.tryResolveSimpleName("Thing", exists)
		if tr.cachedTypeResolver != built {
			t.Fatal("snapshot rebuilt although no import changed")
		}
	})
}

func TestTypeNameMemoIsBuildLocal(t *testing.T) {
	tr := NewGalaASTTransformer().(*galaASTTransformer)
	tr.packageName = "main"
	tr.importManager = NewImportManager()
	tr.importManager.Add("example.com/a", "", true, "a")
	tr.typeMetas = make(map[string]*transpiler.TypeMetadata)

	memo := &typeNameMemo{}
	if got := tr.normalizeTypeNameMemoized("Thing", memo); got != "Thing" {
		t.Fatalf("initial normalization = %q, want Thing", got)
	}
	tr.typeMetas["a.Thing"] = &transpiler.TypeMetadata{Name: "Thing"}
	if got := tr.normalizeTypeNameMemoized("Thing", memo); got != "Thing" {
		t.Fatalf("memoized normalization = %q, want Thing", got)
	}
	if got := tr.normalizeTypeNameMemoized("Thing", &typeNameMemo{}); got != "a.Thing" {
		t.Fatalf("fresh normalization = %q, want a.Thing", got)
	}
}
