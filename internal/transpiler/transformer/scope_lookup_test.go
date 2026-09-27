package transformer

import (
	"go/ast"
	"testing"

	"martianoff/gala/internal/transpiler"
)

// TestTypelessBindingLookup: a binding recorded without a type — a match
// binding over an uninferable scrutinee sets vals but not valTypes — resolves
// to NilType, never a nil interface, so callers can ask it IsNil().
func TestTypelessBindingLookup(t *testing.T) {
	tr := NewGalaASTTransformer().(*galaASTTransformer)
	tr.pushScope()
	defer tr.popScope()
	tr.currentScope.vals["g"] = false // how the default-case binding records it

	typ, isVal, bound := tr.scopeLookup("g")
	if !bound || isVal || typ == nil || !typ.IsNil() {
		t.Fatalf("scopeLookup(g) = (%v, %v, %v), want (NilType, false, true)", typ, isVal, bound)
	}
	if got := tr.getValType("g"); got == nil || !got.IsNil() {
		t.Fatalf("getValType(g) = %v, want NilType", got)
	}
	// Calling the binding (`g(1)`) must not crash the Apply-call rewrite.
	if _, handled := tr.tryTransformValWithApply(ast.NewIdent("g"), []ast.Expr{&ast.BasicLit{Value: "1"}}); handled {
		t.Fatalf("a typeless binding has no Apply to rewrite to")
	}
	// Only bindings with a recorded type count for the concurrency check.
	if _, ok := tr.lookupLocalBinding("g"); ok {
		t.Fatalf("lookupLocalBinding(g) reported a typeless binding as typed")
	}
}

// TestBindingRefOnlyFirstGet: only a `.Get()` directly on a val is its
// Immutable unwrap. In `opt.Get().Get()` the outer call is Option's own Get,
// so the expression does not read the binding `opt` any more.
func TestBindingRefOnlyFirstGet(t *testing.T) {
	tr := NewGalaASTTransformer().(*galaASTTransformer)
	tr.pushScope()
	defer tr.popScope()
	tr.addVal("opt", transpiler.GenericType{
		Base:   transpiler.NamedType{Package: "std", Name: "Option"},
		Params: []transpiler.Type{transpiler.NamedType{Name: "Box"}},
	})
	get := func(x ast.Expr) ast.Expr {
		return &ast.CallExpr{Fun: &ast.SelectorExpr{X: x, Sel: ast.NewIdent("Get")}}
	}

	if b, ok := tr.bindingRef(get(ast.NewIdent("opt"))); !ok || b.name != "opt" {
		t.Fatalf("opt.Get() should read the val opt, got (%+v, %v)", b, ok)
	}
	if b, ok := tr.bindingRef(get(get(ast.NewIdent("opt")))); ok {
		t.Fatalf("opt.Get().Get() is Option.Get on the unwrapped value, not a read of opt; got %+v", b)
	}
}

// TestScopeLookupShadowing: the innermost binding decides val-ness and type,
// and the per-scope flags follow the same binding.
func TestScopeLookupShadowing(t *testing.T) {
	tr := NewGalaASTTransformer().(*galaASTTransformer)
	tr.pushScope()
	tr.addVar("x", transpiler.BasicType{Name: "int"})
	tr.markMutable("x")
	tr.pushScope()
	tr.addVal("x", transpiler.BasicType{Name: "string"})

	if !tr.isVal("x") || tr.isVar("x") || tr.getValType("x").String() != "string" {
		t.Fatalf("inner val x should shadow the outer var")
	}
	if tr.isMutableVar("x") {
		t.Fatalf("an outer mutable var must not show through an inner val")
	}
	tr.popScope()
	if !tr.isVar("x") || !tr.isMutableVar("x") || tr.getValType("x").String() != "int" {
		t.Fatalf("outer var x should be visible again")
	}
	tr.popScope()
}
