package analyzer

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"martianoff/gala/internal/transpiler"
)

// goImporter is a cached importer that tries multiple strategies.
// Created once and reused across all AnalyzeGoPackage calls.
var (
	goImporterOnce sync.Once
	goImporterInst types.Importer
)

// goImporterAvailable tracks whether we have a working Go importer.
var goImporterAvailable bool

func getGoImporter() types.Importer {
	goImporterOnce.Do(func() {
		goroot := findGOROOT()
		if goroot == "" {
			goImporterAvailable = false
			fmt.Fprintf(os.Stderr, "Warning: Go SDK not found — Go type inference disabled. Set GOROOT, pass --goroot, or ensure 'go' is on PATH.\n")
			return
		}

		// Set GOROOT env and go/build.Default BEFORE creating importers.
		// The source importer uses go/build.Default.GOROOT which is cached at init time,
		// so we must also update it directly.
		os.Setenv("GOROOT", goroot)
		build.Default.GOROOT = goroot

		// Try source importer first (works with Bazel Go SDK which has source but no .a files)
		goImporterInst = importer.ForCompiler(token.NewFileSet(), "source", nil)
		if _, err := goImporterInst.Import("fmt"); err == nil {
			goImporterAvailable = true
			return
		}

		// Try default importer (gc — reads compiled .a files, works outside Bazel)
		goImporterInst = importer.Default()
		if _, err := goImporterInst.Import("fmt"); err == nil {
			goImporterAvailable = true
			return
		}

		goImporterAvailable = false
		fmt.Fprintf(os.Stderr, "Warning: Go SDK found at %s but importers failed — Go type inference disabled.\n", goroot)
	})
	return goImporterInst
}

// GoImporterAvailable returns whether the Go type importer is available.
func GoImporterAvailable() bool {
	getGoImporter() // ensure initialized
	return goImporterAvailable
}

