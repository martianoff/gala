package analyzer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GALA-E0066 covers a declaration named like an export of a Go package that
// the file, or another file of its package, dot-imports: Go rejects either,
// and the clash reached go build (#1462).
func TestDotImportedGoExportCollision(t *testing.T) {
	tests := []struct {
		name     string
		main     string
		siblings map[string]string
		want     string // a substring of the error; "" when there is none
	}{
		{
			name: "dot import in the same file",
			main: `package main

import . "sort"

func Strings(x int) int = x

func main() {
    Println(IntsAreSorted(nil), Strings(3))
}
`,
			want: "function Strings is also exported by sort",
		},
		{
			name: "dot import in another file of the package",
			main: `package main

func Strings(x int) int = x

func main() {
    Println(Strings(3), sorted())
}
`,
			siblings: map[string]string{"other.gala": `package main

import . "sort"

func sorted() bool = IntsAreSorted(nil)
`},
			want: "function Strings is also exported by sort",
		},
		{
			// Go's strings declares no JoinStrs; GALA's strings, of the
			// same name, does, but is not dot-imported.
			name: "a name of a GALA package named like the dot-imported Go one",
			main: `package main

import . "strings"
import gs "martianoff/gala/strings"

func JoinStrs(x int) int = x

func main() {
    Println(JoinStrs(3), gs.TrimSpace(" a "), ToUpper("a"))
}
`,
		},
		{
			name: "no name in common",
			main: `package main

import . "sort"

func Sorted(x int) int = x

func main() {
    Println(IntsAreSorted(nil), Sorted(3))
}
`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			skipIfNoGoSDK(t)
			err := analyzeSources(t, tc.main, tc.siblings)
			if tc.want == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0066")
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}
