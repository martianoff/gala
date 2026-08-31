package errdocs

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNormalizeAcceptsTheSpellingsPeopleType covers the forms a code arrives in:
// copied from a diagnostic, typed from memory, or reduced to the number.
func TestNormalizeAcceptsTheSpellingsPeopleType(t *testing.T) {
	for _, in := range []string{"GALA-E0044", "gala-e0044", "E0044", "e0044", "0044", "44", "  44  "} {
		require.Equal(t, "GALA-E0044", Normalize(in), "input %q", in)
	}
	for _, in := range []string{"", "nonsense", "E00X4", "GALA-E00044", "E-44"} {
		require.Empty(t, Normalize(in), "input %q should not normalize", in)
	}
}

// TestPagesAreEmbedded is the guard that the //go:embed pattern actually
// matched. A typo there yields an empty FS and a binary whose `explain` fails
// for every code, which is exactly the sort of break a test should catch rather
// than a user.
func TestPagesAreEmbedded(t *testing.T) {
	codes := Codes()
	require.NotEmpty(t, codes, "no error pages were embedded")
	require.Greater(t, len(codes), 40, "expected the full set of pages, got %d", len(codes))

	// Sorted and well-formed.
	for _, c := range codes {
		require.True(t, strings.HasPrefix(c, "GALA-E"), "unexpected code %q", c)
	}
	require.Equal(t, "GALA-E0001", codes[0])
}

// TestPageReturnsRealContent checks the served page is the page on disk, not a
// stub: it must carry the sections every reference page promises.
func TestPageReturnsRealContent(t *testing.T) {
	page, err := Page("GALA-E0002")
	require.NoError(t, err)
	require.Contains(t, page, "# GALA-E0002")
	require.Contains(t, page, "**When it fires.**")
	require.Contains(t, page, "**Fix.**")
}

// TestPageRejectsUnknownCodeHelpfully verifies the failure names what to do
// next rather than just reporting absence.
func TestPageRejectsUnknownCodeHelpfully(t *testing.T) {
	_, err := Page("GALA-E9999")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--list")

	_, err = Page("not-a-code")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a GALA error code")
}

// TestTitleIsTheHeadingWithoutTheCode backs `gala explain --list`, which prints
// the code and the title as two columns. The title must therefore be the
// summary ALONE — every page's H1 opens with the code, and leaving it in makes
// each listed row repeat itself.
func TestTitleIsTheHeadingWithoutTheCode(t *testing.T) {
	title := Title("GALA-E0044")
	require.Equal(t, "type has no such method", title)
	require.NotContains(t, title, "GALA-E0044",
		"the code is printed as its own column; the title must not repeat it")

	// An unknown code has no heading to read and falls back to the input.
	require.Equal(t, "GALA-E9999", Title("GALA-E9999"))
}
