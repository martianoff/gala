package analyzer

import (
	"fmt"
	"strings"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// checkBuiltinNamesReachedFromGo reports GALA-E0107 for the first top-level
// function, val, var or embed val of root, a file's native tree, named like a
// Go predeclared identifier that the package's hand-written Go refers to:
// goRefs, from OwnGoBuiltinRefs.
//
// The transpiler emits such a declaration under a reserved name, so the
// generated Go that calls Go's builtin (`len`, `print`, …) still reaches it.
// Hand-written Go of the package spelling the name reaches Go's builtin too,
// not the GALA declaration, and nothing tells which one it meant.
func checkBuiltinNamesReachedFromGo(root native.GalaNodeRef, goRefs map[string]bool) error {
	if len(goRefs) == 0 {
		return nil
	}
	var err error
	forEachTopLevelNameNative(root, topLevelVisitor{name: func(name string, at transpiler.SourcePos, kind string) {
		if err != nil || !goRefs[name] || (kind != declKindFunction && kind != declKindValue) {
			return
		}
		err = galaerr.NewCodedSemanticError(galaerr.CodeBuiltinNameReachedFromGo, at.Line, at.Column,
			fmt.Sprintf("`%s` is named like Go's predeclared `%s`, which this package's hand-written Go refers to", name, name),
			fmt.Sprintf("rename it; the generated Go names it differently, so the package's .go files reach Go's `%s`, not this one", name)).
			WithSpan(at.Column + len([]rune(name)))
	}})
	return err
}

// embedValsNative adds the `embed val` declarations of root, a file's native
// tree, to vals, by name, each with its type as its directive resolves it
// (see embedDirective). It allocates vals when there is one and vals is nil,
// and returns it.
func embedValsNative(root native.GalaNodeRef, vals map[string]string) map[string]string {
	for decl := range nativeTopLevelDecls(root) {
		if decl.Kind() != native.GalaKIND_embedDeclaration {
			continue
		}
		ctx := native.EmbedDeclarationNode{GalaNodeRef: decl}
		if !ctx.Identifier().Ok() {
			continue
		}
		typeName := ""
		if typ := ctx.Type_(); typ.Ok() {
			typeName = galaparser.NativeTypeText(typ)
		}
		var patterns []string
		for _, s := range ctx.EmbedPatterns().AllSTRING() {
			patterns = append(patterns, strings.Trim(s.GetText(), "\""))
		}
		d := embedDirective(galaparser.NativeText(ctx.Identifier().GalaNodeRef), typeName, patterns)
		if vals == nil {
			vals = make(map[string]string)
		}
		vals[d.VarName] = d.TypeName
	}
	return vals
}
