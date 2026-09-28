package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"martianoff/gala/internal/depman/mod"
	"martianoff/gala/internal/stdlib"
	"martianoff/gala/internal/transpiler/analyzer"
)

// This file holds the two cache keys `gala build` uses to skip work, and the
// declared input set of each. A key that misses an input serves stale output
// whenever only that input changes, so every input the step reads has to be
// named here — and every input class has a row in the mutation matrix in
// cachekey_test.go, which is where a new one is added first.
//
// Transpile key (.gala-source-hash) guards gen/, which the transpile step
// fills with generated Go plus copies of hand-written files:
//
//	toolchain      gala version string, content hash of the running gala
//	               executable, the Go SDK the analyzer reads types from, and
//	               the fingerprint of the stdlib snapshot
//	tree shape     the layout generated into gen/ (see Builder.treeShape)
//	.gala sources  every non-test .gala file under the project
//	module files   gala.mod, and go.mod (its module path drives import rewriting)
//	Go inputs      hand-written files the Go toolchain compiles, copied into
//	               gen/ (.go, cgo and assembly sources — see goToolchainInput),
//	               and the files their own //go:embed directives match
//	embed assets   files matched by the //go:embed patterns the previous
//	               transpile emitted (recorded next to the key, relative to the
//	               project, since Go resolves a pattern against the directory
//	               of the file that declares it)
//	dependencies   the dependency key below: the analyzer reads dependency
//	               sources while transpiling the project
//
// Dependency key (.gala-deps-hash) guards deps/, the transpiled GALA
// dependencies:
//
//	toolchain      as above
//	gala.mod       requires and replace directives
//	dependencies   for every GALA dependency the transpile visits (direct and
//	               transitive): its coordinates, the directory it is read from,
//	               and the contents of that directory — which is what makes an
//	               edit behind `replace X => ../localX` visible
//
// Everything else a build produces (go.mod, go.sum, the binary) is regenerated
// or left to the Go toolchain's own cache on every build.

// toolchainKey identifies the tools that turn sources into generated Go. The
// version string alone cannot: every unstamped build calls itself "dev", so two
// different transpilers would share a key, and the Go SDK decides the types the
// analyzer resolves for Go packages without appearing in any source file.
type toolchainKey struct {
	GalaVersion string
	Transpiler  string
	GoSDK       string
	Stdlib      string
}

// currentToolchain describes the toolchain this process transpiles with.
func currentToolchain(galaVersion string) toolchainKey {
	return toolchainKey{
		GalaVersion: galaVersion,
		Transpiler:  transpilerIdentity(),
		GoSDK:       analyzer.GoSDKIdentity(),
		Stdlib:      stdlib.Fingerprint(),
	}
}

func (t toolchainKey) writeTo(h hash.Hash) {
	fmt.Fprintf(h, "gala:%s\ntranspiler:%s\ngo:%s\nstdlib:%s\n",
		t.GalaVersion, t.Transpiler, t.GoSDK, t.Stdlib)
}

// transpilerIdentity is a content hash of the running executable (the one the
// analysis cache already computes, analyzer.BinaryHash). The transpiler is
// compiled into this binary, so its bytes are the only identity that changes
// with every change to the transpiler — including the rebuilds of an unstamped
// "dev" binary that the version string cannot tell apart.
//
// When the executable cannot be read, the identity is unique to this process:
// every build re-transpiles, which is slow but never stale.
var transpilerIdentity = sync.OnceValue(func() string {
	if id := analyzer.BinaryHash(); id != "" {
		return id
	}
	return fmt.Sprintf("unhashable:%d:%d", os.Getpid(), time.Now().UnixNano())
})

