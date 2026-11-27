package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// tryTransformSizeSugar lowers the `.Size()` / `.ByteSize()` sugar for Go
// primitive receivers (string, slice, map), where bare len() is not part of
// GALA's surface:
//
//	"abc".Size()        -> utf8.RuneCountInString("abc")  // character count
//	"abc".ByteSize()    -> len("abc")                      // raw byte count
//	xs.Size()  ([]T)    -> len(xs)
//	m.Size()   (map)    -> len(m)
//
// It returns handled=false when the receiver is not a Go string/slice/map (e.g.
// a GALA Array/List/HashMap, which has its own Size() method) so the caller
// falls through to normal method dispatch. `sel.X` is already the transformed
// Go receiver expression at this point, so it can be wrapped directly.
func (t *galaASTTransformer) tryTransformSizeSugar(sel *ast.SelectorExpr) (ast.Expr, bool) {
	recvType := t.sizeSugarReceiverType(sel.X)
	// Read through an Immutable[T] val wrapper (`val s = "x"` -> `s.Get()`).
	recv := t.unwrapImmutable(sel.X)
	basic, isString := recvType.(transpiler.BasicType)
	isString = isString && basic.Name == "string"

	lenCall := func() ast.Expr {
		return &ast.CallExpr{Fun: ast.NewIdent("len"), Args: []ast.Expr{recv}}
	}

	switch sel.Sel.Name {
	case "ByteSize":
		// ByteSize is only meaningful on strings; other receivers fall through.
		if isString {
			return lenCall(), true
		}
		return nil, false
	case "Size":
		if isString {
			t.needsUtf8Import = true
			return &ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X:   ast.NewIdent("utf8"),
					Sel: ast.NewIdent("RuneCountInString"),
				},
				Args: []ast.Expr{sel.X},
			}, true
		}
		switch recvType.(type) {
		case transpiler.ArrayType, transpiler.MapType:
			return lenCall(), true
		}
	}
	return nil, false
}

// sizeSugarReceiverType returns the effective type of a .Size()/.ByteSize()
// receiver, reading through an Immutable[T] wrapper so `val s = "x"` (typed
// Immutable[string]) resolves to string.
func (t *galaASTTransformer) sizeSugarReceiverType(recv ast.Expr) transpiler.Type {
	recvType := t.getExprTypeNameManual(recv)
	if t.isImmutableType(recvType) {
		if gt, ok := recvType.(transpiler.GenericType); ok && len(gt.Params) == 1 {
			recvType = gt.Params[0]
		}
	}
	return t.resolveNamedGoCollectionUnderlying(recvType)
}

// goNamedUnderlying resolves a NAMED Go type to its underlying type via the
// analyzer's Go type info — e.g. time.Duration -> int64, url.Values ->
// map[string][]string; an instance of a generic one (`pkg.Bag[string]`,
// `Bag[string]`) to its underlying with the type arguments substituted. The
// type is written package-qualified, or bare (goBareUnderlying, or
// goDeclaredType for an instance): a dot-imported one, or one of the
// package's own .go files. Returns (nil, false) when goTypeInfo is
// absent, the type is unusable, or it is no resolvable Go named type. Every
// read through a Go named type goes through it.
func (t *galaASTTransformer) goNamedUnderlying(typ transpiler.Type) (transpiler.Type, bool) {
	if t.goTypeInfo == nil || transpiler.IsUnusable(typ) {
		return nil, false
	}
	// An instance, its base qualified or bare (goDeclaredType reads either).
	if _, isGeneric := typ.(transpiler.GenericType); isGeneric {
		// Only an instance of a generic function, slice, map or channel
		// type: a struct's or an interface's underlying is no type of its own.
		if _, data, _ := t.goDeclaredType(typ, nil); data == nil || data.Kind == "struct" || data.Kind == "interface" {
			return nil, false
		}
		u := t.goDefinedUnderlying(typ, nil)
		return u, u != nil
	}
	named, ok := typ.(transpiler.NamedType)
	if !ok || named.Package == "" {
		return t.goBareUnderlying(typ)
	}
	td := t.goTypeInfo.GetTypeData(t.goTypeLookupName(named))
	if td == nil || td.Underlying == nil {
		return nil, false
	}
	return td.Underlying, true
}

// resolveNamedGoCollectionUnderlying resolves a NAMED Go type whose underlying is
// a slice or map (e.g. `url.Values` = map[string][]string, `sort.StringSlice` =
// []string) to that underlying ArrayType/MapType, so the .Size() sugar classifies
// it and lowers to len(). Only slice/map underlyings qualify — never a Go struct
// or a GALA type — so this never mis-fires on a real .Size() method. Returns the
// type unchanged when it is not a resolvable named Go collection.
func (t *galaASTTransformer) resolveNamedGoCollectionUnderlying(typ transpiler.Type) transpiler.Type {
	u, ok := t.goNamedUnderlying(typ)
	if !ok {
		return typ
	}
	switch u.(type) {
	case transpiler.ArrayType, transpiler.MapType:
		// Don't shadow a real Size() method the named type declares — call it
		// instead of lowering to len(). (url.Values / sort.StringSlice have none.)
		// The key Go type info files it under, a bare name's included; a type
		// of the package's own .go files may have one GALA declares.
		base := receiverBase(typ)
		if key, _ := t.goTypeDataFor(base); t.goTypeInfo.GetMethodSignature(key, "Size") != nil {
			return typ
		}
		if t.galaDeclaresOwnGoTypeMethod(base, "Size") {
			return typ
		}
		return u
	}
	return typ
}

// galaDeclaresOwnGoTypeMethod reports whether GALA declares method on typ, a
// type of the package's own .go files, written bare or qualified by the
// package. Its metadata holds only the methods GALA gives it, filed under the
// name the analyzer records the receiver by: bare in main and test, qualified
// by the package elsewhere.
func (t *galaASTTransformer) galaDeclaresOwnGoTypeMethod(typ transpiler.Type, method string) bool {
	name, bare := bareTypeName(typ)
	if nt, isNamed := typ.(transpiler.NamedType); isNamed && nt.Package == t.packageName {
		name, bare = nt.Name, true
	}
	if !bare || !t.isOwnGoType(name) || t.richAST == nil {
		return false
	}
	for _, key := range []string{name, t.packageName + "." + name} {
		if meta := t.richAST.Types[key]; meta != nil && meta.Methods[method] != nil {
			return true
		}
	}
	return false
}

