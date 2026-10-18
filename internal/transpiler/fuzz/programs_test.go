package fuzz_test

import (
	"bytes"
	"context"
	"flag"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"math/rand"
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

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/gooracle"
	"martianoff/gala/internal/transpiler/transformer"
)

// FuzzPrograms generates whole programs, possibly of two packages, together
// with the output each must print (see programs_gen_test.go), and checks the
// strongest property of the targets here: an accepted program's Go
// type-checks, builds and prints exactly what the generator computed; a
// program the generator knows to be invalid is rejected with a positioned
// diagnostic.
//
// The programs cover what has broken most often: pattern matching (variant
// extractors, nested, named and alternative sub-patterns, stable
// identifiers, guards, tuples, generic sealed types, exhaustiveness, partial
// functions) and code that crosses a package boundary — a library imported
// qualified, under an alias or with a dot import, and std used implicitly,
// dot-imported or qualified.
func FuzzPrograms(f *testing.F) {
	rng := rand.New(rand.NewSource(2))
	for range programSeeds {
		seed := make([]byte, 64)
		rng.Read(seed)
		f.Add(seed)
	}
	warmUp(f)
	// Under -fuzz an input that runs for more than ten seconds counts as a
	// hang, and the first program a worker builds pays for transpiling,
	// type-checking and compiling std. Each worker runs one program first.
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
	tr, imp, root := programPipeline(t)
	if p.lib != "" {
		dir := filepath.Join(root, filepath.FromSlash(p.libPath))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// The path is the hash of the source, so an existing file is this
		// library already.
		file := filepath.Join(dir, "lib.gala")
		if _, err := os.Stat(file); err != nil {
			if err := os.WriteFile(file, []byte(p.lib), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	src := p.main
	res := transpileWith(t, tr, src)
	if p.rejected {
		if res.Panic != nil {
			t.Fatalf("transpiler panicked: %v\n%s\ninput:\n%s", res.Panic, res.Stack, describe(p))
		}
		if res.Err == nil {
			t.Fatalf("the program has a match that is not exhaustive and has no default, but was accepted\ninput:\n%s", describe(p))
		}
		checkDiagnostics(t, src, res.Err)
		return
	}
	if res.Panic != nil {
		t.Fatalf("transpiler panicked: %v\n%s\ninput:\n%s", res.Panic, res.Stack, describe(p))
	}
	if res.Err != nil {
		t.Fatalf("the transpiler rejected a valid program: %v\ninput:\n%s", res.Err, describe(p))
	}
	checkGeneratedGo(t, src, res.Go)
	if imp == nil {
		return
	}
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

// describe is the program's source for a failure report.
func describe(p program) string {
	if p.lib == "" {
		return p.main
	}
	return p.main + "\n// ---- " + p.libPath + "/lib.gala\n" + p.lib
}

var (
	pipelineOnce sync.Once
	pipelineTr   *transpiler.GalaToGoTranspiler
	pipelineImp  *gooracle.Importer
	pipelineRoot string
	pipelineErr  error
)

// programPipeline returns the transpiler programs are transpiled with, the
// importer that type-checks them (nil without a Go SDK), and the directory
// generated libraries are written under. The transpiler and the importer
// both look for packages there as well as in the repository.
func programPipeline(tb testing.TB) (*transpiler.GalaToGoTranspiler, *gooracle.Importer, string) {
	tb.Helper()
	pipelineOnce.Do(func() {
		pipelineRoot, pipelineErr = os.MkdirTemp("", "gala-fuzz-programs-")
		if pipelineErr != nil {
			return
		}
		roots := []string{repoRoot(tb), pipelineRoot}
		p := transpiler.NewAntlrGalaParser()
		a := analyzer.NewGalaAnalyzer(p, roots)
		pipelineTr = transpiler.NewGalaToGoTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())
		if analyzer.GoImporterAvailable() {
			pipelineImp = gooracle.NewImporter(gooracle.ImporterConfig{
				Go:         newGoSourceImporter(),
				ModulePath: "martianoff/gala",
				Root:       repoRoot(tb),
				ExtraRoots: []string{pipelineRoot},
				Transpile:  pipelineTr.Transpile,
			})
		}
	})
	if pipelineErr != nil {
		tb.Fatal(pipelineErr)
	}
	return pipelineTr, pipelineImp, pipelineRoot
}

// transpileWith is transpile with a given pipeline: one that also resolves
// the generated libraries.
func transpileWith(tb testing.TB, tr *transpiler.GalaToGoTranspiler, src string) outcome {
	tb.Helper()
	done := make(chan outcome, 1)
	go func() {
		var res outcome
		defer func() {
			if r := recover(); r != nil {
				res.Panic = r
				res.Stack = stack()
			}
			done <- res
		}()
		res.Go, res.Err = tr.Transpile(src, sourceFile)
	}()
	select {
	case res := <-done:
		return res
	case <-time.After(transpileTimeout):
		tb.Fatalf("transpile did not finish within %s (hang) on input:\n%s", transpileTimeout, src)
		return outcome{}
	}
}

var (
	goCacheOnce sync.Once
	goCacheDir  string
)

// runGenerated builds mainGo, with the Go of every GALA package it imports,
// as a module in a fresh directory, runs it and returns what it printed.
func runGenerated(t testing.TB, imp *gooracle.Importer, mainGo string) (string, error) {
	t.Helper()
	goroot := analyzer.GoSDKRoot()
	goBin := filepath.Join(goroot, "bin", "go")
	if _, err := os.Stat(goBin); err != nil {
		if _, err := os.Stat(goBin + ".exe"); err != nil {
			t.Skip("no go binary in the Go SDK: generated programs cannot be run")
		}
	}
	dir := t.TempDir()
	if err := writeModule(imp, dir, mainGo); err != nil {
		return "", err
	}
	goCacheOnce.Do(func() {
		// One build cache for every process: the fuzzer's workers share
		// it, so std is compiled once. The Go build cache is safe for
		// concurrent use.
		goCacheDir = filepath.Join(os.TempDir(), "gala-fuzz-gocache")
		_ = os.MkdirAll(goCacheDir, 0o755)
	})
	home := t.TempDir()
	env := append(os.Environ(),
		"GOROOT="+goroot,
		"GOCACHE="+goCacheDir,
		"GOPATH="+filepath.Join(home, "gopath"),
		"GOMODCACHE="+filepath.Join(home, "gomodcache"),
		"HOME="+home,
		"GOFLAGS=-mod=mod",
		"GOTOOLCHAIN=local",
		"GOPROXY=off",
		"GOWORK=off",
		"CGO_ENABLED=0",
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	bin := filepath.Join(dir, "prog")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.CommandContext(ctx, goBin, "build", "-o", bin, ".")
	build.Dir, build.Env = dir, env
	if out, err := build.CombinedOutput(); err != nil {
		return "", &runError{"go build", err, string(out)}
	}
	var stdout, stderr bytes.Buffer
	run := exec.CommandContext(ctx, bin)
	run.Dir, run.Env, run.Stdout, run.Stderr = dir, env, &stdout, &stderr
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

// writeModule writes mainGo as package main of module martianoff/gala in
// dir, with every GALA package it imports, directly or not, from the
// sources imp generated for them.
func writeModule(imp *gooracle.Importer, dir, mainGo string) error {
	version := "1.25"
	if raw, err := os.ReadFile(filepath.Join(analyzer.GoSDKRoot(), "VERSION")); err == nil {
		v := strings.TrimPrefix(strings.SplitN(string(raw), "\n", 2)[0], "go")
		if parts := strings.Split(v, "."); len(parts) >= 2 {
			version = parts[0] + "." + parts[1]
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module martianoff/gala\n\ngo "+version+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainGo), 0o644); err != nil {
		return err
	}
	seen := map[string]bool{}
	queue := importsOf(mainGo)
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		if seen[path] || !strings.HasPrefix(path, "martianoff/gala/") {
			continue
		}
		seen[path] = true
		sources, ok := imp.Sources(path)
		if !ok {
			return &runError{"writing the module", os.ErrNotExist, "no generated Go for " + path}
		}
		pkgDir := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(path, "martianoff/gala/")))
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			return err
		}
		for name, src := range sources {
			if err := os.WriteFile(filepath.Join(pkgDir, name), []byte(src), 0o644); err != nil {
				return err
			}
			queue = append(queue, importsOf(src)...)
		}
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

// newGoSourceImporter resolves the Go standard library from source, from the
// SDK the analyzer found.
func newGoSourceImporter() types.Importer {
	return importer.ForCompiler(token.NewFileSet(), "source", nil)
}

func stack() string { return string(debug.Stack()) }
