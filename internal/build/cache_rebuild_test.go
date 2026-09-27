package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTranspile_RecopiesWhenOnlyANonGalaInputChanges runs the transpile step
// twice over the same project, editing between the runs only a file that is not
// a .gala source. The transpile step copies such files into gen/, which is the
// tree `go build` compiles; a cache hit that skips the copy compiles the
// previous run's copy, so the edit never reaches the binary.
//
// The assertion is on the copy in gen/ — exactly what the Go toolchain reads —
// so the test needs no Go toolchain.
func TestTranspile_RecopiesWhenOnlyANonGalaInputChanges(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		// edit names the one file rewritten between the runs.
		edit        string
		editContent string
	}{
		{
			name: "hand-written Go file in the main package",
			files: map[string]string{
				"main.gala": "package main\n\nfunc main() {\n    Println(\"main\")\n}\n",
				"hook.go":   "package main\n\nimport \"os\"\n\nfunc init() { os.Stdout.WriteString(\"hook-v1\\n\") }\n",
			},
			edit:        "hook.go",
			editContent: "package main\n\nimport \"os\"\n\nfunc init() { os.Stdout.WriteString(\"hook-v2\\n\") }\n",
		},
		{
			name: "embedded asset",
			files: map[string]string{
				"main.gala":    "package main\n\nembed val greeting = \"greeting.txt\"\n\nfunc main() {\n    Println(greeting)\n}\n",
				"greeting.txt": "embed-v1",
			},
			edit:        "greeting.txt",
			editContent: "embed-v2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolatedConfig(t) // GALA_HOME -> a temp dir for every builder below
			projectDir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(projectDir, "gala.mod"),
				[]byte("module example.com/rebuild\n\ngala 0.0.0\n"), 0644))
			for name, content := range tt.files {
				require.NoError(t, os.WriteFile(filepath.Join(projectDir, name), []byte(content), 0644))
			}

			// Each run is a fresh builder, as each `gala build` is a fresh process.
			transpile := func() string {
				t.Helper()
				b, err := NewBuilder(projectDir, "test", false)
				require.NoError(t, err)
				require.NoError(t, b.workspace.Ensure())
				require.NoError(t, b.ensureStdlib())
				require.NoError(t, b.transpileDeps())
				require.NoError(t, b.transpile())
				copied, err := os.ReadFile(filepath.Join(b.workspace.GenDir, tt.edit))
				require.NoError(t, err, "%s must be copied into gen/", tt.edit)
				return string(copied)
			}

			require.Equal(t, tt.files[tt.edit], transpile())

			require.NoError(t, os.WriteFile(filepath.Join(projectDir, tt.edit), []byte(tt.editContent), 0644))
			require.Equal(t, tt.editContent, transpile(),
				"editing only %s must reach the tree go build compiles", tt.edit)
		})
	}
}
