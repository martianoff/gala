package analyzer_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"

	"github.com/stretchr/testify/require"
)

// siblingFixture builds one file of a large single-package fixture. Every
// declared name carries the seed so 64 files can share a package, and the
// bodies are drawn from the constructs that ANTLR's adaptive prediction works
// hardest on — and that the concurrent-crash report blamed: a generic call
// with explicit type arguments, named-argument construction, nested match
// arms, a lambda, and an interpolated string with embedded calls.
func siblingFixture(pkg string, seed int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", pkg)
	fmt.Fprintf(&b, "sealed type Signal%d {\n\tcase Ping%d(Seq int)\n\tcase Pong%d\n}\n\n", seed, seed, seed)
	fmt.Fprintf(&b, "sealed type Slot%d[T any] {\n\tcase Filled%d(Value T)\n\tcase Empty%d\n}\n\n", seed, seed, seed)
	fmt.Fprintf(&b, "type Payload%d struct {\n\tWidth int\n\tLabel string\n}\n\n", seed)

	fmt.Fprintf(&b, "func classify%d(sig Signal%d) string = sig match {\n\tcase Pong%d => \"pong\"\n\tcase Ping%d(n) => s\"ping $n\"\n}\n\n",
		seed, seed, seed, seed)
	fmt.Fprintf(&b, "func wrap%d[T any](v T) Slot%d[T] = Filled%d(Value = v)\n\n", seed, seed, seed)
	fmt.Fprintf(&b, "func unwrap%d[T any](sl Slot%d[T], fallback T) T = sl match {\n\tcase Filled%d(v) => v\n\tcase Empty%d => fallback\n}\n\n",
		seed, seed, seed, seed)
	fmt.Fprintf(&b, "func build%d(w int, label string) Payload%d = Payload%d(Width = w, Label = label)\n\n", seed, seed, seed)

	// Deeply parenthesized arithmetic around a match used as a value: the
	// shape that pushes the expression rules into full-context prediction.
	for f := 0; f < 4; f++ {
		fmt.Fprintf(&b, "func compute%d_%d(a int, b int, c int) int = ((a + b) * c) - (a match {\n\tcase 0 => %d\n\tcase _ => a\n})\n\n",
			seed, f, seed+f)
	}

	// Generic call with an explicit type argument nested inside another call,
	// inside an interpolation — the `castPayloadOrSkip[T](...)` shape.
	fmt.Fprintf(&b, "func chain%d(n int) string = s\"w=${build%d(n, \"l\").Width} v=${unwrap%d[int](wrap%d[int](n), 0)}\"\n\n",
		seed, seed, seed, seed)

	fmt.Fprintf(&b, "func fold%d(limit int) int {\n\tvar total = 0\n\tfor i := 0; i < limit; i++ {\n\t\ttotal = total + compute%d_0(i, i+1, 2)\n\t}\n\treturn total\n}\n",
		seed, seed)
	return b.String()
}

// TestConcurrentSiblingParse drives the analyzer's concurrent sibling parse
// (parseFilesConcurrent) over a package large enough to saturate every worker,
// from a cold parse cache.
//
// This is the path every surviving stack trace in the nondeterministic
// concurrent-crash report ended in: a worker parsing one file while its peers
// parse theirs, each through an ANTLR parser whose generated constructors share
// package-global prediction state. The crash it guards against is not a test
// failure but a process abort (nil dereference inside the ATN simulator) or a
// "concurrent map writes" fatal, so the assertions below are almost beside the
// point — the value is in running this under `-race`, which the `race` job in
// .github/workflows/test.yml does on every PR.
//
// Distinct from TestParseFilesConcurrent_RaceFree in parallel_parse_test.go,
// which covers the same function but cannot reach the same code. That test uses
// 12 files of `val vN = N`: enough to spawn the pool, but the bodies are so
// simple that ANTLR resolves every decision with SLL lookahead and never enters
// adaptive prediction, which is where the reported crashes lived. This one uses
// 64 files whose bodies are match arms, generic calls with explicit type
// arguments, named-argument construction, lambdas and interpolated strings —
// the constructs the report's blamed lines were made of — so the prediction
// machinery and its shared caches are actually exercised while the pool rotates.
//
// Hermetic by construction: the fixture declares its own sealed types and
// structs and imports nothing, so it needs no stdlib on the search path.
func TestConcurrentSiblingParse(t *testing.T) {
	const pkg = "racefixture"
	// Comfortably above GOMAXPROCS on any CI runner, so every worker is busy
	// and files keep queueing behind them.
	const files = 64

	dir := t.TempDir()
	paths := make([]string, files)
	for i := 0; i < files; i++ {
		p := filepath.Join(dir, fmt.Sprintf("f%02d.gala", i))
		require.NoError(t, os.WriteFile(p, []byte(siblingFixture(pkg, i)), 0o644))
		paths[i] = p
	}

	p := transpiler.NewAntlrGalaParser()
	src, err := os.ReadFile(paths[0])
	require.NoError(t, err)
	tree, docs, err := p.Parse(string(src))
	require.NoError(t, err, "fixture must be valid GALA")

	// A fresh analyzer per run means an empty parsedFileCache, so all 63
	// siblings are parsed for real rather than served from cache.
	a := analyzer.NewGalaAnalyzerWithPackageFiles(p, []string{dir}, paths, dir)
	richAST, err := a.Analyze(tree, docs, paths[0])
	require.NoError(t, err)
	require.NotNil(t, richAST)

	// Guard against this test going vacuous: if sibling discovery or the
	// concurrent parse silently stopped covering the package, every
	// assertion above would still pass. These names come from files the
	// analyzer only sees through parseFilesConcurrent.
	require.True(t, hasSuffixKey(richAST.Types, "Signal63"), "sibling sealed type missing from merged metadata")
	require.True(t, hasSuffixKey(richAST.Types, "Payload32"), "sibling struct missing from merged metadata")
	require.True(t, hasSuffixKey(richAST.Functions, "classify42"), "sibling function missing from merged metadata")
}

// hasSuffixKey reports whether any key in m ends in name, so the assertion
// does not depend on whether metadata keys are package-qualified.
func hasSuffixKey[V any](m map[string]V, name string) bool {
	for k := range m {
		if k == name || strings.HasSuffix(k, "."+name) {
			return true
		}
	}
	return false
}
