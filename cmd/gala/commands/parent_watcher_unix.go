//go:build !windows

package commands

import "syscall"

// parentAlive returns true while the given pid is still a running
// process. Uses signal 0 — the POSIX "test if process exists" idiom:
// kill(pid, 0) returns ESRCH only when the target is gone, EPERM when
// it exists but we lack permission (treat as alive — Bazel's worker
// runs as the same uid as the parent, so EPERM shouldn't actually
// fire here, but being conservative beats false-positive exits), and
// nil when the signal was delivered.
//
// Linux-only fast path NOT taken: prctl(PR_SET_PDEATHSIG, SIGTERM)
// would let the kernel notify us instantly when the parent dies, no
// polling required. Skipped for now because (a) the polling path is
// portable to macOS / BSD with one implementation and (b) 5-second
// detection latency is plenty for Bazel's needs. If a Linux user
// reports orphan accumulation that the polling miss-window happens
// to span, the prctl call is a one-liner addition in an init() block
// gated by `runtime.GOOS == "linux"`.
func parentAlive(ppid int) bool {
	err := syscall.Kill(ppid, 0)
	return err == nil || err == syscall.EPERM
}
