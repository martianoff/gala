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
		if !own || definedIn == "" {
			return def
		}
		scope := &transpiler.DefaultScope{PkgPath: r.pkgPath}
		for _, word := range identifierPattern.FindAllString(def.Text, -1) {
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
