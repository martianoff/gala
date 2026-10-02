package analyzer

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/build/constraint"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/module"
)

// goImporter is a cached importer that tries multiple strategies.
// Created once and reused across all AnalyzeGoPackage calls.
var (
	goImporterOnce sync.Once
	goImporterInst types.Importer
)

// goImporterAvailable tracks whether we have a working Go importer.
var goImporterAvailable bool

var (
	goSDKRootOnce sync.Once
	goSDKRoot     string
)

// GoSDKRoot returns the root of the Go SDK the analyzer reads Go package types
// from, or "" when none is found. It is resolved once per process, and the
// importer is built from the same value, so a caller keying a cache on it names
// exactly the SDK the analysis used.
func GoSDKRoot() string {
	goSDKRootOnce.Do(func() { goSDKRoot = findGOROOT() })
	return goSDKRoot
}

func getGoImporter() types.Importer {
	goImporterOnce.Do(func() {
		goroot := GoSDKRoot()
		if goroot == "" {
			goImporterAvailable = false
			fmt.Fprintf(os.Stderr, "Warning: Go SDK not found — Go type inference disabled. Set GOROOT, pass --goroot, or ensure 'go' is on PATH.\n")
			return
		}

		// Set GOROOT env and go/build.Default BEFORE creating importers.
		// The source importer uses go/build.Default.GOROOT which is cached at init time,
		// so we must also update it directly.
		os.Setenv("GOROOT", goroot)
		build.Default.GOROOT = goroot
		// Read packages with the cgo setting the go command would build them
		// with (see GoCgoEnabled).
		build.Default.CgoEnabled = GoCgoEnabled()

		// Try source importer first (works with Bazel Go SDK which has source but no .a files)
		goImporterInst = importer.ForCompiler(token.NewFileSet(), "source", nil)
		if _, err := goImporterInst.Import("fmt"); err == nil {
			goImporterAvailable = true
			return
		}

		// Try default importer (gc — reads compiled .a files, works outside Bazel)
		goImporterInst = importer.Default()
		if _, err := goImporterInst.Import("fmt"); err == nil {
			goImporterAvailable = true
			return
		}

		goImporterAvailable = false
		fmt.Fprintf(os.Stderr, "Warning: Go SDK found at %s but importers failed — Go type inference disabled.\n", goroot)
	})
	if goImporterInst == nil {
		return nil
	}
	return serialImporter{imp: goImporterInst}
}

// goImporterMu serializes every use of the shared importer. Neither go/importer
// implementation is safe for concurrent use (the source importer keeps its
// packages in an unguarded map), and one process can analyze several files at
// once: the LSP, the build worker, a test running transpilers in parallel.
var goImporterMu sync.Mutex

// serialImporter is the shared importer behind goImporterMu.
type serialImporter struct{ imp types.Importer }

var _ types.Importer = serialImporter{}

func (s serialImporter) Import(path string) (*types.Package, error) {
	goImporterMu.Lock()
	defer goImporterMu.Unlock()
	return s.imp.Import(path)
}

// GoImporterAvailable returns whether the Go type importer is available.
func GoImporterAvailable() bool {
	getGoImporter() // ensure initialized
	return goImporterAvailable
}

// GoSDKIdentity names the Go SDK the analyzer resolves Go package types from,
// for caches of anything derived from them: its root, the contents of its
// VERSION file, and the cgo setting packages are read with. Upgrading Go in
// place keeps the root and changes VERSION; switching SDKs changes the root;
// installing or removing a C compiler can flip cgo, which selects the files
// type-checked. "none" when there is no SDK.
func GoSDKIdentity() string { return goSDKIdentity() }

var goSDKIdentity = sync.OnceValue(func() string {
	root := GoSDKRoot()
	if root == "" {
		return "none"
	}
	version, _ := os.ReadFile(filepath.Join(root, "VERSION"))
	return fmt.Sprintf("%s|%s|cgo=%t", root, strings.TrimSpace(string(version)), GoCgoEnabled())
})

// GoCgoEnabled reports whether the analyzer reads Go packages with cgo enabled:
// the value the go command would build with, resolved once per process.
//
// go/build enables cgo on every platform that supports it, whether or not a C
// compiler is installed. The go command does not: with CGO_ENABLED unset it
// turns cgo off when it cannot find the compiler. The source importer follows
// go/build, so on a host without a C compiler (the golang:alpine image, say)
// it ran `go tool cgo` for net, failed, and failed with it every package that
// imports net — net/http, crypto/tls, … Every Go signature naming one of their
// types was left unresolved, although `go build` compiles that code fine.
func GoCgoEnabled() bool { return goCgoEnabled() }

var goCgoEnabled = sync.OnceValue(func() bool {
	return resolveCgoEnabled(goEnvValue, os.Getenv("CC") != "", exec.LookPath, build.Default.CgoEnabled)
})

// resolveCgoEnabled applies the go command's rule. CGO_ENABLED, when it is 0
// or 1, decides. Otherwise cgo is on where the platform supports it, except
// that with CC unset in the process environment it is off when the default C
// compiler is not on PATH. goenv looks a variable up as the go command does
// (see goEnvValue).
func resolveCgoEnabled(goenv func(string) string, ccSet bool, lookPath func(string) (string, error), platformSupportsCgo bool) bool {
	switch strings.TrimSpace(goenv("CGO_ENABLED")) {
	case "0":
		return false
	case "1":
		return true
	}
	if !platformSupportsCgo {
		return false
	}
	if ccSet {
		return true
	}
	_, err := lookPath(defaultCC())
	return err == nil
}

// goEnvValue is a Go environment variable as the go command sees it: the
// process environment when non-empty, else the user's go env file
// (`go env -w`), else the SDK's $GOROOT/go.env. Reading the files rather than
// running `go env` keeps a subprocess off the analyzer's start-up path.
func goEnvValue(key string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return goEnvFiles()[key]
}

var goEnvFiles = sync.OnceValue(func() map[string]string {
	var files []string
	if root := GoSDKRoot(); root != "" {
		files = append(files, filepath.Join(root, "go.env"))
	}
	userFile := os.Getenv("GOENV")
	if userFile == "" {
		if dir, err := os.UserConfigDir(); err == nil {
			userFile = filepath.Join(dir, "go", "env")
		}
	}
	if userFile != "" && userFile != "off" {
		files = append(files, userFile) // read last: it overrides go.env
	}
	vals := make(map[string]string)
	for _, f := range files {
		if data, err := os.ReadFile(f); err == nil {
			parseGoEnvFile(string(data), vals)
		}
	}
	return vals
})

