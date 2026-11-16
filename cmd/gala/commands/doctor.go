package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"martianoff/gala/internal/build"
	"martianoff/gala/internal/doctor"
	"martianoff/gala/internal/transpiler/analyzer"
)

var doctorJSON bool

var doctorCmd = &cobra.Command{
	Use:   "doctor [directory]",
	Short: "Check the Go toolchain, gala's caches and the project for problems",
	Long: `Doctor checks the environment gala runs in and prints one line per check,
marked OK, WARN or FAIL. Each WARN and FAIL comes with a one-line fix and a
link to the matching section of the troubleshooting page.

It checks:
  - this gala's version and path, and that gala on PATH runs the same version
  - go on PATH, and that it is Go ` + build.MinGoVersion + ` or newer
  - the Go SDK used for Go type inference (resolved as builds resolve it)
  - the gala home (GALA_HOME) and module cache (GALA_CACHE), and their size
  - stdlib cache directories left behind by other gala versions
  - the size of the Go build cache (go env GOCACHE)
  - on Windows, whether long paths are enabled
  - in a project: that gala.mod parses, and that go.mod, if there is one,
    requires the same Go dependency versions as gala.mod

It exits non-zero when any check fails. With --json it prints the checks as
JSON on stdout; attach that output to bug reports.

Examples:
  gala doctor
  gala doctor ./myproject
  gala doctor --json`,
	Args: cobra.MaximumNArgs(1),
	Run:  runDoctor,
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "Print the checks as JSON on stdout")
	rootCmd.AddCommand(doctorCmd)
}

func runDoctor(cmd *cobra.Command, args []string) {
	// Outside a project the project checks are skipped, but a directory named
	// on the command line is meant to be one: skipping it silently would let
	// a mistyped path pass.
	projectDir, err := findProjectRoot(".")
	if len(args) > 0 {
		if projectDir, err = findProjectRoot(args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
	if err != nil {
		projectDir = ""
	}
	report := doctor.Run(defaultDoctorEnv(projectDir))

	if doctorJSON {
		out, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(out))
	} else {
		printDoctorReport(report)
	}
	if report.Failed() {
		os.Exit(1)
	}
}

// printDoctorReport writes one line per check, with the fix and the docs link
// indented under each WARN and FAIL.
func printDoctorReport(report doctor.Report) {
	width := 0
	for _, c := range report.Checks {
		width = max(width, len(c.Name))
	}
	indent := strings.Repeat(" ", 6+width+2) // under the message column
	for _, c := range report.Checks {
		fmt.Printf("%-4s  %-*s  %s\n", c.Status, width, c.Name, c.Message)
		if c.Fix != "" {
			fmt.Printf("%sfix: %s\n", indent, c.Fix)
		}
		if c.DocsURL != "" {
			fmt.Printf("%ssee: %s\n", indent, c.DocsURL)
		}
	}
}

// defaultDoctorEnv is the real environment: the running binary, PATH, the go
// command, the analyzer's Go SDK resolution and the configured caches.
// projectDir is "" outside a project.
func defaultDoctorEnv(projectDir string) doctor.Env {
	exe, err := os.Executable()
	if err != nil {
		exe = ""
	} else if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return doctor.Env{
		Version:          Version,
		Executable:       exe,
		LookPath:         exec.LookPath,
		GoEnv:            goEnv,
		GoSDKRoot:        analyzer.GoSDKRoot,
		SameFile:         sameFile,
		GalaVersion:      galaVersion,
		DirSize:          doctor.DirSize,
		Config:           build.DefaultConfig(),
		ProjectDir:       projectDir,
		LongPathsEnabled: doctor.LongPathsEnabled,
		GoCacheWarnBytes: doctor.DefaultGoCacheWarnBytes,
	}
}

// goEnv runs `goBin env keys...`, which prints one value per line. It runs in
// the temp directory, outside any module: in a module whose go.mod asks for a
// newer Go, the go command switches toolchain (GOTOOLCHAIN) and reports that
// one, not the Go that is installed.
func goEnv(goBin string, keys ...string) ([]string, error) {
	cmd := exec.Command(goBin, append([]string{"env"}, keys...)...)
	cmd.Dir = os.TempDir()
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	// Only the final newline goes: a value can be empty, and its empty line
	// still counts.
	return strings.Split(strings.TrimSuffix(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n"), "\n"), nil
}

// galaVersion runs `bin version` and returns the version from its first line,
// "GALA version <v>" (see versionCmd).
func galaVersion(bin string) (string, error) {
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		return "", err
	}
	first, _, _ := strings.Cut(string(out), "\n")
	v, found := strings.CutPrefix(strings.TrimSpace(first), "GALA version ")
	if !found {
		return "", fmt.Errorf("unexpected output %q", first)
	}
	return v, nil
}

// sameFile reports whether a and b name the same file, following symlinks.
func sameFile(a, b string) bool {
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
}
