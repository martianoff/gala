// Package gocoreexport reads Go source, the transpiler's output and the Go
// of the GALA packages it imports, into GoCore JSON, which the Lean model
// GoCore runs (docs/FORMAL_SEMANTICS.MD, "M4: GoCore and the Go AST
// exporter"). SCHEMA.md documents the format.
//
// It is a plain syntax dump of go/ast: every Go construct has a kind of its
// own, and which ones GoCore runs is decided on the Lean side alone. It
// resolves no names, infers no types and folds no constants; it reads
// literals as the Go type checker does (go/constant). It drops nothing but
// comments. A node JSON cannot carry exactly (a string that is not UTF-8)
// is written as an `unrepresentable` node in its place.
//
// It is a test-support library: no part of the gala CLI links it.
package gocoreexport

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"slices"
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
	// Partial: only some of the package's files are given (Package.Partial).
	Partial bool
}

// Export parses every package's files and exports them, packages in the
// order given, files sorted by name. The error is for Go that does not
// parse; a construct outside GoCore is never an error.
func Export(pkgs []PackageSource) (*Program, error) {
	prog := &Program{GoCore: Version, Packages: []*Package{}}
	for _, ps := range pkgs {
		files := slices.Clone(ps.Files)
		slices.SortFunc(files, func(a, b Source) int { return cmp.Compare(a.Name, b.Name) })
		pkg := &Package{Path: ps.Path, Partial: ps.Partial, Files: []*File{}}
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
	enc.SetIndent("", "  ")
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

// metaNode is a node type whose Meta the exporter fills.
type metaNode interface {
	Node
	setMeta(Meta)
}

// at gives n its kind and position, and returns it.
func at[N metaNode](x *exporter, n N, p token.Pos) N {
	n.setMeta(Meta{Kind: n.Kind(), Pos: x.pos(p)})
	return n
}

func (x *exporter) unrepresentable(what string, p token.Pos) Node {
	return &Unrepresentable{What: what, Pos: x.pos(p)}
}

func (x *exporter) file(name string, f *ast.File) *File {
	out := at(x, &File{Name: name, Imports: []*Import{}, Decls: []Node{}}, f.Package)
	for _, spec := range f.Imports {
		// The parser has checked that the path is a string literal.
		path, _ := strconv.Unquote(spec.Path.Value)
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
		out.Decls = append(out.Decls, x.decl(d))
	}
	return out
}

// decl exports a top-level or local declaration.
func (x *exporter) decl(d ast.Decl) Node {
	switch d := d.(type) {
	case *ast.FuncDecl:
		if d.Recv != nil && len(d.Recv.List) != 1 {
			// go/parser accepts `func () M()`; Go's type checker does not.
			return x.unrepresentable(UnrepresentableBadNode, d.Pos())
		}
		fd := at(x, &FuncDecl{
			Name:       d.Name.Name,
			TypeParams: x.fields(d.Type.TypeParams),
			Type:       x.funcType(d.Type),
		}, d.Pos())
		if d.Recv != nil {
			fd.Recv = x.fields(d.Recv)[0]
		}
		if d.Body != nil {
			fd.Body = x.block(d.Body)
		}
		return fd
	case *ast.GenDecl:
		switch d.Tok {
		case token.TYPE:
			td := at(x, &TypeDecl{Specs: []*TypeSpec{}}, d.Pos())
			for _, s := range d.Specs {
				ts := s.(*ast.TypeSpec)
				td.Specs = append(td.Specs, &TypeSpec{
					Name:       ts.Name.Name,
					TypeParams: x.fields(ts.TypeParams),
					Alias:      ts.Assign.IsValid(),
					Type:       x.expr(ts.Type),
				})
			}
			return td
		case token.VAR, token.CONST:
			vd := at(x, &ValueDecl{Const: d.Tok == token.CONST, Specs: []*ValueSpec{}}, d.Pos())
			for _, s := range d.Specs {
				vs := s.(*ast.ValueSpec)
				vd.Specs = append(vd.Specs, &ValueSpec{Names: names(vs.Names), Type: x.optExpr(vs.Type), Values: x.exprs(vs.Values)})
			}
			return vd
		}
	}
	return x.unrepresentable(UnrepresentableBadNode, d.Pos())
}

func names(ids []*ast.Ident) []string {
	out := []string{}
	for _, id := range ids {
		out = append(out, id.Name)
	}
	return out
}

// fields exports a field list. The parser allows `...T` only as the type of
// a function's last parameter; it exports as T, and funcType sets the flag.
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
		field := &Field{Names: names(f.Names), Type: x.expr(t)}
		if f.Tag != nil {
			field.Tag, _ = strconv.Unquote(f.Tag.Value)
		}
		out = append(out, field)
	}
	return out
}

