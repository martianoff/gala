// Package doctor checks the environment gala runs in — the Go toolchain, the
// Go SDK the analyzer reads types from, gala's caches, and the project in the
// working directory — and reports each finding with a fix. It backs
// `gala doctor`.
//
// Everything a check reads from outside the process (PATH lookups, `go env`,
// directory sizes, the platform) comes in through Env, so the checks run in
// tests against a fake environment.
package doctor

import (
	"errors"
	"fmt"
	"go/version"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/mod/modfile"

	"martianoff/gala/internal/build"
	"martianoff/gala/internal/depman/mod"
)

// Status is the outcome of one check.
type Status string

// The outcomes, from best to worst. Any Fail makes `gala doctor` exit non-zero.
const (
	OK   Status = "OK"
	Warn Status = "WARN"
	Fail Status = "FAIL"
)

// Check is one line of the report.
type Check struct {
	// Name says what was checked ("go", "stdlib cache", ...).
	Name   string `json:"name"`
	Status Status `json:"status"`
	// Message is what the check found.
	Message string `json:"message"`
	// Fix is the one-line remedy; set for Warn and Fail only.
	Fix string `json:"fix,omitempty"`
	// DocsURL is the section of the troubleshooting page that explains the
	// problem; set for Warn and Fail only.
	DocsURL string `json:"docsUrl,omitempty"`
}

// Report is every check, in the order they ran.
type Report struct {
	Checks []Check `json:"checks"`
}

// Failed reports whether any check failed.
func (r Report) Failed() bool {
	for _, c := range r.Checks {
		if c.Status == Fail {
			return true
		}
	}
	return false
}

// TroubleshootingURL is the published troubleshooting page. Each anchor below
// is the id of one of its section headings, which docs/TROUBLESHOOTING.MD and
// its website mirror must keep.
const TroubleshootingURL = "https://gala.fyi/docs/troubleshooting/"

const (
	anchorGalaOnPath   = "gala-on-path"
	anchorGoSDK        = "missing-go-sdk"
	anchorStdlibCache  = "stale-stdlib-cache"
	anchorGoBuildCache = "go-build-cache-growth"
	anchorModDrift     = "galamod-and-gomod-drift"
	anchorWindows      = "windows-notes"
	anchorGalaMod      = "galamod-does-not-parse"
)

// Anchors lists every troubleshooting-page anchor a check links to, so a test
// can verify that the page still has each heading.
func Anchors() []string {
	return []string{anchorGalaOnPath, anchorGoSDK, anchorStdlibCache, anchorGoBuildCache, anchorModDrift, anchorWindows, anchorGalaMod}
}

func docsURL(anchor string) string { return TroubleshootingURL + "#" + anchor }

// DefaultGoCacheWarnBytes is the Go build cache size above which doctor warns.
// The cache has no size limit of its own and only trims entries unused for
// five days, so heavy building can take it to hundreds of gigabytes.
const DefaultGoCacheWarnBytes int64 = 20 << 30

// Env is everything the checks read from outside the process.
type Env struct {
	// Version is the running gala's version.
	Version string
	// Executable is the path of the running gala binary, "" when unknown.
	Executable string
	// LookPath finds an executable on PATH (exec.LookPath).
	LookPath func(name string) (string, error)
	// GoEnv runs `<goBin> env <keys...>` and returns one value per key.
	GoEnv func(goBin string, keys ...string) ([]string, error)
	// GoSDKRoot is the Go SDK the analyzer reads Go types from, "" when it
	// finds none (analyzer.GoSDKRoot).
	GoSDKRoot func() string
	// SameFile reports whether two paths name the same file or directory.
	SameFile func(a, b string) bool
	// GalaVersion returns the version the gala binary at path reports.
	GalaVersion func(path string) (string, error)
	// DirSize returns the total size of the files under dir.
	DirSize func(dir string) (int64, error)
	// Config locates gala's caches.
	Config *build.Config
	// ProjectDir is the directory holding the project's gala.mod, "" when the
	// working directory is not inside a project.
	ProjectDir string
	// LongPathsEnabled reports whether Windows long path support is on. Nil
	// on other platforms, where the check does not apply.
	LongPathsEnabled func() (enabled bool, err error)
	// GoCacheWarnBytes is the Go build cache size above which doctor warns.
	GoCacheWarnBytes int64
}

