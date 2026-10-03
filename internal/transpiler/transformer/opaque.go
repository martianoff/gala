package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

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
	if !t.hasOpaque || typ == nil || transpiler.IsUnusable(typ) {
		return nil
	}
	switch ty := typ.(type) {
	case transpiler.BasicType:
		if transpiler.IsPrimitiveType(ty.Name) {
			return nil
		}
		return t.opaqueMetaByName(ty.Name)
	case transpiler.NamedType:
		if ty.Package != "" && ty.Package != t.packageName {
			return t.opaqueMetaByName(ty.Package + "." + ty.Name)
		}
		return t.opaqueMetaByName(ty.Name)
	case transpiler.GenericType:
		return t.opaqueMeta(ty.Base)
	}
	return nil
}

// opaqueMetaByName returns the metadata of the opaque type a name resolves
// to, or nil. getTypeMeta also falls back to the packages imported by name,
// where a bare name is not in scope: a local alias or function sharing its
// name with such a package's opaque type is not that type.
func (t *galaASTTransformer) opaqueMetaByName(name string) *transpiler.TypeMetadata {
	meta := t.getTypeMeta(name)
	if meta == nil || !meta.IsOpaque {
		return nil
	}
	if !strings.Contains(name, ".") && t.importedByName(meta.Package) {
		return nil
	}
	return meta
}

// importedByName reports whether pkg is a package this file imports under a
// qualifier only (not dot-imported, not this package).
func (t *galaASTTransformer) importedByName(pkg string) bool {
	if t.isLocalPackage(pkg) || t.importManager.IsDotImported(pkg) {
		return false
	}
	_, ok := t.importManager.Qualifier(pkg)
	return ok
}

// isLocalPackage reports whether pkg is the package being transformed, as
// type metadata names it (main and test keep their declarations unqualified).
func (t *galaASTTransformer) isLocalPackage(pkg string) bool {
	return pkg == "" || pkg == t.packageName || pkg == "main" || pkg == "test"
}

// anyOpaque reports whether types holds an opaque type. Every opaque-type
// check is gated on it, so a program that declares none pays nothing.
func anyOpaque(types map[string]*transpiler.TypeMetadata) bool {
	for _, meta := range types {
		if meta.IsOpaque {
			return true
		}
	}
	return false
}

// underlyingName spells an opaque type's underlying type for a hint.
func underlyingName(meta *transpiler.TypeMetadata) string {
	if meta.Underlying == nil || meta.Underlying.IsNil() {
		return "the underlying type"
	}
	return meta.Underlying.String()
}

// diagQualifier is the qualifier a diagnostic writes before a name declared
// in package pkg: none in this package or under a dot import, the alias this
// file imports pkg under, else the package name.
func (t *galaASTTransformer) diagQualifier(pkg string) string {
	if t.isLocalPackage(pkg) || t.importManager.IsDotImported(pkg) {
		return ""
	}
	if entry, ok := t.importManager.Qualifier(pkg); ok && entry.Alias != "" && !entry.implicit {
		return entry.Alias + "."
	}
	return pkg + "."
}

// opaqueName is how diagnostics spell the opaque type meta describes.
func (t *galaASTTransformer) opaqueName(meta *transpiler.TypeMetadata) string {
	return t.diagQualifier(meta.Package) + meta.Name
}

// opaqueTypeName spells typ, an opaque type, with its type arguments if any:
// `UserID`, `billing.UserID`, `Id[User]`.
func (t *galaASTTransformer) opaqueTypeName(typ transpiler.Type, meta *transpiler.TypeMetadata) string {
	name := t.opaqueName(meta)
	gen, ok := typ.(transpiler.GenericType)
	if !ok || len(gen.Params) == 0 {
		return name
	}
	args := make([]string, len(gen.Params))
	for i, p := range gen.Params {
		args[i] = t.diagTypeName(p)
	}
	return name + "[" + strings.Join(args, ", ") + "]"
}

// diagTypeName spells a type argument for a diagnostic, qualified the way
// this file would write it.
func (t *galaASTTransformer) diagTypeName(typ transpiler.Type) string {
	switch ty := typ.(type) {
	case transpiler.NamedType:
		if ty.Package != "" {
			if actual, ok := t.importManager.ResolveAlias(ty.Package); ok {
				return t.diagQualifier(actual) + ty.Name
			}
			return t.diagQualifier(ty.Package) + ty.Name
		}
	case transpiler.GenericType:
		args := make([]string, len(ty.Params))
		for i, p := range ty.Params {
			args[i] = t.diagTypeName(p)
		}
		return t.diagTypeName(ty.Base) + "[" + strings.Join(args, ", ") + "]"
	}
	return typ.String()
}

