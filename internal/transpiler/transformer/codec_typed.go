package transformer

// Typed StructMeta codegen.
//
// This file produces the `EncodeFields` / `DecodeFields` / `FieldIsEmpty`
// methods on every `_StructMeta_T` type. The methods are fully typed end-to-end: no `any`, no
// runtime type assertions, no boxing in the hot path. They delegate
// formatting to a `FieldEncoder` / `FieldDecoder` implementation provided by
// the caller (defined in `std/meta.gala`).
//
// Supported value shapes, at any depth:
//   - Scalars: string, bool, int, int8..int64, uint, uint8..uint64, uintptr,
//     byte, float32, float64, rune — and any alias or Go named type over one
//     (`type Millis int64`, `time.Duration`)
//   - Immutable[T] for any supported T
//   - Option[T] for any supported T other than another Option
//     (None → null, Some(x) → encoded x)
//   - Array[T] / List[T] of any supported T
//   - HashMap[K, V] where K is string-shaped and V is any supported T
//   - Structs whose metadata we also emit (recursive dispatch)
//
// Anything else is rejected at compile time with GALA-E0050. A shape the
// codec cannot represent must never degrade to a silent `null` on encode or a
// zero value on decode: that loses data without any signal to the author.
//
// For nested struct dispatch the generated code calls the inner
// _StructMeta_X{}.EncodeFields / DecodeFields with a fresh per-type
// nameFn/omitFn/lookup derived from the `naming` parameter passed in by the
// caller. The naming convention (SnakeCase, CamelCase, AsIs, ...) propagates
// from the top-level codec into every level of nesting; Rename/Omit/OmitEmpty
// overrides only apply at the top level (the level where the user configured
// them) — nested fields use the naming-only mapping.

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// codecScalar describes how one scalar kind crosses the FieldEncoder /
// FieldDecoder boundary. The encoder and decoder work in a small set of wire
// types (goType); every other kind is converted to and from one of them, and
// narrowing on decode is range-checked by the decoder (ReadIntN / ReadUintN /
// ReadFloat32) rather than wrapped.
type codecScalar struct {
	goType string // the Go type the Write/Read method takes or returns
	write  string // FieldEncoder method
	read   string // FieldDecoder method
	bits   int    // bit-size argument to read; -1 when read takes none
}

var codecScalars = map[string]codecScalar{
	"string":  {goType: "string", write: "WriteString", read: "ReadString", bits: -1},
	"bool":    {goType: "bool", write: "WriteBool", read: "ReadBool", bits: -1},
	"rune":    {goType: "rune", write: "WriteRune", read: "ReadRune", bits: -1},
	"int":     {goType: "int", write: "WriteInt", read: "ReadInt", bits: -1},
	"int64":   {goType: "int64", write: "WriteInt64", read: "ReadInt64", bits: -1},
	"int8":    {goType: "int64", write: "WriteInt64", read: "ReadIntN", bits: 8},
	"int16":   {goType: "int64", write: "WriteInt64", read: "ReadIntN", bits: 16},
	"int32":   {goType: "int64", write: "WriteInt64", read: "ReadIntN", bits: 32},
	"uint8":   {goType: "uint64", write: "WriteUint64", read: "ReadUintN", bits: 8},
	"byte":    {goType: "uint64", write: "WriteUint64", read: "ReadUintN", bits: 8},
	"uint16":  {goType: "uint64", write: "WriteUint64", read: "ReadUintN", bits: 16},
	"uint32":  {goType: "uint64", write: "WriteUint64", read: "ReadUintN", bits: 32},
	"uint64":  {goType: "uint64", write: "WriteUint64", read: "ReadUintN", bits: 64},
	"uint":    {goType: "uint64", write: "WriteUint64", read: "ReadUintN", bits: 0},
	"uintptr": {goType: "uint64", write: "WriteUint64", read: "ReadUintN", bits: 0},
	"float32": {goType: "float32", write: "WriteFloat32", read: "ReadFloat32", bits: -1},
	"float64": {goType: "float64", write: "WriteFloat64", read: "ReadFloat64", bits: -1},
}

// codecShapeError marks a value shape the codec has no encoding for. reason
// says which shape and why; fieldShapeError adds the struct and field it was
// reached through.
type codecShapeError struct{ reason string }

func (e *codecShapeError) Error() string { return "unsupported codec shape: " + e.reason }

func unsupportedShape(format string, args ...any) error {
	return &codecShapeError{reason: fmt.Sprintf(format, args...)}
}

// codecGen carries the per-struct state of one EncodeFields/DecodeFields
// emission: the transformer (for type resolution and imports), the package
// declaring the struct ("" for this one), and a counter that keeps the
// temporaries of nested reads distinct.
type codecGen struct {
	t   *galaASTTransformer
	pkg string
	seq int
}

func (g *codecGen) fresh(prefix string) *ast.Ident {
	g.seq++
	return ast.NewIdent(fmt.Sprintf("__%s%d", prefix, g.seq))
}

// genStructMetaMethods emits EncodeFields and DecodeFields for one struct.
// A field whose type has no encoding fails the whole codec with GALA-E0050,
// positioned at the Codec / StructMeta use site that asked for it.
func (t *galaASTTransformer) genStructMetaMethods(config *structMetaConfig) (*ast.FuncDecl, *ast.FuncDecl, error) {
	enc, err := t.genEncodeFields(config)
	if err != nil {
		return nil, nil, err
	}
	dec, err := t.genDecodeFields(config)
	if err != nil {
		return nil, nil, err
	}
	return enc, dec, nil
}

