package gala2lean

import (
	"maps"
	"slices"
	"strings"

	"martianoff/gala/internal/formal/coreexport"
)

// Generics. A generic function, struct or sealed type is a Lean definition
// or type with its type parameters as Lean parameters:
//
//	func first[A any, B comparable](...)  ~>  def first {A B : Type} [GShow A] [GShow B]
//	                                              [Inhabited A] [BEq B] [Inhabited B] ...
//	struct Box[T any](V T)                ~>  structure Box (T : Type) where ...
//
// Lean infers the type arguments of a call, as Go does. What a body can do
// with a value of a type parameter is what the constraint allows: print it
// (GShow, which is how its type prints), and, under `comparable`, compare it
// with `==` (BEq, the BEq derived for its type, which is how GALA compares a
// value of that type: field by field, case and fields for a sealed value).
// The translation stops where a type argument would change that meaning:
//
//   - a type argument that is a function type, or holds one, which Go prints
//     as an address and does not compare (func-type-arg); a call whose type
//     arguments the translation cannot see is left to Lean, which fails to
//     elaborate it rather than pick a meaning;
//   - printing or comparing a value whose type is a type parameter in a
//     program where some type has its own String or Equal (custom-string,
//     custom-equal), holds a function (print-pointer), or in a program with
//     a method the translation does not see (method): the type argument
//     might be one of those types;
//   - the zero value of a type parameter (type-param-zero), which Lean's
//     `default` does not give for every type;
//   - a value of a type mentioning a type parameter kept in the program
//     state (generic-in-state), whose type cannot mention one;
//   - a constraint other than any and comparable (type-constraint), a
//     generic function used as a value (generic-func-value), and explicit
//     type arguments, which Core JSON does not carry (index).
//
// Option, Either and the tuple types of GALA's standard library are the
// prelude's GOption, GEither and T2 to T4 when the program does not declare
// a type of that name.

// stdType is a generic type of GALA's standard library the prelude models:
// its prelude type, how many type arguments it takes, and whether it is
// one of the immutable collections, which a program has only when it
// imports them (collections.go).
type stdType struct {
	lean        string
	arity       int
	collections bool
}

// stdGeneric maps the generic types of GALA's standard library the prelude
// models to their prelude types.
var stdGeneric = map[string]stdType{
	"Option": {"GOption", 1, false}, "Either": {"GEither", 2, false},
	"Tuple": {"T2", 2, false}, "Tuple3": {"T3", 3, false}, "Tuple4": {"T4", 4, false},
	"Array": {"GArray", 1, true}, "List": {"GList", 1, true},
}

// stdType is the standard-library generic type name stands for in this
// program, if any: one the program does not declare itself, and a
// collection only when the program imports them.
func (g *gen) stdType(name string) (stdType, bool) {
	st, ok := stdGeneric[name]
	if !ok || g.declaredType(name) || st.collections && !g.collections {
		return stdType{}, false
	}
	return st, true
}

// stdCtorArg is, for each Option and Either constructor with a field, its
// type and the type argument its field has.
var stdCtorArg = map[string]struct {
	owner string
	arg   int
}{"Some": {"Option", 0}, "Left": {"Either", 0}, "Right": {"Either", 1}}

// typeParam is one type parameter of a declaration: its GALA name, its Lean
// name (tpLean), and whether its constraint is comparable.
type typeParam struct {
	name, lean string
	comparable bool
}

// typeParams reads a declaration's type parameters, stopping at a
// constraint other than any and comparable.
func (g *gen) typeParams(ns []coreexport.Node) []typeParam {
	var out []typeParam
	for _, n := range ns {
		tp, ok := n.(*coreexport.TypeParam)
		if !ok {
			unsupAt(n)
		}
		c := typeName(tp.Constraint)
		if c != "any" && c != "comparable" {
			unsup(tp.Pos, "type-constraint")
		}
		out = append(out, typeParam{name: tp.Name, lean: g.tpLean(tp.Name), comparable: c == "comparable"})
	}
	return out
}

// tpLean is the Lean name of type parameter x. A type parameter shadows a
// type of the same name in GALA; in Lean the binder would shadow the type,
// its constructor (T.mk) and a function of that name too, and the names in
// the translation's Lean types would be ambiguous, so a type parameter
// named like a program declaration or a modelled standard-library type is
// renamed into the translation's reserved namespace.
func (g *gen) tpLean(x string) string {
	if _, std := stdGeneric[x]; std || g.declaredType(x) || g.declared(x) || g.globalBy[x] != nil {
		return "_gala_tp_" + x
	}
	return ident(x)
}

