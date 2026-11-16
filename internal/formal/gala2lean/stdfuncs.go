package gala2lean

import (
	"fmt"
	"strings"

	"martianoff/gala/internal/formal/coreexport"
)

// Functions of std and of the immutable collections, and Go's int
// conversions, called by name. A name is taken as one of these only when
// it resolves to it: the program declares no such name, the file the name is
// in dot-imports no package but std and the immutable collections (another,
// such as the mutable collections with their own ArrayTabulate, might be
// the one it means), and, for ArrayOf, ArrayTabulate and the other names of
// the immutable collections, that file dot-imports them (hideLibNames).
// Each is translated after the code it cites.

// stdPath is the import path of GALA's standard library.
const stdPath = "martianoff/gala/std"

// stdFunc is one function modelled here: how many arguments it takes, and
// whether it is the immutable collections' (seq) rather than std's or a Go
// conversion.
type stdFunc struct {
	arity int
	seq   bool
}

// stdFuncs are the functions modelled here.
var stdFuncs = map[string]stdFunc{
	"When": {arity: 2}, "Unless": {arity: 2}, "Tuple": {arity: 2}, "Tuple3": {arity: 3}, "Tuple4": {arity: 4},
	"ArrayTabulate": {arity: 2, seq: true}, "ArrayFill": {arity: 2, seq: true},
	"int": {arity: 1}, "int64": {arity: 1},
}

// libName reports whether bare name x means the library's x: the program
// declares nothing named x (a function, type or package-level value; one
// declared in any file of a package is seen in all of them), and seq names
// need the immutable collections dot-imported. Where the file the name is in
// rules the library out, hideLibNames has already renamed it.
func (g *gen) libName(x string, seq bool) bool {
	return (!seq || g.collections) && !g.declared(x) && !g.declaredType(x) &&
		g.globalBy[x] == nil
}

// Names resolve per file, as in the Go the transpiler writes, which keeps
// each file's imports: a dot import makes its package's names visible in
// its own file only. A reference to a modelled library name, in a file where
// the name might not mean the library's, is renamed: with otherDotPrefix
// where another dot-imported package might declare it, with noCollPrefix
// where the file does not dot-import the immutable collections it belongs
// to. GALA rejects a declaration whose name starts with _gala_ (GALA-E0097)
// and no library table holds a renamed name, so it resolves to nothing and
// is reported, under the name written (unhidden).
const (
	otherDotPrefix = "_gala_hidden_dot_"
	noCollPrefix   = "_gala_hidden_coll_"
)

// libNames classifies name x: fn when it is one of the library functions
// libName decides (stdFuncs, seqCtors), seq when it is a name of the
// immutable collections the translation gives a meaning (their functions,
// Array and List, and the HashMap and HashSet names it reports, hash-order).
func libNames(x string) (fn, seq bool) {
	f, isStd := stdFuncs[x]
	t, isGen := stdGeneric[x]
	fn = isStd || seqCtors[x] != ""
	seq = seqCtors[x] != "" || hashTypes[x] || isStd && f.seq || isGen && t.collections
	return fn, seq
}

// hideLibNames is p with references renamed where a file's dot imports rule
// the library out: in a file that dot-imports a package other than std and
// the immutable collections, each library function (fn), which that package
// might declare; in a file that does not dot-import the immutable
// collections, each of their names (seq), as a name or a type name. A name
// the program declares (a function, type, case or package-level value; seen
// in every file of its package) is left alone: libName and stdType already
// rule the library out for it. Binders are never renamed, so a renaming can
// only stop a translation, never change what a translated name means; a
// local named like a library name, in such a file, is reported too. Std's
// Some, None, Left, Right and its Option, Either and tuple types keep the
// program-wide rule they had.
func hideLibNames(p *coreexport.Program) *coreexport.Program {
	declared := map[string]bool{}
	for _, f := range p.Files {
		for _, d := range f.Decls {
			for _, x := range append(declNames(d), globalNames(d)...) {
				declared[x] = true
			}
		}
	}
	hidden := func(x string, other, collections bool) string {
		fn, seq := libNames(x)
		switch {
		case declared[x]:
			return ""
		case other && fn && isUpper(x):
			// A dot import brings in exported names only: never `int64`.
			return otherDotPrefix
		case seq && !collections:
			return noCollPrefix
		}
		return ""
	}
	out := *p
	out.Files = make([]*coreexport.File, len(p.Files))
	for i, f := range p.Files {
		other, collections := fileDotImports(f)
		if collections && !other {
			out.Files[i] = f // nothing to rename
			continue
		}
		// newName is the name reference n takes, "" when it keeps its own.
		newName := func(n coreexport.Node) string {
			switch n := n.(type) {
			case *coreexport.Name:
				if pre := hidden(n.Name, other, collections); pre != "" {
					return pre + n.Name
				}
			case *coreexport.TypeName:
				// A collections type (Array, List, HashMap, HashSet): `int`
				// is a type and a conversion.
				_, isType := stdGeneric[n.Name]
				isType = isType || hashTypes[n.Name]
				if pre := hidden(n.Name, other, collections); pre == noCollPrefix && isType && n.Package == "" {
					return pre + n.Name
				}
			}
			return ""
		}
		found := false
		var scan func(n coreexport.Node)
		scan = func(n coreexport.Node) {
			found = found || newName(n) != ""
			if !found {
				walkChildren(n, scan)
			}
		}
		scan(f)
		if !found {
			out.Files[i] = f
			continue
		}
		var rename func(n coreexport.Node) coreexport.Node
		rename = func(n coreexport.Node) coreexport.Node {
			c := mapChildren(n, rename)
			if x := newName(n); x != "" {
				switch c := c.(type) {
				case *coreexport.Name:
					c.Name = x
				case *coreexport.TypeName:
					c.Name = x
				}
			}
			return c
		}
		out.Files[i] = rename(f).(*coreexport.File)
	}
	return &out
}

