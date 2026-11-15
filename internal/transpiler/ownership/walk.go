package ownership

import (
	"maps"
	"regexp"
	"strings"

	"martianoff/gala/internal/interpolation"
	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// binding is a pointer binding of the function being walked.
type binding struct {
	name    string
	role    Role
	index   int // a parameter's index
	pointee transpiler.Type
	fresh   bool // a local bound to a fresh allocation or a fresh call
	file    string
	line    int
	col     int
	reasons []Reason
	// depth is len(w.overrides) where b was bound: a scope override below it
	// is the body b is declared in (see overridden).
	depth int
	// seq is b's index in walker.order.
	seq int
	// closureLent: a closure passed to a non-escaping parameter uses b
	// (phase 3), which phase 1 counts as a capture.
	closureLent bool
}

// escapes reports whether b has a reason that lets it escape.
func (b *binding) escapes() bool {
	for _, r := range b.reasons {
		if r.Kind.Escapes() {
			return true
		}
	}
	return false
}

// useKind is what happens to an expression's value.
type useKind int

const (
	useDiscard useKind = iota // an expression statement
	useValue                  // stored, compared, … as the use's reason says
	useReturn                 // the function's result
	useArg                    // an argument of a call
	useTarget                 // assigned to
	useAddr                   // its address is taken
)

// use is what happens to the value of the expression being walked. It only
// matters when that value is a pointer binding itself.
type use struct {
	kind   useKind
	reason ReasonKind
	detail string
	call   *callState
	param  int
}

func valueUse(r ReasonKind, detail string) use { return use{kind: useValue, reason: r, detail: detail} }

// calleeKind is what a call calls.
type calleeKind int

const (
	calleeUnknown     calleeKind = iota
	calleeGala                   // a GALA function or method, with a signature and maybe a summary
	calleeGo                     // a Go function or method, or a builtin
	calleeConstructor            // a struct or case constructor, or a generated Copy
	calleeEqual                  // a generated Equal
	calleeValue                  // a function value with a known type: a signature, no summary
)

// callee is a resolved callee.
type callee struct {
	kind       calleeKind
	name       string
	params     []transpiler.Type
	pnames     []string
	variadic   bool
	result     transpiler.Type
	collection bool // of collection_immutable or collection_mutable
	// noThunks: no parameter takes a by-name argument (a builtin).
	noThunks bool
	own      *decl
	meta     *transpiler.BorrowSummary
	// method: a method callee; recvPtr, its receiver is a pointer.
	method  bool
	recvPtr bool
	// tparams are the type parameters params may still name: the callee's
	// own, and its receiver type's.
	tparams []string
}

func (c *callee) summary() *transpiler.BorrowSummary {
	if c.own != nil {
		return c.own.summary
	}
	return c.meta
}

// returnsFresh reports whether a call of c returns a fresh pointer, in
// phase 3's view when p3 (BorrowSummary.ClosureReturnsFresh).
func (c *callee) returnsFresh(p3 bool) bool {
	if c == nil || c.kind != calleeGala || !isPointer(c.result) {
		return false
	}
	s := c.summary()
	if s == nil {
		return false
	}
	if p3 {
		return s.ClosureReturnsFresh
	}
	return s.ReturnsFresh
}

// paramBorrowed reports whether c borrows pointer parameter i, in phase 3's
// view when p3.
func (c *callee) paramBorrowed(i int, p3 bool) bool {
	if p3 {
		return c.summary().ParamClosureBorrowed(i)
	}
	return c.summary().ParamBorrowed(i)
}

// receiverBorrowed reports whether c borrows its pointer receiver, in phase
// 3's view when p3.
func (c *callee) receiverBorrowed(p3 bool) bool {
	s := c.summary()
	if s == nil {
		return false
	}
	if p3 {
		return s.ClosureReceiverBorrowed
	}
	return s.ReceiverBorrowed
}

// paramNonEscaping reports whether c only calls its function parameter i
// during the call: a GALA callee whose summary says so.
func (c *callee) paramNonEscaping(i int) bool {
	return c != nil && c.kind == calleeGala && c.summary().ParamNonEscaping(i)
}

// paramType is the type of argument i, nil when unknown.
func (c *callee) paramType(i int) transpiler.Type {
	switch {
	case i < 0 || len(c.params) == 0:
		return nil
	case c.variadic && i >= len(c.params)-1:
		return transpiler.ArrayType{Elem: c.params[len(c.params)-1]}
	case i < len(c.params):
		return c.params[i]
	}
	return nil
}

func (c *callee) paramIndex(name string) int {
	for i, n := range c.pnames {
		if n == name {
			return i
		}
	}
	return -1
}

// callState is one call being walked: who it calls, and the bindings it is
// lent so far (as a whole, or by its receiver's field path).
type callState struct {
	callee *callee
	whole  map[*binding]bool
	// captured holds the pointer bindings that closures passed to the
	// call's non-escaping parameters use (phase 3): the call may not also
	// lend them.
	captured map[*binding]native.GalaNodeRef
}

// fbinding is a function-typed (or by-name) parameter of the function being
// walked, in phase 3: whether it escapes decides its NonEscapingParams bit.
type fbinding struct {
	index   int
	escapes bool
}

// capFrame is a closure passed to a non-escaping parameter, being walked
// (phase 3): the pointer bindings declared before it (index below start)
// that it uses.
type capFrame struct {
	start int
	used  map[*binding]native.GalaNodeRef
}

// walker walks one function's body.
type walker struct {
	a     *pkg
	d     *decl
	binds map[string]*binding
	order []*binding
	// local holds every name the function binds anywhere: a callee or a
	// qualifier of one of these names is not the package's.
	local map[string]bool
	// bindCount counts the binders of each name.
	bindCount map[string]int
	// types holds the declared types of the parameters and receiver.
	types map[string]transpiler.Type
	// overrides, when not empty, says every use of a pointer binding, of
	// any form, is an escape for the top reason: inside a closure, or an
	// argument of a callee without a summary. A lambda's or partial
	// function's own override does not apply to the bindings it declares
	// (see overridden).
	overrides []override
	// closures counts the closures being walked: a `return` there is not
	// the function's.
	closures int
	// fresh is whether every result returned so far is fresh; retLocals
	// are the local bindings returned.
	fresh     bool
	retLocals []*binding
	// p3 walks as ownership phase 3 sees the body (docs/OWNERSHIP_DESIGN.MD
	// section 3.3): a closure passed directly to a non-escaping parameter
	// borrows the pointers it uses instead of capturing them, and callee
	// summaries are read in their Closure* view.
	p3 bool
	// fbinds holds the function's function-typed parameters (p3 only);
	// frames the non-escaping closures being walked.
	fbinds map[string]*fbinding
	frames []*capFrame
	// scoped holds the types of the lambda parameters and pattern variables
	// in scope, nil when unknown; otherBound the names some other binder
	// binds, whose types scoped never gives (see typeOf).
	scoped     map[string]transpiler.Type
	otherBound map[string]bool
}

// typeOf is the type of the local name: in phase 3, a lambda parameter's or
// pattern variable's in scope when only those bind it; else typed's. Phase
// 1's walk, whose bits the compiler's checks read, keeps its own precision.
func (w *walker) typeOf(name string) transpiler.Type {
	if w.p3 && w.local[name] && !w.otherBound[name] {
		return w.scoped[name]
	}
	return w.typed(name)
}

// withScoped binds the scoped types in ts while body runs, shadowing any
// outer binding of the same names, and restores them after.
func (w *walker) withScoped(ts map[string]transpiler.Type, body func()) {
	if len(ts) == 0 {
		body()
		return
	}
	old := maps.Clone(w.scoped)
	maps.Copy(w.scoped, ts)
	body()
	w.scoped = old
}

// walk walks d's body, as phase 3 sees it when p3.
func (a *pkg) walk(d *decl, p3 bool) *walker {
	w := &walker{a: a, d: d, binds: map[string]*binding{}, local: map[string]bool{}, bindCount: map[string]int{},
		types: map[string]transpiler.Type{}, fresh: true, p3: p3, fbinds: map[string]*fbinding{},
		scoped: map[string]transpiler.Type{}, otherBound: map[string]bool{}}
	w.countBinders(d.node.GalaNodeRef)
	fn := d.node
	if d.mmeta != nil {
		recv := fn.Receiver()
		var recvType transpiler.Type = d.recvType
		if d.recvPtr {
			recvType = transpiler.PointerType{Elem: d.recvType, NonNull: d.recvNN}
			w.newBinding(d.recvName, RoleReceiver, -1, d.recvType, true, recv.Identifier().GalaNodeRef, !d.recvNN)
		}
		w.types[d.recvName] = recvType
	}
	params := fn.Signature().Parameters().ParameterList().AllParameter()
	for i, p := range params {
		if !p.Identifier().Ok() || i >= len(d.params) {
			continue
		}
		name := p.Identifier().GetText()
		w.types[name] = d.params[i]
		if a.pointerParam(d, i) {
			pt := d.params[i].(transpiler.PointerType)
			b := w.newBinding(name, RoleParam, i, pt.Elem, true, p.Identifier().GalaNodeRef, !pt.NonNull)
			if p.VAR().Ok() {
				w.reason(b, Origin, "a var parameter", p.GalaNodeRef)
			}
		}
		if p3 && a.funcParam(d, i) && w.bindCount[name] == 1 && !p.VAR().Ok() {
			// A parameter bound once, that the body cannot reassign: every
			// use of its name is a use of the argument.
			w.fbinds[name] = &fbinding{index: i}
		}
	}
	for _, p := range params {
		if def := p.ParamDefault(); def.Ok() {
			w.expr(def.Expression().GalaNodeRef, valueUse(Other, "a default value"))
		}
	}
	switch {
	case fn.Block().Ok():
		w.bodyBlock(fn.Block())
	case fn.Expression().Ok():
		if d.hasRes {
			w.ret(fn.Expression().GalaNodeRef)
		} else {
			w.expr(fn.Expression().GalaNodeRef, use{kind: useDiscard})
		}
	}
	return w
}

// bodyBlock walks a function's block body. With a result, its trailing
// expression is returned, as a `return` would return it.
func (w *walker) bodyBlock(b native.BlockNode) {
	stmts := b.AllStatement()
	if !w.d.hasRes || len(stmts) == 0 {
		w.block(b, use{kind: useDiscard})
		return
	}
	last := stmts[len(stmts)-1].Declaration().SimpleStatement()
	if !last.Ok() || !last.Expression().Ok() || hasBindDecl(stmts) {
		// No trailing expression, or a bind that turns the rest into a
		// closure: block decides; the result is not known to be fresh.
		w.block(b, use{kind: useReturn})
		w.fresh = false
		return
	}
	for _, s := range stmts[:len(stmts)-1] {
		w.statement(s, use{kind: useDiscard})
	}
	w.ret(last.Expression().GalaNodeRef)
}

// hasBindDecl reports whether a block holds a bind, also or use.
func hasBindDecl(stmts []native.StatementNode) bool {
	for _, s := range stmts {
		if d := s.Declaration(); d.BindDeclaration().Ok() || d.AlsoDeclaration().Ok() || d.UseDeclaration().Ok() {
			return true
		}
	}
	return false
}

// summary is the borrow summary the walk found.
func (w *walker) summary() *transpiler.BorrowSummary {
	d := w.d
	s := &transpiler.BorrowSummary{BorrowedParams: make([]bool, len(d.params)), NonEscapingParams: make([]bool, len(d.params))}
	for _, fb := range w.fbinds {
		s.NonEscapingParams[fb.index] = !fb.escapes
	}
	for _, b := range w.order {
		switch b.role {
		case RoleReceiver:
			s.ReceiverBorrowed = !b.escapes()
		case RoleParam:
			s.BorrowedParams[b.index] = !b.escapes()
		}
	}
	if isPointer(d.result) && w.fresh {
		s.ReturnsFresh = true
		for _, b := range w.retLocals {
			if b.escapes() {
				s.ReturnsFresh = false
			}
		}
	}
	return s
}

// typed is the declared type of a parameter or the receiver, nil for any
// other name or one bound more than once.
func (w *walker) typed(name string) transpiler.Type {
	if w.bindCount[name] > 1 {
		return nil
	}
	return w.types[name]
}

// newBinding adds a pointer binding. nullable marks a `*T`.
func (w *walker) newBinding(name string, role Role, index int, pointee transpiler.Type, fresh bool, at native.GalaNodeRef, nullable bool) *binding {
	tok := at.Start()
	b := &binding{name: name, role: role, index: index, pointee: pointee, fresh: fresh,
		file: w.d.file.path, line: int(tok.Line), col: int(tok.Column) + 1, depth: len(w.overrides), seq: len(w.order)}
	if nullable {
		w.reason(b, Nullable, "", at)
	}
	if w.bindCount[name] > 1 {
		w.reason(b, Rebound, "", at)
	}
	if old := w.binds[name]; old != nil {
		w.reason(old, Rebound, "", at)
	}
	w.binds[name] = b
	w.order = append(w.order, b)
	return b
}

// reason records why b is not unique, at node at.
func (w *walker) reason(b *binding, k ReasonKind, detail string, at native.GalaNodeRef) {
	r := Reason{Kind: k, Detail: detail, File: w.d.file.path}
	if at.Ok() {
		tok := at.Start()
		r.Line, r.Col = int(tok.Line), int(tok.Column)+1
	}
	for _, old := range b.reasons {
		if old == r {
			return
		}
	}
	b.reasons = append(b.reasons, r)
}

// override is one entry of walker.overrides. scope marks the body of a
// lambda or partial function: a binding it declares lives in that body,
// whose result is walked as an escape, so the body's override is not about
// that binding.
type override struct {
	Reason
	scope bool
}

// overridden records the active override against b, if one applies to it:
// any override, except a scope pushed before b was bound (binding.depth).
// A pointer bound inside a lambda is not captured by that lambda.
func (w *walker) overridden(b *binding, at native.GalaNodeRef) bool {
	n := len(w.overrides)
	if n == 0 {
		return false
	}
	o := w.overrides[n-1]
	if o.scope && n <= b.depth {
		return false
	}
	w.reason(b, o.Kind, o.Detail, at)
	return true
}

func (w *walker) push(k ReasonKind, detail string) {
	w.overrides = append(w.overrides, override{Reason: Reason{Kind: k, Detail: detail}})
}

// pushScope pushes the override of a lambda's or partial function's body.
func (w *walker) pushScope(detail string) {
	w.overrides = append(w.overrides, override{Reason: Reason{Kind: Captured, Detail: detail}, scope: true})
}
func (w *walker) pop() { w.overrides = w.overrides[:len(w.overrides)-1] }

// touch records a use of b in every non-escaping closure being walked that
// b was declared outside of: the closure borrows b for its call.
func (w *walker) touch(b *binding, at native.GalaNodeRef) {
	for _, f := range w.frames {
		if b.seq < f.start {
			if _, ok := f.used[b]; !ok {
				f.used[b] = at
			}
			b.closureLent = true
		}
	}
}

// funcUse is a use of fb, a function parameter, by its name with suffixes
// sufs and use u (phase 3). It stays non-escaping when the use is a call,
// or the bare name passed to a non-escaping parameter, outside any override
// (an escaping closure, an argument the analysis cannot follow).
func (w *walker) funcUse(fb *fbinding, sufs []native.PostfixSuffixNode, u use) {
	switch {
	case len(w.overrides) > 0:
		fb.escapes = true
	case len(sufs) > 0 && isCall(sufs[0]):
	case len(sufs) == 0 && u.kind == useArg && u.call.callee.paramNonEscaping(u.param):
	default:
		fb.escapes = true
	}
}

// borrowedClosure walks body, a closure passed to a non-escaping parameter
// of call cs (phase 3): it runs only during the call, so the pointers it
// uses are lent to it rather than captured, and the call may not also lend
// them (exclusivity, checked by args).
func (w *walker) borrowedClosure(cs *callState, body func()) {
	if cs.captured == nil {
		cs.captured = map[*binding]native.GalaNodeRef{}
	}
	w.frames = append(w.frames, &capFrame{start: len(w.order), used: cs.captured})
	w.closures++
	body()
	w.closures--
	w.frames = w.frames[:len(w.frames)-1]
}

// countBinders finds every name the function binds: parameters, the
// receiver, vals and vars, lambda parameters, pattern variables, loop
// variables, local functions, bind/also/use names.
//
// It also records in otherBound the names some binder other than a lambda
// parameter or a case pattern binds: only the rest have scoped types.
func (w *walker) countBinders(root native.GalaNodeRef) {
	lambdaParams := map[int]bool{}
	scopedAdd := func(id native.IdentifierNode) {
		if id.Ok() {
			n := id.GetText()
			w.bindCount[n]++
			w.local[n] = true
		}
	}
	add := func(id native.IdentifierNode) {
		scopedAdd(id)
		if id.Ok() {
			w.otherBound[id.GetText()] = true
		}
	}
	addList := func(l native.IdentifierListNode) {
		for _, id := range l.AllIdentifier() {
			add(id)
		}
	}
	root.Walk(func(n native.GalaNodeRef) bool {
		switch n.Kind() {
		case native.GalaKIND_lambdaExpression:
			for _, p := range (native.LambdaExpressionNode{GalaNodeRef: n}).Parameters().ParameterList().AllParameter() {
				lambdaParams[p.GalaNodeRef.StartIndex()] = true
			}
		case native.GalaKIND_receiver:
			add(native.ReceiverNode{GalaNodeRef: n}.Identifier())
		case native.GalaKIND_parameter:
			if lambdaParams[n.StartIndex()] {
				scopedAdd(native.ParameterNode{GalaNodeRef: n}.Identifier())
			} else {
				add(native.ParameterNode{GalaNodeRef: n}.Identifier())
			}
		case native.GalaKIND_valDeclaration:
			v := native.ValDeclarationNode{GalaNodeRef: n}
			addList(v.IdentifierList())
			addList(v.TuplePattern().IdentifierList())
		case native.GalaKIND_varDeclaration:
			v := native.VarDeclarationNode{GalaNodeRef: n}
			addList(v.IdentifierList())
			addList(v.TuplePattern().IdentifierList())
		case native.GalaKIND_bindDeclaration:
			add(native.BindDeclarationNode{GalaNodeRef: n}.Identifier())
		case native.GalaKIND_alsoDeclaration:
			add(native.AlsoDeclarationNode{GalaNodeRef: n}.Identifier())
		case native.GalaKIND_useDeclaration:
			add(native.UseDeclarationNode{GalaNodeRef: n}.Identifier())
		case native.GalaKIND_rangeClause:
			addList(native.RangeClauseNode{GalaNodeRef: n}.IdentifierList())
		case native.GalaKIND_shortVarDecl:
			addList(native.ShortVarDeclNode{GalaNodeRef: n}.IdentifierList())
		case native.GalaKIND_typedPattern:
			add(native.TypedPatternNode{GalaNodeRef: n}.Identifier())
		case native.GalaKIND_functionDeclaration:
			if n != root {
				add(native.FunctionDeclarationNode{GalaNodeRef: n}.Identifier())
			}
		case native.GalaKIND_caseClause:
			// Any name in a pattern may bind: count them all.
			native.CaseClauseNode{GalaNodeRef: n}.Pattern().GalaNodeRef.Walk(func(p native.GalaNodeRef) bool {
				if p.Kind() == native.GalaKIND_primary {
					scopedAdd(native.PrimaryNode{GalaNodeRef: p}.Identifier())
				}
				return true
			})
		}
		return true
	})
}

// --- Statements ---

// block walks b; u is the use of its last statement's value.
func (w *walker) block(b native.BlockNode, u use) {
	stmts := b.AllStatement()
	pushed := 0
	for i, s := range stmts {
		su := use{kind: useDiscard}
		if i == len(stmts)-1 {
			su = u
		}
		w.statement(s, su)
		if d := s.Declaration(); d.BindDeclaration().Ok() || d.AlsoDeclaration().Ok() || d.UseDeclaration().Ok() {
			// The rest of the block is the body of a lambda the
			// declaration desugars into.
			w.push(Captured, "the rest of a block after "+firstWord(d.GalaNodeRef))
			pushed++
		}
	}
	for ; pushed > 0; pushed-- {
		w.pop()
	}
}

func firstWord(n native.GalaNodeRef) string {
	return n.Tree().Text(n.StartIndex())
}

func (w *walker) statement(s native.StatementNode, u use) {
	if r := s.ReturnStatement(); r.Ok() {
		if r.Expression().Ok() {
			w.ret(r.Expression().GalaNodeRef)
		}
		return
	}
	d := s.Declaration()
	switch {
	case d.ValDeclaration().Ok():
		v := d.ValDeclaration()
		w.binder(v.TuplePattern(), v.IdentifierList(), v.Type_(), v.ExpressionList(), false)
	case d.VarDeclaration().Ok():
		v := d.VarDeclaration()
		w.binder(v.TuplePattern(), v.IdentifierList(), v.Type_(), v.ExpressionList(), true)
	case d.BindDeclaration().Ok():
		w.expr(d.BindDeclaration().Expression().GalaNodeRef, valueUse(Captured, "bound by bind"))
	case d.AlsoDeclaration().Ok():
		w.expr(d.AlsoDeclaration().Expression().GalaNodeRef, valueUse(Captured, "bound by also"))
	case d.UseDeclaration().Ok():
		w.expr(d.UseDeclaration().Expression().GalaNodeRef, valueUse(Captured, "bound by use"))
	case d.FunctionDeclaration().Ok():
		w.closure("a local function", false, func() { w.generic(d.FunctionDeclaration().GalaNodeRef) })
	case d.TypeDeclaration().Ok(), d.ImportDeclaration().Ok():
	case d.IfStatement().Ok():
		w.ifStatement(d.IfStatement())
	case d.ForStatement().Ok():
		w.forStatement(d.ForStatement())
	case d.SimpleStatement().Ok():
		w.simple(d.SimpleStatement(), u)
	default:
		w.generic(d.GalaNodeRef)
	}
}

func (w *walker) ifStatement(s native.IfStatementNode) {
	if s.SimpleStatement().Ok() {
		w.simple(s.SimpleStatement(), use{kind: useDiscard})
	}
	w.expr(s.Expression().GalaNodeRef, valueUse(Other, "a condition"))
	for _, b := range s.AllBlock() {
		w.block(b, use{kind: useDiscard})
	}
	if s.IfStatement().Ok() {
		w.ifStatement(s.IfStatement())
	}
}

func (w *walker) forStatement(s native.ForStatementNode) {
	if c := s.ForClause(); c.Ok() {
		for _, st := range c.AllSimpleStatement() {
			w.simple(st, use{kind: useDiscard})
		}
		if c.Expression().Ok() {
			w.expr(c.Expression().GalaNodeRef, valueUse(Other, "a condition"))
		}
	}
	if c := s.ForCondition(); c.Ok() {
		w.expr(c.Expression().GalaNodeRef, valueUse(Other, "a condition"))
	}
	if r := s.RangeClause(); r.Ok() {
		w.expr(r.Expression().GalaNodeRef, valueUse(Other, "ranged over"))
	}
	w.block(s.Block(), use{kind: useDiscard})
}

func (w *walker) simple(s native.SimpleStatementNode, u use) {
	switch {
	case s.IncDecStmt().Ok():
		w.expr(s.IncDecStmt().Expression().GalaNodeRef, use{kind: useTarget})
	case s.Assignment().Ok():
		as := s.Assignment()
		op := as.GalaNodeRef.Tree().Text(as.ExpressionList(0).StopIndex() + 1)
		targets := as.ExpressionList(0).AllExpression()
		goTarget := make([]bool, len(targets))
		for i, t := range targets {
			goTarget[i] = w.expr(t.GalaNodeRef, use{kind: useTarget}).fromGo
		}
		for i, v := range as.ExpressionList(1).AllExpression() {
			vu := valueUse(Other, "a compound assignment")
			if op == "=" {
				vu = valueUse(Stored, "assigned to a field or element")
				if i < len(targets) {
					if name, ok := bareName(targets[i].GalaNodeRef); ok {
						vu = valueUse(Aliased, "assigned to "+name)
					} else if goTarget[i] {
						vu = valueUse(PassedToGo, "assigned to a field of a Go value")
					}
				}
			}
			w.expr(v.GalaNodeRef, vu)
		}
	case s.ShortVarDecl().Ok():
		sv := s.ShortVarDecl()
		w.binder(native.TuplePatternNode{}, sv.IdentifierList(), native.TypeNode{}, sv.ExpressionList(), true)
	case s.Expression().Ok():
		w.expr(s.Expression().GalaNodeRef, u)
	default:
		w.generic(s.GalaNodeRef)
	}
}

// binder walks a val, var or := declaration, and adds a pointer binding for
// a single name bound to a pointer.
func (w *walker) binder(tp native.TuplePatternNode, ids native.IdentifierListNode, typ native.TypeNode, values native.ExpressionListNode, isVar bool) {
	vals := values.AllExpression()
	names := ids.AllIdentifier()
	if tp.Ok() || len(names) != 1 || len(vals) > 1 {
		for _, v := range vals {
			w.expr(v.GalaNodeRef, valueUse(Aliased, "bound to a name"))
		}
		return
	}
	id := names[0]
	name := id.GetText()
	var res chainResult
	if len(vals) == 1 {
		res = w.expr(vals[0].GalaNodeRef, valueUse(Aliased, "bound to "+name))
	}
	pointee, fresh, nonNull, isPtr := w.origin(vals, res, typ)
	if !isPtr {
		if res.typ != nil {
			// Its type (a var keeps it) lets a method call on it resolve.
			w.types[name] = res.typ
		}
		return
	}
	b := w.newBinding(name, RoleLocal, -1, pointee, fresh, id.GalaNodeRef, !nonNull)
	if res.fromGo || len(vals) == 1 && isReceive(vals[0].GalaNodeRef) {
		w.reason(b, FromGo, "", id.GalaNodeRef)
		b.fresh = false
	}
	switch {
	case isVar:
		w.reason(b, Origin, "a var binding", id.GalaNodeRef)
		b.fresh = false
	case res.fromGo:
	case !fresh:
		w.reason(b, Origin, "not bound to a fresh allocation or a call that returns fresh", id.GalaNodeRef)
	}
}

// isReceive reports whether e is a channel receive, `<-ch`.
func isReceive(e native.GalaNodeRef) bool {
	e = unwrapExpr(e)
	if e.Kind() != native.GalaKIND_unaryExpr {
		return false
	}
	op := native.UnaryExprNode{GalaNodeRef: e}.UnaryOp()
	return op.Ok() && op.GetText() == "<-"
}

// origin decides whether a declaration binds a pointer, and of what origin.
func (w *walker) origin(vals []native.ExpressionNode, res chainResult, typ native.TypeNode) (pointee transpiler.Type, fresh, nonNull, isPtr bool) {
	if len(vals) == 1 {
		if t, ok := w.allocation(vals[0].GalaNodeRef); ok {
			return t, true, true, true
		}
		if res.callee != nil {
			if pt, ok := res.callee.result.(transpiler.PointerType); ok {
				return pt.Elem, res.callee.returnsFresh(w.p3), pt.NonNull, true
			}
		}
		if pt, ok := res.typ.(transpiler.PointerType); ok {
			return pt.Elem, false, pt.NonNull, true
		}
	}
	if typ.Ok() {
		text := typ.GetText()
		if strings.HasPrefix(text, "Ptr[") || strings.HasPrefix(text, "*") {
			return nil, false, strings.HasPrefix(text, "Ptr["), true
		}
	}
	return nil, false, false, false
}

// ret walks a returned expression, and keeps track of whether the result is
// always fresh.
func (w *walker) ret(e native.GalaNodeRef) {
	if w.closures > 0 {
		w.expr(e, closureResult)
		return
	}
	w.expr(e, use{kind: useReturn})
	if isPointer(w.d.result) && !w.isFresh(e) {
		w.fresh = false
	}
}

// isFresh reports whether e is always a fresh pointer: an allocation, a call
// that returns fresh, a fresh local, or a branch of those.
func (w *walker) isFresh(e native.GalaNodeRef) bool {
	e = unwrapExpr(e)
	if _, ok := w.allocation(e); ok {
		return true
	}
	if e.Kind() != native.GalaKIND_postfixExpr {
		return false
	}
	p := native.PostfixExprNode{GalaNodeRef: e}
	if p.MATCH().Ok() {
		if len(p.AllPostfixSuffix()) > 0 {
			return false
		}
		for _, c := range p.AllCaseClause() {
			if !w.freshBody(c.GetBodyBlock(), c.GetBodyStmt()) {
				return false
			}
		}
		return true
	}
	prim := p.PrimaryExpr()
	if ie := prim.IfExpression(); ie.Ok() && len(p.AllPostfixSuffix()) == 0 {
		for _, br := range ie.AllIfExprBranch() {
			if !w.freshBody(br.Block(), native.SimpleStatementNode{}) && !(br.Expression().Ok() && w.isFresh(br.Expression().GalaNodeRef)) {
				return false
			}
		}
		return true
	}
	if name, ok := bareName(e); ok {
		if b := w.binds[name]; b != nil && b.role == RoleLocal && b.fresh {
			w.retLocals = append(w.retLocals, b)
			return true
		}
		return false
	}
	c := w.peekCallee(p)
	return c.returnsFresh(w.p3)
}

// freshBody is isFresh for a block's last statement or a case's statement.
func (w *walker) freshBody(b native.BlockNode, s native.SimpleStatementNode) bool {
	if b.Ok() {
		stmts := b.AllStatement()
		if len(stmts) == 0 {
			return false
		}
		last := stmts[len(stmts)-1]
		if r := last.ReturnStatement(); r.Ok() {
			return true // checked by ret
		}
		s = last.Declaration().SimpleStatement()
	}
	return s.Ok() && s.Expression().Ok() && w.isFresh(s.Expression().GalaNodeRef)
}

// --- Expressions ---

// chainResult is what a walk learns about an expression's value: its type,
// when known, and the callee when it is a call.
type chainResult struct {
	typ    transpiler.Type
	callee *callee
	// fromGo: the value came from Go (a Go call's result, a Go value's field).
	fromGo bool
}

// unwrapExpr descends from e through operator levels with one operand, to
// the unary or postfix expression they hold.
func unwrapExpr(e native.GalaNodeRef) native.GalaNodeRef {
	for {
		switch e.Kind() {
		case native.GalaKIND_expression:
			e = native.ExpressionNode{GalaNodeRef: e}.OrExpr().GalaNodeRef
		case native.GalaKIND_orExpr, native.GalaKIND_andExpr, native.GalaKIND_equalityExpr,
			native.GalaKIND_relationalExpr, native.GalaKIND_additiveExpr, native.GalaKIND_multiplicativeExpr:
			if e.ChildCount() != 1 || e.Child(0).IsToken() {
				return e
			}
			e = e.Child(0).Node()
		case native.GalaKIND_unaryExpr:
			u := native.UnaryExprNode{GalaNodeRef: e}
			if u.UnaryOp().Ok() {
				return e
			}
			e = u.PostfixExpr().GalaNodeRef
		default:
			return e
		}
	}
}

// bareName is the name e is, when it is a bare identifier.
func bareName(e native.GalaNodeRef) (string, bool) {
	e = unwrapExpr(e)
	if e.Kind() != native.GalaKIND_postfixExpr {
		return "", false
	}
	p := native.PostfixExprNode{GalaNodeRef: e}
	if len(p.AllPostfixSuffix()) > 0 || p.MATCH().Ok() {
		return "", false
	}
	return primIdent(p.PrimaryExpr())
}

// primIdent is the identifier a primary expression is, if it is one.
func primIdent(p native.PrimaryExprNode) (string, bool) {
	pr := p.Primary()
	if !pr.Ok() || !pr.Identifier().Ok() {
		return "", false
	}
	return pr.Identifier().GetText(), true
}

// allocation reports whether e is `&T{...}` or `&T(...)` for a struct type
// T, and T.
func (w *walker) allocation(e native.GalaNodeRef) (transpiler.Type, bool) {
	e = unwrapExpr(e)
	if e.Kind() != native.GalaKIND_unaryExpr {
		return nil, false
	}
	u := native.UnaryExprNode{GalaNodeRef: e}
	if op := u.UnaryOp(); !op.Ok() || op.GetText() != "&" {
		return nil, false
	}
	inner := unwrapExpr(u.UnaryExpr().GalaNodeRef)
	if inner.Kind() != native.GalaKIND_postfixExpr {
		return nil, false
	}
	p := native.PostfixExprNode{GalaNodeRef: inner}
	sufs := p.AllPostfixSuffix()
	if p.MATCH().Ok() {
		return nil, false
	}
	if cl := p.PrimaryExpr().Primary().CompositeLiteral(); cl.Ok() && len(sufs) == 0 {
		return w.typeNamed(cl.Type_())
	}
	if c := w.peekCallee(p); c.kind == calleeConstructor && c.result != nil {
		return c.result, true
	}
	return nil, false
}

func isCall(s native.PostfixSuffixNode) bool {
	return !s.Identifier().Ok() && !s.ExpressionList().Ok()
}

// typeNamed resolves a composite literal's type, `T` or `pkg.T`, to a type
// with metadata.
func (w *walker) typeNamed(t native.TypeNode) (transpiler.Type, bool) {
	q := t.QualifiedIdentifier()
	if !q.Ok() || t.TypeArguments().Ok() {
		return nil, false
	}
	ids := q.AllIdentifier()
	switch len(ids) {
	case 1:
		if key := w.typeKeyOfName(ids[0].GetText()); key != "" {
			return namedOfKey(key), true
		}
	case 2:
		if pk, ok := w.d.file.quals[ids[0].GetText()]; ok && !w.local[ids[0].GetText()] {
			key := qualified(pk, ids[1].GetText())
			if _, ok := w.a.rich.Types[key]; ok {
				return namedOfKey(key), true
			}
		}
	}
	return nil, false
}

// typeKeyOfName is the key of the type a bare name names: the package's own
// or a dot-imported one's; "" for none.
func (w *walker) typeKeyOfName(name string) string {
	if w.local[name] {
		return ""
	}
	if key := qualified(w.a.name, name); w.a.rich.Types[key] != nil {
		return key
	}
	for _, dot := range w.d.file.dots {
		if key := qualified(dot, name); w.a.rich.Types[key] != nil {
			return key
		}
	}
	return ""
}

func namedOfKey(key string) transpiler.Type {
	if i := strings.LastIndex(key, "."); i >= 0 {
		return transpiler.NamedType{Package: key[:i], Name: key[i+1:]}
	}
	return transpiler.NamedType{Name: key}
}

// expr walks expression e, whose value is used as u says.
func (w *walker) expr(e native.GalaNodeRef, u use) chainResult {
	switch e.Kind() {
	case native.GalaKIND_expression:
		return w.expr(native.ExpressionNode{GalaNodeRef: e}.OrExpr().GalaNodeRef, u)
	case native.GalaKIND_orExpr, native.GalaKIND_andExpr, native.GalaKIND_equalityExpr,
		native.GalaKIND_relationalExpr, native.GalaKIND_additiveExpr, native.GalaKIND_multiplicativeExpr:
		if e.ChildCount() == 1 && !e.Child(0).IsToken() {
			return w.expr(e.Child(0).Node(), u)
		}
		ou := valueUse(Other, "an operand")
		if e.Kind() == native.GalaKIND_equalityExpr {
			ou = valueUse(Compared, "")
		}
		for i := range e.ChildCount() {
			if c := e.Child(i); !c.IsToken() {
				w.expr(c.Node(), ou)
			}
		}
		return chainResult{}
	case native.GalaKIND_unaryExpr:
		return w.unary(native.UnaryExprNode{GalaNodeRef: e}, u)
	case native.GalaKIND_postfixExpr:
		return w.postfix(native.PostfixExprNode{GalaNodeRef: e}, u)
	}
	w.generic(e)
	return chainResult{}
}

func (w *walker) unary(x native.UnaryExprNode, u use) chainResult {
	op := x.UnaryOp()
	if !op.Ok() {
		return w.postfix(x.PostfixExpr(), u)
	}
	operand := x.UnaryExpr().GalaNodeRef
	switch op.GetText() {
	case "*":
		// A deref read yields a copy: a lend.
		if name, ok := bareName(operand); ok {
			if b := w.binds[name]; b != nil {
				w.touch(b, operand)
				if !w.overridden(b, operand) {
					switch {
					case u.kind == useAddr:
						w.reason(b, Other, "address of its pointee", operand)
					case w.leaksReference(b.pointee, u):
						// The copy still holds the references the pointee
						// does.
						w.reason(b, PassedToGo, "a copy of its pointee, holding a reference", operand)
					}
				}
				return chainResult{}
			}
		}
		w.expr(operand, valueUse(Other, "dereferenced"))
	case "&":
		w.expr(operand, use{kind: useAddr})
	default:
		w.expr(operand, valueUse(Other, "an operand"))
	}
	return chainResult{}
}

// bareUse is a use of binding b by its name alone.
func (w *walker) bareUse(b *binding, u use, at native.GalaNodeRef) {
	if w.overridden(b, at) {
		return
	}
	switch u.kind {
	case useDiscard:
		w.reason(b, Other, "an unused value", at)
	case useValue:
		w.reason(b, u.reason, u.detail, at)
	case useTarget:
		w.reason(b, Other, "reassigned", at)
	case useAddr:
		w.reason(b, Other, "its address taken", at)
	case useReturn:
		if b.role != RoleLocal || !b.fresh || !isPointer(w.d.result) {
			w.reason(b, Returned, "", at)
		}
	case useArg:
		w.lend(b, u.call, u.param, at)
	}
}

// lend is b passed whole as argument i of call cs.
func (w *walker) lend(b *binding, cs *callState, i int, at native.GalaNodeRef) {
	c := cs.callee
	if cs.whole[b] {
		w.reason(b, PassedTwice, c.name, at)
		return
	}
	cs.whole[b] = true
	if pt := c.paramType(i); c.kind != calleeGo && isInterface(w.a, pt) {
		// Converted to an interface: Go code can reach it (fmt, reflection,
		// a type switch in a hand-written .go file).
		w.reason(b, PassedToGo, "converted to "+pt.String()+" for "+c.name, at)
		return
	}
	switch c.kind {
	case calleeGala:
		pt := c.paramType(i)
		switch {
		case isPointer(pt) && c.paramBorrowed(i, w.p3):
		case c.collection:
			w.reason(b, Collection, c.name, at)
		case isPointer(pt) && c.summary() == nil:
			w.reason(b, UnknownCallee, c.name+" has no summary", at)
		case isPointer(pt):
			w.reason(b, EscapingCallee, c.name+" "+paramLabel(c, i), at)
		case pt == nil:
			w.reason(b, UnknownCallee, c.name+" has no parameter for it", at)
		default:
			w.reason(b, EscapingCallee, c.name+" takes it as "+pt.String(), at)
		}
	case calleeGo:
		w.reason(b, PassedToGo, c.name, at)
	case calleeConstructor:
		if c.collection {
			w.reason(b, Collection, c.name, at)
		} else {
			w.reason(b, Stored, c.name, at)
		}
	case calleeEqual:
		w.reason(b, Compared, c.name, at)
	default:
		w.reason(b, UnknownCallee, c.name, at)
	}
}

// isInterface reports whether t is an interface type: any, error, an
// interface literal, or a named GALA or Go interface. A variadic `...any`
// counts.
func isInterface(a *pkg, t transpiler.Type) bool {
	switch tt := t.(type) {
	case transpiler.BasicType:
		return tt.Name == "any" || tt.Name == "error" || tt.Name == "interface{}"
	case transpiler.InterfaceType:
		return true
	case transpiler.ArrayType:
		return isInterface(a, tt.Elem)
	case transpiler.NamedType, transpiler.GenericType:
		key := a.typeKey(tt)
		if tm := a.rich.Types[key]; tm != nil {
			return tm.IsInterface
		}
		if gi := a.rich.GoTypeInfo; gi != nil {
			if gt := gi.Types[key]; gt != nil {
				return gt.Kind == "interface"
			}
		}
	}
	return false
}

func paramLabel(c *callee, i int) string {
	if i >= 0 && i < len(c.pnames) {
		return "parameter " + c.pnames[i]
	}
	return "a parameter"
}

// postfix walks `primary suffix* (match {...})?`.
func (w *walker) postfix(p native.PostfixExprNode, u use) chainResult {
	sufs := p.AllPostfixSuffix()
	vu := u
	if p.MATCH().Ok() {
		vu = valueUse(Compared, "matched")
	}
	res := w.chain(p, sufs, vu)
	if p.MATCH().Ok() {
		for _, c := range p.AllCaseClause() {
			w.caseClause(c, u, res.typ)
		}
		return chainResult{}
	}
	return res
}

// chainState is the value a chain of suffixes has reached.
type chainState struct {
	typ transpiler.Type
	// interior is the pointer binding whose pointee the value lies in: the
	// binding itself before any suffix, a field path of it after one.
	interior *binding
	whole    bool // the value is interior itself, not a field path
	// qual is the key prefix of the package a qualifier names.
	qual string
	// callee is what a call suffix next would call; recvTyp, the type of its
	// receiver when it is a method.
	callee  *callee
	recvTyp transpiler.Type
	// fromGo: the value came from Go.
	fromGo bool
}

func (w *walker) chain(p native.PostfixExprNode, sufs []native.PostfixSuffixNode, u use) chainResult {
	prim := p.PrimaryExpr()
	var st chainState
	if name, ok := primIdent(prim); ok {
		at := prim.GalaNodeRef
		if fb := w.fbinds[name]; fb != nil {
			w.funcUse(fb, sufs, u)
		}
		switch b, t := w.binds[name], w.typeOf(name); {
		case b != nil:
			w.touch(b, at)
			if len(sufs) == 0 {
				w.bareUse(b, u, at)
				return chainResult{typ: transpiler.PointerType{Elem: b.pointee}}
			}
			if w.overridden(b, at) {
				w.suffixesUnknown(sufs)
				return chainResult{}
			}
			st = chainState{typ: b.pointee, interior: b, whole: true}
		case t != nil:
			st.typ = t
			if ft, ok := st.typ.(transpiler.FuncType); ok {
				// A parameter of a function type: its call has a signature
				// but no summary.
				st.callee = &callee{kind: calleeValue, name: "the function value " + name, params: ft.Params}
				if len(ft.Results) == 1 {
					st.callee.result = ft.Results[0]
				}
			}
		case !w.local[name]:
			if key, ok := w.d.file.quals[name]; ok && len(sufs) > 0 {
				st.qual = key
			} else {
				st.callee = w.a.bareCallee(w, name)
				if st.callee.kind == calleeUnknown {
					// A package-level val or var of the package.
					st.typ = w.a.ownValType(name)
				}
				if len(sufs) == 0 || !isCall(sufs[0]) {
					// A function used as a value: anyone, Go included, may
					// call it.
					w.a.asValue(st.callee)
				}
			}
		}
	} else {
		var vu use
		if len(sufs) == 0 {
			vu = u
		} else {
			vu = valueUse(Other, "an operand")
			// A parenthesised operand with suffixes (`(*p).m()`, `(p.f)[0]`):
			// the chain does not follow a pointer through it.
			w.push(Other, "inside a parenthesised operand")
			defer w.pop()
		}
		w.primaryExpr(prim, vu)
	}
	var last chainResult
	for i := 0; i < len(sufs); i++ {
		s := sufs[i]
		next := i+1 < len(sufs) && isCall(sufs[i+1])
		switch {
		case s.Identifier().Ok():
			w.selectSuffix(&st, s, next)
		case s.ExpressionList().Ok() && st.callee != nil && st.callee.kind != calleeUnknown && st.interior == nil && next:
			// Explicit type arguments of the callee: `EmptyArray[T]()`.
			for _, x := range s.ExpressionList().AllExpression() {
				w.expr(x.GalaNodeRef, valueUse(Other, "a type argument"))
			}
		case s.ExpressionList().Ok():
			if st.interior != nil && st.whole {
				if !w.overridden(st.interior, s.GalaNodeRef) {
					w.reason(st.interior, Other, "indexed", s.GalaNodeRef)
				}
				st.interior = nil
			}
			for _, x := range s.ExpressionList().AllExpression() {
				w.expr(x.GalaNodeRef, valueUse(Other, "an index"))
			}
			st.typ, st.whole, st.callee, st.qual = elemType(st.typ), false, nil, ""
		default:
			last = w.callSuffix(&st, s)
		}
	}
	if b := st.interior; b != nil && !st.whole && !w.overridden(b, p.GalaNodeRef) {
		switch {
		case u.kind == useAddr:
			w.reason(b, Other, "address of its field taken", p.GalaNodeRef)
		case w.leaksReference(st.typ, u):
			// A value read from the pointee that holds a reference into it
			// (a slice, a pointer) leaves the function's sight: it may
			// reach Go, now or through whoever receives it.
			what := "a reference inside it"
			if u.kind == useArg {
				what += ", passed to " + u.call.callee.name
			}
			w.reason(b, PassedToGo, what, p.GalaNodeRef)
		}
	}
	// A bare name's type is the result too in phase 3, so a match on it
	// types its patterns.
	if len(sufs) == 0 && w.p3 || len(sufs) > 0 && !isCall(sufs[len(sufs)-1]) {
		return chainResult{typ: st.typ, fromGo: st.fromGo}
	}
	last.fromGo = last.fromGo || st.fromGo
	return last
}

// leaksReference reports whether a value of type t, read from a pointee,
// escapes through use u while holding a reference into the pointee: t is
// not owned-shaped, and u passes the value on (an argument, a result, a
// binding, a field, a print) rather than reading it (an operand, a
// comparison, a condition).
func (w *walker) leaksReference(t transpiler.Type, u use) bool {
	switch u.kind {
	case useArg, useReturn:
	case useValue:
		if u.reason == Other || u.reason == Compared {
			return false
		}
	default:
		return false
	}
	return w.a.shape(t) != ""
}

// elemType is the element type of a slice, array or map; nil otherwise.
func elemType(t transpiler.Type) transpiler.Type {
	switch tt := t.(type) {
	case transpiler.ArrayType:
		return tt.Elem
	case transpiler.MapType:
		return tt.Elem
	}
	return nil
}

// selectSuffix applies `.name` to the chain. call is whether a call follows.
func (w *walker) selectSuffix(st *chainState, s native.PostfixSuffixNode, call bool) {
	name := s.Identifier().GetText()
	at := s.GalaNodeRef
	if st.qual != "" {
		st.callee = w.a.qualifiedCallee(st.qual, name)
		st.typ, st.qual, st.interior = nil, "", nil
		if st.callee.kind == calleeUnknown {
			st.typ = w.a.packageValType(st.callee.name)
		}
		return
	}
	if ft := w.a.fieldType(st.typ, name); ft != nil {
		// A field: the value is a copy of it, or, with a tracked root, a
		// field path into the pointee.
		st.typ, st.callee, st.whole = ft, nil, false
		return
	}
	if ft := w.a.goFieldType(st.typ, name); ft != nil {
		// A field of a Go value: whatever it holds came from Go.
		st.typ, st.callee, st.whole, st.fromGo = ft, nil, false, true
		return
	}
	c := w.a.methodCallee(st.typ, name)
	b := st.interior
	switch {
	case c != nil && call:
		st.callee, st.recvTyp, st.typ = c, st.typ, nil
		return
	case c != nil:
		// A method value: it captures its receiver when that is a pointer,
		// and anyone, Go included, may call it.
		if b != nil && (st.whole || c.recvPtr) && !w.overridden(b, at) {
			w.reason(b, Captured, "the method value ."+name, at)
		}
		w.a.asValue(c)
		st.callee, st.typ, st.interior = nil, nil, nil
		return
	}
	// Unresolved: it might be a pointer-receiver method of the pointee or of
	// a value inside it.
	if b != nil && !valueReceiver(st.typ) && !w.overridden(b, at) {
		if call {
			w.reason(b, UnknownCallee, "the method ."+name+" did not resolve", at)
		} else {
			w.reason(b, Other, "the member ."+name+" did not resolve", at)
		}
	}
	st.callee = &callee{kind: calleeUnknown, name: "." + name}
	st.typ, st.interior = nil, nil
}

// valueReceiver reports whether every method of a value of type t takes a
// copy: a basic type, a Go slice or map (GALA's .Size() and friends).
func valueReceiver(t transpiler.Type) bool {
	switch tt := t.(type) {
	case transpiler.BasicType:
		return valueBasics[tt.Name]
	case transpiler.ArrayType, transpiler.MapType:
		return true
	}
	return false
}

// callSuffix applies a call `(args)` to the chain.
func (w *walker) callSuffix(st *chainState, s native.PostfixSuffixNode) chainResult {
	c := st.callee
	if c == nil {
		c = &callee{kind: calleeUnknown, name: "a function value"}
	}
	cs := &callState{callee: c, whole: map[*binding]bool{}}
	if b := st.interior; b != nil && !w.overridden(b, s.GalaNodeRef) {
		switch {
		case c.method && c.recvPtr:
			// A pointer receiver is lent: the pointer itself, or one into the
			// pointee for a method of a field. The callee must borrow it, and
			// no other argument may reach the pointee.
			cs.whole[b] = true
			what := c.name + " receiver"
			if !st.whole {
				what += ", on a field"
			}
			switch sum := c.summary(); {
			case c.kind == calleeGo:
				w.reason(b, PassedToGo, what, s.GalaNodeRef)
			case sum == nil:
				w.reason(b, UnknownCallee, what+" has no summary", s.GalaNodeRef)
			case !c.receiverBorrowed(w.p3):
				w.reason(b, EscapingCallee, what, s.GalaNodeRef)
			}
		case c.method && c.kind == calleeGo && w.a.shape(st.recvTyp) != "":
			// A Go method of a value in the pointee: its copy still holds
			// the references the value does.
			w.reason(b, PassedToGo, c.name+" receiver, a field holding a reference", s.GalaNodeRef)
		case c.method:
			// A value receiver: the call reads a copy.
		case st.whole:
			w.reason(b, Other, "called", s.GalaNodeRef)
		}
	}
	w.args(cs, s.ArgumentList())
	fromGo := st.fromGo || c.kind == calleeGo
	st.typ, st.interior, st.whole, st.callee, st.qual, st.recvTyp, st.fromGo = c.result, nil, false, nil, "", nil, fromGo
	return chainResult{typ: c.result, callee: c, fromGo: fromGo}
}

// args walks the arguments of call cs.
func (w *walker) args(cs *callState, l native.ArgumentListNode) {
	c := cs.callee
	for i, a := range l.AllArgument() {
		idx := i
		if a.Identifier().Ok() {
			idx = c.paramIndex(a.Identifier().GetText())
		}
		borrowed := w.p3 && c.paramNonEscaping(idx)
		if lam := a.LambdaExpression(); lam.Ok() {
			// The parameter's function type types the lambda's parameters,
			// for a GALA callee: the only kind whose type parameters
			// (c.tparams) are known.
			var ft transpiler.Type
			if c.kind == calleeGala {
				ft = c.paramType(idx)
			}
			if borrowed {
				w.borrowedClosure(cs, func() { w.lambdaBody(lam, ft, c.tparams) })
			} else {
				w.lambda(lam, ft, c.tparams)
			}
			continue
		}
		pat := a.Pattern()
		ep, ok := pat.AsExpressionPattern()
		if !ok {
			if rp, ok := pat.AsRestPattern(); ok {
				w.expr(rp.Expression().GalaNodeRef, valueUse(Stored, "spread into a variadic argument"))
			} else {
				w.push(Other, "a typed pattern argument")
				w.generic(pat.GalaNodeRef)
				w.pop()
			}
			continue
		}
		e := ep.Expression().GalaNodeRef
		switch {
		case c.kind == calleeUnknown:
			// The argument may be a by-name thunk the callee keeps.
			w.push(UnknownCallee, "an argument of "+c.name)
			w.expr(e, valueUse(UnknownCallee, c.name))
			w.pop()
		case c.kind == calleeGo && !c.noThunks && c.paramType(idx) == nil:
			// A Go callee whose signature is unknown: anything in the
			// argument may reach Go, a thunk included.
			w.push(PassedToGo, "an argument of "+c.name)
			w.expr(e, valueUse(PassedToGo, c.name))
			w.pop()
		case borrowed && w.a.byName(c.paramType(idx)):
			// A by-name argument of a non-escaping parameter: a thunk the
			// callee only calls during the call. Its value is the thunk's
			// result, which the callee receives.
			w.borrowedClosure(cs, func() { w.expr(e, closureResult) })
		case !c.noThunks && w.a.byName(c.paramType(idx)):
			w.push(Captured, "a by-name argument of "+c.name)
			w.expr(e, valueUse(Captured, c.name))
			w.pop()
		default:
			w.expr(e, use{kind: useArg, call: cs, param: idx})
		}
	}
	// Exclusivity: a pointer a borrowed closure uses may not also be lent
	// to the same call (as an argument, or as its receiver).
	for b, at := range cs.captured {
		if cs.whole[b] {
			w.reason(b, PassedTwice, "lent to "+c.name+" and used by a closure passed to it", at)
		}
	}
}

// byName reports whether an argument for a parameter of type t is a
// by-name thunk: t is a function type without parameters (or an alias of
// one). An unknown type might be one.
func (a *pkg) byName(t transpiler.Type) bool {
	switch tt := a.unalias(t).(type) {
	case nil:
		return true
	case transpiler.FuncType:
		return len(tt.Params) == 0
	}
	return false
}

// unalias is t with its type aliases followed to what they name.
func (a *pkg) unalias(t transpiler.Type) transpiler.Type {
	for {
		tt, ok := t.(transpiler.NamedType)
		if !ok {
			return t
		}
		alias, ok := a.rich.TypeAliases[a.typeKey(tt)]
		if !ok {
			if alias, ok = a.rich.TypeAliases[tt.Name]; !ok {
				return t
			}
		}
		t = alias
	}
}

// closureResult is the use of a lambda's or partial function's result: it
// leaves the body, so a pointer declared there escapes through it.
var closureResult = valueUse(Returned, "returned from a closure")

// lambda walks a lambda: every use inside of a pointer binding declared
// outside it is a capture.
func (w *walker) lambda(l native.LambdaExpressionNode, ft transpiler.Type, tparams []string) {
	w.closure("a lambda", true, func() { w.lambdaBody(l, ft, tparams) })
}

// lambdaBody walks a lambda's parameter defaults and body; its result is a
// closureResult. ft is the function type of the parameter it is passed to
// (nil when it is not an argument, or unknown), which gives its untyped
// parameters their types unless they name one of tparams.
func (w *walker) lambdaBody(l native.LambdaExpressionNode, ft transpiler.Type, tparams []string) {
	params := l.Parameters().ParameterList().AllParameter()
	for _, p := range params {
		if def := p.ParamDefault(); def.Ok() {
			w.expr(def.Expression().GalaNodeRef, use{kind: useDiscard})
		}
	}
	body := func() {
		if l.Block().Ok() {
			w.block(l.Block(), closureResult)
		} else if l.Expression().Ok() {
			w.expr(l.Expression().GalaNodeRef, closureResult)
		}
	}
	if !w.p3 {
		// Only phase 3 reads scoped types (typeOf).
		body()
		return
	}
	fn, _ := ft.(transpiler.FuncType)
	ts := map[string]transpiler.Type{}
	for i, p := range params {
		if !p.Identifier().Ok() {
			continue
		}
		var t transpiler.Type
		if !p.Type_().Ok() && i < len(fn.Params) && !mentionsAny(fn.Params[i], tparams) {
			t = fn.Params[i]
		}
		ts[p.Identifier().GetText()] = t
	}
	w.withScoped(ts, body)
}

// mentionsAny reports whether t names any of the type parameters names, or
// is unknown: substituting a marker for them, as instantiation would,
// changes it.
func mentionsAny(t transpiler.Type, names []string) bool {
	if t == nil || t.IsNil() || transpiler.IsUnusable(t) {
		return true
	}
	if len(names) == 0 {
		return false
	}
	marker := transpiler.NamedType{Name: "\x00"}
	sub := make(map[string]transpiler.Type, len(names))
	for _, n := range names {
		sub[n] = marker
	}
	return strings.Contains(transpiler.SubstituteTypeParams(t, sub).String(), "\x00")
}

// closure walks a closure's body: every use of a pointer binding there is a
// capture. With scope (a lambda or a partial function, whose results are
// walked as closureResult), a binding the body itself declares is exempt.
func (w *walker) closure(what string, scope bool, body func()) {
	if scope {
		w.pushScope(what)
	} else {
		w.push(Captured, what)
	}
	w.closures++
	body()
	w.closures--
	w.pop()
}

// caseClause walks a case of a match on a value of type subj (nil when
// unknown): its pattern variables have the types subj's extractors give
// them, in its guard and body.
func (w *walker) caseClause(c native.CaseClauseNode, u use, subj transpiler.Type) {
	w.push(Compared, "in a pattern")
	w.generic(c.Pattern().GalaNodeRef)
	w.pop()
	ts := map[string]transpiler.Type{}
	if w.p3 {
		// Only phase 3 reads scoped types (typeOf).
		w.patternTypes(c.Pattern(), subj, ts)
	}
	w.withScoped(ts, func() {
		if g := c.GetGuard(); g.Ok() {
			w.expr(g.GalaNodeRef, valueUse(Other, "a guard"))
		}
		if b := c.GetBodyBlock(); b.Ok() {
			w.block(b, u)
		} else if s := c.GetBodyStmt(); s.Ok() {
			w.simple(s, u)
		}
	})
}

// patternTypes adds to ts the names pattern p binds, matched against a value
// of type t: a variable gets t, an extractor's arguments the types of the
// fields it extracts, nil where unknown. Every name the pattern holds gets
// an entry, so it shadows an outer one.
func (w *walker) patternTypes(p native.PatternNode, t transpiler.Type, ts map[string]transpiler.Type) {
	ep, ok := p.AsExpressionPattern()
	if !ok {
		unknownNames(p.GalaNodeRef, ts)
		return
	}
	e := unwrapExpr(ep.Expression().GalaNodeRef)
	if e.Kind() != native.GalaKIND_postfixExpr {
		unknownNames(p.GalaNodeRef, ts)
		return
	}
	px := native.PostfixExprNode{GalaNodeRef: e}
	name, isIdent := primIdent(px.PrimaryExpr())
	sufs := px.AllPostfixSuffix()
	var extractor string
	var call native.PostfixSuffixNode
	switch {
	case isIdent && len(sufs) == 0 && !px.MATCH().Ok():
		ts[name] = t
		return
	case isIdent && len(sufs) == 1 && isCall(sufs[0]):
		extractor, call = name, sufs[0]
	case isIdent && len(sufs) == 2 && sufs[0].Identifier().Ok() && isCall(sufs[1]):
		extractor, call = sufs[0].Identifier().GetText(), sufs[1]
	}
	if extractor == "" {
		// A literal, a tuple, a stable identifier path.
		unknownNames(p.GalaNodeRef, ts)
		return
	}
	args := call.ArgumentList().AllArgument()
	fields := w.a.extractorFields(extractor, t, len(args))
	for i, a := range args {
		var ft transpiler.Type
		if i < len(fields) {
			ft = fields[i]
		}
		if a.Pattern().Ok() {
			w.patternTypes(a.Pattern(), ft, ts)
		} else {
			unknownNames(a.GalaNodeRef, ts)
		}
	}
}

// unknownNames adds every name under n to ts with an unknown type.
func unknownNames(n native.GalaNodeRef, ts map[string]transpiler.Type) {
	n.Walk(func(c native.GalaNodeRef) bool {
		if c.Kind() == native.GalaKIND_primary {
			if id := (native.PrimaryNode{GalaNodeRef: c}).Identifier(); id.Ok() {
				ts[id.GetText()] = nil
			}
		}
		return true
	})
}

// extractorFields is the types of the n values extractor name takes out of
// a value of type t: a case of t's sealed type, or a companion (Some, Right,
// …) of t's generic type; nil when unknown.
func (a *pkg) extractorFields(name string, t transpiler.Type, n int) []transpiler.Type {
	if t == nil {
		return nil
	}
	if co := a.rich.CompanionObjects[name]; co != nil && len(co.ExtractIndices) == n {
		if g, ok := t.(transpiler.GenericType); ok {
			if base := g.Base.String(); base == co.TargetType || strings.HasSuffix(base, "."+co.TargetType) {
				out := make([]transpiler.Type, n)
				for i, idx := range co.ExtractIndices {
					if idx < 0 || idx >= len(g.Params) {
						return nil
					}
					out[i] = g.Params[idx]
				}
				return out
			}
		}
	}
	tm, sub := a.typeMeta(t)
	if tm == nil || !tm.IsSealed || len(tm.TypeParams) > 0 && sub == nil {
		// Not a sealed type, or a generic one whose type arguments are not
		// known: its fields' types would name its type parameters.
		return nil
	}
	for _, v := range tm.SealedVariants {
		if v.Name == name && len(v.FieldTypes) == n {
			out := make([]transpiler.Type, n)
			for i, ft := range v.FieldTypes {
				out[i] = unwrapImmutable(substitute(ft, sub))
			}
			return out
		}
	}
	return nil
}

// primaryExpr walks a primary expression that is not an identifier.
func (w *walker) primaryExpr(p native.PrimaryExprNode, u use) {
	switch {
	case p.LambdaExpression().Ok():
		w.lambda(p.LambdaExpression(), nil, nil)
	case p.PartialFunctionLiteral().Ok():
		w.closure("a partial function", true, func() {
			for _, c := range p.PartialFunctionLiteral().AllCaseClause() {
				w.caseClause(c, closureResult, nil)
			}
		})
	case p.IfExpression().Ok():
		ie := p.IfExpression()
		w.expr(ie.Expression().GalaNodeRef, valueUse(Other, "a condition"))
		for _, br := range ie.AllIfExprBranch() {
			if br.Block().Ok() {
				w.block(br.Block(), u)
			} else {
				w.expr(br.Expression().GalaNodeRef, u)
			}
		}
	case p.Primary().Ok():
		w.primary(p.Primary(), u)
	default:
		w.generic(p.GalaNodeRef)
	}
}

func (w *walker) primary(p native.PrimaryNode, u use) {
	switch {
	case p.Literal().Ok():
		w.literal(p.Literal())
	case p.CompositeLiteral().Ok():
		if w.isGoType(p.CompositeLiteral().Type_()) {
			// A field of a Go struct: Go code holds whatever is put there.
			w.push(PassedToGo, "a field of the Go struct "+p.CompositeLiteral().Type_().GetText())
			defer w.pop()
		}
		// A struct literal's keys are field names; a map literal's are values.
		isMap := !p.CompositeLiteral().Type_().QualifiedIdentifier().Ok()
		for _, el := range p.CompositeLiteral().ElementList().AllKeyedElement() {
			xs := el.AllExpression()
			if len(xs) == 2 {
				if _, ok := bareName(xs[0].GalaNodeRef); !ok || isMap {
					w.expr(xs[0].GalaNodeRef, valueUse(Stored, "a composite literal key"))
				}
				xs = xs[1:]
			}
			for _, x := range xs {
				w.expr(x.GalaNodeRef, valueUse(Stored, "a composite literal"))
			}
		}
	case p.TupleExpressionList().Ok():
		xs := p.TupleExpressionList().AllExpression()
		if len(xs) == 1 && p.TupleExpressionList().ChildCount() == 1 {
			w.expr(xs[0].GalaNodeRef, u) // parentheses
			return
		}
		for _, x := range xs {
			w.expr(x.GalaNodeRef, valueUse(Stored, "a tuple"))
		}
	default:
		w.generic(p.GalaNodeRef)
	}
}

var wordRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// literal walks the embedded expressions of an interpolated or format
// string; their values are printed.
func (w *walker) literal(l native.LiteralNode) {
	var formatSpecs bool
	switch {
	case l.INTERPOLATED_STRING().Ok():
	case l.FORMAT_STRING().Ok():
		formatSpecs = true
	default:
		return
	}
	text := l.GetText()
	content := text[2 : len(text)-1]
	tok := l.GalaNodeRef.Start()
	for _, part := range interpolation.Split(content, formatSpecs) {
		if part.IsLiteral {
			continue
		}
		line, col := interpolation.Position(content, part.Offset, int(tok.Line), int(tok.Column)+2)
		expr, err := parser.ParseExpressionCheckedAt(part.Text, line, col, "interpolated expression")
		if err != nil {
			// Not parsed: any binding it names escapes.
			for _, word := range wordRe.FindAllString(part.Text, -1) {
				if b := w.binds[word]; b != nil {
					w.reason(b, Other, "in an interpolation that did not parse", l.GalaNodeRef)
				}
			}
			continue
		}
		w.expr(expr.GalaNodeRef, valueUse(PassedToGo, "printed"))
	}
}

// generic walks the children of a node the walker does not model in
// detail: every expression in it is walked as an operand, and every
// statement as one.
func (w *walker) generic(n native.GalaNodeRef) {
	for i := range n.ChildCount() {
		c := n.Child(i)
		if c.IsToken() {
			continue
		}
		k := c.Node()
		switch k.Kind() {
		case native.GalaKIND_expression, native.GalaKIND_orExpr, native.GalaKIND_andExpr, native.GalaKIND_equalityExpr,
			native.GalaKIND_relationalExpr, native.GalaKIND_additiveExpr, native.GalaKIND_multiplicativeExpr,
			native.GalaKIND_unaryExpr, native.GalaKIND_postfixExpr:
			w.expr(k, valueUse(Other, "in "+kindName(n)))
		case native.GalaKIND_block:
			w.block(native.BlockNode{GalaNodeRef: k}, use{kind: useDiscard})
		case native.GalaKIND_statement:
			w.statement(native.StatementNode{GalaNodeRef: k}, use{kind: useDiscard})
		case native.GalaKIND_lambdaExpression:
			w.lambda(native.LambdaExpressionNode{GalaNodeRef: k}, nil, nil)
		case native.GalaKIND_literal:
			w.literal(native.LiteralNode{GalaNodeRef: k})
		default:
			w.generic(k)
		}
	}
}

func kindName(n native.GalaNodeRef) string {
	return native.GalaRuleNames[n.Rule()]
}

// suffixesUnknown walks the expressions inside suffixes whose chain the
// walker does not follow.
func (w *walker) suffixesUnknown(sufs []native.PostfixSuffixNode) {
	for _, s := range sufs {
		w.generic(s.GalaNodeRef)
	}
}
