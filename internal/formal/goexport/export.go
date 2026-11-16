// Package goexport reads Go source, the transpiler's output and the Go of the
// GALA packages it imports, into GoCore JSON: the syntax of the Go subset
// that the Lean model GoCore runs (docs/FORMAL_SEMANTICS.MD, "M4: GoCore and
// the Go AST exporter"). SCHEMA.md documents the format.
//
// It is a plain syntax dump of go/ast. It resolves no names, infers no types,
// folds no constants and drops nothing: a construct outside GoCore is written
// as an `unsupported` node in its place, naming it, so the consumer stops
// there with that name if a run reaches it. Literals are read lexically: an
// integer's value in decimal, a string's with its escapes interpreted.
//
// It is a test-support library: no part of the gala CLI links it.
package goexport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math/big"
	"sort"
	"strconv"
	"unicode/utf8"
)

// Source is one Go file of a package.
type Source struct {
	// Name is the file's name, used in error messages.
	Name string
	Text string
}

// PackageSource is one package of the program: its import path and files.
type PackageSource struct {
	Path  string
	Files []Source
}

// Export parses every package's files and exports them, packages in the
// order given, files sorted by name. The error is for Go that does not
// parse; a construct outside GoCore is never an error.
func Export(pkgs []PackageSource) (*Program, error) {
	prog := &Program{GoCore: Version, Packages: []*Package{}}
	for _, ps := range pkgs {
		files := append([]Source(nil), ps.Files...)
		sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
		pkg := &Package{Path: ps.Path, Files: []*File{}}
		for _, src := range files {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, src.Name, src.Text, parser.SkipObjectResolution)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", src.Name, err)
			}
			if pkg.Name == "" {
				pkg.Name = f.Name.Name
			} else if pkg.Name != f.Name.Name {
				return nil, fmt.Errorf("%s: package %s, but %s's other files are package %s", src.Name, f.Name.Name, ps.Path, pkg.Name)
			}
			x := &exporter{fset: fset}
			pkg.Files = append(pkg.Files, x.file(src.Name, f))
		}
		prog.Packages = append(prog.Packages, pkg)
	}
	return prog, nil
}

// Marshal writes p as indented JSON, without HTML escaping.
func Marshal(p *Program) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(p); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// exporter exports one file.
type exporter struct {
	fset *token.FileSet
}

func (x *exporter) pos(p token.Pos) Pos {
	pp := x.fset.Position(p)
	return Pos{Line: pp.Line, Col: pp.Column}
}

func (x *exporter) meta(kind string, p token.Pos) Meta { return Meta{Kind: kind, Pos: x.pos(p)} }

func (x *exporter) unsupported(what string, p token.Pos) Node {
	return &Unsupported{What: what, Pos: x.pos(p)}
}

func (x *exporter) file(name string, f *ast.File) *File {
	out := &File{Meta: x.meta("file", f.Package), Name: name, Imports: []*Import{}, Decls: []Node{}}
	for _, spec := range f.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			path = spec.Path.Value
		}
		imp := &Import{Path: path}
		if spec.Name != nil {
			imp.Name = spec.Name.Name
		}
		out.Imports = append(out.Imports, imp)
	}
	for _, d := range f.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.IMPORT {
			continue
		}
		out.Decls = append(out.Decls, x.decls(d)...)
	}
	return out
}

// decls exports a top-level or local declaration: a type declaration with
// several specs becomes one TypeDecl each.
func (x *exporter) decls(d ast.Decl) []Node {
	switch d := d.(type) {
	case *ast.FuncDecl:
		return []Node{x.funcDecl(d)}
	case *ast.GenDecl:
		switch d.Tok {
		case token.TYPE:
			var out []Node
			for _, s := range d.Specs {
				ts := s.(*ast.TypeSpec)
				out = append(out, &TypeDecl{
					Meta:       x.meta("type-decl", ts.Pos()),
					Name:       ts.Name.Name,
					TypeParams: x.fields(ts.TypeParams),
					Alias:      ts.Assign.IsValid(),
					Type:       x.expr(ts.Type),
				})
			}
			return out
		case token.VAR, token.CONST:
			vd := &ValueDecl{Meta: x.meta("value-decl", d.Pos()), Const: d.Tok == token.CONST, Specs: []*ValueSpec{}}
			for _, s := range d.Specs {
				vs := s.(*ast.ValueSpec)
				spec := &ValueSpec{Names: names(vs.Names), Values: x.exprs(vs.Values)}
				if vs.Type != nil {
					spec.Type = x.expr(vs.Type)
				}
				vd.Specs = append(vd.Specs, spec)
			}
			return []Node{vd}
		}
	}
	return []Node{x.unsupported("bad-node", d.Pos())}
}

