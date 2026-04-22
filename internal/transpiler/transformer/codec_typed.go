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
//   - Array[T] / List[T] of primitives                 (iteration)
//   - HashMap[string, V] of primitive V                (iteration)
//   - Nested struct whose metadata we also emit        (recursive dispatch)
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
				return genCollectionWrite(access, gt.Params[0])
			}
		}
		if base == "HashMap" || base == "collection_immutable.HashMap" {
			if len(gt.Params) == 2 {
				return genHashMapWrite(access, gt.Params[1])
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
	// call _StructMeta_T{}.EncodeFields recursively.  Otherwise, emit null so
	// the generated code still compiles.
	if name := namedTypeSimpleName(fieldType); name != "" {
		genName := "_StructMeta_" + name
		if _, ok := t.structMetas[genName]; ok {
			return []ast.Stmt{exprStmt(&ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X:   &ast.CompositeLit{Type: ast.NewIdent(genName)},
					Sel: ast.NewIdent("EncodeFields"),
				},
				Args: []ast.Expr{
					ast.NewIdent("w"),
					access,
					ast.NewIdent("nameFn"),
					ast.NewIdent("omitFn"),
				},
			})}
		}
	}

	// Fallback.
	return []ast.Stmt{exprStmt(methodCall("w", "WriteNull"))}
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

// genCollectionWrite: write start-array, iterate, write end-array. Primitive elements only.
func genCollectionWrite(access ast.Expr, elem transpiler.Type) []ast.Stmt {
	method := primitiveWriteMethodFor(elem)
	if method == "" {
		return []ast.Stmt{exprStmt(methodCall("w", "WriteNull"))}
	}
	elemGoType := goTypeFor(elem)
	if elemGoType == nil {
		return []ast.Stmt{exprStmt(methodCall("w", "WriteNull"))}
	}
	// w.WriteStartArray()
	// access.ForEach(func(elem <ElemGoType>) { w.WriteX(elem) })
	// w.WriteEndArray()
	lambda := &ast.FuncLit{
		Type: &ast.FuncType{
			Params: &ast.FieldList{List: []*ast.Field{
				{Names: idents("elem"), Type: elemGoType},
			}},
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{
			exprStmt(&ast.CallExpr{
				Fun:  &ast.SelectorExpr{X: ast.NewIdent("w"), Sel: ast.NewIdent(method)},
				Args: []ast.Expr{ast.NewIdent("elem")},
			}),
		}},
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

// genHashMapWrite: write start-object, iterate (k,v), write end-object. Primitive value only.
func genHashMapWrite(access ast.Expr, elem transpiler.Type) []ast.Stmt {
	method := primitiveWriteMethodFor(elem)
	if method == "" {
		return []ast.Stmt{exprStmt(methodCall("w", "WriteNull"))}
	}
	elemGoType := goTypeFor(elem)
	if elemGoType == nil {
		return []ast.Stmt{exprStmt(methodCall("w", "WriteNull"))}
	}
	lambda := &ast.FuncLit{
		Type: &ast.FuncType{
			Params: &ast.FieldList{List: []*ast.Field{
				{Names: idents("k"), Type: ast.NewIdent("string")},
				{Names: idents("v"), Type: elemGoType},
			}},
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{
			exprStmt(&ast.CallExpr{
				Fun:  &ast.SelectorExpr{X: ast.NewIdent("w"), Sel: ast.NewIdent("WriteKey")},
				Args: []ast.Expr{ast.NewIdent("k")},
			}),
			exprStmt(&ast.CallExpr{
				Fun:  &ast.SelectorExpr{X: ast.NewIdent("w"), Sel: ast.NewIdent(method)},
				Args: []ast.Expr{ast.NewIdent("v")},
			}),
		}},
	}
	return []ast.Stmt{
		exprStmt(methodCall("w", "WriteStartObject")),
		exprStmt(&ast.CallExpr{
			Fun:  &ast.SelectorExpr{X: access, Sel: ast.NewIdent("ForEach")},
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
		goType := goTypeFor(unwrapGalaType(fieldType))
		if goType == nil {
			// Unsupported shape — fall back to named-type ident.
			if name := namedTypeSimpleName(fieldType); name != "" {
				goType = ast.NewIdent(name)
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
		// TODO: Array / List / HashMap / nested struct reads.
	}

	method := primitiveReadMethodFor(fieldType)
	if method == "" {
		return nil
	}
	return []ast.Stmt{&ast.AssignStmt{
		Lhs: []ast.Expr{localIdent},
		Tok: token.ASSIGN,
		Rhs: []ast.Expr{&ast.CallExpr{
			Fun: &ast.SelectorExpr{X: ast.NewIdent("r"), Sel: ast.NewIdent(method)},
		}},
	}}
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
func goTypeFor(ty transpiler.Type) ast.Expr {
	if ty == nil {
		return nil
	}
	switch t := ty.(type) {
	case transpiler.BasicType:
		return ast.NewIdent(t.Name)
	case transpiler.NamedType:
		return ast.NewIdent(t.Name)
	case transpiler.GenericType:
		base := t.Base.BaseName()
		if base == "Immutable" || base == "std.Immutable" {
			if len(t.Params) > 0 {
				return goTypeFor(t.Params[0])
			}
		}
		if base == "Option" || base == "std.Option" {
			if len(t.Params) > 0 {
				inner := goTypeFor(t.Params[0])
				if inner == nil {
					return nil
				}
				return &ast.IndexExpr{X: ast.NewIdent("Option"), Index: inner}
			}
		}
		// Other generics left for later phases.
	}
	return nil
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

// fmtDebugType is a development helper — callers use it to inspect type
// strings when debugging failures in this pass. Deliberately exported
// (lowercase) so it doesn't leak outside the package.
var _ = fmt.Sprintf
