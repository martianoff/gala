package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

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
	externalImports := filterExternalImports(imports, galaMod.Module.Path)

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
	if len(galaMod.Require) > 0 {
		builder := graph.NewBuilder(cache, fetcher)
		g, err := builder.Build(galaMod)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to build dependency graph: %v\n", err)
		} else {
			// Check for cycles
			if cycleErr := g.DetectCycles(); cycleErr != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", cycleErr)
				os.Exit(1)
			}

			// Apply MVS
			mvs := graph.NewMVS()
			mvs.AddRequirements(g)
			selected := mvs.Resolve()

			// Update versions in gala.mod
			for i, req := range galaMod.Require {
				if ver, ok := selected[req.Path]; ok {
					galaMod.Require[i].Version = ver.String()
				}
			}

			// Mark indirect dependencies (but not Go deps - they're already explicit transitive deps)
			directPaths := make(map[string]bool)
			for path := range requiredPaths {
				directPaths[path] = true
			}
			for i, req := range galaMod.Require {
				if !directPaths[req.Path] && !req.Go {
					galaMod.Require[i].Indirect = true
				}
			}
		}
	}

	// Write updated gala.mod
	if err := mod.WriteFile(galaMod, "gala.mod"); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing gala.mod: %v\n", err)
		os.Exit(1)
	}

	// Update gala.sum
	if err := updateGalaSum(galaMod, cache); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to update gala.sum: %v\n", err)
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

	// Remove all GALA-generated sections from existing content
	markers := []struct{ start, end string }{
		{"// GALA-managed Go dependencies below. DO NOT EDIT.", "// End GALA-managed dependencies."},
		{"// GALA package dependencies below. DO NOT EDIT.", "// End GALA package dependencies."},
		{"// GALA stdlib dependencies below. DO NOT EDIT.", "// End GALA stdlib dependencies."},
		{"// GALA dependencies below. DO NOT EDIT.", "// End GALA dependencies."},
	}

	for _, m := range markers {
		for {
			startIdx := strings.Index(existingContent, m.start)
			if startIdx == -1 {
				break
			}
			endIdx := strings.Index(existingContent[startIdx:], m.end)
			if endIdx == -1 {
				break
			}
			existingContent = existingContent[:startIdx] + existingContent[startIdx+endIdx+len(m.end):]
		}
	}

	// Clean up
	existingContent = strings.TrimSpace(existingContent)
	if existingContent == "" {
		existingContent = fmt.Sprintf("module %s\n\ngo 1.21", galaMod.Module.Path)
	}

	// Build new content
	var sb strings.Builder
	sb.WriteString(existingContent)

	// Add Go dependencies if any
	if len(goDeps) > 0 {
		sb.WriteString("\n\n")
		sb.WriteString("// GALA-managed Go dependencies below. DO NOT EDIT.\n")
		sb.WriteString("// Generated from gala.mod by 'gala mod tidy'.\n")
		sb.WriteString("require (\n")
		for _, dep := range goDeps {
			sb.WriteString(fmt.Sprintf("\t%s %s\n", dep.Path, dep.Version))
		}
		sb.WriteString(")\n")
		sb.WriteString("// End GALA-managed dependencies.\n")
	}

	if err := os.WriteFile(goModPath, []byte(sb.String()), 0644); err != nil {
		return err
	}

	fmt.Println("Generated go.mod for Bazel (Go dependencies only)")

	// Generate go.sum by running 'go mod download -json' to get checksums
	if len(goDeps) > 0 {
		if err := generateGoSum(goSumPath, goDeps); err != nil {
			return fmt.Errorf("generating go.sum: %w", err)
		}
		fmt.Println("Generated go.sum for Bazel")
	}

	return nil
}

// generateGoSum generates go.sum file by downloading Go modules and getting their checksums.
func generateGoSum(goSumPath string, goDeps []mod.Require) error {
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

	// Parse JSON output to build go.sum entries
	var sumEntries []string
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	for decoder.More() {
		var info struct {
			Path    string `json:"Path"`
			Version string `json:"Version"`
			Sum     string `json:"Sum"`
			GoMod   string `json:"GoMod"`
			GoModSum string `json:"GoModSum"`
		}
		if err := decoder.Decode(&info); err != nil {
			continue
		}
		if info.Sum != "" {
			sumEntries = append(sumEntries, fmt.Sprintf("%s %s %s", info.Path, info.Version, info.Sum))
		}
		if info.GoModSum != "" {
			sumEntries = append(sumEntries, fmt.Sprintf("%s %s/go.mod %s", info.Path, info.Version, info.GoModSum))
		}
	}

	if len(sumEntries) == 0 {
		// If no entries from JSON, run go mod tidy to generate go.sum
		tidyCmd := exec.Command("go", "mod", "tidy")
		return tidyCmd.Run()
	}

	// Write go.sum
	sumContent := strings.Join(sumEntries, "\n") + "\n"
	return os.WriteFile(goSumPath, []byte(sumContent), 0644)
}

// scanImports scans all .gala files in the directory tree for import statements.
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

		// Simple import extraction (look for import "..." statements)
		lines := strings.Split(string(content), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "import ") {
				// Extract import path
				start := strings.Index(line, "\"")
				end := strings.LastIndex(line, "\"")
				if start >= 0 && end > start {
					importPath := line[start+1 : end]
					imports[importPath] = true
				}
			}
		}

		return nil
	})

	return imports, err
}

// filterExternalImports filters out standard library and local module imports.
func filterExternalImports(imports map[string]bool, modulePath string) map[string]bool {
	external := make(map[string]bool)

	for imp := range imports {
		// Skip standard library (no dots in path)
		if !strings.Contains(imp, ".") {
			continue
		}
		// Skip current module imports
		if strings.HasPrefix(imp, modulePath+"/") || imp == modulePath {
			continue
		}
		// Skip martianoff/gala imports (internal GALA packages)
		if strings.HasPrefix(imp, "martianoff/gala/") {
			continue
		}
		// This is an external dependency
		external[imp] = true
	}

	return external
}

// updateGalaSum updates the gala.sum file with hashes for all dependencies.
func updateGalaSum(galaMod *mod.File, cache *fetch.Cache) error {
	var entries []sum.Entry

	for _, req := range galaMod.Require {
		// Get hash for the module directory
		hash, err := cache.Hash(req.Path, req.Version)
		if err != nil {
			continue // Skip if not cached
		}
		entries = append(entries, sum.Entry{
			Path:    req.Path,
			Version: req.Version,
			Hash:    hash,
		})

		// Get hash for gala.mod
		modDir := cache.Config().ModulePath(req.Path, req.Version)
		galaModPath := filepath.Join(modDir, "gala.mod")
		if _, err := os.Stat(galaModPath); err == nil {
			modHash, err := sum.HashFile(galaModPath)
			if err == nil {
				entries = append(entries, sum.Entry{
					Path:    req.Path,
					Version: req.Version,
					Suffix:  "/gala.mod",
					Hash:    modHash,
				})
			}
		}
	}

	sumFile := &sum.File{Entries: entries}
	return sum.WriteFile(sumFile, "gala.sum")
}

// Helper to check if a version string is valid
func isValidVersion(v string) bool {
	_, err := version.Parse(v)
	return err == nil
}
