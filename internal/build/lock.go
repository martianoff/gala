package build

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// A build workspace is a single mutable scratch tree: every build wipes gen/ and
// rewrites go.mod before transpiling into it. Two gala processes working in the
// same one therefore delete each other's files mid-build, and the errors that
// surface — a module resolved from the network, a package "not in std", a
// missing go.sum entry — name the symptom, never the cause.
//
// Separate per-mode workspaces remove the common collision (a build alongside a
// test). This lock covers the rest: two builds, two test runs, or any two
// invocations pointed at one workspace. The second waits for the first, and a
// wait that does not clear reports what it is waiting for and how to opt out.

const (
	// lockFileName is the lock's name inside the workspace directory.
	lockFileName = ".gala-lock"

	// lockPoll is how often a waiter retries.
	lockPoll = 200 * time.Millisecond

	// lockStale is how long a lock file may go untouched before a waiter
	// treats it as abandoned. A build refreshes its lock well inside this
	// window, so only a killed process leaves one behind.
	//
	// This is also how long the next build waits after someone interrupts a
	// build with Ctrl-C: the signal kills the process without releasing the
	// lock, so the file sits there until it goes stale. Kept to a minute, at
	// twelve times the heartbeat, to bound that wait while leaving a wide
	// margin for a machine too loaded to tick on time. Checking whether the
	// recorded pid is still alive would remove the wait entirely, but is
	// meaningfully harder to do portably than it looks — os.FindProcess
	// succeeds for dead pids on Windows.
	lockStale = time.Minute

	// lockHeartbeat is how often the holder touches the lock to prove it is
	// alive. Comfortably shorter than lockStale so a slow build is never
	// mistaken for a dead one.
	lockHeartbeat = 5 * time.Second

	// workspaceLockTimeout is how long a build waits for another gala process
	// to release the workspace before giving up. Generous enough to cover a
	// cold build of a large project, short enough that a genuinely wedged
	// workspace is reported rather than waited on forever.
	//
	// GALA_BUILD_LOCK_TIMEOUT overrides it — see lockTimeout.
	workspaceLockTimeout = 10 * time.Minute

	// lockTimeoutEnv overrides workspaceLockTimeout. It takes a Go duration
	// ("30s", "2m") or "0" to wait forever.
	lockTimeoutEnv = "GALA_BUILD_LOCK_TIMEOUT"

	// cleanLockTimeout is how long `gala clean` waits for a workspace before
	// leaving it alone. Much shorter than a build's wait: someone who typed a
	// clean wants an answer, and "a build is using this" is a useful answer.
	// The few seconds are there to ride out a lock that is mid-handoff, not to
	// outlast a build.
	cleanLockTimeout = 3 * time.Second
)

// buildDirHint is the "how to proceed" line shared by every error that a caller
// can resolve by giving the build a private workspace. One copy, because the
// flag and the environment variable are named in it.
const buildDirHint = "Give this build a workspace of its own:\n" +
	"  gala <command> --build-dir <dir>   (or set GALA_BUILD_DIR=<dir>)"

// ErrLockBusy reports that another process holds the workspace and did not
// release it within the timeout.
var ErrLockBusy = errors.New("build workspace is in use")

// lockHandle is an acquired workspace lock. Release removes the lock file and
// stops the heartbeat; it is safe to call more than once.
type lockHandle struct {
	path     string
	released chan struct{}
	done     bool
}

// lockTimeout returns the wait a build should use, honouring
// GALA_BUILD_LOCK_TIMEOUT when it is set to a valid duration.
//
// A scripted or CI run wants to fail rather than block: there is no one to
// notice the wait, and a job that sits for the default ten minutes behind a
// stray process burns the runner for nothing. Setting the variable to a few
// seconds turns that into a prompt, legible failure. "0" waits forever, for
// the opposite case — a queue that would rather be slow than flaky.
//
// An unparseable value falls back to the default rather than failing the
// build: the variable is an optimisation, and a typo in it should not stop a
// build that would otherwise work.
func lockTimeout(def time.Duration) time.Duration {
	raw := os.Getenv(lockTimeoutEnv)
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		// Bare seconds are the shape people reach for first.
		if n, nerr := strconv.Atoi(raw); nerr == nil {
			d, err = time.Duration(n)*time.Second, nil
		}
	}
	if err != nil || d < 0 {
		fmt.Fprintf(os.Stderr, "gala: ignoring %s=%q (not a duration such as \"30s\")\n", lockTimeoutEnv, raw)
		return def
	}
	return d
}

