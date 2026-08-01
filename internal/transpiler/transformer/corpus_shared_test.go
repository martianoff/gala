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

		for _, path := range discoverExampleSources() {
			src, err := os.ReadFile(path)
			if err != nil {
				corpusFiles = append(corpusFiles, corpusFile{Name: filepath.Base(path), Err: err})
				continue
			}

			p := transpiler.NewAntlrGalaParser()
			a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
			tr := transformer.NewGalaASTTransformer()
			g := generator.NewGoCodeGenerator()
			trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

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
// this target, sorted for a stable report order.
func discoverExampleSources() []string {
	roots := getStdSearchPath()
	if len(roots) == 0 {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(roots[0], "examples"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".gala") {
			continue
		}
		out = append(out, filepath.Join(roots[0], "examples", e.Name()))
	}
	sort.Strings(out)
	return out
}
