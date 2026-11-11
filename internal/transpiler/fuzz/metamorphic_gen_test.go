package fuzz_test

import (
	"fmt"
	"hash/fnv"
	"maps"
	"math/rand"
	"slices"
	"strings"
)

// metamorphic_gen_test.go generates the programs of the two metamorphic
// targets, FuzzAlphaRenaming and FuzzEvaluationOrder (metamorphic_test.go).
//
// A program is built as a small tree, not as text: every local binder — a
// val, a tuple-destructured val, a function or lambda parameter, a pattern
// variable — is a *mBinder, and every read of it points at that binder. The
// generator runs the tree through its own model of GALA (eval) to get the
// output the program must print, and renders it as GALA source with the
// binders' names filled in afterwards. Rendering the same tree under two
// naming schemes gives the alpha-renaming property: one tree, plain names
// (v1, v2, ...) or hostile ones, the same expected output.
//
// Renaming is done here, on the generator's tree, and not on a parsed
// program, because the tree knows every binder and every reference to it.
// A rename on the parser's tree would first need a full GALA name resolver
// (block, arm, lambda and function scopes, named arguments and sub-pattern
// labels that are not references, string interpolation, imports), and an
// error in that resolver would show up as false failures; here scoping is a
// stack the scan below keeps while it walks the tree.
//
// Hostile names are the names generated Go uses for itself (obj, _tmp_N,
// __p0), Go's predeclared and package names (std, panic, string, len, fmt)
// and the names of other binders in scope: shadowing. A name is only given
// where it keeps every reference resolving to the same binder (see mScan).
//
// For the evaluation-order property, operands, positional and named
// arguments, constructor arguments, match subjects, guards, extractors and
// default arguments call tracing functions that print as they run: tr(id, v)
// and tb(id, b) print their id, and the extractors Even, Pos and Dec print
// their initial and argument. The model evaluates left to right, an
// extractor runs only after the sibling sub-patterns before it matched, a
// match subject runs once and only one if branch runs, so the trace the
// program must print is known.

// mType is the type of a generated expression or binder.
type mType int

const (
	mInt   mType = iota
	mShape       // the sealed type Shape
	mOpt         // Option[int]
	mPair        // Tuple[int, int]
	mFn          // func(int) int: a lambda bound to a val
)

// metaDomain is declared in every program. Its own names are fixed: renaming
// covers the generated code only.
const metaDomain = `sealed type Shape {
    case Circle(R int)
    case Rect(W int, H int)
    case Empty()
}

func area(s Shape) int = s match {
    case Circle(r) => r * 3
    case Rect(w, h) => w * h
    case Empty() => 0
}

func tr(id int, v int) int {
    Print(s"$id ")
    v
}

func tb(id int, b bool) bool {
    Print(s"$id ")
    b
}

struct Even()

func (e Even) Unapply(i int) bool {
    Print(s"E$i ")
    i % 2 == 0
}

struct Pos()

func (p Pos) Unapply(i int) bool {
    Print(s"P$i ")
    i > 0
}

struct Dec()

func (d Dec) Unapply(i int) Option[int] {
    Print(s"D$i ")
    if (i > 0) Some(i - 1) else None[int]()
}
`

// metaCounter is declared when a program reads a mutable package-level var
// and calls a function that changes it.
const metaCounter = `
var counter = 0

func bump(n int) int {
    counter = counter + n
    counter
}
`

// mVal is the model's value of an expression.
type mVal struct {
	n      int // an int, or a bool as 0 and 1
	tag    string
	fields []mVal
	fn     func(*mState, int) int
}

func mBool(b bool) mVal {
	if b {
		return mVal{n: 1}
	}
	return mVal{}
}

// mState is what the model's program prints and the value of counter.
type mState struct {
	out     strings.Builder
	counter int
}

func (s *mState) trace(tok string) { s.out.WriteString(tok + " ") }

// mEnv binds binders to values; it is copied, never changed.
type mEnv map[*mBinder]mVal

func (e mEnv) with(b *mBinder, v mVal) mEnv {
	n := maps.Clone(e)
	if n == nil {
		n = mEnv{}
	}
	n[b] = v
	return n
}

// mBinder is a local binder. Binders declared in one region — a block, an
// arm with its pattern and body, a function or lambda with its parameters —
// are in one scope, so their names must differ.
type mBinder struct {
	id     int
	typ    mType
	region int
	uses   int
	name   string
	// visible: the binders in scope where it is declared (set by the scan),
	// whose names it may take to shadow them.
	visible []*mBinder
}

// mExpr is an expression: ints, bools (conditions), and values of the other
// types.
type mExpr interface {
	eval(s *mState, env mEnv) mVal
	render() string
	scan(c *mScan)
	// ordered: its evaluation prints or reads state that another
	// operand may change, so where it runs relative to its siblings shows.
	ordered() bool
}

// mStmt is a statement of main or of a block.
type mStmt interface {
	exec(s *mState, env mEnv) mEnv
	render() string
	scan(c *mScan)
}

// mPat is a pattern.
type mPat interface {
	// match reports whether v matches, binding through bind and printing
	// what the extractors it runs print.
	match(s *mState, v mVal, bind func(*mBinder, mVal)) bool
	render() string
	refutable() bool
}

// mScan walks a finished tree with the stack of binders in scope, and
// records which pairs of binders must get different names: two binders of
// one region, a binder and any outer binder that is read inside its scope
// (which it would capture), and a val and a binder its initializer reads.
type mScan struct {
	stack    []*mBinder
	refs     []*mBinder // every read, in order
	distinct map[*mBinder]map[*mBinder]bool
	// avoid reports whether the shapes of an open issue are left out, and
	// records that they were (metaGen.avoid). The scan uses it for #996:
	// a pattern variable's name is then kept apart from the outer names
	// the later arms of its match read.
	avoid func(issue int) bool
}

