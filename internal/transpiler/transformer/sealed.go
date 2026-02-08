package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

type sealedVariantInfo struct {
	name     string
	fields   []sealedFieldInfo
	tagConst string // e.g., "_Shape_Circle"
	tagValue int    // iota index
}

type sealedFieldInfo struct {
	name            string
	structFieldName string // Go struct field name (may be prefixed with variant name to avoid collisions)
	typeCtx         grammar.ITypeContext
	isRecursive     bool // true if field type references the parent sealed type (requires pointer indirection)
}

func (t *galaASTTransformer) transformSealedTypeDeclaration(ctx *grammar.SealedTypeDeclarationContext) ([]ast.Decl, error) {
	name := ctx.Identifier().GetText()
	t.pushScope()
	defer t.popScope()

	// Process Type Parameters
	var tParams *ast.FieldList
	if ctx.TypeParameters() != nil {
		var err error
		tParams, err = t.transformTypeParameters(ctx.TypeParameters().(*grammar.TypeParametersContext))
		if err != nil {
			return nil, err
		}
		for _, field := range tParams.List {
			for _, n := range field.Names {
				t.activeTypeParams[n.Name] = true
			}
		}
		defer func() {
			for _, field := range tParams.List {
				for _, n := range field.Names {
					delete(t.activeTypeParams, n.Name)
				}
			}
		}()
	}

	// Parse all variants (two passes: first collect, then resolve field name conflicts)
	var variants []sealedVariantInfo
	allFieldTypes := make(map[string]map[string]bool) // field name -> set of type texts (for conflict detection)
	for i, caseCtx := range ctx.AllSealedCase() {
		sc := caseCtx.(*grammar.SealedCaseContext)
		vi := sealedVariantInfo{
			name:     sc.Identifier().GetText(),
			tagConst: fmt.Sprintf("_%s_%s", name, sc.Identifier().GetText()),
			tagValue: i,
		}

		if sc.SealedCaseFieldList() != nil {
			fieldList := sc.SealedCaseFieldList().(*grammar.SealedCaseFieldListContext)
			for _, fieldCtx := range fieldList.AllSealedCaseField() {
				fc := fieldCtx.(*grammar.SealedCaseFieldContext)
				fieldName := fc.Identifier().GetText()
				fieldTypeText := fc.Type_().GetText()
				if allFieldTypes[fieldName] == nil {
					allFieldTypes[fieldName] = make(map[string]bool)
				}
				allFieldTypes[fieldName][fieldTypeText] = true
				vi.fields = append(vi.fields, sealedFieldInfo{
					name:        fieldName,
					typeCtx:     fc.Type_(),
					isRecursive: isSelfReferentialSealedField(fieldTypeText, name),
				})
			}
		}
		variants = append(variants, vi)
	}

	// Detect field name conflicts: a field name has a conflict when it appears across
	// variants with different types. In that case, prefix all instances with the variant name.
	conflictingFields := make(map[string]bool) // field names that need variant-name prefixing
	for fieldName, typeSet := range allFieldTypes {
		if len(typeSet) > 1 {
			conflictingFields[fieldName] = true
		}
	}

	// Second pass: compute structFieldName for each field in each variant
	for vi := range variants {
		for fi := range variants[vi].fields {
			f := &variants[vi].fields[fi]
			if conflictingFields[f.name] {
				f.structFieldName = variants[vi].name + f.name // e.g., "AddLeft", "SubLeft"
			} else {
				f.structFieldName = f.name // no conflict, use original name
			}
		}
	}

	var decls []ast.Decl

	// 1. Generate parent struct with all variant fields merged + _variant
	parentFields := &ast.FieldList{}
	var fieldNames []string
	var immutFlags []bool

	if t.structFieldTypes[name] == nil {
		t.structFieldTypes[name] = make(map[string]transpiler.Type)
	}

	addedFields := make(map[string]bool)    // track struct field names already added to parent struct
	recursiveFields := make(map[string]bool) // track which struct field names are self-referential
	for _, vi := range variants {
		for _, f := range vi.fields {
			if addedFields[f.structFieldName] {
				continue // shared field already added by a previous variant
			}
			addedFields[f.structFieldName] = true

			typ, err := t.transformType(f.typeCtx)
			if err != nil {
				return nil, err
			}
			fType := t.exprToType(typ)

			var fieldType ast.Expr
			if f.isRecursive {
				// Self-referential field: use pointer to break recursive value type.
				// In Go, Immutable[Expr] where Expr contains Immutable[Expr] is illegal
				// because it creates an infinitely-sized value type.
				// Instead, store as *Expr (pointer to parent type).
				recursiveFields[f.structFieldName] = true
				fieldType = &ast.StarExpr{X: typ}
			} else {
				// Normal field: wrap in Immutable[T]
				fieldType = &ast.IndexExpr{
					X:     t.stdIdent("Immutable"),
					Index: typ,
				}
			}

			parentFields.List = append(parentFields.List, &ast.Field{
				Names: []*ast.Ident{ast.NewIdent(f.structFieldName)},
				Type:  fieldType,
			})

			fieldNames = append(fieldNames, f.structFieldName)
			if f.isRecursive {
				immutFlags = append(immutFlags, false) // pointer field, not Immutable-wrapped
			} else {
				immutFlags = append(immutFlags, true)
				t.immutFields[f.structFieldName] = true
			}
			t.structFieldTypes[name][f.structFieldName] = fType
		}
	}

	// Add _variant field (not immutable-wrapped, it's a plain uint8)
	parentFields.List = append(parentFields.List, &ast.Field{
		Names: []*ast.Ident{ast.NewIdent("_variant")},
		Type:  ast.NewIdent("uint8"),
	})
	fieldNames = append(fieldNames, "_variant")
	immutFlags = append(immutFlags, false) // _variant is not Immutable-wrapped
	t.structFieldTypes[name]["_variant"] = transpiler.BasicType{Name: "uint8"}

	t.structFields[name] = fieldNames
	t.structImmutFields[name] = immutFlags

	typeSpec := &ast.TypeSpec{
		Name:       ast.NewIdent(name),
		TypeParams: tParams,
		Type:       &ast.StructType{Fields: parentFields},
	}

	decls = append(decls, &ast.GenDecl{
		Tok:   token.TYPE,
		Specs: []ast.Spec{typeSpec},
	})

	// 2. Generate variant tag constants: const ( _Shape_Circle uint8 = iota; _Shape_Rectangle; _Shape_Point )
	var constSpecs []ast.Spec
	for i, vi := range variants {
		spec := &ast.ValueSpec{
			Names: []*ast.Ident{ast.NewIdent(vi.tagConst)},
		}
		if i == 0 {
			spec.Type = ast.NewIdent("uint8")
			spec.Values = []ast.Expr{ast.NewIdent("iota")}
		}
		constSpecs = append(constSpecs, spec)
	}
	decls = append(decls, &ast.GenDecl{
		Tok:    token.CONST,
		Lparen: 1, // force parenthesized form
		Specs:  constSpecs,
	})

	// 3. Generate companion structs and methods for each variant
	for _, vi := range variants {
		companionDecls, err := t.generateSealedCompanion(name, vi, tParams, recursiveFields)
		if err != nil {
			return nil, err
		}
		decls = append(decls, companionDecls...)
	}

	// 4. Generate IsXxx() methods on parent
	for _, vi := range variants {
		isMethod := t.generateSealedIsMethod(name, vi, tParams)
		decls = append(decls, isMethod)
	}

	// 5. Generate Copy, Equal methods on parent
	copyMethod, err := t.generateCopyMethod(name, parentFields, tParams)
	if err != nil {
		return nil, err
	}
	decls = append(decls, copyMethod)

	equalMethod, err := t.generateEqualMethod(name, parentFields, tParams)
	if err != nil {
		return nil, err
	}
	decls = append(decls, equalMethod)

	// 6. Generate String() method on parent
	stringMethod := t.generateSealedStringMethod(name, variants, tParams, recursiveFields)
	decls = append(decls, stringMethod)

	// 7. For generic sealed types, generate InstanceMarker
	if tParams != nil {
		interfaceDecl, markerMethod := t.generateInstanceMarker(name, tParams)
		decls = append(decls, interfaceDecl, markerMethod)
	}

	return decls, nil
}