// findGOROOT discovers the Go SDK root directory.
// It tries multiple strategies:
// 0. The hermetic Go SDK provided by rules_go when running under Bazel
// 1. GOROOT environment variable (if valid)
// 2. runtime.GOROOT() (if valid)
// 3. Walk up from the running binary to find a Go SDK layout (for Bazel)
// 4. Find 'go' binary on PATH and derive GOROOT from it
func findGOROOT() string {
	// 0. Prefer the hermetic Go SDK that rules_go materializes inside the Bazel
	// execution root. This must win over the GOROOT env / host PATH so that type
	// inference uses the exact same Go version Bazel compiles the generated code
	// with, instead of whatever `go` happens to be installed on the host. It only
	// matches inside a Bazel action; standalone `gala build` falls through below.
	if goroot := findBazelGoSDK(); goroot != "" {
		return goroot
	}

	// 1. Check GOROOT env
	if goroot := os.Getenv("GOROOT"); goroot != "" && goroot != "GOROOT" && isGoRoot(goroot) {
		return goroot
	}

	// 2. Check runtime.GOROOT()
	if goroot := runtime.GOROOT(); goroot != "" && goroot != "GOROOT" && isGoRoot(goroot) {
		return goroot
	}

	// 3. Find Go SDK from the running binary's directory.
	// In Bazel, the go binary is at <goroot>/bin/go, and our binary is built
	// with the same SDK. Walk up from our executable looking for a Go SDK.
	if exePath, err := os.Executable(); err == nil {
		dir := filepath.Dir(exePath)
		// Walk up a few levels looking for a Go SDK
		for i := 0; i < 5; i++ {
			if isGoRoot(dir) {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	// 4. Find 'go' binary on PATH and derive GOROOT
	if goPath, err := findGoOnPath(); err == nil {
		if goroot := gorootFromGoBinary(goPath); goroot != "" {
			return goroot
		}
	}

	return ""
}

// gorootFromGoBinary derives a valid GOROOT from a `go` executable path.
//
// The naive guess is <dir>/.. of <goroot>/bin/go, but that breaks for managed
// installs where `go` on PATH is a symlink — e.g. Homebrew's
// /opt/homebrew/bin/go points into /opt/homebrew/Cellar/go/<ver>/libexec/bin/go,
// so the unresolved guess yields /opt/homebrew (not a Go SDK). We therefore try,
// in order: the literal parent-of-bin, the same after resolving symlinks, and
// finally `go env GOROOT` which is authoritative. Returns "" if none is a valid
// Go SDK root.
func gorootFromGoBinary(goPath string) string {
	candidates := []string{filepath.Dir(filepath.Dir(goPath))}
	if resolved, err := filepath.EvalSymlinks(goPath); err == nil && resolved != goPath {
		candidates = append(candidates, filepath.Dir(filepath.Dir(resolved)))
	}
	for _, c := range candidates {
		if isGoRoot(c) {
			return c
		}
	}
	// Authoritative fallback: ask the toolchain itself.
	if out, err := exec.Command(goPath, "env", "GOROOT").Output(); err == nil {
		goroot := strings.TrimSpace(string(out))
		if goroot != "" && isGoRoot(goroot) {
			return goroot
		}
	}
	return ""
}

// findBazelGoSDK locates the hermetic Go SDK that rules_go provides when the
// transpiler runs inside a Bazel action. Transpile actions run with the execution
// root as their working directory (and tagged no-sandbox so the SDK on disk is
// reachable), with the SDK materialized at external/<rules_go go_sdk repo>/, e.g.
// external/rules_go++go_sdk+main___download_0 (bzlmod) or external/go_sdk
// (legacy WORKSPACE). Returns "" when not running under Bazel so the normal
// host-Go discovery in findGOROOT applies.
func findBazelGoSDK() string {
	// Search from the working directory (the execution root during an action) and
	// from the running binary's directory, walking up a few levels so we find the
	// `external/` tree regardless of which one Bazel hands us.
	var bases []string
	if cwd, err := os.Getwd(); err == nil {
		bases = append(bases, cwd)
	}
	if exe, err := os.Executable(); err == nil {
		bases = append(bases, filepath.Dir(exe))
	}
	return findBazelGoSDKFrom(bases)
}

// findBazelGoSDKFrom walks up from each base directory (bounded) looking for an
// `external/` tree that contains a rules_go Go SDK. Split out from findBazelGoSDK
// so the search logic can be tested without depending on the ambient process
// working directory or executable path.
func findBazelGoSDKFrom(bases []string) string {
	for _, base := range bases {
		dir := base
		for i := 0; i < 8; i++ {
			if sdk := matchBazelGoSDKUnder(filepath.Join(dir, "external")); sdk != "" {
				return sdk
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return ""
}

// matchBazelGoSDKUnder returns the first entry under externalDir that both looks
// like a rules_go SDK repo (its name contains "go_sdk") and is a valid Go SDK
// root. The legacy WORKSPACE name `go_sdk` is matched by the same glob.
func matchBazelGoSDKUnder(externalDir string) string {
	matches, err := filepath.Glob(filepath.Join(externalDir, "*go_sdk*"))
	if err != nil {
		return ""
	}
	for _, m := range matches {
		if isGoRoot(m) {
			return m
		}
	}
	return ""
}

// isGoRoot checks if a directory looks like a Go SDK root (has src/fmt directory).
func isGoRoot(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "src", "fmt"))
	return err == nil && info.IsDir()
}

// findGoOnPath searches PATH for the 'go' executable.
func findGoOnPath() (string, error) {
	// Search PATH first (covers both system Go and Bazel Go SDK)
	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		pathEnv = os.Getenv("Path") // Windows uses "Path"
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		for _, name := range []string{"go.exe", "go"} {
			candidate := filepath.Join(dir, name)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
		}
	}

	// Check common Go installation paths
	commonPaths := []string{
		// Unix paths
		"/usr/local/go/bin/go",
		"/usr/lib/go/bin/go",
		"/snap/go/current/bin/go",
		filepath.Join(os.Getenv("HOME"), "go", "bin", "go"),
		filepath.Join(os.Getenv("HOME"), "sdk", "go", "bin", "go"),
		// Windows paths
		filepath.Join(os.Getenv("USERPROFILE"), "go", "bin", "go.exe"),
		filepath.Join(os.Getenv("USERPROFILE"), "sdk", "go", "bin", "go.exe"),
		`C:\Go\bin\go.exe`,
		`C:\Program Files\Go\bin\go.exe`,
	}
	for _, p := range commonPaths {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}

	return "", os.ErrNotExist
}

// goPackageCache caches AnalyzeGoPackage results to avoid redundant go/importer calls.
var goPackageCache = struct {
	mu    sync.Mutex
	cache map[string]*transpiler.GoTypeInfo
}{cache: make(map[string]*transpiler.GoTypeInfo)}

// goTypeInfoNonEmpty reports whether a GoTypeInfo carries any usable type
// data. Used to decide whether go/importer resolved a package or whether the
// analyzer should fall back to parsing the package's .go source directly.
func goTypeInfoNonEmpty(info *transpiler.GoTypeInfo) bool {
	if info == nil {
		return false
	}
	return len(info.Functions) > 0 || len(info.Types) > 0 || len(info.Variables) > 0 || len(info.TypeAliases) > 0
}

// AnalyzeGoPackage loads type information for a Go package by import path.
// Uses go/importer to resolve installed packages (stdlib and third-party).
// Returns empty GoTypeInfo if Go SDK is not available (e.g., Bazel sandbox).
func AnalyzeGoPackage(importPath string) *transpiler.GoTypeInfo {
	goPackageCache.mu.Lock()
	if cached, ok := goPackageCache.cache[importPath]; ok {
		goPackageCache.mu.Unlock()
		return cached
	}
	goPackageCache.mu.Unlock()

	info := transpiler.NewGoTypeInfo()

	imp := getGoImporter()
	if !goImporterAvailable {
		return info
	}

	pkg, err := imp.Import(importPath)
	if err != nil {
		return info
	}

	extractPackageInfo(pkg, info)

	goPackageCache.mu.Lock()
	goPackageCache.cache[importPath] = info
	goPackageCache.mu.Unlock()

	return info
}

// goFilesCache memoizes AnalyzeGoFiles results across worker requests.
// The directory's relevant .go files don't change during one Bazel build
// invocation, and parsing + type-checking them with go/types is the most
// expensive thing this package does after the analyzer itself. Without
// memoization, the apex transpile of cmd/main.gala in gala_team
// re-parses every transitive package's hand-written Go files (plus the
// transitive Go-stdlib graph reachable via go/importer) on every visit.
var goFilesCache = struct {
	mu    sync.Mutex
	cache map[string]*transpiler.GoTypeInfo
}{cache: make(map[string]*transpiler.GoTypeInfo)}

// AnalyzeGoFiles parses and type-checks local .go files and extracts type info.
// This handles Go source files that live alongside GALA files or in Go-only packages.
// Generated .gen.go files are skipped — their metadata comes from analyzing the
// originating .gala source.
//
// Results are memoized per dirPath for the lifetime of the process. Within one
// Bazel build the .go files in a package directory don't change, so the
// parse + type-check work happens at most once per directory per worker.
func AnalyzeGoFiles(dirPath string) *transpiler.GoTypeInfo {
	goFilesCache.mu.Lock()
	if cached, ok := goFilesCache.cache[dirPath]; ok {
		goFilesCache.mu.Unlock()
		return cached
	}
	goFilesCache.mu.Unlock()

	info := transpiler.NewGoTypeInfo()

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		goFilesCache.mu.Lock()
		goFilesCache.cache[dirPath] = info
		goFilesCache.mu.Unlock()
		return info
	}

	fset := token.NewFileSet()
	var files []*ast.File
	hasGoFiles := false

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, ".gen.go") {
			continue
		}
		hasGoFiles = true
		fullPath := filepath.Join(dirPath, name)
		f, err := parser.ParseFile(fset, fullPath, nil, 0)
		if err != nil {
			continue
		}
		files = append(files, f)
	}

	if !hasGoFiles || len(files) == 0 {
		goFilesCache.mu.Lock()
		goFilesCache.cache[dirPath] = info
		goFilesCache.mu.Unlock()
		return info
	}

	// Type-check the parsed files
	conf := types.Config{
		Importer: getGoImporter(),
		Error:    func(err error) {}, // Ignore type-check errors (partial analysis is fine)
	}
	typesInfo := &types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		Defs:  make(map[*ast.Ident]types.Object),
		Uses:  make(map[*ast.Ident]types.Object),
	}

	pkg, _ := conf.Check(dirPath, fset, files, typesInfo)
	if pkg == nil {
		// Even if type-checking fails, try to extract what we can from AST
		extractFromAST(files, info)
		goFilesCache.mu.Lock()
		goFilesCache.cache[dirPath] = info
		goFilesCache.mu.Unlock()
		return info
	}

	extractPackageInfo(pkg, info)
	goFilesCache.mu.Lock()
	goFilesCache.cache[dirPath] = info
	goFilesCache.mu.Unlock()
	return info
}

