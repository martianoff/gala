package analyzer

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"sync"

	"martianoff/gala/internal/transpiler"
)

// Which pointer-receiver methods of a Go type only read their receiver
// (GoTypeData.ReadOnlyMethods). GALA calls a pointer method on a value Go
// cannot address through a copy; that is an error (GALA-E0110) when the
// method can mutate its receiver, and fine when it only reads it, as
// `(*bytes.Buffer).Len` and `(*url.URL).String` do.
//
// A method counts as mutating when its body, or a closure in it:
//
//   - assigns, increments or takes the address of its receiver or a place in
//     it (`b.off = 0`, `*b = T{}`, `&b.buf`, `b.arr[:]`, `for b.i = range`);
//   - uses the receiver pointer itself as a value — passes, returns, stores or
//     captures it (comparing it with == or != only reads it);
//   - calls a method that mutates on its receiver or on a field it holds by
//     value, its own or promoted from an embedded field: one of the same
//     package, found by following the calls, or one of another package, read
//     from that package's source.
//
// What happens through a pointer, slice, map, channel, function or interface
// held in a field is not a change to the receiver: a copy shares it.
// Anything the analysis cannot resolve counts as mutating. A package whose
// source is not found gets no read-only methods, so all of its pointer
// methods count as mutating.
//
// The hand-written .go files of a package GALA compiles are analyzed with the
// package (analyzeGoFiles), since their syntax is in hand. A package GALA
// imports is read from the Go SDK only when the transformer asks
// (GoReadOnlyMethods): only a pointer method called on a value Go cannot
// address needs the answer.

// sdkReadOnly memoizes, by import path, the read-only pointer methods of the
// Go SDK packages read so far: the type name, then the method names.
var sdkReadOnly = struct {
	mu      sync.Mutex
	entries map[string]*sdkReadOnlyEntry
}{entries: make(map[string]*sdkReadOnlyEntry)}

type sdkReadOnlyEntry struct {
	once     sync.Once
	readOnly map[string]map[string]bool
}

// sdkPackageReadOnly is the read-only pointer methods of the types pkg
// declares, read from its source in the Go SDK once per import path; nil when
// the source is not there.
func sdkPackageReadOnly(pkg *types.Package) map[string]map[string]bool {
	sdkReadOnly.mu.Lock()
	entry := sdkReadOnly.entries[pkg.Path()]
	if entry == nil {
		entry = &sdkReadOnlyEntry{}
		sdkReadOnly.entries[pkg.Path()] = entry
	}
	sdkReadOnly.mu.Unlock()
	entry.once.Do(func() { entry.readOnly = parseSDKReadOnly(pkg) })
	return entry.readOnly
}

// parseSDKReadOnly parses pkg's source in the Go SDK and works out its
// read-only pointer methods.
func parseSDKReadOnly(pkg *types.Package) map[string]map[string]bool {
	// The Go SDK's packages are under GOROOT; another module's are found as
	// the go command finds them.
	var bp *build.Package
	var err error
	if dir := GoPackageSourceDir(pkg.Path()); dir != "" {
		bp, err = build.Default.ImportDir(dir, 0)
	} else {
		bp, err = build.Default.Import(pkg.Path(), "", 0)
	}
	if err != nil {
		return nil
	}
	dir := bp.Dir
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range append(append([]string(nil), bp.GoFiles...), bp.CgoFiles...) {
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil
		}
		files = append(files, f)
	}
	return goReadOnlyPointerMethods(files, pkg)
}

// GoReadOnlyMethods is the read-only pointer methods of the Go type typeName
// of the package importPath, its source read from the Go SDK on first use
// (see RichAST.GoReadOnlyMethods). Nil when the package or its source is not
// found.
func GoReadOnlyMethods(importPath, typeName string) map[string]bool {
	key := importPath + "." + typeName
	if found, ok := goReadOnlyByType.Load(key); ok {
		return found.(map[string]bool)
	}
	found := lookupGoReadOnly(importPath, typeName)
	goReadOnlyByType.Store(key, found)
	return found
}

// goReadOnlyByType memoizes GoReadOnlyMethods, a miss included, by
// "importPath.typeName".
var goReadOnlyByType sync.Map

// lookupGoReadOnly is GoReadOnlyMethods without the memo.
func lookupGoReadOnly(importPath, typeName string) map[string]bool {
	imp := getGoImporter()
	if imp == nil || !goImporterAvailable {
		return nil
	}
	pkg, err := imp.Import(importPath)
	if err != nil {
		return nil
	}
	tn, ok := pkg.Scope().Lookup(typeName).(*types.TypeName)
	if !ok {
		return nil
	}
	return readOnlyMethodsOfType(tn, sdkPackageReadOnly)
}

