package subprocess

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/runfiles"
)

// echoechoPath returns the absolute path to the testdata fixture binary.
// Under bazel test the binary lives in runfiles; under `go test` directly
// the dev would have to build it themselves, so we skip cleanly in that case.
//
// rules_go puts go_binary outputs in a `<target>_/<target>` (or `.exe`)
// subdir, so we try both that layout and the bare path for forward-compat.
func echoechoPath(t *testing.T) string {
	t.Helper()
	exe := "echoecho"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	candidates := []string{
		filepath.Join("_main", "subprocess", "testdata", "echoecho", "echoecho_", exe),
		filepath.Join("_main", "subprocess", "testdata", "echoecho", exe),
	}
	for _, rel := range candidates {
		if p, err := runfiles.Rlocation(rel); err == nil && p != "" {
			if _, statErr := os.Stat(p); statErr == nil {
				return p
			}
		}
	}
	// Fallback for `go test` without bazel — dev built it manually.
	fb := filepath.Join("testdata", "echoecho", exe)
	if _, statErr := os.Stat(fb); statErr == nil {
		abs, _ := filepath.Abs(fb)
		return abs
	}
	t.Skipf("echoecho fixture not found in runfiles or %s; run via bazel test", fb)
	return ""
}

func TestSpawn_EmptyCmdReturnsFailure(t *testing.T) {
	r := Spawn(NewSpawnOpts("", nil, nil, ""))
	if r.IsSuccess() {
		t.Fatalf("expected Failure for empty Cmd, got Success")
	}
	if !strings.Contains(r.GetError().Error(), "empty Cmd") {
		t.Fatalf("expected 'empty Cmd' in error, got %v", r.GetError())
	}
}

func TestSpawn_UnknownExecutable(t *testing.T) {
	r := Spawn(NewSpawnOpts("this-binary-definitely-does-not-exist-xyz123", nil, nil, ""))
	if r.IsSuccess() {
		// Some platforms defer the lookup until Start; if Spawn succeeded the
		// process should at least exit quickly with a non-zero code via Wait.
		// But the typical and expected path is Failure here.
		_ = r.Get().Kill()
		t.Skip("platform deferred PATH lookup; not the typical path")
	}
	if r.GetError() == nil {
		t.Fatalf("expected non-nil error")
	}
}

func TestRoundTrip_StdoutAndStderr(t *testing.T) {
	bin := echoechoPath(t)
	res := Spawn(NewSpawnOpts(bin, nil, nil, ""))
	if res.IsFailure() {
		t.Fatalf("Spawn failed: %v", res.GetError())
	}
	p := res.Get()

	if !p.IsAlive() {
		t.Fatalf("process should be alive immediately after Spawn")
	}
	if p.Pid() <= 0 {
		t.Fatalf("expected positive PID, got %d", p.Pid())
	}

	if w := p.WriteLine("hello"); w.IsFailure() {
		t.Fatalf("WriteLine failed: %v", w.GetError())
	}
	if w := p.WriteLine("world"); w.IsFailure() {
		t.Fatalf("WriteLine 2 failed: %v", w.GetError())
	}

	for _, want := range []string{"out: hello", "out: world"} {
		got := p.ReadLine()
		if got.IsFailure() {
			t.Fatalf("ReadLine failed: %v", got.GetError())
		}
		if got.Get() != want {
			t.Fatalf("ReadLine = %q, want %q", got.Get(), want)
		}
	}
	for _, want := range []string{"err: hello", "err: world"} {
		got := p.ReadStderrLine()
		if got.IsFailure() {
			t.Fatalf("ReadStderrLine failed: %v", got.GetError())
		}
		if got.Get() != want {
			t.Fatalf("ReadStderrLine = %q, want %q", got.Get(), want)
		}
	}

	// Tell echoecho to exit cleanly. After it does, both ReadLine streams
	// should report EOF and Wait should yield exit code 0.
	if w := p.WriteLine("BYE"); w.IsFailure() {
		t.Fatalf("WriteLine BYE failed: %v", w.GetError())
	}

	// Drain to EOF on both streams.
	if r := p.ReadLine(); r.IsSuccess() || !errors.Is(r.GetError(), io.EOF) {
		t.Fatalf("expected EOF on stdout after BYE, got %v / %v", r.IsSuccess(), r.GetError())
	}
	if r := p.ReadStderrLine(); r.IsSuccess() || !errors.Is(r.GetError(), io.EOF) {
		t.Fatalf("expected EOF on stderr after BYE, got %v / %v", r.IsSuccess(), r.GetError())
	}

	w := p.Wait()
	if w.IsFailure() {
		t.Fatalf("Wait failed: %v", w.GetError())
	}
	if w.Get() != 0 {
		t.Fatalf("expected exit code 0, got %d", w.Get())
	}
	if p.IsAlive() {
		t.Fatalf("IsAlive should be false after Wait")
	}

	// Wait is idempotent.
	if w2 := p.Wait(); w2.IsFailure() || w2.Get() != 0 {
		t.Fatalf("second Wait should match: %v / %d", w2.GetError(), w2.Get())
	}
}

func TestNonZeroExit_SurfacedAsSuccessWithCode(t *testing.T) {
	bin := echoechoPath(t)
	p := Spawn(NewSpawnOpts(bin, nil, nil, "")).Get()

	if w := p.WriteLine("FAIL"); w.IsFailure() {
		t.Fatalf("WriteLine FAIL: %v", w.GetError())
	}
	// Drain.
	for {
		r := p.ReadLine()
		if r.IsFailure() {
			break
		}
	}

	w := p.Wait()
	if w.IsFailure() {
		t.Fatalf("Wait should NOT be Failure for non-zero exit; got %v", w.GetError())
	}
	if w.Get() != 7 {
		t.Fatalf("expected exit code 7, got %d", w.Get())
	}
}

func TestKill_BeforeWait(t *testing.T) {
	bin := echoechoPath(t)
	p := Spawn(NewSpawnOpts(bin, nil, nil, "")).Get()

	if k := p.Kill(); k.IsFailure() {
		t.Fatalf("Kill failed: %v", k.GetError())
	}

	// Wait should still complete; exit code is platform-dependent (-1 on Unix
	// when killed by signal, large value on Windows). We just want it to
	// return without error.
	if w := p.Wait(); w.IsFailure() {
		t.Fatalf("Wait after Kill failed: %v", w.GetError())
	}

	// Kill is idempotent post-Wait.
	if k := p.Kill(); k.IsFailure() {
		t.Fatalf("second Kill should be no-op success, got %v", k.GetError())
	}
}

func TestSpawn_Dir(t *testing.T) {
	bin := echoechoPath(t)
	tmp := t.TempDir()
	p := Spawn(NewSpawnOpts(bin, nil, nil, tmp)).Get()
	defer func() {
		_ = p.Kill()
		_ = p.Wait()
	}()
	if !p.IsAlive() {
		t.Fatalf("process should be alive even with custom Dir")
	}
}
