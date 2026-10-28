package analyzer

import (
	"maps"
	"slices"

	"github.com/antlr4-go/antlr/v4"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/transformer"
)

// A method of a generic type may use one of the type's parameters where Go
// requires a comparable type, while the type declares it `any`:
// `func (m HashMap[K, V]) ToGoMap() map[K]V` on `type HashMap[K any, V any]`.
// Go cannot compile that as a method, because a method cannot constrain its
// receiver's type parameters further. GALA lowers such a method to a function,
// as it lowers a method with type parameters of its own, and gives the
// function's copy of each such parameter the `comparable` constraint. The
// method keeps its GALA form; a call of it is checked for a comparable type
// argument (GALA-E0086) and calls the function.
//
// The requirement is found by one rule: a receiver type parameter must be
// comparable when it reaches a place that instantiates something whose
// parameter is comparable. The places are
//
//   - a type the method spells, in its signature or body: the key of a map
//     type, and a type argument of a generic type declared `comparable`;
//   - a call: a type argument, written or inferred from an argument, of a
//     function or method whose type parameter is `comparable`, or the
//     receiver of a method that is itself lowered this way;
//   - `==` and `!=`, whose operands Go requires to be comparable.
//
// An expression's type is known here from what the method declares: its
// receiver and parameters, typed `val`/`var` bindings, typed lambda parameters
// and patterns, and fields and zero-argument methods reached from those. An
// operand whose type the analyzer cannot tell is left to the transformer,
// which rejects `==` on a type parameter constrained by any (GALA-E0084).

// markComparableReceivers records, on each queued method, the type
// parameters of its type that the method needs comparable
// (MethodMetadata.ComparableTypeParams), and marks the method lowered to a
// function (IsGeneric). A method that calls another such method on a value
// of its own type needs what that one needs, so the scan repeats until no
// method gains a requirement. fieldParams is shared by every scan.
func (d *packageDecls) markComparableReceivers(a *galaAnalyzer, richAST *transpiler.RichAST) {
	outer := a.currentQualifiers
	defer func() { a.currentQualifiers = outer }()
	held := map[*transpiler.TypeMetadata][]bool{}
	interfaces := interfaceMethodSets(richAST)
	for changed := true; changed; {
		changed = false
		for _, r := range d.receivers {
			typeMeta := richAST.Types[r.typeKey]
			if r.ctx == nil || typeMeta == nil || len(typeMeta.TypeParams) != len(r.recvNames) ||
				!slices.ContainsFunc(typeMeta.TypeParams, func(tp string) bool { return typeMeta.TypeParamConstraints[tp] != "comparable" }) {
				continue
			}
			a.currentQualifiers = r.quals
			// The metadata a call reads, which a sibling pass may have
			// recorded before this declaration's own.
			metas := []*transpiler.MethodMetadata{r.meta}
			if stored := typeMeta.Methods[r.meta.Name]; stored != nil && stored != r.meta {
				metas = append(metas, stored)
			}
			// A method by which the type implements an interface keeps its Go
			// method, so a value of the type still implements it; one that
			// needs a comparable parameter stays the error it was
			// (GALA-E0084, or Go's).
			if implementsBy(typeMeta, r.meta.Name, interfaces) {
				continue
			}
			for i, need := range a.comparableNeeds(r, richAST, held) {
				// A parameter constrained by another interface keeps that
				// constraint; only one declared `any` is lowered.
				declared := typeMeta.TypeParams[i]
				if c := typeMeta.TypeParamConstraints[declared]; !need || (c != "" && c != "any") {
					continue
				}
				for _, m := range metas {
					if !slices.Contains(m.ComparableTypeParams, declared) {
						m.ComparableTypeParams = append(m.ComparableTypeParams, declared)
						slices.Sort(m.ComparableTypeParams)
						m.IsGeneric = true
						changed = true
					}
				}
			}
		}
	}
}

// interfaceMethodSets lists, by method name, the method sets of the
// interfaces the analysis knows that name it: GALA's and Go's, and Go's error
// and fmt.Stringer, which Go and fmt look for on any value.
func interfaceMethodSets(richAST *transpiler.RichAST) map[string][][]string {
	sets := map[string][][]string{"Error": {{"Error"}}, "String": {{"String"}}}
	add := func(names []string) {
		for _, n := range names {
			sets[n] = append(sets[n], names)
		}
	}
	for _, meta := range richAST.Types {
		if meta.IsInterface {
			add(slices.Collect(maps.Keys(meta.Methods)))
		}
	}
	if gi := richAST.GoTypeInfo; gi != nil {
		for _, data := range gi.Types {
			if data.Kind == "interface" {
				add(slices.Collect(maps.Keys(data.Methods)))
			}
		}
	}
	return sets
}

