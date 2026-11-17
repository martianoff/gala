package build

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// TestBuild_DefaultCycleAcrossFiles: GALA-E0135 for a default that depends
// on itself through defaults written in other files is reported in the file
// and at the line of the default the cycle comes back to, whichever file the
// call that expands it is in.
func TestBuild_DefaultCycleAcrossFiles(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		file  string // of the reported default, relative to the project
		line  int
		want  string
	}{
		{
			name: "through another package",
			files: map[string]string{
				"lib/a.gala": "package lib\n\nfunc G(x int = H()) int = x\n",
				"lib/b.gala": "package lib\n\nfunc H(y int = G()) int = y\n",
				"main.gala":  "package main\n\nimport \"example.com/cycle/lib\"\n\nfunc main() {\n    Println(lib.G())\n}\n",
			},
			file: "lib/a.gala", line: 3,
			want: "the default value of parameter x of G depends on itself, through the default of H.y: G.x → H.y → G.x",
		},
		{
			name: "to this file's default, through another file's",
			files: map[string]string{
				"a.gala":    "package main\n\nfunc g(x int = h()) int = x\n",
				"main.gala": "package main\n\nfunc h(y int = h()) int = y\n\nfunc main() {\n    Println(g())\n}\n",
			},
			file: "main.gala", line: 3,
			want: "the default value of parameter y of h depends on itself: h.y → h.y",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projectDir := newForeignGenGoProject(t, "example.com/cycle", tc.files)
			chdirForTest(t, projectDir)
			b, err := NewBuilder(projectDir, "test", false)
			require.NoError(t, err)
			_, err = b.Build("")
			require.Error(t, err)
			diags := galaerr.Diagnostics(err)
			require.NotEmpty(t, diags, "%v", err)
			d := diags[0]
			assert.Equal(t, string(galaerr.CodeDefaultCycle), d.Code, "%v", err)
			assert.Equal(t, tc.want, d.Message)
			assert.Equal(t, filepath.Join(projectDir, filepath.FromSlash(tc.file)), d.File)
			assert.Equal(t, tc.line, d.Line)
		})
	}
}
