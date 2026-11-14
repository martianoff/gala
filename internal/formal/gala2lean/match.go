package gala2lean

import (
	"fmt"
	"reflect"
	"strings"

	"martianoff/gala/internal/formal/coreexport"
)

// metaPos is the position of a supported node (every one embeds Meta).
func metaPos(n coreexport.Node) coreexport.Pos {
	v := reflect.ValueOf(n)
	if v.Kind() == reflect.Pointer && !v.IsNil() {
		if m := v.Elem().FieldByName("Meta"); m.IsValid() {
			return m.Interface().(coreexport.Meta).Pos
		}
	}
	return coreexport.Pos{}
}

// pat is a translated pattern: the Lean pattern, the equality tests that
// literal and stable-identifier patterns leave (pure Bool terms, in source
// order), and whether Lean's pattern alone can fail to match.
type pat struct {
	lean      string
	tests     []string
	refutable bool
	binders   []string
}

// pattern translates GALA pattern p. sc is the scope the match is in (for
// stable identifiers).
func (g *gen) pattern(p coreexport.Node, sc *scope) pat {
	switch p := p.(type) {
	case *coreexport.Unsupported:
		unsup(p.Pos, "%s", p.What)
	case *coreexport.PatWild:
		return pat{lean: "_"}
	case *coreexport.PatName:
		x := p.Name
		if v := g.variants[x]; v != nil {
			if len(v.fields) != 0 {
				unsup(p.Pos, "bare-pattern-with-fields")
			}
			return pat{lean: ident(v.sealed.Name) + "." + ident(x), refutable: len(v.sealed.Variants) > 1}
		}
		// An upper-case name that resolves to a value is a stable
		// identifier: the subject must equal it.
		if isUpper(x) && (sc.lookup(x) != nil || g.globalBy[x] != nil) {
			g.basic(&coreexport.Name{Meta: coreexport.Meta{Kind: "name", Pos: p.Pos}, Name: x}, sc)
			f := g.freshName("p")
			return pat{lean: f, tests: []string{fmt.Sprintf("(%s == %s)", f, g.stableValue(x, sc))}}
		}
		return pat{lean: ident(x), binders: []string{x}}
	case *coreexport.PatLit:
		f := g.freshName("p")
		lit := g.expr(p.Value, sc)
		if !lit.pure {
			unsup(p.Pos, "pattern-literal")
		}
		return pat{lean: f, tests: []string{fmt.Sprintf("(%s == %s)", f, lit.code)}}
	case *coreexport.PatTuple:
		if len(p.Elems) > 4 {
			unsup(p.Pos, "tuple-arity-%d", len(p.Elems))
		}
		var out pat
		var parts []string
		for _, el := range p.Elems {
			sub := g.pattern(el, sc)
			parts = append(parts, sub.lean)
			out = out.merge(sub)
		}
		out.lean = fmt.Sprintf("(T%d.mk %s)", len(p.Elems), strings.Join(parts, " "))
		return out
	case *coreexport.PatExtract:
		c, ok := p.Callee.(*coreexport.Name)
		if !ok {
			unsup(p.Pos, "qualified-extractor")
		}
		var ctor string
		var nf int
		var out pat
		switch {
		case g.variants[c.Name] != nil:
			v := g.variants[c.Name]
			ctor, nf = ident(v.sealed.Name)+"."+ident(c.Name), len(v.fields)
			out.refutable = len(v.sealed.Variants) > 1
		case g.structs[c.Name] != nil:
			ctor, nf = ident(c.Name)+".mk", len(g.structs[c.Name].Fields)
		default:
			unsup(p.Pos, "extractor")
		}
		if len(p.Args) != nf {
			unsup(p.Pos, "extractor-arity")
		}
		var parts []string
		for _, a := range p.Args {
			sub := g.pattern(a, sc)
			parts = append(parts, sub.lean)
			out = out.merge(sub)
		}
		out.lean = ctor
		if len(parts) > 0 {
			out.lean = "(" + ctor + " " + strings.Join(parts, " ") + ")"
		}
		return out
	}
	unsup(posOf(p), "pattern-%s", p.Kind())
	return pat{}
}

func (a pat) merge(b pat) pat {
	return pat{
		lean:      a.lean,
		tests:     append(a.tests, b.tests...),
		refutable: a.refutable || b.refutable,
		binders:   append(a.binders, b.binders...),
	}
}

