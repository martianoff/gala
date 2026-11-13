package transformer

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// A method of a generic type that names another generic type is generated as
// a free function (MethodMetadata.IsGeneric without type parameters of its
// own; see the analyzer's markFanOutMethods), so it is not in the type's Go
// method set. A type that has every method of an interface only through such
// a method does not implement the interface in Go.

// loweredMethodHint is the fix for a value used as an interface its type
// implements only through a method generated as a free function.
const loweredMethodHint = "use the concrete type, or declare the interface where the type's package sees it so the method stays a method"

// loweredMethodReason says why a type lacks method name in Go.
func loweredMethodReason(name string) string {
	return fmt.Sprintf("its %s is generated as a free function (it names another generic type), so the type does not have it in Go", name)
}

// checkLoweredInterfacePattern rejects a type pattern `case x: I` (GALA-E0109)
// when I is an interface that some type has every method of, at least one of
// them only as a method generated as a free function: Go's type test would
// never match a value of that type, and the arm would silently fall through.
// It reports the first such type by name, so the message is the same on every
// run.
func (t *galaASTTransformer) checkLoweredInterfacePattern(typ transpiler.Type, at srcNode) error {
	if t.richAST == nil || typ == nil || typ.IsNil() {
		return nil
	}
	iface := t.getTypeMeta(typ.BaseName())
	if iface == nil || !iface.IsInterface || len(iface.Methods) == 0 {
		return nil
	}
	wanted := slices.Sorted(maps.Keys(iface.Methods))
	for _, name := range slices.Sorted(maps.Keys(t.richAST.Types)) {
		meta := t.richAST.Types[name]
		if meta.IsInterface {
			continue
		}
		lowered := ""
		hasAll := true
		for _, m := range wanted {
			mm, ok := meta.Methods[m]
			if !ok || !sameShape(mm, iface.Methods[m]) {
				hasAll = false
				break
			}
			if lowered == "" && mm.IsGeneric && len(mm.TypeParams) == 0 {
				lowered = m
			}
		}
		if !hasAll || lowered == "" {
			continue
		}
		r := at.rng()
		return spanErrorAt(galaerr.CodeInterfaceViaLoweredMethod, r, r,
			fmt.Sprintf("a value of %s never matches `%s`: %s", meta.Name, displayType(typ), loweredMethodReason(lowered)),
			fmt.Sprintf("match the concrete type (`case x: %s`) and call %s on it, or declare %s where %s's package sees it so the method stays a method", meta.Name, lowered, displayType(typ), meta.Name))
	}
	return nil
}

// sameShape reports whether a type's method have could implement the
// interface method want, as far as a generic type's metadata, without its
// type arguments, tells: the same number of parameters, and results of the
// same base types.
func sameShape(have, want *transpiler.MethodMetadata) bool {
	if want == nil || len(have.ParamTypes) != len(want.ParamTypes) || have.Variadic != want.Variadic {
		return false
	}
	return resultBase(have) == resultBase(want)
}

// resultBase is the unqualified base type name of m's result, "" for none:
// the declaring package and its user may spell its package differently.
func resultBase(m *transpiler.MethodMetadata) string {
	if m.GoResults != nil || !transpiler.HasResult(m.ReturnType) {
		return ""
	}
	name := m.ReturnType.BaseName()
	return name[strings.LastIndex(name, ".")+1:]
}
