package transformer

import (
	"go/ast"
	"go/token"
	"slices"

	"martianoff/gala/internal/transpiler"
)

// Evaluation order
//
// GALA evaluates the operands of an expression strictly left to right: the
// two sides of a binary operator, a call's receiver and then its arguments,
// the parts of an interpolated string, the elements of a tuple, collection or
// struct literal. Go orders calls, method calls and receive operations left to
// right, but leaves a plain variable read unordered against them, and the gc
// compiler performs the calls first: `counter + bump()` reads counter after
// bump() has changed it.
//
// orderOperands restores the GALA order on the finished file. Where an
// operand reads a variable that a call could change, and a later operand of
// the same expression makes a call, the read is wrapped in std.EvalOperand: a
// call, so Go evaluates it, and with it the read, before the calls to its
// right. Every other operand list is left exactly as it was lowered.
//
// A variable a call can change is a package-level `var`, or a local `var` (or
// `var` parameter) that a function literal of the same declaration mentions
// or whose address it takes. A local no closure captures can only change by
// assignment in its own function, never during a call, so reading it late is
// the same as reading it early. Names are compared, not bindings: a val that
// shares a captured var's name is read through EvalOperand too, which costs a
// call Go inlines and changes no result.

// recordMutableVar records the local `var` or `var` parameter name, of type
// typ, for orderOperands. Two bindings of the name with different types leave
// its type unknown.
func (t *galaASTTransformer) recordMutableVar(name string, typ transpiler.Type) {
	if t.mutableVars == nil {
		t.mutableVars = make(map[string]transpiler.Type)
	}
	if prev, seen := t.mutableVars[name]; seen && (prev == nil || typ == nil || prev.String() != typ.String()) {
		typ = nil
	}
	t.mutableVars[name] = typ
}

// operandOrder is orderOperands' view of one top-level declaration.
type operandOrder struct {
	t *galaASTTransformer
	// pkgVars is the package-level vars a bare name may read: this package's
	// and those of the packages the file dot-imports, with their types.
	pkgVars map[string]transpiler.Type
	// captured and locals are what capturedNames finds in the declaration.
	captured, locals map[string]bool
}

// orderOperands wraps in std.EvalOperand each operand of file that reads a
// variable a later operand's call could change (see above).
func (t *galaASTTransformer) orderOperands(file *ast.File) {
	pkgVars := t.packageVarTypes()
	if len(pkgVars) == 0 && len(t.mutableVars) == 0 {
		return
	}
	for _, decl := range file.Decls {
		o := &operandOrder{t: t, pkgVars: pkgVars}
		o.captured, o.locals = capturedNames(decl)
		ast.Inspect(decl, o.visit)
	}
}

// packageVarTypes returns the package-level vars (not vals) a bare name of the
// file may read, with their types: the package's own and those of the GALA
// packages the file dot-imports.
func (t *galaASTTransformer) packageVarTypes() map[string]transpiler.Type {
	vars := map[string]transpiler.Type{}
	if t.richAST == nil {
		return vars
	}
	add := func(metas map[string]*transpiler.PackageValMetadata) {
		for name, meta := range metas {
			if meta != nil && !meta.IsVal {
				vars[name] = meta.Type
			}
		}
	}
	add(t.richAST.PackageVals)
	if t.importManager != nil {
		for _, entry := range t.importManager.All() {
			if entry.IsDot {
				add(t.richAST.ImportedVals[entry.Path])
			}
		}
	}
	return vars
}

// capturedNames returns the names decl's function literals mention, the
// names whose address it takes, explicitly or as the receiver of a method
// call (a pointer-receiver method takes it implicitly), and the names it
// declares locally.
func capturedNames(decl ast.Decl) (captured, locals map[string]bool) {
	captured, locals = map[string]bool{}, map[string]bool{}
	mentions := func(n ast.Node) {
		ast.Inspect(n, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				captured[id.Name] = true
			}
			return true
		})
	}
	declare := func(ids ...*ast.Ident) {
		for _, id := range ids {
			locals[id.Name] = true
		}
	}
	fd, isFunc := decl.(*ast.FuncDecl)
	ast.Inspect(decl, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			mentions(n.Body)
		case *ast.UnaryExpr:
			if n.Op == token.AND {
				mentions(n.X)
			}
		case *ast.CallExpr:
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok {
					captured[id.Name] = true
				}
			}
		case *ast.Field:
			// A parameter, result or receiver of the function or of one of
			// its literals; struct fields only appear in types, never read.
			if isFunc {
				declare(n.Names...)
			}
		case *ast.AssignStmt:
			if n.Tok == token.DEFINE {
				for _, lhs := range n.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						declare(id)
					}
				}
			}
		case *ast.ValueSpec:
			if isFunc && fd.Body != nil {
				declare(n.Names...)
			}
		}
		return true
	})
	return captured, locals
}

