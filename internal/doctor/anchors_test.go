package doctor

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/stretchr/testify/require"
)

// TestTroubleshootingAnchors checks that both copies of the troubleshooting
// page have a heading for every anchor a check links to. A renamed heading
// would otherwise leave doctor pointing at the top of the page.
func TestTroubleshootingAnchors(t *testing.T) {
	for _, page := range []string{"docs/TROUBLESHOOTING.MD", "website/docs/troubleshooting.md"} {
		t.Run(page, func(t *testing.T) {
			data, err := os.ReadFile(repoFile(t, page))
			require.NoError(t, err)
			ids := headingIDs(string(data))
			for _, anchor := range Anchors() {
				require.Contains(t, ids, anchor, "%s has no heading for #%s", page, anchor)
			}
		})
	}
}

// repoFile resolves a path relative to the repository root: through the
// runfiles tree under Bazel, else relative to this package's directory.
func repoFile(t *testing.T, rel string) string {
	t.Helper()
	if p, err := bazel.Runfile(rel); err == nil {
		return p
	}
	return filepath.Join("..", "..", filepath.FromSlash(rel))
}

var headingRe = regexp.MustCompile(`(?m)^#{1,6}\s+(.+?)\s*$`)

// headingIDs returns the ids GitHub and the site's Markdown renderer give the
// headings of a page: lower case, punctuation other than '-' and '_' dropped,
// spaces turned into '-'.
func headingIDs(page string) map[string]bool {
	ids := make(map[string]bool)
	for _, m := range headingRe.FindAllStringSubmatch(page, -1) {
		var sb strings.Builder
		for _, r := range strings.ToLower(m[1]) {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
				sb.WriteRune(r)
			case r == ' ':
				sb.WriteRune('-')
			}
		}
		ids[sb.String()] = true
	}
	return ids
}
