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
	// A predeclared basic type (`int`, `string`) has no fields or methods at
	// all. A Go type defined over one (`time.Duration`) is a named type,
	// with its own methods, and is not judged here.
	if basic, ok := xType.(transpiler.BasicType); ok && transpiler.IsPrimitiveType(basic.Name) && basic.Name != "any" && basic.Name != "error" {
		if t.basicReceiverIsCertain(base, name) {
			at := m.identAt().rng()
			return spanErrorAt(galaerr.CodeUnknownMethod, at, at, fmt.Sprintf("%s has no field or method %s", basic.Name, name),
				fmt.Sprintf("%s is a basic type: it has no fields", basic.Name))
		}
		return nil
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

// basicReceiverIsCertain reports whether base, a receiver GALA types as a
// predeclared basic type, surely has that type, so that a read of its member
// name is surely wrong. The basic type of a value computed from Go values can
// be a guess: GALA types arithmetic between an untyped Go constant and a Go
// named type (`math.MaxInt8 * time.Second`) by the constant's default, `int`,
// where Go has a `time.Duration` (#1440). So the read is judged only when
//   - Go type information is loaded,
//   - base is a function, method or lambda parameter (a val may hold such
//     arithmetic),
//   - and, for a lambda parameter typed by its slot, no Go type defined over
//     a basic type has a member of that name (`Minutes` of time.Duration),
//     which the value could really be. A parameter whose type is written
//     (`func f(s string)`, `(x float64) =>`) has that type for sure.
func (t *galaASTTransformer) basicReceiverIsCertain(base ast.Expr, name string) bool {
	if t.goTypeInfo == nil {
		return false
	}
	id, ok := base.(*ast.Ident)
	if !ok || t.fixedBindingOf(id.Name) != fixedParam {
		return false
	}
	if !t.isInferredParam(id.Name) {
		return true
	}
	for _, data := range t.goTypeInfo.Types {
		if data == nil || data.Underlying == nil {
			continue
		}
		if u, ok := data.Underlying.(transpiler.BasicType); !ok || !transpiler.IsPrimitiveType(u.Name) {
			continue
		}
		if _, has := data.Methods[name]; has || data.PointerMethods[name] {
			return false
		}
	}
	return true
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
