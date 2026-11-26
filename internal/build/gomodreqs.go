package build

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/mod/modfile"

	"martianoff/gala/internal/depman/mod"
)

// ReadGoMod parses the go.mod at path; nil, without an error, when there is
// none. The parse is lax, so a directive newer than this gala's x/mod (such as
// Go 1.25's ignore) is skipped rather than an error; it also leaves out the
// replace directives, which goModReplaces reads from the syntax tree.
func ReadGoMod(path string) (*modfile.File, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	f, err := modfile.ParseLax(path, data, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", path, err)
	}
	return f, nil
}

// GoRequirementsOf returns the requirements and replace directives of goMod
// as gala.mod Go requirements and replaces, in go.mod's order and at its
// versions: what `gala mod init` copies, and what `gala doctor` expects
// gala.mod to agree with. `gala build` takes the version of a Go module, and
// what replaces it, from gala.mod, never from go.mod, so a module left out
// would build at whatever version the build workspace's `go mod tidy` picks:
// its latest when nothing else requires it.
//
// Indirect requirements are included too, marked indirect. Since Go 1.17
// go.mod lists every module the build needs, and an indirect requirement is
// how it raises a dependency's dependency above the version that dependency
// asks for; without it, the build would select the lower version go build
// does not.
//
// A requirement or replace of a GALA standard library package is left out,
// and returned in skipped with the reason: gala build always provides those
// packages itself. So is a replace whose path has a space in it, which
// gala.mod has no way to write, with the requirement it applies to: its
// version, often a placeholder for a directory, means nothing without it.
func GoRequirementsOf(goMod *modfile.File) (reqs []mod.Require, replaces []mod.Replace, skipped []string) {
	all := &mod.File{}
	var replacesSkipped []string
	for _, rep := range goModReplaces(goMod) {
		switch {
		case strings.HasPrefix(rep.Old.Path, GalaModulePrefix):
			replacesSkipped = append(replacesSkipped, fmt.Sprintf("replace %s (a package of the GALA module, which gala build provides itself)", rep.Old.Path))
			continue
		case unwritableReplace(rep):
			replacesSkipped = append(replacesSkipped, fmt.Sprintf("replace %s (gala.mod cannot hold a path with a space; add the replace to gala.mod with a path without one)", rep.Old.Path))
		default:
			replaces = append(replaces, rep)
		}
		all.Replace = append(all.Replace, rep)
	}
	reqs = make([]mod.Require, 0, len(goMod.Require))
	for _, r := range goMod.Require {
		path, ver := r.Mod.Path, r.Mod.Version
		// The build workspace's go.mod generator leaves these out of Go
		// requirements: it requires the GALA module's packages itself.
		if strings.HasPrefix(path, GalaModulePrefix) {
			skipped = append(skipped, fmt.Sprintf("%s %s (a package of the GALA module, which gala build provides itself)", path, ver))
			continue
		}
		if rep, ok := all.ReplaceFor(path, ver); ok && unwritableReplace(rep) {
			skipped = append(skipped, fmt.Sprintf("%s %s (go.mod replaces it with a path gala.mod cannot hold)", path, ver))
			continue
		}
		reqs = append(reqs, mod.Require{Path: path, Version: ver, Indirect: r.Indirect, Go: true})
	}
	return reqs, replaces, append(skipped, replacesSkipped...)
}

// UnwritableGoModReplaces returns the modules (path and version limit) that a
// replace directive of goMod replaces with a path gala.mod cannot hold, which
// GoRequirementsOf leaves out. gala.mod has to replace those with another path.
func UnwritableGoModReplaces(goMod *modfile.File) map[mod.ModuleVersion]bool {
	olds := make(map[mod.ModuleVersion]bool)
	for _, rep := range goModReplaces(goMod) {
		if unwritableReplace(rep) {
			olds[rep.Old] = true
		}
	}
	return olds
}

// unwritableReplace reports whether gala.mod cannot hold rep: its replacement
// path has a space in it.
func unwritableReplace(rep mod.Replace) bool {
	return strings.ContainsFunc(rep.New.Path, unicode.IsSpace)
}

// goModReplaces returns the replace directives of f. It reads f's syntax
// tree, since a lax parse leaves f.Replace empty.
func goModReplaces(f *modfile.File) []mod.Replace {
	if f.Syntax == nil {
		return nil
	}
	var reps []mod.Replace
	add := func(tokens []string) {
		unquoted := make([]string, len(tokens))
		for i, tok := range tokens {
			unquoted[i] = unquoteGoModToken(tok)
		}
		if rep, err := mod.ParseReplaceEntry(unquoted); err == nil {
			reps = append(reps, rep)
		}
	}
	for _, stmt := range f.Syntax.Stmt {
		switch s := stmt.(type) {
		case *modfile.Line:
			if len(s.Token) > 0 && s.Token[0] == "replace" {
				add(s.Token[1:])
			}
		case *modfile.LineBlock:
			if len(s.Token) > 0 && s.Token[0] == "replace" {
				for _, l := range s.Line {
					add(l.Token)
				}
			}
		}
	}
	return reps
}

// unquoteGoModToken returns a go.mod token without its quotes, when it has
// them.
func unquoteGoModToken(tok string) string {
	if s, err := strconv.Unquote(tok); err == nil {
		return s
	}
	return tok
}
