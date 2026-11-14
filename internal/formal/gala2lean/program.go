package gala2lean

import (
	"fmt"
	"strings"

	"martianoff/gala/internal/formal/coreexport"
)

// gen is one translation.
type gen struct {
	structs  map[string]*coreexport.Struct
	sealeds  map[string]*coreexport.Sealed
	variants map[string]*variantInfo
	funcs    map[string]*coreexport.Func
	// funcOrder and globalOrder are the functions and package-level
	// declarations in source order.
	funcOrder []*coreexport.Func
	globals   []*globalVar
	globalBy  map[string]*globalVar
	typeNames []string // struct and sealed names in source order
	// selected is every field name some select reads, so that a sealed
	// type's field accessors are written only when used.
	selected map[string]bool
	// raw is the types `fmt` can print raw (rawPrinted), once computed.
	raw map[string]bool
	// outsideDecls are the top-level declarations outside the subset
	// (imports, generic declarations, ...), in source order.
	outsideDecls []*coreexport.Unsupported
	// hasMethods is true when the program declares a method.
	hasMethods bool
	// library is Options.Library.
	library bool

	unsup []Unsupported
	out   strings.Builder
	line  int // lines written to out so far
	decls []Decl

	fresh int
	// fn is the function being translated.
	fn *fnCtx
}

type variantInfo struct {
	sealed *coreexport.Sealed
	v      *coreexport.Variant
	fields []*coreexport.Field
}

// globalVar is one name a package-level val or var declares.
type globalVar struct {
	name  string
	decl  coreexport.Node // the *Val or *Var
	value coreexport.Node // its initializer, nil for none
	typ   coreexport.Node // its declared type, nil for none
	pos   coreexport.Pos
	leanT string // its Lean type, once known
}

type fnCtx struct {
	result string // Lean result type
	level  int    // the do-nesting level of the function body
}

func newGen() *gen {
	return &gen{
		structs:  map[string]*coreexport.Struct{},
		sealeds:  map[string]*coreexport.Sealed{},
		variants: map[string]*variantInfo{},
		funcs:    map[string]*coreexport.Func{},
		globalBy: map[string]*globalVar{},
		selected: map[string]bool{},
	}
}

func (g *gen) freshName(p string) string {
	g.fresh++
	return fmt.Sprintf("_gala_%s%d", p, g.fresh)
}

// collect reads the program's declarations. Unsupported top-level
// declarations (imports, methods, ...) are recorded.
func (g *gen) collect(p *coreexport.Program) {
	for _, f := range p.Files {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *coreexport.Unsupported:
				// A declaration outside the subset means nothing until a
				// name refers to it, and every name the translation cannot
				// resolve is reported, as this declaration's construct
				// (unresolved). A method can also change how a value
				// prints or compares (a String or Equal method), so then
				// only values of a basic type print or compare (basic).
				g.outsideDecls = append(g.outsideDecls, d)
				g.hasMethods = g.hasMethods || d.What == "method"
			case *coreexport.Struct:
				g.structs[d.Name] = d
				g.typeNames = append(g.typeNames, d.Name)
			case *coreexport.Sealed:
				g.sealeds[d.Name] = d
				g.typeNames = append(g.typeNames, d.Name)
				for _, vn := range d.Variants {
					v, ok := vn.(*coreexport.Variant)
					if !ok {
						g.unsupNode(d.Name, vn)
						continue
					}
					g.variants[v.Name] = &variantInfo{sealed: d, v: v, fields: fieldsOf(v.Fields)}
				}
			case *coreexport.Func:
				g.funcs[d.Name] = d
				g.funcOrder = append(g.funcOrder, d)
			case *coreexport.Val:
				g.addGlobal(d, d.Names, d.Destructure, d.Type, d.Values, d.Pos)
			case *coreexport.Var:
				g.addGlobal(d, d.Names, d.Destructure, d.Type, d.Values, d.Pos)
			default:
				g.unsup = append(g.unsup, Unsupported{Construct: "top-level-" + d.Kind()})
			}
		}
	}
	if g.funcs["main"] == nil && !g.library && len(g.unsup) == 0 {
		g.unsup = append(g.unsup, Unsupported{Construct: "no-main"})
	}
	walkAll(p, func(n coreexport.Node) {
		if s, ok := n.(*coreexport.Select); ok {
			g.selected[s.Name] = true
		}
	})
}