func (x *exporter) funcType(t *ast.FuncType) *FuncType {
	ft := at(x, &FuncType{Params: x.fields(t.Params), Results: x.fields(t.Results)}, t.Pos())
	if t.Params != nil && len(t.Params.List) > 0 {
		_, ft.Variadic = t.Params.List[len(t.Params.List)-1].Type.(*ast.Ellipsis)
	}
	return ft
}

// validTags reports whether every tag in fl is valid UTF-8, which a JSON
// string can carry exactly.
func validTags(fl *ast.FieldList) bool {
	for _, f := range fl.List {
		if f.Tag != nil {
			if tag, err := strconv.Unquote(f.Tag.Value); err != nil || !utf8.ValidString(tag) {
				return false
			}
		}
	}
	return true
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
		return at(x, &Ident{Name: e.Name}, e.Pos())
	case *ast.BasicLit:
		return x.basicLit(e)
	case *ast.CompositeLit:
		return at(x, &CompositeLit{Type: x.optExpr(e.Type), Elts: x.exprs(e.Elts)}, e.Pos())
	case *ast.KeyValueExpr:
		return at(x, &KeyValue{Key: x.expr(e.Key), Value: x.expr(e.Value)}, e.Pos())
	case *ast.FuncLit:
		return at(x, &FuncLit{Type: x.funcType(e.Type), Body: x.block(e.Body)}, e.Pos())
	case *ast.ParenExpr:
		return at(x, &Paren{X: x.expr(e.X)}, e.Pos())
	case *ast.SelectorExpr:
		return at(x, &Selector{X: x.expr(e.X), Sel: e.Sel.Name}, e.Pos())
	case *ast.IndexExpr:
		return at(x, &Index{X: x.expr(e.X), Indices: []Node{x.expr(e.Index)}}, e.Pos())
	case *ast.IndexListExpr:
		return at(x, &Index{X: x.expr(e.X), Indices: x.exprs(e.Indices)}, e.Pos())
	case *ast.TypeAssertExpr:
		// A nil Type is `x.(type)` in a type switch.
		return at(x, &TypeAssert{X: x.expr(e.X), Type: x.optExpr(e.Type)}, e.Pos())
	case *ast.CallExpr:
		return at(x, &Call{Fun: x.expr(e.Fun), Args: x.exprs(e.Args), Ellipsis: e.Ellipsis.IsValid()}, e.Pos())
	case *ast.StarExpr:
		return at(x, &Star{X: x.expr(e.X)}, e.Pos())
	case *ast.UnaryExpr:
		return at(x, &Unary{Op: e.Op.String(), X: x.expr(e.X)}, e.Pos())
	case *ast.BinaryExpr:
		return at(x, &Binary{Op: e.Op.String(), X: x.expr(e.X), Y: x.expr(e.Y)}, e.Pos())
	case *ast.StructType:
		if !validTags(e.Fields) {
			return x.unrepresentable(UnrepresentableInvalidUTF8, e.Pos())
		}
		return at(x, &StructType{Fields: x.fields(e.Fields)}, e.Pos())
	case *ast.InterfaceType:
		return at(x, &InterfaceType{Elems: x.fields(e.Methods)}, e.Pos())
	case *ast.FuncType:
		return x.funcType(e)
	case *ast.SliceExpr:
		return at(x, &SliceExpr{X: x.expr(e.X), Low: x.optExpr(e.Low), High: x.optExpr(e.High), Max: x.optExpr(e.Max), Slice3: e.Slice3}, e.Pos())
	case *ast.ArrayType:
		return at(x, &ArrayType{Len: x.optExpr(e.Len), Elt: x.expr(e.Elt)}, e.Pos())
	case *ast.MapType:
		return at(x, &MapType{Key: x.expr(e.Key), Value: x.expr(e.Value)}, e.Pos())
	case *ast.ChanType:
		dir := "both"
		switch e.Dir {
		case ast.SEND:
			dir = "send"
		case ast.RECV:
			dir = "recv"
		}
		return at(x, &ChanType{Dir: dir, Value: x.expr(e.Value)}, e.Pos())
	case *ast.Ellipsis:
		// Only the length of `[...]T`: a variadic parameter's is read by
		// fields.
		return at(x, &Ellipsis{}, e.Pos())
	}
	return x.unrepresentable(UnrepresentableBadNode, e.Pos())
}