// sameOpaqueType reports whether a and b, both opaque types (described by
// ma and mb), are the same type — type arguments included, so the phantom
// instantiations Id[User] and Id[Order] differ. Type arguments are compared
// as the types they resolve to (through aliases and import aliases); one
// that does not resolve to a known type — an open slot, a type parameter —
// is taken to match.
func (t *galaASTTransformer) sameOpaqueType(a transpiler.Type, ma *transpiler.TypeMetadata, b transpiler.Type, mb *transpiler.TypeMetadata) bool {
	if ma.Package != mb.Package || ma.Name != mb.Name {
		return false
	}
	ga, okA := a.(transpiler.GenericType)
	gb, okB := b.(transpiler.GenericType)
	if !okA || !okB || len(ga.Params) != len(gb.Params) {
		return true
	}
	for i := range ga.Params {
		ka, knownA := t.typeArgKey(ga.Params[i])
		kb, knownB := t.typeArgKey(gb.Params[i])
		if knownA && knownB && ka != kb {
			return false
		}
	}
	return true
}

// typeArgKey is a canonical spelling of a type argument, and false when it
// does not resolve to a known type: a type parameter, `any`, or a name GALA
// has no metadata for.
func (t *galaASTTransformer) typeArgKey(typ transpiler.Type) (string, bool) {
	typ = t.followAliasChain(typ)
	if transpiler.IsUnusable(typ) || typ.IsAny() {
		return "", false
	}
	switch ty := typ.(type) {
	case transpiler.BasicType:
		if transpiler.IsPrimitiveType(ty.Name) {
			return canonicalScalar(ty.Name), true
		}
		if t.isActiveTypeParam(ty.Name) {
			return "", false
		}
		return t.typeMetaKey(ty.Name)
	case transpiler.NamedType:
		pkg := ty.Package
		if actual, ok := t.importManager.ResolveAlias(pkg); ok {
			pkg = actual
		}
		if pkg == "" || t.isLocalPackage(pkg) {
			return t.typeMetaKey(ty.Name)
		}
		return t.typeMetaKey(pkg + "." + ty.Name)
	case transpiler.GenericType:
		base, ok := t.typeArgKey(ty.Base)
		if !ok {
			return "", false
		}
		args := make([]string, len(ty.Params))
		for i, p := range ty.Params {
			if args[i], ok = t.typeArgKey(p); !ok {
				return "", false
			}
		}
		return base + "[" + strings.Join(args, ",") + "]", true
	}
	return "", false
}

// typeMetaKey is the declaring package and name of the type name resolves
// to, and false when it resolves to no GALA type.
func (t *galaASTTransformer) typeMetaKey(name string) (string, bool) {
	meta := t.getTypeMeta(name)
	if meta == nil {
		return "", false
	}
	return meta.Package + "." + meta.Name, true
}

// underlyingOf is the type the opaque type meta describes is declared over,
// spelled so it resolves from this file: a bare name of another package's
// declaration (its alias, or a type of its own .go files) is qualified with
// that package. False when meta is not an opaque type.
func (t *galaASTTransformer) underlyingOf(meta *transpiler.TypeMetadata) (transpiler.Type, bool) {
	u, ok := meta.OpaqueUnderlying()
	if !ok {
		return nil, false
	}
	if b, isBasic := u.(transpiler.BasicType); isBasic && !transpiler.IsPrimitiveType(b.Name) && !t.isLocalPackage(meta.Package) {
		return transpiler.NamedType{Package: meta.Package, Name: b.Name}, true
	}
	return u, true
}

// underlyingType is underlyingOf without the flag: nil when meta is not an
// opaque type.
func (t *galaASTTransformer) underlyingType(meta *transpiler.TypeMetadata) transpiler.Type {
	u, _ := t.underlyingOf(meta)
	return u
}

// opaqueUnderlying returns the type an opaque type is declared over, or false
// when typ is not an opaque type. It is for the few consumers that must see
// through the type (codec, numeric slots, Sendable/Shareable); nothing that
// infers or looks up methods may call it.
func (t *galaASTTransformer) opaqueUnderlying(typ transpiler.Type) (transpiler.Type, bool) {
	return t.underlyingOf(t.opaqueMeta(typ))
}