// generateSealedCompanion generates companion struct + Apply + Unapply for a single variant.
func (t *galaASTTransformer) generateSealedCompanion(parentName string, vi sealedVariantInfo, tParams *ast.FieldList, recursiveFields map[string]bool) ([]ast.Decl, error) {
	var decls []ast.Decl

	// Empty companion struct (with same type parameters as parent)
	companionSpec := &ast.TypeSpec{
		Name:       ast.NewIdent(vi.name),
		TypeParams: tParams,
		Type:       &ast.StructType{Fields: &ast.FieldList{}},
	}
	decls = append(decls, &ast.GenDecl{
		Tok:   token.TYPE,
		Specs: []ast.Spec{companionSpec},
	})

	// Register companion struct fields (empty)
	t.structFields[vi.name] = nil
	t.structImmutFields[vi.name] = nil
	if t.structFieldTypes[vi.name] == nil {
		t.structFieldTypes[vi.name] = make(map[string]transpiler.Type)
	}

	// Build receiver type expression
	companionRecvType := t.buildGenericTypeExpr(vi.name, tParams)
	parentType := t.buildGenericTypeExpr(parentName, tParams)

	// Apply method
	applyMethod, err := t.generateSealedApply(parentName, vi, companionRecvType, parentType, tParams, recursiveFields)
	if err != nil {
		return nil, err
	}
	decls = append(decls, applyMethod)

	// Unapply method
	unapplyMethod, err := t.generateSealedUnapply(parentName, vi, companionRecvType, parentType, tParams, recursiveFields)
	if err != nil {
		return nil, err
	}
	decls = append(decls, unapplyMethod)

	return decls, nil
}

