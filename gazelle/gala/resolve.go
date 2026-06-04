package gala

import (
	"path"
	"sort"
	"strings"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/label"
	"github.com/bazelbuild/bazel-gazelle/repo"
	"github.com/bazelbuild/bazel-gazelle/resolve"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

// Imports implements resolve.Resolver. A gala_library is indexed by its
// importpath so other rules can resolve imports to it. gala_binary and
// gala_test are not importable, so they are not indexed (return nil).
func (*galaLang) Imports(c *config.Config, r *rule.Rule, f *rule.File) []resolve.ImportSpec {
	if r.Kind() != "gala_library" {
		return nil
	}
	importpath := r.AttrString("importpath")
	if importpath == "" {
		return nil
	}
	return []resolve.ImportSpec{{Lang: languageName, Imp: importpath}}
}

// Embeds implements resolve.Resolver. GALA rules do not embed one another.
func (*galaLang) Embeds(r *rule.Rule, from label.Label) []label.Label { return nil }

// Resolve implements resolve.Resolver. It translates each GALA import path in
// the rule's payload into a Bazel label and writes the sorted set to "deps".
func (*galaLang) Resolve(
	c *config.Config,
	ix *resolve.RuleIndex,
	rc *repo.RemoteCache,
	r *rule.Rule,
	imports interface{},
	from label.Label,
) {
	gi, ok := imports.(*galaImports)
	if !ok || gi == nil {
		return
	}
	gc := getGalaConfig(c)

	deps := map[string]bool{}
	for _, imp := range gi.imports {
		if imp == gi.self {
			continue
		}
		if gc.ImplicitDeps[imp] {
			// Injected automatically by the gala_* macros (std, test).
			continue
		}
		lbl, ok := resolveImport(gc, ix, imp, from)
		if !ok {
			continue
		}
		deps[lbl] = true
	}

	if len(deps) == 0 {
		r.DelAttr("deps")
		return
	}
	sorted := make([]string, 0, len(deps))
	for d := range deps {
		sorted = append(sorted, d)
	}
	sort.Strings(sorted)
	r.SetAttr("deps", sorted)
}

// resolveImport maps a single GALA import path to a Bazel label string. It
// reports ok=false for imports that need no GALA dependency (Go stdlib and
// other non-GALA paths).
func resolveImport(gc *galaConfig, ix *resolve.RuleIndex, imp string, from label.Label) (string, bool) {
	// Prefer an in-repo library indexed under this importpath.
	if matches := ix.FindRulesByImport(resolve.ImportSpec{Lang: languageName, Imp: imp}, languageName); len(matches) > 0 {
		return matches[0].Label.Rel(from.Repo, from.Pkg).String(), true
	}

	// In-repo namespace: under the configured prefix → //<relpath>.
	if rel, ok := relUnder(imp, gc.Prefix); ok && rel != "" {
		lbl := label.New("", rel, path.Base(rel))
		return lbl.Rel(from.Repo, from.Pkg).String(), true
	}

	// GALA stdlib namespace not present in-repo → @<repo>//<relpath>.
	if rel, ok := relUnder(imp, gc.StdlibPrefix); ok && rel != "" {
		lbl := label.New(gc.StdlibRepo, rel, path.Base(rel))
		return lbl.String(), true
	}

	// Go stdlib / external Go imports: no GALA dependency.
	return "", false
}

// relUnder returns the path of imp relative to ns ("" when imp == ns) and
// whether imp lies within the ns namespace.
func relUnder(imp, ns string) (string, bool) {
	if ns == "" {
		return "", false
	}
	if imp == ns {
		return "", true
	}
	if strings.HasPrefix(imp, ns+"/") {
		return strings.TrimPrefix(imp, ns+"/"), true
	}
	return "", false
}
