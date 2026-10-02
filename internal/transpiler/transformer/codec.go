package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"hash/fnv"
	"path/filepath"
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

// structMetaConfig holds the compile-time configuration for one struct's
// StructMeta.
//
// Where a StructMeta lives depends on the package that declares the struct:
//
//   - A library (any package but main) emits an exported StructMeta_X for
//     every struct it declares that the codec can describe, in the file that
//     declares it. Every other use of X — another file of the package, or
//     another package — references that one declaration. This is what lets a
//     struct with unexported fields be encoded outside its package: only the
//     declaring package can read and construct those fields.
//   - The main package cannot be imported, so it keeps emitting _StructMeta_X
//     on demand, in the file whose Codec / StructMeta use asked for it. The
//     name carries a per-file suffix (codecFileSuffix), so two files of the
//     package that both use Codec[X] do not both declare _StructMeta_X.
type structMetaConfig struct {
	// typeName is the struct as diagnostics name it: X for a struct of this
	// package, pkg.X for an imported one.
	typeName      string
	typeMetadata  *transpiler.TypeMetadata
	generatedName string
	resolvedName  string
	// pkg is the declaring package when it is not this one, "" otherwise.
	pkg string
	// emit is true when this file declares the StructMeta. Otherwise the file
	// only references it, and generating its methods just checks that every
	// field has an encoding, so a missing one is still reported at the use.
	emit bool
	// auto marks a StructMeta a library file emits because it declares the
	// struct, not because a codec asked for it. One whose fields have no
	// encoding is dropped silently: the struct may never be encoded.
	auto bool
	// rootName, line and col identify the use site (`Codec[T]`, `StructMeta[T]()`)
	// that first asked for this codec. A struct reached only as a nested field
	// inherits them from its parent, so a diagnostic about any field points at
	// the code that requested the codec.
	rootName  string
	line, col int
}

// mainPackageName is the one package that cannot be imported, so its structs'
// StructMetas are emitted on demand rather than by the declaring file.
const mainPackageName = "main"

// ---- StructMeta interception ----

// transformStructMetaConstruction handles StructMeta[T]() calls.
// This is the ONLY codec-related compiler intrinsic.
func (t *galaASTTransformer) transformStructMetaConstruction(fun ast.Expr, line, col int) (ast.Expr, error) {
	typeName, err := t.extractTypeArgFromIndex(fun, line, col)
	if err != nil {
		return nil, err
	}

	typeMeta, _ := t.getTypeMetaResolved(typeName)
	if typeMeta == nil {
		return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf("StructMeta[%s]: type %q not found", typeName, typeName))
	}
	if len(typeMeta.FieldNames) == 0 {
		return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf("StructMeta[%s]: type %q has no fields", typeName, typeName))
	}
	site := &structMetaConfig{rootName: typeName, line: line, col: col}
	config := t.registerStructMeta(typeName, site)
	if config == nil {
		return nil, t.codecError(site, t.notDescribableReason(typeName))
	}
	return &ast.CompositeLit{Type: t.structMetaRef(config)}, nil
}

// registerStructMeta returns the StructMeta config for the struct named name,
// registering it on first use, or nil when name is not a struct the codec can
// describe (unknown, sealed, generic, or without fields). site carries the
// use site and whether the request is an auto one. A codec asking for a
// struct that so far was only auto-registered takes it over, so a field
// without an encoding is reported rather than dropped.
func (t *galaASTTransformer) registerStructMeta(name string, site *structMetaConfig) *structMetaConfig {
	meta, resolved := t.getTypeMetaResolved(name)
	if meta == nil || describableReason(name, meta) != "" {
		return nil
	}
	if config, ok := t.structMetas[resolved]; ok {
		if config.auto && !site.auto {
			config.auto = false
			config.rootName, config.line, config.col = site.rootName, site.line, site.col
		}
		return config
	}
	config := &structMetaConfig{
		typeName:      meta.Name,
		typeMetadata:  meta,
		generatedName: "StructMeta_" + meta.Name,
		resolvedName:  resolved,
		auto:          site.auto,
		rootName:      site.rootName,
		line:          site.line,
		col:           site.col,
	}
	switch {
	case meta.Package != "" && meta.Package != t.packageName:
		config.pkg = meta.Package
		config.typeName = meta.Package + "." + meta.Name
	case t.packageName == mainPackageName:
		config.generatedName = "_StructMeta_" + meta.Name + t.codecFileSuffix()
		config.emit = true
	}
	t.structMetas[resolved] = config
	if !site.auto {
		t.registerStructMetaTypeMeta(config)
	}
	return config
}

