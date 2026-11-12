package transformer

import (
	"cmp"
	"go/ast"
	"go/token"
	"slices"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// Argument order
//
// A call evaluates its receiver, then its explicit arguments in the order
// they are written, then the defaults of the parameters it leaves out, in
// parameter order. The lowered Go passes the arguments in parameter order,
// which differs from the written order when named arguments are written out
// of order, and a default that reads an earlier parameter needs that
// parameter's value.
//
// Each call keeps a frame while it is lowered (applyCallSuffix): every
// argument lowered for it (lowerArg), every default filled in, and the
// temporaries the defaults read. sequenceCall then compares the order in
// which the finished Go evaluates them with the order GALA requires. When
// the two disagree about a value that makes a call, the literal's keyed
// elements are put in the required order, or, for any other shape, the values
// are evaluated into temporaries, in the required order, inside a function
// literal called at once that then makes the call. A call whose arguments are
// already in order is left exactly as it was lowered.

// callFrame is what sequenceCall needs to know about one call being lowered.
type callFrame struct {
	args     []frameArg  // the arguments lowered for the call, where they are written
	defaults []ast.Expr  // the defaults filled in, in parameter order
	temps    []frameTemp // values a default reads, bound to temporaries
}

// frameArg is an argument lowered to expr from source at line:col.
type frameArg struct {
	line, col int
	expr      ast.Expr
}

// frameTemp is the temporary name that holds expr.
type frameTemp struct {
	expr ast.Expr
	name string
}

// topCallFrame is the frame of the innermost call being lowered, nil when
// none is.
func (t *galaASTTransformer) topCallFrame() *callFrame {
	if len(t.callFrames) == 0 {
		return nil
	}
	return t.callFrames[len(t.callFrames)-1]
}

// recordCallArg records expr, an argument lowered from v, on the innermost
// call's frame.
func (t *galaASTTransformer) recordCallArg(v argValue, expr ast.Expr) {
	frame := t.topCallFrame()
	if frame == nil || expr == nil || !v.at().ok() {
		return
	}
	line, col := v.at().start()
	frame.args = append(frame.args, frameArg{line: line, col: col, expr: expr})
}

// withoutCallFrame runs lower with no call frame on top. A construction no call
// wrote (a literal's omitted fields, a type's default value) fills in field
// defaults, which read no other field (GALA-E0098) and so need no sequencing;
// with no frame they are not recorded at all, nor taken for the defaults of
// the call being lowered around it.
func withoutCallFrame[T any](t *galaASTTransformer, lower func() (T, error)) (T, error) {
	t.callFrames = append(t.callFrames, nil)
	defer func() { t.callFrames = t.callFrames[:len(t.callFrames)-1] }()
	return lower()
}

// recordCallDefault records expr, a default the innermost call takes.
func (t *galaASTTransformer) recordCallDefault(expr ast.Expr) {
	if frame := t.topCallFrame(); frame != nil && expr != nil {
		frame.defaults = append(frame.defaults, expr)
	}
}

// boundArgValue is what a default reads for the earlier parameter whose
// argument is arg: arg itself when reading it again is the same as reading it
// once (it is inert), or else a temporary the innermost call evaluates arg
// into.
func (t *galaASTTransformer) boundArgValue(arg ast.Expr) ast.Expr {
	if t.inert(arg) {
		return arg
	}
	frame := t.topCallFrame()
	if frame == nil {
		return arg
	}
	for _, tmp := range frame.temps {
		if tmp.expr == arg {
			return ast.NewIdent(tmp.name)
		}
	}
	name := t.nextTempVar()
	frame.temps = append(frame.temps, frameTemp{expr: arg, name: name})
	return ast.NewIdent(name)
}

// changeableInScope reports whether name is bound to a variable a call could
// change: a `var`, local or package-level.
func (t *galaASTTransformer) changeableInScope(name string) bool {
	return t.isMutableVar(name) || (t.isTopLevelBinding(name) && t.isVar(name))
}

// callOperand is one value of a call, in the order GALA evaluates it.
type callOperand struct {
	expr   ast.Expr
	pinned bool   // a receiver its method may take the address of: it stays where it is unless it makes a call
	temp   string // the temporary a default reads it through; "" when none
}

// sequenceCall puts the values of the call lowered to expr, whose callee was
// lowered to fun, in GALA's order (see above).
func (t *galaASTTransformer) sequenceCall(expr, fun ast.Expr, frame *callFrame, line, col int) (ast.Expr, error) {
	if len(frame.temps) == 0 && (len(frame.args)+len(frame.defaults) < 2 || passedAsWritten(expr, frame)) {
		return expr, nil
	}
	var ops []callOperand
	if sel, ok := fun.(*ast.SelectorExpr); ok {
		// A receiver is copied ahead of the arguments only where the method
		// does not need the variable itself (a pointer receiver).
		ops = append(ops, callOperand{expr: sel.X, pinned: !t.copyableReceiver(t.getExprTypeName(sel.X), sel.Sel.Name)})
	}
	args := slices.Clone(frame.args)
	slices.SortStableFunc(args, func(a, b frameArg) int {
		return cmp.Or(cmp.Compare(a.line, b.line), cmp.Compare(a.col, b.col))
	})
	for _, a := range args {
		ops = append(ops, callOperand{expr: a.expr})
	}
	for _, d := range frame.defaults {
		ops = append(ops, callOperand{expr: d})
	}
	for _, tmp := range frame.temps {
		for i := range ops {
			if ops[i].expr == tmp.expr {
				ops[i].temp = tmp.name
			}
		}
	}

	at := evalPositions(expr, ops)
	var placed []int // indexes into ops of the operands expr evaluates, in GALA order
	for i := range ops {
		if _, ok := at[ops[i].expr]; ok {
			placed = append(placed, i)
		}
	}
	if len(frame.temps) == 0 && !t.outOfOrder(ops, placed, at) {
		return expr, nil
	}
	if lit, ok := expr.(*ast.CompositeLit); ok && len(frame.temps) == 0 {
		t.orderKeyedElements(lit, ops, placed)
		return lit, nil
	}
	return t.evaluateInOrder(expr, ops, at, frame.temps, line, col)
}

// passedAsWritten reports whether expr is a plain call whose arguments are the
// frame's, in the order they are written and with no default: the common
// positional call, already in GALA's order.
func passedAsWritten(expr ast.Expr, frame *callFrame) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(frame.defaults) != 0 || len(call.Args) != len(frame.args) {
		return false
	}
	for i, a := range frame.args {
		if call.Args[i] != a.expr || (i > 0 && cmp.Or(cmp.Compare(a.line, frame.args[i-1].line), cmp.Compare(a.col, frame.args[i-1].col)) < 0) {
			return false
		}
	}
	return true
}

