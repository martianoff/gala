package analyzer_test

import (
	"fmt"
	"go/importer"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"martianoff/gala/internal/transpiler/analyzer"
)

func TestGoImporterDebug(t *testing.T) {
	t.Logf("GOROOT: %s", runtime.GOROOT())
	t.Logf("GOROOT env: %s", os.Getenv("GOROOT"))
	t.Logf("GOPATH env: %s", os.Getenv("GOPATH"))
	t.Logf("PATH: %s", os.Getenv("PATH"))
	t.Logf("GoImporterAvailable: %v", analyzer.GoImporterAvailable())

	// Try default importer
	imp := importer.Default()
	pkg, err := imp.Import("fmt")
	if err != nil {
		t.Logf("Default importer failed: %v", err)
	} else {
		t.Logf("Default importer succeeded: %s with %d names", pkg.Path(), len(pkg.Scope().Names()))
	}

	// Try source importer
	fset := token.NewFileSet()
	srcImp := importer.ForCompiler(fset, "source", nil)
	pkg2, err := srcImp.Import("fmt")
	if err != nil {
		t.Logf("Source importer failed: %v", err)
	} else {
		t.Logf("Source importer succeeded: %s with %d names", pkg2.Path(), len(pkg2.Scope().Names()))
	}

	// Check if GOROOT/src/fmt exists
	goroot := runtime.GOROOT()
	fmtPath := goroot + "/src/fmt"
	if _, err := os.Stat(fmtPath); err != nil {
		t.Logf("GOROOT/src/fmt does not exist: %v", err)
	} else {
		t.Logf("GOROOT/src/fmt exists")
		entries, _ := os.ReadDir(fmtPath)
		for _, e := range entries {
			fmt.Fprintf(os.Stderr, "  %s\n", e.Name())
		}
	}

	// Try ForCompiler with "gc"
	gcImp := importer.ForCompiler(fset, "gc", nil)
	pkg3, err := gcImp.Import("fmt")
	if err != nil {
		t.Logf("GC importer failed: %v", err)
	} else {
		t.Logf("GC importer succeeded: %s with %d names", pkg3.Path(), len(pkg3.Scope().Names()))
	}

	// Check GOROOT/pkg directory
	pkgDir := goroot + "/pkg"
	if _, err := os.Stat(pkgDir); err != nil {
		t.Logf("GOROOT/pkg does not exist: %v", err)
	} else {
		t.Logf("GOROOT/pkg exists")
		entries, _ := os.ReadDir(pkgDir)
		for _, e := range entries {
			t.Logf("  %s", e.Name())
		}
	}

	// Try type-checking from source manually
	importerForConfig := types.Importer(srcImp)
	_ = importerForConfig

	// Test findGoOnPath by searching PATH manually
	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		pathEnv = os.Getenv("Path")
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		candidate := filepath.Join(dir, "go.exe")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			goroot := filepath.Dir(filepath.Dir(candidate))
			srcFmt := filepath.Join(goroot, "src", "fmt")
			_, err2 := os.Stat(srcFmt)
			t.Logf("Found go.exe at %s, GOROOT=%s, src/fmt exists=%v", candidate, goroot, err2 == nil)
			// Try setting GOROOT and importing
			os.Setenv("GOROOT", goroot)
			srcImp2 := importer.ForCompiler(fset, "source", nil)
			pkg4, err := srcImp2.Import("fmt")
			if err != nil {
				t.Logf("Source importer with GOROOT=%s failed: %v", goroot, err)
			} else {
				t.Logf("Source importer with GOROOT=%s succeeded: %s with %d names", goroot, pkg4.Path(), len(pkg4.Scope().Names()))
			}
			break
		}
	}
}