func (x *exporter) funcDecl(d *ast.FuncDecl) Node {
	fd := &FuncDecl{
		Meta:       x.meta("func-decl", d.Pos()),
		Name:       d.Name.Name,
		TypeParams: x.fields(d.Type.TypeParams),
		Params:     x.fields(d.Type.Params),
		Results:    x.fields(d.Type.Results),
		Variadic:   variadic(d.Type),
	}
	if d.Recv != nil && len(d.Recv.List) == 1 {
		fd.Recv = x.fields(d.Recv)[0]
	}
	if d.Body != nil {
		fd.Body = x.block(d.Body)
	}
	return fd
}

// variadic reports whether t's last parameter is `...T`; its Field's type
// is then T.
func variadic(t *ast.FuncType) bool {
	if t.Params == nil || len(t.Params.List) == 0 {
		return false
	}
	_, ok := t.Params.List[len(t.Params.List)-1].Type.(*ast.Ellipsis)
	return ok
}

func names(ids []*ast.Ident) []string {
	out := []string{}
	for _, id := range ids {
		out = append(out, id.Name)
	}
	return out
}

func (x *exporter) fields(fl *ast.FieldList) []*Field {
	out := []*Field{}
	if fl == nil {
		return out
	}
	for _, f := range fl.List {
		t := f.Type
		if e, ok := t.(*ast.Ellipsis); ok {
			t = e.Elt
		}
		out = append(out, &Field{Names: names(f.Names), Type: x.expr(t)})
	}
	return out
}

func (x *exporter) exprs(es []ast.Expr) []Node {
	out := []Node{}
	for _, e := range es {
		out = append(out, x.expr(e))
	}
	return out
}

// optExpr is expr, and nil for a nil expression.
func (x *exporter) optExpr(e ast.Expr) Node {
	if e == nil {
		return nil
	}
	return x.expr(e)
}

