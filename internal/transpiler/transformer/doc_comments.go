package transformer

import (
	"go/ast"
	"go/build/constraint"
	"go/token"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/parser/grammar"
)

// Doc comments in generated Go
// ----------------------------
// The parser pairs each `//` run or `/* */` block with the declaration it
// documents (parser.extractDocComments) and the pipeline hands that table to the
// transformer as RichAST.Docs. Here each doc is attached, as an
// *ast.CommentGroup, to the Go declaration that carries the GALA one, so
// `go doc`, gopls, pkg.go.dev and annotation readers such as `swag` see the
// same prose as `gala doc` and hover.
//
// Only declaration documentation travels: the package clause, top-level
// functions and methods, types (struct fields and interface methods included),
// sealed types and their cases, and top-level val/var/embed bindings. Comments
// inside function bodies and trailing comments are not part of the table, so
// they are not emitted.
//
// The doc is the prose the parser harvested — comment markers stripped, `//go:`
// and `//line` directive lines dropped — re-emitted as `//` lines. A block
// comment therefore comes out as a `//` run with the same text.
//
// Struct-field and interface-method docs cannot be printed by go/printer on a
// position-less AST; the generator places those (see
// generator.Generate). Package docs likewise go through File.Doc to the
// generator.

// docCommentGroup renders harvested doc prose as a `//` comment group, or nil
// for an empty doc.
//
// Two kinds of line cannot be copied through verbatim. A `+build` line is a
// build constraint wherever it sits in a Go file — gofmt lifts it into a
// `//go:build` header, which can silently exclude the whole file — so it is
// dropped. (The parser already treats it as a directive; this also covers a
// `+build` line inside a block comment.) And control characters are removed:
// they have no place in rendered documentation, and the characters Go rejects
// outright are removed with them, so a doc can never make the generated file
// unparseable.
func docCommentGroup(doc string) *ast.CommentGroup {
	if doc == "" {
		return nil
	}
	lines := strings.Split(doc, "\n")
	g := &ast.CommentGroup{List: make([]*ast.Comment, 0, len(lines))}
	for _, line := range lines {
		text := "//"
		if line = sanitizeDocLine(line); line != "" {
			text += " " + line
		}
		if constraint.IsPlusBuild(text) {
			continue
		}
		g.List = append(g.List, &ast.Comment{Text: text})
	}
	if len(g.List) == 0 {
		return nil
	}
	return g
}

// sanitizeDocLine removes C0 and DEL control characters other than tab from
// one doc line, along with what Go source may not hold at all: invalid UTF-8
// and the byte-order mark. (The parser already rejects those two, and NUL, in
// any GALA source; removing them here keeps that guarantee local.)
func sanitizeDocLine(line string) string {
	line = strings.ToValidUTF8(line, "")
	return strings.Map(func(r rune) rune {
		if r == 0xFEFF || (r < 0x20 && r != '\t') || r == 0x7F {
			return -1
		}
		return r
	}, line)
}

// docFor returns the doc comment group for the declaration whose first token is
// start, or nil when it is undocumented. The caller has checked t.richAST.
func (t *galaASTTransformer) docFor(start antlr.Token) *ast.CommentGroup {
	if start == nil {
		return nil
	}
	return docCommentGroup(t.richAST.Docs[start.GetStart()])
}

