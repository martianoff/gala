package build

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"

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

// GoRequirementsOf returns the requirements of goMod as gala.mod Go
// requirements, in go.mod's order and at its versions: what `gala mod init`
// copies, and what `gala doctor` expects gala.mod to agree with. `gala build`
// takes the version of a Go module from gala.mod, never from go.mod, so a
// module left out would build at whatever version the build workspace's `go
// mod tidy` picks: its latest when nothing else requires it.
//
// Indirect requirements are included too, marked indirect. Since Go 1.17
// go.mod lists every module the build needs, and an indirect requirement is
// how it raises a dependency's dependency above the version that dependency
// asks for; without it, the build would select the lower version go build
// does not.
//
// Two kinds are left out, and are returned in skipped with the reason:
// a module go.mod replaces at the version it requires, whose version (often a
// v0.0.0 placeholder for a local directory) means nothing without the replace
// directive, which gala build does not apply to Go dependencies; and a GALA
// standard library package, which gala build always provides itself.
func GoRequirementsOf(goMod *modfile.File) (reqs []mod.Require, skipped []string) {
	replaces := goModReplaces(goMod)
	replaced := func(path, ver string) bool {
		for _, old := range replaces {
			if old.Path == path && (old.Version == "" || old.Version == ver) {
				return true
			}
		}
		return false
	}
	reqs = make([]mod.Require, 0, len(goMod.Require))
	for _, r := range goMod.Require {
		path, ver := r.Mod.Path, r.Mod.Version
		switch {
		case replaced(path, ver):
			skipped = append(skipped, fmt.Sprintf("%s %s (go.mod replaces it; gala build does not apply replace directives to Go dependencies)", path, ver))
		// The build workspace's go.mod generator leaves these out of Go
		// requirements: it requires the GALA module's packages itself.
		case strings.HasPrefix(path, GalaModulePrefix):
			skipped = append(skipped, fmt.Sprintf("%s %s (a package of the GALA module, which gala build provides itself)", path, ver))
		default:
			reqs = append(reqs, mod.Require{Path: path, Version: ver, Indirect: r.Indirect, Go: true})
		}
	}
	return reqs, skipped
}

// goModReplaces returns the left-hand side of each replace directive of f:
// the module path, and the version the directive is limited to ("" for every
// version). It reads f's syntax tree, since a lax parse leaves f.Replace
// empty.
func goModReplaces(f *modfile.File) []module.Version {
	var olds []module.Version
	add := func(tokens []string) {
		if len(tokens) == 0 {
			return
		}
		old := module.Version{Path: unquoteGoModToken(tokens[0])}
		if len(tokens) > 1 && tokens[1] != "=>" {
			old.Version = unquoteGoModToken(tokens[1])
		}
		olds = append(olds, old)
	}
	if f.Syntax == nil {
		return nil
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
	return olds
}

// unquoteGoModToken returns a go.mod token without its quotes, when it has
// them.
func unquoteGoModToken(tok string) string {
	if s, err := strconv.Unquote(tok); err == nil {
		return s
	}
	return tok
}
