package gala2lean

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"martianoff/gala/internal/formal/coreexport"
)

// Packages. A program may import other packages of its module
// (Options.Deps). Their declarations are translated into the same Lean module,
// each package's under a namespace of its own: atn.State, atn.State.mk,
// atn.State.m. A GALA identifier never holds a dot, so these names cannot
// clash with the program's. Before the translation, every reference is
// rewritten (qualify) to the name of the declaration it means:
//
//   - inside a package, an unqualified name that a local, parameter or
//     pattern binder does not hide, and that the package declares, is the
//     package's declaration;
//   - `pkg.X`, a type named with a qualifier or a select on an import alias
//     no local hides, is package pkg's declaration X.
//
// Only the declarations of other packages that the program reaches (prune)
// are translated: Lean needs nothing else. A function counts as translated
// only when what it uses of other packages translates too (attributeReach).
//
// Go runs every imported package's initialization, whether or not the
// importer uses it, so every package-level variable of every package the
// program imports, directly or not, is translated, reached or not, and a
// construct outside the subset in one is reported for the program. Go
// initializes packages in import-path order, each once the packages it
// imports are (depOrder); their files come before the program's in that
// order, so that initOrder, which follows declaration order, initializes
// each package's variables, in Go's order within the package, before the
// next package's. GALA drops an import a file does not use from the Go it
// writes (importsOf), and Go does not initialize a package no file imports.
// A dot import of a package of the module is not translated
// (dot-import-module).

// Package is a package of the program's module that the program imports,
// directly or through another such package.
type Package struct {
	// Path is its import path.
	Path string
	// Program is its Core JSON: every file of the package.
	Program *coreexport.Program
}

// ModuleDeps is the packages of module modPath that p imports, directly or
// through each other; load gives a package's Core JSON by its import path.
func ModuleDeps(p *coreexport.Program, modPath string, load func(path string) (*coreexport.Program, error)) ([]Package, error) {
	var deps []Package
	seen := map[string]bool{}
	queue := []*coreexport.Program{p}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, f := range cur.Files {
			for _, d := range f.Decls {
				imp, ok := d.(*coreexport.Import)
				if !ok || seen[imp.Path] || !strings.HasPrefix(imp.Path, modPath+"/") {
					continue
				}
				seen[imp.Path] = true
				dp, err := load(imp.Path)
				if err != nil {
					return nil, err
				}
				deps = append(deps, Package{Path: imp.Path, Program: dp})
				queue = append(queue, dp)
			}
		}
	}
	return deps, nil
}

// ModulePath is the module path a gala.mod declares, "" for none.
func ModulePath(galaMod string) string {
	for _, line := range strings.Split(galaMod, "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[0] == "module" {
			return f[1]
		}
	}
	return ""
}

// pkgPrefix is the namespace part of declaration name x, with its dot: "atn."
// for "atn.State", "" for a declaration of the program's own package.
func pkgPrefix(x string) string {
	if i := strings.LastIndex(x, "."); i >= 0 {
		return x[:i+1]
	}
	return ""
}

// bareName is declaration name x without its package's namespace, as GALA
// prints it.
func bareName(x string) string { return strings.TrimPrefix(x, pkgPrefix(x)) }

// pkgInfo is one package as the translation reads it.
type pkgInfo struct {
	path   string
	prefix string // its namespace and a dot, "" for the program's package
	prog   *coreexport.Program
	// top is every name the package declares at the top level, to its
	// qualified name; types its type names.
	top, types map[string]string
}

