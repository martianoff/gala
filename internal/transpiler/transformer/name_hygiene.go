package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"regexp"
	"strconv"
	"strings"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
)

// Generated code names things a GALA program may also declare: std's
// package qualifier (`std.Some`), Go's predeclared identifiers (`panic`,
// `string`, `int`, `nil`, …) and the temporaries the transformer introduces
// (`_tmp_1`, `__p0`, `__tuple_3`, `_zip`, `_err`, …). A binding of one of
// those names would capture the generated reference, so:
//
//   - a local binding named like std's qualifier or a Go predeclared
//     identifier is emitted under a reserved name (localGoName), and so is a
//     top-level function, val or var of the package (renameShadowingTopLevel);
//     and
//   - a name generated code binds or declares avoids every such name of the
//     source (generatedName, freshTempName), so it never hides a user
//     binding or collides with one.
//
// fmt is handled where it is used instead (see fmtPackage).

// localNamePrefix starts the Go name of a local binding renamed by
// localGoName. The `_gala_` prefix is the transpiler's (see fmtAlias): no
// GALA identifier may start with it (CheckReservedPrefixNative).
const localNamePrefix = transpiler.ReservedNamePrefix

// CheckReservedPrefixNative reports GALA-E0097 for the first identifier below
// root, a node of the native tree, that starts with localNamePrefix, or nil
// when none does. The prefix is the transpiler's: a user name spelled with it
// could meet a renamed local (localGoName) or another name the transpiler
// emits. The analyzer checks each source file with it. An interpolated
// expression needs no check: no binding can carry the prefix, so a name with
// it there is undefined (GALA-E0023).
func CheckReservedPrefixNative(root native.GalaNodeRef) error {
	id := firstIdentifier(root, func(name string) bool { return strings.HasPrefix(name, localNamePrefix) })
	if !id.Ok() {
		return nil
	}
	name := galaparser.NativeText(id)
	line, col := srcNode{node: id}.start()
	return galaerr.NewCodedSemanticError(galaerr.CodeReservedNamePrefix, line, col,
		fmt.Sprintf("identifier %s starts with %s, a prefix reserved for names the transpiler generates", name, localNamePrefix),
		fmt.Sprintf("rename it so it does not start with %s", localNamePrefix))
}

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

// markSourceRef records id, a reference to a binding of the GALA source whose
// name shadowsGeneratedName: for renameShadowingLocals when the binding is a
// local one, and for renameShadowingTopLevel when it is a function, val or
// var of the package (isTopLevelShadower).
func (t *galaASTTransformer) markSourceRef(id *ast.Ident) {
	if !shadowsGeneratedName(id.Name) {
		return
	}
	if s := t.bindingScope(id.Name); s != nil && s.parent != nil {
		t.recordLocalRef(id)
	} else {
		t.markTopLevelRef(id)
	}
}

// markTopLevelRef records id, a reference to a top-level declaration of the
// package, for renameShadowingTopLevel when its name is one
// (isTopLevelShadower).
func (t *galaASTTransformer) markTopLevelRef(id *ast.Ident) {
	if !t.isTopLevelShadower(id.Name) {
		return
	}
	if t.topLevelRefs == nil {
		t.topLevelRefs = make(map[*ast.Ident]bool)
	}
	t.topLevelRefs[id] = true
}

// isTopLevelShadower reports whether name shadowsGeneratedName and is a
// function, val or var declared at the top level of the package, in this
// file or another, embed vals included. In Go such a declaration
// hides the predeclared identifier in every file of the package, so it is
// emitted under its localGoName, and so is every reference to it (renameShadowingTopLevel). The name is
// unexported, so no other package refers to it.
func (t *galaASTTransformer) isTopLevelShadower(name string) bool {
	if !shadowsGeneratedName(name) {
		return false
	}
	if t.richAST != nil && (t.richAST.PackageVals[name] != nil || t.richAST.PackageEmbedVals[name] != "") {
		return true
	}
	fm := t.functions[ownFunctionKey(t.packageName, name)]
	return fm != nil && fm.Package == t.packageName
}

