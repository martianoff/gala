package transformer

import (
	"fmt"
	"go/ast"
	"go/token"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

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
	// This enables type inference for expressions like meta.ReadFrom(...).FirstName
	t.registerStructMetaTypeMeta(genName, typeName)

	return &ast.CompositeLit{Type: ast.NewIdent(genName)}, nil
}

// ---- code generation ----

// finalizeCodecs generates all StructMeta Go declarations.
func (t *galaASTTransformer) finalizeCodecs(file *ast.File) {
	for _, config := range t.structMetas {
		decls := t.generateStructMetaDecls(config)
		file.Decls = append(file.Decls, decls...)
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

// arrayStringType returns the AST for Array[string].
func (t *galaASTTransformer) arrayStringType() ast.Expr {
	return &ast.IndexExpr{
		X:     t.collectionIdent("Array"),
		Index: ast.NewIdent("string"),
	}
}

// hashMapStringType returns the AST for HashMap[string, string].
func (t *galaASTTransformer) hashMapStringType() ast.Expr {
	return &ast.IndexListExpr{
		X:       t.collectionIdent("HashMap"),
		Indices: []ast.Expr{ast.NewIdent("string"), ast.NewIdent("string")},
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
	decls = append(decls, t.genFieldType(genName, meta))
	decls = append(decls, t.genWriteTo(config))
	decls = append(decls, t.genReadFrom(config))
	// Any-based variants for generic codec use (internal to codec libraries)
	decls = append(decls, t.genWriteToAny(config))
	decls = append(decls, t.genReadFromAny(config))

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

// --- FieldType(i int) string ---

func (t *galaASTTransformer) genFieldType(genName string, meta *transpiler.TypeMetadata) *ast.FuncDecl {
	var cases []ast.Stmt
	for i, fieldName := range meta.FieldNames {
		fieldType := meta.Fields[fieldName]
		typeStr := ""
		if fieldType != nil {
			typeStr = fieldType.String()
		}
		cases = append(cases, &ast.CaseClause{
			List: []ast.Expr{intLit(i)},
			Body: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{stringLit(typeStr)}}},
		})
	}
	cases = append(cases, &ast.CaseClause{
		Body: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{stringLit("")}}},
	})

	return &ast.FuncDecl{
		Recv: blankRecv(genName),
		Name: ast.NewIdent("FieldType"),
		Type: &ast.FuncType{
			Params:  &ast.FieldList{List: []*ast.Field{{Names: idents("i"), Type: ast.NewIdent("int")}}},
			Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("string")}}},
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{
			&ast.SwitchStmt{Tag: ast.NewIdent("i"), Body: &ast.BlockStmt{List: cases}},
		}},
	}
}

// --- WriteTo(v T, w FieldWriter, keys Array[string]) ---

func (t *galaASTTransformer) genWriteTo(config *structMetaConfig) *ast.FuncDecl {
	meta := config.typeMetadata
	resolvedName := t.resolveStructTypeName(config.typeName)
	immutFlags := t.structImmutFields[resolvedName]

	var stmts []ast.Stmt

	// w.BeginObject()
	stmts = append(stmts, exprStmt(methodCall("w", "BeginObject")))

	// For each field: if keys.Get(i) != "" { w.WriteKey(keys.Get(i)); w.WriteXxx(v.Field.Get()) }
	for i, fieldName := range meta.FieldNames {
		fieldType := meta.Fields[fieldName]
		isImmut := immutFlags != nil && i < len(immutFlags) && immutFlags[i]
		fieldAccess := buildFieldAccess(ast.NewIdent("v"), fieldName, isImmut)

		keyExpr := &ast.CallExpr{
			Fun:  &ast.SelectorExpr{X: ast.NewIdent("keys"), Sel: ast.NewIdent("Get")},
			Args: []ast.Expr{intLit(i)},
		}

		var fieldStmts []ast.Stmt
		fieldStmts = append(fieldStmts, exprStmt(&ast.CallExpr{
			Fun:  &ast.SelectorExpr{X: ast.NewIdent("w"), Sel: ast.NewIdent("WriteKey")},
			Args: []ast.Expr{keyExpr},
		}))
		fieldStmts = append(fieldStmts, t.genFieldWrite(fieldAccess, fieldType)...)

		// Wrap in if keys.Get(i) != "" to support Omit (empty key = omitted)
		stmts = append(stmts, &ast.IfStmt{
			Cond: &ast.BinaryExpr{
				X:  &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("keys"), Sel: ast.NewIdent("Get")}, Args: []ast.Expr{intLit(i)}},
				Op: token.NEQ,
				Y:  stringLit(""),
			},
			Body: &ast.BlockStmt{List: fieldStmts},
		})
	}

	// w.EndObject()
	stmts = append(stmts, exprStmt(methodCall("w", "EndObject")))

	return &ast.FuncDecl{
		Recv: blankRecv(config.generatedName),
		Name: ast.NewIdent("WriteTo"),
		Type: &ast.FuncType{
			Params: &ast.FieldList{List: []*ast.Field{
				{Names: idents("v"), Type: ast.NewIdent(config.typeName)},
				{Names: idents("w"), Type: t.stdIdent("FieldWriter")},
				{Names: idents("keys"), Type: t.arrayStringType()},
			}},
		},
		Body: &ast.BlockStmt{List: stmts},
	}
}

