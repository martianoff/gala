package analyzer

import (
	"strings"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
)

// The reads analyze makes of the compiled file before and around its
// collection, on galop's native tree: its package clause, its imports, the
// names its top-level declarations introduce, and the checks GALA-E0055,
// GALA-E0058 and GALA-E0066.

// ownNames is the names a file's top-level declarations introduce: whether
// each names a type, and the methods by receiver type.
type ownNames struct {
	types   map[string]bool
	methods map[string]map[string]bool
}

func newOwnNames() ownNames {
	return ownNames{types: make(map[string]bool), methods: make(map[string]map[string]bool)}
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

// ownNamesNative is the names the top-level declarations of root, a file's
// native tree, introduce.
func ownNamesNative(root native.GalaNodeRef) ownNames {
	o := newOwnNames()
	forEachTopLevelNameNative(root, topLevelNameRecorder(o.types, o.methods))
	return o
}

// typeNamesNative is ownNamesNative of root without its methods: each name,
// and whether it names a type.
func typeNamesNative(root native.GalaNodeRef) map[string]bool {
	names := make(map[string]bool)
	forEachTopLevelNameNative(root, topLevelNameRecorder(names, nil))
	return names
}

// forEachTopLevelNameNative tells v every name the top-level declarations of
// root, a file's native tree, introduce, and the kind of declaration: free
// functions, types, struct shorthands, sealed types and their case variants,
// package vals/vars (including tuple-pattern destructuring), and embed
// bindings; and, when v.method is set, every method.
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

// checkGoKeywordNamesNative reports the first identifier in root, a file's
// native tree, spelled like a Go keyword, preferring a declaration over a use,
// so a function named `go` that is called above its declaration is reported
// where it is declared — the place the rename starts. It returns nil when the
// file has none.
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

// inPatternNative reports whether the identifier id is part of a case
// pattern, where a name is a binding (`case break =>`, `case Some(break) =>`),
// never loop control. Call arguments are patterns in the grammar too, so only
// the pattern of a case clause counts.
func inPatternNative(id native.GalaNodeRef) bool {
	prev := id
	for node := id.Parent(); node.Ok(); prev, node = node, node.Parent() {
		if node.Kind() == native.GalaKIND_caseClause {
			return isNativePattern(prev)
		}
	}
	return false
}

// isBareStatementNative reports whether the identifier id is a whole
// statement on its own: `break` in a loop body or a match arm. The
// identifier's ancestors up to that statement each wrap a single child, so the
// walk climbs while that holds. A skipped `defer` / `go` / ... statement is
// left to GALA-E0036 (the transformer's checkForbiddenStatementKeyword), which
// inspects statements of this shape.
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

// declaresNameNative reports whether the identifier id is the name a
// declaration introduces, as opposed to a reference. Pattern bindings (`case
// go =>`) are expressions in the grammar and count as uses; they are still
// reported, only after any declaration.
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
		// `[T any]`: the identifier is the parameter; its constraint is a type.
		return native.TypeParameterNode{GalaNodeRef: parent}.Identifier().GalaNodeRef == id
	}
	return false
}

// DotImportPathsNative is the GALA packages the file whose native tree is
// root dot-imports, in source order; none for a root that is no source file.
func DotImportPathsNative(root native.GalaNodeRef) []string {
	if !root.Ok() || root.Kind() != native.GalaKIND_sourceFile {
		return nil
	}
	return dotImportPaths(scanFileImportsNative(root))
}