// unresolved stops the translation at a name it cannot resolve. The name most
// likely refers to a declaration outside the subset (an import, say), so
// that declaration's construct is reported if there is one, else fallback.
func (g *gen) unresolved(pos coreexport.Pos, fallback string) {
	if len(g.outsideDecls) > 0 {
		unsup(pos, "%s", g.outsideDecls[0].What)
	}
	unsup(pos, "%s", fallback)
}

// basic stops the translation unless e, which is printed or compared, has
// a type no method can change: int, string or bool. It applies only to a
// program that declares methods, whose String or Equal would be called.
func (g *gen) basic(e coreexport.Node, sc *scope) {
	if !g.hasMethods {
		return
	}
	switch g.exprType(e, sc, map[string]bool{}) {
	case "Int64", "String", "Bool":
		return
	}
	unsup(posOf(e), "method")
}

func (g *gen) unsupNode(decl string, n coreexport.Node) {
	what := n.Kind()
	var pos coreexport.Pos
	if u, ok := n.(*coreexport.Unsupported); ok {
		what, pos = u.What, u.Pos
	}
	g.unsup = append(g.unsup, Unsupported{Decl: decl, Construct: what, Pos: pos})
}

func (g *gen) addGlobal(decl coreexport.Node, names []string, destructure bool, typ coreexport.Node, values []coreexport.Node, pos coreexport.Pos) {
	if destructure {
		g.unsup = append(g.unsup, Unsupported{Decl: strings.Join(names, ", "), Construct: "global-destructure", Pos: pos})
		return
	}
	if len(values) != 0 && len(values) != len(names) {
		g.unsup = append(g.unsup, Unsupported{Decl: strings.Join(names, ", "), Construct: "global-count-mismatch", Pos: pos})
		return
	}
	for i, n := range names {
		gv := &globalVar{name: n, decl: decl, typ: typ, pos: pos}
		if len(values) > 0 {
			gv.value = values[i]
		}
		g.globals = append(g.globals, gv)
		g.globalBy[n] = gv
	}
}

func fieldsOf(ns []coreexport.Node) []*coreexport.Field {
	out := make([]*coreexport.Field, 0, len(ns))
	for _, n := range ns {
		if f, ok := n.(*coreexport.Field); ok {
			out = append(out, f)
		} else {
			unsupAt(n)
		}
	}
	return out
}

// unsupAt stops the current translation at node n, naming what it is.
func unsupAt(n coreexport.Node) {
	if u, ok := n.(*coreexport.Unsupported); ok {
		unsup(u.Pos, "%s", u.What)
	}
	unsup(posOf(n), "%s", n.Kind())
}

// write appends text to the output, counting lines.
func (g *gen) write(s string) {
	g.out.WriteString(s)
	g.line += strings.Count(s, "\n")
}

// emitProgram writes the module.
func (g *gen) emitProgram(opts Options) {
	ok := true
	var typeDecls string
	ok = g.guard("", func() {
		g.typeOrder()
		typeDecls = g.typeDecls()
	}) && ok
	// The package-level variables' types and initialization order.
	var initOrder []*globalVar
	ok = g.guard("", func() {
		for _, gv := range g.globals {
			gv.leanT = g.globalType(gv)
		}
		initOrder = g.initOrder()
	}) && ok
	sccs := g.funcSCCs()
	// Each function's definition, translated before anything is written so
	// that every unsupported construct is found.
	type fnOut struct {
		name, code string
		recursive  bool
	}
	var groups [][]fnOut
	for _, scc := range sccs {
		rec := len(scc) > 1 || g.callsSelf(scc[0])
		var grp []fnOut
		for _, f := range scc {
			var code string
			if g.guard(f.Name, func() { code = g.funcDecl(f, rec) }) {
				grp = append(grp, fnOut{f.Name, code, rec})
			} else {
				ok = false
			}
		}
		groups = append(groups, grp)
	}
	var initCode string
	ok = g.guard("", func() { initCode = g.initDecl(initOrder) }) && ok
	if !ok {
		return
	}

	g.write("-- Generated by gala2lean (internal/formal/gala2lean) from Core JSON. Do not edit.\n")
	g.write("import " + PreludeModule + "\n\n")
	g.write("set_option linter.unusedVariables false\n\n")
	g.write("namespace " + opts.Namespace + "\n\nopen Gala.Gen\n\n")
	g.write(typeDecls)
	g.write(g.stateDecl())
	for _, grp := range groups {
		mutual := len(grp) > 1
		if mutual {
			g.write("mutual\n\n")
		}
		for _, f := range grp {
			first := g.line + 1
			g.write(f.code)
			g.decls = append(g.decls, Decl{Name: f.name, Recursive: f.recursive, First: first, End: g.line})
			g.write("\n\n")
		}
		if mutual {
			g.write("end\n\n")
		}
	}
	first := g.line + 1
	g.write(initCode)
	g.decls = append(g.decls, Decl{Name: "<init>", First: first, End: g.line})
	g.write("\n\nend " + opts.Namespace + "\n")
	if opts.Main {
		g.write("\ndef main : IO UInt32 := Gala.Gen.runMain " + opts.Namespace + "._gala_run {}\n")
	}
}