// buildGenericTypeExpr creates a type expression like `Name` or `Name[T]` or `Name[T, U]`.
func (t *galaASTTransformer) buildGenericTypeExpr(name string, tParams *ast.FieldList) ast.Expr {
	if tParams == nil || len(tParams.List) == 0 {
		return ast.NewIdent(name)
	}
	var indices []ast.Expr
	for _, p := range tParams.List {
		for _, n := range p.Names {
			indices = append(indices, ast.NewIdent(n.Name))
		}
	}
	if len(indices) == 1 {
		return &ast.IndexExpr{X: ast.NewIdent(name), Index: indices[0]}
	}
	return &ast.IndexListExpr{X: ast.NewIdent(name), Indices: indices}
}

// generateSealedApply generates the Apply method for a sealed type companion.
// For recursive fields (self-referential), it uses pointer: Field: &value instead of NewImmutable(value).
func (t *galaASTTransformer) generateSealedApply(parentName string, vi sealedVariantInfo, companionType, parentType ast.Expr, tParams *ast.FieldList, recursiveFields map[string]bool) (*ast.FuncDecl, error) {
	// Parameters: the variant's fields
	params := &ast.FieldList{}
	for _, f := range vi.fields {
		typ, err := t.transformType(f.typeCtx)
		if err != nil {
			return nil, err
		}
		params.List = append(params.List, &ast.Field{
			Names: []*ast.Ident{ast.NewIdent(f.name)},
			Type:  typ,
		})
	}

	// Body: construct parent struct
	// Note: struct keys use structFieldName (may be prefixed), but parameter names use the original name
	var elts []ast.Expr
	for _, f := range vi.fields {
		var valueExpr ast.Expr
		if recursiveFields[f.structFieldName] {
			// Recursive field: store as pointer &value
			valueExpr = &ast.UnaryExpr{
				Op: token.AND,
				X:  ast.NewIdent(f.name), // param name is the original (user-facing) name
			}
		} else {
			// Normal field: wrap in NewImmutable
			valueExpr = &ast.CallExpr{
				Fun:  t.stdIdent("NewImmutable"),
				Args: []ast.Expr{ast.NewIdent(f.name)}, // param name is the original name
			}
		}
		elts = append(elts, &ast.KeyValueExpr{
			Key:   ast.NewIdent(f.structFieldName), // struct field may be prefixed
			Value: valueExpr,
		})
	}
	elts = append(elts, &ast.KeyValueExpr{
		Key:   ast.NewIdent("_variant"),
		Value: ast.NewIdent(vi.tagConst),
	})

	return &ast.FuncDecl{
		Recv: &ast.FieldList{
			List: []*ast.Field{
				{
					Names: []*ast.Ident{ast.NewIdent("_")},
					Type:  companionType,
				},
			},
		},
		Name: ast.NewIdent("Apply"),
		Type: &ast.FuncType{
			Params: params,
			Results: &ast.FieldList{
				List: []*ast.Field{{Type: parentType}},
			},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ReturnStmt{
					Results: []ast.Expr{
						&ast.CompositeLit{
							Type: parentType,
							Elts: elts,
						},
					},
				},
			},
		},
	}, nil
}

