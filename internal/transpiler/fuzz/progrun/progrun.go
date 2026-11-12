// Package progrun transpiles GALA programs, then builds and runs the Go with
// the real toolchain. The fuzz targets in internal/transpiler/fuzz and the
// formal semantics' differential harness (internal/formal/difftest) share it.
//
// Every function takes the test it runs for: a broken setup — no repository
// root, no Go SDK under Bazel — fails that test.
package progrun

import (
	"bytes"
	"context"
	"errors"
	"go/importer"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bazelbuild/rules_go/go/tools/bazel"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/gooracle"
	"martianoff/gala/internal/transpiler/transformer"
)

// transpileTimeout bounds a single transpile. Inputs are small; anything that
// takes this long is treated as a hang. It is generous because the seed pass
// also runs on slow shared CI machines.
const transpileTimeout = 60 * time.Second

// runTimeout bounds building and running one program.
const runTimeout = 5 * time.Minute

var (
	rootOnce sync.Once
	rootDir  string
)

// RepoRoot returns the directory that holds std/ (and examples/): the Bazel
// runfiles root under `bazel test`, the module root under `go test`.
func RepoRoot(tb testing.TB) string {
	tb.Helper()
	rootOnce.Do(func() {
		if p, err := bazel.Runfile("std/option.gala"); err == nil {
			rootDir = filepath.Dir(filepath.Dir(p))
			return
		}
		dir, err := os.Getwd()
		if err != nil {
			return
		}
		for {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				rootDir = dir
				return
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				return
			}
			dir = parent
		}
	})
	if rootDir == "" {
		tb.Fatal("cannot locate the repository root (std/ is not staged)")
	}
	return rootDir
}

// newTranspiler builds a fresh pipeline for every input so that no state can
// leak from one input into the next — the analyzer's package cache is global
// and warm after the first call, so this costs little. It resolves packages
// in the repository and in GeneratedRoot, where generated libraries live.
func newTranspiler(tb testing.TB) *transpiler.GalaToGoTranspiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, []string{RepoRoot(tb), GeneratedRoot(tb)})
	return transpiler.NewGalaToGoTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())
}

var (
	generatedOnce sync.Once
	generatedDir  string
	generatedErr  error
)

// GeneratedRoot is the directory generated library packages are written
// under, as <root>/<import path>; one per process.
func GeneratedRoot(tb testing.TB) string {
	tb.Helper()
	generatedOnce.Do(func() {
		generatedDir, generatedErr = os.MkdirTemp("", "gala-fuzz-generated-")
	})
	if generatedErr != nil {
		tb.Fatal(generatedErr)
	}
	return generatedDir
}

// WriteLibrary writes src as the library package importPath under
// GeneratedRoot. Callers name a library by the hash of its source, so an
// existing file is this library already.
func WriteLibrary(tb testing.TB, importPath, src string) {
	tb.Helper()
	dir := filepath.Join(GeneratedRoot(tb), filepath.FromSlash(importPath))
	file := filepath.Join(dir, "lib.gala")
	if _, err := os.Stat(file); err == nil {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		tb.Fatal(err)
	}
}

var (
	importerOnce sync.Once
	oracleImp    *gooracle.Importer
)

// OracleImporter returns the importer that type-checks generated Go: the Go
// standard library from the SDK, GALA packages — the repository's and the
// generated ones — transpiled from source. Without a Go SDK the check is
// skipped, except under Bazel, which always provides one: there a missing SDK
// is a broken setup that would otherwise pass without checking anything.
func OracleImporter(tb testing.TB) *gooracle.Importer {
	tb.Helper()
	importerOnce.Do(func() {
		if !analyzer.GoImporterAvailable() {
			return
		}
		oracleImp = gooracle.NewImporter(gooracle.ImporterConfig{
			Go:         importer.ForCompiler(token.NewFileSet(), "source", nil),
			ModulePath: "martianoff/gala",
			Root:       RepoRoot(tb),
			ExtraRoots: []string{GeneratedRoot(tb)},
			Transpile:  newTranspiler(tb).Transpile,
		})
	})
	if oracleImp == nil {
		requireUnderBazel(tb, "no Go SDK: generated Go cannot be type-checked")
	}
	return oracleImp
}