// extractPackageInfo extracts all exported type information from a types.Package.
func extractPackageInfo(pkg *types.Package, info *transpiler.GoTypeInfo) {
	pkgName := pkg.Name()
	scope := pkg.Scope()

	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if !obj.Exported() {
			continue
		}

		qualName := pkgName + "." + name

		switch obj := obj.(type) {
		case *types.Func:
			sig := obj.Type().(*types.Signature)
			info.Functions[qualName] = convertSignature(sig)
			// A function often returns a named type defined in another package
			// (e.g. sha256.New() returns hash.Hash). Record that type's method
			// set under its canonical "pkg.Name" key so a value bound to the
			// call has completions even though the defining package (hash) was
			// never directly imported.
			registerReturnNamedTypes(sig, info)

		case *types.TypeName:
			if obj.IsAlias() {
				// Go type alias: type X = Y
				// Resolve to the aliased type for transparent type inference.
				underlying := goTypeToTranspilerType(obj.Type())
				info.TypeAliases[qualName] = underlying
				// Also create type data so methods can be looked up on the alias
				// name (e.g. "os.DirEntry").
				info.Types[qualName] = extractTypeData(obj, "alias")
				// When the alias re-exports a *named* type from another package
				// (e.g. `os.DirEntry = io/fs.DirEntry`), also register the target
				// under its own canonical "pkgName.TypeName" key. Go signatures
				// resolve such aliases to the canonical name (a return of
				// `[]os.DirEntry` surfaces as `[]fs.DirEntry`), so a method call
				// on a value of that type (`entries[i].Info()`) looks the method
				// set up under `fs.DirEntry`. Without this, methods on a
				// re-exported type are invisible whenever only the aliasing
				// package — not the defining one — is imported.
				registerAliasTargetType(obj.Type(), qualName, info)
			} else {
				// Go type definition: type X struct{...} or type X int
				info.Types[qualName] = extractTypeData(obj, "")
			}

		case *types.Var:
			info.Variables[qualName] = goTypeToTranspilerType(obj.Type())

		case *types.Const:
			info.Constants[qualName] = goTypeToTranspilerType(obj.Type())
		}
	}
}

