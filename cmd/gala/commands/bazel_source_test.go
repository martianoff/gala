package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A source of another Bazel repository is a dependency's, which the unused
// import check (GALA-E0120) leaves alone; the main repository's is checked.
func TestInExternalRepository(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"external/gala_tui+/runtime.gala", true},
		{"external/gala_tui+/harness/fluent.gala", true},
		{"../gala_tui+/runtime.gala", true},
		{"bazel-out/k8-fastbuild/bin/external/gala_tui+/gen.gala", true},
		{"bazel-out/k8-fastbuild/bin/app/gen.gala", false},
		{"app/main.gala", false},
		{"main.gala", false},
		{"externals/x.gala", false},
		{"app/external/x.gala", false},
		{"./app/main.gala", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			assert.Equal(t, tt.want, inExternalRepository(tt.path))
			assert.Equal(t, tt.want, len(bazelSourceOptions(tt.path)) == 1)
		})
	}
}