func (t *galaASTTransformer) transformCopyCall(receiver ast.Expr, args argsSrc) (ast.Expr, error) {
	// 1. Identify receiver type via the general expression-type inferencer.
	// This handles all receiver shapes uniformly: bare identifiers (`p.Copy(...)`),
	// chained struct-field access (`o.Tab.Copy(...)`, `m.View.Section.Copy(...)`),
	// `.Get().Copy()` chains for std.Immutable wrappers, and any other expression
	// the inferencer can resolve to a struct type.
	typeObj := t.getExprTypeNameManual(receiver)

	// Strip generic decorations (e.g. "Person[int]" -> "Person") and any pointer
	// prefix so the name lines up with the structFields map keys.
	typeName := stripTypeNameDecorations(typeObj.String())
	if typeName == "" {
		// If we can't find the type, we might still be able to proceed if it's a direct struct literal copy,
		// but GALA seems to prefer explicit types for Copy overrides.
		// For now, let's assume it's required to know the type for overrides.
		// If no overrides, we just call the regular Copy() method.
		if len(args.list) == 0 {
			return &ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X:   receiver,
					Sel: ast.NewIdent("Copy"),
				},
			}, nil
		}
		// If there are overrides, we need the type.
		al, ac := args.at.start()
		return nil, galaerr.NewSemanticErrorAt(al, ac, "cannot use Copy overrides: type of receiver unknown")
	}

	// The type may be spelled with an import alias (`l.Point`, from a
	// signature or a field), while struct fields are keyed by the package's
	// own name: resolve it the way every other type lookup does.
	if resolved, found := t.resolveTypeName(typeName, func(name string) bool {
		_, ok := t.structFields[name]
		return ok
	}); found {
		typeName = resolved
	}
	fields, ok := t.structFields[typeName]
	if !ok {
		// If it's not a struct type but we have overrides, compilation error
		if slices.ContainsFunc(args.list, argSrc.named) {
			al, ac := args.at.start()
			return nil, galaerr.NewSemanticErrorAt(al, ac, "Copy overrides only supported for struct types")
		}
		// Fallback to regular Copy() call if no named overrides (though the grammar allows them now)
		return &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   receiver,
				Sel: ast.NewIdent("Copy"),
			},
		}, nil
	}

	// The result is the receiver's own instantiated type; its type arguments
	// also bind the type parameters in the overridden fields' declared types.
	resultType := t.copyResultTypeExpr(typeName, typeObj)
	fieldTypes := t.structFieldTypes[typeName]
	var typeArgs map[string]transpiler.Type
	if subst := t.structTypeArgSubst(resultType, typeName); subst != nil {
		typeArgs = t.typeArgTypes(subst)
	}

	// Another package's struct may keep fields this package cannot name — a
	// private field, or a sealed type's variant tag.
	meta := t.getTypeMeta(typeName)

	// 2. Parse overrides
	overrides := make(map[string]ast.Expr)
	overrideTypes := make(map[string]transpiler.Type)
	for _, arg := range args.list {
		argLine, argCol := arg.at().start()
		fieldName, _ := arg.name()
		if fieldName == "" {
			return nil, galaerr.NewSemanticErrorAt(argLine, argCol, "Copy overrides must be named: Copy(field = value)")
		}
		if (meta == nil || !meta.IsSealed) && !slices.Contains(fields, fieldName) {
			return nil, galaerr.NewSemanticErrorAt(argLine, argCol, fmt.Sprintf("struct %s has no field %s", typeName, fieldName))
		}
		// Visibility is the GALA name's: a sealed case field stored under a
		// Go name of its case (`Bx`) is as private as `x`.
		if meta != nil && !visibleFrom(fieldName, meta, t.packageName) {
			return nil, galaerr.NewSemanticErrorAt(argLine, argCol,
				fmt.Sprintf("field %s of %s is unexported, so only package %s can set it", fieldName, typeName, meta.Package))
		}
		if meta != nil && meta.IsSealed {
			goName, err := sealedCopyField(meta, fieldName, sealedCaseOf(receiver, meta), argLine, argCol)
			if err != nil {
				return nil, err
			}
			fieldName = goName
		}
		// A lambda override parses as the argument rule's lambdaExpression
		// alternative rather than as a pattern, so read the argument the way
		// function calls do.
		v, err := t.argContent(arg)
		if err != nil || v.spread {
			return nil, galaerr.NewSemanticErrorAt(argLine, argCol, "Copy overrides must be expressions")
		}
		// An override fills the field's slot exactly as a named constructor
		// argument does: the field's declared type is the expected type, so a
		// lambda infers its parameter types from it and the other argument
		// forms (partial functions, by-name thunks, generic calls) see it too.
		var expected transpiler.Type = transpiler.NilType{}
		if ft, ok := fieldTypes[fieldName]; ok && ft != nil {
			expected = ft
			if typeArgs != nil {
				expected = t.substituteInType(expected, typeArgs)
			}
		}
		var val ast.Expr
		val, err = t.lowerArg(v, typedSlot(expected), false)
		if err != nil {
			return nil, err
		}
		overrides[fieldName] = val
		overrideTypes[fieldName] = expected
	}
	immutFlags := t.structImmutFields[typeName]
	for i, fn := range fields {
		if val, ok := overrides[fn]; ok && i < len(immutFlags) && immutFlags[i] {
			overrides[fn] = t.newImmutableFor(val, overrideTypes[fn])
		}
	}

	// A kept field this package cannot name cannot be read into a new
	// literal, so copy the receiver with its own Copy() and assign the
	// overrides — every one of them visible, as checked above — to the copy.
	if meta != nil && slices.ContainsFunc(fields, func(f string) bool { return !visibleFrom(f, meta, t.packageName) }) {
		cp := ast.NewIdent(t.nextTempVar())
		var body []ast.Stmt
		for _, fn := range fields {
			if val, ok := overrides[fn]; ok {
				body = append(body, &ast.AssignStmt{
					Lhs: []ast.Expr{&ast.SelectorExpr{X: cp, Sel: ast.NewIdent(fn)}},
					Tok: token.ASSIGN,
					Rhs: []ast.Expr{val},
				})
			}
		}
		body = append(body, &ast.ReturnStmt{Results: []ast.Expr{cp}})
		return &ast.CallExpr{
			Fun: &ast.FuncLit{
				Type: &ast.FuncType{
					Params:  &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{cp}, Type: resultType}}},
					Results: &ast.FieldList{List: []*ast.Field{{Type: t.copyResultTypeExpr(typeName, typeObj)}}},
				},
				Body: &ast.BlockStmt{List: body},
			},
			Args: []ast.Expr{&ast.CallExpr{Fun: &ast.SelectorExpr{X: receiver, Sel: ast.NewIdent("Copy")}}},
		}, nil
	}

	// 3. Construct new struct instance. Each kept field reads the receiver, so
	// a receiver that is not a plain variable path (e.g. `f().Copy(...)`) would
	// run once per kept field, and one whose fields are all overridden would
	// not be read at all — never evaluated, and a Go "declared and not used"
	// error for a local. In either case bind it once as the parameter of an
	// immediately-applied function literal and read the kept fields from that.
	source := receiver
	allOverridden := len(overrides) == len(fields)
	bind := allOverridden || !t.isPlainReceiverPath(receiver)
	if allOverridden {
		source = ast.NewIdent("_") // evaluated, never read
	} else if bind {
		source = ast.NewIdent(t.nextTempVar())
	}
	var elts []ast.Expr
	for _, fn := range fields {
		if val, ok := overrides[fn]; ok {
			elts = append(elts, &ast.KeyValueExpr{
				Key:   ast.NewIdent(fn),
				Value: val,
			})
		} else {
			elts = append(elts, &ast.KeyValueExpr{
				Key: ast.NewIdent(fn),
				Value: &ast.CallExpr{
					Fun: t.stdIdent(transpiler.FuncCopy),
					Args: []ast.Expr{
						&ast.SelectorExpr{
							X:   source,
							Sel: ast.NewIdent(fn),
						},
					},
				},
			})
		}
	}

	lit := &ast.CompositeLit{
		Type: resultType,
		Elts: elts,
	}
	if !bind {
		return lit, nil
	}
	return &ast.CallExpr{
		Fun: &ast.FuncLit{
			Type: &ast.FuncType{
				Params: &ast.FieldList{List: []*ast.Field{{
					Names: []*ast.Ident{source.(*ast.Ident)},
					Type:  t.typeToExpr(typeObj),
				}}},
				Results: &ast.FieldList{List: []*ast.Field{{Type: t.copyResultTypeExpr(typeName, typeObj)}}},
			},
			Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{lit}}}},
		},
		Args: []ast.Expr{receiver},
	}, nil
}

