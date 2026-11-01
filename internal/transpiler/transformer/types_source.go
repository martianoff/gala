package transformer

import (
	"go/ast"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
)

// A type as its lowering reads it (lowerType): typeSrc is a type node, and
// its readers dispatch on the node's children and read them through the rule
// handles and NativeKids, never through Typed() or the All accessors (see
// declarations_native.go).

// typeSrc is a type as written; the zero typeSrc is no type.
type typeSrc struct {
	node native.TypeNode
}

// nativeType is the type node n; no node gives no type.
func nativeType(n native.TypeNode) typeSrc { return typeSrc{node: n} }

// ok reports whether s is a type, not the zero typeSrc.
func (s typeSrc) ok() bool { return s.node.Ok() }

// at is where s is written.
func (s typeSrc) at() srcNode { return srcNode{node: s.node.GalaNodeRef} }

// text is s's tokens run together (GetText).
func (s typeSrc) text() string { return s.node.GetText() }

// typeText is TypeText of s.
func (s typeSrc) typeText() string { return galaparser.NativeTypeText(s.node) }

// sourceText is the source s spans, whitespace included.
func (s typeSrc) sourceText() string { return s.node.SourceText() }

// ptrElem is PtrElem of s: the type argument of `Ptr[T]`, or no type.
func (s typeSrc) ptrElem() typeSrc {
	if !s.node.Ok() {
		return typeSrc{}
	}
	return nativeType(galaparser.NativePtrElem(s.node))
}

// pointerElem is PointerElem of s: the element type of `*T` or `Ptr[T]` and
// true, or (s, false).
func (s typeSrc) pointerElem() (typeSrc, bool) {
	if !s.node.Ok() {
		return s, false
	}
	elem, ok := galaparser.NativePointerElem(s.node)
	return nativeType(elem), ok
}

// named reports whether s is a named type, `pkg.Name[Args]`.
func (s typeSrc) named() bool { return s.node.QualifiedIdentifier().Ok() }

// nameParts is the identifiers of a named type's name, in order.
func (s typeSrc) nameParts() []string {
	var parts []string
	for id := range galaparser.NativeKids(s.node.QualifiedIdentifier().GalaNodeRef, native.GalaKIND_identifier) {
		parts = append(parts, galaparser.NativeText(id))
	}
	return parts
}

// lastName is the last identifier of a named type's name (`Sendable` of
// `std.Sendable`), read without building the list nameParts does.
func (s typeSrc) lastName() string {
	var last native.GalaNodeRef
	for id := range galaparser.NativeKids(s.node.QualifiedIdentifier().GalaNodeRef, native.GalaKIND_identifier) {
		last = id
	}
	if !last.Ok() {
		return ""
	}
	return galaparser.NativeText(last)
}

// nameText is a named type's name as written, its tokens run together.
func (s typeSrc) nameText() string { return s.node.QualifiedIdentifier().GetText() }

// typeArgs is a named type's type arguments and true when it is written
// with them (`Box[A, B]`), and false when it is not.
func (s typeSrc) typeArgs() ([]typeSrc, bool) {
	args := s.node.TypeArguments()
	if !args.Ok() {
		return nil, false
	}
	return nativeTypes(args.TypeList().GalaNodeRef), true
}

// nativeTypes is the child types of n.
func nativeTypes(n native.GalaNodeRef) []typeSrc {
	var out []typeSrc
	for c := range galaparser.NativeKids(n, native.GalaKIND_type) {
		out = append(out, nativeType(native.TypeNode{GalaNodeRef: c}))
	}
	return out
}

// hasTypeArgs reports whether s is written with type arguments.
func (s typeSrc) hasTypeArgs() bool { return s.node.TypeArguments().Ok() }

// typeShape is the form of a type that is not named.
type typeShape uint8

const (
	shapeOther   typeShape = iota // none of the below (parse-error recovery)
	shapePointer                  // *T
	shapeSlice                    // []T
	shapeMap                      // map[K]V
	shapeFunc                     // func(...) R
)

