package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// Go calls an `inout` function through a copy (docs/OWNERSHIP_DESIGN.MD,
// 3.5.1)
//
// An exported function or method with an `inout` parameter or receiver is
// a Go declaration too, with its Go signature: `func Step(p *Parser)`,
// `func (p *Parser) Advance() Token`. Go may pass it a pointer that it
// keeps, shares with another goroutine, or passes twice, so the GALA body,
// which relies on an exclusive place, is not what Go calls:
//
//   - the body is emitted as GALA_<Func>, or GALA_<Type>_<Method> with the
//     receiver as its first parameter (the reserved GALA_ prefix,
//     GALA-E0097), and every GALA call, in any package, calls it directly
//     (inoutBodyCall);
//   - the Go-visible declaration is a wrapper that copies each `inout`
//     argument into a local, calls the body on the copies, and copies them
//     back when the body returns. A panic skips the copy back, so a Go caller
//     that recovers sees its values unchanged.

// inoutSplits reports whether a function or method named name, whose
// receiver and parameters lend places of the types lent (the elements of
// their Ptr types), is split into a Go-visible wrapper and a GALA_ body:
// any one that lends a place, so that no Go code, in another package or
// hand-written beside it, reaches the body. A non-exported one lending a
// place of a type that must not be copied keeps one declaration instead:
// its wrapper would copy it, and only GALA calls it. (An exported one is
// GALA-E0111, see splitInoutBody.)
func (t *galaASTTransformer) inoutSplits(name string, lent []transpiler.Type) bool {
	if len(lent) == 0 {
		return false
	}
	if ast.IsExported(name) {
		return true
	}
	return !slices.ContainsFunc(lent, func(typ transpiler.Type) bool {
		_, why := t.noCopyWhy(typ)
		return why != ""
	})
}

// lentTypes is the types of the places a signature lends: the receiver's
// type when recvInout, then the pointee of each parameter types[i] that
// inout marks.
func lentTypes(recvInout bool, recvType transpiler.Type, types []transpiler.Type, inout []bool) []transpiler.Type {
	var lent []transpiler.Type
	if recvInout {
		lent = append(lent, derefType(recvType))
	}
	for i, typ := range types {
		if i < len(inout) && inout[i] {
			lent = append(lent, derefType(typ))
		}
	}
	return lent
}

// inoutBodyName is the name of the body of the Go-visible `inout` function
// name, or of method name of the type typeName ("" for a function).
func inoutBodyName(typeName, name string) string {
	if typeName == "" {
		return exportedNamePrefix + name
	}
	return exportedNamePrefix + typeName + "_" + name
}

