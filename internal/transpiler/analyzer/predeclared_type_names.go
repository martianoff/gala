package analyzer

import (
	"fmt"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler/registry"
)

// GALA-E0106: a type named like a Go predeclared type, a Go builtin function
// or the std package.

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

// builtinFuncNames are the functions of Go's universe scope, written out for
// the same reason. It is all of them, unlike transformer.ForbiddenGoBuiltins,
// which lists only the builtins GALA code may not call.
var builtinFuncNames = map[string]bool{
	"append": true, "cap": true, "clear": true, "close": true, "complex": true,
	"copy": true, "delete": true, "imag": true, "len": true, "make": true,
	"max": true, "min": true, "new": true, "panic": true, "print": true,
	"println": true, "real": true, "recover": true,
}

// checkPredeclaredTypeNamesNative reports GALA-E0106 for the first type
// declared in root, a file's native tree, with the name of a Go predeclared
// type (`error`, `string`, `int`, `any`, …), of a Go builtin function (`len`,
// `make`, `new`, …) or `std`: a `type` declaration (a block struct, an
// interface, an alias), a shorthand struct, an opaque type, a sealed type or
// one of its cases, or a type parameter, at the top level or in a body. GALA
// reads a predeclared type name as Go's type in every type position, so the
// declared type could not be named there; a type named like a builtin
// function or std hides, in the Go the transpiler generates, the builtin or
// the package that Go uses. It returns nil when root declares none.
func checkPredeclaredTypeNamesNative(root native.GalaNodeRef) error {
	var found native.GalaNodeRef
	root.Walk(func(n native.GalaNodeRef) bool {
		if found.Ok() {
			return false
		}
		if n.Kind() != native.GalaKIND_identifier {
			return true
		}
		if reservedTypeName(galaparser.NativeText(n)) && declaresTypeNative(n) {
			found = n
		}
		return false
	})
	if !found.Ok() {
		return nil
	}
	name := galaparser.NativeText(found)
	pos := nativePos(found)
	msg, hint := reservedTypeNameDiagnostic(name)
	return galaerr.NewCodedSemanticError(galaerr.CodePredeclaredTypeName, pos.Line, pos.Column, msg, hint)
}

// reservedTypeName reports whether a type may not be named name (GALA-E0106).
func reservedTypeName(name string) bool {
	return predeclaredTypeNames[name] || builtinFuncNames[name] || name == registry.StdPackageName
}

// reservedTypeNameDiagnostic is the message and hint of GALA-E0106 for a type
// named name, a reservedTypeName.
func reservedTypeNameDiagnostic(name string) (msg, hint string) {
	switch {
	case predeclaredTypeNames[name]:
		return fmt.Sprintf("type %s is named like a Go predeclared type", name),
			fmt.Sprintf("rename the type; GALA reads %s as Go's own type wherever it is written", name)
	case builtinFuncNames[name]:
		return fmt.Sprintf("type %s is named like a Go builtin function", name),
			fmt.Sprintf("rename the type; it would hide Go's builtin %s, which the generated Go calls", name)
	}
	return fmt.Sprintf("type %s is named like the std package", name),
		fmt.Sprintf("rename the type; it would hide the %s package, which the generated Go refers to", name)
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
