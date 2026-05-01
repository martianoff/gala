package transformer

// Typed StructMeta codegen (Option-C refactor, Phase 2).
//
// This file produces the `EncodeFields` / `DecodeFields` methods on every
// `_StructMeta_T` type. Unlike the legacy `WriteTo` / `ReadFrom` pair, these
// methods are fully typed end-to-end: no `any`, no runtime type assertions,
// no boxing in the hot path. They delegate formatting to a
// `FieldEncoder` / `FieldDecoder` implementation provided by the caller
// (defined in `std/meta.gala`).
//
// Supported field shapes:
//   - Primitives: string, int, int64, float64, bool, rune
//   - Immutable[T] for any supported T
//   - Option[T] for any supported T  (None → null, Some(x) → encoded x)
//   - Array[T] / List[T] of primitives or nested struct
//   - HashMap[string, V] of primitive V or nested struct
//   - Nested struct whose metadata we also emit        (recursive dispatch)
//
// For nested struct dispatch (both directly-typed struct fields and
// struct elements inside Array/List/HashMap), the generated code calls
// the inner _StructMeta_X{}.EncodeFields / DecodeFields with a fresh
// per-type nameFn/omitFn/lookup derived from the `naming` parameter
// passed in by the caller.  The naming convention (SnakeCase, CamelCase,
// AsIs, ...) propagates from the top-level codec into every level of
// nesting; Rename/Omit/OmitEmpty overrides only apply at the top level
// (the level where the user configured them) — nested fields use the
// naming-only mapping.
//
// Unsupported field shapes fall back to `w.WriteNull()` on encode and
// `r.Skip()` on decode so the generated code always compiles. The design
// doc flags these cases for future extension.

import (
	"fmt"
	"go/ast"
	"go/token"

	"martianoff/gala/internal/transpiler"
)

// --- EncodeFields(w FieldEncoder, t T, nameFn func(int) string, omitFn func(int) bool) ---

func (t *galaASTTransformer) genEncodeFields(config *structMetaConfig) *ast.FuncDecl {
	meta := config.typeMetadata
	resolvedName := t.resolveStructTypeName(config.typeName)
	immutFlags := t.structImmutFields[resolvedName]

	var stmts []ast.Stmt

	// w.WriteStartObject()
	stmts = append(stmts, exprStmt(methodCall("w", "WriteStartObject")))

	// Per-field: if !omitFn(i) { w.WriteKey(nameFn(i)); <typed write> }
	for i, fieldName := range meta.FieldNames {
		fieldType := meta.Fields[fieldName]
		isImmut := immutFlags != nil && i < len(immutFlags) && immutFlags[i]
		fieldAccess := buildFieldAccess(ast.NewIdent("t"), fieldName, isImmut)

		writeStmts := []ast.Stmt{
			exprStmt(&ast.CallExpr{
				Fun: &ast.SelectorExpr{X: ast.NewIdent("w"), Sel: ast.NewIdent("WriteKey")},
				Args: []ast.Expr{&ast.CallExpr{
					Fun:  ast.NewIdent("nameFn"),
					Args: []ast.Expr{intLit(i)},
				}},
			}),
		}
		writeStmts = append(writeStmts, t.genTypedWrite(fieldAccess, fieldType)...)

		stmts = append(stmts, &ast.IfStmt{
			Cond: &ast.UnaryExpr{
				Op: token.NOT,
				X: &ast.CallExpr{
					Fun:  ast.NewIdent("omitFn"),
					Args: []ast.Expr{intLit(i)},
				},
			},
			Body: &ast.BlockStmt{List: writeStmts},
		})
	}

	// w.WriteEndObject()
	stmts = append(stmts, exprStmt(methodCall("w", "WriteEndObject")))

	return &ast.FuncDecl{
		Recv: blankRecv(config.generatedName),
		Name: ast.NewIdent("EncodeFields"),
		Type: &ast.FuncType{
			Params: &ast.FieldList{List: []*ast.Field{
				{Names: idents("w"), Type: t.stdIdent("FieldEncoder")},
				{Names: idents("t"), Type: ast.NewIdent(config.typeName)},
				{Names: idents("nameFn"), Type: funcIntStringType()},
				{Names: idents("omitFn"), Type: funcIntBoolType()},
				{Names: idents("naming"), Type: funcStringStringType()},
			}},
		},
		Body: &ast.BlockStmt{List: stmts},
	}
}

