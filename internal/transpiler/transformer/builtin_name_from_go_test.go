package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

// TestBuiltinNameReachedFromGo pins GALA-E0107: a top-level declaration named
// like a Go builtin is emitted under a reserved name, so the package's
// hand-written Go that spells the name reaches Go's builtin instead. That is
// an error only where the hand-written Go refers to the name.
func TestBuiltinNameReachedFromGo(t *testing.T) {
	// selfContained calls the GALA max itself, so it needs no Go of its
	// package declaring larger.
	const selfContained = "package main\n\nfunc max(a int, b int) int = if (a > b) a else b\n\nfunc main() {\n    Println(max(1, 2))\n}\n"
	tests := []struct {
		name    string
		src     string
		bridge  string // hand-written Go beside main.gala; "" for none
		wantErr bool
	}{
		{
			name:    "Go refers to the builtin the GALA function is named like",
			src:     builtinNameFromGoMain,
			bridge:  builtinNameFromGoBridge,
			wantErr: true,
		},
		{
			name:    "a val named like the builtin the Go refers to",
			src:     "package main\n\nval max = 3\n\nfunc main() {\n    Println(larger(1, max))\n}\n",
			bridge:  builtinNameFromGoBridge,
			wantErr: true,
		},
		{
			name:   "Go does not refer to the name",
			src:    selfContained,
			bridge: "package main\n\nfunc larger(a, b int) int {\n\tif a > b {\n\t\treturn a\n\t}\n\treturn b\n}\n",
		},
		{
			name:   "Go file of another package",
			src:    selfContained,
			bridge: "package other\n\nfunc larger(a, b int) int { return max(a, b) }\n",
		},
		{
			name: "no hand-written Go",
			src:  selfContained,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.bridge != "" {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "bridge.go"), []byte(tt.bridge), 0o600))
			}
			mainPath := filepath.Join(dir, "main.gala")
			require.NoError(t, os.WriteFile(mainPath, []byte(tt.src), 0o600))

			_, err := newDocGuardTranspiler().Transpile(tt.src, mainPath)
			if !tt.wantErr {
				require.NoError(t, err)
				return
			}
			var se *galaerr.SemanticError
			require.ErrorAs(t, err, &se)
			require.Equal(t, galaerr.CodeBuiltinNameReachedFromGo, se.Code)
			require.Equal(t, 3, se.Line)
		})
	}
}