// outOfOrder reports whether expr evaluates two of the placed operands in the
// opposite order to GALA, at least one of them making a call the other could
// observe.
func (t *galaASTTransformer) outOfOrder(ops []callOperand, placed []int, at map[ast.Expr]int) bool {
	effectful := make([]bool, len(ops))
	inert := make([]bool, len(ops))
	for _, i := range placed {
		effectful[i] = t.makesEffectfulCall(ops[i].expr)
		inert[i] = !effectful[i] && t.inert(ops[i].expr)
	}
	for i, a := range placed {
		for _, b := range placed[i+1:] {
			if at[ops[a].expr] > at[ops[b].expr] && ((effectful[a] && !inert[b]) || (effectful[b] && !inert[a])) {
				return true
			}
		}
	}
	return false
}

// orderKeyedElements sorts the elements of lit, a struct literal, by the
// first operand each holds; an element holding none keeps its place ahead of
// them. Go evaluates a literal's elements in the order they are written, and
// the keys name the fields whatever the order.
func (t *galaASTTransformer) orderKeyedElements(lit *ast.CompositeLit, ops []callOperand, placed []int) {
	rank := func(elt ast.Expr) int {
		at := evalPositions(elt, ops)
		for _, i := range placed {
			if _, ok := at[ops[i].expr]; ok {
				return i
			}
		}
		return -1
	}
	slices.SortStableFunc(lit.Elts, func(a, b ast.Expr) int { return cmp.Compare(rank(a), rank(b)) })
}

// evaluateInOrder evaluates the operands of expr that are not inert into
// temporaries, in GALA order, inside a function literal that is called at
// once and returns expr with the temporaries in their place. A receiver stays
// where it is unless it makes a call: a pointer-receiver method must see the
// variable itself, not a copy.
func (t *galaASTTransformer) evaluateInOrder(expr ast.Expr, ops []callOperand, at map[ast.Expr]int, temps []frameTemp, line, col int) (ast.Expr, error) {
	var resultType ast.Expr
	if lit, ok := expr.(*ast.CompositeLit); ok && lit.Type != nil {
		resultType = lit.Type
	} else {
		typ := t.getExprTypeName(expr)
		switch {
		case transpiler.IsUnusable(typ):
			return nil, galaerr.NewSemanticErrorAt(line, col,
				"cannot evaluate the arguments of this call in the order they are written: the type of its result is unknown; write the arguments in parameter order, or bind them to vals first")
		case !typ.IsVoid():
			resultType = t.typeToExpr(typ)
		}
	}
	replace := map[ast.Expr]ast.Expr{}
	var stmts []ast.Stmt
	bind := func(name string, value ast.Expr) {
		stmts = append(stmts, &ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent(name)}, Tok: token.DEFINE, Rhs: []ast.Expr{value}})
		replace[value] = ast.NewIdent(name)
	}
	for _, op := range ops {
		if _, placed := at[op.expr]; !placed {
			continue
		}
		switch {
		case op.temp != "":
			bind(op.temp, op.expr)
		case op.pinned && !t.makesEffectfulCall(op.expr), t.inert(op.expr):
		default:
			bind(t.nextTempVar(), op.expr)
		}
	}
	// A temporary a default reads whose argument is no operand of expr (it was
	// rewritten on the way) is still evaluated once, in its place.
	for _, tmp := range temps {
		if _, done := replace[tmp.expr]; !done {
			bind(tmp.name, tmp.expr)
		}
	}
	expr = replaceExprs(expr, replace)
	fn := &ast.FuncLit{Type: &ast.FuncType{Params: &ast.FieldList{}}, Body: &ast.BlockStmt{List: stmts}}
	if resultType == nil {
		fn.Body.List = append(fn.Body.List, &ast.ExprStmt{X: expr})
	} else {
		fn.Type.Results = &ast.FieldList{List: []*ast.Field{{Type: resultType}}}
		fn.Body.List = append(fn.Body.List, &ast.ReturnStmt{Results: []ast.Expr{expr}})
	}
	return &ast.CallExpr{Fun: fn}, nil
}