// implementsBy reports whether method is one by which the type meta
// implements an interface of interfaces: the type has every method of an
// interface that names it.
func implementsBy(meta *transpiler.TypeMetadata, method string, interfaces map[string][][]string) bool {
	for _, set := range interfaces[method] {
		if !slices.ContainsFunc(set, func(n string) bool { return meta.Methods[n] == nil }) {
			return true
		}
	}
	return false
}

// comparableScan walks one method for the receiver type parameters it uses
// where a comparable type is required.
type comparableScan struct {
	a       *galaAnalyzer
	richAST *transpiler.RichAST
	pkgName string
	quals   fileQualifiers
	params  []string
	// env holds the declared type of each name the method binds.
	env  map[string]transpiler.Type
	need []bool
	// held memoizes fieldParams.
	held map[*transpiler.TypeMetadata][]bool
}

// comparableNeeds reports, by position, the receiver type parameters of r
// that the method needs comparable.
func (a *galaAnalyzer) comparableNeeds(r receiverDecl, richAST *transpiler.RichAST, held map[*transpiler.TypeMetadata][]bool) []bool {
	s := &comparableScan{a: a, richAST: richAST, pkgName: r.pkgName, quals: r.quals, params: r.recvNames,
		env: map[string]transpiler.Type{}, need: make([]bool, len(r.recvNames)), held: held}
	if recv, ok := r.ctx.Receiver().(*grammar.ReceiverContext); ok && recv != nil {
		s.env[recv.Identifier().GetText()] = s.resolve(recv.Type_())
	}
	s.walk(r.ctx)
	return s.need
}

// paramIndex is the position in params of the type parameter typ names, or
// -1 when typ is not one of them.
func paramIndex(typ transpiler.Type, params []string) int {
	switch t := typ.(type) {
	case transpiler.BasicType:
		return slices.Index(params, t.Name)
	case transpiler.NamedType:
		if t.Package == "" {
			return slices.Index(params, t.Name)
		}
	}
	return -1
}

func (s *comparableScan) resolve(ctx grammar.ITypeContext) transpiler.Type {
	if ctx == nil {
		return transpiler.NilType{}
	}
	return s.a.resolveTypeWithParams(galaparser.TypeText(ctx), s.pkgName, s.params)
}

// walk visits every node of tree, binding names before the nodes that use
// them are reached (a declaration precedes its uses in the source).
func (s *comparableScan) walk(tree antlr.Tree) {
	switch n := tree.(type) {
	case *grammar.LambdaExpressionContext, *grammar.BlockContext, *grammar.CaseClauseContext:
		// A scope: what it binds is not seen after it.
		outer := maps.Clone(s.env)
		defer func() { s.env = outer }()
	case *grammar.TypeContext:
		s.instantiations(s.resolve(n))
		return // nested types are part of the one just walked
	case *grammar.ParameterContext:
		if n.Identifier() != nil {
			if n.Type_() != nil {
				s.env[n.Identifier().GetText()] = s.resolve(n.Type_())
			} else {
				// An untyped lambda parameter shadows what the name held.
				delete(s.env, n.Identifier().GetText())
			}
		}
	case *grammar.TypedPatternContext:
		s.env[n.Identifier().GetText()] = s.resolve(n.Type_())
	case *grammar.ValDeclarationContext:
		s.bindAll(n.IdentifierList(), n.Type_())
	case *grammar.VarDeclarationContext:
		s.bindAll(n.IdentifierList(), n.Type_())
	case *grammar.EqualityExprContext:
		if operands := n.AllRelationalExpr(); len(operands) > 1 {
			for _, op := range operands {
				s.require(s.operandType(op))
			}
		}
	case *grammar.PostfixExprContext:
		s.calls(n)
	}
	for i := 0; i < tree.GetChildCount(); i++ {
		s.walk(tree.GetChild(i))
	}
}

func (s *comparableScan) bindAll(ids grammar.IIdentifierListContext, typ grammar.ITypeContext) {
	list, ok := ids.(*grammar.IdentifierListContext)
	if !ok || list == nil {
		return
	}
	for _, id := range list.AllIdentifier() {
		if typ == nil {
			delete(s.env, id.GetText()) // an inferred binding shadows the name
		} else {
			s.env[id.GetText()] = s.resolve(typ)
		}
	}
}

