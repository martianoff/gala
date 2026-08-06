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

// exitBuildFailed reports a failed build and exits. When the build could not
// choose between several main packages it renders the candidates plus an
// example naming one, using cmd.Name() so `gala run` and `gala build` each
// suggest themselves; every other failure goes through the rich renderer.
func exitBuildFailed(cmd *cobra.Command, err error) {
	var multi *build.MultipleMainPackagesError
	if errors.As(err, &multi) {
		fmt.Fprintf(os.Stderr, "Error: %v\n\nName the one you want, for example:\n  gala %s ./%s\n",
			multi, cmd.Name(), suggestedMain(multi.Candidates))
	} else {
		fmt.Fprintln(os.Stderr, galaerr.RenderRich(err, galaerr.Options{Color: galaerr.ColorEnabled()}))
	}
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