func (c *mScan) differ(a, b *mBinder) {
	if a == b {
		return
	}
	for _, p := range [][2]*mBinder{{a, b}, {b, a}} {
		if c.distinct[p[0]] == nil {
			c.distinct[p[0]] = map[*mBinder]bool{}
		}
		c.distinct[p[0]][p[1]] = true
	}
}

func (c *mScan) declare(b *mBinder) {
	b.visible = append([]*mBinder(nil), c.stack...)
	for _, o := range c.stack {
		if o.region == b.region {
			c.differ(o, b)
		}
	}
	c.stack = append(c.stack, b)
}

func (c *mScan) ref(b *mBinder) {
	i := len(c.stack) - 1
	for i >= 0 && c.stack[i] != b {
		i--
	}
	if i < 0 {
		panic(fmt.Sprintf("generator bug: binder %d read out of its scope", b.id))
	}
	for _, inner := range c.stack[i+1:] {
		c.differ(b, inner)
	}
	c.refs = append(c.refs, b)
}

// declareVal declares b, a val whose initializer's reads are refs[from:].
func (c *mScan) declareVal(b *mBinder, from int) {
	for _, r := range c.refs[from:] {
		c.differ(b, r)
	}
	c.declare(b)
}

// ---- expressions

type eLit struct{ n int }

func (e eLit) eval(*mState, mEnv) mVal { return mVal{n: e.n} }
func (e eLit) render() string          { return fmt.Sprint(e.n) }
func (e eLit) scan(*mScan)             {}
func (e eLit) ordered() bool           { return false }

// eRef reads a binder as a value of its own type.
type eRef struct{ b *mBinder }

func (e eRef) eval(_ *mState, env mEnv) mVal {
	v, ok := env[e.b]
	if !ok {
		panic(fmt.Sprintf("generator bug: binder %d unbound in the model", e.b.id))
	}
	return v
}
func (e eRef) render() string { return e.b.name }
func (e eRef) scan(c *mScan)  { c.ref(e.b) }
func (e eRef) ordered() bool  { return false }

// eRead reads a Shape or Option binder as an int.
type eRead struct{ b *mBinder }

func (e eRead) eval(s *mState, env mEnv) mVal {
	v := eRef(e).eval(s, env)
	if e.b.typ == mShape {
		return mVal{n: metaArea(v)}
	}
	if v.tag == "Some" {
		return v.fields[0]
	}
	return mVal{n: 7}
}

func (e eRead) render() string {
	if e.b.typ == mShape {
		return "area(" + e.b.name + ")"
	}
	return e.b.name + ".GetOrElse(7)"
}
func (e eRead) scan(c *mScan) { c.ref(e.b) }
func (e eRead) ordered() bool { return false }

func metaArea(v mVal) int {
	switch v.tag {
	case "Circle":
		return v.fields[0].n * 3
	case "Rect":
		return v.fields[0].n * v.fields[1].n
	}
	return 0
}

type eBin struct {
	op   string
	l, r mExpr
}

func (e eBin) eval(s *mState, env mEnv) mVal {
	a := e.l.eval(s, env).n
	switch e.op {
	case "&&", "||":
		if (a != 0) == (e.op == "||") {
			return mBool(a != 0)
		}
		return mBool(e.r.eval(s, env).n != 0)
	}
	b := e.r.eval(s, env).n
	switch e.op {
	case "+":
		return mVal{n: a + b}
	case "-":
		return mVal{n: a - b}
	case "*":
		return mVal{n: a * b}
	case "<":
		return mBool(a < b)
	case ">":
		return mBool(a > b)
	case "==":
		return mBool(a == b)
	}
	panic("unknown operator " + e.op)
}
func (e eBin) render() string { return "(" + e.l.render() + " " + e.op + " " + e.r.render() + ")" }
func (e eBin) scan(c *mScan)  { e.l.scan(c); e.r.scan(c) }
func (e eBin) ordered() bool  { return e.l.ordered() || e.r.ordered() }

// eTrace is tr(id, e) or, for a condition, tb(id, e).
type eTrace struct {
	id   int
	cond bool
	e    mExpr
}

func (e eTrace) eval(s *mState, env mEnv) mVal {
	v := e.e.eval(s, env)
	s.trace(fmt.Sprint(e.id))
	return v
}

func (e eTrace) render() string {
	f := "tr"
	if e.cond {
		f = "tb"
	}
	return fmt.Sprintf("%s(%d, %s)", f, e.id, e.e.render())
}
func (e eTrace) scan(c *mScan) { e.e.scan(c) }
func (e eTrace) ordered() bool { return true }

// mFunc is a generated top-level function returning an int. Its last
// parameter, when an int, may have a default: a traced constant.
type mFunc struct {
	name   string
	params []*mBinder
	def    mExpr
	body   mExpr
}

func (f *mFunc) render() string {
	ps := make([]string, len(f.params))
	for i, p := range f.params {
		ps[i] = p.name + " " + metaTypeName(p.typ)
		if i == len(f.params)-1 && f.def != nil {
			ps[i] += " = " + f.def.render()
		}
	}
	return fmt.Sprintf("func %s(%s) int = %s", f.name, strings.Join(ps, ", "), f.body.render())
}

func metaTypeName(t mType) string {
	switch t {
	case mInt:
		return "int"
	case mShape:
		return "Shape"
	case mOpt:
		return "Option[int]"
	}
	return "Tuple[int, int]"
}

// eCall calls a generated function. args[i] is the argument for parameter
// i, nil for a defaulted one; order is the order the arguments are written
// in, by parameter index; named writes them as `p = arg`.
type eCall struct {
	f     *mFunc
	args  []mExpr
	order []int
	named bool
}