// codecError builds the GALA-E0050 diagnostic, positioned at the Codec /
// StructMeta use site that asked for the codec.
func (t *galaASTTransformer) codecError(config *structMetaConfig, reason string) error {
	return galaerr.NewCodedSemanticError(galaerr.CodeUnsupportedCodecField, config.line, config.col,
		fmt.Sprintf("cannot generate a codec for %s: %s", config.rootName, reason),
		codecSupportedShapesHint)
}

const codecSupportedShapesHint = "a codec field can be a string, bool, rune, int/uint/float kind, " +
	"an alias or named type over one, a struct, or an Option, Array, List or HashMap[string, _] of those"

// fieldShapeError names the struct field an unsupported shape was reached
// through. Errors that are not shape errors pass through unchanged.
func (t *galaASTTransformer) fieldShapeError(config *structMetaConfig, fieldName string, fieldType transpiler.Type, err error) error {
	var shape *codecShapeError
	if !errors.As(err, &shape) {
		return err
	}
	return t.codecError(config, fmt.Sprintf("field %s.%s has type %s: %s",
		config.typeName, fieldName, unwrapGalaType(fieldType).String(), shape.reason))
}

// --- EncodeFields(w FieldEncoder, t T, nameFn, omitFn, naming) ---

func (t *galaASTTransformer) genEncodeFields(config *structMetaConfig) (*ast.FuncDecl, error) {
	meta := config.typeMetadata
	isImmut := t.codecImmutField(config)
	g := &codecGen{t: t, pkg: config.pkg}

	stmts := []ast.Stmt{exprStmt(methodCall("w", "WriteStartObject"))}

	// Per-field: if !omitFn(i) { w.WriteKey(nameFn(i)); <typed write> }
	for i, fieldName := range meta.FieldNames {
		fieldType := meta.Fields[fieldName]
		fieldAccess := buildFieldAccess(ast.NewIdent("t"), fieldName, isImmut(i))

		valueStmts, err := g.write(fieldAccess, unwrapGalaType(fieldType))
		if err != nil {
			return nil, t.fieldShapeError(config, fieldName, fieldType, err)
		}
		writeStmts := append([]ast.Stmt{
			exprStmt(methodCall("w", "WriteKey", &ast.CallExpr{Fun: ast.NewIdent("nameFn"), Args: []ast.Expr{intLit(i)}})),
		}, valueStmts...)

		stmts = append(stmts, &ast.IfStmt{
			Cond: &ast.UnaryExpr{
				Op: token.NOT,
				X:  &ast.CallExpr{Fun: ast.NewIdent("omitFn"), Args: []ast.Expr{intLit(i)}},
			},
			Body: &ast.BlockStmt{List: writeStmts},
		})
	}

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
	}, nil
}

// write returns the statements that serialize the value `access` of type ty
// through the FieldEncoder `w`. The caller has already written the key (or is
// inside an array).
func (g *codecGen) write(access ast.Expr, ty transpiler.Type) ([]ast.Stmt, error) {
	ty = g.t.codecUnalias(ty, g.pkg)
	switch kind, params := codecContainer(ty); kind {
	case "Immutable":
		return g.write(&ast.CallExpr{Fun: &ast.SelectorExpr{X: access, Sel: ast.NewIdent("Get")}}, params[0])
	case "Option":
		if g.t.codecNullable(params[0], g.pkg) {
			return nil, errNestedOption()
		}
		body, err := g.write(&ast.CallExpr{Fun: &ast.SelectorExpr{X: access, Sel: ast.NewIdent("Get")}}, params[0])
		if err != nil {
			return nil, err
		}
		return []ast.Stmt{&ast.IfStmt{
			Cond: &ast.CallExpr{Fun: &ast.SelectorExpr{X: access, Sel: ast.NewIdent("IsDefined")}},
			Body: &ast.BlockStmt{List: body},
			Else: &ast.BlockStmt{List: []ast.Stmt{exprStmt(methodCall("w", "WriteNull"))}},
		}}, nil
	case "Array", "List":
		elemType, err := g.goType(params[0])
		if err != nil {
			return nil, err
		}
		elem := g.fresh("elem")
		body, err := g.write(elem, params[0])
		if err != nil {
			return nil, err
		}
		lambda := &ast.FuncLit{
			Type: &ast.FuncType{Params: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{elem}, Type: elemType}}}},
			Body: &ast.BlockStmt{List: body},
		}
		return []ast.Stmt{
			exprStmt(methodCall("w", "WriteStartArray")),
			exprStmt(&ast.CallExpr{Fun: &ast.SelectorExpr{X: access, Sel: ast.NewIdent("ForEach")}, Args: []ast.Expr{lambda}}),
			exprStmt(methodCall("w", "WriteEndArray")),
		}, nil
	case "HashMap":
		keyType, keyIsString, err := g.mapKey(params[0])
		if err != nil {
			return nil, err
		}
		valueType, err := g.goType(params[1])
		if err != nil {
			return nil, err
		}
		k, v := g.fresh("k"), g.fresh("v")
		var keyExpr ast.Expr = k
		if !keyIsString {
			keyExpr = &ast.CallExpr{Fun: ast.NewIdent("string"), Args: []ast.Expr{k}}
		}
		body, err := g.write(v, params[1])
		if err != nil {
			return nil, err
		}
		body = append([]ast.Stmt{exprStmt(methodCall("w", "WriteKey", keyExpr))}, body...)
		lambda := &ast.FuncLit{
			Type: &ast.FuncType{Params: &ast.FieldList{List: []*ast.Field{
				{Names: []*ast.Ident{k}, Type: keyType},
				{Names: []*ast.Ident{v}, Type: valueType},
			}}},
			Body: &ast.BlockStmt{List: body},
		}
		return []ast.Stmt{
			exprStmt(methodCall("w", "WriteStartObject")),
			exprStmt(&ast.CallExpr{Fun: &ast.SelectorExpr{X: access, Sel: ast.NewIdent("ForEachKV")}, Args: []ast.Expr{lambda}}),
			exprStmt(methodCall("w", "WriteEndObject")),
		}, nil
	}

	if sc, _, isWireType, ok := g.t.codecScalarOf(ty); ok {
		arg := access
		if !isWireType {
			arg = &ast.CallExpr{Fun: ast.NewIdent(sc.goType), Args: []ast.Expr{access}}
		}
		return []ast.Stmt{exprStmt(methodCall("w", sc.write, arg))}, nil
	}

	config, err := g.t.codecStructMeta(ty, g.pkg)
	if err != nil {
		return nil, err
	}
	return genNestedStructWrite(func() ast.Expr { return g.t.structMetaRef(config) }, access), nil
}

