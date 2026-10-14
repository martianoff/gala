package transformer

import (
	"fmt"
	"strings"

	"github.com/antlr4-go/antlr/v4"

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
// that holds the type by value, or nil. at locates the declaration, for a
// field whose own position is not recorded.
func (t *galaASTTransformer) selfContainingTypeError(name string, at antlr.Token) error {
	meta := t.getTypeMeta(name)
	if meta == nil || meta.IsOpaque {
		return nil
	}
	visited := map[string]bool{}
	params := typeParamSet(meta)
	for _, field := range meta.FieldNames {
		typ := meta.Fields[field]
		if isSealedSelfField(typ, meta) || !t.holdsByValue(typ, meta, params, visited) {
			continue
		}
		self := name
		if len(meta.TypeParams) > 0 {
			self = name + "[" + strings.Join(meta.TypeParams, ", ") + "]"
		}
		kind, fix := "struct", fmt.Sprintf("declare recursive data as a sealed type whose case field has the type itself (`case Cons(head int, tail %s)`)", self)
		if meta.IsSealed {
			kind, fix = "sealed type", fmt.Sprintf("give the case field the type itself (`%s %s`), which is held by reference", field, self)
		}
		pos := meta.FieldPositions[field]
		if pos.Line == 0 {
			pos = transpiler.SourcePos{Line: at.GetLine(), Column: at.GetColumn()}
		}
		return galaerr.NewCodedSemanticError(galaerr.CodeSelfContainingType, pos.Line, pos.Column,
			fmt.Sprintf("%s %s holds itself by value: field %s has type %s, which contains %s itself", kind, name, field, displayType(typ), name),
			fmt.Sprintf("a value cannot contain itself; %s, or hold it through a pointer (`%s *%s`)", fix, field, self),
		)
	}
	return nil
}

// holdsByValue reports whether a value of typ contains a value of the type
// target describes: it is that type, or a struct or sealed type with a field
// that holds it. A pointer, slice, map, function, interface or Go type holds
// nothing by value that GALA declares, and neither does a type parameter in
// params. visited holds the types already walked for this target.
func (t *galaASTTransformer) holdsByValue(typ transpiler.Type, target *transpiler.TypeMetadata, params map[string]bool, visited map[string]bool) bool {
	var args []transpiler.Type
	switch tt := typ.(type) {
	case transpiler.BasicType: // a declared type's name may be either
		if isPrimitiveType(tt.Name) || params[tt.Name] {
			return false
		}
	case transpiler.NamedType:
		if tt.Package == "" && params[tt.Name] {
			return false
		}
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
		return meta.Underlying != nil && t.holdsByValue(meta.Underlying, target, params, visited)
	}
	var subst map[string]transpiler.Type
	if len(meta.TypeParams) > 0 {
		subst = make(map[string]transpiler.Type, len(meta.TypeParams))
		for i, p := range meta.TypeParams {
			if i < len(args) {
				subst[p] = args[i]
			}
		}
	}
	// A parameter the arguments did not bind stays a parameter of meta.
	fieldParams := params
	if len(subst) < len(meta.TypeParams) {
		fieldParams = typeParamSet(meta)
	}
	for _, field := range meta.FieldNames {
		fieldType := meta.Fields[field]
		if isSealedSelfField(fieldType, meta) {
			continue
		}
		if subst != nil {
			fieldType = t.substituteInType(fieldType, subst)
		}
		if t.holdsByValue(fieldType, target, fieldParams, visited) {
			return true
		}
	}
	return false
}

// typeParamSet is the set of meta's type parameter names.
func typeParamSet(meta *transpiler.TypeMetadata) map[string]bool {
	set := make(map[string]bool, len(meta.TypeParams))
	for _, p := range meta.TypeParams {
		set[p] = true
	}
	return set
}

// isSealedSelfField reports whether a field of type typ in the type meta
// describes is a sealed case field of the sealed type itself, which the
// lowering holds through a pointer.
func isSealedSelfField(typ transpiler.Type, meta *transpiler.TypeMetadata) bool {
	return meta.IsSealed && typ != nil && isSelfReferentialSealedField(stripPackagePrefix(typ.BaseName()), meta.Name)
}
