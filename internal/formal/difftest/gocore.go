package difftest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"martianoff/gala/internal/formal/gocoreexport"
)

// The GoCore lane (docs/FORMAL_SEMANTICS.MD, "M4: GoCore and the Go AST
// exporter"): GoCore, the Lean model of the Go the transpiler emits, runs the
// transformer's actual output, read back by the Go AST exporter, and is
// compared with `go run` of the same Go. Its verdicts measure GoCore's
// fidelity to Go, not the transpiler: a GoCore DISAGREE is a bug in GoCore
// or the exporter, never listed in known_disagreements.json, and fails the
// run until it is fixed or the construct is reported as unsupported.

// GoCoreEnv is the environment variable that names the gocore binary.
const GoCoreEnv = "GALA_GOCORE_BIN"

// GoCoreTimeout bounds one gocore run. gocore stops itself when it runs out
// of steps or depth, so reaching it is a broken gocore, not an inconclusive
// run.
const GoCoreTimeout = 5 * time.Minute

// Resource is the verdict for a run Go aborted for lack of stack or memory, or
// that ran past its time limit: outside every claim (docs/FORMAL_SEMANTICS.MD,
// M4), reported and never compared.
const Resource Verdict = "RESOURCE"

// GoCoreRunner runs a GoCore JSON document; *GoCore is the real one.
type GoCoreRunner interface {
	Run(path string) (GoCoreRun, error)
}

// GoCore runs the gocore binary.
type GoCore struct {
	Bin string
}

var _ GoCoreRunner = GoCore{}

// GoCoreRun is what a gocore run did.
type GoCoreRun struct {
	Stdout string
	// Outcome is "ok", "panic", "fuel", "unsupported:<construct>", "stuck"
	// or "malformed", as gocore's last stderr line spells it.
	Outcome string
	// Detail is the text after `gocore: panic: ` or `gocore: stuck: `.
	Detail string
}

// goCoreExitCodes is gocore's exit status for each outcome.
var goCoreExitCodes = map[string]int{"ok": 0, "panic": 1, "fuel": 2, "unsupported": 3, "malformed": 4, "stuck": 5}

// Run runs gocore on the document at path. The error is for a gocore that
// could not run or broke its contract: an outcome line that is missing or
// disagrees with the exit status.
func (g GoCore) Run(path string) (GoCoreRun, error) {
	ctx, cancel := context.WithTimeout(context.Background(), GoCoreTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, g.Bin, "run", path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return GoCoreRun{}, fmt.Errorf("gocore did not finish within %s on %s", GoCoreTimeout, path)
	}
	code := 0
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode()
	case err != nil:
		return GoCoreRun{}, fmt.Errorf("running gocore (%s): %w", g.Bin, err)
	}
	r, err := ParseGoCore(stdout.String(), stderr.String(), code)
	if err != nil {
		return GoCoreRun{}, fmt.Errorf("%w\nstderr:\n%s", err, stderr.String())
	}
	return r, nil
}

// ParseGoCore reads a gocore run from its output and exit status.
func ParseGoCore(stdout, stderr string, exitCode int) (GoCoreRun, error) {
	r := GoCoreRun{Stdout: stdout}
	for _, line := range strings.Split(strings.ReplaceAll(stderr, "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "gocore: outcome="):
			r.Outcome = strings.TrimPrefix(line, "gocore: outcome=")
		case strings.HasPrefix(line, "gocore: panic: "):
			r.Detail = strings.TrimPrefix(line, "gocore: panic: ")
		case strings.HasPrefix(line, "gocore: stuck: "):
			r.Detail = strings.TrimPrefix(line, "gocore: stuck: ")
		}
	}
	kind, _, _ := strings.Cut(r.Outcome, ":")
	want, ok := goCoreExitCodes[kind]
	switch {
	case r.Outcome == "":
		return GoCoreRun{}, errors.New("gocore: no outcome line")
	case !ok:
		return GoCoreRun{}, fmt.Errorf("gocore: unknown outcome %q", r.Outcome)
	case want != exitCode:
		return GoCoreRun{}, fmt.Errorf("gocore: outcome %s must exit with status %d, got %d", r.Outcome, want, exitCode)
	}
	return r, nil
}

// GoCoreRecord is how GoCore fared on one program: its run of the Go the
// transformer emitted, compared with `go run` of it.
type GoCoreRecord struct {
	Comparison
	// Construct is what GoCore does not run, for an UNSUPPORTED verdict.
	Construct string
}