// require records the receiver type parameters typ needs comparable for typ
// to be comparable: those it holds by value. A pointer, a function, a Go map
// or slice is comparable, or never is, whatever its type arguments; a generic
// type needs the arguments of the parameters its fields hold by value
// (fieldParams), so OptionPtr[Node[K]], which holds a pointer, needs none.
func (s *comparableScan) require(typ transpiler.Type) {
	s.mark(typ, s.params, s.need, nil)
}

// mark sets, in held, the type parameters of params that typ holds by value:
// typ itself, or the arguments of a generic type's parameters its fields hold
// by value (fieldParams). visiting is fieldParams' cycle guard.
func (s *comparableScan) mark(typ transpiler.Type, params []string, held []bool, visiting map[*transpiler.TypeMetadata]bool) {
	if i := paramIndex(typ, params); i >= 0 {
		held[i] = true
		return
	}
	if t, ok := typ.(transpiler.GenericType); ok {
		inner := s.fieldParams(s.typeMeta(t.Base), visiting)
		for i, p := range t.Params {
			if i < len(inner) && inner[i] {
				s.mark(p, params, held, visiting)
			}
		}
	}
}

// fieldParams reports, by position, the type parameters of the GALA type
// meta whose argument must be comparable for the type to be: those a field
// holds by value (a field of any case of a sealed type, the underlying type
// of an opaque one), directly or through another generic type. visiting
// breaks a cycle through a type that holds itself. An unknown type needs
// none: Go decides.
func (s *comparableScan) fieldParams(meta *transpiler.TypeMetadata, visiting map[*transpiler.TypeMetadata]bool) []bool {
	if meta == nil || meta.IsInterface || visiting[meta] {
		return nil
	}
	if held, ok := s.held[meta]; ok {
		return held
	}
	if visiting == nil {
		visiting = map[*transpiler.TypeMetadata]bool{}
	}
	visiting[meta] = true
	defer delete(visiting, meta)
	held := make([]bool, len(meta.TypeParams))
	fields := slices.Collect(maps.Values(meta.Fields))
	for _, v := range meta.SealedVariants {
		fields = append(fields, v.FieldTypes...)
	}
	if meta.IsOpaque && meta.Underlying != nil {
		fields = append(fields, meta.Underlying)
	}
	for _, f := range fields {
		s.mark(f, meta.TypeParams, held, visiting)
	}
	if len(visiting) == 1 { // not a partial answer inside a cycle
		s.held[meta] = held
	}
	return held
}

// instantiations visits the instantiations typ spells: a map type requires
// its key comparable, and a generic type the arguments of its parameters
// declared comparable.
func (s *comparableScan) instantiations(typ transpiler.Type) {
	switch t := typ.(type) {
	case transpiler.MapType:
		s.require(t.Key)
		s.instantiations(t.Key)
		s.instantiations(t.Elem)
	case transpiler.GenericType:
		if meta := s.typeMeta(t.Base); meta != nil {
			for i, p := range t.Params {
				if i < len(meta.TypeParams) && meta.TypeParamConstraints[meta.TypeParams[i]] == "comparable" {
					s.require(p)
				}
			}
		}
		for _, p := range t.Params {
			s.instantiations(p)
		}
	case transpiler.ArrayType:
		s.instantiations(t.Elem)
	case transpiler.PointerType:
		s.instantiations(t.Elem)
	case transpiler.FuncType:
		for _, p := range t.Params {
			s.instantiations(p)
		}
		for _, r := range t.Results {
			s.instantiations(r)
		}
	}
}

func (s *comparableScan) typeMeta(base transpiler.Type) *transpiler.TypeMetadata {
	if bt, ok := base.(transpiler.BasicType); ok { // a type of package main
		if slices.Contains(s.params, bt.Name) {
			return nil
		}
		return s.richAST.Types[bt.Name]
	}
	nt, ok := base.(transpiler.NamedType)
	if !ok {
		return nil
	}
	if nt.Package != "" {
		if meta := s.richAST.Types[nt.Package+"."+nt.Name]; meta != nil {
			return meta
		}
	}
	return s.richAST.Types[nt.Name]
}

// callee is what a call needs to know of the function or method it calls:
// its type parameters, which of them are comparable, and its parameters.
type callee struct {
	typeParams []string
	comparable func(string) bool
	params     []transpiler.Type
	variadic   bool
}

