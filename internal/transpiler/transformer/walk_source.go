package transformer

import (
	"go/ast"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// The compiled file's top-level walk reads the file through walkSource:
// galop's native tree when the parse came with one (RichAST.Native), ANTLR's
// tree otherwise. A file is walked on one tree or the other, never on a mix;
// below the declarations, the native walk crosses to ANTLR's tree where the
// port has not reached (see declarations_native.go).

// walkSource is the compiled file as the top-level walk reads it: ANTLR's
// tree sf, and the native tree root when the walk reads that instead
// (root.Ok()). sf stays for the checks the native walk does not cover yet.
type walkSource struct {
	sf   *grammar.SourceFileContext
	root native.SourceFileNode
}

// walkSource picks the tree the walk of sf reads: richAST's native tree when
// it has one that converts and is sf's (its root's context is sf), and sf
// otherwise. It sets t.native to the handle the native walk crosses to ANTLR's
// tree through, or nil.
func (t *galaASTTransformer) walkSource(richAST *transpiler.RichAST, sf *grammar.SourceFileContext) walkSource {
	t.native = nil
	h := richAST.Native
	if h == nil || t.antlrWalkOnly {
		return walkSource{sf: sf}
	}
	tree, err := h.Tree()
	if err != nil {
		return walkSource{sf: sf}
	}
	root := tree.Root()
	if h.Ctx(root) != antlr.ParserRuleContext(sf) {
		return walkSource{sf: sf}
	}
	t.native = h
	return walkSource{sf: sf, root: native.SourceFileNode{GalaNodeRef: root}}
}

// packageClause is the file's package name and the doc key of its package
// clause.
func (s walkSource) packageClause() (string, parser.DocKey) {
	if s.root.Ok() {
		pc := s.root.PackageClause()
		return pc.Identifier().GetText(), parser.NativeDocKeyOf(pc.GalaNodeRef)
	}
	pc := s.sf.PackageClause()
	var key parser.DocKey
	if start := pc.GetStart(); start != nil {
		key = parser.DocKeyOf(start)
	}
	return pc.(*grammar.PackageClauseContext).Identifier().GetText(), key
}

// importDecl is an import declaration: its specs and where it is written.
type importDecl struct {
	specs []importSpecDecl
	at    srcRange
}

// imports reads the file's import declarations, in source order.
func (s walkSource) imports() []importDecl {
	var out []importDecl
	if s.root.Ok() {
		for n := range parser.NativeKids(s.root.GalaNodeRef, native.GalaKIND_importDeclaration) {
			out = append(out, importDecl{specs: readImportDeclNative(n), at: nativeRange(n)})
		}
		return out
	}
	for _, ctx := range s.sf.AllImportDeclaration() {
		out = append(out, importDecl{specs: readImportDecl(ctx.(*grammar.ImportDeclarationContext)), at: ctxRange(ctx)})
	}
	return out
}

// aliasTargets calls yield with the name and the target type, as written, of
// each type alias the file declares, in source order.
func (s walkSource) aliasTargets(yield func(name, target string)) {
	if s.root.Ok() {
		for td := range parser.NativeKids(s.root.GalaNodeRef, native.GalaKIND_topLevelDeclaration) {
			d, ok := parser.OnlyNativeKid(td, native.GalaKIND_typeDeclaration)
			if !ok {
				continue
			}
			typeDecl := native.TypeDeclarationNode{GalaNodeRef: d}
			if id, alias := typeDecl.Identifier(), typeDecl.TypeAlias(); id.Ok() && alias.Ok() {
				yield(id.GetText(), parser.NativeAliasText(alias))
			}
		}
		return
	}
	for _, topDeclCtx := range s.sf.AllTopLevelDeclaration() {
		typeDecl, ok := topDeclCtx.TypeDeclaration().(*grammar.TypeDeclarationContext)
		if !ok || typeDecl == nil || typeDecl.Identifier() == nil || typeDecl.TypeAlias() == nil {
			continue
		}
		yield(typeDecl.Identifier().GetText(), parser.AliasText(typeDecl.TypeAlias()))
	}
}

// topDecl is a top-level declaration of the file: its context on ANTLR's
// walk, its topLevelDeclaration node on the native walk.
type topDecl struct {
	ctx  grammar.ITopLevelDeclarationContext
	node native.GalaNodeRef
}

// topDecls is the file's top-level declarations, in source order.
func (s walkSource) topDecls() []topDecl {
	if s.root.Ok() {
		var out []topDecl
		for td := range parser.NativeKids(s.root.GalaNodeRef, native.GalaKIND_topLevelDeclaration) {
			out = append(out, topDecl{node: td})
		}
		return out
	}
	all := s.sf.AllTopLevelDeclaration()
	out := make([]topDecl, len(all))
	for i, ctx := range all {
		out[i] = topDecl{ctx: ctx}
	}
	return out
}

// startLine is the line d starts on, and false when it has no first token.
func (d topDecl) startLine() (int, bool) {
	if d.node.Ok() {
		return int(d.node.Start().Line), true
	}
	if start := d.ctx.GetStart(); start != nil {
		return start.GetLine(), true
	}
	return 0, false
}

// lowerTopDecl lowers d on the tree it comes from.
func (t *galaASTTransformer) lowerTopDecl(d topDecl) ([]ast.Decl, error) {
	if d.node.Ok() {
		return t.transformTopLevelDeclarationNative(d.node)
	}
	return t.transformTopLevelDeclaration(d.ctx)
}

// attachTopDeclDocs is attachDocComments of d, which lowered to decls.
func (t *galaASTTransformer) attachTopDeclDocs(d topDecl, decls []ast.Decl) {
	if !d.node.Ok() {
		t.attachDocComments(d.ctx, decls)
		return
	}
	if t.richAST == nil || len(t.richAST.Docs) == 0 || len(decls) == 0 {
		return
	}
	t.attachDocs(readDeclDocsNative(d.node), decls)
}

// readDeclDocsNative is readDeclDocs of td, a topLevelDeclaration node.
func readDeclDocsNative(td native.GalaNodeRef) declDocs {
	dd := declDocs{doc: parser.NativeDocKeyOf(td)}
	if td.ChildCount() == 0 {
		return dd
	}
	decl := td.Child(0).Node()
	if !decl.Ok() {
		return dd
	}
	member := func(n native.GalaNodeRef, id native.IdentifierNode) {
		dd.members = append(dd.members, memberDoc{id.GetText(), parser.NativeDocKeyOf(n)})
	}
	switch decl.Kind() {
	case native.GalaKIND_functionDeclaration, native.GalaKIND_valDeclaration, native.GalaKIND_varDeclaration:
		dd.kind = docsOne

	case native.GalaKIND_embedDeclaration:
		dd.kind, dd.name = docsEmbed, native.EmbedDeclarationNode{GalaNodeRef: decl}.Identifier().GetText()

	case native.GalaKIND_typeDeclaration:
		typeDecl := native.TypeDeclarationNode{GalaNodeRef: decl}
		dd.kind, dd.name = docsType, typeDecl.Identifier().GetText()
		for f := range parser.NativeKids(typeDecl.StructType().GalaNodeRef, native.GalaKIND_structField) {
			member(f, native.StructFieldNode{GalaNodeRef: f}.Identifier())
		}
		for m := range parser.NativeKids(typeDecl.InterfaceType().GalaNodeRef, native.GalaKIND_methodSpec) {
			member(m, native.MethodSpecNode{GalaNodeRef: m}.Identifier())
		}

	case native.GalaKIND_structShorthandDeclaration:
		sd := native.StructShorthandDeclarationNode{GalaNodeRef: decl}
		dd.kind, dd.name = docsShorthand, sd.Identifier().GetText()
		for p := range parser.NativeKids(sd.Parameters().ParameterList().GalaNodeRef, native.GalaKIND_parameter) {
			if id := (native.ParameterNode{GalaNodeRef: p}).Identifier(); id.Ok() {
				member(p, id)
			}
		}

	case native.GalaKIND_opaqueTypeDeclaration:
		dd.kind, dd.name = docsOpaque, native.OpaqueTypeDeclarationNode{GalaNodeRef: decl}.Identifier().GetText()

	case native.GalaKIND_sealedTypeDeclaration:
		dd.kind, dd.name = docsSealed, native.SealedTypeDeclarationNode{GalaNodeRef: decl}.Identifier().GetText()
		for c := range parser.NativeKids(decl, native.GalaKIND_sealedCase) {
			member(c, native.SealedCaseNode{GalaNodeRef: c}.Identifier())
		}
	}
	return dd
}

// bindingNames is the names a val or var declaration d binds, for
// recordPackageValTypes, and false when d is no val or var or binds none
// (parse-error recovery).
func bindingNames(d topDecl) ([]string, bool) {
	var names []string
	if d.node.Ok() {
		decl, ok := parser.OnlyNativeKid(d.node, native.GalaKIND_valDeclaration)
		if !ok {
			if decl, ok = parser.OnlyNativeKid(d.node, native.GalaKIND_varDeclaration); !ok {
				return nil, false
			}
		}
		// val and var declarations share the shape read here.
		idList, _ := parser.OnlyNativeKid(decl, native.GalaKIND_identifierList)
		if tp, ok := parser.OnlyNativeKid(decl, native.GalaKIND_tuplePattern); ok {
			idList, _ = parser.OnlyNativeKid(tp, native.GalaKIND_identifierList)
		}
		if !idList.Ok() {
			return nil, false
		}
		for id := range parser.NativeKids(idList, native.GalaKIND_identifier) {
			names = append(names, id.GetText())
		}
		return names, true
	}
	// val and var declarations share the shape read here.
	var decl interface {
		IdentifierList() grammar.IIdentifierListContext
		TuplePattern() grammar.ITuplePatternContext
	}
	switch {
	case d.ctx.ValDeclaration() != nil:
		decl = d.ctx.ValDeclaration()
	case d.ctx.VarDeclaration() != nil:
		decl = d.ctx.VarDeclaration()
	default:
		return nil, false
	}
	idList := decl.IdentifierList()
	if tp := decl.TuplePattern(); tp != nil {
		idList = tp.IdentifierList()
	}
	if idList == nil {
		return nil, false // parse-error recovery
	}
	for _, id := range idList.(*grammar.IdentifierListContext).AllIdentifier() {
		names = append(names, id.GetText())
	}
	return names, true
}