// mergePackages returns the program with the declarations of the module
// packages it reaches added, every reference qualified (see above), the
// constructs outside the subset that the packages put the program out of,
// and the packages' namespaces.
func mergePackages(p *coreexport.Program, deps []Package) (*coreexport.Program, []Unsupported, map[string]bool) {
	if len(deps) == 0 {
		return p, nil, nil
	}
	byPath := map[string]*Package{}
	for i := range deps {
		byPath[deps[i].Path] = &deps[i]
	}
	var unsup []Unsupported
	order, err := depOrder(p, byPath)
	if err != nil {
		return p, []Unsupported{{Construct: "import-cycle"}}, nil
	}
	// Namespaces: a package's name, numbered when two packages share one.
	pkgs := map[string]*pkgInfo{}
	used := map[string]bool{}
	for _, path := range order {
		dep := byPath[path]
		name := packageName(dep.Program, path)
		ns := name
		for k := 2; used[ns]; k++ {
			ns = fmt.Sprintf("%s_%d", name, k)
		}
		used[ns] = true
		pkgs[path] = newPkgInfo(path, ns+".", dep.Program)
	}
	main := newPkgInfo("", "", p)
	q := &qualifier{pkgs: pkgs, module: byPath}
	var files, depFiles []*coreexport.File
	for _, f := range p.Files {
		files = append(files, q.file(main, f, &unsup))
	}
	for _, path := range order {
		for _, f := range pkgs[path].prog.Files {
			depFiles = append(depFiles, q.file(pkgs[path], f, &unsup))
		}
	}
	// The packages' files come first, in dependency order, so that their
	// package-level variables are declared, and so initialized, before
	// the importer's (initOrder): Go initializes a package before every
	// package that imports it.
	out := &coreexport.Program{Core: p.Core, Files: append(prune(files, depFiles), files...)}
	return out, unsup, used
}

// depOrder is the import paths of the module packages p reaches, in the order
// Go initializes them (Go spec, "Package initialization", since Go 1.21):
// sorted by import path, then repeatedly the first package of that list
// whose imports are all initialized. Go forbids import cycles.
func depOrder(p *coreexport.Program, byPath map[string]*Package) ([]string, error) {
	imports := map[string][]string{} // each reached package's imports
	var reach func(paths []string)
	reach = func(paths []string) {
		for _, path := range paths {
			if _, ok := imports[path]; !ok {
				imports[path] = importsOf(byPath[path].Program, byPath)
				reach(imports[path])
			}
		}
	}
	reach(importsOf(p, byPath))
	pending := slices.Sorted(maps.Keys(imports))
	done := map[string]bool{}
	var order []string
	for len(pending) > 0 {
		i := slices.IndexFunc(pending, func(path string) bool {
			for _, imp := range imports[path] {
				if !done[imp] {
					return false
				}
			}
			return true
		})
		if i < 0 {
			return nil, fmt.Errorf("import cycle among %v", pending)
		}
		done[pending[i]] = true
		order = append(order, pending[i])
		pending = slices.Delete(pending, i, i+1)
	}
	return order, nil
}

// importsOf is the module packages program p imports and uses, sorted. GALA
// drops an import its file does not use from the Go it generates (the
// transpiler's PruneUnused: an import whose name no selector `name.X` names),
// so Go neither imports nor initializes that package for it.
func importsOf(p *coreexport.Program, byPath map[string]*Package) []string {
	set := map[string]bool{}
	for _, f := range p.Files {
		used := selectorNames(f)
		for _, d := range f.Decls {
			imp, ok := d.(*coreexport.Import)
			if !ok || byPath[imp.Path] == nil {
				continue
			}
			name := imp.Alias
			if name == "" {
				name = packageName(byPath[imp.Path].Program, imp.Path)
			}
			if used[name] || name == "_" {
				set[imp.Path] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(set))
}

// selectorNames is every name file f qualifies something with: the target
// of a select `name.X`, and the package of a type `name.T`. Like the
// transpiler's check, it does not ask whether a local hides the name.
func selectorNames(f *coreexport.File) map[string]bool {
	used := map[string]bool{}
	walk(f, func(n coreexport.Node) {
		switch n := n.(type) {
		case *coreexport.Select:
			if t, ok := n.Target.(*coreexport.Name); ok {
				used[t.Name] = true
			}
		case *coreexport.TypeName:
			if n.Package != "" {
				used[n.Package] = true
			}
		}
	})
	return used
}

// packageName is the name a package's files declare, or the last element
// of its path.
func packageName(p *coreexport.Program, path string) string {
	for _, f := range p.Files {
		if f.Package != "" {
			return f.Package
		}
	}
	return path[strings.LastIndex(path, "/")+1:]
}

func newPkgInfo(path, prefix string, p *coreexport.Program) *pkgInfo {
	info := &pkgInfo{path: path, prefix: prefix, prog: p, top: map[string]string{}, types: map[string]string{}}
	for _, f := range p.Files {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *coreexport.Func:
				if d.Receiver == nil {
					info.top[d.Name] = prefix + d.Name
				}
			case *coreexport.Struct:
				info.top[d.Name] = prefix + d.Name
				info.types[d.Name] = prefix + d.Name
			case *coreexport.Sealed:
				info.types[d.Name] = prefix + d.Name
				for _, vn := range d.Variants {
					if v, ok := vn.(*coreexport.Variant); ok {
						info.top[v.Name] = prefix + v.Name
					}
				}
			case *coreexport.Val:
				for _, n := range d.Names {
					info.top[n] = prefix + n
				}
			case *coreexport.Var:
				for _, n := range d.Names {
					info.top[n] = prefix + n
				}
			}
		}
	}
	return info
}