// isPlainReceiverPath reports whether reading a Copy receiver more than once
// is equivalent to reading it once: a variable, a field path off one, and the
// nullary accessor the transformer inserts — `.Get()` unwrapping an Immutable
// val or field. A user type's own `Get()` method may have side effects, so it
// counts as plain only when its operand is provably the wrapper; anything
// unproven is bound once instead, which is always correct.
func (t *galaASTTransformer) isPlainReceiverPath(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return true
	case *ast.ParenExpr:
		return t.isPlainReceiverPath(e.X)
	case *ast.SelectorExpr:
		return t.isPlainReceiverPath(e.X)
	case *ast.CallExpr:
		x, ok := t.immutableGetOperand(e)
		return ok && t.isPlainReceiverPath(x)
	}
	return false
}

// immutableGetOperand returns x when call is `x.Get()` unwrapping an
// Immutable — a val, an immutable field or an Immutable[T] value.
func (t *galaASTTransformer) immutableGetOperand(call *ast.CallExpr) (ast.Expr, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != transpiler.MethodGet || len(call.Args) != 0 || !t.isImmutableOperand(sel.X) {
		return nil, false
	}
	return sel.X, true
}

// isImmutableOperand reports whether x is an Immutable wrapper: a val binding
// (whose inferred type is its unwrapped value type), an Immutable struct
// field, or an expression whose inferred type is Immutable.
func (t *galaASTTransformer) isImmutableOperand(x ast.Expr) bool {
	switch e := x.(type) {
	case *ast.Ident:
		if t.isBoxedVal(e.Name) {
			return true
		}
	case *ast.SelectorExpr:
		if t.isImmutableField(t.getExprTypeName(e.X), e, e.Sel.Name) {
			return true
		}
	}
	return t.isImmutableType(t.getExprTypeName(x))
}

// copyResultTypeExpr builds the composite-literal type for an inlined
// `recv.Copy(field = value)`. Copy cannot change field types, so the result is
// the receiver's own instantiated type: a generic receiver keeps its type
// arguments (`Box[string]`, `Box[T]`), and a pointer receiver yields the
// pointee's value type — matching the generated `func (s Box[T]) Copy() Box[T]`.
func (t *galaASTTransformer) copyResultTypeExpr(typeName string, recvType transpiler.Type) ast.Expr {
	base := t.ident(typeName)
	for {
		ptr, ok := recvType.(transpiler.PointerType)
		if !ok {
			break
		}
		recvType = ptr.Elem
	}
	gen, ok := recvType.(transpiler.GenericType)
	if !ok || len(gen.Params) == 0 {
		return base
	}
	params := make([]ast.Expr, len(gen.Params))
	for i, p := range gen.Params {
		params[i] = t.typeToExpr(p)
	}
	if len(params) == 1 {
		return &ast.IndexExpr{X: base, Index: params[0]}
	}
	return &ast.IndexListExpr{X: base, Indices: params}
}

