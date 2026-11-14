package gala2lean

import (
	"fmt"
	"strings"

	"martianoff/gala/internal/formal/coreexport"
)

// leanType is GALA type t in Lean.
func (g *gen) leanType(t coreexport.Node) string {
	switch t := t.(type) {
	case nil:
		return "Unit"
	case *coreexport.TypeName:
		switch t.Name {
		case "int", "int64":
			return "Int64"
		case "string":
			return "String"
		case "bool":
			return "Bool"
		}
		if g.structs[t.Name] != nil || g.sealeds[t.Name] != nil {
			return ident(t.Name)
		}
		unsup(t.Pos, "type-%s", t.Name)
	case *coreexport.TypeFunc:
		unsup(t.Pos, "type-func")
	case *coreexport.Unsupported:
		unsup(t.Pos, "%s", t.What)
	}
	unsup(posOf(t), "type-%s", t.Kind())
	return ""
}

// typeName is the name of a plain type, "" for any other.
func typeName(t coreexport.Node) string {
	if n, ok := t.(*coreexport.TypeName); ok {
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
	lt := g.leanType(t)
	switch lt {
	case "Int64":
		return "(0 : Int64)"
	case "String":
		return `""`
	case "Bool":
		return "false"
	}
	name := typeName(t)
	if s := g.structs[name]; s != nil {
		parts := []string{ident(name) + ".mk"}
		for _, f := range fieldsOf(s.Fields) {
			if f.Type == nil {
				unsup(f.Pos, "untyped-field")
			}
			parts = append(parts, g.zeroDepth(f.Type, depth+1))
		}
		return "(" + strings.Join(parts, " ") + ")"
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
			if typeName(f.Type) == name {
				unsup(f.Pos, "sealed-zero-value")
			}
			parts = append(parts, g.zeroDepth(f.Type, depth+1))
		}
		if len(parts) == 1 {
			return parts[0]
		}
		return "(" + strings.Join(parts, " ") + ")"
	}
	unsup(s.Pos, "sealed-zero-value")
	return ""
}