// qualifier rewrites the references of one file (qualify).
type qualifier struct {
	pkgs map[string]*pkgInfo
	// module is every package of the module the program was given.
	module map[string]*Package
	// The file being rewritten: its package, and its import aliases of
	// module packages.
	pkg     *pkgInfo
	aliases map[string]*pkgInfo
	// tparams are the type parameters in scope.
	tparams map[string]bool
}

// file is f with every reference qualified. A file of another package
// keeps no imports: the merged program has none of its own to report.
func (q *qualifier) file(pkg *pkgInfo, f *coreexport.File, unsup *[]Unsupported) *coreexport.File {
	q.pkg, q.aliases, q.tparams = pkg, map[string]*pkgInfo{}, map[string]bool{}
	out := *f
	out.Decls = nil
	for _, d := range f.Decls {
		if imp, ok := d.(*coreexport.Import); ok && q.module[imp.Path] != nil {
			// An import of a package of the module, used or not (an unused
			// one is not in pkgs: Go does not import it).
			if imp.Alias == "." {
				*unsup = append(*unsup, Unsupported{Construct: "dot-import-module", Pos: imp.Pos})
				continue
			}
			if dep := q.pkgs[imp.Path]; dep != nil {
				switch imp.Alias {
				case "_":
				case "":
					q.aliases[packageName(dep.prog, dep.path)] = dep
				default:
					q.aliases[imp.Alias] = dep
				}
			}
			continue
		}
		// An import of a package outside the module stays: a declaration
		// that uses it is reported (import).
		out.Decls = append(out.Decls, q.decl(d))
	}
	return &out
}

func (q *qualifier) decl(d coreexport.Node) coreexport.Node {
	switch d := d.(type) {
	case *coreexport.Func:
		c := *d
		saved := q.withTypeParams(d.TypeParams)
		defer func() { q.tparams = saved }()
		if d.Receiver == nil {
			c.Name = q.pkg.prefix + d.Name
		}
		bound := map[string]bool{}
		if r, ok := d.Receiver.(*coreexport.Receiver); ok {
			rc := *r
			rc.Type = q.typ(r.Type)
			c.Receiver = &rc
			bound[r.Name] = true
		}
		c.Params = q.params(d.Params, bound)
		c.Result = q.typ(d.Result)
		c.Body = q.expr(d.Body, bound)
		return &c
	case *coreexport.Struct:
		c := *d
		saved := q.withTypeParams(d.TypeParams)
		defer func() { q.tparams = saved }()
		c.Name = q.pkg.prefix + d.Name
		c.Fields = q.fields(d.Fields)
		return &c
	case *coreexport.Sealed:
		c := *d
		saved := q.withTypeParams(d.TypeParams)
		defer func() { q.tparams = saved }()
		c.Name = q.pkg.prefix + d.Name
		c.Variants = make([]coreexport.Node, len(d.Variants))
		for i, vn := range d.Variants {
			v, ok := vn.(*coreexport.Variant)
			if !ok {
				c.Variants[i] = vn
				continue
			}
			vc := *v // a case keeps its own name: Lean puts it in the type's namespace
			vc.Fields = q.fields(v.Fields)
			c.Variants[i] = &vc
		}
		return &c
	case *coreexport.Val:
		c := *d
		c.Names = q.qualifyNames(d.Names)
		c.Type = q.typ(d.Type)
		c.Values = q.exprs(d.Values, map[string]bool{})
		return &c
	case *coreexport.Var:
		c := *d
		c.Names = q.qualifyNames(d.Names)
		c.Type = q.typ(d.Type)
		c.Values = q.exprs(d.Values, map[string]bool{})
		return &c
	}
	return d
}