// registerAliasTargetType records the method set / fields of the named type an
// alias points at under that target's own canonical "pkgName.TypeName" key.
// aliasType is the alias TypeName's Type(); aliasQualName is the alias's own
// qualified name (used to avoid registering a self-referential entry). It is a
// no-op when the target is not a named type from a real package, or when a
// (real, non-alias) entry for the canonical name already exists. This lets type
// inference that has resolved an alias to its canonical name find the target's
// methods even though the defining package was never directly imported.
func registerAliasTargetType(aliasType types.Type, aliasQualName string, info *transpiler.GoTypeInfo) {
	named, ok := types.Unalias(aliasType).(*types.Named)
	if !ok {
		return
	}
	tn := named.Obj()
	if tn == nil || tn.Pkg() == nil {
		return
	}
	canonical := tn.Pkg().Name() + "." + tn.Name()
	if canonical == aliasQualName {
		return
	}
	if _, exists := info.Types[canonical]; exists {
		return
	}
	info.Types[canonical] = extractTypeData(tn, "")
}

// registerReturnNamedTypes records, for each of a function's return values, the
// method set of any named type reached (unwrapping pointer/slice/array layers)
// that is defined in another package. This mirrors
// registerAliasTargetType but for return positions, so completion and type
// inference can find methods on values produced by a call into an imported
// package even when the value's own type lives in a package that was never
// directly imported (the classic case: a constructor returning an interface,
// like sha256.New() -> hash.Hash).
func registerReturnNamedTypes(sig *types.Signature, info *transpiler.GoTypeInfo) {
	if sig == nil {
		return
	}
	res := sig.Results()
	for i := 0; i < res.Len(); i++ {
		registerNamedTypeClosure(res.At(i).Type(), info)
	}
}