func (e eCall) eval(s *mState, env mEnv) mVal {
	vals := make([]mVal, len(e.args))
	for _, i := range e.order {
		vals[i] = e.args[i].eval(s, env)
	}
	last := len(e.args) - 1
	if e.args[last] == nil {
		vals[last] = e.f.def.eval(s, nil)
	}
	callee := mEnv{}
	for i, p := range e.f.params {
		callee[p] = vals[i]
	}
	return e.f.body.eval(s, callee)
}

func (e eCall) render() string {
	var parts []string
	for _, i := range e.order {
		a := e.args[i].render()
		if e.named {
			a = e.f.params[i].name + " = " + a
		}
		parts = append(parts, a)
	}
	return e.f.name + "(" + strings.Join(parts, ", ") + ")"
}

func (e eCall) scan(c *mScan) {
	for _, i := range e.order {
		e.args[i].scan(c)
	}
}
func (e eCall) ordered() bool { return true }

// eCtor builds a Shape variant, Some, None or a tuple (tag ""). With names
// the arguments are written named, in order (indices into names).
type eCtor struct {
	tag   string
	args  []mExpr
	names []string
	order []int
}

func (e eCtor) eval(s *mState, env mEnv) mVal {
	v := mVal{tag: e.tag, fields: make([]mVal, len(e.args))}
	for _, i := range e.order {
		v.fields[i] = e.args[i].eval(s, env)
	}
	return v
}

func (e eCtor) render() string {
	if e.tag == "None" {
		return "None[int]()"
	}
	var parts []string
	for _, i := range e.order {
		a := e.args[i].render()
		if e.names != nil {
			a = e.names[i] + " = " + a
		}
		parts = append(parts, a)
	}
	return e.tag + "(" + strings.Join(parts, ", ") + ")"
}

func (e eCtor) scan(c *mScan) {
	for _, i := range e.order {
		e.args[i].scan(c)
	}
}

func (e eCtor) ordered() bool {
	for _, a := range e.args {
		if a.ordered() {
			return true
		}
	}
	return false
}

// mArm is a case of a match; binders are its pattern's variables.
type mArm struct {
	pat     mPat
	binders []*mBinder
	guard   mExpr
	body    mExpr
}

type eMatch struct {
	subj mExpr
	arms []*mArm
}

func (e eMatch) eval(s *mState, env mEnv) mVal {
	v := e.subj.eval(s, env)
	for _, a := range e.arms {
		armEnv := env
		if !a.pat.match(s, v, func(b *mBinder, x mVal) { armEnv = armEnv.with(b, x) }) {
			continue
		}
		if a.guard != nil && a.guard.eval(s, armEnv).n == 0 {
			continue
		}
		return a.body.eval(s, armEnv)
	}
	panic("generator bug: a match without a default")
}

func (e eMatch) render() string {
	var b strings.Builder
	b.WriteString("(" + e.subj.render() + " match {\n")
	for _, a := range e.arms {
		b.WriteString("case " + a.pat.render())
		if a.guard != nil {
			b.WriteString(" if " + a.guard.render())
		}
		b.WriteString(" => " + a.body.render() + "\n")
	}
	b.WriteString("})")
	return b.String()
}

func (e eMatch) scan(c *mScan) {
	e.subj.scan(c)
	armRefs := make([][]*mBinder, len(e.arms))
	for i, a := range e.arms {
		mark, from := len(c.stack), len(c.refs)
		for _, b := range a.binders {
			c.declare(b)
		}
		if a.guard != nil {
			a.guard.scan(c)
		}
		a.body.scan(c)
		armRefs[i] = c.refs[from:]
		c.stack = c.stack[:mark]
	}
	// #996: an arm's pattern variables are also in scope in the later arms,
	// so they would capture the outer names those read.
	if c.avoid == nil {
		return
	}
	outer := map[*mBinder]bool{}
	for _, b := range c.stack {
		outer[b] = true
	}
	for i, a := range e.arms {
		for _, later := range armRefs[i+1:] {
			for _, x := range later {
				if outer[x] && len(a.binders) > 0 && c.avoid(996) {
					for _, p := range a.binders {
						c.differ(p, x)
					}
				}
			}
		}
	}
}
func (e eMatch) ordered() bool { return true }

type eIf struct{ cond, then, els mExpr }

func (e eIf) eval(s *mState, env mEnv) mVal {
	if e.cond.eval(s, env).n != 0 {
		return e.then.eval(s, env)
	}
	return e.els.eval(s, env)
}

func (e eIf) render() string {
	return "(if (" + e.cond.render() + ") " + e.then.render() + " else " + e.els.render() + ")"
}
func (e eIf) scan(c *mScan) { e.cond.scan(c); e.then.scan(c); e.els.scan(c) }
func (e eIf) ordered() bool { return true }

// eBlock is `{ val ...; result }`, an if branch, an arm's or a lambda's
// body.
type eBlock struct {
	vals   []*sVal
	result mExpr
}

func (e eBlock) eval(s *mState, env mEnv) mVal {
	for _, v := range e.vals {
		env = v.exec(s, env)
	}
	return e.result.eval(s, env)
}

func (e eBlock) render() string {
	var b strings.Builder
	b.WriteString("{\n")
	for _, v := range e.vals {
		b.WriteString(v.render() + "\n")
	}
	b.WriteString(e.result.render() + "\n}")
	return b.String()
}

func (e eBlock) scan(c *mScan) {
	mark := len(c.stack)
	for _, v := range e.vals {
		v.scan(c)
	}
	e.result.scan(c)
	c.stack = c.stack[:mark]
}
func (e eBlock) ordered() bool { return true }

// eApply calls a lambda bound to a val.
type eApply struct {
	f   *mBinder
	arg mExpr
}

