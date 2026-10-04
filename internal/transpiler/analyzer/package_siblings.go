package analyzer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PackageSiblings returns the other non-test .gala files in file's directory:
// the set Bazel passes as package_files for each of a package's sources.
//
// Batch callers that cannot take the sibling set from their input list pass
// this to SetPackageFiles rather than relying on the analyzer's own directory
// scan, which skips packages named main or test (it treats those directories
// as independent programs, like examples/). A library package may be called
// either, as the shipped test package is, and its files must still see each
// other's declarations.
func PackageSiblings(file string) ([]string, error) {
	dir := filepath.Dir(file)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("listing package of %s: %w", file, err)
	}
	self := filepath.Base(file)
	var siblings []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == self || filepath.Ext(name) != ".gala" || strings.HasSuffix(name, "_test.gala") {
			continue
		}
		siblings = append(siblings, filepath.Join(dir, name))
	}
	return siblings, nil
}
