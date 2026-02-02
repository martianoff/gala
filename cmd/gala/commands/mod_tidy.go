package commands

import (
	"fmt"
	"io"
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

	// Copy GALA dependencies to local _gala/deps folder
	if err := copyGalaDepsToLocal(galaMod, cache); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to copy GALA deps locally: %v\n", err)
	}

	// Sync go.mod with Go dependencies from gala.mod
	if err := syncGoMod(galaMod); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to sync go.mod: %v\n", err)
	}

	if len(missing) == 0 && len(unused) == 0 {
		fmt.Println("All dependencies are up to date.")
	} else {
		fmt.Println("Done.")
	}
}

// syncGoMod creates or updates go.mod with Go dependencies from gala.mod.
// This ensures Bazel/gazelle can resolve transitive Go dependencies.
// It also adds replace directives for GALA dependencies pointing to _gala/deps/.
func syncGoMod(galaMod *mod.File) error {
	// Collect Go dependencies from gala.mod (marked with Go flag)
	var goDeps []mod.Require
	// Collect GALA dependencies (not Go deps)
	var galaDeps []mod.Require
	for _, req := range galaMod.Require {
		if req.Go {
			goDeps = append(goDeps, req)
		} else {
			galaDeps = append(galaDeps, req)
		}
	}

	goModPath := "go.mod"
	const galaHeader = "// Code generated by gala mod tidy. DO NOT EDIT this section.\n"
	const galaMarkerStart = "// GALA-managed Go dependencies below. DO NOT EDIT."
	const galaMarkerEnd = "// End GALA-managed dependencies."
	const galaPkgMarkerStart = "// GALA package dependencies below. DO NOT EDIT."
	const galaPkgMarkerEnd = "// End GALA package dependencies."

	// Read existing go.mod or create new one
	var existingContent string
	if content, err := os.ReadFile(goModPath); err == nil {
		existingContent = string(content)
	}

	// Remove any existing GALA-managed section (including header)
	// Check for old-style markers too for backwards compatibility
	oldMarkerStart := "// GALA-managed dependencies - DO NOT EDIT below this line"
	oldMarkerEnd := "// End GALA-managed dependencies"
	for _, startMarker := range []string{galaMarkerStart, oldMarkerStart} {
		if startIdx := strings.Index(existingContent, startMarker); startIdx != -1 {
			for _, endMarker := range []string{galaMarkerEnd, oldMarkerEnd} {
				if endIdx := strings.Index(existingContent, endMarker); endIdx != -1 {
					existingContent = existingContent[:startIdx] + existingContent[endIdx+len(endMarker):]
					existingContent = strings.TrimRight(existingContent, "\n") + "\n"
					break
				}
			}
		}
	}

	// Remove all existing GALA package dependencies sections (there may be duplicates from prior bugs)
	for {
		startIdx := strings.Index(existingContent, galaPkgMarkerStart)
		if startIdx == -1 {
			break
		}
		endIdx := strings.Index(existingContent[startIdx:], galaPkgMarkerEnd)
		if endIdx == -1 {
			break
		}
		existingContent = existingContent[:startIdx] + existingContent[startIdx+endIdx+len(galaPkgMarkerEnd):]
		existingContent = strings.TrimRight(existingContent, "\n") + "\n"
	}

	// Remove generated header if present
	existingContent = strings.TrimPrefix(existingContent, galaHeader)

	// If no existing go.mod, create base content
	if existingContent == "" {
		existingContent = fmt.Sprintf("module %s\n\ngo 1.21\n", galaMod.Module.Path)
	}

	// Remove existing require and replace entries for GALA deps
	// (they'll be re-added with relative paths)
	for _, dep := range galaDeps {
		// Remove require line
		existingContent = strings.ReplaceAll(existingContent, fmt.Sprintf("require %s %s\n", dep.Path, dep.Version), "")
		existingContent = strings.ReplaceAll(existingContent, fmt.Sprintf("require %s %s\r\n", dep.Path, dep.Version), "")
		// Remove replace lines (any version, any path - they could be absolute)
		lines := strings.Split(existingContent, "\n")
		var newLines []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "replace "+dep.Path) {
				continue // Remove this replace directive
			}
			newLines = append(newLines, line)
		}
		existingContent = strings.Join(newLines, "\n")
	}

	// Remove any existing require entries for deps we'll manage
	// This prevents duplicates when deps are already in go.mod
	for _, dep := range goDeps {
		// Remove single-line require
		existingContent = strings.ReplaceAll(existingContent, fmt.Sprintf("require %s %s", dep.Path, dep.Version), "")
		// Remove from require block - match the dep line
		lines := strings.Split(existingContent, "\n")
		var newLines []string
		inRequireBlock := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "require (" {
				inRequireBlock = true
			}
			if inRequireBlock && trimmed == ")" {
				inRequireBlock = false
			}
			// Skip lines that contain the managed dep path
			if inRequireBlock && strings.Contains(trimmed, dep.Path) {
				continue
			}
			newLines = append(newLines, line)
		}
		existingContent = strings.Join(newLines, "\n")
	}

	// Clean up empty require blocks
	existingContent = strings.ReplaceAll(existingContent, "require (\n)", "")
	existingContent = strings.ReplaceAll(existingContent, "\n\n\n", "\n\n")

	// Build the new content
	var sb strings.Builder
	sb.WriteString(strings.TrimRight(existingContent, "\n"))

	// Add GALA package dependencies with relative replace directives
	if len(galaDeps) > 0 {
		sb.WriteString("\n\n")
		sb.WriteString("// GALA package dependencies below. DO NOT EDIT.\n")
		sb.WriteString("// Code generated by gala mod tidy. DO NOT EDIT.\n")
		for _, dep := range galaDeps {
			sb.WriteString(fmt.Sprintf("require %s %s\n", dep.Path, dep.Version))
		}
		sb.WriteString("\n")
		for _, dep := range galaDeps {
			safeName := strings.ReplaceAll(dep.Path, "/", "_") + "@" + dep.Version
			sb.WriteString(fmt.Sprintf("replace %s => ./_gala/deps/%s\n", dep.Path, safeName))
		}
		sb.WriteString("// End GALA package dependencies.\n")
	}

	// Add GALA-managed require block if we have Go deps
	if len(goDeps) > 0 {
		sb.WriteString("\n")
		sb.WriteString(galaMarkerStart)
		sb.WriteString("\n// Code generated by gala mod tidy. DO NOT EDIT.\n")
		sb.WriteString("require (\n")
		for _, dep := range goDeps {
			sb.WriteString(fmt.Sprintf("\t%s %s // indirect\n", dep.Path, dep.Version))
		}
		sb.WriteString(")\n")
		sb.WriteString(galaMarkerEnd)
	}
	sb.WriteString("\n")

	// Write updated go.mod
	if err := os.WriteFile(goModPath, []byte(sb.String()), 0644); err != nil {
		return err
	}

	// Sync go.sum - only fetch deps not already in go.sum
	if len(goDeps) > 0 {
		fmt.Println("Synced go.mod with Go dependencies from gala.mod")

		// Read existing go.sum to check which deps need fetching
		existingSum := make(map[string]bool)
		if sumContent, err := os.ReadFile("go.sum"); err == nil {
			for _, line := range strings.Split(string(sumContent), "\n") {
				parts := strings.Fields(line)
				if len(parts) >= 2 && !strings.HasSuffix(parts[0], "/go.mod") {
					existingSum[parts[0]+" "+parts[1]] = true
				}
			}
		}

		// Fetch only missing deps
		for _, dep := range goDeps {
			key := dep.Path + " " + dep.Version
			if !existingSum[key] {
				cmd := exec.Command("go", "get", dep.Path+"@"+dep.Version)
				cmd.Stderr = os.Stderr
				_ = cmd.Run()
			}
		}
		fmt.Println("Updated go.sum")
	}

	return nil
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
			if name != "." && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" || strings.HasPrefix(name, "bazel-")) {
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

