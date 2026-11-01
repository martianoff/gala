package analyzer

import (
	"go/ast"
	"regexp"
	"slices"

	"martianoff/gala/internal/transpiler"
)

// A default value is source text, lowered at every use site that omits the
// argument (see transpiler.DefaultExpr). A use site in another package lowers
// it in its own file, where the names the default borrows from its declaring
// file are not in scope: that file's imports, and its package's unexported
// vals. recordDefaultScopes writes down what those names mean where the
// default was written, so the use site can spell them for itself.

// defaultScopeRecorder collects, while a package loaded as a dependency is
// analyzed file by file, what recordDefaultScopes needs once all its files
// are in.
type defaultScopeRecorder struct {
	pkgPath     string
	quals       map[string]fileQualifiers // canonical file path -> its import table
	privateVals map[string]bool           // the package's unexported package-level vals and vars
	// keepRecorded leaves a default that already has a scope as it is: one
	// recorded when its package was loaded as a dependency, which also
	// knows the package's private vals.
	keepRecorded bool
	// goDotName, when set, names a Go package a file dot-imports that its
	// import table could not name because it was not loaded (a sibling's Go
	// imports are not), loading it. "" when it does not load.
	goDotName func(path string) string
}

func newDefaultScopeRecorder(pkgPath string) *defaultScopeRecorder {
	return &defaultScopeRecorder{pkgPath: pkgPath, quals: map[string]fileQualifiers{}, privateVals: map[string]bool{}}
}

// addFile records one analyzed file of the package: its import table, and the
// unexported package-level bindings its analysis saw.
func (r *defaultScopeRecorder) addFile(canonFile string, quals fileQualifiers, res *transpiler.RichAST) {
	r.quals[canonFile] = quals
	for name, pv := range res.PackageVals {
		if pv != nil && !ast.IsExported(name) {
			r.privateVals[name] = true
		}
	}
}

// loadGoTypesByPath loads the Go type info of the Go package at path into
// richAST by import path only (see transpiler.GoTypeInfo.KeyedByPath), for a
// default that names it through its own file's imports, and returns that
// info: the file need not import the package, so the names its own imports
// bind keep their meaning.
func (a *galaAnalyzer) loadGoTypesByPath(path string, richAST *transpiler.RichAST) *transpiler.GoTypeInfo {
	goInfo := a.analyzeGoImport(path)
	if keyed := goInfo.KeyedByPath(path); !keyed.IsEmpty() {
		if richAST.GoTypeInfo == nil {
			richAST.GoTypeInfo = transpiler.NewGoTypeInfo()
		}
		richAST.GoTypeInfo.Merge(keyed)
	}
	return goInfo
}

// siblingGoDotName is a defaultScopeRecorder.goDotName for the defaults of a
// file's siblings: it loads the Go package at path (see loadGoTypesByPath),
// once, so a default lowered in the file can reach the names a sibling's Go
// dot import brings in (`ToUpper` of `import . "strings"`), and names it.
func (a *galaAnalyzer) siblingGoDotName(richAST *transpiler.RichAST) func(path string) string {
	names := map[string]string{}
	return func(path string) string {
		name, ok := names[path]
		if !ok {
			name = goPackageName(a.loadGoTypesByPath(path, richAST), path)
			names[path] = name
		}
		return name
	}
}

// loadDefaultScopeGoTypes loads the Go type info of the Go packages that the
// defaults of the packages a file imports (imports) name through their own
// files' imports — `strings` in `sep string = strings.Repeat("=", 2)`, or a Go
// dot import's `ToUpper` — so such a default lowered in the file types as it
// does where it was written, and its names can be qualified for the file (see
// loadGoTypesByPath).
func (a *galaAnalyzer) loadDefaultScopeGoTypes(imports []fileImport, richAST *transpiler.RichAST) {
	imported := map[string]bool{}
	for _, imp := range imports {
		imported[imp.Path] = true
	}
	loaded := map[string]bool{}
	loadPath := func(path string) {
		if imported[path] || loaded[path] || a.isGalaImport(path) {
			return
		}
		loaded[path] = true
		a.loadGoTypesByPath(path, richAST)
	}
	load := func(def transpiler.DefaultExpr) {
		if def.Scope == nil || !imported[def.Scope.PkgPath] {
			return
		}
		for _, path := range def.Scope.Imports {
			loadPath(path)
		}
		for path := range def.Scope.DotImports {
			loadPath(path)
		}
	}
	for _, fm := range richAST.Functions {
		for _, def := range fm.DefaultExprs {
			load(def)
		}
	}
	for _, tm := range richAST.Types {
		for _, def := range tm.FieldDefaults {
			load(def)
		}
		for _, mm := range tm.Methods {
			for _, def := range mm.DefaultExprs {
				load(def)
			}
		}
	}
}

// identifierPattern matches every identifier-shaped word of a default's text.
// A word inside a string literal or a member name (`x.strings`) matches too:
// the scope only answers for names, and a use site consults it only for a
// name the lowered default reads unqualified or as a qualifier.
var identifierPattern = regexp.MustCompile(`[\p{L}_][\p{L}\p{Nd}_]*`)

// record fills in the Scope of every default the package's own declarations
// carry. canonical maps a declaration's DefinedIn to the key addFile used.
func (r *defaultScopeRecorder) record(pkgAST *transpiler.RichAST, canonical func(string) string) {
	scopeOf := func(def transpiler.DefaultExpr, definedIn string) transpiler.DefaultExpr {
		quals, own := r.quals[canonical(definedIn)]
		if !own || definedIn == "" || (r.keepRecorded && def.Scope != nil) {
			return def
		}
		scope := &transpiler.DefaultScope{PkgPath: r.pkgPath}
		words := identifierPattern.FindAllString(def.Text, -1)
		// A Go dot import brings in exported names only: a default naming
		// none (`n int = 0`) needs no Go package loaded for it.
		namesExported := slices.ContainsFunc(words, ast.IsExported)
		for _, b := range quals.dots {
			pkgName := b.PkgName
			if pkgName == "" && !b.IsGala && r.goDotName != nil && namesExported {
				pkgName = r.goDotName(b.Path)
			}
			if pkgName == "" {
				continue // the package did not load; nothing resolves through it
			}
			if scope.DotImports == nil {
				scope.DotImports = map[string]string{}
			}
			scope.DotImports[b.Path] = pkgName
		}
		for _, word := range words {
			if b, ok := quals.named[word]; ok {
				if scope.Imports == nil {
					scope.Imports = map[string]string{}
				}
				scope.Imports[word] = b.Path
			}
			if r.privateVals[word] && !slices.Contains(scope.PrivateVals, word) {
				scope.PrivateVals = append(scope.PrivateVals, word)
			}
		}
		def.Scope = scope
		return def
	}
	for _, fm := range pkgAST.Functions {
		for i, def := range fm.DefaultExprs {
			fm.DefaultExprs[i] = scopeOf(def, fm.DefinedIn)
		}
	}
	for _, tm := range pkgAST.Types {
		for name, def := range tm.FieldDefaults {
			tm.FieldDefaults[name] = scopeOf(def, tm.DefinedIn)
		}
		for _, mm := range tm.Methods {
			definedIn := mm.DefinedIn
			if definedIn == "" {
				definedIn = tm.DefinedIn // a synthesized method, such as a case's companion Apply
			}
			for i, def := range mm.DefaultExprs {
				mm.DefaultExprs[i] = scopeOf(def, definedIn)
			}
		}
	}
}