// registerNamedTypeClosure unwraps common type constructors (pointer, slice,
// array, map, chan) to find a named type and records its GoTypeData under the
// canonical "pkgName.TypeName" key. It is a no-op when the type is not a
// package-scoped named type or when an entry already exists (which also bounds
// the recursion).
func registerNamedTypeClosure(t types.Type, info *transpiler.GoTypeInfo) {
	switch tt := t.(type) {
	case *types.Pointer:
		registerNamedTypeClosure(tt.Elem(), info)
	case *types.Slice:
		registerNamedTypeClosure(tt.Elem(), info)
	case *types.Array:
		registerNamedTypeClosure(tt.Elem(), info)
	case *types.Chan:
		registerNamedTypeClosure(tt.Elem(), info)
	case *types.Map:
		registerNamedTypeClosure(tt.Key(), info)
		registerNamedTypeClosure(tt.Elem(), info)
	case *types.Named:
		tn := tt.Obj()
		if tn == nil || tn.Pkg() == nil {
			return
		}
		canonical := tn.Pkg().Name() + "." + tn.Name()
		if _, exists := info.Types[canonical]; exists {
			return
		}
		info.Types[canonical] = extractTypeData(tn, "")
	}
}

// GoPackageSourceDir resolves a Go import path to its on-disk source directory.
// It covers the standard library (under GOROOT/src), which is what the LSP needs
// to offer go-to-definition into Go source for stdlib calls. Returns "" when the
// directory can't be located (e.g. no Go SDK, or a third-party module that isn't
// unpacked under GOROOT).
func GoPackageSourceDir(importPath string) string {
	if importPath == "" {
		return ""
	}
	goroot := findGOROOT()
	if goroot == "" {
		return ""
	}
	dir := filepath.Join(goroot, "src", filepath.FromSlash(importPath))
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return dir
	}
	return ""
}