func (e eApply) eval(s *mState, env mEnv) mVal {
	a := e.arg.eval(s, env).n
	return mVal{n: eRef{e.f}.eval(s, env).fn(s, a)}
}
func (e eApply) render() string { return e.f.name + "(" + e.arg.render() + ")" }
func (e eApply) scan(c *mScan)  { c.ref(e.f); e.arg.scan(c) }
func (e eApply) ordered() bool  { return true }

// eOptMap is opt.Map(lambda).GetOrElse(0): the lambda (param) => body, or
// the placeholder lambda `_ + body` when param is nil.
type eOptMap struct {
	opt   mExpr
	param *mBinder
	body  mExpr
}

func (e eOptMap) eval(s *mState, env mEnv) mVal {
	o := e.opt.eval(s, env)
	if o.tag != "Some" {
		return mVal{}
	}
	if e.param == nil {
		return mVal{n: o.fields[0].n + e.body.eval(s, env).n}
	}
	return e.body.eval(s, env.with(e.param, o.fields[0]))
}

func (e eOptMap) render() string {
	lam := "_ + " + e.body.render()
	if e.param != nil {
		lam = "(" + e.param.name + ") => " + e.body.render()
	}
	return e.opt.render() + ".Map(" + lam + ").GetOrElse(0)"
}

func (e eOptMap) scan(c *mScan) {
	e.opt.scan(c)
	mark := len(c.stack)
	if e.param != nil {
		c.declare(e.param)
	}
	e.body.scan(c)
	c.stack = c.stack[:mark]
}
func (e eOptMap) ordered() bool { return true }

// eCounter reads the package-level var counter; eBump changes it.
type eCounter struct{}

func (eCounter) eval(s *mState, _ mEnv) mVal { return mVal{n: s.counter} }
func (eCounter) render() string              { return "counter" }
func (eCounter) scan(*mScan)                 {}
func (eCounter) ordered() bool               { return true }

type eBump struct{ n int }

func (e eBump) eval(s *mState, _ mEnv) mVal {
	s.counter += e.n
	return mVal{n: s.counter}
}
func (e eBump) render() string { return fmt.Sprintf("bump(%d)", e.n) }
func (e eBump) scan(*mScan)    {}
func (e eBump) ordered() bool  { return true }

// ---- statements

// sVal is `val b = init`.
type sVal struct {
	b    *mBinder
	init mExpr
}

func (v *sVal) exec(s *mState, env mEnv) mEnv { return env.with(v.b, v.init.eval(s, env)) }
func (v *sVal) render() string                { return "val " + v.b.name + " = " + v.init.render() }
func (v *sVal) scan(c *mScan) {
	from := len(c.refs)
	v.init.scan(c)
	c.declareVal(v.b, from)
}

// sTuple is `val (a, b) = (x, y)`.
type sTuple struct {
	a, b *mBinder
	init mExpr
}

func (v *sTuple) exec(s *mState, env mEnv) mEnv {
	t := v.init.eval(s, env)
	return env.with(v.a, t.fields[0]).with(v.b, t.fields[1])
}
func (v *sTuple) render() string {
	return "val (" + v.a.name + ", " + v.b.name + ") = " + v.init.render()
}
func (v *sTuple) scan(c *mScan) {
	from := len(c.refs)
	v.init.scan(c)
	c.declareVal(v.a, from)
	c.declareVal(v.b, from)
}

// sLambda is `val f = (p int) => body`.
type sLambda struct {
	f, p *mBinder
	body mExpr
}

func (v *sLambda) exec(_ *mState, env mEnv) mEnv {
	captured := env
	return env.with(v.f, mVal{fn: func(s *mState, x int) int {
		return v.body.eval(s, captured.with(v.p, mVal{n: x})).n
	}})
}
func (v *sLambda) render() string {
	return "val " + v.f.name + " = (" + v.p.name + " int) int => " + v.body.render()
}
func (v *sLambda) scan(c *mScan) {
	from, mark := len(c.refs), len(c.stack)
	c.declare(v.p)
	v.body.scan(c)
	c.stack = c.stack[:mark]
	c.declareVal(v.f, from)
}

// sPrint is Println(e), or Println(s"${a} ${b}") with two expressions.
type sPrint struct{ es []mExpr }

func (p *sPrint) exec(s *mState, env mEnv) mEnv {
	vals := make([]string, len(p.es))
	for i, e := range p.es {
		vals[i] = fmt.Sprint(e.eval(s, env).n)
	}
	s.out.WriteString(strings.Join(vals, " ") + "\n")
	return env
}

func (p *sPrint) render() string {
	if len(p.es) == 1 {
		return "Println(" + p.es[0].render() + ")"
	}
	parts := make([]string, len(p.es))
	for i, e := range p.es {
		parts[i] = "${" + e.render() + "}"
	}
	return `Println(s"` + strings.Join(parts, " ") + `")`
}

func (p *sPrint) scan(c *mScan) {
	for _, e := range p.es {
		e.scan(c)
	}
}

// ---- patterns

type pWild struct{}

func (pWild) match(*mState, mVal, func(*mBinder, mVal)) bool { return true }
func (pWild) render() string                                 { return "_" }
func (pWild) refutable() bool                                { return false }

type pBind struct{ b *mBinder }

func (p pBind) match(_ *mState, v mVal, bind func(*mBinder, mVal)) bool {
	bind(p.b, v)
	return true
}
func (p pBind) render() string  { return p.b.name }
func (p pBind) refutable() bool { return false }

type pLit struct{ n int }

func (p pLit) match(_ *mState, v mVal, _ func(*mBinder, mVal)) bool { return v.n == p.n }
func (p pLit) render() string                                       { return fmt.Sprint(p.n) }
func (p pLit) refutable() bool                                      { return true }

// pAlt is `a | b`: the alternatives are tried in order until one matches.
type pAlt struct{ alts []mPat }

