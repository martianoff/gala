package analyzer

import (
	"fmt"
	"strings"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/transpiler"
)

// fileDecls is what analyze's collection of the analyzed file's own
// declarations works on: the metadata it fills, the file it reads and where
// that file sits, the file's doc comments, and the declarations seen so far
// in the package (see packageDecls). The collection reads the file's native
// tree (collectFileTypesNative, collectFileFuncsNative and the functions
// after them in collect_native.go).
type fileDecls struct {
	richAST              *transpiler.RichAST
	pkgName              string
	filePath             string
	absFilePath          string
	docs                 galaparser.Docs
	decls                *packageDecls
	pendingDefaultChecks []pendingDefaultCheck
}

// pendingDefaultCheck is a function whose parameter defaults analyze
// validates after section 2.5, when every declared type of the package can be
// resolved: its metadata, where its declaration starts, and the span of each
// default.
type pendingDefaultCheck struct {
	meta      *transpiler.FunctionMetadata
	line, col int
	spans     map[int]defaultExprSpan
}

// qualifiedName is name keyed in package pkgName: qualified unless the
// package is main or test, or there is none.
func qualifiedName(pkgName, name string) string {
	if pkgName != "" && pkgName != "main" && pkgName != "test" {
		return pkgName + "." + name
	}
	return name
}

// startTypeEntry readies the entry richAST.Types[key] for collecting into it
// the declaration, at pos in file, of the type name of package pkg. Every
// path that registers a type declaration starts here: a struct, shorthand
// struct, interface, sealed or opaque type, in the analyzed file or in a
// sibling. The entry is a new one holding only its Name and Package, or one
// of pkg that an earlier collection, another file's methods or a cache load
// filled. Such a declaration may have changed form since (a struct become an
// interface, say), so no field the old one described survives, and the entry
// is reset in place, so references to it stay valid. Name and Package are its
// key.
//
// Of its methods, those another file declares are kept: a type's methods may
// be declared in any file of its package, and the analyzed file's methods are
// collected before its siblings' types are. The methods file declares are
// dropped, as are those that came with the old declaration (OfTypeDecl: an
// interface's method specs, a sealed type's case predicates): collecting
// file gives again those it still declares. So are the companions of an old
// sealed declaration's cases, which registerSealedType makes again for the
// cases there still are — but not a case another sealed type has taken since
// (isCompanionOf). Doc is kept as well: the caller's nativeSetDoc replaces
// it only when the declaration has a doc comment. So is DefinedIn, which a
// struct declaration sets and other forms leave as an earlier pass recorded
// it.
func (a *galaAnalyzer) startTypeEntry(richAST *transpiler.RichAST, key, name, pkg string, pos transpiler.SourcePos, file string) *transpiler.TypeMetadata {
	meta := richAST.Types[key]
	if meta == nil || meta.Package != pkg {
		meta = &transpiler.TypeMetadata{Name: name, Package: pkg}
		richAST.Types[key] = meta
	}
	methods := make(map[string]*transpiler.MethodMetadata)
	for methodName, m := range meta.Methods {
		if !m.OfTypeDecl && !a.paths.sameFile(m.DefinedIn, file) {
			methods[methodName] = m
		}
	}
	for _, v := range meta.SealedVariants {
		caseKey := qualifiedName(pkg, v.Name)
		if c := richAST.Types[caseKey]; c != nil && c.Package == pkg && isCompanionOf(c, key) {
			delete(richAST.Types, caseKey)
		}
	}
	*meta = transpiler.TypeMetadata{
		Name:      meta.Name,
		Package:   meta.Package,
		Doc:       meta.Doc,
		Pos:       pos,
		Methods:   methods,
		Fields:    make(map[string]transpiler.Type),
		DefinedIn: meta.DefinedIn,
	}
	return meta
}

// isCompanionOf reports whether c is the companion of a case of the sealed
// type keyed parentKey: registerSealedType's, whose Apply builds that type. A
// case the collection has meanwhile registered for another sealed type of the
// file, as when a case moves to a type declared further up, builds that
// other type, so its companion is not this one's to drop.
func isCompanionOf(c *transpiler.TypeMetadata, parentKey string) bool {
	apply := c.Methods["Apply"]
	return apply != nil && apply.OfTypeDecl && apply.ReturnType != nil && apply.ReturnType.BaseName() == parentKey
}

