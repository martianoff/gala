package transpiler

import (
	"slices"
	"strings"

	"martianoff/gala/internal/transpiler/registry"
)

// MaxTupleArity is the arity of the widest std tuple, Tuple10: a sealed
// variant's extractor returns its fields as one tuple, so a variant has at
// most that many.
const MaxTupleArity = 10

// MaxGoResultValues is the most values a Go call may return and still have a
// GALA value, one tuple of them.
const MaxGoResultValues = MaxTupleArity

// GoResultValue is the one GALA value a Go call returning several results is
// presented as:
//
//	(T, error)     → Try[T]
//	(A, B)         → Tuple[A, B]      (A, B, C) → Tuple3[A, B, C] … Tuple10
//	(A, B, error)  → Try[Tuple[A, B]]
//
// The transformer converts such a call where it is used as a value, and the
// language server shows the same type, so both read it from here.
type GoResultValue struct {
	Values int  // results that are values, a trailing error excluded
	Fails  bool // the last result is `error`: the value is a Try
	// Type is the whole value; NilType when a result type is unknown or
	// there are more than MaxGoResultValues values.
	Type Type
}

// GoResultValueOf describes the GALA value of a Go call with the given
// results. ok is false for fewer than two results, which are one value already.
func GoResultValueOf(returns []Type) (r GoResultValue, ok bool) {
	if len(returns) < 2 {
		return GoResultValue{}, false
	}
	last := returns[len(returns)-1]
	r.Fails = last != nil && last.String() == "error"
	values := returns
	if r.Fails {
		values = returns[:len(returns)-1]
	}
	r.Values = len(values)
	r.Type = NilType{}
	for _, v := range values {
		if v == nil || v.IsNil() {
			return r, true
		}
	}
	var inner Type
	if len(values) == 1 {
		inner = values[0]
	} else {
		name, fits := TupleArityName(len(values))
		if !fits {
			return r, true
		}
		inner = GenericType{Base: NamedType{Package: registry.StdPackageName, Name: name}, Params: values}
	}
	r.Type = inner
	if r.Fails {
		r.Type = GenericType{Base: NamedType{Package: registry.StdPackageName, Name: TypeTry}, Params: []Type{inner}}
	}
	return r, true
}

// GoResultsOf is the inverse of GoResultValueOf: the n Go results a GALA value
// of type value is spread over, where Go expects n results — `Try[T]` is
// `(T, error)`, `Try[Tuple[A, B]]` is `(A, B, error)`, `Tuple[A, B]` is
// `(A, B)`. ok is false when value cannot make n results.
func GoResultsOf(value Type, n int) (results []Type, ok bool) {
	gen, isGeneric := value.(GenericType)
	if !isGeneric || n < 2 {
		return nil, false
	}
	base, isNamed := gen.Base.(NamedType)
	if !isNamed || base.Package != registry.StdPackageName {
		return nil, false
	}
	errorType := BasicType{Name: "error"}
	switch {
	case base.Name == TypeTry && len(gen.Params) == 1:
		if n == 2 {
			return []Type{gen.Params[0], errorType}, true
		}
		values, ok := tupleComponents(gen.Params[0], n-1)
		if !ok {
			return nil, false
		}
		return append(values, errorType), true
	default:
		return tupleComponents(value, n)
	}
}

// tupleComponents returns the component types of value when it is the std
// Tuple of n values.
func tupleComponents(value Type, n int) ([]Type, bool) {
	gen, isGeneric := value.(GenericType)
	if !isGeneric || len(gen.Params) != n {
		return nil, false
	}
	name, fits := TupleArityName(n)
	if base, isNamed := gen.Base.(NamedType); !fits || !isNamed || base.Package != registry.StdPackageName || base.Name != name {
		return nil, false
	}
	return slices.Clone(gen.Params), true
}

// TupleArityName returns the std type name of a tuple of n values: `Tuple` for
// 2, `Tuple3` … `Tuple10` above. ok is false outside 2..10.
func TupleArityName(n int) (name string, ok bool) {
	switch n {
	case 2:
		return TypeTuple, true
	case 3:
		return TypeTuple3, true
	case 4:
		return TypeTuple4, true
	case 5:
		return TypeTuple5, true
	case 6:
		return TypeTuple6, true
	case 7:
		return TypeTuple7, true
	case 8:
		return TypeTuple8, true
	case 9:
		return TypeTuple9, true
	case 10:
		return TypeTuple10, true
	}
	return "", false
}

// PlaceholderNames renders binding names for a Go call's n results, the last
// one `err` when the call fails — `v, err` / `a, b, err` / `a, b, c` — for a
// hint or hover that shows the multi-name binding `val v, err = call`.
func PlaceholderNames(n int, fails bool) string {
	if fails && n == 2 {
		return "v, err"
	}
	names := make([]string, n)
	for i := range names {
		names[i] = string(rune('a' + i))
	}
	if fails && n > 0 {
		names[n-1] = "err"
	}
	return strings.Join(names, ", ")
}
