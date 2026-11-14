// Package ownership is phase 1 of GALA's ownership design: inferred
// uniqueness (docs/OWNERSHIP_DESIGN.MD, section 3.1). It adds no syntax and
// reports no errors. For every function and method of a package it infers
//
//   - a borrow summary (transpiler.BorrowSummary): which pointer receivers and
//     parameters are only lent downward, and whether a pointer result is
//     always fresh; and
//   - for every pointer binding (a pointer receiver or parameter, or a local
//     `val` bound to a pointer), whether it is unique: of fresh origin, only
//     ever lent, and pointing at an owned-shaped value. When it is not, the
//     reasons why, each at a source position.
//
// The analysis is sound for what it calls unique and borrowed. Whatever it
// cannot see through is an escape, with its reason recorded: a callee without
// a summary, a member it cannot resolve, a construct it does not model.
//
// It reads the native parse tree and the analyzer's metadata (RichAST), and
// writes the summaries into that metadata (Apply), so that a caller in another
// package sees them. The transpiler never reads them: the generated Go does
// not depend on this package.
package ownership

import (
	"fmt"
	"sort"

	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// ReasonKind is why a pointer binding is not unique. The kinds are stable:
// formal/COVERAGE.md counts them.
type ReasonKind string

// The escapes: uses of a pointer that create a second access path to its
// pointee, or that the analysis cannot see through.
const (
	// Stored: stored in a field, a constructor, a composite literal, a tuple
	// or an Option.
	Stored ReasonKind = "stored"
	// Collection: aliased through a collection (an argument of a function or
	// method of collection_immutable or collection_mutable).
	Collection ReasonKind = "collection"
	// Aliased: bound to another name, `val q = p`.
	Aliased ReasonKind = "aliased"
	// Returned: returned while not fresh, for example a parameter.
	Returned ReasonKind = "returned"
	// Captured: captured by a lambda, a local function, a partial function, a
	// by-name argument, a method value, or the rest of a block after a
	// `bind`, `also` or `use`.
	Captured ReasonKind = "captured"
	// PassedToGo: the pointer, or a reference reachable from its pointee,
	// flows into Go: an argument or receiver of a Go function or method (a
	// method a hand-written .go file declares on a GALA type included), a
	// builtin, printing, a field of a Go struct, or a conversion to an
	// interface (`any`, `fmt.Stringer`, …). The detail names the Go side.
	PassedToGo ReasonKind = "passed-to-go"
	// Compared: compared with == or !=, or matched.
	Compared ReasonKind = "compared"
	// PassedTwice: two arguments of one call (the receiver included) reach
	// the same pointee.
	PassedTwice ReasonKind = "passed-twice"
	// EscapingCallee: lent to a GALA parameter or receiver whose summary says
	// it escapes, or to a parameter that is not a pointer (a type parameter).
	EscapingCallee ReasonKind = "escaping-callee"
	// UnknownCallee: used in a call whose callee has no summary: a function
	// value, a method the analysis cannot resolve. An argument of such a call
	// may be a by-name thunk, so any use inside it counts.
	UnknownCallee ReasonKind = "unknown-callee"
	// Rebound: the name is bound more than once in the function, so the
	// analysis does not tell its bindings apart.
	Rebound ReasonKind = "rebound"
	// Other: any other use (reassigned, indexed, called, an address taken of
	// it or of its fields, an operand of an operator).
	Other ReasonKind = "other"
)

// The properties: the binding is not of fresh origin, or not a value.
const (
	// Origin: not bound to a fresh allocation or a borrowed parameter: a
	// `var`, or the result of a call that does not return fresh.
	Origin ReasonKind = "origin"
	// Shape: the pointee is not owned-shaped: it holds a pointer, a Go slice
	// or map, a function, an interface or another reference.
	Shape ReasonKind = "shape"
	// Nullable: a `*T` that may be nil, not a `Ptr[T]`.
	Nullable ReasonKind = "nullable"
	// FromGo: the pointer was received from Go, so Go may hold another path
	// to it: the result of a Go function or method, a field of a Go value, a
	// channel receive, or a parameter of a function Go can call (one passed
	// to Go as a callback, or any function of a package with hand-written
	// .go files).
	FromGo ReasonKind = "from-go"
)

// Escapes reports whether k is a use that lets the pointer escape, as opposed
// to a property of the binding (Origin, Shape, Nullable, FromGo). A parameter
// or receiver is borrowed exactly when it has no escape.
func (k ReasonKind) Escapes() bool {
	return k != Origin && k != Shape && k != Nullable && k != FromGo
}

// Kinds lists every ReasonKind, escapes first, in a fixed order.
var Kinds = []ReasonKind{
	Stored, Collection, Aliased, Returned, Captured, PassedToGo, Compared,
	PassedTwice, EscapingCallee, UnknownCallee, Rebound, Other,
	Origin, FromGo, Shape, Nullable,
}

// Describe is a one-line description of k, for reports.
func (k ReasonKind) Describe() string {
	switch k {
	case Stored:
		return "stored in a field, constructor, literal, tuple or Option"
	case Collection:
		return "aliased through a collection"
	case Aliased:
		return "bound to another name"
	case Returned:
		return "returned while not fresh"
	case Captured:
		return "captured by a closure, method value, by-name argument or bind"
	case PassedToGo:
		return "it, or a reference inside its pointee, flows into Go"
	case Compared:
		return "compared or matched"
	case PassedTwice:
		return "two arguments of one call reach it"
	case EscapingCallee:
		return "lent to a parameter or receiver that escapes"
	case UnknownCallee:
		return "used in a call without a summary"
	case Rebound:
		return "its name is bound more than once"
	case Other:
		return "another use (reassigned, indexed, address taken, an operand)"
	case Origin:
		return "not of fresh origin"
	case FromGo:
		return "received from Go"
	case Shape:
		return "the pointee is not owned-shaped"
	case Nullable:
		return "a `*T` that may be nil"
	}
	return string(k)
}

// Reason is one reason a binding is not unique, at the source position of the
// use (or the declaration) it comes from.
type Reason struct {
	Kind   ReasonKind
	Detail string
	File   string
	Line   int
	Col    int
}

func (r Reason) String() string {
	s := fmt.Sprintf("%s at %s:%d:%d", r.Kind, r.File, r.Line, r.Col)
	if r.Detail != "" {
		s += " (" + r.Detail + ")"
	}
	return s
}

// Role is what declares a pointer binding.
type Role string

const (
	RoleReceiver Role = "receiver"
	RoleParam    Role = "param"
	RoleLocal    Role = "local"
)

// Binding is the verdict on one pointer binding of a function.
type Binding struct {
	Name    string
	Role    Role
	Pointee string // the pointee type, "" when unknown
	Unique  bool
	Reasons []Reason // empty when Unique
}

// Func is the analysis of one function or method.
type Func struct {
	File string
	Line int
	// Name is the function's name, or Type.method for a method.
	Name string
	// Summary is the function's borrow summary; nil when it has no pointer
	// receiver, parameter or result.
	Summary  *transpiler.BorrowSummary
	Bindings []Binding
}

// Unique reports whether every pointer binding of f is unique.
func (f Func) Unique() bool {
	for _, b := range f.Bindings {
		if !b.Unique {
			return false
		}
	}
	return true
}

// Report is the analysis of a package.
type Report struct {
	Package string
	// Funcs holds every function and method the analysis read, in file
	// order and then source order.
	Funcs []Func

	decls []*decl
}

// File is one source file of the package to analyze.
type File struct {
	Path string
	Root native.SourceFileNode
}

// Analyze infers the borrow summaries of the functions and methods the files
// of package pkg declare, and the uniqueness of their pointer bindings. rich
// is the analyzer's metadata for the package: its own declarations and its
// imports', the summaries of imported functions included. It is read only;
// Apply writes the summaries into it.
func Analyze(pkg string, files []File, rich *transpiler.RichAST, opts Options) *Report {
	a := newPackage(pkg, files, rich, opts)
	a.fixpoint()
	rep := &Report{Package: pkg, decls: a.decls}
	// Walk every declaration before judging any: a later one may use an
	// earlier one as a function value.
	walks := make([]*walker, len(a.decls))
	for i, d := range a.decls {
		walks[i] = a.walk(d)
	}
	for i, d := range a.decls {
		w := walks[i]
		f := Func{File: d.file.path, Line: d.line, Name: d.name}
		if d.relevant {
			f.Summary = d.summary
		}
		for _, b := range w.order {
			f.Bindings = append(f.Bindings, a.verdict(d, b))
		}
		rep.Funcs = append(rep.Funcs, f)
	}
	return rep
}

// Options configure an analysis.
type Options struct {
	// HandWrittenGo: the package has hand-written .go files, which may call
	// any of its functions with any pointer.
	HandWrittenGo bool
}

// Apply writes each function's and method's borrow summary into its metadata
// in rich, the RichAST Analyze read, for the package's importers to read.
func (r *Report) Apply() { applySummaries(r.decls) }

// AnalyzeAndApply is Analyze followed by Apply. A failure of the analysis (a
// panic, which would be a bug in it) leaves the metadata without summaries:
// that is sound, since a missing summary means every pointer escapes, and it
// never fails the compilation, whose output does not depend on it.
func AnalyzeAndApply(pkg string, files []File, rich *transpiler.RichAST, opts Options) {
	defer func() { _ = recover() }()
	// Only the summaries are needed: the fixed point's, without the report.
	a := newPackage(pkg, files, rich, opts)
	a.fixpoint()
	applySummaries(a.decls)
}

// applySummaries writes each declaration's summary into its metadata.
func applySummaries(decls []*decl) {
	for _, d := range decls {
		var s *transpiler.BorrowSummary
		if d.relevant {
			s = d.summary
		}
		switch {
		case d.fmeta != nil:
			d.fmeta.Borrow = s
		case d.mmeta != nil:
			d.mmeta.Borrow = s
		}
	}
}

// verdict is the Binding for b, a binding of d, after the final walk.
func (a *pkg) verdict(d *decl, b *binding) Binding {
	out := Binding{Name: b.name, Role: b.role, Pointee: typeText(b.pointee)}
	reasons := append([]Reason(nil), b.reasons...)
	if b.role != RoleLocal {
		// A parameter or receiver is unique only when every caller lends
		// it exclusively; a caller in Go does not.
		at := Reason{Kind: FromGo, File: b.file, Line: b.line, Col: b.col}
		switch {
		case a.opts.HandWrittenGo:
			at.Detail = "the package's hand-written Go may call " + d.name
			reasons = append(reasons, at)
		case d.asValue:
			at.Detail = d.name + " is used as a function value, which Go may call"
			reasons = append(reasons, at)
		case d.goCallable(a.name):
			at.Detail = d.name + " is exported, so Go may call it (directly, or through an interface)"
			reasons = append(reasons, at)
		}
	}
	if b.pointee != nil {
		if why := a.shape(b.pointee); why != "" {
			reasons = append(reasons, Reason{Kind: Shape, Detail: why, File: b.file, Line: b.line, Col: b.col})
		}
	} else {
		reasons = append(reasons, Reason{Kind: Shape, Detail: "pointee type unknown", File: b.file, Line: b.line, Col: b.col})
	}
	sort.SliceStable(reasons, func(i, j int) bool {
		if reasons[i].Line != reasons[j].Line {
			return reasons[i].Line < reasons[j].Line
		}
		return reasons[i].Col < reasons[j].Col
	})
	out.Reasons = reasons
	out.Unique = len(reasons) == 0
	return out
}

func typeText(t transpiler.Type) string {
	if t == nil {
		return ""
	}
	return t.String()
}