// computeSourceHash computes the transpile key over the declared input set
// described at the top of this file. files holds every file input; a file that
// cannot be read yields "", which never matches a recorded key, so the build
// re-transpiles rather than trusting a result it cannot vouch for.
//
// Two of the non-file inputs are easy to mistake for redundant:
//
//   - The stdlib is a transpile input, not just a runtime dependency: the
//     signatures declared there decide which analyses run over the project's
//     code, so repairing a stale stdlib must re-check every project built
//     against it.
//   - shape names the LAYOUT generated into gen/. `gala build` puts the library
//     at the gen root; `gala build ./cmd/app` additionally synthesizes a
//     consumer main. Keyed on contents alone the two are identical, and a plain
//     build following a subdirectory build would compile the consumer tree the
//     previous command left behind.
func computeSourceHash(files []string, tc toolchainKey, shape, depsKey string) string {
	h := sha256.New()
	tc.writeTo(h)
	fmt.Fprintf(h, "shape:%s\ndeps:%s\n", shape, depsKey)
	if !hashFiles(h, files) {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// hashFiles writes each file's path and contents to h, in sorted path order.
// It reports false when a file cannot be read.
func hashFiles(h hash.Hash, files []string) bool {
	sorted := make([]string, len(files))
	copy(sorted, files)
	sort.Strings(sorted)
	for _, f := range sorted {
		content, err := os.ReadFile(f)
		if err != nil {
			return false
		}
		fmt.Fprintf(h, "file:%s:%d\n", filepath.ToSlash(f), len(content))
		h.Write(content)
	}
	return true
}

// depInput is one GALA dependency as the dependency transpile sees it.
type depInput struct {
	Require mod.Require
	// Dir is the directory the dependency is read from: a local replacement or
	// the module cache.
	Dir string
}

// computeDepsHash computes the dependency key over the declared input set
// described at the top of this file. As with computeSourceHash, a dependency
// file that cannot be read yields "".
func computeDepsHash(requires []mod.Require, replaces []mod.Replace, tc toolchainKey, deps []depInput) string {
	h := sha256.New()
	tc.writeTo(h)
	for _, req := range requires {
		fmt.Fprintf(h, "require %s@%s\n", req.Path, req.Version)
	}
	for _, rep := range replaces {
		fmt.Fprintf(h, "replace %s@%s=>%s@%s\n", rep.Old.Path, rep.Old.Version, rep.New.Path, rep.New.Version)
	}
	sorted := make([]depInput, len(deps))
	copy(sorted, deps)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Require.Path < sorted[j].Require.Path })
	for _, dep := range sorted {
		fmt.Fprintf(h, "dep %s@%s at %s\n", dep.Require.Path, dep.Require.Version, filepath.ToSlash(dep.Dir))
		files, err := dependencyInputs(dep.Dir)
		if err != nil || !hashFiles(h, files) {
			return ""
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// walkCopiedTree visits the files of a source tree the transpile step reads or
// copies (copyNonGalaFiles is built on it), and is the one definition of that
// tree: hidden directories, vendor, testdata, bazel-* entries, symlinks and
// stale transpiler output (.gen.go) are not part of it. Entries that cannot be
// stat'd are skipped (Bazel junctions on Windows report "Incorrect function").
func walkCopiedTree(root string, visit func(path string, info os.FileInfo) error) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || path == root {
			return nil
		}
		// Bazel creates symlinks (Linux) or junctions (Windows) that may point
		// to nonexistent targets.
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		// On Windows, junctions may not have ModeDir set, so the bazel-* name is
		// checked before the IsDir() gate.
		name := info.Name()
		if strings.HasPrefix(name, "bazel-") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ".gen.go") {
			return nil
		}
		return visit(path, info)
	})
}

// dependencyInputs lists every file of a dependency's source tree that its
// transpile reads or copies. A dependency's non-.gala files are all copied into
// its output (embedded assets included), so all of them are inputs.
func dependencyInputs(dir string) ([]string, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, err
	}
	var files []string
	err := walkCopiedTree(dir, func(path string, _ os.FileInfo) error {
		files = append(files, path)
		return nil
	})
	return files, err
}

// goToolchainInput reports whether a hand-written file copied into gen/ is read
// by `go build`: Go sources, and the cgo, assembly and object inputs a package
// may carry. Other copied files only reach the binary through //go:embed, which
// the embed patterns cover (those in generated code and those in hand-written
// Go files) — keying on them too would make every build that drops its binary
// into the project directory invalidate the next one.
func goToolchainInput(name string) bool {
	switch filepath.Ext(name) {
	case ".go", ".s", ".S", ".sx", ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx",
		".m", ".f", ".F", ".for", ".f90", ".syso":
		return true
	}
	return false
}

