package analyzer

import (
	"strings"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// The reads analyze makes of the compiled file before and around its
// collection, on galop's native tree: its package clause, its imports, the
// names its top-level declarations introduce, and the checks GALA-E0055,
// GALA-E0058 and GALA-E0066. Each function here is the twin of the one it is
// named after, which reads ANTLR's tree, and hands the same values to the
// same tree-free code. analyze reads a file on its native tree exactly when
// it collects the file's declarations there (see nativeFileOf), so a file is
// read on one tree or the other, never a mix. TestFileChecksAgree holds each
// pair together.

// readTree is one read of the compiled file at filePath: onNative of its
// native tree when it is read on nf, a native tree (see nativeFileOf), and
// onANTLR otherwise. what names the read for galaAnalyzer.onTreeRead, which,
// when set, is told of it, with a way to make it on ANTLR's tree in the same
// state.
func readTree[T any](a *galaAnalyzer, filePath, what string, nf nativeFile, onNative func(native.GalaNodeRef) T, onANTLR func() T) T {
	return readOn(a.onTreeRead, filePath, what, nf, onNative, onANTLR)
}

// readOn is readTree telling hook, when set, of the read.
func readOn[T any](hook func(treeRead), filePath, what string, nf nativeFile, onNative func(native.GalaNodeRef) T, onANTLR func() T) T {
	var got T
	if nf.ok() {
		got = onNative(nf.root.GalaNodeRef)
	} else {
		got = onANTLR()
	}
	if hook != nil {
		hook(treeRead{filePath: filePath, what: what, native: nf.ok(), got: got, antlr: func() any { return onANTLR() }})
	}
	return got
}

// treeRead is what readTree tells galaAnalyzer.onTreeRead about a read: the
// file, the read, whether it took the native tree, what it gave, and how to
// make it again, in the same state, on ANTLR's tree. A read that records
// metadata cannot be made again: it has no got and no antlr (see
// notePackageRead).
type treeRead struct {
	filePath, what string
	native         bool
	got            any
	antlr          func() any
}

// ownNames is the names a file's top-level declarations introduce, as
// collectTopLevelDeclaredNames records them: whether each names a type, and
// the methods by receiver type.
type ownNames struct {
	types   map[string]bool
	methods map[string]map[string]bool
}

func newOwnNames() ownNames {
	return ownNames{types: make(map[string]bool), methods: make(map[string]map[string]bool)}
}

// ownNamesOf is the names sf's top-level declarations introduce.
func ownNamesOf(sf *grammar.SourceFileContext) ownNames {
	o := newOwnNames()
	collectTopLevelDeclaredNames(sf, o.types, o.methods)
	return o
}

// packageClause is what a file's package clause says: the package's name, and
// the doc comment on the clause in docs, the file's doc comments.
type packageClause struct {
	name, doc string
}

// packageClauseNative is the package clause of root.
func packageClauseNative(root native.GalaNodeRef, docs galaparser.Docs) packageClause {
	pc := native.SourceFileNode{GalaNodeRef: root}.PackageClause()
	return packageClause{name: galaparser.NativeText(pc.Identifier().GalaNodeRef), doc: nativeDocAt(docs, pc.GalaNodeRef)}
}

// scanFileImportsNative is scanFileImports of root.
func scanFileImportsNative(root native.GalaNodeRef) []fileImport {
	var out []fileImport
	for decl := range galaparser.NativeKids(root, native.GalaKIND_importDeclaration) {
		for spec := range galaparser.NativeKids(decl, native.GalaKIND_importSpec) {
			s := native.ImportSpecNode{GalaNodeRef: spec}
			str := s.STRING()
			if !str.Ok() {
				continue
			}
			fi := fileImport{
				Path:   strings.Trim(str.GetText(), "\""),
				At:     nativePos(spec),
				PathAt: nativeTokenPos(str.Token()),
			}
			if alias := s.Identifier(); alias.Ok() {
				fi.Alias = galaparser.NativeText(alias.GalaNodeRef)
			} else {
				fi.IsDot = spec.ChildCount() > 1
			}
			out = append(out, fi)
		}
	}
	return out
}

// ownNamesNative is ownNamesOf root.
func ownNamesNative(root native.GalaNodeRef) ownNames {
	o := newOwnNames()
	forEachTopLevelNameNative(root, topLevelNameRecorder(o.types, o.methods))
	return o
}

// typeNamesOf is ownNamesOf sf's names without its methods: each name, and
// whether it names a type.
func typeNamesOf(sf *grammar.SourceFileContext) map[string]bool {
	names := make(map[string]bool)
	collectTopLevelDeclaredNames(sf, names, nil)
	return names
}

// typeNamesNative is typeNamesOf root.
func typeNamesNative(root native.GalaNodeRef) map[string]bool {
	names := make(map[string]bool)
	forEachTopLevelNameNative(root, topLevelNameRecorder(names, nil))
	return names
}

// checkDotImportCollisionsNative is checkDotImportCollisions of root.
func checkDotImportCollisionsNative(root native.GalaNodeRef, dotImports map[string][]string, richAST *transpiler.RichAST) error {
	return dotImportCollisions(dotImports, richAST, func(v topLevelVisitor) { forEachTopLevelNameNative(root, v) })
}

// forEachTopLevelNameNative is forEachTopLevelName of root.
func forEachTopLevelNameNative(root native.GalaNodeRef, v topLevelVisitor) {
	report := func(id native.IdentifierNode, kind string) {
		if id.Ok() {
			v.name(galaparser.NativeText(id.GalaNodeRef), nativePos(id.GalaNodeRef), kind)
		}
	}
	reportList := func(il native.IdentifierListNode) {
		for id := range galaparser.NativeKids(il.GalaNodeRef, native.GalaKIND_identifier) {
			report(native.IdentifierNode{GalaNodeRef: id}, declKindValue)
		}
	}
	for decl := range nativeTopLevelDecls(root) {
		switch decl.Kind() {
		case native.GalaKIND_functionDeclaration:
			fn := native.FunctionDeclarationNode{GalaNodeRef: decl}
			if recv := fn.Receiver(); !recv.Ok() {
				report(fn.Identifier(), declKindFunction)
			} else if v.method != nil {
				v.method(nativeBaseTypeName(recv.Type_()), galaparser.NativeText(fn.Identifier().GalaNodeRef))
			}
		case native.GalaKIND_typeDeclaration:
			report(native.TypeDeclarationNode{GalaNodeRef: decl}.Identifier(), declKindType)
		case native.GalaKIND_structShorthandDeclaration:
			report(native.StructShorthandDeclarationNode{GalaNodeRef: decl}.Identifier(), declKindType)
		case native.GalaKIND_opaqueTypeDeclaration:
			report(native.OpaqueTypeDeclarationNode{GalaNodeRef: decl}.Identifier(), declKindType)
		case native.GalaKIND_sealedTypeDeclaration:
			sc := native.SealedTypeDeclarationNode{GalaNodeRef: decl}
			report(sc.Identifier(), declKindType)
			for cc := range galaparser.NativeKids(decl, native.GalaKIND_sealedCase) {
				report(native.SealedCaseNode{GalaNodeRef: cc}.Identifier(), declKindVariant)
			}
		case native.GalaKIND_valDeclaration:
			vc := native.ValDeclarationNode{GalaNodeRef: decl}
			reportList(vc.IdentifierList())
			reportList(vc.TuplePattern().IdentifierList())
		case native.GalaKIND_varDeclaration:
			vc := native.VarDeclarationNode{GalaNodeRef: decl}
			reportList(vc.IdentifierList())
			reportList(vc.TuplePattern().IdentifierList())
		case native.GalaKIND_embedDeclaration:
			report(native.EmbedDeclarationNode{GalaNodeRef: decl}.Identifier(), declKindValue)
		}
	}
}

// checkGoKeywordNamesNative is checkGoKeywordNames of root.
func checkGoKeywordNamesNative(root native.GalaNodeRef) error {
	var k keywordNames
	root.Walk(func(n native.GalaNodeRef) bool {
		if k.done() {
			return false
		}
		if n.Kind() != native.GalaKIND_identifier {
			return true
		}
		if name := galaparser.NativeText(n); keywordNamed(name) {
			k.see(name, nativePos(n),
				func() bool { return isBareStatementNative(n) },
				func() bool { return declaresNameNative(n) },
				func() bool { return inPatternNative(n) })
		}
		return false
	})
	return k.err()
}

// isNativePattern reports whether n is a pattern: one of the rule's labeled
// alternatives, as every pattern node is.
func isNativePattern(n native.GalaNodeRef) bool {
	switch n.Kind() {
	case native.GalaKIND_restPattern, native.GalaKIND_expressionPattern, native.GalaKIND_typedPattern:
		return true
	}
	return false
}

// inPatternNative is inPattern of id.
func inPatternNative(id native.GalaNodeRef) bool {
	prev := id
	for node := id.Parent(); node.Ok(); prev, node = node, node.Parent() {
		if node.Kind() == native.GalaKIND_caseClause {
			return isNativePattern(prev)
		}
	}
	return false
}

// isBareStatementNative is isBareStatement of id.
func isBareStatementNative(id native.GalaNodeRef) bool {
	node := id
	for {
		parent := node.Parent()
		if !parent.Ok() || parent.ChildCount() != 1 {
			return false
		}
		if parent.Kind() == native.GalaKIND_simpleStatement {
			return true
		}
		node = parent
	}
}

// declaresNameNative is declaresName of id.
func declaresNameNative(id native.GalaNodeRef) bool {
	parent := id.Parent()
	if !parent.Ok() {
		return false
	}
	switch parent.Kind() {
	case native.GalaKIND_packageClause,
		native.GalaKIND_importSpec,
		native.GalaKIND_embedDeclaration,
		native.GalaKIND_structShorthandDeclaration,
		native.GalaKIND_sealedTypeDeclaration,
		native.GalaKIND_opaqueTypeDeclaration,
		native.GalaKIND_sealedCase,
		native.GalaKIND_sealedCaseField,
		native.GalaKIND_typeDeclaration,
		native.GalaKIND_structField,
		native.GalaKIND_methodSpec,
		native.GalaKIND_functionDeclaration,
		native.GalaKIND_receiver,
		native.GalaKIND_parameter,
		native.GalaKIND_identifierList,
		native.GalaKIND_bindDeclaration,
		native.GalaKIND_alsoDeclaration,
		native.GalaKIND_useDeclaration,
		native.GalaKIND_typedPattern:
		return true
	case native.GalaKIND_typeParameter:
		// `[T any]`: the first identifier is the parameter, the second its
		// constraint.
		return native.TypeParameterNode{GalaNodeRef: parent}.Identifier(0).GalaNodeRef == id
	}
	return false
}

// DotImportPathsNative is DotImportPaths of root, a file's native tree.
func DotImportPathsNative(root native.GalaNodeRef) []string {
	if !root.Ok() || root.Kind() != native.GalaKIND_sourceFile {
		return nil
	}
	return dotImportPaths(scanFileImportsNative(root))
}