// typeParamNames are the names of a declaration's type parameters.
func typeParamNames(ns []coreexport.Node) []string {
	var out []string
	for _, n := range ns {
		if tp, ok := n.(*coreexport.TypeParam); ok {
			out = append(out, tp.Name)
		}
	}
	return out
}

// declParams is the Lean parameters a generic definition takes for its type
// parameters: the types and how each prints (declParamsShow), then the
// instances its body may use.
func declParams(tps []typeParam) []string {
	out := declParamsShow(tps)
	for _, tp := range tps {
		if tp.comparable {
			out = append(out, "[BEq "+tp.lean+"]")
		}
		out = append(out, "[Inhabited "+tp.lean+"]")
	}
	return out
}

// selfType is the Lean type of program type name inside its own
// declaration: applied to its type parameters.
func selfType(name string, tps []typeParam) string {
	if len(tps) == 0 {
		return ident(name)
	}
	return "(" + strings.Join(append([]string{ident(name)}, typeParamIdents(tps)...), " ") + ")"
}

// typeOnlyBinders is the implicit type parameters alone, for a definition
// that needs no instance of them (a sealed type's field accessor).
func typeOnlyBinders(tps []typeParam) string {
	if len(tps) == 0 {
		return ""
	}
	return " {" + strings.Join(typeParamIdents(tps), " ") + " : Type}"
}

// binders is Lean parameters ps as written after a name, each after a space.
func binders(ps []string) string {
	return strings.Join(append([]string{""}, ps...), " ")
}

// typeBinders is a generic type's parameters in its Lean declaration.
func typeBinders(tps []typeParam) string {
	if len(tps) == 0 {
		return ""
	}
	return " (" + strings.Join(typeParamIdents(tps), " ") + " : Type)"
}

// declParamsShow is the Lean parameters a definition that prints a generic
// type's values takes: the types, and how each prints.
func declParamsShow(tps []typeParam) []string {
	if len(tps) == 0 {
		return nil
	}
	out := []string{"{" + strings.Join(typeParamIdents(tps), " ") + " : Type}"}
	for _, tp := range tps {
		out = append(out, "[GShow "+tp.lean+"]")
	}
	return out
}

// instBinders is declParamsShow written before an instance's colon.
func instBinders(tps []typeParam) string { return binders(declParamsShow(tps)) }

func typeParamIdents(tps []typeParam) []string {
	out := make([]string, len(tps))
	for i, tp := range tps {
		out[i] = tp.lean
	}
	return out
}

// anyIn reports whether GALA type t, a field type of program type owner,
// names one of types, at any depth. A name of one of owner's type
// parameters is the parameter, not a type of that name.
func (g *gen) anyIn(owner string, t coreexport.Node, types map[string]bool) bool {
	for _, n := range g.fieldTypeNames(owner, t) {
		if types[n] {
			return true
		}
	}
	return false
}

// fieldTypeNames is typeNamesIn of t, a field type of program type owner,
// without owner's type parameters.
func (g *gen) fieldTypeNames(owner string, t coreexport.Node) []string {
	return slices.DeleteFunc(typeNamesIn(t), func(n string) bool { return g.isTypeParamOf(owner, n) })
}

// inTypeParams is f's result with the type parameters tps in scope, each
// standing for itself.
func (g *gen) inTypeParams(tps []typeParam, f func() string) string {
	m := map[string]string{}
	cmp := map[string]bool{}
	for _, tp := range tps {
		m[tp.name] = tp.lean
		cmp[tp.lean] = tp.comparable
	}
	saved := g.cmpParams
	g.cmpParams = cmp
	defer func() { g.cmpParams = saved }()
	return g.withTypeArgs(m, f)
}

// equalityOfParams stops the translation where a value of Lean type t is
// compared and t mentions a type parameter in scope that is not
// comparable: the definition has no BEq for it (and std.Equal, which a
// collection's Contains calls, panics on a function type argument).
func (g *gen) equalityOfParams(t string, pos coreexport.Pos) {
	for _, w := range leanWords(t) {
		if c, isParam := g.cmpParams[w]; isParam && !c && g.mentionsTypeParam(w) {
			unsup(pos, "type-param-equal")
		}
	}
}

// withTypeArgs runs f with the type parameters named in m standing for the
// Lean types in m (a type parameter in scope stands for itself; one of a
// callee, for the type argument of the call); "" stands for a type argument
// not known, which stops f.
func (g *gen) withTypeArgs(m map[string]string, f func() string) string {
	saved := g.tparams
	g.tparams = m
	defer func() { g.tparams = saved }()
	return f()
}