// extractTypeData creates GoTypeData for a types.TypeName.
func extractTypeData(tn *types.TypeName, forceKind string) *transpiler.GoTypeData {
	data := &transpiler.GoTypeData{
		Fields:  make(map[string]transpiler.Type),
		Methods: make(map[string]*transpiler.GoFuncSignature),
	}

	typ := tn.Type()

	// Determine kind
	if forceKind != "" {
		data.Kind = forceKind
	} else {
		switch typ.Underlying().(type) {
		case *types.Struct:
			data.Kind = "struct"
		case *types.Interface:
			data.Kind = "interface"
		default:
			data.Kind = "named"
		}
	}

	// Set underlying type
	data.Underlying = goTypeToTranspilerType(typ.Underlying())

	// Extract struct fields. Preserve declaration order in FieldOrder so
	// downstream consumers that build positional composite literals
	// (synthesizeTypeMetadataFromGo's FieldNames slice) match the Go-side
	// field layout instead of inheriting the random iteration order of the
	// Fields map.
	if s, ok := typ.Underlying().(*types.Struct); ok {
		for i := 0; i < s.NumFields(); i++ {
			f := s.Field(i)
			if f.Exported() {
				data.Fields[f.Name()] = goTypeToTranspilerType(f.Type())
				data.FieldOrder = append(data.FieldOrder, f.Name())
			}
		}
	}

	// Extract type parameter names for generic types so downstream consumers
	// (e.g., the dot-import-used scan and method lookup on generic Go-style
	// structs imported via dot import) can resolve `Type[T]` references and
	// the methods they expose.
	if named, ok := typ.(*types.Named); ok {
		if tps := named.TypeParams(); tps != nil {
			for i := 0; i < tps.Len(); i++ {
				data.TypeParams = append(data.TypeParams, tps.At(i).Obj().Name())
			}
		}
	}

	// Extract method set (including pointer receiver methods)
	mset := types.NewMethodSet(types.NewPointer(typ))
	for i := 0; i < mset.Len(); i++ {
		sel := mset.At(i)
		fn := sel.Obj().(*types.Func)
		if !fn.Exported() {
			continue
		}
		sig := fn.Type().(*types.Signature)
		data.Methods[fn.Name()] = convertSignature(sig)
	}

	// Also include value receiver methods
	mset = types.NewMethodSet(typ)
	for i := 0; i < mset.Len(); i++ {
		sel := mset.At(i)
		fn := sel.Obj().(*types.Func)
		if !fn.Exported() {
			continue
		}
		if _, exists := data.Methods[fn.Name()]; !exists {
			sig := fn.Type().(*types.Signature)
			data.Methods[fn.Name()] = convertSignature(sig)
		}
	}

	// types.NewMethodSet returns nothing for an uninstantiated generic named
	// type, so for generics we also pull methods directly off the *types.Named
	// origin. This lets a dot-importing GALA consumer find methods on a
	// generic Go-style struct (e.g. `Single[T]`) referenced only through a
	// generic instantiation.
	if named, ok := typ.(*types.Named); ok && named.TypeParams() != nil {
		for i := 0; i < named.NumMethods(); i++ {
			fn := named.Method(i)
			if !fn.Exported() {
				continue
			}
			if _, exists := data.Methods[fn.Name()]; exists {
				continue
			}
			sig := fn.Type().(*types.Signature)
			data.Methods[fn.Name()] = convertSignature(sig)
		}
	}

	return data
}

// convertSignature converts a types.Signature to a transpiler.GoFuncSignature.
func convertSignature(sig *types.Signature) *transpiler.GoFuncSignature {
	result := &transpiler.GoFuncSignature{
		IsVariadic: sig.Variadic(),
	}

	// Record the declared type-parameter names so a call site can instantiate
	// the signature. Both the function's own type params and a generic
	// receiver's are in scope inside the signature, so both are collected.
	for _, tps := range []*types.TypeParamList{sig.TypeParams(), sig.RecvTypeParams()} {
		for i := 0; tps != nil && i < tps.Len(); i++ {
			result.TypeParams = append(result.TypeParams, tps.At(i).Obj().Name())
		}
	}

	// Convert parameters (skip receiver)
	params := sig.Params()
	for i := 0; i < params.Len(); i++ {
		p := params.At(i)
		paramType := p.Type()
		// For variadic, the last param type is a slice — extract the element type
		if sig.Variadic() && i == params.Len()-1 {
			if sl, ok := paramType.(*types.Slice); ok {
				paramType = sl.Elem()
			}
		}
		result.Params = append(result.Params, transpiler.GoParam{
			Name: p.Name(),
			Type: goTypeToTranspilerType(paramType),
		})
	}

	// Convert return types
	results := sig.Results()
	for i := 0; i < results.Len(); i++ {
		result.Returns = append(result.Returns, goTypeToTranspilerType(results.At(i).Type()))
	}

	return result
}