// sealedFieldAccessExpr generates the expression to read a sealed type field value.
// For recursive (pointer) fields: *v.Field (dereference the pointer)
// For normal (Immutable) fields: v.Field.Get() (unwrap the Immutable)
func sealedFieldAccessExpr(paramName string, fieldName string, isRecursive bool) ast.Expr {
	if isRecursive {
		// Dereference pointer: *v.Field
		return &ast.StarExpr{
			X: &ast.SelectorExpr{
				X:   ast.NewIdent(paramName),
				Sel: ast.NewIdent(fieldName),
			},
		}
	}
	// Normal Immutable field: v.Field.Get()
	return &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X: &ast.SelectorExpr{
				X:   ast.NewIdent(paramName),
				Sel: ast.NewIdent(fieldName),
			},
			Sel: ast.NewIdent("Get"),
		},
	}
}

// generateSealedUnapply generates the Unapply method for a sealed type companion.
// 0-field variant: returns bool
// 1-field variant: returns Option[FieldType]
// 2+-field variant: returns Option[Tuple[...]]
func (t *galaASTTransformer) generateSealedUnapply(parentName string, vi sealedVariantInfo, companionType, parentType ast.Expr, tParams *ast.FieldList, recursiveFields map[string]bool) (*ast.FuncDecl, error) {
	paramName := "v"

	switch len(vi.fields) {
	case 0:
		// Returns bool: v._variant == tag
		return &ast.FuncDecl{
			Recv: &ast.FieldList{
				List: []*ast.Field{
					{Names: []*ast.Ident{ast.NewIdent("_")}, Type: companionType},
				},
			},
			Name: ast.NewIdent("Unapply"),
			Type: &ast.FuncType{
				Params: &ast.FieldList{
					List: []*ast.Field{
						{Names: []*ast.Ident{ast.NewIdent(paramName)}, Type: parentType},
					},
				},
				Results: &ast.FieldList{
					List: []*ast.Field{{Type: ast.NewIdent("bool")}},
				},
			},
			Body: &ast.BlockStmt{
				List: []ast.Stmt{
					&ast.ReturnStmt{
						Results: []ast.Expr{
							&ast.BinaryExpr{
								X: &ast.SelectorExpr{
									X:   ast.NewIdent(paramName),
									Sel: ast.NewIdent("_variant"),
								},
								Op: token.EQL,
								Y:  ast.NewIdent(vi.tagConst),
							},
						},
					},
				},
			},
		}, nil

	case 1:
		// Returns Option[FieldType]: if matches, Some(v.Field.Get()) or Some(*v.Field), else None
		f := vi.fields[0]
		fieldType, err := t.transformType(f.typeCtx)
		if err != nil {
			return nil, err
		}
		optionType := &ast.IndexExpr{
			X:     t.stdIdent("Option"),
			Index: fieldType,
		}

		fieldAccess := sealedFieldAccessExpr(paramName, f.structFieldName, recursiveFields[f.structFieldName])

		return &ast.FuncDecl{
			Recv: &ast.FieldList{
				List: []*ast.Field{
					{Names: []*ast.Ident{ast.NewIdent("_")}, Type: companionType},
				},
			},
			Name: ast.NewIdent("Unapply"),
			Type: &ast.FuncType{
				Params: &ast.FieldList{
					List: []*ast.Field{
						{Names: []*ast.Ident{ast.NewIdent(paramName)}, Type: parentType},
					},
				},
				Results: &ast.FieldList{
					List: []*ast.Field{{Type: optionType}},
				},
			},
			Body: &ast.BlockStmt{
				List: []ast.Stmt{
					&ast.IfStmt{
						Cond: &ast.BinaryExpr{
							X: &ast.SelectorExpr{
								X:   ast.NewIdent(paramName),
								Sel: ast.NewIdent("_variant"),
							},
							Op: token.EQL,
							Y:  ast.NewIdent(vi.tagConst),
						},
						Body: &ast.BlockStmt{
							List: []ast.Stmt{
								&ast.ReturnStmt{
									Results: []ast.Expr{
										&ast.CallExpr{
											Fun: &ast.SelectorExpr{
												X: &ast.CompositeLit{
													Type: t.buildSomeType(fieldType),
												},
												Sel: ast.NewIdent("Apply"),
											},
											Args: []ast.Expr{fieldAccess},
										},
									},
								},
							},
						},
					},
					&ast.ReturnStmt{
						Results: []ast.Expr{
							&ast.CallExpr{
								Fun: &ast.SelectorExpr{
									X: &ast.CompositeLit{
										Type: t.buildNoneType(fieldType),
									},
									Sel: ast.NewIdent("Apply"),
								},
							},
						},
					},
				},
			},
		}, nil

	default:
		// Returns Option[TupleN[...]]
		var tupleFieldTypes []ast.Expr
		for _, f := range vi.fields {
			typ, err := t.transformType(f.typeCtx)
			if err != nil {
				return nil, err
			}
			tupleFieldTypes = append(tupleFieldTypes, typ)
		}

		tupleName := fmt.Sprintf("Tuple%d", len(vi.fields))
		if len(vi.fields) == 2 {
			tupleName = "Tuple"
		}
		var tupleType ast.Expr
		if len(tupleFieldTypes) == 1 {
			tupleType = &ast.IndexExpr{
				X:     t.stdIdent(tupleName),
				Index: tupleFieldTypes[0],
			}
		} else {
			tupleType = &ast.IndexListExpr{
				X:       t.stdIdent(tupleName),
				Indices: tupleFieldTypes,
			}
		}

		optionType := &ast.IndexExpr{
			X:     t.stdIdent("Option"),
			Index: tupleType,
		}

		// Build tuple constructor args: v.Field.Get() or *v.Field for recursive fields
		var tupleArgs []ast.Expr
		for _, f := range vi.fields {
			tupleArgs = append(tupleArgs, sealedFieldAccessExpr(paramName, f.structFieldName, recursiveFields[f.structFieldName]))
		}

		// Some[TupleN[...]](TupleN[...]{V1: ..., V2: ...})
		var tupleElts []ast.Expr
		for i := range vi.fields {
			tupleElts = append(tupleElts, &ast.KeyValueExpr{
				Key: ast.NewIdent(fmt.Sprintf("V%d", i+1)),
				Value: &ast.CallExpr{
					Fun:  t.stdIdent("NewImmutable"),
					Args: []ast.Expr{tupleArgs[i]},
				},
			})
		}

		return &ast.FuncDecl{
			Recv: &ast.FieldList{
				List: []*ast.Field{
					{Names: []*ast.Ident{ast.NewIdent("_")}, Type: companionType},
				},
			},
			Name: ast.NewIdent("Unapply"),
			Type: &ast.FuncType{
				Params: &ast.FieldList{
					List: []*ast.Field{
						{Names: []*ast.Ident{ast.NewIdent(paramName)}, Type: parentType},
					},
				},
				Results: &ast.FieldList{
					List: []*ast.Field{{Type: optionType}},
				},
			},
			Body: &ast.BlockStmt{
				List: []ast.Stmt{
					&ast.IfStmt{
						Cond: &ast.BinaryExpr{
							X: &ast.SelectorExpr{
								X:   ast.NewIdent(paramName),
								Sel: ast.NewIdent("_variant"),
							},
							Op: token.EQL,
							Y:  ast.NewIdent(vi.tagConst),
						},
						Body: &ast.BlockStmt{
							List: []ast.Stmt{
								&ast.ReturnStmt{
									Results: []ast.Expr{
										&ast.CallExpr{
											Fun: &ast.SelectorExpr{
												X: &ast.CompositeLit{
													Type: t.buildSomeType(tupleType),
												},
												Sel: ast.NewIdent("Apply"),
											},
											Args: []ast.Expr{
												&ast.CompositeLit{
													Type: tupleType,
													Elts: tupleElts,
												},
											},
										},
									},
								},
							},
						},
					},
					&ast.ReturnStmt{
						Results: []ast.Expr{
							&ast.CallExpr{
								Fun: &ast.SelectorExpr{
									X: &ast.CompositeLit{
										Type: t.buildNoneType(tupleType),
									},
									Sel: ast.NewIdent("Apply"),
								},
							},
						},
					},
				},
			},
		}, nil
	}
}

