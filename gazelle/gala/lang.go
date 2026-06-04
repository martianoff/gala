// Package gala implements a Gazelle language extension for the GALA language.
//
// It generates and maintains gala_library, gala_binary, gala_test, and
// gala_exec_test rules (loaded from @rules_gala//gala:defs.bzl) and resolves
// GALA import strings to Bazel labels. Import extraction is delegated to a
// helper binary ("gala imports --json <files...>") rather than re-parsing the
// grammar, mirroring the rules_python gazelle plugin's helper model.
package gala

import (
	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

// languageName is the Gazelle language identifier. It is also the prefix of the
// rule kinds this extension manages (gala_library, gala_test, ...).
const languageName = "gala"

// The defs.bzl file that the gala_* rules are loaded from. The extension only
// needs the load-string and attribute shapes — not the rules_gala source — so
// this label is hardcoded.
const galaDefs = "@rules_gala//gala:defs.bzl"

// galaLang is the language.Language implementation for GALA.
type galaLang struct {
	// runner extracts imports from a set of .gala files. It is injectable so
	// tests can supply a fake in place of shelling out to the helper binary.
	runner importRunner
}

// NewLanguage returns a new GALA Gazelle language extension. This is the entry
// point referenced by gazelle_binary's generated main.
func NewLanguage() language.Language {
	return &galaLang{runner: execRunner}
}

var (
	_ language.Language           = (*galaLang)(nil)
	_ config.Configurer           = (*galaLang)(nil)
	_ language.FinishableLanguage = (*galaLang)(nil)
)

// Name implements resolve.Resolver / language.Language.
func (*galaLang) Name() string { return languageName }

// Kinds implements language.Language. It describes how each managed rule kind
// is matched and merged.
func (*galaLang) Kinds() map[string]rule.KindInfo {
	srcsMergeable := map[string]bool{"srcs": true}
	depsResolvable := map[string]bool{"deps": true, "gala_deps": true}
	return map[string]rule.KindInfo{
		"gala_library": {
			MatchAttrs:    []string{"importpath"},
			NonEmptyAttrs: map[string]bool{"srcs": true, "src": true},
			MergeableAttrs: map[string]bool{
				"srcs":       true,
				"src":        true,
				"importpath": true,
			},
			ResolveAttrs: depsResolvable,
		},
		"gala_binary": {
			NonEmptyAttrs:  map[string]bool{"srcs": true, "src": true},
			MergeableAttrs: srcsMergeable,
			ResolveAttrs:   depsResolvable,
		},
		"gala_test": {
			NonEmptyAttrs:  map[string]bool{"srcs": true, "src": true},
			MergeableAttrs: srcsMergeable,
			ResolveAttrs:   depsResolvable,
		},
		"gala_exec_test": {
			NonEmptyAttrs:  map[string]bool{"src": true},
			MergeableAttrs: map[string]bool{"src": true, "expected": true},
			ResolveAttrs:   depsResolvable,
		},
	}
}

// Loads implements language.Language. Every managed kind loads from the same
// rules_gala defs.bzl file.
func (*galaLang) Loads() []rule.LoadInfo {
	return []rule.LoadInfo{
		{
			Name: galaDefs,
			Symbols: []string{
				"gala_binary",
				"gala_exec_test",
				"gala_library",
				"gala_test",
			},
		},
	}
}

// Fix implements language.Language. GALA has no deprecated rule forms to repair.
func (*galaLang) Fix(c *config.Config, f *rule.File) {}

// DoneGeneratingRules implements language.FinishableLanguage. No background
// resources are held, so this is a no-op.
func (*galaLang) DoneGeneratingRules() {}