// stateDecl is the program state: the printed output and every package-level
// variable, and the monad over it.
func (g *gen) stateDecl() string {
	var b strings.Builder
	b.WriteString("/-- The program state: the printed output and the package-level variables. -/\n")
	b.WriteString("structure _gala_St where\n  _gala_out : String := \"\"\n")
	for _, gv := range g.globals {
		fmt.Fprintf(&b, "  %s : %s := default\n", ident(gv.name), gv.leanT)
	}
	b.WriteString("\ninstance : HasOut _gala_St := ⟨_gala_St._gala_out, fun s o => { s with _gala_out := o }⟩\n\n")
	b.WriteString("/-- The program's monad. -/\nabbrev _gala_M := Prog _gala_St\n\n")
	return b.String()
}

// initDecl initializes the package-level variables in order and runs main.
func (g *gen) initDecl(order []*globalVar) string {
	g.fn = &fnCtx{result: "Unit"}
	sc := newScope(nil, 0)
	var items []string
	for _, gv := range order {
		var v string
		if gv.value != nil {
			v = g.expr(gv.value, sc).val()
		} else {
			v = g.zero(gv.typ)
		}
		tmp := g.freshName("i")
		items = append(items, "let "+tmp+" := "+v,
			fmt.Sprintf("modify fun _gala_st => { _gala_st with %s := %s }", ident(gv.name), tmp))
	}
	if g.funcs["main"] != nil {
		items = append(items, "main")
	} else {
		items = append(items, "pure ()")
	}
	return "/-- Package initialization, then `main`. -/\ndef _gala_run : _gala_M Unit := do\n" + indentLines(items, 2)
}

// funcDecl is the definition of one top-level function.
func (g *gen) funcDecl(f *coreexport.Func, recursive bool) string {
	sc := newScope(nil, 0)
	var ps []string
	for _, pn := range f.Params {
		p, ok := pn.(*coreexport.Param)
		if !ok {
			unsupAt(pn)
		}
		if p.Default != nil {
			unsup(p.Pos, "param-default")
		}
		if p.Type == nil {
			unsup(p.Pos, "untyped-param")
		}
		ps = append(ps, fmt.Sprintf("(%s : %s)", ident(p.Name), g.leanType(p.Type)))
		sc.bind(p.Name, false, g.leanType(p.Type))
	}
	res := "Unit"
	if f.Result != nil {
		res = g.leanType(f.Result)
	}
	g.fn = &fnCtx{result: res, level: 0}
	var body string
	if b, ok := f.Body.(*coreexport.Block); ok {
		body = indentLines(g.fnBlock(b, sc), 2)
	} else {
		body = indentLines(g.valueItems(f.Body, sc), 2)
	}
	head := "def " + ident(f.Name)
	if len(ps) > 0 {
		head += " " + strings.Join(ps, " ")
	}
	s := fmt.Sprintf("%s : _gala_M %s := do\n%s", head, res, body)
	if recursive {
		s += "\npartial_fixpoint"
	}
	return s
}

// fnBlock is a function's block body as do-items.
func (g *gen) fnBlock(b *coreexport.Block, sc *scope) []string {
	inner := sc.child()
	items := g.stmts(b.Stmts, inner, g.fn.result != "Unit")
	if g.fn.result != "Unit" && (len(b.Stmts) == 0 || !isExpr(b.Stmts[len(b.Stmts)-1])) && !terminates(b.Stmts) {
		// Every path returned (GALA checks this); Lean still wants a value
		// at the end.
		items = append(items, "pure default")
	}
	return items
}