// embedDirective is the directive of `embed val varName typeName = patterns`,
// typeName "" when the declaration gives none.
func embedDirective(varName, typeName string, patterns []string) transpiler.EmbedDirective {
	// Determine Go variable name
	goVar := varName
	if typeName == transpiler.TypeEmbeddedFS || typeName == "std.EmbeddedFS" {
		goVar = "_embed_" + varName
	} else if typeName == "" {
		// Infer type: glob patterns → EmbeddedFS, single file → string
		hasGlob := false
		for _, p := range patterns {
			if strings.ContainsAny(p, "*?") || len(patterns) > 1 {
				hasGlob = true
				break
			}
		}
		if hasGlob {
			typeName = transpiler.TypeEmbeddedFS
			goVar = "_embed_" + varName
		} else {
			typeName = "string"
		}
	}
	return transpiler.EmbedDirective{
		VarName:  varName,
		GoVar:    goVar,
		Patterns: patterns,
		TypeName: typeName,
	}
}

// sealedDeclaredHere marks the sealed type fullName, which the analyzed file
// declares, and its cases' companions as defined in that file, and queues the
// cases whose fields declare defaults: those are checked as a function's
// parameter defaults are, through the companion's Apply.
func (fd *fileDecls) sealedDeclaredHere(fullName string) {
	meta, ok := fd.richAST.Types[fullName]
	if !ok {
		return
	}
	meta.DefinedIn = fd.filePath
	for _, v := range meta.SealedVariants {
		cm, ok := fd.richAST.Types[qualifiedName(fd.pkgName, v.Name)]
		if !ok {
			continue
		}
		cm.DefinedIn = fd.filePath
		apply := cm.Methods["Apply"]
		if apply == nil {
			continue
		}
		apply.DefinedIn = fd.filePath // its field defaults are lowered as declared here
		if len(apply.DefaultExprs) == 0 {
			continue
		}
		// Each default's own position, so a mismatch underlines it.
		spans := make(map[int]defaultExprSpan, len(apply.DefaultExprs))
		for i, def := range apply.DefaultExprs {
			text := def.Text
			if nl := strings.IndexByte(text, '\n'); nl >= 0 {
				text = text[:nl]
			}
			spans[i] = defaultExprSpan{line: def.Pos.Line, startCol: def.Pos.Column, endCol: def.Pos.Column + len([]rune(text))}
		}
		fd.pendingDefaultChecks = append(fd.pendingDefaultChecks, pendingDefaultCheck{
			meta: &transpiler.FunctionMetadata{
				Name: v.Name, ParamTypes: apply.ParamTypes, ParamNames: apply.ParamNames, DefaultExprs: apply.DefaultExprs,
			},
			line: v.Pos.Line, col: v.Pos.Column,
			spans: spans,
		})
	}
}

// genericInterfaceMethodError is GALA-E0086 for the method spec methodName,
// at line:col, of the interface iface: the spec declares the type parameters
// methodTPs, whose constraints, as written, are methodCons. Go interface
// methods cannot have type parameters, and a call through an interface value
// is dispatched at run time, so no set of instantiations can be generated for
// it. The hint moves the parameters onto the interface, after its own; a
// name the interface already declares is listed once.
func genericInterfaceMethodError(line, col int, iface *transpiler.TypeMetadata, methodName string, methodTPs []string, methodCons map[string]string) error {
	typeName := iface.Name
	var decl []string
	seen := make(map[string]bool)
	add := func(tp, constraint string) {
		if !seen[tp] {
			seen[tp] = true
			decl = append(decl, tp+" "+constraint)
		}
	}
	for _, tp := range iface.TypeParams {
		add(tp, iface.TypeParamConstraints[tp])
	}
	for _, tp := range methodTPs {
		add(tp, methodCons[tp])
	}
	noun := "a type parameter"
	if len(methodTPs) > 1 {
		noun = "type parameters"
	}
	return galaerr.NewCodedSemanticError(
		galaerr.CodeGenericInterfaceMethod,
		line, col,
		fmt.Sprintf("interface method %q of %q declares %s [%s]: a Go interface method cannot have type parameters", methodName, typeName, noun, strings.Join(methodTPs, ", ")),
		fmt.Sprintf("declare %s on the interface instead (type %s[%s] interface { ... }), or write %s as a generic function that takes a %s",
			strings.Join(methodTPs, ", "), typeName, strings.Join(decl, ", "), methodName, typeName),
	)
}
