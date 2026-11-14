package transpiler

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/runfiles"
)

// TestNoRetainMethodsDoNotEscape verifies the no-retain allow-list
// (noretain.go) against the escape analysis of the pinned Go compiler. For
// each entry it compiles a function that calls the method on its pointer
// parameter x, with `-gcflags=-m`, and requires the compiler to report
// `x does not escape`: a leak to the heap, or to a result, fails the entry.
func TestNoRetainMethodsDoNotEscape(t *testing.T) {
	goBin := goBinary(t)
	dir := t.TempDir()
	var src strings.Builder
	imports := map[string]bool{}
	for _, m := range NoRetainMethods() {
		imports[m.Path] = true
	}
	src.WriteString("package stub\n\nimport (\n")
	for path := range imports {
		fmt.Fprintf(&src, "\t%q\n", path)
	}
	src.WriteString(")\n")
	for i, m := range NoRetainMethods() {
		// noinline keeps each stub a function of its own, so its parameter
		// gets its own escape verdict.
		fmt.Fprintf(&src, "\n//go:noinline\nfunc stub%d(x *%s.%s) {\n\t%s\n}\n", i, filepath.Base(m.Path), m.Type, m.Call)
	}
	write := func(name, text string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module stub\n\ngo 1.25\n")
	write("stub.go", src.String())

	cmd := exec.Command(goBin, "build", "-gcflags=-m", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOCACHE="+filepath.Join(dir, "cache"), "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local", "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	report := string(out)
	for i, m := range NoRetainMethods() {
		line := stubLine(t, src.String(), i)
		ok := regexp.MustCompile(fmt.Sprintf(`stub\.go:%d:\d+: x does not escape`, line))
		if !ok.MatchString(report) {
			t.Errorf("%s.(*%s).%s is on the no-retain allow-list, but the compiler does not report that its receiver does not escape:\n%s",
				m.Path, m.Type, m.Method, grepLine(report, line))
		}
	}
}

// stubLine is the line of stub i's signature in src.
func stubLine(t *testing.T, src string, i int) int {
	for n, l := range strings.Split(src, "\n") {
		if strings.HasPrefix(l, fmt.Sprintf("func stub%d(", i)) {
			return n + 1
		}
	}
	t.Fatalf("no stub%d", i)
	return 0
}

// grepLine is the lines of report about line.
func grepLine(report string, line int) string {
	var b strings.Builder
	for _, l := range strings.Split(report, "\n") {
		if strings.Contains(l, fmt.Sprintf("stub.go:%d:", line)) {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}

// goBinary is the pinned Go toolchain's `go`, from the test's runfiles.
func goBinary(t *testing.T) string {
	rel := os.Getenv("GO_BIN")
	if rel == "" {
		t.Skip("GO_BIN is not set; run the test with Bazel")
	}
	path, err := runfiles.Rlocation(rel)
	if err != nil {
		t.Fatalf("locating %s: %v", rel, err)
	}
	return path
}
