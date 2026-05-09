//go:build windows

package commands

import "golang.org/x/sys/windows"

// stillActive is the Win32 STILL_ACTIVE constant returned by
// GetExitCodeProcess for processes that haven't exited yet. The MSDN
// docs warn that "STILL_ACTIVE" can collide with a real exit code of
// 259 — for our purposes that's fine, because a Bazel server exiting
// with code 259 is a hostile child-of-bazel scenario we can't
// distinguish anyway, and bailing out a worker on it is the safe
// behavior.
const stillActive = 259

// parentAlive returns true while the given pid is still a running
// process on Windows. Uses OpenProcess to grab a handle to the
// parent (with the minimum-privilege PROCESS_QUERY_LIMITED_INFORMATION
// access right — works even when the parent runs at a different
// integrity level than us, which can happen if Bazel was launched
// elevated and we weren't or vice versa) and queries its exit code.
//
// PID reuse caveat: Windows does recycle PIDs, so in principle we
// could open a handle to a NEW process that happens to have the
// original parent's PID. In practice the worker watcher polls within
// 5 s of startup and PID reuse takes a non-trivial amount of process
// churn, so we accept the theoretical race for the much larger
// payoff of clean orphan exit. A defensive caller could capture the
// process creation timestamp at startup and compare on each poll;
// that's overkill for the failure mode we're closing.
func parentAlive(ppid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(ppid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var exitCode uint32
	if err := windows.GetExitCodeProcess(h, &exitCode); err != nil {
		return false
	}
	return exitCode == stillActive
}
