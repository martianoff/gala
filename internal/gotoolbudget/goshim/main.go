// Command goshim stands in for the go command of a Go SDK in the Go
// toolchain call budget test (see ../budget_test.go). It appends its
// arguments, one invocation per line, to the file $GOSHIM_LOG, then runs the
// go command of the real SDK at $GOSHIM_GOROOT with the same arguments,
// standard streams and exit status.
//
// It sits at <goroot>/bin/go of a copy of the SDK because that is the path
// every caller runs: go/build spawns $GOROOT/bin/go itself, so a shim earlier
// on PATH would not see its calls.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	os.Exit(run())
}

func run() int {
	logPath, goroot := os.Getenv("GOSHIM_LOG"), os.Getenv("GOSHIM_GOROOT")
	if logPath == "" || goroot == "" {
		fmt.Fprintln(os.Stderr, "goshim: GOSHIM_LOG and GOSHIM_GOROOT must be set")
		return 2
	}
	if err := record(logPath, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "goshim: %v\n", err)
		return 2
	}
	cmd := exec.Command(filepath.Join(goroot, "bin", "go"), os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// The real SDK's own root: its tools and packages, not the copy's. The
	// last value of a repeated key wins.
	cmd.Env = append(os.Environ(), "GOROOT="+goroot)
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "goshim: %v\n", err)
		return 2
	}
	return 0
}

// record appends args to the log as one line, each argument quoted so that
// none can break the line. A write this short to a file opened for appending
// is not interleaved with another process's.
func record(logPath string, args []string) error {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = strconv.Quote(a)
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(strings.Join(quoted, " ") + "\n")
	return errors.Join(err, f.Close())
}
