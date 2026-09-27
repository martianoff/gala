package transpiler

import (
	"fmt"

	"martianoff/gala/internal/transpiler/registry"
)

// MaxGoResultValues is the widest Tuple (Tuple10): a Go call returning more
// values than that has no GALA value.
const MaxGoResultValues = 10

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
	// Inner is the Try's value type, or the whole value when the call does
	// not fail; Type is the whole value. Both are NilType when a result type
	// is unknown or there are more than MaxGoResultValues values.
	Inner Type
	Type  Type
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
	r.Inner, r.Type = NilType{}, NilType{}
	if r.Values > MaxGoResultValues {
		return r, true
	}
	for _, v := range values {
		if v == nil || v.IsNil() {
			return r, true
		}
	}
	if len(values) == 1 {
		r.Inner = values[0]
	} else {
		name := TypeTuple
		if len(values) > 2 {
			name = fmt.Sprintf("%s%d", TypeTuple, len(values))
		}
		r.Inner = GenericType{Base: NamedType{Package: registry.StdPackageName, Name: name}, Params: values}
	}
	r.Type = r.Inner
	if r.Fails {
		r.Type = GenericType{Base: NamedType{Package: registry.StdPackageName, Name: TypeTry}, Params: []Type{r.Inner}}
	}
	return r, true
}
