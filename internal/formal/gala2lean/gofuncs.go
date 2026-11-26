package gala2lean

import (
	"fmt"
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
// qualifier resolves to that package in its own file (qualifyGoImports).

// versionElem is a major-version path element, `v2` in `math/rand/v2`.
var versionElem = regexp.MustCompile(`^v[0-9]+$`)

// importNames are the names a file may refer to import imp by: its alias,
// or, without one, the names Go's convention gives a package of that path
// (its last element; the one before a major version, `rand` for
// `math/rand/v2`; the part before a dot, `yaml` for `gopkg.in/yaml.v3`).
// A dot or blank import has none. Taking every candidate can only make a
// name mean more than one package in a file, which qualifyGoImports then
// leaves alone.
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

// isGoPackage reports whether qualifier q is Go's package goPath: the name
// qualifyGoImports gives it, which nothing else can have.
func isGoPackage(q coreexport.Node, goPath string) bool {
	n, ok := q.(*coreexport.Name)
	return ok && n.Name == goPackageName(goPath)
}

// goPackagePrefix starts the name a qualifier of a modelled Go package is
// renamed to (qualifyGoImports). GALA rejects a declaration whose name
// starts with _gala_ (GALA-E0097), so no declaration or local has it.
const goPackagePrefix = "_gala_go_"

// goPackageName is the name a qualifier of Go's package path is renamed to:
// the path with `_` written `_u` and `/` written `_s`, so different paths
// never share a name.
func goPackageName(path string) string {
	return goPackagePrefix + goPathEscaper.Replace(path)
}

// goPathEscaper writes a package path as part of an identifier, injectively.
var goPathEscaper = strings.NewReplacer("_", "_u", "/", "_s")

// goModelled reports whether Go's package path has a function modelled here.
func goModelled(path string) bool {
	_, ok := goFuncs[path]
	return ok || path == "fmt"
}

// qualifyGoImports is p with, in each file, every qualifier `q` of a select
// `q.F` renamed to goPackageName(path) where the file imports Go's package
// path, one modelled here, as q, and nothing else as q: imports resolve per
// file, in GALA as in Go. A file where q is also bound (a val, var,
// parameter, receiver or pattern binder), or where the program declares q,
// keeps its references: a local `strings` is never taken for the package.
// That is conservative: a binder anywhere in the file, even out of scope of
// a call, leaves the file's calls reported. So a renaming only ever lets a
// call of a modelled function translate in a file that imports its package.
func qualifyGoImports(p *coreexport.Program) *coreexport.Program {
	declared := programDecls(p)
	out := *p
	out.Files = make([]*coreexport.File, len(p.Files))
	for i, f := range p.Files {
		paths := map[string][]string{}
		for _, d := range f.Decls {
			if imp, ok := d.(*coreexport.Import); ok {
				for _, name := range importNames(imp) {
					paths[name] = append(paths[name], imp.Path)
				}
			}
		}
		bound := binderNames(f)
		rename := map[string]string{}
		for q, ps := range paths {
			if len(ps) == 1 && goModelled(ps[0]) && !bound[q] && !declared[q] {
				rename[q] = goPackageName(ps[0])
			}
		}
		if len(rename) == 0 {
			out.Files[i] = f
			continue
		}
		var walk func(n coreexport.Node) coreexport.Node
		walk = func(n coreexport.Node) coreexport.Node {
			c := mapChildren(n, walk)
			if s, ok := c.(*coreexport.Select); ok {
				if t, ok := s.Target.(*coreexport.Name); ok && rename[t.Name] != "" {
					nt := *t
					nt.Name = rename[t.Name]
					s.Target = &nt
				}
			}
			return c
		}
		out.Files[i] = walk(f).(*coreexport.File)
	}
	return &out
}

// binderNames are the names file f binds anywhere: its vals and vars,
// parameters, receivers and pattern binders.
func binderNames(f *coreexport.File) map[string]bool {
	bound := map[string]bool{}
	var visit func(n coreexport.Node)
	visit = func(n coreexport.Node) {
		switch n := n.(type) {
		case *coreexport.Val:
			for _, x := range n.Names {
				bound[x] = true
			}
		case *coreexport.Var:
			for _, x := range n.Names {
				bound[x] = true
			}
		case *coreexport.Param:
			bound[n.Name] = true
		case *coreexport.Receiver:
			bound[n.Name] = true
		case *coreexport.PatName:
			bound[n.Name] = true
		}
		walkChildren(n, visit)
	}
	visit(f)
	return bound
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
		if f, ok := fs[s.Name]; ok && isGoPackage(s.Target, pkg) {
			return f, true
		}
	}
	return goFunc{}, false
}

