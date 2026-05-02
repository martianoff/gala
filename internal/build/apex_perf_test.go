package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// TestTranspile_ApexShape mirrors the workload that dominates cold
// build time on the bazel perf-real-build.yml workflow:
//
//   - one apex `main.gala` that dot-imports many sibling packages
//     (stand-in for cmd/main.gala in gala_team, which dot-imports
//     ~11 first-party packages and pulls in ~100 transitive ones)
//   - each imported package has multiple .gala files declaring
//     types and functions, so analyzePackage's parseFilesConcurrent
//     loop fires for real
//
// The point is to expose the actual hot path in a way that can be
// CPU-profiled with `go test -cpuprofile`. Capture the profile with:
//
//	bazel test //internal/build:build_test --test_output=all \
//	  --test_arg=-test.run=TestTranspile_ApexShape \
//	  --test_arg=-test.cpuprofile=/tmp/apex.pprof \
//	  --test_arg=-test.v --cache_test_results=no
//	go tool pprof -top -cum /tmp/apex.pprof
//
// The wall time the test logs is the cold transpile time; this is
// what we want to reduce. There is no budget assertion — diagnostic
// only.
const (
	apexNumPackages       = 12 // ~mirrors gala_team's first-party package count
	apexFilesPerPackage   = 5
	apexTypesPerFile      = 15
)

func TestTranspile_ApexShape(t *testing.T) {
	if testing.Short() {
		t.Skip("apex profile test skipped in -short mode")
	}

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "gala.mod"),
		[]byte("module example.com/apex\n\ngala 0.0.0\n"), 0644))

	// Generate N packages each with M sibling files. Each package's
	// types are declared with a unique prefix so apex can reference
	// them without name collisions.
	pkgNames := make([]string, apexNumPackages)
	for p := 0; p < apexNumPackages; p++ {
		pkgNames[p] = fmt.Sprintf("pkg%02d", p)
		pkgDir := filepath.Join(projectDir, pkgNames[p])
		require.NoError(t, os.MkdirAll(pkgDir, 0755))
		for f := 0; f < apexFilesPerPackage; f++ {
			path := filepath.Join(pkgDir, fmt.Sprintf("file_%02d.gala", f))
			body := apexPackageFileBody(pkgNames[p], f)
			require.NoError(t, os.WriteFile(path, []byte(body), 0644))
		}
	}

	// Apex file: dot-imports all packages, references one type from
	// each so the analyzer is forced to load the full closure.
	apexBody := apexFileBody(pkgNames)
	apexPath := filepath.Join(projectDir, "main.gala")
	require.NoError(t, os.WriteFile(apexPath, []byte(apexBody), 0644))

	// Wipe any prior cache so cold pass is genuinely cold.
	_ = os.RemoveAll(filepath.Join(projectDir, ".gala"))

	stdDir := findStdDir(t.Fatalf)
	searchPaths := []string{projectDir, stdDir}

	apexContent, err := os.ReadFile(apexPath)
	require.NoError(t, err)

	p := transpiler.NewAntlrGalaParser()
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	batch := analyzer.NewBatchAnalyzer(p, searchPaths, projectDir)
	batch.SetPackageFiles(nil) // apex has no siblings; it's package main
	txp := transpiler.NewGalaToGoTranspiler(p, batch, tr, g)

	start := time.Now()
	_, err = txp.Transpile(string(apexContent), apexPath)
	cold := time.Since(start)
	require.NoErrorf(t, err, "apex transpile failed (this is expected to succeed; if it fails the synthetic was malformed)")

	t.Logf("apex cold transpile: %s", cold)
	t.Logf("  packages:         %d", apexNumPackages)
	t.Logf("  files per pkg:    %d", apexFilesPerPackage)
	t.Logf("  types per file:   %d", apexTypesPerFile)
	t.Logf("  total .gala files in closure: %d", apexNumPackages*apexFilesPerPackage+1)
}

// apexFileBody returns the source for the apex file: package main,
// dot-imports every pkg, references one type from each so the
// analyzer must build the full type closure.
func apexFileBody(pkgs []string) string {
	var b strings.Builder
	b.WriteString("package main\n\nimport (\n")
	for _, p := range pkgs {
		fmt.Fprintf(&b, "    . \"example.com/apex/%s\"\n", p)
	}
	b.WriteString(")\n\n")
	// Force a reference to a type from each pkg so the analyzer
	// can't optimize the import away.
	b.WriteString("func main() {\n")
	for _, p := range pkgs {
		// Reference T0 from this pkg (defined in file_00.gala).
		fmt.Fprintf(&b, "    var _ %s_T_00_0\n", p)
	}
	b.WriteString("}\n")
	return b.String()
}

// apexPackageFileBody returns the source for the f-th file in pkgName.
// Declares apexTypesPerFile struct types prefixed with the package
// name (so dot-imports don't collide) and a constructor for each.
func apexPackageFileBody(pkgName string, fileIdx int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", pkgName)
	for k := 0; k < apexTypesPerFile; k++ {
		typeName := fmt.Sprintf("%s_T_%02d_%d", pkgName, fileIdx, k)
		fmt.Fprintf(&b, "type %s struct {\n    a int\n    b int\n    c string\n    d string\n}\n\n", typeName)
		fmt.Fprintf(&b, "func make_%s() %s {\n    return %s{a: %d, b: %d, c: \"x\", d: \"y\"}\n}\n\n",
			typeName, typeName, typeName, k, k)
	}
	return b.String()
}
