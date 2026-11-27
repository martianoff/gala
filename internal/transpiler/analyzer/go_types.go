package analyzer

import (
	"context"
	"fmt"
	"go/ast"
	"go/build"
	"go/build/constraint"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"go/version"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/genheader"
	"martianoff/gala/internal/transpiler/module"
	"martianoff/gala/internal/transpiler/profiler"
)

// goImporter is a cached importer that tries multiple strategies.
// Created once and reused across all AnalyzeGoPackage calls.
var (
	goImporterOnce sync.Once
	goImporterInst types.Importer

	// goTableImporter is goImporterInst behind goExportTable (see
	// SetGoExportFiles): export files a build system hands over come first.
	// It wraps goImporterInst from the start, whether or not the table has
	// entries yet, so the importer a package is read with never depends on
	// when they arrived.
	goTableImporter types.Importer
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
	goSDKRootOnce.Do(func() {
		var src sdkSource
		goSDKRoot, src = findGOROOT()
		if v := os.Getenv("GOROOT"); src != sdkFromBazel && v != "" && v != "GOROOT" && !isGoRoot(v) {
			fmt.Fprintf(os.Stderr, "Warning: GOROOT (or --goroot) %s is not a Go SDK; ignoring it.\n", v)
		}
		switch {
		case goSDKRoot != "" && src != sdkSwitched:
			// The go commands gala runs (cgo through the source importer,
			// `go build`) use the SDK it reads.
			os.Setenv("GOROOT", goSDKRoot)
		case !isGoRoot(os.Getenv("GOROOT")):
			// A switched toolchain is not the go on PATH's own, so it is not
			// exported: that go would run another release's tools under it.
			// A GOROOT naming no SDK is cleared so the go commands find theirs.
			os.Unsetenv("GOROOT")
		}
	})
	return goSDKRoot
}

// sdkSource is where findGOROOT found the SDK.
type sdkSource int

const (
	sdkFromBazel sdkSource = iota // the hermetic rules_go SDK
	sdkFromHost                   // GOROOT, or an SDK installed on the host
	sdkSwitched                   // the toolchain the go command switches to
)