// genTypedWrite returns the statements that serialize a single field via
// the format-neutral FieldEncoder.  The caller has already written the key
// (`w.WriteKey(...)`).  Unsupported shapes produce `w.WriteNull()` so the
// code always compiles.
func (t *galaASTTransformer) genTypedWrite(access ast.Expr, fieldType transpiler.Type) []ast.Stmt {
	// Immutable[T] — unwrap.  The access already pulled `.Get()` for us
	// (see buildFieldAccess); just use the inner type for method selection.
	if gt, ok := fieldType.(transpiler.GenericType); ok {
		base := gt.Base.BaseName()
		if base == "Immutable" || base == "std.Immutable" {
			if len(gt.Params) > 0 {
				return t.genTypedWrite(access, gt.Params[0])
			}
		}
		if base == "Option" || base == "std.Option" {
			if len(gt.Params) > 0 {
				return genOptionWrite(access, gt.Params[0])
			}
		}
		if base == "Array" || base == "List" ||
			base == "collection_immutable.Array" || base == "collection_immutable.List" {
			if len(gt.Params) > 0 {
				return t.genCollectionWrite(access, gt.Params[0])
			}
		}
		if base == "HashMap" || base == "collection_immutable.HashMap" {
			if len(gt.Params) == 2 {
				return t.genHashMapWrite(access, gt.Params[1])
			}
		}
	}

	// Primitives.
	if method := primitiveWriteMethodFor(fieldType); method != "" {
		return []ast.Stmt{exprStmt(&ast.CallExpr{
			Fun:  &ast.SelectorExpr{X: ast.NewIdent("w"), Sel: ast.NewIdent(method)},
			Args: []ast.Expr{access},
		})}
	}

	// Nested struct fallback: if the type is a struct we've emitted meta for,
	// call _StructMeta_T{}.EncodeFields recursively.  The inner call gets
	// fresh nameFn/omitFn derived from `naming` and the inner StructMeta's
	// own field names — Rename/Omit/OmitEmpty overrides do not propagate
	// past the top level.  If the type is unknown, emit null so the
	// generated code still compiles.
	if name := namedTypeSimpleName(fieldType); name != "" {
		genName := "_StructMeta_" + name
		if _, ok := t.structMetas[genName]; ok {
			return genNestedStructWrite(genName, access)
		}
	}

	// Fallback.
	return []ast.Stmt{exprStmt(methodCall("w", "WriteNull"))}
}

// genNestedStructWrite emits the call to _StructMeta_Inner{}.EncodeFields
// with fresh per-type nameFn/omitFn closures that derive their key from the
// inherited `naming` mapping and the inner StructMeta's FieldName.
func genNestedStructWrite(genName string, access ast.Expr) []ast.Stmt {
	innerNameFn := &ast.FuncLit{
		Type: &ast.FuncType{
			Params:  &ast.FieldList{List: []*ast.Field{{Names: idents("i"), Type: ast.NewIdent("int")}}},
			Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("string")}}},
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{
			&ast.ReturnStmt{Results: []ast.Expr{&ast.CallExpr{
				Fun: ast.NewIdent("naming"),
				Args: []ast.Expr{&ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   &ast.CompositeLit{Type: ast.NewIdent(genName)},
						Sel: ast.NewIdent("FieldName"),
					},
					Args: []ast.Expr{ast.NewIdent("i")},
				}},
			}}},
		}},
	}
	innerOmitFn := &ast.FuncLit{
		Type: &ast.FuncType{
			Params:  &ast.FieldList{List: []*ast.Field{{Names: idents("i"), Type: ast.NewIdent("int")}}},
			Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("bool")}}},
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{
			// _ = i; return false
			&ast.AssignStmt{
				Lhs: []ast.Expr{ast.NewIdent("_")},
				Tok: token.ASSIGN,
				Rhs: []ast.Expr{ast.NewIdent("i")},
			},
			&ast.ReturnStmt{Results: []ast.Expr{ast.NewIdent("false")}},
		}},
	}
	return []ast.Stmt{exprStmt(&ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   &ast.CompositeLit{Type: ast.NewIdent(genName)},
			Sel: ast.NewIdent("EncodeFields"),
		},
		Args: []ast.Expr{
			ast.NewIdent("w"),
			access,
			innerNameFn,
			innerOmitFn,
			ast.NewIdent("naming"),
		},
	})}
}

// genOptionWrite: if Some → write inner; if None → WriteNull.
func genOptionWrite(access ast.Expr, inner transpiler.Type) []ast.Stmt {
	method := primitiveWriteMethodFor(inner)
	if method == "" {
		// Unsupported inner type — emit conservative null.
		return []ast.Stmt{exprStmt(methodCall("w", "WriteNull"))}
	}
	return []ast.Stmt{&ast.IfStmt{
		Cond: &ast.CallExpr{Fun: &ast.SelectorExpr{X: access, Sel: ast.NewIdent("IsDefined")}},
		Body: &ast.BlockStmt{List: []ast.Stmt{
			exprStmt(&ast.CallExpr{
				Fun: &ast.SelectorExpr{X: ast.NewIdent("w"), Sel: ast.NewIdent(method)},
				Args: []ast.Expr{&ast.CallExpr{
					Fun: &ast.SelectorExpr{X: access, Sel: ast.NewIdent("Get")},
				}},
			}),
		}},
		Else: &ast.BlockStmt{List: []ast.Stmt{
			exprStmt(methodCall("w", "WriteNull")),
		}},
	}}
}