// resolveScalar follows typ through local aliases and Go named types — the
// package's own hand-written .go types included — to the type Go sees at the
// bottom, and classifies it. viaOpaque is the opaque type met on the way, if
// any (an opaque type over another is rejected).
func (t *galaASTTransformer) resolveScalar(typ transpiler.Type) (end transpiler.Type, kind scalarKind, viaOpaque *transpiler.TypeMetadata) {
	for hop := 0; hop < 16 && typ != nil && !typ.IsNil(); hop++ {
		if basic, ok := typ.(transpiler.BasicType); ok {
			if k := scalarKindOfPrimitive(basic.Name); k != scalarNone {
				return typ, k, nil
			}
			if t.isOwnGoType(basic.Name) {
				typ = transpiler.NamedType{Package: t.packageName, Name: basic.Name}
			}
		}
		if meta := t.opaqueMeta(typ); meta != nil {
			return typ, scalarNone, meta
		}
		if next, ok := t.aliasTarget(typ); ok && next.BaseName() != typ.BaseName() {
			typ = next
			continue
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

	hasOwn := t.declaresMethods(name, "Hash", "Compare")
	recvType := t.buildGenericTypeExpr(name, tParams)
	if !hasOwn["Hash"] {
		decls = append(decls, t.opaqueHashMethod(recvType, kind))
	}
	if !hasOwn["Compare"] && kind != scalarBool {
		decls = append(decls, t.opaqueCompareMethod(recvType, kind))
	}
	return decls, nil
}

// scalarHelpers names, for each kind, the std helpers that hash and compare a
// value, and the conversions applied to it first (innermost first) for each.
// A float hashes through HashUint of its integer conversion, the rule the
// collections' own type switch uses.
func scalarHelpers(kind scalarKind) (hash string, hashConv []string, compare, compareConv string) {
	switch kind {
	case scalarInt:
		return "HashInt", []string{"int64"}, "CompareInt", "int64"
	case scalarUint:
		return "HashUint", []string{"uint64"}, "CompareUint", "uint64"
	case scalarFloat:
		return "HashUint", []string{"float64", "uint64"}, "CompareFloat", "float64"
	case scalarString:
		return "HashString", []string{"string"}, "CompareString", "string"
	default:
		return "HashBool", []string{"bool"}, "", ""
	}
}

// convertTo wraps x in the conversions named, innermost first.
func convertTo(x ast.Expr, typeNames ...string) ast.Expr {
	for _, name := range typeNames {
		x = &ast.CallExpr{Fun: ast.NewIdent(name), Args: []ast.Expr{x}}
	}
	return x
}

// opaqueHashMethod is `func (s T) Hash() uint32 { return std.HashX(conv(s)) }`.
func (t *galaASTTransformer) opaqueHashMethod(recvType ast.Expr, kind scalarKind) *ast.FuncDecl {
	hash, conv, _, _ := scalarHelpers(kind)
	arg := convertTo(ast.NewIdent("s"), conv...)
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
	_, _, compare, conv := scalarHelpers(kind)
	return &ast.FuncDecl{
		Recv: &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("s")}, Type: recvType}}},
		Name: ast.NewIdent("Compare"),
		Type: &ast.FuncType{
			Params:  &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("other")}, Type: recvType}}},
			Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("int")}}},
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{
			&ast.CallExpr{Fun: t.stdIdent(compare), Args: []ast.Expr{
				convertTo(ast.NewIdent("s"), conv), convertTo(ast.NewIdent("other"), conv),
			}},
		}}}},
	}
}

// checkOpaqueUnderlying rejects an underlying type an opaque type cannot be
// declared over (GALA-E0062) and classifies the accepted ones.
func (t *galaASTTransformer) checkOpaqueUnderlying(ctx *grammar.OpaqueTypeDeclarationContext, name string, declared transpiler.Type, tParams *ast.FieldList) (scalarKind, error) {
	written := transpiler.SourceText(ctx.Type_())
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
		return reject(fmt.Sprintf("%s is itself an opaque type, and an opaque type inherits nothing from the type it is declared over", t.opaqueName(viaOpaque)),
			fmt.Sprintf("declare %s over %s instead", name, underlyingName(viaOpaque)))
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
	if !t.hasOpaque {
		return nil
	}
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
	if name := t.getBaseTypeName(fun); name != "" {
		return t.opaqueMetaByName(name)
	}
	return nil
}

