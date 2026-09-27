// Package build provides build workspace management for GALA projects.
package build

import (
	"os"
	"path/filepath"

	"martianoff/gala/internal/depman/fetch"
)

// Config holds configuration for the build system.
type Config struct {
	// GalaHome is the root directory for GALA data.
	// Defaults to ~/.gala
	GalaHome string

	// BuildDir is where build workspaces are created.
	// Defaults to GalaHome/build; see defaultBuildDir for the overrides.
	BuildDir string

	// StdlibDir is where the standard library is cached.
	// Defaults to GalaHome/stdlib
	StdlibDir string

	// GoPkgDir is where Go dependencies are cached (GOMODCACHE).
	// Defaults to GalaHome/go/pkg/mod
	GoPkgDir string

	// GalaPkgDir is where GALA dependencies are cached: GALA_CACHE if set,
	// otherwise GalaHome/pkg/mod (see fetch.ModuleCacheDir). Dependencies are
	// both fetched into and read from here.
	GalaPkgDir string
}

// DefaultConfig returns the default build configuration.
func DefaultConfig() *Config {
	galaHome := fetch.DefaultGalaHome()
	return &Config{
		GalaHome:   galaHome,
		BuildDir:   defaultBuildDir(galaHome),
		StdlibDir:  filepath.Join(galaHome, "stdlib"),
		GoPkgDir:   filepath.Join(galaHome, "go", "pkg", "mod"),
		GalaPkgDir: fetch.ModuleCacheDir(galaHome),
	}
}

// buildDirOverride is the --build-dir value. It is set once, before any Config
// is constructed, and read by defaultBuildDir.
var buildDirOverride string

// SetBuildDirOverride points every workspace this process creates at dir. An
// empty dir restores the default. Set from the --build-dir flag.
func SetBuildDirOverride(dir string) {
	buildDirOverride = dir
}

// defaultBuildDir resolves where build workspaces live: --build-dir, then
// GALA_BUILD_DIR, then GalaHome/build.
//
// This is deliberately separate from GALA_HOME. A build workspace is the only
// directory a build rewrites on every invocation; the caches beside it (pkg,
// stdlib, go) are large and read-mostly, so redirecting the whole home to
// isolate a build forces a caller to re-download everything. Concurrent builds
// — CI matrices, several checkouts of one project, an editor building while a
// test run is in flight — get a knob that moves only what needs to be private.
//
// Read-mostly is not read-only: the stdlib cache is rewritten when the embedded
// snapshot changes, and the module caches are written when a dependency is
// fetched. Those are shared across projects and are NOT moved by this setting,
// so they carry their own coordination rather than relying on it — see
// ensureStdlibExtracted.
func defaultBuildDir(galaHome string) string {
	if buildDirOverride != "" {
		return buildDirOverride
	}
	if dir := os.Getenv("GALA_BUILD_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(galaHome, "build")
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