func errNestedOption() error {
	return unsupportedShape("an Option nested directly in an Option has no encoding: None and Some(None) would both be null")
}

// genNestedStructWrite emits the call to _StructMeta_Inner{}.EncodeFields
// with fresh per-type nameFn/omitFn closures that derive their key from the
// inherited `naming` mapping and the inner StructMeta's FieldName. metaType
// names the inner StructMeta type; it is called once per use.
func genNestedStructWrite(metaType func() ast.Expr, access ast.Expr) []ast.Stmt {
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
						X:   &ast.CompositeLit{Type: metaType()},
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
			X:   &ast.CompositeLit{Type: metaType()},
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

// --- FieldIsEmpty(t T, i int) bool ---

// genFieldIsEmpty emits the per-field emptiness test a codec's OmitEmpty
// consults at encode time:
//
//	switch i { case 0: return t.Name == ""; case 1: return t.Tags.IsEmpty(); ... }
//	return false
//
// A field that can never be empty (a nested struct) gets no case. The field
// shapes were already validated by genEncodeFields.
func (t *galaASTTransformer) genFieldIsEmpty(config *structMetaConfig) *ast.FuncDecl {
	meta := config.typeMetadata
	isImmut := t.codecImmutField(config)

	var cases []ast.Stmt
	for i, fieldName := range meta.FieldNames {
		access := buildFieldAccess(ast.NewIdent("t"), fieldName, isImmut(i))
		if empty := t.codecIsEmpty(access, unwrapGalaType(meta.Fields[fieldName]), config.pkg); empty != nil {
			cases = append(cases, &ast.CaseClause{
				List: []ast.Expr{intLit(i)},
				Body: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{empty}}},
			})
		}
	}

	var body []ast.Stmt
	if len(cases) > 0 {
		body = append(body, &ast.SwitchStmt{Tag: ast.NewIdent("i"), Body: &ast.BlockStmt{List: cases}})
	}
	body = append(body, &ast.ReturnStmt{Results: []ast.Expr{ast.NewIdent("false")}})

	return &ast.FuncDecl{
		Recv: blankRecv(config.generatedName),
		Name: ast.NewIdent("FieldIsEmpty"),
		Type: &ast.FuncType{
			Params: &ast.FieldList{List: []*ast.Field{
				{Names: idents("t"), Type: ast.NewIdent(config.typeName)},
				{Names: idents("i"), Type: ast.NewIdent("int")},
			}},
			Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("bool")}}},
		},
		Body: &ast.BlockStmt{List: body},
	}
}

// codecIsEmpty returns the boolean expression that tests whether the value
// `access` of type ty is empty, or nil when a value of ty is never empty.
// Empty is: "" for a string kind, 0 for a numeric kind (rune included), false
// for bool, None for an Option, and no elements for an Array, List or HashMap.
// A struct is never empty, as in Go's encoding/json: an all-zero struct is
// still a value worth writing. Immutable and aliases are looked through. pkg
// is the package declaring the field ty came from ("" for this one).
func (t *galaASTTransformer) codecIsEmpty(access ast.Expr, ty transpiler.Type, pkg string) ast.Expr {
	ty = t.codecUnalias(ty, pkg)
	switch kind, params := codecContainer(ty); kind {
	case "Immutable":
		return t.codecIsEmpty(&ast.CallExpr{Fun: &ast.SelectorExpr{X: access, Sel: ast.NewIdent("Get")}}, params[0], pkg)
	case "Option", "Array", "List", "HashMap":
		return &ast.CallExpr{Fun: &ast.SelectorExpr{X: access, Sel: ast.NewIdent("IsEmpty")}}
	}
	sc, _, _, ok := t.codecScalarOf(ty)
	if !ok {
		return nil
	}
	switch sc.goType {
	case "bool":
		return &ast.UnaryExpr{Op: token.NOT, X: access}
	case "string":
		return &ast.BinaryExpr{X: access, Op: token.EQL, Y: stringLit("")}
	}
	return &ast.BinaryExpr{X: access, Op: token.EQL, Y: intLit(0)}
}

// --- DecodeFields(r FieldDecoder, lookup func(string) int, naming) T ---