// typeArg is the Lean type type parameter x stands for, and whether x is
// one.
func (g *gen) typeArg(x string, pos coreexport.Pos) (string, bool) {
	t, ok := g.tparams[x]
	if ok && t == "" {
		unsup(pos, "unknown-type-arg")
	}
	return t, ok
}

// mentionsTypeParam reports whether Lean type t names a type parameter in
// scope.
func (g *gen) mentionsTypeParam(t string) bool {
	for _, w := range leanWords(t) {
		for x, lt := range g.tparams {
			if lt == g.tpLean(x) && w == lt {
				return true
			}
		}
	}
	return false
}

// leanWords are the identifiers in Lean type t.
func leanWords(t string) []string {
	return strings.FieldsFunc(t, func(r rune) bool { return r == ' ' || r == '(' || r == ')' })
}

// genericType is the Lean type of program or standard-library generic type
// name applied to the Lean types args, "" when name is neither.
func (g *gen) genericType(name string, args []string, pos coreexport.Pos) string {
	var head string
	var want int
	std, isStd := g.stdType(name)
	switch {
	case g.declaredType(name):
		head, want = ident(name), len(g.declTypeParams(name))
	case isStd:
		head, want = std.lean, std.arity
	default:
		return ""
	}
	if len(args) != want {
		unsup(pos, "generic-type")
	}
	if len(args) == 0 {
		return head
	}
	return "(" + head + " " + strings.Join(args, " ") + ")"
}

// declTypeParams is the type parameters of program type name.
func (g *gen) declTypeParams(name string) []coreexport.Node {
	if s := g.structs[name]; s != nil {
		return s.TypeParams
	}
	if s := g.sealeds[name]; s != nil {
		return s.TypeParams
	}
	return nil
}

// typeParamNamesOf is the names of program type name's type parameters.
func (g *gen) typeParamNamesOf(name string) []string {
	return typeParamNames(g.declTypeParams(name))
}

// isTypeParamOf reports whether x is a type parameter of program type name.
func (g *gen) isTypeParamOf(name, x string) bool {
	return slices.Contains(g.typeParamNamesOf(name), x)
}

// isGenericDecl reports whether program type name has type parameters.
func (g *gen) isGenericDecl(name string) bool { return len(g.declTypeParams(name)) > 0 }

// ltype is a Lean type as the translation writes it, read back: a name
// applied to arguments, or a function type.
type ltype struct {
	head   string
	args   []ltype
	params []ltype // a function type's parameters
	result *ltype  // a function type's result (inside _gala_M)
}

// parseLean reads a Lean type the translation wrote.
func parseLean(s string) ltype {
	s = strings.TrimSpace(s)
	for strings.HasPrefix(s, "(") && matchingParen(s) == len(s)-1 {
		inner := s[1 : len(s)-1]
		if parts := splitTop(inner, " → "); len(parts) > 1 {
			t := ltype{}
			for _, p := range parts[:len(parts)-1] {
				t.params = append(t.params, parseLean(p))
			}
			r := parseLean(strings.TrimPrefix(parts[len(parts)-1], "_gala_M "))
			t.result = &r
			return t
		}
		words := splitTop(inner, " ")
		if len(words) == 1 {
			s = inner
			continue
		}
		t := ltype{head: words[0]}
		for _, w := range words[1:] {
			t.args = append(t.args, parseLean(w))
		}
		return t
	}
	return ltype{head: s}
}

// matchingParen is the index of the parenthesis closing s[0].
func matchingParen(s string) int {
	depth := 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitTop splits s at sep outside parentheses.
func splitTop(s, sep string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		}
		if depth == 0 && strings.HasPrefix(s[i:], sep) {
			out = append(out, s[start:i])
			start = i + len(sep)
			i += len(sep) - 1
		}
	}
	return append(out, s[start:])
}

// String writes t back as the translation writes it.
func (t ltype) String() string {
	if t.result != nil {
		parts := make([]string, len(t.params))
		for i, p := range t.params {
			parts[i] = p.String()
		}
		return "(" + strings.Join(parts, " → ") + " → _gala_M " + t.result.String() + ")"
	}
	if len(t.args) == 0 {
		return t.head
	}
	parts := []string{t.head}
	for _, a := range t.args {
		parts = append(parts, a.String())
	}
	return "(" + strings.Join(parts, " ") + ")"
}