func (p pAlt) match(s *mState, v mVal, bind func(*mBinder, mVal)) bool {
	for _, a := range p.alts {
		if a.match(s, v, bind) {
			return true
		}
	}
	return false
}

func (p pAlt) render() string {
	parts := make([]string, len(p.alts))
	for i, a := range p.alts {
		parts[i] = a.render()
	}
	return strings.Join(parts, " | ")
}
func (p pAlt) refutable() bool { return true }

// pTest is Even() or Pos(): a boolean extractor that prints.
type pTest struct{ name string }

func (p pTest) match(s *mState, v mVal, _ func(*mBinder, mVal)) bool {
	s.trace(fmt.Sprintf("%c%d", p.name[0], v.n))
	if p.name == "Even" {
		return v.n%2 == 0
	}
	return v.n > 0
}
func (p pTest) render() string  { return p.name + "()" }
func (p pTest) refutable() bool { return true }

// pDec is Dec(sub): it prints, and matches a positive int, passing it less
// one to sub.
type pDec struct{ sub mPat }

func (p pDec) match(s *mState, v mVal, bind func(*mBinder, mVal)) bool {
	s.trace(fmt.Sprintf("D%d", v.n))
	return v.n > 0 && p.sub.match(s, mVal{n: v.n - 1}, bind)
}
func (p pDec) render() string  { return "Dec(" + p.sub.render() + ")" }
func (p pDec) refutable() bool { return true }

// pCtor is a variant's (or a tuple's, tag "") pattern: subs[i] matches field
// fields[i]; with labels it is written with named sub-patterns.
type pCtor struct {
	tag    string
	fields []int
	subs   []mPat
	labels []string
}

func (p pCtor) match(s *mState, v mVal, bind func(*mBinder, mVal)) bool {
	if p.tag != "" && v.tag != p.tag {
		return false
	}
	for i, sub := range p.subs {
		if !sub.match(s, v.fields[p.fields[i]], bind) {
			return false
		}
	}
	return true
}

func (p pCtor) render() string {
	parts := make([]string, len(p.subs))
	for i, sub := range p.subs {
		parts[i] = sub.render()
		if p.labels != nil {
			parts[i] = p.labels[p.fields[i]] + " = " + parts[i]
		}
	}
	return p.tag + "(" + strings.Join(parts, ", ") + ")"
}

func (p pCtor) refutable() bool {
	if p.tag != "" {
		return true
	}
	for _, sub := range p.subs {
		if sub.refutable() {
			return true
		}
	}
	return false
}

// ---- the generator

// metaConfig selects what a generated program exercises.
type metaConfig struct {
	// hostile renames the binders to hostile names.
	hostile bool
	// traceOdds: one expression in traceOdds is wrapped in a tracing call.
	traceOdds int
}

// metaProgram is a generated program: its source with the binders named
// as configured, the same program with plain names, and its output.
type metaProgram struct {
	src, plain string
	want       string
	// steered: the open issues whose shapes the generator avoided.
	steered []int
}

type metaGen struct {
	data    []byte
	pos     int
	cfg     metaConfig
	binders []*mBinder
	regions int
	visible []*mBinder
	funcs   []*mFunc
	traces  int
	counter bool
	steered map[int]bool
}

func (g *metaGen) pick(n int) int {
	if g.pos >= len(g.data) {
		return 0
	}
	c := int(g.data[g.pos]) % n
	g.pos++
	return c
}

// avoid reports whether the shapes of issue are left out because it is
// open (see knownFailures), and records that they were.
func (g *metaGen) avoid(issue int) bool {
	if !openIssue(issue) {
		return false
	}
	g.steered[issue] = true
	return true
}

func (g *metaGen) region() int {
	g.regions++
	return g.regions
}

func (g *metaGen) binder(t mType, region int) *mBinder {
	b := &mBinder{id: len(g.binders) + 1, typ: t, region: region}
	g.binders = append(g.binders, b)
	return b
}

func (g *metaGen) traceID() int {
	g.traces++
	return g.traces
}

// visibleOf lists the binders in scope that can be read as a value of type
// t: ints, and Shapes and Options through area and GetOrElse when t is int.
func (g *metaGen) visibleOf(t mType) []*mBinder {
	var out []*mBinder
	for _, b := range g.visible {
		if b.typ == t || t == mInt && (b.typ == mShape || b.typ == mOpt) {
			out = append(out, b)
		}
	}
	return out
}

// read is an expression of type t that reads b.
func (g *metaGen) read(b *mBinder, t mType) mExpr {
	b.uses++
	switch {
	case b.typ == mFn:
		return eApply{b, eLit{g.pick(5)}}
	case t == mInt && b.typ != mInt:
		return eRead{b}
	}
	return eRef{b}
}

// useAll makes every binder in bs read at least once, adding the reads to
// *result: GALA rejects an unused pattern variable and Go an unused local.
func (g *metaGen) useAll(bs []*mBinder, result *mExpr) {
	if blk, ok := (*result).(eBlock); ok {
		// A block is not an operand: the reads go into its result.
		g.useAll(bs, &blk.result)
		*result = blk
		return
	}
	for _, b := range bs {
		if b.uses == 0 {
			*result = eBin{"+", *result, g.read(b, mInt)}
		}
	}
}

func (g *metaGen) maybeTrace(e mExpr, cond bool) mExpr {
	if g.pick(g.cfg.traceOdds) == 0 {
		return eTrace{g.traceID(), cond, e}
	}
	return e
}

// intExpr is an int expression of nesting depth d. A flat one stays on one
// line (no match, if or block), for string interpolation.
func (g *metaGen) intExpr(d int, flat bool) mExpr {
	return g.maybeTrace(g.intExpr0(d, flat), false)
}

