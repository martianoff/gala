package analyzer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
)

// TestCompanionKeysE2E drives the companion filing through the real Analyze
// path: two GALA packages, pkg_a and pkg_b, each with a sealed case Same,
// imported by one consumer file. Each package's companion is filed under its
// qualified key, and no bare Same is filed for either, whatever the consumer
// dot-imports: the transformer resolves a bare case name per file. A main
// package's own companion, which has no qualified key, is filed bare. Every
// scenario runs on a cold analyzer, which analyzes the packages from source,
// and on warm ones, which load them from the disk cache the cold one wrote.
// Two companions of one name used to race for the bare key in types-map
// order, so the scenarios run several times: a wrong filing shows on most
// runs.
func TestCompanionKeysE2E(t *testing.T) {
	tests := []struct {
		name     string
		consumer string
		wantBare string // the package the bare Same is filed for; "" for none
	}{
		{
			name: "a dot import beside a named import",
			consumer: "package main\n\n" +
				"import . \"martianoff/gala/pkg_a\"\n" +
				"import \"martianoff/gala/pkg_b\"\n\n" +
				"func main() { Println(Same(), pkg_b.Same()) }\n",
			wantBare: "",
		},
		{
			name: "named imports only",
			consumer: "package main\n\n" +
				"import \"martianoff/gala/pkg_a\"\n" +
				"import \"martianoff/gala/pkg_b\"\n\n" +
				"func main() { Println(pkg_a.Same(), pkg_b.Same()) }\n",
			wantBare: "",
		},
		{
			name: "two dot imports",
			consumer: "package main\n\n" +
				"import . \"martianoff/gala/pkg_b\"\n" +
				"import . \"martianoff/gala/pkg_a\"\n\n" +
				"func main() { Println(\"two dot imports\") }\n",
			wantBare: "",
		},
		{
			// A dot import of pkg_a would clash with it (GALA-E0066).
			name: "the main package's own companion, beside named imports",
			consumer: "package main\n\n" +
				"import \"martianoff/gala/pkg_a\"\n" +
				"import \"martianoff/gala/pkg_b\"\n\n" +
				"sealed type Local {\n    case Same\n    case Other\n}\n\n" +
				"func main() { Println(Same(), pkg_a.Same(), pkg_b.Same()) }\n",
			wantBare: "main",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module testmod\ngo 1.25\n"), 0644))
			for _, pkg := range []string{"pkg_a", "pkg_b"} {
				dir := filepath.Join(tmp, pkg)
				require.NoError(t, os.MkdirAll(dir, 0755))
				src := "package " + pkg + "\n\nsealed type Kind {\n    case Same\n    case Diff\n}\n"
				require.NoError(t, os.WriteFile(filepath.Join(dir, pkg+".gala"), []byte(src), 0644))
			}
			dir := filepath.Join(tmp, "app")
			require.NoError(t, os.MkdirAll(dir, 0755))
			file := filepath.Join(dir, "main.gala")
			require.NoError(t, os.WriteFile(file, []byte(tt.consumer), 0644))

			parser := transpiler.NewAntlrGalaParser()
			searchPaths := []string{tmp}
			if std := stdSearchPathForE2E(); std != "" {
				searchPaths = append(searchPaths, std)
			}
			for run := 0; run < 12; run++ {
				// Run 0 is cold and writes the disk cache; every later run
				// is a fresh analyzer that loads pkg_a and pkg_b from it.
				tree, _, err := parser.ParseFile(tt.consumer).Strict()
				require.NoError(t, err)
				res, err := transpiler.Analyze(NewBatchAnalyzer(parser, searchPaths, tmp), tree, nil, file)
				require.NoError(t, err)
				if run == 0 {
					readDiskCacheEntry(t, tmp, "pkg_a")
				}

				got := res.CompanionObjects["Same"]
				if tt.wantBare == "" {
					require.Nil(t, got, "run %d: the bare Same is filed for package %v, want none", run, got)
				} else {
					require.NotNil(t, got, "run %d: no bare Same, want package %s's", run, tt.wantBare)
					require.Equal(t, tt.wantBare, got.Package, "run %d: the bare Same's package", run)
				}
				for _, pkg := range []string{"pkg_a", "pkg_b"} {
					q := res.CompanionObjects[pkg+".Same"]
					require.NotNil(t, q, "run %d: %s.Same is not filed", run, pkg)
					require.Equal(t, pkg, q.Package, "run %d: %s.Same's package", run, pkg)
				}
			}
		})
	}
}
