package build

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `gala build` and `gala test` generate different trees from the same sources.
// Sharing one workspace made them race: each wipes gen/ before transpiling, so
// running both at once left a half-written tree behind. They must not resolve
// to the same directory.
func TestBuildAndTestWorkspacesAreSeparate(t *testing.T) {
	config := isolatedConfig(t)
	projectDir := t.TempDir()

	buildWS, err := NewWorkspace(config, projectDir, ModeBuild)
	require.NoError(t, err)
	testWS, err := NewWorkspace(config, projectDir, ModeTest)
	require.NoError(t, err)

	require.NotEqual(t, buildWS.Dir, testWS.Dir,
		"build and test must not share a workspace directory")
	require.NotEqual(t, buildWS.GenDir, testWS.GenDir)

	// The project hash still identifies the project: only the directory the
	// two modes occupy differs.
	require.Equal(t, buildWS.Hash, testWS.Hash)

	// The build workspace keeps the bare hash, so workspaces that predate
	// modes stay valid.
	require.Equal(t, filepath.Join(config.BuildDir, buildWS.Hash), buildWS.Dir)
}

// Two projects must never share a workspace either — the directory is keyed on
// the project path.
func TestWorkspacesDifferPerProject(t *testing.T) {
	config := isolatedConfig(t)

	a, err := NewWorkspace(config, t.TempDir(), ModeBuild)
	require.NoError(t, err)
	b, err := NewWorkspace(config, t.TempDir(), ModeBuild)
	require.NoError(t, err)

	require.NotEqual(t, a.Dir, b.Dir)
}

// --build-dir / GALA_BUILD_DIR moves only the workspace, so a caller can
// isolate the one directory a build mutates while still sharing the large
// read-mostly caches beside it.
func TestBuildDirOverrides(t *testing.T) {
	t.Run("environment", func(t *testing.T) {
		home := t.TempDir()
		private := t.TempDir()
		t.Setenv("GALA_HOME", home)
		t.Setenv("GALA_BUILD_DIR", private)

		config := DefaultConfig()
		require.Equal(t, private, config.BuildDir)
		// The caches stay where they were: isolating a build costs no downloads.
		require.Equal(t, filepath.Join(home, "stdlib"), config.StdlibDir)
		require.Equal(t, filepath.Join(home, "pkg", "mod"), config.GalaPkgDir)
	})

	t.Run("flag wins over environment", func(t *testing.T) {
		t.Setenv("GALA_HOME", t.TempDir())
		t.Setenv("GALA_BUILD_DIR", t.TempDir())
		flagDir := t.TempDir()

		SetBuildDirOverride(flagDir)
		defer SetBuildDirOverride("")

		require.Equal(t, flagDir, DefaultConfig().BuildDir)
	})

	t.Run("default", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("GALA_HOME", home)
		t.Setenv("GALA_BUILD_DIR", "")

		require.Equal(t, filepath.Join(home, "build"), DefaultConfig().BuildDir)
	})
}

// The workspace lock is what stops two same-mode invocations (two builds, two
// test runs) from sharing one mutable tree. Only one may hold it at a time.
func TestWorkspaceLockIsExclusive(t *testing.T) {
	config := isolatedConfig(t)
	ws, err := NewWorkspace(config, t.TempDir(), ModeBuild)
	require.NoError(t, err)
	require.NoError(t, ws.Ensure())

	held, err := ws.Lock(time.Second)
	require.NoError(t, err)

	_, err = ws.Lock(200 * time.Millisecond)
	require.Error(t, err, "a second lock must not be granted while the first is held")
	require.ErrorIs(t, err, ErrLockBusy)
	// The message has to tell the user how to proceed, not just that it failed.
	require.Contains(t, err.Error(), "--build-dir")

	held.Release()

	after, err := ws.Lock(time.Second)
	require.NoError(t, err, "the lock must be available once released")
	after.Release()
}

// Release is called from a defer on paths that may already have released, so it
// has to tolerate being called twice.
func TestWorkspaceLockReleaseIsIdempotent(t *testing.T) {
	config := isolatedConfig(t)
	ws, err := NewWorkspace(config, t.TempDir(), ModeBuild)
	require.NoError(t, err)
	require.NoError(t, ws.Ensure())

	h, err := ws.Lock(time.Second)
	require.NoError(t, err)
	h.Release()
	require.NotPanics(t, h.Release)
}