func (t *galaASTTransformer) initGenericMethods() {
	t.genericMethods = make(map[string]map[string]bool)
	t.structFieldTypes = make(map[string]map[string]transpiler.Type)
	t.functions = make(map[string]*transpiler.FunctionMetadata)
	t.typeMetas = make(map[string]*transpiler.TypeMetadata)
}

// generatesStructString reports whether shorthand struct name gets a
// generated String method: when transpiler.GeneratedMethodNames lists it (the
// struct has no field named String, which Go cannot hold beside a method of
// the name) and the source does not declare a String itself, in GALA or in a
// hand-written .go file of its package. A declared String replaces the
// generated one, so it must be `String() string` (GALA-E0093, see
// userGeneratedMethod).
func (t *galaASTTransformer) generatesStructString(name string) (bool, error) {
	meta := t.getTypeMeta(name)
	if meta == nil || !transpiler.GeneratedMethodNames(meta)["String"] {
		return false, nil
	}
	declared, err := t.userGeneratedMethod(name, "struct", "String", "string")
	return !declared, err
}

// generateStructStringMethod generates the String method of a shorthand
// struct, so it prints as GALA writes it, as a sealed value does:
//
//	func (s Pt) String() string {
//		return std.FormatStruct("Pt", s.X, s.Y)
//	}
//
// One call into std keeps the method small (see std.FormatStruct); a struct
// with no fields returns its text, "Pt()".
func (t *galaASTTransformer) generateStructStringMethod(name string, fieldNames []string, tParams *ast.FieldList) *ast.FuncDecl {
	fields := make([]ast.Expr, len(fieldNames))
	for i, f := range fieldNames {
		fields[i] = &ast.SelectorExpr{X: ast.NewIdent("s"), Sel: ast.NewIdent(f)}
	}
	return t.stringMethodDecl(name, tParams, &ast.ReturnStmt{Results: []ast.Expr{t.formatStructExpr(name, fields)}})
}

// printedValue is expr, a value Println, Print or an interpolation prints,
// as it is passed to fmt, and the type it then has; typ is expr's type. A
// value that prints through a pointer to it (see pointerPrinted), by a String
// declared in GALA on its pointer receiver, which fmt, given the value, would
// not call:
//
//   - one of a GALA type becomes the text a pointer to it prints as, of type
//     string: by that String, or by a pointer-receiver Format or Error,
//     which fmt calls first. The method runs where any pointer-receiver
//     method would: on the value itself when printedInPlace,
//     `std.StringAt(&expr)`, so it sees what other pointers write and copies
//     no lock the value holds; otherwise (a val, a call's result) on a copy,
//     `std.PtrString(expr)`; and
//   - one whose type is known only at run time — a type parameter, `any` or
//     another interface, as an element a collection or a tuple prints is —
//     becomes `std.Printed(expr)`, which makes the same choice at run time
//     and otherwise is the value itself, so typ is unchanged.
//
// Every other value is expr itself.
//
// A print that would run that method on a copy of a value that must not be
// copied (see noCopyReason) is GALA-E0053, as a call of it would be: the
// value printed when it is not printedInPlace, or a field its generated
// String prints (see checkPrintedFieldsCopy). Printing a value that is not
// printedInPlace by a method that can change its receiver is GALA-E0110, as
// that call would be, and so is printing a value whose generated String
// prints a field by such a method, which runs on a copy of the field. line
// and col locate the printed value.
func (t *galaASTTransformer) printedValue(expr ast.Expr, typ transpiler.Type, line, col int) (ast.Expr, transpiler.Type, error) {
	if transpiler.IsUnusable(typ) {
		return expr, typ, nil
	}
	// An error prints by its Error, which every value it holds has.
	if t.holdsAnyType(typ) && !isBasicNamed(typ, "error") {
		return &ast.CallExpr{Fun: t.stdIdent("Printed"), Args: []ast.Expr{expr}}, typ, nil
	}
	if _, isPtr := typ.(transpiler.PointerType); isPtr {
		return expr, typ, t.checkPrintedFieldsCopy(typ, line, col)
	}
	method, galaString := t.pointerPrinted(typ)
	if method == "" || !galaString {
		return expr, typ, t.checkPrintedFieldsCopy(typ, line, col)
	}
	if t.printedInPlace(expr) {
		return &ast.CallExpr{Fun: t.stdIdent("StringAt"), Args: []ast.Expr{&ast.UnaryExpr{Op: token.AND, X: expr}}}, transpiler.BasicType{Name: "string"}, nil
	}
	if typeName, why := t.noCopyWhy(typ); why != "" {
		return nil, nil, galaerr.NewCodedSemanticError(galaerr.CodeNoCopyReceiverCopied, line, col,
			fmt.Sprintf("cannot print this %s: it cannot be addressed here, so its %s would run on a copy, and %s", typeName, method, why),
			"declare it with `var`, or hold a pointer (`&T{...}`)")
	}
	if t.pointerMethodMutates(typ, method) {
		return nil, nil, mutatingCallOnCopyError(method, true, typ, line, col)
	}
	return &ast.CallExpr{Fun: t.stdIdent("PtrString"), Args: []ast.Expr{expr}}, transpiler.BasicType{Name: "string"}, nil
}

// printMethods are the methods fmt prints a value by, in the order it looks
// for them: fmt.Formatter's Format, error's Error, fmt.Stringer's String.
var printMethods = [...]string{"Format", "Error", "String"}

// printChoice is a method fmt prints a value by (see printedBy).
type printChoice struct {
	method    string // "Format", "Error" or "String"; "" for none
	onPointer bool   // declared on the pointer receiver
	generated bool   // the String generated for a GALA struct or sealed type
	inGALA    bool   // declared in GALA or generated, not declared in Go
}