// checkOpaqueConversion rejects `OrderID(userID)`, a direct conversion of one
// opaque type into another (GALA-E0063).
// targetType is the type the conversion names (`OrderID`, `Id[Order]`).
func (t *galaASTTransformer) checkOpaqueConversion(target *transpiler.TypeMetadata, targetType transpiler.Type, arg ast.Expr, argCtx antlr.ParserRuleContext) error {
	fromType := t.probeExprType(arg)
	from := t.opaqueMeta(fromType)
	if from == nil || t.sameOpaqueType(fromType, from, targetType, target) {
		return nil
	}
	argText := transpiler.SourceText(argCtx)
	tok := argCtx.GetStart()
	return galaerr.NewCodedSemanticError(galaerr.CodeOpaqueToOpaqueConversion,
		tok.GetLine(), tok.GetColumn(),
		fmt.Sprintf("cannot convert %s to %s directly: they are different opaque types",
			t.opaqueTypeName(fromType, from), t.opaqueTypeName(targetType, target)),
		t.throughUnderlyingHint(t.opaqueTypeName(targetType, target), from, argText),
	).WithSpan(tok.GetColumn() + len([]rune(argText)))
}

// throughUnderlyingHint is the hint for a value of opaque type from where
// the opaque type spelled to is wanted: convert through from's underlying
// type.
func (t *galaASTTransformer) throughUnderlyingHint(to string, from *transpiler.TypeMetadata, text string) string {
	return fmt.Sprintf("convert through the underlying type if this is intended: %s(%s(%s))", to, underlyingName(from), text)
}

// isSynthesizedOpaqueMethod reports whether method is a Hash or Compare the
// transformer generates on the opaque type meta describes (Compare is not
// generated over bool). A user-declared method of the name wins: lookups
// consult meta.Methods and the package's .go methods first.
func (t *galaASTTransformer) isSynthesizedOpaqueMethod(meta *transpiler.TypeMetadata, method string) bool {
	if meta == nil || !meta.IsOpaque {
		return false
	}
	switch method {
	case "Hash":
		return true
	case "Compare":
		_, kind, _ := t.resolveScalar(t.underlyingType(meta))
		return kind != scalarBool
	}
	return false
}

// checkOpaqueMismatch rejects a value that would need an implicit conversion
// to fill a slot of type expected (GALA-E0064): an opaque type where its
// underlying type or another opaque type is expected, or the underlying type
// where the opaque type is expected. Untyped constants mix, as in Go. Any
// other mismatch is left to Go, which stays the backstop.
func (t *galaASTTransformer) checkOpaqueMismatch(expr ast.Expr, expected transpiler.Type, exprCtx antlr.ParserRuleContext) error {
	if !t.hasOpaque || expected == nil || transpiler.IsUnusable(expected) || expected.IsAny() {
		return nil
	}
	expM := t.opaqueMeta(expected)
	if expM == nil {
		// Only a scalar slot can be an opaque type's underlying type.
		if _, kind, _ := t.resolveScalar(expected); kind == scalarNone {
			return nil
		}
	}
	actual := t.probeExprType(expr)
	if actual == nil || transpiler.IsUnusable(actual) || actual.IsAny() {
		return nil
	}
	actM := t.opaqueMeta(actual)
	if expM == nil && actM == nil {
		return nil
	}
	// spell names a side of the mismatch: an opaque type as diagnostics
	// spell it, anything else as its type.
	spell := func(typ transpiler.Type, meta *transpiler.TypeMetadata) string {
		if meta != nil {
			return t.opaqueTypeName(typ, meta)
		}
		return typ.String()
	}
	switch {
	case expM != nil && actM != nil:
		if t.sameOpaqueType(expected, expM, actual, actM) {
			return nil
		}
	case expM != nil:
		if isUntypedConst(expr) || !t.sameScalar(actual, t.underlyingType(expM)) {
			return nil
		}
	default:
		if !t.sameScalar(expected, t.underlyingType(actM)) {
			return nil
		}
	}
	text := transpiler.SourceText(exprCtx)
	hint := fmt.Sprintf("convert explicitly: %s(%s)", spell(expected, expM), text)
	if expM != nil && actM != nil {
		hint = t.throughUnderlyingHint(spell(expected, expM), actM, text)
	}
	tok := exprCtx.GetStart()
	return galaerr.NewCodedSemanticError(galaerr.CodeOpaqueTypeMismatch, tok.GetLine(), tok.GetColumn(),
		fmt.Sprintf("cannot use %s (%s) as %s: an opaque type never converts implicitly", text, spell(actual, actM), spell(expected, expM)),
		hint,
	).WithSpan(tok.GetColumn() + len([]rune(text)))
}

