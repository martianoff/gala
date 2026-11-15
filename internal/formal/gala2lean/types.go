package gala2lean

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"martianoff/gala/internal/formal/coreexport"
)

// leanType is GALA type t in Lean.
func (g *gen) leanType(t coreexport.Node) string {
	switch t := t.(type) {
	case nil:
		return "Unit"
	case *coreexport.TypeName:
		if t.Package != "" {
			// A type of a package outside the module, or one the
			// translation was not given (Options.Deps).
			unsup(t.Pos, "qualified-type")
		}
		if len(t.Args) == 0 {
			if lt, ok := g.typeArg(t.Name, t.Pos); ok {
				return lt
			}
			switch t.Name {
			case unknownTypeArg:
				unsup(t.Pos, "unknown-type-arg")
			case inferTypeArg:
				return "_"
			}
		}
		if len(t.Args) > 0 || g.isGenericDecl(t.Name) {
			args := make([]string, len(t.Args))
			for i, a := range t.Args {
				args[i] = g.leanType(a)
			}
			if lt := g.genericType(t.Name, args, t.Pos); lt != "" {
				return lt
			}
			g.hashType(t.Name, t.Pos)
			unsup(t.Pos, "generic-type")
		}
		switch t.Name {
		case "int", "int64":
			return "Int64"
		case "string":
			return "String"
		case "bool":
			return "Bool"
		}
		if g.declaredType(t.Name) {
			g.use(t.Name)
			return ident(t.Name)
		}
		unsup(t.Pos, "type-%s", t.Name)
	case *coreexport.TypeFunc:
		return g.funcTypeLean(t)
	case *coreexport.Unsupported:
		unsup(t.Pos, "%s", t.What)
	}
	unsup(posOf(t), "type-%s", t.Kind())
	return ""
}

// typeName is the name of a plain type, "" for any other.
func typeName(t coreexport.Node) string {
	if n, ok := t.(*coreexport.TypeName); ok && len(n.Args) == 0 {
		return n.Name
	}
	return ""
}

// zero is the zero value of type t (docs/GALA.MD, "Default Values"): Go's
// for int, string and bool, each field's zero for a struct, and the default
// case for a sealed type.
func (g *gen) zero(t coreexport.Node) string {
	return g.zeroDepth(t, 0)
}

func (g *gen) zeroDepth(t coreexport.Node, depth int) string {
	if depth > 64 {
		unsup(posOf(t), "zero-value-depth")
	}
	if _, ok := t.(*coreexport.TypeFunc); ok {
		// A function's zero value is nil.
		unsup(posOf(t), "nil")
	}
	lt := g.leanType(t)
	switch lt {
	case "Int64":
		return "(0 : Int64)"
	case "String":
		return `""`
	case "Bool":
		return "false"
	}
	name := typeHead(t)
	if _, isParam := g.tparams[name]; isParam && len(t.(*coreexport.TypeName).Args) == 0 {
		// Lean's default is not Go's zero value for every type argument.
		unsup(posOf(t), "type-param-zero")
	}
	// A generic type's zero value says which type it is: Lean cannot tell
	// from `GOption.None` alone.
	typed := func(v string) string {
		switch {
		case strings.HasPrefix(lt, "("):
			return "(" + v + " : " + lt + ")"
		case strings.Contains(v, " ") || strings.HasSuffix(v, ".mk"):
			return "(" + v + ")"
		}
		return v
	}
	if !g.declaredType(name) {
		st, _ := g.stdType(name)
		switch std := st.lean; std {
		case "GArray", "GList":
			// An empty Array or List.
			return typed(std + ".mk []")
		case "GOption":
			// None is Option's default case.
			return typed("GOption.None")
		case "T2", "T3", "T4":
			parts := []string{std + ".mk"}
			for _, a := range t.(*coreexport.TypeName).Args {
				parts = append(parts, g.zeroDepth(a, depth+1))
			}
			return typed(strings.Join(parts, " "))
		}
		unsup(posOf(t), "sealed-zero-value")
	}
	args := g.typeArgsOf(name, t)
	if s := g.structs[name]; s != nil {
		parts := []string{ident(name) + ".mk"}
		for _, f := range fieldsOf(s.Fields) {
			if f.Type == nil {
				unsup(f.Pos, "untyped-field")
			}
			parts = append(parts, g.zeroDepth(substType(f.Type, args), depth+1))
		}
		return typed(strings.Join(parts, " "))
	}
	s := g.sealeds[name]
	for _, vn := range s.Variants {
		v := vn.(*coreexport.Variant)
		if !v.Default {
			continue
		}
		parts := []string{ident(name) + "." + ident(v.Name)}
		for _, f := range fieldsOf(v.Fields) {
			if f.Type == nil {
				unsup(f.Pos, "untyped-field")
			}
			if slices.Contains(typeNamesIn(f.Type), name) {
				unsup(f.Pos, "sealed-zero-value")
			}
			parts = append(parts, g.zeroDepth(substType(f.Type, args), depth+1))
		}
		if len(parts) == 1 {
			return typed(parts[0])
		}
		return typed(strings.Join(parts, " "))
	}
	unsup(s.Pos, "sealed-zero-value")
	return ""
}