// Run performs every check against env.
func Run(env Env) Report {
	var r Report
	add := func(c Check) { r.Checks = append(r.Checks, c) }

	add(checkGala(env))
	goEnv, goCheck := checkGo(env)
	add(goCheck)
	add(checkGoroot(env, goEnv))
	add(checkGalaHome(env))
	if c, ok := checkModuleCache(env); ok {
		add(c)
	}
	add(checkStdlibCache(env))
	if goEnv != nil { // otherwise the go check has already failed
		add(checkGoBuildCache(env, goEnv.gocache))
	}
	if env.LongPathsEnabled != nil {
		add(checkLongPaths(env))
	}
	r.Checks = append(r.Checks, checkProject(env)...)
	return r
}

func ok(name, msg string) Check { return Check{Name: name, Status: OK, Message: msg} }

func problem(status Status, name, msg, fix, anchor string) Check {
	return Check{Name: name, Status: status, Message: msg, Fix: fix, DocsURL: docsURL(anchor)}
}

// checkGala reports the running gala, and warns when `gala` on PATH runs some
// other version: an older install shadowing a newer one is a common cause of
// errors that the newer version fixed. A `gala` on PATH that is another file
// but the same version, such as a package manager's shim, is fine.
func checkGala(env Env) Check {
	where := env.Executable
	if where == "" {
		where = "unknown path"
	}
	msg := fmt.Sprintf("gala %s at %s", env.Version, where)
	onPath, err := env.LookPath("gala")
	if err != nil && !errors.Is(err, exec.ErrDot) {
		return problem(Warn, "gala", msg+"; no gala on PATH",
			"add the directory holding gala to PATH", anchorGalaOnPath)
	}
	if env.Executable == "" || env.SameFile(onPath, env.Executable) {
		return ok("gala", msg)
	}
	switch v, err := env.GalaVersion(onPath); {
	case err != nil:
		return problem(Warn, "gala", fmt.Sprintf("%s, but gala on PATH is %s, whose version is unknown: %v", msg, onPath, err),
			"remove the other install, or put this one first on PATH", anchorGalaOnPath)
	case v != env.Version:
		return problem(Warn, "gala", fmt.Sprintf("%s, but gala on PATH is %s, version %s", msg, onPath, v),
			"remove the other install, or put this one first on PATH", anchorGalaOnPath)
	}
	return ok("gala", fmt.Sprintf("%s (on PATH as %s)", msg, onPath))
}

// goEnvValues is the go on PATH and what its `go env` reported.
type goEnvValues struct{ bin, version, goroot, gocache string }

// checkGo finds go on PATH and checks its version against build.MinGoVersion.
// It returns the go environment for the checks that follow, nil when go is
// missing or `go env` failed.
func checkGo(env Env) (*goEnvValues, Check) {
	const name = "go"
	fix := fmt.Sprintf("install Go %s or newer from https://go.dev/dl/ and put it on PATH", build.MinGoVersion)
	goBin, err := env.LookPath("go")
	if err != nil {
		return nil, problem(Fail, name, "go not found on PATH; gala build, run and test need it", fix, anchorGoSDK)
	}
	vals, err := env.GoEnv(goBin, "GOVERSION", "GOROOT", "GOCACHE")
	switch {
	case err != nil:
		return nil, problem(Fail, name, fmt.Sprintf("%s env failed: %v", goBin, err), fix, anchorGoSDK)
	case len(vals) != 3:
		return nil, problem(Fail, name, fmt.Sprintf("%s env printed %d values for 3 variables", goBin, len(vals)), fix, anchorGoSDK)
	}
	ge := &goEnvValues{bin: goBin, version: vals[0], goroot: vals[1], gocache: vals[2]}
	minimum := "go" + build.MinGoVersion
	switch {
	case !version.IsValid(ge.version):
		return ge, problem(Warn, name, fmt.Sprintf("%s at %s; cannot tell whether it is %s or newer", ge.version, goBin, minimum), fix, anchorGoSDK)
	case version.Compare(ge.version, minimum) < 0:
		return ge, problem(Fail, name, fmt.Sprintf("%s at %s is older than %s, the oldest GALA supports", ge.version, goBin, minimum), fix, anchorGoSDK)
	}
	return ge, ok(name, fmt.Sprintf("%s at %s (%s or newer required)", ge.version, goBin, minimum))
}

