package ownership

import (
	"go/token"
	"strconv"
	"strings"

	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// pkg is the analysis of one package.
type pkg struct {
	name  string
	opts  Options
	rich  *transpiler.RichAST
	decls []*decl
	// funcs and methods index the package's own declarations: a function by
	// its metadata key, a method by its type's key and name.
	funcs   map[string]*decl
	methods map[string]*decl
	shapes  map[string]string // memo of shape, by type key
	// galaPkgs holds the key prefixes of the packages with GALA metadata.
	galaPkgs map[string]bool
}

// fileCtx is what a file's declarations resolve names against.
type fileCtx struct {
	path string
	// quals maps an import's qualifier (its alias or package name) to the
	// key prefix of the package's metadata.
	quals map[string]string
	// dots holds the key prefixes of the dot-imported packages, std's
	// implicit import included.
	dots []string
}

// decl is one function or method declaration.
type decl struct {
	file *fileCtx
	node native.FunctionDeclarationNode
	name string
	line int

	fmeta *transpiler.FunctionMetadata
	mmeta *transpiler.MethodMetadata

	typeKey  string          // a method's receiver type key
	recvName string          // a method's receiver name
	recvType transpiler.Type // the receiver's pointee (a method only)
	recvPtr  bool            // a pointer receiver
	recvNN   bool            // a Ptr[T] receiver
	params   []transpiler.Type
	pnames   []string
	variadic bool
	result   transpiler.Type
	hasRes   bool

	// relevant: it has a pointer receiver, a pointer parameter or a pointer
	// result, so it gets a summary.
	relevant bool
	summary  *transpiler.BorrowSummary
	// asValue: the declaration is used as a function value somewhere in the
	// package, so its callers are not all known.
	asValue bool
}

func newPackage(name string, files []File, rich *transpiler.RichAST, opts Options) *pkg {
	a := &pkg{name: name, opts: opts, rich: rich, funcs: map[string]*decl{}, methods: map[string]*decl{}, shapes: map[string]string{}}
	for _, f := range files {
		fc := a.fileContext(f)
		for _, top := range f.Root.AllTopLevelDeclaration() {
			fn := top.FunctionDeclaration()
			if !fn.Ok() {
				continue
			}
			if d := a.newDecl(fc, fn); d != nil {
				a.decls = append(a.decls, d)
			}
		}
	}
	return a
}

// qualified is the metadata key of name in package p (see the analyzer's
// qualifiedName): bare in main and test.
func qualified(p, name string) string {
	if p != "" && p != "main" && p != "test" {
		return p + "." + name
	}
	return name
}

// fileContext reads f's imports.
func (a *pkg) fileContext(f File) *fileCtx {
	fc := &fileCtx{path: f.Path, quals: map[string]string{}}
	if a.name != "std" {
		fc.dots = append(fc.dots, "std")
	}
	for _, imp := range f.Root.AllImportDeclaration() {
		for _, spec := range imp.AllImportSpec() {
			path, err := strconv.Unquote(spec.STRING().GetText())
			if err != nil {
				continue
			}
			key := a.packageKey(path)
			switch {
			case spec.Identifier().Ok():
				fc.quals[spec.Identifier().GetText()] = key
			case strings.HasPrefix(spec.GetText(), "."):
				fc.dots = append(fc.dots, key)
			default:
				fc.quals[lastSegment(path)] = key
				if name, ok := a.rich.Packages[path]; ok && name != "" {
					fc.quals[name] = key
				}
			}
		}
	}
	return fc
}

// packageKey is the key prefix of the metadata of the package imported as
// path: the key it is recorded under when another package of its name is
// loaded too, else its name.
func (a *pkg) packageKey(path string) string {
	if k, ok := a.rich.PackageKeys[path]; ok && k != "" {
		return k
	}
	if name, ok := a.rich.Packages[path]; ok && name != "" {
		return name
	}
	return lastSegment(path)
}

func lastSegment(path string) string {
	return path[strings.LastIndex(path, "/")+1:]
}

// newDecl reads a declaration's signature from the metadata; nil when there
// is none (the declaration did not analyze, or is a kind the analysis skips).
func (a *pkg) newDecl(fc *fileCtx, fn native.FunctionDeclarationNode) *decl {
	name := fn.Identifier().GetText()
	tok := fn.Identifier().GalaNodeRef.Start()
	d := &decl{file: fc, node: fn, name: name, line: int(tok.Line)}
	if recv := fn.Receiver(); recv.Ok() {
		base := receiverBase(recv.Type_())
		if base == "" {
			return nil
		}
		d.typeKey = base
		if !strings.Contains(base, ".") {
			d.typeKey = qualified(a.name, base)
		}
		tm := a.rich.Types[d.typeKey]
		if tm == nil || tm.Methods[name] == nil {
			return nil
		}
		m := tm.Methods[name]
		d.mmeta = m
		d.name = strings.TrimPrefix(d.typeKey, a.name+".") + "." + name
		d.recvName = recv.Identifier().GetText()
		d.recvPtr, d.recvNN = m.PointerReceiver, m.PtrReceiver
		d.recvType = transpiler.NamedType{Package: a.name, Name: strings.TrimPrefix(d.typeKey, a.name+".")}
		d.params, d.pnames, d.variadic, d.result = m.ParamTypes, m.ParamNames, m.Variadic, m.ReturnType
		a.methods[d.typeKey+"#"+name] = d
	} else {
		key := qualified(a.name, name)
		fm := a.rich.Functions[key]
		if fm == nil {
			return nil
		}
		d.fmeta = fm
		d.params, d.pnames, d.variadic, d.result = fm.ParamTypes, fm.ParamNames, fm.Variadic, fm.ReturnType
		a.funcs[key] = d
	}
	sig := fn.Signature()
	d.hasRes = sig.Type_().Ok() || sig.GoResults().Ok()
	d.relevant = d.recvPtr || isPointer(d.result)
	for i := range d.params {
		if a.pointerParam(d, i) {
			d.relevant = true
		}
	}
	if d.relevant {
		d.summary = optimistic(d, a)
	}
	return d
}

// goCallable reports whether Go code outside the analysis may call d: an
// exported method (fmt calls String, encoding/json MarshalJSON, … through
// interfaces), or an exported function of a package other than main.
func (d *decl) goCallable(pkg string) bool {
	if !token.IsExported(d.node.Identifier().GetText()) {
		return false
	}
	return d.mmeta != nil || pkg != "main"
}

// pointerParam reports whether parameter i of d is a pointer the summary has
// a bit for: not the variadic one, whose argument is a slice.
func (a *pkg) pointerParam(d *decl, i int) bool {
	if d.variadic && i == len(d.params)-1 {
		return false
	}
	return isPointer(d.params[i])
}

// optimistic is the greatest summary d could have: the fixed point starts
// there and only demotes.
func optimistic(d *decl, a *pkg) *transpiler.BorrowSummary {
	s := &transpiler.BorrowSummary{ReceiverBorrowed: d.recvPtr, ReturnsFresh: isPointer(d.result)}
	s.BorrowedParams = make([]bool, len(d.params))
	for i := range d.params {
		s.BorrowedParams[i] = a.pointerParam(d, i)
	}
	return s
}

// fixpoint computes the greatest fixed point of the summaries: every
// summary starts optimistic, recursive calls included, and is demoted until
// none changes. Each round can only clear bits, so it ends.
func (a *pkg) fixpoint() {
	for changed := true; changed; {
		changed = false
		for _, d := range a.decls {
			if !d.relevant {
				continue
			}
			s := a.walk(d).summary()
			// Only demote: keep the iteration monotone.
			s.ReceiverBorrowed = s.ReceiverBorrowed && d.summary.ReceiverBorrowed
			s.ReturnsFresh = s.ReturnsFresh && d.summary.ReturnsFresh
			for i := range s.BorrowedParams {
				s.BorrowedParams[i] = s.BorrowedParams[i] && d.summary.BorrowedParams[i]
			}
			if !s.Equal(d.summary) {
				d.summary = s
				changed = true
			}
		}
	}
}

// receiverBase is the name of a receiver's type: `T` for `T`, `Ptr[T]`, `*T`
// and `T[A]`; "" for another form.
func receiverBase(t native.TypeNode) string {
	if !t.Ok() {
		return ""
	}
	if q := t.QualifiedIdentifier(); q.Ok() {
		name := q.GetText()
		if name == "Ptr" && t.TypeArguments().Ok() {
			if args := t.TypeArguments().TypeList().AllType_(); len(args) == 1 {
				return receiverBase(args[0])
			}
		}
		return name
	}
	if inner := t.AllType_(); len(inner) == 1 && strings.HasPrefix(t.GetText(), "*") {
		return receiverBase(inner[0])
	}
	return ""
}

// isPointer reports whether t is a pointer type, `Ptr[T]` or `*T`.
func isPointer(t transpiler.Type) bool {
	_, ok := t.(transpiler.PointerType)
	return ok
}

// typeKey is the metadata key of t's named type: its base for a generic
// type, its element for a pointer; "" when it has none.
func (a *pkg) typeKey(t transpiler.Type) string {
	switch t := t.(type) {
	case transpiler.NamedType:
		if t.Package == "" {
			if _, ok := a.rich.Types[qualified(a.name, t.Name)]; ok {
				return qualified(a.name, t.Name)
			}
			return t.Name
		}
		return qualified(t.Package, t.Name)
	case transpiler.BasicType:
		// The analyzer may record a type of the package by its bare name.
		if _, ok := a.rich.Types[qualified(a.name, t.Name)]; ok && !valueBasics[t.Name] {
			return qualified(a.name, t.Name)
		}
	case transpiler.GenericType:
		return a.typeKey(t.Base)
	case transpiler.PointerType:
		return a.typeKey(t.Elem)
	}
	return ""
}

// typeMeta is t's metadata, with the substitution of its type parameters by
// t's type arguments; nil when there is none.
func (a *pkg) typeMeta(t transpiler.Type) (*transpiler.TypeMetadata, map[string]transpiler.Type) {
	if p, ok := t.(transpiler.PointerType); ok {
		t = p.Elem
	}
	tm := a.rich.Types[a.typeKey(t)]
	if tm == nil {
		return nil, nil
	}
	var sub map[string]transpiler.Type
	if g, ok := t.(transpiler.GenericType); ok && len(g.Params) == len(tm.TypeParams) {
		sub = make(map[string]transpiler.Type, len(g.Params))
		for i, p := range tm.TypeParams {
			sub[p] = g.Params[i]
		}
	}
	return tm, sub
}

// fieldType is the type of field name of t; nil when t has no such field.
func (a *pkg) fieldType(t transpiler.Type, name string) transpiler.Type {
	tm, sub := a.typeMeta(t)
	if tm == nil || tm.IsInterface {
		return nil
	}
	ft, ok := tm.Fields[name]
	if !ok || (tm.IsSealed && !tm.IsGalaField(name)) {
		return nil
	}
	return unwrapImmutable(substitute(ft, sub))
}

// unwrapImmutable is T for std's Immutable[T], the wrapper of a val field.
func unwrapImmutable(t transpiler.Type) transpiler.Type {
	if e, ok := transpiler.ImmutableElem(t); ok {
		return e
	}
	return t
}

// substitute replaces the type parameters sub names in t.
func substitute(t transpiler.Type, sub map[string]transpiler.Type) transpiler.Type {
	return transpiler.SubstituteTypeParams(t, sub)
}
