package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/build"
	"martianoff/gala/internal/depman/fetch"
	"martianoff/gala/internal/depman/graph"
	"martianoff/gala/internal/depman/mod"
	"martianoff/gala/internal/depman/sum"
	"martianoff/gala/internal/depman/version"
)

var modTidyCmd = &cobra.Command{
	Use:   "tidy",
	Short: "Add missing and remove unused dependencies",
	Long: `Tidy ensures that gala.mod matches the imports in your source files.

It adds any missing module requirements and removes unused ones.
It also updates gala.sum with the correct checksums.

Examples:
  gala mod tidy`,
	Run: runModTidy,
}

func runModTidy(cmd *cobra.Command, args []string) {
	// Load gala.mod
	galaMod, err := mod.ParseFile("gala.mod")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "Run 'gala mod init' first.")
		os.Exit(1)
	}

	// Update GALA version to current version
	galaMod.Gala = Version

	// Scan source files for imports
	imports, err := scanImports(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error scanning imports: %v\n", err)
		os.Exit(1)
	}

	// Determine which imports are external dependencies
	externalImports := filterExternalImports(imports, galaMod.Module.Path, galaMod.Require)

	// Build sets for comparison
	requiredPaths := make(map[string]bool)
	for path := range externalImports {
		requiredPaths[path] = true
	}

	currentDeps := make(map[string]mod.Require)
	for _, req := range galaMod.Require {
		currentDeps[req.Path] = req
	}

	// Find missing and unused dependencies
	var missing []string
	var unused []string

	for path := range requiredPaths {
		if _, ok := currentDeps[path]; !ok {
			missing = append(missing, path)
		}
	}

	for path, req := range currentDeps {
		// Don't remove Go dependencies (marked with // go) - they're explicit transitive deps
		if !requiredPaths[path] && !req.Go {
			unused = append(unused, path)
		}
	}

	// Initialize fetcher for adding missing deps
	config := fetch.DefaultConfig()
	cache := fetch.NewCache(config)
	fetcher := fetch.NewGitFetcher(cache)

	// Add missing dependencies
	for _, path := range missing {
		fmt.Printf("Adding %s...\n", path)

		// Fetch latest version
		ver, _, _, err := fetcher.FetchLatest(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to fetch %s: %v\n", path, err)
			continue
		}

		galaMod.Require = append(galaMod.Require, mod.Require{
			Path:    path,
			Version: ver,
		})
	}

	// Remove unused dependencies
	if len(unused) > 0 {
		var newRequire []mod.Require
		for _, req := range galaMod.Require {
			keep := true
			for _, path := range unused {
				if req.Path == path {
					fmt.Printf("Removing unused %s\n", path)
					keep = false
					break
				}
			}
			if keep {
				newRequire = append(newRequire, req)
			}
		}
		galaMod.Require = newRequire
	}

	// Build dependency graph and resolve versions with MVS
	if err := resolveModuleGraph(galaMod, requiredPaths, cache, fetcher); err != nil {
		var cycle *graph.CycleError
		if errors.As(err, &cycle) {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	// Compute gala.sum before writing anything, so a module that cannot be
	// recorded leaves both files as they were.
	galaSum, err := galaSumFor(galaMod, loadGalaSum(), cache, fetcher)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Write updated gala.mod
	if err := mod.WriteFile(galaMod, "gala.mod"); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing gala.mod: %v\n", err)
		os.Exit(1)
	}
	if err := sum.WriteFile(galaSum, "gala.sum"); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing gala.sum: %v\n", err)
		os.Exit(1)
	}

	// For Bazel projects, generate minimal go.mod with only Go dependencies
	// (GALA deps are handled by the gala bzlmod extension)
	if _, err := os.Stat("MODULE.bazel"); err == nil {
		if err := syncGoModForBazel(galaMod); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to sync go.mod for Bazel: %v\n", err)
		}
	}

	if len(missing) == 0 && len(unused) == 0 {
		fmt.Println("All dependencies are up to date.")
	} else {
		fmt.Println("Done.")
	}
	fmt.Println("Run 'gala build' to compile your project.")
}

