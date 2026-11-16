// Package gotoolbudget bounds how many times one transpile runs the go
// command to read the types of the Go packages a GALA program uses.
//
// A transpile once ran about 1,551 of them: go/importer's source importer
// makes go/build run `go list` for every import it resolves in module mode,
// recursive ones included (#884, fixed by #891). Nothing in the code keeps
// the count down, so this test does. It counts at the process boundary, with
// a go command that logs each call: go/build spawns $GOROOT/bin/go itself,
// so a counter in GALA's own code would miss exactly those calls.
package gotoolbudget

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/runfiles"
)

// budgetCase is one transpile and the most go commands it may run.
type budgetCase struct {
	name string
	// program is the directory under testdata the transpiled files are in.
	program string
	inputs  []string
	// chain, when set, writes a chain of that many Go packages of the
	// program's module, each importing the one before, and chain.go, which
	// imports them all, next to the GALA files.
	chain int
	// worker transpiles in the Bazel persistent worker with the export
	// data rules-gala hands it, instead of with the CLI.
	worker bool
	// wantGo is Go the transpile writes only when it has the Go types it
	// reads through the measured calls: a cheap transpile that lost them
	// does not pass.
	wantGo string
	// budget is the most go commands the transpile may run: the count
	// measured when it was set, plus a small margin.
	budget int
	// minCalls is the fewest it must run. A CLI transpile reads Go types
	// through `go list -export`, so a count of 0 there means the logging
	// go command was bypassed, not that the transpile got cheaper.
	minCalls int
}

// cases are the measured transpiles. To change a budget, run
//
//	bazel test //internal/gotoolbudget:gotoolbudget_test --test_output=all --test_arg=-test.v
//
// which logs each case's count and calls, set the budget to the new count
// plus a small margin, and say in the pull request why the calls are needed.
var cases = []budgetCase{
	{
		// Several Go standard library packages over two GALA files, a
		// hand-written Go file and a Go package of the program's module.
		name:     "cli/stdlib_multi",
		program:  "stdlib_multi",
		inputs:   []string{"main.gala", "report.gala"},
		wantGo:   "fmt.Println(Report(len(words)))",
		budget:   5, // measured 3
		minCalls: 1,
	},
	{
		// A third-party Go module (cobra) and one it depends on (pflag).
		name:     "cli/third_party",
		program:  "third_party",
		inputs:   []string{"main.gala"},
		wantGo:   `return std.GoTry(flags.GetInt("count"))`,
		budget:   5, // measured 3
		minCalls: 1,
	},
	{
		// The shape of #884: hand-written Go importing many Go packages
		// that are not the standard library's. Reading them from source
		// runs `go list` once per package.
		name:     "cli/many_packages",
		program:  "many_packages",
		inputs:   []string{"main.gala"},
		chain:    40,
		wantGo:   "fmt.Println(ChainDepth())",
		budget:   5, // measured 3; 118 when read from source
		minCalls: 1,
	},
	{
		// Every Go package comes with export data, so no go command is
		// needed: any call is a package the export data missed, and no
		// margin is allowed.
		name:    "worker/stdlib_multi",
		program: "stdlib_multi",
		inputs:  []string{"main.gala", "report.gala"},
		worker:  true,
		wantGo:  "fmt.Println(Report(len(words)))",
		budget:  0,
	},
	{
		name:    "worker/third_party",
		program: "third_party",
		inputs:  []string{"main.gala"},
		worker:  true,
		wantGo:  `return std.GoTry(flags.GetInt("count"))`,
		budget:  0,
	},
}

func TestGoToolchainCallBudget(t *testing.T) {
	h := newHarness(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			calls, generated := h.transpile(t, c)
			t.Logf("%s ran the go command %d times (budget %d):\n%s", c.name, len(calls), c.budget, indent(calls))
			if !strings.Contains(generated, c.wantGo) {
				t.Errorf("%s: the generated Go has no %q, so the transpile did not get the Go types it reads:\n%s", c.name, c.wantGo, generated)
			}
			if len(calls) > c.budget {
				t.Errorf("%s ran the go command %d times; its budget is %d. The calls:\n%s\n"+
					"A transpile reads Go package types through these calls; #891 took a regression of about "+
					"1,551 per transpile back down to a handful. If the change needs more calls, raise the "+
					"budget of %q in internal/gotoolbudget/budget_test.go to the new count plus a small "+
					"margin, and say why in the pull request. Otherwise, find the code that adds them.",
					c.name, len(calls), c.budget, indent(calls), c.name)
			}
			if len(calls) < c.minCalls {
				t.Errorf("%s ran the go command %d times, fewer than %d: the transpile did not run the logging go command, so the count measures nothing",
					c.name, len(calls), c.minCalls)
			}
		})
	}
}

