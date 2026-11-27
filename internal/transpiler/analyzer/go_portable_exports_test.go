package analyzer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// portableExports keeps an exported name when the files declaring it build
// on every portable target between them: a plain file, one whose constraint
// always holds, or complementary platform files. A name only some targets
// get, or an unexported one, is left out.
func TestPortableExports(t *testing.T) {
	decl := "\n\npackage p\n\nvar Exported = 1\n"
	tests := []struct {
		name     string
		files    map[string]string // file name -> source declaring Exported
		portable bool
	}{
		{"plain file", map[string]string{"p.go": decl}, true},
		{"name not a platform", map[string]string{"lin_ux.go": decl}, true},
		{"constraint true everywhere", map[string]string{"release.go": "//go:build go1.21 && (unix || windows || !unix)" + decl}, true},
		{"unix and windows files", map[string]string{"p_windows.go": decl, "p_other.go": "//go:build !windows" + decl}, true},
		{"cgo and no-cgo files", map[string]string{"p_cgo.go": "package p\n\nimport \"C\"\n\nvar Exported = 1\n", "p_nocgo.go": "//go:build !cgo" + decl}, true},
		{"GOOS file", map[string]string{"p_windows.go": decl}, false},
		{"GOOS_GOARCH file", map[string]string{"p_linux_amd64.go": decl}, false},
		{"unsupported GOOS file", map[string]string{"p_zos.go": decl}, false},
		{"GOOS file with a second dot", map[string]string{"p_linux.pb.go": decl}, false},
		{"ignored file", map[string]string{"_p.go": decl}, false},
		{"go:build constraint", map[string]string{"tagged.go": "//go:build linux" + decl}, false},
		{"invalid go:build constraint", map[string]string{"bad.go": "//go:build linux &&" + decl}, false},
		{"legacy +build line", map[string]string{"legacy.go": "// +build windows" + decl}, false},
		{"cgo constraint", map[string]string{"cgo.go": "//go:build cgo" + decl}, false},
		{"import C", map[string]string{"c.go": "package p\n\nimport \"C\"\n\nvar Exported = 1\n"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			var files []*ast.File
			sources := map[string]string{"base.go": "package p\n\nfunc Common() {}\n\nfunc helper() {}\n"}
			for name, src := range tc.files {
				sources[name] = src
			}
			for name, src := range sources {
				f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
				require.NoError(t, err)
				files = append(files, f)
			}
			// `import "C"` and two declarations of Exported do not type-check;
			// the names are in scope all the same.
			pkg, _ := (&types.Config{Error: func(error) {}}).Check("example.com/p", fset, files, nil)
			require.NotNil(t, pkg.Scope().Lookup("Exported"))

			want := []string{"Common"}
			if tc.portable {
				want = []string{"Common", "Exported"}
			}
			assert.Equal(t, want, portableExports(pkg, fset, files))
		})
	}
}
