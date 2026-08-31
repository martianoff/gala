package commands

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/build"
)

// diagnosticsJSON is set by the --json flag on the commands that compile. When
// on, failures are emitted as a machine-readable envelope on stdout instead of
// a framed snippet on stderr — see galaerr/json.go for why a consumer should
// not have to parse the frame.
var diagnosticsJSON bool

// addDiagnosticsJSONFlag registers --json on a compiling command.
func addDiagnosticsJSONFlag(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&diagnosticsJSON, "json", false,
		"Emit diagnostics as JSON on stdout instead of framed text on stderr")
}

// exitBuildFailed reports a failed build and exits. When the build could not
// choose between several main packages it renders the candidates plus an
// example naming one, using cmd.Name() so `gala run` and `gala build` each
// suggest themselves; every other failure goes through the rich renderer.
func exitBuildFailed(cmd *cobra.Command, err error) {
	var multi *build.MultipleMainPackagesError
	isMultiMain := errors.As(err, &multi)

	if diagnosticsJSON {
		// The ambiguous-main failure carries its remediation in the CLI layer
		// rather than in the error, so hand that text to the JSON path too —
		// it is the one failure whose whole value is the suggestion, and a
		// bare `{"message": "..."}` would drop it.
		hint := ""
		if isMultiMain {
			hint = fmt.Sprintf("name the one you want, for example: gala %s ./%s",
				cmd.Name(), suggestedMain(multi.Candidates))
		}
		exitBuildFailedJSON(err, hint)
		return // unreachable; exitBuildFailedJSON exits, but do not rely on it
	}

	if isMultiMain {
		fmt.Fprintf(os.Stderr, "Error: %v\n\nName the one you want, for example:\n  gala %s ./%s\n",
			multi, cmd.Name(), suggestedMain(multi.Candidates))
	} else {
		fmt.Fprintln(os.Stderr, galaerr.RenderRich(err, galaerr.Options{Color: galaerr.ColorEnabled()}))
	}
	os.Exit(1)
}

// exitBuildFailedJSON emits the JSON envelope and exits. It goes to stdout so a
// caller can capture diagnostics without also capturing progress chatter, and
// falls back to the text renderer if the envelope cannot be marshalled — a
// failed build must never exit 0 or print nothing.
// hint, when non-empty, is remediation the CLI knows but the error does not;
// it is attached to the first diagnostic so JSON consumers see the same advice
// the text output prints.
func exitBuildFailedJSON(err error, hint string) {
	out, marshalErr := galaerr.RenderJSONWithHint(err, hint)
	if marshalErr != nil {
		fmt.Fprintln(os.Stderr, galaerr.RenderRich(err, galaerr.Options{Color: false}))
		os.Exit(1)
	}
	fmt.Println(out)
	os.Exit(1)
}

// suggestedMain picks the candidate to put in the example. A command under
// cmd/ wins: that is where Go convention — and so GALA's — puts the program
// the user most likely meant, so a project with cmd/app alongside a benchmark
// tool gets pointed at cmd/app rather than whichever path sorts first.
func suggestedMain(candidates []string) string {
	for _, c := range candidates {
		if strings.HasPrefix(c, "cmd/") {
			return c
		}
	}
	return candidates[0]
}