// visit orders the operands of n, an expression of the declaration.
func (o *operandOrder) visit(n ast.Node) bool {
	switch n := n.(type) {
	case *ast.BinaryExpr:
		// && and || evaluate their left operand first already.
		if n.Op != token.LAND && n.Op != token.LOR {
			o.sequence([]*ast.Expr{&n.X, &n.Y}, nil)
		}
	case *ast.CallExpr:
		slots := make([]*ast.Expr, 0, len(n.Args)+1)
		var receiver *ast.Expr
		switch fun := ast.Unparen(n.Fun).(type) {
		case *ast.SelectorExpr:
			if !o.isPackageQualifier(fun.X) {
				receiver = &fun.X
				slots = append(slots, receiver)
			}
		case *ast.Ident:
			slots = append(slots, &n.Fun)
		}
		for i := range n.Args {
			slots = append(slots, &n.Args[i])
		}
		o.sequence(slots, func(slot *ast.Expr) bool {
			return slot != receiver || o.receiverCopyable(n.Fun)
		})
	case *ast.CompositeLit:
		_, isMap := n.Type.(*ast.MapType)
		var slots []*ast.Expr
		for i, elt := range n.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			switch {
			case !ok:
				slots = append(slots, &n.Elts[i])
			case isMap:
				slots = append(slots, &kv.Key, &kv.Value)
			default:
				slots = append(slots, &kv.Value)
			}
		}
		o.sequence(slots, nil)
	}
	return true
}

// sequence wraps in std.EvalOperand each operand of slots, in evaluation order,
// that reads a changeable variable outside any call while a later operand
// makes a call. copyable, when set, vetoes the wrap for a slot.
func (o *operandOrder) sequence(slots []*ast.Expr, copyable func(*ast.Expr) bool) {
	if len(slots) < 2 {
		return
	}
	// laterCall[i]: an operand after i makes a call.
	laterCall := make([]bool, len(slots))
	for i := len(slots) - 2; i >= 0; i-- {
		laterCall[i] = laterCall[i+1] || o.makesCall(*slots[i+1])
	}
	for i, slot := range slots {
		if !laterCall[i] || !o.readsChangeable(*slot) || (copyable != nil && !copyable(slot)) {
			continue
		}
		*slot = &ast.CallExpr{Fun: o.t.stdIdent("EvalOperand"), Args: []ast.Expr{*slot}}
	}
}

// readsChangeable reports whether evaluating e reads a variable that a call
// could change, other than inside a call of its own (Go evaluates that call,
// and its operands, in order with the others) or a function literal (not
// evaluated here).
func (o *operandOrder) readsChangeable(e ast.Expr) bool {
	found := false
	var walk func(ast.Node) bool
	walk = func(n ast.Node) bool {
		if found {
			return false
		}
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.CallExpr:
			if o.t.isTransparentCall(n, o.changeable) {
				for _, arg := range n.Args {
					ast.Inspect(arg, walk)
				}
			}
			return false
		case *ast.UnaryExpr:
			// An address does not change; a receive is ordered like a call.
			return n.Op != token.AND && n.Op != token.ARROW
		case *ast.SelectorExpr:
			if id, ok := n.X.(*ast.Ident); ok && o.isPackageQualifier(id) {
				found = o.isImportedVar(id.Name, n.Sel.Name)
				return false
			}
			ast.Inspect(n.X, walk)
			return false
		case *ast.CompositeLit:
			_, isMap := n.Type.(*ast.MapType)
			for _, elt := range n.Elts {
				if kv, ok := elt.(*ast.KeyValueExpr); ok && !isMap {
					elt = kv.Value
				}
				ast.Inspect(elt, walk)
			}
			return false
		case *ast.Ident:
			found = o.changeable(n.Name)
		}
		return true
	}
	ast.Inspect(e, walk)
	return found
}

// makesCall reports whether evaluating e makes a call that could change a
// variable (see makesCallExcept); a package-level val's `.Get()` cannot.
func (o *operandOrder) makesCall(e ast.Expr) bool {
	return o.t.makesCallExcept(e, o.changeable, o.isPackageValGet)
}

// makesCallExcept reports whether evaluating e calls a function, or receives
// from a channel, that could change a variable: any call but a conversion or
// a builtin that only reads (isTransparentCall, with changeable the names that
// are variables), the std helpers the transformer wraps values in, and the
// calls pure accepts. A function literal not called is not evaluated.
func (t *galaASTTransformer) makesCallExcept(e ast.Expr, changeable func(string) bool, pure func(*ast.CallExpr) bool) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if found {
			return false
		}
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.CallExpr:
			if !t.isTransparentCall(n, changeable) && !t.isStdHelperCall(n, "EvalOperand", "NewImmutable", "Copy") && !pure(n) {
				found = true
			}
		case *ast.UnaryExpr:
			found = n.Op == token.ARROW
		}
		return !found
	})
	return found
}

