package analyzer

import (
	"errors"
	"fmt"
	"iter"
	"strings"
	"unicode/utf8"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// The collection of a file's own declarations on galop's native tree
// (internal/parser/native). analyze reads every file on its native tree,
// from its first read on (file_checks_native.go): the compiled file, an LSP
// buffer (an error-recovered one included, see Parsed.RecoveredNative), a
// sibling and a file of an imported package alike (siblings_native.go). The
// undefined-symbol check reads that tree too (undefined_symbol_native.go),
// and the initializers of package-level bindings are inferred on it
// (infer_native.go).
//
// Children are read through the rule handles and dispatched on Kind(), never
// through Typed(), which builds a handle per node.

// nativeFile is a file's native tree, with its top-level declarations by
// kind; or the zero nativeFile: no tree (see ok).
type nativeFile struct {
	root native.SourceFileNode
	// The top-level declarations, in source order, bucketed as the
	// collection reads them: the type and struct shorthand declarations
	// together (collectFileTypesNative takes them in one pass), the sealed types,
	// the opaque types, the functions and methods, the embeds, and the val
	// and var declarations together.
	types, sealed, opaque, funcs, embeds, vals []native.GalaNodeRef
}

// nativeFileOf is the native tree of a parse's handle h, converted now if it
// was not yet, with its top-level declarations bucketed in one pass. An error
// says there is none: a nil handle, or the conversion failed (a bug in galop's
// converter), which no reader of the file can work around.
func nativeFileOf(h *galaparser.Native) (nativeFile, error) {
	tree, err := h.Tree()
	if err != nil {
		return nativeFile{}, err
	}
	root := tree.Root()
	if !root.Ok() || root.Kind() != native.GalaKIND_sourceFile {
		return nativeFile{}, errors.New("the native tree is not a source file")
	}
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
	return nf, nil
}

// methodReceiverTypesNative is the base type name (receiverBaseTypeNameNative)
// of the receiver of each method among nf's top-level declarations, in source
// order.
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

// nativePos is the SourcePos of n's first token.
func nativePos(n native.GalaNodeRef) transpiler.SourcePos {
	return nativeTokenPos(n.Start())
}

// nativeTokenPos is where tok starts: its line and its column in code points,
// as ANTLR counts them.
func nativeTokenPos(tok native.GalaToken) transpiler.SourcePos {
	return transpiler.SourcePos{Line: int(tok.Line), Column: int(tok.Column)}
}

// nativeDocAt returns the doc comment attached to the declaration n, keyed by
// its first token (the `func`, `type`, `sealed`, `case` or `val` keyword), or
// "" when the declaration is undocumented or docs is nil.
func nativeDocAt(docs galaparser.Docs, n native.GalaNodeRef) string {
	if docs == nil || !n.Ok() {
		return ""
	}
	return docs[galaparser.NativeDocKeyOf(n)]
}

// nativeSetDoc records the doc comment of the declaration n on a metadata
// entry that may already carry one.
//
// The re-analysis and sibling-merge paths reuse a TypeMetadata that an earlier
// pass — or a cache load — already populated. Those passes may run with a nil
// docs map (a caller that did not thread one), and assigning unconditionally
// would erase documentation that is already correct. An empty result therefore
// leaves the existing value alone; only a real doc comment overwrites.
func nativeSetDoc(dst *string, docs galaparser.Docs, n native.GalaNodeRef) {
	if d := nativeDocAt(docs, n); d != "" {
		*dst = d
	}
}

// nativeTypeParamsOf returns the names of the type parameters tps declares on
// a declaration, in order, and the constraint each is declared with, as
// written (`B Bumper` maps B to "Bumper", `C Iterable[T, C]` maps C to
// "Iterable[T, C]"); both are nil when it declares none. resolveConstraints
// turns the texts into types.
func nativeTypeParamsOf(tps native.TypeParametersNode) ([]string, map[string]string) {
	list := tps.TypeParameterList()
	if !list.Ok() {
		return nil, nil
	}
	var names []string
	var constraints map[string]string
	for n := range galaparser.NativeKids(list.GalaNodeRef, native.GalaKIND_typeParameter) {
		tp := native.TypeParameterNode{GalaNodeRef: n}
		name, constraint := tp.Identifier().GetText(), tp.Type_()
		names = append(names, name)
		if constraint.Ok() {
			if constraints == nil {
				constraints = make(map[string]string)
			}
			constraints[name] = galaparser.NativeTypeText(constraint)
		}
	}
	return names, constraints
}

// resolveConstraints resolves texts, the constraints of the type parameters
// names of a declaration of package pkgName, where they are declared — the
// package's own type, an import's, a dot-import's or std's — so a user in
// another package finds the same interface. A constraint's type arguments
// may name the list's own parameters (`C Iterable[T, C]`), which stay type
// parameters; `comparable` stays the predeclared constraint.
func (a *galaAnalyzer) resolveConstraints(names []string, texts map[string]string, pkgName string) map[string]transpiler.Type {
	if texts == nil {
		return nil
	}
	out := make(map[string]transpiler.Type, len(texts))
	for tp, text := range texts {
		if text == "comparable" {
			out[tp] = transpiler.BasicType{Name: text}
			continue
		}
		out[tp] = a.resolveTypeWithParams(text, pkgName, names)
	}
	return out
}

// nativeDeclaredTypeParams is nativeTypeParamsOf with each constraint
// resolved in package pkgName (resolveConstraints).
func (a *galaAnalyzer) nativeDeclaredTypeParams(tps native.TypeParametersNode, pkgName string) ([]string, map[string]transpiler.Type) {
	names, texts := nativeTypeParamsOf(tps)
	return names, a.resolveConstraints(names, texts, pkgName)
}

// nativeBaseTypeName is the name of the type t is built on, as written: its
// qualified identifier (`std.Option`, `Option`), looked through pointers
// (`*T`, `Ptr[T]`) and other nested types, and `[]` followed by the element's
// for a slice. It is "" when t names no type.
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

// nativeReceiverTypeArgNames lists the type arguments a receiver type t
// spells, as written and in order: [U] for `Box[U]` and `*Box[U]`, nil for a
// non-generic receiver.
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

// nativeReceiverPointers reports whether a method receiver of type t is
// declared as a pointer, `*T` (`func (c *Counter) Bump()`) or `Ptr[T]`, and
// whether it is declared as the non-null `Ptr[T]` (`func (c Ptr[Counter])
// Bump()`).
func nativeReceiverPointers(t native.TypeNode) (pointer, ptr bool) {
	if galaparser.NativePtrElem(t).Ok() {
		return true, true
	}
	_, pointer = galaparser.NativePointerElem(t)
	return pointer, false
}

// nativeSignatureResult resolves a declared signature's result: its result
// type, or for a Go result list (`(int, error)`) the GALA value a call of it
// is (see transpiler.GoResultValueOf) together with the Go result types. A
// signature with no result is VoidType, with no Go result types.
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

// nativeSignatureParams are a signature's parameters as collectFileFuncsNative
// and collectFileTypesNative record them, from its parameters node: each one's
// type (NilType for one written without), whether the signature is variadic,
// each one's name ("" for one without), each default by parameter index
// (nativeDefaultExpr), and the source span of each default (nativeSpanOf).
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

// nativeCausesInstantiationCycle checks if a method return type would cause a
// Go generics instantiation cycle. This happens when:
//   - The receiver is a generic type (e.g., MyList[T])
//   - The return type is the same base type (e.g., MyList)
//   - But with different type arguments (e.g., MyList[Pair[T, int]])
//
// Go's compiler detects this as a potential infinite instantiation chain.
// The return type is the signature's result, or the first of its Go result
// list (what Go returns), and pointers (`*MyList[T]`, `Ptr[MyList[T]]`) are
// looked through on both sides.
//
// The analyzer is the only place the decision is made (here, and in
// markFanOutMethods for a method that names another generic type): the
// result, recorded as MethodMetadata.IsGeneric, is what the transformer reads
// both to lower the declaration in its own package and to call it from any
// package (#814).
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

// nativePointee strips every pointer, `*T` or `Ptr[T]`, from t.
func nativePointee(t native.TypeNode) native.TypeNode {
	for {
		elem, isPtr := galaparser.NativePointerElem(t)
		if !isPtr {
			return t
		}
		t = elem
	}
}

// nativeTypeAliasTarget returns the target of a `type X Y` declaration as
// written, or "" when the declaration is a struct or interface. `typeAlias:
// identifier | type`, and both branches name an alias: `type Millis int64`
// and `type Coord Point` take the identifier branch.
func nativeTypeAliasTarget(typeDecl native.TypeDeclarationNode) string {
	alias := typeDecl.TypeAlias()
	if !alias.Ok() {
		return ""
	}
	return galaparser.NativeAliasText(alias)
}

// nativeRecordFieldDefault stores the source text of a shorthand struct
// field's default expression, p's, on the type metadata.
//
// The shorthand form `struct Cfg(Name string, Tries int = 3)` reuses the
// grammar's `parameter` rule, so `= 3` parses as a `paramDefault` exactly as it
// would on a function parameter. Capturing it here is what lets construction
// sites fill an omitted field with its declared default instead of leaving Go's
// zero value in place, and lets them tell an omitted-but-defaulted field apart
// from an omitted required one.
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

// nativeDefaultExpr is the DefaultExpr of default expression e: its text
// exactly as it is written in the source, whitespace included, and the
// position of its first token.
//
// The text is re-parsed at every call or construction site that omits the
// argument, so it must survive a second lexing. Concatenating token texts
// would drop the hidden-channel whitespace between them, which turns `(a int)
// => a + 1` into `(aint)=>a+1` — a lambda whose one parameter is named `aint`.
// The source text covered by the expression's tokens keeps every space,
// newline and comment as written.
func nativeDefaultExpr(e native.ExpressionNode) transpiler.DefaultExpr {
	return transpiler.DefaultExpr{Text: e.SourceText(), Pos: nativePos(e.GalaNodeRef)}
}

// nativeSpanOf returns the source span covering n, from its start token's
// position to one past its stop token. Columns are 0-based code points, as
// ANTLR's are; endCol is exclusive. When n spans multiple lines the renderer
// clamps the caret to the start line, so only line/startCol matter there.
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

// nativeSealedDeclOf reads n, a sealed type declaration, with the doc
// comments of its file.
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

// nativeOpaqueDeclOf reads n, an opaque type declaration, with the doc
// comments of its file.
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

// recordStructFieldNative records on meta the struct field fctx declares,
// resolved with meta's type parameters, as collectFileTypesNative and the
// sibling pass record one: its type, its order and mutability, where its name is and
// its doc comment.
func (a *galaAnalyzer) recordStructFieldNative(meta *transpiler.TypeMetadata, fctx native.StructFieldNode, pkgName string, docs galaparser.Docs) {
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

// interfaceMethodNative is the metadata of the method msCtx, a method spec
// of the interface meta, declared in definedIn: its signature, resolved with
// meta's type parameters, and its parameters' types only.
func (a *galaAnalyzer) interfaceMethodNative(meta *transpiler.TypeMetadata, msCtx native.MethodSpecNode, pkgName, definedIn string, docs galaparser.Docs) *transpiler.MethodMetadata {
	methodMeta := &transpiler.MethodMetadata{
		Name:       msCtx.Identifier().GetText(),
		Package:    pkgName,
		Doc:        nativeDocAt(docs, msCtx.GalaNodeRef),
		DefinedIn:  definedIn,
		OfTypeDecl: true,
	}
	sig := msCtx.Signature()
	methodMeta.ReturnType, methodMeta.GoResults = a.nativeSignatureResult(sig, pkgName, meta.TypeParams)
	params := a.nativeParamsOf(sig.Parameters(), pkgName, meta.TypeParams)
	methodMeta.ParamTypes, methodMeta.Variadic = params.types, params.variadic
	return methodMeta
}

// recordShorthandFieldNative records on meta the field pctx, a parameter of a
// struct shorthand, declares, through decls (see recordShorthandField), as
// collectFileTypesNative and the sibling pass record one: its type, resolved with
// meta's type parameters, where its name is and its default.
func (a *galaAnalyzer) recordShorthandFieldNative(meta *transpiler.TypeMetadata, pctx native.ParameterNode, pkgName string, decls *packageDecls) {
	fieldID := pctx.Identifier()
	fieldName := fieldID.GetText()
	fieldType := ""
	if typ := pctx.Type_(); typ.Ok() {
		fieldType = galaparser.NativeTypeText(typ)
	}
	decls.recordShorthandField(meta, fieldName, a.resolveTypeWithParams(fieldType, pkgName, meta.TypeParams), pctx.VAL().Ok(), pctx.VAR().Ok())
	if meta.FieldPositions == nil {
		meta.FieldPositions = make(map[string]transpiler.SourcePos)
	}
	meta.FieldPositions[fieldName] = nativePos(fieldID.GalaNodeRef)
	nativeRecordFieldDefault(meta, fieldName, pctx)
}

// collectFileTypesNative records the struct, shorthand-struct and interface
// types nf declares (analyze's section 1).
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

			if existing, ok := richAST.Types[fullTypeName]; ok && existing.Package == pkgName {
				// Error if type is being redefined from a DIFFERENT file.
				// Skip if DefinedIn is empty (cache) or same file (re-analysis from analyzePackage).
				if existing.DefinedIn != "" && hasTypeDefinition(existing) && !(existing.DefinedIn != filePath && a.paths.sameFile(existing.DefinedIn, absFilePath)) {
					pos := nativePos(decl)
					return typeRedefinedError(typeName, pkgName, pos.Line, pos.Column,
						existing.DefinedIn, filePath, existing.Pos)
				}
			}
			meta := a.startTypeEntry(richAST, fullTypeName, typeName, pkgName, nativePos(id.GalaNodeRef), absFilePath)
			nativeSetDoc(&meta.Doc, docs, decl)
			meta.TypeParams, meta.TypeParamConstraints = a.nativeDeclaredTypeParams(ctx.TypeParameters(), pkgName)

			if structType := ctx.StructType(); structType.Ok() {
				for fNode := range galaparser.NativeKids(structType.GalaNodeRef, native.GalaKIND_structField) {
					fctx := native.StructFieldNode{GalaNodeRef: fNode}
					fieldID := fctx.Identifier()
					fieldName := fieldID.GetText()
					// Reject duplicate field names within a single struct
					// declaration. Without this, the second declaration
					// silently overwrote the first in the Fields map and
					// duplicated the name in FieldNames, producing invalid
					// Go output. This guard is scoped to one parse of one
					// struct (Fields was cleared for this declaration above),
					// so it does not need an existing.DefinedIn check.
					if _, exists := meta.Fields[fieldName]; exists {
						pos := nativePos(fieldID.GalaNodeRef)
						return galaerr.NewCodedSemanticError(
							galaerr.CodeStructFieldRedeclared,
							pos.Line, pos.Column,
							fmt.Sprintf("field %q already declared in struct %q", fieldName, typeName),
							"rename or remove the duplicate field",
						)
					}
					a.recordStructFieldNative(meta, fctx, pkgName, docs)
				}
				meta.DefinedIn = filePath
			}

			// Extract interface method signatures as type methods
			if ifaceType := ctx.InterfaceType(); ifaceType.Ok() {
				meta.IsInterface = true
				// seenSpecs is a per-declaration dedup so re-analysis (which
				// preserves the Methods map across calls) does not falsely
				// trigger the redeclaration check on already-registered
				// specs. Within one walk of the method specs we still
				// catch genuine duplicates.
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
					if tps, cons := nativeTypeParamsOf(msCtx.TypeParameters()); len(tps) > 0 {
						pos := nativePos(msID.GalaNodeRef)
						return genericInterfaceMethodError(pos.Line, pos.Column, meta, methodName, tps, cons)
					}
					meta.Methods[methodName] = a.interfaceMethodNative(meta, msCtx, pkgName, filePath, docs)
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

			pos := nativePos(id.GalaNodeRef)
			if existing, ok := richAST.Types[fullTypeName]; ok && existing.Package == pkgName {
				if existing.DefinedIn != "" && hasTypeDefinition(existing) && !(existing.DefinedIn != filePath && a.paths.sameFile(existing.DefinedIn, absFilePath)) {
					declPos := nativePos(decl)
					return typeRedefinedError(typeName, pkgName, declPos.Line, declPos.Column,
						existing.DefinedIn, filePath, existing.Pos)
				}
			}
			meta := a.startTypeEntry(richAST, fullTypeName, typeName, pkgName, pos, absFilePath)
			nativeSetDoc(&meta.Doc, docs, decl)
			meta.TypeParams, meta.TypeParamConstraints = a.nativeDeclaredTypeParams(ctx.TypeParameters(), pkgName)

			if params := ctx.Parameters(); params.Ok() {
				for _, pctx := range nativeParams(params) {
					fieldID := pctx.Identifier()
					fieldName := fieldID.GetText()
					// Reject duplicate field names within a shorthand
					// struct declaration, as for a struct type above.
					if _, exists := meta.Fields[fieldName]; exists {
						fieldPos := nativePos(fieldID.GalaNodeRef)
						return galaerr.NewCodedSemanticError(
							galaerr.CodeStructFieldRedeclared,
							fieldPos.Line, fieldPos.Column,
							fmt.Sprintf("field %q already declared in struct %q", fieldName, typeName),
							"rename or remove the duplicate field",
						)
					}
					a.recordShorthandFieldNative(meta, pctx, pkgName, decls)
				}
				meta.IsShorthand = true
				meta.DefinedIn = filePath
			}
		}
	}
	return nil
}