// calls visits the calls of postfix: `f(...)`, `pkg.f(...)`, `f[A](...)` and
// `x.m(...)` on a value whose type is known.
func (s *comparableScan) calls(postfix *grammar.PostfixExprContext) {
	primary := transformer.PrimaryOf(postfix)
	if primary == nil || primary.Identifier() == nil {
		return
	}
	suffixes := postfix.AllPostfixSuffix()
	name := primary.Identifier().GetText()
	recv, haveRecv := s.env[name] // a value: its methods are called
	if haveRecv {
		name = ""
	}
	qual := ""
	if _, isPkg := s.quals.named[name]; isPkg && !haveRecv {
		qual, name = name, ""
	}
	var typeArgs []grammar.IExpressionContext
	for _, suf := range suffixes {
		sx := suf.(*grammar.PostfixSuffixContext)
		switch {
		case sx.Identifier() != nil: // `.name`
			sel := sx.Identifier().GetText()
			switch {
			case qual != "" && name == "":
				name = sel
			case haveRecv && name == "":
				name = sel // a method or field of recv, decided at the call
			case haveRecv:
				recv, haveRecv = s.memberType(recv, name), true
				name = sel
			default:
				return
			}
			typeArgs = nil
		case sx.ExpressionList() != nil: // `[...]`: type arguments before a call
			typeArgs = sx.ExpressionList().(*grammar.ExpressionListContext).AllExpression()
		default: // `(...)`
			var c *callee
			switch {
			case haveRecv && name != "":
				c = s.method(recv, name)
			case !haveRecv && name != "":
				c = s.function(qual, name)
			}
			if c != nil {
				s.call(c, typeArgs, sx.ArgumentList())
			}
			if haveRecv && name != "" {
				recv = s.callResult(recv, name)
				haveRecv = recv != nil
			} else {
				haveRecv = false
			}
			name, qual, typeArgs = "", "", nil
			if !haveRecv {
				return
			}
		}
	}
}

// call records what calling c with typeArgs written and the arguments of
// args needs: each comparable type parameter's argument, written or inferred
// from an argument whose declared type is known.
func (s *comparableScan) call(c *callee, typeArgs []grammar.IExpressionContext, args grammar.IArgumentListContext) {
	bound := map[string]transpiler.Type{}
	for i, ta := range typeArgs {
		if i < len(c.typeParams) {
			bound[c.typeParams[i]] = s.a.resolveTypeWithParams(ta.GetText(), s.pkgName, s.params)
		}
	}
	if list, ok := args.(*grammar.ArgumentListContext); ok && list != nil {
		for i, arg := range list.AllArgument() {
			pi := min(i, len(c.params)-1)
			if pi < 0 || (i >= len(c.params) && !c.variadic) {
				break
			}
			ti := paramIndex(c.params[pi], c.typeParams)
			if ti < 0 || bound[c.typeParams[ti]] != nil {
				continue
			}
			if t := s.argumentType(arg.(*grammar.ArgumentContext)); t != nil {
				bound[c.typeParams[ti]] = t
			}
		}
	}
	for tp, t := range bound {
		if c.comparable(tp) {
			s.require(t)
		}
	}
}

// function resolves a call of name, qualified by the import qual or not.
func (s *comparableScan) function(qual, name string) *callee {
	var pkgs []string
	if qual != "" {
		b := s.quals.named[qual]
		if b.PkgName == "" {
			b.PkgName = qual
		}
		pkgs = []string{b.PkgName}
	} else {
		pkgs = []string{s.pkgName}
		for _, d := range s.quals.dots {
			pkgs = append(pkgs, d.PkgName)
		}
	}
	for _, pkg := range pkgs {
		key := name
		if pkg != "" && pkg != "main" && pkg != "test" {
			key = pkg + "." + name
		}
		if fn := s.richAST.Functions[key]; fn != nil && len(fn.TypeParams) > 0 {
			return &callee{fn.TypeParams, constrainedBy(fn.TypeParamConstraints), fn.ParamTypes, fn.Variadic}
		}
		if gi := s.richAST.GoTypeInfo; gi != nil {
			if sig := gi.Functions[pkg+"."+name]; sig != nil && len(sig.TypeParams) > 0 {
				params := make([]transpiler.Type, len(sig.Params))
				for i, p := range sig.Params {
					params[i] = p.Type
				}
				return &callee{sig.TypeParams, func(tp string) bool { return slices.Contains(sig.ComparableTypeParams, tp) }, params, sig.IsVariadic}
			}
		}
	}
	return nil
}

// method resolves a call of method name on a value of type recv. The
// receiver's type arguments are bound up front: a method lowered for a
// comparable receiver parameter needs that argument comparable.
func (s *comparableScan) method(recv transpiler.Type, name string) *callee {
	meta, args := s.instance(recv)
	if meta == nil {
		return nil
	}
	mm := meta.Methods[name]
	if mm == nil {
		return nil
	}
	for i, tp := range meta.TypeParams {
		if i < len(args) && slices.Contains(mm.ComparableTypeParams, tp) {
			s.require(args[i])
		}
	}
	if len(mm.TypeParams) == 0 {
		return nil
	}
	return &callee{mm.TypeParams, constrainedBy(mm.TypeParamConstraints), s.substitute(meta, args, mm.ParamTypes), mm.Variadic}
}

