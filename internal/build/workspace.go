package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Workspace represents a build workspace for a GALA project.
type Workspace struct {
	// Config is the build configuration.
	Config *Config

	// ProjectDir is the absolute path to the project directory (where gala.mod is).
	ProjectDir string

	// Hash is the unique identifier for this workspace (based on ProjectDir).
	Hash string

	// Dir is the absolute path to the workspace directory.
	Dir string

	// GenDir is where generated .go files are placed.
	GenDir string

	// GoModPath is the path to the generated go.mod file.
	GoModPath string

	// GoSumPath is the path to the generated go.sum file.
	GoSumPath string
}

// NewWorkspace creates a new workspace for the given project directory.
// The projectDir should be the directory containing gala.mod.
func NewWorkspace(config *Config, projectDir string) (*Workspace, error) {
	absProjectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, fmt.Errorf("resolving project path: %w", err)
	}

	// Compute hash from absolute path
	hash := computeHash(absProjectDir)

	workspaceDir := filepath.Join(config.BuildDir, hash)

	return &Workspace{
		Config:     config,
		ProjectDir: absProjectDir,
		Hash:       hash,
		Dir:        workspaceDir,
		GenDir:     filepath.Join(workspaceDir, "gen"),
		GoModPath:  filepath.Join(workspaceDir, "go.mod"),
		GoSumPath:  filepath.Join(workspaceDir, "go.sum"),
	}, nil
}

// computeHash computes a short hash from the project path.
// Uses SHA256 truncated to 12 hex characters.
func computeHash(projectDir string) string {
	// Normalize path separators for consistent hashing across platforms
	normalized := strings.ReplaceAll(projectDir, "\\", "/")
	normalized = strings.ToLower(normalized) // Case-insensitive for Windows

	h := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(h[:])[:12]
}

// Ensure creates the workspace directory structure.
func (w *Workspace) Ensure() error {
	// Create workspace directories
	dirs := []string{
		w.Dir,
		w.GenDir,
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("creating workspace dir %s: %w", dir, err)
		}
	}

	// Write a marker file with project info (for debugging/cleanup)
	markerPath := filepath.Join(w.Dir, ".gala-workspace")
	markerContent := fmt.Sprintf("project=%s\ncreated=%s\n",
		w.ProjectDir,
		time.Now().Format(time.RFC3339))

	if err := os.WriteFile(markerPath, []byte(markerContent), 0644); err != nil {
		return fmt.Errorf("writing workspace marker: %w", err)
	}

	return nil
}

// Clean removes the workspace directory.
func (w *Workspace) Clean() error {
	return os.RemoveAll(w.Dir)
}

// Exists returns true if the workspace directory exists.
func (w *Workspace) Exists() bool {
	info, err := os.Stat(w.Dir)
	return err == nil && info.IsDir()
}

// WriteGenFile writes a generated Go file to the workspace.
func (w *Workspace) WriteGenFile(filename string, content []byte) error {
	filePath := filepath.Join(w.GenDir, filename)
	return os.WriteFile(filePath, content, 0644)
}

// GenFiles returns all .go files in the gen directory.
func (w *Workspace) GenFiles() ([]string, error) {
	entries, err := os.ReadDir(w.GenDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
			files = append(files, filepath.Join(w.GenDir, entry.Name()))
		}
	}

	return files, nil
}

// CleanGen removes all files from the gen directory.
func (w *Workspace) CleanGen() error {
	entries, err := os.ReadDir(w.GenDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if err := os.Remove(filepath.Join(w.GenDir, entry.Name())); err != nil {
			return err
		}
	}

	return nil
}

// FindWorkspaceByProject finds an existing workspace for a project path.
func FindWorkspaceByProject(config *Config, projectDir string) (*Workspace, error) {
	absProjectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, err
	}

	hash := computeHash(absProjectDir)
	workspaceDir := filepath.Join(config.BuildDir, hash)

	if info, err := os.Stat(workspaceDir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("workspace not found for project: %s", projectDir)
	}

	return &Workspace{
		Config:     config,
		ProjectDir: absProjectDir,
		Hash:       hash,
		Dir:        workspaceDir,
		GenDir:     filepath.Join(workspaceDir, "gen"),
		GoModPath:  filepath.Join(workspaceDir, "go.mod"),
		GoSumPath:  filepath.Join(workspaceDir, "go.sum"),
	}, nil
}

// CleanAllWorkspaces removes all build workspaces.
func CleanAllWorkspaces(config *Config) error {
	return os.RemoveAll(config.BuildDir)
}

// CleanStaleWorkspaces removes workspaces older than the given duration.
func CleanStaleWorkspaces(config *Config, maxAge time.Duration) (int, error) {
	entries, err := os.ReadDir(config.BuildDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	cleaned := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		workspaceDir := filepath.Join(config.BuildDir, entry.Name())
		markerPath := filepath.Join(workspaceDir, ".gala-workspace")

		info, err := os.Stat(markerPath)
		if err != nil {
			// No marker file, remove workspace
			os.RemoveAll(workspaceDir)
			cleaned++
			continue
		}

		if time.Since(info.ModTime()) > maxAge {
			os.RemoveAll(workspaceDir)
			cleaned++
		}
	}

	return cleaned, nil
}

// ListWorkspaces returns all existing workspaces with their project paths.
func ListWorkspaces(config *Config) (map[string]string, error) {
	entries, err := os.ReadDir(config.BuildDir)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]string), nil
		}
		return nil, err
	}

	result := make(map[string]string)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		workspaceDir := filepath.Join(config.BuildDir, entry.Name())
		markerPath := filepath.Join(workspaceDir, ".gala-workspace")

		content, err := os.ReadFile(markerPath)
		if err != nil {
			continue
		}

		// Parse project path from marker
		for _, line := range strings.Split(string(content), "\n") {
			if strings.HasPrefix(line, "project=") {
				result[entry.Name()] = strings.TrimPrefix(line, "project=")
				break
			}
		}
	}

	return result, nil
}