func getGoImporter() types.Importer {
	goImporterOnce.Do(func() {
		goroot := GoSDKRoot()
		if goroot == "" {
			goImporterAvailable = false
			fmt.Fprintf(os.Stderr, "Warning: Go SDK not found — Go type inference disabled. Set GOROOT, pass --goroot, or ensure 'go' is on PATH.\n")
			return
		}

		// Set go/build.Default BEFORE creating importers: the source importer
		// reads go/build.Default.GOROOT, which is cached at init time.
		// GoSDKRoot exports it as the process's GOROOT too, unless it is a
		// switched toolchain, which only build.Default.GOROOT names.
		build.Default.GOROOT = goroot
		// Read packages with the cgo setting the go command would build them
		// with (see GoCgoEnabled).
		build.Default.CgoEnabled = GoCgoEnabled()

		fset := token.NewFileSet()
		source := importer.ForCompiler(fset, "source", nil)
		defer func() {
			if goImporterInst != nil {
				goTableImporter = newTableExportImporter(fset, goExportTable, goImporterInst)
			}
		}()

		// Export data when the caller opted in (see UseGoExportData) and the
		// go command produces export data this build of GALA can read;
		// otherwise the source importer.
		if goExportData.Load() {
			exp := newExportDataImporter(fset, goListExports(goroot, build.Default.CgoEnabled), source)
			exp.prefetch([]string{"fmt"})
			if _, err := exp.importExportData("fmt"); err == nil {
				goImporterInst = exp
				goImporterAvailable = true
				return
			}
			if profiler.Enabled {
				fmt.Fprintln(os.Stderr, "  [go-types] no usable Go export data; reading Go packages from source")
			}
		}

		// Try source importer first (works with Bazel Go SDK which has source but no .a files)
		goImporterInst = source
		// The standard library's export data, when the build system gave it,
		// is read before the source importer: probing that with fmt would
		// type-check fmt and its imports from source for nothing.
		if readsGoExportData(goExportTable) {
			goImporterAvailable = true
			return
		}
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
	return serialImporter{imp: goTableImporter}
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

// prefetchGoPackages resolves paths in one batch ahead of importing them one
// by one, when the shared importer can (the export-data importer lists them
// with a single `go list`); otherwise it does nothing.
func prefetchGoPackages(paths []string) {
	if len(paths) == 0 || getGoImporter() == nil {
		return
	}
	if p, ok := goImporterInst.(*exportDataImporter); ok {
		goImporterMu.Lock()
		defer goImporterMu.Unlock()
		p.prefetch(paths)
	}
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
	return resolveCgoEnabled(GoEnvValue, os.Getenv("CC") != "", exec.LookPath, build.Default.CgoEnabled)
})

// resolveCgoEnabled applies the go command's rule. CGO_ENABLED, when it is 0
// or 1, decides. Otherwise cgo is on where the platform supports it, except
// that with CC unset in the process environment it is off when the default C
// compiler is not on PATH. goenv looks a variable up as the go command does
// (see GoEnvValue).
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

// GoEnvValue returns a Go environment variable as the go command sees it: the
// process environment when non-empty, else the user's go env file
// (`go env -w`), else the SDK's $GOROOT/go.env. Reading the files rather than
// running `go env` keeps a subprocess off the analyzer's start-up path.
func GoEnvValue(key string) string {
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
	if userFile := goUserEnvFile(); userFile != "" {
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

// goUserEnvFile is the user's go env file (`go env -w` writes it), or "" when
// there is none (GOENV=off).
func goUserEnvFile() string {
	userFile := os.Getenv("GOENV")
	if userFile == "" {
		if dir, err := os.UserConfigDir(); err == nil {
			userFile = filepath.Join(dir, "go", "env")
		}
	}
	if userFile == "off" {
		return ""
	}
	return userFile
}

// goToolchain is GOTOOLCHAIN as the go command of the SDK at goroot sees it:
// the process environment, else the user's go env file, else goroot/go.env.
// GoEnvValue reads the same files, but for the resolved SDK, which is what
// is being resolved here.
func goToolchain(goroot string) string {
	if v := os.Getenv("GOTOOLCHAIN"); v != "" {
		return v
	}
	for _, f := range []string{goUserEnvFile(), filepath.Join(goroot, "go.env")} {
		if f == "" {
			continue
		}
		if data, err := os.ReadFile(f); err == nil {
			vals := make(map[string]string)
			parseGoEnvFile(string(data), vals)
			if v := vals["GOTOOLCHAIN"]; v != "" {
				return v
			}
		}
	}
	return ""
}

// toolchainMaySwitch reports whether a go command of version host ("go1.23.4",
// "" when unknown) may build a module whose go line is GeneratedGoVersion with
// another toolchain, given GOTOOLCHAIN. It is false where it does not:
// GOTOOLCHAIN=local; host new enough, or a development build (unknown), with
// the choice left to the go line (auto, path, unset); or GOTOOLCHAIN naming
// host itself (go1.25.5, go1.25.5+auto) with host new enough.
func toolchainMaySwitch(host, gotoolchain string) bool {
	if gotoolchain == "local" {
		return false
	}
	name, _, _ := strings.Cut(gotoolchain, "+")
	named := name != "" && name != "auto" && name != "path" && name != "local" // local+auto is auto
	if !version.IsValid(host) {
		// A release SDK has a VERSION; one without is a development build,
		// which counts as the newest Go and switches only when told to.
		return named
	}
	if version.Compare(host, "go"+GeneratedGoVersion) < 0 {
		return true
	}
	return named && name != host
}

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
//
// An SDK found by steps 2–4 goes through switchedGoRoot: a build may compile
// with another toolchain than the one installed. A GOROOT set explicitly
// (step 1, which --goroot also sets) is used as given.
func findGOROOT() (string, sdkSource) {
	// 0. Prefer the hermetic Go SDK that rules_go materializes inside the Bazel
	// execution root. This must win over the GOROOT env / host PATH so that type
	// inference uses the exact same Go version Bazel compiles the generated code
	// with, instead of whatever `go` happens to be installed on the host. It only
	// matches inside a Bazel action; standalone `gala build` falls through below.
	if goroot := findBazelGoSDK(); goroot != "" {
		return goroot, sdkFromBazel
	}

	// 1. Check GOROOT env
	if goroot := os.Getenv("GOROOT"); goroot != "" && goroot != "GOROOT" && isGoRoot(goroot) {
		return goroot, sdkFromHost
	}

	goroot := findHostGOROOT()
	if goroot == "" {
		return "", sdkFromHost
	}
	if abs, err := filepath.Abs(goroot); err == nil {
		goroot = abs // the probe runs the go command from another directory
	}
	goPath, err := exec.LookPath(filepath.Join(goroot, "bin", "go"))
	if err != nil {
		return goroot, sdkFromHost // no go command to ask (a source tree, say)
	}
	if switched := switchedGoRoot(goroot, goToolchain(goroot), func(dir string) string { return goEnvGoroot(goPath, dir) }); !sameDir(switched, goroot) {
		return switched, sdkSwitched
	}
	return goroot, sdkFromHost
}

// sameDir reports whether a and b name the same directory, through symlinks:
// `go env GOROOT` reports a resolved path.
func sameDir(a, b string) bool {
	if a == b {
		return true
	}
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
}

// findHostGOROOT is the Go SDK installed on the host: steps 2–4 of findGOROOT.
func findHostGOROOT() string {
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
	return goEnvGoroot(goPath, "")
}

// GeneratedGoVersion is the go line of the go.mod files GALA generates, the Go
// version every build asks the go command for. It mirrors stdlib.GoVersion
// (asserted equal by a test in internal/build), which this package does not
// import: that package embeds the whole standard library source.
const GeneratedGoVersion = "1.25"

// switchedGoRoot is the SDK a build compiles GALA's generated code with, given
// goroot, the host's SDK, and gotoolchain, its GOTOOLCHAIN. The go command
// switches to another toolchain when the module asks for a newer Go than its
// own (every go.mod GALA generates asks for GeneratedGoVersion) or when
// GOTOOLCHAIN names one. goEnv runs that SDK's `go env GOROOT` in a given
// directory; it is run in a temporary module with that go line to learn which
// toolchain the build uses. goroot is kept, with no subprocess, when the go
// command surely does not switch (toolchainMaySwitch); also when goEnv gives
// "".
func switchedGoRoot(goroot, gotoolchain string, goEnv func(dir string) string) string {
	if !toolchainMaySwitch(goRootVersion(goroot), gotoolchain) {
		return goroot
	}
	switched := probeGoRoot(goEnv)
	switch v := goRootVersion(switched); {
	case switched == "":
		fmt.Fprintf(os.Stderr, "Warning: could not tell which Go toolchain builds GALA code (go %s may switch away from %s); Go type inference reads %s.\n", GeneratedGoVersion, goroot, goroot)
		return goroot
	case version.IsValid(v) && version.Compare(v, "go"+GeneratedGoVersion) < 0:
		// A go before 1.21 never switches; GOTOOLCHAIN can pin an old one.
		fmt.Fprintf(os.Stderr, "Warning: GALA code builds with %s (%s), older than Go %s; install Go %s or newer.\n", v, switched, GeneratedGoVersion, GeneratedGoVersion)
	}
	return switched
}

// probeGoRoot is the root goEnv reports in a temporary module whose go line is
// GeneratedGoVersion, or "" when it cannot run there.
func probeGoRoot(goEnv func(dir string) string) string {
	dir, err := os.MkdirTemp("", "gala-goroot-")
	if err != nil {
		return ""
	}
	defer os.RemoveAll(dir)
	mod := "module gala_goroot_probe\n\ngo " + GeneratedGoVersion + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
		return ""
	}
	return goEnv(dir)
}

// goEnvTimeout bounds a `go env GOROOT` run. Switching toolchain may first
// download one; offline, that must not hang every gala process.
const goEnvTimeout = 2 * time.Minute

// goEnvGoroot is `go env GOROOT` as goPath reports it, or "" when that fails
// or is no Go SDK. Run in dir, its toolchain is the one the user's GOTOOLCHAIN
// and the go.work GOWORK names, else dir's go.mod, pick (the build's module
// cache and workspace directory are not reproduced), and its stderr is the
// user's, so that a toolchain download reports there. With dir "", it is goPath's own root: it
// runs in the current directory with no toolchain switch (`go env` answers
// even in a module that asks for a newer Go).
func goEnvGoroot(goPath, dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), goEnvTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, goPath, "env", "GOROOT")
	// A go that switches toolchain on Windows runs the other one as a child,
	// which killing the parent leaves holding the output pipe.
	cmd.WaitDelay = 5 * time.Second
	// GOROOT, when set, is one findGOROOT rejected: let go find its own.
	cmd.Env = append(os.Environ(), "GOROOT=")
	if dir != "" {
		cmd.Dir = dir
		cmd.Stderr = os.Stderr
	} else {
		cmd.Env = append(cmd.Env, "GOTOOLCHAIN=local")
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	if goroot := strings.TrimSpace(string(out)); goroot != "" && isGoRoot(goroot) {
		return goroot
	}
	return ""
}

// goRootVersion is the Go version of the SDK at goroot, from the first line of
// its VERSION file ("go1.25.5"), or "" when it has none.
func goRootVersion(goroot string) string {
	data, err := os.ReadFile(filepath.Join(goroot, "VERSION"))
	if err != nil {
		return ""
	}
	first, _, _ := strings.Cut(string(data), "\n")
	return strings.TrimSpace(first)
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

// goPackageCache caches AnalyzeGoPackage results to avoid redundant go/importer
// calls, and, by import path, the exported names each package declares
// (scopeExports).
var goPackageCache = struct {
	mu      sync.Mutex
	cache   map[string]*transpiler.GoTypeInfo
	exports map[string][]string
}{cache: make(map[string]*transpiler.GoTypeInfo), exports: make(map[string][]string)}

// scopeExports lists, sorted, the exported package-level names pkg declares.
// Its GoTypeInfo cannot tell them: it also holds the types its API reaches,
// filed under their own packages' names, which may be pkg's name too.
func scopeExports(pkg *types.Package) []string {
	var names []string
	for _, name := range pkg.Scope().Names() {
		if token.IsExported(name) {
			names = append(names, name)
		}
	}
	return names
}

// forgetGoPackages drops the cached AnalyzeGoPackage results of paths, or of
// every package when all is set.
func forgetGoPackages(paths []string, all bool) {
	goPackageCache.mu.Lock()
	defer goPackageCache.mu.Unlock()
	if all {
		clear(goPackageCache.cache)
		clear(goPackageCache.exports)
		return
	}
	for _, p := range paths {
		delete(goPackageCache.cache, p)
		delete(goPackageCache.exports, p)
	}
}

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
	info, _ := analyzeGoPackage(importPath)
	return info
}

// analyzeGoPackage is AnalyzeGoPackage, with the exported names the package
// declares (scopeExports), read together with its type info; nil when it did
// not load.
func analyzeGoPackage(importPath string) (*transpiler.GoTypeInfo, []string) {
	goPackageCache.mu.Lock()
	if cached, ok := goPackageCache.cache[importPath]; ok {
		exports := goPackageCache.exports[importPath]
		goPackageCache.mu.Unlock()
		return cached, exports
	}
	goPackageCache.mu.Unlock()

	info := transpiler.NewGoTypeInfo()

	imp := getGoImporter()
	if !goImporterAvailable {
		return info, nil
	}

	pkg, err := imp.Import(importPath)
	if err != nil {
		return info, nil
	}

	extractPackageInfo(pkg, info, false)
	info.AddImportPathKeys(pkg.Name(), pkg.Path())
	exports := scopeExports(pkg)

	goPackageCache.mu.Lock()
	goPackageCache.cache[importPath] = info
	goPackageCache.exports[importPath] = exports
	goPackageCache.mu.Unlock()

	return info, exports
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
	cache map[string][]goFilesResult
}{cache: make(map[string][]goFilesResult)}

// goFilesResult is what one scan of a directory's .go files yields: their type
// info, and the bare names of the types they declare themselves. Type info
// cannot tell the second apart, since it is keyed by package name and also
// files types of other packages a signature mentions.
type goFilesResult struct {
	info     *transpiler.GoTypeInfo
	ownTypes map[string]bool
	imports  []string // the import paths of the files that took part
	// exports is the package's portableExports.
	exports []string
	// builtinRefs are the Go predeclared identifiers (`len`, `print`, …)
	// the files refer to.
	builtinRefs map[string]bool
	// dirsUsed records how the type check resolved the imports it read.
	dirsUsed moduleDirs
}

// AnalyzeGoFiles parses and type-checks local .go files and extracts type info.
// This handles Go source files that live alongside GALA files or in Go-only packages.
// Files the GALA transpiler wrote are skipped — their metadata comes from
// analyzing the originating .gala source. A .gen.go another generator wrote
// (oapi-codegen, stringer, …) is analyzed like hand-written Go.
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
	return analyzeGoFilesMemo(dirPath, importPath, "", nil).info
}

// AnalyzeOwnGoFiles is AnalyzeGoFiles for the hand-written .go files of the
// package being compiled, named pkgName. Only the files of that package take
// part — the package clause must name pkgName — and the package's unexported
// declarations are recorded too. It also returns the bare names of the types
// those files declare, and the paths they import.
func AnalyzeOwnGoFiles(dirPath, importPath, pkgName string) (*transpiler.GoTypeInfo, map[string]bool, []string) {
	r := analyzeGoFilesMemo(dirPath, importPath, pkgName, nil)
	return r.info, r.ownTypes, r.imports
}

// analyzeGoFilesMemo is analyzeGoFiles, memoized. goSrcDirs is the table of Go
// module source directories (see galaAnalyzer.goSrcDirs) that the files'
// imports of module packages are type-checked from; nil when none is wired.
// A result is reused under another table that resolves the imports it read
// the same way (see moduleDirs).
func analyzeGoFilesMemo(dirPath, importPath, pkgName string, goSrcDirs map[string]string) goFilesResult {
	importPath = goFilesImportPath(dirPath, importPath)
	cacheKey := dirPath + "\x00" + importPath + "\x00" + pkgName

	goFilesCache.mu.Lock()
	for _, cached := range goFilesCache.cache[cacheKey] {
		if cached.dirsUsed.holdIn(goSrcDirs) {
			goFilesCache.mu.Unlock()
			return cached
		}
	}
	goFilesCache.mu.Unlock()

	r := analyzeGoFiles(dirPath, importPath, pkgName, goSrcDirs)
	goFilesCache.mu.Lock()
	goFilesCache.cache[cacheKey] = append(goFilesCache.cache[cacheKey], r)
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
func analyzeGoFiles(dirPath, importPath, pkgName string, goSrcDirs map[string]string) goFilesResult {
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
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fullPath := filepath.Join(dirPath, name)
		// Told by its first line, before paying for a full parse.
		if genheader.StaleFile(fullPath) {
			continue
		}
		f, err := parser.ParseFile(fset, fullPath, nil, parser.ParseComments)
		if err != nil {
			continue
		}
		if (pkgName != "" && f.Name.Name != pkgName) || neverBuilt(f) || genheader.Written(f) {
			continue
		}
		files = append(files, f)
		for _, imp := range f.Imports {
			if p := strings.Trim(imp.Path.Value, `"`); !slices.Contains(result.imports, p) {
				result.imports = append(result.imports, p)
			}
		}
	}

	if len(files) == 0 {
		return result
	}

	// Type-check the parsed files, the imports the shared importer serves
	// resolved in one batch first (a wired module's are read from source).
	prefetchGoPackages(slices.DeleteFunc(slices.Clone(result.imports), func(p string) bool {
		_, fromSource := ResolveGoSrcDir(goSrcDirs, p)
		return fromSource
	}))
	imp := newModuleSourceImporter(goSrcDirs)
	conf := types.Config{
		Importer: imp,
		Error:    func(err error) {}, // Ignore type-check errors (partial analysis is fine)
	}
	typesInfo := &types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		Defs:  make(map[*ast.Ident]types.Object),
		Uses:  make(map[*ast.Ident]types.Object),
	}

	pkg, _ := conf.Check(importPath, fset, files, typesInfo)
	result.dirsUsed = imp.used
	for id, obj := range typesInfo.Uses {
		if obj != nil && types.Universe.Lookup(id.Name) == obj {
			if result.builtinRefs == nil {
				result.builtinRefs = make(map[string]bool)
			}
			result.builtinRefs[id.Name] = true
		}
	}
	if pkg == nil {
		// Even if type-checking fails, try to extract what we can from AST
		extractFromAST(files, info)
		return result
	}

	own := pkgName != ""
	extractPackageInfo(pkg, info, own)
	repairUnresolvedSignatures(files, pkg.Name(), info, own)
	extractMethodsOnForeignTypes(files, typesInfo, pkg, info, own)
	recordReadOnlyMethods(files, pkg, info)
	info.AddImportPathKeys(pkg.Name(), importPath)
	result.exports = portableExports(pkg, fset, files)
	result.ownTypes = make(map[string]bool)
	for _, name := range pkg.Scope().Names() {
		if tn, ok := pkg.Scope().Lookup(name).(*types.TypeName); ok && (own || tn.Exported()) {
			result.ownTypes[name] = true
		}
	}
	return result
}

