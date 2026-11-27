package analyzer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
)

// GoDotImportExports lists what a dot-imported Go package declares, and only
// that. crypto/rand's API reaches math/rand's Rand, which its type info files
// as `rand.Rand` like crypto/rand's own names; crypto/rand declares no Rand.
// A Go package imported by name is not listed.
func TestGoDotImportExports(t *testing.T) {
	skipIfNoGoSDK(t)
	tests := []struct {
		name    string
		imports string
		body    string
		path    string
		listed  bool
		has     []string
		hasNot  []string
	}{
		{
			name:    "dot import",
			imports: `import . "crypto/rand"`,
			body:    "Println(Reader != nil)",
			path:    "crypto/rand",
			listed:  true,
			has:     []string{"Read", "Reader"},
			hasNot:  []string{"Rand"},
		},
		{
			name:    "import by name",
			imports: `import "strings"`,
			body:    `Println(strings.ToUpper("a"))`,
			path:    "strings",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n\n" + tc.imports + "\n\nfunc main() {\n    " + tc.body + "\n}\n"
			filePath := filepath.Join(t.TempDir(), "main.gala")
			require.NoError(t, os.WriteFile(filePath, []byte(src), 0644))
			p := transpiler.NewAntlrGalaParser()
			parsed := p.ParseFile(src)
			require.NoError(t, parsed.Err())
			richAST, err := analyzer.NewGalaAnalyzer(p, getStdSearchPath()).AnalyzeParsed(parsed, filePath)
			require.NoError(t, err)

			exports, listed := richAST.GoDotImportExports[tc.path]
			require.Equal(t, tc.listed, listed)
			for _, name := range tc.has {
				assert.Contains(t, exports, name)
			}
			for _, name := range tc.hasNot {
				assert.NotContains(t, exports, name)
			}
		})
	}
}
