package transformer

import (
	"fmt"
	"go/ast"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// Unknown field on a known GALA type
//
// A member read `x.name` that the receiver's GALA type has neither as a field
// nor as a method used to be emitted as written and left to `go build`, which
// reported it against the generated Go: `tup._1 undefined (type
// std.Tuple3[int, string, bool] has no field or method _1)`. It is
// GALA-E0044 here, as an unknown method is (unknown_method.go), with the
// fields the type does have. A member that is called is that check's.

// checkUnknownField reports the member m of base, a read that is not called,
// when xType, base's type with any Immutable unwrapped, is a GALA struct that
// has no field or method of that name. Like unknownMethodError, it judges
// only a type whose members it knows (canJudgeMembers): a struct declared in
// GALA source. A sealed type is goOnlyFieldError's.
func (t *galaASTTransformer) checkUnknownField(base ast.Expr, xType transpiler.Type, m memberSrc) error {
	name := m.ident()
	if id, ok := base.(*ast.Ident); ok && (t.importManager.IsPackage(id.Name) || t.bindingScope(id.Name) == nil && t.getTypeMeta(id.Name) != nil) {
		// A package's member, or a type's (`Point.Apply`).
		return nil
	}
	if ptr, ok := xType.(transpiler.PointerType); ok {
		xType = ptr.Elem
	}
	_, meta, _, ok := t.receiverTypeInfo(xType)
	if !ok || meta == nil || !declaredInGala(meta) || meta.IsSealed || meta.IsInterface || meta.IsOpaque ||
		len(meta.FieldNames) == 0 || t.hasMemberOrGoMethod(meta, name) || !t.canJudgeMembers(meta, xType) {
		return nil
	}
	hint := fmt.Sprintf("%s's fields are %s", meta.Name, strings.Join(meta.FieldNames, ", "))
	if best := nearestName(name, meta.FieldNames); best != "" {
		hint = fmt.Sprintf("did you mean `%s`? %s", best, hint)
	}
	at := m.identAt().rng()
	return spanErrorAt(galaerr.CodeUnknownMethod, at, at, fmt.Sprintf("%s has no field or method %s", meta.Name, name), hint)
}

// hasMemberOrGoMethod reports whether the GALA type meta describes has name as
// a member (hasMember), or as a method a hand-written .go file of its package
// declares on it.
func (t *galaASTTransformer) hasMemberOrGoMethod(meta *transpiler.TypeMetadata, name string) bool {
	if hasMember(meta, name) {
		return true
	}
	_, declared := t.goMethodsOnGalaType(meta)[name]
	return declared
}

// canJudgeMembers reports whether a member missing from the GALA type meta
// describes, read on a value of type recvType, is surely missing: an empty
// method set of another package's type may be one loaded without its
// methods, and a receiver that names no instance of the type has no member
// set to judge (receiverNamesAnInstance).
func (t *galaASTTransformer) canJudgeMembers(meta *transpiler.TypeMetadata, recvType transpiler.Type) bool {
	if len(meta.Methods) == 0 && meta.Package != t.packageName {
		return false
	}
	return t.receiverNamesAnInstance(meta, recvType)
}

// declaredInGala reports whether meta describes a type declared in GALA
// source, not one synthesized from a hand-written Go declaration, whose field
// list holds its exported fields only.
func declaredInGala(meta *transpiler.TypeMetadata) bool {
	return meta.DefinedIn != "" || meta.Pos.Line > 0
}
