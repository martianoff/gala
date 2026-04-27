// Package subprocess — Go-side helpers for the things subprocess.gala can't
// express (errors.As pointer pattern, bufio.Scanner buffer config). The
// public API lives in subprocess.gala; everything in this file is
// package-private and exists only because GALA's parser doesn't accept the
// underlying Go syntax yet.
package subprocess

import (
	"bufio"
	"errors"
	"io"
	"os/exec"

	"martianoff/gala/std"
)

// NewSpawnOpts is a Go-friendly constructor for SpawnOpts. The GALA-emitted
// struct uses Immutable[T] fields, so Go callers (tests, other Go-side
// libraries) need this shim instead of a bare struct literal. GALA callers
// just write SpawnOpts(Cmd = ..., ...) and the transpiler injects the
// Immutable wrapping.
func NewSpawnOpts(cmd string, args, env []string, dir string) SpawnOpts {
	return SpawnOpts{
		Cmd:  std.NewImmutable(cmd),
		Args: std.NewImmutable(args),
		Env:  std.NewImmutable(env),
		Dir:  std.NewImmutable(dir),
	}
}

// Maximum scanned line length for stdout/stderr. Stream-json output can
// include long single-line JSON objects, so the default 64 KiB scanner
// buffer is too small. 4 MiB matches what claude emits in practice with
// room to spare.
const maxLineBytes = 4 * 1024 * 1024

// newLineScanner builds a bufio.Scanner whose internal buffer is sized to
// hold a full claude stream-json line. Pulled out of the GALA caller because
// bufio.Scanner.Buffer takes a `make([]byte, 0, N)` argument and GALA's
// parser doesn't accept three-arg make on slices.
func newLineScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	return s
}

// exitCodeFromError unwraps an *exec.ExitError to its code; returns the code
// and a nil residual error for non-zero exits, or -1 and the original error
// for true failures (the child didn't even run, or a syscall failure).
// errors.As needs a typed pointer that GALA can't construct directly.
func exitCodeFromError(err error) (int, error) {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return -1, err
}

// isProcessFinished reports whether err is the os/exec sentinel raised when
// Kill is called on an already-exited process. errors.Is alone would suffice,
// but exporting it as a one-liner helper keeps the GALA caller readable.
func isProcessFinished(err error) bool {
	if errors.Is(err, errProcessFinished) {
		return true
	}
	// The sentinel string is stable across Go versions and is what os/exec
	// returns when Process.Kill races with the kernel's reap.
	return err != nil && err.Error() == "os: process already finished"
}
