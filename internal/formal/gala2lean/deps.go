package gala2lean

import (
	"maps"
	"reflect"

	"martianoff/gala/internal/formal/coreexport"
)

// walkAll calls f on every node of p, parents before children.
func walkAll(p *coreexport.Program, f func(coreexport.Node)) {
	for _, file := range p.Files {
		walk(file, f)
	}
}

var nodeType = reflect.TypeOf((*coreexport.Node)(nil)).Elem()

// walk calls f on n and every node below it, in field order.
func walk(n coreexport.Node, f func(coreexport.Node)) {
	if n == nil {
		return
	}
	v := reflect.ValueOf(n)
	if v.Kind() == reflect.Pointer && v.IsNil() {
		return
	}
	f(n)
	walkChildren(n, func(c coreexport.Node) { walk(c, f) })
}

// refs is the set of package-level names (functions and variables) that n
// refers to, with the scoping GALA gives them: a parameter, local or pattern
// binder hides a package-level name of the same spelling.
func (g *gen) refs(n coreexport.Node, bound map[string]bool, out map[string]bool) {
	if n == nil {
		return
	}
	switch n := n.(type) {
	case *coreexport.Name:
		if !bound[n.Name] && (g.funcs[n.Name] != nil || g.globalBy[n.Name] != nil) {
			out[n.Name] = true
		}
		return
	case *coreexport.PatName:
		if !bound[n.Name] && g.globalBy[n.Name] != nil && isUpper(bareName(n.Name)) {
			out[n.Name] = true
		}
		return
	case *coreexport.Block:
		inner := maps.Clone(bound)
		for _, st := range n.Stmts {
			g.refs(st, inner, out)
			switch st := st.(type) {
			case *coreexport.Val:
				bindAll(inner, st.Names)
			case *coreexport.Var:
				bindAll(inner, st.Names)
			case *coreexport.Func:
				inner[st.Name] = true
			}
		}
		return
	case *coreexport.Val:
		for _, v := range n.Values {
			g.refs(v, bound, out)
		}
		return
	case *coreexport.Var:
		for _, v := range n.Values {
			g.refs(v, bound, out)
		}
		return
	case *coreexport.Func:
		inner := maps.Clone(bound)
		// A parameter's default counts at each call that leaves it out
		// (the Call case), not here.
		for _, p := range n.Params {
			if p, ok := p.(*coreexport.Param); ok {
				inner[p.Name] = true
			}
		}
		if r, ok := n.Receiver.(*coreexport.Receiver); ok {
			inner[r.Name] = true
		}
		g.refs(n.Body, inner, out)
		return
	case *coreexport.Call:
		for _, a := range n.Args {
			g.refs(a, bound, out)
		}
		g.refs(n.Callee, bound, out)
		c, ok := n.Callee.(*coreexport.Name)
		if !ok || bound[c.Name] {
			return
		}
		s := g.sigOfSafe(c.Name)
		if s == nil {
			return
		}
		named := map[string]bool{}
		positional := 0
		for _, a := range n.Args {
			if na, isNamed := a.(*coreexport.NamedArg); isNamed {
				named[na.Name] = true
			} else {
				positional++
			}
		}
		if positional+len(named) >= len(s.names) {
			return // no parameter left to its default
		}
		// A default sees the parameters before it, never the caller's
		// locals.
		params := map[string]bool{}
		for k, name := range s.names {
			// A default that needs itself is reported where it is
			// translated; here it only must not recurse forever.
			if key := s.head + "." + name; k >= positional && !named[name] && !g.refsInDefault[key] {
				g.refsInDefault[key] = true
				g.refs(s.defaults[k], params, out)
				delete(g.refsInDefault, key)
			}
			params[name] = true
		}
		return
	case *coreexport.Lambda:
		inner := maps.Clone(bound)
		for _, p := range n.Params {
			if p, ok := p.(*coreexport.Param); ok {
				inner[p.Name] = true
			}
		}
		g.refs(n.Body, inner, out)
		return
	case *coreexport.Case:
		inner := maps.Clone(bound)
		g.refs(n.Pattern, bound, out)
		g.patBinders(n.Pattern, inner)
		g.refs(n.Guard, inner, out)
		g.refs(n.Body, inner, out)
		return
	case *coreexport.Select:
		// The selected name is a field or a method. The receiver's type is
		// not known here, so it refers to every method of that name: more
		// edges than calls, which only merges recursive groups.
		for _, k := range g.methodsNamed[n.Name] {
			out[k] = true
		}
		g.refs(n.Target, bound, out)
		return
	case *coreexport.NamedArg:
		g.refs(n.Value, bound, out)
		return
	}
	walkChildren(n, func(c coreexport.Node) { g.refs(c, bound, out) })
}