// parseGoEnvFile adds the KEY=VALUE lines of a go env file to vals.
func parseGoEnvFile(data string, vals map[string]string) {
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && k != "" {
			vals[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
}

// defaultCC is the C compiler the go command uses when CC is unset.
func defaultCC() string {
	switch runtime.GOOS {
	case "darwin", "ios", "freebsd", "openbsd":
		return "clang"
	}
	return "gcc"
}

// findGOROOT discovers the Go SDK root directory.
// It tries multiple strategies:
// 0. The hermetic Go SDK provided by rules_go when running under Bazel
// 1. GOROOT environment variable (if valid)
// 2. runtime.GOROOT() (if valid)
// 3. Walk up from the running binary to find a Go SDK layout (for Bazel)
// 4. Find 'go' binary on PATH and derive GOROOT from it
func findGOROOT() string {
	// 0. Prefer the hermetic Go SDK that rules_go materializes inside the Bazel
	// execution root. This must win over the GOROOT env / host PATH so that type
	// inference uses the exact same Go version Bazel compiles the generated code
	// with, instead of whatever `go` happens to be installed on the host. It only
	// matches inside a Bazel action; standalone `gala build` falls through below.
	if goroot := findBazelGoSDK(); goroot != "" {
		return goroot
	}

	// 1. Check GOROOT env
	if goroot := os.Getenv("GOROOT"); goroot != "" && goroot != "GOROOT" && isGoRoot(goroot) {
		return goroot
	}

	// 2. Check runtime.GOROOT()
	if goroot := runtime.GOROOT(); goroot != "" && goroot != "GOROOT" && isGoRoot(goroot) {
		return goroot
	}

	// 3. Find Go SDK from the running binary's directory.
	// In Bazel, the go binary is at <goroot>/bin/go, and our binary is built
	// with the same SDK. Walk up from our executable looking for a Go SDK.
	if exePath, err := os.Executable(); err == nil {
		dir := filepath.Dir(exePath)
		// Walk up a few levels looking for a Go SDK
		for i := 0; i < 5; i++ {
			if isGoRoot(dir) {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	// 4. Find 'go' binary on PATH and derive GOROOT
	if goPath, err := findGoOnPath(); err == nil {
		if goroot := gorootFromGoBinary(goPath); goroot != "" {
			return goroot
		}
	}

	return ""
}

// gorootFromGoBinary derives a valid GOROOT from a `go` executable path.
//
// The naive guess is <dir>/.. of <goroot>/bin/go, but that breaks for managed
// installs where `go` on PATH is a symlink — e.g. Homebrew's
// /opt/homebrew/bin/go points into /opt/homebrew/Cellar/go/<ver>/libexec/bin/go,
// so the unresolved guess yields /opt/homebrew (not a Go SDK). We therefore try,
// in order: the literal parent-of-bin, the same after resolving symlinks, and
// finally `go env GOROOT` which is authoritative. Returns "" if none is a valid
// Go SDK root.
func gorootFromGoBinary(goPath string) string {
	candidates := []string{filepath.Dir(filepath.Dir(goPath))}
	if resolved, err := filepath.EvalSymlinks(goPath); err == nil && resolved != goPath {
		candidates = append(candidates, filepath.Dir(filepath.Dir(resolved)))
	}
	for _, c := range candidates {
		if isGoRoot(c) {
			return c
		}
	}
	// Authoritative fallback: ask the toolchain itself.
	if out, err := exec.Command(goPath, "env", "GOROOT").Output(); err == nil {
		goroot := strings.TrimSpace(string(out))
		if goroot != "" && isGoRoot(goroot) {
			return goroot
		}
	}
	return ""
}

// findBazelGoSDK locates the hermetic Go SDK that rules_go provides when the
// transpiler runs inside a Bazel action. Transpile actions run with the execution
// root as their working directory (and tagged no-sandbox so the SDK on disk is
// reachable), with the SDK materialized at external/<rules_go go_sdk repo>/, e.g.
// external/rules_go++go_sdk+main___download_0 (bzlmod) or external/go_sdk
// (legacy WORKSPACE). Returns "" when not running under Bazel so the normal
// host-Go discovery in findGOROOT applies.
func findBazelGoSDK() string {
	// Search from the working directory (the execution root during an action) and
	// from the running binary's directory, walking up a few levels so we find the
	// `external/` tree regardless of which one Bazel hands us.
	var bases []string
	if cwd, err := os.Getwd(); err == nil {
		bases = append(bases, cwd)
	}
	if exe, err := os.Executable(); err == nil {
		bases = append(bases, filepath.Dir(exe))
	}
	return findBazelGoSDKFrom(bases)
}

// findBazelGoSDKFrom walks up from each base directory (bounded) looking for an
// `external/` tree that contains a rules_go Go SDK. Split out from findBazelGoSDK
// so the search logic can be tested without depending on the ambient process
// working directory or executable path.
func findBazelGoSDKFrom(bases []string) string {
	for _, base := range bases {
		dir := base
		for i := 0; i < 8; i++ {
			if sdk := matchBazelGoSDKUnder(filepath.Join(dir, "external")); sdk != "" {
				return sdk
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return ""
}

// matchBazelGoSDKUnder returns the first entry under externalDir that both looks
// like a rules_go SDK repo (its name contains "go_sdk") and is a valid Go SDK
// root. The legacy WORKSPACE name `go_sdk` is matched by the same glob.
func matchBazelGoSDKUnder(externalDir string) string {
	matches, err := filepath.Glob(filepath.Join(externalDir, "*go_sdk*"))
	if err != nil {
		return ""
	}
	for _, m := range matches {
		if isGoRoot(m) {
			return m
		}
	}
	return ""
}

// isGoRoot checks if a directory looks like a Go SDK root (has src/fmt directory).
func isGoRoot(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "src", "fmt"))
	return err == nil && info.IsDir()
}

// findGoOnPath searches PATH for the 'go' executable.
func findGoOnPath() (string, error) {
	// Search PATH first (covers both system Go and Bazel Go SDK)
	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		pathEnv = os.Getenv("Path") // Windows uses "Path"
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		for _, name := range []string{"go.exe", "go"} {
			candidate := filepath.Join(dir, name)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate, nil
			}
		}
	}

	// Check common Go installation paths
	commonPaths := []string{
		// Unix paths
		"/usr/local/go/bin/go",
		"/usr/lib/go/bin/go",
		"/snap/go/current/bin/go",
		filepath.Join(os.Getenv("HOME"), "go", "bin", "go"),
		filepath.Join(os.Getenv("HOME"), "sdk", "go", "bin", "go"),
		// Windows paths
		filepath.Join(os.Getenv("USERPROFILE"), "go", "bin", "go.exe"),
		filepath.Join(os.Getenv("USERPROFILE"), "sdk", "go", "bin", "go.exe"),
		`C:\Go\bin\go.exe`,
		`C:\Program Files\Go\bin\go.exe`,
	}
	for _, p := range commonPaths {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}

	return "", os.ErrNotExist
}

// goPackageCache caches AnalyzeGoPackage results to avoid redundant go/importer calls.
var goPackageCache = struct {
	mu    sync.Mutex
	cache map[string]*transpiler.GoTypeInfo
}{cache: make(map[string]*transpiler.GoTypeInfo)}

// goTypeInfoNonEmpty reports whether a GoTypeInfo carries any usable type
// data. Used to decide whether go/importer resolved a package or whether the
// analyzer should fall back to parsing the package's .go source directly.
func goTypeInfoNonEmpty(info *transpiler.GoTypeInfo) bool {
	if info == nil {
		return false
	}
	return len(info.Functions) > 0 || len(info.Types) > 0 || len(info.Variables) > 0 || len(info.TypeAliases) > 0
}

// AnalyzeGoPackage loads type information for a Go package by import path.
// Uses go/importer to resolve installed packages (stdlib and third-party).
// Returns empty GoTypeInfo if Go SDK is not available (e.g., Bazel sandbox).
func AnalyzeGoPackage(importPath string) *transpiler.GoTypeInfo {
	goPackageCache.mu.Lock()
	if cached, ok := goPackageCache.cache[importPath]; ok {
		goPackageCache.mu.Unlock()
		return cached
	}
	goPackageCache.mu.Unlock()

	info := transpiler.NewGoTypeInfo()

	imp := getGoImporter()
	if !goImporterAvailable {
		return info
	}

	pkg, err := imp.Import(importPath)
	if err != nil {
		return info
	}

	extractPackageInfo(pkg, info, false)

	goPackageCache.mu.Lock()
	goPackageCache.cache[importPath] = info
	goPackageCache.mu.Unlock()

	return info
}

// goFilesCache memoizes AnalyzeGoFiles results across worker requests.
// The directory's relevant .go files don't change during one Bazel build
// invocation, and parsing + type-checking them with go/types is the most
// expensive thing this package does after the analyzer itself. Without
// memoization, the apex transpile of cmd/main.gala in gala_team
// re-parses every transitive package's hand-written Go files (plus the
// transitive Go-stdlib graph reachable via go/importer) on every visit.
var goFilesCache = struct {
	mu    sync.Mutex
	cache map[string]goFilesResult
}{cache: make(map[string]goFilesResult)}

// goFilesResult is what one scan of a directory's .go files yields: their type
// info, and the bare names of the types they declare themselves. Type info
// cannot tell the second apart, since it is keyed by package name and also
// files types of other packages a signature mentions.
type goFilesResult struct {
	info     *transpiler.GoTypeInfo
	ownTypes map[string]bool
}

// AnalyzeGoFiles parses and type-checks local .go files and extracts type info.
// This handles Go source files that live alongside GALA files or in Go-only packages.
// Generated .gen.go files are skipped — their metadata comes from analyzing the
// originating .gala source.
//
// importPath is the package's Go import path: every type declared in it
// records it (NamedType.ImportPath), and code generation imports the type by
// it. An empty or invalid one — a directory, say, which would be emitted as
// `import "C:\\…"` — is replaced by the path derived from the enclosing module,
// or by "" when there is none, so the types resolve by package name through
// the importing file's imports.
//
// Results are memoized per (dirPath, importPath) for the lifetime of the
// process. Within one Bazel build the .go files in a package directory don't
// change, so the parse + type-check work happens at most once per directory
// per worker.
func AnalyzeGoFiles(dirPath, importPath string) *transpiler.GoTypeInfo {
	return analyzeGoFilesMemo(dirPath, importPath, "").info
}

// AnalyzeOwnGoFiles is AnalyzeGoFiles for the hand-written .go files of the
// package being compiled, named pkgName. Only the files of that package take
// part — the package clause must name pkgName — and the package's unexported
// declarations are recorded too. It also returns the bare names of the types
// those files declare.
func AnalyzeOwnGoFiles(dirPath, importPath, pkgName string) (*transpiler.GoTypeInfo, map[string]bool) {
	r := analyzeGoFilesMemo(dirPath, importPath, pkgName)
	return r.info, r.ownTypes
}

func analyzeGoFilesMemo(dirPath, importPath, pkgName string) goFilesResult {
	importPath = goFilesImportPath(dirPath, importPath)
	cacheKey := dirPath + "\x00" + importPath + "\x00" + pkgName

	goFilesCache.mu.Lock()
	cached, ok := goFilesCache.cache[cacheKey]
	goFilesCache.mu.Unlock()
	if ok {
		return cached
	}

	r := analyzeGoFiles(dirPath, importPath, pkgName)
	goFilesCache.mu.Lock()
	goFilesCache.cache[cacheKey] = r
	goFilesCache.mu.Unlock()
	return r
}

// analyzeGoFiles is AnalyzeGoFiles without the memo. A non-empty pkgName
// keeps only the files of that package (see AnalyzeOwnGoFiles).
//
// Whatever the caller, a file no build configuration compiles (`//go:build
// ignore`, say, on a generator) and a file the GALA transpiler wrote (an old
// `gala transpile -o main.go`) are left out: neither is hand-written source of
// the package.
func analyzeGoFiles(dirPath, importPath, pkgName string) goFilesResult {
	info := transpiler.NewGoTypeInfo()
	result := goFilesResult{info: info}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return result
	}

	fset := token.NewFileSet()
	var files []*ast.File
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, ".gen.go") {
			continue
		}
		fullPath := filepath.Join(dirPath, name)
		f, err := parser.ParseFile(fset, fullPath, nil, parser.ParseComments)
		if err != nil {
			continue
		}
		if (pkgName != "" && f.Name.Name != pkgName) || neverBuilt(f) || writtenByGala(f) {
			continue
		}
		files = append(files, f)
	}

	if len(files) == 0 {
		return result
	}

	// Type-check the parsed files
	conf := types.Config{
		Importer: getGoImporter(),
		Error:    func(err error) {}, // Ignore type-check errors (partial analysis is fine)
	}
	typesInfo := &types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		Defs:  make(map[*ast.Ident]types.Object),
		Uses:  make(map[*ast.Ident]types.Object),
	}

	pkg, _ := conf.Check(importPath, fset, files, typesInfo)
	if pkg == nil {
		// Even if type-checking fails, try to extract what we can from AST
		extractFromAST(files, info)
		return result
	}

	own := pkgName != ""
	extractPackageInfo(pkg, info, own)
	repairUnresolvedSignatures(files, pkg.Name(), info)
	extractMethodsOnForeignTypes(files, typesInfo, pkg, info, own)
	result.ownTypes = make(map[string]bool)
	for _, name := range pkg.Scope().Names() {
		if tn, ok := pkg.Scope().Lookup(name).(*types.TypeName); ok && (own || tn.Exported()) {
			result.ownTypes[name] = true
		}
	}
	return result
}

