package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// This file contains the non-codegen half of the StructMeta compiler
// intrinsic: call-site detection, auto-injection, metadata registration,
// and a handful of helper primitives shared with codec_typed.go (which
// owns the fully-typed EncodeFields / DecodeFields emission).
//
// The JSON-specific legacy codegen that used to live here (genWriteTo,
// genReadFrom, genWriteToAny, genReadFromAny, genFieldType, genFieldWrite,
// genFieldRead, writeMethodForBasicType, readMethodForBasicType) was
// removed in Phase 4 of the Option-C refactor.  JSON serialisation now
// lives entirely in std/json/codec.gala, built on top of the
// StructMeta[T] interface from std/meta.gala.  The transpiler carries no
// format-specific knowledge.

// structMetaConfig holds the compile-time configuration for a StructMeta[T] intrinsic.
type structMetaConfig struct {
	typeName      string
	typeMetadata  *transpiler.TypeMetadata
	generatedName string
	resolvedName  string
	// rootName, line and col identify the use site (`Codec[T]`, `StructMeta[T]()`)
	// that first asked for this codec. A struct reached only as a nested field
	// inherits them from its parent, so a diagnostic about any field points at
	// the code that requested the codec.
	rootName  string
	line, col int
}

// ---- StructMeta interception ----

// transformStructMetaConstruction handles StructMeta[T]() calls.
// This is the ONLY codec-related compiler intrinsic.
func (t *galaASTTransformer) transformStructMetaConstruction(fun ast.Expr, line, col int) (ast.Expr, error) {
	typeName, err := t.extractTypeArgFromIndex(fun, line, col)
	if err != nil {
		return nil, err
	}

	typeMeta, resolvedName := t.getTypeMetaResolved(typeName)
	if typeMeta == nil {
		return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf("StructMeta[%s]: type %q not found", typeName, typeName))
	}
	if len(typeMeta.FieldNames) == 0 {
		return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf("StructMeta[%s]: type %q has no fields", typeName, typeName))
	}

	genName := "_StructMeta_" + typeName
	if _, exists := t.structMetas[genName]; exists {
		return &ast.CompositeLit{Type: ast.NewIdent(genName)}, nil
	}

	t.structMetas[genName] = &structMetaConfig{
		typeName:      typeName,
		typeMetadata:  typeMeta,
		generatedName: genName,
		resolvedName:  resolvedName,
		rootName:      typeName,
		line:          line,
		col:           col,
	}

	// Register type metadata so the transpiler can resolve method return types.
	t.registerStructMetaTypeMeta(genName, typeName)

	return &ast.CompositeLit{Type: ast.NewIdent(genName)}, nil
}

// ---- code generation ----

// finalizeCodecs generates all StructMeta Go declarations.
//
// Before codegen runs we close over the set of nested struct types
// reachable from the registered top-level metas — every field whose
// (unwrapped) type is itself a struct, including struct elements inside
// Array/List/HashMap, gets its own _StructMeta_X registered so the
// recursive EncodeFields/DecodeFields dispatch in codec_typed.go can
// find it.  The walk is a simple worklist fixpoint.
//
// A field whose type has no encoding fails the build with GALA-E0050. The
// configs are visited in name order so that, when several are broken, the
// error reported is the same on every run.
func (t *galaASTTransformer) finalizeCodecs(file *ast.File) error {
	t.expandNestedStructMetas()
	// Emit in name order: structMetas is a map, and ranging over it directly
	// made the declaration order — and so the generated file — differ from
	// one run to the next for any program with more than one codec'd struct.
	names := make([]string, 0, len(t.structMetas))
	for genName := range t.structMetas {
		names = append(names, genName)
	}
	sort.Strings(names)
	for _, genName := range names {
		decls, err := t.generateStructMetaDecls(t.structMetas[genName])
		if err != nil {
			return err
		}
		file.Decls = append(file.Decls, decls...)
	}
	return nil
}