// buildSomeType builds the type expression Some[T]{}.
func (t *galaASTTransformer) buildSomeType(elemType ast.Expr) ast.Expr {
	return &ast.IndexExpr{
		X:     t.stdIdent("Some"),
		Index: elemType,
	}
}

// buildNoneType builds the type expression None[T]{}.
func (t *galaASTTransformer) buildNoneType(elemType ast.Expr) ast.Expr {
	return &ast.IndexExpr{
		X:     t.stdIdent("None"),
		Index: elemType,
	}
}

// generateSealedIsMethod generates a private isXxx() method on the parent sealed type.
// e.g., func (s Shape) isCircle() bool { return s._variant == _Shape_Circle }
func (t *galaASTTransformer) generateSealedIsMethod(parentName string, vi sealedVariantInfo, tParams *ast.FieldList) *ast.FuncDecl {
	parentType := t.buildGenericTypeExpr(parentName, tParams)

	return &ast.FuncDecl{
		Recv: &ast.FieldList{
			List: []*ast.Field{
				{
					Names: []*ast.Ident{ast.NewIdent("s")},
					Type:  parentType,
				},
			},
		},
		Name: ast.NewIdent("is" + vi.name),
		Type: &ast.FuncType{
			Params: &ast.FieldList{},
			Results: &ast.FieldList{
				List: []*ast.Field{{Type: ast.NewIdent("bool")}},
			},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ReturnStmt{
					Results: []ast.Expr{
						&ast.BinaryExpr{
							X: &ast.SelectorExpr{
								X:   ast.NewIdent("s"),
								Sel: ast.NewIdent("_variant"),
							},
							Op: token.EQL,
							Y:  ast.NewIdent(vi.tagConst),
						},
					},
				},
			},
		},
	}
}