// basicLit reads a literal as the Go type checker does (Go spec, "Integer
// literals", "Floating-point literals", "Imaginary literals", "Rune
// literals", "String literals").
func (x *exporter) basicLit(e *ast.BasicLit) Node {
	v := constant.MakeFromLiteral(e.Value, e.Kind, 0)
	switch {
	case v.Kind() == constant.Unknown:
		return x.unrepresentable(UnrepresentableBadNode, e.Pos())
	case e.Kind == token.INT:
		return at(x, &IntLit{Value: v.ExactString()}, e.Pos())
	case e.Kind == token.FLOAT:
		return at(x, &FloatLit{Value: v.ExactString()}, e.Pos())
	case e.Kind == token.IMAG:
		return at(x, &ImagLit{Value: constant.Imag(v).ExactString()}, e.Pos())
	case e.Kind == token.CHAR:
		r, _ := constant.Int64Val(v)
		return at(x, &CharLit{Value: r}, e.Pos())
	}
	s := constant.StringVal(v)
	if !utf8.ValidString(s) {
		return x.unrepresentable(UnrepresentableInvalidUTF8, e.Pos())
	}
	return at(x, &StringLit{Value: s}, e.Pos())
}

func (x *exporter) block(b *ast.BlockStmt) Node {
	return at(x, &Block{Stmts: x.stmts(b.List)}, b.Pos())
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
		return at(x, &DeclStmt{Decl: x.decl(s.Decl)}, s.Pos())
	case *ast.ExprStmt:
		return at(x, &ExprStmt{X: x.expr(s.X)}, s.Pos())
	case *ast.IncDecStmt:
		return at(x, &IncDec{Op: s.Tok.String(), X: x.expr(s.X)}, s.Pos())
	case *ast.AssignStmt:
		return at(x, &Assign{Tok: s.Tok.String(), Lhs: x.exprs(s.Lhs), Rhs: x.exprs(s.Rhs)}, s.Pos())
	case *ast.ReturnStmt:
		return at(x, &Return{Results: x.exprs(s.Results)}, s.Pos())
	case *ast.BranchStmt:
		br := at(x, &Branch{Tok: s.Tok.String()}, s.Pos())
		if s.Label != nil {
			br.Label = s.Label.Name
		}
		return br
	case *ast.LabeledStmt:
		return at(x, &Labeled{Label: s.Label.Name, Stmt: x.stmt(s.Stmt)}, s.Pos())
	case *ast.IfStmt:
		return at(x, &If{Init: x.optStmt(s.Init), Cond: x.expr(s.Cond), Body: x.block(s.Body), Else: x.optStmt(s.Else)}, s.Pos())
	case *ast.SwitchStmt:
		return at(x, &Switch{Init: x.optStmt(s.Init), Tag: x.optExpr(s.Tag), Clauses: x.caseClauses(s.Body)}, s.Pos())
	case *ast.TypeSwitchStmt:
		return at(x, &TypeSwitch{Init: x.optStmt(s.Init), Assign: x.stmt(s.Assign), Clauses: x.caseClauses(s.Body)}, s.Pos())
	case *ast.SelectStmt:
		sel := at(x, &Select{Clauses: []*CommClause{}}, s.Pos())
		for _, c := range s.Body.List {
			cc := c.(*ast.CommClause)
			sel.Clauses = append(sel.Clauses, &CommClause{Comm: x.optStmt(cc.Comm), Body: x.stmts(cc.Body)})
		}
		return sel
	case *ast.ForStmt:
		return at(x, &For{Init: x.optStmt(s.Init), Cond: x.optExpr(s.Cond), Post: x.optStmt(s.Post), Body: x.block(s.Body)}, s.Pos())
	case *ast.RangeStmt:
		tok := ""
		if s.Tok != token.ILLEGAL {
			tok = s.Tok.String()
		}
		return at(x, &Range{Key: x.optExpr(s.Key), Value: x.optExpr(s.Value), Tok: tok, X: x.expr(s.X), Body: x.block(s.Body)}, s.Pos())
	case *ast.EmptyStmt:
		return at(x, &Empty{}, s.Pos())
	case *ast.SendStmt:
		return at(x, &Send{Chan: x.expr(s.Chan), Value: x.expr(s.Value)}, s.Pos())
	case *ast.GoStmt:
		return at(x, &Go{Call: x.expr(s.Call)}, s.Pos())
	case *ast.DeferStmt:
		return at(x, &Defer{Call: x.expr(s.Call)}, s.Pos())
	}
	return x.unrepresentable(UnrepresentableBadNode, s.Pos())
}

func (x *exporter) caseClauses(b *ast.BlockStmt) []*CaseClause {
	out := []*CaseClause{}
	for _, c := range b.List {
		cc := c.(*ast.CaseClause)
		out = append(out, &CaseClause{List: x.exprs(cc.List), Body: x.stmts(cc.Body)})
	}
	return out
}

// ConstructCounts is how often each construct occurs in p: the kind of
// every node, and the name of every unrepresentable one. It reads p as
// JSON, the form a consumer sees.
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
			for _, field := range []string{"kind", "unrepresentable"} {
				if name, ok := v[field].(string); ok {
					counts[name]++
				}
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
