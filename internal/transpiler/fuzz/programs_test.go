package fuzz_test

import (
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler/fuzz/proggen"
	"martianoff/gala/internal/transpiler/fuzz/progrun"
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
		checkProgram(f, proggen.Generate(nil))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		checkProgram(t, proggen.Generate(data))
	})
}

// programSeeds is how many generated programs the seed pass builds and runs.
// Each costs a Go build, so it is kept small.
const programSeeds = 40

func checkProgram(t testing.TB, p proggen.Program) {
	t.Helper()
	if failure := programFailure(t, p); failure != "" {
		t.Fatal(failure)
	}
}

// programFailure runs p and describes how it went wrong: a transpiler
// panic, a wrong verdict, Go that does not type-check, build or run, or the
// wrong output. It is "" when p does what it must.
func programFailure(t testing.TB, p proggen.Program) string {
	t.Helper()
	if p.Lib != "" {
		progrun.WriteLibrary(t, p.LibPath, p.Lib)
	}
	res := transpile(t, p.Main)
	if res.Panic != nil {
		return fmt.Sprintf("transpiler panicked: %v\n%s\ninput:\n%s", res.Panic, res.Stack, describe(p))
	}
	if r := p.Rejection; r != nil {
		if res.Err == nil {
			return fmt.Sprintf("an invalid program was accepted (it must be rejected with %+v)\ninput:\n%s", *r, describe(p))
		}
		checkDiagnostics(t, p.Main, res.Err)
		if !hasDiagnostic(res.Err, *r) {
			return fmt.Sprintf("the program must be rejected with %+v, got: %v\ninput:\n%s", *r, res.Err, describe(p))
		}
		return ""
	}
	if res.Err != nil {
		return fmt.Sprintf("the transpiler rejected a valid program: %v\ninput:\n%s", res.Err, describe(p))
	}
	checkGeneratedGo(t, p.Main, res.Go)
	imp := progrun.OracleImporter(t)
	if tc := imp.TypeCheck("main.go", res.Go); tc.Skipped != "" || len(tc.Errors) > 0 {
		return fmt.Sprintf("the Go generated for a valid program does not type-check: %s%s\ninput:\n%s\ngenerated:\n%s",
			tc.Skipped, strings.Join(tc.Errors, "\n  "), describe(p), res.Go)
	}
	got, err := progrun.RunOK(t, imp, res.Go)
	if err != nil {
		return fmt.Sprintf("the generated program failed: %v\ninput:\n%s\ngenerated:\n%s", err, describe(p), res.Go)
	}
	if got != p.Want {
		return fmt.Sprintf("the program printed\n%s\nbut must print\n%s\ninput:\n%s\ngenerated:\n%s", got, p.Want, describe(p), res.Go)
	}
	return ""
}

// hasDiagnostic reports whether one of err's diagnostics is r: it has r's code
// (any, when r has none) and its message holds r's text.
func hasDiagnostic(err error, r proggen.Rejection) bool {
	for _, leaf := range leafErrors(err) {
		var se *galaerr.SemanticError
		if errors.As(leaf, &se) && (r.Code == "" || se.Code == r.Code) && strings.Contains(se.Msg, r.Text) {
			return true
		}
	}
	return false
}

// describe is the program's source for a failure report.
func describe(p proggen.Program) string {
	if p.Lib == "" {
		return p.Main
	}
	return p.Main + "\n// ---- " + p.LibPath + "/lib.gala\n" + p.Lib
}