// requireUnderBazel skips the test with reason, or fails it under Bazel.
func requireUnderBazel(tb testing.TB, reason string) {
	tb.Helper()
	if os.Getenv("TEST_SRCDIR") != "" {
		tb.Fatal(reason)
	}
	tb.Skip(reason)
}

// Outcome is the result of one bounded transpile.
type Outcome struct {
	Go    string
	Err   error
	Panic any
	Stack string
}

// Transpile runs the full pipeline on src, named file, converting a panic
// into a reported value and a run past transpileTimeout into a test failure.
func Transpile(tb testing.TB, src, file string) Outcome {
	tb.Helper()
	tr := newTranspiler(tb)
	done := make(chan Outcome, 1)
	go func() {
		var res Outcome
		defer func() {
			if r := recover(); r != nil {
				res.Panic = r
				res.Stack = string(debug.Stack())
			}
			done <- res
		}()
		res.Go, res.Err = tr.Transpile(src, file)
	}()
	select {
	case res := <-done:
		return res
	case <-time.After(transpileTimeout):
		tb.Fatalf("transpile did not finish within %s (hang) on input:\n%q", transpileTimeout, src)
		return Outcome{}
	}
}

// Result is what a built program did when it ran.
type Result struct {
	Stdout, Stderr string
	// ExitCode is the process's exit status; -1 when it did not exit
	// normally (killed, or past runTimeout).
	ExitCode int
	// TimedOut is true when the run was stopped at runTimeout.
	TimedOut bool
}

// Error is a failure to build or start a program, as opposed to the program
// itself failing (which is a Result).
type Error struct {
	Step string
	Err  error
	Out  string
}

func (e *Error) Error() string { return e.Step + ": " + e.Err.Error() + "\n" + e.Out }

// goRunner builds and runs generated programs in one module directory per
// process: the GALA packages a program imports are written there once, and
// each program replaces only its main package.
type goRunner struct {
	mu      sync.Mutex // one program at a time: they share main.go
	goBin   string
	dir     string
	env     []string
	written map[string]bool // the GALA packages already in dir
}

var (
	runnerOnce sync.Once
	runner     *goRunner
	runnerErr  error
)

// programRunner returns the process's goRunner. Without a go binary the
// run is skipped, except under Bazel (see requireUnderBazel).
func programRunner(tb testing.TB) *goRunner {
	tb.Helper()
	runnerOnce.Do(func() {
		goroot := analyzer.GoSDKRoot()
		goBin := filepath.Join(goroot, "bin", "go")
		if runtime.GOOS == "windows" {
			goBin += ".exe"
		}
		if _, err := os.Stat(goBin); err != nil {
			return
		}
		r := &goRunner{goBin: goBin, written: map[string]bool{}}
		if r.dir, runnerErr = os.MkdirTemp("", "gala-fuzz-programs-"); runnerErr != nil {
			return
		}
		home := filepath.Join(r.dir, ".home")
		// One build cache for every process: the fuzzer's workers share it,
		// so std is compiled once. The Go build cache is safe for concurrent
		// use.
		cache := filepath.Join(os.TempDir(), "gala-fuzz-gocache")
		r.env = append(os.Environ(),
			"GOROOT="+goroot,
			"GOCACHE="+cache,
			"GOPATH="+filepath.Join(home, "gopath"),
			"GOMODCACHE="+filepath.Join(home, "gomodcache"),
			"HOME="+home,
			"GOENV=off", // no user `go env -w` settings
			"GOFLAGS=",
			"GOTOOLCHAIN=local",
			"GOPROXY=off",
			"GOWORK=off",
			"CGO_ENABLED=0",
		)
		version := "1.25"
		if raw, err := os.ReadFile(filepath.Join(goroot, "VERSION")); err == nil {
			v := strings.TrimPrefix(strings.SplitN(string(raw), "\n", 2)[0], "go")
			if parts := strings.Split(v, "."); len(parts) >= 2 {
				version = parts[0] + "." + parts[1]
			}
		}
		runnerErr = os.WriteFile(filepath.Join(r.dir, "go.mod"), []byte("module martianoff/gala\n\ngo "+version+"\n"), 0o644)
		runner = r
	})
	if runnerErr != nil {
		tb.Fatal(runnerErr)
	}
	if runner == nil {
		requireUnderBazel(tb, "no go binary in the Go SDK: generated programs cannot be run")
	}
	return runner
}