// recordReadOnlyMethods fills data.ReadOnlyMethods for each type pkg
// declares, and for each GALA type its Go adds methods to, from files, the
// package's parsed .go files.
func recordReadOnlyMethods(files []*ast.File, pkg *types.Package, info *transpiler.GoTypeInfo) {
	declared := goReadOnlyPointerMethods(files, pkg)
	declaredBy := func(p *types.Package) map[string]map[string]bool {
		if p == pkg {
			return declared
		}
		return sdkPackageReadOnly(p)
	}
	for _, name := range pkg.Scope().Names() {
		tn, isType := pkg.Scope().Lookup(name).(*types.TypeName)
		data := info.Types[pkg.Name()+"."+name]
		if isType && data != nil && len(data.PointerMethods) > 0 {
			data.ReadOnlyMethods = readOnlyMethodsOfType(tn, declaredBy)
		}
	}
	for typeName, methods := range declared {
		if data := info.GalaTypeMethods[pkg.Name()+"."+typeName]; data != nil {
			data.ReadOnlyMethods = methods
		}
	}
}

// readOnlyMethodsOfType is the read-only pointer methods of tn: those its
// package declares on it (declared gives a package's), and those promoted
// from an embedded field that is a pointer, or that are read-only on the
// embedded type.
func readOnlyMethodsOfType(tn *types.TypeName, declared func(*types.Package) map[string]map[string]bool) map[string]bool {
	named, ok := types.Unalias(tn.Type()).(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return nil
	}
	own := declared(named.Obj().Pkg())[named.Obj().Name()]
	var out map[string]bool
	mset := types.NewMethodSet(types.NewPointer(named))
	for i := 0; i < mset.Len(); i++ {
		name := mset.At(i).Obj().Name()
		if own[name] || promotedReadOnly(named, name, declared) {
			if out == nil {
				out = make(map[string]bool)
			}
			out[name] = true
		}
	}
	return out
}

// promotedReadOnly reports whether the method name of named is promoted from
// an embedded field and only reads its receiver.
func promotedReadOnly(named *types.Named, name string, declared func(*types.Package) map[string]map[string]bool) bool {
	decl, viaPointer, ok := methodDeclaredOn(named, name, named.Obj().Pkg())
	switch {
	case !ok:
		return false
	case viaPointer:
		return true // the embedded pointer leaves the receiver
	case decl.Obj() == named.Obj() || decl.Obj().Pkg() == nil:
		return false // declared on named itself
	}
	return declared(decl.Obj().Pkg())[decl.Obj().Name()][name]
}

// goMethodKey names a method of a type of the package analyzed.
type goMethodKey struct{ typeName, method string }

// goReadOnlyPointerMethods works out which pointer-receiver methods files
// declare only read their receiver, by the type they are declared on; pkg is
// the package, for the types of the receivers' fields (a type it does not
// declare, such as a GALA type a mixed package's Go adds methods to, has
// fields of unknown types).
func goReadOnlyPointerMethods(files []*ast.File, pkg *types.Package) map[string]map[string]bool {
	a := goEffectsAnalysis{pkg: pkg, calls: make(map[goMethodKey][]goMethodKey), mutating: make(map[goMethodKey]bool)}
	var methods []goMethodKey
	for _, f := range files {
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || len(fd.Recv.List) != 1 || fd.Body == nil {
				continue
			}
			typeName, pointer := receiverBaseName(fd.Recv.List[0].Type)
			if !pointer || typeName == "" {
				continue
			}
			key := goMethodKey{typeName, fd.Name.Name}
			methods = append(methods, key)
			if names := fd.Recv.List[0].Names; len(names) == 1 && names[0].Name != "_" {
				a.analyzeBody(key, names[0].Name, fd.Body)
			}
		}
	}
	// A method mutates when it reaches one that does.
	for changed := true; changed; {
		changed = false
		for _, m := range methods {
			if a.mutating[m] {
				continue
			}
			for _, callee := range a.calls[m] {
				if a.mutating[callee] {
					a.mutating[m], changed = true, true
					break
				}
			}
		}
	}
	out := make(map[string]map[string]bool)
	for _, m := range methods {
		if a.mutating[m] {
			continue
		}
		if out[m.typeName] == nil {
			out[m.typeName] = make(map[string]bool)
		}
		out[m.typeName][m.method] = true
	}
	return out
}

