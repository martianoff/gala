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
// when base's type is a GALA struct that has no field or method of that name.
// Like unknownMethodError, it judges only a type whose members it knows: a
// struct declared in GALA source, spelled as an instance of it. A sealed type
// is goOnlyFieldError's.
func (t *galaASTTransformer) checkUnknownField(base ast.Expr, m memberSrc) error {
	name := m.ident()
	if t.goResultOf(base) != nil {
		// A Go call's Try or Tuple: GALA-E0049 names it (checkGoResultMember).
		return nil
	}
	if id, ok := base.(*ast.Ident); ok && (t.importManager.IsPackage(id.Name) || t.bindingScope(id.Name) == nil && t.getTypeMeta(id.Name) != nil) {
		// A package's member, or a type's (`Point.Apply`).
		return nil
	}
	xType := t.getExprTypeName(base)
	if t.isImmutableType(xType) {
		gen, ok := xType.(transpiler.GenericType)
		if !ok || len(gen.Params) == 0 || name == "Get" || name == "value" {
			return nil
		}
		xType = gen.Params[0]
	}
	if ptr, ok := xType.(transpiler.PointerType); ok {
		xType = ptr.Elem
	}
	_, meta, goData, ok := t.receiverTypeInfo(xType)
	if !ok || meta == nil || !declaredInGala(meta) || meta.IsSealed || meta.IsInterface || meta.IsOpaque ||
		len(meta.FieldNames) == 0 || isSynthesizedMethodName(name) {
		return nil
	}
	if _, isField := meta.Fields[name]; isField {
		return nil
	}
	if _, isMethod := meta.Methods[name]; isMethod {
		return nil
	}
	if goData != nil {
		if _, isMethod := goData.Methods[name]; isMethod {
			return nil
		}
	}
	// A foreign type's empty method set may be one loaded without methods
	// (see unknownMethodError).
	if len(meta.Methods) == 0 && meta.Package != t.packageName {
		return nil
	}
	if !t.receiverNamesAnInstance(meta, xType) {
		return nil
	}
	line, col := m.identAt().start()
	hint := fmt.Sprintf("%s's fields are %s", meta.Name, strings.Join(meta.FieldNames, ", "))
	if best := nearestName(name, meta.FieldNames); best != "" {
		hint = fmt.Sprintf("did you mean `%s`? %s", best, hint)
	}
	return galaerr.NewCodedSemanticError(galaerr.CodeUnknownMethod, line, col,
		fmt.Sprintf("%s has no field or method %s", meta.Name, name), hint).
		WithSpan(col + len([]rune(name)))
}

// declaredInGala reports whether meta describes a type declared in GALA
// source, not one synthesized from a hand-written Go declaration, whose field
// list holds its exported fields only.
func declaredInGala(meta *transpiler.TypeMetadata) bool {
	return meta.DefinedIn != "" || meta.Pos.Line > 0
}