// --- ReadFrom(r FieldReader, reverseKeys HashMap[string, string]) T ---

func (t *galaASTTransformer) genReadFrom(config *structMetaConfig) *ast.FuncDecl {
	meta := config.typeMetadata
	resolvedName := t.resolveStructTypeName(config.typeName)
	immutFlags := t.structImmutFields[resolvedName]

	var stmts []ast.Stmt

	// var result T
	stmts = append(stmts, &ast.DeclStmt{Decl: &ast.GenDecl{
		Tok: token.VAR,
		Specs: []ast.Spec{
			&ast.ValueSpec{Names: idents("result"), Type: ast.NewIdent(config.typeName)},
		},
	}})

	// Default Option[T] fields to None[T]() so a missing JSON field produces
	// None (not an accidental zero-valued Some). Without this, the zero
	// _variant tag (Some) leaks through as Some(zero-T) for any field that
	// wasn't present in the input. This matches Scala circe / serde_json /
	// encoding/json-with-*T semantics: missing == null == None.
	for i, fieldName := range meta.FieldNames {
		fieldType := meta.Fields[fieldName]
		inner, ok := optionInner(fieldType)
		if !ok {
			continue
		}
		isImmut := immutFlags != nil && i < len(immutFlags) && immutFlags[i]
		innerExpr := t.typeToExpr(inner)
		var defaultValue ast.Expr = &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   &ast.CompositeLit{Type: t.buildNoneType(innerExpr)},
				Sel: ast.NewIdent("Apply"),
			},
		}
		if isImmut {
			defaultValue = &ast.CallExpr{Fun: t.stdIdent("NewImmutable"), Args: []ast.Expr{defaultValue}}
		}
		stmts = append(stmts, &ast.AssignStmt{
			Lhs: []ast.Expr{&ast.SelectorExpr{X: ast.NewIdent("result"), Sel: ast.NewIdent(fieldName)}},
			Tok: token.ASSIGN,
			Rhs: []ast.Expr{defaultValue},
		})
	}

	// r.BeginObject()
	stmts = append(stmts, exprStmt(methodCall("r", "BeginObject")))

	// Build switch cases
	var switchCases []ast.Stmt
	for i, fieldName := range meta.FieldNames {
		fieldType := meta.Fields[fieldName]
		isImmut := immutFlags != nil && i < len(immutFlags) && immutFlags[i]

		caseBody := t.genFieldReadStmts(fieldName, fieldType, isImmut)
		if caseBody == nil {
			switchCases = append(switchCases, &ast.CaseClause{
				List: []ast.Expr{stringLit(fieldName)},
				Body: []ast.Stmt{exprStmt(methodCall("r", "SkipValue"))},
			})
			continue
		}

		switchCases = append(switchCases, &ast.CaseClause{
			List: []ast.Expr{stringLit(fieldName)},
			Body: caseBody,
		})
	}

	// default: r.SkipValue()
	switchCases = append(switchCases, &ast.CaseClause{
		Body: []ast.Stmt{exprStmt(methodCall("r", "SkipValue"))},
	})

	// for r.HasNextField() { _fn := reverseKeys.GetOrElse(r.NextKey().Get(), ""); switch _fn { ... } }
	forBody := []ast.Stmt{
		// _fn := reverseKeys.GetOrElse(r.NextKey().Get(), "")
		shortVarDecl("_fn", &ast.CallExpr{
			Fun:  &ast.SelectorExpr{X: ast.NewIdent("reverseKeys"), Sel: ast.NewIdent("GetOrElse")},
			Args: []ast.Expr{
				&ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   methodCall("r", "NextKey"),
						Sel: ast.NewIdent("Get"),
					},
				},
				stringLit(""),
			},
		}),
		&ast.SwitchStmt{
			Tag:  ast.NewIdent("_fn"),
			Body: &ast.BlockStmt{List: switchCases},
		},
	}

	stmts = append(stmts, &ast.ForStmt{
		Cond: methodCall("r", "HasNextField"),
		Body: &ast.BlockStmt{List: forBody},
	})

	// r.EndObject()
	stmts = append(stmts, exprStmt(methodCall("r", "EndObject")))

	// return result
	stmts = append(stmts, &ast.ReturnStmt{Results: []ast.Expr{ast.NewIdent("result")}})

	return &ast.FuncDecl{
		Recv: blankRecv(config.generatedName),
		Name: ast.NewIdent("ReadFrom"),
		Type: &ast.FuncType{
			Params: &ast.FieldList{List: []*ast.Field{
				{Names: idents("r"), Type: t.stdIdent("FieldReader")},
				{Names: idents("reverseKeys"), Type: t.hashMapStringType()},
			}},
			Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent(config.typeName)}}},
		},
		Body: &ast.BlockStmt{List: stmts},
	}
}

