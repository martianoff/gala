package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/depman/mod"
)

// gala mod init in a Go module copies the requirements of go.mod into gala.mod
// as Go requirements, at go.mod's version and keeping its indirect mark. A
// requirement go.mod replaces, and a GALA standard library package, are not
// copied, and are named with the reason.
func TestGoRequirementsOfGoMod(t *testing.T) {
	tests := []struct {
		name        string
		goMod       string
		want        []mod.Require
		wantSkipped []string
	}{
		{
			name:  "no requirements",
			goMod: "module example.com/app\n\ngo 1.25\n",
			want:  []mod.Require{},
		},
		{
			name: "direct and indirect requirements, a pseudo-version",
			goMod: "module example.com/app\n\ngo 1.25\n\ntoolchain go1.25.5\n\n" +
				"require github.com/google/uuid v1.3.0\n\n" +
				"require (\n\tgithub.com/spf13/cobra v1.10.2\n" +
				"\tgithub.com/spf13/pflag v1.0.10 // indirect\n" +
				"\tgolang.org/x/text v0.0.0-20240101000000-abcdefabcdef // indirect\n)\n",
			want: []mod.Require{
				{Path: "github.com/google/uuid", Version: "v1.3.0", Go: true},
				{Path: "github.com/spf13/cobra", Version: "v1.10.2", Go: true},
				{Path: "github.com/spf13/pflag", Version: "v1.0.10", Indirect: true, Go: true},
				{Path: "golang.org/x/text", Version: "v0.0.0-20240101000000-abcdefabcdef", Indirect: true, Go: true},
			},
		},
		{
			name: "a replaced module and a standard library package are not copied",
			goMod: "module example.com/app\n\ngo 1.25\n\n" +
				"require (\n\tgithub.com/google/uuid v1.3.0\n" +
				"\texample.com/shared v0.0.0-00010101000000-000000000000\n" +
				"\tmartianoff/gala/std v0.0.0\n)\n\n" +
				"replace example.com/shared => ../shared\n",
			want: []mod.Require{{Path: "github.com/google/uuid", Version: "v1.3.0", Go: true}},
			wantSkipped: []string{
				"example.com/shared v0.0.0-00010101000000-000000000000 (go.mod replaces it; gala build does not apply replace directives to Go dependencies)",
				"martianoff/gala/std v0.0.0 (part of the GALA standard library, which gala build provides)",
			},
		},
		{
			name: "a replace block, a version-limited replace, and a directive newer than x/mod",
			goMod: "module example.com/app\n\ngo 1.25\n\nignore ./node_modules\n\n" +
				"require (\n\texample.com/old v1.2.0\n\t\"example.com/quoted\" v1.0.0\n\texample.com/pinned v1.0.0\n)\n\n" +
				"replace (\n\texample.com/old v1.0.0 => ../old\n\t\"example.com/quoted\" => example.com/fork v1.1.0\n)\n\n" +
				"replace example.com/pinned v1.0.0 => ../pinned\n",
			want: []mod.Require{{Path: "example.com/old", Version: "v1.2.0", Go: true}},
			wantSkipped: []string{
				"example.com/quoted v1.0.0 (go.mod replaces it; gala build does not apply replace directives to Go dependencies)",
				"example.com/pinned v1.0.0 (go.mod replaces it; gala build does not apply replace directives to Go dependencies)",
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
			reqs, skipped := goRequirementsOf(goMod)
			assert.Equal(t, tt.want, reqs)
			assert.Equal(t, tt.wantSkipped, skipped)

			// The requirements survive a round trip through gala.mod.
			f := mod.NewFile("example.com/app")
			f.Require = reqs
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

// Only a rule of the work tree's own .gitignore files counts: a personal one
// (core.excludesFile, .git/info/exclude) is not shared with other users.
func TestSharedIgnoreRule(t *testing.T) {
	rule := func(source, pattern string) string {
		return source + ":1:" + pattern + "\t.gala/\n"
	}
	tests := []struct {
		out  string
		want bool
	}{
		{rule(".gitignore", ".gala/"), true},
		{rule("../../.gitignore", ".gala/"), true},
		{rule("services/.gitignore", "**/.gala/"), true},
		{rule(`"d\303\251j\303\240/.gitignore"`, ".gala"), true},
		{rule(`"/home/d\303\251j\303\240/.gitignore"`, ".gala"), false},
		{rule(".git/info/exclude", ".gala/"), false},
		{rule("C:/Users/me/.gitignore", ".gala/"), false},
		{rule("/home/me/.gitignore", ".gala/"), false},
		{rule("/home/me/.config/git/ignore", ".gala/"), false},
		{rule(".gitignore", "!.gala/"), false},
		{"::\t.gala/\n", false}, // -n: no rule matches
		{"", false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, sharedIgnoreRule(tt.out), "%q", tt.out)
	}
}

// In a Git work tree whose parent directory's .gitignore already ignores .gala/,
// gala mod init in a subdirectory leaves .gitignore alone.
func TestEnsureGalaGitignoredRespectsGitRules(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	root := t.TempDir()
	require.NoError(t, exec.Command("git", "init", "-q", root).Run())
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".gala/\n"), 0644))
	sub := filepath.Join(root, "services", "app")
	require.NoError(t, os.MkdirAll(sub, 0755))

	added, err := ensureGalaGitignored(filepath.Join(sub, ".gitignore"))
	require.NoError(t, err)
	assert.False(t, added)
	assert.NoFileExists(t, filepath.Join(sub, ".gitignore"))

	// A later negation re-includes .gala/, so the line is added after all.
	negated := ".gala/\n!.gala/\n"
	require.NoError(t, os.WriteFile(filepath.Join(sub, ".gitignore"), []byte(negated), 0644))
	added, err = ensureGalaGitignored(filepath.Join(sub, ".gitignore"))
	require.NoError(t, err)
	assert.True(t, added)

	// A rule only this clone has, in .git/info/exclude, is not shared.
	other := t.TempDir()
	require.NoError(t, exec.Command("git", "init", "-q", other).Run())
	require.NoError(t, os.MkdirAll(filepath.Join(other, ".git", "info"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(other, ".git", "info", "exclude"), []byte(".gala/\n"), 0644))
	added, err = ensureGalaGitignored(filepath.Join(other, ".gitignore"))
	require.NoError(t, err)
	assert.True(t, added)
	assert.FileExists(t, filepath.Join(other, ".gitignore"))
}
