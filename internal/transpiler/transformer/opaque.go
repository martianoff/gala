package transformer

import (
	"fmt"
	"go/ast"
	"go/token"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// Opaque types
//
// `opaque type UserID int64` lowers to the Go defined type `type UserID int64`.
// The type is distinct from its underlying type and from every other opaque
// type; conversions `UserID(n)` / `int64(id)` are explicit and allowed in
// every package; operators come from the underlying kind; untyped constants
// mix. It is not an alias: it never enters t.typeAliases, so inference,
// unification and method lookup never see through it. Only the codec, numeric
// slot admission and the Sendable/Shareable checker look at its underlying
// type, through opaqueUnderlying.
//
// The transformer adds what GALA's runtime needs and Go lacks: a `Hash`
// method for every opaque type, and a `Compare` method for an ordered
// underlying type, so HashMap, HashSet, TreeSet and Sorted work. Each is
// skipped when GALA or a hand-written .go file of the package declares it.

// scalarKind classifies the underlying type of an opaque type.
type scalarKind int

const (
	scalarNone scalarKind = iota
	scalarInt
	scalarUint
	scalarFloat
	scalarString
	scalarBool
)

// scalarKindOfPrimitive classifies a predeclared Go type name.
func scalarKindOfPrimitive(name string) scalarKind {
	switch name {
	case "int", "int8", "int16", "int32", "int64", "rune":
		return scalarInt
	case "uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "byte":
		return scalarUint
	case "float32", "float64":
		return scalarFloat
	case "string":
		return scalarString
	case "bool":
		return scalarBool
	}
	return scalarNone
}

// opaqueMeta returns the metadata of the opaque type typ names (bare, package
// qualified, or instantiated), or nil when typ is not an opaque type.
func (t *galaASTTransformer) opaqueMeta(typ transpiler.Type) *transpiler.TypeMetadata {
	if typ == nil || transpiler.IsUnusable(typ) {
		return nil
	}
	var name string
	switch ty := typ.(type) {
	case transpiler.BasicType:
		if transpiler.IsPrimitiveType(ty.Name) {
			return nil
		}
		name = ty.Name
	case transpiler.NamedType:
		name = ty.Name
		if ty.Package != "" && ty.Package != t.packageName {
			name = ty.Package + "." + ty.Name
		}
	case transpiler.GenericType:
		return t.opaqueMeta(ty.Base)
	default:
		return nil
	}
	meta := t.getTypeMeta(name)
	if meta == nil || !meta.IsOpaque {
		return nil
	}
	return meta
}

// opaqueName is how diagnostics spell the opaque type meta describes: bare in
// its own package, qualified with its package elsewhere.
func (t *galaASTTransformer) opaqueName(meta *transpiler.TypeMetadata) string {
	if meta.Package == "" || meta.Package == t.packageName || meta.Package == "main" || meta.Package == "test" {
		return meta.Name
	}
	return meta.Package + "." + meta.Name
}

// sameOpaque reports whether a and b describe the same opaque type.
func sameOpaque(a, b *transpiler.TypeMetadata) bool {
	return a.Package == b.Package && a.Name == b.Name
}

// opaqueUnderlying returns the type an opaque type is declared over, or false
// when typ is not an opaque type. It is for the few consumers that must see
// through the type (codec, numeric slots, Sendable/Shareable); nothing that
// infers or looks up methods may call it.
func (t *galaASTTransformer) opaqueUnderlying(typ transpiler.Type) (transpiler.Type, bool) {
	meta := t.opaqueMeta(typ)
	if meta == nil || meta.Underlying == nil || meta.Underlying.IsNil() {
		return nil, false
	}
	return meta.Underlying, true
}

// resolveScalar follows typ through local aliases and Go named types to the
// type Go sees at the bottom, and classifies it. viaOpaque is the opaque type
// met on the way, if any (an opaque type over another is rejected).
func (t *galaASTTransformer) resolveScalar(typ transpiler.Type) (end transpiler.Type, kind scalarKind, viaOpaque *transpiler.TypeMetadata) {
	for hop := 0; hop < 16 && typ != nil && !typ.IsNil(); hop++ {
		if basic, ok := typ.(transpiler.BasicType); ok {
			if k := scalarKindOfPrimitive(basic.Name); k != scalarNone {
				return typ, k, nil
			}
		}
		if meta := t.opaqueMeta(typ); meta != nil {
			return typ, scalarNone, meta
		}
		if next, ok := t.aliasTarget(typ); ok && next.BaseName() != typ.BaseName() {
			typ = next
			continue
		}
		if basic, ok := typ.(transpiler.BasicType); ok {
			if next, ok := t.typeAliases[basic.Name]; ok && !next.IsNil() && next.BaseName() != basic.Name {
				typ = next
				continue
			}
		}
		if u, ok := t.goNamedUnderlying(typ); ok {
			typ = u
			continue
		}
		break
	}
	return typ, scalarNone, nil
}

// transformOpaqueTypeDeclaration lowers `opaque type Name[TP] U` to the Go
// defined type `type Name[TP] U`, plus the synthesized Hash and Compare.
func (t *galaASTTransformer) transformOpaqueTypeDeclaration(ctx *grammar.OpaqueTypeDeclarationContext) ([]ast.Decl, error) {
	name := ctx.Identifier().GetText()

	var tParams *ast.FieldList
	if ctx.TypeParameters() != nil {
		var err error
		tParams, err = t.transformTypeParameters(ctx.TypeParameters().(*grammar.TypeParametersContext))
		if err != nil {
			return nil, err
		}
		defer t.bindTypeParams(tParams.List...)()
	}

	underlyingExpr, err := t.transformType(ctx.Type_())
	if err != nil {
		return nil, err
	}
	declared := t.astTypeToTranspilerType(underlyingExpr)
	kind, err := t.checkOpaqueUnderlying(ctx, name, declared, tParams)
	if err != nil {
		return nil, err
	}

	decls := []ast.Decl{&ast.GenDecl{
		Tok: token.TYPE,
		Specs: []ast.Spec{&ast.TypeSpec{
			Name:       ast.NewIdent(name),
			TypeParams: tParams,
			Type:       underlyingExpr,
		}},
	}}

	hasHash, hasCompare := t.userDefinedOpaqueMethods(name)
	recvType := genericSelfType(name, tParams)
	if !hasHash {
		decls = append(decls, t.opaqueHashMethod(recvType, kind))
	}
	if !hasCompare && kind != scalarBool {
		decls = append(decls, t.opaqueCompareMethod(recvType, kind))
	}
	return decls, nil
}

// userDefinedOpaqueMethods reports whether GALA or a hand-written .go file of
// the package declares Hash or Compare on the opaque type, so the synthesized
// one is skipped instead of colliding with it.
func (t *galaASTTransformer) userDefinedOpaqueMethods(typeName string) (hasHash, hasCompare bool) {
	meta := t.getTypeMeta(typeName)
	if meta == nil {
		return false, false
	}
	goMethods := t.goMethodsOnGalaType(meta)
	declared := func(method string) bool {
		_, inGala := meta.Methods[method]
		_, inGo := goMethods[method]
		return inGala || inGo
	}
	return declared("Hash"), declared("Compare")
}

// genericSelfType is `Name` or, for a generic declaration, `Name[T1, T2]`.
func genericSelfType(name string, tParams *ast.FieldList) ast.Expr {
	if tParams == nil {
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

// scalarHelpers names, for each kind, the Go type a value is converted to and
// the std helpers that hash and compare it. A float hashes through HashUint
// of its integer conversion, the rule the collections' own type switch uses.
func scalarHelpers(kind scalarKind) (conv, hash, compare string) {
	switch kind {
	case scalarInt:
		return "int64", "HashInt", "CompareInt"
	case scalarUint:
		return "uint64", "HashUint", "CompareUint"
	case scalarFloat:
		return "float64", "HashUint", "CompareFloat"
	case scalarString:
		return "string", "HashString", "CompareString"
	default:
		return "bool", "HashBool", ""
	}
}

func convertTo(typeName string, x ast.Expr) ast.Expr {
	return &ast.CallExpr{Fun: ast.NewIdent(typeName), Args: []ast.Expr{x}}
}

// opaqueHashMethod is `func (s T) Hash() uint32 { return std.HashX(conv(s)) }`.
func (t *galaASTTransformer) opaqueHashMethod(recvType ast.Expr, kind scalarKind) *ast.FuncDecl {
	conv, hash, _ := scalarHelpers(kind)
	arg := convertTo(conv, ast.NewIdent("s"))
	if kind == scalarFloat {
		arg = convertTo("uint64", convertTo("float64", ast.NewIdent("s")))
	}
	return &ast.FuncDecl{
		Recv: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("s")}, Type: recvType}}},
		Name: ast.NewIdent("Hash"),
		Type: &ast.FuncType{
			Params:  &ast.FieldList{},
			Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("uint32")}}},
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{
			&ast.CallExpr{Fun: t.stdIdent(hash), Args: []ast.Expr{arg}},
		}}}},
	}
}

