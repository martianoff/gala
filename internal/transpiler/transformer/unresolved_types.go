package transformer

import (
	"go/ast"
	"strings"

	"martianoff/gala/internal/transpiler"
)

// Unresolved-type inventory
//
// transpiler.NilType is the "I could not work out this type" answer. It is not
// an error: a caller that receives it usually has a fallback — another
// inference route, a declared annotation, an expected type from context, or,
// last of all, `any`.
//
// That layering is also why type bugs stay hidden. A NilType some later
// fallback rescues looks identical, from the outside, to one nothing rescues.
// Users only file the second kind, so the reported bugs are a sample of the
// unresolved sites rather than the whole set, and fixing one says nothing about
// the rest.
//
// UnresolvedType records both kinds, with a source position, whatever the
// caller went on to do. The result is a finite, ranked list of places inference
// gives up, which can be driven down deliberately.
//
// Recording is gated on GALA_WARN_TYPES=1 so a normal transpile pays nothing.
// The gate is tested by the caller, before this file is reached.

// UnresolvedType is one expression whose type the transformer could not
// determine.
type UnresolvedType struct {
	// Line and Col are the last known source position. The transformer builds
	// Go AST nodes without positions, so this is the enclosing GALA
	// construct's position rather than the expression's own — close enough to
	// find it. The file is not recorded: a transform covers one file, and both
	// readers already know which.
	Line int
	Col  int
	// Expr renders the Go expression that could not be typed.
	Expr string
}

// recordUnresolved logs an expression whose type could not be determined.
// Callers test warnTypeInference first; see getExprTypeNameManual.
//
// It is called from the type-query choke point rather than from each
// individual `return NilType{}`. Most of those returns are interior: a helper
// reporting "not my case" to a sibling that then succeeds. Logging them would
// bury the real signal. What matters is a top-level query that has exhausted
// its routes and is handing NilType back to a caller that must now fall back.
func (t *galaASTTransformer) recordUnresolved(expr ast.Expr) {
	if t.hasNoTypeByConstruction(expr) {
		return
	}
	// Deduplicate on the AST node, before rendering. A failed lookup is not
	// cached — it may succeed once more scope is known — so one expression can
	// be queried, and reach here, several times. Rendering runs go/printer and
	// allocates a FileSet per call, so paying it per query rather than per
	// distinct expression is most of the cost of having diagnostics on.
	if t.unresolvedSeen == nil {
		t.unresolvedSeen = map[ast.Expr]bool{}
	}
	if t.unresolvedSeen[expr] {
		return
	}
	t.unresolvedSeen[expr] = true

	t.unresolvedTypes = append(t.unresolvedTypes, UnresolvedType{
		Line: t.lastLine,
		Col:  t.lastCol,
		Expr: strings.Join(strings.Fields(formatExprForTrace(expr)), " "),
	})
}

// UnresolvedTypes returns the unresolved-type inventory collected by the most
// recent transform. It is empty unless GALA_WARN_TYPES=1 was set.
//
// Exported for the corpus harness, which lives outside this package because
// the analyzer imports the transformer.
//
// Returns nil for a transformer this package did not construct.
func UnresolvedTypes(a transpiler.ASTTransformer) []UnresolvedType {
	t, ok := a.(*galaASTTransformer)
	if !ok {
		return nil
	}
	return dedupeUnresolved(t.unresolvedTypes)
}

// dedupeUnresolved collapses entries that describe the same site.
//
// Recording already skips an AST node it has seen, which is what keeps the
// rendering cost down. This is the second, coarser pass: distinct nodes can
// still render identically at one position — a desugaring that builds the same
// sub-expression twice, say — and to anyone reading the report those are one
// place to go and look, not two. Order is preserved so the report follows the
// source.
func dedupeUnresolved(in []UnresolvedType) []UnresolvedType {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[UnresolvedType]bool, len(in))
	out := make([]UnresolvedType, 0, len(in))
	for _, u := range in {
		if seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out
}

