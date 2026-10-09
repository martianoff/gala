package commands

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"

	"martianoff/gala/internal/build"
	"martianoff/gala/internal/goexport"
	"martianoff/gala/internal/stdlib"
)

// defaultStdlibGoModule is the Go module path GALA releases publish the
// standard library under.
const defaultStdlibGoModule = "go.gala.fyi/stdlib"

var (
	exModule        string
	exOut           string
	exProxy         string
	exVersion       string
	exStdlibModule  string
	exStdlibVersion string
	exCommit        string
	exVerbose       bool
)

var exportCmd = &cobra.Command{
	Use:   "export [directory]",
	Short: "Write a GALA project as a plain Go module that Go programs can import",
	Long: `Write a GALA project's packages as a plain Go module, so a Go program can import
them with go get and build them with plain go build, without GALA.

The project is transpiled the way gala build does it. The export holds every
package's generated and hand-written Go (no tests), with imports of GALA's
standard library rewritten to the published Go module go.gala.fyi/stdlib and
a go.mod that requires it, at this compiler's version, along with every Go
module the code needs, as go mod tidy resolves them for gala build.

--go-module is the module path Go programs import the export by; it defaults
to the project's module path and must be one the go command can download.
Publish the export under that path: commit it to a repository (or a tag) whose
go.mod it is.

Packages that import another GALA module cannot be exported yet.

Examples:
  gala export --out ../mylib-go
  gala export --out ../mylib-go --go-module github.com/me/mylib-go
  gala export --out ../mylib-go --stdlib-version v0.0.0-local.1 \
    --proxy ../goproxy --version v0.0.0-local.1`,
	Args: cobra.MaximumNArgs(1),
	RunE: runExport,
}

func init() {
	f := exportCmd.Flags()
	f.StringVar(&exOut, "out", "", "Directory to write the module to; must be empty and outside any Bazel workspace (required)")
	f.StringVar(&exModule, "go-module", "", "Go module path of the export (default: the project's module path)")
	f.StringVar(&exStdlibModule, "stdlib-module", defaultStdlibGoModule, "Go module path GALA's standard library is imported from")
	f.StringVar(&exStdlibVersion, "stdlib-version", "", "Version of the standard library module to require (default: this binary's GALA version, v-prefixed)")
	f.StringVar(&exProxy, "proxy", "", "Also write the module into this GOPROXY file layout")
	f.StringVar(&exVersion, "version", "", "Module version for --proxy (required with --proxy)")
	f.StringVar(&exCommit, "commit", "", "Compiler commit recorded in VERSION (default: this binary's commit)")
	f.BoolVarP(&exVerbose, "verbose", "v", false, "Verbose output")
	_ = exportCmd.MarkFlagRequired("out")
}

func runExport(cmd *cobra.Command, args []string) error {
	projectDir := "."
	if len(args) > 0 {
		projectDir = args[0]
	}
	projectDir, err := callerPath(projectDir)
	if err != nil {
		return err
	}
	root, err := findProjectRoot(projectDir)
	if err != nil {
		return err
	}
	if root != projectDir {
		return fmt.Errorf("gala export writes a whole project; run it in the project root %s, not in %s", root, projectDir)
	}
	dest, err := newExportDest(exOut, exProxy, exVersion, "")
	if err != nil {
		return err
	}
	// gala build copies everything under the project into its workspace, so
	// an export inside the project would be built, and exported, as part of it.
	if rel, err := filepath.Rel(root, dest.out); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("refusing to export into %s: it is inside the project %s; choose a directory outside it", dest.out, root)
	}
	stdlibVersion := exStdlibVersion
	if stdlibVersion == "" {
		stdlibVersion = Version
	}
	if stdlibVersion, err = goexport.ModuleVersion(stdlibVersion); err != nil {
		return fmt.Errorf("standard library version: %w; pass --stdlib-version for a dev build", err)
	}

	builder, err := build.NewBuilder(root, Version, exVerbose)
	if err != nil {
		return err
	}
	modulePath := exModule
	if modulePath == "" {
		modulePath = builder.ModulePath()
	}
	if err := module.CheckPath(modulePath); err != nil {
		return fmt.Errorf("%w; pass --go-module with a path the go command can download", err)
	}

	if replaced := builder.ReplacedGoModules(); len(replaced) > 0 {
		return fmt.Errorf("gala.mod replaces the Go modules %s; a Go module's replace directives do not apply to the programs that import it, so the export would build against different code", strings.Join(replaced, ", "))
	}
	unsupported := map[string]string{}
	for _, p := range builder.GalaImportPaths() {
		unsupported[p] = "it is a GALA module, and exporting packages that import another GALA module is not supported yet"
	}
	goVersion, err := exportGoVersion(root)
	if err != nil {
		return err
	}
	commit := exCommit
	if commit == "" {
		commit = GitCommit
	}

	var files []goexport.File
	err = builder.Generate(func(gen build.Generated) error {
		// The workspace's tidied go.mod has every Go module the code needs,
		// including those only hand-written Go imports through a dependency.
		requires := []module.Version{{Path: exStdlibModule, Version: stdlibVersion}}
		indirect := map[string]bool{}
		for _, r := range gen.GoRequires {
			requires = append(requires, module.Version{Path: r.Path, Version: r.Version})
			indirect[r.Path] = r.Indirect
		}
		pkgs, err := goexport.ReadPackages(gen.GenDir)
		if err != nil {
			return err
		}
		for _, dir := range goexport.MainPackages(pkgs) {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s is package main, a program Go code cannot import\n", path.Join(modulePath, dir))
		}
		files, err = goexport.Export(goexport.Module{
			Path:      modulePath,
			GoVersion: goVersion,
			Packages:  pkgs,
			Remap: map[string]string{
				build.WorkspaceGenModule:      modulePath,
				goexport.StdlibSourceModule(): exStdlibModule,
			},
			Requires:    requires,
			Indirect:    indirect,
			Unsupported: unsupported,
			Version:     goexport.VersionFile(Version, commit),
		})
		return err
	})
	if err != nil {
		return err
	}
	return dest.write(cmd, modulePath, files)
}

