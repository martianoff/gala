// Package mod provides parsing and writing of gala.mod files.
package mod

// File represents a parsed gala.mod file.
type File struct {
	Module  Module    // Module path declaration
	Gala    string    // Minimum GALA version (e.g., "1.0")
	Require []Require // Direct and indirect dependencies
	Replace []Replace // Path substitutions
	Exclude []Exclude // Excluded versions
}

// Module represents the module declaration in gala.mod.
type Module struct {
	Path string // Module path (e.g., "github.com/user/project")
}

// Require represents a dependency in gala.mod.
type Require struct {
	Path     string // Module path (e.g., "github.com/example/utils")
	Version  string // Version constraint (e.g., "v1.2.3", "^1.0.0")
	Indirect bool   // True if this is a transitive dependency
	Go       bool   // True if this is a Go (not GALA) dependency
}

// Replace represents a path substitution in gala.mod.
type Replace struct {
	Old ModuleVersion // Original module path and optional version
	New ModuleVersion // Replacement path (can be local path)
}

// Exclude represents a version exclusion in gala.mod.
type Exclude struct {
	Path    string // Module path
	Version string // Specific version to exclude
}

// ModuleVersion represents a module path with an optional version.
type ModuleVersion struct {
	Path    string // Module path or local filesystem path
	Version string // Version (empty for local paths)
}

// IsLocal returns true if the ModuleVersion represents a local path: a
// relative one (./, ../) or a rooted one, a Windows drive path among them.
func (mv ModuleVersion) IsLocal() bool {
	p := mv.Path
	if mv.Version != "" || p == "" {
		return false
	}
	driveLetter := len(p) >= 2 && p[1] == ':' && ('a' <= p[0]|0x20 && p[0]|0x20 <= 'z')
	return p[0] == '.' || p[0] == '/' || p[0] == '\\' || driveLetter
}

// NewFile creates a new empty gala.mod file with the given module path.
func NewFile(modulePath string) *File {
	return &File{
		Module:  Module{Path: modulePath},
		Require: make([]Require, 0),
		Replace: make([]Replace, 0),
		Exclude: make([]Exclude, 0),
	}
}

// AddRequire adds a dependency to the file.
// If the dependency already exists, it updates the version.
func (f *File) AddRequire(path, version string, indirect bool) {
	for i := range f.Require {
		if f.Require[i].Path == path {
			f.Require[i].Version = version
			f.Require[i].Indirect = indirect
			return
		}
	}
	f.Require = append(f.Require, Require{
		Path:     path,
		Version:  version,
		Indirect: indirect,
	})
}

// RemoveRequire removes a dependency from the file.
func (f *File) RemoveRequire(path string) bool {
	for i := range f.Require {
		if f.Require[i].Path == path {
			f.Require = append(f.Require[:i], f.Require[i+1:]...)
			return true
		}
	}
	return false
}

// GetRequire returns the Require entry for a given path, or nil if not found.
func (f *File) GetRequire(path string) *Require {
	for i := range f.Require {
		if f.Require[i].Path == path {
			return &f.Require[i]
		}
	}
	return nil
}

// ReplaceFor returns the replace directive that applies to a module required
// at path and version. As in Go, one for that version wins over one for every
// version of path. A nil File has none.
func (f *File) ReplaceFor(path, version string) (Replace, bool) {
	if f == nil {
		return Replace{}, false
	}
	var any *Replace
	for i, r := range f.Replace {
		switch {
		case r.Old.Path != path:
		case r.Old.Version == version && version != "":
			return r, true
		case r.Old.Version == "" && any == nil:
			any = &f.Replace[i]
		}
	}
	if any == nil {
		return Replace{}, false
	}
	return *any, true
}

// GoReplaces returns the replace directives of a module the file requires as a
// Go dependency.
func (f *File) GoReplaces() []Replace {
	goReqs := make(map[string]bool)
	for _, r := range f.GoRequires() {
		goReqs[r.Path] = true
	}
	var reps []Replace
	for _, r := range f.Replace {
		if goReqs[r.Old.Path] {
			reps = append(reps, r)
		}
	}
	return reps
}

// AddReplace adds a path replacement to the file.
func (f *File) AddReplace(oldPath, oldVersion, newPath, newVersion string) {
	for i := range f.Replace {
		if f.Replace[i].Old.Path == oldPath && f.Replace[i].Old.Version == oldVersion {
			f.Replace[i].New = ModuleVersion{Path: newPath, Version: newVersion}
			return
		}
	}
	f.Replace = append(f.Replace, Replace{
		Old: ModuleVersion{Path: oldPath, Version: oldVersion},
		New: ModuleVersion{Path: newPath, Version: newVersion},
	})
}

// AddExclude adds a version exclusion to the file.
func (f *File) AddExclude(path, version string) {
	for i := range f.Exclude {
		if f.Exclude[i].Path == path && f.Exclude[i].Version == version {
			return
		}
	}
	f.Exclude = append(f.Exclude, Exclude{
		Path:    path,
		Version: version,
	})
}

// DirectRequires returns only the direct (non-indirect) dependencies.
func (f *File) DirectRequires() []Require {
	direct := make([]Require, 0)
	for _, r := range f.Require {
		if !r.Indirect {
			direct = append(direct, r)
		}
	}
	return direct
}

// IndirectRequires returns only the indirect dependencies.
func (f *File) IndirectRequires() []Require {
	indirect := make([]Require, 0)
	for _, r := range f.Require {
		if r.Indirect {
			indirect = append(indirect, r)
		}
	}
	return indirect
}

// GalaRequires returns only the GALA dependencies (not Go packages).
func (f *File) GalaRequires() []Require {
	galaReqs := make([]Require, 0)
	for _, r := range f.Require {
		if !r.Go {
			galaReqs = append(galaReqs, r)
		}
	}
	return galaReqs
}

// GoRequires returns only the Go dependencies.
func (f *File) GoRequires() []Require {
	goReqs := make([]Require, 0)
	for _, r := range f.Require {
		if r.Go {
			goReqs = append(goReqs, r)
		}
	}
	return goReqs
}