// printChoices is the method fmt prints a value of typ by, chosen as fmt
// chooses it: Format, then Error, then String (printMethods), each with its
// interface's signature, declared in GALA, generated, or declared in Go.
// byValue is fmt's choice given the value, which has the value-receiver
// methods; byPointer, given a pointer to it, which has them all. str is the
// type's String, wherever fmt would call it. Each is the zero printChoice
// when there is none.
func (t *galaASTTransformer) printChoices(typ transpiler.Type) (byValue, byPointer, str printChoice) {
	_, meta, goData, ok := t.receiverTypeInfo(typ)
	if !ok {
		return
	}
	for _, name := range printMethods {
		c := t.printMethod(meta, goData, name)
		if c.method == "" {
			continue
		}
		if byPointer.method == "" {
			byPointer = c
		}
		if byValue.method == "" && !c.onPointer {
			byValue = c
		}
		if name == "String" {
			str = c
		}
	}
	return
}

// printedBy is the method fmt prints a value of typ by (see printChoices):
// given the value, or, when viaPointer, a pointer to it.
func (t *galaASTTransformer) printedBy(typ transpiler.Type, viaPointer bool) printChoice {
	byValue, byPointer, _ := t.printChoices(typ)
	if viaPointer {
		return byPointer
	}
	return byValue
}

// pointerPrinted is the method GALA prints a value of typ by through a
// pointer to it, as std's run-time printsByPointerString chooses: when fmt,
// given the value, prints it by none of its own methods, and a pointer to it
// has a String, the value prints as fmt prints that pointer, by the method
// printChoices names for it (a pointer-receiver Format or Error before the
// String). method is "" for any other value. galaString reports whether that
// String is declared in GALA rather than in Go.
func (t *galaASTTransformer) pointerPrinted(typ transpiler.Type) (method string, galaString bool) {
	byValue, byPointer, str := t.printChoices(typ)
	if byValue.method != "" || str.method == "" {
		return "", false
	}
	return byPointer.method, str.inGALA
}

// printMethod is name, one of printMethods, as the type meta and goData
// describe declares it with the signature fmt calls it by: in GALA, as the
// generated String, or in Go, on a Go type or by hand-written Go on a GALA
// type. It is the zero printChoice when the type has no such method.
func (t *galaASTTransformer) printMethod(meta *transpiler.TypeMetadata, goData *transpiler.GoTypeData, name string) printChoice {
	if meta != nil {
		if name == "String" && t.printsByGeneratedString(meta) {
			return printChoice{method: name, generated: true, inGALA: true}
		}
		if m := meta.Methods[name]; m != nil && !m.GoDeclared && printSignature(name, m.ParamTypes, m.ReturnType) {
			return printChoice{method: name, onPointer: m.PointerReceiver, inGALA: true}
		}
	}
	if goData == nil {
		return printChoice{}
	}
	if sig := goData.Methods[name]; sig == nil || !goPrintSignature(name, sig) {
		return printChoice{}
	}
	return printChoice{method: name, onPointer: goData.PointerMethods[name]}
}

// printSignature reports whether a GALA method's params and result are those
// of name, one of printMethods, as fmt's interfaces declare it:
// `Format(fmt.State, rune)`, `Error() string`, `String() string`.
func printSignature(name string, params []transpiler.Type, result transpiler.Type) bool {
	if name == "Format" {
		return isVoidResult(result) && isFormatterParams(params)
	}
	return len(params) == 0 && isBasicNamed(result, "string")
}

// goPrintSignature is printSignature for a Go method's signature sig.
func goPrintSignature(name string, sig *transpiler.GoFuncSignature) bool {
	if name == "Format" {
		return len(sig.Returns) == 0 && len(sig.Params) == 2 &&
			isFormatterParams([]transpiler.Type{sig.Params[0].Type, sig.Params[1].Type})
	}
	return len(sig.Params) == 0 && len(sig.Returns) == 1 && isBasicNamed(sig.Returns[0], "string")
}

// printsByGeneratedString reports whether a value of the GALA type meta
// describes has the String generated for it, which prints its fields by
// std.FormatStruct: one fmt calls unless the value has a Format or an Error
// (see printedBy).
func (t *galaASTTransformer) printsByGeneratedString(meta *transpiler.TypeMetadata) bool {
	if !transpiler.GeneratedMethodNames(meta)["String"] {
		return false
	}
	if m := meta.Methods["String"]; m != nil && !m.OfTypeDecl {
		return false
	}
	_, inGo := t.goMethodsOnGalaType(meta)["String"]
	return !inGo
}

// checkPrintedFieldsCopy reports an error when printing a value of typ, or
// the value a pointer typ points to, runs a pointer-receiver method on a copy
// of a field (see printedField) where the copy does harm: GALA-E0053 when the
// field must not be copied, and GALA-E0110 when that method can change its
// receiver, since the change would be made to the copy and lost, as for a
// call of it on a value that cannot be addressed. line and col locate the
// printed value.
func (t *galaASTTransformer) checkPrintedFieldsCopy(typ transpiler.Type, line, col int) error {
	ptr, isPtr := typ.(transpiler.PointerType)
	if isPtr {
		typ = ptr.Elem // fmt prints the pointer by its method set
	}
	if path, fieldType := t.printedField(typ, isPtr, t.noCopyPrintedField, make(map[string]bool)); path != "" {
		typeName, why := t.noCopyWhy(fieldType)
		method, _ := t.pointerPrinted(fieldType)
		return galaerr.NewCodedSemanticError(galaerr.CodeNoCopyReceiverCopied, line, col,
			fmt.Sprintf("cannot print this %s: its field %s, a %s, prints by its %s on a copy, and %s", receiverBase(typ), path, typeName, method, why),
			fmt.Sprintf("hold the field as a pointer (`*%s`), or declare a String for %s", fieldType, receiverBase(typ)))
	}
	if path, fieldType := t.printedField(typ, isPtr, t.mutatedPrintedField, make(map[string]bool)); path != "" {
		method, _ := t.pointerPrinted(fieldType)
		return galaerr.NewCodedSemanticError(galaerr.CodeMutatingCallOnCopy, line, col,
			fmt.Sprintf("cannot print this %s: its field %s, a %s, prints by its %s on a copy, and that %s can change its receiver, so the change would be made to the copy and lost", receiverBase(typ), path, receiverBase(fieldType), method, method),
			fmt.Sprintf("hold field %s as a `Ptr[%s]`, so its %s runs on the value it points to", path, fieldType, method))
	}
	return nil
}