func (g *metaGen) intExpr0(d int, flat bool) mExpr {
	if d <= 0 {
		return g.atom()
	}
	n := 10
	if flat {
		n = 5
	}
	switch g.pick(n) {
	case 1, 2:
		return eBin{string("+-*"[g.pick(3)]), g.intExpr(d-1, flat), g.intExpr(d-1, flat)}
	case 3:
		if len(g.funcs) > 0 {
			return g.call(d, flat)
		}
	case 4:
		if g.avoid(1001) {
			break
		}
		g.counter = true
		if g.pick(2) == 0 {
			return eCounter{}
		}
		return eBump{1 + g.pick(4)}
	case 5, 6:
		return g.match(d)
	case 7:
		return eIf{g.cond(d - 1), g.body(d-1, g.region(), 3), g.body(d-1, g.region(), 3)}
	case 8:
		if fs := g.visibleOf(mFn); len(fs) > 0 {
			f := fs[g.pick(len(fs))]
			f.uses++
			return eApply{f, g.intExpr(d-1, false)}
		}
		return g.optMap(d)
	case 9:
		return g.optMap(d)
	}
	return g.atom()
}

func (g *metaGen) atom() mExpr {
	if bs := g.visibleOf(mInt); len(bs) > 0 && g.pick(3) != 0 {
		return g.read(bs[g.pick(len(bs))], mInt)
	}
	return eLit{g.pick(6)}
}

// expr is an expression of type t.
func (g *metaGen) expr(t mType, d int, flat bool) mExpr {
	if t == mInt {
		return g.intExpr(d, flat)
	}
	if bs := g.visibleOf(t); len(bs) > 0 && g.pick(4) == 0 {
		return g.read(bs[g.pick(len(bs))], t)
	}
	switch t {
	case mShape:
		switch g.pick(3) {
		case 0:
			return g.ctor("Circle", []string{"R"}, d, flat)
		case 1:
			return g.ctor("Rect", []string{"W", "H"}, d, flat)
		}
		return eCtor{tag: "Empty"}
	case mOpt:
		if g.pick(3) == 0 {
			return eCtor{tag: "None"}
		}
		return eCtor{tag: "Some", args: []mExpr{g.intExpr(d-1, flat)}, order: []int{0}}
	}
	return eCtor{args: []mExpr{g.intExpr(d-1, flat), g.intExpr(d-1, flat)}, order: []int{0, 1}}
}

// ctor builds a Shape variant, positionally or with named arguments in
// some order.
func (g *metaGen) ctor(tag string, fields []string, d int, flat bool) mExpr {
	e := eCtor{tag: tag}
	for range fields {
		e.args = append(e.args, g.intExpr(d-1, flat))
	}
	e.order = g.argOrder(e.args)
	if g.pick(2) == 0 || !slices.IsSorted(e.order) {
		e.names = fields
	}
	return e
}

// argOrder is the order arguments are written in: their own, or shuffled.
// A shuffle that would run two ordered arguments out of parameter order
// is not made while #1000 is open.
func (g *metaGen) argOrder(args []mExpr) []int {
	order := make([]int, len(args))
	for i := range order {
		order[i] = i
	}
	if g.pick(2) == 0 {
		return order
	}
	for i := len(order) - 1; i > 0; i-- {
		j := g.pick(i + 1)
		order[i], order[j] = order[j], order[i]
	}
	ordered := 0
	for _, a := range args {
		if a.ordered() {
			ordered++
		}
	}
	if ordered > 1 && !slices.IsSorted(order) && g.avoid(1000) {
		slices.Sort(order)
	}
	return order
}

// call calls a generated function: positionally or with named arguments
// in some order, leaving out a defaulted last argument or not.
func (g *metaGen) call(d int, flat bool) mExpr {
	f := g.funcs[g.pick(len(g.funcs))]
	e := eCall{f: f, args: make([]mExpr, len(f.params))}
	// The defaulted argument is left out before the others are generated:
	// a read in an argument that is then dropped would count as a use.
	given := e.args
	if f.def != nil && g.pick(2) == 0 {
		given = e.args[:len(e.args)-1]
	}
	for i := range given {
		given[i] = g.expr(f.params[i].typ, d-1, flat)
	}
	e.order = g.argOrder(given)
	e.named = g.pick(2) == 0 || !slices.IsSorted(e.order)
	return e
}

func (g *metaGen) cond(d int) mExpr {
	var c mExpr
	switch g.pick(5) {
	case 0:
		if d > 0 {
			c = eBin{"&&", g.cond(d - 1), g.cond(d - 1)}
		}
	case 1:
		if d > 0 {
			c = eBin{"||", g.cond(d - 1), g.cond(d - 1)}
		}
	}
	if c == nil {
		c = eBin{[]string{"<", ">", "=="}[g.pick(3)], g.intExpr(d, false), g.intExpr(d, false)}
	}
	return g.maybeTrace(c, true)
}

// body is an int expression or, one time in odds, a block whose vals are
// in region: an if branch, an arm's or a lambda's body.
func (g *metaGen) body(d, region, odds int) mExpr {
	if g.pick(odds) == 0 {
		return g.block(d, region)
	}
	return g.intExpr(d, false)
}

// block is `{ val ...; result }` whose vals are in region.
func (g *metaGen) block(d int, region int) mExpr {
	mark := len(g.visible)
	var blk eBlock
	for range 1 + g.pick(2) {
		t := []mType{mInt, mInt, mShape, mOpt}[g.pick(4)]
		init := g.expr(t, d, false)
		b := g.binder(t, region)
		blk.vals = append(blk.vals, &sVal{b, init})
		g.visible = append(g.visible, b)
	}
	blk.result = g.intExpr(d, false)
	g.useAll(g.visible[mark:], &blk.result)
	g.visible = g.visible[:mark]
	return blk
}

