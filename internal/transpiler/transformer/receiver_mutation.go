package transformer

import (
	"fmt"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// A pointer-receiver method called on a value that cannot be addressed (a
// val, a field reached through one, a call's result) runs on a copy. When the
// method can mutate its receiver the change is lost, so such a call is
// GALA-E0110; a method that only reads its receiver runs on the copy as
// before. methodMutates decides which, from what the analyzer recorded:
//
//   - a GALA method: its ReceiverEffects, judged here against the types of
//     the fields they reach; a call it makes on its receiver, or on a field
//     stored in it, counts when that method mutates;
//   - a Go method (one of a Go type, or one hand-written Go declares on a GALA
//     type): the read-only methods the analyzer works out from the Go source
//     (see goReadOnlyMethods).
//
// A write through a pointer, slice or map held in a field does not change
// the receiver's own memory; the copy holds the same pointer, so nothing is
// lost and it does not count.

// mutatingCallOnCopyError is GALA-E0110 for a call of method, which can
// change its receiver, on a value of recvType that cannot be addressed; a
// print runs String that way. line and col locate the call.
func mutatingCallOnCopyError(method string, print bool, recvType transpiler.Type, line, col int) error {
	typeName := receiverBase(recvType).String()
	what, who := "call "+method+" on", method
	if print {
		what, who = "print", "its "+method
	}
	return galaerr.NewCodedSemanticError(galaerr.CodeMutatingCallOnCopy, line, col,
		fmt.Sprintf("cannot %s this %s: %s can change its receiver, and this %s cannot be addressed here, so the change would be made to a copy and lost", what, typeName, who, typeName),
		"declare it with `var`, or hold a `Ptr`")
}

// pointerMethodMutates reports whether method, declared on the pointer
// receiver of recvType's base type, can write through its receiver.
func (t *galaASTTransformer) pointerMethodMutates(recvType transpiler.Type, method string) bool {
	return t.methodMutates(recvType, method, make(map[string]bool))
}

// methodMutates is pointerMethodMutates; seen holds the methods already on
// the way, so a recursive call adds nothing a caller further up does not
// already look at.
func (t *galaASTTransformer) methodMutates(recvType transpiler.Type, method string, seen map[string]bool) bool {
	base, meta, goData, ok := t.receiverTypeInfo(recvType)
	if !ok {
		return false // a pointer: the method runs on what it points to
	}
	key := recvType.String() + "." + method
	if seen[key] {
		return false
	}
	seen[key] = true
	if meta != nil {
		if m := meta.Methods[method]; m != nil && !m.GoDeclared {
			return m.PointerReceiver && t.effectsMutate(recvType, m.ReceiverEffects, seen)
		}
	}
	if goData != nil {
		return goData.PointerMethods[method] && !t.goReadOnlyMethods(base, goData)[method]
	}
	if meta != nil {
		return false // a GALA type without such a method: Go calls it on the value
	}
	// A type of another package nothing here describes may have such a method.
	named, isNamed := base.(transpiler.NamedType)
	return isNamed && named.Package != ""
}

// goReadOnlyMethods is the read-only pointer methods of the Go type base,
// whose Go type data is goData: recorded by the analyzer for a type of a
// package's own .go files, and otherwise looked up from the type's package.
func (t *galaASTTransformer) goReadOnlyMethods(base transpiler.Type, goData *transpiler.GoTypeData) map[string]bool {
	if goData.ReadOnlyMethods != nil || t.lookupGoReadOnly == nil {
		return goData.ReadOnlyMethods
	}
	named, isNamed := base.(transpiler.NamedType)
	if !isNamed {
		return nil
	}
	importPath := named.ImportPath
	if importPath == "" {
		entry, ok := t.goImportForPathlessType(named)
		if !ok {
			return nil
		}
		importPath = entry.Path
	}
	return t.lookupGoReadOnly(importPath, named.Name)
}

// effectsMutate reports whether any of a GALA method's receiver effects
// changes a value of recvType.
func (t *galaASTTransformer) effectsMutate(recvType transpiler.Type, effects []transpiler.ReceiverEffect, seen map[string]bool) bool {
	for _, eff := range effects {
		if eff.Kind == transpiler.ReceiverEscape {
			return true
		}
		place, inReceiver, known := t.receiverPlace(recvType, eff)
		switch {
		case !known:
			return true
		case !inReceiver:
			continue
		case eff.Kind == transpiler.ReceiverWrite:
			return true
		}
		if _, _, isField := t.fieldOf(place, eff.Method); isField {
			continue // a read of the field
		}
		if t.methodMutates(place, eff.Method, seen) {
			return true
		}
	}
	return false
}

// receiverPlace follows eff's path from a receiver of recvType. place is the
// type of the place it reaches; inReceiver is false when the path leaves the
// receiver's own memory first: through a val field (read through a copy), a
// pointer, slice, map or other reference held in a field, or an element of a
// slice, a map or a GALA collection (read as a copy). A write's last field is
// the place assigned, so it counts whatever its type. known is false when a
// name on the path is no field of a type it is looked up in.
func (t *galaASTTransformer) receiverPlace(recvType transpiler.Type, eff transpiler.ReceiverEffect) (place transpiler.Type, inReceiver, known bool) {
	place = recvType
	for i, name := range eff.Path {
		if name == transpiler.ReceiverIndex {
			arr, isArray := place.(transpiler.ArrayType)
			if !isArray || !arr.Fixed {
				return nil, false, true // a shared element or a copy
			}
			place = arr.Elem
			continue
		}
		fieldType, isVal, found := t.fieldOf(place, name)
		switch {
		case !found:
			return nil, false, false
		case isVal:
			return nil, false, true
		case i == len(eff.Path)-1 && eff.Kind == transpiler.ReceiverWrite:
			return fieldType, true, true
		case isReference(fieldType):
			return nil, false, true
		}
		place = fieldType
	}
	return place, true, true
}

// fieldOf is the type of the field name of a value of typ, with typ's type
// arguments substituted, and whether it is a val field.
func (t *galaASTTransformer) fieldOf(typ transpiler.Type, name string) (fieldType transpiler.Type, isVal, found bool) {
	if ft, immutable, ok := t.resolveStructFieldType(typ, name); ok {
		return ft, immutable, true
	}
	if _, _, goData, ok := t.receiverTypeInfo(typ); ok && goData != nil {
		if ft, has := goData.Fields[name]; has {
			return ft, false, true
		}
	}
	return nil, false, false
}

// isReference reports whether a value of typ refers to memory it does not
// hold, which a copy of it shares: a pointer, slice, map, channel, function
// or interface.
func isReference(typ transpiler.Type) bool {
	switch typ := typ.(type) {
	case transpiler.PointerType, transpiler.MapType, transpiler.FuncType, transpiler.ChanType, transpiler.InterfaceType:
		return true
	case transpiler.ArrayType:
		return !typ.Fixed
	}
	return false
}