// expandNestedStructMetas walks every registered StructMeta config's fields
// and registers fresh entries for any nested struct types that we have
// metadata for.  Repeats until the set is closed.
func (t *galaASTTransformer) expandNestedStructMetas() {
	// Seed in name order: a nested struct inherits its use site from whichever
	// parent reaches it first, and that must not depend on map iteration.
	worklist := make([]string, 0, len(t.structMetas))
	for genName := range t.structMetas {
		worklist = append(worklist, genName)
	}
	sort.Strings(worklist)
	for len(worklist) > 0 {
		genName := worklist[0]
		worklist = worklist[1:]
		config, ok := t.structMetas[genName]
		if !ok || config.typeMetadata == nil {
			continue
		}
		for _, fieldName := range config.typeMetadata.FieldNames {
			fieldType := config.typeMetadata.Fields[fieldName]
			added := t.registerNestedStructMetaForType(fieldType, config)
			worklist = append(worklist, added...)
		}
	}
}

// registerNestedStructMetaForType registers a _StructMeta_X for any struct
// type reachable through the given field type's container layers
// (Immutable, Option, Array, List, HashMap value).  Returns the list of
// freshly-registered generated names so the caller can keep walking.
//
// Sealed types are not registered: they have no codec encoding, and leaving
// them out is what makes the field that reaches one fail with GALA-E0050.
func (t *galaASTTransformer) registerNestedStructMetaForType(fieldType transpiler.Type, parent *structMetaConfig) []string {
	var added []string
	t.collectNestedStructTypeNames(fieldType, func(name string) {
		genName := "_StructMeta_" + name
		if _, exists := t.structMetas[genName]; exists {
			return
		}
		typeMeta, resolved := t.getTypeMetaResolved(name)
		if typeMeta == nil || len(typeMeta.FieldNames) == 0 || typeMeta.IsSealed {
			return
		}
		t.structMetas[genName] = &structMetaConfig{
			typeName:      name,
			typeMetadata:  typeMeta,
			generatedName: genName,
			resolvedName:  resolved,
			rootName:      parent.rootName,
			line:          parent.line,
			col:           parent.col,
		}
		t.registerStructMetaTypeMeta(genName, name)
		added = append(added, genName)
	})
	return added
}

// collectNestedStructTypeNames invokes `cb` for every potentially-struct
// named type reachable from `ty` by unwrapping Immutable/Option and
// stepping into Array/List element + HashMap value parameters.
func (t *galaASTTransformer) collectNestedStructTypeNames(ty transpiler.Type, cb func(string)) {
	if ty == nil {
		return
	}
	ty = t.codecUnalias(ty)
	switch kind, params := codecContainer(ty); kind {
	case "Immutable", "Option", "Array", "List":
		t.collectNestedStructTypeNames(params[0], cb)
		return
	case "HashMap":
		t.collectNestedStructTypeNames(params[1], cb)
		return
	}
	if name := codecStructName(ty); name != "" {
		cb(name)
	}
}

// collectionIdent returns a qualified reference to a collection_immutable type.
// Adds the import automatically.
func (t *galaASTTransformer) collectionIdent(name string) ast.Expr {
	if t.importManager.IsDotImported("collection_immutable") {
		t.markDotImportUsed("collection_immutable")
		return ast.NewIdent(name)
	}
	t.importManager.AddTransitive("martianoff/gala/collection_immutable", "collection_immutable")
	return &ast.SelectorExpr{
		X:   ast.NewIdent("collection_immutable"),
		Sel: ast.NewIdent(name),
	}
}

func (t *galaASTTransformer) generateStructMetaDecls(config *structMetaConfig) ([]ast.Decl, error) {
	encode, decode, err := t.genStructMetaMethods(config)
	if err != nil {
		return nil, err
	}
	var decls []ast.Decl
	meta := config.typeMetadata
	genName := config.generatedName

	// type _StructMeta_T struct{}
	decls = append(decls, &ast.GenDecl{
		Tok: token.TYPE,
		Specs: []ast.Spec{
			&ast.TypeSpec{
				Name: ast.NewIdent(genName),
				Type: &ast.StructType{Fields: &ast.FieldList{}},
			},
		},
	})

	decls = append(decls, t.genNumFields(genName, len(meta.FieldNames)))
	decls = append(decls, t.genFieldName(genName, meta.FieldNames))
	// The only typed serialisation methods. EncodeFields / DecodeFields live
	// in codec_typed.go.
	decls = append(decls, encode, decode)

	return decls, nil
}