// ---- field type dispatch ----

// --- WriteToAny(v any, w FieldWriter, keys Array[string]) ---
// Delegates to typed WriteTo after type assertion. Used by codec libraries internally.
func (t *galaASTTransformer) genWriteToAny(config *structMetaConfig) *ast.FuncDecl {
	// func (_ _StructMeta_T) WriteToAny(v any, w FieldWriter, keys Array[string]) {
	//     _StructMeta_T{}.WriteTo(v.(T), w, keys)
	// }
	return &ast.FuncDecl{
		Recv: blankRecv(config.generatedName),
		Name: ast.NewIdent("WriteToAny"),
		Type: &ast.FuncType{
			Params: &ast.FieldList{List: []*ast.Field{
				{Names: idents("v"), Type: ast.NewIdent("any")},
				{Names: idents("w"), Type: t.stdIdent("FieldWriter")},
				{Names: idents("keys"), Type: t.arrayStringType()},
			}},
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{
			exprStmt(&ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X:   &ast.CompositeLit{Type: ast.NewIdent(config.generatedName)},
					Sel: ast.NewIdent("WriteTo"),
				},
				Args: []ast.Expr{
					&ast.TypeAssertExpr{X: ast.NewIdent("v"), Type: ast.NewIdent(config.typeName)},
					ast.NewIdent("w"),
					ast.NewIdent("keys"),
				},
			}),
		}},
	}
}

// --- ReadFromAny(r FieldReader, reverseKeys HashMap[string, string]) any ---
// Delegates to typed ReadFrom. Used by codec libraries internally.
func (t *galaASTTransformer) genReadFromAny(config *structMetaConfig) *ast.FuncDecl {
	// func (_ _StructMeta_T) ReadFromAny(r FieldReader, reverseKeys HashMap[string, string]) any {
	//     return _StructMeta_T{}.ReadFrom(r, reverseKeys)
	// }
	return &ast.FuncDecl{
		Recv: blankRecv(config.generatedName),
		Name: ast.NewIdent("ReadFromAny"),
		Type: &ast.FuncType{
			Params: &ast.FieldList{List: []*ast.Field{
				{Names: idents("r"), Type: t.stdIdent("FieldReader")},
				{Names: idents("reverseKeys"), Type: t.hashMapStringType()},
			}},
			Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("any")}}},
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{
			&ast.ReturnStmt{Results: []ast.Expr{
				&ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   &ast.CompositeLit{Type: ast.NewIdent(config.generatedName)},
						Sel: ast.NewIdent("ReadFrom"),
					},
					Args: []ast.Expr{ast.NewIdent("r"), ast.NewIdent("reverseKeys")},
				},
			}},
		}},
	}
}