func (t *galaASTTransformer) genDecodeFields(config *structMetaConfig) (*ast.FuncDecl, error) {
	meta := config.typeMetadata
	isImmut := t.codecImmutField(config)
	g := &codecGen{t: t, pkg: config.pkg}

	var stmts []ast.Stmt

	// Per-field locals of the field's Go type, holding the field's empty value:
	// a field absent from the input — one an OmitEmpty encoder left out, say —
	// decodes to it. An empty value that runs a Validate method (a struct with
	// private fields, at any depth) is built only when the field turns out to
	// be absent: Validate may reject it, and a present field never needs it.
	seen := make([]*ast.Ident, len(meta.FieldNames))
	var absent []ast.Stmt
	for i, fieldName := range meta.FieldNames {
		fieldType := meta.Fields[fieldName]
		valueType := unwrapGalaType(fieldType)
		goType, err := g.goType(valueType)
		if err != nil {
			return nil, t.fieldShapeError(config, fieldName, fieldType, err)
		}
		empty, err := g.emptyValue(valueType)
		if err != nil {
			return nil, t.fieldShapeError(config, fieldName, fieldType, err)
		}
		switch {
		case empty == nil:
			stmts = append(stmts, seqVarDecl("_"+fieldName, goType))
		case t.codecEmptyKind(valueType, config.pkg) == emptyValidated:
			flag := g.fresh("seen")
			seen[i] = flag
			stmts = append(stmts, seqVarDecl("_"+fieldName, goType), seqVarDecl(flag.Name, ast.NewIdent("bool")))
			absent = append(absent, &ast.IfStmt{
				Cond: &ast.UnaryExpr{Op: token.NOT, X: flag},
				Body: &ast.BlockStmt{List: []ast.Stmt{&ast.AssignStmt{
					Lhs: []ast.Expr{ast.NewIdent("_" + fieldName)}, Tok: token.ASSIGN, Rhs: []ast.Expr{empty},
				}}},
			})
		default:
			stmts = append(stmts, seqVarDecl("_"+fieldName, goType, empty))
		}
	}

	stmts = append(stmts, exprStmt(methodCall("r", "StartObject")))

	var switchCases []ast.Stmt
	for i, fieldName := range meta.FieldNames {
		fieldType := meta.Fields[fieldName]
		assignStmts, err := g.read(ast.NewIdent("_"+fieldName), unwrapGalaType(fieldType))
		if err != nil {
			return nil, t.fieldShapeError(config, fieldName, fieldType, err)
		}
		if flag := seen[i]; flag != nil {
			assignStmts = append(assignStmts, &ast.AssignStmt{Lhs: []ast.Expr{flag}, Tok: token.ASSIGN, Rhs: []ast.Expr{ast.NewIdent("true")}})
		}
		switchCases = append(switchCases, &ast.CaseClause{
			List: []ast.Expr{intLit(i)},
			Body: assignStmts,
		})
	}
	// default: r.Skip() — keys the struct does not declare.
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

	stmts = append(stmts, exprStmt(methodCall("r", "EndObject")))
	stmts = append(stmts, absent...)

	// return T{FieldName: _FieldName, ...}  (wrap Immutable fields), through
	// Validate for a struct with private fields.
	var compositeElts []ast.Expr
	for i, fieldName := range meta.FieldNames {
		var value ast.Expr = ast.NewIdent("_" + fieldName)
		if isImmut(i) {
			value = t.newImmutable(value)
		}
		compositeElts = append(compositeElts, &ast.KeyValueExpr{Key: ast.NewIdent(fieldName), Value: value})
	}
	stmts = t.decodedValue(config, stmts, &ast.CompositeLit{Type: ast.NewIdent(config.typeName), Elts: compositeElts})

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
	}, nil
}

