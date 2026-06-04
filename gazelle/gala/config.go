package gala

import (
	"flag"
	"strings"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

// Directive keys understood by the GALA extension.
const (
	// galaPrefixDirective sets the import-path prefix for in-repo packages.
	// The importpath of a generated gala_library is <prefix>/<relpath>.
	galaPrefixDirective = "gala_prefix"
	// galaHelperDirective sets the path to the helper binary used to extract
	// imports ("gala imports --json <files...>"). Defaults to "gala" on PATH.
	galaHelperDirective = "gala_helper"
	// galaStdlibPrefixDirective sets the import-path namespace of the GALA
	// standard library. Imports under this namespace that are not found in the
	// repo are mapped to the external stdlib repo.
	galaStdlibPrefixDirective = "gala_stdlib_prefix"
	// galaStdlibRepoDirective sets the Bazel external repo name used for GALA
	// stdlib imports not present in-repo (e.g. "@gala//collection_immutable").
	galaStdlibRepoDirective = "gala_stdlib_repo"
	// galaImplicitDepDirective replaces the set of import paths that the
	// gala_* macros inject automatically and which must NOT be emitted in
	// "deps". Space-separated list; default is the std and test packages.
	galaImplicitDepDirective = "gala_implicit_dep"
)

const (
	defaultPrefix       = "martianoff/gala"
	defaultHelper       = "gala"
	defaultStdlibPrefix = "martianoff/gala"
	defaultStdlibRepo   = "gala"
)

// galaConfig is the per-directory configuration for the GALA extension. A copy
// is threaded through config.Config.Exts keyed by the language name.
type galaConfig struct {
	// Prefix is the import-path prefix for in-repo GALA packages.
	Prefix string
	// Helper is the path to the import-extraction helper binary.
	Helper string
	// StdlibPrefix is the import namespace of the GALA standard library.
	StdlibPrefix string
	// StdlibRepo is the Bazel external repo name for out-of-repo stdlib imports.
	StdlibRepo string
	// ImplicitDeps maps import paths injected by the gala_* macros. Imports in
	// this set are skipped when computing "deps".
	ImplicitDeps map[string]bool
}

func newGalaConfig() *galaConfig {
	return &galaConfig{
		Prefix:       defaultPrefix,
		Helper:       defaultHelper,
		StdlibPrefix: defaultStdlibPrefix,
		StdlibRepo:   defaultStdlibRepo,
		ImplicitDeps: map[string]bool{
			defaultStdlibPrefix + "/std":  true,
			defaultStdlibPrefix + "/test": true,
		},
	}
}

func (gc *galaConfig) clone() *galaConfig {
	cp := *gc
	cp.ImplicitDeps = make(map[string]bool, len(gc.ImplicitDeps))
	for k, v := range gc.ImplicitDeps {
		cp.ImplicitDeps[k] = v
	}
	return &cp
}

// getGalaConfig returns the GALA config for the current directory, creating a
// root one on first access.
func getGalaConfig(c *config.Config) *galaConfig {
	if gc, ok := c.Exts[languageName].(*galaConfig); ok {
		return gc
	}
	gc := newGalaConfig()
	c.Exts[languageName] = gc
	return gc
}

// RegisterFlags implements config.Configurer.
func (*galaLang) RegisterFlags(fs *flag.FlagSet, cmd string, c *config.Config) {
	gc := newGalaConfig()
	c.Exts[languageName] = gc
	fs.StringVar(&gc.Helper, "gala_helper", gc.Helper,
		"path to the GALA helper binary used to extract imports (gala imports --json)")
	fs.StringVar(&gc.Prefix, "gala_prefix", gc.Prefix,
		"import-path prefix for in-repo GALA packages")
}

// CheckFlags implements config.Configurer.
func (*galaLang) CheckFlags(fs *flag.FlagSet, c *config.Config) error { return nil }

// KnownDirectives implements config.Configurer.
func (*galaLang) KnownDirectives() []string {
	return []string{
		galaPrefixDirective,
		galaHelperDirective,
		galaStdlibPrefixDirective,
		galaStdlibRepoDirective,
		galaImplicitDepDirective,
	}
}

// Configure implements config.Configurer. It starts from a clone of the parent
// directory's config and applies any directives found in this directory's
// build file.
func (*galaLang) Configure(c *config.Config, rel string, f *rule.File) {
	var gc *galaConfig
	if parent, ok := c.Exts[languageName].(*galaConfig); ok {
		gc = parent.clone()
	} else {
		gc = newGalaConfig()
	}
	c.Exts[languageName] = gc

	if f == nil {
		return
	}
	for _, d := range f.Directives {
		value := strings.TrimSpace(d.Value)
		switch d.Key {
		case galaPrefixDirective:
			gc.Prefix = value
		case galaHelperDirective:
			gc.Helper = value
		case galaStdlibPrefixDirective:
			gc.StdlibPrefix = value
		case galaStdlibRepoDirective:
			gc.StdlibRepo = value
		case galaImplicitDepDirective:
			gc.ImplicitDeps = map[string]bool{}
			for _, dep := range strings.Fields(value) {
				if dep != "" && dep != "none" {
					gc.ImplicitDeps[dep] = true
				}
			}
		}
	}
}