// renameShadowingTopLevel gives the package's top-level functions, vals and
// vars that shadowsGeneratedName (isTopLevelShadower), among decls, the file's
// lowered declarations, their localGoName, where they are declared and where
// the source refers to them (markSourceRef). A generated reference of the same
// name is not marked and keeps meaning std or Go's predeclared identifier.
func (t *galaASTTransformer) renameShadowingTopLevel(decls []ast.Decl) {
	for _, decl := range decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				t.markTopLevelRef(d.Name)
			}
		case *ast.GenDecl:
			if d.Tok == token.VAR || d.Tok == token.CONST {
				for _, spec := range d.Specs {
					for _, name := range spec.(*ast.ValueSpec).Names {
						t.markTopLevelRef(name)
					}
				}
			}
		}
	}
	if len(t.topLevelRefs) == 0 {
		return
	}
	for _, decl := range decls {
		ast.Inspect(decl, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && t.topLevelRefs[id] {
				id.Name = localGoName(id.Name)
			}
			return true
		})
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
// (markSourceRef). A generated reference of the same name is not marked and
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

// sourceNamePattern matches the identifiers spelled with a leading
// underscore: the shape of every name generated code binds or declares for
// itself (`_tmp_1`, `__p0`, `__tuple_3`, `_zip`, `_err`, …). A lone `_` is
// none.
var sourceNamePattern = regexp.MustCompile(`\b_\w+`)

// sourceGenNames collects the names of src, the file's source, spelled like
// a name generated code binds or declares, which such a name then avoids
// (see userSpells); nil for a source that spells none. A binding declared
// elsewhere (a sibling file's val) is hidden only where it is referenced,
// and a reference spells it here.
func sourceGenNames(src string) map[string]bool {
	if strings.IndexByte(src, '_') < 0 {
		return nil
	}
	matches := sourceNamePattern.FindAllString(src, -1)
	if len(matches) == 0 {
		return nil
	}
	names := make(map[string]bool, len(matches))
	for _, m := range matches {
		names[m] = true
	}
	return names
}

// userSpells reports whether name, spelled like a name generated code binds
// or declares, is also the source's: spelled in the file's source, or
// declared at package level by any file of the package. A generated binding
// or declaration of that name would hide it or collide with it.
func (t *galaASTTransformer) userSpells(name string) bool {
	if t.sourceGenNames[name] {
		return true
	}
	key := ownFunctionKey(t.packageName, name)
	return t.functions[key] != nil || t.typeMetas[key] != nil ||
		t.richAST != nil && (t.richAST.PackageVals[name] != nil || t.richAST.PackageEmbedVals[name] != "")
}

// generatedName is the Go name of name, a fixed name generated code binds or
// declares (`_zip`, `_err`, `__tuple_x`, …): name itself, or, where the
// source has a name spelled so (userSpells), name under the reserved prefix,
// which no name of the source has (GALA-E0097). Every fixed generated name
// is chosen here, and every numbered one by freshTempName, so programs that
// spell no such name get the same Go as ever.
func (t *galaASTTransformer) generatedName(name string) string {
	if t.userSpells(name) {
		return localNamePrefix + name
	}
	return name
}

// isTransformerTemp reports whether name is a temporary nextTempVar made,
// not a binding of the source spelled like one (which a temporary skips).
func (t *galaASTTransformer) isTransformerTemp(name string) bool {
	return isTempVarName(name) && !t.sourceGenNames[name]
}

// freshTempName returns prefix followed by the next value of *counter that
// the file's source does not spell, advancing the counter past it. A numbered
// temporary is local, so it can hide only a name the file spells.
func (t *galaASTTransformer) freshTempName(prefix string, counter *int) string {
	for {
		*counter++
		name := prefix + strconv.Itoa(*counter)
		if !t.sourceGenNames[name] {
			return name
		}
	}
}