// checkGoroot reports the Go SDK the analyzer reads Go package types from,
// resolved exactly as a build resolves it. Without one, Go type inference is
// off and builds fail on whatever needed a Go type. When it is not the SDK of
// the go on PATH, the types GALA infers can disagree with the compiler's.
func checkGoroot(env Env, goEnv *goEnvValues) Check {
	const name = "GOROOT"
	root := env.GoSDKRoot()
	if root == "" {
		return problem(Fail, name, "no Go SDK found; Go type inference is disabled",
			"put go on PATH, or set GOROOT to your Go installation", anchorGoSDK)
	}
	if goEnv != nil && goEnv.goroot != "" && !env.SameFile(root, goEnv.goroot) {
		return problem(Warn, name, fmt.Sprintf("type inference reads %s, but %s builds with %s", root, goEnv.bin, goEnv.goroot),
			"set GOROOT to "+goEnv.goroot, anchorGoSDK)
	}
	return ok(name, root)
}

// checkGalaHome reports where gala keeps its data and how much space it takes.
func checkGalaHome(env Env) Check {
	return ok("gala home", sizedDir(env, env.Config.GalaHome))
}

// checkModuleCache reports the GALA module cache when GALA_CACHE moved it out
// of the gala home; inside the home its size is already counted.
func checkModuleCache(env Env) (Check, bool) {
	if within(env.Config.GalaPkgDir, env.Config.GalaHome) {
		return Check{}, false
	}
	return ok("module cache", sizedDir(env, env.Config.GalaPkgDir)), true
}

// checkStdlibCache warns about stdlib cache directories that other gala
// versions extracted: each upgrade leaves one behind, and nothing reads them.
func checkStdlibCache(env Env) Check {
	const name = "stdlib cache"
	cfg := env.Config
	stale, err := cfg.StaleStdlibDirs(env.Version)
	if err != nil {
		return problem(Warn, name, fmt.Sprintf("cannot read %s: %v", cfg.StdlibDir, err),
			"check the permissions of "+cfg.StdlibDir, anchorStdlibCache)
	}
	current := cfg.StdlibVersionDir(env.Version)
	state := "up to date"
	if !cfg.StdlibCacheFresh(env.Version) {
		state = "extracted on the next build"
	}
	msg := fmt.Sprintf("%s (%s)", current, state)
	if len(stale) == 0 {
		return ok(name, msg)
	}
	var total int64
	for _, dir := range stale {
		n, _ := env.DirSize(dir)
		total += n
	}
	return problem(Warn, name,
		fmt.Sprintf("%s; %d directories from other gala versions use %s", msg, len(stale), formatBytes(total)),
		"gala clean --stale-stdlib", anchorStdlibCache)
}

// checkGoBuildCache warns when the Go build cache, which every gala build
// fills and nothing bounds, has grown past env.GoCacheWarnBytes.
func checkGoBuildCache(env Env, gocache string) Check {
	const name = "go build cache"
	if gocache == "" || gocache == "off" {
		return ok(name, "disabled (GOCACHE="+gocache+")")
	}
	size, err := env.DirSize(gocache)
	if err != nil {
		return ok(name, gocache+" (not created yet)")
	}
	msg := fmt.Sprintf("%s, %s", gocache, formatBytes(size))
	if size > env.GoCacheWarnBytes {
		return problem(Warn, name, fmt.Sprintf("%s, more than %s", msg, formatBytes(env.GoCacheWarnBytes)),
			"go clean -cache", anchorGoBuildCache)
	}
	return ok(name, msg)
}

// checkLongPaths warns when Windows limits paths to 260 characters: build
// workspaces nest dependency module paths deep under the gala home.
func checkLongPaths(env Env) Check {
	const name = "long paths"
	enabled, err := env.LongPathsEnabled()
	switch {
	case err != nil:
		return problem(Warn, name, "cannot read the Windows long path setting: "+err.Error(),
			"enable long paths (LongPathsEnabled) in the registry", anchorWindows)
	case !enabled:
		return problem(Warn, name, "Windows long path support is off; Bazel, git and other tools without their own long path handling can fail on deep paths",
			"enable long paths (LongPathsEnabled) in the registry", anchorWindows)
	}
	return ok(name, "enabled")
}

