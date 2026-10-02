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
	"regexp"
	"sort"
	"strings"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
)

// valueMetaConfig is one generated _ValueMeta_X: the value type it describes
// and the use site that asked for it (for diagnostics).
type valueMetaConfig struct {
	ty            transpiler.Type
	generatedName string
	site          *structMetaConfig
}

// injectedMeta names which generated metadata, if any, the transpiler supplies
// for an Apply parameter.
type injectedMeta int

const (
	noInjectedMeta injectedMeta = iota
	injectedStructMeta
	injectedValueMeta
)

// The method sets of the generated metadata types, as method name → number of
// parameters. A parameter is injected when its declared type is an interface
// with exactly one of these method sets — the interface the generated type
// satisfies — whatever the interface is called and wherever it is declared.
// Matching the shape rather than a name keeps a user's own type that happens
// to be called ValueMeta or StructMeta an ordinary parameter.
var (
	structMetaMethods = map[string]int{"NumFields": 0, "FieldName": 1, "EncodeFields": 5, "DecodeFields": 3, "FieldIsEmpty": 2}
	valueMetaMethods  = map[string]int{"EncodeValue": 3, "DecodeValue": 2}
)

// injectedMetaParam reports which generated metadata the transpiler supplies
// for an Apply parameter of type ty, or noInjectedMeta for an ordinary one.
func (t *galaASTTransformer) injectedMetaParam(ty transpiler.Type) injectedMeta {
	if ty == nil || ty.IsNil() {
		return noInjectedMeta
	}
	meta := t.getTypeMeta(ty.BaseName())
	switch {
	case meta == nil:
		return noInjectedMeta
	case isInterfaceWithMethods(meta, valueMetaMethods):
		return injectedValueMeta
	case isInterfaceWithMethods(meta, structMetaMethods):
		return injectedStructMeta
	}
	return noInjectedMeta
}

// isInterfaceWithMethods reports whether meta describes an interface whose
// methods are exactly want (name → parameter count).
func isInterfaceWithMethods(meta *transpiler.TypeMetadata, want map[string]int) bool {
	if len(meta.Fields) > 0 || len(meta.Methods) != len(want) {
		return false
	}
	for name, arity := range want {
		m, ok := meta.Methods[name]
		// Interface methods have no receiver.
		if !ok || m.ReceiverName != "" || len(m.ParamTypes) != arity {
			return false
		}
	}
	return true
}

// injectedMetaTypeArg returns the type argument the injected metadata
// describes: the receiver's type argument in the position of the type
// parameter the metadata parameter names, so `Apply(meta ValueMeta[V])` on
// `Pair[K, V]` describes V, not K.
func (t *galaASTTransformer) injectedMetaTypeArg(param transpiler.Type, receiver *transpiler.TypeMetadata, typeArgs []ast.Expr, typeName string, line, col int) (ast.Expr, error) {
	if gt, ok := param.(transpiler.GenericType); ok && len(gt.Params) == 1 {
		for i, tp := range receiver.TypeParams {
			if gt.Params[0].String() == tp && i < len(typeArgs) {
				return typeArgs[i], nil
			}
		}
	}
	return nil, t.codecError(&structMetaConfig{rootName: typeName, line: line, col: col},
		fmt.Sprintf("the type argument of Apply's %s parameter must be one of %s's own type parameters",
			param.String(), typeName))
}

// stdQualifier matches the std package qualifier the Go AST puts on std types.
var stdQualifier = regexp.MustCompile(`\b` + registry.StdPackageName + `\.`)

// codecSpelling renders a codec type argument as GALA source spells it: std
// types are in scope unqualified, so "std.Option[std.Option[int]]" is
// reported as "Option[Option[int]]".
func codecSpelling(typeArg ast.Expr) string {
	return stdQualifier.ReplaceAllString(types.ExprString(typeArg), "")
}

// autoInjectValueMeta prepends a generated _ValueMeta_X{} before the call's
// existing args: Value[Array[int]]() → Apply(_ValueMeta_Array_int{}).
func (t *galaASTTransformer) autoInjectValueMeta(args []ast.Expr, typeArg ast.Expr, line, col int) ([]ast.Expr, error) {
	key := types.ExprString(typeArg)
	site := &structMetaConfig{rootName: codecSpelling(typeArg), line: line, col: col}
	ty := t.astTypeToTranspilerType(typeArg)
	if ty == nil || ty.IsNil() {
		return nil, t.codecError(site, fmt.Sprintf("type %s is not known", site.rootName))
	}
	config, ok := t.valueMetas[key]
	if !ok {
		config = &valueMetaConfig{ty: ty, generatedName: t.valueMetaName(key), site: site}
		t.valueMetas[key] = config
		// Structs reachable from the value (the root itself, Array elements,
		// Option payloads, ...) need their own _StructMeta_X; finalizeCodecs
		// closes over anything nested deeper.
		t.registerNestedStructMetaForType(ty, site)
		// Visible to type inference; getType resolves unqualified names
		// through typeMetas, which the cached environment has normalized.
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
// "_ValueMeta_collection_immutable_Array_User" plus the file suffix, see
// codecFileSuffix), disambiguating the rare spelling that sanitizes to a name
// already taken.
func (t *galaASTTransformer) valueMetaName(key string) string {
	var sb strings.Builder
	sb.WriteString(valueMetaPrefix)
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
	suffix := t.codecFileSuffix()
	name := base + suffix
	for n := 2; t.valueMetaNameTaken(name); n++ {
		name = fmt.Sprintf("%s_%d%s", base, n, suffix)
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

// valueMetaBodies generates the Go type of the value and the statements that
// write and read it.
func (t *galaASTTransformer) valueMetaBodies(ty transpiler.Type) (ast.Expr, []ast.Stmt, []ast.Stmt, error) {
	g := &codecGen{t: t}
	goType, err := g.goType(ty)
	if err != nil {
		return nil, nil, nil, err
	}
	encodeBody, err := g.write(ast.NewIdent("v"), ty)
	if err != nil {
		return nil, nil, nil, err
	}
	readStmts, err := g.read(ast.NewIdent("v"), ty)
	return goType, encodeBody, readStmts, err
}

func (t *galaASTTransformer) genValueMeta(config *valueMetaConfig) ([]ast.Decl, error) {
	goType, encodeBody, readStmts, err := t.valueMetaBodies(config.ty)
	if err != nil {
		// An unsupported shape is reported at the Value[T]() use site.
		var shape *codecShapeError
		if errors.As(err, &shape) {
			return nil, t.codecError(config.site, shape.reason)
		}
		return nil, err
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