// A process killed mid-build leaves its lock file behind. That must not wedge
// the workspace for every later build.
func TestWorkspaceLockBreaksStaleLock(t *testing.T) {
	config := isolatedConfig(t)
	ws, err := NewWorkspace(config, t.TempDir(), ModeBuild)
	require.NoError(t, err)
	require.NoError(t, ws.Ensure())

	lockPath := filepath.Join(ws.Dir, lockFileName)
	require.NoError(t, os.WriteFile(lockPath, []byte("pid=999999\n"), 0644))

	// Age it past the staleness window, as an abandoned lock would be.
	old := time.Now().Add(-2 * lockStale)
	require.NoError(t, os.Chtimes(lockPath, old, old))

	h, err := ws.Lock(2 * time.Second)
	require.NoError(t, err, "an abandoned lock must be broken, not waited on forever")
	h.Release()
}

// A waiter must acquire the lock once the holder releases it, rather than
// failing outright — the common case is a short wait, not a conflict.
func TestWorkspaceLockWaitsForRelease(t *testing.T) {
	config := isolatedConfig(t)
	ws, err := NewWorkspace(config, t.TempDir(), ModeBuild)
	require.NoError(t, err)
	require.NoError(t, ws.Ensure())

	first, err := ws.Lock(time.Second)
	require.NoError(t, err)

	// Ordering is explicit rather than sleep-based: the waiter announces that
	// it has started before the holder releases, so the test asserts "a waiter
	// already blocked gets the lock" without depending on how promptly a loaded
	// machine schedules the goroutine.
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(started)
		h, lockErr := ws.Lock(30 * time.Second)
		if h != nil {
			h.Release()
		}
		result <- lockErr
	}()

	<-started
	first.Release()

	select {
	case waitErr := <-result:
		require.NoError(t, waitErr, "the waiter should acquire the lock after it is released")
	case <-time.After(60 * time.Second):
		t.Fatal("waiter never returned after the lock was released")
	}
}

// On Windows a file another process holds open cannot be unlinked, so a gen/
// file that is briefly locked — a test binary that has just exited, a scanner —
// used to fail the whole build. CleanGen must wait such a holder out.
func TestCleanGenRetriesThroughTransientLock(t *testing.T) {
	config := isolatedConfig(t)
	ws, err := NewWorkspace(config, t.TempDir(), ModeBuild)
	require.NoError(t, err)
	require.NoError(t, ws.Ensure())

	stuck := filepath.Join(ws.GenDir, "held_open.go")
	require.NoError(t, os.WriteFile(stuck, []byte("package gen\n"), 0644))

	f, err := os.Open(stuck)
	require.NoError(t, err)

	// Release the handle shortly after CleanGen starts, as a real short-lived
	// holder would.
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = f.Close()
	}()

	require.NoError(t, ws.CleanGen(), "CleanGen must retry through a transient lock")

	entries, err := os.ReadDir(ws.GenDir)
	require.NoError(t, err)
	require.Empty(t, entries, "gen/ must be empty after CleanGen")
}

// A holder that never lets go is a real problem: building on a stale gen tree
// would compile stale code. CleanGen must fail — but say what to do about it,
// rather than surfacing a bare "unlinkat ...".
func TestCleanGenReportsHeldFileClearly(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows refuses to unlink a file that is open")
	}

	config := isolatedConfig(t)
	ws, err := NewWorkspace(config, t.TempDir(), ModeBuild)
	require.NoError(t, err)
	require.NoError(t, ws.Ensure())

	stuck := filepath.Join(ws.GenDir, "held_open.go")
	require.NoError(t, os.WriteFile(stuck, []byte("package gen\n"), 0644))

	f, err := os.Open(stuck)
	require.NoError(t, err)
	defer f.Close()

	err = ws.CleanGen()
	require.Error(t, err, "a permanently held file must not be silently built around")
	require.Contains(t, err.Error(), ws.GenDir, "the error should name the directory")
	require.Contains(t, err.Error(), "--build-dir", "the error should say how to proceed")
}

