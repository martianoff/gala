package gala2lean

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/runfiles"
	"github.com/stretchr/testify/require"
)

// TestGoStdImportsInStep checks that gala2lean and galaspec exempt the same
// Go standard-library imports (goStdImports, Json.goStdImports): both rest
// on the assumption that those packages print nothing while they
// initialize, and the two models must make it for the same packages.
func TestGoStdImportsInStep(t *testing.T) {
	path, err := runfiles.Rlocation(os.Getenv("GALASPEC_JSON_LEAN"))
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	def := regexp.MustCompile(`(?s)def goStdImports : List String :=\s*\[(.*?)\]`).FindStringSubmatch(string(data))
	require.NotNil(t, def, "no goStdImports in formal/Gala/Json.lean")
	var lean []string
	for _, m := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(def[1], -1) {
		lean = append(lean, m[1])
	}
	require.Equal(t, lean, goStdImports, "gala2lean's goStdImports differs from galaspec's:\n%s", strings.Join(lean, ", "))
}
