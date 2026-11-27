package analyzer

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"martianoff/gala/internal/transpiler"
)

// goDotImportedType ties a bare name to the dot import whose Go types
// declare it: a Go package's, or the hand-written .go files of a GALA one,
// looked up by its import path so a Go package of its name (io/fs beside
// GALA's fs) is not taken for it (#1452).
func TestGoDotImportedType(t *testing.T) {
	gi := transpiler.NewGoTypeInfo()
	gi.Types["lib.Bare"] = &transpiler.GoTypeData{Kind: "named"}
	gi.Types["example.com/x/lib.Bare"] = &transpiler.GoTypeData{Kind: "named"}
	gi.Types["fs.FileMode"] = &transpiler.GoTypeData{Kind: "named"} // Go's io/fs, by name only
	gi.Types["url.Values"] = &transpiler.GoTypeData{Kind: "named"}
	gi.Types["example.com/x/lib.hidden"] = &transpiler.GoTypeData{Kind: "named"}

	tests := []struct {
		name string
		dot  importBinding
		typ  string
		want transpiler.Type
	}{
		{
			name: "a GALA package's Go type",
			dot:  importBinding{Path: "example.com/x/lib", PkgName: "lib", IsGala: true},
			typ:  "Bare",
			want: transpiler.NamedType{Package: "lib", Name: "Bare"},
		},
		{
			name: "a Go package of a GALA package's name is not it",
			dot:  importBinding{Path: "martianoff/gala/fs", PkgName: "fs", IsGala: true},
			typ:  "FileMode",
		},
		{
			name: "a module's root GALA package, whose path is its name",
			dot:  importBinding{Path: "lib", PkgName: "lib", IsGala: true},
			typ:  "Bare",
			want: transpiler.NamedType{Package: "lib", Name: "Bare"},
		},
		{
			name: "a Go package's type",
			dot:  importBinding{Path: "net/url", PkgName: "url"},
			typ:  "Values",
			want: transpiler.NamedType{Package: "url", Name: "Values", ImportPath: "net/url"},
		},
		{
			name: "an unexported Go type, which a dot import does not bring in",
			dot:  importBinding{Path: "example.com/x/lib", PkgName: "lib", IsGala: true},
			typ:  "hidden",
		},
		{
			name: "a name none declares",
			dot:  importBinding{Path: "example.com/x/lib", PkgName: "lib", IsGala: true},
			typ:  "Missing",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := fileQualifiers{dots: []importBinding{tc.dot}}.goDotImportedType(tc.typ, gi)
			assert.Equal(t, tc.want != nil, ok)
			if tc.want != nil {
				assert.Equal(t, tc.want, got)
			}
		})
	}
}
