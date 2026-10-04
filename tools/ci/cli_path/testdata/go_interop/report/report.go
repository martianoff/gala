// Package report is plain Go that imports the GALA package textstats from
// the same module: `gala build` transpiles textstats and builds both.
package report

import (
	"fmt"

	"example.com/gointerop/textstats"
)

// Line summarizes text, turning the GALA Try into a Go string.
func Line(text string) string {
	stats := textstats.Summarize(text)
	if stats.IsFailure() {
		return "error: " + stats.GetError().Error()
	}
	s := stats.Get()
	return fmt.Sprintf("%d lines, %d words, longest %q", s.Lines.Get(), s.Words.Get(), s.Longest.Get())
}