// goosList and goarchList are the configurations neverBuilt tries.
var (
	goosList   = []string{"aix", "android", "darwin", "dragonfly", "freebsd", "illumos", "ios", "js", "linux", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows"}
	goarchList = []string{"386", "amd64", "arm", "arm64", "loong64", "mips", "mips64", "mips64le", "mipsle", "ppc64", "ppc64le", "riscv64", "s390x", "wasm"}
)

// neverBuilt reports whether f's `//go:build` constraint holds for no
// GOOS/GOARCH pair, with cgo and every Go release on — `//go:build ignore`
// on a generator, a custom tag nothing sets. A file built only for some
// platforms is kept: the transpiler serves every target, not just its host.
func neverBuilt(f *ast.File) bool {
	return constraintNeverHolds(f, []bool{true}, false)
}

// neverBuiltAnyConfig is neverBuilt for any build configuration: cgo on or
// off, and any custom tag (`purego`, `netgo`) set except `ignore`, the
// conventional tag of a file nothing builds.
func neverBuiltAnyConfig(f *ast.File) bool {
	return constraintNeverHolds(f, []bool{true, false}, true)
}

// constraintNeverHolds reports whether f's `//go:build` constraint holds for
// no GOOS/GOARCH pair and cgo setting, with Go's implied tags (android sets
// linux, ios darwin, illumos solaris), every architecture feature level
// (`amd64.v1`) and every Go release on. With customTags, every other tag but
// `ignore` is on too.
func constraintNeverHolds(f *ast.File, cgoSettings []bool, customTags bool) bool {
	var expr constraint.Expr
	for _, group := range f.Comments {
		if group.Pos() >= f.Package {
			break
		}
		for _, c := range group.List {
			if constraint.IsGoBuild(c.Text) {
				if e, err := constraint.Parse(c.Text); err == nil {
					expr = e
				}
			}
		}
	}
	if expr == nil {
		return false
	}
	unix := map[string]bool{"aix": true, "android": true, "darwin": true, "dragonfly": true, "freebsd": true, "illumos": true, "ios": true, "linux": true, "netbsd": true, "openbsd": true, "solaris": true}
	implied := map[string]string{"android": "linux", "ios": "darwin", "illumos": "solaris"}
	known := map[string]bool{"cgo": true, "gc": true, "gccgo": true, "unix": true, "ignore": true}
	for _, name := range goosList {
		known[name] = true
	}
	for _, name := range goarchList {
		known[name] = true
	}
	for _, goos := range goosList {
		for _, goarch := range goarchList {
			for _, cgo := range cgoSettings {
				ok := expr.Eval(func(tag string) bool {
					switch {
					case tag == goos || tag == goarch || tag == implied[goos] || tag == "gc",
						strings.HasPrefix(tag, goarch+"."), // GOAMD64-style feature levels
						tag == "cgo" && cgo,
						tag == "unix" && unix[goos],
						strings.HasPrefix(tag, "go1."):
						return true
					}
					return customTags && !known[tag] && !strings.Contains(tag, ".")
				})
				if ok {
					return false
				}
			}
		}
	}
	return true
}

// writtenByGala reports whether f is output of the GALA transpiler — its
// metadata comes from the .gala source it was transpiled from.
func writtenByGala(f *ast.File) bool {
	if !ast.IsGenerated(f) {
		return false
	}
	for _, group := range f.Comments {
		if group.Pos() >= f.Package {
			break
		}
		if strings.Contains(group.Text(), "Code generated by GALA transpiler") {
			return true
		}
	}
	return false
}

// extractMethodsOnForeignTypes records the methods these .go files declare on
// a type they do not declare themselves — in a mixed package, a type declared
// in a .gala file (`struct Repo()` there, `func (r Repo) Save() error` here).
// Each such type is filed in GalaTypeMethods under its "pkg.Name" key with Kind
// GoKindMethodsOnly: only its methods are known, the rest of the type comes
// from its GALA declaration. Exported methods are recorded, and, when own (the
// package being compiled), unexported ones too.
func extractMethodsOnForeignTypes(files []*ast.File, typesInfo *types.Info, pkg *types.Package, info *transpiler.GoTypeInfo, own bool) {
	// A GALA type a signature names (`func (r Repo) Renamed() Repo`) is
	// invisible to go/types and is recovered from the source. It is qualified
	// the way GALA metadata keys the package's types: not at all in main/test.
	galaQualifier := pkg.Name()
	if galaQualifier == "main" || galaQualifier == "test" {
		galaQualifier = ""
	}
	for _, f := range files {
		imports := fileImportPaths(f)
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || len(fd.Recv.List) != 1 || (!own && !fd.Name.IsExported()) {
				continue
			}
			recvName, pointer := receiverBaseName(fd.Recv.List[0].Type)
			if recvName == "" || pkg.Scope().Lookup(recvName) != nil {
				continue // a Go-declared type: extractPackageInfo has its methods
			}
			key := pkg.Name() + "." + recvName
			data := info.GalaTypeMethods[key]
			if data == nil {
				data = &transpiler.GoTypeData{
					Kind:    transpiler.GoKindMethodsOnly,
					Methods: make(map[string]*transpiler.GoFuncSignature),
				}
				if info.GalaTypeMethods == nil {
					info.GalaTypeMethods = make(map[string]*transpiler.GoTypeData)
				}
				info.GalaTypeMethods[key] = data
			}
			sig := &transpiler.GoFuncSignature{}
			if fn, ok := typesInfo.Defs[fd.Name].(*types.Func); ok {
				sig = convertSignature(fn.Type().(*types.Signature))
				repairSignature(sig, fd, imports, galaQualifier)
			}
			data.Methods[fd.Name.Name] = sig
			if pointer {
				if data.PointerMethods == nil {
					data.PointerMethods = make(map[string]bool)
				}
				data.PointerMethods[fd.Name.Name] = true
			}
		}
	}
}