func (q *qualifier) qualifyNames(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = q.pkg.prefix + n
	}
	return out
}

// withTypeParams puts type parameters ps in scope and returns the scope
// before.
func (q *qualifier) withTypeParams(ps []coreexport.Node) map[string]bool {
	saved := q.tparams
	q.tparams = maps.Clone(saved)
	for _, n := range typeParamNames(ps) {
		q.tparams[n] = true
	}
	return saved
}

func (q *qualifier) fields(fs []coreexport.Node) []coreexport.Node {
	out := make([]coreexport.Node, len(fs))
	for i, fn := range fs {
		f, ok := fn.(*coreexport.Field)
		if !ok {
			out[i] = fn
			continue
		}
		c := *f
		c.Type = q.typ(f.Type)
		c.Default = q.expr(f.Default, map[string]bool{})
		out[i] = &c
	}
	return out
}

// params rewrites parameters, binding each in bound; a default sees the
// parameters before it.
func (q *qualifier) params(ps []coreexport.Node, bound map[string]bool) []coreexport.Node {
	out := make([]coreexport.Node, len(ps))
	for i, pn := range ps {
		p, ok := pn.(*coreexport.Param)
		if !ok {
			out[i] = pn
			continue
		}
		c := *p
		c.Type = q.typ(p.Type)
		c.Default = q.expr(p.Default, bound)
		bound[p.Name] = true
		out[i] = &c
	}
	return out
}

// typ is type t with its names qualified.
func (q *qualifier) typ(t coreexport.Node) coreexport.Node {
	switch t := t.(type) {
	case *coreexport.TypeName:
		c := *t
		c.Args = make([]coreexport.Node, len(t.Args))
		for i, a := range t.Args {
			c.Args[i] = q.typ(a)
		}
		switch {
		case t.Package != "":
			if dep := q.aliases[t.Package]; dep != nil && dep.types[t.Name] != "" {
				c.Package, c.Name = "", dep.types[t.Name]
			}
		case q.tparams[t.Name]:
		case q.pkg.types[t.Name] != "":
			c.Name = q.pkg.types[t.Name]
		}
		return &c
	case *coreexport.TypeFunc:
		c := *t
		c.Params = make([]coreexport.Node, len(t.Params))
		for i, p := range t.Params {
			c.Params[i] = q.typ(p)
		}
		c.Result = q.typ(t.Result)
		return &c
	}
	return t
}

func (q *qualifier) exprs(ns []coreexport.Node, bound map[string]bool) []coreexport.Node {
	out := make([]coreexport.Node, len(ns))
	for i, n := range ns {
		out[i] = q.expr(n, bound)
	}
	return out
}

// value is the qualified name x means in an expression, "" when it is not
// a declaration of a package: a local, or a name of no package.
func (q *qualifier) value(x string, bound map[string]bool) string {
	if bound[x] {
		return ""
	}
	return q.pkg.top[x]
}