// syncGoModForBazel creates a minimal go.mod and go.sum for Bazel projects.
// It only includes Go dependencies from gala.mod (marked with // go).
// GALA dependencies are handled by the gala bzlmod extension.
func syncGoModForBazel(galaMod *mod.File) error {
	// Collect only Go dependencies
	var goDeps []mod.Require
	for _, req := range galaMod.Require {
		if req.Go {
			goDeps = append(goDeps, req)
		}
	}

	goModPath := "go.mod"
	goSumPath := "go.sum"

	// Read existing go.mod if present
	var existingContent string
	if content, err := os.ReadFile(goModPath); err == nil {
		existingContent = string(content)
	}

	// If no Go deps and no existing go.mod, skip
	if len(goDeps) == 0 && existingContent == "" {
		return nil
	}

	goMod, updated := renderBazelGoMod(existingContent, galaMod.Module.Path, goDeps)
	if err := os.WriteFile(goModPath, []byte(goMod), 0644); err != nil {
		return err
	}
	for _, u := range updated {
		fmt.Printf("go.mod: %s (the version gala.mod requires)\n", u)
	}

	fmt.Println("Generated go.mod for Bazel (Go dependencies only)")

	// Generate go.sum by running 'go mod download -json' to get checksums
	if len(goDeps) > 0 {
		if err := generateGoSum(goSumPath); err != nil {
			return fmt.Errorf("generating go.sum: %w", err)
		}
		fmt.Println("Generated go.sum for Bazel")
	}

	return nil
}

// goModManagedMarker is the start and end line of the go.mod section gala
// writes.
type goModManagedMarker struct{ start, end string }

// The first and last line of the section gala writes today.
const (
	managedGoModStart = "// GALA-managed Go dependencies below. DO NOT EDIT."
	managedGoModEnd   = "// End GALA-managed dependencies."
)

// goModManagedMarkers returns every spelling of the managed section: today's
// first, then those older gala versions wrote, which are still removed so a
// go.mod written by an earlier gala is cleaned up.
func goModManagedMarkers() []goModManagedMarker {
	return []goModManagedMarker{
		{managedGoModStart, managedGoModEnd},
		{"// GALA package dependencies below. DO NOT EDIT.", "// End GALA package dependencies."},
		{"// GALA stdlib dependencies below. DO NOT EDIT.", "// End GALA stdlib dependencies."},
		{"// GALA dependencies below. DO NOT EDIT.", "// End GALA dependencies."},
	}
}

// renderBazelGoMod returns the go.mod for a Bazel project: existing (the
// current go.mod, possibly hand-written, possibly "") with every section gala
// manages replaced by one section requiring goDeps.
//
// A module the rest of the file already requires is not required again in the
// managed section. A hand-written go.mod — one maintained with `go get` —
// already requires the Go dependencies its code imports, and a second
// requirement of the same module is one the go command either reports twice
// (`go mod download -json` lists it once per requirement, which put its go.sum
// lines in twice) or, at a different version, refuses ("updates to go.mod
// needed"). Instead, that requirement is set to the version gala.mod requires;
// updated describes each one changed.
//
// The line endings of existing are kept. The result depends only on the
// requirements, not on what gala wrote before, so rendering its own output
// again returns it unchanged.
func renderBazelGoMod(existing, modulePath string, goDeps []mod.Require) (goMod string, updated []string) {
	crlf := strings.Contains(existing, "\r\n")
	existing = strings.ReplaceAll(existing, "\r\n", "\n")

	for _, m := range goModManagedMarkers() {
		for {
			startIdx := strings.Index(existing, m.start)
			if startIdx == -1 {
				break
			}
			endIdx := strings.Index(existing[startIdx:], m.end)
			if endIdx == -1 {
				break
			}
			existing = existing[:startIdx] + existing[startIdx+endIdx+len(m.end):]
		}
	}

	// Strip any GALA stdlib replace directives (legacy or manually added).
	// For Bazel projects, GALA deps are handled by the bzlmod extension,
	// so go.mod should not have replace directives pointing to local cache paths.
	// For non-Bazel projects, gala build generates these in a temp workspace.
	var cleanedLines []string
	for _, line := range strings.Split(existing, "\n") {
		trimmed := strings.TrimSpace(line)
		// Strip GALA stdlib replace directives
		if strings.HasPrefix(trimmed, "replace martianoff/gala/") {
			continue
		}
		// Strip GALA stdlib require entries (inside or outside require blocks)
		if strings.Contains(trimmed, "martianoff/gala/") &&
			!strings.HasPrefix(trimmed, "module ") &&
			!strings.HasPrefix(trimmed, "//") {
			continue
		}
		cleanedLines = append(cleanedLines, line)
	}
	existing = strings.Join(cleanedLines, "\n")

	// Clean up empty require/replace blocks left after stripping
	existing = strings.TrimSpace(cleanEmptyGoModBlocks(existing))
	if existing == "" {
		existing = fmt.Sprintf("module %s\n\ngo 1.22", modulePath)
	}

	// Requirements the file already has are set to gala.mod's version in place.
	want := make(map[string]string, len(goDeps))
	for _, dep := range goDeps {
		want[dep.Path] = dep.Version
	}
	lines := strings.Split(existing, "\n")
	required := make(map[string]bool)
	for _, r := range build.ParseGoModRequireLines(existing) {
		required[r.Path] = true
		ver, managed := want[r.Path]
		if !managed || ver == r.Version {
			continue
		}
		line := lines[r.Line]
		at := strings.Index(line, r.Path) + len(r.Path)
		lines[r.Line] = line[:at] + strings.Replace(line[at:], r.Version, ver, 1)
		updated = append(updated, fmt.Sprintf("%s %s -> %s", r.Path, r.Version, ver))
	}

	var sb strings.Builder
	sb.WriteString(strings.Join(lines, "\n"))
	var managed []mod.Require
	for _, dep := range goDeps {
		if !required[dep.Path] {
			managed = append(managed, dep)
		}
	}
	if len(managed) > 0 {
		sb.WriteString("\n\n")
		sb.WriteString(managedGoModStart + "\n")
		sb.WriteString("// Generated by 'gala mod tidy'. Use 'gala build' or 'bazel build' to compile.\n")
		sb.WriteString("require (\n")
		for _, dep := range managed {
			sb.WriteString(fmt.Sprintf("\t%s %s\n", dep.Path, dep.Version))
		}
		sb.WriteString(")\n")
		sb.WriteString(managedGoModEnd + "\n")
	}
	sb.WriteString("\n")

	goMod = sb.String()
	if crlf {
		goMod = strings.ReplaceAll(goMod, "\n", "\r\n")
	}
	return goMod, updated
}