// goEffectsAnalysis holds what goReadOnlyPointerMethods learns: the methods
// that mutate by their own body, and the same-package pointer methods each
// method calls on its receiver or on a field it holds by value.
type goEffectsAnalysis struct {
	pkg      *types.Package
	calls    map[goMethodKey][]goMethodKey
	mutating map[goMethodKey]bool
}

// analyzeBody records the effects of the uses of recv in body, the body of
// the method key.
func (a *goEffectsAnalysis) analyzeBody(key goMethodKey, recv string, body *ast.BlockStmt) {
	var stack []ast.Node
	ast.Inspect(body, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		if id, ok := n.(*ast.Ident); ok && id.Name == recv && !a.mutating[key] && a.receiverUse(key, id, stack) {
			a.mutating[key] = true
		}
		stack = append(stack, n)
		return true
	})
}

// receiverUse reports whether id, a use of the receiver of the method key
// whose ancestors are stack (innermost last), mutates the receiver by
// itself; a call it makes of a same-package method is recorded in a.calls.
func (a *goEffectsAnalysis) receiverUse(key goMethodKey, id *ast.Ident, stack []ast.Node) bool {
	i := len(stack) - 1
	// A selector's name or a composite literal's key is not the receiver.
	switch p := stackAt(stack, i).(type) {
	case *ast.SelectorExpr:
		if p.Sel == id {
			return false
		}
	case *ast.KeyValueExpr:
		if _, inLit := stackAt(stack, i-1).(*ast.CompositeLit); inLit && p.Key == id {
			return false
		}
	}
	var node ast.Node = id
	var path []string
	deref := false
climb:
	for ; i >= 0; i-- {
		switch p := stack[i].(type) {
		case *ast.SelectorExpr:
			if p.X != node {
				break climb
			}
			path = append(path, p.Sel.Name)
		case *ast.IndexExpr:
			if p.X != node {
				break climb
			}
			path = append(path, transpiler.ReceiverIndex)
		case *ast.ParenExpr:
		case *ast.StarExpr:
			if len(path) > 0 || deref {
				break climb
			}
			deref = true
		case *ast.CallExpr:
			if p.Fun != node {
				break climb
			}
			return a.call(key, path)
		default:
			break climb
		}
		node = stack[i]
	}
	bare := len(path) == 0 && !deref // the receiver pointer itself
	switch p := stackAt(stack, i).(type) {
	case *ast.AssignStmt:
		for _, lhs := range p.Lhs {
			if lhs == node {
				return bare || a.writes(key.typeName, path) // bare: the receiver is rebound
			}
		}
	case *ast.IncDecStmt:
		return a.writes(key.typeName, path)
	case *ast.UnaryExpr:
		if p.Op == token.AND {
			return bare || a.writes(key.typeName, path)
		}
	case *ast.RangeStmt:
		if p.Key == node || p.Value == node {
			return a.writes(key.typeName, path)
		}
	case *ast.SliceExpr:
		if p.X == node && !bare {
			return a.writes(key.typeName, append(path, transpiler.ReceiverIndex))
		}
	case *ast.BinaryExpr:
		if bare && (p.Op == token.EQL || p.Op == token.NEQ) {
			return false
		}
	}
	if bare {
		return true // the pointer escapes
	}
	// A field read, or a method value (`r.Write`), which is bound to the
	// receiver and may be called later.
	if len(path) > 0 && path[len(path)-1] != transpiler.ReceiverIndex {
		if _, _, known := a.place(key.typeName, path, false); !known {
			return a.call(key, path)
		}
	}
	return false
}

// stackAt is stack[i], or nil past its bottom.
func stackAt(stack []ast.Node, i int) ast.Node {
	if i < 0 || i >= len(stack) {
		return nil
	}
	return stack[i]
}

// call reports whether a call of the method path's last selector names, on
// the place before it, mutates the receiver of the method key by itself; a
// call of a same-package pointer method is recorded in a.calls instead.
func (a *goEffectsAnalysis) call(key goMethodKey, path []string) bool {
	last := len(path) - 1
	if last < 0 {
		return true // the receiver called as a function
	}
	if path[last] == transpiler.ReceiverIndex {
		return false // a function value read from the receiver
	}
	if _, inReceiver, known := a.place(key.typeName, path, false); known && !inReceiver {
		return false // a function-typed field read and called
	}
	place, inReceiver, known := a.place(key.typeName, path[:last], false)
	switch {
	case !known:
		return true
	case !inReceiver:
		return false
	case place == nil: // the receiver itself, of a type pkg does not declare
		a.calls[key] = append(a.calls[key], goMethodKey{key.typeName, path[last]})
		return false
	}
	return a.callOn(key, place, path[last])
}