// stableValue is the value of stable identifier x, which must be pure: a
// package-level val is read before the match.
func (g *gen) stableValue(x string, sc *scope) string {
	if sc.lookup(x) != nil {
		return ident(x)
	}
	return "(← get)." + ident(x)
}

// matchItems translates a match to do-items. With value, they end with the
// match's value; without, with `pure ()`.
//
// The subject is evaluated once. Each arm is tried in order: Lean's own
// pattern match, then the arm's literal tests and guard, which run only
// after the pattern matched; the first arm that passes runs. No arm is
// Panic.noArm. Every arm's code appears once, so the output grows linearly
// with the arms:
//
//	match s with                     -- an arm whose Lean pattern cannot fail
//	| P => if (← cond) then BODY else REST
//
//	match s with                     -- a refutable pattern without tests
//	| P => BODY
//	| _ => REST
//
//	let ok ← (match s with           -- a refutable pattern with tests or a guard
//	  | P => cond
//	  | _ => pure false)
//	if ok then (match s with | P => BODY | _ => throw Panic.noArm) else REST
//
// The `throw` in the last form cannot run: the pattern matched just before.
func (g *gen) matchItems(m *coreexport.Match, sc *scope, value bool) []string {
	var items []string
	subj := m.Subject
	for {
		p, ok := subj.(*coreexport.Paren)
		if !ok {
			break
		}
		subj = p.Expr
	}
	var s string
	if n, ok := subj.(*coreexport.Name); ok && sc.lookup(n.Name) != nil {
		// A local is matched on directly, which keeps its equation for
		// proofs.
		s = ident(n.Name)
	} else {
		s = g.freshName("s")
		items = append(items, "let "+s+" := "+g.expr(m.Subject, sc).val())
	}
	rest := []string{"throw Panic.noArm"}
	for i := len(m.Cases) - 1; i >= 0; i-- {
		c, ok := m.Cases[i].(*coreexport.Case)
		if !ok {
			unsupAt(m.Cases[i])
		}
		rest = g.arm(c, s, sc, value, rest)
	}
	return append(items, rest...)
}

// arm is the do-items of one match arm followed by rest, the items of the
// arms after it.
func (g *gen) arm(c *coreexport.Case, s string, sc *scope, value bool, rest []string) []string {
	p := g.pattern(c.Pattern, sc)
	armSc := sc.child()
	for _, b := range p.binders {
		armSc.bind(b, false, "")
	}
	var body []string
	switch {
	case value:
		body = g.valueItems(c.Body, armSc)
	default:
		body = g.unitItems(c.Body, armSc)
	}
	// cond is the arm's tests and guard, as a Bool term or action.
	var cond term
	hasCond := len(p.tests) > 0 || c.Guard != nil
	if hasCond {
		tests := term{code: "(" + strings.Join(p.tests, " && ") + ")", pure: true}
		switch {
		case c.Guard == nil:
			cond = tests
		case len(p.tests) == 0:
			cond = g.expr(c.Guard, armSc.term())
		default:
			gd := g.expr(c.Guard, armSc.term())
			if gd.pure {
				cond = term{code: "(" + tests.code + " && " + gd.code + ")", pure: true}
			} else {
				cond = act(fmt.Sprintf("if %s then %s else pure false", tests.code, gd.mon()))
			}
		}
	}
	alt := func(pattern string, items []string) string {
		return "| " + pattern + " =>\n" + indentLines(items, 2)
	}
	ifElse := func(c string, th, el []string) string {
		return "if " + c + " then\n" + indentLines(th, 2) + "\nelse\n" + indentLines(el, 2)
	}
	switch {
	case !p.refutable && !hasCond:
		return []string{"match " + s + " with\n" + alt(p.lean, body)}
	case !hasCond:
		return []string{"match " + s + " with\n" + alt(p.lean, body) + "\n" + alt("_", rest)}
	case !p.refutable && len(p.binders) == 0:
		return []string{"match " + s + " with\n" + alt(p.lean, []string{ifElse(cond.val(), body, rest)})}
	}
	// The pattern's binders must not reach the later arms, so the test and
	// the body are two matches.
	ok := g.freshName("m")
	test := "match " + s + " with\n" + alt(p.lean, []string{cond.mon()})
	run := "match " + s + " with\n" + alt(p.lean, body)
	if p.refutable {
		test += "\n" + alt("_", []string{"pure false"})
		run += "\n" + alt("_", []string{"throw Panic.noArm"})
	}
	return []string{
		"let " + ok + " ← " + doTerm([]string{test}),
		ifElse(ok, []string{run}, rest),
	}
}