// receiverBaseName returns the name of the type a method receiver expression
// names (`T`, `*T`, `T[A]`, `*T[A, B]`) and whether it is a pointer receiver.
func receiverBaseName(expr ast.Expr) (string, bool) {
	pointer := false
	if star, ok := expr.(*ast.StarExpr); ok {
		expr, pointer = star.X, true
	}
	switch e := expr.(type) {
	case *ast.IndexExpr:
		expr = e.X
	case *ast.IndexListExpr:
		expr = e.X
	}
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name, pointer
	}
	return "", false
}

// goFilesImportPath is the import path AnalyzeGoFiles records for the package
// in dirPath when its caller names importPath: importPath itself when it is a
// valid Go import path, else the path derived from the enclosing module, else
// "" (unknown).
func goFilesImportPath(dirPath, importPath string) string {
	if transpiler.IsValidGoImportPath(importPath) {
		return importPath
	}
	if cached, ok := derivedImportPaths.Load(dirPath); ok {
		return cached.(string)
	}
	derived := module.PackageImportPathForDir(dirPath)
	if !transpiler.IsValidGoImportPath(derived) {
		derived = ""
	}
	derivedImportPaths.Store(dirPath, derived)
	return derived
}

// derivedImportPaths memoizes, per directory, the import path derived from
// the enclosing module: deriving it reads a module file at every ancestor,
// and every file of a package asks. Like goFilesCache it lives for the
// process, over which module files do not move.
var derivedImportPaths sync.Map // dirPath -> string