// member is the declaration `alias.x` names, "" when alias is not an
// import of a module package in scope.
func (q *qualifier) member(target coreexport.Node, x string, bound map[string]bool) string {
	n, ok := target.(*coreexport.Name)
	if !ok || bound[n.Name] {
		return ""
	}
	if dep := q.aliases[n.Name]; dep != nil {
		if d := dep.top[x]; d != "" {
			return d
		}
		return dep.types[x]
	}
	return ""
}

// expr is n with its references qualified; bound is the locals in scope.
func (q *qualifier) expr(n coreexport.Node, bound map[string]bool) coreexport.Node {
	switch n := n.(type) {
	case nil:
		return nil
	case *coreexport.Name:
		if d := q.value(n.Name, bound); d != "" {
			c := *n
			c.Name = d
			return &c
		}
		return n
	case *coreexport.Select:
		if d := q.member(n.Target, n.Name, bound); d != "" {
			return &coreexport.Name{Meta: coreexport.Meta{Kind: "name", Pos: n.Pos}, Name: d}
		}
		c := *n
		c.Target = q.expr(n.Target, bound)
		return &c
	case *coreexport.TypeName, *coreexport.TypeFunc:
		return q.typ(n)
	case *coreexport.Block:
		c := *n
		inner := maps.Clone(bound)
		c.Stmts = make([]coreexport.Node, len(n.Stmts))
		for i, st := range n.Stmts {
			c.Stmts[i] = q.expr(st, inner)
			switch st := st.(type) {
			case *coreexport.Val:
				bindAll(inner, st.Names)
			case *coreexport.Var:
				bindAll(inner, st.Names)
			case *coreexport.Func:
				inner[st.Name] = true
			}
		}
		return &c
	case *coreexport.Val:
		c := *n
		c.Type = q.typ(n.Type)
		c.Values = q.exprs(n.Values, bound)
		return &c
	case *coreexport.Var:
		c := *n
		c.Type = q.typ(n.Type)
		c.Values = q.exprs(n.Values, bound)
		return &c
	case *coreexport.Lambda:
		c := *n
		inner := maps.Clone(bound)
		c.Params = q.params(n.Params, inner)
		c.Result = q.typ(n.Result)
		c.Body = q.expr(n.Body, inner)
		return &c
	case *coreexport.Func:
		// A local function is outside the subset; its name hides a
		// package's (bound by the enclosing block).
		return n
	case *coreexport.Case:
		c := *n
		inner := maps.Clone(bound)
		c.Pattern = q.pattern(n.Pattern, bound, inner)
		c.Guard = q.expr(n.Guard, inner)
		c.Body = q.expr(n.Body, inner)
		return &c
	}
	return q.children(n, bound)
}

// pattern is pattern p qualified: a case or struct of a package, or a
// package-level value used as a stable identifier, is qualified; any other
// name is a binder, added to inner.
func (q *qualifier) pattern(p coreexport.Node, bound, inner map[string]bool) coreexport.Node {
	switch p := p.(type) {
	case *coreexport.PatName:
		if d := q.value(p.Name, bound); d != "" {
			c := *p
			c.Name = d
			return &c
		}
		inner[p.Name] = true
		return p
	case *coreexport.PatTuple:
		c := *p
		c.Elems = make([]coreexport.Node, len(p.Elems))
		for i, e := range p.Elems {
			c.Elems[i] = q.pattern(e, bound, inner)
		}
		return &c
	case *coreexport.PatExtract:
		c := *p
		switch callee := p.Callee.(type) {
		case *coreexport.Name:
			if d := q.value(callee.Name, bound); d != "" {
				cc := *callee
				cc.Name = d
				c.Callee = &cc
			}
		case *coreexport.Select:
			if d := q.member(callee.Target, callee.Name, bound); d != "" {
				c.Callee = &coreexport.Name{Meta: coreexport.Meta{Kind: "name", Pos: callee.Pos}, Name: d}
			}
		}
		c.Args = make([]coreexport.Node, len(p.Args))
		for i, a := range p.Args {
			c.Args[i] = q.pattern(a, bound, inner)
		}
		return &c
	case *coreexport.PatLit:
		c := *p
		c.Value = q.expr(p.Value, bound)
		return &c
	}
	return p
}