// declaredType reports whether the program declares a struct or sealed type
// named name.
func (g *gen) declaredType(name string) bool {
	return g.structs[name] != nil || g.sealeds[name] != nil
}

// funcTypeNames is every plain type name in function type t, nested
// function types included.
func funcTypeNames(t *coreexport.TypeFunc) []string {
	var out []string
	for _, p := range append(slices.Clone(t.Params), t.Result) {
		out = append(out, typeNamesIn(p)...)
	}
	return out
}

// typeNamesIn is every type name in type t: its own, its type arguments',
// and those in function types, at any depth.
func typeNamesIn(t coreexport.Node) []string {
	switch t := t.(type) {
	case *coreexport.TypeName:
		out := []string{t.Name}
		for _, a := range t.Args {
			out = append(out, typeNamesIn(a)...)
		}
		return out
	case *coreexport.TypeFunc:
		return funcTypeNames(t)
	}
	return nil
}

// typeOrder sorts g.typeNames so that each type comes after the types its
// fields use. A type may use itself (a recursive sealed type); two types
// that use each other are not translated.
func (g *gen) typeOrder() {
	deps := map[string][]string{}
	for _, n := range g.typeNames {
		for _, f := range g.typeFields(n) {
			// Every type a field's type names comes first. A type may use
			// itself directly (`Tail Chain[T]`), but not inside a type
			// argument (`Next Option[Node]`, a nested inductive) or a
			// function type: Lean has no type that holds a function taking
			// or giving itself (it is not strictly positive).
			var inner []string
			nested := "nested-recursive-type"
			switch t := f.Type.(type) {
			case *coreexport.TypeName:
				if t.Name != n && !g.isTypeParamOf(n, t.Name) {
					deps[n] = append(deps[n], t.Name)
				}
				for _, a := range t.Args {
					inner = append(inner, g.fieldTypeNames(n, a)...)
				}
			case *coreexport.TypeFunc:
				inner, nested = g.fieldTypeNames(n, t), "recursive-func-field"
			}
			for _, t := range inner {
				if t == n {
					unsup(f.Pos, "%s", nested)
				}
				deps[n] = append(deps[n], t)
			}
		}
	}
	state := map[string]int{}
	var order []string
	var visit func(n string)
	visit = func(n string) {
		if g.structs[n] == nil && g.sealeds[n] == nil {
			return
		}
		switch state[n] {
		case 1:
			unsup(g.typePos(n), "mutually-recursive-types")
		case 2:
			return
		}
		state[n] = 1
		for _, d := range deps[n] {
			visit(d)
		}
		state[n] = 2
		order = append(order, n)
	}
	for _, n := range g.typeNames {
		visit(n)
	}
	g.typeNames = order
}

