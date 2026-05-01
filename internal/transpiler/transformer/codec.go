package transformer

import (
	"fmt"
	"go/ast"
	"go/token"

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
func (t *galaASTTransformer) finalizeCodecs(file *ast.File) {
	t.expandNestedStructMetas()
	for _, config := range t.structMetas {
		decls := t.generateStructMetaDecls(config)
		file.Decls = append(file.Decls, decls...)
	}
}

// expandNestedStructMetas walks every registered StructMeta config's fields
// and registers fresh entries for any nested struct types that we have
// metadata for.  Repeats until the set is closed.
func (t *galaASTTransformer) expandNestedStructMetas() {
	worklist := make([]string, 0, len(t.structMetas))
	for genName := range t.structMetas {
		worklist = append(worklist, genName)
	}
	for len(worklist) > 0 {
		genName := worklist[0]
		worklist = worklist[1:]
		config, ok := t.structMetas[genName]
		if !ok || config.typeMetadata == nil {
			continue
		}
		for _, fieldName := range config.typeMetadata.FieldNames {
			fieldType := config.typeMetadata.Fields[fieldName]
			added := t.registerNestedStructMetaForType(fieldType)
			worklist = append(worklist, added...)
		}
	}
}

// registerNestedStructMetaForType registers a _StructMeta_X for any struct
// type reachable through the given field type's container layers
// (Immutable, Option, Array, List, HashMap value).  Returns the list of
// freshly-registered generated names so the caller can keep walking.
func (t *galaASTTransformer) registerNestedStructMetaForType(fieldType transpiler.Type) []string {
	var added []string
	t.collectNestedStructTypeNames(fieldType, func(name string) {
		genName := "_StructMeta_" + name
		if _, exists := t.structMetas[genName]; exists {
			return
		}
		typeMeta, resolved := t.getTypeMetaResolved(name)
		if typeMeta == nil || len(typeMeta.FieldNames) == 0 {
			return
		}
		t.structMetas[genName] = &structMetaConfig{
			typeName:      name,
			typeMetadata:  typeMeta,
			generatedName: genName,
			resolvedName:  resolved,
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
	if gt, ok := ty.(transpiler.GenericType); ok {
		base := gt.Base.BaseName()
		if base == "Immutable" || base == "std.Immutable" ||
			base == "Option" || base == "std.Option" {
			if len(gt.Params) > 0 {
				t.collectNestedStructTypeNames(gt.Params[0], cb)
			}
			return
		}
		if base == "Array" || base == "List" ||
			base == "collection_immutable.Array" || base == "collection_immutable.List" {
			if len(gt.Params) > 0 {
				t.collectNestedStructTypeNames(gt.Params[0], cb)
			}
			return
		}
		if base == "HashMap" || base == "collection_immutable.HashMap" {
			if len(gt.Params) == 2 {
				t.collectNestedStructTypeNames(gt.Params[1], cb)
			}
			return
		}
		// Other generics (user-defined) — only recurse into the base name as
		// the candidate; their type params are not part of any struct shape
		// the transpiler currently codegens for.
		if name := gt.Base.BaseName(); name != "" {
			cb(name)
		}
		return
	}
	if name := namedTypeSimpleName(ty); name != "" {
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

func (t *galaASTTransformer) generateStructMetaDecls(config *structMetaConfig) []ast.Decl {
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
	// Option-C: the only typed serialisation methods.  EncodeFields /
	// DecodeFields live in codec_typed.go.
	decls = append(decls, t.genEncodeFields(config))
	decls = append(decls, t.genDecodeFields(config))

	return decls
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

func baseTypeName(t transpiler.Type) string {
	if t == nil {
		return ""
	}
	switch bt := t.(type) {
	case transpiler.BasicType:
		return bt.Name
	case transpiler.NamedType:
		return bt.Name
	case transpiler.GenericType:
		// Generic types (e.g., Option[int]) — report the base name so codec
		// dispatch can find metadata for the parameterized container.
		return bt.Base.BaseName()
	case transpiler.PointerType:
		return baseTypeName(bt.Elem)
	case transpiler.ArrayType, transpiler.MapType, transpiler.FuncType, transpiler.NilType:
		// Composite/terminal kinds have no single base name — codec paths that
		// reach here with these should skip field-by-field codegen.
		return ""
	default:
		// New Type implementations must be added here so codec generation does
		// not silently fall through.
		return ""
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
}

// autoInjectStructMeta prepends a generated _StructMeta_T{} before existing args.
// This enables: Codec[Person](SnakeCase()) → Apply(_StructMeta_Person{}, SnakeCase())
func (t *galaASTTransformer) autoInjectStructMeta(args []ast.Expr, methodMeta *transpiler.MethodMetadata, typeArgs []ast.Expr) []ast.Expr {
	if len(typeArgs) == 0 {
		return args
	}
	typeArgName := ""
	if id, ok := typeArgs[0].(*ast.Ident); ok {
		typeArgName = id.Name
	} else if sel, ok := typeArgs[0].(*ast.SelectorExpr); ok {
		typeArgName = sel.Sel.Name
	}
	if typeArgName == "" {
		return args
	}

	// Ensure StructMeta is generated for this type
	genName := "_StructMeta_" + typeArgName
	if _, exists := t.structMetas[genName]; !exists {
		typeMeta, resolved := t.getTypeMetaResolved(typeArgName)
		if typeMeta != nil && len(typeMeta.FieldNames) > 0 {
			t.structMetas[genName] = &structMetaConfig{
				typeName:      typeArgName,
				typeMetadata:  typeMeta,
				generatedName: genName,
				resolvedName:  resolved,
			}
			t.registerStructMetaTypeMeta(genName, typeArgName)
		}
	}

	// Prepend StructMeta before existing args
	return append([]ast.Expr{&ast.CompositeLit{Type: ast.NewIdent(genName)}}, args...)
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

func methodCall(receiver, method string) *ast.CallExpr {
	return &ast.CallExpr{
		Fun: &ast.SelectorExpr{X: ast.NewIdent(receiver), Sel: ast.NewIdent(method)},
	}
}