// compareGoCore is the fidelity verdict: GoCore's run against Go's, on
// stdout and on how they end, a panic by the exact text Go prints after
// `panic: `. real's outcome and goPanic are Go's.
func compareGoCore(g GoCoreRun, real Run, goPanic string) GoCoreRecord {
	if r, ok := goResource(real); ok {
		return r
	}
	kind, construct, _ := strings.Cut(g.Outcome, ":")
	switch kind {
	case "unsupported":
		return GoCoreRecord{Comparison: Comparison{Unsupported, "outside GoCore: " + construct}, Construct: construct}
	case "fuel":
		return GoCoreRecord{Comparison: Comparison{Inconclusive, "gocore ran out of fuel"}}
	case "stuck":
		return GoCoreRecord{Comparison: Comparison{Disagree, "gocore is stuck (a GoCore or exporter bug): " + g.Detail}}
	case "malformed":
		return GoCoreRecord{Comparison: Comparison{Disagree, "gocore rejected the GoCore JSON document as malformed"}}
	}
	var parts []string
	switch {
	case kind == "ok" && real.Outcome.Kind != KindOK, kind == "panic" && real.Outcome.Kind != KindPanic:
		parts = append(parts, fmt.Sprintf("outcome: gocore=%s, go=%s", kind, real.Outcome))
	case kind == "panic" && g.Detail != goPanic:
		parts = append(parts, fmt.Sprintf("panic: gocore=%q, go=%q", g.Detail, goPanic))
	}
	if g.Stdout != real.Stdout {
		parts = append(parts, "stdout: "+firstDifference("gocore", g.Stdout, "go", real.Stdout))
	}
	if len(parts) > 0 {
		return GoCoreRecord{Comparison: Comparison{Disagree, strings.Join(parts, "; ")}}
	}
	return GoCoreRecord{Comparison: Comparison{Verdict: Agree}}
}

// goResource is the RESOURCE verdict for a Go run that ran past its time
// limit or aborted for lack of stack or memory; ok is false for any other run.
func goResource(real Run) (r GoCoreRecord, ok bool) {
	switch {
	case real.Outcome.Kind == KindTimeout:
		return GoCoreRecord{Comparison: Comparison{Resource, "the Go program ran past its time limit"}}, true
	case real.Outcome.Kind == KindCrash && isResource(real.Outcome.Detail):
		return GoCoreRecord{Comparison: Comparison{Resource, "Go aborted: " + real.Outcome.Detail}}, true
	}
	return GoCoreRecord{}, false
}

// isResource reports whether a Go runtime fatal error is a lack of stack or
// memory.
func isResource(msg string) bool {
	return strings.Contains(msg, "stack overflow") || strings.Contains(msg, "out of memory") ||
		strings.Contains(msg, "cannot allocate memory")
}

// goPanicText is the text of the panic that ended a Go run, without the
// ` [recovered]` a re-panic adds. A panic raised while another unwinds is
// printed after it, tab-indented, so the last panic line is the one that
// ended the run; a line the program printed itself comes before them all.
func goPanicText(stderr string) string {
	text := ""
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimLeft(strings.TrimRight(line, "\r"), "\t")
		if msg, ok := strings.CutPrefix(line, "panic: "); ok {
			text, _, _ = strings.Cut(msg, " [recovered")
		}
	}
	return text
}

// GoProgram is the Go of a compiled program: its main package's file and the
// sources of every GALA package it imports, directly or not, by import path.
type GoProgram struct {
	Main     string
	Packages map[string]map[string]string
}

// exportGoCore writes prog's GoCore JSON document under dir, named for id,
// and returns its path.
func exportGoCore(dir, id string, prog GoProgram) (string, error) {
	pkgs := []gocoreexport.PackageSource{{Path: "main", Files: []gocoreexport.Source{{Name: "main.go", Text: prog.Main}}}}
	paths := make([]string, 0, len(prog.Packages))
	for p := range prog.Packages {
		paths = append(paths, p)
	}
	// The exporter keeps the order given; sorted, the document is the same
	// from run to run.
	slices.Sort(paths)
	for _, p := range paths {
		ps := gocoreexport.PackageSource{Path: p}
		for name, text := range prog.Packages[p] {
			ps.Files = append(ps.Files, gocoreexport.Source{Name: name, Text: text})
		}
		pkgs = append(pkgs, ps)
	}
	doc, err := gocoreexport.Export(pkgs)
	if err != nil {
		return "", err
	}
	data, err := gocoreexport.Marshal(doc)
	if err != nil {
		return "", err
	}
	file := filepath.Join(dir, safeName(id)+".gocore.json")
	return file, os.WriteFile(file, data, 0o644)
}

// safeName is id as a file name.
func safeName(id string) string {
	return strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(id)
}