// read returns the statements that decode one value of type ty from the
// FieldDecoder `r` and assign it to target (an already-declared variable of
// ty's Go type).
func (g *codecGen) read(target ast.Expr, ty transpiler.Type) ([]ast.Stmt, error) {
	ty = g.t.codecUnalias(ty, g.pkg)
	assign := func(value ast.Expr) ast.Stmt {
		return &ast.AssignStmt{Lhs: []ast.Expr{target}, Tok: token.ASSIGN, Rhs: []ast.Expr{value}}
	}

	switch kind, params := codecContainer(ty); kind {
	case "Immutable":
		innerType, err := g.goType(params[0])
		if err != nil {
			return nil, err
		}
		tmp := g.fresh("inner")
		body, err := g.read(tmp, params[0])
		if err != nil {
			return nil, err
		}
		stmts := append([]ast.Stmt{seqVarDecl(tmp.Name, innerType)}, body...)
		stmts = append(stmts, assign(g.t.newImmutable(tmp)))
		return []ast.Stmt{&ast.BlockStmt{List: stmts}}, nil
	case "Option":
		if g.t.codecNullable(params[0], g.pkg) {
			return nil, errNestedOption()
		}
		innerType, err := g.goType(params[0])
		if err != nil {
			return nil, err
		}
		tmp := g.fresh("some")
		body, err := g.read(tmp, params[0])
		if err != nil {
			return nil, err
		}
		someStmts := append([]ast.Stmt{seqVarDecl(tmp.Name, innerType)}, body...)
		someStmts = append(someStmts, assign(applyCtor(g.t.buildSomeType(innerType), tmp)))
		return []ast.Stmt{&ast.IfStmt{
			Cond: methodCall("r", "IsNull"),
			Body: &ast.BlockStmt{List: []ast.Stmt{
				exprStmt(methodCall("r", "ReadNull")),
				assign(applyCtor(g.t.buildNoneType(innerType))),
			}},
			Else: &ast.BlockStmt{List: someStmts},
		}}, nil
	case "Array", "List":
		elemType, err := g.goType(params[0])
		if err != nil {
			return nil, err
		}
		slice, elem := g.fresh("slice"), g.fresh("elem")
		elemStmts, err := g.read(elem, params[0])
		if err != nil {
			return nil, err
		}
		loopBody := append([]ast.Stmt{seqVarDecl(elem.Name, elemType)}, elemStmts...)
		loopBody = append(loopBody, &ast.AssignStmt{
			Lhs: []ast.Expr{slice},
			Tok: token.ASSIGN,
			Rhs: []ast.Expr{&ast.CallExpr{Fun: ast.NewIdent("append"), Args: []ast.Expr{slice, elem}}},
		})
		ctorName := "ArrayFromSlice"
		if kind == "List" {
			ctorName = "ListFromSlice"
		}
		return []ast.Stmt{&ast.BlockStmt{List: []ast.Stmt{
			seqVarDecl(slice.Name, &ast.ArrayType{Elt: elemType}),
			exprStmt(methodCall("r", "StartArray")),
			&ast.ForStmt{Cond: methodCall("r", "HasMoreElements"), Body: &ast.BlockStmt{List: loopBody}},
			exprStmt(methodCall("r", "EndArray")),
			assign(&ast.CallExpr{Fun: g.t.collectionIdent(ctorName), Args: []ast.Expr{slice}}),
		}}}, nil
	case "HashMap":
		keyType, keyIsString, err := g.mapKey(params[0])
		if err != nil {
			return nil, err
		}
		valueType, err := g.goType(params[1])
		if err != nil {
			return nil, err
		}
		m, k, v := g.fresh("m"), g.fresh("k"), g.fresh("v")
		valueStmts, err := g.read(v, params[1])
		if err != nil {
			return nil, err
		}
		var keyValue ast.Expr = methodCall("r", "ReadKey")
		if !keyIsString {
			keyValue = &ast.CallExpr{Fun: keyType, Args: []ast.Expr{keyValue}}
		}
		loopBody := []ast.Stmt{
			&ast.AssignStmt{Lhs: []ast.Expr{k}, Tok: token.DEFINE, Rhs: []ast.Expr{keyValue}},
			seqVarDecl(v.Name, valueType),
		}
		loopBody = append(loopBody, valueStmts...)
		loopBody = append(loopBody, &ast.AssignStmt{
			Lhs: []ast.Expr{m},
			Tok: token.ASSIGN,
			Rhs: []ast.Expr{&ast.CallExpr{Fun: &ast.SelectorExpr{X: m, Sel: ast.NewIdent("Put")}, Args: []ast.Expr{k, v}}},
		})
		emptyMap := &ast.CallExpr{Fun: &ast.IndexListExpr{
			X:       g.t.collectionIdent("EmptyHashMap"),
			Indices: []ast.Expr{keyType, valueType},
		}}
		return []ast.Stmt{&ast.BlockStmt{List: []ast.Stmt{
			&ast.AssignStmt{Lhs: []ast.Expr{m}, Tok: token.DEFINE, Rhs: []ast.Expr{emptyMap}},
			exprStmt(methodCall("r", "StartObject")),
			&ast.ForStmt{Cond: methodCall("r", "HasMoreFields"), Body: &ast.BlockStmt{List: loopBody}},
			exprStmt(methodCall("r", "EndObject")),
			assign(m),
		}}}, nil
	}

	if sc, declared, isWireType, ok := g.t.codecScalarOf(ty); ok {
		var args []ast.Expr
		if sc.bits >= 0 {
			args = []ast.Expr{intLit(sc.bits)}
		}
		var value ast.Expr = methodCall("r", sc.read, args...)
		if !isWireType {
			value = &ast.CallExpr{Fun: declared, Args: []ast.Expr{value}}
		}
		return []ast.Stmt{assign(value)}, nil
	}

	config, err := g.t.codecStructMeta(ty, g.pkg)
	if err != nil {
		return nil, err
	}
	metaType := func() ast.Expr { return g.t.structMetaRef(config) }
	return []ast.Stmt{assign(&ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   &ast.CompositeLit{Type: metaType()},
			Sel: ast.NewIdent("DecodeFields"),
		},
		Args: []ast.Expr{ast.NewIdent("r"), genNestedStructLookup(metaType), ast.NewIdent("naming")},
	})}, nil
}

// applyCtor builds `<ctorType>{}.Apply(args...)` — a sealed-case constructor
// call such as `Some[T]{}.Apply(x)` or `None[T]{}.Apply()`.
func applyCtor(ctorType ast.Expr, args ...ast.Expr) ast.Expr {
	return &ast.CallExpr{
		Fun:  &ast.SelectorExpr{X: &ast.CompositeLit{Type: ctorType}, Sel: ast.NewIdent("Apply")},
		Args: args,
	}
}

// emptyValue returns the expression for the empty value of ty (see
// codecIsEmpty) where Go's zero value is not it, or nil where it is. Go's zero
// Option is a Some holding the zero element, and Go's zero List is not a valid
// empty list, so both are spelled out; the zero Array and HashMap are already
// empty, and so is every scalar's zero. A struct with such a field, at any
// depth, is built by its StructMeta's Empty method.
func (g *codecGen) emptyValue(ty transpiler.Type) (ast.Expr, error) {
	ty = g.t.codecUnalias(ty, g.pkg)
	switch kind, params := codecContainer(ty); kind {
	case "Immutable":
		inner, err := g.emptyValue(params[0])
		if inner == nil || err != nil {
			return nil, err
		}
		return g.t.newImmutable(inner), nil
	case "Option":
		elemType, err := g.goType(params[0])
		if err != nil {
			return nil, err
		}
		return applyCtor(g.t.buildNoneType(elemType)), nil
	case "List":
		elemType, err := g.goType(params[0])
		if err != nil {
			return nil, err
		}
		return &ast.CallExpr{Fun: &ast.IndexExpr{X: g.t.collectionIdent("EmptyList"), Index: elemType}}, nil
	case "":
		if config, err := g.t.codecStructMeta(ty, g.pkg); err == nil && g.t.structEmptyKind(config) >= emptyNeedsInit {
			return &ast.CallExpr{Fun: &ast.SelectorExpr{
				X:   &ast.CompositeLit{Type: g.t.structMetaRef(config)},
				Sel: ast.NewIdent("Empty"),
			}}, nil
		}
	}
	return nil, nil
}