// projectGoInputs lists the hand-written files under the project that `go
// build` reads from gen/: the Go toolchain inputs, the files their own
// //go:embed directives match, and the project's go.mod when present.
func projectGoInputs(projectDir string) ([]string, error) {
	var files, goFiles []string
	err := walkCopiedTree(projectDir, func(path string, info os.FileInfo) error {
		if goToolchainInput(info.Name()) {
			files = append(files, path)
			if filepath.Ext(path) == ".go" {
				goFiles = append(goFiles, path)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, goFile := range goFiles {
		content, err := os.ReadFile(goFile)
		if err != nil {
			return nil, err
		}
		files = append(files, embedInputs(filepath.Dir(goFile), extractEmbedPatterns(string(content)))...)
	}
	if goMod := filepath.Join(projectDir, "go.mod"); fileExists(goMod) {
		files = append(files, goMod)
	}
	return files, nil
}

// embedInputs resolves //go:embed patterns against dir the way copyEmbedFiles
// does, expanding matched directories to their files.
func embedInputs(dir string, patterns []string) []string {
	var files []string
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(filepath.Join(dir, filepath.FromSlash(pattern)))
		for _, m := range matches {
			info, err := os.Stat(m)
			if err != nil {
				continue
			}
			if !info.IsDir() {
				files = append(files, m)
				continue
			}
			filepath.Walk(m, func(path string, fi os.FileInfo, err error) error {
				if err == nil && !fi.IsDir() {
					files = append(files, path)
				}
				return nil
			})
		}
	}
	return dedupe(files)
}

func dedupe(files []string) []string {
	seen := make(map[string]bool, len(files))
	out := files[:0]
	for _, f := range files {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// The files in the workspace directory that record each key. Cleaning gen/ or
// deps/ removes the matching one (Workspace.CleanGen, Workspace.CleanDeps).
const (
	sourceStampName = ".gala-source-hash"
	depsStampName   = ".gala-deps-hash"
)

// sourceStamp is what .gala-source-hash records: the transpile key, and the
// embed patterns that transpile emitted, relative to the project directory
// (each joined to the directory of the file that declares it). The patterns
// come out of the generated code, so they are only known after transpiling;
// recording them lets the next build key on the assets they match before
// deciding whether to transpile.
type sourceStamp struct {
	Key    string
	Embeds []string
}

const embedStampPrefix = "embed "

func readSourceStamp(path string) (sourceStamp, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return sourceStamp{}, false
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	stamp := sourceStamp{Key: lines[0]}
	for _, line := range lines[1:] {
		if p, ok := strings.CutPrefix(line, embedStampPrefix); ok {
			stamp.Embeds = append(stamp.Embeds, p)
		}
	}
	return stamp, stamp.Key != ""
}

func writeSourceStamp(path string, stamp sourceStamp) {
	var sb strings.Builder
	sb.WriteString(stamp.Key)
	sb.WriteByte('\n')
	for _, p := range stamp.Embeds {
		sb.WriteString(embedStampPrefix + p + "\n")
	}
	// Best effort: a stamp that fails to write only costs the next build a
	// re-transpile.
	os.WriteFile(path, []byte(sb.String()), 0644)
}

// sourceKey computes the transpile key for the current project, with embed
// assets resolved from the given project-relative patterns. It returns "" when
// an input cannot be read.
//
// The key has two parts because the embed patterns of generated code are only
// known once transpiling is done: transpile() computes the base part before it
// transpiles and, when the patterns changed, the embed part before it copies
// the assets, so no part describes a file read later than the copy in gen/.
func (b *Builder) sourceKey(galaFiles, embeds []string) string {
	return combineSourceKey(b.baseSourceKey(galaFiles), b.embedKey(embeds))
}

// combineSourceKey joins the two parts of the transpile key; either part being
// "" (an unreadable input) makes the whole key "".
func combineSourceKey(base, embeds string) string {
	if base == "" || embeds == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("base:" + base + "\nembeds:" + embeds + "\n"))
	return hex.EncodeToString(sum[:])
}

// embedKey hashes the assets the given project-relative embed patterns match,
// or returns "" when one cannot be read.
func (b *Builder) embedKey(patterns []string) string {
	h := sha256.New()
	if !hashFiles(h, embedInputs(b.workspace.ProjectDir, patterns)) {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// baseSourceKey hashes every transpile input except the assets matched by the
// embed patterns of generated code (see embedKey).
func (b *Builder) baseSourceKey(galaFiles []string) string {
	files := append([]string(nil), galaFiles...)
	if galaMod := filepath.Join(b.workspace.ProjectDir, "gala.mod"); fileExists(galaMod) {
		files = append(files, galaMod)
	}
	goInputs, err := projectGoInputs(b.workspace.ProjectDir)
	if err != nil {
		return ""
	}
	files = dedupe(append(files, goInputs...))
	depsKey, err := b.depsKey()
	if err != nil {
		return ""
	}
	return computeSourceHash(files, b.toolchain(), b.treeShape(), depsKey)
}

// toolchain returns the toolchain identity the builder keys on. Tests inject
// one through the toolchainID field; production builders compute it lazily so
// constructing a builder costs nothing.
func (b *Builder) toolchain() toolchainKey {
	if b.toolchainID == nil {
		tc := currentToolchain(b.stdlibVersion)
		b.toolchainID = &tc
	}
	return *b.toolchainID
}

// galaDependencies lists the GALA dependencies the dependency transpile
// visits, direct and transitive, each with the directory it is read from. It
// reuses the transpile's own traversal so the key and the work cannot drift
// apart.
func (b *Builder) galaDependencies() []depInput {
	dt := NewDepTranspiler(b.config, b.workspace, b.galaMod, b.stdlibVersion, false)
	all := make(map[string]mod.Require)
	dt.collectGalaDeps(b.galaMod, all, make(map[string]bool))
	deps := make([]depInput, 0, len(all))
	for _, req := range all {
		deps = append(deps, depInput{Require: req, Dir: dt.effectiveDepDir(req)})
	}
	return deps
}

// depsKey returns the dependency key, computed once per builder. It is "" when
// the project has no GALA dependencies, and an error when one of them cannot be
// read — the caller then treats the cache as a miss.
func (b *Builder) depsKey() (string, error) {
	if b.depsKeyValue != nil {
		return *b.depsKeyValue, nil
	}
	key := ""
	if b.galaMod != nil && len(b.galaMod.GalaRequires()) > 0 {
		galaReqs := b.galaMod.GalaRequires()
		key = computeDepsHash(galaReqs, b.galaMod.Replace, b.toolchain(), b.galaDependencies())
		if key == "" {
			return "", fmt.Errorf("a GALA dependency could not be read")
		}
	}
	b.depsKeyValue = &key
	return key, nil
}
