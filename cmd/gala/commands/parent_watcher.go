package commands

import (
	"os"
	"time"
)

// watchParentForDeath polls the parent process and exits this worker
// when the parent dies. Persistent-worker mode runs this loop
// alongside the main ReadRequest loop in runWorker.
//
// Background: the main loop blocks on ReadRequest reading from stdin;
// when Bazel closes the worker's stdin during a clean shutdown, the
// reader returns io.EOF and the worker exits. But on Windows — and in
// edge cases on every OS where a parent is force-killed (taskkill,
// system crash, IDE killing the terminal) — the OS may not eagerly
// signal EOF on the orphaned pipe. The worker's ReadRequest then
// blocks indefinitely, the executable file stays locked, and the next
// `bazel build` of the same project fails with "Permission denied"
// when it tries to relink the worker binary at the same output path.
// We've seen this in practice on Windows: 7+ orphan workers
// accumulating across project switches, none of them reachable by
// `bazel shutdown`.
//
// The fix is platform-specific liveness detection (parentAlive lives
// in a per-OS file). On Linux/macOS/BSD we use signal-0 (kill(pid,
// 0)), which returns ESRCH only when the process is gone. On Windows
// we use OpenProcess + GetExitCodeProcess.
//
// Polling cadence: every 5 s. The cost is trivial (one syscall) and
// the latency is acceptable — orphan workers don't need to die in
// milliseconds, just before the next Bazel invocation hits a file-
// lock conflict (typically minutes later).
//
// Exit code 0 because this is a clean shutdown, not a worker crash.
// Bazel doesn't see this exit (the parent that would receive it is
// already dead); the goal is just to release the file lock.
func watchParentForDeath() {
	ppid := os.Getppid()
	// PPID 1 means the OS already re-parented us to init / launchd /
	// the system reaper — i.e. our original parent is already gone
	// at startup. Don't bother polling.
	if ppid <= 1 {
		os.Exit(0)
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if !parentAlive(ppid) {
			os.Exit(0)
		}
	}
}