// emptyInitState is how a value's empty value (see emptyValue) is built. The
// states are ordered, so a struct's state is the greatest of its fields'.
type emptyInitState uint8

const (
	emptyInitUnknown emptyInitState = iota
	// emptyIsZero: Go's zero value is the empty value.
	emptyIsZero
	// emptyNeedsInit: the empty value is spelled out (None, an empty List).
	emptyNeedsInit
	// emptyValidated: the empty value runs a Validate method — a struct with
	// private fields, at any depth. DecodeFields builds it only when the
	// field is absent, since Validate may reject it.
	emptyValidated
)

// codecEmptyKind is how the empty value of ty is built. pkg is the package
// declaring the field ty came from ("" for this one).
func (t *galaASTTransformer) codecEmptyKind(ty transpiler.Type, pkg string) emptyInitState {
	ty = t.codecUnalias(ty, pkg)
	switch kind, params := codecContainer(ty); kind {
	case "Immutable":
		return t.codecEmptyKind(params[0], pkg)
	case "Option", "List":
		return emptyNeedsInit
	case "":
		// A scalar, or anything else that is not a struct, has no StructMeta:
		// its zero value is its empty value.
		if config, err := t.codecStructMeta(ty, pkg); err == nil {
			return t.structEmptyKind(config)
		}
	}
	return emptyIsZero
}

// structEmptyKind is how the empty value of config's struct is built,
// memoized on config. A struct cannot contain itself except through an Option
// or a collection, so the recursion ends.
func (t *galaASTTransformer) structEmptyKind(config *structMetaConfig) emptyInitState {
	if config.emptyInit == emptyInitUnknown {
		config.emptyInit = emptyIsZero
		if t.structDecodeMode(config) != decodeRaw {
			// Go's zero value never went through Validate.
			config.emptyInit = emptyValidated
		} else {
			meta := config.typeMetadata
			for _, fieldName := range meta.FieldNames {
				config.emptyInit = max(config.emptyInit, t.codecEmptyKind(unwrapGalaType(meta.Fields[fieldName]), config.pkg))
			}
		}
	}
	return config.emptyInit
}

// codecImmutField reports whether field i of config's struct is a val field,
// stored as Immutable[T] and read through Get().
func (t *galaASTTransformer) codecImmutField(config *structMetaConfig) func(int) bool {
	flags := t.structImmutFields[t.resolveStructTypeName(config.typeName)]
	return func(i int) bool { return i < len(flags) && flags[i] }
}

// newImmutable wraps x in std.NewImmutable, as a val field stores it.
func (t *galaASTTransformer) newImmutable(x ast.Expr) ast.Expr {
	return &ast.CallExpr{Fun: t.stdIdent("NewImmutable"), Args: []ast.Expr{x}}
}

// --- Empty() T ---

// genEmpty emits the struct's empty value — every field holding the value an
// absent field decodes to (see emptyValue):
//
//	func (_ _StructMeta_T) Empty() T { return T{Tags: NewImmutable(EmptyList[string]())} }
//
// It is a method of the generated type rather than an inline literal so a
// struct of another package, whose fields only that package can set, still
// has one. DecodeFields of an enclosing struct calls it for a nested struct
// field that is absent from the input. A struct with private fields builds
// it through its Validate method, as DecodeFields does.
//
// The field shapes were already validated by genDecodeFields, which builds
// the same empty values.
func (t *galaASTTransformer) genEmpty(config *structMetaConfig) *ast.FuncDecl {
	meta := config.typeMetadata
	isImmut := t.codecImmutField(config)
	g := &codecGen{t: t, pkg: config.pkg}

	var elts []ast.Expr
	for i, fieldName := range meta.FieldNames {
		empty, _ := g.emptyValue(unwrapGalaType(meta.Fields[fieldName]))
		if empty == nil {
			continue
		}
		if isImmut(i) {
			empty = t.newImmutable(empty)
		}
		elts = append(elts, &ast.KeyValueExpr{Key: ast.NewIdent(fieldName), Value: empty})
	}
	return &ast.FuncDecl{
		Recv: blankRecv(config.generatedName),
		Name: ast.NewIdent("Empty"),
		Type: &ast.FuncType{Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent(config.typeName)}}}},
		Body: &ast.BlockStmt{List: t.decodedValue(config, nil, &ast.CompositeLit{Type: ast.NewIdent(config.typeName), Elts: elts})},
	}
}