// genFieldWrite generates the Go statements that serialize a single struct field
// to the FieldWriter `w`.
//
// For plain types: `w.WriteString(v.Field)` (or WriteInt, WriteBool, …).
//
// For Option[T] fields: branches on the Option's state so `WriteNull()` is
// called with no arguments (matching the FieldWriter interface) when the
// field is None, and the inner T is written via the appropriate WriteXxx
// when the field is Some:
//
//	if fieldAccess.IsDefined() {
//	    w.WriteString(fieldAccess.Get())
//	} else {
//	    w.WriteNull()
//	}
func (t *galaASTTransformer) genFieldWrite(fieldAccess ast.Expr, fieldType transpiler.Type) []ast.Stmt {
	if inner, ok := optionInner(fieldType); ok {
		// Option[T] field: branch on IsDefined / IsEmpty.
		innerMethod := writeMethodForBasicType(baseTypeName(unwrapGalaType(inner)))
		if innerMethod == "WriteNull" {
			// Unsupported inner type (nested Option, custom struct, etc.).
			// Emit a single WriteNull so the output is at least valid JSON and
			// the Go code still compiles. Proper support for these inner types
			// is tracked separately.
			return []ast.Stmt{exprStmt(methodCall("w", "WriteNull"))}
		}

		// if fieldAccess.IsDefined() { w.WriteXxx(fieldAccess.Get()) } else { w.WriteNull() }
		someBody := &ast.BlockStmt{List: []ast.Stmt{exprStmt(&ast.CallExpr{
			Fun: &ast.SelectorExpr{X: ast.NewIdent("w"), Sel: ast.NewIdent(innerMethod)},
			Args: []ast.Expr{&ast.CallExpr{
				Fun: &ast.SelectorExpr{X: fieldAccess, Sel: ast.NewIdent("Get")},
			}},
		})}}
		noneBody := &ast.BlockStmt{List: []ast.Stmt{exprStmt(methodCall("w", "WriteNull"))}}
		return []ast.Stmt{&ast.IfStmt{
			Cond: &ast.CallExpr{
				Fun: &ast.SelectorExpr{X: fieldAccess, Sel: ast.NewIdent("IsDefined")},
			},
			Body: someBody,
			Else: noneBody,
		}}
	}

	baseType := unwrapGalaType(fieldType)
	method := writeMethodForBasicType(baseTypeName(baseType))
	return []ast.Stmt{exprStmt(&ast.CallExpr{
		Fun:  &ast.SelectorExpr{X: ast.NewIdent("w"), Sel: ast.NewIdent(method)},
		Args: []ast.Expr{fieldAccess},
	})}
}

// genFieldReadStmts generates the Go statements that populate
// `result.<fieldName>` from the FieldReader `r` for a single field.
//
// For plain types it emits a single assignment:
//
//	result.Name = r.ReadString().Get()
//
// For Option[T] it branches on r.IsNull() so JSON `null` decodes to
// None[T]() and any other JSON value decodes to Some[T](read value). A
// missing field never reaches this code path — the switch in ReadFrom
// simply skips fields that are absent, leaving the zero Option value
// (None) in `result`. So JSON `null` and missing-field have the same
// observable result: `None[T]()`. This matches Scala's circe, Go's
// encoding/json (with *T), and most mainstream JSON libraries.
//
// Returns nil if the field type cannot be read (e.g. composite kinds),
// signalling the caller to emit a SkipValue fallback.
func (t *galaASTTransformer) genFieldReadStmts(fieldName string, fieldType transpiler.Type, isImmut bool) []ast.Stmt {
	lhs := &ast.SelectorExpr{X: ast.NewIdent("result"), Sel: ast.NewIdent(fieldName)}

	if inner, ok := optionInner(fieldType); ok {
		innerMethod := readMethodForBasicType(baseTypeName(unwrapGalaType(inner)))
		if innerMethod == "" {
			// Can't decode this inner type — skip the value.
			return nil
		}
		innerExpr := t.typeToExpr(inner)

		// Some[T]{}.Apply(r.ReadXxx().Get())
		someValue := &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   &ast.CompositeLit{Type: t.buildSomeType(innerExpr)},
				Sel: ast.NewIdent("Apply"),
			},
			Args: []ast.Expr{&ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X:   methodCall("r", innerMethod),
					Sel: ast.NewIdent("Get"),
				},
			}},
		}

		// None[T]{}.Apply()
		noneValue := &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   &ast.CompositeLit{Type: t.buildNoneType(innerExpr)},
				Sel: ast.NewIdent("Apply"),
			},
		}

		someAssignValue := ast.Expr(someValue)
		noneAssignValue := ast.Expr(noneValue)
		if isImmut {
			someAssignValue = &ast.CallExpr{Fun: t.stdIdent("NewImmutable"), Args: []ast.Expr{someAssignValue}}
			noneAssignValue = &ast.CallExpr{Fun: t.stdIdent("NewImmutable"), Args: []ast.Expr{noneAssignValue}}
		}

		// if r.IsNull() { r.ReadNull(); result.F = None[T]{}.Apply() } else { result.F = Some[T]{}.Apply(r.ReadX().Get()) }
		thenBody := &ast.BlockStmt{List: []ast.Stmt{
			exprStmt(methodCall("r", "ReadNull")),
			&ast.AssignStmt{Lhs: []ast.Expr{lhs}, Tok: token.ASSIGN, Rhs: []ast.Expr{noneAssignValue}},
		}}
		elseBody := &ast.BlockStmt{List: []ast.Stmt{
			&ast.AssignStmt{Lhs: []ast.Expr{lhs}, Tok: token.ASSIGN, Rhs: []ast.Expr{someAssignValue}},
		}}
		return []ast.Stmt{&ast.IfStmt{
			Cond: methodCall("r", "IsNull"),
			Body: thenBody,
			Else: elseBody,
		}}
	}

	baseType := unwrapGalaType(fieldType)
	method := readMethodForBasicType(baseTypeName(baseType))
	if method == "" {
		return nil
	}
	// r.ReadXxx().Get()
	var readExpr ast.Expr = &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("r"), Sel: ast.NewIdent(method)}},
			Sel: ast.NewIdent("Get"),
		},
	}
	if isImmut {
		readExpr = &ast.CallExpr{Fun: t.stdIdent("NewImmutable"), Args: []ast.Expr{readExpr}}
	}
	return []ast.Stmt{&ast.AssignStmt{
		Lhs: []ast.Expr{lhs},
		Tok: token.ASSIGN,
		Rhs: []ast.Expr{readExpr},
	}}
}