// genCollectionWrite: write start-array, iterate, write end-array.
// Supports both primitive elements (direct WriteX) and nested struct
// elements (recursive EncodeFields dispatch through _StructMeta_X{}).
func (t *galaASTTransformer) genCollectionWrite(access ast.Expr, elem transpiler.Type) []ast.Stmt {
	elemGoType := t.goTypeFor(elem)
	if elemGoType == nil {
		// Try named-struct path for elements like Array[Inner].
		if name := namedTypeSimpleName(elem); name != "" {
			if _, ok := t.structMetas["_StructMeta_"+name]; ok {
				elemGoType = t.qualifiedTypeIdent(name)
			}
		}
		if elemGoType == nil {
			return []ast.Stmt{exprStmt(methodCall("w", "WriteNull"))}
		}
	}

	// Body of the lambda: either a primitive WriteX, a nested struct encode,
	// or a nested collection / hashmap encode (Array[Array[T]] etc.).
	innerStmts := t.genElementWrite(elem, ast.NewIdent("elem"))
	if innerStmts == nil {
		return []ast.Stmt{exprStmt(methodCall("w", "WriteNull"))}
	}

	lambda := &ast.FuncLit{
		Type: &ast.FuncType{
			Params: &ast.FieldList{List: []*ast.Field{
				{Names: idents("elem"), Type: elemGoType},
			}},
		},
		Body: &ast.BlockStmt{List: innerStmts},
	}
	return []ast.Stmt{
		exprStmt(methodCall("w", "WriteStartArray")),
		exprStmt(&ast.CallExpr{
			Fun:  &ast.SelectorExpr{X: access, Sel: ast.NewIdent("ForEach")},
			Args: []ast.Expr{lambda},
		}),
		exprStmt(methodCall("w", "WriteEndArray")),
	}
}

// genElementWrite emits the body that serialises a single collection
// element (or HashMap value) of type `elem`, given an access expression
// for the element value.  Returns nil if the shape is unsupported.
func (t *galaASTTransformer) genElementWrite(elem transpiler.Type, access ast.Expr) []ast.Stmt {
	// Primitive element: w.WriteX(access).
	if method := primitiveWriteMethodFor(elem); method != "" {
		return []ast.Stmt{exprStmt(&ast.CallExpr{
			Fun:  &ast.SelectorExpr{X: ast.NewIdent("w"), Sel: ast.NewIdent(method)},
			Args: []ast.Expr{access},
		})}
	}
	// Generic shapes (Immutable[T], Option[T], Array[T], List[T], HashMap[K, V]).
	if gt, ok := elem.(transpiler.GenericType); ok {
		base := gt.Base.BaseName()
		if base == "Immutable" || base == "std.Immutable" {
			if len(gt.Params) > 0 {
				// Unwrap Immutable: call .Get() on the access then recurse.
				inner := &ast.CallExpr{
					Fun: &ast.SelectorExpr{X: access, Sel: ast.NewIdent("Get")},
				}
				return t.genElementWrite(gt.Params[0], inner)
			}
		}
		if base == "Option" || base == "std.Option" {
			if len(gt.Params) > 0 {
				return genOptionWrite(access, gt.Params[0])
			}
		}
		if base == "Array" || base == "List" ||
			base == "collection_immutable.Array" || base == "collection_immutable.List" {
			if len(gt.Params) > 0 {
				return t.genCollectionWrite(access, gt.Params[0])
			}
		}
		if base == "HashMap" || base == "collection_immutable.HashMap" {
			if len(gt.Params) == 2 {
				return t.genHashMapWrite(access, gt.Params[1])
			}
		}
	}
	// Nested struct element: dispatch through _StructMeta_X{}.EncodeFields.
	if name := namedTypeSimpleName(elem); name != "" {
		genName := "_StructMeta_" + name
		if _, ok := t.structMetas[genName]; ok {
			return genNestedStructWrite(genName, access)
		}
	}
	return nil
}

// genHashMapWrite: write start-object, iterate (k,v), write end-object.
// Supports primitive values and nested struct values (recursive
// EncodeFields dispatch through _StructMeta_X{}).
func (t *galaASTTransformer) genHashMapWrite(access ast.Expr, elem transpiler.Type) []ast.Stmt {
	elemGoType := t.goTypeFor(elem)
	if elemGoType == nil {
		// Try named-struct path for values like HashMap[string, Inner].
		if name := namedTypeSimpleName(elem); name != "" {
			if _, ok := t.structMetas["_StructMeta_"+name]; ok {
				elemGoType = t.qualifiedTypeIdent(name)
			}
		}
		if elemGoType == nil {
			return []ast.Stmt{exprStmt(methodCall("w", "WriteNull"))}
		}
	}

	valueStmts := t.genElementWrite(elem, ast.NewIdent("v"))
	if valueStmts == nil {
		return []ast.Stmt{exprStmt(methodCall("w", "WriteNull"))}
	}

	lambdaBody := []ast.Stmt{
		exprStmt(&ast.CallExpr{
			Fun:  &ast.SelectorExpr{X: ast.NewIdent("w"), Sel: ast.NewIdent("WriteKey")},
			Args: []ast.Expr{ast.NewIdent("k")},
		}),
	}
	lambdaBody = append(lambdaBody, valueStmts...)

	lambda := &ast.FuncLit{
		Type: &ast.FuncType{
			Params: &ast.FieldList{List: []*ast.Field{
				{Names: idents("k"), Type: ast.NewIdent("string")},
				{Names: idents("v"), Type: elemGoType},
			}},
		},
		Body: &ast.BlockStmt{List: lambdaBody},
	}
	return []ast.Stmt{
		exprStmt(methodCall("w", "WriteStartObject")),
		exprStmt(&ast.CallExpr{
			Fun:  &ast.SelectorExpr{X: access, Sel: ast.NewIdent("ForEachKV")},
			Args: []ast.Expr{lambda},
		}),
		exprStmt(methodCall("w", "WriteEndObject")),
	}
}