// Run builds mainGo, with the Go of every GALA package it imports, runs it
// and reports what it did. imp must have type-checked mainGo already: that
// loads the GALA packages it imports, whose Go Run writes out. The error is
// for a program that could not be built or started; a program that panics or
// exits non-zero is a Result.
func Run(tb testing.TB, imp *gooracle.Importer, mainGo string) (Result, error) {
	tb.Helper()
	r := programRunner(tb)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.writePackages(imp, mainGo); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(filepath.Join(r.dir, "main.go"), []byte(mainGo), 0o644); err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	bin := filepath.Join(r.dir, "prog")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.CommandContext(ctx, r.goBin, "build", "-ldflags=-s -w", "-o", bin, ".")
	build.Dir, build.Env = r.dir, r.env
	if out, err := build.CombinedOutput(); err != nil {
		return Result{}, &Error{"go build", err, string(out)}
	}
	var stdout, stderr bytes.Buffer
	run := exec.CommandContext(ctx, bin)
	run.Dir, run.Env, run.Stdout, run.Stderr = r.dir, r.env, &stdout, &stderr
	err := run.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String(), TimedOut: ctx.Err() != nil}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		res.ExitCode = exit.ExitCode()
	default:
		return res, &Error{"running it", err, stderr.String()}
	}
	return res, nil
}

// RunOK is Run for a program that must exit normally: it returns the
// program's stdout, and an error when it could not be built or failed.
func RunOK(tb testing.TB, imp *gooracle.Importer, mainGo string) (string, error) {
	tb.Helper()
	res, err := Run(tb, imp, mainGo)
	if err != nil {
		return res.Stdout, err
	}
	if res.ExitCode != 0 || res.TimedOut {
		return res.Stdout, &Error{"running it", errors.New("exit status " + strconv.Itoa(res.ExitCode)), res.Stderr}
	}
	return res.Stdout, nil
}

// writePackages writes every GALA package mainGo imports, directly or not,
// that the module directory does not have yet, from the sources imp
// generated for them. A generated library's path is the hash of its source,
// so a package once written never changes.
func (r *goRunner) writePackages(imp *gooracle.Importer, mainGo string) error {
	queue := importsOf(mainGo)
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		if r.written[path] || !strings.HasPrefix(path, "martianoff/gala/") {
			continue
		}
		sources, ok := imp.Sources(path)
		if !ok {
			return &Error{"writing the module", os.ErrNotExist, "no generated Go for " + path}
		}
		pkgDir := filepath.Join(r.dir, filepath.FromSlash(strings.TrimPrefix(path, "martianoff/gala/")))
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			return err
		}
		for name, src := range sources {
			if err := os.WriteFile(filepath.Join(pkgDir, name), []byte(src), 0o644); err != nil {
				return err
			}
			queue = append(queue, importsOf(src)...)
		}
		r.written[path] = true
	}
	return nil
}

// importsOf lists the import paths of a Go file.
func importsOf(src string) []string {
	f, err := parser.ParseFile(token.NewFileSet(), "", src, parser.ImportsOnly)
	if err != nil {
		return nil
	}
	var out []string
	for _, spec := range f.Imports {
		if p, err := strconv.Unquote(spec.Path.Value); err == nil {
			out = append(out, p)
		}
	}
	return out
}