// unify matches GALA type pattern p, over the type parameters params, with
// Lean type t, and records in m the Lean type each parameter stands for.
func (g *gen) unify(p coreexport.Node, t ltype, params []string, m map[string]string) {
	switch p := p.(type) {
	case *coreexport.TypeName:
		if len(p.Args) == 0 && slices.Contains(params, p.Name) {
			known := t.result != nil || t.head != "" && t.head != "_"
			if _, ok := m[p.Name]; !ok && known {
				m[p.Name] = t.String()
			}
			return
		}
		st, _ := g.stdType(p.Name)
		head := st.lean
		if g.declaredType(p.Name) {
			head = ident(p.Name)
		}
		if head == "" || t.head != head || len(t.args) != len(p.Args) {
			return
		}
		for i, a := range p.Args {
			g.unify(a, t.args[i], params, m)
		}
	case *coreexport.TypeFunc:
		if t.result == nil || len(t.params) != max(len(p.Params), 1) {
			return
		}
		for i, a := range p.Params {
			g.unify(a, t.params[i], params, m)
		}
		if p.Result != nil {
			g.unify(p.Result, *t.result, params, m)
		}
	}
}

// instantiate is the type arguments, over the type parameters params, of a
// call with the arguments args of a callee whose parameters are names of
// types types: each type parameter's Lean type, as far as the arguments'
// types are plain to see, "" for one not known.
func (g *gen) instantiate(params, names []string, types []coreexport.Node, args []coreexport.Node, sc *scope, seen map[string]bool) map[string]string {
	m := map[string]string{}
	for i, a := range args {
		k := i
		if na, ok := a.(*coreexport.NamedArg); ok {
			k = slices.Index(names, na.Name)
		}
		if k < 0 || k >= len(types) || types[k] == nil {
			continue
		}
		// Each argument's own cycle guard: a global two arguments name is
		// typed for each.
		t := g.safeType(func() string { return g.exprType(argNode(a), sc, maps.Clone(seen)) })
		if t != "" {
			g.unify(types[k], parseLean(t), params, m)
		}
	}
	for _, x := range params {
		if _, ok := m[x]; !ok {
			m[x] = ""
		}
	}
	return m
}

// instFunc is instantiate for a call of generic function f with args.
func (g *gen) instFunc(f *coreexport.Func, args []coreexport.Node, sc *scope, seen map[string]bool) map[string]string {
	s, _ := attempt(func() *sig { return funcSig(f, "") })
	if s == nil {
		return nil
	}
	return g.instantiate(typeParamNames(f.TypeParams), s.names, s.types, args, sc, seen)
}

// instSig puts the type arguments of the call e of generic function f into
// s, f's signature: each parameter's type is the type the call passes. A
// type argument that is or holds a function stops the translation
// (func-type-arg); one not plain to see is left to Lean, and stops a
// parameter type the translation needs (a lambda's).
func (g *gen) instSig(f *coreexport.Func, s *sig, e *coreexport.Call, sc *scope) {
	m := g.instantiate(typeParamNames(f.TypeParams), s.names, s.types, e.Args, sc, map[string]bool{})
	for _, lt := range m {
		if strings.Contains(lt, "→") {
			unsup(e.Pos, "func-type-arg")
		}
	}
	g.substSig(s, m, e.Pos)
}

// ctorOf is the program type whose constructor x is (a struct, or a case
// of a sealed type), "" for none.
func (g *gen) ctorOf(x string) string {
	if g.structs[x] != nil {
		return x
	}
	if v := g.variants[x]; v != nil {
		return v.sealed.Name
	}
	return ""
}

// instCtor puts the type arguments of the call e of the constructor of
// generic type name into s, its signature, as instSig does. A function type
// argument is a field holding a function, as in a non-generic type.
func (g *gen) instCtor(name string, s *sig, e *coreexport.Call, sc *scope) {
	m := g.instantiate(g.typeParamNamesOf(name), s.names, s.types, e.Args, sc, map[string]bool{})
	g.substSig(s, m, e.Pos)
}

// substSig puts the type arguments m into s's parameter types.
func (g *gen) substSig(s *sig, m map[string]string, pos coreexport.Pos) {
	nodes := map[string]coreexport.Node{}
	for x, lt := range m {
		n := g.galaType(lt)
		if n == nil {
			n = &coreexport.TypeName{Meta: coreexport.Meta{Pos: pos}, Name: unknownTypeArg}
		}
		nodes[x] = n
	}
	for i, t := range s.types {
		if t != nil {
			s.types[i] = substType(t, nodes)
		}
	}
}

