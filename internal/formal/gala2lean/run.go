package gala2lean

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bazelbuild/rules_go/go/runfiles"
)

// Runner elaborates and runs translated programs with Lean (`lean --run`),
// against the prelude built once from its source.
type Runner struct {
	lean string
	dir  string
	n    int
}

// NewRunner builds the prelude (formal/Gala/Gala2Lean/Prelude.lean, at
// preludeSrc) with the lean executable at leanBin, in dir, where it also
// writes the programs it runs.
func NewRunner(leanBin, preludeSrc, dir string) (*Runner, error) {
	src, err := os.ReadFile(preludeSrc)
	if err != nil {
		return nil, err
	}
	rel := filepath.Join("Gala", "Gala2Lean", "Prelude")
	for _, sub := range []string{"src", "lib"} {
		if err := os.MkdirAll(filepath.Join(dir, sub, filepath.Dir(rel)), 0o755); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "src", rel+".lean"), src, 0o644); err != nil {
		return nil, err
	}
	r := &Runner{lean: leanBin, dir: dir}
	cmd := exec.Command(leanBin, "-DwarningAsError=true", "-o", filepath.Join(dir, "lib", rel+".olean"), filepath.Join(rel+".lean"))
	cmd.Dir = filepath.Join(dir, "src")
	cmd.Env = r.env()
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("building the gala2lean prelude with %s: %v\n%s", leanBin, err, out)
	}
	return r, nil
}

// LeanFromRunfiles is the lean executable that //formal:lean_runtime puts in
// a test's runfiles at rlocation loc. It is the installed file the runfiles
// manifest names, when there is one: Lean reads its library next to its
// executable, and on Windows the paths inside a runfiles tree can be longer
// than the system allows.
func LeanFromRunfiles(loc string) (string, error) {
	var manifests []string
	if m := os.Getenv("RUNFILES_MANIFEST_FILE"); m != "" {
		manifests = append(manifests, m)
	}
	if d := os.Getenv("RUNFILES_DIR"); d != "" {
		manifests = append(manifests, filepath.Join(d, "MANIFEST"))
	}
	for _, m := range manifests {
		data, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if real, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), loc+" "); ok {
				if _, err := os.Stat(real); err == nil {
					return real, nil
				}
			}
		}
	}
	bin, err := runfiles.Rlocation(loc)
	if err != nil {
		return "", err
	}
	_, err = os.Stat(bin)
	return bin, err
}

func (r *Runner) env() []string {
	return append(os.Environ(), "LEAN_PATH="+filepath.Join(r.dir, "lib"))
}

// RunKind is how running a translated program ended.
type RunKind int

const (
	// RanOK: the program finished; Outcome is "ok" or "panic:CLASS".
	RanOK RunKind = iota
	// Diverged: the program's result is divergence (Option's none).
	Diverged
	// TimedOut: the program ran past the time limit.
	TimedOut
	// Crashed: Lean died without an outcome line (a stack overflow, say).
	Crashed
	// NotMonotone: Lean could not prove a recursive definition monotone,
	// which partial_fixpoint needs; Decl names the function.
	NotMonotone
	// LeanRejected: Lean rejected the translation for another reason, a
	// gala2lean bug; Message has Lean's first error.
	LeanRejected
)

// RunResult is one run of a translated program.
type RunResult struct {
	Kind RunKind
	// Stdout is what the program printed (RanOK only).
	Stdout string
	// Outcome is the program's outcome in galaspec's spelling (RanOK only).
	Outcome string
	// Decl is the GALA function an error is in, when known.
	Decl string
	// Message is Lean's first error, or how the run crashed.
	Message string
}

// outcomeLine matches the line the prelude's runMain ends with.
var outcomeLine = regexp.MustCompile(`(?m)^gala2lean: outcome=(\S+)\r?$`)

// errorLine matches the position of a Lean error message.
var errorLine = regexp.MustCompile(`(?m)^[^\n]*?\.lean:(\d+):\d+: error(?:\([^)]*\))?: (.*)$`)

// Run elaborates res (translated with Options.Main) and runs it, for at most
// timeout. Warnings are errors, so that nothing but the program's output
// reaches stdout.
func (r *Runner) Run(res *Result, timeout time.Duration) (RunResult, error) {
	r.n++
	name := "Prog" + strconv.Itoa(r.n) + ".lean"
	file := filepath.Join(r.dir, "src", name)
	if err := os.WriteFile(file, []byte(res.Lean), 0o644); err != nil {
		return RunResult{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.lean, "-DwarningAsError=true", "--run", name)
	cmd.Dir = filepath.Join(r.dir, "src")
	cmd.Env = r.env()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return RunResult{Kind: TimedOut}, nil
	}
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return RunResult{}, fmt.Errorf("running %s: %w", r.lean, err)
	}
	if m := outcomeLine.FindAllStringSubmatch(stderr.String(), -1); len(m) > 0 {
		outcome := m[len(m)-1][1]
		if outcome == "diverge" {
			return RunResult{Kind: Diverged}, nil
		}
		return RunResult{Kind: RanOK, Stdout: strings.ReplaceAll(stdout.String(), "\r\n", "\n"), Outcome: outcome}, nil
	}
	if m := errorLine.FindStringSubmatch(stdout.String()); m != nil {
		line, _ := strconv.Atoi(m[1])
		rr := RunResult{Kind: LeanRejected, Message: m[2]}
		if d := res.DeclAt(line); d != nil {
			rr.Decl = d.Name
		}
		if strings.Contains(m[2], "to be monotone") {
			rr.Kind = NotMonotone
		}
		return rr, nil
	}
	return RunResult{Kind: Crashed, Message: firstNonEmpty(stderr.String(), stdout.String())}, nil
}

func firstNonEmpty(texts ...string) string {
	for _, t := range texts {
		for _, line := range strings.Split(t, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				return line
			}
		}
	}
	return "no output"
}