// cleanEmptyGoModBlocks removes empty require() and replace() blocks from go.mod content.
func cleanEmptyGoModBlocks(content string) string {
	lines := strings.Split(content, "\n")
	var result []string
	i := 0
	for i < len(lines) {
		trimmed := strings.TrimSpace(lines[i])
		// Check for empty require/replace blocks: "require (" followed by ")"
		if (trimmed == "require (" || trimmed == "replace (") && i+1 < len(lines) {
			nextTrimmed := strings.TrimSpace(lines[i+1])
			if nextTrimmed == ")" {
				i += 2 // skip the empty block
				continue
			}
		}
		result = append(result, lines[i])
		i++
	}
	return strings.Join(result, "\n")
}

// generateGoSum generates go.sum file by downloading Go modules and getting their checksums.
func generateGoSum(goSumPath string) error {
	// Run 'go mod download -json' to get module info with checksums
	cmd := exec.Command("go", "mod", "download", "-json")
	output, err := cmd.Output()
	if err != nil {
		// If go mod download fails, try running go mod tidy
		tidyCmd := exec.Command("go", "mod", "tidy")
		if tidyErr := tidyCmd.Run(); tidyErr != nil {
			return fmt.Errorf("go mod download failed and go mod tidy failed: %v, %v", err, tidyErr)
		}
		// After tidy, the go.sum should exist
		return nil
	}

	downloaded, err := goSumLinesFromDownload(output)
	if err != nil {
		return err
	}
	if len(downloaded) == 0 {
		// If no entries from JSON, run go mod tidy to generate go.sum
		tidyCmd := exec.Command("go", "mod", "tidy")
		return tidyCmd.Run()
	}

	var existing string
	if content, err := os.ReadFile(goSumPath); err == nil {
		existing = string(content)
	}
	return os.WriteFile(goSumPath, []byte(mergeGoSum(existing, downloaded)), 0644)
}

// goSumLinesFromDownload turns the output of `go mod download -json` into
// go.sum lines, one module record at a time.
func goSumLinesFromDownload(downloadJSON []byte) ([]string, error) {
	var lines []string
	decoder := json.NewDecoder(bytes.NewReader(downloadJSON))
	for decoder.More() {
		var info struct {
			Path     string `json:"Path"`
			Version  string `json:"Version"`
			Sum      string `json:"Sum"`
			GoModSum string `json:"GoModSum"`
		}
		if err := decoder.Decode(&info); err != nil {
			return nil, fmt.Errorf("reading `go mod download -json` output: %w", err)
		}
		if info.Sum != "" {
			lines = append(lines, fmt.Sprintf("%s %s %s", info.Path, info.Version, info.Sum))
		}
		if info.GoModSum != "" {
			lines = append(lines, fmt.Sprintf("%s %s/go.mod %s", info.Path, info.Version, info.GoModSum))
		}
	}
	return lines, nil
}