// Cleaning a project has to remove every workspace it owns, not only the build
// one — otherwise `gala clean` leaves the test tree behind.
func TestFindWorkspacesByProjectReturnsEveryMode(t *testing.T) {
	config := isolatedConfig(t)
	projectDir := t.TempDir()

	for _, mode := range []Mode{ModeBuild, ModeTest} {
		ws, err := NewWorkspace(config, projectDir, mode)
		require.NoError(t, err)
		require.NoError(t, ws.Ensure())
	}

	found, err := FindWorkspacesByProject(config, projectDir)
	require.NoError(t, err)
	require.Len(t, found, 2)

	modes := map[Mode]bool{}
	for _, ws := range found {
		modes[ws.Mode] = true
	}
	require.True(t, modes[ModeBuild])
	require.True(t, modes[ModeTest])
}

func TestFindWorkspacesByProjectErrorsWhenAbsent(t *testing.T) {
	config := isolatedConfig(t)
	_, err := FindWorkspacesByProject(config, t.TempDir())
	require.Error(t, err)
}

// Deleting a workspace a build is using is the same corruption the lock exists
// to prevent, and worse: it removes the running build's lock file along with
// its gen tree, so the next build sees an unlocked workspace and writes into
// the wreckage. Clean must refuse.
func TestCleanRefusesWorkspaceInUse(t *testing.T) {
	config := isolatedConfig(t)
	ws, err := NewWorkspace(config, t.TempDir(), ModeBuild)
	require.NoError(t, err)
	require.NoError(t, ws.Ensure())

	held, err := ws.Lock(time.Second)
	require.NoError(t, err)
	defer held.Release()

	err = ws.Clean()
	require.Error(t, err, "a workspace a build holds must not be deleted")
	require.ErrorIs(t, err, ErrLockBusy)
	require.DirExists(t, ws.Dir, "the running build's tree must still be there")
}

// The ordinary case still works: an idle workspace cleans.
func TestCleanRemovesIdleWorkspace(t *testing.T) {
	config := isolatedConfig(t)
	ws, err := NewWorkspace(config, t.TempDir(), ModeBuild)
	require.NoError(t, err)
	require.NoError(t, ws.Ensure())

	require.NoError(t, ws.Clean())
	require.NoDirExists(t, ws.Dir)
}

// A sweep must not abort on one busy workspace — one active build should not
// stop the other forty from being cleaned — and must say what it left behind.
func TestSweepsSkipBusyWorkspacesAndCountThem(t *testing.T) {
	config := isolatedConfig(t)

	idle, err := NewWorkspace(config, t.TempDir(), ModeBuild)
	require.NoError(t, err)
	require.NoError(t, idle.Ensure())

	busyWS, err := NewWorkspace(config, t.TempDir(), ModeBuild)
	require.NoError(t, err)
	require.NoError(t, busyWS.Ensure())
	held, err := busyWS.Lock(time.Second)
	require.NoError(t, err)
	defer held.Release()

	removed, busy, err := CleanAllWorkspaces(config)
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	require.Equal(t, 1, busy)

	require.NoDirExists(t, idle.Dir, "the idle workspace should be gone")
	require.DirExists(t, busyWS.Dir, "the busy workspace should survive")
}

// A workspace with a live build is not stale, however old its marker is.
func TestStaleSweepSkipsBusyWorkspace(t *testing.T) {
	config := isolatedConfig(t)
	ws, err := NewWorkspace(config, t.TempDir(), ModeBuild)
	require.NoError(t, err)
	require.NoError(t, ws.Ensure())

	// Age the marker well past any threshold.
	marker := filepath.Join(ws.Dir, ".gala-workspace")
	old := time.Now().Add(-30 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(marker, old, old))

	held, err := ws.Lock(time.Second)
	require.NoError(t, err)
	defer held.Release()

	removed, busy, err := CleanStaleWorkspaces(config, 7*24*time.Hour)
	require.NoError(t, err)
	require.Equal(t, 0, removed)
	require.Equal(t, 1, busy)
	require.DirExists(t, ws.Dir)
}

// Discard must leave the lock file alone: after a workspace is deleted, the
// path may already belong to a lock another process took.
func TestDiscardLeavesTheLockFile(t *testing.T) {
	config := isolatedConfig(t)
	ws, err := NewWorkspace(config, t.TempDir(), ModeBuild)
	require.NoError(t, err)
	require.NoError(t, ws.Ensure())

	h, err := ws.Lock(time.Second)
	require.NoError(t, err)

	lockPath := filepath.Join(ws.Dir, lockFileName)
	require.FileExists(t, lockPath)

	h.Discard()
	require.FileExists(t, lockPath, "Discard must not unlink the lock file")

	require.NoError(t, os.Remove(lockPath))
}