// copyGalaDepsToLocal copies GALA dependencies from global cache to _gala/deps/ folder.
// This makes the project portable by using relative paths instead of absolute ones.
func copyGalaDepsToLocal(galaMod *mod.File, cache *fetch.Cache) error {
	depsDir := filepath.Join("_gala", "deps")

	for _, req := range galaMod.Require {
		// Skip Go dependencies
		if req.Go {
			continue
		}

		// Source: global cache
		srcDir := cache.Config().ModulePath(req.Path, req.Version)
		if _, err := os.Stat(srcDir); err != nil {
			continue // Not cached, skip
		}

		// Destination: _gala/deps/<path>@<version>
		// Use a safe directory name
		safeName := strings.ReplaceAll(req.Path, "/", "_") + "@" + req.Version
		dstDir := filepath.Join(depsDir, safeName)

		// Remove existing copy if present
		os.RemoveAll(dstDir)

		// Create destination directory
		if err := os.MkdirAll(dstDir, 0755); err != nil {
			return fmt.Errorf("creating dep dir %s: %w", dstDir, err)
		}

		// Copy all files (not subdirs like _gala within the cached package)
		if err := copyDirContents(srcDir, dstDir); err != nil {
			return fmt.Errorf("copying %s: %w", req.Path, err)
		}

		fmt.Printf("Copied %s@%s to %s\n", req.Path, req.Version, dstDir)
	}

	return nil
}

// copyDirContents copies files from src to dst, excluding _gala subdirectory.
func copyDirContents(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		// Skip _gala directory (stdlib is provided by consumer)
		if entry.Name() == "_gala" {
			continue
		}

		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, 0755); err != nil {
				return err
			}
			if err := copyDirContents(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}

// copyFile copies a single file from src to dst.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// Helper to check if a version string is valid
func isValidVersion(v string) bool {
	_, err := version.Parse(v)
	return err == nil
}