// goTypeToTranspilerType converts a go/types.Type to a transpiler.Type.
// This is the core bridge between Go's type system and GALA's.
// It handles type aliases by resolving them to their underlying type.
func goTypeToTranspilerType(t types.Type) transpiler.Type {
	if t == nil {
		return transpiler.NilType{}
	}

	switch t := t.(type) {
	case *types.Basic:
		name := t.Name()
		// Untyped constants (e.g., "untyped string", "untyped int") should
		// resolve to their concrete Go type for code generation purposes.
		if t.Info()&types.IsUntyped != 0 {
			switch t.Kind() {
			case types.UntypedBool:
				name = "bool"
			case types.UntypedInt:
				name = "int"
			case types.UntypedRune:
				name = "rune"
			case types.UntypedFloat:
				name = "float64"
			case types.UntypedComplex:
				name = "complex128"
			case types.UntypedString:
				name = "string"
			}
		}
		return transpiler.BasicType{Name: name}

	case *types.Named:
		obj := t.Obj()
		pkg := obj.Pkg()
		if pkg == nil {
			// Built-in type (error, etc.)
			return transpiler.BasicType{Name: obj.Name()}
		}
		// Check if this is an alias — if so, resolve to the aliased type
		if obj.IsAlias() {
			return goTypeToTranspilerType(types.Unalias(t))
		}
		return transpiler.NamedType{
			Package:    pkg.Name(),
			Name:       obj.Name(),
			ImportPath: pkg.Path(),
		}

	case *types.Alias:
		// Go 1.22+ explicit alias type — resolve to the underlying aliased type
		return goTypeToTranspilerType(types.Unalias(t))

	case *types.Pointer:
		elem := goTypeToTranspilerType(t.Elem())
		return transpiler.PointerType{Elem: elem}

	case *types.Slice:
		elem := goTypeToTranspilerType(t.Elem())
		return transpiler.ArrayType{Elem: elem}

	case *types.Array:
		elem := goTypeToTranspilerType(t.Elem())
		return transpiler.ArrayType{Elem: elem}

	case *types.Map:
		key := goTypeToTranspilerType(t.Key())
		elem := goTypeToTranspilerType(t.Elem())
		return transpiler.MapType{Key: key, Elem: elem}

	case *types.Chan:
		// Map channels to their element type (GALA uses Signal wrappers)
		elem := goTypeToTranspilerType(t.Elem())
		return transpiler.NamedType{Name: "chan " + elem.String()}

	case *types.Signature:
		result := transpiler.FuncType{}
		params := t.Params()
		for i := 0; i < params.Len(); i++ {
			result.Params = append(result.Params, goTypeToTranspilerType(params.At(i).Type()))
		}
		results := t.Results()
		for i := 0; i < results.Len(); i++ {
			result.Results = append(result.Results, goTypeToTranspilerType(results.At(i).Type()))
		}
		return result

	case *types.Interface:
		// Empty interface → any
		return transpiler.BasicType{Name: "any"}

	case *types.Struct:
		// Anonymous struct — can't represent directly, return any
		return transpiler.BasicType{Name: "any"}

	case *types.TypeParam:
		// Generic type parameter
		return transpiler.BasicType{Name: t.Obj().Name()}

	default:
		return transpiler.NilType{}
	}
}

// extractFromAST is a fallback that extracts minimal type info from Go AST
// when full type-checking fails (e.g., missing dependencies).
func extractFromAST(files []*ast.File, info *transpiler.GoTypeInfo) {
	for _, f := range files {
		pkgName := f.Name.Name
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv != nil || !d.Name.IsExported() {
					continue
				}
				qualName := pkgName + "." + d.Name.Name
				sig := &transpiler.GoFuncSignature{}
				if d.Type.Results != nil {
					for range d.Type.Results.List {
						sig.Returns = append(sig.Returns, transpiler.BasicType{Name: "any"})
					}
				}
				info.Functions[qualName] = sig

			case *ast.GenDecl:
				if d.Tok != token.TYPE {
					continue
				}
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || !ts.Name.IsExported() {
						continue
					}
					qualName := pkgName + "." + ts.Name.Name
					kind := "named"
					if _, ok := ts.Type.(*ast.StructType); ok {
						kind = "struct"
					} else if _, ok := ts.Type.(*ast.InterfaceType); ok {
						kind = "interface"
					}
					if ts.Assign.IsValid() {
						kind = "alias"
					}
					info.Types[qualName] = &transpiler.GoTypeData{
						Kind:    kind,
						Fields:  make(map[string]transpiler.Type),
						Methods: make(map[string]*transpiler.GoFuncSignature),
					}
				}
			}
		}
	}
}