// unhider writes a renamed name as the source has it.
var unhider = strings.NewReplacer(otherDotPrefix, "", noCollPrefix, "")

// unhide is text with every renamed name written as the source has it.
func unhide(text string) string {
	return unhider.Replace(text)
}

// fileDotImports reports whether file f dot-imports a package other than std
// and the immutable collections, and whether it dot-imports the immutable
// collections.
func fileDotImports(f *coreexport.File) (other, collections bool) {
	for _, d := range f.Decls {
		if imp, ok := d.(*coreexport.Import); ok && imp.Alias == "." {
			switch imp.Path {
			case collectionsPath:
				collections = true
			case stdPath:
			default:
				other = true
			}
		}
	}
	return other, collections
}

// isStdFunc reports whether name x, called, is one of stdFuncs as it
// resolves in this program.
func (g *gen) isStdFunc(x string) bool {
	f, ok := stdFuncs[x]
	return ok && g.libName(x, f.seq)
}

// tabulateFn is the type ArrayTabulate's function argument wants.
func tabulateFn() *coreexport.TypeFunc {
	return &coreexport.TypeFunc{Params: []coreexport.Node{&coreexport.TypeName{Name: "int"}},
		Result: &coreexport.TypeName{Name: inferTypeArg}}
}

// stdFuncCall translates a call of stdFunc x.
func (g *gen) stdFuncCall(x string, e *coreexport.Call, sc *scope) term {
	for _, a := range e.Args {
		positional(a)
	}
	arity := stdFuncs[x].arity
	if len(e.Args) != arity {
		unsup(e.Pos, "argument-count")
	}
	switch x {
	case "int", "int64":
		// Go's conversion of an int or int64 to int or int64 keeps the
		// value: both are 64 bits, as galaspec takes Go's int to be
		// (docs/FORMAL_SEMANTICS.MD). Any other conversion is reported.
		if g.typeOf(e.Args[0], sc) != "Int64" {
			unsup(e.Pos, "conversion")
		}
		return g.expr(e.Args[0], sc)
	case "ArrayTabulate":
		n := g.exprExpect(e.Args[0], sc, &coreexport.TypeName{Name: "int"})
		f := g.exprExpect(e.Args[1], sc, tabulateFn())
		return act(fmt.Sprintf("GArray.mk <$> seqTabulate %s %s", n.val(), f.val()))
	}
	args := make([]term, len(e.Args))
	vals := make([]string, len(e.Args))
	for i, a := range e.Args {
		args[i] = g.expr(a, sc)
		vals[i] = args[i].val()
	}
	var code string
	switch x {
	case "When":
		code = fmt.Sprintf("(optWhen %s %s)", vals[0], vals[1])
	case "Unless":
		code = fmt.Sprintf("(optWhen (!%s) %s)", vals[0], vals[1])
	case "ArrayFill":
		code = fmt.Sprintf("(GArray.mk (seqFill %s %s))", vals[0], vals[1])
	default: // Tuple, Tuple3, Tuple4
		code = fmt.Sprintf("(T%d.mk %s)", arity, strings.Join(vals, " "))
	}
	return wrap(code, args...)
}

// stdFuncType is exprType of a call of stdFunc x.
func (g *gen) stdFuncType(x string, e *coreexport.Call, sc *scope, seen map[string]bool) string {
	arg := func(i int) string {
		if i >= len(e.Args) {
			return ""
		}
		return g.exprType(argNode(e.Args[i]), sc, seen)
	}
	switch x {
	case "int", "int64":
		return "Int64"
	case "When", "Unless":
		if t := arg(1); t != "" {
			return "(GOption " + t + ")"
		}
	case "ArrayFill":
		if t := arg(1); t != "" {
			return "(GArray " + t + ")"
		}
	case "ArrayTabulate":
		if len(e.Args) == 2 {
			if r := g.funcArgResult(e.Args[1], tabulateFn(), sc, seen); r != "" {
				return "(GArray " + r + ")"
			}
		}
	default: // Tuple, Tuple3, Tuple4
		parts := make([]string, len(e.Args))
		for i := range e.Args {
			if parts[i] = arg(i); parts[i] == "" {
				return ""
			}
		}
		return fmt.Sprintf("(T%d %s)", len(e.Args), strings.Join(parts, " "))
	}
	return ""
}
