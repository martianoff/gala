package build

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Mode distinguishes the workspaces one project can have.
//
// `gala build` and `gala test` generate different trees from the same sources:
// test adds a generated runner main and compiles a library as package main. A
// workspace per command keeps the two from racing on one tree, and is cheaper
// than locking for the common case of building and testing at once. See
// lock.go's header for what that race did.
type Mode string

const (
	// ModeBuild is the workspace for `gala build` and `gala run`.
	ModeBuild Mode = "build"
	// ModeTest is the workspace for `gala test`.
	ModeTest Mode = "test"
)

// Workspace represents a build workspace for a GALA project.
type Workspace struct {
	// Config is the build configuration.
	Config *Config

	// ProjectDir is the absolute path to the project directory (where gala.mod is).
	ProjectDir string

	// Mode is the command family this workspace serves; see Mode.
	Mode Mode

	// Hash is the unique identifier for this workspace (based on ProjectDir).
	Hash string

	// Dir is the absolute path to the workspace directory.
	Dir string

	// GenDir is where generated .go files are placed.
	GenDir string

	// DepsDir is where transpiled GALA dependency .go files are placed.
	DepsDir string

	// GoModPath is the path to the generated go.mod file.
	GoModPath string

	// GoSumPath is the path to the generated go.sum file.
	GoSumPath string
}

// NewWorkspace creates a new workspace for the given project directory and
// mode. The projectDir should be the directory containing gala.mod.
func NewWorkspace(config *Config, projectDir string, mode Mode) (*Workspace, error) {
	absProjectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, fmt.Errorf("resolving project path: %w", err)
	}

	// Compute hash from absolute path
	hash := computeHash(absProjectDir)

	workspaceDir := filepath.Join(config.BuildDir, workspaceDirName(hash, mode))

	return &Workspace{
		Config:     config,
		ProjectDir: absProjectDir,
		Mode:       mode,
		Hash:       hash,
		Dir:        workspaceDir,
		GenDir:     filepath.Join(workspaceDir, "gen"),
		DepsDir:    filepath.Join(workspaceDir, "deps"),
		GoModPath:  filepath.Join(workspaceDir, "go.mod"),
		GoSumPath:  filepath.Join(workspaceDir, "go.sum"),
	}, nil
}

// workspaceDirName names the per-mode workspace directory. Build keeps the bare
// hash, so workspaces created before modes existed stay valid and anything that
// learned the path still resolves; every other mode takes a suffix.
func workspaceDirName(hash string, mode Mode) string {
	if mode == ModeBuild || mode == "" {
		return hash
	}
	return hash + "-" + string(mode)
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
		w.DepsDir,
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
//
// It takes the lock first. Deleting a workspace a build is using is the same
// corruption the lock exists to prevent — worse, actually, since it removes the
// running build's lock file along with its gen tree, so the next build sees an
// unlocked workspace and starts writing into the wreckage. A workspace that is
// busy is reported rather than emptied.
func (w *Workspace) Clean() error {
	removed, err := removeWorkspaceDir(w.Dir)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("%w: %s\nheld by: %s\nA build is using this workspace — wait for it to finish, or stop it",
			ErrLockBusy, w.Dir, holderDescription(filepath.Join(w.Dir, lockFileName)))
	}
	return nil
}

