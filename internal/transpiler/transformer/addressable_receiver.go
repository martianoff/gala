package transformer

import (
	"fmt"
	"go/ast"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// Go calls a pointer-receiver method (`func (u *URL) String()`) only on an
// addressable operand. A val is stored as std.Immutable[T] and read through
// Get(), which returns a copy — a call result, not addressable — so
// `val u = url.URL(...); u.String()` lowered to `u.Get().String()` and Go
// rejected it with "cannot call pointer method String on url.URL". The same
// holds for a field reached through a val (`w.Get().link.Get()`), for any
// other call result, and for a composite literal.
//
// Such a receiver is handed to the method as `std.AddrOfCopy(recv)`: a pointer
// to a fresh copy. The method runs on that copy, so a mutating method cannot
// change the val — the val stays immutable, which is what GALA promises.
//
// A value that must not be copied (see noCopyReason) is rejected instead with
// GALA-E0053: on a copy, a Mutex locks nothing and a Builder's writes are lost.

// addressableReceiver returns the receiver to select `method` on. It is recv
// itself unless the method has a pointer receiver and recv is not
// addressable, in which case it is recv wrapped in std.AddrOfCopy — or
// GALA-E0053 when the receiver's type must not be copied. line and col locate
// the method name.
func (t *galaASTTransformer) addressableReceiver(recv ast.Expr, recvType transpiler.Type, method string, line, col int) (ast.Expr, error) {
	if !t.hasPointerReceiverMethod(recvType, method) || t.isAddressable(recv) {
		return recv, nil
	}
	if reason := t.noCopyReason(recvType, make(map[string]bool)); reason != "" {
		typeName := receiverBase(recvType).String()
		why := fmt.Sprintf("%s must not be copied", reason)
		if reason != typeName {
			why = fmt.Sprintf("%s contains %s, which must not be copied", typeName, reason)
		}
		return nil, galaerr.NewCodedSemanticError(
			galaerr.CodeNoCopyReceiverCopied,
			line, col,
			fmt.Sprintf("cannot call %s on this %s: it cannot be addressed here, so the call would run on a copy, and %s", method, typeName, why),
			"declare it with `var`, or hold a pointer (`&T{...}`)",
		)
	}
	return &ast.CallExpr{
		Fun:  t.stdIdent(transpiler.FuncAddrOfCopy),
		Args: []ast.Expr{recv},
	}, nil
}

// receiverBase strips a generic instantiation to its base type.
func receiverBase(typ transpiler.Type) transpiler.Type {
	if gen, ok := typ.(transpiler.GenericType); ok {
		return gen.Base
	}
	return typ
}

// receiverTypeInfo resolves a value (non-pointer) receiver type to its GALA
// metadata and its Go type data; either may be nil. A Go type's package NAME
// can match a GALA package (io/fs's `fs.X` beside GALA's `fs.X`), so a type
// known to be Go gets no GALA metadata, and callers consult the GALA metadata
// first when both are found. ok is false for a pointer or any other type that
// is not a named value type.
func (t *galaASTTransformer) receiverTypeInfo(typ transpiler.Type) (base transpiler.Type, meta *transpiler.TypeMetadata, goData *transpiler.GoTypeData, ok bool) {
	if typ == nil || transpiler.IsUnusable(typ) {
		return nil, nil, nil, false
	}
	base = receiverBase(typ)
	switch base.(type) {
	case transpiler.NamedType, transpiler.BasicType:
	default:
		return nil, nil, nil, false // a pointer already addresses its target
	}
	named, isNamed := base.(transpiler.NamedType)
	if !isNamed || !t.isGoTyped(named) {
		meta = t.getTypeMeta(base.BaseName())
	}
	goData = t.goTypeInfo.GetTypeData(t.goTypeLookupName(base))
	return base, meta, goData, true
}

// noCopyReason names the type that makes a value of typ unsafe to copy, or
// returns "" (the rule is go vet's copylocks; see the analyzer's
// noCopyReason). A Go type carries the answer in its type data, computed with
// go/types over all its fields, unexported ones included; that answer is taken
// even when GALA metadata was synthesized for the same type (a Go struct of a
// mixed package), because the synthesized field list holds exported fields
// only. A GALA struct is unsafe when *T has Lock() and Unlock() and T lacks
// one of them, when it is named noCopy, or when it has such a field — through
// struct fields only, with a generic struct's type arguments substituted, not
// through pointers, slices, maps or collections. seen guards a recursive
// struct.
func (t *galaASTTransformer) noCopyReason(typ transpiler.Type, seen map[string]bool) string {
	base, meta, goData, ok := t.receiverTypeInfo(typ)
	switch {
	case !ok:
		return ""
	case goData != nil && goData.NoCopy != "":
		return goData.NoCopy
	case meta == nil || seen[typ.String()]:
		return ""
	}
	seen[typ.String()] = true
	if meta.Name == "noCopy" || isPointerOnlyLocker(meta) || isPointerOnlyGoLocker(goData) {
		return base.String()
	}
	typeArgs := make(map[string]transpiler.Type, len(meta.TypeParams))
	if gen, isGeneric := typ.(transpiler.GenericType); isGeneric && len(gen.Params) == len(meta.TypeParams) {
		for i, name := range meta.TypeParams {
			typeArgs[name] = gen.Params[i]
		}
	}
	for _, field := range meta.FieldNames {
		fieldType := meta.Fields[field]
		if arg, isParam := typeArgs[fieldType.String()]; isParam {
			fieldType = arg
		}
		if reason := t.noCopyReason(fieldType, seen); reason != "" {
			return reason
		}
	}
	return ""
}

// isPointerOnlyLocker reports whether *T has niladic Lock() and Unlock()
// methods and T lacks one of them (go vet's sync.Locker test): a GALA type
// that locks through its own address.
func isPointerOnlyLocker(typeMeta *transpiler.TypeMetadata) bool {
	pointerOnly := false
	for _, name := range []string{"Lock", "Unlock"} {
		m := typeMeta.Methods[name]
		if m == nil || len(m.ParamTypes) != 0 || (m.ReturnType != nil && !m.ReturnType.IsNil() && !m.ReturnType.IsVoid()) {
			return false
		}
		pointerOnly = pointerOnly || m.PointerReceiver
	}
	return pointerOnly
}

// isPointerOnlyGoLocker is isPointerOnlyLocker for the Lock and Unlock a
// hand-written .go file declares on a GALA type (GoKindMethodsOnly).
func isPointerOnlyGoLocker(goData *transpiler.GoTypeData) bool {
	if goData == nil || goData.Kind != transpiler.GoKindMethodsOnly {
		return false
	}
	pointerOnly := false
	for _, name := range []string{"Lock", "Unlock"} {
		sig := goData.Methods[name]
		if sig == nil || len(sig.Params) != 0 || len(sig.Returns) != 0 {
			return false
		}
		pointerOnly = pointerOnly || goData.PointerMethods[name]
	}
	return pointerOnly
}

// hasPointerReceiverMethod reports whether `method` is declared on *T only,
// for a value (non-pointer) receiver type T — a GALA type, or a Go type from
// the analyzer's Go type info. Methods lowered to free functions (a method
// with its own type parameters, or one flagged IsGeneric) take their receiver
// as an ordinary argument and are left alone.
func (t *galaASTTransformer) hasPointerReceiverMethod(recvType transpiler.Type, method string) bool {
	_, meta, goData, _ := t.receiverTypeInfo(recvType)
	if meta != nil {
		if m := meta.Methods[method]; m != nil {
			return m.PointerReceiver && !m.IsGeneric && len(m.TypeParams) == 0
		}
	}
	return goData != nil && goData.PointerMethods[method]
}

// isAddressable reports whether Go can take the address of expr, following
// the language spec: a variable, a pointer indirection, a field selector of
// an addressable struct or of a pointer, or a slice index. When the answer
// depends on a type the transformer cannot resolve it reports true, so an
// unknown shape is emitted unchanged, exactly as before.
func (t *galaASTTransformer) isAddressable(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.CallExpr, *ast.CompositeLit, *ast.BasicLit, *ast.FuncLit, *ast.TypeAssertExpr:
		return false
	case *ast.ParenExpr:
		return t.isAddressable(e.X)
	case *ast.SelectorExpr:
		if id, ok := e.X.(*ast.Ident); ok && t.importManager.IsPackage(id.Name) {
			return true // a package-level variable
		}
		xType := t.getExprTypeName(e.X)
		if xType == nil || transpiler.IsUnusable(xType) {
			return true
		}
		if _, isPtr := xType.(transpiler.PointerType); isPtr {
			return true // implicit dereference
		}
		return t.isAddressable(e.X)
	case *ast.IndexExpr:
		// A map element is never addressable. A slice element always is; an
		// array element only when the array is — the type does not tell the
		// two apart, so both are left alone.
		_, isMap := t.getExprTypeName(e.X).(transpiler.MapType)
		return !isMap
	}
	return true
}
