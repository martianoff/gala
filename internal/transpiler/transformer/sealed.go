package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

type sealedVariantInfo struct {
	name     string
	fields   []sealedFieldInfo
	tagConst string // e.g., "_Shape_Circle"
	tagValue int    // the case's tag: 0 for the default case, else 1, 2, … in declaration order
}

type sealedFieldInfo struct {
	name            string
	structFieldName string // Go struct field name (may be prefixed with variant name to avoid collisions)
	typ             typeSrc
	isRecursive     bool // true if field type references the parent sealed type (requires pointer indirection)
}

func (t *galaASTTransformer) transformSealedTypeDeclaration(ctx *grammar.SealedTypeDeclarationContext) ([]ast.Decl, error) {
	return t.lowerSealedDecl(readSealedDecl(ctx))
}

// lowerSealedDecl lowers a sealed type declaration, read from either tree
// (sealed_source.go).
func (t *galaASTTransformer) lowerSealedDecl(d sealedDecl) ([]ast.Decl, error) {
	name := d.name
	if err := t.selfContainingTypeError(name, d.nameAt); err != nil {
		return nil, err
	}
	t.pushScope()
	defer t.popScope()

	// Process Type Parameters
	var tParams *ast.FieldList
	if len(d.typeParams) > 0 {
		tParams = lowerTypeParams(d.typeParams)
		defer t.bindTypeParams(tParams.List...)()
	}

	// Parse all variants (two passes: first collect, then resolve field name conflicts)
	var variants []sealedVariantInfo
	// The default case takes tag 0, so a Go zero value of the type is that
	// case with zero fields. Without one, tags start at 1 and a zero value
	// is no case at all (see exhaustiveDefaultBody).
	nextTag := 1
	var defaultCase *sealedCaseDecl
	for ci := range d.cases {
		sc := &d.cases[ci]
		vi := sealedVariantInfo{
			name:     sc.name,
			tagConst: fmt.Sprintf("_%s_%s", name, sc.name),
		}
		if sc.isDefault {
			if defaultCase != nil {
				line, col := sc.at.start()
				return nil, galaerr.NewCodedSemanticError(galaerr.CodeInvalidDefaultCase,
					line, col,
					fmt.Sprintf("sealed type %q marks both %q and %q as its default case", name, defaultCase.name, vi.name),
					"keep `default` on one case: the default case is the type's zero value")
			}
			defaultCase = sc
		} else {
			vi.tagValue = nextTag
			nextTag++
		}

		for _, fc := range sc.fields {
			fieldName := fc.name
			recursive := isSelfReferentialSealedField(fc.typ.typeText(), name)
			if recursive && sc.isDefault {
				// The field is held through a pointer, which a zero
				// value leaves nil.
				line, col := fc.at.start()
				return nil, galaerr.NewCodedSemanticError(galaerr.CodeInvalidDefaultCase,
					line, col,
					fmt.Sprintf("the default case %q of sealed type %q has a field %q of type %s itself, which its zero value cannot hold", vi.name, name, fieldName, name),
					"make a case without such a field the default, or drop `default`")
			}
			vi.fields = append(vi.fields, sealedFieldInfo{
				name:        fieldName,
				typ:         fc.typ,
				isRecursive: recursive,
			})
		}
		if n := len(vi.fields); n > transpiler.MaxTupleArity {
			line, col := sc.nameAt.start()
			return nil, galaerr.NewCodedSemanticError(galaerr.CodeSealedVariantTooWide,
				line, col,
				fmt.Sprintf("sealed variant %q has %d fields; a variant can have at most %d", vi.name, n, transpiler.MaxTupleArity),
				"group related fields into a struct and give the variant a field of that type")
		}
		variants = append(variants, vi)
	}

	// Name each case field in Go as the analyzer laid the type out
	// (layoutSealedFields), so this struct and every reader of the type's
	// metadata agree.
	meta := t.getTypeMeta(name)
	if meta == nil || !meta.IsSealed || len(meta.SealedVariants) != len(variants) {
		return nil, t.semanticErrorAtNode(d.at, fmt.Sprintf("internal error: no metadata for sealed type %s", name))
	}
	for vi := range variants {
		goNames := meta.SealedVariants[vi].GoFieldNames
		if len(goNames) != len(variants[vi].fields) {
			return nil, t.semanticErrorAtNode(d.at, fmt.Sprintf("internal error: no field layout for case %s of sealed type %s", variants[vi].name, name))
		}
		for fi := range variants[vi].fields {
			variants[vi].fields[fi].structFieldName = goNames[fi]
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
	funcFields := make(map[string]bool)      // track which struct field names hold func-typed values
	for _, vi := range variants {
		for _, f := range vi.fields {
			if addedFields[f.structFieldName] {
				continue // shared field already added by a previous variant
			}
			addedFields[f.structFieldName] = true

			typ, err := t.lowerType(f.typ)
			if err != nil {
				return nil, err
			}
			if _, isFunc := typ.(*ast.FuncType); isFunc {
				// Func-valued fields can't be formatted with %v (go vet rejects
				// fmt.Sprintf("%v", funcValue)). The String() method must substitute
				// a placeholder string for them instead.
				funcFields[f.structFieldName] = true
			}
			fType := t.astTypeToTranspilerType(typ)

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

	// 2. Generate variant tag constants: const ( _Shape_Circle uint8 = 1; _Shape_Rectangle uint8 = 2 )
	var constSpecs []ast.Spec
	for _, vi := range variants {
		constSpecs = append(constSpecs, &ast.ValueSpec{
			Names:  []*ast.Ident{ast.NewIdent(vi.tagConst)},
			Type:   ast.NewIdent("uint8"),
			Values: []ast.Expr{&ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(vi.tagValue)}},
		})
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

	// 4. Generate isXxx() methods on parent, and IsXxx() for Go callers
	for _, vi := range variants {
		decls = append(decls, t.generateSealedIsMethod(name, "is"+vi.name, "", vi, tParams))
	}
	decls = append(decls, t.sealedGoPredicates(name, variants, parentFields, tParams)...)

	// 5. Generate Copy, Equal methods on parent — skip if user-defined
	hasCopy, hasEqual, _ := t.userDefinedMethodFlags(name)

	if !hasCopy {
		copyMethod, err := t.generateCopyMethod(name, parentFields, tParams)
		if err != nil {
			return nil, err
		}
		decls = append(decls, copyMethod)
	}

	if !hasEqual {
		equalMethod, err := t.generateEqualMethod(name, parentFields, tParams)
		if err != nil {
			return nil, err
		}
		decls = append(decls, equalMethod)
	}

	// 6. Generate String() method on parent
	stringMethod := t.generateSealedStringMethod(name, variants, tParams, recursiveFields, funcFields)
	decls = append(decls, stringMethod)

	// 7. For generic sealed types, generate InstanceMarker
	if tParams != nil {
		interfaceDecl, markerMethod := t.generateInstanceMarker(name, tParams)
		decls = append(decls, interfaceDecl, markerMethod)
	}

	// 8. Constructors for Go callers, and the doc comment naming them.
	line, col := d.at.start()
	ctors, guidance, err := t.sealedGoConstructors(name, variants, tParams, decls, line, col)
	if err != nil {
		return nil, err
	}
	if decl, _ := findTypeSpec(decls, name); decl != nil && guidance != "" {
		decl.Doc = docCommentGroup(guidance)
	}
	decls = append(decls, ctors...)

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
		typ, err := t.lowerType(f.typ)
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
//
// Return-type contract (intentional asymmetry):
//
//	 0-field variant  -> bool             (guard extractor — nothing to bind)
//	 1-field variant  -> Option[T]        (extracts a single field)
//	 2+-field variant -> Option[TupleN]   (extracts into a tuple)
//
// The 0-field case returns `bool` rather than `Option[Unit]` because there is
// no payload to wrap and unwrap — matching it is purely a tag test, and the
// pattern-match codegen in patterns.go recognises the bool-return form as the
// guard-extractor shape. This asymmetry is load-bearing: changing 0-field to
// Option[Unit] would cascade through generateDirectUnapplyPattern and the
// isDirectUnapplyReturnType check. Keep the three forms distinct and ensure
// patterns.go handles each explicitly.
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
		fieldType, err := t.lowerType(f.typ)
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
			typ, err := t.lowerType(f.typ)
			if err != nil {
				return nil, err
			}
			tupleFieldTypes = append(tupleFieldTypes, typ)
		}

		tupleName, _ := transpiler.TupleArityName(len(vi.fields))
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

// sealedGoPredicates returns, for each exported case of the exported sealed
// type parentName, the method Go callers test it with: `d.IsPercent()`. A
// predicate is left out when the type declares a method or has a field of its
// name (a case field named like a predicate keeps its name; see
// layoutSealedFields).
func (t *galaASTTransformer) sealedGoPredicates(parentName string, variants []sealedVariantInfo, parentFields *ast.FieldList, tParams *ast.FieldList) []ast.Decl {
	if !ast.IsExported(parentName) {
		return nil
	}
	names := make([]string, 0, len(variants))
	for _, vi := range variants {
		names = append(names, "Is"+vi.name)
	}
	declared := t.declaresMethods(parentName, names...)
	for _, f := range parentFields.List {
		for _, n := range f.Names {
			declared[n.Name] = true
		}
	}
	var out []ast.Decl
	for i, vi := range variants {
		name := names[i]
		if !ast.IsExported(vi.name) || declared[name] {
			continue
		}
		doc := fmt.Sprintf("%s reports whether s is the %s case of %s.", name, vi.name, parentName)
		out = append(out, t.generateSealedIsMethod(parentName, name, doc, vi, tParams))
	}
	return out
}

// generateSealedIsMethod generates the method name testing whether the parent
// sealed type is case vi, with doc comment doc ("" for none).
// e.g., func (s Shape) isCircle() bool { return s._variant == _Shape_Circle }
func (t *galaASTTransformer) generateSealedIsMethod(parentName, name, doc string, vi sealedVariantInfo, tParams *ast.FieldList) *ast.FuncDecl {
	parentType := t.buildGenericTypeExpr(parentName, tParams)

	return &ast.FuncDecl{
		Doc: docCommentGroup(doc),
		Recv: &ast.FieldList{
			List: []*ast.Field{
				{
					Names: []*ast.Ident{ast.NewIdent("s")},
					Type:  parentType,
				},
			},
		},
		Name: ast.NewIdent(name),
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
//
// Func-typed fields are rendered with a literal "<fn>" placeholder rather than %v,
// because fmt.Sprintf("%v", funcValue) is flagged by go vet as "is a func value, not called".
func (t *galaASTTransformer) generateSealedStringMethod(parentName string, variants []sealedVariantInfo, tParams *ast.FieldList, recursiveFields map[string]bool, funcFields map[string]bool) *ast.FuncDecl {
	parentType := t.buildGenericTypeExpr(parentName, tParams)

	var cases []ast.Stmt

	for _, vi := range variants {
		var retExpr ast.Expr

		if len(vi.fields) == 0 {
			// Return "VariantName()"
			retExpr = &ast.BasicLit{
				Kind:  token.STRING,
				Value: fmt.Sprintf(`"%s()"`, vi.name),
			}
		} else {
			// Return fmt.Sprintf("VariantName(%v, %v)", s.Field1.Get(), s.Field2.Get())
			// For recursive fields: *s.Field instead of s.Field.Get()
			// For func-typed fields: emit a literal "<fn>" in the format string (no arg),
			// because go vet rejects %v-formatting a function value.
			var formatParts []string
			var args []ast.Expr
			for _, f := range vi.fields {
				if funcFields[f.structFieldName] {
					// Literal placeholder — no format verb, no argument.
					formatParts = append(formatParts, "<fn>")
					continue
				}
				formatParts = append(formatParts, "%v")
				args = append(args, sealedFieldAccessExpr("s", f.structFieldName, recursiveFields[f.structFieldName]))
			}
			formatStr := fmt.Sprintf(`"%s(%s)"`, vi.name, strings.Join(formatParts, ", "))
			if len(args) == 0 {
				// All fields are func-typed: no format verbs remain, use a plain string literal.
				retExpr = &ast.BasicLit{Kind: token.STRING, Value: formatStr}
			} else {
				allArgs := append([]ast.Expr{
					&ast.BasicLit{Kind: token.STRING, Value: formatStr},
				}, args...)

				retExpr = &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   t.fmtPackage(),
						Sel: ast.NewIdent("Sprintf"),
					},
					Args: allArgs,
				}
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

// sealedCaseParent returns the sealed parent type for a sealed-case type, or
// nil if `caseType` is not a known sealed-variant case. The parent type
// preserves the case type's type-argument bindings — e.g. given a case
// `MsgCmd[AppMsg]` of sealed type `Cmd[T]`, the result is `Cmd[AppMsg]`.
//
// Used by match arm result-type unification to widen a sealed-case arm to
// its parent when a sibling arm produced the parent type. Without this,
// `Tuple[A, MsgCmd[X]]` and `Tuple[A, Cmd[X]]` are treated as incompatible
// match-arm result types even though `MsgCmd[X]` is a value of `Cmd[X]`.
func (t *galaASTTransformer) sealedCaseParent(caseType transpiler.Type) transpiler.Type {
	if transpiler.IsUnusable(caseType) {
		return nil
	}
	// Extract the case's bare base name and any type arguments.
	caseBase := stripPackagePrefix(caseType.BaseName())
	var caseArgs []transpiler.Type
	if gen, ok := caseType.(transpiler.GenericType); ok {
		caseArgs = gen.Params
	}

	// A sealed case must be registered as a companion object whose target is
	// the parent sealed type: the companion of the case type's own package,
	// named by the type's base name, a metadata key (`yaml.AsIs`), not a
	// name as this file spells it. A case of a package with no qualified key
	// may still carry its package (`main.MsgCmd`), and its companion is
	// filed bare (companionOf). A bare base name resolves as this file
	// would, else to the one package's case of that name if exactly one
	// has it (a package this file imports by name); a qualified one that is
	// not filed has no companion here, never another package's.
	var companion *transpiler.CompanionObjectMetadata
	if pkg, bare := splitPackageQualifier(caseType.BaseName()); pkg != "" {
		companion = t.companionOf(pkg, bare)
	} else if companion = t.lookupCompanion(bare); companion == nil {
		companion = t.companionBySuffix(bare)
	}
	if companion == nil {
		return nil
	}
	parentBase := stripPackagePrefix(companion.TargetType)
	if parentBase == "" || parentBase == caseBase {
		return nil
	}

	// Confirm the parent is registered as a sealed type AND lists this case
	// as one of its variants. Without this guard a non-sealed companion
	// (e.g. a plain extractor type) could masquerade as a sealed case.
	parentMeta := t.getTypeMeta(parentBase)
	if parentMeta == nil && companion.Package != "" {
		parentMeta = t.getTypeMeta(companion.Package + "." + parentBase)
	}
	if parentMeta == nil || !parentMeta.IsSealed {
		return nil
	}
	variantMatches := false
	for _, v := range parentMeta.SealedVariants {
		if v.Name == caseBase {
			variantMatches = true
			break
		}
	}
	if !variantMatches {
		return nil
	}

	// Reconstruct the parent type with the case's bound type arguments.
	// The companion's TargetType may include a package prefix; preserve it
	// so the resulting GenericType prints with the same qualification as
	// other references to the parent type produce in the same scope.
	//
	// Special case: when the companion's package is the current package (or
	// the convenience markers "main"/"test" the analyzer applies to local
	// declarations), other inferred references print the type WITHOUT a
	// package prefix. Mirror that here so the widened parent stringifies
	// identically — otherwise the expected-type comparison at the call site
	// (`expected.String() == parent.String()`) would mismatch on package
	// qualification alone.
	var parentBaseType transpiler.Type
	pkg := companion.Package
	if pkg == "main" || pkg == "test" || pkg == t.packageName {
		pkg = ""
	}
	if pkg != "" {
		typeName := companion.TargetType
		if strings.HasPrefix(typeName, pkg+".") {
			typeName = typeName[len(pkg)+1:]
		}
		parentBaseType = transpiler.NamedType{Package: pkg, Name: typeName}
	} else {
		parentBaseType = transpiler.BasicType{Name: parentBase}
	}
	if len(caseArgs) > 0 && len(caseArgs) == len(parentMeta.TypeParams) {
		return transpiler.GenericType{Base: parentBaseType, Params: caseArgs}
	}
	if len(parentMeta.TypeParams) == 0 {
		return parentBaseType
	}
	// Generic parent but case had no/mismatched type args — surface the parent
	// without args; downstream substitution handles the mismatch.
	return parentBaseType
}

// goOnlyFieldError rejects reading by name a field of a sealed type that GALA
// code has no name for: its case tag, or a case field stored under a Go name
// of its case (see TypeMetadata.IsGalaField). Such a field holds a value
// only for its own case.
func (t *galaASTTransformer) goOnlyFieldError(xType transpiler.Type, selName string, line, col int) error {
	if xType == nil {
		return nil
	}
	meta := t.getTypeMeta(stripTypeNameDecorations(xType.String()))
	if meta == nil || meta.IsGalaField(selName) {
		return nil
	}
	if _, isMethod := meta.Methods[selName]; isMethod {
		return nil
	}
	hint := "test which case a value is with `match`"
	for _, v := range meta.SealedVariants {
		for i, goName := range v.GoFieldNames {
			if goName == selName {
				hint = fmt.Sprintf("read %s's field %s by matching the case: `case %s(...) => ...`", v.Name, v.FieldNames[i], v.Name)
			}
		}
	}
	return galaerr.NewCodedSemanticError(galaerr.CodeUnknownMethod, line, col,
		fmt.Sprintf("%s has no field or method %s", meta.Name, selName), hint)
}