// portableExports is the scopeExports of pkg, type-checked from files, that
// every target in portableTargets builds: the files declaring a name build
// on each of them between them (open_unix.go and open_windows.go). The files
// are those of every platform (neverBuilt), so a name only some targets get
// (foo_windows.go alone, `//go:build linux`) may be absent from the build;
// it is left out.
func portableExports(pkg *types.Package, fset *token.FileSet, files []*ast.File) []string {
	declaredIn := make(map[string][]fileBuild)
	for _, f := range files {
		fb := newFileBuild(f, fset.Position(f.Package).Filename)
		for _, name := range topLevelNames(f) {
			declaredIn[name] = append(declaredIn[name], fb)
		}
	}
	return slices.DeleteFunc(scopeExports(pkg), func(name string) bool {
		for _, target := range portableTargets {
			for _, cgo := range []bool{true, false} {
				if !slices.ContainsFunc(declaredIn[name], func(fb fileBuild) bool {
					return fb.buildsOn(target[0], target[1], cgo)
				}) {
					return true
				}
			}
		}
		return false
	})
}

// portableTargets are the GOOS/GOARCH pairs portableExports requires a name
// on: the platforms the transpiled programs are built for.
var portableTargets = [][2]string{
	{"linux", "amd64"}, {"linux", "arm64"},
	{"darwin", "amd64"}, {"darwin", "arm64"},
	{"windows", "amd64"}, {"windows", "arm64"},
}

// topLevelNames lists the package-level names f declares: its functions (not
// methods), types, vars and consts.
func topLevelNames(f *ast.File) []string {
	var names []string
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				names = append(names, d.Name.Name)
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					names = append(names, s.Name.Name)
				case *ast.ValueSpec:
					for _, n := range s.Names {
						names = append(names, n.Name)
					}
				}
			}
		}
	}
	return names
}

// fileBuild is what decides, as go/build does, which targets build a file.
type fileBuild struct {
	ignored bool   // go/build skips it by name (`_x.go`, `.x.go`)
	goos    string // the GOOS its name requires, or ""
	goarch  string // the GOARCH its name requires, or ""
	cgo     bool   // it imports "C", so only a cgo build has it
	unknown bool   // a legacy `// +build` line only, or an unparseable `//go:build`
	expr    constraint.Expr
}

func newFileBuild(f *ast.File, path string) fileBuild {
	base := filepath.Base(path)
	fb := fileBuild{ignored: strings.HasPrefix(base, "_") || strings.HasPrefix(base, ".")}
	name, _, _ := strings.Cut(base, ".")
	if parts := strings.Split(strings.TrimSuffix(name, "_test"), "_"); len(parts) >= 2 {
		last := parts[len(parts)-1]
		switch {
		case knownGOARCH[last]:
			fb.goarch = last
			if len(parts) >= 3 && knownGOOS[parts[len(parts)-2]] {
				fb.goos = parts[len(parts)-2]
			}
		case knownGOOS[last]:
			fb.goos = last
		}
	}
	fb.cgo = slices.ContainsFunc(f.Imports, func(s *ast.ImportSpec) bool { return s.Path.Value == `"C"` })
	expr, plusBuild, invalid := buildConstraint(f)
	fb.expr, fb.unknown = expr, invalid || (expr == nil && plusBuild)
	return fb
}

// buildsOn reports whether a build for goos/goarch, with cgo as given,
// compiles the file. go/build matches a linux file on android, a solaris one
// on illumos and a darwin one on ios.
func (fb fileBuild) buildsOn(goos, goarch string, cgo bool) bool {
	osMatches := fb.goos == "" || fb.goos == goos ||
		(fb.goos == "linux" && goos == "android") || (fb.goos == "solaris" && goos == "illumos") ||
		(fb.goos == "darwin" && goos == "ios")
	switch {
	case fb.ignored || fb.unknown || !osMatches || (fb.goarch != "" && fb.goarch != goarch) || (fb.cgo && !cgo):
		return false
	case fb.expr != nil:
		return buildsFor(fb.expr, goos, goarch, cgo)
	}
	return true
}

// buildConstraint is f's `//go:build` expression, nil when it has none,
// whether it has a legacy `// +build` line, and whether its `//go:build`
// line does not parse.
func buildConstraint(f *ast.File) (expr constraint.Expr, plusBuild, invalid bool) {
	for _, group := range f.Comments {
		if group.Pos() >= f.Package {
			break
		}
		for _, c := range group.List {
			if constraint.IsGoBuild(c.Text) {
				if e, err := constraint.Parse(c.Text); err == nil {
					expr = e
				} else {
					invalid = true
				}
			}
			plusBuild = plusBuild || constraint.IsPlusBuild(c.Text)
		}
	}
	return expr, plusBuild, invalid
}

// unixGOOS holds the GOOS values the `unix` build tag matches.
var unixGOOS = map[string]bool{"aix": true, "android": true, "darwin": true, "dragonfly": true, "freebsd": true, "illumos": true, "ios": true, "linux": true, "netbsd": true, "openbsd": true, "solaris": true}

// buildsFor reports whether expr holds for goos/goarch built by gc, with every
// Go release on and cgo as given.
func buildsFor(expr constraint.Expr, goos, goarch string, cgo bool) bool {
	return expr.Eval(func(tag string) bool {
		return tag == goos || tag == goarch || (tag == "cgo" && cgo) || tag == "gc" ||
			(tag == "unix" && unixGOOS[goos]) || strings.HasPrefix(tag, "go1.")
	})
}

// knownGOOS and knownGOARCH are every GOOS and GOARCH go/build matches in a
// file name (name_GOOS.go, name_GOARCH.go, name_GOOS_GOARCH.go), the
// unsupported ones included.
var (
	knownGOOS   = map[string]bool{"aix": true, "android": true, "darwin": true, "dragonfly": true, "freebsd": true, "hurd": true, "illumos": true, "ios": true, "js": true, "linux": true, "nacl": true, "netbsd": true, "openbsd": true, "plan9": true, "solaris": true, "wasip1": true, "windows": true, "zos": true}
	knownGOARCH = map[string]bool{"386": true, "amd64": true, "amd64p32": true, "arm": true, "armbe": true, "arm64": true, "arm64be": true, "loong64": true, "mips": true, "mipsle": true, "mips64": true, "mips64le": true, "mips64p32": true, "mips64p32le": true, "ppc": true, "ppc64": true, "ppc64le": true, "riscv": true, "riscv64": true, "s390": true, "s390x": true, "sparc": true, "sparc64": true, "wasm": true}
)

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
	expr, _, _ := buildConstraint(f)
	if expr == nil {
		return false
	}
	for _, goos := range goosList {
		for _, goarch := range goarchList {
			if buildsFor(expr, goos, goarch, true) {
				return false
			}
		}
	}
	return true
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
				sig = convertSignatureIn(fn.Type().(*types.Signature), ownPackage(pkg, own))
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

