package ownership

import (
	"fmt"
	"slices"
	"sort"

	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// Adoption of `inout` (docs/OWNERSHIP_DESIGN.MD, section 4, "A safe
// codemod"): the plan of a rewrite of `Ptr[T]` receivers and parameters to
// `inout T`, for the ones phase 1 finds borrowed. `gala fix inout` applies
// it (internal/inoutfix).
//
// A binding is converted only when all of these hold:
//
//   - it is a `Ptr[T]` (never nil), written `p Ptr[T]`, with no `var`
//     modifier, default or `...`;
//   - phase 1 finds no escape: it is only read through, written through,
//     dereferenced, or lent to a borrowed parameter or receiver;
//   - its function is not used as a function value, its package has no
//     hand-written Go, and it is not public API (unless AdoptConfig.Public);
//   - every same-package parameter or receiver it is lent to is converted
//     too: an `inout` place may only be lent on to an `inout` parameter, or
//     to a borrowed receiver of another package's method.
//
// The rewrite of a converted binding p is: `p Ptr[T]` becomes `inout p T`;
// `*p` becomes `p`; p passed whole as an argument becomes `&p`. Field reads
// and writes and method calls on p are unchanged. A call site elsewhere
// needs no change: `&x` and a `Ptr` both fill an `inout` parameter.
//
// Whatever the plan cannot see (a caller that passes a call result, or
// calls an `inout` method on a `val`), the compiler rejects; the fix command
// compiles the rewritten package and excludes such functions.

// Edit replaces the bytes [Start, End) of File's source with Text. Offsets
// are into the source as parsed, without a leading byte order mark.
type Edit struct {
	File       string
	Start, End int
	Text       string
}

// AdoptBinding is the verdict on one `Ptr[T]` (or `*T`) receiver or
// parameter.
type AdoptBinding struct {
	Name      string
	Role      Role
	Pointee   string
	Converted bool
	// Blocked says why it is not converted; "" when it is.
	Blocked string
}

// AdoptFunc is one function or method with a pointer receiver or
// parameter.
type AdoptFunc struct {
	Name string
	File string
	Line int
	// Public: Go or another package can call it (an exported function of a
	// package other than main, or an exported method of such a package).
	// Converting it changes the package's API.
	Public bool
	// SigStart and SigEnd span its header, `func` to the end of the
	// signature, in File's source.
	SigStart, SigEnd int
	// End is the offset just past the declaration.
	End int
	Bindings         []AdoptBinding
}

// Converted reports whether the rewrite converts any binding of f.
func (f AdoptFunc) Converted() bool {
	for _, b := range f.Bindings {
		if b.Converted {
			return true
		}
	}
	return false
}

// AdoptPlan is the `inout` rewrite of one package.
type AdoptPlan struct {
	Package string
	Funcs   []AdoptFunc
	// Edits are the rewrite's edits, sorted by file and offset. They never
	// overlap.
	Edits []Edit
}

// AdoptConfig configures PlanInout.
type AdoptConfig struct {
	// Public converts public functions too (AdoptFunc.Public).
	Public bool
	// Exclude names functions (AdoptFunc.Name) to leave as they are, with
	// the reason why.
	Exclude map[string]string
}

// bindingUses is what the rewrite of a binding needs to know: where it is
// dereferenced, and where it is lent whole, as an argument or a receiver.
type bindingUses struct {
	derefs    []native.GalaNodeRef // the unary expressions `*p`
	lends     []lendUse
	receivers []lendUse
}

// lendUse is a binding lent whole to a GALA callee: argument index of it,
// or its receiver (index -1). own is the callee's declaration when the
// package declares it.
type lendUse struct {
	at     native.GalaNodeRef
	own    *decl
	index  int
	callee string
}

// recordUse applies f to b's uses, when the walk records them.
func (w *walker) recordUse(b *binding, f func(*bindingUses)) {
	if w.uses == nil || w.p3 {
		return
	}
	u := w.uses[b]
	if u == nil {
		u = &bindingUses{}
		w.uses[b] = u
	}
	f(u)
}

// candidate is one binding the plan may convert.
type candidate struct {
	d       *decl
	b       *binding
	uses    *bindingUses
	typ     native.TypeNode // its `Ptr[T]`
	at      native.GalaNodeRef
	blocked string
}

// slot names a receiver (index -1) or parameter of a declaration.
type slot struct {
	d     *decl
	index int
}

// PlanInout plans the `inout` rewrite of package pkg: its files, the
// analyzer's metadata for it, and the analysis options, as Analyze takes
// them.
func PlanInout(pkg string, files []File, rich *transpiler.RichAST, opts Options, cfg AdoptConfig) *AdoptPlan {
	a := newPackage(pkg, files, rich, opts)
	a.fixpoint()
	byDecl := map[*decl][]*candidate{}
	slots := map[slot]*candidate{}
	// Walk every declaration before judging: a later one may use an
	// earlier one as a function value.
	walks := make([]*walker, len(a.decls))
	for i, d := range a.decls {
		walks[i] = a.walkWith(d, false, map[*binding]*bindingUses{})
	}
	for i, d := range a.decls {
		for _, b := range walks[i].order {
			switch {
			case b.role == RoleLocal:
				continue
			case b.inout:
				// Already `inout`: a binding may be lent on to it.
				slots[slot{d, b.index}] = &candidate{d: d, b: b}
				continue
			}
			c := &candidate{d: d, b: b, uses: walks[i].uses[b]}
			if c.uses == nil {
				c.uses = &bindingUses{}
			}
			c.typ, c.at = a.declaredPointer(d, b)
			c.blocked = a.blocked(d, b, c.typ, cfg)
			byDecl[d] = append(byDecl[d], c)
			slots[slot{d, b.index}] = c
		}
	}
	// Close the set: a binding lent to a same-package receiver or parameter
	// that is not converted cannot be converted either; nor can one lent to
	// another package's parameter, which is never inout.
	for changed := true; changed; {
		changed = false
		for _, cs := range byDecl {
			for _, c := range cs {
				if c.blocked != "" {
					continue
				}
				if why := lendBlocked(c, slots); why != "" {
					c.blocked, changed = why, true
				}
			}
		}
	}
	plan := &AdoptPlan{Package: pkg}
	for _, d := range a.decls {
		cs := byDecl[d]
		if len(cs) == 0 {
			continue
		}
		fn := d.node
		f := AdoptFunc{Name: d.name, File: d.file.path, Line: d.line, Public: a.public(d),
			SigStart: int(fn.Start().Start), SigEnd: int(fn.Signature().Stop().End), End: int(fn.Stop().End)}
		for _, c := range cs {
			f.Bindings = append(f.Bindings, AdoptBinding{Name: c.b.name, Role: c.b.role, Pointee: typeText(c.b.pointee),
				Converted: c.blocked == "", Blocked: c.blocked})
			if c.blocked == "" {
				plan.Edits = append(plan.Edits, c.edits(d.file.path)...)
			}
		}
		plan.Funcs = append(plan.Funcs, f)
	}
	sort.SliceStable(plan.Edits, func(i, j int) bool {
		if plan.Edits[i].File != plan.Edits[j].File {
			return plan.Edits[i].File < plan.Edits[j].File
		}
		return plan.Edits[i].Start < plan.Edits[j].Start
	})
	return plan
}

// public reports whether d is part of its package's API: callable from
// another package or from Go. A main package has no importers.
func (a *pkg) public(d *decl) bool {
	return a.name != "main" && a.name != "test" && d.goCallable(a.name)
}

// declaredPointer is the `Ptr[T]` type node b is declared with, and the
// node `inout ` goes in front of (the receiver's name, or the parameter); a
// zero type when it is declared some other way.
func (a *pkg) declaredPointer(d *decl, b *binding) (native.TypeNode, native.GalaNodeRef) {
	if b.role == RoleReceiver {
		r := d.node.Receiver()
		return ptrType(r.Type_()), r.Identifier().GalaNodeRef
	}
	params := d.node.Signature().Parameters().ParameterList().AllParameter()
	if b.index < 0 || b.index >= len(params) {
		return native.TypeNode{}, native.GalaNodeRef{}
	}
	p := params[b.index]
	if p.VAL().Ok() || p.VAR().Ok() || p.INOUT().Ok() || p.ELLIPSIS().Ok() || p.ParamDefault().Ok() {
		return native.TypeNode{}, p.GalaNodeRef
	}
	return ptrType(p.Type_()), p.GalaNodeRef
}

// ptrType is t when it is written `Ptr[T]` on one line, else the zero node:
// the rewrite never moves a line.
func ptrType(t native.TypeNode) native.TypeNode {
	if !t.Ok() || !t.QualifiedIdentifier().Ok() || t.QualifiedIdentifier().GetText() != "Ptr" || !t.TypeArguments().Ok() ||
		t.Start().Line != t.Stop().Line {
		return native.TypeNode{}
	}
	if args := t.TypeArguments().TypeList().AllType_(); len(args) != 1 {
		return native.TypeNode{}
	}
	return t
}

// blocked is why b, a receiver or parameter of d declared with typ, cannot
// be converted on its own; "" when it can.
func (a *pkg) blocked(d *decl, b *binding, typ native.TypeNode, cfg AdoptConfig) string {
	for _, r := range b.reasons {
		if r.Kind == Nullable {
			return "a *T that may be nil"
		}
	}
	for _, r := range b.reasons {
		if r.Kind.Escapes() {
			return "escapes: " + r.String()
		}
	}
	switch why, excluded := cfg.Exclude[d.name]; {
	case excluded:
		return why
	case !typ.Ok():
		return "not declared as a plain `Ptr[T]` (a var, val, variadic or defaulted parameter)"
	case a.opts.HandWrittenGo:
		return "the package's hand-written Go may call it"
	case d.asValue:
		return d.name + " is used as a function value"
	case a.public(d) && !cfg.Public:
		return "public API: converting it changes the package's signature"
	}
	return ""
}

// lendBlocked is why c cannot be converted given the slots converted so
// far; "" when it can.
func lendBlocked(c *candidate, slots map[slot]*candidate) string {
	check := func(l lendUse) string {
		what := "the receiver"
		if l.index >= 0 {
			what = fmt.Sprintf("parameter %d", l.index+1)
		}
		if l.own == nil {
			if l.index < 0 {
				return "" // another package's borrowed receiver
			}
			return fmt.Sprintf("lent to %s of %s, another package's parameter, which is not inout", what, l.callee)
		}
		if t := slots[slot{l.own, l.index}]; t == nil || t.blocked != "" {
			return fmt.Sprintf("lent to %s of %s, which stays a pointer", what, l.callee)
		}
		return ""
	}
	for _, l := range slices.Concat(c.uses.lends, c.uses.receivers) {
		if why := check(l); why != "" {
			return why
		}
	}
	return ""
}

// edits are the rewrite of c in file.
func (c *candidate) edits(file string) []Edit {
	// `p Ptr[T]` becomes `inout p T`; the receiver's parentheses stay.
	at := int(c.at.Start().Start)
	out := []Edit{{File: file, Start: at, End: at, Text: "inout "}}
	elem := c.typ.TypeArguments().TypeList().AllType_()[0]
	out = append(out, Edit{File: file, Start: int(c.typ.Start().Start), End: int(c.typ.Stop().End), Text: elem.SourceText()})
	for _, u := range c.uses.derefs {
		op := native.UnaryExprNode{GalaNodeRef: u}.UnaryOp()
		out = append(out, Edit{File: file, Start: int(op.Start().Start), End: int(op.Stop().End), Text: ""})
	}
	for _, l := range c.uses.lends {
		at := int(l.at.Start().Start)
		out = append(out, Edit{File: file, Start: at, End: at, Text: "&"})
	}
	return out
}