// describableReason says why the named type has no StructMeta, or "" when it
// can have one.
func describableReason(name string, meta *transpiler.TypeMetadata) string {
	switch {
	case meta.IsSealed:
		return sealedReason(name)
	case len(meta.TypeParams) > 0:
		return fmt.Sprintf("generic type %s has no codec encoding", name)
	case len(meta.FieldNames) == 0:
		return noFieldsReason(name)
	}
	return ""
}

// notDescribableReason explains why registerStructMeta found no StructMeta
// for the named type.
func (t *galaASTTransformer) notDescribableReason(name string) string {
	if meta, _ := t.getTypeMetaResolved(name); meta != nil {
		if reason := describableReason(name, meta); reason != "" {
			return reason
		}
	}
	return fmt.Sprintf("%s is neither a scalar nor a GALA struct the codec can describe", name)
}

// structMetaRef names config's StructMeta type in this file: bare when this
// package declares it, qualified (or bare through a dot import) when the
// struct's own package does.
func (t *galaASTTransformer) structMetaRef(config *structMetaConfig) ast.Expr {
	if config.pkg == "" {
		return ast.NewIdent(config.generatedName)
	}
	return t.ident(config.pkg + "." + config.generatedName)
}

// ---- code generation ----

// finalizeCodecs generates all StructMeta and ValueMeta Go declarations.
//
// Before codegen runs we close over the set of nested struct types
// reachable from the registered top-level metas — every field whose
// (unwrapped) type is itself a struct, including struct elements inside
// Array/List/HashMap, gets its own StructMeta registered so the recursive
// EncodeFields/DecodeFields dispatch in codec_typed.go can find it. The walk
// is a simple worklist fixpoint. A library file then also registers every
// struct it declares, so the StructMeta its other files and its importers
// reference exists.
//
// A field whose type has no encoding fails the build with GALA-E0050. The
// configs are visited in name order so that, when several are broken, the
// error reported is the same on every run.
func (t *galaASTTransformer) finalizeCodecs(file *ast.File) error {
	t.expandNestedStructMetas()
	if t.packageName != mainPackageName {
		declared := make(map[string]bool)
		for _, name := range declaredStructNames(file) {
			declared[name] = true
			t.registerStructMeta(name, &structMetaConfig{rootName: name, auto: true})
		}
		t.expandNestedStructMetas()
		for _, config := range t.structMetas {
			config.emit = config.pkg == "" && declared[config.typeMetadata.Name]
		}
	}
	// Dropping an auto StructMeta whose fields have no encoding can leave
	// another one referring to it, so generate again until nothing is
	// dropped. Each pass starts from the imports as they were before it.
	for {
		restore := t.snapshotCodecImports()
		decls, dropped, err := t.generateStructMetas()
		if err != nil {
			return err
		}
		if !dropped {
			valueDecls, err := t.generateValueMetaDecls()
			if err != nil {
				return err
			}
			file.Decls = append(append(file.Decls, decls...), valueDecls...)
			return nil
		}
		restore()
	}
}

// generateStructMetas generates every registered StructMeta and returns the
// declarations this file emits. An auto StructMeta that cannot be generated
// is unregistered and reported as dropped.
func (t *galaASTTransformer) generateStructMetas() ([]ast.Decl, bool, error) {
	// Emit in name order: structMetas is a map, and ranging over it directly
	// made the declaration order — and so the generated file — differ from
	// one run to the next for any program with more than one codec'd struct.
	keys := sortedStructMetaKeys(t.structMetas)
	sort.SliceStable(keys, func(i, j int) bool {
		return t.structMetas[keys[i]].generatedName < t.structMetas[keys[j]].generatedName
	})
	var out []ast.Decl
	dropped := false
	for _, key := range keys {
		config := t.structMetas[key]
		var decls []ast.Decl
		var err error
		if config.emit {
			decls, err = t.generateStructMetaDecls(config)
		} else {
			// Only checked here: whatever the throwaway code imported goes.
			restore := t.snapshotCodecImports()
			_, err = t.generateStructMetaDecls(config)
			restore()
		}
		if err != nil {
			if config.auto {
				delete(t.structMetas, key)
				dropped = true
				continue
			}
			return nil, false, err
		}
		out = append(out, decls...)
	}
	return out, dropped, nil
}

