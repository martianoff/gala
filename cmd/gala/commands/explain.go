package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	errdocs "martianoff/gala/docs/errors"
	"martianoff/gala/galaerr"
)

var explainList bool

var explainCmd = &cobra.Command{
	Use:   "explain [GALA-Exxxx]",
	Short: "Explain a GALA error code",
	Long: `Explain a GALA error code.

Prints the reference page for a diagnostic: what triggers it, a minimal repro,
the compiler's real output, how to fix it, and what the code deliberately does
not cover. The pages are compiled into the binary, so this works offline.

The code may be written in any of the forms it appears in:

  gala explain GALA-E0044
  gala explain E0044
  gala explain 44

Examples:
  gala explain GALA-E0042      # why a bare lambda parameter is rejected
  gala explain --list          # every documented code`,
	Args: cobra.MaximumNArgs(1),
	Run:  runExplain,
}

func init() {
	explainCmd.Flags().BoolVar(&explainList, "list", false, "List every documented error code")
	rootCmd.AddCommand(explainCmd)
}

func runExplain(cmd *cobra.Command, args []string) {
	if explainList {
		for _, code := range errdocs.Codes() {
			fmt.Printf("%s  %s\n", code, errdocs.Title(code))
		}
		return
	}

	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: no error code given")
		fmt.Fprintln(os.Stderr, "Usage: gala explain GALA-E0044   (or --list)")
		os.Exit(1)
	}

	page, err := errdocs.Page(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(page)

	// The published page carries the same content plus cross-links, so point
	// at it rather than leaving the reader to guess the URL.
	if code := errdocs.Normalize(args[0]); code != "" {
		fmt.Printf("\nOnline: %s\n", galaerr.DocsURL(galaerr.ErrorCode(code)))
	}
}