func (g *metaGen) optMap(d int) mExpr {
	e := eOptMap{opt: g.expr(mOpt, d-1, false)}
	if ints := g.visibleOf(mInt); len(ints) > 0 && g.pick(3) == 0 {
		// A placeholder lambda reading an outer binder.
		e.body = g.read(ints[g.pick(len(ints))], mInt)
		return e
	}
	region := g.region()
	mark := len(g.visible)
	e.param = g.binder(mInt, region)
	g.visible = append(g.visible, e.param)
	e.body = g.body(d-1, region, 3)
	g.useAll([]*mBinder{e.param}, &e.body)
	g.visible = g.visible[:mark]
	return e
}

// match is a match over a value of a random type, with up to three arms and
// a default.
func (g *metaGen) match(d int) mExpr {
	t := []mType{mInt, mShape, mOpt, mPair}[g.pick(4)]
	m := eMatch{subj: g.expr(t, d-1, false)}
	for range 1 + g.pick(3) {
		region := g.region()
		mark := len(g.visible)
		a := &mArm{}
		a.pat = g.pattern(t, 2, false, true, region, a)
		if a.pat == (pWild{}) {
			// The match ends in the one default case `case _` a match
			// may have.
			if t == mPair {
				a.pat = g.ctorPat("", []mType{mInt, mInt}, 0, false, true, region, a)
			} else {
				a.pat = g.patVar(t, region, a)
			}
		}
		if g.pick(3) == 0 {
			a.guard = g.cond(d - 1)
		}
		a.body = g.body(d-1, region, 4)
		g.useAll(a.binders, &a.body)
		g.visible = g.visible[:mark]
		m.arms = append(m.arms, a)
	}
	m.arms = append(m.arms, &mArm{pat: pWild{}, body: g.intExpr(d-1, false)})
	return m
}

// patVar is a pattern variable of type t, bound by arm a.
func (g *metaGen) patVar(t mType, region int, a *mArm) mPat {
	b := g.binder(t, region)
	a.binders = append(a.binders, b)
	g.visible = append(g.visible, b)
	return pBind{b}
}

// pattern is a pattern for values of type t. noBind keeps it free of
// variables (inside an alternative); effects allows extractors.
func (g *metaGen) pattern(t mType, d int, noBind, effects bool, region int, a *mArm) mPat {
	bind := func() mPat {
		if noBind || t == mPair {
			return pWild{}
		}
		return g.patVar(t, region, a)
	}
	c := g.pick(7)
	if d <= 0 && c > 2 {
		c = 0
	}
	switch c {
	case 0:
		return pWild{}
	case 1, 2:
		return bind()
	}
	switch t {
	case mInt:
		switch c {
		case 3:
			return pLit{g.pick(5)}
		case 4:
			// Only the first alternative may run an extractor while #998
			// is open: the others' would run whatever the first did.
			alts := []mPat{g.intLeaf(effects)}
			later := effects
			if later && g.avoid(998) {
				later = false
			}
			second := g.intLeaf(later)
			if l, ok := second.(pLit); ok && alts[0] == second {
				second = pLit{l.n + 1}
			}
			return pAlt{append(alts, second)}
		case 5:
			if effects {
				return pDec{g.pattern(mInt, d-1, noBind, effects, region, a)}
			}
		}
		return g.intLeaf(effects)
	case mShape:
		switch c {
		case 3:
			return g.ctorPat("Circle", []mType{mInt}, d, noBind, effects, region, a)
		case 4:
			return g.ctorPat("Rect", []mType{mInt, mInt}, d, noBind, effects, region, a)
		case 5:
			// One named sub-pattern.
			f := g.pick(2)
			sub := g.pattern(mInt, d-1, noBind, effects, region, a)
			return pCtor{tag: "Rect", fields: []int{f}, subs: []mPat{sub}, labels: []string{"W", "H"}}
		}
		if noBind {
			return pCtor{tag: "Empty"}
		}
		alts := []mPat{
			g.ctorPat("Circle", []mType{mInt}, d, true, effects, region, a),
			pCtor{tag: "Empty"},
		}
		return pAlt{alts}
	case mOpt:
		if c%2 == 0 {
			return g.ctorPat("Some", []mType{mInt}, d, noBind, effects, region, a)
		}
		return pCtor{tag: "None"}
	}
	return g.ctorPat("", []mType{mInt, mInt}, d, noBind, effects, region, a)
}

// intLeaf is an int pattern without variables: a literal or, when effects
// allows, an extractor.
func (g *metaGen) intLeaf(effects bool) mPat {
	if effects && g.pick(2) == 0 {
		return pTest{[]string{"Even", "Pos"}[g.pick(2)]}
	}
	return pLit{g.pick(5)}
}

// ctorPat is tag(sub, ...). While #998 is open a sub-pattern runs an
// extractor only if every sibling before it always matches.
func (g *metaGen) ctorPat(tag string, fields []mType, d int, noBind, effects bool, region int, a *mArm) mPat {
	p := pCtor{tag: tag}
	refutable := false
	for i, t := range fields {
		allow := effects
		if allow && refutable && g.avoid(998) {
			allow = false
		}
		sub := g.pattern(t, d-1, noBind, allow, region, a)
		refutable = refutable || sub.refutable()
		p.fields = append(p.fields, i)
		p.subs = append(p.subs, sub)
	}
	return p
}

