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
		name   string
		goMod  string // "" = no go.mod
		want   []mod.Require
		module string
	}{
		{
			name:   "no go.mod",
			goMod:  "",
			want:   nil,
			module: "",
		},
		{
			name:   "no requirements",
			goMod:  "module example.com/app\n\ngo 1.25\n",
			want:   []mod.Require{},
			module: "example.com/app",
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
			module: "example.com/app",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "go.mod")
			if tt.goMod != "" {
				require.NoError(t, os.WriteFile(path, []byte(tt.goMod), 0644))
			}
			goMod, err := readGoMod(path)
			require.NoError(t, err)
			assert.Equal(t, tt.want, goRequirementsOf(goMod))
			if tt.module != "" {
				assert.Equal(t, tt.module, goMod.Module.Mod.Path)
			}

			// The requirements survive a round trip through gala.mod.
			f := mod.NewFile("example.com/app")
			f.Require = goRequirementsOf(goMod)
			parsed, err := mod.Parse(mod.Format(f))
			require.NoError(t, err)
			assert.ElementsMatch(t, f.Require, parsed.Require)
		})
	}
}

func TestReadGoModRejectsAnUnparsableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "go.mod")
	require.NoError(t, os.WriteFile(path, []byte("module example.com/app\nrequire (\n"), 0644))
	_, err := readGoMod(path)
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

// gala mod tidy marks a module it adds as Go when the module has no GALA
// sources, as gala mod add does; unmarked, gala build would transpile it as a
// GALA module.
func TestTidyRequireMissingMarksGoModules(t *testing.T) {
	goDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(goDir, "uuid.go"), []byte("package uuid\n"), 0644))
	galaDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(galaDir, "gala.mod"), []byte("module example.com/lib\n"), 0644))
	dirs := map[string]string{"github.com/google/uuid": goDir, "example.com/lib": galaDir}
	fetchLatest := func(path string) (string, string, string, error) {
		dir, ok := dirs[path]
		if !ok {
			return "", "", "", os.ErrNotExist
		}
		return "v1.6.0", dir, "h1:x", nil
	}

	f := mod.NewFile("example.com/app")
	requireMissing(f, []string{"github.com/google/uuid", "example.com/lib", "example.com/missing"}, fetchLatest)
	assert.Equal(t, []mod.Require{
		{Path: "github.com/google/uuid", Version: "v1.6.0", Go: true},
		{Path: "example.com/lib", Version: "v1.6.0"},
	}, f.Require)
}