// splitInoutBody turns fd, the lowering of d, a function or method that
// lends places, into its GALA_ body, and returns the Go-visible wrapper to
// emit before it (see the comment at the top of the file), or nil when d
// is not split (inoutSplits). recvType is the receiver's lowered type and
// typeName its type's bare name; checks are the nil checks of the exported
// declaration, which the wrapper runs (GALA callers never pass nil). A
// method lowered to a function (a generic one) is split as that function.
// An exported declaration lending a place of a type that must not be copied
// is GALA-E0111: Go would reach it through a copy.
func (t *galaASTTransformer) splitInoutBody(d funcDecl, fd *ast.FuncDecl, recvType ast.Expr, typeName string, checks []ast.Stmt) (*ast.FuncDecl, error) {
	if !(d.recv != nil && d.recv.isInout) && !slices.ContainsFunc(d.sig.params(), func(p paramDecl) bool { return p.isInout }) {
		return nil, nil
	}
	lowered := d.recv != nil && fd.Recv == nil
	var lends []bool // per Go parameter of the body, receiver first
	var at []srcNode // where each is declared
	var lent []transpiler.Type
	if d.recv != nil {
		lends = append(lends, d.recv.isInout)
		at = append(at, d.recv.at)
	}
	for _, p := range d.sig.params() {
		lends = append(lends, p.isInout)
		at = append(at, p.at)
	}
	goParams := fd.Type.Params.List
	if fd.Recv != nil {
		goParams = append([]*ast.Field{fd.Recv.List[0]}, goParams...)
	}
	for i, f := range goParams {
		if i < len(lends) && lends[i] {
			if star, ok := f.Type.(*ast.StarExpr); ok {
				lent = append(lent, t.astTypeToTranspilerType(star.X))
			}
		}
	}
	if !t.inoutSplits(d.name, lent) {
		return nil, nil
	}
	if ast.IsExported(d.name) {
		for i, f := range goParams {
			star, ok := f.Type.(*ast.StarExpr)
			if !ok || i >= len(lends) || !lends[i] {
				continue
			}
			if typeName, why := t.noCopyWhy(t.astTypeToTranspilerType(star.X)); why != "" {
				return nil, inoutError(at[i], fmt.Sprintf("exported %s cannot lend %s to Go code through a copy: %s", d.name, f.Names[0].Name, why),
					fmt.Sprintf("take a Ptr[%s] parameter instead, or keep %s unexported", typeName, d.name))
			}
		}
	}

	// The wrapper's parameters, as fields of its own: the body's are renamed
	// apart from them (renameShadowingLocals runs on each declaration).
	params := make([]*ast.Field, len(fd.Type.Params.List))
	for i, f := range fd.Type.Params.List {
		params[i] = &ast.Field{Names: []*ast.Ident{ast.NewIdent(f.Names[0].Name)}, Type: f.Type}
	}
	wrapper := &ast.FuncDecl{Recv: fd.Recv, Name: ast.NewIdent(fd.Name.Name), Type: &ast.FuncType{Params: &ast.FieldList{List: params}, Results: fd.Type.Results, TypeParams: fd.Type.TypeParams}}
	all := params // the wrapper's arguments to the body, receiver first
	switch {
	case fd.Recv != nil:
		recv := fd.Recv.List[0]
		wrapper.Recv = &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent(recv.Names[0].Name)}, Type: recv.Type}}}
		all = append([]*ast.Field{wrapper.Recv.List[0]}, params...)
		fd.Name = ast.NewIdent(inoutBodyName(typeName, d.name))
		fd.Recv = nil
		fd.Type.Params.List = append([]*ast.Field{recv}, fd.Type.Params.List...)
		if tps := t.extractTypeParams(recvType); len(tps) > 0 {
			fd.Type.TypeParams = &ast.FieldList{List: tps}
		}
	case lowered:
		// Already a function taking its receiver first: Box_Map is the
		// wrapper, GALA_Box_Map the body.
		fd.Name = ast.NewIdent(exportedNamePrefix + fd.Name.Name)
	default:
		fd.Name = ast.NewIdent(inoutBodyName("", d.name))
	}
	if other := t.inoutBodyClash(fd.Name.Name, d.name, typeName); other != "" {
		return nil, inoutError(at[0], fmt.Sprintf("the generated body %s of %s is also the body of %s", fd.Name.Name, d.name, other),
			"rename one of them: GALA_<Type>_<Method> and GALA_<Func> must differ")
	}
	var typeArgs []ast.Expr
	if fd.Type.TypeParams != nil {
		for _, tp := range fd.Type.TypeParams.List {
			for _, n := range tp.Names {
				typeArgs = append(typeArgs, ast.NewIdent(n.Name))
			}
		}
	}

	stmts := checks
	var copyOut []ast.Stmt
	var args []ast.Expr
	variadic := false
	for i, p := range all {
		name := p.Names[0].Name
		if name == "_" {
			name = fmt.Sprintf("%sarg%d", localNamePrefix, i)
			p.Names[0] = ast.NewIdent(name)
		}
		if _, isEllipsis := p.Type.(*ast.Ellipsis); isEllipsis {
			variadic = true
		}
		if !lends[i] {
			args = append(args, p.Names[0])
			continue
		}
		local := localNamePrefix + "in_" + name
		stmts = append(stmts, &ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent(local)}, Tok: token.DEFINE, Rhs: []ast.Expr{&ast.StarExpr{X: p.Names[0]}}})
		args = append(args, &ast.UnaryExpr{Op: token.AND, X: ast.NewIdent(local)})
		copyOut = append(copyOut, &ast.AssignStmt{Lhs: []ast.Expr{&ast.StarExpr{X: p.Names[0]}}, Tok: token.ASSIGN, Rhs: []ast.Expr{ast.NewIdent(local)}})
	}
	call := &ast.CallExpr{Fun: withTypeArgs(ast.NewIdent(fd.Name.Name), typeArgs), Args: args, Ellipsis: ellipsisPos(variadic)}
	var results []ast.Expr
	if fd.Type.Results != nil {
		for i := range fd.Type.Results.NumFields() {
			results = append(results, ast.NewIdent(fmt.Sprintf("%sr%d", localNamePrefix, i)))
		}
	}
	if len(results) == 0 {
		stmts = append(stmts, &ast.ExprStmt{X: call})
	} else {
		stmts = append(stmts, &ast.AssignStmt{Lhs: results, Tok: token.DEFINE, Rhs: []ast.Expr{call}})
	}
	stmts = append(stmts, copyOut...)
	if len(results) > 0 {
		stmts = append(stmts, &ast.ReturnStmt{Results: results})
	}
	wrapper.Body = &ast.BlockStmt{List: stmts}

	callee := d.name
	if typeName != "" {
		callee = typeName + "." + d.name
	}
	fd.Doc = docCommentGroup(fmt.Sprintf("%s is generated: it is the GALA body of %s, which GALA code calls directly.\nGo code calls %s, which lends it a copy of each inout argument.", fd.Name.Name, callee, d.name))
	return wrapper, nil
}