// repairUnresolvedSignatures recovers the types go/types could not resolve —
// in function and method signatures, struct fields and package variables —
// from the type as it is WRITTEN in the source (a variable without one, from
// the composite literal that initializes it).
//
// A hand-written .go file in a GALA package may name a GALA-defined type — e.g.
// `func OptionFromMap[K comparable, V any](m map[K]V, key K) std.Option[V]`. That
// type only exists as Go once the GALA package has been transpiled, but this
// analysis deliberately runs against the un-transpiled tree (the transpiler's
// own output is skipped so metadata comes from the .gala source). go/types therefore resolves
// the reference to Invalid while the rest of the signature — including the type
// parameters — resolves normally.
//
// Left alone, that unresolved slot degrades to `any` and silently erases a
// concrete type. The AST still says `std.Option[V]`, and the file's import list
// says which package `std` is, which together are exactly the GenericType the rest
// of the pipeline needs. Recovery is therefore syntactic.
//
// Nothing here is specific to std, or even to GALA: any Go declaration naming a
// type the checker could not load is recovered the same way, and slots go/types
// DID resolve are never touched.
func repairUnresolvedSignatures(files []*ast.File, pkgName string, info *transpiler.GoTypeInfo, own bool) {
	for _, f := range files {
		imports := fileImportPaths(f)
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				// Every recorded function and method is repaired: the
				// package's own files record unexported ones too (see
				// AnalyzeOwnGoFiles).
				if d.Type == nil {
					continue
				}
				if sig := recordedSignature(info, pkgName, d); sig != nil {
					repairSignature(sig, d, imports, pkgName)
				}
			case *ast.GenDecl:
				repairGenDecl(d, imports, pkgName, info, own)
			}
		}
	}
}

// recordedSignature returns the signature info records for fd: a function's,
// or a method's on its receiver type. (Methods hand-written Go adds to a GALA
// type are repaired by extractMethodsOnForeignTypes.)
func recordedSignature(info *transpiler.GoTypeInfo, pkgName string, fd *ast.FuncDecl) *transpiler.GoFuncSignature {
	if fd.Recv == nil {
		return info.Functions[pkgName+"."+fd.Name.Name]
	}
	if len(fd.Recv.List) != 1 {
		return nil
	}
	recv, _ := receiverBaseName(fd.Recv.List[0].Type)
	if td := info.Types[pkgName+"."+recv]; recv != "" && td != nil {
		return td.Methods[fd.Name.Name]
	}
	return nil
}