// --- DecodeFields(r FieldDecoder, lookup func(string) int) T ---

func (t *galaASTTransformer) genDecodeFields(config *structMetaConfig) *ast.FuncDecl {
	meta := config.typeMetadata
	resolvedName := t.resolveStructTypeName(config.typeName)
	immutFlags := t.structImmutFields[resolvedName]

	var stmts []ast.Stmt

	// Per-field local var decls of the correct Go type (with zero-value
	// defaults — Option fields default to None via zero-value, since
	// Some/None share representation and the zero value is equivalent to
	// None for our option layout in stdlib... but to be safe, initialise
	// Options explicitly).
	for i, fieldName := range meta.FieldNames {
		fieldType := meta.Fields[fieldName]
		_ = i
		localName := "_" + fieldName
		goType := t.goTypeFor(unwrapGalaType(fieldType))
		if goType == nil {
			// Unsupported shape — fall back to named-type ident.
			if name := namedTypeSimpleName(fieldType); name != "" {
				goType = t.qualifiedTypeIdent(name)
			} else {
				goType = ast.NewIdent("interface{}")
			}
		}
		stmts = append(stmts, &ast.DeclStmt{Decl: &ast.GenDecl{
			Tok: token.VAR,
			Specs: []ast.Spec{
				&ast.ValueSpec{Names: idents(localName), Type: goType},
			},
		}})
	}

	// r.StartObject()
	stmts = append(stmts, exprStmt(methodCall("r", "StartObject")))

	// Switch cases for each field.
	var switchCases []ast.Stmt
	for i, fieldName := range meta.FieldNames {
		fieldType := meta.Fields[fieldName]
		localName := "_" + fieldName
		assignStmts := t.genTypedRead(ast.NewIdent(localName), fieldType)
		if assignStmts == nil {
			assignStmts = []ast.Stmt{exprStmt(methodCall("r", "Skip"))}
		}
		switchCases = append(switchCases, &ast.CaseClause{
			List: []ast.Expr{intLit(i)},
			Body: assignStmts,
		})
	}
	// default: r.Skip()
	switchCases = append(switchCases, &ast.CaseClause{
		Body: []ast.Stmt{exprStmt(methodCall("r", "Skip"))},
	})

	// for r.HasMoreFields() { key := r.ReadKey(); switch lookup(key) { ... } }
	forBody := []ast.Stmt{
		&ast.AssignStmt{
			Lhs: []ast.Expr{ast.NewIdent("key")},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{methodCall("r", "ReadKey")},
		},
		&ast.SwitchStmt{
			Tag:  &ast.CallExpr{Fun: ast.NewIdent("lookup"), Args: []ast.Expr{ast.NewIdent("key")}},
			Body: &ast.BlockStmt{List: switchCases},
		},
	}
	stmts = append(stmts, &ast.ForStmt{
		Cond: methodCall("r", "HasMoreFields"),
		Body: &ast.BlockStmt{List: forBody},
	})

	// r.EndObject()
	stmts = append(stmts, exprStmt(methodCall("r", "EndObject")))

	// return T{FieldName: _FieldName, ...}  (wrap Immutable fields)
	var compositeElts []ast.Expr
	for i, fieldName := range meta.FieldNames {
		localName := "_" + fieldName
		isImmut := immutFlags != nil && i < len(immutFlags) && immutFlags[i]
		var value ast.Expr = ast.NewIdent(localName)
		if isImmut {
			value = &ast.CallExpr{Fun: t.stdIdent("NewImmutable"), Args: []ast.Expr{value}}
		}
		compositeElts = append(compositeElts, &ast.KeyValueExpr{
			Key:   ast.NewIdent(fieldName),
			Value: value,
		})
	}
	stmts = append(stmts, &ast.ReturnStmt{Results: []ast.Expr{
		&ast.CompositeLit{
			Type: ast.NewIdent(config.typeName),
			Elts: compositeElts,
		},
	}})

	return &ast.FuncDecl{
		Recv: blankRecv(config.generatedName),
		Name: ast.NewIdent("DecodeFields"),
		Type: &ast.FuncType{
			Params: &ast.FieldList{List: []*ast.Field{
				{Names: idents("r"), Type: t.stdIdent("FieldDecoder")},
				{Names: idents("lookup"), Type: funcStringIntType()},
				{Names: idents("naming"), Type: funcStringStringType()},
			}},
			Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent(config.typeName)}}},
		},
		Body: &ast.BlockStmt{List: stmts},
	}
}