// generateSealedStringMethod generates a String() method on the parent sealed type.
// Each variant case returns "VariantName(field1, field2, ...)" or "VariantName()" for 0-field variants.
func (t *galaASTTransformer) generateSealedStringMethod(parentName string, variants []sealedVariantInfo, tParams *ast.FieldList, recursiveFields map[string]bool) *ast.FuncDecl {
	parentType := t.buildGenericTypeExpr(parentName, tParams)

	var cases []ast.Stmt
	needsFmt := false

	for _, vi := range variants {
		var retExpr ast.Expr

		if len(vi.fields) == 0 {
			// Return "VariantName()"
			retExpr = &ast.BasicLit{
				Kind:  token.STRING,
				Value: fmt.Sprintf(`"%s()"`, vi.name),
			}
		} else {
			needsFmt = true
			// Return fmt.Sprintf("VariantName(%v, %v)", s.Field1.Get(), s.Field2.Get())
			// For recursive fields: *s.Field instead of s.Field.Get()
			var formatParts []string
			var args []ast.Expr
			for _, f := range vi.fields {
				formatParts = append(formatParts, "%v")
				args = append(args, sealedFieldAccessExpr("s", f.structFieldName, recursiveFields[f.structFieldName]))
			}
			formatStr := fmt.Sprintf(`"%s(%s)"`, vi.name, strings.Join(formatParts, ", "))
			allArgs := append([]ast.Expr{
				&ast.BasicLit{Kind: token.STRING, Value: formatStr},
			}, args...)

			retExpr = &ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X:   ast.NewIdent("fmt"),
					Sel: ast.NewIdent("Sprintf"),
				},
				Args: allArgs,
			}
		}

		cases = append(cases, &ast.CaseClause{
			List: []ast.Expr{ast.NewIdent(vi.tagConst)},
			Body: []ast.Stmt{
				&ast.ReturnStmt{Results: []ast.Expr{retExpr}},
			},
		})
	}

	// Default case
	cases = append(cases, &ast.CaseClause{
		List: nil, // nil = default
		Body: []ast.Stmt{
			&ast.ReturnStmt{
				Results: []ast.Expr{
					&ast.BasicLit{
						Kind:  token.STRING,
						Value: fmt.Sprintf(`"%s(<unknown>)"`, parentName),
					},
				},
			},
		},
	})

	if needsFmt {
		t.needsFmtImport = true
	}

	return &ast.FuncDecl{
		Recv: &ast.FieldList{
			List: []*ast.Field{
				{
					Names: []*ast.Ident{ast.NewIdent("s")},
					Type:  parentType,
				},
			},
		},
		Name: ast.NewIdent("String"),
		Type: &ast.FuncType{
			Params: &ast.FieldList{},
			Results: &ast.FieldList{
				List: []*ast.Field{{Type: ast.NewIdent("string")}},
			},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.SwitchStmt{
					Tag: &ast.SelectorExpr{
						X:   ast.NewIdent("s"),
						Sel: ast.NewIdent("_variant"),
					},
					Body: &ast.BlockStmt{List: cases},
				},
			},
		},
	}
}

// isSelfReferentialSealedField checks if a field type text references the parent sealed type.
// This handles direct references like "Expr" and generic references like "Tree[T]".
// When a sealed type field references the parent type, it must use pointer indirection
// to avoid illegal recursive value types in Go.
func isSelfReferentialSealedField(fieldTypeText, parentName string) bool {
	// Direct match: field type is exactly the parent name (e.g., "Expr")
	if fieldTypeText == parentName {
		return true
	}
	// Generic match: field type starts with "ParentName[" (e.g., "Tree[T]")
	if strings.HasPrefix(fieldTypeText, parentName+"[") {
		return true
	}
	return false
}
