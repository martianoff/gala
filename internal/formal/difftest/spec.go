package difftest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"time"
)

// SpecEnv is the environment variable that names the galaspec binary.
const SpecEnv = "GALA_SPEC_BIN"

// SpecTimeout bounds one galaspec run. galaspec stops itself when it runs out
// of fuel, so reaching this is a broken galaspec, not an inconclusive run.
const SpecTimeout = 2 * time.Minute

// Spec runs galaspec, the reference interpreter.
type Spec struct {
	// Bin is the galaspec binary.
	Bin string
	// Fuel is the step budget passed as --fuel; 0 keeps galaspec's default.
	Fuel int
}

// Run runs galaspec on the Core JSON document at path. The error is for a
// galaspec that could not run or broke its contract (see ParseSpecOutcome);
// a program outside the subset or a malformed document is an Outcome.
func (s Spec) Run(path string) (Run, error) {
	ctx, cancel := context.WithTimeout(context.Background(), SpecTimeout)
	defer cancel()
	args := []string{"run", path}
	if s.Fuel > 0 {
		args = append(args, "--fuel", strconv.Itoa(s.Fuel))
	}
	cmd := exec.CommandContext(ctx, s.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return Run{}, fmt.Errorf("galaspec did not finish within %s on %s", SpecTimeout, path)
	}
	code := 0
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode()
	case err != nil:
		return Run{}, fmt.Errorf("running galaspec (%s): %w", s.Bin, err)
	}
	o, err := ParseSpecOutcome(stderr.String(), code)
	if err != nil {
		return Run{}, fmt.Errorf("%w\nstderr:\n%s", err, stderr.String())
	}
	return Run{Stdout: stdout.String(), Outcome: o}, nil
}