func (g *gen) typePos(n string) coreexport.Pos {
	if s := g.structs[n]; s != nil {
		return s.Pos
	}
	return g.sealeds[n].Pos
}

// typeFields is every field of a struct, or of every case of a sealed type.
func (g *gen) typeFields(n string) []*coreexport.Field {
	if s := g.structs[n]; s != nil {
		return fieldsOf(s.Fields)
	}
	var fs []*coreexport.Field
	for _, vn := range g.sealeds[n].Variants {
		fs = append(fs, fieldsOf(vn.(*coreexport.Variant).Fields)...)
	}
	return fs
}

// typeDecls declares every struct and sealed type, with how its values
// print.
// The types holding a function come apart (fnTypes): a function is an action
// over the program state, so they follow the state, which may hold only the
// others.
func (g *gen) typeDecls() (plain, fnTypes string, ok bool) {
	var b, f strings.Builder
	ok = true
	for _, n := range g.typeNames {
		var d string
		// Each type is reported as a declaration of its own, so that what
		// uses it can be told apart from what does not (reach).
		ok = g.guard(n, func() {
			if s := g.structs[n]; s != nil {
				d = g.structDecl(s)
			} else {
				d = g.sealedDecl(g.sealeds[n])
			}
		}) && ok
		if g.noBEq[n] {
			f.WriteString(d)
		} else {
			b.WriteString(d)
		}
	}
	return b.String(), f.String(), ok
}

// stateType checks that a value of Lean type t, a package-level variable or
// a captured var, can live in the program state: it must not hold a
// function, which is itself an action over the state.
func (g *gen) stateType(t string, pos coreexport.Pos) {
	if strings.Contains(t, "→") {
		unsup(pos, "func-in-state")
	}
	if g.mentionsTypeParam(t) {
		unsup(pos, "generic-in-state")
	}
	g.stateTypeIn(t, g.noBEq, pos, "func-in-state")
}

// stateTypeIn stops the translation with construct when Lean type t names
// one of types.
func (g *gen) stateTypeIn(t string, types map[string]bool, pos coreexport.Pos, construct string) {
	for _, w := range leanWords(t) {
		for n := range types {
			if w == ident(n) {
				unsup(pos, "%s", construct)
			}
		}
	}
}

// directFn reports whether one of fields has a function type.
func (g *gen) directFn(fields []*coreexport.Field) bool {
	for _, f := range fields {
		if _, ok := f.Type.(*coreexport.TypeFunc); ok {
			return true
		}
	}
	return false
}

// fieldType is a field's Lean type; every field has a declared type.
func (g *gen) fieldType(f *coreexport.Field) string {
	if f.Type == nil {
		unsup(f.Pos, "untyped-field")
	}
	return g.leanType(f.Type)
}

// structDecl is a struct as a Lean structure. A shorthand struct prints
// through the String method GALA generates for it, `Pt(1, 2)`, unless it has
// a field named String; any other struct prints as Go prints its fields,
// `{1 2}` (docs/GALA.MD, "How Values Print"; galaspec's Gala/Print.lean).
func (g *gen) structDecl(s *coreexport.Struct) string {
	tps := g.typeParams(s.TypeParams)
	return g.inTypeParams(tps, func() string { return g.structDeclIn(s, tps) })
}

