package transformer

import (
	"fmt"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// A type that holds itself by value
//
// A Go value cannot contain itself, so `struct Node(value int, next
// Option[Node])` is an invalid recursive type: Option holds its value, and the
// value is a Node. Recursion needs a reference on the way back: a sealed case
// field of the sealed type itself (lowered to a pointer), a pointer, a slice,
// a map. GALA-E0077 names the field instead of leaving Go to reject the
// generated type.

// selfContainingTypeError reports the first field of the type declared as name
// that holds the type by value, or nil.
func (t *galaASTTransformer) selfContainingTypeError(name string) error {
	meta := t.getTypeMeta(name)
	if meta == nil || meta.IsOpaque {
		return nil
	}
	for _, field := range meta.FieldNames {
		typ := meta.Fields[field]
		if meta.IsSealed && t.sameTypeMeta(typ, meta) {
			continue // lowered to a pointer
		}
		if !t.holdsByValue(typ, meta, map[string]bool{}) {
			continue
		}
		pos := meta.FieldPositions[field]
		kind := "struct"
		if meta.IsSealed {
			kind = "sealed type"
		}
		return galaerr.NewCodedSemanticError(galaerr.CodeSelfContainingType, pos.Line, pos.Column,
			fmt.Sprintf("%s %s holds itself by value: field %s has type %s, which contains a %s", kind, name, field, displayType(typ), name),
			fmt.Sprintf("a value cannot contain itself; declare recursive data as a sealed type whose case field has the type itself (`case Cons(head int, tail %s)`), or hold it through a pointer (`%s *%s`)", name, field, name),
		)
	}
	return nil
}

// holdsByValue reports whether a value of typ contains a value of the type
// target describes: it is that type, or a struct or sealed type with a field
// that holds it. A pointer, slice, map, function, interface or Go type holds
// nothing by value that GALA declares.
func (t *galaASTTransformer) holdsByValue(typ transpiler.Type, target *transpiler.TypeMetadata, visited map[string]bool) bool {
	var args []transpiler.Type
	switch tt := typ.(type) {
	case transpiler.BasicType, transpiler.NamedType: // a declared type's name may be either
	case transpiler.GenericType:
		args = tt.Params
	default:
		return false
	}
	meta := t.getTypeMeta(typ.BaseName())
	if meta == nil {
		return false
	}
	if meta == target {
		return true
	}
	key := typ.String()
	if visited[key] {
		return false
	}
	visited[key] = true
	if meta.IsOpaque {
		return meta.Underlying != nil && t.holdsByValue(meta.Underlying, target, visited)
	}
	subst := make(map[string]transpiler.Type, len(meta.TypeParams))
	for i, p := range meta.TypeParams {
		if i < len(args) {
			subst[p] = args[i]
		}
	}
	for _, field := range meta.FieldNames {
		fieldType := meta.Fields[field]
		if meta.IsSealed && t.sameTypeMeta(fieldType, meta) {
			continue // a sealed type's own case field is a pointer
		}
		if t.holdsByValue(t.substituteInType(fieldType, subst), target, visited) {
			return true
		}
	}
	return false
}

// sameTypeMeta reports whether typ names the type meta describes.
func (t *galaASTTransformer) sameTypeMeta(typ transpiler.Type, meta *transpiler.TypeMetadata) bool {
	switch typ.(type) {
	case transpiler.BasicType, transpiler.NamedType, transpiler.GenericType:
		return t.getTypeMeta(typ.BaseName()) == meta
	}
	return false
}
