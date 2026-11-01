package transformer

import (
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// The compiled file's top-level walk reads the file's native tree
// (RichAST.Native), whichever engine parsed it: galop's native parser, or
// ANTLR's with its tree converted by galop's converter. Every clean parse has
// one, and only a clean parse is transformed.

// walkSource is the compiled file as the top-level walk reads it: the root of
// its native tree.
type walkSource struct {
	root native.SourceFileNode
}

// newWalkSource is the walk of richAST's file, converting its native tree if
// the analysis has not. A file without a native tree, or whose conversion
// failed, is an internal error: every clean parse has one.
func newWalkSource(richAST *transpiler.RichAST) (walkSource, error) {
	tree, err := richAST.Native.Tree()
	if err != nil {
		return walkSource{}, noNativeTree(err.Error())
	}
	root := tree.Root()
	if !root.Ok() || root.Kind() != native.GalaKIND_sourceFile {
		return walkSource{}, noNativeTree("its root is not a source file")
	}
	return walkSource{root: native.SourceFileNode{GalaNodeRef: root}}, nil
}

// noNativeTree is the internal error of a transform without a usable native
// tree, for why.
func noNativeTree(why string) error {
	return galaerr.NewCodedSemanticError(galaerr.CodeInternalTransformerPanic, 1, 0,
		"internal transpiler error: no native tree to transform: "+why, internalDefectHint)
}

// internalDefectHint is the hint of an internal error raised where the
// transformer knows the cause, unlike a recovered panic's
// (galaerr.InternalTransformerPanicHint).
const internalDefectHint = "not this file's fault: an internal transpiler defect. Please file an issue at https://github.com/martianoff/gala/issues with the source that triggered it"

// packageClause is the file's package name and the doc key of its package
// clause.
func (s walkSource) packageClause() (string, parser.DocKey) {
	pc := s.root.PackageClause()
	return pc.Identifier().GetText(), parser.NativeDocKeyOf(pc.GalaNodeRef)
}

// importDecl is an import declaration: its specs and where it is written.
type importDecl struct {
	specs []importSpecDecl
	at    srcNode
}

// imports reads the file's import declarations, in source order.
func (s walkSource) imports() []importDecl {
	var out []importDecl
	for n := range parser.NativeKids(s.root.GalaNodeRef, native.GalaKIND_importDeclaration) {
		out = append(out, importDecl{specs: readImportDeclNative(n), at: srcNode{node: n}})
	}
	return out
}

// aliasTargets calls yield with the name and the target type, as written, of
// each type alias the file declares, in source order.
func (s walkSource) aliasTargets(yield func(name, target string)) {
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
}

// topDecls is the file's top-level declarations, their topLevelDeclaration
// nodes, in source order.
func (s walkSource) topDecls() []native.GalaNodeRef {
	var out []native.GalaNodeRef
	for td := range parser.NativeKids(s.root.GalaNodeRef, native.GalaKIND_topLevelDeclaration) {
		out = append(out, td)
	}
	return out
}

// readDeclDocsNative is what attachDocs reads of td, a topLevelDeclaration
// node.
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

// bindingNames is the names a val or var declaration td, a
// topLevelDeclaration node, binds, for recordPackageValTypes, and false when
// td is no val or var or binds none (parse-error recovery).
func bindingNames(td native.GalaNodeRef) ([]string, bool) {
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