// mergeGoSum returns the go.sum holding the lines of existing (the current
// go.sum) and downloaded, each once and in sorted order.
//
// The existing lines are kept: `go mod download` reports only the modules in
// the build list, while a go.sum maintained with `go get` also holds the go.mod
// hashes of the rest of the module graph, which the go command needs. And each
// line is written once: the download output has one record per requirement,
// and the existing file may already hold a line — both of which used to repeat
// lines in go.sum.
func mergeGoSum(existing string, downloaded []string) string {
	seen := make(map[string]bool)
	var lines []string
	for _, line := range append(strings.Split(existing, "\n"), downloaded...) {
		line = strings.TrimSpace(line)
		if line != "" && !seen[line] {
			seen[line] = true
			lines = append(lines, line)
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}

// scanImports scans all .gala files in the directory tree for import statements.
// Handles both single-line imports (import "pkg") and multi-line import blocks
// (import (\n    "pkg"\n)), including dot imports (. "pkg") and aliased imports (alias "pkg").
func scanImports(dir string) (map[string]bool, error) {
	imports := make(map[string]bool)

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden directories and common non-source directories
		if info.IsDir() {
			name := info.Name()
			// Don't skip the root directory "."
			if name != "." && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" || strings.HasPrefix(name, "bazel-") || name == "_gala") {
				return filepath.SkipDir
			}
			return nil
		}

		// Only process .gala files
		if filepath.Ext(path) != ".gala" {
			return nil
		}

		// Read and scan file
		content, err := os.ReadFile(path)
		if err != nil {
			return nil // Skip files we can't read
		}

		lines := strings.Split(galaerr.StripBOM(string(content)), "\n")
		inImportBlock := false
		for _, line := range lines {
			line = strings.TrimSpace(line)

			// Single-line import: import "pkg" or import . "pkg"
			if strings.HasPrefix(line, "import ") && !strings.HasPrefix(line, "import (") {
				if importPath := extractImportPath(line); importPath != "" {
					imports[importPath] = true
				}
				continue
			}

			// Start of multi-line import block
			if strings.HasPrefix(line, "import (") {
				inImportBlock = true
				continue
			}

			// End of multi-line import block
			if inImportBlock && line == ")" {
				inImportBlock = false
				continue
			}

			// Line inside import block: "pkg", . "pkg", alias "pkg"
			if inImportBlock {
				if importPath := extractImportPath(line); importPath != "" {
					imports[importPath] = true
				}
			}
		}

		return nil
	})

	return imports, err
}

// extractImportPath extracts a quoted import path from a line.
// Handles: "pkg", . "pkg", alias "pkg"
func extractImportPath(line string) string {
	start := strings.Index(line, "\"")
	end := strings.LastIndex(line, "\"")
	if start >= 0 && end > start {
		return line[start+1 : end]
	}
	return ""
}

// filterExternalImports filters out standard library and local module imports.
// It recognizes external dependencies by:
//   - Domain-based paths (containing a dot, e.g., "github.com/foo/bar") — Go convention
//   - Paths matching existing gala.mod require entries — GALA convention (e.g., "martianoff/gala-server")
func filterExternalImports(imports map[string]bool, modulePath string, requires []mod.Require) map[string]bool {
	// Build a set of known dependency root paths for matching
	knownDeps := make(map[string]bool, len(requires))
	for _, req := range requires {
		knownDeps[req.Path] = true
	}

	external := make(map[string]bool)

	for imp := range imports {
		// Skip current module imports
		if strings.HasPrefix(imp, modulePath+"/") || imp == modulePath {
			continue
		}
		// Skip martianoff/gala imports (internal GALA packages)
		if strings.HasPrefix(imp, "martianoff/gala/") {
			continue
		}
		// Check if this import matches a known dependency from gala.mod
		isKnownDep := false
		for depPath := range knownDeps {
			if imp == depPath || strings.HasPrefix(imp, depPath+"/") {
				isKnownDep = true
				break
			}
		}
		if isKnownDep {
			external[imp] = true
			continue
		}
		// Domain-based paths (containing a dot) are external Go deps
		if strings.Contains(imp, ".") {
			external[imp] = true
			continue
		}
		// Everything else (no dot, not a known dep) is assumed to be stdlib/local
	}

	return external
}

