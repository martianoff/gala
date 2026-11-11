package transformer

import (
	"go/ast"
	"go/token"
	"go/types"
	"regexp"
	"strconv"
	"strings"

	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler/registry"
)

// Generated code names things a GALA program may also declare: std's
// package qualifier (`std.Some`), Go's predeclared identifiers (`panic`,
// `string`, `int`, `nil`, …) and the temporaries the transformer introduces
// (`_tmp_1`, `__p0`). A local binding of one of those names would capture the
// generated reference, so:
//
//   - a local binding named like std's qualifier or a Go predeclared
//     identifier is emitted under a reserved name (localGoName); and
//   - a temporary skips every name the file's own source spells
//     (freshTempName), so it never hides a user binding.
//
// fmt is handled where it is used instead (see fmtPackage).

// localNamePrefix starts the Go name of a local binding renamed by
// localGoName. The `_gala_` prefix is the transpiler's (see fmtAlias).
const localNamePrefix = "_gala_"

// shadowsGeneratedName reports whether a local binding named name would hide
// a name generated code refers to: std's qualifier or a Go predeclared
// identifier (a type, constant or builtin of Go's universe scope).
func shadowsGeneratedName(name string) bool {
	return name == registry.StdPackageName || types.Universe.Lookup(name) != nil
}

// localGoName is the Go name of a local binding named name.
func localGoName(name string) string {
	if shadowsGeneratedName(name) {
		return localNamePrefix + name
	}
	return name
}

// markLocalRef records id, a reference to a binding of the GALA source, for
// renameShadowingLocals when the binding is a local one whose name
// shadowsGeneratedName. A package-level binding keeps its name.
func (t *galaASTTransformer) markLocalRef(id *ast.Ident) {
	if !shadowsGeneratedName(id.Name) {
		return
	}
	if s := t.bindingScope(id.Name); s != nil && s.parent != nil {
		t.recordLocalRef(id)
	}
}

// recordLocalRef records id, a reference to a local binding, for
// renameShadowingLocals. Generated code that names a parameter of the
// function being lowered (see tco.go) records its reference here too.
func (t *galaASTTransformer) recordLocalRef(id *ast.Ident) {
	if t.localRefs == nil {
		t.localRefs = make(map[*ast.Ident]bool)
	}
	t.localRefs[id] = true
}

// renameShadowingLocals gives every local binding of decl, a declaration
// lowered from a function, method, val or var of the source, whose name
// shadowsGeneratedName its localGoName: where it is declared — a parameter,
// result, receiver or local variable — and where the source refers to it
// (markLocalRef). A generated reference of the same name is not marked and
// keeps meaning std or Go's predeclared identifier.
//
// Every local such declaration in decl is the source's: generated code
// declares no local of such a name.
func (t *galaASTTransformer) renameShadowingLocals(decl ast.Decl) {
	rename := func(id *ast.Ident) {
		if id != nil {
			id.Name = localGoName(id.Name)
		}
	}
	renameFields := func(fl *ast.FieldList) {
		if fl == nil {
			return
		}
		for _, f := range fl.List {
			for _, n := range f.Names {
				rename(n)
			}
		}
	}
	visit := func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Ident:
			if t.localRefs[n] {
				rename(n)
			}
		case *ast.FuncType:
			renameFields(n.Params)
			renameFields(n.Results)
		case *ast.AssignStmt:
			if n.Tok == token.DEFINE {
				for _, lhs := range n.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						rename(id)
					}
				}
			}
		case *ast.RangeStmt:
			if n.Tok == token.DEFINE {
				for _, e := range []ast.Expr{n.Key, n.Value} {
					if id, ok := e.(*ast.Ident); ok {
						rename(id)
					}
				}
			}
		case *ast.DeclStmt:
			if gd, ok := n.Decl.(*ast.GenDecl); ok && gd.Tok == token.VAR {
				for _, spec := range gd.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok {
						for _, name := range vs.Names {
							rename(name)
						}
					}
				}
			}
		}
		return true
	}
	switch d := decl.(type) {
	case *ast.FuncDecl:
		renameFields(d.Recv)
		ast.Inspect(d.Type, visit)
		if d.Body != nil {
			ast.Inspect(d.Body, visit)
		}
	case *ast.GenDecl:
		// A package-level val or var keeps its name; only what its
		// initializer binds is local.
		for _, spec := range d.Specs {
			if vs, ok := spec.(*ast.ValueSpec); ok {
				for _, v := range vs.Values {
					ast.Inspect(v, visit)
				}
			}
		}
	}
}

// genericMethodFuncName names the free function a generic method lowers to:
// `Box_Map` for method Map of type Box, `pkg.Box_Map` for typeName `pkg.Box`.
// While a function of the type's package has that name, `_` is appended, so
// the declaration and every call site, which read the same function
// metadata, agree on a name that collides with none.
func (t *galaASTTransformer) genericMethodFuncName(typeName, method string) string {
	pkg, bare, qualified := strings.Cut(typeName, ".")
	if !qualified {
		pkg, bare = t.packageName, typeName
	}
	name := bare + "_" + method
	for t.functions[ownFunctionKey(pkg, name)] != nil {
		name += "_"
	}
	if qualified {
		return pkg + "." + name
	}
	return name
}

// loweredMethodNames returns the method names the part after the type in the
// name of a generic method's free function may stand for: itself, and, for
// one genericMethodFuncName gave `_` suffixes, the name without them.
func loweredMethodNames(part string) []string {
	if trimmed := strings.TrimRight(part, "_"); trimmed != part && trimmed != "" {
		return []string{part, trimmed}
	}
	return []string{part}
}

// declaresCode reports whether the top-level declaration td is a function,
// method, val or var: one whose lowering holds the source's code, and so its
// local bindings (see renameShadowingLocals). What a type declaration lowers
// to is generated.
func declaresCode(td native.GalaNodeRef) bool {
	decl, ok := firstKidNode(td)
	if !ok {
		return false
	}
	switch decl.Kind() {
	case native.GalaKIND_functionDeclaration, native.GalaKIND_valDeclaration, native.GalaKIND_varDeclaration:
		return true
	}
	return false
}

// tempNamePattern matches the names the transformer gives its temporaries.
var tempNamePattern = regexp.MustCompile(`\b(?:_tmp_|__p)\d+\b`)

// sourceTempNames collects the names of src, the file's source, that are
// spelled like a temporary of the transformer's, which freshTempName then
// skips; nil for a source that spells none, as nearly every one. A binding
// declared elsewhere (a sibling file's val) is hidden only where it is
// referenced, and a reference spells it here.
func sourceTempNames(src string) map[string]bool {
	if !strings.Contains(src, "_tmp_") && !strings.Contains(src, "__p") {
		return nil
	}
	names := make(map[string]bool)
	for _, m := range tempNamePattern.FindAllString(src, -1) {
		names[m] = true
	}
	return names
}

// freshTempName returns prefix followed by the next value of *counter that
// spells no name of the file's source, advancing the counter past it.
func (t *galaASTTransformer) freshTempName(prefix string, counter *int) string {
	for {
		*counter++
		name := prefix + strconv.Itoa(*counter)
		if !t.sourceTempNames[name] {
			return name
		}
	}
}