// repairGenDecl recovers the struct field types and the declared types of
// package variables in d that go/types could not resolve.
func repairGenDecl(d *ast.GenDecl, imports map[string]string, pkgName string, info *transpiler.GoTypeInfo, own bool) {
	for _, spec := range d.Specs {
		switch sp := spec.(type) {
		case *ast.TypeSpec:
			// An alias whose target go/types could not load (`type
			// ExecutionContext = go_interop.ExecutionContext`, the target a
			// package of the GALA module) names it as written.
			key := pkgName + "." + sp.Name.Name
			if cur, ok := info.TypeAliases[key]; ok && sp.Assign.IsValid() && sp.TypeParams == nil && transpiler.ContainsUnusable(cur) {
				if rec := syntacticGoType(sp.Type, imports, pkgName, nil); !transpiler.ContainsUnusable(rec) {
					info.TypeAliases[key] = rec
				}
			}
			st, ok := sp.Type.(*ast.StructType)
			td := info.Types[key]
			if !ok || td == nil || st.Fields == nil {
				continue
			}
			typeParams := map[string]bool{}
			if sp.TypeParams != nil {
				for _, field := range sp.TypeParams.List {
					for _, name := range field.Names {
						typeParams[name.Name] = true
					}
				}
			}
			for _, field := range st.Fields.List {
				for _, name := range field.Names {
					if cur, ok := td.Fields[name.Name]; ok && transpiler.ContainsUnusable(cur) {
						if rec := syntacticGoType(field.Type, imports, pkgName, typeParams); !rec.IsNil() {
							td.Fields[name.Name] = rec
						}
					}
				}
			}
		case *ast.ValueSpec:
			if d.Tok != token.VAR {
				continue
			}
			for i, name := range sp.Names {
				if name.Name == "_" {
					continue
				}
				key := pkgName + "." + name.Name
				// A variable go/types could not type at all is not recorded;
				// it is added the way extraction would have (own files record
				// unexported names too).
				cur, ok := info.Variables[key]
				if ok && !transpiler.ContainsUnusable(cur) || !ok && !own && !name.IsExported() {
					continue
				}
				typeExpr := sp.Type
				if typeExpr == nil && i < len(sp.Values) {
					typeExpr = literalTypeExpr(sp.Values[i])
				}
				if typeExpr == nil {
					continue
				}
				if rec := syntacticGoType(typeExpr, imports, pkgName, nil); !rec.IsNil() {
					info.Variables[key] = rec
				}
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

	case *ast.StructType:
		// Only the empty struct: typing a field exactly needs the type
		// checker (see goTypeToTranspilerType).
		if e.Fields == nil || len(e.Fields.List) == 0 {
			return transpiler.StructType{}
		}
		return transpiler.NilType{}

	case *ast.ChanType:
		elem := syntacticGoType(e.Value, imports, pkgName, typeParams)
		if elem.IsNil() {
			return transpiler.NilType{}
		}
		dir := transpiler.ChanBoth
		switch e.Dir {
		case ast.RECV:
			dir = transpiler.ChanRecv
		case ast.SEND:
			dir = transpiler.ChanSend
		}
		return transpiler.ChanType{Elem: elem, Dir: dir}

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
			info.Functions[qualName] = convertSignatureIn(sig, ownPackage(pkg, own))

		case *types.TypeName:
			if obj.IsAlias() {
				// Go type alias: type X = Y
				// Resolve to the aliased type for transparent type inference,
				// exactly when GALA can spell it (`type Gen = func() [4]byte`
				// keeps the array's length), as a value of it is written.
				underlying := spelledGoTypeIn(obj.Type(), ownPackage(pkg, own))
				info.TypeAliases[qualName] = underlying
				// Also create type data so methods can be looked up on the alias
				// name (e.g. "os.DirEntry").
				info.Types[qualName] = extractTypeData(obj, "alias", own)
				// When the alias re-exports a *named* type from another package
				// (e.g. `os.DirEntry = io/fs.DirEntry`), the walk below also
				// registers the target under its own canonical key. Go
				// signatures resolve such aliases to the canonical name (a
				// return of `[]os.DirEntry` surfaces as `[]fs.DirEntry`), so a
				// method call on a value of that type (`entries[i].Info()`)
				// looks the method set up under `fs.DirEntry`.
			} else {
				// Go type definition: type X struct{...} or type X int
				info.Types[qualName] = extractTypeData(obj, "", own)
			}

		case *types.Var:
			info.Variables[qualName] = spelledGoTypeIn(obj.Type(), ownPackage(pkg, own))

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

	// A value of the package's API often has a named type defined in another
	// package: sha256.New() returns hash.Hash, and cobra's Command.Flags()
	// returns *pflag.FlagSet, whose Lookup returns *pflag.Flag in turn. Record
	// the method set of every type a value can be reached through, under its
	// canonical "pkg.Name" key, so a call on such a value finds its Go
	// signature (and a (T, error) result lowers to Try) although the defining
	// package was never directly imported. It runs after the loop above so
	// the package's own types keep the entries that loop made for them.
	reach := reachableTypes{info: info, seen: make(map[*types.TypeName]bool)}
	for _, name := range scope.Names() {
		if obj := scope.Lookup(name); obj.Exported() || own {
			switch obj.(type) {
			case *types.Func, *types.Var, *types.TypeName:
				reach.walk(obj.Type())
			}
		}
	}
}

// reachableTypes records in info.Types, under the canonical "pkgName.TypeName"
// key, each named type a value of a Go package's API can have, unless an entry
// exists already. A value comes from a function's or method's results, from a
// field or variable, or as an argument handed to a callback, so from a named
// type the walk follows its exported fields and its exported methods. Each
// named type is visited once, which bounds the walk.
type reachableTypes struct {
	info *transpiler.GoTypeInfo
	seen map[*types.TypeName]bool
}

// walk visits the named types reachable from a value of type t.
func (r reachableTypes) walk(t types.Type) {
	switch tt := t.(type) {
	case *types.Alias:
		r.walk(types.Unalias(tt))
	case *types.Pointer:
		r.walk(tt.Elem())
	case *types.Slice:
		r.walk(tt.Elem())
	case *types.Array:
		r.walk(tt.Elem())
	case *types.Chan:
		r.walk(tt.Elem())
	case *types.Map:
		r.walk(tt.Key())
		r.walk(tt.Elem())
	case *types.Signature:
		r.walkFunc(tt)
	case *types.Struct:
		for i := 0; i < tt.NumFields(); i++ {
			if f := tt.Field(i); f.Exported() {
				r.walk(f.Type())
			}
		}
	case *types.Named:
		if args := tt.TypeArgs(); args != nil {
			for i := 0; i < args.Len(); i++ {
				r.walk(args.At(i))
			}
		}
		named := tt.Origin()
		tn := named.Obj()
		if tn == nil || tn.Pkg() == nil || r.seen[tn] {
			return
		}
		r.seen[tn] = true
		canonical := tn.Pkg().Name() + "." + tn.Name()
		if _, exists := r.info.Types[canonical]; !exists {
			r.info.Types[canonical] = extractTypeData(tn, "", false)
		}
		// An interface's methods are in its own method set (a pointer to an
		// interface has none); any other type's are in its pointer's.
		var recv types.Type = named
		if _, isIface := named.Underlying().(*types.Interface); !isIface {
			r.walk(named.Underlying())
			recv = types.NewPointer(named)
		}
		mset := types.NewMethodSet(recv)
		for i := 0; i < mset.Len(); i++ {
			if fn := mset.At(i).Obj(); fn.Exported() {
				r.walkFunc(fn.Type().(*types.Signature))
			}
		}
	}
}

// walkFunc visits the named types a call of sig yields, and those of the
// arguments a callback parameter of sig is handed.
func (r reachableTypes) walkFunc(sig *types.Signature) {
	for i := 0; i < sig.Results().Len(); i++ {
		r.walk(sig.Results().At(i).Type())
	}
	for i := 0; i < sig.Params().Len(); i++ {
		if cb, ok := types.Unalias(sig.Params().At(i).Type()).Underlying().(*types.Signature); ok {
			for j := 0; j < cb.Params().Len(); j++ {
				r.walk(cb.Params().At(j).Type())
			}
		}
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
	goroot := GoSDKRoot()
	if goroot == "" {
		return ""
	}
	dir := filepath.Join(goroot, "src", filepath.FromSlash(importPath))
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return dir
	}
	return ""
}

// isGenericGoType reports whether typ, a declared Go type or alias, has type
// parameters of its own.
func isGenericGoType(typ types.Type) bool {
	switch v := typ.(type) {
	case *types.Named:
		return v.TypeParams().Len() > 0
	case *types.Alias:
		return v.TypeParams().Len() > 0
	}
	return false
}

// hasSelfEqual reports whether the value method set of typ has
// `Equal(typ) bool`: the method std.EqualComparable finds (Equatable[T]), so
// == compares with it in concrete code as in generic code. An Equal on the
// pointer only, or one taking *typ or another type, does not count.
func hasSelfEqual(typ types.Type) bool {
	sel := types.NewMethodSet(typ).Lookup(nil, "Equal")
	if sel == nil {
		return false
	}
	sig, ok := sel.Type().(*types.Signature)
	return ok && !sig.Variadic() && sig.Params().Len() == 1 && sig.Results().Len() == 1 &&
		types.Identical(sig.Params().At(0).Type(), typ) &&
		types.Identical(sig.Results().At(0).Type(), types.Typ[types.Bool])
}

// universeComparable is Go's predeclared comparable constraint.
var universeComparable = types.Universe.Lookup("comparable").Type()

// embedsComparable reports whether iface embeds comparable, directly or
// through an interface it embeds. seen guards an embedding cycle, which the
// type checker reports but may still hand over.
func embedsComparable(iface *types.Interface, seen map[*types.Interface]bool) bool {
	for i := range iface.NumEmbeddeds() {
		e := iface.EmbeddedType(i)
		if types.Identical(e, universeComparable) {
			return true
		}
		inner, ok := e.Underlying().(*types.Interface)
		if !ok || seen[inner] {
			continue
		}
		if seen == nil {
			seen = map[*types.Interface]bool{}
		}
		seen[iface] = true
		if embedsComparable(inner, seen) {
			return true
		}
	}
	return false
}

// interfaceCoreType is the one underlying type of every type in iface's type
// set (`~int64`, `int64 | MyInt`, `interface{ ~int | ~int64; ~int64 }`), or
// nil when the set is unrestricted (no type terms, or a union with `any`) or
// holds types of different underlying types (`~int | ~int64`), which Go
// cannot range over.
func interfaceCoreType(iface *types.Interface) types.Type {
	set := underlyingTypeSet(iface, nil)
	if set.all || len(set.types) == 0 {
		return nil
	}
	core := set.types[0]
	if core == types.Typ[types.Invalid] { // a type error, an embedding cycle say
		return nil
	}
	for _, u := range set.types[1:] {
		if !types.Identical(core, u) {
			return nil
		}
	}
	return core
}

// interfaceHasNoCoreType reports whether iface's type set provably has no
// core type, so Go cannot range over a type parameter it constrains: the set
// is unrestricted (`any`, `comparable`, methods only), or holds types of
// different underlying types. A set with an unresolved type, or with a
// channel (Go merges channels that differ only in direction), is not known
// to have none, nor is an empty one, nor one whose types mention a type
// parameter (an instantiation can make them identical).
func interfaceHasNoCoreType(iface *types.Interface) bool {
	set := underlyingTypeSet(iface, nil)
	if set.all {
		return true
	}
	distinct := false
	for _, u := range set.types {
		if _, isChan := u.(*types.Chan); isChan || !settledType(u) {
			return false
		}
		if !types.Identical(set.types[0], u) {
			distinct = true
		}
	}
	return distinct
}

// settledType reports whether t mentions neither an unresolved type nor a
// type parameter, at any depth.
func settledType(t types.Type) bool {
	switch t := t.(type) {
	case *types.Basic:
		return t.Kind() != types.Invalid
	case *types.TypeParam:
		return false
	case *types.Pointer:
		return settledType(t.Elem())
	case *types.Slice:
		return settledType(t.Elem())
	case *types.Array:
		return settledType(t.Elem())
	case *types.Chan:
		return settledType(t.Elem())
	case *types.Map:
		return settledType(t.Key()) && settledType(t.Elem())
	case *types.Signature:
		return settledTuple(t.Params()) && settledTuple(t.Results())
	case *types.Struct:
		for i := range t.NumFields() {
			if !settledType(t.Field(i).Type()) {
				return false
			}
		}
		return true
	case *types.Named:
		for i := range t.TypeArgs().Len() {
			if !settledType(t.TypeArgs().At(i)) {
				return false
			}
		}
		return true
	case *types.Alias:
		return settledType(types.Unalias(t))
	case *types.Interface:
		for i := range t.NumExplicitMethods() {
			if !settledType(t.ExplicitMethod(i).Type()) {
				return false
			}
		}
		for i := range t.NumEmbeddeds() {
			if !settledType(t.EmbeddedType(i)) {
				return false
			}
		}
		return true
	case *types.Union:
		for i := range t.Len() {
			if !settledType(t.Term(i).Type()) {
				return false
			}
		}
		return true
	}
	return true
}

func settledTuple(t *types.Tuple) bool {
	for i := range t.Len() {
		if !settledType(t.At(i).Type()) {
			return false
		}
	}
	return true
}

// typeSet is a type set by underlying type: all types (all), or those whose
// underlying type is one of types. Telling `~T` from `T` apart is not needed
// to find a core type: it only shrinks a set whose types share T.
type typeSet struct {
	all   bool
	types []types.Type
}

// underlyingTypeSet is iface's type set: the intersection of its elements,
// each a union of terms or an embedded interface (comparable and method
// requirements do not restrict it by type). seen holds the interfaces being
// expanded: an embedding cycle, which the type checker reports but may still
// hand over, is the unrestricted set.
func underlyingTypeSet(iface *types.Interface, seen map[*types.Interface]bool) typeSet {
	if seen[iface] {
		return typeSet{all: true}
	}
	if seen == nil {
		seen = map[*types.Interface]bool{}
	}
	seen[iface] = true
	defer delete(seen, iface)
	set := typeSet{all: true}
	for i := range iface.NumEmbeddeds() {
		var elem typeSet
		switch e := iface.EmbeddedType(i).(type) {
		case *types.Union:
			for j := range e.Len() {
				elem = elem.union(termSet(e.Term(j).Type(), seen))
			}
		default:
			elem = termSet(e, seen)
		}
		set = set.intersect(elem)
	}
	return set
}

// termSet is the type set of one term: an interface's own, or the term's
// underlying type.
func termSet(t types.Type, seen map[*types.Interface]bool) typeSet {
	if inner, isIface := t.Underlying().(*types.Interface); isIface {
		return underlyingTypeSet(inner, seen)
	}
	return typeSet{types: []types.Type{t.Underlying()}}
}

func (s typeSet) union(o typeSet) typeSet {
	if s.all || o.all {
		return typeSet{all: true}
	}
	return typeSet{types: append(slices.Clip(s.types), o.types...)}
}

func (s typeSet) intersect(o typeSet) typeSet {
	switch {
	case s.all:
		return o
	case o.all:
		return s
	}
	var both []types.Type
	for _, t := range s.types {
		if slices.ContainsFunc(o.types, func(u types.Type) bool { return types.Identical(t, u) }) {
			both = append(both, t)
		}
	}
	return typeSet{types: both}
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

	// Set underlying type. A named struct's fields are in Fields, and its
	// underlying struct, unexported fields and all, is never spelled in GALA
	// or emitted, so it stays the placeholder rather than a StructType that
	// would copy every field into the cache a second time.
	if _, isStruct := typ.Underlying().(*types.Struct); isStruct {
		data.Underlying = transpiler.BasicType{Name: "any"}
	} else if forceKind == "alias" && isGenericGoType(typ) {
		// A generic alias's target types the lambdas that fill a slot of
		// an instance of it (`Handler[int]`), so it is recorded as Go spells
		// it: an instance keeps its type arguments, an array its length, an
		// interface literal its methods. A target GALA cannot spell exactly
		// (a variadic function, an unexported name), a type parameter (whose
		// Underlying is its constraint) or an interface records the `any`
		// placeholder, which types nothing.
		under := typ.Underlying()
		_, isParam := types.Unalias(typ).(*types.TypeParam)
		_, isIface := under.(*types.Interface)
		if isParam || isIface || !spellsExactlyIn(under, ownPackage(tn.Pkg(), own)) {
			data.Underlying = transpiler.BasicType{Name: "any"}
		} else {
			data.Underlying = convertGoType(under, true)
		}
	} else if _, isArr := typ.Underlying().(*types.Array); isArr && forceKind == "alias" {
		// An alias over an array (`type Key = [32]byte`) keeps its length
		// as a defined type does, so a slot of it is known to hold no nil,
		// also when its element is spelled lossily (`[4]os.FileMode`).
		data.Underlying = definedUnderlying(typ.Underlying(), ownPackage(tn.Pkg(), own))
	} else if _, isIface := typ.Underlying().(*types.Interface); !isIface && forceKind != "alias" {
		data.Underlying = definedUnderlying(typ.Underlying(), ownPackage(tn.Pkg(), own))
	} else {
		data.Underlying = goTypeToTranspilerType(typ.Underlying())
	}
	data.NoCopy = noCopyReason(typ)
	// A generic type's comparability depends on its type arguments.
	if !isGenericGoType(typ) {
		data.Incomparable = !types.Comparable(typ)
	}
	if iface, ok := typ.Underlying().(*types.Interface); ok {
		data.EmbedsComparable = embedsComparable(iface, nil)
		if core := interfaceCoreType(iface); core != nil {
			// An unresolved core (types.Typ[Invalid]) converts to NilType:
			// none is recorded.
			if ct := spelledGoTypeIn(core, ownPackage(tn.Pkg(), own)); ct != nil && !ct.IsNil() {
				data.CoreType = ct
			}
		}
		data.NoCoreType = interfaceHasNoCoreType(iface)
	} else if !isGenericGoType(typ) {
		data.SelfEqual = hasSelfEqual(typ)
	}

	// Extract struct fields. Preserve declaration order in FieldOrder so
	// downstream consumers that build positional composite literals
	// (synthesizeTypeMetadataFromGo's FieldNames slice) match the Go-side
	// field layout instead of inheriting the random iteration order of the
	// Fields map.
	if s, ok := typ.Underlying().(*types.Struct); ok {
		for i := 0; i < s.NumFields(); i++ {
			f := s.Field(i)
			if visible(f) {
				data.Fields[f.Name()] = spelledGoTypeIn(f.Type(), ownPackage(tn.Pkg(), own))
				data.FieldOrder = append(data.FieldOrder, f.Name())
			}
		}
	}

	// Extract type parameter names for generic types so downstream consumers
	// (e.g., the dot-import-used scan and method lookup on generic Go-style
	// structs imported via dot import) can resolve `Type[T]` references and
	// the methods they expose.
	var tps *types.TypeParamList
	switch t := typ.(type) {
	case *types.Named:
		tps = t.TypeParams()
	case *types.Alias:
		// A generic alias (`type A[T any] = *T`): its target, recorded in
		// TypeAliases, names these, which an instantiation substitutes.
		tps = t.TypeParams()
	}
	for i := 0; i < tps.Len(); i++ {
		data.TypeParams = append(data.TypeParams, tps.At(i).Obj().Name())
	}

	// Extract method set (including pointer receiver methods)
	mset := types.NewMethodSet(types.NewPointer(typ))
	for i := 0; i < mset.Len(); i++ {
		sel := mset.At(i)
		fn := sel.Obj().(*types.Func)
		if !visible(fn) {
			continue
		}
		data.Methods[fn.Name()] = goMethodSignature(fn, typ, ownPackage(tn.Pkg(), own))
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
			data.Methods[fn.Name()] = goMethodSignature(fn, typ, ownPackage(tn.Pkg(), own))
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
			data.Methods[fn.Name()] = goMethodSignature(fn, typ, ownPackage(tn.Pkg(), own))
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

// goMethodSignature converts the signature of fn, a method of the Go type typ.
// A method declared on typ itself whose receiver names typ's type parameters
// differently (`func (b Box[U]) M(f func(U))` on `Box[T]`) is recorded in
// typ's own names, as renameReceiverTypeParams does for a GALA method: the
// type's metadata is read against its declared names. A Go method has no type
// parameters of its own, so no rename can capture one. A method promoted from
// an embedded field keeps its signature as is.
func goMethodSignature(fn *types.Func, typ types.Type, home *types.Package) *transpiler.GoFuncSignature {
	sig := fn.Type().(*types.Signature)
	result := convertSignatureIn(sig, home)
	named, ok := typ.(*types.Named)
	recv := sig.RecvTypeParams()
	// A generic alias's method (`Next() T` of `type Src[T any] = Iter[T]`,
	// `Get() U` of `type B[U any] = Box[U]`) names the alias's type
	// parameters: it takes them when it names any, and none otherwise.
	if alias, isAlias := typ.(*types.Alias); isAlias && alias.TypeParams().Len() > 0 {
		result.TypeParams, result.CoreTypes = nil, nil
		if mentionsTypeParam(sig) {
			for i := 0; i < alias.TypeParams().Len(); i++ {
				result.TypeParams = append(result.TypeParams, alias.TypeParams().At(i).Obj().Name())
			}
		}
		return result
	}
	// A generic interface's method has no receiver type parameters of its
	// own in go/types but names the interface's (`Next() T` of
	// `Iter[T any]`): it takes them, so an instance (`Iter[int]`)
	// substitutes them. One that names none (`String()` promoted from an
	// embedded strings.Builder) is left as it is.
	if ok && recv.Len() == 0 && len(result.TypeParams) == 0 && mentionsTypeParam(sig) {
		if declared := named.Origin().TypeParams(); declared.Len() > 0 {
			for i := 0; i < declared.Len(); i++ {
				result.TypeParams = append(result.TypeParams, declared.At(i).Obj().Name())
			}
			return result
		}
	}
	// A method promoted from an embedded generic field (`Get() T` of an
	// embedded `Inner[T]`) keeps that field's receiver type parameters in
	// go/types, but its signature already names the outer type's: it takes
	// the outer type's when it names any, and none otherwise.
	if ok && recv.Len() > 0 && !declaresMethod(named.Origin(), fn.Origin()) {
		declared := named.Origin().TypeParams()
		result.TypeParams, result.CoreTypes = nil, nil
		if mentionsTypeParam(sig) {
			for i := 0; i < declared.Len(); i++ {
				result.TypeParams = append(result.TypeParams, declared.At(i).Obj().Name())
			}
		}
		return result
	}
	if !ok || recv == nil || fn.Origin().Pkg() != named.Obj().Pkg() || !declaresMethod(named.Origin(), fn.Origin()) {
		return result
	}
	declared := named.Origin().TypeParams()
	if declared == nil || recv.Len() != declared.Len() {
		return result
	}
	renames := map[string]transpiler.Type{}
	for i := 0; i < recv.Len(); i++ {
		if from, to := recv.At(i).Obj().Name(), declared.At(i).Obj().Name(); from != to {
			renames[from] = transpiler.BasicType{Name: to}
		}
	}
	if len(renames) == 0 {
		return result
	}
	for i, tp := range result.TypeParams {
		if to, ok := renames[tp]; ok {
			result.TypeParams[i] = to.String()
		}
	}
	for i := range result.Params {
		result.Params[i].Type = transpiler.SubstituteTypeParams(result.Params[i].Type, renames)
	}
	for i, r := range result.Returns {
		result.Returns[i] = transpiler.SubstituteTypeParams(r, renames)
	}
	if len(result.CoreTypes) > 0 {
		cores := make(map[string]transpiler.Type, len(result.CoreTypes))
		for tp, core := range result.CoreTypes {
			if to, ok := renames[tp]; ok {
				tp = to.String()
			}
			cores[tp] = transpiler.SubstituteTypeParams(core, renames)
		}
		result.CoreTypes = cores
	}
	return result
}

// declaresMethod reports whether fn is one of the methods named declares (not
// one promoted from an embedded field).
func declaresMethod(named *types.Named, fn *types.Func) bool {
	for i := 0; i < named.NumMethods(); i++ {
		if named.Method(i) == fn {
			return true
		}
	}
	return false
}

// spelledGoType is t as GALA writes it in Go: exactly when GALA can spell
// it, so an instance keeps its type arguments and an array its length (a
// lambda for a `func(*Box[int]) error` is a `func(*Box[int]) error`, a
// `Some` of a `*Box[int]` an `Option[*Box[int]]`). A type GALA cannot spell
// exactly (an unexported name, a package's alias of a named type), or that holds an
// anonymous struct or a non-empty interface literal (which GALA does not
// write; the lossy spelling keeps them as before), keeps the lossy spelling,
// which still reads its elements and results. It records the types values
// are written with: package variables, struct fields, and function
// parameters and results. A defined type's underlying type keeps type
// literals too (definedUnderlying).
func spelledGoType(t types.Type) transpiler.Type { return spelledGoTypeIn(t, nil) }

// ownPackage is pkg when it is the package being compiled (own), whose Go
// code GALA writes in, and nil otherwise.
func ownPackage(pkg *types.Package, own bool) *types.Package {
	if own {
		return pkg
	}
	return nil
}

// spelledGoTypeIn is spelledGoType for Go written in package home (see
// spellsExactlyIn): the package's own unexported types spell exactly there.
func spelledGoTypeIn(t types.Type, home *types.Package) transpiler.Type {
	if spellsExactlyIn(t, home) && !holdsTypeLiteral(t) {
		return convertGoType(t, true)
	}
	return goTypeToTranspilerType(t)
}

// definedUnderlying is the underlying type of a Go defined type other than a
// struct or an interface, as its record keeps it: exactly whenever GALA can
// spell it, type literals included (a lambda for `type WithBox[T any]
// func(*Box[T]) error` is a `func(*Box[int]) error`), else the lossy
// spelling.
func definedUnderlying(under types.Type, home *types.Package) transpiler.Type {
	if spellsExactlyIn(under, home) {
		return convertGoType(under, true)
	}
	return withArrayLengths(under, goTypeToTranspilerType(under))
}

// withArrayLengths is lossy, the lossy spelling of t, with the lengths of the
// arrays t is made of (`[3][3]os.FileMode`) put back: an array is one
// whatever its element, so it is never taken for a slice nil can be.
func withArrayLengths(t types.Type, lossy transpiler.Type) transpiler.Type {
	arr, isArr := types.Unalias(t).(*types.Array)
	a, ok := lossy.(transpiler.ArrayType)
	if !isArr || !ok {
		return lossy
	}
	a.Elem = withArrayLengths(arr.Elem(), a.Elem)
	a.Len, a.Fixed = arr.Len(), true
	return a
}

// holdsTypeLiteral reports whether t holds an anonymous struct or a
// non-empty interface literal, anywhere inside it.
func holdsTypeLiteral(t types.Type) bool {
	switch t := t.(type) {
	case *types.Struct:
		return true
	case *types.Interface:
		return !t.Empty()
	case *types.Named:
		for i := 0; i < t.TypeArgs().Len(); i++ {
			if holdsTypeLiteral(t.TypeArgs().At(i)) {
				return true
			}
		}
		return false
	case *types.Alias:
		return holdsTypeLiteral(types.Unalias(t))
	case *types.Pointer:
		return holdsTypeLiteral(t.Elem())
	case *types.Slice:
		return holdsTypeLiteral(t.Elem())
	case *types.Array:
		return holdsTypeLiteral(t.Elem())
	case *types.Chan:
		return holdsTypeLiteral(t.Elem())
	case *types.Map:
		return holdsTypeLiteral(t.Key()) || holdsTypeLiteral(t.Elem())
	case *types.Signature:
		return holdsTypeLiteral(t.Params()) || holdsTypeLiteral(t.Results())
	case *types.Tuple:
		for i := 0; i < t.Len(); i++ {
			if holdsTypeLiteral(t.At(i).Type()) {
				return true
			}
		}
	}
	return false
}

// inexactGoType reports whether goTypeToTranspilerType may lose part of t: a
// channel, an anonymous struct, a non-empty interface literal, or an instance
// of a generic type, anywhere inside t. It is conservative for the instances
// whose type arguments convertGoType keeps (keepsTypeArgs).
func inexactGoType(t types.Type) bool {
	switch t := t.(type) {
	case *types.Basic, *types.TypeParam:
		return false
	case *types.Named:
		return t.TypeArgs().Len() > 0
	case *types.Alias:
		return inexactGoType(types.Unalias(t))
	case *types.Pointer:
		return inexactGoType(t.Elem())
	case *types.Slice:
		return inexactGoType(t.Elem())
	case *types.Array:
		return inexactGoType(t.Elem())
	case *types.Map:
		return inexactGoType(t.Key()) || inexactGoType(t.Elem())
	case *types.Signature:
		return inexactGoType(t.Params()) || inexactGoType(t.Results())
	case *types.Tuple:
		for i := 0; i < t.Len(); i++ {
			if inexactGoType(t.At(i).Type()) {
				return true
			}
		}
		return false
	case *types.Interface:
		return !t.Empty()
	}
	return true
}

// convertSignature converts a types.Signature to a transpiler.GoFuncSignature.
func convertSignature(sig *types.Signature) *transpiler.GoFuncSignature {
	return convertSignatureIn(sig, nil)
}

// convertSignatureIn is convertSignature for a function of package home,
// whose own unexported types its parameters and results spell exactly (nil:
// any other package).
func convertSignatureIn(sig *types.Signature, home *types.Package) *transpiler.GoFuncSignature {
	result := &transpiler.GoFuncSignature{
		IsVariadic: sig.Variadic(),
		Inexact:    lossyTuple(sig.Params(), home) || lossyTuple(sig.Results(), home),
	}

	// Record the declared type-parameter names so a call site can instantiate
	// the signature. Both the function's own type params and a generic
	// receiver's are in scope inside the signature, so both are collected.
	for _, tps := range []*types.TypeParamList{sig.TypeParams(), sig.RecvTypeParams()} {
		for i := 0; tps != nil && i < tps.Len(); i++ {
			tp := tps.At(i)
			result.TypeParams = append(result.TypeParams, tp.Obj().Name())
			if core := constraintCoreType(tp); core != nil {
				if result.CoreTypes == nil {
					result.CoreTypes = make(map[string]transpiler.Type)
				}
				result.CoreTypes[tp.Obj().Name()] = spelledGoTypeIn(core, home)
			}
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
			Type: spelledGoTypeIn(paramType, home),
		})
	}

	// Convert return types
	results := sig.Results()
	for i := 0; i < results.Len(); i++ {
		result.Returns = append(result.Returns, spelledGoTypeIn(results.At(i).Type(), home))
	}

	return result
}

// mentionsTypeParam reports whether t names a type parameter anywhere
// inside it.
func mentionsTypeParam(t types.Type) bool {
	switch t := t.(type) {
	case *types.TypeParam:
		return true
	case *types.Named:
		for i := 0; i < t.TypeArgs().Len(); i++ {
			if mentionsTypeParam(t.TypeArgs().At(i)) {
				return true
			}
		}
	case *types.Alias:
		return mentionsTypeParam(types.Unalias(t))
	case *types.Pointer:
		return mentionsTypeParam(t.Elem())
	case *types.Slice:
		return mentionsTypeParam(t.Elem())
	case *types.Array:
		return mentionsTypeParam(t.Elem())
	case *types.Chan:
		return mentionsTypeParam(t.Elem())
	case *types.Map:
		return mentionsTypeParam(t.Key()) || mentionsTypeParam(t.Elem())
	case *types.Signature:
		return mentionsTypeParam(t.Params()) || mentionsTypeParam(t.Results())
	case *types.Tuple:
		for i := 0; i < t.Len(); i++ {
			if mentionsTypeParam(t.At(i).Type()) {
				return true
			}
		}
	case *types.Struct:
		for i := 0; i < t.NumFields(); i++ {
			if mentionsTypeParam(t.Field(i).Type()) {
				return true
			}
		}
	case *types.Interface:
		for i := 0; i < t.NumMethods(); i++ {
			if mentionsTypeParam(t.Method(i).Type()) {
				return true
			}
		}
	}
	return false
}

// lossyTuple reports whether a parameter or result of t, a signature of a
// function of package home, is recorded in a spelling that may lose part of
// it: one spelledGoTypeIn leaves lossy that the lossy conversion does not
// keep whole (inexactGoType).
func lossyTuple(t *types.Tuple, home *types.Package) bool {
	for i := 0; i < t.Len(); i++ {
		if pt := t.At(i).Type(); (!spellsExactlyIn(pt, home) || holdsTypeLiteral(pt)) && inexactGoType(pt) {
			return true
		}
	}
	return false
}

// keepsTypeArgs reports whether convertGoType keeps the type arguments of
// t, an instance of a generic type, outside exact mode: an instance of a
// function, slice, map or channel type whose type arguments that mode
// converts without loss (no array, generic instance, channel, anonymous
// struct or non-empty interface in them), so each is typed as it is
// anywhere else. In the lossy spelling a struct's or an interface's stays the
// bare type name its fields and methods are looked up by; the exact spelling
// (spelledGoType) keeps its type arguments.
func keepsTypeArgs(t *types.Named) bool {
	switch t.Underlying().(type) {
	case *types.Signature, *types.Slice, *types.Map, *types.Chan:
	default:
		return false
	}
	for i := 0; i < t.TypeArgs().Len(); i++ {
		if arg := t.TypeArgs().At(i); inexactGoType(arg) || hasArrayType(arg) {
			return false
		}
	}
	return true
}

// hasArrayType reports whether t has a fixed-length array anywhere inside,
// which convertGoType outside exact mode types as a slice.
func hasArrayType(t types.Type) bool {
	switch t := t.(type) {
	case *types.Array:
		return true
	case *types.Alias:
		return hasArrayType(types.Unalias(t))
	case *types.Pointer:
		return hasArrayType(t.Elem())
	case *types.Slice:
		return hasArrayType(t.Elem())
	case *types.Map:
		return hasArrayType(t.Key()) || hasArrayType(t.Elem())
	case *types.Signature:
		return hasArrayType(t.Params()) || hasArrayType(t.Results())
	case *types.Tuple:
		for i := 0; i < t.Len(); i++ {
			if hasArrayType(t.At(i).Type()) {
				return true
			}
		}
	}
	return false
}

// constraintCoreType is the core type of tp's constraint (`~[]E` and `[]E`
// give []E; see interfaceCoreType), or nil. Only a composite core type is
// kept: it is the one that names other type parameters and so can bind them.
func constraintCoreType(tp *types.TypeParam) types.Type {
	iface, ok := tp.Constraint().Underlying().(*types.Interface)
	if !ok {
		return nil
	}
	switch core := interfaceCoreType(iface).(type) {
	case *types.Slice, *types.Map, *types.Pointer, *types.Chan, *types.Signature, *types.Array:
		return core
	}
	return nil
}

// goTypeToTranspilerType converts a go/types.Type to a transpiler.Type.
// This is the core bridge between Go's type system and GALA's.
// It handles type aliases by resolving them to their underlying type.
func goTypeToTranspilerType(t types.Type) transpiler.Type { return convertGoType(t, false) }

// convertGoType is goTypeToTranspilerType. When exact, as for an anonymous
// struct's field, t is typed as Go has it: a fixed-length array keeps its
// length, a generic type instance its type arguments and an inline interface
// its methods. Elsewhere those stay as they have been, a slice and `any`, and
// a generic type instance keeps its type arguments only when spellsExactly
// and keepsTypeArgs hold for it (the bare type name otherwise).
func convertGoType(t types.Type, exact bool) transpiler.Type {
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
			return convertGoType(types.Unalias(t), exact)
		}
		named := transpiler.NamedType{
			Package:    pkg.Name(),
			Name:       obj.Name(),
			ImportPath: pkg.Path(),
		}
		if t.TypeArgs().Len() == 0 {
			return named
		}
		// An instance of a generic function, slice, map or channel type
		// keeps its type arguments wherever they spell exactly
		// (iter.Seq2[string, int]), so ranging over it or unifying with it
		// binds them. Any other instance (a struct or an interface, whose
		// fields and methods are looked up by its bare name) is the bare
		// type name.
		if !exact && (!spellsExactly(t) || !keepsTypeArgs(t)) {
			return named
		}
		args := make([]transpiler.Type, t.TypeArgs().Len())
		for i := range args {
			args[i] = convertGoType(t.TypeArgs().At(i), exact)
		}
		return transpiler.GenericType{Base: named, Params: args}

	case *types.Alias:
		// Go 1.22+ explicit alias type — resolve to the underlying aliased type
		return convertGoType(types.Unalias(t), exact)

	case *types.Pointer:
		return transpiler.PointerType{Elem: convertGoType(t.Elem(), exact)}

	case *types.Slice:
		return transpiler.ArrayType{Elem: convertGoType(t.Elem(), exact)}

	case *types.Array:
		arr := transpiler.ArrayType{Elem: convertGoType(t.Elem(), exact)}
		if exact {
			arr.Len, arr.Fixed = t.Len(), true
		}
		return arr

	case *types.Map:
		return transpiler.MapType{Key: convertGoType(t.Key(), exact), Elem: convertGoType(t.Elem(), exact)}

	case *types.Chan:
		dir := transpiler.ChanBoth
		switch t.Dir() {
		case types.RecvOnly:
			dir = transpiler.ChanRecv
		case types.SendOnly:
			dir = transpiler.ChanSend
		}
		return transpiler.ChanType{Elem: convertGoType(t.Elem(), exact), Dir: dir}

	case *types.Signature:
		result := transpiler.FuncType{Variadic: t.Variadic()}
		params := t.Params()
		for i := 0; i < params.Len(); i++ {
			pt := params.At(i).Type()
			// A variadic `...T` is recorded element-wise, as a declaration's is.
			if s, ok := pt.(*types.Slice); ok && result.Variadic && i == params.Len()-1 {
				pt = s.Elem()
			}
			result.Params = append(result.Params, convertGoType(pt, exact))
		}
		results := t.Results()
		for i := 0; i < results.Len(); i++ {
			result.Results = append(result.Results, convertGoType(results.At(i).Type(), exact))
		}
		return result

	case *types.Interface:
		if !exact || t.Empty() {
			return transpiler.BasicType{Name: "any"}
		}
		// The method set, embedded interfaces' methods included, sorted as
		// go/types keeps it: the same type as the interface as written.
		var it transpiler.InterfaceType
		for i := 0; i < t.NumMethods(); i++ {
			m := t.Method(i)
			ft, _ := convertGoType(m.Type(), exact).(transpiler.FuncType)
			it.Methods = append(it.Methods, transpiler.InterfaceMethod{Name: m.Name(), Type: ft})
		}
		return it

	case *types.Struct:
		// An anonymous struct, such as a signal channel's `struct{}`, keeps
		// its fields, so a value of it can be typed and its fields read. GALA
		// never spells it in generated Go: wherever it would have to, the
		// transformer reports GALA-E0090. So a struct with an unexported
		// field, or a field Go spells in a way the type model does not
		// (unsafe.Pointer, a variadic function), is typed too instead of
		// being erased to any. A field's type is exact (an array keeps its
		// length) when the model spells it as Go has it; otherwise it is
		// typed as the same type is outside a struct, so reading the field
		// never yields a type that names a different Go type.
		var st transpiler.StructType
		for i := 0; i < t.NumFields(); i++ {
			f := t.Field(i)
			st.Fields = append(st.Fields, transpiler.StructField{
				Name:     f.Name(),
				Type:     convertGoType(f.Type(), spellsExactly(f.Type())),
				Embedded: f.Embedded(),
				Tag:      t.Tag(i),
			})
		}
		return st

	case *types.TypeParam:
		// Generic type parameter
		return transpiler.BasicType{Name: t.Obj().Name()}

	default:
		return transpiler.NilType{}
	}
}

// aliasSpellsExactly is spellsExactly of an alias obj of target. An alias is
// spelled as the type it names. A package's alias of a named type may name
// one another package cannot write (an internal package's type), so it has
// no exact form; an unnamed target (`type Key = [32]byte`, or the universe's
// `any`) is written out as Go has it.
func aliasSpellsExactly(obj *types.TypeName, target types.Type, home *types.Package) bool {
	if _, named := target.(*types.Named); named && obj.Pkg() != nil {
		return false
	}
	return spellsExactlyIn(target, home)
}

// spellsExactly reports whether convertGoType, exact, types t as Go has it,
// so that the type names the same Go type: every array length, type
// argument and interface method is kept. A name only t's own package can
// write (an unexported field, method or type), a package's alias of a named
// type and unsafe.Pointer have no exact form in the type model.
func spellsExactly(t types.Type) bool { return spellsExactlyIn(t, nil) }

// spellsExactlyIn is spellsExactly for Go written in package home, which
// spells its own unexported names (nil: any other package).
func spellsExactlyIn(t types.Type, home *types.Package) bool {
	switch t := t.(type) {
	case *types.Basic:
		return t.Kind() != types.Invalid && t.Kind() != types.UnsafePointer && t.Info()&types.IsUntyped == 0
	case *types.TypeParam:
		return true
	case *types.Alias:
		return aliasSpellsExactly(t.Obj(), types.Unalias(t), home)
	case *types.Named:
		obj := t.Obj()
		if obj.IsAlias() {
			return aliasSpellsExactly(obj, types.Unalias(t), home)
		}
		if obj.Pkg() != nil && !obj.Exported() && obj.Pkg() != home {
			return false
		}
		for i := 0; i < t.TypeArgs().Len(); i++ {
			if !spellsExactlyIn(t.TypeArgs().At(i), home) {
				return false
			}
		}
		return true
	case *types.Pointer:
		return spellsExactlyIn(t.Elem(), home)
	case *types.Slice:
		return spellsExactlyIn(t.Elem(), home)
	case *types.Array:
		return spellsExactlyIn(t.Elem(), home)
	case *types.Map:
		return spellsExactlyIn(t.Key(), home) && spellsExactlyIn(t.Elem(), home)
	case *types.Chan:
		return spellsExactlyIn(t.Elem(), home)
	case *types.Signature:
		return spellsExactlyIn(t.Params(), home) && spellsExactlyIn(t.Results(), home)
	case *types.Tuple:
		for i := 0; i < t.Len(); i++ {
			if !spellsExactlyIn(t.At(i).Type(), home) {
				return false
			}
		}
		return true
	case *types.Interface:
		if !t.IsMethodSet() {
			return false
		}
		for i := 0; i < t.NumMethods(); i++ {
			if m := t.Method(i); !m.Exported() && m.Pkg() != home || !spellsExactlyIn(m.Type(), home) {
				return false
			}
		}
		return true
	case *types.Struct:
		for i := 0; i < t.NumFields(); i++ {
			if f := t.Field(i); !f.Exported() && f.Pkg() != home || !spellsExactlyIn(f.Type(), home) {
				return false
			}
		}
		return true
	}
	return false
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

// literalTypeExpr returns the type a composite literal value states, `T` of
// `T{...}` or `*T` of `&T{...}`, or nil for any other value.
func literalTypeExpr(v ast.Expr) ast.Expr {
	switch e := v.(type) {
	case *ast.CompositeLit:
		return e.Type
	case *ast.UnaryExpr:
		if lit, ok := e.X.(*ast.CompositeLit); ok && e.Op == token.AND && lit.Type != nil {
			return &ast.StarExpr{X: lit.Type}
		}
	}
	return nil
}
