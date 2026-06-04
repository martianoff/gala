package gala

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

// rawImport mirrors one entry of the "imports" array emitted by the helper.
type rawImport struct {
	Path  string `json:"path"`
	Alias string `json:"alias"`
	Dot   bool   `json:"dot"`
}

// rawFile mirrors one element of the JSON array emitted by
// "gala imports --json <files...>".
type rawFile struct {
	File    string      `json:"file"`
	Package string      `json:"package"`
	Imports []rawImport `json:"imports"`
	Error   string      `json:"error"`
}

// fileInfo is the parsed, error-free view of one source file.
type fileInfo struct {
	Name    string
	Package string
	Imports []string
}

// importRunner executes the helper binary and returns its raw stdout. It is an
// interface so tests can inject a fake that emits the JSON contract directly,
// avoiding any dependency on an installed "gala" binary.
type importRunner func(helper, dir string, files []string) ([]byte, error)

// execRunner is the production importRunner: it shells out to the helper.
func execRunner(helper, dir string, files []string) ([]byte, error) {
	args := append([]string{"imports", "--json"}, files...)
	cmd := exec.Command(helper, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%s imports failed: %v: %s", helper, err, ee.Stderr)
		}
		return nil, fmt.Errorf("running %s imports: %w", helper, err)
	}
	return out, nil
}

// extractImports runs the helper over files (relative to dir) and returns a map
// keyed by file name. Files whose entry carries a non-empty error are skipped.
func extractImports(run importRunner, helper, dir string, files []string) (map[string]fileInfo, error) {
	if len(files) == 0 {
		return map[string]fileInfo{}, nil
	}
	out, err := run(helper, dir, files)
	if err != nil {
		return nil, err
	}
	return parseImports(out)
}

// parseImports decodes the helper's JSON output into fileInfo records. Entries
// reporting an error are dropped (the helper could not parse that file).
func parseImports(data []byte) (map[string]fileInfo, error) {
	var raws []rawFile
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, fmt.Errorf("parsing gala imports JSON: %w", err)
	}
	result := make(map[string]fileInfo, len(raws))
	for _, rf := range raws {
		if rf.Error != "" {
			continue
		}
		imps := make([]string, 0, len(rf.Imports))
		for _, imp := range rf.Imports {
			if imp.Path != "" {
				imps = append(imps, imp.Path)
			}
		}
		result[rf.File] = fileInfo{
			Name:    rf.File,
			Package: rf.Package,
			Imports: imps,
		}
	}
	return result, nil
}