// optionInner detects Option[T] field types and returns the inner T.
// Recognises both the bare name `Option` (when std is dot-imported, the
// common case) and the fully-qualified `std.Option`.
func optionInner(typ transpiler.Type) (transpiler.Type, bool) {
	// Peel Immutable[Option[T]] -> Option[T] first (val-field).
	t := unwrapGalaType(typ)
	gt, ok := t.(transpiler.GenericType)
	if !ok || len(gt.Params) != 1 {
		return nil, false
	}
	base := gt.Base.BaseName()
	if base == "Option" || base == "std.Option" {
		return gt.Params[0], true
	}
	return nil, false
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
		// B14: unknown Type kind — new Type implementations must be added here
		// so codec generation does not silently fall through.
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

// registerStructMetaTypeMeta registers type metadata for a generated StructMeta struct.
// This allows the transpiler to resolve return types of ReadFrom, enabling
// Immutable field unwrapping in string interpolation and type inference.
func (t *galaASTTransformer) registerStructMetaTypeMeta(genName, targetTypeName string) {
	targetType := transpiler.BasicType{Name: targetTypeName}
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
			"FieldType": {
				Name:       "FieldType",
				ParamTypes: []transpiler.Type{transpiler.BasicType{Name: "int"}},
				ReturnType: transpiler.BasicType{Name: "string"},
			},
			"ReadFrom": {
				Name:       "ReadFrom",
				ReturnType: targetType,
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

func writeMethodForBasicType(name string) string {
	switch name {
	case "string":
		return "WriteString"
	case "int", "int64":
		return "WriteInt"
	case "float64":
		return "WriteFloat64"
	case "bool":
		return "WriteBool"
	default:
		return "WriteNull"
	}
}

func readMethodForBasicType(name string) string {
	switch name {
	case "string":
		return "ReadString"
	case "int", "int64":
		return "ReadInt"
	case "float64":
		return "ReadFloat64"
	case "bool":
		return "ReadBool"
	default:
		return ""
	}
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

func shortVarDecl(name string, value ast.Expr) *ast.AssignStmt {
	return &ast.AssignStmt{
		Lhs: []ast.Expr{ast.NewIdent(name)},
		Tok: token.DEFINE,
		Rhs: []ast.Expr{value},
	}
}

func exprStmt(expr ast.Expr) *ast.ExprStmt {
	return &ast.ExprStmt{X: expr}
}

func methodCall(receiver, method string) *ast.CallExpr {
	return &ast.CallExpr{
		Fun: &ast.SelectorExpr{X: ast.NewIdent(receiver), Sel: ast.NewIdent(method)},
	}
}