// opaqueCompareMethod is `func (s T) Compare(other T) int { return
// std.CompareX(conv(s), conv(other)) }`, which makes T an Ordered[T].
func (t *galaASTTransformer) opaqueCompareMethod(recvType ast.Expr, kind scalarKind) *ast.FuncDecl {
	conv, _, compare := scalarHelpers(kind)
	return &ast.FuncDecl{
		Recv: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("s")}, Type: recvType}}},
		Name: ast.NewIdent("Compare"),
		Type: &ast.FuncType{
			Params:  &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("other")}, Type: recvType}}},
			Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("int")}}},
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{
			&ast.CallExpr{Fun: t.stdIdent(compare), Args: []ast.Expr{
				convertTo(conv, ast.NewIdent("s")), convertTo(conv, ast.NewIdent("other")),
			}},
		}}}},
	}
}

// checkOpaqueUnderlying rejects an underlying type an opaque type cannot be
// declared over (GALA-E0062) and classifies the accepted ones.
func (t *galaASTTransformer) checkOpaqueUnderlying(ctx *grammar.OpaqueTypeDeclarationContext, name string, declared transpiler.Type, tParams *ast.FieldList) (scalarKind, error) {
	written := ctx.Type_().GetText()
	reject := func(reason, hint string) (scalarKind, error) {
		tok := ctx.Type_().GetStart()
		return scalarNone, galaerr.NewCodedSemanticError(galaerr.CodeInvalidOpaqueUnderlying,
			tok.GetLine(), tok.GetColumn(),
			fmt.Sprintf("cannot declare opaque type %q over %s: %s", name, written, reason),
			hint,
		).WithSpan(tok.GetColumn() + len([]rune(written)))
	}
	wrapHint := "use a struct to wrap it, or a type alias to name it"

	if tParams != nil {
		for _, p := range tParams.List {
			for _, n := range p.Names {
				if n.Name == written {
					return reject("a type parameter cannot be the underlying type",
						fmt.Sprintf("declare %s over a scalar type; a type parameter may still appear only as a phantom, as in `opaque type %s[%s any] int64`", name, name, n.Name))
				}
			}
		}
	}

	end, kind, viaOpaque := t.resolveScalar(declared)
	if viaOpaque != nil {
		base := "its underlying type"
		if viaOpaque.Underlying != nil && !viaOpaque.Underlying.IsNil() {
			base = viaOpaque.Underlying.String()
		}
		return reject(fmt.Sprintf("%s is itself an opaque type, and an opaque type inherits nothing from the type it is declared over", t.opaqueName(viaOpaque)),
			fmt.Sprintf("declare %s over %s instead", name, base))
	}
	if kind != scalarNone {
		return kind, nil
	}

	switch ty := end.(type) {
	case transpiler.FuncType:
		return reject("a function type cannot be the underlying type of an opaque type",
			"use a type alias to name a function type")
	case transpiler.ArrayType, transpiler.MapType, transpiler.PointerType:
		return reject("Go slices, maps and pointers are Go-interop types, not scalars", wrapHint)
	case transpiler.BasicType:
		switch ty.Name {
		case "any", "error":
			return reject("an interface has no value of its own to make distinct", wrapHint)
		case "complex64", "complex128":
			return reject("complex numbers are not supported as the underlying type", "declare it over an integer, floating-point, string or bool type")
		}
	}
	if reason := t.nonScalarReason(end); reason != "" {
		return reject(reason, wrapHint)
	}
	return reject("it is not a scalar type (bool, string, an integer or a floating-point kind)", wrapHint)
}

