package build

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestResolveOutputPath pins `go build -o` naming: an explicit output path is
// used as given whatever the target OS, and only the default name gains
// ".exe", and only when the target (not the host) is windows.
func TestResolveOutputPath(t *testing.T) {
	projectDir := filepath.Join(t.TempDir(), "hello")
	absOut := filepath.Join(t.TempDir(), "bin", "app")

	tests := []struct {
		name       string
		outputPath string
		targetGOOS string
		want       string
	}{
		{"default name targeting windows", "", "windows", filepath.Join(projectDir, "hello.exe")},
		{"default name targeting linux", "", "linux", filepath.Join(projectDir, "hello")},
		{"default name targeting darwin", "", "darwin", filepath.Join(projectDir, "hello")},
		{"relative -o targeting linux", "hi-linux-arm64", "linux", filepath.Join(projectDir, "hi-linux-arm64")},
		{"relative -o targeting windows is used as given", "app", "windows", filepath.Join(projectDir, "app")},
		{"relative -o with .exe kept", "app.exe", "windows", filepath.Join(projectDir, "app.exe")},
		{"relative -o in a subdirectory", filepath.Join("out", "app"), "linux", filepath.Join(projectDir, "out", "app")},
		{"absolute -o used as given", absOut, "windows", absOut},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, resolveOutputPath(projectDir, tt.outputPath, tt.targetGOOS))
		})
	}
}

func TestGoTargetOSHonoursGOOS(t *testing.T) {
	t.Setenv("GOOS", "plan9")
	assert.Equal(t, "plan9", goTargetOS())
}
