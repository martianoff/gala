package transformer_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEveryErrorCodeHasADocPage is the completeness half of the error-doc
// contract.
//
// TestErrorDocsQuoteRealOutput checks that the pages which exist quote what the
// compiler actually prints. It is explicitly scoped to the codes whose output
// was captured by hand, and says so. Nothing checked the other direction: that
// a code introduced today gets a page at all. Codes have been added in batches
// before — five redeclaration codes in one change — and the pages arrived in a
// separate change months later, so for that whole window a user searching a
// real diagnostic found nothing.
//
// The codes are read out of galaerr/errors.go by parsing its declarations
// rather than by grepping it. Grepping finds "GALA-E0042" in the doc comment
// that uses it as an example of the format and reports a missing page for a
// code that does not exist; parsing sees only what is declared.
func TestEveryErrorCodeHasADocPage(t *testing.T) {
	root := repoRoot(t)

	codes := declaredErrorCodes(t, filepath.Join(root, "galaerr", "errors.go"))
	require.NotEmpty(t, codes, "no error codes parsed; this guard would pass vacuously")

	var missing []string
	for _, code := range codes {
		page := filepath.Join(root, "docs", "errors", code+".md")
		if _, err := os.Stat(page); err != nil {
			missing = append(missing, code)
		}
	}
	require.Empty(t, missing,
		"error code(s) %v are declared in galaerr/errors.go with no page under docs/errors/. "+
			"Every code needs one: the pages are what a user finds when they paste a "+
			"diagnostic into a search engine. Add docs/errors/<code>.md with a minimal "+
			"repro, the expected output, the fix, and why the rule exists.",
		missing)

	// The reverse direction. A page for a code that no longer exists is a
	// result users can still reach, describing behaviour the compiler no
	// longer has.
	declared := make(map[string]bool, len(codes))
	for _, c := range codes {
		declared[c] = true
	}
	pages, err := filepath.Glob(filepath.Join(root, "docs", "errors", "GALA-E*.md"))
	require.NoError(t, err)
	require.NotEmpty(t, pages, "no error pages found; check the //docs/errors:error_docs filegroup")

	var orphaned []string
	for _, p := range pages {
		code := strings.TrimSuffix(filepath.Base(p), ".md")
		if !declared[code] {
			orphaned = append(orphaned, code)
		}
	}
	require.Empty(t, orphaned,
		"page(s) %v under docs/errors/ have no matching code in galaerr/errors.go. "+
			"Either the code was removed and the page should go with it, or it was "+
			"renumbered — which it should not be, since the codes are a stable "+
			"external interface.",
		orphaned)
}

var errorCodePattern = regexp.MustCompile(`^GALA-E\d{4}$`)

// declaredErrorCodes returns the GALA-Exxxx values declared as ErrorCode
// constants in the given file, in sorted order.
func declaredErrorCodes(t *testing.T, path string) []string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	require.NoError(t, err, "could not parse %s. Under Bazel this is usually a "+
		"missing data dependency on //galaerr:errors_src.", path)

	seen := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for _, v := range spec.Values {
			lit, ok := v.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil || !errorCodePattern.MatchString(s) {
				continue
			}
			seen[s] = true
		}
		return true
	})

	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// repoRoot returns the staged repository root, reusing the search path the
// rest of this package already resolves.
func repoRoot(t *testing.T) string {
	t.Helper()
	roots := getStdSearchPath()
	require.NotEmpty(t, roots, "cannot locate the repository root")
	return roots[0]
}
