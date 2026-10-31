package analyzer

import (
	"fmt"
	"iter"
	"strings"
	"unicode/utf8"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// The collection of the compiled file's own declarations on galop's native
// tree (internal/parser/native): each function here is the twin of the one
// it is named after, which reads ANTLR's tree, and records the same metadata,
// in the same order, with the same diagnostics. TestCollectionsAgree and
// TestCollectionDiagnosticsAgree hold each pair together.
//
// analyze collects a file on its native tree only when the undefined-symbol
// check reads that tree too (see checksUndefined there, and
// undefined_symbol_native.go): converting costs several times what
// collecting on it saves. Every other
// file is collected on ANTLR's tree: a parse by ANTLR's parser or one with
// syntax errors (no handle, see Parsed.CleanNative), an LSP buffer or a tree
// a caller handed in bare, a sibling or an imported file (the parsed-file
// cache keeps no handle), and a file whose check stands down. A file is
// collected on one tree or the other, never on a mix, and the native
// collection reads no ANTLR context: the initializers of package-level
// bindings are inferred on the native tree too (infer_native.go).
//
// Children are read through the rule handles and dispatched on Kind(), never
// through Typed(), which builds a handle per node.

// collectTrace is what analyze tells galaAnalyzer.onCollect about a file:
// whether the undefined-symbol check runs on it, whether its parse came with
// a native-tree handle, and whether its declarations are collected on the
// native tree, which they are exactly when both hold and the tree converts.
type collectTrace struct {
	filePath        string
	checksUndefined bool
	hasHandle       bool
	native          bool
}

// nativeFile is a file's native tree, with its top-level declarations by
// kind; or the zero nativeFile: no tree (see ok).
type nativeFile struct {
	root native.SourceFileNode
	// The top-level declarations, in source order, bucketed as the
	// collection reads them: the type and struct shorthand declarations
	// together (collectFileTypes takes them in one pass), the sealed types,
	// the opaque types, the functions and methods, the embeds, and the val
	// and var declarations together.
	types, sealed, opaque, funcs, embeds, vals []native.GalaNodeRef
}

// nativeFileOf is the native tree of a parse's handle h, converted now if it
// was not yet, with its top-level declarations bucketed in one pass; or no
// tree when there is none (a nil handle, the conversion failed, or the tree
// is not well formed: see Native.WellFormed), and the file is collected, and
// checked, on ANTLR's tree. Neither the collection nor the undefined-symbol
// check crosses from the native tree to ANTLR's.
func nativeFileOf(h *galaparser.Native) nativeFile {
	if !h.WellFormed() {
		return nativeFile{}
	}
	tree, _ := h.Tree()
	root := tree.Root()
	nf := nativeFile{root: native.SourceFileNode{GalaNodeRef: root}}
	for decl := range nativeTopLevelDecls(root) {
		switch decl.Kind() {
		case native.GalaKIND_typeDeclaration, native.GalaKIND_structShorthandDeclaration:
			nf.types = append(nf.types, decl)
		case native.GalaKIND_sealedTypeDeclaration:
			nf.sealed = append(nf.sealed, decl)
		case native.GalaKIND_opaqueTypeDeclaration:
			nf.opaque = append(nf.opaque, decl)
		case native.GalaKIND_functionDeclaration:
			nf.funcs = append(nf.funcs, decl)
		case native.GalaKIND_embedDeclaration:
			nf.embeds = append(nf.embeds, decl)
		case native.GalaKIND_valDeclaration, native.GalaKIND_varDeclaration:
			nf.vals = append(nf.vals, decl)
		}
	}
	return nf
}

// methodReceiverTypesNative is methodReceiverTypes of nf.
func methodReceiverTypesNative(nf nativeFile) iter.Seq[string] {
	return func(yield func(string) bool) {
		for _, fn := range nf.funcs {
			typ := native.FunctionDeclarationNode{GalaNodeRef: fn}.Receiver().Type_()
			if typ.Ok() && !yield(receiverBaseTypeNameNative(typ)) {
				return
			}
		}
	}
}

// nativeTopLevelDecls is the top-level declarations of root, a source file,
// in source order: the node each topLevelDeclaration holds.
func nativeTopLevelDecls(root native.GalaNodeRef) iter.Seq[native.GalaNodeRef] {
	return func(yield func(native.GalaNodeRef) bool) {
		for td := range galaparser.NativeKids(root, native.GalaKIND_topLevelDeclaration) {
			if td.ChildCount() == 0 {
				continue
			}
			if decl := td.Child(0).Node(); decl.Ok() && !yield(decl) {
				return
			}
		}
	}
}

// ok reports whether f is a native tree, not the zero nativeFile.
func (f nativeFile) ok() bool { return f.root.Ok() }

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
	for n := range galaparser.NativeKids(list.GalaNodeRef, native.GalaKIND_typeParameter) {
		tp := native.TypeParameterNode{GalaNodeRef: n}
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
		// A slice, `[]T`, is the one type whose text starts with `[`.
		if galaparser.NativeStartText(t.GalaNodeRef) == "[" {
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
		elem, ok := galaparser.OnlyNativeKid(t.GalaNodeRef, native.GalaKIND_type)
		if !ok {
			return nil
		}
		t = native.TypeNode{GalaNodeRef: elem}
	}
	list := t.TypeArguments().TypeList()
	if !list.Ok() {
		return nil
	}
	var names []string
	for arg := range galaparser.NativeKids(list.GalaNodeRef, native.GalaKIND_type) {
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
	for typ := range galaparser.NativeKids(list.GalaNodeRef, native.GalaKIND_type) {
		results = append(results, a.resolveTypeWithParams(galaparser.NativeTypeText(native.TypeNode{GalaNodeRef: typ}), pkgName, typeParams))
	}
	value, _ := transpiler.GoResultValueOf(results)
	return value.Type, results
}

// nativeSignatureParams are a signature's parameters as collectFileFuncs and
// collectFileTypes record them, from its parameters node: each one's type
// (NilType for one written without), whether the signature is variadic,
// each one's name ("" for one without), each default by parameter index
// (recordParamDefault), and the source span of each default (spanOfCtx).
// Types are resolved with typeParams in scope.
type nativeSignatureParams struct {
	types    []transpiler.Type
	variadic bool
	names    []string
	defaults map[int]transpiler.DefaultExpr
	spans    map[int]defaultExprSpan
}

// nativeParamsOf reads the parameters of params (see nativeSignatureParams).
func (a *galaAnalyzer) nativeParamsOf(params native.ParametersNode, pkgName string, typeParams []string) nativeSignatureParams {
	var sp nativeSignatureParams
	for i, p := range nativeParams(params) {
		if typ := p.Type_(); typ.Ok() {
			sp.types = append(sp.types, a.resolveTypeWithParams(galaparser.NativeTypeText(typ), pkgName, typeParams))
			sp.variadic = p.ELLIPSIS().Ok()
		} else {
			sp.types = append(sp.types, transpiler.NilType{})
		}
		name := ""
		if id := p.Identifier(); id.Ok() {
			name = id.GetText()
		}
		sp.names = append(sp.names, name)
		if pd := p.ParamDefault(); pd.Ok() {
			expr := pd.Expression()
			if sp.defaults == nil {
				sp.defaults = make(map[int]transpiler.DefaultExpr)
				sp.spans = make(map[int]defaultExprSpan)
			}
			sp.defaults[i] = nativeDefaultExpr(expr)
			sp.spans[i] = nativeSpanOf(expr.GalaNodeRef)
		}
	}
	return sp
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
	pd := p.ParamDefault()
	if !pd.Ok() {
		return
	}
	if meta.FieldDefaults == nil {
		meta.FieldDefaults = make(map[string]transpiler.DefaultExpr)
	}
	meta.FieldDefaults[fieldName] = nativeDefaultExpr(pd.Expression())
}

// nativeDefaultExpr is paramDefaultSource's DefaultExpr of default
// expression e: its source text, as transpiler.SourceText gives a context's,
// and its position.
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
	for scNode := range galaparser.NativeKids(n.GalaNodeRef, native.GalaKIND_sealedCase) {
		sc := native.SealedCaseNode{GalaNodeRef: scNode}
		caseID := sc.Identifier()
		c := sealedCase{
			name:      caseID.GetText(),
			doc:       nativeDocAt(docs, scNode),
			pos:       nativePos(caseID.GalaNodeRef),
			isDefault: sc.DEFAULT().Ok(),
		}
		for fcNode := range galaparser.NativeKids(sc.SealedCaseFieldList().GalaNodeRef, native.GalaKIND_sealedCaseField) {
			fc := native.SealedCaseFieldNode{GalaNodeRef: fcNode}
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
		for p := range galaparser.NativeKids(params.ParameterList().GalaNodeRef, native.GalaKIND_parameter) {
			if !yield(i, native.ParameterNode{GalaNodeRef: p}) {
				return
			}
			i++
		}
	}
}

// collectFileTypesNative is collectFileTypes.
func (a *galaAnalyzer) collectFileTypesNative(nf nativeFile, fd *fileDecls) error {
	richAST, pkgName, filePath, absFilePath, docs, decls := fd.richAST, fd.pkgName, fd.filePath, fd.absFilePath, fd.docs, fd.decls
	for _, decl := range nf.types {
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

			// Assigned, not appended: a reused entry already holds them.
			meta.TypeParams, meta.TypeParamConstraints = nativeTypeParamsOf(ctx.TypeParameters())

			if structType := ctx.StructType(); structType.Ok() {
				for fNode := range galaparser.NativeKids(structType.GalaNodeRef, native.GalaKIND_structField) {
					fctx := native.StructFieldNode{GalaNodeRef: fNode}
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
					addFieldDoc(meta, fieldName, nativeDocAt(docs, fNode))
				}
				meta.DefinedIn = filePath
			}

			// Extract interface method signatures as type methods
			if ifaceType := ctx.InterfaceType(); ifaceType.Ok() {
				meta.IsInterface = true
				// seenSpecs: see collectFileTypes.
				seenSpecs := make(map[string]bool)
				for msNode := range galaparser.NativeKids(ifaceType.GalaNodeRef, native.GalaKIND_methodSpec) {
					msCtx := native.MethodSpecNode{GalaNodeRef: msNode}
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
						Doc:       nativeDocAt(docs, msNode),
						DefinedIn: filePath,
					}
					methodMeta.TypeParams, methodMeta.TypeParamConstraints = a.nativeDeclaredTypeParams(msCtx.TypeParameters(), pkgName)
					var allTypeParams []string
					allTypeParams = append(allTypeParams, meta.TypeParams...)
					allTypeParams = append(allTypeParams, methodMeta.TypeParams...)
					sig := msCtx.Signature()
					methodMeta.ReturnType, methodMeta.GoResults = a.nativeSignatureResult(sig, pkgName, allTypeParams)
					// An interface method records its parameters' types only.
					params := a.nativeParamsOf(sig.Parameters(), pkgName, allTypeParams)
					methodMeta.ParamTypes, methodMeta.Variadic = params.types, params.variadic
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

			// Assigned, not appended: a reused entry already holds them.
			meta.TypeParams, meta.TypeParamConstraints = nativeTypeParamsOf(ctx.TypeParameters())

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

// collectFileFuncsNative is collectFileFuncs.
func (a *galaAnalyzer) collectFileFuncsNative(nf nativeFile, fd *fileDecls) error {
	richAST, pkgName, filePath, absFilePath, docs, decls := fd.richAST, fd.pkgName, fd.filePath, fd.absFilePath, fd.docs, fd.decls
	// 1.5 Collect sealed types.
	for _, decl := range nf.sealed {
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
	for _, decl := range nf.opaque {
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
	for _, decl := range nf.funcs {
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

			params := a.nativeParamsOf(sig.Parameters(), pkgName, allTypeParams)
			methodMeta.ParamTypes, methodMeta.Variadic, methodMeta.ParamNames, methodMeta.DefaultExprs = params.types, params.variadic, params.names, params.defaults

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
		params := a.nativeParamsOf(sig.Parameters(), pkgName, funcMeta.TypeParams)
		funcMeta.ParamTypes, funcMeta.Variadic, funcMeta.ParamNames, funcMeta.DefaultExprs = params.types, params.variadic, params.names, params.defaults
		// Validate default parameter rules once the sibling files' type
		// declarations are known too (section 2.5).
		if len(funcMeta.DefaultExprs) > 0 {
			fd.pendingDefaultChecks = append(fd.pendingDefaultChecks, pendingDefaultCheck{
				meta: funcMeta, line: declPos.Line, col: declPos.Column, spans: params.spans,
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
	for _, decl := range nf.embeds {
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
	for _, decl := range nf.types {
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

// extractPackageValsNative is extractPackageVals.
func (a *galaAnalyzer) extractPackageValsNative(nf nativeFile, pkgName string, richAST *transpiler.RichAST, docs galaparser.Docs, absFilePath string) {
	for _, decl := range nf.vals {
		var (
			idList   native.IdentifierListNode
			typeCtx  native.TypeNode
			exprList native.ExpressionListNode
			tp       native.TuplePatternNode
		)
		isVal := decl.Kind() == native.GalaKIND_valDeclaration
		if isVal {
			vc := native.ValDeclarationNode{GalaNodeRef: decl}
			idList, typeCtx, exprList, tp = vc.IdentifierList(), vc.Type_(), vc.ExpressionList(), vc.TuplePattern()
		} else {
			vc := native.VarDeclarationNode{GalaNodeRef: decl}
			idList, typeCtx, exprList, tp = vc.IdentifierList(), vc.Type_(), vc.ExpressionList(), vc.TuplePattern()
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
				valType = a.inferPackageValInitTypeNative(exprs[i], pkgName, richAST)
				if a.onInferInit != nil {
					a.onInferInit(exprs[i], pkgName, richAST, valType)
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