// generateMeta generates a program from fuzz bytes; once they run out every
// choice is the first option, so generation always terminates.
func generateMeta(data []byte, cfg metaConfig) metaProgram {
	g := &metaGen{data: data, cfg: cfg, steered: map[int]bool{}}

	for i := range g.pick(3) {
		region := g.region()
		f := &mFunc{name: fmt.Sprintf("fn%d", i+1)}
		for range 1 + g.pick(3) {
			f.params = append(f.params, g.binder([]mType{mInt, mInt, mShape, mOpt}[g.pick(4)], region))
		}
		if f.params[len(f.params)-1].typ == mInt && g.pick(2) == 0 {
			f.def = eTrace{id: g.traceID(), e: eLit{g.pick(6)}}
		}
		g.visible = append([]*mBinder(nil), f.params...)
		f.body = g.intExpr(2, false)
		g.useAll(f.params, &f.body)
		g.visible = nil
		g.funcs = append(g.funcs, f)
	}

	mainRegion := g.region()
	var stmts []mStmt
	var vals []*mBinder
	declare := func(bs ...*mBinder) {
		vals = append(vals, bs...)
		g.visible = append(g.visible, bs...)
	}
	for range 2 + g.pick(4) {
		switch g.pick(8) {
		case 0, 1:
			t := []mType{mInt, mInt, mShape, mOpt}[g.pick(4)]
			init := g.expr(t, 3, false)
			b := g.binder(t, mainRegion)
			stmts = append(stmts, &sVal{b, init})
			declare(b)
		case 2:
			init := g.expr(mPair, 2, false)
			a, b := g.binder(mInt, mainRegion), g.binder(mInt, mainRegion)
			stmts = append(stmts, &sTuple{a, b, init})
			declare(a, b)
		case 3:
			region := g.region()
			p := g.binder(mInt, region)
			mark := len(g.visible)
			g.visible = append(g.visible, p)
			body := g.body(2, region, 3)
			g.useAll([]*mBinder{p}, &body)
			g.visible = g.visible[:mark]
			f := g.binder(mFn, mainRegion)
			stmts = append(stmts, &sLambda{f, p, body})
			declare(f)
		case 4:
			stmts = append(stmts, &sPrint{[]mExpr{g.intExpr(1, true), g.intExpr(1, true)}})
		default:
			stmts = append(stmts, &sPrint{[]mExpr{g.intExpr(3, false)}})
		}
	}
	for _, b := range vals {
		if b.uses == 0 {
			stmts = append(stmts, &sPrint{[]mExpr{g.read(b, mInt)}})
		}
	}

	// The model.
	st := &mState{}
	env := mEnv{}
	for _, s := range stmts {
		env = s.exec(st, env)
	}

	// Scoping.
	c := &mScan{distinct: map[*mBinder]map[*mBinder]bool{}}
	if cfg.hostile {
		c.avoid = g.avoid
	}
	for _, f := range g.funcs {
		c.stack = nil
		for _, p := range f.params {
			c.declare(p)
		}
		f.body.scan(c)
	}
	c.stack = nil
	for _, s := range stmts {
		s.scan(c)
	}

	render := func() string {
		var b strings.Builder
		b.WriteString("package main\n\n" + metaDomain)
		if g.counter {
			b.WriteString(metaCounter)
		}
		for _, f := range g.funcs {
			b.WriteString("\n" + f.render() + "\n")
		}
		b.WriteString("\nfunc main() {\n")
		for _, s := range stmts {
			b.WriteString(s.render() + "\n")
		}
		b.WriteString("}\n")
		return b.String()
	}
	for _, b := range g.binders {
		b.name = fmt.Sprintf("v%d", b.id)
	}
	p := metaProgram{want: st.out.String()}
	p.plain = render()
	p.src = p.plain
	if cfg.hostile {
		g.nameHostile(c)
		p.src = render()
	}
	p.steered = slices.Sorted(maps.Keys(g.steered))
	return p
}

// hostileNames are the names a binder is renamed to besides the names of
// other binders: names generated Go uses for its own temporaries and its
// match subject, and Go's predeclared identifiers and package names.
// Programs never spell a type or package these would hide.
var hostileNames = []struct {
	name  string
	issue int // the open issue the name hits; 0 for none
}{
	{"obj", 997},
	{"_tmp_1", 0},
	{"_tmp_2", 0},
	{"std", 0},
	{"panic", 0},
	{"string", 0},
	{"__p0", 0},
	{"len", 0},
	{"fmt", 0},
}

// nameHostile names every binder, in declaration order, with a hostile name
// or the name of a binder visible where it is declared, chosen at random
// among those that keep every reference resolving as before (c.distinct).
// The choices are seeded from data, so a program renders the same each
// time.
func (g *metaGen) nameHostile(c *mScan) {
	h := fnv.New64a()
	h.Write(g.data)
	rng := rand.New(rand.NewSource(int64(h.Sum64())))
	var pool []string
	for _, n := range hostileNames {
		if n.issue != 0 && g.avoid(n.issue) {
			continue
		}
		pool = append(pool, n.name)
	}
	for _, b := range g.binders {
		b.name = ""
	}
	ok := func(b *mBinder, name string) bool {
		for o := range c.distinct[b] {
			if o.name == name {
				return false
			}
		}
		return true
	}
	for _, b := range g.binders {
		var shadow []string
		for _, v := range b.visible {
			if v.name != "" {
				shadow = append(shadow, v.name)
			}
		}
		hostile := append([]string(nil), pool...)
		rng.Shuffle(len(shadow), func(i, j int) { shadow[i], shadow[j] = shadow[j], shadow[i] })
		rng.Shuffle(len(hostile), func(i, j int) { hostile[i], hostile[j] = hostile[j], hostile[i] })
		// Half the binders try to shadow first, most of the rest try a
		// hostile name first, and a few keep their plain name.
		var cands []string
		switch r := rng.Intn(10); {
		case r < 5:
			cands = append(shadow, hostile...)
		case r < 9:
			cands = append(hostile, shadow...)
		}
		b.name = fmt.Sprintf("v%d", b.id)
		for _, n := range cands {
			if ok(b, n) {
				b.name = n
				break
			}
		}
	}
}