// genNestedStructLookup emits a closure: func(key string) int that looks up
// the field index in _StructMeta_Inner using the inherited `naming`
// mapping.  We bind the meta to a local so its composite-literal use does
// not collide with for-clause syntax (Go treats `_StructMeta_T{` in a for
// header as the start of a composite-literal block, which is a parse error).
func genNestedStructLookup(metaType func() ast.Expr) ast.Expr {
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
			Rhs: []ast.Expr{&ast.CompositeLit{Type: metaType()}},
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
			Cond: &ast.BinaryExpr{X: ast.NewIdent("i"), Op: token.LSS, Y: ast.NewIdent("n")},
			Post: &ast.IncDecStmt{X: ast.NewIdent("i"), Tok: token.INC},
			Body: &ast.BlockStmt{List: []ast.Stmt{
				&ast.IfStmt{
					Cond: &ast.BinaryExpr{
						X: &ast.CallExpr{
							Fun: ast.NewIdent("naming"),
							Args: []ast.Expr{&ast.CallExpr{
								Fun:  &ast.SelectorExpr{X: ast.NewIdent("_meta"), Sel: ast.NewIdent("FieldName")},
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

// --- type classification ---

// codecContainer reports which codec-aware generic container ty is
// ("Immutable", "Option", "Array", "List", "HashMap") together with its type
// arguments, or "" when ty is none of them (or has the wrong arity).
func codecContainer(ty transpiler.Type) (string, []transpiler.Type) {
	gt, ok := ty.(transpiler.GenericType)
	if !ok {
		return "", nil
	}
	var kind string
	arity := 1
	switch gt.Base.BaseName() {
	case "Immutable", "std.Immutable":
		kind = "Immutable"
	case "Option", "std.Option":
		kind = "Option"
	case "Array", "collection_immutable.Array":
		kind = "Array"
	case "List", "collection_immutable.List":
		kind = "List"
	case "HashMap", "collection_immutable.HashMap":
		kind, arity = "HashMap", 2
	default:
		return "", nil
	}
	if len(gt.Params) != arity {
		return "", nil
	}
	return kind, gt.Params
}

// codecScalarOf classifies ty as a scalar the codec can carry: a built-in
// scalar kind, an alias chain that ends at one (`type Millis int64`), or a Go
// named type whose underlying type is one (`time.Duration`). It returns the
// wire description, the Go expression naming ty (for the conversion back on
// decode), and whether ty already is the wire type so no conversion is
// needed.
func (t *galaASTTransformer) codecScalarOf(ty transpiler.Type) (codecScalar, ast.Expr, bool, bool) {
	name, pkg, ok := simpleTypeName(ty)
	if !ok {
		return codecScalar{}, nil, false, false
	}
	if pkg == "" || pkg == t.packageName {
		if sc, ok := codecScalars[name]; ok {
			return sc, ast.NewIdent(name), name == sc.goType, true
		}
		// A named type the package's own hand-written .go files declare
		// (`type Millis int64` in a sibling .go) is a Go named type like any
		// imported one, only unqualified.
		if !t.isOwnGoType(name) {
			return codecScalar{}, nil, false, false
		}
		ty = transpiler.NamedType{Package: t.packageName, Name: name}
	}
	if underlying, ok := t.goNamedUnderlying(ty); ok {
		if uname, upkg, ok := simpleTypeName(underlying); ok && upkg == "" {
			if sc, ok := codecScalars[uname]; ok {
				return sc, t.codecTypeExpr(ty), false, true
			}
		}
	}
	return codecScalar{}, nil, false, false
}

// codecUnalias resolves an alias to the type it names, so an alias of a
// scalar (`type Millis int64`), of a Go named type (`type Wait time.Duration`)
// or of a container (`type Tags Array[string]`) classifies like its target.
// GALA aliases lower to Go aliases (`type Millis = int64`), so the generated
// code may name either side. pkg is the package whose declaration ty comes
// from ("" for this one): an unqualified name there is that package's. This
// package's aliases are keyed by simple name and another's by qualified name,
// so an imported struct's `Millis` field never resolves through an unrelated
// local alias of the same name. A generic alias has its type arguments
// substituted: `Items[int]` for `type Items[T any] Array[T]` is `Array[int]`.
//
// The hop count bounds a chain that refers back to itself.
func (t *galaASTTransformer) codecUnalias(ty transpiler.Type, pkg string) transpiler.Type {
	for hop := 0; hop <= len(t.typeAliases); hop++ {
		base := ty
		if g, isGeneric := ty.(transpiler.GenericType); isGeneric {
			base = g.Base
		}
		name, owner, ok := simpleTypeName(base)
		if !ok {
			return ty
		}
		if owner == "" {
			owner = pkg
		}
		key := name
		if owner != "" && owner != t.packageName {
			key = owner + "." + name
		}
		target, isAlias := t.aliasTargetByKey(key, ty)
		if !isAlias {
			return ty
		}
		// The target is written in owner's terms, so it resolves there in turn.
		ty, pkg = target, owner
	}
	return ty
}

// codecNullable reports whether ty can encode as null: an Option, possibly
// behind Immutable layers or aliases. Such a type inside an Option would make
// None and Some(None) indistinguishable. pkg is as for codecUnalias.
func (t *galaASTTransformer) codecNullable(ty transpiler.Type, pkg string) bool {
	for {
		switch kind, params := codecContainer(t.codecUnalias(ty, pkg)); kind {
		case "Option":
			return true
		case "Immutable":
			ty = params[0]
		default:
			return false
		}
	}
}

// simpleTypeName splits a basic or named type into its bare name and package
// qualifier ("" when unqualified).
func simpleTypeName(ty transpiler.Type) (string, string, bool) {
	switch v := ty.(type) {
	case transpiler.BasicType:
		if dot := lastDot(v.Name); dot >= 0 {
			return v.Name[dot+1:], v.Name[:dot], true
		}
		return v.Name, "", true
	case transpiler.NamedType:
		if v.Package == "" {
			if dot := lastDot(v.Name); dot >= 0 {
				return v.Name[dot+1:], v.Name[:dot], true
			}
		}
		return v.Name, v.Package, true
	}
	return "", "", false
}

// codecStructMeta returns the registered StructMeta for a struct-typed value
// of a field declared in package pkg ("" for this one), or an
// unsupported-shape error explaining why ty has no encoding.
func (t *galaASTTransformer) codecStructMeta(ty transpiler.Type, pkg string) (*structMetaConfig, error) {
	if name := t.codecStructName(ty, pkg); name != "" {
		if meta, resolved := t.getTypeMetaResolved(name); meta != nil {
			if config, ok := t.structMetas[resolved]; ok {
				return config, nil
			}
			if reason := t.describableReason(name, meta); reason != "" {
				return nil, unsupportedShape("%s", reason)
			}
		}
	}
	switch ty.(type) {
	case transpiler.FuncType:
		return nil, unsupportedShape("functions have no serialized form")
	case transpiler.PointerType:
		return nil, unsupportedShape("pointers are not encoded; store the value itself")
	case transpiler.ArrayType, transpiler.MapType:
		return nil, unsupportedShape("Go slices and maps are not encoded; use Array, List or HashMap")
	case transpiler.GenericType:
		return nil, unsupportedShape("generic type %s has no codec encoding", ty.String())
	}
	if named, ok := ty.(transpiler.NamedType); ok && named.Package != "" && named.Package != t.packageName && t.goTypeInfo == nil {
		return nil, unsupportedShape("the underlying kind of %s is unknown because Go type information is unavailable (is the Go SDK on PATH or GOROOT set?)", ty.String())
	}
	return nil, unsupportedShape("%s", notCodecTypeReason(ty.String()))
}

// sealedReason and noFieldsReason explain why a named type has no codec
// encoding; they are shared by the field check and the Codec[T] root check.
func sealedReason(name string) string {
	return fmt.Sprintf("%s is a sealed type, and sealed types have no codec encoding yet", name)
}

func noFieldsReason(name string) string {
	return fmt.Sprintf("%s has no fields, so the codec has nothing to describe", name)
}

// codecStructName is the name to look a struct-typed value's StructMeta up by
// (callers resolve aliases first): qualified when the struct belongs to
// another package, so an imported Email never resolves to a local one. pkg is
// the package declaring the field ty came from ("" for this one); an
// unqualified name there is that package's. Returns "" for anything but a
// basic or named type: generic user structs are not described by StructMeta.
func (t *galaASTTransformer) codecStructName(ty transpiler.Type, pkg string) string {
	name, owner, ok := simpleTypeName(ty)
	if !ok {
		return ""
	}
	if owner == "" && pkg != "" {
		if meta, _ := t.getTypeMetaResolved(pkg + "." + name); meta != nil {
			owner = pkg
		}
	}
	if owner == "" || owner == t.packageName {
		return name
	}
	return owner + "." + name
}

// mapKey checks that a HashMap key type is string-shaped — JSON and YAML
// object keys are text — and returns its Go type and whether it is exactly
// `string` (so no conversion is needed).
func (g *codecGen) mapKey(ty transpiler.Type) (ast.Expr, bool, error) {
	ty = g.t.codecUnalias(ty, g.pkg)
	sc, declared, isWireType, ok := g.t.codecScalarOf(ty)
	if !ok || sc.goType != "string" {
		return nil, false, unsupportedShape("HashMap keys must be strings (or an alias of string): object keys are text, and %s is not", ty.String())
	}
	return declared, isWireType, nil
}

// goType returns the Go type expression for a codec-supported value type, or
// an unsupported-shape error.
func (g *codecGen) goType(ty transpiler.Type) (ast.Expr, error) {
	ty = g.t.codecUnalias(ty, g.pkg)
	switch kind, params := codecContainer(ty); kind {
	case "Immutable", "Option":
		inner, err := g.goType(params[0])
		if err != nil {
			return nil, err
		}
		if kind == "Option" && g.t.codecNullable(params[0], g.pkg) {
			return nil, errNestedOption()
		}
		return &ast.IndexExpr{X: g.t.stdIdent(kind), Index: inner}, nil
	case "Array", "List":
		inner, err := g.goType(params[0])
		if err != nil {
			return nil, err
		}
		return &ast.IndexExpr{X: g.t.collectionIdent(kind), Index: inner}, nil
	case "HashMap":
		k, _, err := g.mapKey(params[0])
		if err != nil {
			return nil, err
		}
		v, err := g.goType(params[1])
		if err != nil {
			return nil, err
		}
		return &ast.IndexListExpr{X: g.t.collectionIdent("HashMap"), Indices: []ast.Expr{k, v}}, nil
	}
	if _, declared, _, ok := g.t.codecScalarOf(ty); ok {
		return declared, nil
	}
	if _, err := g.t.codecStructMeta(ty, g.pkg); err != nil {
		return nil, err
	}
	return g.t.codecTypeExpr(ty), nil
}

// codecTypeExpr names a basic or named type in generated Go, qualifying and
// importing it when it belongs to another package.
func (t *galaASTTransformer) codecTypeExpr(ty transpiler.Type) ast.Expr {
	if named, ok := ty.(transpiler.NamedType); ok && named.Package != "" && named.Package != t.packageName {
		return t.typeToExpr(ty)
	}
	name, pkg, _ := simpleTypeName(ty)
	if pkg == "" || pkg == t.packageName {
		return ast.NewIdent(name)
	}
	return t.qualifiedTypeIdent(pkg + "." + name)
}

// --- helpers ---

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
