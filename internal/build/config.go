// Package build provides build workspace management for GALA projects.
package build

import (
	"os"
	"path/filepath"
)

// Config holds configuration for the build system.
type Config struct {
	// GalaHome is the root directory for GALA data.
	// Defaults to ~/.gala
	GalaHome string

	// BuildDir is where build workspaces are created.
	// Defaults to GalaHome/build
	BuildDir string

	// StdlibDir is where the standard library is cached.
	// Defaults to GalaHome/stdlib
	StdlibDir string

	// GoPkgDir is where Go dependencies are cached (GOMODCACHE).
	// Defaults to GalaHome/go/pkg/mod
	GoPkgDir string

	// GalaPkgDir is where GALA dependencies are cached.
	// Defaults to GalaHome/pkg/mod
	GalaPkgDir string
}

// DefaultConfig returns the default build configuration.
func DefaultConfig() *Config {
	galaHome := defaultGalaHome()
	return &Config{
		GalaHome:   galaHome,
		BuildDir:   filepath.Join(galaHome, "build"),
		StdlibDir:  filepath.Join(galaHome, "stdlib"),
		GoPkgDir:   filepath.Join(galaHome, "go", "pkg", "mod"),
		GalaPkgDir: filepath.Join(galaHome, "pkg", "mod"),
	}
}

// defaultGalaHome returns the default GALA home directory.
// Uses GALA_HOME environment variable if set, otherwise ~/.gala
func defaultGalaHome() string {
	if dir := os.Getenv("GALA_HOME"); dir != "" {
		return dir
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		// Fall back to current directory
		return filepath.Join(".", ".gala")
	}

	return filepath.Join(homeDir, ".gala")
}

// EnsureDirs creates all necessary directories.
func (c *Config) EnsureDirs() error {
	dirs := []string{
		c.GalaHome,
		c.BuildDir,
		c.StdlibDir,
		c.GoPkgDir,
		c.GalaPkgDir,
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	return nil
}

// StdlibVersionDir returns the path for a specific stdlib version.
// Format: StdlibDir/v{version}/
// The version string may contain a git describe suffix (e.g.,
// "0.29.4-1-ga528ffd"); it is normalized to the base version ("0.29.4") here so
// that every consumer of the cache — build, transpile and the LSP — agrees on
// one directory.
func (c *Config) StdlibVersionDir(version string) string {
	return filepath.Join(c.StdlibDir, "v"+normalizeStdlibVersion(version))
}

// EnsureStdlib extracts the embedded stdlib for the given version and returns
// the stdlib directory path, or "" if extraction failed. This is the canonical
// stdlib resolution used by both the CLI transpiler and the LSP server; it
// shares its implementation with the builder so the two cannot drift apart.
func (c *Config) EnsureStdlib(version string) string {
	stdlibDir, _, err := c.ensureStdlibExtracted(version)
	if err != nil {
		return ""
	}
	return stdlibDir
}

// GalaModulePath returns the path where a GALA module version is cached.
// Format: GalaPkgDir/module/path@version/
func (c *Config) GalaModulePath(modulePath, version string) string {
	return filepath.Join(c.GalaPkgDir, modulePath+"@"+version)
}