// TestLockTimeoutEnvOverride covers GALA_BUILD_LOCK_TIMEOUT. A scripted or CI
// run wants to fail fast rather than block behind a stray process for the
// default ten minutes.
func TestLockTimeoutEnvOverride(t *testing.T) {
	const def = 10 * time.Minute

	cases := []struct {
		name string
		env  string
		want time.Duration
	}{
		{name: "unset falls back to the default", env: "", want: def},
		{name: "go duration", env: "30s", want: 30 * time.Second},
		{name: "minutes", env: "2m", want: 2 * time.Minute},
		{name: "bare seconds", env: "45", want: 45 * time.Second},
		{name: "zero waits forever", env: "0", want: 0},
		// A typo must not fail a build that would otherwise work.
		{name: "unparseable falls back", env: "soon", want: def},
		{name: "negative falls back", env: "-5s", want: def},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env == "" {
				os.Unsetenv(lockTimeoutEnv)
			} else {
				t.Setenv(lockTimeoutEnv, tc.env)
			}
			require.Equal(t, tc.want, lockTimeout(def))
		})
	}
}

// TestLockWaitAnnouncesItself pins that a build blocked on the workspace lock
// says so. Without the message the process produces no output at all while it
// waits, and the first reading of a build that sits for minutes is that it has
// hung.
func TestLockWaitAnnouncesItself(t *testing.T) {
	dir := t.TempDir()

	held, err := lockDir(dir, time.Second, workspaceNotice)
	require.NoError(t, err)

	stderr := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w
	defer func() { os.Stderr = stderr }()

	// Release from under the waiter so it reports both the wait and the
	// acquisition.
	go func() {
		time.Sleep(300 * time.Millisecond)
		held.Release()
	}()

	h, err := lockDir(dir, 10*time.Second, workspaceNotice)
	require.NoError(t, err)
	h.Release()

	require.NoError(t, w.Close())
	os.Stderr = stderr
	out, err := io.ReadAll(r)
	require.NoError(t, err)

	assert.Contains(t, string(out), "waiting for the build workspace lock")
	assert.Contains(t, string(out), "pid=")
	assert.Contains(t, string(out), "workspace lock acquired after")
}

// TestLockWaitIsSilentForCallersThatWantNoNotice pins that the wait notice is
// the caller's to supply. `gala clean` skips a held workspace by design, so a
// notice there would report a non-event as a problem; the stdlib cache is
// shared by every project, so the build-dir hint cannot help there.
func TestLockWaitIsSilentForCallersThatWantNoNotice(t *testing.T) {
	dir := t.TempDir()

	held, err := lockDir(dir, time.Second, workspaceNotice)
	require.NoError(t, err)

	stderr := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w
	defer func() { os.Stderr = stderr }()

	go func() {
		time.Sleep(300 * time.Millisecond)
		held.Release()
	}()

	// The empty notice is what removeWorkspaceDir passes.
	h, err := lockDir(dir, 10*time.Second, lockNotice{})
	require.NoError(t, err)
	h.Release()

	require.NoError(t, w.Close())
	os.Stderr = stderr
	out, err := io.ReadAll(r)
	require.NoError(t, err)

	assert.Empty(t, string(out), "a caller that supplies no notice must print nothing")
}

// TestStdlibLockNoticeOmitsTheBuildDirHint pins that a lock the user cannot
// avoid by moving their build does not advise them to move their build.
func TestStdlibLockNoticeOmitsTheBuildDirHint(t *testing.T) {
	dir := t.TempDir()

	held, err := lockDir(dir, time.Second, workspaceNotice)
	require.NoError(t, err)

	stderr := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w
	defer func() { os.Stderr = stderr }()

	go func() {
		time.Sleep(300 * time.Millisecond)
		held.Release()
	}()

	h, err := lockDir(dir, 10*time.Second, lockNotice{what: "stdlib cache"})
	require.NoError(t, err)
	h.Release()

	require.NoError(t, w.Close())
	os.Stderr = stderr
	out, err := io.ReadAll(r)
	require.NoError(t, err)

	assert.Contains(t, string(out), "waiting for the stdlib cache lock")
	assert.NotContains(t, string(out), "--build-dir",
		"the stdlib cache is shared by every project; a private build dir cannot avoid this lock")
}
