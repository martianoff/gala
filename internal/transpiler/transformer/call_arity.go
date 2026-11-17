package transformer

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"martianoff/gala/galaerr"
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
// Only callees GALA declared are checked, except for a call passing arguments
// by name, which is laid out against the callee's parameters (layoutArgs). A
// Go function or method called by position keeps Go's own check: its metadata
// is synthesized from the Go type, and a call of it can legitimately pass one
// multi-result Go call for several parameters.

// paramList is the parameter list of a callee, as a call's argument count is
// checked against it and its arguments are laid out.
type paramList struct {
	callee   string // the callee's declared name, for the message
	names    []string
	defaults map[int]transpiler.DefaultExpr
	variadic bool // the last parameter is `...T`
	// goDeclared: the callee is declared in Go, so it takes no default.
	goDeclared bool
}

func funcParamList(callee string, fm *transpiler.FunctionMetadata) paramList {
	return paramList{callee: callee, names: paddedParamNames(fm.ParamNames, len(fm.ParamTypes)), defaults: fm.DefaultExprs, variadic: fm.Variadic}
}

func methodParamList(callee string, mm *transpiler.MethodMetadata) paramList {
	return paramList{callee: callee, names: paddedParamNames(mm.ParamNames, len(mm.ParamTypes)), defaults: mm.DefaultExprs, variadic: mm.Variadic, goDeclared: mm.GoDeclared}
}

// goParamList is the parameter list of the Go function or method sig (nil
// when unknown: no parameters), as a call passing arguments by name is laid
// out against it (layoutArgs). A Go signature declares no defaults, and a
// parameter Go leaves unnamed has the name "".
func goParamList(callee string, sig *transpiler.GoFuncSignature) paramList {
	if sig == nil {
		return paramList{callee: callee}
	}
	names := make([]string, len(sig.Params))
	for i, p := range sig.Params {
		names[i] = p.Name
	}
	return paramList{callee: callee, names: names, variadic: sig.IsVariadic, goDeclared: true}
}

// slotOf is the index of the parameter the i-th argument of a call fills,
// passed by name when name is not "": its own position, as positional
// arguments come first, or the parameter of its name (-1 for none). Its
// argument is typed against that parameter before layoutArgs places it.
func (p paramList) slotOf(i int, name string) int {
	if name == "" {
		return i
	}
	return slices.Index(p.names, name)
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

// namedArg is an argument passed by name, as layoutArgs places it.
type namedArg[A any] struct {
	name string
	arg  A
}

// sortedNamedArgs is named as layoutArgs takes it, in name order, so the
// error reported for a call with several wrong names does not depend on map
// iteration order.
func sortedNamedArgs[A any](named map[string]A) []namedArg[A] {
	out := make([]namedArg[A], 0, len(named))
	for _, name := range slices.Sorted(maps.Keys(named)) {
		out = append(out, namedArg[A]{name, named[name]})
	}
	return out
}

// layoutArgs lays a call's arguments out in p's parameter order, the order Go
// takes them in: the positional ones fill the leading parameters, and each
// named one the parameter of its name. A parameter given no argument takes its
// default, from fill (nil to only check that it has one), and a variadic last
// one given none takes no argument: the slots returned then stop before it.
// nargs is the argument count a missing-argument error names, -1 for none.
//
// Every call passing an argument by name is laid out here, whatever its
// callee: a GALA function or method, an interface method, a Go function or
// method. A callee whose parameter names are not known takes no argument by
// name; that is reported rather than guessed at.
func layoutArgs[A any](p paramList, positional []A, named []namedArg[A], nargs int, fill func(i int, slots []A) (A, error), line, col int) ([]A, error) {
	slots := make([]A, len(p.names))
	filled := make([]bool, len(p.names))
	if len(positional) > len(slots) {
		return nil, p.tooManyError(len(positional)+len(named), line, col)
	}
	for i, a := range positional {
		slots[i], filled[i] = a, true
	}
	for _, n := range named {
		idx := slices.Index(p.names, n.name)
		switch {
		case idx < 0 && slices.Contains(p.names, ""):
			return nil, unknownParamNamesError(n.name, p.callee, line, col)
		case idx < 0:
			return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf("unknown parameter %q in call to %s", n.name, p.callee))
		case idx < len(positional):
			return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf("parameter %q specified both positionally and by name in call to %s", n.name, p.callee))
		case filled[idx]:
			return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf("parameter %q is given more than once in call to %s", n.name, p.callee))
		}
		slots[idx], filled[idx] = n.arg, true
	}
	for i := range slots {
		if filled[i] {
			continue
		}
		if p.isVariadicSlot(i) {
			// A variadic parameter left out takes no argument.
			return slots[:i], nil
		}
		if _, ok := p.defaults[i]; !ok {
			return nil, p.missingError(i, nargs, line, col)
		}
		if fill != nil {
			a, err := fill(i, slots)
			if err != nil {
				return nil, err
			}
			slots[i] = a
		}
	}
	return slots, nil
}

// unknownParamNamesError reports the argument name passed by name to callee,
// whose parameter names are not known: a Go signature that leaves its
// parameters unnamed, or a method with no signature or metadata to read them
// from. Placing it by its name is impossible, and placing it where it is
// written could fill the wrong parameter.
func unknownParamNamesError(name, callee string, line, col int) error {
	return galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf(
		"cannot pass %q by name in call to %s: its parameter names are not known; pass the arguments by position", name, callee))
}

// checkSpreads reports a spread argument `xs...` of args that fills a
// parameter other than the variadic last one, the only one Go spreads a slice
// into, whether it is passed by position or by name. Callers run it only for
// a call that spreads one.
func (p paramList) checkSpreads(args argsSrc) error {
	pos := 0
	for _, a := range args.list {
		name, _ := a.name()
		idx := pos
		if name == "" {
			pos++
		} else {
			idx = slices.Index(p.names, name)
		}
		// An unknown name is reported as the call's arguments are laid out.
		if !a.spread() || idx < 0 || p.isVariadicSlot(idx) {
			continue
		}
		line, col := a.at().start()
		switch {
		case idx < len(p.names):
			return galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf(
				"a spread argument cannot fill parameter %q of %s: only a variadic last parameter takes one", p.names[idx], p.callee))
		case p.variadic:
			return galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf(
				"a spread argument must be the only argument of variadic parameter %q of %s", p.names[len(p.names)-1], p.callee))
		default:
			return galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf(
				"a spread argument cannot fill a parameter of %s: it declares no variadic parameter", p.callee))
		}
	}
	return nil
}

// missingError is GALA-E0082 for a call that leaves parameter i, which
// declares no default, without an argument. nargs is the number of arguments
// the call passes, or -1 when it passes some by name.
func (p paramList) missingError(i, nargs, line, col int) error {
	msg := fmt.Sprintf("missing required argument %q (parameter %d) in call to %s", p.names[i], i+1, p.callee)
	if nargs >= 0 {
		msg += fmt.Sprintf(": expected %s, got %d", p.expected(), nargs)
	}
	fix := fmt.Sprintf("pass %q, or declare a default for it", p.names[i])
	if p.goDeclared {
		fix = fmt.Sprintf("pass %q", p.names[i])
	}
	return galaerr.NewCodedSemanticError(galaerr.CodeArgumentCountMismatch, line, col, msg,
		fix+"; "+p.signatureHint())
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

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
