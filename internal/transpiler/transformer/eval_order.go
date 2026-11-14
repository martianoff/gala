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
// Storage a call can change is a package-level `var`; a local `var` (or `var`
// parameter) that a function literal of the same declaration mentions, whose
// address it takes, or that is the receiver of a method call (a
// pointer-receiver method takes its address, also of the variable a field
// receiver is stored in); and whatever is reached through a pointer: `*p`,
// and `p.f` where p is a pointer (operandMark). A local no closure or pointer
// reaches can only change by assignment in its own function, never during a
// call, so reading it late is the same as reading it early. Names are
// compared, not bindings: a val that shares a captured var's name is read
// through EvalOperand too, which costs a call Go inlines and changes no
// result. The names are the Go names, after renameShadowingLocals gave a
// local named like a Go builtin its own.

// operandMark says how a node lowered from the source reaches storage: a set
// of the flags below. The lowering marks nodes where the operand's type is
// known (markMember, markDeref); orderOperands reads the marks on the
// finished file. A method call is marked on its receiver node, not on its
// selector, because the method-call lowering builds its own selector around
// the receiver.
type operandMark int

const (
	// operandThroughPointer is `*p`, a field or method value `p.f` with p a
	// pointer, or an element `x[i]` of a slice or map x: storage any call
	// may reach.
	operandThroughPointer operandMark = 1 << iota
	// operandAddressed is the receiver x of a method call `x.m(...)` that
	// takes x's address: a pointer-receiver method on a value.
	operandAddressed
	// operandPointerReceiver is the receiver p of a call `p.m(...)` that Go
	// reads *p for when it makes the call: a value-receiver method, or a
	// func field, called through the pointer p.
	operandPointerReceiver
	// operandBoxGet is `x.f.Get()`, a val field read out of its
	// std.Immutable box: a call that changes nothing.
	operandBoxGet
	// operandPackageVar is a name that reads a package-level var, as the
	// lowering resolved it: a local binding of the same name elsewhere in
	// the declaration does not hide it (see changeable).
	operandPackageVar
)

func (t *galaASTTransformer) markOperand(n ast.Node, mark operandMark) {
	if t.operandMarks == nil {
		t.operandMarks = make(map[ast.Node]operandMark)
	}
	t.operandMarks[n] |= mark
}

// marked reports whether n carries mark.
func (t *galaASTTransformer) marked(n ast.Node, mark operandMark) bool {
	return t.operandMarks[n]&mark != 0
}

// readsThroughPointer reports whether a node of the file reads through a
// pointer (operandThroughPointer, operandPointerReceiver).
func (t *galaASTTransformer) readsThroughPointer() bool {
	for _, mark := range t.operandMarks {
		if mark&(operandThroughPointer|operandPointerReceiver) != 0 {
			return true
		}
	}
	return false
}

// markMember marks sel, the member access `base.name` lowered from the
// source, for orderOperands. valueType is the type of base, or of what it
// points to, throughPointer whether base is a pointer, and called whether sel
// is called.
func (t *galaASTTransformer) markMember(sel *ast.SelectorExpr, valueType transpiler.Type, throughPointer, called bool) {
	pointerMethod := called && t.hasPointerReceiverMethod(valueType, sel.Sel.Name)
	switch {
	case !called && throughPointer:
		t.markOperand(sel, operandThroughPointer)
	case pointerMethod && !throughPointer:
		t.markOperand(sel.X, operandAddressed)
	case called && !pointerMethod && throughPointer:
		t.markOperand(sel.X, operandPointerReceiver)
	}
	// A pointer-receiver method called through p gets p itself, not *p.
}

// markDeref marks star, a pointer dereference, for orderOperands.
func (t *galaASTTransformer) markDeref(star *ast.StarExpr) *ast.StarExpr {
	t.markOperand(star, operandThroughPointer)
	return star
}

