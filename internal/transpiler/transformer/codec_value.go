package transformer

// ValueMeta[T] codegen.
//
// StructMeta[T] describes a struct and is what a codec needs for a document
// whose root is an object. ValueMeta[T] describes a whole value of any shape
// the codec can carry — a scalar, an alias or Go named type over one, an
// Option, an Array or List, a HashMap[string, _], a struct, nested to any
// depth — so a library can encode a document whose root is not a struct:
// `[1,2,3]`, `[{"id":1}]`, `42`, `"abc"`.
//
// It is the same auto-injection mechanism as StructMeta: when a generic
// type's Apply declares ValueMeta[T] as its first parameter, the call site
// `Value[Array[int]]()` gets a `_ValueMeta_<T>{}` prepended. The generated
// type has two methods whose bodies are the very per-type dispatch that
// EncodeFields / DecodeFields emit for a field of type T (codecGen.write /
// codecGen.read), so a root value and a field value are encoded identically
// and the set of supported shapes cannot drift between them.

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"martianoff/gala/internal/transpiler"
)

// valueMetaConfig is one generated _ValueMeta_X: the value type it describes
// and the use site that asked for it (for diagnostics).
type valueMetaConfig struct {
	ty            transpiler.Type
	generatedName string
	site          *structMetaConfig
}

// isInjectedMetaParam reports whether an Apply parameter of this type is
// supplied by the transpiler rather than the caller: StructMeta[T] (and the
// legacy StructMetaOps) or ValueMeta[T].
func isInjectedMetaParam(ty transpiler.Type) bool {
	switch ty.BaseName() {
	case "StructMeta", "std.StructMeta", "StructMetaOps", "json.StructMetaOps":
		return true
	}
	return isValueMetaParam(ty)
}

func isValueMetaParam(ty transpiler.Type) bool {
	switch ty.BaseName() {
	case "ValueMeta", "std.ValueMeta":
		return true
	}
	return false
}

// autoInjectValueMeta prepends a generated _ValueMeta_X{} before the call's
// existing args: Value[Array[int]]() → Apply(_ValueMeta_Array_int{}).
func (t *galaASTTransformer) autoInjectValueMeta(args []ast.Expr, typeArgs []ast.Expr, line, col int) ([]ast.Expr, error) {
	if len(typeArgs) == 0 {
		return args, nil
	}
	key := types.ExprString(typeArgs[0])
	site := &structMetaConfig{rootName: key, line: line, col: col}
	ty := t.astTypeToTranspilerType(typeArgs[0])
	if ty == nil || ty.IsNil() {
		return nil, t.codecError(site, fmt.Sprintf("type %s is not known", key))
	}
	config, ok := t.valueMetas[key]
	if !ok {
		config = &valueMetaConfig{ty: ty, generatedName: t.valueMetaName(key), site: site}
		t.valueMetas[key] = config
		// Structs reachable from the value (the root itself, Array elements,
		// Option payloads, ...) need their own _StructMeta_X; finalizeCodecs
		// closes over anything nested deeper.
		t.registerNestedStructMetaForType(ty, site)
		t.typeMetas[config.generatedName] = &transpiler.TypeMetadata{
			Name:    config.generatedName,
			Package: t.packageName,
			Methods: map[string]*transpiler.MethodMetadata{},
			Fields:  make(map[string]transpiler.Type),
		}
		t.invalidateTypeEnv()
	}
	return append([]ast.Expr{&ast.CompositeLit{Type: ast.NewIdent(config.generatedName)}}, args...), nil
}