// harness runs gala against a copy of the pinned Go SDK whose go command is
// the logging shim.
type harness struct {
	gala      string
	toolchain toolchain
	// base holds the SDK copy, as external/go_sdk, and a directory per
	// case. The transpiler takes the SDK under external/ in a directory it
	// runs in, or one above it, as a Bazel execution root's (see
	// analyzer.findBazelGoSDK), so every case finds the copy, before any Go
	// installed on the host.
	base string
	env  []string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		gala:      rlocation(t, os.Getenv("GALA_BIN")),
		toolchain: readManifest(t, rlocation(t, os.Getenv("TOOLCHAIN_MANIFEST"))),
		base:      t.TempDir(),
	}
	shim := rlocation(t, os.Getenv("GOSHIM_BIN"))
	copySDK(t, h.toolchain.goroot, filepath.Join(h.base, "external", "go_sdk"), shim)

	// Nothing from the host's Go or GALA setup, and nothing from the
	// network: modules come from local directories (see writeGoMod).
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GO") && !strings.HasPrefix(kv, "GALA_") && !strings.HasPrefix(kv, "CGO_") {
			h.env = append(h.env, kv)
		}
	}
	h.env = append(h.env,
		"GOSHIM_GOROOT="+h.toolchain.goroot,
		"GOCACHE="+filepath.Join(h.base, "gocache"),
		"GOPATH="+filepath.Join(h.base, "gopath"),
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOENV=off",
		"GOWORK=off",
		"GOTOOLCHAIN=local",
		"CGO_ENABLED=0",
	)
	return h
}

// transpile runs c's transpile in a directory of its own, with a fresh GALA
// home so that no analysis is cached, and returns the go commands it ran and
// the Go it wrote.
func (h *harness) transpile(t *testing.T, c budgetCase) (calls []string, generated string) {
	t.Helper()
	dir := filepath.Join(h.base, strings.ReplaceAll(c.name, "/", "_"))
	prog := filepath.Join(dir, "prog")
	copyDir(t, filepath.Join("testdata", c.program), prog)
	writeChain(t, prog, c.chain)
	log := filepath.Join(dir, "go-calls.log")
	env := append(slices.Clone(h.env), "GOSHIM_LOG="+log, "GALA_HOME="+filepath.Join(dir, "gala_home"))

	outputs := make([]string, len(c.inputs))
	for i, in := range c.inputs {
		outputs[i] = strings.TrimSuffix(in, ".gala") + ".gen.go"
	}
	if c.worker {
		h.runWorker(t, dir, env, c, outputs)
	} else {
		h.writeGoMod(t, prog)
		cmd := exec.Command(h.gala, "transpile-package",
			"--inputs="+strings.Join(c.inputs, ","),
			"--outputs="+strings.Join(outputs, ","))
		cmd.Dir, cmd.Env = prog, env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("gala transpile-package: %v\n%s", err, out)
		}
	}
	var gen strings.Builder
	for _, o := range outputs {
		b, err := os.ReadFile(filepath.Join(prog, o))
		if err != nil {
			t.Fatal(err)
		}
		gen.Write(b)
	}
	return readCalls(t, log), gen.String()
}

// writeChain writes, in prog, Go packages chain/p0 ... chain/p<n-1> of
// module example.com/budget, each importing the one before, and chain.go,
// which imports every one and defines ChainDepth. It writes nothing when n is
// 0.
func writeChain(t *testing.T, prog string, n int) {
	t.Helper()
	if n == 0 {
		return
	}
	var imports strings.Builder
	for i := range n {
		body := "func Depth() int { return 0 }\n"
		if i > 0 {
			body = fmt.Sprintf("import prev \"example.com/budget/chain/p%d\"\n\nfunc Depth() int { return prev.Depth() + 1 }\n", i-1)
		}
		writeFile(t, filepath.Join(prog, "chain", fmt.Sprintf("p%d", i), "p.go"), fmt.Sprintf("package p%d\n\n%s", i, body))
		fmt.Fprintf(&imports, "\t_ \"example.com/budget/chain/p%d\"\n", i)
	}
	writeFile(t, filepath.Join(prog, "chain.go"), fmt.Sprintf(
		"package main\n\nimport (\n%s\n\tlast \"example.com/budget/chain/p%d\"\n)\n\n// ChainDepth is the depth of the last package of the chain.\nfunc ChainDepth() int { return last.Depth() }\n",
		imports.String(), n-1))
}

