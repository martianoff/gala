package difftest

import (
	"fmt"
	"math/rand"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"

	"martianoff/gala/internal/transpiler/fuzz/proggen"
)

// ExamplePrograms is every examples/*.gala staged in the runfiles that has a
// .out file, with the .out as its oracle: the program ends normally and
// prints it. The examples in subdirectories are packages of multi-file
// programs, not programs of their own.
func ExamplePrograms(tb testing.TB) []Program {
	tb.Helper()
	files, err := bazel.ListRunfiles()
	if err != nil {
		tb.Fatalf("cannot list the runfiles: %v", err)
	}
	byPath := map[string]string{}
	for _, f := range files {
		if path.Dir(f.ShortPath) == "examples" {
			byPath[f.ShortPath] = f.Path
		}
	}
	var out []Program
	for short, file := range byPath {
		if !strings.HasSuffix(short, ".gala") {
			continue
		}
		outFile, ok := byPath[strings.TrimSuffix(short, ".gala")+".out"]
		if !ok {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			tb.Fatal(err)
		}
		want, err := os.ReadFile(outFile)
		if err != nil {
			tb.Fatal(err)
		}
		out = append(out, Program{
			ID:     short,
			Source: FromExample,
			Path:   path.Base(short),
			Text:   string(src),
			Oracle: &Run{Stdout: strings.ReplaceAll(string(want), "\r\n", "\n"), Outcome: Outcome{Kind: KindOK}},
		})
	}
	return out
}

// GeneratedPrograms is n programs from FuzzPrograms' generator, from a fixed
// seed so a run is reproducible, with the generator's model as the oracle.
// The generator also writes programs that must be rejected; the spec has no
// type checker to compare those with, so they are left out, and skipped says
// how many were.
func GeneratedPrograms(n int, seed int64) (programs []Program, skipped int) {
	rng := rand.New(rand.NewSource(seed))
	for i := range n {
		data := make([]byte, 128)
		rng.Read(data)
		p := proggen.Generate(data)
		if p.Rejection != nil {
			skipped++
			continue
		}
		programs = append(programs, Program{
			ID:      fmt.Sprintf("fuzzprograms/seed-%03d", i),
			Source:  FromGenerator,
			Path:    "main.gala",
			Text:    p.Main,
			Lib:     p.Lib,
			LibPath: p.LibPath,
			Oracle:  &Run{Stdout: p.Want, Outcome: Outcome{Kind: KindOK}},
		})
	}
	return programs, skipped
}
