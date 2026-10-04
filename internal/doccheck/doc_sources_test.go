package doccheck

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDocListingsMatchSources keeps code listings in step with the files they
// are taken from. A fenced block of any language marked with
//
//	<!-- doc-source: examples/go_interop/pricing/pricing.gala -->
//
// on the line before its fence (or before the block's `doc-check` directive)
// is an excerpt of that file: its non-blank lines must appear, in order, as
// lines of the file, ignoring indentation and trailing spaces. An excerpt may
// skip lines but not change one, so a listing whose source is edited, and
// still tested, cannot silently go stale on the page.
func TestDocListingsMatchSources(t *testing.T) {
	root := repoRoot(t)
	var pages []string
	for _, g := range docGlobs {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(g)))
		require.NoError(t, err)
		pages = append(pages, matches...)
	}
	sort.Strings(pages)

	checked := 0
	var problems []string
	for _, page := range pages {
		data, err := os.ReadFile(page)
		require.NoError(t, err)
		rel, err := filepath.Rel(root, page)
		require.NoError(t, err)
		for _, l := range sourcedListings(string(data)) {
			checked++
			loc := fmt.Sprintf("%s:%d", filepath.ToSlash(rel), l.line)
			src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(l.source)))
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: cannot read %s: %v", loc, l.source, err))
				continue
			}
			if missing, ok := excerptOf(l.lines, string(src)); !ok {
				problems = append(problems, fmt.Sprintf("%s: not an excerpt of %s; no match, in order, for line %q",
					loc, l.source, missing))
			}
		}
	}
	require.NotZero(t, checked, "no `doc-source` listings found; check the data dependencies of this test")
	require.Empty(t, problems, "Listings differ from their source files. Copy the source "+
		"into the page again (or fix the marker):\n  %s", strings.Join(problems, "\n  "))
}

var (
	sourceMarker = regexp.MustCompile(`^[ \t]*<!--[ \t]*doc-source:[ \t]*(\S+)[ \t]*-->[ \t]*$`)
	anyFence     = regexp.MustCompile("^[ \t]*```")
)

type listing struct {
	line   int    // 1-based line of the opening fence
	source string // repository-relative path from the marker
	lines  []string
}

// sourcedListings returns the page's fenced blocks that carry a doc-source
// marker, either directly above the fence or above its doc-check directive.
func sourcedListings(page string) []listing {
	lines := strings.Split(strings.ReplaceAll(page, "\r\n", "\n"), "\n")
	var out []listing
	for i := 0; i < len(lines); i++ {
		if !anyFence.MatchString(lines[i]) {
			continue
		}
		end := i + 1
		for end < len(lines) && strings.TrimSpace(lines[end]) != "```" {
			end++
		}
		marker := i - 1
		if marker >= 0 && directiveLine.MatchString(lines[marker]) {
			marker--
		}
		if marker >= 0 {
			if m := sourceMarker.FindStringSubmatch(lines[marker]); m != nil {
				out = append(out, listing{line: i + 1, source: m[1], lines: lines[i+1 : min(end, len(lines))]})
			}
		}
		i = end
	}
	return out
}

// excerptOf reports whether every non-blank line of excerpt matches a line of
// src, in order, comparing trimmed text. On failure it returns the first line
// that found no match.
func excerptOf(excerpt []string, src string) (string, bool) {
	srcLines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	pos := 0
	for _, l := range excerpt {
		want := strings.TrimSpace(l)
		if want == "" {
			continue
		}
		for pos < len(srcLines) && strings.TrimSpace(srcLines[pos]) != want {
			pos++
		}
		if pos == len(srcLines) {
			return want, false
		}
		pos++
	}
	return "", true
}