// goCall translates a call of a modelled Go function: its arguments in the
// order written, then the prelude's model; ok is false for any other call.
func (g *gen) goCall(e *coreexport.Call, s *coreexport.Select, sc *scope) (term, bool) {
	if nr, ok := g.newReplacerOf(e, s, sc); ok {
		return g.replacerReplace(e, nr, sc), true
	}
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
		args[i] = g.argExpect(a, sc, &coreexport.TypeName{Name: f.params[i]})
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
	if _, ok := g.newReplacerOf(e, s, sc); ok {
		return "String"
	}
	if f, ok := g.goFuncOf(e, s, sc); ok {
		return g.leanType(&coreexport.TypeName{Name: f.result})
	}
	return ""
}

// newReplacerOf is, for call e of s, `strings.NewReplacer(oldnew...).Replace(x)`,
// the NewReplacer call. A Replacer is translated only so, built and used at
// once: no Replacer value is held anywhere else.
func (g *gen) newReplacerOf(e *coreexport.Call, s *coreexport.Select, sc *scope) (*coreexport.Call, bool) {
	nr, ok := s.Target.(*coreexport.Call)
	if !ok || s.Name != "Replace" || e.Bracket != nil || nr.Bracket != nil {
		return nil, false
	}
	q, ok := nr.Callee.(*coreexport.Select)
	if !ok || q.Name != "NewReplacer" || !isGoPackage(q.Target, "strings") {
		return nil, false
	}
	return nr, true
}

// replacerReplace translates `strings.NewReplacer(oldnew...).Replace(x)` (e,
// with nr the NewReplacer call), in Go's order: the old/new strings, then
// NewReplacer (which panics on an odd count), then x, then the replacement
// (the prelude's goNewReplacer and goReplacerReplace).
func (g *gen) replacerReplace(e, nr *coreexport.Call, sc *scope) term {
	if len(e.Args) != 1 {
		unsup(e.Pos, "argument-count")
	}
	positional(e.Args[0])
	list := g.goStringArgs(nr, sc)
	x := g.argExpect(e.Args[0], sc, &coreexport.TypeName{Name: "string"})
	return act(fmt.Sprintf("goReplacerReplace (← goNewReplacer %s) %s", list.val(), x.val()))
}

// goStringArgs are the arguments of call c of a Go function taking
// ...string, as a Lean list of strings: the arguments written, or one
// spread, of a variadic parameter, or of an Array or List's ToGoSlice() (a
// copy of its elements in order that nothing else sees: the callee copies
// its arguments).
func (g *gen) goStringArgs(c *coreexport.Call, sc *scope) term {
	if len(c.Args) == 1 {
		if sp, ok := c.Args[0].(*coreexport.Spread); ok {
			if isSliceParam(sp.Value, sc) {
				return g.spreadTerm(sp.Value, sc)
			}
			if t, ok := g.toGoSliceOfStrings(sp.Value, sc); ok {
				return t
			}
			unsup(sp.Pos, "spread-arg")
		}
	}
	parts := make([]string, len(c.Args))
	subs := make([]term, len(c.Args))
	for i, a := range c.Args {
		positional(a)
		subs[i] = g.argExpect(a, sc, &coreexport.TypeName{Name: "string"})
		parts[i] = subs[i].val()
	}
	return wrap("(["+strings.Join(parts, ", ")+"] : List String)", subs...)
}

// toGoSliceOfStrings is `xs.ToGoSlice()` of an Array or List of strings xs,
// as the list of its elements; ok is false for anything else.
func (g *gen) toGoSliceOfStrings(v coreexport.Node, sc *scope) (term, bool) {
	tc, ok := v.(*coreexport.Call)
	if !ok || len(tc.Args) != 0 || tc.Bracket != nil {
		return term{}, false
	}
	ts, ok := tc.Callee.(*coreexport.Select)
	if !ok || ts.Name != "ToGoSlice" || !g.collections {
		return term{}, false
	}
	if _, elem, ok := seqOf(g.typeOf(ts.Target, sc)); !ok || elem != "String" {
		return term{}, false
	}
	t := g.expr(ts.Target, sc)
	return wrap("("+t.val()+").data", t), true
}

// isGoPrint reports whether call c is `fmt.Println(...)` or `fmt.Print(...)`
// of Go's fmt.
func (g *gen) isGoPrint(c *coreexport.Call, sc *scope) (*coreexport.Select, bool) {
	s, ok := c.Callee.(*coreexport.Select)
	if !ok || c.Bracket != nil || (s.Name != "Println" && s.Name != "Print") || !isGoPackage(s.Target, "fmt") {
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
