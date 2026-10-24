package analyzer

import (
	"os"
	"path/filepath"
	"sync"
)

// pathCache memoizes symlink resolution for one analyzer.
//
// The analyzer compares file paths constantly — every sibling's path on every
// Analyze call, every type's DefinedIn against the file being compiled — and
// each comparison resolves symlinks first. filepath.EvalSymlinks costs a
// system call per path component, and on Windows each of those is a
// FindFirstFile, so the same handful of paths resolved over and over was most
// of the analyzer's CPU time there.
//
// The file tree is fixed for the duration of a build, so a resolved path stays
// valid. The cache lives as long as its analyzer: in the Bazel worker that is
// one build (workers quit after each), the same lifetime as the analyzed
// package graph the worker already reuses on that assumption. Only successful resolutions are kept: a path that does not exist yet
// is resolved again next time, so a file created later in a long-lived
// analyzer (the LSP) is still found.
//
// filepath.Abs is cached too: on Windows it is a GetFullPathName call even
// for a path that is already absolute, and the analyzer makes it for every
// type it compares. Only absolute inputs are cached, since a relative path's
// result depends on the working directory.
//
// A nil *pathCache is valid and resolves without caching.
type pathCache struct {
	mu   sync.Mutex
	abs  map[string]string // absolute path as written -> filepath.Abs of it
	real map[string]string // absolute path -> path with symlinks resolved
}

func newPathCache() *pathCache {
	return &pathCache{abs: make(map[string]string), real: make(map[string]string)}
}

// absPath is filepath.Abs, cached for paths that are already absolute.
func (c *pathCache) absPath(path string) (string, error) {
	if c == nil || !filepath.IsAbs(path) {
		return filepath.Abs(path)
	}
	c.mu.Lock()
	abs, ok := c.abs[path]
	c.mu.Unlock()
	if ok {
		return abs, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.abs[path] = abs
	c.mu.Unlock()
	return abs, nil
}

// evalSymlinks is filepath.EvalSymlinks, cached. abs must be absolute.
func (c *pathCache) evalSymlinks(abs string) (string, error) {
	if c == nil {
		return filepath.EvalSymlinks(abs)
	}
	c.mu.Lock()
	real, ok := c.real[abs]
	c.mu.Unlock()
	if ok {
		return real, nil
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.real[abs] = real
	c.mu.Unlock()
	return real, nil
}

// canonical returns path made absolute with its symlinks resolved. It falls
// back to the absolute path when symlinks cannot be resolved, and to path
// itself when it cannot be made absolute.
func (c *pathCache) canonical(path string) string {
	abs, err := c.absPath(path)
	if err != nil {
		return path
	}
	real, err := c.evalSymlinks(abs)
	if err != nil {
		return abs
	}
	return real
}

// sameFile reports whether two paths refer to the same file. It handles
// relative and absolute paths and symlinked paths (Bazel sandboxes and
// local_path_override reach the same file through a symlink and its target).
//
// Fast paths, with no system calls beyond (cached) filepath.Abs:
//
//  1. The absolute paths are equal: same file.
//  2. The base names differ: not the same file. Only a symlink to a file of a
//     different name could break this, which does not happen in practice, and
//     skipping resolution here avoids two system calls per sibling every time
//     the analyzer tells file_01.gala from file_02.gala.
//
// Otherwise it compares the resolved paths, and if either cannot be resolved,
// falls back to os.SameFile.
func (c *pathCache) sameFile(pathA, pathB string) bool {
	if pathA == "" || pathB == "" {
		return false
	}
	absA, err := c.absPath(pathA)
	if err != nil {
		absA = pathA
	}
	absB, err := c.absPath(pathB)
	if err != nil {
		absB = pathB
	}
	if absA == absB {
		return true
	}
	if filepath.Base(absA) != filepath.Base(absB) {
		return false
	}
	realA, errA := c.evalSymlinks(absA)
	realB, errB := c.evalSymlinks(absB)
	if errA == nil && errB == nil {
		return realA == realB
	}
	infoA, errA := os.Stat(absA)
	infoB, errB := os.Stat(absB)
	if errA == nil && errB == nil {
		return os.SameFile(infoA, infoB)
	}
	return false
}