// genTypedRead emits statements that read a single field from the decoder
// and assign to the given local variable.  Returns nil for shapes we can't
// handle (the caller will fall back to `r.Skip()`).
func (t *galaASTTransformer) genTypedRead(localIdent ast.Expr, fieldType transpiler.Type) []ast.Stmt {
	// Unwrap Immutable: read inner, assign, final compositeLit will wrap.
	if gt, ok := fieldType.(transpiler.GenericType); ok {
		base := gt.Base.BaseName()
		if base == "Immutable" || base == "std.Immutable" {
			if len(gt.Params) > 0 {
				return t.genTypedRead(localIdent, gt.Params[0])
			}
		}
		if base == "Option" || base == "std.Option" {
			if len(gt.Params) > 0 {
				return t.genOptionRead(localIdent, gt.Params[0])
			}
		}
		if base == "Array" || base == "List" ||
			base == "collection_immutable.Array" || base == "collection_immutable.List" {
			if len(gt.Params) > 0 {
				short := base
				if dot := lastDot(base); dot >= 0 {
					short = base[dot+1:]
				}
				return t.genCollectionRead(localIdent, gt.Params[0], short)
			}
		}
		if base == "HashMap" || base == "collection_immutable.HashMap" {
			if len(gt.Params) == 2 {
				return t.genHashMapRead(localIdent, gt.Params[1])
			}
		}
	}

	if method := primitiveReadMethodFor(fieldType); method != "" {
		return []ast.Stmt{&ast.AssignStmt{
			Lhs: []ast.Expr{localIdent},
			Tok: token.ASSIGN,
			Rhs: []ast.Expr{&ast.CallExpr{
				Fun: &ast.SelectorExpr{X: ast.NewIdent("r"), Sel: ast.NewIdent(method)},
			}},
		}}
	}

	// Nested struct field: dispatch through _StructMeta_X{}.DecodeFields with
	// a freshly-built lookup keyed by the inherited `naming` mapping.
	if name := namedTypeSimpleName(fieldType); name != "" {
		genName := "_StructMeta_" + name
		if _, ok := t.structMetas[genName]; ok {
			return []ast.Stmt{&ast.AssignStmt{
				Lhs: []ast.Expr{localIdent},
				Tok: token.ASSIGN,
				Rhs: []ast.Expr{&ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   &ast.CompositeLit{Type: ast.NewIdent(genName)},
						Sel: ast.NewIdent("DecodeFields"),
					},
					Args: []ast.Expr{
						ast.NewIdent("r"),
						genNestedStructLookup(genName),
						ast.NewIdent("naming"),
					},
				}},
			}}
		}
	}
	return nil
}

// genNestedStructLookup emits a closure: func(key string) int that looks up
// the field index in _StructMeta_Inner using the inherited `naming`
// mapping.  We bind the meta to a local so its composite-literal use does
// not collide with for-clause syntax (Go treats `_StructMeta_T{` in a for
// header as the start of a composite-literal block, which is a parse error).
func genNestedStructLookup(genName string) ast.Expr {
	// _meta := _StructMeta_Inner{}
	// n := _meta.NumFields()
	// for i := 0; i < n; i++ {
	//     if naming(_meta.FieldName(i)) == key { return i }
	// }
	// return -1
	body := []ast.Stmt{
		&ast.AssignStmt{
			Lhs: []ast.Expr{ast.NewIdent("_meta")},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{&ast.CompositeLit{Type: ast.NewIdent(genName)}},
		},
		&ast.AssignStmt{
			Lhs: []ast.Expr{ast.NewIdent("n")},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{&ast.CallExpr{
				Fun: &ast.SelectorExpr{X: ast.NewIdent("_meta"), Sel: ast.NewIdent("NumFields")},
			}},
		},
		&ast.ForStmt{
			Init: &ast.AssignStmt{
				Lhs: []ast.Expr{ast.NewIdent("i")},
				Tok: token.DEFINE,
				Rhs: []ast.Expr{intLit(0)},
			},
			Cond: &ast.BinaryExpr{
				X:  ast.NewIdent("i"),
				Op: token.LSS,
				Y:  ast.NewIdent("n"),
			},
			Post: &ast.IncDecStmt{X: ast.NewIdent("i"), Tok: token.INC},
			Body: &ast.BlockStmt{List: []ast.Stmt{
				&ast.IfStmt{
					Cond: &ast.BinaryExpr{
						X: &ast.CallExpr{
							Fun: ast.NewIdent("naming"),
							Args: []ast.Expr{&ast.CallExpr{
								Fun: &ast.SelectorExpr{
									X:   ast.NewIdent("_meta"),
									Sel: ast.NewIdent("FieldName"),
								},
								Args: []ast.Expr{ast.NewIdent("i")},
							}},
						},
						Op: token.EQL,
						Y:  ast.NewIdent("key"),
					},
					Body: &ast.BlockStmt{List: []ast.Stmt{
						&ast.ReturnStmt{Results: []ast.Expr{ast.NewIdent("i")}},
					}},
				},
			}},
		},
		&ast.ReturnStmt{Results: []ast.Expr{intLit(-1)}},
	}
	return &ast.FuncLit{
		Type: &ast.FuncType{
			Params:  &ast.FieldList{List: []*ast.Field{{Names: idents("key"), Type: ast.NewIdent("string")}}},
			Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("int")}}},
		},
		Body: &ast.BlockStmt{List: body},
	}
}