// isPackageVar reports whether name is a package-level var a bare name may
// read, as packageVarTypes lists them: this package's, or one of a package
// the file dot-imports. (An embed val is bound like a var, but is none.)
func (t *galaASTTransformer) isPackageVar(name string) bool {
	if t.richAST == nil {
		return false
	}
	isVar := func(meta *transpiler.PackageValMetadata) bool { return meta != nil && !meta.IsVal }
	if isVar(t.richAST.PackageVals[name]) {
		return true
	}
	if t.importManager != nil {
		for _, entry := range t.importManager.All() {
			if entry.IsDot && isVar(t.richAST.ImportedVals[entry.Path][name]) {
				return true
			}
		}
	}
	return false
}

// markIndex marks idx, an element read `x[i]` lowered from the source, for
// orderOperands when x is a slice, a map or a pointer to an array: its
// elements are shared storage any call may change, as through a pointer. A
// string or an array value holds its own elements.
//
// written is the base as written, before its Immutable box, if any, was
// unwrapped: the node (inside any parentheses) the lowering already asked
// the type of, so asking again reports nothing new. The type is read through
// the box and a Go named collection type (url.Values, sort.IntSlice), as for
// the .Size() sugar.
func (t *galaASTTransformer) markIndex(idx *ast.IndexExpr, written ast.Expr) {
	switch typ := t.followAliasChain(t.sizeSugarReceiverType(ast.Unparen(written))).(type) {
	case transpiler.ArrayType:
		if typ.Fixed {
			return
		}
	case transpiler.MapType:
	case transpiler.PointerType:
		if _, toArray := typ.Elem.(transpiler.ArrayType); !toArray {
			return
		}
	default:
		return
	}
	t.markOperand(idx, operandThroughPointer)
}

// recordMutableVar records the local `var` or `var` parameter name, of type
// typ, for orderOperands. Two bindings of the name with different types leave
// its type unknown.
func (t *galaASTTransformer) recordMutableVar(name string, typ transpiler.Type) {
	if t.mutableVars == nil {
		t.mutableVars = make(map[string]transpiler.Type)
	}
	name = localGoName(name)
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
	// decl is the declaration; bound is every name it binds below its top
	// level (see declBoundNames), computed when first needed.
	decl  ast.Decl
	bound map[string]bool
}

// declBoundNames returns every name decl binds for itself, at any depth: the
// parameters, results and locals of a function and of its literals, range
// variables, and the same inside a package-level initializer, but not the
// names a GenDecl declares at the top level. A name bound anywhere counts, so
// it errs toward not taking a read of the name for a package-level binding.
func declBoundNames(decl ast.Decl) map[string]bool {
	bound := map[string]bool{}
	add := func(names map[string]bool) {
		for name := range names {
			bound[name] = true
		}
	}
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d.Recv != nil {
			for _, f := range d.Recv.List {
				for _, name := range f.Names {
					bound[name.Name] = true
				}
			}
		}
		if d.Body != nil {
			add(boundNames(&ast.FuncLit{Type: d.Type, Body: d.Body}))
		}
	case *ast.GenDecl:
		for _, spec := range d.Specs {
			if vs, ok := spec.(*ast.ValueSpec); ok {
				for _, v := range vs.Values {
					add(boundNames(v))
				}
			}
		}
	}
	return bound
}