// changeable reports whether name, read in this declaration, may be changed by
// a call: a package-level var the declaration does not shadow, or a local var
// a function literal or a method call captures.
func (o *operandOrder) changeable(name string) bool {
	if _, ok := o.pkgVars[name]; ok && !o.locals[name] {
		return true
	}
	_, local := o.t.mutableVars[name]
	return local && o.locals[name] && o.captured[name]
}

// varType is the type of the changeable variable name, nil when unknown.
func (o *operandOrder) varType(name string) transpiler.Type {
	if o.locals[name] {
		return o.t.mutableVars[name]
	}
	return o.pkgVars[name]
}

// isPackageValGet reports whether call reads a package-level val of this
// package, `name.Get()`: its box never changes.
func (o *operandOrder) isPackageValGet(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != transpiler.MethodGet || len(call.Args) != 0 || o.t.richAST == nil {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	if _, local := o.t.mutableVars[id.Name]; local {
		return false
	}
	meta := o.t.richAST.PackageVals[id.Name]
	return meta != nil && meta.IsVal
}

// isPackageQualifier reports whether x names an imported package.
func (o *operandOrder) isPackageQualifier(x ast.Expr) bool {
	id, ok := x.(*ast.Ident)
	return ok && !o.changeable(id.Name) && o.t.isPackageQualifier(id.Name)
}

// isImportedVar reports whether pkg.name is a package-level var of the GALA
// package the file imports as pkg.
func (o *operandOrder) isImportedVar(pkg, name string) bool {
	pv := o.t.importedPackageVal(pkg, name)
	return pv != nil && !pv.IsVal
}

// receiverCopyable reports whether the receiver of the method call fun can be
// wrapped in std.EvalOperand: it is a variable whose type is known, and the
// method does not need its address (a pointer-receiver method called on an
// addressable variable sees the variable itself, which no earlier read can
// copy).
func (o *operandOrder) receiverCopyable(fun ast.Expr) bool {
	sel, ok := ast.Unparen(fun).(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return o.t.copyableReceiver(o.varType(id.Name), sel.Sel.Name)
}

// copyableReceiver reports whether a receiver of type typ can be copied ahead
// of a call of its method: typ is known, and method does not take the
// receiver's address.
func (t *galaASTTransformer) copyableReceiver(typ transpiler.Type, method string) bool {
	if transpiler.IsUnusable(typ) {
		return false
	}
	_, meta, goData, named := t.receiverTypeInfo(typ)
	if !named {
		return true // a pointer, function or interface value: copying it is reading it
	}
	if meta == nil && goData == nil {
		return false
	}
	return !t.hasPointerReceiverMethod(typ, method)
}

// isTransparentCall reports whether call is a conversion or a builtin that
// only reads its operands: Go does not order it among the calls, so the
// operands are evaluated as if it were not there.
func (t *galaASTTransformer) isTransparentCall(call *ast.CallExpr, changeable func(string) bool) bool {
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.ArrayType, *ast.MapType, *ast.ChanType, *ast.FuncType, *ast.InterfaceType, *ast.StructType, *ast.StarExpr:
		return true
	case *ast.Ident:
		return (transparentBuiltins[fun.Name] || t.resolveTypeMetaName(fun.Name) != "") && !changeable(fun.Name)
	case *ast.SelectorExpr, *ast.IndexExpr, *ast.IndexListExpr:
		return t.namesType(fun)
	}
	return false
}

// namesType reports whether fun, a callee spelled `T[...]`, `pkg.T` or
// `pkg.T[...]`, names a GALA or Go type: a call of it is a conversion.
func (t *galaASTTransformer) namesType(fun ast.Expr) bool {
	base, _ := splitCallFunTypeArgs(fun)
	switch b := base.(type) {
	case *ast.Ident:
		return t.resolveTypeMetaName(b.Name) != ""
	case *ast.SelectorExpr:
		pkg, ok := b.X.(*ast.Ident)
		if !ok || !t.isPackageQualifier(pkg.Name) {
			return false
		}
		qualified := pkg.Name + "." + b.Sel.Name
		return t.resolveTypeMetaName(qualified) != "" || (t.goTypeInfo != nil && t.goTypeInfo.GetTypeData(t.goTypeKey(qualified)) != nil)
	}
	return false
}

// isStdHelperCall reports whether call calls one of the std functions names.
func (t *galaASTTransformer) isStdHelperCall(call *ast.CallExpr, names ...string) bool {
	fun, _ := splitCallFunTypeArgs(call.Fun)
	return slices.ContainsFunc(names, func(name string) bool { return t.isStdHelper(fun, name) })
}

// transparentBuiltins are Go's predeclared types, and the builtins that only
// read their operands.
var transparentBuiltins = map[string]bool{
	"bool": true, "byte": true, "rune": true, "string": true, "error": true, "any": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true, "uintptr": true,
	"float32": true, "float64": true, "complex64": true, "complex128": true,
	"len": true, "cap": true, "min": true, "max": true, "real": true, "imag": true, "complex": true,
}
