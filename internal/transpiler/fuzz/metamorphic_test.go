package fuzz_test

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler/fuzz/proggen"
)

// FuzzAlphaRenaming checks that renaming local binders does not change what
// a program does. It generates a program (metamorphic_gen_test.go), renames
// every val, parameter, lambda parameter and pattern variable to a hostile
// name — obj, _tmp_1, __tuple_1, _err, std, panic, string, len, fmt, __p0,
// or the name of another binder in scope, shadowing it where that keeps
// every reference resolving as before — and its top-level functions to the
// names of Go builtins (panic, new, len, …), and checks that it builds and prints
// exactly the output the generator computed. When it does not, the same
// program with plain names is run too, so the report says whether the
// renaming is to blame.
//
// Each input is built with the Go toolchain: fuzz it with -parallel 2, as
// FuzzPrograms.
func FuzzAlphaRenaming(f *testing.F) {
	fuzzMeta(f, metaConfig{hostile: true, traceOdds: 6}, 3)
}

// FuzzEvaluationOrder checks that GALA evaluates left to right. Its
// programs call tracing functions from operands, positional and named
// arguments, constructor arguments, default arguments, match subjects,
// guards and extractors in sub-patterns, read local vars that later
// operands change through a pointer (an address taken, a pointer-receiver
// method, a val holding a pointer, a function reading through its pointer
// parameter) or an element of a Go slice or map that later operands write,
// some of them named like Go builtins, read the package var counter where a
// block elsewhere binds the name, and must print the trace the
// generator computed from source order: left to right, an extractor only
// after the sibling sub-patterns before it matched, a match subject once,
// one if branch.
func FuzzEvaluationOrder(f *testing.F) {
	fuzzMeta(f, metaConfig{traceOdds: 2}, 4)
}

// metaSeeds is how many generated programs the seed pass of each
// metamorphic target builds and runs. Each costs a Go build, so it is kept
// small, as programSeeds is.
const metaSeeds = 8

func fuzzMeta(f *testing.F, cfg metaConfig, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	for range metaSeeds {
		data := make([]byte, 160)
		rng.Read(data)
		f.Add(data)
	}
	warmUp(f)
	// As in FuzzPrograms: under -fuzz each worker builds a program before
	// its inputs are timed.
	if fl := flag.Lookup("test.fuzz"); fl != nil && fl.Value.String() != "" {
		checkMeta(f, generateMeta(nil, cfg))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		checkMeta(t, generateMeta(data, cfg))
	})
}

func checkMeta(t testing.TB, p metaProgram) {
	t.Helper()
	failure := programFailure(t, proggen.Program{Main: p.src, Want: p.want})
	if failure == "" {
		if len(p.steered) > 0 {
			t.Logf("shapes of open issues %s were left out (knownFailures)", issueList(p.steered))
		}
		return
	}
	if p.plain == p.src {
		t.Fatal(failure)
	}
	plain := programFailure(t, proggen.Program{Main: p.plain, Want: p.want})
	if plain == "" {
		t.Fatalf("renaming the binders changed what the program does:\n%s\n\nthe same program with plain names passes:\n%s", failure, p.plain)
	}
	t.Fatalf("the program fails with plain names too, so the renaming is not the cause:\n%s\n\nwith the binders renamed:\n%s", plain, failure)
}

func issueList(issues []int) string {
	refs := make([]string, len(issues))
	for i, n := range issues {
		refs[i] = "#" + strconv.Itoa(n)
	}
	return strings.Join(refs, ", ")
}

// knownFailure is an open issue the metamorphic generators would hit. While
// it is listed, the generators leave out the shapes that hit it (see the
// calls of metaGen.avoid) and log that they did.
//
// TestKnownFailuresStillFail runs each entry's repro and fails once it
// passes: the fix for the issue must remove the entry, so the generators
// cover the shape again. The check runs offline, in the PR that fixes the
// bug, which a query of the issue tracker could not. An issue closed
// without a change — a language decision that keeps today's behaviour — is
// removed by hand, together with the generator's shape for it.
type knownFailure struct {
	issue int
	title string
	repro proggen.Program // fails while the issue is open
	// symptom is a substring of the repro's failure while the issue is open,
	// so a repro broken for another reason is not taken for the issue.
	symptom string
}

var knownFailures = []knownFailure{}

// openIssues are the listed issues whose shapes the generators leave out.
// GALA_FUZZ_IGNORE_KNOWN_FAILURES — "all" or a comma-separated list of
// issue numbers — generates the shapes of listed issues anyway, to show
// that the generators find them. Under bazel test, pass it with --test_env.
var openIssues = func() map[int]bool {
	ignored := strings.ToLower(strings.TrimSpace(os.Getenv("GALA_FUZZ_IGNORE_KNOWN_FAILURES")))
	open := map[int]bool{}
	if ignored == "all" {
		return open
	}
	for _, k := range knownFailures {
		open[k.issue] = true
	}
	for _, s := range strings.Split(ignored, ",") {
		if n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(s), "#")); err == nil {
			delete(open, n)
		}
	}
	return open
}()

// openIssue reports whether the generators leave out issue's shapes.
func openIssue(issue int) bool { return openIssues[issue] }

// TestKnownFailuresStillFail fails when a listed issue's repro passes: the
// issue is fixed, and its entry must go.
func TestKnownFailuresStillFail(t *testing.T) {
	for _, k := range knownFailures {
		t.Run(fmt.Sprintf("issue_%d", k.issue), func(t *testing.T) {
			failure := programFailure(t, k.repro)
			if failure == "" {
				t.Fatalf("#%d (%s) no longer reproduces: remove it from knownFailures so the metamorphic generators cover it again", k.issue, k.title)
			}
			if !strings.Contains(failure, k.symptom) {
				t.Fatalf("#%d's repro fails, but not with %q, the issue's symptom; fix the repro:\n%s", k.issue, k.symptom, failure)
			}
		})
	}
}