// nonScalarReason explains why a GALA or Go named type cannot be the
// underlying type of an opaque type, or returns "" when it cannot tell.
func (t *galaASTTransformer) nonScalarReason(typ transpiler.Type) string {
	base := typ
	if g, ok := typ.(transpiler.GenericType); ok {
		base = g.Base
	}
	name := base.BaseName()
	if pkg := base.GetPackage(); pkg != "" && pkg != t.packageName {
		if _, isNamed := base.(transpiler.NamedType); isNamed {
			name = pkg + "." + base.(transpiler.NamedType).Name
		}
	}
	meta := t.getTypeMeta(name)
	switch {
	case meta == nil:
		if u, ok := t.goNamedUnderlying(base); ok {
			return t.nonScalarReason(u)
		}
		return ""
	case meta.IsSealed:
		return fmt.Sprintf("%s is a sealed type, and a distinct type over it loses all of its methods and its variants", meta.Name)
	case meta.IsShorthand || len(meta.FieldNames) > 0:
		return fmt.Sprintf("%s is a struct, and a distinct type over a struct (a GALA collection included) loses all of its methods", meta.Name)
	case len(meta.Methods) > 0:
		return "an interface has no value of its own to make distinct"
	}
	return ""
}

// opaqueConversionCallee returns the metadata of the opaque type a call
// target names when the call is a conversion `UserID(x)`, or nil.
func (t *galaASTTransformer) opaqueConversionCallee(fun ast.Expr) *transpiler.TypeMetadata {
	base := fun
	switch f := fun.(type) {
	case *ast.IndexExpr:
		base = f.X
	case *ast.IndexListExpr:
		base = f.X
	}
	switch b := base.(type) {
	case *ast.Ident:
		if _, bound := t.bindingRef(b); bound {
			return nil
		}
	case *ast.SelectorExpr:
		// Only `pkg.Type`: `x.Name(...)` on a value is a method call.
		pkg, ok := b.X.(*ast.Ident)
		if !ok || !t.importManager.IsPackage(pkg.Name) {
			return nil
		}
	default:
		return nil
	}
	name := t.getBaseTypeName(fun)
	if name == "" {
		return nil
	}
	meta := t.getTypeMeta(name)
	if meta == nil || !meta.IsOpaque {
		return nil
	}
	return meta
}

