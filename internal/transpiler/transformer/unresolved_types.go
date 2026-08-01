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
// last of all, `any`. That layering is why GALA compiles as much as it does.
//
// It is also why type bugs stay hidden. A NilType that some later fallback
// rescues looks identical, from the outside, to one that nothing rescues.
// Users only file the second kind, so the reported bugs are a sample of the
// unresolved sites rather than the whole set, and fixing one says nothing
// about the rest.
//
// UnresolvedType records the first kind alongside the second. Every
// expression whose type could not be determined is logged with its source
// position, whatever the caller went on to do about it. The result is a
// finite, ranked list of places inference gives up — which is the thing that
// can be driven down deliberately, instead of waiting for whichever site a
// user trips over next.
//
// Recording is gated on GALA_WARN_TYPES=1 so a normal transpile pays nothing.

// UnresolvedType is one expression whose type the transformer could not
// determine.
type UnresolvedType struct {
	// File is the .gala source being transformed.
	File string
	// Line and Col are the last known source position. The transformer builds
	// Go AST nodes without positions, so this is the enclosing GALA construct's
	// position rather than the expression's own — close enough to find it.
	Line int
	Col  int
	// Expr renders the Go expression that could not be typed.
	Expr string
	// Site names the query that gave up, so sites can be grouped by cause.
	Site string
}

// recordUnresolved logs an expression whose type could not be determined.
//
// It is called from the type-query choke points rather than from each
// individual `return NilType{}`. Most of those returns are interior: a helper
// reporting "not my case" to a sibling that then succeeds. Logging them would
// bury the real signal. What matters is the boundary — a top-level query that
// has exhausted its routes and is handing NilType back to a caller that must
// now fall back.
func (t *galaASTTransformer) recordUnresolved(site string, expr ast.Expr) {
	if !t.warnTypeInference || t.hasNoTypeByConstruction(expr) {
		return
	}
	t.unresolvedTypes = append(t.unresolvedTypes, UnresolvedType{
		File: t.filePath,
		Line: t.lastLine,
		Col:  t.lastCol,
		Expr: renderExprForDiagnostics(expr),
		Site: site,
	})
}

// noteUnresolved records typ when it is the give-up answer and returns it
// unchanged, so a query can be instrumented in place:
//
//	return t.noteUnresolved("getExprTypeNameManual", expr, result)
func (t *galaASTTransformer) noteUnresolved(site string, expr ast.Expr, typ transpiler.Type) transpiler.Type {
	if typ == nil || typ.IsNil() {
		t.recordUnresolved(site, expr)
	}
	return typ
}

// UnresolvedTypes returns the deduplicated unresolved-type inventory collected
// by the most recent transform. It is empty unless GALA_WARN_TYPES=1 was set.
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

// hasNoTypeByConstruction reports whether asking for this expression's type is
// a category error rather than a failed inference.
//
// The type query is called on whatever the traversal is holding, which is not
// always a value. A package qualifier is the common case: the `fmt` in
// `fmt.Println` is a name in package scope, not an expression, and it has no
// type for the same reason a keyword has none. Counting those would swamp the
// inventory — they were the majority of the first run — and none of them is a
// place inference could be improved.
//
// This is deliberately narrow. Anything genuinely value-shaped stays in, even
// when the transformer has a good reason to fail on it, because "we have a
// reason" is how a real gap gets filtered out and stops being counted.
func (t *galaASTTransformer) hasNoTypeByConstruction(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return false
	}
	// A local val or param shadowing a package name is a value; the scope
	// lookup wins over the package-name check.
	if !t.getValType(ident.Name).IsNil() {
		return false
	}
	return t.isPackageQualifier(ident.Name)
}

// isPackageQualifier reports whether name refers to a package rather than a
// value.
//
// importManager alone is not enough. GALA's `Println` needs no import in
// source — the transformer synthesizes the `fmt` qualifier during lowering —
// so the qualifier can be queried before, or without, any import entry
// existing for it. Names known from Go type info cover that: they are keyed
// "pkg.Name", so their prefixes are exactly the package qualifiers that can
// appear in generated code.
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

// synthesizedImportQualifiers are the package qualifiers the transformer emits
// on its own, without a corresponding import in GALA source.
//
// `Println` lowers to `fmt.Println`, a rune-length operation lowers to
// `utf8.RuneCountInString`, an embed val lowers to an `embed` directive. In
// each case the qualifier is created during lowering and the import is added
// afterwards, so it is not in importManager at the point the type query runs.
// Go type info would normally supply these names, but it is unavailable
// whenever the Go SDK is not on the path — under the Bazel test sandbox, for
// one — and the inventory should not measure something different there.
//
// This mirrors the needs*Import flags on the transformer; a new synthesized
// import belongs in both places.
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

// dedupeUnresolved collapses repeats of the same site.
//
// A failed lookup is not cached — it may succeed once more scope is known — so
// one expression can be queried, and recorded, several times. Counting those
// repeats would make the inventory track how often the transformer retries
// rather than how many places it gives up. Order is preserved so the report
// follows the source.
func dedupeUnresolved(in []UnresolvedType) []UnresolvedType {
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

// renderExprForDiagnostics prints a Go expression compactly for a diagnostic
// line, collapsing any wrapped output onto one line so an inventory entry
// stays greppable. Rendering itself is formatExprForTrace's job.
func renderExprForDiagnostics(expr ast.Expr) string {
	return strings.Join(strings.Fields(formatExprForTrace(expr)), " ")
}