// children is n with each child expression qualified, in a copy.
func (q *qualifier) children(n coreexport.Node, bound map[string]bool) coreexport.Node {
	return mapChildren(n, func(c coreexport.Node) coreexport.Node { return q.expr(c, bound) })
}

// reacher finds the declarations code reaches: the functions, values and
// cases it names, the types it names, the methods whose name it selects
// (which type's method a select calls is not known here, so every method of
// that name), the methods that decide how a type it reaches prints or
// compares, and what those reach. A type reached only as a type (`x
// lib.Pt`) reaches its field types; called as a constructor (`lib.Pt(1)`)
// it reaches its field defaults too, which the call runs.
type reacher struct {
	byName map[string][]coreexport.Node
	seen   map[reachItem]bool
	queue  []reachItem
}

type reachItem struct {
	d    coreexport.Node
	full bool // reached as a value or constructor, not only as a type
}

func newReacher(files []*coreexport.File) *reacher {
	r := &reacher{byName: map[string][]coreexport.Node{}, seen: map[reachItem]bool{}}
	for _, f := range files {
		for _, d := range f.Decls {
			for _, k := range append(declNames(d), globalNames(d)...) {
				r.byName[k] = append(r.byName[k], d)
			}
		}
	}
	return r
}

// add queues declaration d, reached as a value (full) or as a type.
func (r *reacher) add(d coreexport.Node, full bool) {
	it := reachItem{d, full}
	if !r.seen[it] {
		r.seen[it] = true
		r.queue = append(r.queue, it)
	}
}

// mark queues what n names.
func (r *reacher) mark(n coreexport.Node) {
	walk(n, func(c coreexport.Node) {
		name, full := "", true
		switch c := c.(type) {
		case *coreexport.Name:
			name = c.Name
		case *coreexport.PatName:
			name = c.Name
		case *coreexport.TypeName:
			name, full = c.Name, false
		case *coreexport.Select:
			name = "." + c.Name
		default:
			return
		}
		for _, d := range r.byName[name] {
			r.add(d, full)
		}
	})
}

// run reaches everything the queue leads to, and returns every declaration
// reached.
func (r *reacher) run() map[coreexport.Node]bool {
	reached := map[coreexport.Node]bool{}
	for len(r.queue) > 0 {
		it := r.queue[0]
		r.queue = r.queue[1:]
		reached[it.d] = true
		var fields []coreexport.Node
		switch d := it.d.(type) {
		case *coreexport.Struct:
			fields = d.Fields
		case *coreexport.Sealed:
			for _, vn := range d.Variants {
				if v, ok := vn.(*coreexport.Variant); ok {
					fields = append(fields, v.Fields...)
				}
			}
		default:
			r.mark(it.d)
			continue
		}
		if it.full {
			r.mark(it.d)
		} else {
			// Only a type: its fields' types, not their defaults.
			for _, fn := range fields {
				if f, ok := fn.(*coreexport.Field); ok {
					r.mark(f.Type)
				}
			}
		}
		// The methods that decide how the type prints or compares.
		for _, k := range declNames(it.d) {
			for _, m := range r.byName[k] {
				if _, isMethod := m.(*coreexport.Func); isMethod {
					r.add(m, true)
				}
			}
		}
	}
	return reached
}

// prune is the declarations of deps that the program's declarations reach
// (reacher), in their order, and every package-level variable and init
// function of every package, reached or not, which Go runs, with what they
// reach.
func prune(program, deps []*coreexport.File) []*coreexport.File {
	r := newReacher(deps)
	for _, f := range program {
		for _, d := range f.Decls {
			r.mark(d)
		}
	}
	for _, f := range deps {
		for _, d := range f.Decls {
			fn, isFunc := d.(*coreexport.Func)
			if len(globalNames(d)) > 0 || isFunc && fn.Receiver == nil && bareName(fn.Name) == "init" {
				r.add(d, true)
			}
		}
	}
	keep := r.run()
	var out []*coreexport.File
	for _, f := range deps {
		c := *f
		c.Decls = nil
		for _, d := range f.Decls {
			// An import outside the module is kept: what it reports
			// depends on the declarations that use it.
			if _, isImport := d.(*coreexport.Import); isImport || keep[d] {
				c.Decls = append(c.Decls, d)
			}
		}
		if len(c.Decls) > 0 {
			out = append(out, &c)
		}
	}
	return out
}