// walkChildren calls f on each direct child of n.
func walkChildren(n coreexport.Node, f func(coreexport.Node)) {
	v := reflect.ValueOf(n)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return
	}
	v = v.Elem()
	for i := range v.NumField() {
		fv := v.Field(i)
		switch {
		case fv.Type() == nodeType:
			if !fv.IsNil() {
				f(fv.Interface().(coreexport.Node))
			}
		case fv.Kind() == reflect.Slice && (fv.Type().Elem() == nodeType || fv.Type().Elem().Implements(nodeType)):
			for j := range fv.Len() {
				f(fv.Index(j).Interface().(coreexport.Node))
			}
		}
	}
	// A call's bracket, read as types: the only reading the translation
	// takes (bracketCall).
	if c, ok := n.(*coreexport.Call); ok && c.Bracket != nil {
		for _, t := range c.Bracket.Types {
			f(t)
		}
	}
}

// mapChildren is a copy of n with each direct child c replaced by f(c).
func mapChildren(n coreexport.Node, f func(coreexport.Node) coreexport.Node) coreexport.Node {
	v := reflect.ValueOf(n)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return n
	}
	c := reflect.New(v.Elem().Type())
	c.Elem().Set(v.Elem())
	cv := c.Elem()
	for i := range cv.NumField() {
		fv := cv.Field(i)
		switch {
		case fv.Type() == nodeType:
			if !fv.IsNil() {
				if r := f(fv.Interface().(coreexport.Node)); r != nil {
					fv.Set(reflect.ValueOf(r))
				} else {
					fv.Set(reflect.Zero(nodeType))
				}
			}
		case fv.Kind() == reflect.Slice && fv.Type().Elem() == nodeType:
			s := reflect.MakeSlice(fv.Type(), fv.Len(), fv.Len())
			for j := range fv.Len() {
				el := fv.Index(j)
				if el.IsNil() {
					continue
				}
				if r := f(el.Interface().(coreexport.Node)); r != nil {
					s.Index(j).Set(reflect.ValueOf(r))
				}
			}
			fv.Set(s)
		}
	}
	out := c.Interface().(coreexport.Node)
	// A call's bracket: its types mapped as children are (walkChildren).
	if call, ok := out.(*coreexport.Call); ok && call.Bracket != nil && call.Bracket.Types != nil {
		b := *call.Bracket
		b.Types = make([]coreexport.Node, len(call.Bracket.Types))
		for j, t := range call.Bracket.Types {
			if b.Types[j] = f(t); b.Types[j] == nil {
				b.Types[j] = t
			}
		}
		call.Bracket = &b
	}
	return out
}

func bindAll(m map[string]bool, names []string) {
	for _, n := range names {
		m[n] = true
	}
}

