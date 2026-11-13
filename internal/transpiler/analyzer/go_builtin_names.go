package analyzer

import (
	"fmt"
	"slices"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
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
	for decl := range nativeTopLevelDecls(root) {
		var ids []native.GalaNodeRef
		switch decl.Kind() {
		case native.GalaKIND_functionDeclaration:
			fn := native.FunctionDeclarationNode{GalaNodeRef: decl}
			if fn.Receiver().Ok() {
				continue
			}
			ids = append(ids, fn.Identifier().GalaNodeRef)
		case native.GalaKIND_embedDeclaration:
			ids = append(ids, native.EmbedDeclarationNode{GalaNodeRef: decl}.Identifier().GalaNodeRef)
		case native.GalaKIND_valDeclaration:
			vd := native.ValDeclarationNode{GalaNodeRef: decl}
			ids = declaredIdentifiers(vd.IdentifierList(), vd.TuplePattern())
		case native.GalaKIND_varDeclaration:
			vd := native.VarDeclarationNode{GalaNodeRef: decl}
			ids = declaredIdentifiers(vd.IdentifierList(), vd.TuplePattern())
		}
		for _, id := range ids {
			if !id.Ok() {
				continue
			}
			if name := galaparser.NativeText(id); goRefs[name] {
				at := nativePos(id)
				return galaerr.NewCodedSemanticError(galaerr.CodeBuiltinNameReachedFromGo, at.Line, at.Column,
					fmt.Sprintf("`%s` is named like Go's predeclared `%s`, which this package's hand-written Go refers to", name, name),
					fmt.Sprintf("rename it; the generated Go names it differently, so the package's .go files reach Go's `%s`, not this one", name)).
					WithSpan(at.Column + len([]rune(name)))
			}
		}
	}
	return nil
}

// declaredIdentifiers is the identifiers a val or var declaration binds:
// those of its identifier list, or of its tuple pattern.
func declaredIdentifiers(list native.IdentifierListNode, tp native.TuplePatternNode) []native.GalaNodeRef {
	if tp.Ok() {
		list = tp.IdentifierList()
	}
	return slices.Collect(galaparser.NativeKids(list.GalaNodeRef, native.GalaKIND_identifier))
}
