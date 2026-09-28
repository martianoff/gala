package analyzer

import (
	"sort"
	"strconv"
	"strings"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
)

// galaScope decides whether a bare name that some GALA package declares is in
// scope for the file being checked.
//
// The merged metadata the existence check consults holds every package that
// reached the compilation, and a package reaches it when *anything* in the
// import graph imports it. Accepting a name on that basis let a file that
// imports only the GALA `strings` package call `ArrayOf(...)` unqualified:
// `strings` pulls in the collection packages, `ArrayOf` was found, and the
// transformer then bound it to whichever package declared it — which one was
// an accident of metadata order. Go's rule, and GALA's, is that a bare name
// comes from the file's own package, its dot imports, or the implicit std
// prelude; a package the file imports by name is reached through its
// qualifier, and one it does not import at all is not reachable.
//
// Only GALA declarations are policed here. A bare Go name that no dot import
// provides is left to the Go compiler, as before.
type galaScope struct {
	// declarers maps a simple name to the GALA packages whose metadata
	// declares it (functions, types, companion objects).
	declarers map[string][]string
	// visible holds the package names a bare name may come from: this file's
	// package, the prelude packages, and every GALA package the file
	// dot-imports.
	visible map[string]bool
	// named maps a GALA package imported by name to the qualifier it is
	// bound to in this file, for the hint.
	named map[string]string
	// goNames holds every simple name a Go package in the compilation
	// declares, plus the package's own hand-written Go declarations. A name
	// that is also a Go symbol is never hidden.
	goNames map[string]bool
	// paths maps a loaded GALA package name to its import path(s), for the
	// hint.
	paths map[string][]string
}

func (a *galaAnalyzer) buildGalaScope(imports []fileImport, rich *transpiler.RichAST, filePath string) galaScope {
	s := galaScope{
		declarers: make(map[string][]string),
		visible:   map[string]bool{rich.PackageName: true},
		named:     make(map[string]string),
		goNames:   make(map[string]bool),
		paths:     make(map[string][]string),
	}
	// The prelude is an implicit dot import of every registered prelude
	// package, the same set the transformer resolves prelude names from.
	for _, p := range registry.Global.PreludePackages() {
		s.visible[p.Name] = true
	}
	for path, pkg := range rich.Packages {
		if pkg != "" {
			s.paths[pkg] = append(s.paths[pkg], path)
		}
	}
	for _, imp := range imports {
		if !a.isGalaImport(imp.Path) {
			continue
		}
		pkg := rich.Packages[imp.Path]
		if pkg == "" {
			pkg = transpiler.LastPathSegment(imp.Path)
		}
		if imp.IsDot {
			s.visible[pkg] = true
		} else if _, seen := s.named[pkg]; !seen {
			s.named[pkg] = imp.SpelledName()
		}
	}

	declare := func(key, pkg string) {
		if pkg == "" {
			// A key qualified as "pkg.Name" carries its package; a bare key
			// with no recorded package is this package's own. Recording it
			// under this package keeps it in scope even when a loaded
			// package declares the same name.
			if dot := strings.LastIndexByte(key, '.'); dot > 0 {
				pkg = key[:dot]
			} else {
				pkg = rich.PackageName
			}
		}
		name := simpleNameOf(key)
		for _, p := range s.declarers[name] {
			if p == pkg {
				return
			}
		}
		s.declarers[name] = append(s.declarers[name], pkg)
	}
	for k, fm := range rich.Functions {
		if fm != nil {
			declare(k, fm.Package)
		}
	}
	for k, tm := range rich.Types {
		if tm != nil {
			declare(k, tm.Package)
		}
	}
	for k, co := range rich.CompanionObjects {
		if co != nil {
			declare(k, co.Package)
		}
	}
	// Package-level vals and type aliases carry no package field: a bare key
	// is this package's own, a qualified one names its package.
	for k := range rich.PackageVals {
		declare(k, "")
	}
	for k := range rich.TypeAliases {
		declare(k, "")
	}
	for name := range s.declarers {
		sort.Strings(s.declarers[name])
	}

	if gi := rich.GoTypeInfo; gi != nil {
		for _, m := range []map[string]transpiler.Type{gi.Variables, gi.Constants} {
			for k := range m {
				s.goNames[simpleNameOf(k)] = true
			}
		}
		for k := range gi.Functions {
			s.goNames[simpleNameOf(k)] = true
		}
		for k := range gi.Types {
			s.goNames[simpleNameOf(k)] = true
		}
		for k := range gi.TypeAliases {
			s.goNames[simpleNameOf(k)] = true
		}
	}
	for _, symbols := range rich.GoExports {
		for _, sym := range symbols {
			s.goNames[sym] = true
		}
	}
	for name := range a.undefinedSymbolLocalGoNames(filePath) {
		s.goNames[name] = true
	}
	return s
}

// hides reports whether name is declared only by GALA packages this file
// cannot name it from unqualified.
func (s galaScope) hides(name string) bool {
	return s.hidesFrom(name, nil)
}

// hidesFrom is hides with extra package names also in scope.
func (s galaScope) hidesFrom(name string, extra map[string]bool) bool {
	pkgs := s.declarers[name]
	if len(pkgs) == 0 || s.goNames[name] {
		return false
	}
	for _, p := range pkgs {
		if s.visible[p] || extra[p] {
			return false
		}
	}
	return true
}

// namedImportQualifier returns the qualifier of a package this file imports
// by name that declares `name`, so the hint can point at `qualifier.name`.
func (s galaScope) namedImportQualifier(name string) (string, bool) {
	for _, p := range s.declarers[name] {
		if q, ok := s.named[p]; ok {
			return q, true
		}
	}
	return "", false
}

// declaringImportPaths returns the import paths of the loaded GALA packages
// that declare name, for the hint.
func (s galaScope) declaringImportPaths(name string) []string {
	var out []string
	for _, p := range s.declarers[name] {
		out = append(out, s.paths[p]...)
	}
	sort.Strings(out)
	return out
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strconv.Quote(s)
	}
	return out
}
