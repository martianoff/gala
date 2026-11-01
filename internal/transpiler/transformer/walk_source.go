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
// galop's native tree when the parse came with one that the analysis has
// converted (RichAST.Native), ANTLR's tree otherwise. A file is walked on one
// tree or the other, never on a mix (see declarations_native.go).

// walkSource is the compiled file as the top-level walk reads it: ANTLR's
// tree sf, and, when the walk reads the native tree instead, its root and the
// handle h a lowering entered with an ANTLR context finds its node through
// (root.Ok()). sf stays for the checks the native walk does not cover yet.
type walkSource struct {
	sf   *grammar.SourceFileContext
	root native.SourceFileNode
	h    *parser.Native
}

// newWalkSource picks the tree the walk of sf reads: richAST's native tree
// when it has one that is converted already and is sf's (its root's context
// is sf), and sf otherwise. The walk never converts a tree itself: that costs
// more than walking it saves, so it reads one only where the analysis has
// paid for it.
func newWalkSource(richAST *transpiler.RichAST, sf *grammar.SourceFileContext) walkSource {
	h := richAST.Native
	// A lowering entered with an ANTLR context finds its node through the
	// bridge (nativeNodeOf). Native.Bridged checks every node has a context
	// of its rule, so where it does not hold the whole file is walked on
	// ANTLR's tree, as the analysis reads it, and no lookup can miss.
	if !h.Converted() || !h.Bridged() {
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
	return walkSource{sf: sf, root: native.SourceFileNode{GalaNodeRef: root}, h: h}
}

// packageClause is the file's package name and the doc key of its package
// clause.
func (s walkSource) packageClause() (string, parser.DocKey) {
	if s.root.Ok() {
		pc := s.root.PackageClause()
		return pc.Identifier().GetText(), parser.NativeDocKeyOf(pc.GalaNodeRef)
	}
	pc := s.sf.PackageClause()
	return pc.(*grammar.PackageClauseContext).Identifier().GetText(), docKeyOf(pc.GetStart())
}

// importDecl is an import declaration: its specs and where it is written.
type importDecl struct {
	specs []importSpecDecl
	at    srcNode
}

// imports reads the file's import declarations, in source order.
func (s walkSource) imports() []importDecl {
	var out []importDecl
	if s.root.Ok() {
		for n := range parser.NativeKids(s.root.GalaNodeRef, native.GalaKIND_importDeclaration) {
			out = append(out, importDecl{specs: readImportDeclNative(n), at: srcNode{node: n}})
		}
		return out
	}
	for _, ctx := range s.sf.AllImportDeclaration() {
		out = append(out, importDecl{specs: readImportDecl(ctx.(*grammar.ImportDeclarationContext)), at: srcNode{ctx: ctx}})
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

// readDeclDocsNative is readDeclDocs of td, a topLevelDeclaration node.
func readDeclDocsNative(td native.GalaNodeRef) declDocs {
	dd := declDocs{doc: parser.NativeDocKeyOf(td)}
	decl, ok := firstKidNode(td)
	if !ok {
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
	if d.node.Ok() {
		return bindingNamesNative(d.node)
	}
	var decl tupleDeclaration
	switch {
	case d.ctx.ValDeclaration() != nil:
		decl = d.ctx.ValDeclaration().(*grammar.ValDeclarationContext)
	case d.ctx.VarDeclaration() != nil:
		decl = d.ctx.VarDeclaration().(*grammar.VarDeclarationContext)
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
	ids := idList.(*grammar.IdentifierListContext).AllIdentifier()
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = id.GetText()
	}
	return names, true
}

// bindingNamesNative is bindingNames of td, a topLevelDeclaration node.
func bindingNamesNative(td native.GalaNodeRef) ([]string, bool) {
	decl, ok := firstKidNode(td)
	if !ok {
		return nil, false
	}
	var binding nativeBinding
	switch decl.Kind() {
	case native.GalaKIND_valDeclaration:
		binding = native.ValDeclarationNode{GalaNodeRef: decl}
	case native.GalaKIND_varDeclaration:
		binding = native.VarDeclarationNode{GalaNodeRef: decl}
	default:
		return nil, false
	}
	idList := bindingIdList(binding)
	if !idList.Ok() {
		return nil, false // parse-error recovery
	}
	var names []string
	for _, n := range nativeDeclNames(idList) {
		names = append(names, n.text)
	}
	return names, true
}
