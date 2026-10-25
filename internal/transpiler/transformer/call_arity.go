package transformer

import (
	"fmt"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// Argument count of a call (GALA-E0082)
//
// A call of a GALA function, method or struct constructor with too many
// positional arguments, or too few to cover the parameters that declare no
// default, used to be emitted as written. `go build` then reported it against
// the generated call ("not enough arguments in call to fs.WriteFileString
// have (string, string) want (string, string, int)"), in Go's vocabulary; a
// struct called with one argument too many was reported as GALA-E0043, as if
// the type had no constructor at all. The checks here report both in GALA
// terms, naming the parameters the callee declares.
//
// Only callees GALA declared are checked. A Go function or method keeps Go's
// own check: its metadata is synthesized from the Go type, and a call of it
// can legitimately pass one multi-result Go call for several parameters.

// paramList is the parameter list of a GALA callee, as a call's argument
// count is checked against it.
type paramList struct {
	callee   string // the callee's declared name, for the message
	names    []string
	defaults map[int]transpiler.DefaultExpr
	variadic bool // the last parameter is `...T`
}

func funcParamList(callee string, fm *transpiler.FunctionMetadata) paramList {
	return paramList{callee: callee, names: paddedParamNames(fm.ParamNames, len(fm.ParamTypes)), defaults: fm.DefaultExprs, variadic: fm.Variadic}
}

func methodParamList(callee string, mm *transpiler.MethodMetadata) paramList {
	return paramList{callee: callee, names: paddedParamNames(mm.ParamNames, len(mm.ParamTypes)), defaults: mm.DefaultExprs, variadic: mm.Variadic}
}

// paddedParamNames is names with one entry per parameter: metadata that lost
// a name still counts the parameter.
func paddedParamNames(names []string, count int) []string {
	if len(names) >= count {
		return names[:count]
	}
	out := make([]string, count)
	copy(out, names)
	return out
}

// fixed is the number of parameters a positional argument fills one to one:
// all of them, less a variadic last one.
func (p paramList) fixed() int {
	if p.variadic && len(p.names) > 0 {
		return len(p.names) - 1
	}
	return len(p.names)
}

// required is the number of leading parameters a call must pass: those up to
// the last one that declares no default. (A defaulted parameter is never
// followed by a required one, GALA-E0013.)
func (p paramList) required() int {
	n := 0
	for i := 0; i < p.fixed(); i++ {
		if _, ok := p.defaults[i]; !ok {
			n = i + 1
		}
	}
	return n
}

// checkPositional reports a call passing nargs positional arguments and no
// named one: more than the callee takes, or fewer than it requires.
func (p paramList) checkPositional(nargs, line, col int) error {
	if !p.variadic && nargs > len(p.names) {
		return p.tooManyError(nargs, line, col)
	}
	for i := nargs; i < p.fixed(); i++ {
		if _, ok := p.defaults[i]; !ok {
			return p.missingError(i, nargs, line, col)
		}
	}
	return nil
}

// tooManyError is GALA-E0082 for a call passing nargs arguments, some by
// name, to a callee that takes fewer. A variadic callee is reported here only
// for a call with named arguments, which fill one slot per parameter, so its
// variadic parameter takes at most one argument.
func (p paramList) tooManyError(nargs, line, col int) error {
	expected := p.expected()
	if p.variadic {
		expected = fmt.Sprintf("at most %d with named arguments", len(p.names))
	}
	return galaerr.NewCodedSemanticError(galaerr.CodeArgumentCountMismatch, line, col,
		fmt.Sprintf("too many arguments in call to %s: expected %s, got %d", p.callee, expected, nargs),
		removeExtra(nargs-len(p.names))+"; "+p.signatureHint())
}

// isVariadicSlot reports whether parameter i is the variadic last one, which
// a call may leave without an argument although it declares no default.
func (p paramList) isVariadicSlot(i int) bool {
	return p.variadic && i == len(p.names)-1
}

// missingError is GALA-E0082 for a call that leaves parameter i, which
// declares no default, without an argument. nargs is the number of arguments
// the call passes, or -1 when it passes some by name.
func (p paramList) missingError(i, nargs, line, col int) error {
	msg := fmt.Sprintf("missing required argument %q (parameter %d) in call to %s", p.names[i], i+1, p.callee)
	if nargs >= 0 {
		msg += fmt.Sprintf(": expected %s, got %d", p.expected(), nargs)
	}
	return galaerr.NewCodedSemanticError(galaerr.CodeArgumentCountMismatch, line, col, msg,
		fmt.Sprintf("pass %q, or declare a default for it; %s", p.names[i], p.signatureHint()))
}

// expected is the argument count the callee accepts: "2", "1 to 3", or
// "at least 1" for a variadic callee.
func (p paramList) expected() string {
	lo := p.required()
	switch {
	case p.variadic:
		return fmt.Sprintf("at least %d", lo)
	case lo == len(p.names):
		return fmt.Sprint(lo)
	default:
		return fmt.Sprintf("%d to %d", lo, len(p.names))
	}
}

// signatureHint names the callee's parameters: `add takes (a, b = ...)`.
func (p paramList) signatureHint() string {
	parts := make([]string, len(p.names))
	for i, name := range p.names {
		_, defaulted := p.defaults[i]
		switch {
		case p.variadic && i == len(p.names)-1:
			parts[i] = name + "..."
		case defaulted:
			parts[i] = name + " = ..."
		default:
			parts[i] = name
		}
	}
	return fmt.Sprintf("%s takes (%s)", p.callee, strings.Join(parts, ", "))
}

// structTooManyArgsError is GALA-E0082 for a positional construction of a
// struct passing more arguments than it has fields.
func structTooManyArgsError(typeName string, fields []string, nargs, line, col int) error {
	return galaerr.NewCodedSemanticError(galaerr.CodeArgumentCountMismatch, line, col,
		fmt.Sprintf("too many arguments in call to %s: expected at most %d, got %d", typeName, len(fields), nargs),
		fmt.Sprintf("%s; %s has %d %s: %s", removeExtra(nargs-len(fields)),
			typeName, len(fields), plural(len(fields), "field", "fields"), strings.Join(fields, ", ")))
}

// removeExtra is the hint's first clause for a call passing n arguments too
// many, the part shown beside the caret.
func removeExtra(n int) string {
	return fmt.Sprintf("remove %d extra %s", n, plural(n, "argument", "arguments"))
}

// argIsSpread reports whether arg spreads a slice, `xs...`.
func argIsSpread(arg *grammar.ArgumentContext) bool {
	_, _, spread, _ := extractArgContent(arg)
	return spread
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