// elems is the types s is written with directly: T of `*T` and `[]T`, K and
// V of `map[K]V`.
func (s typeSrc) elems() []typeSrc { return nativeTypes(s.node.GalaNodeRef) }

// startText is the text of s's first token.
func (s typeSrc) startText() string { return galaparser.NativeStartText(s.node.GalaNodeRef) }

// hasSignature reports whether s is written with a signature, a function
// type's.
func (s typeSrc) hasSignature() bool { return s.node.Signature().Ok() }

// shape is the form of s, which is not named, and, for a pointer, slice or
// map type, its elems.
func (s typeSrc) shape() (typeShape, []typeSrc) {
	var shape typeShape
	minElems := 1
	switch s.startText() {
	case "*":
		shape = shapePointer
	case "[":
		shape = shapeSlice
	case "map":
		shape, minElems = shapeMap, 2
	}
	if shape != shapeOther {
		if elems := s.elems(); len(elems) >= minElems {
			return shape, elems
		}
	}
	if s.hasSignature() {
		return shapeFunc, nil
	}
	return shapeOther, nil
}

// funcTypeParam is a parameter of a function type: its type, or, written
// without one, the identifier that names it (`func(T) bool`), and whether it
// is variadic.
type funcTypeParam struct {
	typ      typeSrc
	name     string
	variadic bool
}

// funcSignature is the parameters and results of s, a function type.
func (s typeSrc) funcSignature() ([]funcTypeParam, []typeSrc) {
	sig := s.node.Signature()
	var params []funcTypeParam
	for p := range galaparser.NativeKids(sig.Parameters().ParameterList().GalaNodeRef, native.GalaKIND_parameter) {
		param := native.ParameterNode{GalaNodeRef: p}
		fp := funcTypeParam{variadic: param.ELLIPSIS().Ok()}
		if typ := param.Type_(); typ.Ok() {
			fp.typ = nativeType(typ)
		} else {
			fp.name = param.Identifier().GetText()
		}
		params = append(params, fp)
	}
	return params, nativeSigResults(sig)
}

// nativeSigResults is the result types of the signature node sig: its one
// result type, or each type of its Go result list (`(int, error)`).
func nativeSigResults(sig native.SignatureNode) []typeSrc {
	if typ := sig.Type_(); typ.Ok() {
		return []typeSrc{nativeType(typ)}
	}
	return nativeTypes(sig.GoResults().GalaNodeRef)
}

// noteNativeType tells onNativeType of n.
func (t *galaASTTransformer) noteNativeType(n srcNode) {
	if t.onNativeType != nil && n.node.Ok() {
		t.onNativeType(int(n.node.Start().Line))
	}
}

// lowerFuncTypeSignature is the Go function type of s, a function type. A
// parameter written without a type is a type itself (`func(T) bool`).
func (t *galaASTTransformer) lowerFuncTypeSignature(s typeSrc) (*ast.FuncType, error) {
	params, resultTypes := s.funcSignature()
	fieldList := &ast.FieldList{}
	for _, p := range params {
		field := &ast.Field{}
		if p.typ.ok() {
			typ, err := t.lowerType(p.typ)
			if err != nil {
				return nil, err
			}
			field.Type = typ
		} else {
			field.Type = t.funcTypeParamName(p.name)
		}
		if p.variadic {
			field.Type = &ast.Ellipsis{Elt: field.Type}
		}
		fieldList.List = append(fieldList.List, field)
	}
	results, err := t.lowerSignatureResults(resultTypes)
	if err != nil {
		return nil, err
	}
	return &ast.FuncType{Params: fieldList, Results: results}, nil
}

// lowerSignatureResults lowers a signature's result types; nil when it has
// none.
func (t *galaASTTransformer) lowerSignatureResults(types []typeSrc) (*ast.FieldList, error) {
	if len(types) == 0 {
		return nil, nil
	}
	results := &ast.FieldList{}
	for _, typ := range types {
		retType, err := t.lowerType(typ)
		if err != nil {
			return nil, err
		}
		t.isImmutableType(t.astTypeToTranspilerType(retType))
		results.List = append(results.List, &ast.Field{Type: retType})
	}
	return results, nil
}
