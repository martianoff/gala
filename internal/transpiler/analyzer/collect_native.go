package analyzer

import (
	"fmt"
	"iter"
	"strings"
	"unicode/utf8"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// The collection of a file's declarations on galop's native tree
// (internal/parser/native): each function here is the twin of the one it is
// named after, which reads ANTLR's tree, and records the same metadata, in
// the same order, with the same diagnostics. TestCollectionsAgree holds each
// pair together over the repository's GALA sources.
//
// A file is collected on its native tree when it has one (see nativeFileOf):
// the file being compiled, whose parse comes with a handle, and a package
// file the parsed-file cache holds a converted tree for. Every other file —
// one parsed with syntax errors, an LSP buffer, a tree a caller handed in
// bare — is collected on ANTLR's tree. A file is collected on one tree or
// the other, never on a mix: the only crossing is inferPackageValInitType,
// expression inference not yet ported, which reads the initializer's
// context (nativeFile.expressionCtx).
//
// Children are read through the rule handles and dispatched on Kind(), never
// through Typed(), which builds a handle per node.

// nativeFile is a file's native tree, with the handle it came from for the
// crossings to ANTLR's tree, or the zero nativeFile: no tree (see ok).
type nativeFile struct {
	handle *galaparser.Native
	root   native.SourceFileNode
}

// nativeFileOf is the native tree of a parse's handle h, converted now if it
// was not yet, or no tree when there is none (a nil handle, or the conversion
// failed) or its bridge to ANTLR's tree does not hold, and the file is
// collected on ANTLR's tree. A handle comes from Parsed.CleanNative: an
// error-recovered tree is ANTLR's to read.
func nativeFileOf(h *galaparser.Native) nativeFile {
	tree, err := h.Tree()
	if err != nil {
		return nativeFile{}
	}
	root := tree.Root()
	if !root.Ok() || root.Kind() != native.GalaKIND_sourceFile {
		return nativeFile{}
	}
	// The crossing reads each node's context from the bridge, which holds one
	// for every node or none.
	if _, ok := h.Ctx(root).(*grammar.SourceFileContext); !ok {
		return nativeFile{}
	}
	return nativeFile{handle: h, root: native.SourceFileNode{GalaNodeRef: root}}
}

// ok reports whether f is a native tree, not the zero nativeFile.
func (f nativeFile) ok() bool { return f.root.Ok() }

// expressionCtx is the ANTLR context of expression e, or nil.
func (f nativeFile) expressionCtx(e native.ExpressionNode) grammar.IExpressionContext {
	ctx, _ := f.handle.Ctx(e.GalaNodeRef).(grammar.IExpressionContext)
	return ctx
}

// nativeKids is n's child nodes of kind, in order, in one pass over its
// children without allocating.
func nativeKids[H ~struct{ native.GalaNodeRef }](n native.GalaNodeRef, kind native.GalaKind) iter.Seq[H] {
	return func(yield func(H) bool) {
		if !n.Ok() {
			return
		}
		for i := range n.ChildCount() {
			if c := n.Child(i).Node(); c.Ok() && c.Kind() == kind && !yield(H{c}) {
				return
			}
		}
	}
}

// topLevelDecls is the declaration of each of root's top-level declarations,
// in order: the one node a topLevelDeclaration holds.
func topLevelDecls(root native.SourceFileNode) iter.Seq[native.GalaNodeRef] {
	return func(yield func(native.GalaNodeRef) bool) {
		for td := range nativeKids[native.TopLevelDeclarationNode](root.GalaNodeRef, native.GalaKIND_topLevelDeclaration) {
			if td.ChildCount() == 0 {
				continue
			}
			if d := td.Child(0).Node(); d.Ok() && !yield(d) {
				return
			}
		}
	}
}

// nativePos is transpiler.PosFromToken of n's first token.
func nativePos(n native.GalaNodeRef) transpiler.SourcePos {
	start := n.Start()
	return transpiler.SourcePos{Line: int(start.Line), Column: int(start.Column)}
}

// nativeDocAt is docAt of n's first token: the doc comment of the
// declaration n is.
func nativeDocAt(docs galaparser.Docs, n native.GalaNodeRef) string {
	if docs == nil || !n.Ok() {
		return ""
	}
	return docs[galaparser.NativeDocKeyOf(n)]
}

// nativeSetDoc is setDoc of n's first token.
func nativeSetDoc(dst *string, docs galaparser.Docs, n native.GalaNodeRef) {
	if d := nativeDocAt(docs, n); d != "" {
		*dst = d
	}
}

// nativeTypeParamsOf is typeParamsOf.
func nativeTypeParamsOf(tps native.TypeParametersNode) ([]string, map[string]string) {
	list := tps.TypeParameterList()
	if !list.Ok() {
		return nil, nil
	}
	var names []string
	var constraints map[string]string
	for tp := range nativeKids[native.TypeParameterNode](list.GalaNodeRef, native.GalaKIND_typeParameter) {
		name, constraint := tp.Identifier(0).GetText(), tp.Identifier(1)
		names = append(names, name)
		if constraint.Ok() {
			if constraints == nil {
				constraints = make(map[string]string)
			}
			constraints[name] = constraint.GetText()
		}
	}
	return names, constraints
}

// nativeDeclaredTypeParams is declaredTypeParams.
func (a *galaAnalyzer) nativeDeclaredTypeParams(tps native.TypeParametersNode, pkgName string) ([]string, map[string]string) {
	names, constraints := nativeTypeParamsOf(tps)
	for tp, c := range constraints {
		if c != "any" && c != "comparable" {
			constraints[tp] = a.resolveBaseName(c, pkgName).String()
		}
	}
	return names, constraints
}

// nativeBaseTypeName is getBaseTypeName.
func nativeBaseTypeName(t native.TypeNode) string {
	if !t.Ok() {
		return ""
	}
	if elem := galaparser.NativePtrElem(t); elem.Ok() {
		return nativeBaseTypeName(elem)
	}
	if qid := t.QualifiedIdentifier(); qid.Ok() {
		return qid.GetText()
	}
	if first := t.Type_(0); first.Ok() {
		// A slice, `[]T`, is the one type whose text starts with `[`. A
		// type node always spans tokens, so it has a first one.
		if t.StopIndex() >= t.StartIndex() && t.Tree().Text(t.StartIndex()) == "[" {
			return "[]" + nativeBaseTypeName(first)
		}
		return nativeBaseTypeName(first)
	}
	return ""
}

// nativeReceiverTypeArgNames is receiverTypeArgNames.
func nativeReceiverTypeArgNames(t native.TypeNode) []string {
	t, _ = galaparser.NativePointerElem(t)
	for t.Ok() && !t.QualifiedIdentifier().Ok() {
		if !t.Type_(0).Ok() || t.Type_(1).Ok() {
			return nil
		}
		t = t.Type_(0)
	}
	list := t.TypeArguments().TypeList()
	if !list.Ok() {
		return nil
	}
	var names []string
	for arg := range nativeKids[native.TypeNode](list.GalaNodeRef, native.GalaKIND_type) {
		names = append(names, arg.GetText())
	}
	return names
}

// nativeReceiverPointers is isPointerReceiver and isPtrReceiver of a
// receiver of type t: whether it is a pointer, `*T` or `Ptr[T]`, and whether
// it is the non-null `Ptr[T]`.
func nativeReceiverPointers(t native.TypeNode) (pointer, ptr bool) {
	if galaparser.NativePtrElem(t).Ok() {
		return true, true
	}
	_, pointer = galaparser.NativePointerElem(t)
	return pointer, false
}

// nativeSignatureResult is signatureResult.
func (a *galaAnalyzer) nativeSignatureResult(sig native.SignatureNode, pkgName string, typeParams []string) (transpiler.Type, []transpiler.Type) {
	if typ := sig.Type_(); typ.Ok() {
		return a.resolveTypeWithParams(galaparser.NativeTypeText(typ), pkgName, typeParams), nil
	}
	list := sig.GoResults()
	if !list.Ok() {
		return transpiler.VoidType{}, nil
	}
	var results []transpiler.Type
	for typ := range nativeKids[native.TypeNode](list.GalaNodeRef, native.GalaKIND_type) {
		results = append(results, a.resolveTypeWithParams(galaparser.NativeTypeText(typ), pkgName, typeParams))
	}
	value, _ := transpiler.GoResultValueOf(results)
	return value.Type, results
}

// nativeCausesInstantiationCycle is causesInstantiationCycle.
func (a *galaAnalyzer) nativeCausesInstantiationCycle(recvType native.TypeNode, sig native.SignatureNode) bool {
	retType := sig.Type_()
	if list := sig.GoResults(); list.Ok() {
		retType = list.Type_(0)
	}
	if !retType.Ok() {
		return false
	}
	// A receiver without type arguments, as most are, has no cycle:
	// instantiationCycle finds none in a text without `[`.
	recvText := galaparser.NativeTypeText(nativePointee(recvType))
	if !strings.Contains(recvText, "[") {
		return false
	}
	return instantiationCycle(recvText, galaparser.NativeTypeText(nativePointee(retType)))
}

// nativePointee is pointee.
func nativePointee(t native.TypeNode) native.TypeNode {
	for {
		elem, isPtr := galaparser.NativePointerElem(t)
		if !isPtr {
			return t
		}
		t = elem
	}
}

// nativeTypeAliasTarget is typeAliasTarget.
func nativeTypeAliasTarget(typeDecl native.TypeDeclarationNode) string {
	alias := typeDecl.TypeAlias()
	if !alias.Ok() {
		return ""
	}
	return galaparser.NativeAliasText(alias)
}

// nativeRecordFieldDefault is recordFieldDefault.
func nativeRecordFieldDefault(meta *transpiler.TypeMetadata, fieldName string, p native.ParameterNode) {
	if !p.ParamDefault().Ok() {
		return
	}
	if meta.FieldDefaults == nil {
		meta.FieldDefaults = make(map[string]transpiler.DefaultExpr)
	}
	meta.FieldDefaults[fieldName], _ = nativeParamDefaultSource(p)
}

// nativeRecordParamDefault is recordParamDefault, the expression's node for
// its parse tree.
func nativeRecordParamDefault(exprs *map[int]transpiler.DefaultExpr, i int, p native.ParameterNode) native.ExpressionNode {
	if !p.ParamDefault().Ok() {
		return native.ExpressionNode{}
	}
	if *exprs == nil {
		*exprs = make(map[int]transpiler.DefaultExpr)
	}
	def, expr := nativeParamDefaultSource(p)
	(*exprs)[i] = def
	return expr
}

// nativeParamDefaultSource is paramDefaultSource.
func nativeParamDefaultSource(p native.ParameterNode) (transpiler.DefaultExpr, native.ExpressionNode) {
	expr := p.ParamDefault().Expression()
	return nativeDefaultExpr(expr), expr
}

// nativeDefaultExpr is the DefaultExpr of default expression e: its source
// text, as transpiler.SourceText gives a context's, and its position.
func nativeDefaultExpr(e native.ExpressionNode) transpiler.DefaultExpr {
	return transpiler.DefaultExpr{Text: e.SourceText(), Pos: nativePos(e.GalaNodeRef)}
}

// nativeSpanOf is spanOfCtx. Columns are code points, as ANTLR's are.
func nativeSpanOf(n native.GalaNodeRef) defaultExprSpan {
	start := n.Start()
	sp := defaultExprSpan{line: int(start.Line), startCol: int(start.Column)}
	if stop := n.StopIndex(); stop >= 0 {
		sp.endCol = int(n.Stop().Column) + utf8.RuneCountInString(n.Tree().Text(stop))
	} else {
		sp.endCol = sp.startCol
	}
	return sp
}

// nativeSealedDeclOf is sealedDeclOf.
func nativeSealedDeclOf(n native.SealedTypeDeclarationNode, docs galaparser.Docs) sealedDecl {
	id := n.Identifier()
	d := sealedDecl{
		name: id.GetText(),
		doc:  nativeDocAt(docs, n.GalaNodeRef),
		pos:  nativePos(id.GalaNodeRef),
	}
	d.typeParams, d.typeParamConstraints = nativeTypeParamsOf(n.TypeParameters())
	for sc := range nativeKids[native.SealedCaseNode](n.GalaNodeRef, native.GalaKIND_sealedCase) {
		caseID := sc.Identifier()
		c := sealedCase{
			name:      caseID.GetText(),
			doc:       nativeDocAt(docs, sc.GalaNodeRef),
			pos:       nativePos(caseID.GalaNodeRef),
			isDefault: sc.DEFAULT().Ok(),
		}
		for fc := range nativeKids[native.SealedCaseFieldNode](sc.SealedCaseFieldList().GalaNodeRef, native.GalaKIND_sealedCaseField) {
			field := sealedCaseField{name: fc.Identifier().GetText(), typeName: galaparser.NativeTypeText(fc.Type_())}
			if pd := fc.ParamDefault(); pd.Ok() {
				def := nativeDefaultExpr(pd.Expression())
				field.def = &def
			}
			c.fields = append(c.fields, field)
		}
		d.cases = append(d.cases, c)
	}
	return d
}

// nativeOpaqueDeclOf is opaqueDeclOf.
func nativeOpaqueDeclOf(n native.OpaqueTypeDeclarationNode, docs galaparser.Docs) opaqueDecl {
	id := n.Identifier()
	d := opaqueDecl{
		name: id.GetText(),
		doc:  nativeDocAt(docs, n.GalaNodeRef),
		pos:  nativePos(id.GalaNodeRef),
	}
	d.typeParams, d.typeParamConstraints = nativeTypeParamsOf(n.TypeParameters())
	if typ := n.Type_(); typ.Ok() {
		d.underlying = galaparser.NativeTypeText(typ)
	}
	return d
}

// nativeParams is the parameters of a parameters node, in order, with their
// indexes.
func nativeParams(params native.ParametersNode) iter.Seq2[int, native.ParameterNode] {
	return func(yield func(int, native.ParameterNode) bool) {
		i := 0
		for p := range nativeKids[native.ParameterNode](params.ParameterList().GalaNodeRef, native.GalaKIND_parameter) {
			if !yield(i, p) {
				return
			}
			i++
		}
	}
}

// collectFileTypesNative is collectFileTypes.
func (a *galaAnalyzer) collectFileTypesNative(nf nativeFile, fd *fileDecls) error {
	richAST, pkgName, filePath, absFilePath, docs, decls := fd.richAST, fd.pkgName, fd.filePath, fd.absFilePath, fd.docs, fd.decls
	for decl := range topLevelDecls(nf.root) {
		switch decl.Kind() {
		case native.GalaKIND_typeDeclaration:
			ctx := native.TypeDeclarationNode{GalaNodeRef: decl}
			id := ctx.Identifier()
			typeName := id.GetText()

			// The std names the transpiler gives built-in meaning are reserved;
			// any other std type is shadowed by the declaration.
			if err := CheckStdConflict(typeName, pkgName); err != nil {
				return err
			}

			fullTypeName := qualifiedName(pkgName, typeName)

			var meta *transpiler.TypeMetadata
			if existing, ok := richAST.Types[fullTypeName]; ok && existing.Package == pkgName {
				// Error if type is being redefined from a DIFFERENT file.
				// Skip if DefinedIn is empty (cache) or same file (re-analysis from analyzePackage).
				if existing.DefinedIn != "" && hasTypeDefinition(existing) && !(existing.DefinedIn != filePath && a.paths.sameFile(existing.DefinedIn, absFilePath)) {
					pos := nativePos(decl)
					return typeRedefinedError(typeName, pkgName, pos.Line, pos.Column,
						existing.DefinedIn, filePath, existing.Pos)
				}
				meta = existing
				// Clear fields to avoid duplicates if re-analyzing
				meta.IsInterface = false
				meta.Fields = make(map[string]transpiler.Type)
				meta.FieldNames = nil
				meta.ImmutFlags = nil
				meta.StoredAsDeclared = nil
				meta.FieldPositions = nil
				meta.FieldDocs = nil
				meta.Pos = nativePos(id.GalaNodeRef)
				nativeSetDoc(&meta.Doc, docs, decl)
			} else {
				meta = &transpiler.TypeMetadata{
					Name:    typeName,
					Package: pkgName,
					Doc:     nativeDocAt(docs, decl),
					Pos:     nativePos(id.GalaNodeRef),
					Methods: make(map[string]*transpiler.MethodMetadata),
					Fields:  make(map[string]transpiler.Type),
				}
				richAST.Types[fullTypeName] = meta
			}

			names, constraints := nativeTypeParamsOf(ctx.TypeParameters())
			meta.TypeParams = append(meta.TypeParams, names...)
			for tp, c := range constraints {
				if meta.TypeParamConstraints == nil {
					meta.TypeParamConstraints = make(map[string]string)
				}
				meta.TypeParamConstraints[tp] = c
			}

			if structType := ctx.StructType(); structType.Ok() {
				for fctx := range nativeKids[native.StructFieldNode](structType.GalaNodeRef, native.GalaKIND_structField) {
					fieldID := fctx.Identifier()
					fieldName := fieldID.GetText()
					// Reject duplicate field names within a single struct
					// declaration (see collectFileTypes).
					if _, exists := meta.Fields[fieldName]; exists {
						pos := nativePos(fieldID.GalaNodeRef)
						return galaerr.NewCodedSemanticError(
							galaerr.CodeStructFieldRedeclared,
							pos.Line, pos.Column,
							fmt.Sprintf("field %q already declared in struct %q", fieldName, typeName),
							"rename or remove the duplicate field",
						)
					}
					meta.Fields[fieldName] = a.resolveTypeWithParams(galaparser.NativeTypeText(fctx.Type_()), pkgName, meta.TypeParams)
					meta.FieldNames = append(meta.FieldNames, fieldName)
					meta.ImmutFlags = append(meta.ImmutFlags, !fctx.VAR().Ok())
					if meta.FieldPositions == nil {
						meta.FieldPositions = make(map[string]transpiler.SourcePos)
					}
					meta.FieldPositions[fieldName] = nativePos(fieldID.GalaNodeRef)
					addFieldDoc(meta, fieldName, nativeDocAt(docs, fctx.GalaNodeRef))
				}
				meta.DefinedIn = filePath
			}

			// Extract interface method signatures as type methods
			if ifaceType := ctx.InterfaceType(); ifaceType.Ok() {
				meta.IsInterface = true
				// seenSpecs: see collectFileTypes.
				seenSpecs := make(map[string]bool)
				for msCtx := range nativeKids[native.MethodSpecNode](ifaceType.GalaNodeRef, native.GalaKIND_methodSpec) {
					msID := msCtx.Identifier()
					methodName := msID.GetText()
					if seenSpecs[methodName] {
						pos := nativePos(msID.GalaNodeRef)
						return galaerr.NewCodedSemanticError(
							galaerr.CodeInterfaceMethodRedeclared,
							pos.Line, pos.Column,
							fmt.Sprintf("method %q already declared in interface %q", methodName, typeName),
							"rename or remove the duplicate method",
						)
					}
					seenSpecs[methodName] = true
					methodMeta := &transpiler.MethodMetadata{
						Name:      methodName,
						Package:   pkgName,
						Doc:       nativeDocAt(docs, msCtx.GalaNodeRef),
						DefinedIn: filePath,
					}
					a.nativeMethodSpec(methodMeta, msCtx, pkgName, meta.TypeParams)
					meta.Methods[methodName] = methodMeta
				}
			}

		case native.GalaKIND_structShorthandDeclaration:
			ctx := native.StructShorthandDeclarationNode{GalaNodeRef: decl}
			id := ctx.Identifier()
			typeName := id.GetText()

			// Reserved std names: see the type declaration case above.
			if err := CheckStdConflict(typeName, pkgName); err != nil {
				return err
			}

			fullTypeName := qualifiedName(pkgName, typeName)

			var meta *transpiler.TypeMetadata
			pos := nativePos(id.GalaNodeRef)
			if existing, ok := richAST.Types[fullTypeName]; ok && existing.Package == pkgName {
				if existing.DefinedIn != "" && hasTypeDefinition(existing) && !(existing.DefinedIn != filePath && a.paths.sameFile(existing.DefinedIn, absFilePath)) {
					declPos := nativePos(decl)
					return typeRedefinedError(typeName, pkgName, declPos.Line, declPos.Column,
						existing.DefinedIn, filePath, existing.Pos)
				}
				meta = existing
				meta.Fields = make(map[string]transpiler.Type)
				meta.FieldNames = nil
				meta.ImmutFlags = nil
				meta.StoredAsDeclared = nil
				meta.FieldPositions = nil
				meta.FieldDocs = nil
				meta.FieldDefaults = nil
				meta.Pos = pos
				nativeSetDoc(&meta.Doc, docs, decl)
			} else {
				meta = &transpiler.TypeMetadata{
					Name:    typeName,
					Package: pkgName,
					Doc:     nativeDocAt(docs, decl),
					Pos:     pos,
					Methods: make(map[string]*transpiler.MethodMetadata),
					Fields:  make(map[string]transpiler.Type),
				}
				richAST.Types[fullTypeName] = meta
			}

			names, constraints := nativeTypeParamsOf(ctx.TypeParameters())
			meta.TypeParams = append(meta.TypeParams, names...)
			for tp, c := range constraints {
				if meta.TypeParamConstraints == nil {
					meta.TypeParamConstraints = make(map[string]string)
				}
				meta.TypeParamConstraints[tp] = c
			}

			if params := ctx.Parameters(); params.Ok() {
				for _, pctx := range nativeParams(params) {
					fieldID := pctx.Identifier()
					fieldName := fieldID.GetText()
					// Reject duplicate field names within a shorthand
					// struct declaration (see collectFileTypes).
					if _, exists := meta.Fields[fieldName]; exists {
						fieldPos := nativePos(fieldID.GalaNodeRef)
						return galaerr.NewCodedSemanticError(
							galaerr.CodeStructFieldRedeclared,
							fieldPos.Line, fieldPos.Column,
							fmt.Sprintf("field %q already declared in struct %q", fieldName, typeName),
							"rename or remove the duplicate field",
						)
					}
					decls.recordShorthandField(meta, fieldName, a.resolveTypeWithParams(galaparser.NativeTypeText(pctx.Type_()), pkgName, meta.TypeParams), pctx.VAL().Ok(), pctx.VAR().Ok())
					if meta.FieldPositions == nil {
						meta.FieldPositions = make(map[string]transpiler.SourcePos)
					}
					meta.FieldPositions[fieldName] = nativePos(fieldID.GalaNodeRef)
					nativeRecordFieldDefault(meta, fieldName, pctx)
				}
				meta.IsShorthand = true
				meta.DefinedIn = filePath
			}
		}
	}
	return nil
}

// nativeMethodSpec fills in the type parameters, result and parameter types
// of methodMeta, the metadata of interface method msCtx, as collectFileTypes
// and extractSiblingFullMetadata do; typeTypeParams are the interface's type
// parameters.
func (a *galaAnalyzer) nativeMethodSpec(methodMeta *transpiler.MethodMetadata, msCtx native.MethodSpecNode, pkgName string, typeTypeParams []string) {
	methodMeta.TypeParams, methodMeta.TypeParamConstraints = a.nativeDeclaredTypeParams(msCtx.TypeParameters(), pkgName)
	var allTypeParams []string
	allTypeParams = append(allTypeParams, typeTypeParams...)
	allTypeParams = append(allTypeParams, methodMeta.TypeParams...)
	sig := msCtx.Signature()
	methodMeta.ReturnType, methodMeta.GoResults = a.nativeSignatureResult(sig, pkgName, allTypeParams)
	for _, paramCtx := range nativeParams(sig.Parameters()) {
		if typ := paramCtx.Type_(); typ.Ok() {
			methodMeta.ParamTypes = append(methodMeta.ParamTypes, a.resolveTypeWithParams(galaparser.NativeTypeText(typ), pkgName, allTypeParams))
			methodMeta.Variadic = paramCtx.ELLIPSIS().Ok()
		} else {
			methodMeta.ParamTypes = append(methodMeta.ParamTypes, transpiler.NilType{})
		}
	}
}

// nativeParamName is a parameter's name, "" when it has none.
func nativeParamName(p native.ParameterNode) string {
	if id := p.Identifier(); id.Ok() {
		return id.GetText()
	}
	return ""
}

// collectFileFuncsNative is collectFileFuncs.
func (a *galaAnalyzer) collectFileFuncsNative(nf nativeFile, fd *fileDecls) error {
	richAST, pkgName, filePath, absFilePath, docs, decls := fd.richAST, fd.pkgName, fd.filePath, fd.absFilePath, fd.docs, fd.decls
	// 1.5 Collect sealed types.
	for decl := range topLevelDecls(nf.root) {
		if decl.Kind() != native.GalaKIND_sealedTypeDeclaration {
			continue
		}
		ctx := native.SealedTypeDeclarationNode{GalaNodeRef: decl}
		sealedName := ctx.Identifier().GetText()
		fullSealedName := qualifiedName(pkgName, sealedName)
		// Check for redefinition (skip if DefinedIn is empty — type came from cache)
		if existing, ok := richAST.Types[fullSealedName]; ok && existing.DefinedIn != "" && hasTypeDefinition(existing) && !(existing.DefinedIn != filePath && a.paths.sameFile(existing.DefinedIn, absFilePath)) {
			pos := nativePos(decl)
			return typeRedefinedError(sealedName, pkgName, pos.Line, pos.Column,
				existing.DefinedIn, filePath, existing.Pos)
		}
		if err := a.registerSealedType(nativeSealedDeclOf(ctx, docs), pkgName, richAST); err != nil {
			return err
		}
		fd.sealedDeclaredHere(fullSealedName)
	}

	// 1.6 Collect opaque types
	for decl := range topLevelDecls(nf.root) {
		if decl.Kind() != native.GalaKIND_opaqueTypeDeclaration {
			continue
		}
		octx := native.OpaqueTypeDeclarationNode{GalaNodeRef: decl}
		typeName := octx.Identifier().GetText()
		if err := CheckStdConflict(typeName, pkgName); err != nil {
			return err
		}
		fullTypeName := qualifiedName(pkgName, typeName)
		if existing, ok := richAST.Types[fullTypeName]; ok && existing.Package == pkgName && existing.DefinedIn != "" && hasTypeDefinition(existing) && !(existing.DefinedIn != filePath && a.paths.sameFile(existing.DefinedIn, absFilePath)) {
			pos := nativePos(decl)
			return typeRedefinedError(typeName, pkgName, pos.Line, pos.Column,
				existing.DefinedIn, filePath, existing.Pos)
		}
		a.registerOpaqueType(nativeOpaqueDeclOf(octx, docs), pkgName, filePath, richAST)
	}

	// 2. Collect methods and functions
	for decl := range topLevelDecls(nf.root) {
		if decl.Kind() != native.GalaKIND_functionDeclaration {
			continue
		}
		ctx := native.FunctionDeclarationNode{GalaNodeRef: decl}
		declPos := nativePos(decl)
		sig := ctx.Signature()
		if recvCtx := ctx.Receiver(); recvCtx.Ok() {
			recvType := recvCtx.Type_()
			baseType := nativeBaseTypeName(recvType)
			if baseType == "" {
				continue
			}
			id := ctx.Identifier()
			methodName := id.GetText()
			fullBaseType := baseType
			if !strings.Contains(baseType, ".") {
				fullBaseType = qualifiedName(pkgName, baseType)
			}

			pointer, ptr := nativeReceiverPointers(recvType)
			methodMeta := &transpiler.MethodMetadata{
				Name:            methodName,
				Package:         pkgName,
				Doc:             nativeDocAt(docs, decl),
				Pos:             nativePos(id.GalaNodeRef),
				ReceiverName:    recvCtx.Identifier().GetText(),
				PointerReceiver: pointer,
				PtrReceiver:     ptr,
			}
			methodMeta.TypeParams, methodMeta.TypeParamConstraints = a.nativeDeclaredTypeParams(ctx.TypeParameters(), pkgName)

			// The receiver type's parameters, for resolving the signature (see
			// collectFileFuncs).
			var typeTypeParams []string
			if typeMeta, ok := richAST.Types[fullBaseType]; ok {
				typeTypeParams = typeMeta.TypeParams
			}
			recvNames := nativeReceiverTypeArgNames(recvType)
			allTypeParams := methodTypeParamScope(recvNames, typeTypeParams, methodMeta.TypeParams)

			methodMeta.ReturnType, methodMeta.GoResults = a.nativeSignatureResult(sig, pkgName, allTypeParams)
			// Go generics instantiation cycle: see collectFileFuncs.
			if a.nativeCausesInstantiationCycle(recvType, sig) {
				methodMeta.IsGeneric = true
			}

			for i, paramCtx := range nativeParams(sig.Parameters()) {
				if typ := paramCtx.Type_(); typ.Ok() {
					methodMeta.ParamTypes = append(methodMeta.ParamTypes, a.resolveTypeWithParams(galaparser.NativeTypeText(typ), pkgName, allTypeParams))
					methodMeta.Variadic = paramCtx.ELLIPSIS().Ok()
				} else {
					methodMeta.ParamTypes = append(methodMeta.ParamTypes, transpiler.NilType{})
				}
				methodMeta.ParamNames = append(methodMeta.ParamNames, nativeParamName(paramCtx))
				nativeRecordParamDefault(&methodMeta.DefaultExprs, i, paramCtx)
			}

			// The signature takes its type's parameter names after
			// section 2.5, when every file's types are known.
			decls.recordReceiver(methodMeta, recvNames, fullBaseType)

			if typeMeta, ok := richAST.Types[fullBaseType]; ok {
				if existing, exists := typeMeta.Methods[methodName]; exists {
					// Error if method already has a user-defined implementation
					if existing.DefinedIn != "" && !(existing.DefinedIn != filePath && a.paths.sameFile(existing.DefinedIn, absFilePath)) {
						return methodRedefinedError(methodName, baseType, pkgName,
							declPos.Line, declPos.Column,
							existing.DefinedIn, filePath, existing.Pos)
					}
					// Merge IsGeneric: see collectFileFuncs.
					methodMeta.IsGeneric = methodMeta.IsGeneric || existing.IsGeneric
				}
				methodMeta.DefinedIn = filePath
				typeMeta.Methods[methodName] = methodMeta
			} else {
				richAST.Types[fullBaseType] = &transpiler.TypeMetadata{
					Name:    baseType,
					Package: pkgName,
					Methods: map[string]*transpiler.MethodMetadata{methodName: methodMeta},
					Fields:  make(map[string]transpiler.Type),
				}
			}
			// Record the declaration for the sibling pass: see
			// collectFileFuncs.
			if prev, dup := decls.recordMethod(fullBaseType+"."+methodName, filePath, methodMeta.Pos); dup {
				return methodRedefinedError(methodName, baseType, pkgName,
					declPos.Line, declPos.Column,
					prev.file, filePath, prev.pos)
			}
			continue
		}

		// Top-level function
		id := ctx.Identifier()
		funcName := id.GetText()

		// Check for std library conflicts
		if err := CheckStdConflict(funcName, pkgName); err != nil {
			return err
		}

		fullFuncName := qualifiedName(pkgName, funcName)
		funcMeta := &transpiler.FunctionMetadata{
			Name:      funcName,
			Package:   pkgName,
			Doc:       nativeDocAt(docs, decl),
			Pos:       nativePos(id.GalaNodeRef),
			DefinedIn: filePath,
		}
		// Collect type parameters first so we can resolve param types correctly
		funcMeta.TypeParams, funcMeta.TypeParamConstraints = a.nativeDeclaredTypeParams(ctx.TypeParameters(), pkgName)
		funcMeta.ReturnType, funcMeta.GoResults = a.nativeSignatureResult(sig, pkgName, funcMeta.TypeParams)
		// Source spans of each default expression, keyed by param index.
		var defaultSpans map[int]defaultExprSpan
		for i, paramCtx := range nativeParams(sig.Parameters()) {
			if typ := paramCtx.Type_(); typ.Ok() {
				funcMeta.ParamTypes = append(funcMeta.ParamTypes, a.resolveTypeWithParams(galaparser.NativeTypeText(typ), pkgName, funcMeta.TypeParams))
				funcMeta.Variadic = paramCtx.ELLIPSIS().Ok()
			} else {
				funcMeta.ParamTypes = append(funcMeta.ParamTypes, transpiler.NilType{})
			}
			funcMeta.ParamNames = append(funcMeta.ParamNames, nativeParamName(paramCtx))
			if expr := nativeRecordParamDefault(&funcMeta.DefaultExprs, i, paramCtx); expr.Ok() {
				if defaultSpans == nil {
					defaultSpans = make(map[int]defaultExprSpan)
				}
				defaultSpans[i] = nativeSpanOf(expr.GalaNodeRef)
			}
		}
		// Validate default parameter rules once the sibling files' type
		// declarations are known too (section 2.5).
		if len(funcMeta.DefaultExprs) > 0 {
			fd.pendingDefaultChecks = append(fd.pendingDefaultChecks, pendingDefaultCheck{
				meta: funcMeta, line: declPos.Line, col: declPos.Column, spans: defaultSpans,
			})
		}
		// Reject redeclaration of a top-level function within the same
		// package: see collectFileFuncs.
		if existing, ok := richAST.Functions[fullFuncName]; ok {
			if existing.DefinedIn != "" && !(existing.DefinedIn != filePath && a.paths.sameFile(existing.DefinedIn, absFilePath)) {
				return funcRedeclaredError(funcName, pkgName,
					declPos.Line, declPos.Column,
					existing.DefinedIn, filePath, existing.Pos)
			}
		}
		if prev, dup := decls.recordFunc(fullFuncName, filePath, funcMeta.Pos); dup {
			return funcRedeclaredError(funcName, pkgName,
				declPos.Line, declPos.Column,
				prev.file, filePath, prev.pos)
		}
		richAST.Functions[fullFuncName] = funcMeta
	}
	return nil
}

// collectFileEmbedsNative is collectFileEmbeds.
func collectFileEmbedsNative(nf nativeFile, richAST *transpiler.RichAST) {
	for decl := range topLevelDecls(nf.root) {
		if decl.Kind() != native.GalaKIND_embedDeclaration {
			continue
		}
		ctx := native.EmbedDeclarationNode{GalaNodeRef: decl}
		typeName := ""
		if typ := ctx.Type_(); typ.Ok() {
			typeName = galaparser.NativeTypeText(typ)
		}
		var patterns []string
		for _, s := range ctx.EmbedPatterns().AllSTRING() {
			patterns = append(patterns, strings.Trim(s.GetText(), "\""))
		}
		richAST.EmbedDirectives = append(richAST.EmbedDirectives, embedDirective(ctx.Identifier().GetText(), typeName, patterns))
	}
}

// fileTypeAliasesNative is fileTypeAliases.
func (a *galaAnalyzer) fileTypeAliasesNative(nf nativeFile, pkgName string) map[string]transpiler.Type {
	aliases := make(map[string]transpiler.Type)
	for decl := range topLevelDecls(nf.root) {
		if decl.Kind() != native.GalaKIND_typeDeclaration {
			continue
		}
		typeDecl := native.TypeDeclarationNode{GalaNodeRef: decl}
		id := typeDecl.Identifier()
		if !id.Ok() {
			continue
		}
		target := nativeTypeAliasTarget(typeDecl)
		if target == "" {
			continue
		}
		if resolved := a.resolveTypeWithParams(target, pkgName, nil); !resolved.IsNil() {
			aliases[id.GetText()] = resolved
		}
	}
	return aliases
}

// extractPackageValsNative is extractPackageVals. An initializer is inferred
// on its ANTLR context (inferPackageValInitType is not ported yet).
func (a *galaAnalyzer) extractPackageValsNative(nf nativeFile, pkgName string, richAST *transpiler.RichAST, docs galaparser.Docs, absFilePath string) {
	for decl := range topLevelDecls(nf.root) {
		var (
			idList   native.IdentifierListNode
			typeCtx  native.TypeNode
			exprList native.ExpressionListNode
			tp       native.TuplePatternNode
			isVal    bool
		)
		switch decl.Kind() {
		case native.GalaKIND_valDeclaration:
			vc := native.ValDeclarationNode{GalaNodeRef: decl}
			idList, typeCtx, exprList, tp = vc.IdentifierList(), vc.Type_(), vc.ExpressionList(), vc.TuplePattern()
			isVal = true
		case native.GalaKIND_varDeclaration:
			vc := native.VarDeclarationNode{GalaNodeRef: decl}
			idList, typeCtx, exprList, tp = vc.IdentifierList(), vc.Type_(), vc.ExpressionList(), vc.TuplePattern()
		default:
			continue
		}
		if tp.Ok() {
			// A tuple destructuring: see extractPackageVals.
			idList, typeCtx, exprList = tp.IdentifierList(), native.TypeNode{}, native.ExpressionListNode{}
		}
		if !idList.Ok() {
			continue // parse-error recovery
		}

		names := idList.AllIdentifier()
		var exprs []native.ExpressionNode
		if exprList.Ok() {
			exprs = exprList.AllExpression()
		}

		// The declaration's doc, shared by every name it binds (see
		// extractPackageVals). Its first token is its topLevelDeclaration's.
		doc := nativeDocAt(docs, decl)
		for i, idCtx := range names {
			name := idCtx.GetText()
			if name == "_" {
				continue // the blank identifier binds nothing
			}
			var valType transpiler.Type = transpiler.NilType{}
			if typeCtx.Ok() {
				valType = a.resolveTypeWithParams(galaparser.NativeTypeText(typeCtx), pkgName, nil)
			} else if len(exprs) == len(names) {
				if expr := nf.expressionCtx(exprs[i]); expr != nil {
					valType = a.inferPackageValInitType(expr, pkgName, richAST)
				}
			}
			valType = knownType(valType)
			if richAST.PackageVals == nil {
				richAST.PackageVals = make(map[string]*transpiler.PackageValMetadata)
			}
			pv := &transpiler.PackageValMetadata{
				Name:      name,
				Type:      valType,
				IsVal:     isVal,
				Doc:       doc,
				Pos:       nativePos(idCtx.GalaNodeRef),
				DefinedIn: absFilePath,
			}
			if transpiler.PreferPackageVal(richAST.PackageVals[name], pv) {
				richAST.PackageVals[name] = pv
			}
		}
	}
}

// extractSiblingDeclsNative is extractSiblingFullMetadata after its prologue,
// which resolves the sibling's path (absSibPath, and sibDisplay for
// diagnostics), doc comments and imports and calls it for a sibling the
// parsed-file cache holds a native tree of.
func (a *galaAnalyzer) extractSiblingDeclsNative(nf nativeFile, pkgName string, richAST *transpiler.RichAST, absSibPath, sibDisplay string, docs galaparser.Docs, decls *packageDecls) error {
	// 1. Collect struct types with full field info
	for decl := range topLevelDecls(nf.root) {
		switch decl.Kind() {
		case native.GalaKIND_typeDeclaration:
			ctx := native.TypeDeclarationNode{GalaNodeRef: decl}
			id := ctx.Identifier()
			typeName := id.GetText()
			fullTypeName := qualifiedName(pkgName, typeName)
			// Redefinition, or a type already loaded from this same sibling:
			// see extractSiblingFullMetadata.
			if existing, ok := richAST.Types[fullTypeName]; ok && len(existing.FieldNames) > 0 {
				if existing.DefinedIn != "" && !a.paths.sameFile(existing.DefinedIn, absSibPath) {
					return siblingTypeRedefinedError(typeName, pkgName, existing, sibDisplay,
						nativePos(id.GalaNodeRef), decls.mainFile)
				}
				if existing.DefinedIn != "" {
					continue
				}
			}
			// Preserve existing methods from placeholder entry (e.g., methods from current file)
			existingMethods := make(map[string]*transpiler.MethodMetadata)
			if existing, ok := richAST.Types[fullTypeName]; ok {
				for k, v := range existing.Methods {
					existingMethods[k] = v
				}
			}
			meta := &transpiler.TypeMetadata{
				Name:    typeName,
				Package: pkgName,
				Doc:     nativeDocAt(docs, decl),
				Pos:     nativePos(id.GalaNodeRef),
				Methods: existingMethods,
				Fields:  make(map[string]transpiler.Type),
			}
			// Preserve DefinedIn from main analysis if already set
			if ex, ok := richAST.Types[fullTypeName]; ok && ex.DefinedIn != "" {
				meta.DefinedIn = ex.DefinedIn
			}
			meta.TypeParams, meta.TypeParamConstraints = nativeTypeParamsOf(ctx.TypeParameters())
			if structType := ctx.StructType(); structType.Ok() {
				for fctx := range nativeKids[native.StructFieldNode](structType.GalaNodeRef, native.GalaKIND_structField) {
					fieldID := fctx.Identifier()
					fieldName := fieldID.GetText()
					meta.Fields[fieldName] = a.resolveTypeWithParams(galaparser.NativeTypeText(fctx.Type_()), pkgName, meta.TypeParams)
					meta.FieldNames = append(meta.FieldNames, fieldName)
					meta.ImmutFlags = append(meta.ImmutFlags, !fctx.VAR().Ok())
					if meta.FieldPositions == nil {
						meta.FieldPositions = make(map[string]transpiler.SourcePos)
					}
					meta.FieldPositions[fieldName] = nativePos(fieldID.GalaNodeRef)
					addFieldDoc(meta, fieldName, nativeDocAt(docs, fctx.GalaNodeRef))
				}
				if meta.DefinedIn == "" {
					meta.DefinedIn = absSibPath
				}
			}
			if ifaceType := ctx.InterfaceType(); ifaceType.Ok() {
				meta.IsInterface = true
				for msCtx := range nativeKids[native.MethodSpecNode](ifaceType.GalaNodeRef, native.GalaKIND_methodSpec) {
					msID := msCtx.Identifier()
					methodName := msID.GetText()
					methodMeta := &transpiler.MethodMetadata{
						Name:      methodName,
						Package:   pkgName,
						Doc:       nativeDocAt(docs, msCtx.GalaNodeRef),
						Pos:       nativePos(msID.GalaNodeRef),
						DefinedIn: absSibPath,
					}
					a.nativeMethodSpec(methodMeta, msCtx, pkgName, meta.TypeParams)
					meta.Methods[methodName] = methodMeta
				}
			}
			// Type aliases: see extractSiblingFullMetadata.
			if aliasTarget := nativeTypeAliasTarget(ctx); aliasTarget != "" {
				underlyingType := a.resolveTypeWithParams(aliasTarget, pkgName, meta.TypeParams)
				if !underlyingType.IsNil() {
					if richAST.TypeAliases == nil {
						richAST.TypeAliases = make(map[string]transpiler.Type)
					}
					richAST.TypeAliases[typeName] = underlyingType
				}
			}
			richAST.Types[fullTypeName] = meta

		case native.GalaKIND_structShorthandDeclaration:
			ctx := native.StructShorthandDeclarationNode{GalaNodeRef: decl}
			id := ctx.Identifier()
			typeName := id.GetText()
			fullTypeName := qualifiedName(pkgName, typeName)
			if existing, ok := richAST.Types[fullTypeName]; ok && len(existing.FieldNames) > 0 {
				if existing.DefinedIn != "" && !a.paths.sameFile(existing.DefinedIn, absSibPath) {
					return siblingTypeRedefinedError(typeName, pkgName, existing, sibDisplay,
						nativePos(id.GalaNodeRef), decls.mainFile)
				}
				if existing.DefinedIn != "" {
					continue
				}
			}
			existingMethods := make(map[string]*transpiler.MethodMetadata)
			if existing, ok := richAST.Types[fullTypeName]; ok {
				for k, v := range existing.Methods {
					existingMethods[k] = v
				}
			}
			meta := &transpiler.TypeMetadata{
				Name:    typeName,
				Package: pkgName,
				Doc:     nativeDocAt(docs, decl),
				Pos:     nativePos(id.GalaNodeRef),
				Methods: existingMethods,
				Fields:  make(map[string]transpiler.Type),
			}
			// Preserve DefinedIn from main analysis if already set
			if ex, ok := richAST.Types[fullTypeName]; ok && ex.DefinedIn != "" {
				meta.DefinedIn = ex.DefinedIn
			}
			if meta.DefinedIn == "" {
				meta.DefinedIn = absSibPath
			}
			meta.TypeParams, meta.TypeParamConstraints = nativeTypeParamsOf(ctx.TypeParameters())
			for _, pctx := range nativeParams(ctx.Parameters()) {
				fieldID := pctx.Identifier()
				fieldName := fieldID.GetText()
				decls.recordShorthandField(meta, fieldName, a.resolveTypeWithParams(galaparser.NativeTypeText(pctx.Type_()), pkgName, meta.TypeParams), pctx.VAL().Ok(), pctx.VAR().Ok())
				if meta.FieldPositions == nil {
					meta.FieldPositions = make(map[string]transpiler.SourcePos)
				}
				meta.FieldPositions[fieldName] = nativePos(fieldID.GalaNodeRef)
				nativeRecordFieldDefault(meta, fieldName, pctx)
			}
			meta.IsShorthand = true
			richAST.Types[fullTypeName] = meta
		}
	}

	// 2. Collect sealed types
	for decl := range topLevelDecls(nf.root) {
		if decl.Kind() != native.GalaKIND_sealedTypeDeclaration {
			continue
		}
		ctx := native.SealedTypeDeclarationNode{GalaNodeRef: decl}
		id := ctx.Identifier()
		typeName := id.GetText()
		fullTypeName := qualifiedName(pkgName, typeName)
		// Redefinition: see extractSiblingFullMetadata.
		if existing, ok := richAST.Types[fullTypeName]; ok && existing.IsSealed {
			if existing.DefinedIn != "" && !a.paths.sameFile(existing.DefinedIn, absSibPath) {
				return siblingTypeRedefinedError(typeName, pkgName, existing, sibDisplay,
					nativePos(id.GalaNodeRef), decls.mainFile)
			}
			if existing.DefinedIn != "" {
				continue
			}
		}
		if err := a.registerSealedType(nativeSealedDeclOf(ctx, docs), pkgName, richAST); err != nil {
			return err
		}
		markSiblingSealed(richAST, pkgName, fullTypeName, absSibPath)
	}

	// 2.5 Collect opaque types
	for decl := range topLevelDecls(nf.root) {
		if decl.Kind() != native.GalaKIND_opaqueTypeDeclaration {
			continue
		}
		octx := native.OpaqueTypeDeclarationNode{GalaNodeRef: decl}
		id := octx.Identifier()
		typeName := id.GetText()
		fullTypeName := qualifiedName(pkgName, typeName)
		if existing, ok := richAST.Types[fullTypeName]; ok && hasTypeDefinition(existing) {
			if existing.DefinedIn != "" && !a.paths.sameFile(existing.DefinedIn, absSibPath) {
				return siblingTypeRedefinedError(typeName, pkgName, existing, sibDisplay,
					nativePos(id.GalaNodeRef), decls.mainFile)
			}
			if existing.DefinedIn != "" {
				continue
			}
		}
		a.registerOpaqueType(nativeOpaqueDeclOf(octx, docs), pkgName, absSibPath, richAST)
	}

	// 3. Collect methods and functions
	for decl := range topLevelDecls(nf.root) {
		if decl.Kind() != native.GalaKIND_functionDeclaration {
			continue
		}
		ctx := native.FunctionDeclarationNode{GalaNodeRef: decl}
		id := ctx.Identifier()
		sig := ctx.Signature()
		if recvCtx := ctx.Receiver(); recvCtx.Ok() {
			recvType := recvCtx.Type_()
			baseType := nativeBaseTypeName(recvType)
			if baseType == "" {
				continue
			}
			methodName := id.GetText()
			fullBaseType := baseType
			if !strings.Contains(baseType, ".") {
				fullBaseType = qualifiedName(pkgName, baseType)
			}

			sibPos := nativePos(id.GalaNodeRef)
			// A method the analyzed file already declares on the same type:
			// see extractSiblingFullMetadata.
			prev, dup := decls.recordMethod(fullBaseType+"."+methodName, absSibPath, sibPos)
			if dup && a.paths.sameFile(prev.file, decls.mainFile) {
				return methodRedefinedError(methodName, baseType, pkgName,
					prev.pos.Line, prev.pos.Column,
					sibDisplay, decls.mainFile, sibPos)
			}

			pointer, ptr := nativeReceiverPointers(recvType)
			methodMeta := &transpiler.MethodMetadata{
				Name:            methodName,
				Package:         pkgName,
				Doc:             nativeDocAt(docs, decl),
				Pos:             sibPos,
				ReceiverName:    recvCtx.Identifier().GetText(),
				PointerReceiver: pointer,
				PtrReceiver:     ptr,
				DefinedIn:       absSibPath,
			}
			methodMeta.TypeParams, methodMeta.TypeParamConstraints = a.nativeDeclaredTypeParams(ctx.TypeParameters(), pkgName)

			var typeTypeParams []string
			if typeMeta, ok := richAST.Types[fullBaseType]; ok {
				typeTypeParams = typeMeta.TypeParams
			}
			recvNames := nativeReceiverTypeArgNames(recvType)
			allTypeParams := methodTypeParamScope(recvNames, typeTypeParams, methodMeta.TypeParams)

			methodMeta.ReturnType, methodMeta.GoResults = a.nativeSignatureResult(sig, pkgName, allTypeParams)
			// IsGeneric: see extractSiblingFullMetadata.
			if a.nativeCausesInstantiationCycle(recvType, sig) {
				methodMeta.IsGeneric = true
			}
			for i, paramCtx := range nativeParams(sig.Parameters()) {
				if typ := paramCtx.Type_(); typ.Ok() {
					methodMeta.ParamTypes = append(methodMeta.ParamTypes, a.resolveTypeWithParams(galaparser.NativeTypeText(typ), pkgName, allTypeParams))
					methodMeta.Variadic = paramCtx.ELLIPSIS().Ok()
				} else {
					methodMeta.ParamTypes = append(methodMeta.ParamTypes, transpiler.NilType{})
				}
				methodMeta.ParamNames = append(methodMeta.ParamNames, nativeParamName(paramCtx))
				nativeRecordParamDefault(&methodMeta.DefaultExprs, i, paramCtx)
			}
			decls.recordReceiver(methodMeta, recvNames, fullBaseType)

			if typeMeta, ok := richAST.Types[fullBaseType]; ok {
				if _, exists := typeMeta.Methods[methodName]; !exists {
					typeMeta.Methods[methodName] = methodMeta
				} else if !typeMeta.Methods[methodName].IsGeneric && methodMeta.IsGeneric {
					// IsGeneric is append-only: see extractSiblingFullMetadata.
					typeMeta.Methods[methodName].IsGeneric = true
				}
			} else {
				richAST.Types[fullBaseType] = &transpiler.TypeMetadata{
					Name:    baseType,
					Package: pkgName,
					Methods: map[string]*transpiler.MethodMetadata{methodName: methodMeta},
					Fields:  make(map[string]transpiler.Type),
				}
			}
			continue
		}

		funcName := id.GetText()
		fullFuncName := qualifiedName(pkgName, funcName)
		sibPos := nativePos(id.GalaNodeRef)
		// A top-level function the analyzed file already declares: see
		// extractSiblingFullMetadata.
		prev, dup := decls.recordFunc(fullFuncName, absSibPath, sibPos)
		if dup && a.paths.sameFile(prev.file, decls.mainFile) {
			return funcRedeclaredError(funcName, pkgName,
				prev.pos.Line, prev.pos.Column,
				sibDisplay, decls.mainFile, sibPos)
		}
		if _, ok := richAST.Functions[fullFuncName]; ok {
			continue
		}
		funcMeta := &transpiler.FunctionMetadata{
			Name:      funcName,
			Package:   pkgName,
			Doc:       nativeDocAt(docs, decl),
			Pos:       sibPos,
			DefinedIn: absSibPath,
		}
		funcMeta.TypeParams, funcMeta.TypeParamConstraints = a.nativeDeclaredTypeParams(ctx.TypeParameters(), pkgName)
		funcMeta.ReturnType, funcMeta.GoResults = a.nativeSignatureResult(sig, pkgName, funcMeta.TypeParams)
		for i, paramCtx := range nativeParams(sig.Parameters()) {
			if typ := paramCtx.Type_(); typ.Ok() {
				funcMeta.ParamTypes = append(funcMeta.ParamTypes, a.resolveTypeWithParams(galaparser.NativeTypeText(typ), pkgName, funcMeta.TypeParams))
				funcMeta.Variadic = paramCtx.ELLIPSIS().Ok()
			} else {
				funcMeta.ParamTypes = append(funcMeta.ParamTypes, transpiler.NilType{})
			}
			funcMeta.ParamNames = append(funcMeta.ParamNames, nativeParamName(paramCtx))
			nativeRecordParamDefault(&funcMeta.DefaultExprs, i, paramCtx)
		}
		richAST.Functions[fullFuncName] = funcMeta
	}

	// The sibling's package-level bindings: see extractSiblingFullMetadata.
	a.extractPackageValsNative(nf, pkgName, richAST, docs, absSibPath)
	return nil
}
