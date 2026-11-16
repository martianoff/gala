package transformer

import (
	"fmt"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// Range over a GALA type
//
// `for _, x := range xs` is lowered to a Go range statement as written. Go
// ranges over slices, arrays, maps, strings, integers, channels and functions;
// a GALA struct, which every GALA collection is, is none of them, so the loop
// used to pass every GALA check and fail `go build` with "cannot range over xs
// (variable of struct type collection_immutable.Array[int])". It is
// GALA-E0133 here, with the methods the type has for the job.

// rangeOverGalaTypeError reports the ranged expression e, of type xType, when
// xType is a GALA struct or a pointer to one. A Go type, an interface, an
// opaque type (a scalar underneath, and Go ranges over an integer) and an
// unresolved type are left to Go, so valid code is never rejected.
func (t *galaASTTransformer) rangeOverGalaTypeError(e exprSrc, xType transpiler.Type) error {
	// Immutable and Sendable wrap the value Go ranges over.
	if elem, ok := transpiler.ImmutableElem(xType); ok {
		xType = elem
	}
	xType, _ = transpiler.UnwrapSendable(xType)
	impl, ok := t.implTypeOf(xType)
	if !ok || impl.meta.IsOpaque {
		return nil
	}
	// A multi-line expression is spelled on one line in the message and in
	// the hint's forms.
	x := oneLine(e.at().sourceText())
	at := e.at().rng()
	return spanErrorAt(galaerr.CodeRangeOverGalaType, at, at,
		fmt.Sprintf("cannot range over `%s`: it is a %s, and `range` takes a Go slice, array, map, string, integer, channel or function", x, displayType(xType)),
		t.rangeOverGalaTypeHint(impl.meta, x))
}

// rangeLoopForms lists, per job a range loop does — visiting each element,
// the index, an accumulator, and a Go slice or map for a loop that has to
// stay one — the methods that do it, best first, each with its form on a
// receiver %[1]s.
var rangeLoopForms = [][]struct{ method, form string }{
	{{"ForEachKV", "`%[1]s.ForEachKV((k, v) => ...)`"}, {"ForEach", "`%[1]s.ForEach((x) => ...)`"}},
	{{"ZipWithIndex", "`%[1]s.ZipWithIndex()` for the index"}},
	{{"FoldLeftKV", "`%[1]s.FoldLeftKV(init, (acc, k, v) => ...)` for an accumulator"}, {"FoldLeft", "`%[1]s.FoldLeft(init, (acc, x) => ...)` for an accumulator"}},
	{{"ToGoMap", "`range %[1]s.ToGoMap()` if a Go loop is really needed"}, {"ToGoSlice", "`range %[1]s.ToGoSlice()` if a Go loop is really needed"}},
}

// rangeOverGalaTypeHint names, for each job in rangeLoopForms, the first
// method meta's type has, spelled on x. A type with none of them, a user
// struct, is told which of its fields Go can range over, if any, and to give
// itself such a method.
func (t *galaASTTransformer) rangeOverGalaTypeHint(meta *transpiler.TypeMetadata, x string) string {
	var forms []string
	for _, job := range rangeLoopForms {
		for _, f := range job {
			if t.hasMemberOrGoMethod(meta, f.method) {
				forms = append(forms, fmt.Sprintf(f.form, receiverText(x)))
				break
			}
		}
	}
	if len(forms) == 0 {
		fields := t.rangeableFields(meta)
		if len(fields) == 0 {
			return fmt.Sprintf("give %s a method that visits its elements", meta.Name)
		}
		return fmt.Sprintf("range over a field Go can range over, %s, or give %s a method that visits its elements", strings.Join(fields, " or "), meta.Name)
	}
	if len(forms) > 1 {
		forms[len(forms)-1] = "or " + forms[len(forms)-1]
	}
	return "iterate with a method; here, " + strings.Join(forms, ", ")
}

// rangeableFields names the fields of meta's type whose declared type is a
// Go slice or map.
func (t *galaASTTransformer) rangeableFields(meta *transpiler.TypeMetadata) []string {
	var out []string
	for _, name := range meta.FieldNames {
		switch t.followAliasChain(meta.Fields[name]).(type) {
		case transpiler.ArrayType, transpiler.MapType:
			out = append(out, "`"+name+"`")
		}
	}
	return out
}

// receiverText is x as the receiver of a method call: parenthesized unless
// it has no space outside its brackets, so that the hint for
// `if (c) xs else ys` calls the method on the whole expression, not on ys.
func receiverText(x string) string {
	depth := 0
	for _, r := range x {
		switch r {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ' ':
			if depth == 0 {
				return "(" + x + ")"
			}
		}
	}
	return x
}
