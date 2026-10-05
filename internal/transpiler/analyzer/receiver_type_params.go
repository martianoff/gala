package analyzer

import (
	"slices"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// A receiver may name its type's parameters differently from the type's
// declaration (`func (b Box[U]) Twice(f func(U) U) U` on `type Box[T any]`).
// The method's signature is resolved in the receiver's names, the ones in
// scope there (methodTypeParamScope), then renamed to the type's
// (renameReceiverTypeParams): method metadata is read against the type's own
// names (substituteConcreteTypes over TypeMetadata.TypeParams), so a `U` left
// in it is unbound at every call, and a lambda argument is typed `func(x U) U`.

// methodTypeParamScope gives the type parameters a method's signature is
// resolved against: those its receiver names (recvNames), or the type's
// (typeTypeParams) when the receiver names none, then the method's own.
func methodTypeParamScope(recvNames, typeTypeParams, methodTypeParams []string) []string {
	if recvNames == nil {
		recvNames = typeTypeParams
	}
	return slices.Concat(recvNames, methodTypeParams)
}

// renameReceiverTypeParams rewrites meta, a method's metadata resolved in its
// receiver's names recvNames, to the names typeTypeParams its type declares.
// A type parameter of the method the type's names would capture (the `T` of
// `func (b Box[U]) M[T any]` on a `Box[T]`) gets a name no other parameter
// has. meta is left as is when the receiver names none of the type's
// parameters differently, or does not name as many as the type declares.
func renameReceiverTypeParams(meta *transpiler.MethodMetadata, recvNames, typeTypeParams []string) {
	if len(recvNames) != len(typeTypeParams) {
		return
	}
	renames := map[string]transpiler.Type{}
	for i, name := range recvNames {
		if declared := typeTypeParams[i]; name != declared && name != "_" {
			renames[name] = transpiler.BasicType{Name: declared}
		}
	}
	if len(renames) == 0 {
		return
	}
	methodParams := slices.Clone(meta.TypeParams)
	taken := slices.Concat(recvNames, typeTypeParams, meta.TypeParams)
	for i, name := range methodParams {
		if !slices.Contains(typeTypeParams, name) {
			continue
		}
		fresh := name
		for slices.Contains(taken, fresh) {
			fresh += "_"
		}
		taken = append(taken, fresh)
		renames[name] = transpiler.BasicType{Name: fresh}
		methodParams[i] = fresh
	}
	meta.TypeParams = methodParams
	for i, pt := range meta.ParamTypes {
		meta.ParamTypes[i] = transpiler.SubstituteTypeParams(pt, renames)
	}
	meta.ReturnType = transpiler.SubstituteTypeParams(meta.ReturnType, renames)
}

// receiverTypeArgNames lists the type arguments a receiver type spells, as
// written and in order: [U] for `Box[U]` and `*Box[U]`, nil for a
// non-generic receiver.
func receiverTypeArgNames(ctx grammar.ITypeContext) []string {
	for ctx != nil && ctx.QualifiedIdentifier() == nil {
		if len(ctx.AllType_()) != 1 {
			return nil
		}
		ctx = ctx.Type_(0)
	}
	if ctx == nil || ctx.TypeArguments() == nil || ctx.TypeArguments().TypeList() == nil {
		return nil
	}
	var names []string
	for _, arg := range ctx.TypeArguments().TypeList().AllType_() {
		names = append(names, arg.GetText())
	}
	return names
}