// printedField finds a field that printing a value of typ, or a pointer to
// one when viaPointer, prints in a way sought says is harmful. Only a value
// printed by its generated String (a shorthand struct's or a sealed type's;
// see printedBy) is searched: that String's std.FormatStruct prints each
// field through a copy of it. sought is given each field's type, with typ's
// type arguments substituted and a pointer's target in place of the pointer
// (viaPointer), and says whether the field is the one sought (found), and
// whether to look no further into it (leaf). A field it does neither for is
// searched in turn, held by value or by pointer: either way a generated
// String of it runs on a copy. It returns the field's path from typ (`inner`
// or `inner.g`) and its type, or "" when there is none. A sealed type's cases
// are searched together, since the printed value's case is not known. seen
// guards a recursive type.
func (t *galaASTTransformer) printedField(typ transpiler.Type, viaPointer bool, sought func(fieldType transpiler.Type, viaPointer bool) (found, leaf bool), seen map[string]bool) (string, transpiler.Type) {
	_, meta, _, ok := t.receiverTypeInfo(typ)
	if !ok || meta == nil || seen[typ.String()] || !t.printedBy(typ, viaPointer).generated {
		return "", nil
	}
	seen[typ.String()] = true
	typeArgs := t.getReceiverTypeArgTypes(typ)
	var names []string
	var types []transpiler.Type
	if meta.IsSealed {
		for _, v := range meta.SealedVariants {
			names = append(names, v.FieldNames...)
			types = append(types, v.FieldTypes...)
		}
	} else {
		for _, name := range meta.FieldNames {
			names = append(names, name)
			types = append(types, meta.Fields[name])
		}
	}
	for i, fieldType := range types {
		fieldType = t.substituteConcreteTypes(fieldType, meta.TypeParams, typeArgs)
		ptr, fieldViaPointer := fieldType.(transpiler.PointerType)
		if fieldViaPointer {
			fieldType = ptr.Elem
		}
		found, leaf := sought(fieldType, fieldViaPointer)
		if found {
			return names[i], fieldType
		}
		if leaf {
			continue
		}
		if path, inner := t.printedField(fieldType, fieldViaPointer, sought, seen); path != "" {
			return names[i] + "." + path, inner
		}
	}
	return "", nil
}

// noCopyPrintedField is printedField's test for a field that must not be
// copied (see noCopyReason) and prints through a pointer by a String declared
// in GALA on its pointer receiver (see pointerPrinted), held by value:
// std.FormatStruct runs the method it prints by on a copy of it. Through a
// pointer it runs on the field itself.
func (t *galaASTTransformer) noCopyPrintedField(fieldType transpiler.Type, viaPointer bool) (found, leaf bool) {
	// A type holding nothing that must not be copied has no such field.
	if t.noCopyReason(fieldType, make(map[string]bool)) == "" {
		return false, true
	}
	if method, galaString := t.pointerPrinted(fieldType); method != "" && galaString {
		return !viaPointer, true
	}
	return false, false
}

// mutatedPrintedField is printedField's test for a field held by value that
// prints through a pointer (see pointerPrinted), by a String declared in GALA
// or in Go on its pointer receiver, or the Format or Error fmt calls before
// it, that can change its receiver (see pointerMethodMutates):
// std.FormatStruct runs that method on a copy of the field, so the change is
// lost. Through a pointer it runs on the field itself.
func (t *galaASTTransformer) mutatedPrintedField(fieldType transpiler.Type, viaPointer bool) (found, leaf bool) {
	method, _ := t.pointerPrinted(fieldType)
	if method == "" {
		return false, false
	}
	return !viaPointer && t.pointerMethodMutates(fieldType, method), true
}

// isFormatterParams reports whether params are fmt.Formatter's Format
// parameters, (fmt.State, rune).
func isFormatterParams(params []transpiler.Type) bool {
	if len(params) != 2 || !(isBasicNamed(params[1], "rune") || isBasicNamed(params[1], "int32")) {
		return false
	}
	named, isNamed := params[0].(transpiler.NamedType)
	return isNamed && named.Name == "State" && (named.Package == "fmt" || named.ImportPath == "fmt")
}

// isVoidResult reports whether typ, a GALA method's ReturnType, is no result.
func isVoidResult(typ transpiler.Type) bool {
	_, isVoid := typ.(transpiler.VoidType)
	return isVoid
}

// printedInPlace reports whether expr, a generated Go expression of a value
// type, is printed through its own address, `&expr`: a pointer-receiver
// method call would run on it in place (isAddressable), and `&` of it
// compiles. That is `*p` or a path from a variable (a var, a parameter, a
// pattern binding) whose every index is into a slice or an array; not a
// call's result, such as an Immutable's Get, nor a path from a local val that
// sealPlainValPath would re-box as one. isAddressable takes an index into a
// value of unresolved type for addressable, which `&` of a map element is not.
func (t *galaASTTransformer) printedInPlace(expr ast.Expr) bool {
	if _, ok := expr.(*ast.StarExpr); ok {
		return true
	}
	if !t.variablePath(expr) {
		return false
	}
	if root, judged := t.plainValPathRoot(expr); root != nil && !judged {
		return false
	}
	return t.isAddressable(expr)
}

// variablePath reports whether path is an identifier, or fields and elements
// reached from one, indexing only slices and arrays.
func (t *galaASTTransformer) variablePath(path ast.Expr) bool {
	for {
		switch e := path.(type) {
		case *ast.Ident:
			return true
		case *ast.ParenExpr:
			path = e.X
		case *ast.SelectorExpr:
			path = e.X
		case *ast.IndexExpr:
			if _, isArray := t.getExprTypeName(e.X).(transpiler.ArrayType); !isArray {
				return false
			}
			path = e.X
		default:
			return false
		}
	}
}