// valueMetaName derives a readable Go identifier for the _ValueMeta_ of the
// type spelled key ("collection_immutable.Array[User]" →
// "_ValueMeta_collection_immutable_Array_User"), disambiguating the rare
// spelling that sanitizes to a name already taken.
func (t *galaASTTransformer) valueMetaName(key string) string {
	var sb strings.Builder
	sb.WriteString("_ValueMeta_")
	for _, r := range key {
		switch {
		case r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9':
			sb.WriteRune(r)
		case r == ' ':
		default:
			sb.WriteByte('_')
		}
	}
	base := strings.TrimRight(sb.String(), "_")
	name := base
	for n := 2; t.valueMetaNameTaken(name); n++ {
		name = fmt.Sprintf("%s_%d", base, n)
	}
	return name
}

func (t *galaASTTransformer) valueMetaNameTaken(name string) bool {
	for _, c := range t.valueMetas {
		if c.generatedName == name {
			return true
		}
	}
	return false
}

// generateValueMetaDecls emits, in name order:
//
//	type _ValueMeta_X struct{}
//	func (_ _ValueMeta_X) EncodeValue(w std.FieldEncoder, v X, naming func(string) string) { <write v> }
//	func (_ _ValueMeta_X) DecodeValue(r std.FieldDecoder, naming func(string) string) X { var v X; <read v>; return v }
func (t *galaASTTransformer) generateValueMetaDecls() ([]ast.Decl, error) {
	configs := make([]*valueMetaConfig, 0, len(t.valueMetas))
	for _, c := range t.valueMetas {
		configs = append(configs, c)
	}
	sort.Slice(configs, func(i, j int) bool { return configs[i].generatedName < configs[j].generatedName })

	var decls []ast.Decl
	for _, config := range configs {
		d, err := t.genValueMeta(config)
		if err != nil {
			return nil, err
		}
		decls = append(decls, d...)
	}
	return decls, nil
}

func (t *galaASTTransformer) genValueMeta(config *valueMetaConfig) ([]ast.Decl, error) {
	shapeErr := func(err error) error {
		var shape *codecShapeError
		if errors.As(err, &shape) {
			return t.codecError(config.site, shape.reason)
		}
		return err
	}
	g := &codecGen{t: t}
	goType, err := g.goType(config.ty)
	if err != nil {
		return nil, shapeErr(err)
	}
	encodeBody, err := g.write(ast.NewIdent("v"), config.ty)
	if err != nil {
		return nil, shapeErr(err)
	}
	readStmts, err := g.read(ast.NewIdent("v"), config.ty)
	if err != nil {
		return nil, shapeErr(err)
	}
	decodeBody := append([]ast.Stmt{seqVarDecl("v", goType)}, readStmts...)
	decodeBody = append(decodeBody, &ast.ReturnStmt{Results: []ast.Expr{ast.NewIdent("v")}})

	namingParam := func() *ast.Field { return &ast.Field{Names: idents("naming"), Type: funcStringStringType()} }
	genName := config.generatedName
	return []ast.Decl{
		&ast.GenDecl{Tok: token.TYPE, Specs: []ast.Spec{&ast.TypeSpec{
			Name: ast.NewIdent(genName),
			Type: &ast.StructType{Fields: &ast.FieldList{}},
		}}},
		&ast.FuncDecl{
			Recv: blankRecv(genName),
			Name: ast.NewIdent("EncodeValue"),
			Type: &ast.FuncType{Params: &ast.FieldList{List: []*ast.Field{
				{Names: idents("w"), Type: t.stdIdent("FieldEncoder")},
				{Names: idents("v"), Type: goType},
				namingParam(),
			}}},
			Body: &ast.BlockStmt{List: encodeBody},
		},
		&ast.FuncDecl{
			Recv: blankRecv(genName),
			Name: ast.NewIdent("DecodeValue"),
			Type: &ast.FuncType{
				Params: &ast.FieldList{List: []*ast.Field{
					{Names: idents("r"), Type: t.stdIdent("FieldDecoder")},
					namingParam(),
				}},
				Results: &ast.FieldList{List: []*ast.Field{{Type: goType}}},
			},
			Body: &ast.BlockStmt{List: decodeBody},
		},
	}, nil
}