func (x *exporter) expr(e ast.Expr) Node {
	switch e := e.(type) {
	case *ast.Ident:
		return &Ident{Meta: x.meta("ident", e.Pos()), Name: e.Name}
	case *ast.BasicLit:
		return x.basicLit(e)
	case *ast.CompositeLit:
		return &CompositeLit{Meta: x.meta("composite-lit", e.Pos()), Type: x.optExpr(e.Type), Elts: x.exprs(e.Elts)}
	case *ast.KeyValueExpr:
		return &KeyValue{Meta: x.meta("key-value", e.Pos()), Key: x.expr(e.Key), Value: x.expr(e.Value)}
	case *ast.FuncLit:
		return &FuncLit{Meta: x.meta("func-lit", e.Pos()), Type: x.funcType(e.Type), Body: x.block(e.Body)}
	case *ast.ParenExpr:
		return &Paren{Meta: x.meta("paren", e.Pos()), X: x.expr(e.X)}
	case *ast.SelectorExpr:
		return &Selector{Meta: x.meta("selector", e.Pos()), X: x.expr(e.X), Sel: e.Sel.Name}
	case *ast.IndexExpr:
		return &Index{Meta: x.meta("index", e.Pos()), X: x.expr(e.X), Indices: []Node{x.expr(e.Index)}}
	case *ast.IndexListExpr:
		return &Index{Meta: x.meta("index", e.Pos()), X: x.expr(e.X), Indices: x.exprs(e.Indices)}
	case *ast.TypeAssertExpr:
		if e.Type == nil {
			return x.unsupported("type-switch", e.Pos())
		}
		return &TypeAssert{Meta: x.meta("type-assert", e.Pos()), X: x.expr(e.X), Type: x.expr(e.Type)}
	case *ast.CallExpr:
		return &Call{Meta: x.meta("call", e.Pos()), Fun: x.expr(e.Fun), Args: x.exprs(e.Args), Ellipsis: e.Ellipsis.IsValid()}
	case *ast.StarExpr:
		return &Star{Meta: x.meta("star", e.Pos()), X: x.expr(e.X)}
	case *ast.UnaryExpr:
		switch e.Op {
		case token.SUB, token.ADD, token.NOT, token.XOR, token.AND:
			return &Unary{Meta: x.meta("unary", e.Pos()), Op: e.Op.String(), X: x.expr(e.X)}
		}
		return x.unsupported("receive", e.Pos())
	case *ast.BinaryExpr:
		return &Binary{Meta: x.meta("binary", e.Pos()), Op: e.Op.String(), X: x.expr(e.X), Y: x.expr(e.Y)}
	case *ast.StructType:
		return &StructType{Meta: x.meta("struct-type", e.Pos()), Fields: x.fields(e.Fields)}
	case *ast.InterfaceType:
		return &InterfaceType{Meta: x.meta("interface-type", e.Pos()), Elems: x.fields(e.Methods)}
	case *ast.FuncType:
		return x.funcType(e)
	case *ast.SliceExpr:
		return x.unsupported("slice-expr", e.Pos())
	case *ast.ArrayType:
		return x.unsupported("array-type", e.Pos())
	case *ast.MapType:
		return x.unsupported("map-type", e.Pos())
	case *ast.ChanType:
		return x.unsupported("chan-type", e.Pos())
	case *ast.Ellipsis:
		return x.unsupported("ellipsis", e.Pos())
	}
	return x.unsupported("bad-node", e.Pos())
}

func (x *exporter) funcType(t *ast.FuncType) *FuncType {
	return &FuncType{
		Meta:     x.meta("func-type", t.Pos()),
		Params:   x.fields(t.Params),
		Results:  x.fields(t.Results),
		Variadic: variadic(t),
	}
}

// basicLit reads a literal lexically (Go spec, "Integer literals", "Rune
// literals", "String literals").
func (x *exporter) basicLit(e *ast.BasicLit) Node {
	switch e.Kind {
	case token.INT:
		n, ok := new(big.Int).SetString(e.Value, 0)
		if !ok {
			return x.unsupported("bad-node", e.Pos())
		}
		return &IntLit{Meta: x.meta("int-lit", e.Pos()), Value: n.String()}
	case token.STRING:
		s, err := strconv.Unquote(e.Value)
		if err != nil {
			return x.unsupported("bad-node", e.Pos())
		}
		return &StringLit{Meta: x.meta("string-lit", e.Pos()), Value: s}
	case token.CHAR:
		s, err := strconv.Unquote(e.Value)
		r, _ := utf8.DecodeRuneInString(s)
		if err != nil {
			return x.unsupported("bad-node", e.Pos())
		}
		return &CharLit{Meta: x.meta("char-lit", e.Pos()), Value: r}
	case token.FLOAT:
		return x.unsupported("float-literal", e.Pos())
	case token.IMAG:
		return x.unsupported("imag-literal", e.Pos())
	}
	return x.unsupported("bad-node", e.Pos())
}

func (x *exporter) block(b *ast.BlockStmt) Node {
	return &Block{Meta: x.meta("block", b.Pos()), Stmts: x.stmts(b.List)}
}

func (x *exporter) stmts(ss []ast.Stmt) []Node {
	out := []Node{}
	for _, s := range ss {
		out = append(out, x.stmt(s))
	}
	return out
}

// optStmt is stmt, and nil for a nil statement.
func (x *exporter) optStmt(s ast.Stmt) Node {
	if s == nil {
		return nil
	}
	return x.stmt(s)
}