// replaceExprs returns expr with every node that is a key of with replaced by
// its value.
func replaceExprs(expr ast.Expr, with map[ast.Expr]ast.Expr) ast.Expr {
	if r, ok := with[expr]; ok {
		return r
	}
	holder := &ast.ParenExpr{X: expr}
	ast.Inspect(holder, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		for _, slot := range evalSlots(n) {
			if r, ok := with[*slot]; ok {
				*slot = r
			}
		}
		return true
	})
	return holder.X
}

// evalSlots returns the expression slots of n.
func evalSlots(n ast.Node) []*ast.Expr {
	switch n := n.(type) {
	case *ast.KeyValueExpr:
		return []*ast.Expr{&n.Key, &n.Value}
	case *ast.CompositeLit:
		slots := make([]*ast.Expr, len(n.Elts))
		for i := range n.Elts {
			slots[i] = &n.Elts[i]
		}
		return slots
	case *ast.ReturnStmt:
		slots := make([]*ast.Expr, len(n.Results))
		for i := range n.Results {
			slots[i] = &n.Results[i]
		}
		return slots
	case *ast.ExprStmt:
		return []*ast.Expr{&n.X}
	case *ast.AssignStmt:
		slots := make([]*ast.Expr, len(n.Rhs))
		for i := range n.Rhs {
			slots[i] = &n.Rhs[i]
		}
		return slots
	}
	return referenceSlots(n)
}

// evalPositions numbers the operands of ops that expr evaluates when it is
// evaluated, in the order Go evaluates them: a function literal called at
// once runs after its arguments, one not called is not evaluated here, and
// nothing inside an operand counts on its own.
func evalPositions(expr ast.Expr, ops []callOperand) map[ast.Expr]int {
	want := make(map[ast.Expr]bool, len(ops))
	for _, op := range ops {
		want[op.expr] = true
	}
	at := map[ast.Expr]int{}
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if e, ok := n.(ast.Expr); ok && want[e] {
			if _, seen := at[e]; !seen {
				at[e] = len(at)
			}
			return
		}
		switch n := n.(type) {
		case nil:
			return
		case *ast.FuncLit:
			return
		case *ast.CallExpr:
			if lit, ok := ast.Unparen(n.Fun).(*ast.FuncLit); ok {
				for _, arg := range n.Args {
					walk(arg)
				}
				walk(lit.Body)
				return
			}
		}
		ast.Inspect(n, func(c ast.Node) bool {
			if c == n {
				return true
			}
			if c != nil {
				walk(c)
			}
			return false
		})
	}
	walk(expr)
	return at
}

// makesEffectfulCall reports whether evaluating e, in the current scope, makes
// a call that could have an effect (see makesCallExcept); a val's `.Get()`
// has none.
func (t *galaASTTransformer) makesEffectfulCall(e ast.Expr) bool {
	return t.makesCallExcept(e, t.changeableInScope, func(call *ast.CallExpr) bool {
		_, isGet := t.immutableGetOperand(call)
		return isGet
	})
}

// inert reports whether e neither makes an effectful call nor reads a
// variable: evaluating it earlier or later gives the same value.
func (t *galaASTTransformer) inert(e ast.Expr) bool {
	if t.makesEffectfulCall(e) {
		return false
	}
	reads := false
	ast.Inspect(e, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.SelectorExpr:
			if pkg, ok := n.X.(*ast.Ident); ok {
				if pv := t.importedPackageVal(pkg.Name, n.Sel.Name); pv != nil {
					reads = !pv.IsVal // another package's var
					return false
				}
			}
			ast.Inspect(n.X, func(m ast.Node) bool {
				if id, ok := m.(*ast.Ident); ok && t.changeableInScope(id.Name) {
					reads = true
				}
				return !reads
			})
			return false
		case *ast.StarExpr:
			reads = true
		case *ast.Ident:
			if t.changeableInScope(n.Name) {
				reads = true
			}
		}
		return !reads
	})
	return !reads
}
