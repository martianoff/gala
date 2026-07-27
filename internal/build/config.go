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
// Format: StdlibDir/v{version}/, with the version put through
// normalizeStdlibVersion so that every consumer of the cache resolves the same
// directory for a given binary.
func (c *Config) StdlibVersionDir(version string) string {
	return filepath.Join(c.StdlibDir, "v"+normalizeStdlibVersion(version))
}

// EnsureStdlib extracts the embedded stdlib for the given version and returns
// the stdlib directory path. This is the canonical stdlib resolution used by
// both the CLI transpiler and the LSP server; it shares its implementation with
// the builder so the two cannot drift apart.
//
// The failure is reported rather than collapsed into an empty path: refusing to
// repair the cache is exactly the situation this code exists to notice, and a
// caller that only sees "" degrades it into the far less informative "no stdlib
// on the search path".
func (c *Config) EnsureStdlib(version string) (string, error) {
	stdlibDir, _, err := c.ensureStdlibExtracted(version)
	return stdlibDir, err
}

// GalaModulePath returns the path where a GALA module version is cached.
// Format: GalaPkgDir/module/path@version/
func (c *Config) GalaModulePath(modulePath, version string) string {
	return filepath.Join(c.GalaPkgDir, modulePath+"@"+version)
}