// genCollectionRead emits read statements for Array[T] or List[T] fields.
// `containerName` is "Array" or "List" — the GALA collection ctor used for
// the final FromSlice wrap.
func (t *galaASTTransformer) genCollectionRead(localIdent ast.Expr, elem transpiler.Type, containerName string) []ast.Stmt {
	elemGoType := t.goTypeFor(elem)
	if elemGoType == nil {
		if name := namedTypeSimpleName(elem); name != "" {
			if _, ok := t.structMetas["_StructMeta_"+name]; ok {
				elemGoType = t.qualifiedTypeIdent(name)
			}
		}
		if elemGoType == nil {
			return nil
		}
	}

	// Build the per-element read into a fresh local `__elem`.
	elemLocal := ast.NewIdent("__elem")
	elemReadStmts := t.genElementRead(elem, elemLocal)
	if elemReadStmts == nil {
		return nil
	}

	// var __sliceN []ElemGoType
	sliceLocal := ast.NewIdent("__slice")
	stmts := []ast.Stmt{
		&ast.DeclStmt{Decl: &ast.GenDecl{
			Tok: token.VAR,
			Specs: []ast.Spec{
				&ast.ValueSpec{
					Names: []*ast.Ident{sliceLocal},
					Type:  &ast.ArrayType{Elt: elemGoType},
				},
			},
		}},
	}

	// r.StartArray()
	stmts = append(stmts, exprStmt(methodCall("r", "StartArray")))

	// for r.HasMoreElements() { var __elem T; <read>; __slice = append(__slice, __elem) }
	loopBody := []ast.Stmt{
		&ast.DeclStmt{Decl: &ast.GenDecl{
			Tok: token.VAR,
			Specs: []ast.Spec{
				&ast.ValueSpec{
					Names: []*ast.Ident{elemLocal},
					Type:  elemGoType,
				},
			},
		}},
	}
	loopBody = append(loopBody, elemReadStmts...)
	loopBody = append(loopBody, &ast.AssignStmt{
		Lhs: []ast.Expr{sliceLocal},
		Tok: token.ASSIGN,
		Rhs: []ast.Expr{&ast.CallExpr{
			Fun:  ast.NewIdent("append"),
			Args: []ast.Expr{sliceLocal, elemLocal},
		}},
	})
	stmts = append(stmts, &ast.ForStmt{
		Cond: methodCall("r", "HasMoreElements"),
		Body: &ast.BlockStmt{List: loopBody},
	})

	// r.EndArray()
	stmts = append(stmts, exprStmt(methodCall("r", "EndArray")))

	// localIdent = ArrayFromSlice(__slice) (or ListFromSlice for List)
	ctorName := "ArrayFromSlice"
	if containerName == "List" {
		ctorName = "ListFromSlice"
	}
	stmts = append(stmts, &ast.AssignStmt{
		Lhs: []ast.Expr{localIdent},
		Tok: token.ASSIGN,
		Rhs: []ast.Expr{&ast.CallExpr{
			Fun:  t.collectionIdent(ctorName),
			Args: []ast.Expr{sliceLocal},
		}},
	})

	// Wrap the whole thing in a block so the __slice / __elem locals don't
	// leak across multiple collection reads inside the same parent.
	return []ast.Stmt{&ast.BlockStmt{List: stmts}}
}

// genHashMapRead emits read statements for HashMap[string, V] fields.
func (t *galaASTTransformer) genHashMapRead(localIdent ast.Expr, valueType transpiler.Type) []ast.Stmt {
	valueGoType := t.goTypeFor(valueType)
	if valueGoType == nil {
		if name := namedTypeSimpleName(valueType); name != "" {
			if _, ok := t.structMetas["_StructMeta_"+name]; ok {
				valueGoType = t.qualifiedTypeIdent(name)
			}
		}
		if valueGoType == nil {
			return nil
		}
	}

	// var __m HashMap[string, V] = EmptyHashMap[string, V]()
	mLocal := ast.NewIdent("__m")
	stringIdent := ast.NewIdent("string")
	emptyMapType := &ast.IndexListExpr{
		X:       t.collectionIdent("HashMap"),
		Indices: []ast.Expr{stringIdent, valueGoType},
	}
	stmts := []ast.Stmt{
		&ast.DeclStmt{Decl: &ast.GenDecl{
			Tok: token.VAR,
			Specs: []ast.Spec{
				&ast.ValueSpec{
					Names:  []*ast.Ident{mLocal},
					Type:   emptyMapType,
					Values: []ast.Expr{&ast.CallExpr{
						Fun: &ast.IndexListExpr{
							X:       t.collectionIdent("EmptyHashMap"),
							Indices: []ast.Expr{ast.NewIdent("string"), valueGoType},
						},
					}},
				},
			},
		}},
	}

	// r.StartObject()
	stmts = append(stmts, exprStmt(methodCall("r", "StartObject")))

	// for r.HasMoreFields() { __k := r.ReadKey(); var __v V; <read>; __m = __m.Put(__k, __v) }
	kLocal := ast.NewIdent("__k")
	vLocal := ast.NewIdent("__v")
	valueReadStmts := t.genElementRead(valueType, vLocal)
	if valueReadStmts == nil {
		return nil
	}
	loopBody := []ast.Stmt{
		&ast.AssignStmt{
			Lhs: []ast.Expr{kLocal},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{methodCall("r", "ReadKey")},
		},
		&ast.DeclStmt{Decl: &ast.GenDecl{
			Tok: token.VAR,
			Specs: []ast.Spec{
				&ast.ValueSpec{
					Names: []*ast.Ident{vLocal},
					Type:  valueGoType,
				},
			},
		}},
	}
	loopBody = append(loopBody, valueReadStmts...)
	loopBody = append(loopBody, &ast.AssignStmt{
		Lhs: []ast.Expr{mLocal},
		Tok: token.ASSIGN,
		Rhs: []ast.Expr{&ast.CallExpr{
			Fun: &ast.SelectorExpr{X: mLocal, Sel: ast.NewIdent("Put")},
			Args: []ast.Expr{kLocal, vLocal},
		}},
	})
	stmts = append(stmts, &ast.ForStmt{
		Cond: methodCall("r", "HasMoreFields"),
		Body: &ast.BlockStmt{List: loopBody},
	})

	// r.EndObject()
	stmts = append(stmts, exprStmt(methodCall("r", "EndObject")))

	// localIdent = __m
	stmts = append(stmts, &ast.AssignStmt{
		Lhs: []ast.Expr{localIdent},
		Tok: token.ASSIGN,
		Rhs: []ast.Expr{mLocal},
	})

	return []ast.Stmt{&ast.BlockStmt{List: stmts}}
}

