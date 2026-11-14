package analyzer

import (
	"strings"

	"martianoff/gala/internal/transpiler"
)

// Go compiles every method of a generic type once for each type argument
// shape the type is instantiated with, whether or not the method is called
// (#872). A method whose signature names a different generic type with
// methods (`Array[T].ToList() List[T]`) instantiates that type too, with its
// whole method set, and those methods instantiate theirs: every Array[T] of a
// program used to bring a List[T], an Option[Array[T]] and more with it. A
// function is compiled only where it is called.
//
// markFanOutMethods records such a method of a generic type declared in
// package pkgName as lowered to a function (MethodMetadata.IsGeneric), the
// same record a method with type parameters or an instantiation cycle gets,
// read by the declaration and by every call site in any package. A call
// `xs.ToList()` keeps its GALA spelling; only the generated Go calls
// `Array_ToList(xs)`.
//
// A method stays a method when the type may need it in its method set: its
// name is a method of an interface the package can see (its own and those of
// the packages it imports, std's Iterable, Seq and Equatable among them), or
// one the language or Go looks up by name (keptMethodNames). Lowering is
// decided from the package's own declarations and imports only, so every
// file of the package and every importer reads the same record.
func markFanOutMethods(richAST *transpiler.RichAST, pkgName string) {
	var interfaceMethods map[string]bool
	for _, typeMeta := range richAST.Types {
		if typeMeta.Package != pkgName || len(typeMeta.TypeParams) == 0 || typeMeta.IsInterface {
			continue
		}
		for name, method := range typeMeta.Methods {
			// A method declaring a Go result list stays a method: its
			// method value is a Go method value (see loweredMethodValue).
			if method.IsGeneric || len(method.TypeParams) > 0 || method.GoDeclared || method.OfTypeDecl || method.GoResults != nil || keptMethodNames[name] {
				continue
			}
			if !fanOutSignature(richAST, typeMeta, method) {
				continue
			}
			if interfaceMethods == nil {
				interfaceMethods = visibleInterfaceMethods(richAST)
			}
			if interfaceMethods[name] {
				continue
			}
			method.IsGeneric = true
		}
	}
}

// keptMethodNames are methods a type keeps whatever their signature: the
// ones GALA's own machinery calls by name on any type (companion Apply and
// Unapply, a sealed view's MatchView, Copy, Equal, Hash, Compare, Get) and
// Go's protocol methods, which Go packages find through interfaces GALA code
// never names.
var keptMethodNames = map[string]bool{
	"Apply": true, "Unapply": true, "MatchView": true,
	"Copy": true, "Equal": true, "Hash": true, "Compare": true, "Get": true,
	"String": true, "GoString": true, "Format": true, "Error": true, "Unwrap": true, "Is": true, "As": true,
	"MarshalJSON": true, "UnmarshalJSON": true, "MarshalText": true, "UnmarshalText": true,
	"MarshalYAML": true, "UnmarshalYAML": true, "MarshalBinary": true, "UnmarshalBinary": true, "IsZero": true,
}

// visibleInterfaceMethods returns the method names of every interface in
// richAST: the analyzed package's and its imports'.
func visibleInterfaceMethods(richAST *transpiler.RichAST) map[string]bool {
	names := make(map[string]bool)
	for _, typeMeta := range richAST.Types {
		// A test file's interface is left out: the package's library files,
		// transpiled without the tests, decide without it too.
		if !typeMeta.IsInterface || strings.HasSuffix(typeMeta.DefinedIn, "_test.gala") {
			continue
		}
		for name := range typeMeta.Methods {
			names[name] = true
		}
	}
	return names
}

// fanOutSignature reports whether method's parameters or result name a
// generic type with methods other than its receiver type, typeMeta, with its
// own type parameters.
func fanOutSignature(richAST *transpiler.RichAST, typeMeta *transpiler.TypeMetadata, method *transpiler.MethodMetadata) bool {
	own := typeMeta.Package + "." + typeMeta.Name + "[" + strings.Join(typeMeta.TypeParams, ", ") + "]"
	var visit func(t transpiler.Type) bool
	visit = func(t transpiler.Type) bool {
		switch x := t.(type) {
		case transpiler.GenericType:
			if x.String() != own && hasMethods(richAST, x.Base) {
				return true
			}
			for _, p := range x.Params {
				if p != nil && visit(p) {
					return true
				}
			}
		case transpiler.PointerType:
			return visit(x.Elem)
		case transpiler.ArrayType:
			return visit(x.Elem)
		case transpiler.MapType:
			return visit(x.Key) || visit(x.Elem)
		case transpiler.ChanType:
			return visit(x.Elem)
		case transpiler.FuncType:
			for _, p := range x.Params {
				if p != nil && visit(p) {
					return true
				}
			}
			for _, r := range x.Results {
				if r != nil && visit(r) {
					return true
				}
			}
		}
		return false
	}
	if method.ReturnType != nil && visit(method.ReturnType) {
		return true
	}
	for _, r := range method.GoResults {
		if r != nil && visit(r) {
			return true
		}
	}
	for _, p := range method.ParamTypes {
		if p != nil && visit(p) {
			return true
		}
	}
	return false
}

// hasMethods reports whether base names a non-interface type of richAST
// that declares methods.
func hasMethods(richAST *transpiler.RichAST, base transpiler.Type) bool {
	if base == nil {
		return false
	}
	typeMeta, ok := richAST.Types[base.BaseName()]
	return ok && !typeMeta.IsInterface && len(typeMeta.Methods) > 0
}
