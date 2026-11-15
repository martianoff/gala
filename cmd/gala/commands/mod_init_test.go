package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/depman/mod"
)

// gala mod init in a Go module copies every requirement of go.mod into gala.mod
// as a Go requirement, at go.mod's version and keeping its indirect mark.
func TestGoRequirementsOfGoMod(t *testing.T) {
	tests := []struct {
		name  string
		goMod string
		want  []mod.Require
	}{
		{
			name:  "no requirements",
			goMod: "module example.com/app\n\ngo 1.25\n",
			want:  []mod.Require{},
		},
		{
			name: "direct and indirect requirements, a pseudo-version, a replace",
			goMod: "module example.com/app\n\ngo 1.25\n\ntoolchain go1.25.5\n\n" +
				"require github.com/google/uuid v1.3.0\n\n" +
				"require (\n\tgithub.com/spf13/cobra v1.10.2\n" +
				"\tgithub.com/spf13/pflag v1.0.10 // indirect\n" +
				"\tgolang.org/x/text v0.0.0-20240101000000-abcdefabcdef // indirect\n)\n\n" +
				"replace github.com/google/uuid => ../uuid\n",
			want: []mod.Require{
				{Path: "github.com/google/uuid", Version: "v1.3.0", Go: true},
				{Path: "github.com/spf13/cobra", Version: "v1.10.2", Go: true},
				{Path: "github.com/spf13/pflag", Version: "v1.0.10", Indirect: true, Go: true},
				{Path: "golang.org/x/text", Version: "v0.0.0-20240101000000-abcdefabcdef", Indirect: true, Go: true},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "go.mod")
			require.NoError(t, os.WriteFile(path, []byte(tt.goMod), 0644))
			goMod, err := readGoMod(path)
			require.NoError(t, err)
			assert.Equal(t, "example.com/app", goMod.Module.Mod.Path)
			assert.Equal(t, tt.want, goRequirementsOf(goMod))

			// The requirements survive a round trip through gala.mod.
			f := mod.NewFile("example.com/app")
			f.Require = goRequirementsOf(goMod)
			parsed, err := mod.Parse(mod.Format(f))
			require.NoError(t, err)
			assert.ElementsMatch(t, f.Require, parsed.Require)
		})
	}
}

func TestReadGoMod(t *testing.T) {
	dir := t.TempDir()
	missing, err := readGoMod(filepath.Join(dir, "go.mod"))
	require.NoError(t, err)
	assert.Nil(t, missing, "no go.mod reads as nil")

	path := filepath.Join(dir, "go.mod")
	require.NoError(t, os.WriteFile(path, []byte("module example.com/app\nrequire (\n"), 0644))
	_, err = readGoMod(path)
	assert.ErrorContains(t, err, "cannot parse")
}

// gala mod init adds .gala/ to .gitignore once, creating the file if needed
// and keeping its line endings.
func TestEnsureGalaGitignored(t *testing.T) {
	const section = galaGitignoreComment + "\n" + galaCacheDirIgnore + "\n"
	tests := []struct {
		name      string
		existing  *string // nil = no .gitignore
		want      string
		wantAdded bool
	}{
		{name: "no .gitignore", existing: nil, want: section, wantAdded: true},
		{name: "empty .gitignore", existing: stringPtr(""), want: section, wantAdded: true},
		{name: "other entries", existing: stringPtr("bin/\n"), want: "bin/\n" + section, wantAdded: true},
		{name: "no final newline", existing: stringPtr("bin/"), want: "bin/\n" + section, wantAdded: true},
		{
			name:      "CRLF line endings",
			existing:  stringPtr("bin/\r\n"),
			want:      "bin/\r\n" + galaGitignoreComment + "\r\n" + galaCacheDirIgnore + "\r\n",
			wantAdded: true,
		},
		{name: "already ignored", existing: stringPtr("bin/\n.gala/\n"), want: "bin/\n.gala/\n"},
		{name: "already ignored without a slash", existing: stringPtr(".gala\n"), want: ".gala\n"},
		{name: "already ignored from the root", existing: stringPtr("/.gala/\r\n"), want: "/.gala/\r\n"},
		{
			name:      "a similar name is another entry",
			existing:  stringPtr(".galaxy/\n"),
			want:      ".galaxy/\n" + section,
			wantAdded: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".gitignore")
			if tt.existing != nil {
				require.NoError(t, os.WriteFile(path, []byte(*tt.existing), 0644))
			}
			added, err := ensureGalaGitignored(path)
			require.NoError(t, err)
			assert.Equal(t, tt.wantAdded, added)
			got, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))

			// A second run changes nothing.
			added, err = ensureGalaGitignored(path)
			require.NoError(t, err)
			assert.False(t, added)
			again, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, string(got), string(again))
		})
	}
}

func stringPtr(s string) *string { return &s }