// --- NumFields() int ---

func (t *galaASTTransformer) genNumFields(genName string, count int) *ast.FuncDecl {
	return &ast.FuncDecl{
		Recv: blankRecv(genName),
		Name: ast.NewIdent("NumFields"),
		Type: &ast.FuncType{
			Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("int")}}},
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{
			&ast.ReturnStmt{Results: []ast.Expr{intLit(count)}},
		}},
	}
}

// --- FieldName(i int) string ---

func (t *galaASTTransformer) genFieldName(genName string, fieldNames []string) *ast.FuncDecl {
	var cases []ast.Stmt
	for i, name := range fieldNames {
		cases = append(cases, &ast.CaseClause{
			List: []ast.Expr{intLit(i)},
			Body: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{stringLit(name)}}},
		})
	}
	cases = append(cases, &ast.CaseClause{
		Body: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{stringLit("")}}},
	})

	return &ast.FuncDecl{
		Recv: blankRecv(genName),
		Name: ast.NewIdent("FieldName"),
		Type: &ast.FuncType{
			Params:  &ast.FieldList{List: []*ast.Field{{Names: idents("i"), Type: ast.NewIdent("int")}}},
			Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("string")}}},
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{
			&ast.SwitchStmt{Tag: ast.NewIdent("i"), Body: &ast.BlockStmt{List: cases}},
		}},
	}
}

// ---- helpers ----

func (t *galaASTTransformer) extractTypeArgFromIndex(expr ast.Expr, line, col int) (string, error) {
	if e, ok := expr.(*ast.IndexExpr); ok {
		if id, ok := e.Index.(*ast.Ident); ok {
			return id.Name, nil
		}
		if sel, ok := e.Index.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok {
				return id.Name + "." + sel.Sel.Name, nil
			}
		}
	}
	return "", galaerr.NewSemanticErrorAt(line, col, "expected TypeName[T] with a single type argument")
}

// registerStructMetaTypeMeta registers type metadata for a generated StructMeta
// struct so the transpiler's type inference can see the shape-building methods
// it emits.  Note: DecodeFields intentionally does NOT have a registered return
// type here — registering `ReturnType: targetType` would trigger the
// auto-Immutable-unwrap pass on field chains like `d.Nickname.Get()`, inserting
// a spurious extra `.Get()` on Option fields.  Callers of DecodeFields in the
// stdlib (json.Codec[T]) wrap the result in Try before exposing it, and the
// examples reach for fields via explicit `.Get()` access chains, so leaving
// DecodeFields's return type unregistered is safe.
func (t *galaASTTransformer) registerStructMetaTypeMeta(genName, targetTypeName string) {
	_ = targetTypeName
	meta := &transpiler.TypeMetadata{
		Name:    genName,
		Package: t.packageName,
		Methods: map[string]*transpiler.MethodMetadata{
			"NumFields": {
				Name:       "NumFields",
				ReturnType: transpiler.BasicType{Name: "int"},
			},
			"FieldName": {
				Name:       "FieldName",
				ParamTypes: []transpiler.Type{transpiler.BasicType{Name: "int"}},
				ReturnType: transpiler.BasicType{Name: "string"},
			},
		},
		Fields:     make(map[string]transpiler.Type),
		FieldNames: nil,
	}
	t.typeMetas[genName] = meta
	// getType resolves unqualified names through typeMetas, which the cached
	// function environment has already normalized.
	t.invalidateTypeEnv()
}