// formatStructExpr is the text a struct or sealed case called name prints
// as, from its field values fields: `std.FormatStruct("Pt", s.X, s.Y)`, or
// the literal "Pt()" for no fields.
func (t *galaASTTransformer) formatStructExpr(name string, fields []ast.Expr) ast.Expr {
	if len(fields) == 0 {
		return stringLit(name + "()")
	}
	return &ast.CallExpr{Fun: t.stdIdent("FormatStruct"), Args: append([]ast.Expr{stringLit(name)}, fields...)}
}

// stringMethodDecl is the generated `func (s T) String() string` of type
// name, with type parameters tParams, whose body is body.
func (t *galaASTTransformer) stringMethodDecl(name string, tParams *ast.FieldList, body ast.Stmt) *ast.FuncDecl {
	return &ast.FuncDecl{
		Recv: &ast.FieldList{List: []*ast.Field{{
			Names: []*ast.Ident{ast.NewIdent("s")},
			Type:  t.buildGenericTypeExpr(name, tParams),
		}}},
		Name: ast.NewIdent("String"),
		Type: &ast.FuncType{
			Params:  &ast.FieldList{},
			Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("string")}}},
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{body}},
	}
}

func (t *galaASTTransformer) generateCopyMethod(name string, fields *ast.FieldList, tParams *ast.FieldList) (*ast.FuncDecl, error) {
	var elts []ast.Expr
	fieldCtx := newFieldTypeCtx(tParams)
	for _, field := range fields.List {
		for _, fieldName := range field.Names {
			elts = append(elts, &ast.KeyValueExpr{
				Key: ast.NewIdent(fieldName.Name),
				Value: t.fieldCopyExpr(fieldCtx, field.Type, &ast.SelectorExpr{
					X:   ast.NewIdent("s"),
					Sel: ast.NewIdent(fieldName.Name),
				}),
			})
		}
	}

	retType := ast.Expr(ast.NewIdent(name))
	if tParams != nil {
		var indices []ast.Expr
		for _, p := range tParams.List {
			for _, n := range p.Names {
				indices = append(indices, ast.NewIdent(n.Name))
			}
		}
		retType = &ast.IndexListExpr{
			X:       ast.NewIdent(name),
			Indices: indices,
		}
	}

	return &ast.FuncDecl{
		Recv: &ast.FieldList{
			List: []*ast.Field{
				{
					Names: []*ast.Ident{ast.NewIdent("s")},
					Type:  retType,
				},
			},
		},
		Name: ast.NewIdent("Copy"),
		Type: &ast.FuncType{
			Results: &ast.FieldList{
				List: []*ast.Field{{Type: retType}},
			},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ReturnStmt{
					Results: []ast.Expr{
						&ast.CompositeLit{
							Type: retType,
							Elts: elts,
						},
					},
				},
			},
		},
	}, nil
}

func (t *galaASTTransformer) generateEqualMethod(name string, fields *ast.FieldList, tParams *ast.FieldList) (*ast.FuncDecl, error) {
	var condition ast.Expr
	fieldCtx := newFieldTypeCtx(tParams)
	for _, field := range fields.List {
		for _, fieldName := range field.Names {
			expr := t.fieldEqualExpr(fieldCtx, field.Type,
				&ast.SelectorExpr{X: ast.NewIdent("s"), Sel: ast.NewIdent(fieldName.Name)},
				&ast.SelectorExpr{X: ast.NewIdent("other"), Sel: ast.NewIdent(fieldName.Name)})

			if condition == nil {
				condition = expr
			} else {
				condition = &ast.BinaryExpr{
					X:  condition,
					Op: token.LAND,
					Y:  expr,
				}
			}
		}
	}

	if condition == nil {
		condition = ast.NewIdent("true")
	}

	retType := ast.Expr(ast.NewIdent(name))
	if tParams != nil {
		var indices []ast.Expr
		for _, p := range tParams.List {
			for _, n := range p.Names {
				indices = append(indices, ast.NewIdent(n.Name))
			}
		}
		retType = &ast.IndexListExpr{
			X:       ast.NewIdent(name),
			Indices: indices,
		}
	}

	return &ast.FuncDecl{
		Recv: &ast.FieldList{
			List: []*ast.Field{
				{
					Names: []*ast.Ident{ast.NewIdent("s")},
					Type:  retType,
				},
			},
		},
		Name: ast.NewIdent("Equal"),
		Type: &ast.FuncType{
			Params: &ast.FieldList{
				List: []*ast.Field{
					{
						Names: []*ast.Ident{ast.NewIdent("other")},
						Type:  retType,
					},
				},
			},
			Results: &ast.FieldList{
				List: []*ast.Field{{Type: ast.NewIdent("bool")}},
			},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ReturnStmt{
					Results: []ast.Expr{condition},
				},
			},
		},
	}, nil
}