// writeGoMod makes prog the Go module its imports resolve in, as a user's
// project is: example.com/budget, requiring each Go library of the manifest
// from a local copy of its sources.
func (h *harness) writeGoMod(t *testing.T, prog string) {
	t.Helper()
	var mod strings.Builder
	mod.WriteString("module example.com/budget\n\ngo 1.24\n")
	for _, p := range h.toolchain.thirdParty() {
		dir := filepath.Join(prog, "mods", filepath.FromSlash(p.importPath))
		for _, src := range p.srcs {
			copyFile(t, src, filepath.Join(dir, filepath.Base(src)))
		}
		writeFile(t, filepath.Join(dir, "go.mod"), fmt.Sprintf("module %s\n\ngo 1.21\n", p.importPath))
		fmt.Fprintf(&mod, "\nrequire %s v1.0.0\nreplace %s => ./mods/%s\n", p.importPath, p.importPath, p.importPath)
	}
	writeFile(t, filepath.Join(prog, "go.mod"), mod.String())
}

// runWorker sends c's transpile to `gala --persistent_worker` as rules-gala's
// transpile rule does: with the export data of every Go library and of the
// standard library, and the sources of the direct ones. It runs in dir, as
// the worker runs in the execution root, which holds the go.mod of a
// repository with Go code at its root: a Go package the export data missed
// would be looked up with `go list` there.
func (h *harness) runWorker(t *testing.T, dir string, env []string, c budgetCase, outputs []string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/repo\n\ngo 1.24\n")
	var inputs, outs, exports, srcs []string
	for i := range c.inputs {
		inputs = append(inputs, filepath.Join("prog", c.inputs[i]))
		outs = append(outs, filepath.Join("prog", outputs[i]))
	}
	for _, p := range h.toolchain.pkgs {
		exports = append(exports, p.importPath+"="+p.export)
		if p.direct {
			srcs = append(srcs, p.importPath+"="+filepath.Dir(p.srcs[0]))
		}
	}
	args := []string{
		"transpile-package",
		"--inputs=" + strings.Join(inputs, ","),
		"--outputs=" + strings.Join(outs, ","),
		"--search=prog",
		"--go-src=" + strings.Join(srcs, ","),
		"--go-export=" + strings.Join(exports, ","),
		"--go-stdlib-export=" + h.toolchain.stdlibDir,
	}

	cmd := exec.Command(h.gala, "--persistent_worker")
	cmd.Dir, cmd.Env = dir, env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Write(encodeWorkRequest(args)); err != nil {
		t.Fatalf("writing the work request: %v", err)
	}
	exitCode, output, err := readWorkResponse(bufio.NewReader(stdout))
	stdin.Close()
	if werr := cmd.Wait(); err == nil && werr != nil {
		err = werr
	}
	if err != nil {
		t.Fatalf("gala worker: %v\n%s", err, stderr.Bytes())
	}
	if exitCode != 0 {
		t.Fatalf("gala worker: transpile-package exited with %d:\n%s%s", exitCode, output, stderr.Bytes())
	}
}

// encodeWorkRequest is a length-delimited blaze.worker.WorkRequest with
// args as its arguments (field 1).
func encodeWorkRequest(args []string) []byte {
	var msg []byte
	for _, a := range args {
		msg = append(msg, 1<<3|2)
		msg = binary.AppendUvarint(msg, uint64(len(a)))
		msg = append(msg, a...)
	}
	return append(binary.AppendUvarint(nil, uint64(len(msg))), msg...)
}

// readWorkResponse reads a length-delimited blaze.worker.WorkResponse and
// returns its exit_code (field 1) and output (field 2).
func readWorkResponse(r *bufio.Reader) (exitCode int, output string, err error) {
	size, err := binary.ReadUvarint(r)
	if err != nil {
		return 0, "", fmt.Errorf("reading the work response: %w", err)
	}
	msg := make([]byte, size)
	if _, err := io.ReadFull(r, msg); err != nil {
		return 0, "", fmt.Errorf("reading the work response: %w", err)
	}
	m := bufio.NewReader(bytes.NewReader(msg))
	for {
		key, err := binary.ReadUvarint(m)
		if err == io.EOF {
			return exitCode, output, nil
		}
		if err != nil {
			return 0, "", fmt.Errorf("decoding the work response: %w", err)
		}
		switch key & 7 {
		case 0:
			v, err := binary.ReadUvarint(m)
			if err != nil {
				return 0, "", fmt.Errorf("decoding the work response: %w", err)
			}
			if key>>3 == 1 {
				exitCode = int(int32(v))
			}
		case 2:
			n, err := binary.ReadUvarint(m)
			if err != nil {
				return 0, "", fmt.Errorf("decoding the work response: %w", err)
			}
			b := make([]byte, n)
			if _, err := io.ReadFull(m, b); err != nil {
				return 0, "", fmt.Errorf("decoding the work response: %w", err)
			}
			if key>>3 == 2 {
				output = string(b)
			}
		default:
			return 0, "", fmt.Errorf("decoding the work response: unexpected wire type %d", key&7)
		}
	}
}