// inoutBodyCall rewrites call, a call of callee from GALA, to call the body
// of a split `inout` function or method directly (splitInoutBody): `F(&c)`
// becomes `GALA_F(&c)`, and `x.M(a)` becomes `GALA_T_M(&x, a)` for an
// `inout` receiver, or `GALA_T_M(x, a)` for a value receiver. Any other
// call keeps calling the wrapper, which is correct and costs two copies:
// a method lowered to a function, a method of an interface, a Ptr-receiver
// method, and a callee that declares Go results (whose lifting to Try/Tuple
// reads the wrapper's name).
//
// The body has no metadata of its own, so the call's type is inferred, and
// cached on the call, before it is renamed.
func (t *galaASTTransformer) inoutBodyCall(call *ast.CallExpr, callee inoutCallee) {
	m, recvType := callee.method, callee.recvType
	if !callee.declared || callee.shift != 0 || !callee.lends() {
		return
	}
	if m == nil {
		fm := callee.fm
		if len(fm.GoResults) > 0 || !t.inoutSplits(fm.Name, lentTypes(false, nil, fm.ParamTypes, fm.InoutParams)) {
			return
		}
		t.getExprTypeName(call)
		renameCallee(call, inoutBodyName("", callee.name))
		return
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	// An interface's method spec (OfTypeDecl) dispatches through Go.
	if !ok || len(m.GoResults) > 0 || m.GoDeclared || m.OfTypeDecl || m.IsGeneric || len(m.TypeParams) > 0 ||
		m.PointerReceiver && !m.InoutReceiver || !t.inoutSplits(m.Name, lentTypes(m.InoutReceiver, recvType, m.ParamTypes, m.InoutParams)) {
		return
	}
	base := receiverBase(derefType(recvType))
	named, ok := base.(transpiler.NamedType)
	if !ok {
		// A receiver type known by name only: a type of the method's package.
		name := base.BaseName()
		named = transpiler.NamedType{Package: m.Package, Name: name[strings.LastIndex(name, ".")+1:]}
	}
	t.getExprTypeName(call)
	recv := t.derefPointer(sel.X, recvType) // a value receiver takes the value
	if m.InoutReceiver {
		recv = t.receiverPointer(sel.X, recvType)
	}
	body := named
	body.Name = inoutBodyName(named.Name, callee.name)
	call.Args = append([]ast.Expr{recv}, call.Args...)
	call.Fun = t.resolveTypeQualifier(body)
}

// derefType is the type typ points to, or typ.
func derefType(typ transpiler.Type) transpiler.Type {
	if ptr, isPtr := typ.(transpiler.PointerType); isPtr {
		return ptr.Elem
	}
	return typ
}

// checkInoutInterfaceSlot rejects a value of the type valTypeOf returns
// filling slotType, an interface that declares `inout` methods, when its
// type does not implement each with an `inout` receiver (GALA-E0116,
// docs/OWNERSHIP_DESIGN.MD 3.5.3): a GALA method with another receiver, or a
// Go method, unless it is on the no-retain allow-list. An interface value
// that is already an interface, built in GALA or arriving from Go, is
// trusted (decision Q16).
func (t *galaASTTransformer) checkInoutInterfaceSlot(valTypeOf func() transpiler.Type, slotType transpiler.Type, at srcNode) error {
	valType, why, hint := t.inoutImplMismatch(valTypeOf, slotType)
	if why == "" {
		return nil
	}
	return inoutCodedError(galaerr.CodeInoutForeignImpl, at,
		fmt.Sprintf("%s cannot fill %s: %s", displayType(valType), displayType(slotType), why), hint)
}

// inoutTypeArgMisuse returns the GALA-E0116 message and hint for typeArg as
// the type argument of o's type parameter tp, when tp's constraint is an
// interface that declares `inout` methods typeArg does not implement with
// `inout` receivers: the rule of checkInoutInterfaceSlot, which Go's
// constraint check cannot tell from a Ptr receiver's.
func (t *galaASTTransformer) inoutTypeArgMisuse(o typeParamOwner, tp string, typeArg transpiler.Type) (msg, hint string, bad bool) {
	if !t.inoutConstraints {
		return "", "", false
	}
	constraint := o.constraints[tp]
	if !transpiler.IsInterfaceConstraint(constraint) {
		return "", "", false
	}
	constraint = t.followAliasChain(constraint)
	_, why, hint := t.inoutImplMismatch(func() transpiler.Type { return typeArg }, constraint)
	if why == "" {
		return "", "", false
	}
	return fmt.Sprintf("%s, the type argument for %s of %s, does not satisfy its constraint %s: %s",
		displayType(typeArg), tp, o.name, displayType(constraint), why), hint, true
}

// hasInoutConstraint reports whether a type parameter of o is constrained
// by an interface that declares an `inout` method.
func (t *galaASTTransformer) hasInoutConstraint(o typeParamOwner) bool {
	if !t.inoutConstraints {
		return false
	}
	for _, tp := range o.params {
		if c := o.constraints[tp]; transpiler.IsInterfaceConstraint(c) {
			if meta := t.getTypeMeta(t.followAliasChain(c).BaseName()); meta != nil && declaresInout(meta) {
				return true
			}
		}
	}
	return false
}

// declaresInoutMethod reports whether an interface of types declares an
// `inout` method.
func declaresInoutMethod(types map[string]*transpiler.TypeMetadata) bool {
	for _, meta := range types {
		if meta != nil && meta.IsInterface && declaresInout(meta) {
			return true
		}
	}
	return false
}

// declaresInout reports whether meta has a method with an `inout` receiver.
func declaresInout(meta *transpiler.TypeMetadata) bool {
	for _, m := range meta.Methods {
		if m != nil && m.InoutReceiver {
			return true
		}
	}
	return false
}

// inoutImplMismatch says why a value of the type valTypeOf returns does not
// implement the `inout` methods slotType declares with `inout` receivers,
// and how to fix it; why is "" when it does, when slotType declares none, or
// when that cannot be told. valType is the value's type as the message
// names it: a pointer's pointee. A type parameter is judged by its
// constraint, an interface whose values may be of any type implementing it.
func (t *galaASTTransformer) inoutImplMismatch(valTypeOf func() transpiler.Type, slotType transpiler.Type) (valType transpiler.Type, why, hint string) {
	iface := t.getTypeMeta(slotType.BaseName())
	if iface == nil || !iface.IsInterface {
		return nil, "", ""
	}
	var inout []string
	for name, m := range iface.Methods {
		if m.InoutReceiver {
			inout = append(inout, name)
		}
	}
	if len(inout) == 0 {
		return nil, "", ""
	}
	slices.Sort(inout)
	valType = t.followAliasChain(valTypeOf())
	if ptr, isPtr := valType.(transpiler.PointerType); isPtr {
		valType = t.followAliasChain(ptr.Elem)
	}
	impl := valType
	c, isTypeParam := t.typeParamConstraint(valType)
	if isTypeParam {
		impl = t.followAliasChain(c)
	}
	base := impl
	if g, isGeneric := impl.(transpiler.GenericType); isGeneric {
		base = g.Base
	}
	if transpiler.IsUnusable(base) {
		return nil, "", ""
	}
	base, meta, goData, ok := t.receiverTypeInfo(base)
	if !ok || meta == nil && goData == nil {
		return nil, "", "" // a type nothing here describes
	}
	named, isNamed := base.(transpiler.NamedType)
	goType := isNamed && t.isGoTyped(named) || meta == nil && goData != nil
	for _, name := range inout {
		var m *transpiler.MethodMetadata
		if meta != nil {
			m = meta.Methods[name]
		}
		switch {
		case !isTypeParam && meta != nil && meta.IsInterface && goDeclared(meta):
			// An interface value of a Go interface: trusted (Q16). Not a
			// type parameter's value, which GALA instantiates.
			return nil, "", ""
		case meta != nil && meta.IsInterface && m != nil && m.InoutReceiver:
			continue
		case goType && goData != nil && !goData.PointerMethods[name] && goData.Methods[name] == nil:
			return nil, "", "" // no such method: GALA-E0100 reports it
		case goType && t.isNoRetainGoMethod(base, name):
			continue
		case goType || m != nil && m.GoDeclared:
			return valType, fmt.Sprintf("its method %s is Go code, which may keep or share the receiver %s lends", name, displayType(slotType)),
				fmt.Sprintf("wrap it in a GALA type whose %s has an inout receiver", name)
		case m == nil || !m.InoutReceiver:
			// A GALA type, or a GALA interface: its method must be declared
			// inout (a method it gets from an embedded or hand-written Go type
			// is not).
			hint := fmt.Sprintf("declare it `func (inout r %s) %s(...)`", displayType(impl), name)
			if meta != nil && meta.IsInterface {
				hint = fmt.Sprintf("declare %s's method `inout %s(...)`", displayType(impl), name)
			}
			return valType, fmt.Sprintf("%s declares %s inout, and %s.%s has no inout receiver", displayType(slotType), name, displayType(impl), name), hint
		}
	}
	return nil, "", ""
}

// renameCallee renames the function call calls, `F`, `pkg.F` or `F[T]`, to
// name.
func renameCallee(call *ast.CallExpr, name string) {
	fun := call.Fun
	switch f := fun.(type) {
	case *ast.IndexExpr:
		fun = f.X
	case *ast.IndexListExpr:
		fun = f.X
	}
	switch f := fun.(type) {
	case *ast.Ident:
		f.Name = name
	case *ast.SelectorExpr:
		f.Sel = ast.NewIdent(name)
	}
}

// receiverPointer is the pointer to the receiver recv of type recvType that
// a method body lowered to a function takes: recv itself when it is a
// pointer, the pointer x of an `inout` place `(*x)`, and `&recv` otherwise
// (an addressable place, as checkInoutReceiver ensures).
func (t *galaASTTransformer) receiverPointer(recv ast.Expr, recvType transpiler.Type) ast.Expr {
	if _, isPtr := recvType.(transpiler.PointerType); isPtr {
		return recv
	}
	if ptr, ok := t.inoutAddr(recv); ok {
		// The inout place lent on to the body: an approved lend, which a
		// by-name thunk around the call still sees (checkInoutThunk).
		t.markOperand(ptr, operandInoutLend)
		if t.inoutApproved == nil {
			t.inoutApproved = make(map[ast.Node]bool)
		}
		t.inoutApproved[ptr] = true
		t.inoutLends = true
		return ptr
	}
	return &ast.UnaryExpr{Op: token.AND, X: recv}
}

// inoutBodyClash names another declaration of the package whose GALA_ body
// would also be named body (`GALA_A_B_C` for method B_C of A and for method
// C of A_B), or returns "". name and typeName are the declaration's own.
func (t *galaASTTransformer) inoutBodyClash(body, name, typeName string) string {
	if t.richAST == nil {
		return ""
	}
	for _, f := range t.richAST.Functions {
		if f.Package == t.packageName && f.Name != name && slices.Contains(f.InoutParams, true) && inoutBodyName("", f.Name) == body {
			return f.Name
		}
	}
	for _, typ := range t.richAST.Types {
		if typ.Package != t.packageName {
			continue
		}
		for _, m := range typ.Methods {
			lends := m.InoutReceiver || slices.Contains(m.InoutParams, true)
			if lends && !(typ.Name == typeName && m.Name == name) && inoutBodyName(typ.Name, m.Name) == body {
				return typ.Name + "." + m.Name
			}
		}
	}
	return ""
}