// declNames are the qualified names declaration d answers to: its own, and
// its cases'. A method answers to a select of its name, `.m`, and a method
// that decides how its type's values print or compare (markCustom) also to
// its type, which uses it without naming it.
func declNames(d coreexport.Node) []string {
	switch d := d.(type) {
	case *coreexport.Func:
		if d.Receiver != nil {
			names := []string{"." + d.Name}
			if t := receiverTypeName(d); t != "" && customMethods[d.Name] != "" {
				names = append(names, t)
			}
			return names
		}
		return []string{d.Name}
	case *coreexport.Struct:
		return []string{d.Name}
	case *coreexport.Sealed:
		names := []string{d.Name}
		for _, vn := range d.Variants {
			if v, ok := vn.(*coreexport.Variant); ok {
				names = append(names, pkgPrefix(d.Name)+v.Name)
			}
		}
		return names
	}
	return nil
}

// use records that the declaration being translated uses declaration key.
func (g *gen) use(key string) {
	if g.cur == "" {
		return
	}
	if g.uses[g.cur] == nil {
		g.uses[g.cur] = map[string]bool{}
	}
	g.uses[g.cur][key] = true
}

// attributeReach reports each function of the program's own package that
// uses, directly or through what it uses, a declaration of another package
// the translation reported, and is not reported itself, with that
// declaration's construct: a function translates only when what it needs
// of other packages does. What a declaration uses is what its translation
// looked up (use): the functions it calls, the types it names, the methods
// it calls, the variables it reads. (A function that needs a declaration of
// its own package outside the subset is counted as before: each declaration
// of the package is reported for itself.)
func (g *gen) attributeReach(p *coreexport.Program) {
	failed := map[string]string{}
	reported := map[string]bool{}
	for _, it := range g.unsup {
		reported[it.Decl] = true
		if _, seen := failed[it.Decl]; g.isDepKey(it.Decl) && !seen {
			failed[it.Decl] = it.Construct
		}
	}
	if len(failed) == 0 {
		return
	}
	for _, f := range p.Files {
		for _, d := range f.Decls {
			fn, ok := d.(*coreexport.Func)
			if !ok || g.isDepKey(funcKey(fn)) || reported[funcKey(fn)] {
				continue
			}
			if c := g.usesFailed(funcKey(fn), failed); c != "" {
				g.unsup = append(g.unsup, Unsupported{Decl: funcKey(fn), Construct: c, Pos: fn.Pos})
			}
		}
	}
}

// usesFailed is the construct of the first failed declaration that key
// uses, directly or not, in a fixed order; "" for none.
func (g *gen) usesFailed(key string, failed map[string]string) string {
	seen := map[string]bool{key: true}
	queue := []string{key}
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		for _, u := range slices.Sorted(maps.Keys(g.uses[k])) {
			if seen[u] {
				continue
			}
			seen[u] = true
			if c, ok := failed[u]; ok {
				return c
			}
			queue = append(queue, u)
		}
	}
	return ""
}

// isDepKey reports whether report key k (a function, method, type or
// variable) names a declaration of another package: `ns.X`, or a method
// `ns.T.m`, ns a package's namespace.
func (g *gen) isDepKey(k string) bool {
	ns, rest, ok := strings.Cut(k, ".")
	return ok && g.depNS[ns] && rest != ""
}

// globalNames are the names a package-level val or var declares.
func globalNames(d coreexport.Node) []string {
	switch d := d.(type) {
	case *coreexport.Val:
		return d.Names
	case *coreexport.Var:
		return d.Names
	}
	return nil
}