// autoInjectStructMeta prepends a generated _StructMeta_T{} before existing args.
// This enables: Codec[Person](SnakeCase()) → Apply(_StructMeta_Person{}, SnakeCase())
//
// A root type the codec cannot describe — a generic struct, or a struct with
// no fields — fails with GALA-E0050 here rather than as a Go compile error
// about a StructMeta that was never generated.
func (t *galaASTTransformer) autoInjectStructMeta(args []ast.Expr, methodMeta *transpiler.MethodMetadata, typeArgs []ast.Expr, line, col int) ([]ast.Expr, error) {
	if len(typeArgs) == 0 {
		return args, nil
	}
	typeArgName := ""
	switch arg := typeArgs[0].(type) {
	case *ast.Ident:
		typeArgName = arg.Name
	case *ast.SelectorExpr:
		typeArgName = arg.Sel.Name
	case *ast.IndexExpr, *ast.IndexListExpr:
		root := types.ExprString(arg)
		return nil, t.codecError(&structMetaConfig{rootName: root, line: line, col: col},
			fmt.Sprintf("generic type %s has no codec encoding", root))
	}
	if typeArgName == "" {
		return args, nil
	}

	// Ensure StructMeta is generated for this type
	genName := "_StructMeta_" + typeArgName
	if _, exists := t.structMetas[genName]; !exists {
		typeMeta, resolved := t.getTypeMetaResolved(typeArgName)
		if typeMeta != nil && !typeMeta.IsSealed && len(typeMeta.FieldNames) == 0 {
			return nil, t.codecError(&structMetaConfig{rootName: typeArgName, line: line, col: col},
				noFieldsReason(typeArgName))
		}
		if typeMeta != nil && len(typeMeta.FieldNames) > 0 {
			t.structMetas[genName] = &structMetaConfig{
				typeName:      typeArgName,
				typeMetadata:  typeMeta,
				generatedName: genName,
				resolvedName:  resolved,
				rootName:      typeArgName,
				line:          line,
				col:           col,
			}
			t.registerStructMetaTypeMeta(genName, typeArgName)
		}
	}

	// Prepend StructMeta before existing args
	return append([]ast.Expr{&ast.CompositeLit{Type: ast.NewIdent(genName)}}, args...), nil
}

func buildFieldAccess(receiver ast.Expr, fieldName string, isImmut bool) ast.Expr {
	access := &ast.SelectorExpr{X: receiver, Sel: ast.NewIdent(fieldName)}
	if isImmut {
		return &ast.CallExpr{Fun: &ast.SelectorExpr{X: access, Sel: ast.NewIdent("Get")}}
	}
	return access
}

func unwrapGalaType(t transpiler.Type) transpiler.Type {
	if gt, ok := t.(transpiler.GenericType); ok {
		base := gt.Base.BaseName()
		if base == "Immutable" || base == "std.Immutable" {
			if len(gt.Params) > 0 {
				return gt.Params[0]
			}
		}
	}
	return t
}

// AST builder helpers

func blankRecv(typeName string) *ast.FieldList {
	return &ast.FieldList{List: []*ast.Field{
		{Names: []*ast.Ident{ast.NewIdent("_")}, Type: ast.NewIdent(typeName)},
	}}
}

func idents(names ...string) []*ast.Ident {
	result := make([]*ast.Ident, len(names))
	for i, n := range names {
		result[i] = ast.NewIdent(n)
	}
	return result
}

func intLit(n int) *ast.BasicLit {
	return &ast.BasicLit{Kind: token.INT, Value: fmt.Sprintf("%d", n)}
}

func stringLit(s string) *ast.BasicLit {
	return &ast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("%q", s)}
}

func exprStmt(expr ast.Expr) *ast.ExprStmt {
	return &ast.ExprStmt{X: expr}
}

func methodCall(receiver, method string, args ...ast.Expr) *ast.CallExpr {
	return &ast.CallExpr{
		Fun:  &ast.SelectorExpr{X: ast.NewIdent(receiver), Sel: ast.NewIdent(method)},
		Args: args,
	}
}
