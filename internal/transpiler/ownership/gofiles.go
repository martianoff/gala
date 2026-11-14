package ownership

import (
	"os"
	"path/filepath"
	"strings"

	"martianoff/gala/internal/transpiler/genheader"
)

// HasHandWrittenGo reports whether directory dir holds a .go file (not a
// test) that the GALA transpiler did not write. Such a file is Go code of the
// package that may call any of its functions (Options.HandWrittenGo).
func HasHandWrittenGo(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || filepath.Ext(n) != ".go" || strings.HasSuffix(n, "_test.go") {
			continue
		}
		if !genheader.StaleFile(filepath.Join(dir, n)) {
			return true
		}
	}
	return false
}