func (g *gen) structDeclIn(s *coreexport.Struct, tps []typeParam) string {
	name := ident(s.Name)
	self := selfType(s.Name, tps)
	fields := fieldsOf(s.Fields)
	var b strings.Builder
	fmt.Fprintf(&b, "structure %s%s where\n", name, typeBinders(tps))
	if len(fields) == 0 {
		b.WriteString("  mk ::\n")
	}
	var shows, plain, raws []string
	hasString, hasFn, unprintable := false, false, false
	for _, f := range fields {
		fmt.Fprintf(&b, "  %s : %s\n", ident(f.Name), g.fieldType(f))
		v := "s." + ident(f.Name)
		_, isFn := f.Type.(*coreexport.TypeFunc)
		hasFn = hasFn || isFn || g.anyIn(s.Name, f.Type, g.noBEq)
		unprintable = unprintable || g.anyIn(s.Name, f.Type, g.unprintable)
		show, raw := "gshow "+v, fmt.Sprintf("wrapImm %t (graw %s)", f.Mutable, v)
		if isFn {
			// Inside a generated String a function prints as <fn>; anywhere
			// else fmt prints its address (unprintable below).
			show, raw = "fnText", "fnText"
		}
		shows, raws = append(shows, show), append(raws, raw)
		if isUpper(f.Name) {
			plain = append(plain, show)
		} else {
			plain = append(plain, raw)
		}
		hasString = hasString || f.Name == "String"
	}
	derive := "BEq, Inhabited"
	if hasFn {
		// Functions do not compare; GALA rejects `==` on such a struct.
		derive = "Inhabited"
		g.noBEq[s.Name] = true
	}
	fmt.Fprintf(&b, "  deriving %s\n\n", derive)
	show := `"{" ++ " ".intercalate [` + strings.Join(plain, ", ") + `] ++ "}"`
	generated := s.Shorthand && !hasString
	if generated {
		show = fmt.Sprintf(`"%s(" ++ ", ".intercalate [%s] ++ ")"`, bareName(s.Name), strings.Join(shows, ", "))
	}
	if g.directFn(fields) && (!generated || g.rawPrinted()[s.Name]) || unprintable {
		g.unprintable[s.Name] = true
	}
	fmt.Fprintf(&b, "instance%s : GShow %s where\n  gshow s := %s\n  graw s := \"{\" ++ \" \".intercalate [%s] ++ \"}\"\n\n",
		instBinders(tps), self, show, strings.Join(raws, ", "))
	return b.String()
}

// sealedDecl is a sealed type as a Lean inductive, with an accessor for each
// field some select reads, and how its values print: through the String
// method GALA generates, `Case(f1, f2)`.
func (g *gen) sealedDecl(s *coreexport.Sealed) string {
	tps := g.typeParams(s.TypeParams)
	return g.inTypeParams(tps, func() string { return g.sealedDeclIn(s, tps) })
}