// resolveModuleGraph builds the module graph of galaMod and applies Minimal
// Version Selection to it: each requirement in galaMod is set to its selected
// version, and one no source file imports (requiredPaths) is marked indirect.
// It fails, leaving galaMod as it was, when the graph cannot be built or has a
// cycle (a *graph.CycleError).
func resolveModuleGraph(galaMod *mod.File, requiredPaths map[string]bool, cache *fetch.Cache, fetcher *fetch.GitFetcher) error {
	if len(galaMod.Require) == 0 {
		return nil
	}
	g, err := graph.NewBuilder(cache, fetcher).Build(galaMod)
	if err != nil {
		return fmt.Errorf("failed to build dependency graph: %w", err)
	}
	if err := g.DetectCycles(); err != nil {
		return err
	}

	// Apply MVS
	mvs := graph.NewMVS()
	mvs.AddRequirements(g)
	selected := mvs.Resolve()

	// Update versions in gala.mod, preserving the v prefix if the original had it
	for i, req := range galaMod.Require {
		if ver, ok := selected[req.Path]; ok {
			resolved := ver.String()
			// Preserve the original prefix convention:
			// if the user wrote "v1.0.0", keep "v"; if "1.0.0", strip "v"
			if !strings.HasPrefix(req.Version, "v") {
				resolved = strings.TrimPrefix(resolved, "v")
			}
			galaMod.Require[i].Version = resolved
		}
	}

	// Mark indirect dependencies (but not Go deps - they're already explicit transitive deps)
	for i, req := range galaMod.Require {
		if !requiredPaths[req.Path] && !req.Go {
			galaMod.Require[i].Indirect = true
		}
	}
	return nil
}

// galaSumFor returns the gala.sum for galaMod as tidy leaves it: the content
// hash, and the gala.mod hash when there is one, of each module galaMod
// requires, at the version it requires. Only what the requirements name is
// kept, so an entry for a version no longer required, or a module no longer
// required, is dropped.
//
// A GALA module that is not cached is fetched, and one that cannot be is an
// error: a gala.sum without it would not describe the build. Tidy used to leave
// such a module out without a word, which is how gala.sum fell behind gala.mod.
//
// As in go.sum, a requirement replaced by a local directory has no entry, and
// one replaced by another module is recorded as that module.
//
// A Go module (`// go`) is not fetched for gala.sum: its checksum is go.sum's
// to keep, and its path need not name a Git repository (golang.org/x/...). It
// is recorded when it is cached, and otherwise keeps the entries previous (the
// gala.sum being replaced) has for it.
func galaSumFor(galaMod *mod.File, previous *sum.File, cache *fetch.Cache, fetcher *fetch.GitFetcher) (*sum.File, error) {
	f := sum.NewFile()
	for _, req := range galaMod.Require {
		path, ver := req.Path, req.Version
		if rep := replacementFor(galaMod, req); rep != nil {
			if rep.New.IsLocal() {
				continue
			}
			path, ver = rep.New.Path, rep.New.Version
		}
		if req.Go && !cache.Config().IsCached(path, ver) {
			f.Entries = append(f.Entries, previous.GetModuleEntries(path, ver)...)
			continue
		}
		info, err := fetcher.FetchWithInfo(path, ver)
		if err != nil {
			return nil, fmt.Errorf("cannot record %s@%s in gala.sum: %w", path, ver, err)
		}
		f.Add(path, ver, "", info.Hash)
		if info.GalaModHash != "" {
			f.Add(path, ver, "/gala.mod", info.GalaModHash)
		}
	}
	return f, nil
}

// replacementFor returns the replace directive of galaMod that applies to req:
// one for its path at its version, or for its path at any version.
func replacementFor(galaMod *mod.File, req mod.Require) *mod.Replace {
	for i, rep := range galaMod.Replace {
		if rep.Old.Path == req.Path && (rep.Old.Version == "" || rep.Old.Version == req.Version) {
			return &galaMod.Replace[i]
		}
	}
	return nil
}

// Helper to check if a version string is valid
func isValidVersion(v string) bool {
	_, err := version.Parse(v)
	return err == nil
}