// typeOrder sorts g.typeNames so that each type comes after the types its
// fields use. A type may use itself (a recursive sealed type); two types
// that use each other are not translated.
func (g *gen) typeOrder() {
	deps := map[string][]string{}
	for _, n := range g.typeNames {
		for _, f := range g.typeFields(n) {
			if t := typeName(f.Type); t != "" && t != n {
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
func (g *gen) typeDecls() string {
	var b strings.Builder
	for _, n := range g.typeNames {
		if s := g.structs[n]; s != nil {
			b.WriteString(g.structDecl(s))
		} else {
			b.WriteString(g.sealedDecl(g.sealeds[n]))
		}
	}
	return b.String()
}

// fieldType is a field's Lean type; every field has a declared type.
func (g *gen) fieldType(f *coreexport.Field) string {
	if f.Type == nil {
		unsup(f.Pos, "untyped-field")
	}
	if f.Default != nil {
		unsup(f.Pos, "field-default")
	}
	return g.leanType(f.Type)
}

// structDecl is a struct as a Lean structure. A shorthand struct prints
// through the String method GALA generates for it, `Pt(1, 2)`, unless it has
// a field named String; any other struct prints as Go prints its fields,
// `{1 2}` (docs/GALA.MD, "How Values Print"; galaspec's Gala/Print.lean).
func (g *gen) structDecl(s *coreexport.Struct) string {
	name := ident(s.Name)
	fields := fieldsOf(s.Fields)
	var b strings.Builder
	fmt.Fprintf(&b, "structure %s where\n", name)
	if len(fields) == 0 {
		b.WriteString("  mk ::\n")
	}
	var shows, plain, raws []string
	hasString := false
	for _, f := range fields {
		fmt.Fprintf(&b, "  %s : %s\n", ident(f.Name), g.fieldType(f))
		v := "s." + ident(f.Name)
		shows = append(shows, "gshow "+v)
		raw := fmt.Sprintf("wrapImm %t (graw %s)", f.Mutable, v)
		raws = append(raws, raw)
		if isUpper(f.Name) {
			plain = append(plain, "gshow "+v)
		} else {
			plain = append(plain, raw)
		}
		hasString = hasString || f.Name == "String"
	}
	b.WriteString("  deriving BEq, Inhabited\n\n")
	show := `"{" ++ " ".intercalate [` + strings.Join(plain, ", ") + `] ++ "}"`
	if s.Shorthand && !hasString {
		show = fmt.Sprintf(`"%s(" ++ ", ".intercalate [%s] ++ ")"`, s.Name, strings.Join(shows, ", "))
	}
	fmt.Fprintf(&b, "instance : GShow %s where\n  gshow s := %s\n  graw s := \"{\" ++ \" \".intercalate [%s] ++ \"}\"\n\n",
		name, show, strings.Join(raws, ", "))
	return b.String()
}

// sealedDecl is a sealed type as a Lean inductive, with an accessor for each
// field some select reads, and how its values print: through the String
// method GALA generates, `Case(f1, f2)`.
func (g *gen) sealedDecl(s *coreexport.Sealed) string {
	name := ident(s.Name)
	var b strings.Builder
	fmt.Fprintf(&b, "inductive %s where\n", name)
	type fieldUse struct {
		variant string
		index   int
		total   int
	}
	uses := map[string][]fieldUse{}
	fieldTy := map[string]coreexport.Node{}
	var arms []string
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
			if typeName(f.Type) == s.Name {
				sh = name + "._gala_show " + a
			}
			binds, shows = append(binds, a), append(shows, sh)
			if prev, ok := fieldTy[f.Name]; ok && typeName(prev) != typeName(f.Type) {
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
	b.WriteString("  deriving BEq, Inhabited\n\n")
	fmt.Fprintf(&b, "/-- How a %s prints: the String method GALA generates for it. -/\ndef %s._gala_show : %s → String\n%s\n\n",
		s.Name, name, name, strings.Join(arms, "\n"))
	fmt.Fprintf(&b, "instance : GShow %s where\n  gshow := %s._gala_show\n", name, name)
	if g.rawPrinted()[s.Name] {
		fmt.Fprintf(&b, "  graw v := match v with\n%s\n", strings.Join(g.sealedRaw(s), "\n"))
	}
	b.WriteString("\n")
	// A field read on a value whose case lacks it is the field type's zero
	// value, as in the Go struct that holds every case.
	for _, fname := range sortedKeys(uses) {
		if !g.selected[fname] {
			continue
		}
		ft := fieldTy[fname]
		fmt.Fprintf(&b, "def %s.%s : %s → %s\n", name, ident(fname), name, g.leanType(ft))
		for _, u := range uses[fname] {
			args := make([]string, u.total)
			for i := range args {
				args[i] = "_"
			}
			args[u.index] = "x"
			fmt.Fprintf(&b, "  | .%s %s => x\n", ident(u.variant), strings.Join(args, " "))
		}
		if len(uses[fname]) < len(s.Variants) {
			if typeName(ft) == s.Name {
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
		t := g.exprType(e.Target, sc, seen)
		for _, n := range g.typeNames {
			if ident(n) != t {
				continue
			}
			for _, f := range g.typeFields(n) {
				if f.Name == e.Name && f.Type != nil {
					return g.leanType(f.Type)
				}
			}
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
		if v := g.variants[e.Name]; v != nil && len(v.fields) == 0 {
			return ident(v.sealed.Name)
		}
	case *coreexport.Call:
		n, ok := e.Callee.(*coreexport.Name)
		if !ok {
			return ""
		}
		switch {
		case g.funcs[n.Name] != nil:
			return g.leanType(g.funcs[n.Name].Result)
		case g.structs[n.Name] != nil:
			return ident(n.Name)
		case g.variants[n.Name] != nil:
			return ident(g.variants[n.Name].sealed.Name)
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
	var add func(t string)
	add = func(t string) {
		if g.raw[t] || (g.structs[t] == nil && g.sealeds[t] == nil) {
			return
		}
		g.raw[t] = true
		for _, f := range g.typeFields(t) {
			add(typeName(f.Type))
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
				add(typeName(f.Type))
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
				parts = append(parts, "wrapImm false (graw "+binds[i]+")")
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