// genElementRead emits the read statements for a single collection element
// or HashMap value of type `elem`, assigning to the `target` ident.
// Returns nil for unsupported shapes.
func (t *galaASTTransformer) genElementRead(elem transpiler.Type, target ast.Expr) []ast.Stmt {
	if method := primitiveReadMethodFor(elem); method != "" {
		return []ast.Stmt{&ast.AssignStmt{
			Lhs: []ast.Expr{target},
			Tok: token.ASSIGN,
			Rhs: []ast.Expr{&ast.CallExpr{
				Fun: &ast.SelectorExpr{X: ast.NewIdent("r"), Sel: ast.NewIdent(method)},
			}},
		}}
	}
	if gt, ok := elem.(transpiler.GenericType); ok {
		base := gt.Base.BaseName()
		if base == "Immutable" || base == "std.Immutable" {
			if len(gt.Params) > 0 {
				return t.genElementRead(gt.Params[0], target)
			}
		}
		if base == "Option" || base == "std.Option" {
			if len(gt.Params) > 0 {
				return t.genOptionRead(target, gt.Params[0])
			}
		}
		if base == "Array" || base == "List" ||
			base == "collection_immutable.Array" || base == "collection_immutable.List" {
			if len(gt.Params) > 0 {
				short := base
				if dot := lastDot(base); dot >= 0 {
					short = base[dot+1:]
				}
				return t.genCollectionRead(target, gt.Params[0], short)
			}
		}
		if base == "HashMap" || base == "collection_immutable.HashMap" {
			if len(gt.Params) == 2 {
				return t.genHashMapRead(target, gt.Params[1])
			}
		}
	}
	if name := namedTypeSimpleName(elem); name != "" {
		genName := "_StructMeta_" + name
		if _, ok := t.structMetas[genName]; ok {
			return []ast.Stmt{&ast.AssignStmt{
				Lhs: []ast.Expr{target},
				Tok: token.ASSIGN,
				Rhs: []ast.Expr{&ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   &ast.CompositeLit{Type: ast.NewIdent(genName)},
						Sel: ast.NewIdent("DecodeFields"),
					},
					Args: []ast.Expr{
						ast.NewIdent("r"),
						genNestedStructLookup(genName),
						ast.NewIdent("naming"),
					},
				}},
			}}
		}
	}
	return nil
}

// genOptionRead: if r.IsNull() { r.ReadNull(); local = None[E]{}.Apply() }
//                else               { local = Some[E]{}.Apply(r.ReadX()) }
func (t *galaASTTransformer) genOptionRead(localIdent ast.Expr, inner transpiler.Type) []ast.Stmt {
	readMethod := primitiveReadMethodFor(inner)
	if readMethod == "" {
		return nil
	}
	innerName := baseTypeName(inner)
	noneCtor := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X: &ast.CompositeLit{Type: &ast.IndexExpr{
				X:     t.stdIdent("None"),
				Index: ast.NewIdent(innerName),
			}},
			Sel: ast.NewIdent("Apply"),
		},
	}
	someCtor := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X: &ast.CompositeLit{Type: &ast.IndexExpr{
				X:     t.stdIdent("Some"),
				Index: ast.NewIdent(innerName),
			}},
			Sel: ast.NewIdent("Apply"),
		},
		Args: []ast.Expr{&ast.CallExpr{
			Fun: &ast.SelectorExpr{X: ast.NewIdent("r"), Sel: ast.NewIdent(readMethod)},
		}},
	}
	return []ast.Stmt{&ast.IfStmt{
		Cond: methodCall("r", "IsNull"),
		Body: &ast.BlockStmt{List: []ast.Stmt{
			exprStmt(methodCall("r", "ReadNull")),
			&ast.AssignStmt{
				Lhs: []ast.Expr{localIdent},
				Tok: token.ASSIGN,
				Rhs: []ast.Expr{noneCtor},
			},
		}},
		Else: &ast.BlockStmt{List: []ast.Stmt{
			&ast.AssignStmt{
				Lhs: []ast.Expr{localIdent},
				Tok: token.ASSIGN,
				Rhs: []ast.Expr{someCtor},
			},
		}},
	}}
}

