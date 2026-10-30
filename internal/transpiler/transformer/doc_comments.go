package transformer

import (
	"go/ast"
	"go/build/constraint"
	"go/token"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/parser"
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
// Package, struct-field and interface-method docs are placed by the generator
// (see generator/docs.go).

// docCommentGroup renders harvested doc prose as a `//` comment group, or nil
// for an empty doc.
//
// Two kinds of line cannot be copied through verbatim. A `+build` line is a
// build constraint wherever it sits in a Go file — gofmt lifts it into a
// `//go:build` header, which can silently exclude the whole file — so it is
// dropped. (The parser already drops it from the doc table; this keeps the
// guarantee at the point of emission.) And control characters are removed:
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

// sanitizeDocLine removes C0 and DEL control characters other than tab, and the
// byte-order mark Go rejects outside a file's start, from one doc line.
// strings.Map also re-encodes any invalid UTF-8 as U+FFFD. (The parser already
// rejects NUL, a stray BOM and invalid UTF-8 in any GALA source.)
func sanitizeDocLine(line string) string {
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
	return t.docAt(parser.DocKeyOf(start))
}

// docAt is the doc comment of the declaration whose first token is at key.
func (t *galaASTTransformer) docAt(key parser.DocKey) *ast.CommentGroup {
	return docCommentGroup(t.richAST.Docs[key])
}

// declDocs is what attachDocs reads of a GALA top-level declaration, from
// either tree (readDeclDocs, readDeclDocsNative): which kind it is, where it
// starts, its name, and its documented members.
type declDocs struct {
	kind declDocKind
	doc  parser.DocKey
	name string
	// members are a type's struct fields or interface methods, a struct
	// shorthand's named parameters, or a sealed type's cases.
	members []memberDoc
}

// memberDoc is a member of a declaration: its name and where it starts.
type memberDoc struct {
	name string
	doc  parser.DocKey
}

// declDocKind is the kind of declaration declDocs describes, as
// attachDocs places its docs.
type declDocKind int

const (
	docsNone      declDocKind = iota
	docsOne                   // a function, val or var: one Go declaration
	docsEmbed                 // an embed
	docsType                  // a type declaration
	docsShorthand             // a struct shorthand declaration
	docsOpaque                // an opaque type
	docsSealed                // a sealed type
)

// attachTopDeclDocs attaches the doc comments of d, one GALA top-level
// declaration, to the Go declarations it lowered to. decls is the
// transformer's output for that declaration, in emission order.
func (t *galaASTTransformer) attachTopDeclDocs(d topDecl, decls []ast.Decl) {
	if t.richAST == nil || len(t.richAST.Docs) == 0 || len(decls) == 0 {
		return
	}
	if d.node.Ok() {
		t.attachDocs(readDeclDocsNative(d.node), decls)
	} else {
		t.attachDocs(readDeclDocs(d.ctx), decls)
	}
}

// readDeclDocs reads what attachDocs needs of ctx on ANTLR's tree.
func readDeclDocs(ctx grammar.ITopLevelDeclarationContext) declDocs {
	dd := declDocs{doc: parser.DocKeyOf(ctx.GetStart())}
	switch {
	case ctx.FunctionDeclaration() != nil, ctx.ValDeclaration() != nil, ctx.VarDeclaration() != nil:
		dd.kind = docsOne

	case ctx.EmbedDeclaration() != nil:
		dd.kind = docsEmbed
		dd.name = ctx.EmbedDeclaration().(*grammar.EmbedDeclarationContext).Identifier().GetText()

	case ctx.TypeDeclaration() != nil:
		typeCtx := ctx.TypeDeclaration().(*grammar.TypeDeclarationContext)
		dd.kind, dd.name = docsType, typeCtx.Identifier().GetText()
		if st, ok := typeCtx.StructType().(*grammar.StructTypeContext); ok && st != nil {
			for _, f := range st.AllStructField() {
				fctx := f.(*grammar.StructFieldContext)
				dd.members = append(dd.members, memberDoc{fctx.Identifier().GetText(), parser.DocKeyOf(fctx.GetStart())})
			}
		}
		if it, ok := typeCtx.InterfaceType().(*grammar.InterfaceTypeContext); ok && it != nil {
			for _, m := range it.AllMethodSpec() {
				mctx := m.(*grammar.MethodSpecContext)
				dd.members = append(dd.members, memberDoc{mctx.Identifier().GetText(), parser.DocKeyOf(mctx.GetStart())})
			}
		}

	case ctx.StructShorthandDeclaration() != nil:
		sctx := ctx.StructShorthandDeclaration().(*grammar.StructShorthandDeclarationContext)
		dd.kind, dd.name = docsShorthand, sctx.Identifier().GetText()
		params := sctx.Parameters().(*grammar.ParametersContext)
		if params.ParameterList() == nil {
			break
		}
		for _, p := range params.ParameterList().(*grammar.ParameterListContext).AllParameter() {
			pctx := p.(*grammar.ParameterContext)
			if pctx.Identifier() != nil {
				dd.members = append(dd.members, memberDoc{pctx.Identifier().GetText(), parser.DocKeyOf(pctx.GetStart())})
			}
		}

	case ctx.OpaqueTypeDeclaration() != nil:
		dd.kind = docsOpaque
		dd.name = ctx.OpaqueTypeDeclaration().(*grammar.OpaqueTypeDeclarationContext).Identifier().GetText()

	case ctx.SealedTypeDeclaration() != nil:
		sctx := ctx.SealedTypeDeclaration().(*grammar.SealedTypeDeclarationContext)
		dd.kind, dd.name = docsSealed, sctx.Identifier().GetText()
		for _, c := range sctx.AllSealedCase() {
			cctx := c.(*grammar.SealedCaseContext)
			dd.members = append(dd.members, memberDoc{cctx.Identifier().GetText(), parser.DocKeyOf(cctx.GetStart())})
		}
	}
	return dd
}

// attachDocs attaches the doc comments of the declaration dd describes to
// decls, the Go declarations it lowered to, in emission order.
func (t *galaASTTransformer) attachDocs(dd declDocs, decls []ast.Decl) {
	doc := t.docAt(dd.doc)

	switch dd.kind {
	case docsOne:
		// Each lowers to exactly one Go declaration.
		*declDoc(decls[0]) = doc

	case docsEmbed:
		// An EmbeddedFS embed adds a hidden `_embed_<name>` var ahead of the
		// public binding; the doc belongs to the binding.
		if d := findValueDecl(decls, dd.name); d != nil {
			d.Doc = doc
		}

	case docsType:
		decl, spec := findTypeSpec(decls, dd.name)
		if decl == nil {
			return
		}
		decl.Doc = doc
		for _, m := range dd.members {
			t.attachFieldDoc(spec, m.name, m.doc)
		}

	case docsShorthand:
		decl, spec := findTypeSpec(decls, dd.name)
		if decl == nil {
			return
		}
		// The written doc first, then the constructor guidance the lowering
		// put there (see structGoConstructor).
		decl.Doc = joinDocs(doc, decl.Doc)
		for _, m := range dd.members {
			t.attachFieldDoc(spec, m.name, m.doc)
		}

	case docsOpaque:
		if decl, _ := findTypeSpec(decls, dd.name); decl != nil {
			decl.Doc = doc
		}

	case docsSealed:
		if decl, _ := findTypeSpec(decls, dd.name); decl != nil {
			// The written doc first, then the constructor guidance the
			// lowering put there (see sealedGoConstructors).
			decl.Doc = joinDocs(doc, decl.Doc)
		}
		// Each case is a companion type of its own name; its doc goes there.
		for _, m := range dd.members {
			if decl, _ := findTypeSpec(decls, m.name); decl != nil {
				decl.Doc = t.docAt(m.doc)
			}
		}
	}
}

// attachFieldDoc documents the struct field or interface method called name in
// spec's type with the doc comment at key.
func (t *galaASTTransformer) attachFieldDoc(spec *ast.TypeSpec, name string, key parser.DocKey) {
	doc := t.docAt(key)
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

// declDoc returns the Doc field of a top-level Go declaration.
func declDoc(d ast.Decl) **ast.CommentGroup {
	switch d := d.(type) {
	case *ast.FuncDecl:
		return &d.Doc
	case *ast.GenDecl:
		return &d.Doc
	}
	return new(*ast.CommentGroup) // *ast.BadDecl: nowhere to put a doc
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

// joinDocs is first followed by second; either may be nil. No blank line
// parts them: the //line directive is laid out above a doc only while the
// doc fits in the GALA lines above the declaration (see
// transpiler.insertLineDirectives), and a blank line would take one.
func joinDocs(first, second *ast.CommentGroup) *ast.CommentGroup {
	switch {
	case first == nil:
		return second
	case second == nil:
		return first
	}
	return &ast.CommentGroup{List: append(append([]*ast.Comment{}, first.List...), second.List...)}
}