// unknownTypeArg stands for a type argument the translation cannot see.
const unknownTypeArg = "_gala_unknown"

// ctorType is the Lean type of a call of the constructor of program type
// name (a struct, or a case of a sealed type) whose fields are fields, as far
// as the arguments' types are plain to see; "" when a type argument is not.
func (g *gen) ctorType(name string, fields []*coreexport.Field, args []coreexport.Node, sc *scope, seen map[string]bool) string {
	params := g.typeParamNamesOf(name)
	if len(params) == 0 {
		return ident(name)
	}
	names := make([]string, len(fields))
	types := make([]coreexport.Node, len(fields))
	for i, f := range fields {
		names[i], types[i] = f.Name, f.Type
	}
	m := g.instantiate(params, names, types, args, sc, seen)
	parts := []string{ident(name)}
	for _, x := range params {
		if m[x] == "" {
			return ""
		}
		parts = append(parts, m[x])
	}
	return "(" + strings.Join(parts, " ") + ")"
}

// instType is the Lean type of GALA type t, which mentions type parameters,
// given their type arguments m; "" when one it needs is not known.
func (g *gen) instType(t coreexport.Node, m map[string]string) string {
	return g.safeType(func() string {
		return g.withTypeArgs(m, func() string { return g.leanType(t) })
	})
}

// substType is GALA type t with each type parameter named in m replaced by
// m's type.
func substType(t coreexport.Node, m map[string]coreexport.Node) coreexport.Node {
	switch t := t.(type) {
	case *coreexport.TypeName:
		if len(t.Args) == 0 {
			if r, ok := m[t.Name]; ok {
				return r
			}
			return t
		}
		c := *t
		c.Args = make([]coreexport.Node, len(t.Args))
		for i, a := range t.Args {
			c.Args[i] = substType(a, m)
		}
		return &c
	case *coreexport.TypeFunc:
		c := *t
		c.Params = make([]coreexport.Node, len(t.Params))
		for i, p := range t.Params {
			c.Params[i] = substType(p, m)
		}
		if t.Result != nil {
			c.Result = substType(t.Result, m)
		}
		return &c
	}
	return t
}

// typeArgsOf maps the type parameters of program type name to the type
// arguments of t, a use of it; nil when t gives none.
func (g *gen) typeArgsOf(name string, t coreexport.Node) map[string]coreexport.Node {
	tn, ok := t.(*coreexport.TypeName)
	params := g.typeParamNamesOf(name)
	if !ok || tn.Name != name || len(tn.Args) != len(params) {
		return nil
	}
	m := map[string]coreexport.Node{}
	for i, x := range params {
		m[x] = tn.Args[i]
	}
	return m
}

// fieldTypeIn is the type of field f of program type name, for a value of
// GALA type t: f's own type with name's type arguments put in. For a generic
// type whose arguments t does not give, nil when the field's type mentions
// one.
func (g *gen) fieldTypeIn(name string, f *coreexport.Field, t coreexport.Node) coreexport.Node {
	params := g.typeParamNamesOf(name)
	if len(params) == 0 {
		return f.Type
	}
	if m := g.typeArgsOf(name, t); m != nil {
		return substType(f.Type, m)
	}
	for _, x := range typeNamesIn(f.Type) {
		if slices.Contains(params, x) {
			return nil
		}
	}
	return f.Type
}

// stdCtorFieldType is the type of the one field of Option or Either
// constructor x (Some, Left, Right) for a subject of GALA type t: the
// matching type argument of t, nil when t does not give it. None has none.
func stdCtorFieldType(x string, t coreexport.Node) []coreexport.Node {
	c, has := stdCtorArg[x]
	if !has {
		return nil
	}
	tn, ok := t.(*coreexport.TypeName)
	if !ok || tn.Name != c.owner || c.arg >= len(tn.Args) {
		return []coreexport.Node{nil}
	}
	return []coreexport.Node{tn.Args[c.arg]}
}

// genericNames are the Lean names of the program's generic types and
// functions, in source order.
func (g *gen) genericNames() []string {
	var out []string
	for _, n := range g.typeNames {
		if g.isGenericDecl(n) {
			out = append(out, ident(n))
		}
	}
	for _, f := range g.funcOrder {
		if f.Receiver == nil && len(f.TypeParams) > 0 {
			out = append(out, ident(f.Name))
		}
	}
	return out
}

// typeHead is the name of a named type, with or without type arguments, ""
// for any other.
func typeHead(t coreexport.Node) string {
	if n, ok := t.(*coreexport.TypeName); ok {
		return n.Name
	}
	return ""
}