// checkProject checks the gala.mod of the project in the working directory and,
// when the project also has a go.mod, that the two agree on Go dependencies.
func checkProject(env Env) []Check {
	if env.ProjectDir == "" {
		return []Check{ok("project", "no gala.mod here or above; project checks skipped")}
	}
	modPath := filepath.Join(env.ProjectDir, "gala.mod")
	galaMod, err := mod.ParseFile(modPath)
	if err != nil {
		return []Check{problem(Fail, "gala.mod", fmt.Sprintf("%s: %v", modPath, err),
			"fix the line the error names", anchorGalaMod)}
	}
	checks := []Check{ok("gala.mod", fmt.Sprintf("%s (module %s)", modPath, galaMod.Module.Path))}

	goMod, err := build.ReadGoMod(filepath.Join(env.ProjectDir, "go.mod"))
	if err != nil {
		return append(checks, problem(Warn, "go.mod", err.Error(),
			"fix go.mod so go mod tidy accepts it", anchorModDrift))
	}
	if goMod == nil {
		return checks
	}
	drift := modDrift(galaMod, goMod)
	if len(drift) == 0 {
		return append(checks, ok("go.mod", "Go dependencies match gala.mod"))
	}
	return append(checks, problem(Warn, "go.mod",
		fmt.Sprintf("gala.mod and go.mod disagree on %d Go dependencies: %s", len(drift), summarize(drift, 3)),
		"run gala mod add <path>@<version> --go with the version go.mod requires", anchorModDrift))
}

// modDrift lists the Go dependencies gala.mod and go.mod disagree on. gala
// build takes Go module versions from gala.mod and go build from go.mod, so
// each difference is a dependency the two builds resolve differently. go.mod's
// side is what `gala mod init` would copy from it (build.GoRequirementsOf),
// indirect requirements included and replaced modules and GALA's own packages
// left out; drift is a module that set has at another version or that
// gala.mod lacks, and a Go dependency of gala.mod that go.mod does not require.
func modDrift(galaMod *mod.File, goMod *modfile.File) []string {
	inGala := make(map[string]string)
	for _, r := range galaMod.GoRequires() {
		inGala[r.Path] = r.Version
	}
	var drift []string
	wanted, _ := build.GoRequirementsOf(goMod)
	for _, r := range wanted {
		switch galaVer, found := inGala[r.Path]; {
		case !found:
			drift = append(drift, fmt.Sprintf("%s %s (go.mod only)", r.Path, r.Version))
		case galaVer != r.Version:
			drift = append(drift, fmt.Sprintf("%s (gala.mod %s, go.mod %s)", r.Path, galaVer, r.Version))
		}
	}
	inGo := make(map[string]bool)
	for _, r := range goMod.Require {
		inGo[r.Mod.Path] = true
	}
	for path, ver := range inGala {
		if !inGo[path] {
			drift = append(drift, fmt.Sprintf("%s %s (gala.mod only)", path, ver))
		}
	}
	sort.Strings(drift)
	return drift
}

// summarize joins the first max items and counts the rest.
func summarize(items []string, max int) string {
	if len(items) <= max {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s, and %d more", strings.Join(items[:max], ", "), len(items)-max)
}

// sizedDir describes dir and its size.
func sizedDir(env Env, dir string) string {
	size, err := env.DirSize(dir)
	if err != nil {
		return dir + " (not created yet)"
	}
	return fmt.Sprintf("%s, %s", dir, formatBytes(size))
}

// within reports whether path is dir or lies below it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// formatBytes renders n in binary units.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// DirSize returns the total size of the regular files under dir. Entries it
// cannot read are skipped, so the result is a lower bound on a tree with
// unreadable parts. It fails only when dir itself cannot be read.
//
// A gala home or Go build cache holds hundreds of thousands of files, and
// listing them one directory at a time takes minutes on a cold disk, so
// directories are read concurrently.
func DirSize(dir string) (int64, error) {
	if _, err := os.Stat(dir); err != nil {
		return 0, err
	}
	var (
		total atomic.Int64
		wg    sync.WaitGroup
		// slots bounds the extra goroutines; with none free, a directory is
		// walked by the goroutine that found it.
		slots = make(chan struct{}, 4*runtime.NumCPU())
		walk  func(string)
	)
	walk = func(d string) {
		entries, err := os.ReadDir(d)
		if err != nil {
			return
		}
		for _, e := range entries {
			switch {
			case e.IsDir():
				sub := filepath.Join(d, e.Name())
				select {
				case slots <- struct{}{}:
					wg.Add(1)
					go func() {
						defer func() { <-slots; wg.Done() }()
						walk(sub)
					}()
				default:
					walk(sub)
				}
			case e.Type().IsRegular():
				if info, err := e.Info(); err == nil {
					total.Add(info.Size())
				}
			}
		}
	}
	walk(dir)
	wg.Wait()
	return total.Load(), nil
}