// repairUnresolvedSignatures recovers signature slots go/types could not resolve
// from the type as it is WRITTEN in the source.
//
// A hand-written .go file in a GALA package may name a GALA-defined type — e.g.
// `func OptionFromMap[K comparable, V any](m map[K]V, key K) std.Option[V]`. That
// type only exists as Go once the GALA package has been transpiled, but this
// analysis deliberately runs against the un-transpiled tree (.gen.go files are
// skipped so metadata comes from the .gala source). go/types therefore resolves
// the reference to Invalid while the rest of the signature — including the type
// parameters — resolves normally.
//
// Left alone, that unresolved slot degrades to `any` and silently erases a
// concrete type. The AST still says `std.Option[V]`, and the file's import list
// says which package `std` is, which together are exactly the GenericType the rest
// of the pipeline needs. Recovery is therefore syntactic.
//
// Nothing here is specific to std, or even to GALA: any Go signature naming a type
// the checker could not load is recovered the same way, and slots go/types DID
// resolve are never touched.
func repairUnresolvedSignatures(files []*ast.File, pkgName string, info *transpiler.GoTypeInfo) {
	for _, f := range files {
		imports := fileImportPaths(f)
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Type == nil || !fd.Name.IsExported() {
				continue
			}
			if sig := info.Functions[pkgName+"."+fd.Name.Name]; sig != nil {
				repairSignature(sig, fd, imports, pkgName)
			}
		}
	}
}

// repairSignature recovers the slots of sig, the signature of fd, that
// go/types could not resolve, from the types as written (see
// repairUnresolvedSignatures).
func repairSignature(sig *transpiler.GoFuncSignature, fd *ast.FuncDecl, imports map[string]string, pkgName string) {
	typeParams := funcDeclTypeParams(fd)

	astParams := flattenFieldTypes(fd.Type.Params)
	for i := range sig.Params {
		if i >= len(astParams) || !transpiler.ContainsUnusable(sig.Params[i].Type) {
			continue
		}
		if rec := syntacticGoType(astParams[i], imports, pkgName, typeParams); !rec.IsNil() {
			sig.Params[i].Type = rec
		}
	}

	astResults := flattenFieldTypes(fd.Type.Results)
	for i := range sig.Returns {
		if i >= len(astResults) || !transpiler.ContainsUnusable(sig.Returns[i]) {
			continue
		}
		if rec := syntacticGoType(astResults[i], imports, pkgName, typeParams); !rec.IsNil() {
			sig.Returns[i] = rec
		}
	}
}

// fileImportPaths maps the names a file refers to each import by to that
// import's path, ranked as transpiler.ImportNames describes.
func fileImportPaths(f *ast.File) map[string]string {
	names := transpiler.NewRankedNames[string]()
	for _, imp := range f.Imports {
		if imp.Path == nil {
			continue
		}
		path := strings.Trim(imp.Path.Value, `"`)
		alias := ""
		if imp.Name != nil {
			if imp.Name.Name == "." {
				continue
			}
			alias = imp.Name.Name
		}
		names.Bind(path, alias, "", path)
	}
	return names.Map()
}

// funcDeclTypeParams returns the declaration's type-parameter names as a set, so
// syntactic recovery can tell `V` (a type parameter) from a local type name.
func funcDeclTypeParams(fd *ast.FuncDecl) map[string]bool {
	out := make(map[string]bool)
	if fd.Type != nil && fd.Type.TypeParams != nil {
		for _, field := range fd.Type.TypeParams.List {
			for _, name := range field.Names {
				out[name.Name] = true
			}
		}
	}
	// A method's receiver declares the type parameters of its generic type
	// (`func (b Box[T]) Get() T`); they are in scope in the signature too.
	if fd.Recv != nil && len(fd.Recv.List) == 1 {
		recv := fd.Recv.List[0].Type
		if star, ok := recv.(*ast.StarExpr); ok {
			recv = star.X
		}
		var indices []ast.Expr
		switch e := recv.(type) {
		case *ast.IndexExpr:
			indices = []ast.Expr{e.Index}
		case *ast.IndexListExpr:
			indices = e.Indices
		}
		for _, idx := range indices {
			if id, ok := idx.(*ast.Ident); ok {
				out[id.Name] = true
			}
		}
	}
	return out
}

// flattenFieldTypes expands a parameter/result list into one AST type per slot,
// repeating the type for grouped names (`a, b int` is two slots). This makes the
// result index-aligned with GoFuncSignature's flat Params/Returns.
func flattenFieldTypes(fl *ast.FieldList) []ast.Expr {
	if fl == nil {
		return nil
	}
	var out []ast.Expr
	for _, field := range fl.List {
		n := len(field.Names)
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			out = append(out, field.Type)
		}
	}
	return out
}

