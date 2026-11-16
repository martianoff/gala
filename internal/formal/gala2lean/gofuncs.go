package gala2lean

import (
	"path"
	"regexp"
	"strings"

	"martianoff/gala/internal/formal/coreexport"
)

// Functions of Go's standard library, called through their package:
// `fmt.Println(x)`. Each is translated only where a trusted model says what
// it does, after the Go source of the release the repository pins (Go
// 1.25.5): code of the prelude, or an assumption stated here that it
// matches one the prelude already has. Each is tested, on the examples the
// differential run compares with `go run` and on a case of testdata whose
// output the gala CLI printed; none is proved (docs/FORMAL_SEMANTICS.MD,
// "Trusted Go models"). A call is taken as one of them only when its
// qualifier resolves to that package: no local, package-level value or
// declaration hides it, and every import the program may refer to by that
// name is the package.

// versionElem is a major-version path element, `v2` in `math/rand/v2`.
var versionElem = regexp.MustCompile(`^v[0-9]+$`)

// importNames are the names a file may refer to import imp by: its alias,
// or, without one, the names Go's convention gives a package of that path
// (its last element; the one before a major version, `rand` for
// `math/rand/v2`; the part before a dot, `yaml` for `gopkg.in/yaml.v3`).
// A dot or blank import has none. Taking every candidate can only make a
// name mean more than one package, which goPackage then rejects.
func importNames(imp *coreexport.Import) []string {
	switch imp.Alias {
	case ".", "_":
		return nil
	case "":
	default:
		return []string{imp.Alias}
	}
	base := path.Base(imp.Path)
	names := []string{base}
	if versionElem.MatchString(base) {
		names = append(names, path.Base(path.Dir(imp.Path)))
	}
	if before, _, ok := strings.Cut(base, "."); ok {
		names = append(names, before)
	}
	return names
}

// goPackage reports whether qualifier q, at a use in scope sc, is Go's
// package goPath: the program imports it by that name, no import of another
// package may have the name (in any file: conservative), and no local,
// package-level value or declaration hides it.
func (g *gen) goPackage(q coreexport.Node, goPath string, sc *scope) bool {
	n, ok := q.(*coreexport.Name)
	if !ok || sc.lookup(n.Name) != nil || g.globalBy[n.Name] != nil || g.declared(n.Name) {
		return false
	}
	paths := g.importPaths[n.Name]
	if len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		if p != goPath {
			return false
		}
	}
	return true
}

// goFunc is a function of Go's standard library the prelude models: its
// parameter types and result (GALA types: int, string, bool), the prelude
// function, and whether that is an action (it can panic).
type goFunc struct {
	params []string
	result string
	lean   string
	act    bool
}

// goFuncs are the modelled functions, by package path and name. strings'
// functions match characters, which is Go's byte matching on the valid
// UTF-8 every translated string is (the prelude's "Trusted Go models").
var goFuncs = map[string]map[string]goFunc{
	"strings": {
		"HasPrefix":  {[]string{"string", "string"}, "bool", "goHasPrefix", false},
		"HasSuffix":  {[]string{"string", "string"}, "bool", "goHasSuffix", false},
		"TrimPrefix": {[]string{"string", "string"}, "string", "goTrimPrefix", false},
		"TrimSuffix": {[]string{"string", "string"}, "string", "goTrimSuffix", false},
		"Contains":   {[]string{"string", "string"}, "bool", "goContains", false},
		"ReplaceAll": {[]string{"string", "string", "string"}, "string", "goReplaceAll", false},
		"Repeat":     {[]string{"string", "int"}, "string", "goRepeat", true},
	},
	"strconv": {
		"Itoa": {[]string{"int"}, "string", "goItoa", false},
	},
}

// goFuncOf is the modelled function call e of q.F names, if any.
func (g *gen) goFuncOf(e *coreexport.Call, s *coreexport.Select, sc *scope) (goFunc, bool) {
	if e.Bracket != nil {
		return goFunc{}, false
	}
	for pkg, fs := range goFuncs {
		if f, ok := fs[s.Name]; ok && g.goPackage(s.Target, pkg, sc) {
			return f, true
		}
	}
	return goFunc{}, false
}

// goCall translates a call of a modelled Go function: its arguments in the
// order written, then the prelude's model; ok is false for any other call.
func (g *gen) goCall(e *coreexport.Call, s *coreexport.Select, sc *scope) (term, bool) {
	f, ok := g.goFuncOf(e, s, sc)
	if !ok {
		return term{}, false
	}
	if len(e.Args) != len(f.params) {
		unsup(e.Pos, "argument-count")
	}
	args := make([]term, len(e.Args))
	vals := make([]string, len(e.Args))
	for i, a := range e.Args {
		positional(a)
		args[i] = g.exprExpect(a, sc, &coreexport.TypeName{Name: f.params[i]})
		vals[i] = args[i].val()
	}
	code := "(" + f.lean + " " + strings.Join(vals, " ") + ")"
	if f.act {
		return act(code), true
	}
	return wrap(code, args...), true
}

// goCallType is exprType of a call of a modelled Go function, "" for any
// other call.
func (g *gen) goCallType(e *coreexport.Call, s *coreexport.Select, sc *scope) string {
	if f, ok := g.goFuncOf(e, s, sc); ok {
		return map[string]string{"int": "Int64", "string": "String", "bool": "Bool"}[f.result]
	}
	return ""
}

// isGoPrint reports whether call c is `fmt.Println(...)` or `fmt.Print(...)`
// of Go's fmt.
func (g *gen) isGoPrint(c *coreexport.Call, sc *scope) (*coreexport.Select, bool) {
	s, ok := c.Callee.(*coreexport.Select)
	if !ok || c.Bracket != nil || (s.Name != "Println" && s.Name != "Print") || !g.goPackage(s.Target, "fmt", sc) {
		return nil, false
	}
	return s, true
}

// goPrintStmt translates a statement `fmt.Println(args)` or
// `fmt.Print(args)`; ok is false for any other statement. The model is an
// assumption, not new code: Go prints an operand of type int, string or
// bool (fmt's %v: the decimal int, the string itself, true or false) as
// GALA's Println and Print do, with the same spacing, which the prelude
// models (gprintln, gprint). So only operands of those types are
// translated; any other is reported (fmt-operand). Used as a value, the
// call's results (n int, err error) are outside the subset (fmt-value, in
// call).
func (g *gen) goPrintStmt(st coreexport.Node, sc *scope) ([]string, bool) {
	c, ok := st.(*coreexport.Call)
	if !ok {
		return nil, false
	}
	s, ok := g.isGoPrint(c, sc)
	if !ok {
		return nil, false
	}
	for _, a := range c.Args {
		if !isBasic(g.typeOf(argNode(a), sc)) {
			unsup(posOf(a), "fmt-operand")
		}
	}
	// GALA's own Println and Print, which run the operands in order first.
	return []string{g.print(s.Name, c, sc).asItem()}, true
}