func (g *gen) sealedDeclIn(s *coreexport.Sealed, tps []typeParam) string {
	name := ident(s.Name)
	self := selfType(s.Name, tps)
	var b strings.Builder
	fmt.Fprintf(&b, "inductive %s%s where\n", name, typeBinders(tps))
	type fieldUse struct {
		variant string
		index   int
		total   int
	}
	uses := map[string][]fieldUse{}
	fieldTy := map[string]coreexport.Node{}
	var arms []string
	hasFn, unprintable := false, false
	for _, vn := range s.Variants {
		v := vn.(*coreexport.Variant)
		vname := ident(v.Name)
		fmt.Fprintf(&b, "  | %s", vname)
		fields := fieldsOf(v.Fields)
		var binds, shows []string
		for i, f := range fields {
			ft := g.fieldType(f)
			fmt.Fprintf(&b, " (%s : %s)", ident(f.Name), ft)
			a := fmt.Sprintf("a%d", i)
			sh := "GShow.gshow " + a
			_, isFn := f.Type.(*coreexport.TypeFunc)
			hasFn = hasFn || isFn || g.anyIn(s.Name, f.Type, g.noBEq)
			unprintable = unprintable || g.anyIn(s.Name, f.Type, g.unprintable) || isFn && g.rawPrinted()[s.Name]
			switch {
			case typeHead(f.Type) == s.Name:
				sh = name + "._gala_show " + a
			case isFn:
				sh = "fnText"
			}
			binds, shows = append(binds, a), append(shows, sh)
			if prev, ok := fieldTy[f.Name]; ok && g.leanType(prev) != ft {
				unsup(f.Pos, "sealed-field-types-differ")
			}
			fieldTy[f.Name] = f.Type
			uses[f.Name] = append(uses[f.Name], fieldUse{v.Name, i, len(fields)})
		}
		b.WriteString("\n")
		pattern := "." + vname
		if len(binds) > 0 {
			pattern += " " + strings.Join(binds, " ")
		}
		arms = append(arms, fmt.Sprintf("  | %s => \"%s(\" ++ \", \".intercalate [%s] ++ \")\"", pattern, v.Name, strings.Join(shows, ", ")))
	}
	if hasFn {
		b.WriteString("  deriving Inhabited\n\n")
		g.noBEq[s.Name] = true
	} else {
		b.WriteString("  deriving BEq, Inhabited\n\n")
	}
	if unprintable {
		g.unprintable[s.Name] = true
	}
	fmt.Fprintf(&b, "/-- How a %s prints: the String method GALA generates for it. -/\ndef %s._gala_show%s : %s → String\n%s\n\n",
		s.Name, name, instBinders(tps), self, strings.Join(arms, "\n"))
	fmt.Fprintf(&b, "instance%s : GShow %s where\n  gshow := %s._gala_show\n", instBinders(tps), self, name)
	if g.rawPrinted()[s.Name] && !g.rawGeneric[s.Name] {
		fmt.Fprintf(&b, "  graw v := match v with\n%s\n", strings.Join(g.sealedRaw(s), "\n"))
	}
	b.WriteString("\n")
	// A field read on a value whose case lacks it is the field type's zero
	// value, as in the Go struct that holds every case.
	for _, fname := range slices.Sorted(maps.Keys(uses)) {
		if !g.selected[fname] {
			continue
		}
		ft := fieldTy[fname]
		fmt.Fprintf(&b, "def %s.%s%s : %s → %s\n", name, ident(fname),
			typeOnlyBinders(tps), self, g.leanType(ft))
		for _, u := range uses[fname] {
			args := make([]string, u.total)
			for i := range args {
				args[i] = "_"
			}
			args[u.index] = "x"
			fmt.Fprintf(&b, "  | .%s %s => x\n", ident(u.variant), strings.Join(args, " "))
		}
		if len(uses[fname]) < len(s.Variants) {
			if typeHead(ft) == s.Name {
				unsup(s.Pos, "sealed-self-field-read")
			}
			fmt.Fprintf(&b, "  | _ => %s\n", g.zero(ft))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// globalType is the Lean type of a package-level variable: its declared
// type, or the type of its initializer where that is plain to see.
func (g *gen) globalType(gv *globalVar) string {
	if gv.typ != nil {
		return g.leanType(gv.typ)
	}
	if t := g.exprType(gv.value, nil, map[string]bool{gv.name: true}); t != "" {
		return t
	}
	unsup(gv.pos, "untyped-global")
	return ""
}

// galaType is the GALA type whose Lean type is t, for the types a name
// spells (int, string, bool, the program's structs and sealed types); nil
// for any other.
func (g *gen) galaType(t string) coreexport.Node {
	if t == "" {
		return nil
	}
	return g.galaTypeOf(parseLean(t))
}

// galaTypeOf is galaType of a parsed Lean type: a basic type, a program
// type, a standard-library generic type, a type parameter in scope, or a
// generic type applied to such types.
func (g *gen) galaTypeOf(t ltype) coreexport.Node {
	if t.result != nil {
		return nil
	}
	var name string
	switch t.head {
	case "Int64":
		name = "int"
	case "String":
		name = "string"
	case "Bool":
		name = "bool"
	default:
		name = g.typeByLean(t.head)
		for std := range stdGeneric {
			if st, ok := g.stdType(std); ok && st.lean == t.head {
				name = std
			}
		}
		for x, lt := range g.tparams {
			if lt == g.tpLean(x) && t.head == lt && len(t.args) == 0 {
				name = x
			}
		}
	}
	if name == "" {
		return nil
	}
	out := &coreexport.TypeName{Name: name, Args: []coreexport.Node{}}
	for _, a := range t.args {
		an := g.galaTypeOf(a)
		if an == nil {
			return nil
		}
		out.Args = append(out.Args, an)
	}
	return out
}

// typeByLean is the program's struct or sealed type whose Lean name is t,
// "" for none.
func (g *gen) typeByLean(t string) string {
	if g.leanNames == nil {
		g.leanNames = map[string]string{}
		for _, n := range g.typeNames {
			g.leanNames[ident(n)] = n
		}
	}
	return g.leanNames[t]
}

// optTypeSafe is the Lean type of optional type t, "" when absent or
// outside the subset.
func (g *gen) optTypeSafe(t coreexport.Node) string {
	return g.safeType(func() string { return g.optType(t) })
}

// typeOf is exprType of e in sc, where plain to see.
func (g *gen) typeOf(e coreexport.Node, sc *scope) string {
	return g.exprType(e, sc, map[string]bool{})
}

// declared reports whether the program declares callable x itself.
func (g *gen) declared(x string) bool {
	return g.funcs[x] != nil || g.structs[x] != nil || g.variants[x] != nil
}

// fieldByType is field name of the struct or sealed type whose Lean name is
// t, or nil.
func (g *gen) fieldByType(t, name string) *coreexport.Field {
	if t == "" {
		return nil
	}
	n := g.typeByLean(parseLean(t).head)
	if n == "" {
		return nil
	}
	for _, f := range g.typeFields(n) {
		if f.Name != name {
			continue
		}
		if !g.isGenericDecl(n) {
			return f
		}
		// A generic type's field, with the value's type arguments put in.
		ft := g.fieldTypeIn(n, f, g.galaType(t))
		if ft == nil {
			return nil
		}
		c := *f
		c.Type = ft
		return &c
	}
	return nil
}

// exprType is the Lean type of expression e in scope sc (nil for a
// package-level initializer), "" when it is not plain to see from the
// declarations: literals, operators, locals and globals of a known type,
// calls, constructors and fields. seen guards against a cycle of globals.
func (g *gen) exprType(e coreexport.Node, sc *scope, seen map[string]bool) string {
	if n, ok := e.(*coreexport.Name); ok && sc != nil {
		if l := sc.lookup(n.Name); l != nil {
			return l.typ
		}
	}
	switch e := e.(type) {
	case *coreexport.Select:
		if f := g.fieldByType(g.exprType(e.Target, sc, seen), e.Name); f != nil && f.Type != nil {
			return g.leanType(f.Type)
		}
		return ""
	case *coreexport.Int:
		return "Int64"
	case *coreexport.String, *coreexport.Interp:
		return "String"
	case *coreexport.Bool:
		return "Bool"
	case *coreexport.Paren:
		return g.exprType(e.Expr, sc, seen)
	case *coreexport.Unary:
		if e.Op == "!" {
			return "Bool"
		}
		return g.exprType(e.Operand, sc, seen)
	case *coreexport.Binary:
		switch e.Op {
		case "==", "!=", "<", "<=", ">", ">=", "&&", "||":
			return "Bool"
		}
		if t := g.exprType(e.Left, sc, seen); t != "" {
			return t
		}
		return g.exprType(e.Right, sc, seen)
	case *coreexport.IfExpr:
		return g.exprType(e.Then, sc, seen)
	case *coreexport.Name:
		if gv := g.globalBy[e.Name]; gv != nil && !seen[e.Name] {
			if gv.typ != nil {
				return g.leanType(gv.typ)
			}
			seen[e.Name] = true
			return g.exprType(gv.value, sc, seen)
		}
		if v := g.variants[e.Name]; v != nil && len(v.fields) == 0 && !g.isGenericDecl(v.sealed.Name) {
			return ident(v.sealed.Name)
		}
		if f := g.funcs[e.Name]; f != nil {
			return g.funcDeclType(f)
		}
	case *coreexport.Lambda:
		return g.lambdaType(e, sc)
	case *coreexport.Call:
		if s, isSel := e.Callee.(*coreexport.Select); isSel {
			// A method: its declared result, or a generated Copy or Equal.
			if f, _ := g.methodOf(s.Target, s.Name, sc, seen); f != nil {
				return g.safeType(func() string { return g.leanType(f.Result) })
			}
			if t := g.seqCallType(e, s, sc, seen); t != "" {
				return t
			}
			switch s.Name {
			case "Copy":
				return g.exprType(s.Target, sc, seen)
			case "Equal":
				return "Bool"
			}
		}
		n, ok := e.Callee.(*coreexport.Name)
		if !ok || sc != nil && sc.lookup(n.Name) != nil {
			// A function value: the result of its type.
			if r := funcResult(g.exprType(e.Callee, sc, seen)); r != "_" {
				return r
			}
			return ""
		}
		switch {
		case g.collections && seqCtors[n.Name] != "" && !g.declared(n.Name):
			return g.seqCtorType(n.Name, e, sc, seen)
		case n.Name == "Some" && !g.declared("Some") && len(e.Args) == 1:
			if t := g.exprType(argNode(e.Args[0]), sc, seen); t != "" {
				return "(GOption " + t + ")"
			}
		case g.funcs[n.Name] != nil:
			f := g.funcs[n.Name]
			if len(f.TypeParams) > 0 {
				return g.instType(f.Result, g.instFunc(f, e.Args, sc, seen))
			}
			return g.leanType(f.Result)
		case g.structs[n.Name] != nil:
			return g.ctorType(n.Name, fieldsOf(g.structs[n.Name].Fields), e.Args, sc, seen)
		case g.variants[n.Name] != nil:
			v := g.variants[n.Name]
			return g.ctorType(v.sealed.Name, v.fields, e.Args, sc, seen)
		}
	case *coreexport.Tuple:
		if len(e.Elems) > 4 {
			return ""
		}
		parts := make([]string, len(e.Elems))
		for i, el := range e.Elems {
			if parts[i] = g.exprType(el, sc, seen); parts[i] == "" {
				return ""
			}
		}
		return fmt.Sprintf("(T%d %s)", len(parts), strings.Join(parts, " "))
	}
	return ""
}

// rawPrinted is the set of types whose values `fmt` can print raw, as Go
// structs: the types of the fields `fmt` reaches without methods (a lower-case
// field of a struct that prints its fields), and the types of their fields.
func (g *gen) rawPrinted() map[string]bool {
	if g.raw != nil {
		return g.raw
	}
	g.raw = map[string]bool{}
	g.rawGeneric = map[string]bool{}
	// reach records that fmt reaches a value of type t raw inside a value
	// of program type owner. Only plain types are followed: a generic type,
	// a type parameter, Option or a tuple prints raw as a Go struct whose
	// text the prelude does not model, so owner's raw text is not
	// translated (rawGeneric).
	var add func(t string)
	reach := func(owner string, t coreexport.Node) {
		switch t := t.(type) {
		case *coreexport.TypeFunc:
			return
		case *coreexport.TypeName:
			if len(t.Args) == 0 && !g.isGenericDecl(t.Name) && !g.isTypeParamOf(owner, t.Name) {
				add(t.Name)
				return
			}
		}
		g.rawGeneric[owner] = true
	}
	add = func(t string) {
		if g.raw[t] || (g.structs[t] == nil && g.sealeds[t] == nil) {
			return
		}
		g.raw[t] = true
		if g.isGenericDecl(t) {
			g.rawGeneric[t] = true
		}
		for _, f := range g.typeFields(t) {
			reach(t, f.Type)
		}
	}
	for _, n := range g.typeNames {
		s := g.structs[n]
		if s == nil {
			continue
		}
		fields := fieldsOf(s.Fields)
		plain := !s.Shorthand
		for _, f := range fields {
			plain = plain || f.Name == "String"
		}
		if !plain {
			continue
		}
		for _, f := range fields {
			if !isUpper(f.Name) {
				reach(n, f.Type)
			}
		}
	}
	return g.raw
}

// sealedLayout is the fields of the Go struct that holds a sealed type's
// values: every case's fields in order, each name once.
func (g *gen) sealedLayout(s *coreexport.Sealed) []*coreexport.Field {
	var out []*coreexport.Field
	seen := map[string]bool{}
	for _, vn := range s.Variants {
		for _, f := range fieldsOf(vn.(*coreexport.Variant).Fields) {
			if !seen[f.Name] {
				seen[f.Name] = true
				out = append(out, f)
			}
		}
	}
	return out
}

// caseTag is the Go tag of a case: 0 for the default case, else 1, 2, ... in
// declaration order over the other cases.
func caseTag(s *coreexport.Sealed, name string) int {
	tag := 0
	for _, vn := range s.Variants {
		v := vn.(*coreexport.Variant)
		if v.Default {
			if v.Name == name {
				return 0
			}
			continue
		}
		tag++
		if v.Name == name {
			return tag
		}
	}
	return 0
}

// sealedRaw is the match arms of a sealed type's raw text: its Go struct,
// each layout field in an Immutable holding the active case's value or the
// field type's zero value, then the tag. A field of the sealed type itself is
// a pointer, whose address `fmt` would print: not translated.
func (g *gen) sealedRaw(s *coreexport.Sealed) []string {
	layout := g.sealedLayout(s)
	var arms []string
	for _, vn := range s.Variants {
		v := vn.(*coreexport.Variant)
		fields := fieldsOf(v.Fields)
		binds := make([]string, len(fields))
		at := map[string]int{}
		for i, f := range fields {
			binds[i] = fmt.Sprintf("a%d", i)
			at[f.Name] = i
		}
		var parts []string
		for _, f := range layout {
			if typeName(f.Type) == s.Name {
				unsup(f.Pos, "print-pointer")
			}
			if i, ok := at[f.Name]; ok {
				if _, isFn := f.Type.(*coreexport.TypeFunc); isFn {
					parts = append(parts, "fnText")
				} else {
					parts = append(parts, "wrapImm false (graw "+binds[i]+")")
				}
			} else {
				parts = append(parts, strLit("{"+g.rawZero(f.Type, 0)+"}", f.Pos))
			}
		}
		parts = append(parts, strLit(fmt.Sprint(caseTag(s, v.Name)), s.Pos))
		pattern := "." + ident(v.Name)
		if len(binds) > 0 {
			pattern += " " + strings.Join(binds, " ")
		}
		arms = append(arms, fmt.Sprintf("    | %s => \"{\" ++ \" \".intercalate [%s] ++ \"}\"", pattern, strings.Join(parts, ", ")))
	}
	return arms
}

// rawZero is the raw text of type t's zero value.
func (g *gen) rawZero(t coreexport.Node, depth int) string {
	if depth > 64 {
		unsup(posOf(t), "zero-value-depth")
	}
	if _, ok := t.(*coreexport.TypeFunc); ok {
		return "<nil>"
	}
	switch g.leanType(t) {
	case "Int64":
		return "0"
	case "String":
		return ""
	case "Bool":
		return "false"
	}
	name := typeName(t)
	if s := g.structs[name]; s != nil {
		var parts []string
		for _, f := range fieldsOf(s.Fields) {
			parts = append(parts, wrapImm(f.Mutable, g.rawZero(f.Type, depth+1)))
		}
		return "{" + strings.Join(parts, " ") + "}"
	}
	s := g.sealeds[name]
	var parts []string
	for _, f := range g.sealedLayout(s) {
		if typeName(f.Type) == name {
			parts = append(parts, "<nil>")
		} else {
			parts = append(parts, "{"+g.rawZero(f.Type, depth+1)+"}")
		}
	}
	return "{" + strings.Join(append(parts, "0"), " ") + "}"
}

func wrapImm(mutable bool, s string) string {
	if mutable {
		return s
	}
	return "{" + s + "}"
}