func sortedStructMetaKeys(metas map[string]*structMetaConfig) []string {
	keys := make([]string, 0, len(metas))
	for key := range metas {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// snapshotCodecImports records the import state that generating a StructMeta
// changes, and returns a function that puts it back. Code that is generated
// and thrown away — a StructMeta only checked here, or one dropped — must not
// leave the file importing what only that code referenced.
func (t *galaASTTransformer) snapshotCodecImports() func() {
	needsStd := t.needsStdImport
	restoreImports := t.importManager.snapshotUsage()
	return func() {
		t.needsStdImport = needsStd
		restoreImports()
	}
}

// declaredStructNames lists the non-generic structs this file declares.
func declaredStructNames(file *ast.File) []string {
	var names []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.TypeParams != nil || ts.Assign.IsValid() {
				continue
			}
			if _, isStruct := ts.Type.(*ast.StructType); isStruct {
				names = append(names, ts.Name.Name)
			}
		}
	}
	return names
}

// expandNestedStructMetas walks every registered StructMeta config's fields
// and registers fresh entries for any nested struct types that we have
// metadata for. Repeats until the set is closed. A nested struct inherits its
// parent's use site, and whether it is auto.
func (t *galaASTTransformer) expandNestedStructMetas() {
	// Seed in name order: a nested struct inherits its use site from whichever
	// parent reaches it first, and that must not depend on map iteration.
	var worklist []*structMetaConfig
	for _, key := range sortedStructMetaKeys(t.structMetas) {
		worklist = append(worklist, t.structMetas[key])
	}
	walked := make(map[*structMetaConfig]bool, len(worklist))
	for len(worklist) > 0 {
		config := worklist[0]
		worklist = worklist[1:]
		if walked[config] {
			continue
		}
		walked[config] = true
		for _, fieldName := range config.typeMetadata.FieldNames {
			for _, nested := range t.registerNestedStructMetaForType(config.typeMetadata.Fields[fieldName], config) {
				if !walked[nested] {
					worklist = append(worklist, nested)
				}
			}
		}
	}
}

// registerNestedStructMetaForType registers a StructMeta for every struct
// reachable through ty's container layers (Immutable, Option, Array, List,
// HashMap value) and returns them. ty is a field of parent's struct, or a
// value parent stands as the use site for; a field type's unqualified names
// belong to parent's package. Each nested StructMeta inherits parent's use
// site, and whether it is auto.
func (t *galaASTTransformer) registerNestedStructMetaForType(ty transpiler.Type, parent *structMetaConfig) []*structMetaConfig {
	var nested []*structMetaConfig
	t.collectNestedStructTypeNames(ty, parent.pkg, func(name string) {
		if config := t.registerStructMeta(name, parent); config != nil {
			nested = append(nested, config)
		}
	})
	return nested
}

// collectNestedStructTypeNames invokes `cb` for every potentially-struct
// named type reachable from `ty` by unwrapping Immutable/Option and
// stepping into Array/List element + HashMap value parameters. pkg is the
// package declaring the struct whose field ty is ("" for this one).
func (t *galaASTTransformer) collectNestedStructTypeNames(ty transpiler.Type, pkg string, cb func(string)) {
	if ty == nil {
		return
	}
	ty = t.codecUnalias(ty)
	switch kind, params := codecContainer(ty); kind {
	case "Immutable", "Option", "Array", "List":
		t.collectNestedStructTypeNames(params[0], pkg, cb)
		return
	case "HashMap":
		t.collectNestedStructTypeNames(params[1], pkg, cb)
		return
	}
	if name := t.codecStructName(ty, pkg); name != "" {
		cb(name)
	}
}

