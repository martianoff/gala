package analyzer

import (
	"fmt"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
)

// GALA-E0106: a type named like a Go predeclared type.

// predeclaredTypeNames are the types of Go's universe scope. The list is
// GALA's rule, written out so it does not change with the Go release that
// builds the transpiler.
var predeclaredTypeNames = map[string]bool{
	"bool": true, "byte": true, "rune": true, "string": true, "error": true,
	"any": true, "comparable": true, "uintptr": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"float32": true, "float64": true, "complex64": true, "complex128": true,
}

// checkPredeclaredTypeNamesNative reports GALA-E0106 for the first type
// declared in root, a file's native tree, with the name of a Go predeclared
// type (`error`, `string`, `int`, `any`, …): a `type` declaration (a block
// struct, an interface, an alias), a shorthand struct, an opaque type, a
// sealed type or one of its cases, or a type parameter, at the top level or
// in a body. GALA reads such a name as Go's type in every type position, so
// the declared type could not be named there. It returns nil when root
// declares none.
func checkPredeclaredTypeNamesNative(root native.GalaNodeRef) error {
	var found native.GalaNodeRef
	root.Walk(func(n native.GalaNodeRef) bool {
		if found.Ok() {
			return false
		}
		if n.Kind() != native.GalaKIND_identifier {
			return true
		}
		if predeclaredTypeNames[galaparser.NativeText(n)] && declaresTypeNative(n) {
			found = n
		}
		return false
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

// declaresTypeNative reports whether the identifier id is the name a type
// declaration or a type parameter introduces.
func declaresTypeNative(id native.GalaNodeRef) bool {
	parent := id.Parent()
	if !parent.Ok() {
		return false
	}
	switch parent.Kind() {
	case native.GalaKIND_typeDeclaration,
		native.GalaKIND_structShorthandDeclaration,
		native.GalaKIND_opaqueTypeDeclaration,
		native.GalaKIND_sealedTypeDeclaration,
		native.GalaKIND_sealedCase:
		return true
	case native.GalaKIND_typeParameter:
		return native.TypeParameterNode{GalaNodeRef: parent}.Identifier().GalaNodeRef == id
	}
	return false
}