// syntacticGoType converts a Go type expression to a transpiler.Type using only
// syntax plus the file's import table — no go/types resolution. Returns NilType for
// any shape it cannot represent, so callers can leave the original slot alone.
func syntacticGoType(expr ast.Expr, imports map[string]string, pkgName string, typeParams map[string]bool) transpiler.Type {
	switch e := expr.(type) {
	case *ast.Ident:
		if typeParams[e.Name] || transpiler.IsPrimitiveType(e.Name) || e.Name == "any" || e.Name == "error" {
			return transpiler.BasicType{Name: e.Name}
		}
		return transpiler.NamedType{Package: pkgName, Name: e.Name}

	case *ast.SelectorExpr:
		x, ok := e.X.(*ast.Ident)
		if !ok {
			return transpiler.NilType{}
		}
		return transpiler.NamedType{Package: x.Name, Name: e.Sel.Name, ImportPath: imports[x.Name]}

	case *ast.ParenExpr:
		return syntacticGoType(e.X, imports, pkgName, typeParams)

	case *ast.Ellipsis:
		// convertSignature stores a variadic parameter element-wise, so `...T`
		// recovers as T to stay index-aligned with it.
		return syntacticGoType(e.Elt, imports, pkgName, typeParams)

	case *ast.StarExpr:
		elem := syntacticGoType(e.X, imports, pkgName, typeParams)
		if elem.IsNil() {
			return transpiler.NilType{}
		}
		return transpiler.PointerType{Elem: elem}

	case *ast.ArrayType:
		elem := syntacticGoType(e.Elt, imports, pkgName, typeParams)
		if elem.IsNil() {
			return transpiler.NilType{}
		}
		return transpiler.ArrayType{Elem: elem}

	case *ast.MapType:
		key := syntacticGoType(e.Key, imports, pkgName, typeParams)
		val := syntacticGoType(e.Value, imports, pkgName, typeParams)
		if key.IsNil() || val.IsNil() {
			return transpiler.NilType{}
		}
		return transpiler.MapType{Key: key, Elem: val}

	case *ast.IndexExpr:
		return syntacticGenericType(e.X, []ast.Expr{e.Index}, imports, pkgName, typeParams)

	case *ast.IndexListExpr:
		return syntacticGenericType(e.X, e.Indices, imports, pkgName, typeParams)

	case *ast.InterfaceType:
		if e.Methods == nil || len(e.Methods.List) == 0 {
			return transpiler.BasicType{Name: "any"}
		}
	}
	return transpiler.NilType{}
}

// syntacticGenericType builds a GenericType for `Base[Args...]`.
func syntacticGenericType(base ast.Expr, args []ast.Expr, imports map[string]string, pkgName string, typeParams map[string]bool) transpiler.Type {
	baseType := syntacticGoType(base, imports, pkgName, typeParams)
	if baseType.IsNil() {
		return transpiler.NilType{}
	}
	params := make([]transpiler.Type, 0, len(args))
	for _, arg := range args {
		p := syntacticGoType(arg, imports, pkgName, typeParams)
		if p.IsNil() {
			return transpiler.NilType{}
		}
		params = append(params, p)
	}
	return transpiler.GenericType{Base: baseType, Params: params}
}

// extractPackageInfo extracts all exported type information from a types.Package.
// For the package being compiled (own), which sees its unexported declarations
// as well, it extracts those too.
func extractPackageInfo(pkg *types.Package, info *transpiler.GoTypeInfo, own bool) {
	pkgName := pkg.Name()
	scope := pkg.Scope()

	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if !obj.Exported() && !own {
			continue
		}

		qualName := pkgName + "." + name

		switch obj := obj.(type) {
		case *types.Func:
			sig := obj.Type().(*types.Signature)
			info.Functions[qualName] = convertSignature(sig)
			// A function often returns a named type defined in another package
			// (e.g. sha256.New() returns hash.Hash). Record that type's method
			// set under its canonical "pkg.Name" key so a value bound to the
			// call has completions even though the defining package (hash) was
			// never directly imported.
			registerReturnNamedTypes(sig, info)

		case *types.TypeName:
			if obj.IsAlias() {
				// Go type alias: type X = Y
				// Resolve to the aliased type for transparent type inference.
				underlying := goTypeToTranspilerType(obj.Type())
				info.TypeAliases[qualName] = underlying
				// Also create type data so methods can be looked up on the alias
				// name (e.g. "os.DirEntry").
				info.Types[qualName] = extractTypeData(obj, "alias", own)
				// When the alias re-exports a *named* type from another package
				// (e.g. `os.DirEntry = io/fs.DirEntry`), also register the target
				// under its own canonical "pkgName.TypeName" key. Go signatures
				// resolve such aliases to the canonical name (a return of
				// `[]os.DirEntry` surfaces as `[]fs.DirEntry`), so a method call
				// on a value of that type (`entries[i].Info()`) looks the method
				// set up under `fs.DirEntry`. Without this, methods on a
				// re-exported type are invisible whenever only the aliasing
				// package — not the defining one — is imported.
				registerAliasTargetType(obj.Type(), qualName, info)
			} else {
				// Go type definition: type X struct{...} or type X int
				info.Types[qualName] = extractTypeData(obj, "", own)
			}

		case *types.Var:
			info.Variables[qualName] = goTypeToTranspilerType(obj.Type())

		case *types.Const:
			info.Constants[qualName] = goTypeToTranspilerType(obj.Type())
			if basic, ok := obj.Type().(*types.Basic); ok && basic.Info()&types.IsUntyped != 0 {
				if info.UntypedConstants == nil {
					info.UntypedConstants = make(map[string]bool)
				}
				info.UntypedConstants[qualName] = true
			}
		}
	}
}

// registerAliasTargetType records the method set / fields of the named type an
// alias points at under that target's own canonical "pkgName.TypeName" key.
// aliasType is the alias TypeName's Type(); aliasQualName is the alias's own
// qualified name (used to avoid registering a self-referential entry). It is a
// no-op when the target is not a named type from a real package, or when a
// (real, non-alias) entry for the canonical name already exists. This lets type
// inference that has resolved an alias to its canonical name find the target's
// methods even though the defining package was never directly imported.
func registerAliasTargetType(aliasType types.Type, aliasQualName string, info *transpiler.GoTypeInfo) {
	named, ok := types.Unalias(aliasType).(*types.Named)
	if !ok {
		return
	}
	tn := named.Obj()
	if tn == nil || tn.Pkg() == nil {
		return
	}
	canonical := tn.Pkg().Name() + "." + tn.Name()
	if canonical == aliasQualName {
		return
	}
	if _, exists := info.Types[canonical]; exists {
		return
	}
	info.Types[canonical] = extractTypeData(tn, "", false)
}

// registerReturnNamedTypes records, for each of a function's return values, the
// method set of any named type reached (unwrapping pointer/slice/array layers)
// that is defined in another package. This mirrors
// registerAliasTargetType but for return positions, so completion and type
// inference can find methods on values produced by a call into an imported
// package even when the value's own type lives in a package that was never
// directly imported (the classic case: a constructor returning an interface,
// like sha256.New() -> hash.Hash).
func registerReturnNamedTypes(sig *types.Signature, info *transpiler.GoTypeInfo) {
	if sig == nil {
		return
	}
	res := sig.Results()
	for i := 0; i < res.Len(); i++ {
		registerNamedTypeClosure(res.At(i).Type(), info)
	}
}

