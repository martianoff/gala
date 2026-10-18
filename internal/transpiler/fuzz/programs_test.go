package fuzz_test

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"go/parser"
	"go/token"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/gooracle"
)

// FuzzPrograms generates whole programs, possibly of two packages, together
// with the output each must print (see programs_gen_test.go), and checks the
// strongest property of the targets here: an accepted program's Go
// type-checks, builds and prints exactly what the generator computed, and a
// program the generator knows to be invalid is rejected with the diagnostic
// it must get.
//
// The programs cover what has broken most often: pattern matching (variant
// extractors, nested, named and alternative sub-patterns, stable
// identifiers, guards, tuples, sequences, generic sealed types,
// exhaustiveness, partial functions) and code that crosses a package
// boundary — a library imported qualified, under an alias or with a dot
// import, std used implicitly, dot-imported or qualified, and
// collection_immutable imported each way.
//
// Each program is built with the Go toolchain, so an input takes seconds.
// Fuzz it with few workers (tools/fuzz.sh FuzzPrograms 20m -parallel 2): the
// fuzzer counts an input that runs for more than ten seconds as a hang.
func FuzzPrograms(f *testing.F) {
	rng := rand.New(rand.NewSource(2))
	for range programSeeds {
		seed := make([]byte, 128)
		rng.Read(seed)
		f.Add(seed)
	}
	warmUp(f)
	// The first program a process builds pays for transpiling,
	// type-checking and compiling std; under -fuzz each worker builds one
	// before its inputs are timed.
	if fl := flag.Lookup("test.fuzz"); fl != nil && fl.Value.String() != "" {
		checkProgram(f, generateProgram(nil))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		checkProgram(t, generateProgram(data))
	})
}

// programSeeds is how many generated programs the seed pass builds and runs.
// Each costs a Go build, so it is kept small.
const programSeeds = 40

func checkProgram(t testing.TB, p program) {
	t.Helper()
	if p.lib != "" {
		writeLibrary(t, p)
	}
	res := transpile(t, p.main)
	if res.Panic != nil {
		t.Fatalf("transpiler panicked: %v\n%s\ninput:\n%s", res.Panic, res.Stack, describe(p))
	}
	if p.rejected {
		if res.Err == nil {
			t.Fatalf("a match with no default that is not exhaustive was accepted\ninput:\n%s", describe(p))
		}
		checkDiagnostics(t, p.main, res.Err)
		if !hasCode(res.Err, galaerr.CodeNonExhaustiveMatch) {
			t.Fatalf("a match with no default that is not exhaustive must be %s, got: %v\ninput:\n%s",
				galaerr.CodeNonExhaustiveMatch, res.Err, describe(p))
		}
		return
	}
	if res.Err != nil {
		t.Fatalf("the transpiler rejected a valid program: %v\ninput:\n%s", res.Err, describe(p))
	}
	checkGeneratedGo(t, p.main, res.Go)
	imp := oracleImporter(t)
	if tc := imp.TypeCheck("main.go", res.Go); tc.Skipped != "" || len(tc.Errors) > 0 {
		t.Fatalf("the Go generated for a valid program does not type-check: %s%s\ninput:\n%s\ngenerated:\n%s",
			tc.Skipped, strings.Join(tc.Errors, "\n  "), describe(p), res.Go)
	}
	got, err := runGenerated(t, imp, res.Go)
	if err != nil {
		t.Fatalf("the generated program failed: %v\ninput:\n%s\ngenerated:\n%s", err, describe(p), res.Go)
	}
	if got != p.want {
		t.Fatalf("the program printed\n%s\nbut must print\n%s\ninput:\n%s\ngenerated:\n%s", got, p.want, describe(p), res.Go)
	}
}

// writeLibrary writes the program's library package under generatedRoot.
// Its import path is the hash of its source, so an existing file is this
// library already.
func writeLibrary(tb testing.TB, p program) {
	tb.Helper()
	dir := filepath.Join(generatedRoot(tb), filepath.FromSlash(p.libPath))
	file := filepath.Join(dir, "lib.gala")
	if _, err := os.Stat(file); err == nil {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(p.lib), 0o644); err != nil {
		tb.Fatal(err)
	}
}

// hasCode reports whether one of err's diagnostics has code.
func hasCode(err error, code galaerr.ErrorCode) bool {
	for _, leaf := range leafErrors(err) {
		var se *galaerr.SemanticError
		if errors.As(leaf, &se) && se.Code == code {
			return true
		}
	}
	return false
}

// describe is the program's source for a failure report.
func describe(p program) string {
	if p.lib == "" {
		return p.main
	}
	return p.main + "\n// ---- " + p.libPath + "/lib.gala\n" + p.lib
}

// goRunner builds and runs generated programs in one module directory per
// process: the GALA packages a program imports are written there once, and
// each program replaces only its main package.
type goRunner struct {
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

// runGenerated builds mainGo, with the Go of every GALA package it imports,
// runs it and returns what it printed.
func runGenerated(tb testing.TB, imp *gooracle.Importer, mainGo string) (string, error) {
	tb.Helper()
	r := programRunner(tb)
	if err := r.writePackages(imp, mainGo); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(r.dir, "main.go"), []byte(mainGo), 0o644); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	bin := filepath.Join(r.dir, "prog")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.CommandContext(ctx, r.goBin, "build", "-ldflags=-s -w", "-o", bin, ".")
	build.Dir, build.Env = r.dir, r.env
	if out, err := build.CombinedOutput(); err != nil {
		return "", &runError{"go build", err, string(out)}
	}
	var stdout, stderr bytes.Buffer
	run := exec.CommandContext(ctx, bin)
	run.Dir, run.Env, run.Stdout, run.Stderr = r.dir, r.env, &stdout, &stderr
	if err := run.Run(); err != nil {
		return stdout.String(), &runError{"running it", err, stderr.String()}
	}
	return stdout.String(), nil
}

type runError struct {
	step string
	err  error
	out  string
}

func (e *runError) Error() string { return e.step + ": " + e.err.Error() + "\n" + e.out }

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
			return &runError{"writing the module", os.ErrNotExist, "no generated Go for " + path}
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
