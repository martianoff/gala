package transformer

import (
	"fmt"
	"go/ast"
	"strings"
	"unicode"
	"unicode/utf8"

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
	xType = unwrapValueMarkers(xType)
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

// rangeOverTypeParamError reports the ranged expression e, of type xType,
// when xType is a type parameter whose constraint has no core type: `any`,
// `comparable`, a GALA interface (methods only), or a Go interface whose
// types differ in underlying type. Go ranges over a type parameter only
// through its core type. A Go constraint whose type information is missing
// is left to Go.
func (t *galaASTTransformer) rangeOverTypeParamError(e exprSrc, xType transpiler.Type) error {
	name, c, ok := t.constrainedTypeParam(unwrapValueMarkers(xType))
	if !ok {
		return nil
	}
	if transpiler.IsInterfaceConstraint(c) {
		if data := t.goConstraintData(c); data != nil {
			if data.CoreType != nil {
				return nil
			}
		} else if meta := t.getTypeMeta(c.BaseName()); meta == nil || !meta.IsInterface {
			return nil
		}
	}
	constraint := "any"
	if c != nil && !c.IsNil() {
		constraint = t.sourceTypeName(c)
	}
	x := oneLine(e.at().sourceText())
	at := e.at().rng()
	return spanErrorAt(galaerr.CodeRangeOverTypeParamNoCore, at, at,
		fmt.Sprintf("cannot range over `%s`: its type %s is constrained by %s, whose types do not share one underlying type", x, name, constraint),
		"use a constraint whose types share one underlying type (`interface{ ~int64 }`), or range over a value of a concrete type")
}

// unwrapValueMarkers is typ without the Immutable and Sendable markers that
// wrap the value Go sees, in either order: the type Go ranges over or spreads.
func unwrapValueMarkers(typ transpiler.Type) transpiler.Type {
	for {
		if elem, ok := transpiler.ImmutableElem(typ); ok {
			typ = elem
		} else if elem, ok := transpiler.UnwrapSendable(typ); ok {
			typ = elem
		} else {
			return typ
		}
	}
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
	goMethods := t.goMethodsOnGalaType(meta)
	var forms []string
	for _, job := range rangeLoopForms {
		for _, f := range job {
			_, inGala := meta.Methods[f.method]
			_, inGo := goMethods[f.method]
			if inGala || inGo {
				forms = append(forms, fmt.Sprintf(f.form, receiverText(x)))
				break
			}
		}
	}
	if len(forms) == 0 {
		if len(meta.Methods) == 0 && meta.Package != t.packageName {
			// Another package's type may have been loaded without its
			// methods (canJudgeMembers), so none can be named.
			return fmt.Sprintf("iterate with a method of %s, such as ForEach or FoldLeft, or range over a Go slice or map it gives", meta.Name)
		}
		fields := t.rangeableFields(meta)
		if len(fields) == 0 {
			return fmt.Sprintf("give %s a method that visits its elements", meta.Name)
		}
		return fmt.Sprintf("range over its Go slice or map field %s, or give %s a method that visits its elements", strings.Join(fields, " or "), meta.Name)
	}
	if len(forms) > 1 {
		forms[len(forms)-1] = "or " + forms[len(forms)-1]
	}
	return "iterate with a method; here, " + strings.Join(forms, ", ")
}

// rangeableFields names the fields of meta's type, reachable from this
// package, whose declared type is a Go slice or map.
func (t *galaASTTransformer) rangeableFields(meta *transpiler.TypeMetadata) []string {
	var out []string
	for _, name := range meta.FieldNames {
		if meta.Package != t.packageName && !ast.IsExported(name) {
			continue
		}
		switch t.followAliasChain(meta.Fields[name]).(type) {
		case transpiler.ArrayType, transpiler.MapType:
			out = append(out, "`"+name+"`")
		}
	}
	return out
}

// receiverText is x as the receiver of a method call: bare when it is a chain
// of names, selectors, calls and indexes (`xs`, `b.items`, `ListOf(1, 2)`),
// parenthesized otherwise, so that the hint for `if (c) xs else ys` or `*p`
// calls the method on the whole expression rather than on its last operand.
// Brackets inside a string or rune literal are not counted.
func receiverText(x string) string {
	isName := func(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }
	if r, _ := utf8.DecodeRuneInString(x); r == utf8.RuneError || !isName(r) || unicode.IsDigit(r) {
		return "(" + x + ")"
	}
	depth := 0
	var quote rune
	escaped := false
	for _, r := range x {
		switch {
		case quote != 0:
			switch {
			case escaped:
				escaped = false
			case r == '\\' && quote != '`':
				escaped = true
			case r == quote:
				quote = 0
			}
		case r == '"' || r == '\'' || r == '`':
			quote = r
		case r == '(' || r == '[' || r == '{':
			depth++
		case r == ')' || r == ']' || r == '}':
			depth--
		case depth == 0 && r != '.' && !isName(r):
			return "(" + x + ")"
		}
	}
	return x
}