// exportGoVersion is the go directive of an export: the one generated code
// needs, or the project's own go.mod's when that is newer.
func exportGoVersion(root string) (string, error) {
	v := stdlib.GoVersion
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if os.IsNotExist(err) {
		return v, nil
	}
	if err != nil {
		return "", err
	}
	f, err := modfile.ParseLax("go.mod", data, nil)
	if err != nil {
		return "", err
	}
	if f.Go != nil && semver.Compare("v"+f.Go.Version, "v"+v) > 0 {
		v = f.Go.Version
	}
	return v, nil
}

// exportDest is where an export is written: a module directory and,
// optionally, a GOPROXY file layout.
type exportDest struct {
	out, proxy, version string
}

// newExportDest resolves the destination paths from the caller's directory
// and checks them, and the proxy version, before any work is done. The proxy
// version is version, else defaultVersion; with neither, --proxy is an error.
func newExportDest(out, proxy, version, defaultVersion string) (exportDest, error) {
	var d exportDest
	var err error
	if d.out, err = callerPath(out); err != nil {
		return d, err
	}
	// Gazelle would pick up the rewritten imports of an export inside a
	// Bazel workspace and generate targets for them.
	if ws, ok := enclosingBazelWorkspace(d.out); ok {
		return d, fmt.Errorf("refusing to export into %s: it is inside the Bazel workspace %s; choose a directory outside it", d.out, ws)
	}
	if entries, err := os.ReadDir(d.out); err == nil && len(entries) > 0 {
		return d, fmt.Errorf("refusing to export into %s: the directory is not empty", d.out)
	}
	if proxy == "" {
		if version != "" {
			return d, fmt.Errorf("--version only applies with --proxy")
		}
		return d, nil
	}
	if d.proxy, err = callerPath(proxy); err != nil {
		return d, err
	}
	if version == "" {
		version = defaultVersion
	}
	if version == "" {
		return d, fmt.Errorf("--proxy needs --version, the module version to write")
	}
	if d.version, err = goexport.ModuleVersion(version); err != nil {
		return d, fmt.Errorf("%w; pass --version for a dev build", err)
	}
	return d, nil
}

func (d exportDest) write(cmd *cobra.Command, modulePath string, files []goexport.File) error {
	if err := goexport.WriteDir(d.out, files); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Exported %s (%d files) to %s\n", modulePath, len(files), d.out)
	if d.proxy == "" {
		return nil
	}
	if err := goexport.WriteProxy(d.proxy, modulePath, d.version, files); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s@%s to proxy %s\n", modulePath, d.version, d.proxy)
	return nil
}

// callerPath makes p absolute relative to the directory the user ran the
// command from. Under `bazel run` the process starts in the runfiles tree,
// and Bazel reports the user's directory in BUILD_WORKING_DIRECTORY. A
// process started elsewhere can inherit that variable, so it is honored only
// while the working directory is a runfiles tree.
func callerPath(p string) (string, error) {
	if wd := os.Getenv("BUILD_WORKING_DIRECTORY"); wd != "" && !filepath.IsAbs(p) && inRunfiles() {
		p = filepath.Join(wd, p)
	}
	return filepath.Abs(p)
}

func inRunfiles() bool {
	cwd, err := os.Getwd()
	return err == nil && strings.Contains(filepath.ToSlash(cwd), ".runfiles/")
}

// enclosingBazelWorkspace reports the nearest ancestor of dir (or dir itself)
// that is a Bazel workspace root.
func enclosingBazelWorkspace(dir string) (string, bool) {
	for d := dir; ; d = filepath.Dir(d) {
		for _, marker := range []string{"MODULE.bazel", "WORKSPACE", "WORKSPACE.bazel", "REPO.bazel"} {
			if _, err := os.Stat(filepath.Join(d, marker)); err == nil {
				return d, true
			}
		}
		if filepath.Dir(d) == d {
			return "", false
		}
	}
}