func (x *exporter) stmt(s ast.Stmt) Node {
	switch s := s.(type) {
	case *ast.BlockStmt:
		return x.block(s)
	case *ast.DeclStmt:
		return &DeclStmt{Meta: x.meta("decl-stmt", s.Pos()), Decls: x.decls(s.Decl)}
	case *ast.ExprStmt:
		return &ExprStmt{Meta: x.meta("expr-stmt", s.Pos()), X: x.expr(s.X)}
	case *ast.IncDecStmt:
		return &IncDec{Meta: x.meta("inc-dec", s.Pos()), Op: s.Tok.String(), X: x.expr(s.X)}
	case *ast.AssignStmt:
		return &Assign{Meta: x.meta("assign", s.Pos()), Tok: s.Tok.String(), Lhs: x.exprs(s.Lhs), Rhs: x.exprs(s.Rhs)}
	case *ast.ReturnStmt:
		return &Return{Meta: x.meta("return", s.Pos()), Results: x.exprs(s.Results)}
	case *ast.BranchStmt:
		if s.Label != nil {
			return x.unsupported("labeled-branch", s.Pos())
		}
		switch s.Tok {
		case token.BREAK, token.CONTINUE:
			return &Branch{Meta: x.meta("branch", s.Pos()), Tok: s.Tok.String()}
		case token.GOTO:
			return x.unsupported("goto", s.Pos())
		}
		return x.unsupported("fallthrough", s.Pos())
	case *ast.IfStmt:
		return &If{Meta: x.meta("if", s.Pos()), Init: x.optStmt(s.Init), Cond: x.expr(s.Cond), Body: x.block(s.Body), Else: x.optStmt(s.Else)}
	case *ast.SwitchStmt:
		sw := &Switch{Meta: x.meta("switch", s.Pos()), Init: x.optStmt(s.Init), Tag: x.optExpr(s.Tag), Clauses: []*CaseClause{}}
		for _, c := range s.Body.List {
			cc := c.(*ast.CaseClause)
			sw.Clauses = append(sw.Clauses, &CaseClause{List: x.exprs(cc.List), Default: cc.List == nil, Body: x.stmts(cc.Body)})
		}
		return sw
	case *ast.ForStmt:
		return &For{Meta: x.meta("for", s.Pos()), Init: x.optStmt(s.Init), Cond: x.optExpr(s.Cond), Post: x.optStmt(s.Post), Body: x.block(s.Body)}
	case *ast.RangeStmt:
		tok := ""
		if s.Tok != token.ILLEGAL {
			tok = s.Tok.String()
		}
		return &Range{Meta: x.meta("range", s.Pos()), Key: x.optExpr(s.Key), Value: x.optExpr(s.Value), Tok: tok, X: x.expr(s.X), Body: x.block(s.Body)}
	case *ast.EmptyStmt:
		return &Empty{Meta: x.meta("empty", s.Pos())}
	case *ast.LabeledStmt:
		return x.unsupported("labeled-stmt", s.Pos())
	case *ast.SendStmt:
		return x.unsupported("send", s.Pos())
	case *ast.GoStmt:
		return x.unsupported("go", s.Pos())
	case *ast.DeferStmt:
		return x.unsupported("defer", s.Pos())
	case *ast.SelectStmt:
		return x.unsupported("select", s.Pos())
	case *ast.TypeSwitchStmt:
		return x.unsupported("type-switch", s.Pos())
	}
	return x.unsupported("bad-node", s.Pos())
}

// ConstructCounts is how often each construct occurs in p: the kind of
// every supported node and the name of every unsupported one (its
// name as written).
func ConstructCounts(p *Program) (map[string]int, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	counts := map[string]int{}
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if k, ok := v["kind"].(string); ok {
				counts[k]++
			}
			if u, ok := v["unsupported"].(string); ok {
				name := u
				counts[name]++
			}
			for _, c := range v {
				walk(c)
			}
		case []any:
			for _, c := range v {
				walk(c)
			}
		}
	}
	walk(doc)
	return counts, nil
}