// toolchain is what the go_toolchain_manifest rule names (see defs.bzl),
// resolved to files on disk.
type toolchain struct {
	// goroot is the pinned Go SDK.
	goroot string
	// stdlibDir is rules_go's compiled standard library, an <importpath>.a
	// archive per package.
	stdlibDir string
	pkgs      []goPackage
}

type goPackage struct {
	importPath string
	export     string
	direct     bool
	srcs       []string
}

// thirdParty is the libraries that are not the test program's own.
func (tc toolchain) thirdParty() []goPackage {
	var out []goPackage
	for _, p := range tc.pkgs {
		if !strings.HasPrefix(p.importPath, "example.com/budget/") {
			out = append(out, p)
		}
	}
	return out
}

func readManifest(t *testing.T, path string) toolchain {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var tc toolchain
	byPath := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		f := strings.Fields(line)
		switch {
		case len(f) == 2 && f[0] == "goroot":
			tc.goroot = sdkRoot(t, f[1])
		case len(f) == 2 && f[0] == "stdlib":
			tc.stdlibDir = filepath.Dir(rlocation(t, f[1]))
		case len(f) == 4 && f[0] == "pkg":
			if _, ok := byPath[f[1]]; ok {
				continue // reached from more than one dep
			}
			byPath[f[1]] = len(tc.pkgs)
			tc.pkgs = append(tc.pkgs, goPackage{importPath: f[1], export: rlocation(t, f[2]), direct: f[3] == "direct"})
		case len(f) == 3 && f[0] == "src":
			i, ok := byPath[f[1]]
			if !ok {
				t.Fatalf("manifest: sources of %s before its pkg line", f[1])
			}
			if src := rlocation(t, f[2]); !slices.Contains(tc.pkgs[i].srcs, src) {
				tc.pkgs[i].srcs = append(tc.pkgs[i].srcs, src)
			}
		default:
			t.Fatalf("manifest: unexpected line %q", line)
		}
	}
	return tc
}

// sdkRoot returns the Go SDK on disk whose root marker file is marker, a
// runfiles path. The runfiles hold the marker alone: a link to the SDK's, or,
// on Windows, a copy, whose location the runfiles manifest gives.
func sdkRoot(t *testing.T, marker string) string {
	t.Helper()
	candidates := []string{}
	if p, err := filepath.EvalSymlinks(rlocation(t, marker)); err == nil {
		candidates = append(candidates, p)
	}
	for _, m := range []string{os.Getenv("RUNFILES_MANIFEST_FILE"), filepath.Join(os.Getenv("RUNFILES_DIR"), "MANIFEST")} {
		if m == "" {
			continue
		}
		if r, err := runfiles.New(runfiles.ManifestFile(m)); err == nil {
			if p, err := r.Rlocation(marker); err == nil {
				candidates = append(candidates, p)
			}
		}
	}
	for _, c := range candidates {
		root := filepath.Dir(c)
		if info, err := os.Stat(filepath.Join(root, "src", "fmt")); err == nil && info.IsDir() {
			return root
		}
	}
	t.Fatalf("no Go SDK with sources at %v", candidates)
	return ""
}

// copySDK makes dst a Go SDK that is goroot with its go command replaced by
// shim: every other top-level entry links to goroot's.
func copySDK(t *testing.T, goroot, dst, shim string) {
	t.Helper()
	entries, err := os.ReadDir(goroot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dst, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "bin" {
			link(t, filepath.Join(goroot, e.Name()), filepath.Join(dst, e.Name()))
		}
	}
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	copyFile(t, shim, filepath.Join(dst, "bin", "go"+exe))
}

// link makes newname a link to oldname. Windows without the privilege to
// create symbolic links gets a directory junction, or a copy of a file.
func link(t *testing.T, oldname, newname string) {
	t.Helper()
	err := os.Symlink(oldname, newname)
	if err == nil || runtime.GOOS != "windows" {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	info, statErr := os.Stat(oldname)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if !info.IsDir() {
		copyFile(t, oldname, newname)
		return
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", newname, oldname).CombinedOutput(); err != nil {
		t.Fatalf("linking %s to %s: %v\n%s", newname, oldname, err, out)
	}
}

// readCalls returns the go commands the shim logged, one per line; none
// when it logged nothing.
func readCalls(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func indent(lines []string) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString("  go " + l + "\n")
	}
	return b.String()
}

func rlocation(t *testing.T, path string) string {
	t.Helper()
	if path == "" {
		t.Skip("run with Bazel: the test reads its inputs from its runfiles")
	}
	p, err := runfiles.Rlocation(path)
	if err != nil {
		t.Fatalf("locating %s: %v", path, err)
	}
	return p
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		copyFile(t, path, filepath.Join(dst, rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