// registerNamedTypeClosure unwraps common type constructors (pointer, slice,
// array, map, chan) to find a named type and records its GoTypeData under the
// canonical "pkgName.TypeName" key. It is a no-op when the type is not a
// package-scoped named type or when an entry already exists (which also bounds
// the recursion).
func registerNamedTypeClosure(t types.Type, info *transpiler.GoTypeInfo) {
	switch tt := t.(type) {
	case *types.Pointer:
		registerNamedTypeClosure(tt.Elem(), info)
	case *types.Slice:
		registerNamedTypeClosure(tt.Elem(), info)
	case *types.Array:
		registerNamedTypeClosure(tt.Elem(), info)
	case *types.Chan:
		registerNamedTypeClosure(tt.Elem(), info)
	case *types.Map:
		registerNamedTypeClosure(tt.Key(), info)
		registerNamedTypeClosure(tt.Elem(), info)
	case *types.Named:
		tn := tt.Obj()
		if tn == nil || tn.Pkg() == nil {
			return
		}
		canonical := tn.Pkg().Name() + "." + tn.Name()
		if _, exists := info.Types[canonical]; exists {
			return
		}
		info.Types[canonical] = extractTypeData(tn, "", false)
	}
}

// GoPackageSourceDir resolves a Go import path to its on-disk source directory.
// It covers the standard library (under GOROOT/src), which is what the LSP needs
// to offer go-to-definition into Go source for stdlib calls. Returns "" when the
// directory can't be located (e.g. no Go SDK, or a third-party module that isn't
// unpacked under GOROOT).
func GoPackageSourceDir(importPath string) string {
	if importPath == "" {
		return ""
	}
	goroot := findGOROOT()
	if goroot == "" {
		return ""
	}
	dir := filepath.Join(goroot, "src", filepath.FromSlash(importPath))
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return dir
	}
	return ""
}

// extractTypeData creates GoTypeData for a types.TypeName. Its exported fields
// and methods are recorded and, when own (the type belongs to the package being
// compiled), the unexported ones that package declares as well.
func extractTypeData(tn *types.TypeName, forceKind string, own bool) *transpiler.GoTypeData {
	visible := func(o types.Object) bool {
		return o.Exported() || (own && o.Pkg() == tn.Pkg())
	}
	data := &transpiler.GoTypeData{
		Fields:  make(map[string]transpiler.Type),
		Methods: make(map[string]*transpiler.GoFuncSignature),
	}

	typ := tn.Type()

	// Determine kind
	if forceKind != "" {
		data.Kind = forceKind
	} else {
		switch typ.Underlying().(type) {
		case *types.Struct:
			data.Kind = "struct"
		case *types.Interface:
			data.Kind = "interface"
		default:
			data.Kind = "named"
		}
	}

	// Set underlying type
	data.Underlying = goTypeToTranspilerType(typ.Underlying())
	data.NoCopy = noCopyReason(typ)

	// Extract struct fields. Preserve declaration order in FieldOrder so
	// downstream consumers that build positional composite literals
	// (synthesizeTypeMetadataFromGo's FieldNames slice) match the Go-side
	// field layout instead of inheriting the random iteration order of the
	// Fields map.
	if s, ok := typ.Underlying().(*types.Struct); ok {
		for i := 0; i < s.NumFields(); i++ {
			f := s.Field(i)
			if visible(f) {
				data.Fields[f.Name()] = goTypeToTranspilerType(f.Type())
				data.FieldOrder = append(data.FieldOrder, f.Name())
			}
		}
	}

	// Extract type parameter names for generic types so downstream consumers
	// (e.g., the dot-import-used scan and method lookup on generic Go-style
	// structs imported via dot import) can resolve `Type[T]` references and
	// the methods they expose.
	if named, ok := typ.(*types.Named); ok {
		if tps := named.TypeParams(); tps != nil {
			for i := 0; i < tps.Len(); i++ {
				data.TypeParams = append(data.TypeParams, tps.At(i).Obj().Name())
			}
		}
	}

	// Extract method set (including pointer receiver methods)
	mset := types.NewMethodSet(types.NewPointer(typ))
	for i := 0; i < mset.Len(); i++ {
		sel := mset.At(i)
		fn := sel.Obj().(*types.Func)
		if !visible(fn) {
			continue
		}
		sig := fn.Type().(*types.Signature)
		data.Methods[fn.Name()] = convertSignature(sig)
	}

	// Also include value receiver methods
	mset = types.NewMethodSet(typ)
	valueMethods := make(map[string]bool, mset.Len())
	for i := 0; i < mset.Len(); i++ {
		sel := mset.At(i)
		fn := sel.Obj().(*types.Func)
		if !visible(fn) {
			continue
		}
		valueMethods[fn.Name()] = true
		if _, exists := data.Methods[fn.Name()]; !exists {
			sig := fn.Type().(*types.Signature)
			data.Methods[fn.Name()] = convertSignature(sig)
		}
	}

	// A method in the *T set but not the T set has a pointer receiver: Go
	// calls it only on an addressable T. Interfaces and pointer types have no
	// such split.
	markPointerMethod := func(name string) {
		if data.PointerMethods == nil {
			data.PointerMethods = make(map[string]bool)
		}
		data.PointerMethods[name] = true
	}
	if _, isIface := typ.Underlying().(*types.Interface); !isIface {
		for name := range data.Methods {
			if !valueMethods[name] {
				markPointerMethod(name)
			}
		}
	}

	// types.NewMethodSet returns nothing for an uninstantiated generic named
	// type, so for generics we also pull methods directly off the *types.Named
	// origin. This lets a dot-importing GALA consumer find methods on a
	// generic Go-style struct (e.g. `Single[T]`) referenced only through a
	// generic instantiation.
	if named, ok := typ.(*types.Named); ok && named.TypeParams() != nil {
		for i := 0; i < named.NumMethods(); i++ {
			fn := named.Method(i)
			if !visible(fn) {
				continue
			}
			if _, exists := data.Methods[fn.Name()]; exists {
				continue
			}
			sig := fn.Type().(*types.Signature)
			data.Methods[fn.Name()] = convertSignature(sig)
			// The method-set difference above sees nothing here (both sets
			// are empty for an uninstantiated generic), so read the receiver.
			if recv := sig.Recv(); recv != nil {
				if _, isPtr := recv.Type().(*types.Pointer); isPtr {
					markPointerMethod(fn.Name())
				}
			}
		}
	}

	return data
}

