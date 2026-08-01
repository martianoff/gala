package transformer_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/require"
)

// The corpus-wide guards — the unresolved-type inventory and the type-erasure
// check — both need every example transpiled. Transpiling the corpus takes
// most of a minute, and doing it twice in one test binary overran the target's
// timeout. They share a single pass instead.
//
// The pass runs with GALA_WARN_TYPES=1 so the inventory is collected. That only
// enables diagnostics; it does not change the generated Go the erasure check
// reads.

// corpusFile is one example's transpile result.
type corpusFile struct {
	Name       string // base name, e.g. "apply_method.gala"
	Source     string
	Output     string // generated Go; empty when Err is set
	Unresolved []transformer.UnresolvedType
	Err        error
}

var (
	corpusOnce  sync.Once
	corpusFiles []corpusFile
)

// exampleCorpus transpiles every single-file example once and returns the
// results. Safe to call from multiple tests; the work happens on first use.
func exampleCorpus(t *testing.T) []corpusFile {
	t.Helper()
	corpusOnce.Do(func() {
		// os.Setenv rather than t.Setenv: this runs once, under whichever test
		// happens to be first, and the value has to stay set for the whole
		// pass rather than being unwound when that test ends.
		prev, had := os.LookupEnv("GALA_WARN_TYPES")
		os.Setenv("GALA_WARN_TYPES", "1")
		defer func() {
			if had {
				os.Setenv("GALA_WARN_TYPES", prev)
			} else {
				os.Unsetenv("GALA_WARN_TYPES")
			}
		}()

		for _, path := range discoverExampleSources(t) {
			src, err := os.ReadFile(path)
			if err != nil {
				corpusFiles = append(corpusFiles, corpusFile{Name: filepath.Base(path), Err: err})
				continue
			}

			trans, tr := newTranspilerWithTransformer()
			out, err := trans.Transpile(string(src), filepath.Base(path))
			corpusFiles = append(corpusFiles, corpusFile{
				Name:       filepath.Base(path),
				Source:     string(src),
				Output:     out,
				Unresolved: transformer.UnresolvedTypes(tr),
				Err:        err,
			})
		}
	})
	require.NotEmpty(t, corpusFiles, "the example corpus is empty; the guards would pass vacuously")
	return corpusFiles
}

// discoverExampleSources returns the single-file example sources staged for
// this target, sorted for a stable report order. It uses findExamplesDir so
// that every corpus-wide guard in this package resolves examples/ the same
// way; two strategies in one binary can disagree after a layout change and
// then quietly measure different file sets.
func discoverExampleSources(t *testing.T) []string {
	t.Helper()
	dir := findExamplesDir(t)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "cannot read %s", dir)

	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".gala") {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	sort.Strings(out)
	return out
}

// newTranspilerWithTransformer builds the standard pipeline and also returns
// the transformer, which the corpus guards need in order to read the
// diagnostics it collected. newTranspiler discards it.
func newTranspilerWithTransformer() (*transpiler.GalaToGoTranspiler, transpiler.ASTTransformer) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return transpiler.NewGalaToGoTranspiler(p, a, tr, g), tr
}