// removeWorkspaceDir deletes one workspace under its lock, and reports whether
// it did. A workspace another gala process holds is left alone: false, no error.
//
// The lock is Discarded rather than Released — the file is gone with the
// directory, and removing it by path could unlink a lock a later process has
// already taken.
func removeWorkspaceDir(dir string) (bool, error) {
	// A silent notice: a held workspace is skipped by design (false, no error),
	// so announcing the wait would report a non-event as a problem.
	lock, err := lockDir(dir, cleanLockTimeout, lockNotice{})
	if err != nil {
		if errors.Is(err, ErrLockBusy) {
			return false, nil
		}
		return false, err
	}
	defer lock.Discard()

	if err := os.RemoveAll(dir); err != nil {
		return false, err
	}
	return true, nil
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

// MainPackageDirs returns every directory under GenDir that holds a
// `package main`, relative to GenDir and slash-separated. The gen root is
// excluded: it is the project's own package, which callers check separately.
//
// Directories mirroring a nested Go module — one carrying its own go.mod in the
// project — are skipped. Their sources are copied into the workspace but they
// resolve dependencies through their own module, so they are not buildable here
// and must never be offered as a build target.
//
// TODO: that exclusion belongs at the copy step (copyNonGalaFiles), which today
// copies a nested module's sources in while dropping its go.mod, silently
// absorbing it into the workspace module. Skipping it here only stops us
// offering a target that cannot build; the sources are still compiled by
// `go build ./gen/...`.
func (w *Workspace) MainPackageDirs() ([]string, error) {
	var dirs []string
	err := filepath.WalkDir(w.GenDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(w.GenDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil // the project's own package — the caller's business
		}
		if strings.HasPrefix(d.Name(), ".") || d.Name() == "vendor" {
			return filepath.SkipDir
		}
		if _, err := os.Stat(filepath.Join(w.ProjectDir, rel, "go.mod")); err == nil {
			return filepath.SkipDir // a nested module, not part of this build
		}
		if PackageNameIn(path) == "main" {
			dirs = append(dirs, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sort.Strings(dirs)
	return dirs, nil
}

// PackageNameIn returns the Go package name declared by the .go files directly
// in dir, or "" when dir holds no Go source. Only the first package clause
// found is consulted — a directory is a single Go package by construction.
func PackageNameIn(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		if name := detectPackageName(filepath.Join(dir, entry.Name())); name != "" {
			return name
		}
	}
	return ""
}

// CleanDeps removes all files from the deps directory.
func (w *Workspace) CleanDeps() error {
	// deps/ holds transpiled .go files and is open to the same "file is held by
	// another process" failure as gen/, so it gets the same retry.
	if err := removeWithRetry(w.DepsDir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.MkdirAll(w.DepsDir, 0755)
}

// DepModuleDir returns the directory for a transpiled dependency module.
func (w *Workspace) DepModuleDir(modulePath, version string) string {
	return filepath.Join(w.DepsDir, modulePath+"@"+version)
}

// CleanGen removes all files and subdirectories from the gen directory.
func (w *Workspace) CleanGen() error {
	if err := removeWithRetry(w.GenDir); err != nil {
		return fmt.Errorf(
			"could not clear the workspace's gen directory (%s): %w\n"+
				"A file in it is still held open by another process. Retry the build; if it\n"+
				"persists, close whatever is using the file, or:\n%s",
			w.GenDir, err, buildDirHint)
	}
	return os.MkdirAll(w.GenDir, 0755)
}

// removeWithRetry deletes a tree, retrying briefly before giving up.
//
// On Windows a file another process holds open cannot be unlinked, and the
// delete fails with "The process cannot access the file because it is being
// used by another process". Moving the directory aside instead does not help:
// renaming a directory that contains an open file is refused the same way.
//
// What does help is waiting. The holders that show up in practice — a test
// binary that has just exited, a virus scanner, the file indexer — release
// within milliseconds, so a short backoff clears almost all of them. A holder
// that outlasts the backoff is a real problem the caller must report rather
// than silently build around, because a stale gen tree compiles stale code.
func removeWithRetry(dir string) error {
	const ms = time.Millisecond

	var err error
	for _, delay := range []time.Duration{0, 20 * ms, 50 * ms, 100 * ms, 200 * ms, 400 * ms, 800 * ms} {
		time.Sleep(delay) // RemoveAll already reports success for a path that is not there
		if err = os.RemoveAll(dir); err == nil {
			return nil
		}
	}
	return err
}

// FindWorkspacesByProject finds every existing workspace for a project path —
// one per mode. Cleaning a project means removing all of them, so this returns
// a slice rather than the build workspace alone.
func FindWorkspacesByProject(config *Config, projectDir string) ([]*Workspace, error) {
	absProjectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, err
	}

	var found []*Workspace
	for _, mode := range []Mode{ModeBuild, ModeTest} {
		ws, wsErr := NewWorkspace(config, absProjectDir, mode)
		if wsErr != nil {
			return nil, wsErr
		}
		if ws.Exists() {
			found = append(found, ws)
		}
	}

	if len(found) == 0 {
		return nil, fmt.Errorf("workspace not found for project: %s", projectDir)
	}
	return found, nil
}

// CleanAllWorkspaces removes all build workspaces.
// It removes each workspace under its own lock, so a sweep cannot delete a
// running build's tree. Workspaces that are busy are counted and left; one
// active build should not stop the other forty from being cleaned.
func CleanAllWorkspaces(config *Config) (removed int, busy int, err error) {
	return sweepWorkspaces(config, func(string, os.FileInfo) bool { return true })
}

// CleanStaleWorkspaces removes workspaces older than the given duration, each
// under its own lock. Busy workspaces are counted and left — and a workspace
// with a live build is by definition not stale, whatever its marker says.
func CleanStaleWorkspaces(config *Config, maxAge time.Duration) (removed int, busy int, err error) {
	return sweepWorkspaces(config, func(dir string, marker os.FileInfo) bool {
		if marker == nil {
			return true // no marker: not a workspace this tool wrote, or a half-made one
		}
		return time.Since(marker.ModTime()) > maxAge
	})
}

// sweepWorkspaces removes every workspace directory under BuildDir that `want`
// selects, each under its own lock. It is the shared body of the --all and
// --stale sweeps, which differ only in that predicate.
//
// `want` receives the workspace directory and its .gala-workspace marker, or
// nil when there is no readable marker.
func sweepWorkspaces(config *Config, want func(dir string, marker os.FileInfo) bool) (removed int, busy int, err error) {
	entries, err := os.ReadDir(config.BuildDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		workspaceDir := filepath.Join(config.BuildDir, entry.Name())
		marker, statErr := os.Stat(filepath.Join(workspaceDir, ".gala-workspace"))
		if statErr != nil {
			marker = nil
		}
		if !want(workspaceDir, marker) {
			continue
		}

		ok, rmErr := removeWorkspaceDir(workspaceDir)
		if rmErr != nil {
			return removed, busy, rmErr
		}
		if ok {
			removed++
		} else {
			busy++
		}
	}

	return removed, busy, nil
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