// collectFileFuncsNative records the sealed and opaque types, methods and
// top-level functions nf declares (analyze's sections 1.5 to 2), and queues on
// fd the functions and sealed cases whose parameter defaults analyze checks
// once the siblings are in.
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
		if err := a.registerSealedType(nativeSealedDeclOf(ctx, docs), pkgName, absFilePath, richAST); err != nil {
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
				DefinedIn:       filePath,
			}
			var constraintTexts map[string]string
			methodMeta.TypeParams, constraintTexts = nativeTypeParamsOf(ctx.TypeParameters())

			// The receiver type's parameters, for resolving the signature:
			// for `func (s Some[T]) Unapply(o Option[T])`, T is a type
			// parameter.
			var typeTypeParams []string
			if typeMeta, ok := richAST.Types[fullBaseType]; ok {
				typeTypeParams = typeMeta.TypeParams
			}
			recvNames := nativeReceiverTypeArgNames(recvType)
			allTypeParams := methodTypeParamScope(recvNames, typeTypeParams, methodMeta.TypeParams)
			// A constraint may name the receiver's type parameters too.
			methodMeta.TypeParamConstraints = a.resolveConstraints(allTypeParams, constraintTexts, pkgName)

			methodMeta.ReturnType, methodMeta.GoResults = a.nativeSignatureResult(sig, pkgName, allTypeParams)
			// A Go generics instantiation cycle: a receiver Container[T]
			// returning Container[SomeType[T, ...]] is an infinite
			// instantiation to Go.
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
					// Merge IsGeneric: preserve a pre-populated flag (e.g. from a
					// sibling-metadata pass) without clobbering a fresh true that
					// nativeCausesInstantiationCycle just set. Either source may
					// legitimately flag the method as needing function-form
					// generation; once flagged, never flip back to false (Go
					// generation would emit method syntax against a function
					// definition).
					methodMeta.IsGeneric = methodMeta.IsGeneric || existing.IsGeneric
				}
				typeMeta.Methods[methodName] = methodMeta
			} else {
				// A placeholder until the sibling pass: see collectFileFuncs.
				richAST.Types[fullBaseType] = &transpiler.TypeMetadata{
					Name:    baseType,
					Package: pkgName,
					Methods: map[string]*transpiler.MethodMetadata{methodName: methodMeta},
					Fields:  make(map[string]transpiler.Type),
				}
			}
			// Record the declaration so the sibling pass can detect a
			// second definition of this method in another file of the
			// package. This also catches a duplicate within this file
			// when the receiver type is declared in a sibling: the
			// check above sees only a placeholder entry whose
			// DefinedIn is still empty, and would let it through.
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
		// package. The sibling-metadata pass may have already registered
		// the function from another file; if it lives in a different
		// file, that's a true redeclaration (mirrors Go's "redeclared
		// in this block"). Skip when the existing entry is empty
		// (placeholder / cache) or refers to this same file
		// (re-analysis of the same source). recordFunc records the
		// declaration so the sibling pass can detect a second
		// definition in another file of the package.
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