// instance gives the metadata of the GALA type typ instantiates, and its
// type arguments.
func (s *comparableScan) instance(typ transpiler.Type) (*transpiler.TypeMetadata, []transpiler.Type) {
	if p, ok := typ.(transpiler.PointerType); ok {
		typ = p.Elem
	}
	switch t := typ.(type) {
	case transpiler.GenericType:
		return s.typeMeta(t.Base), t.Params
	case transpiler.NamedType:
		return s.typeMeta(t), nil
	}
	return nil, nil
}

func (s *comparableScan) substitute(meta *transpiler.TypeMetadata, args []transpiler.Type, types []transpiler.Type) []transpiler.Type {
	subst := map[string]transpiler.Type{}
	for i, tp := range meta.TypeParams {
		if i < len(args) {
			subst[tp] = args[i]
		}
	}
	out := make([]transpiler.Type, len(types))
	for i, t := range types {
		out[i] = transpiler.SubstituteTypeParams(t, subst)
	}
	return out
}

// memberType is the type of field name of a value of type recv, or nil.
func (s *comparableScan) memberType(recv transpiler.Type, name string) transpiler.Type {
	meta, args := s.instance(recv)
	if meta == nil {
		return nil
	}
	if f, ok := meta.Fields[name]; ok {
		return s.substitute(meta, args, []transpiler.Type{f})[0]
	}
	return nil
}

// callResult is the result type of calling method name on recv, or nil.
func (s *comparableScan) callResult(recv transpiler.Type, name string) transpiler.Type {
	meta, args := s.instance(recv)
	if meta == nil || meta.Methods[name] == nil || meta.Methods[name].ReturnType == nil {
		return nil
	}
	return s.substitute(meta, args, []transpiler.Type{meta.Methods[name].ReturnType})[0]
}

// operandType is the declared type of an `==` operand, or nil when the
// analyzer cannot tell it.
func (s *comparableScan) operandType(ctx grammar.IRelationalExprContext) transpiler.Type {
	rel, ok := ctx.(*grammar.RelationalExprContext)
	if !ok || len(rel.AllAdditiveExpr()) != 1 {
		return nil
	}
	add := rel.AdditiveExpr(0).(*grammar.AdditiveExprContext)
	if len(add.AllMultiplicativeExpr()) != 1 {
		return nil
	}
	mul := add.MultiplicativeExpr(0).(*grammar.MultiplicativeExprContext)
	if len(mul.AllUnaryExpr()) != 1 {
		return nil
	}
	postfix, _ := mul.UnaryExpr(0).(*grammar.UnaryExprContext).PostfixExpr().(*grammar.PostfixExprContext)
	return s.valueType(postfix)
}

// argumentType is the declared type of a call argument, or nil.
func (s *comparableScan) argumentType(arg *grammar.ArgumentContext) transpiler.Type {
	p, ok := arg.Pattern().(*grammar.ExpressionPatternContext)
	if !ok || p == nil {
		return nil
	}
	return s.valueType(transformer.LeadingPostfixExpr(p.Expression(), true))
}

// valueType is the type of a name the method binds, read through fields and
// zero-argument method calls: `k`, `p.key`, `m.Keys()`.
func (s *comparableScan) valueType(postfix *grammar.PostfixExprContext) transpiler.Type {
	primary := transformer.PrimaryOf(postfix)
	if primary == nil || primary.Identifier() == nil {
		return nil
	}
	t := s.env[primary.Identifier().GetText()]
	member := ""
	for _, suf := range postfix.AllPostfixSuffix() {
		if t == nil {
			return nil
		}
		sx := suf.(*grammar.PostfixSuffixContext)
		switch {
		case sx.Identifier() != nil:
			if member != "" {
				t = s.memberType(t, member)
			}
			member = sx.Identifier().GetText()
		case sx.ExpressionList() == nil && sx.ArgumentList() == nil && member != "":
			t, member = s.callResult(t, member), ""
		default:
			return nil
		}
	}
	if member != "" && t != nil {
		t = s.memberType(t, member)
	}
	return t
}

// constrainedBy reports whether a type parameter is declared comparable.
func constrainedBy(constraints map[string]string) func(string) bool {
	return func(tp string) bool { return constraints[tp] == "comparable" }
}