// collectionIdent returns a qualified reference to a collection_immutable type.
// Adds the import automatically.
func (t *galaASTTransformer) collectionIdent(name string) ast.Expr {
	if t.packageName == "collection_immutable" {
		return ast.NewIdent(name)
	}
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

// registerStructMetaTypeMeta registers type metadata for a StructMeta type so
// the transpiler's type inference can see the shape-building methods it
// emits. It is registered under the name this file refers to it by.  Note:
// DecodeFields intentionally does NOT have a registered return
// type here — registering `ReturnType: targetType` would trigger the
// auto-Immutable-unwrap pass on field chains like `d.Nickname.Get()`, inserting
// a spurious extra `.Get()` on Option fields.  Callers of DecodeFields in the
// stdlib (json.Codec[T]) wrap the result in Try before exposing it, and the
// examples reach for fields via explicit `.Get()` access chains, so leaving
// DecodeFields's return type unregistered is safe.
func (t *galaASTTransformer) registerStructMetaTypeMeta(config *structMetaConfig) {
	key, pkg := config.generatedName, t.packageName
	if config.pkg != "" {
		key, pkg = config.pkg+"."+config.generatedName, config.pkg
	}
	meta := &transpiler.TypeMetadata{
		Name:    config.generatedName,
		Package: pkg,
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
	t.typeMetas[key] = meta
	// getType resolves unqualified names through typeMetas, which the cached
	// function environment has already normalized.
	t.invalidateTypeEnv()
}

// autoInjectStructMeta prepends the StructMeta value for T before the existing
// args. This enables: Codec[Person](SnakeCase()) →
// Apply(_StructMeta_Person{}, SnakeCase()).
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
		typeArgName = types.ExprString(arg)
	case *ast.IndexExpr, *ast.IndexListExpr:
		root := codecSpelling(arg)
		site := &structMetaConfig{rootName: root, line: line, col: col}
		if kind, _ := codecContainer(t.astTypeToTranspilerType(arg)); kind != "" {
			return nil, t.codecError(site, notAStructReason(root))
		}
		return nil, t.codecError(site, fmt.Sprintf("generic type %s has no codec encoding", root))
	}
	if typeArgName == "" {
		return args, nil
	}

	site := &structMetaConfig{rootName: typeArgName, line: line, col: col}
	config := t.registerStructMeta(typeArgName, site)
	if config == nil {
		// A scalar root (Codec[int]), a collection or an alias of one is not a
		// struct at all: say so, and name what does describe it.
		ty := t.codecUnalias(t.astTypeToTranspilerType(typeArgs[0]))
		_, _, _, isScalar := t.codecScalarOf(ty)
		if container, _ := codecContainer(ty); isScalar || container != "" {
			return nil, t.codecError(site, notAStructReason(typeArgName))
		}
		return nil, t.codecError(site, t.notDescribableReason(typeArgName))
	}

	// Prepend StructMeta before existing args
	return append([]ast.Expr{&ast.CompositeLit{Type: t.structMetaRef(config)}}, args...), nil
}

// notAStructReason explains that a StructMeta-based codec was asked for a
// root that is not a struct, and names the mechanism that does cover it.
func notAStructReason(name string) string {
	return fmt.Sprintf("%s is not a struct: StructMeta[T] describes the fields of a struct; "+
		"a root of another shape needs a codec built on ValueMeta[T]", name)
}

// codecFileSuffix tells apart the metadata types generated on demand by
// different files of one package — a main-package _StructMeta_X, and every
// _ValueMeta_X, which describes a value shape no single file declares. Each
// file is transpiled on its own and declares what it uses, so two files that
// both use Codec[X] (in main) or Value[int] would otherwise both declare the
// same type and the package would not compile. The suffix is a hash of the
// file's name, so it is stable across builds; it is empty when there is no
// file name.
func (t *galaASTTransformer) codecFileSuffix() string {
	if t.filePath == "" {
		return ""
	}
	h := fnv.New32a()
	h.Write([]byte(filepath.Base(t.filePath)))
	return fmt.Sprintf("_%08x", h.Sum32())
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