// convertSignature converts a types.Signature to a transpiler.GoFuncSignature.
func convertSignature(sig *types.Signature) *transpiler.GoFuncSignature {
	result := &transpiler.GoFuncSignature{
		IsVariadic: sig.Variadic(),
	}

	// Record the declared type-parameter names so a call site can instantiate
	// the signature. Both the function's own type params and a generic
	// receiver's are in scope inside the signature, so both are collected.
	for _, tps := range []*types.TypeParamList{sig.TypeParams(), sig.RecvTypeParams()} {
		for i := 0; tps != nil && i < tps.Len(); i++ {
			result.TypeParams = append(result.TypeParams, tps.At(i).Obj().Name())
		}
	}

	// Convert parameters (skip receiver)
	params := sig.Params()
	for i := 0; i < params.Len(); i++ {
		p := params.At(i)
		paramType := p.Type()
		// For variadic, the last param type is a slice — extract the element type
		if sig.Variadic() && i == params.Len()-1 {
			if sl, ok := paramType.(*types.Slice); ok {
				paramType = sl.Elem()
			}
		}
		result.Params = append(result.Params, transpiler.GoParam{
			Name: p.Name(),
			Type: goTypeToTranspilerType(paramType),
		})
	}

	// Convert return types
	results := sig.Results()
	for i := 0; i < results.Len(); i++ {
		result.Returns = append(result.Returns, goTypeToTranspilerType(results.At(i).Type()))
	}

	return result
}

// goTypeToTranspilerType converts a go/types.Type to a transpiler.Type.
// This is the core bridge between Go's type system and GALA's.
// It handles type aliases by resolving them to their underlying type.
func goTypeToTranspilerType(t types.Type) transpiler.Type {
	if t == nil {
		return transpiler.NilType{}
	}

	switch t := t.(type) {
	case *types.Basic:
		name := t.Name()
		// An unresolved type is NOT a type. go/types names the Invalid basic
		// "invalid type" — a display string, not a Go identifier — and emitting
		// it produces Go that does not even parse (`func() invalid type`), which
		// then surfaces as an opaque internal error naming no user construct.
		// NilType is the pipeline's contract for "unknown", and it is what lets
		// repairUnresolvedSignatures find the slot and recover it from source.
		if t.Kind() == types.Invalid {
			return transpiler.NilType{}
		}
		// Untyped constants (e.g., "untyped string", "untyped int") should
		// resolve to their concrete Go type for code generation purposes.
		if t.Info()&types.IsUntyped != 0 {
			switch t.Kind() {
			case types.UntypedBool:
				name = "bool"
			case types.UntypedInt:
				name = "int"
			case types.UntypedRune:
				name = "rune"
			case types.UntypedFloat:
				name = "float64"
			case types.UntypedComplex:
				name = "complex128"
			case types.UntypedString:
				name = "string"
			}
		}
		return transpiler.BasicType{Name: name}

	case *types.Named:
		obj := t.Obj()
		pkg := obj.Pkg()
		if pkg == nil {
			// Built-in type (error, etc.)
			return transpiler.BasicType{Name: obj.Name()}
		}
		// Check if this is an alias — if so, resolve to the aliased type
		if obj.IsAlias() {
			return goTypeToTranspilerType(types.Unalias(t))
		}
		return transpiler.NamedType{
			Package:    pkg.Name(),
			Name:       obj.Name(),
			ImportPath: pkg.Path(),
		}

	case *types.Alias:
		// Go 1.22+ explicit alias type — resolve to the underlying aliased type
		return goTypeToTranspilerType(types.Unalias(t))

	case *types.Pointer:
		elem := goTypeToTranspilerType(t.Elem())
		return transpiler.PointerType{Elem: elem}

	case *types.Slice:
		elem := goTypeToTranspilerType(t.Elem())
		return transpiler.ArrayType{Elem: elem}

	case *types.Array:
		elem := goTypeToTranspilerType(t.Elem())
		return transpiler.ArrayType{Elem: elem}

	case *types.Map:
		key := goTypeToTranspilerType(t.Key())
		elem := goTypeToTranspilerType(t.Elem())
		return transpiler.MapType{Key: key, Elem: elem}

	case *types.Chan:
		// Map channels to their element type (GALA uses Signal wrappers)
		elem := goTypeToTranspilerType(t.Elem())
		return transpiler.NamedType{Name: "chan " + elem.String()}

	case *types.Signature:
		result := transpiler.FuncType{}
		params := t.Params()
		for i := 0; i < params.Len(); i++ {
			result.Params = append(result.Params, goTypeToTranspilerType(params.At(i).Type()))
		}
		results := t.Results()
		for i := 0; i < results.Len(); i++ {
			result.Results = append(result.Results, goTypeToTranspilerType(results.At(i).Type()))
		}
		return result

	case *types.Interface:
		// Empty interface → any
		return transpiler.BasicType{Name: "any"}

	case *types.Struct:
		// Anonymous struct — can't represent directly, return any
		return transpiler.BasicType{Name: "any"}

	case *types.TypeParam:
		// Generic type parameter
		return transpiler.BasicType{Name: t.Obj().Name()}

	default:
		return transpiler.NilType{}
	}
}

// extractFromAST is a fallback that extracts minimal type info from Go AST
// when full type-checking fails (e.g., missing dependencies).
func extractFromAST(files []*ast.File, info *transpiler.GoTypeInfo) {
	for _, f := range files {
		pkgName := f.Name.Name
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv != nil || !d.Name.IsExported() {
					continue
				}
				qualName := pkgName + "." + d.Name.Name
				sig := &transpiler.GoFuncSignature{}
				if d.Type.Results != nil {
					for range d.Type.Results.List {
						sig.Returns = append(sig.Returns, transpiler.BasicType{Name: "any"})
					}
				}
				info.Functions[qualName] = sig

			case *ast.GenDecl:
				if d.Tok != token.TYPE {
					continue
				}
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || !ts.Name.IsExported() {
						continue
					}
					qualName := pkgName + "." + ts.Name.Name
					kind := "named"
					if _, ok := ts.Type.(*ast.StructType); ok {
						kind = "struct"
					} else if _, ok := ts.Type.(*ast.InterfaceType); ok {
						kind = "interface"
					}
					if ts.Assign.IsValid() {
						kind = "alias"
					}
					info.Types[qualName] = &transpiler.GoTypeData{
						Kind:    kind,
						Fields:  make(map[string]transpiler.Type),
						Methods: make(map[string]*transpiler.GoFuncSignature),
					}
				}
			}
		}
	}
}