// Lock takes the workspace's exclusive lock, waiting up to timeout for a
// concurrent holder to finish. A zero timeout waits forever.
//
// The lock is advisory and cooperative — it exists to serialize this tool
// against itself, not to defend the directory from arbitrary writers.
func (w *Workspace) Lock(timeout time.Duration) (*lockHandle, error) {
	return lockDir(w.Dir, timeout)
}

// lockDir is Lock over a bare directory path. `gala clean` sweeps workspaces it
// finds on disk rather than ones it constructed, so it has a directory and no
// Workspace — and deleting a workspace needs the lock just as much as building
// one does.
func lockDir(dir string, timeout time.Duration) (*lockHandle, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating workspace dir: %w", err)
	}

	path := filepath.Join(dir, lockFileName)
	deadline := time.Time{}
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}

	waiting := false
	waitStart := time.Now()
	for {
		h, err := tryLock(path)
		if err == nil {
			if waiting {
				fmt.Fprintf(os.Stderr, "gala: workspace lock acquired after %s\n",
					waitDuration(time.Since(waitStart)))
			}
			return h, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("locking build workspace: %w", err)
		}

		if breakIfStale(path) {
			continue // the holder is gone; the next attempt takes it
		}

		// Say what the wait is for, once, the first time round. Without this
		// the process produces no output at all while it blocks, and the first
		// reading of a build that sits for minutes is that it has hung — the
		// wait is invisible, so it gets attributed to the slowest thing the
		// command was known to be doing.
		if !waiting {
			waiting = true
			fmt.Fprintf(os.Stderr, "gala: waiting for the build workspace lock (%s)\n", holderDescription(path))
			fmt.Fprintf(os.Stderr, "gala: %s\n", buildDirHint)
		}

		if !deadline.IsZero() && time.Now().After(deadline) {
			return nil, fmt.Errorf(
				"%w: %s\nheld by: %s\nwaited: %s\n"+
					"Another gala process is building this project. Wait for it to finish, or\n%s",
				ErrLockBusy, dir, holderDescription(path), waitDuration(time.Since(waitStart)), buildDirHint)
		}

		time.Sleep(lockPoll)
	}
}

// waitDuration renders a wait for a human: whole seconds are enough, and the
// sub-millisecond tail time.Duration prints by default is noise here.
func waitDuration(d time.Duration) time.Duration {
	return d.Round(time.Second)
}

// tryLock creates the lock file, failing with fs.ErrExist when it is taken.
// O_EXCL makes the create-or-fail atomic, which is what makes this a lock.
func tryLock(path string) (*lockHandle, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintf(f, "pid=%d\nstarted=%s\n", os.Getpid(), time.Now().Format(time.RFC3339))
	if cerr := f.Close(); cerr != nil {
		_ = os.Remove(path)
		return nil, cerr
	}

	h := &lockHandle{path: path, released: make(chan struct{})}
	go h.heartbeat()
	return h, nil
}

// heartbeat keeps the lock file's mtime current so waiters can tell a long
// build from a dead one.
func (h *lockHandle) heartbeat() {
	ticker := time.NewTicker(lockHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-h.released:
			return
		case <-ticker.C:
			now := time.Now()
			_ = os.Chtimes(h.path, now, now)
		}
	}
}

// Release drops the lock.
func (h *lockHandle) Release() {
	h.finish(true)
}

// Discard stops the heartbeat but leaves the lock file alone. It is what a
// caller that has just deleted the whole workspace wants: the lock file went
// with the directory, and unlinking "it" by path could remove a lock that
// another process created in the window since — handing two builds the same
// fresh workspace.
func (h *lockHandle) Discard() {
	h.finish(false)
}

func (h *lockHandle) finish(remove bool) {
	if h == nil || h.done {
		return
	}
	h.done = true
	close(h.released)
	if remove {
		_ = os.Remove(h.path)
	}
}

// breakIfStale removes a lock whose holder stopped refreshing it, and reports
// whether it did. A process killed mid-build must not wedge the workspace for
// every later build.
func breakIfStale(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		// It vanished between the failed create and here — the holder
		// released it. Treat that as progress so the caller retries.
		return errors.Is(err, fs.ErrNotExist)
	}
	if time.Since(info.ModTime()) < lockStale {
		return false
	}
	return os.Remove(path) == nil
}

// holderDescription reads the lock file for the "held by" line of the busy
// message. It is diagnostics only, so an unreadable lock degrades to a
// placeholder rather than masking the real error.
func holderDescription(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return "unknown process"
	}
	// Fields splits on all whitespace, newlines included, so this collapses the
	// lock file's key=value lines onto one.
	if s := strings.Join(strings.Fields(string(content)), " "); s != "" {
		return s
	}
	return "unknown process"
}