// orderOperands wraps in std.EvalOperand each operand of file that reads a
// variable a later operand's call could change (see above).
func (t *galaASTTransformer) orderOperands(file *ast.File) {
	pkgVars := t.packageVarTypes()
	// With no variable to change, only a read through a pointer can need a
	// wrap; the other marks only refine those two.
	if len(pkgVars) == 0 && len(t.mutableVars) == 0 && !t.readsThroughPointer() {
		return
	}
	for _, decl := range file.Decls {
		o := &operandOrder{t: t, pkgVars: pkgVars, decl: decl}
		o.captured, o.locals = capturedNames(decl, func(sel *ast.SelectorExpr) bool {
			return t.marked(sel.X, operandAddressed)
		})
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
// call (a pointer-receiver method takes it implicitly; addressed reports the
// method calls on a field `x.f` that take the address of x), and the names
// it declares locally.
func capturedNames(decl ast.Decl, addressed func(*ast.SelectorExpr) bool) (captured, locals map[string]bool) {
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
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok {
				break
			}
			if id, ok := sel.X.(*ast.Ident); ok {
				captured[id.Name] = true
			} else if id, ok := pathRootIdent(sel.X); ok && addressed(sel) {
				captured[id.Name] = true // `h.c.Inc()` takes &h.c
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
		// The receiver as written: sequence may replace it with a copy.
		var recv ast.Expr
		if receiver != nil {
			recv = *receiver
		}
		argCall, argExact := o.sequence(slots, func(slot *ast.Expr) bool {
			return slot != receiver || o.receiverCopyable(n.Fun)
		})
		// Go reads the value a method takes, `*p` for a value method called
		// through p, or a func field, when it makes the call, after the
		// arguments: a call among them could change it first. The method
		// value is read in order instead. Reads through a pointer are
		// ordered against the exact calls only, as in sequence.
		if recv != nil && argCall {
			if vars, pointers := o.receiverReads(recv); vars || pointers && argExact {
				n.Fun = &ast.CallExpr{Fun: o.t.stdIdent("EvalOperand"), Args: []ast.Expr{n.Fun}}
			}
		}
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

// receiverReads reports what the call reads through its receiver recv when it
// is made, rather than when recv is evaluated: a variable a call could change
// (vars), or storage through a pointer (pointers), the target of a value
// method or func field called through p included. A pointer-receiver method
// on a value reads nothing then: it takes the variable itself. A variable
// receiver's own value is sequence's to copy, or not, by its rule for
// variables; only what is read through it counts here.
func (o *operandOrder) receiverReads(recv ast.Expr) (vars, pointers bool) {
	if o.t.marked(recv, operandAddressed) {
		return false, false
	}
	vars, pointers = o.readsChangeable(recv)
	if _, isVar := recv.(*ast.Ident); isVar {
		vars = false
	}
	return vars, pointers || o.t.marked(recv, operandPointerReceiver)
}

// sequence wraps in std.EvalOperand each operand of slots, in evaluation order,
// that reads a changeable variable outside any call while a later operand
// makes a call. copyable, when set, vetoes the wrap for a slot. It returns
// whether an operand after the first makes a call, and an exact one
// (makesCall), as found before any wrap.
func (o *operandOrder) sequence(slots []*ast.Expr, copyable func(*ast.Expr) bool) (laterCall0, laterExact0 bool) {
	if len(slots) < 2 {
		return false, false
	}
	// laterCall[i] / laterExact[i]: an operand after i makes a call, by
	// makesCall's rule for variable reads / for reads through a pointer.
	laterCall := make([]bool, len(slots))
	laterExact := make([]bool, len(slots))
	for i := len(slots) - 2; i >= 0; i-- {
		call, exact := o.makesCall(*slots[i+1])
		laterCall[i] = laterCall[i+1] || call
		laterExact[i] = laterExact[i+1] || exact
	}
	for i, slot := range slots {
		if !laterCall[i] || (copyable != nil && !copyable(slot)) {
			continue
		}
		if vars, pointers := o.readsChangeable(*slot); !vars && !(pointers && laterExact[i]) {
			continue
		}
		*slot = &ast.CallExpr{Fun: o.t.stdIdent("EvalOperand"), Args: []ast.Expr{*slot}}
	}
	return laterCall[0], laterExact[0]
}

// readsChangeable reports whether evaluating e reads a variable that a call
// could change (vars), or reads through a pointer (pointers), other than
// inside a call of its own (Go evaluates that call, and its operands, in
// order with the others) or a function literal (not evaluated here).
func (o *operandOrder) readsChangeable(e ast.Expr) (vars, pointers bool) {
	var walk func(ast.Node) bool
	walk = func(n ast.Node) bool {
		if vars && pointers {
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
		case *ast.StarExpr:
			// A pointer's target: any call may change it.
			pointers = pointers || o.t.marked(n, operandThroughPointer)
			ast.Inspect(n.X, walk)
			return false
		case *ast.TypeAssertExpr:
			ast.Inspect(n.X, walk) // not the type
			return false
		case *ast.IndexExpr:
			// An element of a slice or map: any call may change it.
			pointers = pointers || o.t.marked(n, operandThroughPointer)
		case *ast.SelectorExpr:
			if id, ok := n.X.(*ast.Ident); ok && o.isPackageQualifier(id) {
				vars = vars || o.isImportedVar(id.Name, n.Sel.Name)
				return false
			}
			// A field read through a pointer reads its target.
			pointers = pointers || o.t.marked(n, operandThroughPointer)
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
			vars = vars || o.changeable(n.Name) || o.t.marked(n, operandPackageVar)
		}
		return true
	}
	ast.Inspect(e, walk)
	return vars, pointers
}

// makesCall reports whether evaluating e makes a call that could change a
// variable (see makesCallExcept); a package-level val's `.Get()` cannot.
// exact is the same with the calls known to change nothing left out too:
// reading a val field out of its std.Immutable box, `x.f.Get()`, and
// std.SomePtr, which only wraps its pointer. Reads through a pointer are
// ordered against the exact calls only; variable reads keep the rule they had
// before pointers were tracked, so their generated Go does not change. One
// walk answers both.
func (o *operandOrder) makesCall(e ast.Expr) (call, exact bool) {
	exact = o.t.makesCallExcept(e, o.changeable, func(c *ast.CallExpr) bool {
		if o.isPackageValGet(c) {
			return true
		}
		call = true
		return o.t.marked(c, operandBoxGet) || o.t.isStdHelperCall(c, "SomePtr")
	})
	return call || exact, exact
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
// a function literal or a method call captures. Names are compared, not
// bindings: a read the lowering resolved to a package var is marked
// (operandPackageVar) and counts wherever the name is bound locally too;
// this rule covers the reads it did not lower, such as names the transformer
// writes itself.
func (o *operandOrder) changeable(name string) bool {
	if _, ok := o.pkgVars[name]; ok && !o.locals[name] {
		return true
	}
	_, local := o.t.mutableVars[name]
	return local && o.locals[name] && o.captured[name]
}

// varType is the type of the changeable variable id reads, nil when unknown.
func (o *operandOrder) varType(id *ast.Ident) transpiler.Type {
	name := id.Name
	if o.t.marked(id, operandPackageVar) {
		return o.pkgVars[name] // whatever else the name binds here
	}
	if o.locals[name] {
		return o.t.mutableVars[name]
	}
	// A binding locals leaves out (a range variable, say) may be what the
	// name reads here, not the package var: its type is not known.
	if o.boundNames()[name] {
		return nil
	}
	return o.pkgVars[name]
}

// boundNames is declBoundNames of the declaration, computed once.
func (o *operandOrder) boundNames() map[string]bool {
	if o.bound == nil {
		o.bound = declBoundNames(o.decl)
	}
	return o.bound
}

// isPackageValGet reports whether call reads a package-level val of this
// package, `name.Get()`: its box never changes.
func (o *operandOrder) isPackageValGet(call *ast.CallExpr) bool {
	sel := immutableGetReceiver(call)
	if sel == nil || o.t.richAST == nil {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	// A local binding of the name, val or var, is not the package's val: its
	// Get may be any method.
	if _, local := o.t.mutableVars[id.Name]; local || o.boundNames()[id.Name] {
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
	return o.t.copyableReceiver(o.varType(id), sel.Sel.Name)
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