// shareableUnderlying is the Sendable/Shareable checker's view of a named
// scalar: an opaque type's underlying scalar, or a Go named type's
// underlying type. The checker accepts either only when it is a primitive.
func (t *galaASTTransformer) shareableUnderlying(typ transpiler.Type) (transpiler.Type, bool) {
	if u, ok := t.opaqueUnderlying(typ); ok {
		end, _, _ := t.resolveScalar(u)
		return end, true
	}
	return t.goNamedUnderlying(typ)
}

// opaqueCodecScalar returns the wire scalar of the opaque type name declared
// in package pkg ("" for this one), read from the declaring package's
// metadata: an opaque type encodes as its underlying scalar. ok is false when
// the name is not an opaque type over a scalar the codec writes.
func (t *galaASTTransformer) opaqueCodecScalar(name, pkg string) (codecScalar, bool) {
	meta := t.opaqueMeta(transpiler.NamedType{Package: pkg, Name: name})
	u, ok := meta.OpaqueUnderlying()
	if !ok {
		return codecScalar{}, false
	}
	owner := meta.Package
	if t.isLocalPackage(owner) {
		owner = ""
	}
	// The underlying type is a scalar, an alias of one (resolved in the
	// declaring package) or a Go named scalar, which codecScalarOf reads.
	sc, _, _, ok := t.codecScalarOf(t.codecUnalias(u, owner))
	return sc, ok
}

// probeExprType is expr's inferred type for a check that only judges when the
// type is known. A give-up here is not an inference failure the generated code
// depends on, so it is kept out of the unresolved-type inventory.
func (t *galaASTTransformer) probeExprType(expr ast.Expr) transpiler.Type {
	warn := t.warnTypeInference
	t.warnTypeInference = false
	defer func() { t.warnTypeInference = warn }()
	return t.getExprTypeNameManual(expr)
}

// sameScalar reports whether a and b, followed through aliases and Go named
// types, end at the same predeclared scalar type.
func (t *galaASTTransformer) sameScalar(a, b transpiler.Type) bool {
	if a == nil || b == nil {
		return false
	}
	endA, kindA, viaA := t.resolveScalar(a)
	endB, kindB, viaB := t.resolveScalar(b)
	return viaA == nil && viaB == nil && kindA != scalarNone && kindA == kindB &&
		canonicalScalar(endA.String()) == canonicalScalar(endB.String())
}

// canonicalScalar maps Go's alias spellings to the type they name.
func canonicalScalar(name string) string {
	switch name {
	case "rune":
		return "int32"
	case "byte":
		return "uint8"
	}
	return name
}

// isUntypedConst reports whether expr is untyped in Go, so it takes the type
// of the slot it fills: an untyped numeric or string constant
// (isUntypedConstExpr), or an untyped bool — true / false, any comparison,
// and `!`, `&&`, `||` over untyped bools. (A logical operator over typed
// operands has their type; inference reports it.)
func isUntypedConst(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name == "true" || e.Name == "false"
	case *ast.ParenExpr:
		return isUntypedConst(e.X)
	case *ast.UnaryExpr:
		if e.Op == token.NOT {
			return isUntypedConst(e.X)
		}
	case *ast.BinaryExpr:
		switch e.Op {
		case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
			return true
		case token.LAND, token.LOR:
			return isUntypedConst(e.X) && isUntypedConst(e.Y)
		}
	}
	return isUntypedConstExpr(expr)
}

// logicalOperandType is the type of a `!`, `&&` or `||` whose operand is an
// opaque type over bool: Go gives the result the operands' type. It returns
// nil for anything else, which inference types as plain bool.
func (t *galaASTTransformer) logicalOperandType(operands ...ast.Expr) transpiler.Type {
	if !t.hasOpaque {
		return nil
	}
	for _, x := range operands {
		if typ := t.getExprTypeNameManual(x); t.opaqueMeta(typ) != nil {
			return typ
		}
	}
	return nil
}