func (t *galaASTTransformer) generateUnapplyMethod(name string, fields *ast.FieldList, tParams *ast.FieldList) (*ast.FuncDecl, error) {
	if meta := t.getTypeMeta(name); meta != nil {
		if _, ok := meta.Methods["Unapply"]; ok {
			return nil, nil
		}
	}

	// Only generate Unapply if all fields are Public (exported)
	for _, field := range fields.List {
		for _, fieldName := range field.Names {
			if !ast.IsExported(fieldName.Name) {
				return nil, nil
			}
		}
	}

	retType := ast.Expr(ast.NewIdent(name))
	if tParams != nil {
		var indices []ast.Expr
		for _, p := range tParams.List {
			for _, n := range p.Names {
				indices = append(indices, ast.NewIdent(n.Name))
			}
		}
		if len(indices) == 1 {
			retType = &ast.IndexExpr{
				X:     ast.NewIdent(name),
				Index: indices[0],
			}
		} else if len(indices) > 1 {
			retType = &ast.IndexListExpr{
				X:       ast.NewIdent(name),
				Indices: indices,
			}
		}
	}

	var results []*ast.Field
	var retVals []ast.Expr
	var zeroVals []ast.Expr

	hasFields := false
	for _, field := range fields.List {
		for _, fieldName := range field.Names {
			hasFields = true
			results = append(results, &ast.Field{Type: field.Type})
			retVals = append(retVals, &ast.SelectorExpr{
				X:   ast.NewIdent("p"),
				Sel: ast.NewIdent(fieldName.Name),
			})
			zeroVals = append(zeroVals, zeroValueExpr(field.Type))
		}
	}
	results = append(results, &ast.Field{Type: ast.NewIdent("bool")})
	retVals = append(retVals, ast.NewIdent("true"))
	zeroVals = append(zeroVals, ast.NewIdent("false"))

	pName := "p"
	if !hasFields {
		pName = "_"
	}

	body := []ast.Stmt{
		&ast.IfStmt{
			Init: &ast.AssignStmt{
				Lhs: []ast.Expr{ast.NewIdent(pName), ast.NewIdent("ok")},
				Tok: token.DEFINE,
				Rhs: []ast.Expr{
					&ast.TypeAssertExpr{
						X:    ast.NewIdent("v"),
						Type: retType,
					},
				},
			},
			Cond: ast.NewIdent("ok"),
			Body: &ast.BlockStmt{
				List: []ast.Stmt{
					&ast.ReturnStmt{Results: retVals},
				},
			},
		},
		&ast.IfStmt{
			Init: &ast.AssignStmt{
				Lhs: []ast.Expr{ast.NewIdent(pName), ast.NewIdent("ok")},
				Tok: token.DEFINE,
				Rhs: []ast.Expr{
					&ast.TypeAssertExpr{
						X:    ast.NewIdent("v"),
						Type: &ast.StarExpr{X: retType},
					},
				},
			},
			Cond: &ast.BinaryExpr{
				X:  ast.NewIdent("ok"),
				Op: token.LAND,
				Y: &ast.BinaryExpr{
					X:  ast.NewIdent("p"), // still use p here for nil check if it exists
					Op: token.NEQ,
					Y:  ast.NewIdent("nil"),
				},
			},
			Body: &ast.BlockStmt{
				List: []ast.Stmt{
					&ast.ReturnStmt{Results: retVals},
				},
			},
		},
		&ast.ReturnStmt{Results: zeroVals},
	}

	// Update the nil check if p is _
	if !hasFields {
		ifStmt := body[1].(*ast.IfStmt)
		ifStmt.Cond = ast.NewIdent("ok") // No nil check needed if we don't use p
	}

	return &ast.FuncDecl{
		Recv: &ast.FieldList{
			List: []*ast.Field{
				{
					Names: []*ast.Ident{ast.NewIdent("s")},
					Type:  retType,
				},
			},
		},
		Name: ast.NewIdent("Unapply"),
		Type: &ast.FuncType{
			Params: &ast.FieldList{
				List: []*ast.Field{
					{
						Names: []*ast.Ident{ast.NewIdent("v")},
						Type:  ast.NewIdent("any"),
					},
				},
			},
			Results: &ast.FieldList{List: results},
		},
		Body: &ast.BlockStmt{List: body},
	}, nil
}

// generateInstanceMarker generates a marker interface and method for generic struct wildcard pattern matching.
// For a generic struct Wrap[T], it generates:
//   - Interface: type WrapInstance interface { IsWrap() bool }
//   - Method: func (w Wrap[T]) IsWrap() bool { return true }
func (t *galaASTTransformer) generateInstanceMarker(name string, tParams *ast.FieldList) (ast.Decl, ast.Decl) {
	interfaceName := name + "Instance"
	// Avoid collision: if a type with the same name as the generated interface
	// already exists (e.g., type Codec[T] generates "CodecInstance" which collides
	// with a user-defined type JsonEncoder), use an underscore prefix.
	if t.getTypeMeta(interfaceName) != nil {
		interfaceName = "_" + interfaceName
	}
	// Store the actual interface name so pattern matching can look it up
	if t.instanceInterfaceNames == nil {
		t.instanceInterfaceNames = make(map[string]string)
	}
	t.instanceInterfaceNames[name] = interfaceName
	methodName := "Is" + name

	// Generate interface: type WrapInstance interface { IsWrap() bool }
	interfaceDecl := &ast.GenDecl{
		Tok: token.TYPE,
		Specs: []ast.Spec{
			&ast.TypeSpec{
				Name: ast.NewIdent(interfaceName),
				Type: &ast.InterfaceType{
					Methods: &ast.FieldList{
						List: []*ast.Field{
							{
								Names: []*ast.Ident{ast.NewIdent(methodName)},
								Type: &ast.FuncType{
									Params:  &ast.FieldList{},
									Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("bool")}}},
								},
							},
						},
					},
				},
			},
		},
	}

	// Build receiver type with type params: Wrap[T] or Wrap[T, U]
	var recvType ast.Expr = ast.NewIdent(name)
	if tParams != nil && len(tParams.List) > 0 {
		var indices []ast.Expr
		for _, p := range tParams.List {
			for _, n := range p.Names {
				indices = append(indices, ast.NewIdent(n.Name))
			}
		}
		if len(indices) == 1 {
			recvType = &ast.IndexExpr{X: ast.NewIdent(name), Index: indices[0]}
		} else {
			recvType = &ast.IndexListExpr{X: ast.NewIdent(name), Indices: indices}
		}
	}

	// Generate method: func (w Wrap[T]) IsWrap() bool { return true }
	// Note: Methods get type params from receiver, not from method signature
	methodDecl := &ast.FuncDecl{
		Recv: &ast.FieldList{
			List: []*ast.Field{
				{
					Names: []*ast.Ident{ast.NewIdent("_")},
					Type:  recvType,
				},
			},
		},
		Name: ast.NewIdent(methodName),
		Type: &ast.FuncType{
			Params:  &ast.FieldList{},
			Results: &ast.FieldList{List: []*ast.Field{{Type: ast.NewIdent("bool")}}},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ReturnStmt{Results: []ast.Expr{ast.NewIdent("true")}},
			},
		},
	}

	return interfaceDecl, methodDecl
}