// patBinders adds the names pattern p binds to m.
func (g *gen) patBinders(p coreexport.Node, m map[string]bool) {
	switch p := p.(type) {
	case *coreexport.PatName:
		if g.variants[p.Name] == nil && !(isUpper(bareName(p.Name)) && g.globalBy[p.Name] != nil) {
			m[p.Name] = true
		}
	case *coreexport.PatTuple:
		for _, e := range p.Elems {
			g.patBinders(e, m)
		}
	case *coreexport.PatExtract:
		for _, a := range p.Args {
			g.patBinders(a, m)
		}
	}
}

// funcRefs is what function f refers to.
func (g *gen) funcRefs(f *coreexport.Func) map[string]bool {
	if out, ok := g.refsOf[g.key(f)]; ok {
		return out
	}
	out := map[string]bool{}
	g.refs(f, map[string]bool{}, out)
	g.refsOf[g.key(f)] = out
	return out
}

// callsSelf reports whether f refers to itself.
func (g *gen) callsSelf(f *coreexport.Func) bool { return g.funcRefs(f)[g.key(f)] }

// funcSCCs groups the functions into strongly connected components of the
// reference graph, callees before callers, each in source order (Tarjan's
// algorithm, started from the functions in source order).
func (g *gen) funcSCCs() [][]*coreexport.Func {
	keyOf := map[*coreexport.Func]string{}
	for _, f := range g.funcOrder {
		keyOf[f] = g.key(f)
	}
	index := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	var out [][]*coreexport.Func
	next := 0
	var strong func(v string)
	strong = func(v string) {
		index[v], low[v] = next, next
		next++
		stack = append(stack, v)
		onStack[v] = true
		refs := g.funcRefs(g.byKey[v])
		for _, w := range g.funcOrder {
			if !refs[keyOf[w]] {
				continue
			}
			if _, seen := index[keyOf[w]]; !seen {
				strong(keyOf[w])
				low[v] = min(low[v], low[keyOf[w]])
			} else if onStack[keyOf[w]] {
				low[v] = min(low[v], index[keyOf[w]])
			}
		}
		if low[v] == index[v] {
			members := map[string]bool{}
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				members[w] = true
				if w == v {
					break
				}
			}
			var scc []*coreexport.Func
			for _, f := range g.funcOrder {
				if members[keyOf[f]] {
					scc = append(scc, f)
				}
			}
			out = append(out, scc)
		}
	}
	for _, f := range g.funcOrder {
		if _, seen := index[keyOf[f]]; !seen {
			strong(keyOf[f])
		}
	}
	return out
}

// initOrder is the order Go initializes the package-level variables in:
// repeatedly the earliest variable in declaration order that is not yet
// initialized and whose initializer depends on no uninitialized variable,
// through the functions it calls too (Go spec, "Package initialization").
func (g *gen) initOrder() []*globalVar {
	// deps[v] is every variable v's initializer reaches.
	deps := map[string]map[string]bool{}
	for _, gv := range g.globals {
		reach := map[string]bool{}
		seenFn := map[string]bool{}
		var visit func(refs map[string]bool)
		visit = func(refs map[string]bool) {
			for r := range refs {
				if g.globalBy[r] != nil {
					reach[r] = true
				} else if f := g.byKey[r]; f != nil && !seenFn[r] {
					seenFn[r] = true
					visit(g.funcRefs(f))
				}
			}
		}
		refs := map[string]bool{}
		g.refs(gv.value, map[string]bool{}, refs)
		visit(refs)
		deps[gv.name] = reach
	}
	done := map[string]bool{}
	var order []*globalVar
	for len(order) < len(g.globals) {
		progress := false
		for _, gv := range g.globals {
			if done[gv.name] {
				continue
			}
			ready := true
			for d := range deps[gv.name] {
				if !done[d] {
					ready = false
					break
				}
			}
			if ready {
				done[gv.name] = true
				order = append(order, gv)
				progress = true
				break
			}
		}
		if !progress {
			for _, gv := range g.globals {
				if !done[gv.name] {
					unsup(gv.pos, "init-cycle")
				}
			}
		}
	}
	return order
}