// collectFileEmbedsNative records the embed declarations nf makes (analyze's
// section 2.75).
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

// fileTypeAliasesNative maps each alias nf declares (`type Millis int64`) to
// the type it names.
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

// extractPackageValsNative records package-level `val`/`var` declarations
// from nf into richAST.PackageVals. The transformer pre-registers these so
// that a reference to a package-level `val` in another file of the same
// package is unwrapped from its `std.Immutable[T]` wrapper at the use site —
// exactly as a same-file reference already is. Without this, the cross-file
// reference emits the raw wrapper where a plain `T` is expected and `go
// build` rejects it.
//
// A package's exported records reach its importers as RichAST.ImportedVals
// (see RichAST.AddImportedVals), which is how an importer unwraps `pkg.Name`.
//
// The element type is recorded when it can be determined cheaply (an explicit
// annotation, or an initializer shape inferPackageValInitTypeNative
// understands); otherwise it is left as NilType. The unwrap itself only needs
// the val/var classification, so an unknown type still produces correct code
// — it merely yields weaker downstream type inference for that identifier.
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
			// A tuple destructuring (`val (a, b) = pair`) records its names
			// with no type: each is one component of the initializer, which
			// the cross-file unwrap does not need to infer, since it needs
			// only the val/var classification.
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

		// The declaration's doc, shared by every name it binds (`val a, b =
		// 1, 2`), the way Go documents a grouped declaration; the position
		// is each identifier's own. Doc, position and file are written
		// together with the type, as one declaration's description: merging
		// them field-wise across passes would pair one file's prose with
		// another's location. Its first token is its topLevelDeclaration's.
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
					a.onInferInit(exprs[i], valType)
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
