package gala2lean

import (
	"path"

	"martianoff/gala/internal/formal/coreexport"
)

// Functions of Go's standard library, called through their package:
// `fmt.Println(x)`. Each is a trusted model in the prelude, written after
// the Go source of the release the repository pins (Go 1.25.5), and tested
// against `go run`, never proved (docs/FORMAL_SEMANTICS.MD, "Trusted Go
// models"). A call is taken as one of them only when its qualifier resolves
// to that package: no local or package-level value hides it, and every
// import the program refers to by that name is the package.

// importName is the name a file refers to import imp by: its alias, or the
// last element of its path; "" for a dot or blank import.
func importName(imp *coreexport.Import) string {
	switch imp.Alias {
	case ".", "_":
		return ""
	case "":
		return path.Base(imp.Path)
	}
	return imp.Alias
}

// goPackage reports whether qualifier q, at a use in scope sc, is Go's
// package goPath: the program imports it by that name, no import of another
// package has the name, and no local or package-level value hides it.
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

// goPrintStmt translates a statement `fmt.Println(args)` or
// `fmt.Print(args)`; ok is false for any other statement. Go prints an
// operand of type int, string or bool as GALA's Println and Print do
// (fmt's %v: the decimal int, the string itself, true or false), with the
// same spacing (gprintln, gprint), so only operands of those types are
// translated; any other is reported (fmt-operand). Only a statement is
// translated: as a value, the call's results (n int, err error) are
// outside the subset.
func (g *gen) goPrintStmt(st coreexport.Node, sc *scope) ([]string, bool) {
	c, ok := st.(*coreexport.Call)
	if !ok || c.Bracket != nil {
		return nil, false
	}
	s, ok := c.Callee.(*coreexport.Select)
	if !ok || (s.Name != "Println" && s.Name != "Print") || !g.goPackage(s.Target, "fmt", sc) {
		return nil, false
	}
	for _, a := range c.Args {
		switch g.typeOf(argNode(a), sc) {
		case "Int64", "String", "Bool":
		default:
			unsup(posOf(a), "fmt-operand")
		}
	}
	// GALA's own Println and Print, which run the operands in order first.
	return []string{g.print(s.Name, c, sc).asItem()}, true
}
