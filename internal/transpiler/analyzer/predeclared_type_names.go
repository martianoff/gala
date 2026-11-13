package analyzer

import (
	"fmt"
	"go/types"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
)

// GALA-E0106: a type named like a Go predeclared type.

// checkPredeclaredTypeNamesNative reports GALA-E0106 for the first type
// declared in root, a file's native tree, with the name of a Go predeclared
// type (`error`, `string`, `int`, `any`, …): a `type` declaration (a block
// struct, an interface, an alias), a shorthand struct, an opaque type, a
// sealed type or one of its cases, at the top level or in a body. GALA
// resolves such a name to Go's type wherever it is written, so nothing could
// refer to the declaration. It returns nil when root declares none.
func checkPredeclaredTypeNamesNative(root native.GalaNodeRef) error {
	var found native.GalaNodeRef
	root.Walk(func(n native.GalaNodeRef) bool {
		if found.Ok() {
			return false
		}
		var id native.GalaNodeRef
		switch n.Kind() {
		case native.GalaKIND_typeDeclaration:
			id = native.TypeDeclarationNode{GalaNodeRef: n}.Identifier().GalaNodeRef
		case native.GalaKIND_structShorthandDeclaration:
			id = native.StructShorthandDeclarationNode{GalaNodeRef: n}.Identifier().GalaNodeRef
		case native.GalaKIND_opaqueTypeDeclaration:
			id = native.OpaqueTypeDeclarationNode{GalaNodeRef: n}.Identifier().GalaNodeRef
		case native.GalaKIND_sealedTypeDeclaration:
			id = native.SealedTypeDeclarationNode{GalaNodeRef: n}.Identifier().GalaNodeRef
		case native.GalaKIND_sealedCase:
			id = native.SealedCaseNode{GalaNodeRef: n}.Identifier().GalaNodeRef
		default:
			return true
		}
		if id.Ok() && isPredeclaredTypeName(galaparser.NativeText(id)) {
			found = id
			return false
		}
		return true
	})
	if !found.Ok() {
		return nil
	}
	name := galaparser.NativeText(found)
	pos := nativePos(found)
	return galaerr.NewCodedSemanticError(galaerr.CodePredeclaredTypeName, pos.Line, pos.Column,
		fmt.Sprintf("type %s is named like a Go predeclared type", name),
		fmt.Sprintf("rename the type; GALA reads %s as Go's own type wherever it is written", name))
}

// isPredeclaredTypeName reports whether name is a type of Go's universe
// scope: bool, byte, rune, string, error, any, comparable, and the numeric
// types.
func isPredeclaredTypeName(name string) bool {
	_, isType := types.Universe.Lookup(name).(*types.TypeName)
	return isType
}