// callOn reports whether a call of method on a place of type place in the
// receiver mutates the receiver by itself; a same-package pointer method is
// recorded in a.calls instead.
func (a *goEffectsAnalysis) callOn(key goMethodKey, place types.Type, method string) bool {
	if _, isParam := place.(*types.TypeParam); isParam {
		return false
	}
	switch place.Underlying().(type) {
	case *types.Pointer, *types.Interface:
		return false
	}
	if types.NewMethodSet(place).Lookup(a.pkg, method) != nil {
		return false // a value method: it gets a copy
	}
	decl, viaPointer, ok := methodDeclaredOn(place, method, a.pkg)
	switch {
	case !ok:
		return true
	case viaPointer:
		return false // promoted through an embedded pointer
	case decl.Obj().Pkg() == nil:
		return true
	case decl.Obj().Pkg() != a.pkg: // another package's: read from its source
		return !sdkPackageReadOnly(decl.Obj().Pkg())[decl.Obj().Name()][method]
	}
	a.calls[key] = append(a.calls[key], goMethodKey{decl.Obj().Name(), method})
	return false
}

// methodDeclaredOn finds the pointer method name of typ: the named type that
// declares it, typ itself or a type embedded in it, and whether it is reached
// through an embedded pointer. ok is false when typ has no such method.
func methodDeclaredOn(typ types.Type, name string, pkg *types.Package) (decl *types.Named, viaPointer, ok bool) {
	obj, index, _ := types.LookupFieldOrMethod(types.NewPointer(typ), false, pkg, name)
	if _, isFunc := obj.(*types.Func); !isFunc || len(index) == 0 {
		return nil, false, false
	}
	cur := typ
	for _, i := range index[:len(index)-1] {
		st, isStruct := cur.Underlying().(*types.Struct)
		if !isStruct || i >= st.NumFields() {
			return nil, false, false
		}
		cur = st.Field(i).Type()
		if _, isPtr := cur.Underlying().(*types.Pointer); isPtr {
			return nil, true, true
		}
	}
	decl, ok = types.Unalias(cur).(*types.Named)
	return decl, false, ok
}

// writes reports whether a write to path, from a receiver of the type
// typeName, changes the receiver's own memory.
func (a *goEffectsAnalysis) writes(typeName string, path []string) bool {
	_, inReceiver, known := a.place(typeName, path, true)
	return !known || inReceiver
}

// place follows path from a receiver of the type typeName. place is the
// type of the place reached, nil for the receiver itself when pkg does not
// declare typeName; inReceiver is false when the path leaves the receiver's
// own memory (through a pointer, slice, map or other reference); known is
// false when a step cannot be resolved. For a write, the last step is the
// place assigned, and counts whatever its type.
func (a *goEffectsAnalysis) place(typeName string, path []string, write bool) (place types.Type, inReceiver, known bool) {
	if a.pkg != nil {
		if tn, ok := a.pkg.Scope().Lookup(typeName).(*types.TypeName); ok {
			place = tn.Type()
		}
	}
	for i, step := range path {
		if place == nil {
			return nil, true, false
		}
		if step == transpiler.ReceiverIndex {
			arr, isArray := place.Underlying().(*types.Array)
			if !isArray {
				return nil, false, true // a slice's or map's element is shared; a string's cannot be written
			}
			place = arr.Elem()
			continue
		}
		obj, _, _ := types.LookupFieldOrMethod(place, true, a.pkg, step)
		field, isField := obj.(*types.Var)
		if !isField {
			return nil, true, false
		}
		if write && i == len(path)-1 {
			return field.Type(), true, true
		}
		if isReferenceGoType(field.Type()) {
			return nil, false, true
		}
		place = field.Type()
	}
	return place, true, true
}

// isReferenceGoType reports whether a value of t refers to memory it does
// not hold, which a copy of it shares.
func isReferenceGoType(t types.Type) bool {
	switch t.Underlying().(type) {
	case *types.Pointer, *types.Slice, *types.Map, *types.Chan, *types.Signature, *types.Interface:
		return true
	}
	return false
}