// attachDocComments attaches the doc comments of one GALA top-level declaration
// to the Go declarations it lowered to. decls is the transformer's output for
// that declaration, in emission order.
func (t *galaASTTransformer) attachDocComments(ctx grammar.ITopLevelDeclarationContext, decls []ast.Decl) {
	if t.richAST == nil || len(t.richAST.Docs) == 0 || len(decls) == 0 {
		return
	}
	doc := t.docFor(ctx.GetStart())

	switch {
	case ctx.FunctionDeclaration() != nil, ctx.ValDeclaration() != nil, ctx.VarDeclaration() != nil:
		// Each lowers to exactly one Go declaration.
		setDeclDoc(decls[0], doc)

	case ctx.EmbedDeclaration() != nil:
		// An EmbeddedFS embed adds a hidden `_embed_<name>` var ahead of the
		// public binding; the doc belongs to the binding.
		name := ctx.EmbedDeclaration().(*grammar.EmbedDeclarationContext).Identifier().GetText()
		if d := findValueDecl(decls, name); d != nil {
			d.Doc = doc
		}

	case ctx.TypeDeclaration() != nil:
		typeCtx := ctx.TypeDeclaration().(*grammar.TypeDeclarationContext)
		decl, spec := findTypeSpec(decls, typeCtx.Identifier().GetText())
		if decl == nil {
			return
		}
		decl.Doc = doc
		if st, ok := typeCtx.StructType().(*grammar.StructTypeContext); ok && st != nil {
			for _, f := range st.AllStructField() {
				fctx := f.(*grammar.StructFieldContext)
				t.attachFieldDoc(spec, fctx.Identifier().GetText(), fctx.GetStart())
			}
		}
		if it, ok := typeCtx.InterfaceType().(*grammar.InterfaceTypeContext); ok && it != nil {
			for _, m := range it.AllMethodSpec() {
				mctx := m.(*grammar.MethodSpecContext)
				t.attachFieldDoc(spec, mctx.Identifier().GetText(), mctx.GetStart())
			}
		}

	case ctx.StructShorthandDeclaration() != nil:
		sctx := ctx.StructShorthandDeclaration().(*grammar.StructShorthandDeclarationContext)
		decl, spec := findTypeSpec(decls, sctx.Identifier().GetText())
		if decl == nil {
			return
		}
		decl.Doc = doc
		params := sctx.Parameters().(*grammar.ParametersContext)
		if params.ParameterList() == nil {
			return
		}
		for _, p := range params.ParameterList().(*grammar.ParameterListContext).AllParameter() {
			pctx := p.(*grammar.ParameterContext)
			if pctx.Identifier() != nil {
				t.attachFieldDoc(spec, pctx.Identifier().GetText(), pctx.GetStart())
			}
		}

	case ctx.SealedTypeDeclaration() != nil:
		sctx := ctx.SealedTypeDeclaration().(*grammar.SealedTypeDeclarationContext)
		if decl, _ := findTypeSpec(decls, sctx.Identifier().GetText()); decl != nil {
			decl.Doc = doc
		}
		// Each case is a companion type of its own name; its doc goes there.
		for _, c := range sctx.AllSealedCase() {
			cctx := c.(*grammar.SealedCaseContext)
			if decl, _ := findTypeSpec(decls, cctx.Identifier().GetText()); decl != nil {
				decl.Doc = t.docFor(cctx.GetStart())
			}
		}
	}
}

// attachFieldDoc documents the struct field or interface method called name in
// spec's type.
func (t *galaASTTransformer) attachFieldDoc(spec *ast.TypeSpec, name string, start antlr.Token) {
	doc := t.docFor(start)
	if doc == nil {
		return
	}
	var fields *ast.FieldList
	switch typ := spec.Type.(type) {
	case *ast.StructType:
		fields = typ.Fields
	case *ast.InterfaceType:
		fields = typ.Methods
	}
	if fields == nil {
		return
	}
	for _, f := range fields.List {
		for _, n := range f.Names {
			if n.Name == name {
				f.Doc = doc
				return
			}
		}
	}
}

// setDeclDoc sets the doc comment of a top-level Go declaration.
func setDeclDoc(d ast.Decl, doc *ast.CommentGroup) {
	switch d := d.(type) {
	case *ast.FuncDecl:
		d.Doc = doc
	case *ast.GenDecl:
		d.Doc = doc
	}
}

// takeDeclDoc removes and returns the doc comment of a top-level Go declaration.
func takeDeclDoc(d ast.Decl) *ast.CommentGroup {
	var doc *ast.CommentGroup
	switch d := d.(type) {
	case *ast.FuncDecl:
		doc, d.Doc = d.Doc, nil
	case *ast.GenDecl:
		doc, d.Doc = d.Doc, nil
	}
	return doc
}

// findTypeSpec returns the type declaration among decls that declares name.
func findTypeSpec(decls []ast.Decl, name string) (*ast.GenDecl, *ast.TypeSpec) {
	for _, d := range decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, s := range gd.Specs {
			if ts, ok := s.(*ast.TypeSpec); ok && ts.Name.Name == name {
				return gd, ts
			}
		}
	}
	return nil, nil
}

// findValueDecl returns the var/const declaration among decls that declares name.
func findValueDecl(decls []ast.Decl, name string) *ast.GenDecl {
	for _, d := range decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || (gd.Tok != token.VAR && gd.Tok != token.CONST) {
			continue
		}
		for _, s := range gd.Specs {
			if vs, ok := s.(*ast.ValueSpec); ok {
				for _, n := range vs.Names {
					if n.Name == name {
						return gd
					}
				}
			}
		}
	}
	return nil
}
