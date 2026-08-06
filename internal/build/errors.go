package build

import (
	"fmt"
	"strings"
)

// MultipleMainPackagesError reports that a build rooted at the project
// directory found no package of its own to build and could not pick between
// the executables in its subdirectories. Candidates are project-relative and
// slash-separated ("cmd/galakv"), in the form a user can pass straight back on
// the command line.
//
// Carried as a typed error rather than a formatted string so `gala build` and
// `gala run` can each append their own "name one, for example" hint.
type MultipleMainPackagesError struct {
	Candidates []string
}

func (e *MultipleMainPackagesError) Error() string {
	var b strings.Builder
	b.WriteString("no main package in the project root; found several in subdirectories:")
	for _, c := range e.Candidates {
		fmt.Fprintf(&b, "\n  ./%s", c)
	}
	return b.String()
}