// hasNoTypeByConstruction reports whether asking for this expression's type is
// a category error rather than a failed inference.
//
// The type query is called on whatever the traversal is holding, which is not
// always a value. A package qualifier is the common case: the `fmt` in
// `fmt.Println` is a name in package scope, not an expression, and has no type
// for the same reason a keyword has none. Counting those would swamp the
// inventory — they were the majority of the first run — and none is a place
// inference could be improved.
//
// Deliberately narrow, and it stays that way. Generic function names were also
// most of an early inventory, and filtering them here looked equally
// justified — but the filter resolved names better than the instantiation path
// it deferred to, so in a package other than main it would have suppressed
// exactly the reports that matter. They are kept out at the source instead, by
// not asking: see the function-name early-out in unwrapImmutable.
//
// The lesson generalises. Anything genuinely value-shaped stays in, even when
// the transformer has a good reason to fail on it, because "we have a reason"
// is how a real gap gets filtered out and stops being counted.
func (t *galaASTTransformer) hasNoTypeByConstruction(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return false
	}
	// A local val or param shadowing a package or function name is a value; the
	// scope lookup wins over the checks below.
	if !t.getValType(ident.Name).IsNil() {
		return false
	}
	return t.isPackageQualifier(ident.Name)
}

// isPackageQualifier reports whether name refers to a package rather than a
// value.
//
// importManager alone is not enough. Three imports are synthesized during
// lowering rather than declared in GALA source — `Println` becomes
// `fmt.Println`, a rune-length operation becomes `utf8.RuneCountInString`, an
// embed val needs `embed` — and the import is added after lowering, so the
// qualifier can be queried before any entry exists for it.
//
// Those three are named, not read off the matching needs*Import flags, and the
// distinction matters: the flags record that a synthesized import *has been*
// emitted, so they are set as lowering proceeds. A qualifier queried before
// its flag is set would not be recognised. Deriving from the flags was tried
// and let 21 corpus sites through for exactly that reason.
//
// Names known from Go type info cover the rest: they are keyed "pkg.Name", so
// their prefixes are exactly the package qualifiers that can appear in
// generated code. That source is empty when the Go SDK is not on the path —
// under the Bazel test sandbox, for one — which is the other reason the
// synthesized three are listed rather than discovered.
func (t *galaASTTransformer) isPackageQualifier(name string) bool {
	if t.importManager.IsPackage(name) {
		return true
	}
	if synthesizedImportQualifiers[name] {
		return true
	}
	if t.diagPackageNames == nil {
		t.diagPackageNames = t.collectGoPackageNames()
	}
	return t.diagPackageNames[name]
}

// synthesizedImportQualifiers are the qualifiers the transformer emits without
// a corresponding import in GALA source. They pair with the needsFmtImport /
// needsUtf8Import / needsEmbedImport flags and the import-injection blocks in
// transformer.go; a fourth synthesized import belongs in all three places.
//
// The real fix is for lowering to record the qualifier into ImportManager at
// the point it emits it, which would make IsPackage answer for these too and
// delete this list. That is a change to the import mechanism, not to
// diagnostics.
var synthesizedImportQualifiers = map[string]bool{
	"fmt":   true,
	"utf8":  true,
	"embed": true,
}

// collectGoPackageNames derives the package qualifiers present in Go type info
// from its "pkg.Name" keys. Built once per transform; only reached under
// GALA_WARN_TYPES=1.
func (t *galaASTTransformer) collectGoPackageNames() map[string]bool {
	names := map[string]bool{}
	if t.goTypeInfo == nil {
		return names
	}
	addPrefix := func(key string) {
		if i := strings.Index(key, "."); i > 0 {
			names[key[:i]] = true
		}
	}
	for k := range t.goTypeInfo.Functions {
		addPrefix(k)
	}
	for k := range t.goTypeInfo.Types {
		addPrefix(k)
	}
	for k := range t.goTypeInfo.Variables {
		addPrefix(k)
	}
	for k := range t.goTypeInfo.Constants {
		addPrefix(k)
	}
	for k := range t.goTypeInfo.TypeAliases {
		addPrefix(k)
	}
	return names
}