// --- helpers ---

func primitiveWriteMethodFor(ty transpiler.Type) string {
	switch baseTypeName(unwrapGalaType(ty)) {
	case "string":
		return "WriteString"
	case "int":
		return "WriteInt"
	case "int64":
		return "WriteInt64"
	case "float64":
		return "WriteFloat64"
	case "bool":
		return "WriteBool"
	case "rune":
		return "WriteRune"
	}
	return ""
}

func primitiveReadMethodFor(ty transpiler.Type) string {
	switch baseTypeName(unwrapGalaType(ty)) {
	case "string":
		return "ReadString"
	case "int":
		return "ReadInt"
	case "int64":
		return "ReadInt64"
	case "float64":
		return "ReadFloat64"
	case "bool":
		return "ReadBool"
	case "rune":
		return "ReadRune"
	}
	return ""
}

// goTypeFor returns a Go AST type ident for a primitive or common generic
// field type so DecodeFields can declare locals with the correct type.
// Returns nil for shapes where we can't confidently name the Go type here.
// The transformer receiver is required to emit properly-qualified references
// to non-local packages (collection_immutable, std) honouring dot-imports.
func (t *galaASTTransformer) goTypeFor(ty transpiler.Type) ast.Expr {
	if ty == nil {
		return nil
	}
	switch ty := ty.(type) {
	case transpiler.BasicType:
		return ast.NewIdent(ty.Name)
	case transpiler.NamedType:
		return t.qualifiedTypeIdent(ty.Name)
	case transpiler.GenericType:
		base := ty.Base.BaseName()
		if base == "Immutable" || base == "std.Immutable" {
			if len(ty.Params) > 0 {
				return t.goTypeFor(ty.Params[0])
			}
		}
		if base == "Option" || base == "std.Option" {
			if len(ty.Params) > 0 {
				inner := t.goTypeFor(ty.Params[0])
				if inner == nil {
					return nil
				}
				return &ast.IndexExpr{X: t.stdIdent("Option"), Index: inner}
			}
		}
		if base == "Array" || base == "List" ||
			base == "collection_immutable.Array" || base == "collection_immutable.List" {
			if len(ty.Params) > 0 {
				inner := t.goTypeFor(ty.Params[0])
				if inner == nil {
					return nil
				}
				name := base
				if dot := lastDot(base); dot >= 0 {
					name = base[dot+1:]
				}
				return &ast.IndexExpr{X: t.collectionIdent(name), Index: inner}
			}
		}
		if base == "HashMap" || base == "collection_immutable.HashMap" {
			if len(ty.Params) == 2 {
				k := t.goTypeFor(ty.Params[0])
				v := t.goTypeFor(ty.Params[1])
				if k == nil || v == nil {
					return nil
				}
				name := base
				if dot := lastDot(base); dot >= 0 {
					name = base[dot+1:]
				}
				return &ast.IndexListExpr{X: t.collectionIdent(name), Indices: []ast.Expr{k, v}}
			}
		}
		// Other generics left for later phases.
	}
	return nil
}

// qualifiedTypeIdent emits an ast.Expr for a type name that may be
// package-qualified.  For known packages (collection_immutable, std) it
// uses the importManager-aware helpers; for anything else it falls back
// to a plain Selector/Ident split.
func (t *galaASTTransformer) qualifiedTypeIdent(name string) ast.Expr {
	if dot := lastDot(name); dot >= 0 {
		pkg := name[:dot]
		sym := name[dot+1:]
		switch pkg {
		case "collection_immutable":
			return t.collectionIdent(sym)
		case "std":
			return t.stdIdent(sym)
		}
		return &ast.SelectorExpr{X: ast.NewIdent(pkg), Sel: ast.NewIdent(sym)}
	}
	return ast.NewIdent(name)
}

func lastDot(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return i
		}
	}
	return -1
}

func namedTypeSimpleName(ty transpiler.Type) string {
	switch t := ty.(type) {
	case transpiler.BasicType:
		return t.Name
	case transpiler.NamedType:
		return t.Name
	case transpiler.GenericType:
		return t.Base.BaseName()
	}
	return ""
}

func funcIntStringType() ast.Expr {
	return &ast.FuncType{
		Params:  &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("int")}}},
		Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("string")}}},
	}
}

func funcIntBoolType() ast.Expr {
	return &ast.FuncType{
		Params:  &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("int")}}},
		Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("bool")}}},
	}
}

func funcStringIntType() ast.Expr {
	return &ast.FuncType{
		Params:  &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("string")}}},
		Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("int")}}},
	}
}

func funcStringStringType() ast.Expr {
	return &ast.FuncType{
		Params:  &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("string")}}},
		Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("string")}}},
	}
}

// fmtDebugType is a development helper — callers use it to inspect type
// strings when debugging failures in this pass. Deliberately exported
// (lowercase) so it doesn't leak outside the package.
var _ = fmt.Sprintf