// checkOpaqueConversion rejects `OrderID(userID)`, a direct conversion of one
// opaque type into another (GALA-E0063).
func (t *galaASTTransformer) checkOpaqueConversion(target *transpiler.TypeMetadata, arg ast.Expr, argCtx antlr.ParserRuleContext) error {
	from := t.opaqueMeta(t.getExprTypeNameManual(arg))
	if from == nil || sameOpaque(from, target) {
		return nil
	}
	argText := sourceTextOf(argCtx)
	underlying := "the underlying type"
	if from.Underlying != nil && !from.Underlying.IsNil() {
		underlying = from.Underlying.String()
	}
	tok := argCtx.GetStart()
	return galaerr.NewCodedSemanticError(galaerr.CodeOpaqueToOpaqueConversion,
		tok.GetLine(), tok.GetColumn(),
		fmt.Sprintf("cannot convert %s to %s directly: they are different opaque types", t.opaqueName(from), t.opaqueName(target)),
		fmt.Sprintf("convert through the underlying type if this is intended: %s(%s(%s))", t.opaqueName(target), underlying, argText),
	).WithSpan(tok.GetColumn() + len([]rune(argText)))
}

// sourceTextOf is ctx's source text as written, whitespace included.
func sourceTextOf(ctx antlr.ParserRuleContext) string {
	start, stop := ctx.GetStart(), ctx.GetStop()
	if start == nil || stop == nil || start.GetInputStream() == nil {
		return ctx.GetText()
	}
	return start.GetInputStream().GetText(start.GetStart(), stop.GetStop())
}

// synthesizedOpaqueMethod returns the signature of the Hash or Compare the
// transformer generates on the opaque type meta describes, or nil. A
// user-declared method of the name wins: it is in meta.Methods (GALA) or the
// package's .go methods, and lookups consult those first.
func (t *galaASTTransformer) synthesizedOpaqueMethod(meta *transpiler.TypeMetadata, method string) *transpiler.MethodMetadata {
	if meta == nil || !meta.IsOpaque {
		return nil
	}
	switch method {
	case "Hash":
		return &transpiler.MethodMetadata{Name: "Hash", Package: meta.Package, ReturnType: transpiler.BasicType{Name: "uint32"}}
	case "Compare":
		if _, kind, _ := t.resolveScalar(meta.Underlying); kind == scalarBool {
			return nil
		}
		self := t.opaqueSelfType(meta)
		return &transpiler.MethodMetadata{
			Name:       "Compare",
			Package:    meta.Package,
			ParamNames: []string{"other"},
			ParamTypes: []transpiler.Type{self},
			ReturnType: transpiler.BasicType{Name: "int"},
		}
	}
	return nil
}

// isSynthesizedOpaqueMethod reports whether method is a Hash or Compare the
// transformer generates on the opaque type meta describes.
func (t *galaASTTransformer) isSynthesizedOpaqueMethod(meta *transpiler.TypeMetadata, method string) bool {
	return t.synthesizedOpaqueMethod(meta, method) != nil
}

// opaqueSelfType is the type meta declares, instantiated with its own type
// parameters when it has any.
func (t *galaASTTransformer) opaqueSelfType(meta *transpiler.TypeMetadata) transpiler.Type {
	var self transpiler.Type = transpiler.NamedType{Package: meta.Package, Name: meta.Name}
	if len(meta.TypeParams) == 0 {
		return self
	}
	params := make([]transpiler.Type, len(meta.TypeParams))
	for i, p := range meta.TypeParams {
		params[i] = transpiler.BasicType{Name: p}
	}
	return transpiler.GenericType{Base: self, Params: params}
}
