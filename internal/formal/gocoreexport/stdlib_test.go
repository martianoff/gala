package gocoreexport_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/bazelbuild/rules_go/go/runfiles"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/formal/gocoreexport"
)

// TestAddStdlib: a runnable standard-library package is added from GOROOT when
// the program imports it and uses only names its files declare.
func TestAddStdlib(t *testing.T) {
	goroot := t.TempDir()
	dir := filepath.Join(goroot, "src", "errors")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "errors.go"),
		[]byte("package errors\n\nfunc New(text string) error { return &errorString{text} }\n\n"+
			"type errorString struct{ s string }\n\nfunc (e *errorString) Error() string { return e.s }\n"), 0o644))
	main := func(src string) []gocoreexport.PackageSource {
		return []gocoreexport.PackageSource{{Path: "main", Files: []gocoreexport.Source{{Name: "main.go", Text: src}}}}
	}
	paths := func(pkgs []gocoreexport.PackageSource) []string {
		var out []string
		for _, p := range pkgs {
			out = append(out, p.Path)
		}
		return out
	}
	for _, tc := range []struct {
		name, src string
		want      []string
	}{
		{"declared names", "package main\nimport \"errors\"\nvar e = errors.New(\"x\")\n", []string{"main", "errors"}},
		{"an alias", "package main\nimport e \"errors\"\nvar x = e.New(\"x\")\n", []string{"main", "errors"}},
		{"a name the file lacks", "package main\nimport \"errors\"\nvar e = errors.Is(nil, nil)\n", []string{"main"}},
		{"a dot import", "package main\nimport . \"errors\"\nvar e = New(\"x\")\n", []string{"main"}},
		{"not imported", "package main\nvar e = 1\n", []string{"main"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := gocoreexport.AddStdlib(goroot, main(tc.src))
			require.NoError(t, err)
			require.Equal(t, tc.want, paths(got))
		})
	}
	// A listed file GoCore could run differently from Go fails the export.
	src := main("package main\nimport \"errors\"\nvar e = errors.New(\"x\")\n")
	for name, text := range map[string]string{
		"a build constraint": "//go:build linux\n\npackage errors\n\nfunc New(s string) error { return nil }\n",
		"a linkname":         "package errors\n\nimport _ \"unsafe\"\n\n//go:linkname x y\nfunc New(s string) error { return nil }\n",
		"an import":          "package errors\n\nimport \"os\"\n\nfunc New(s string) error { return os.ErrExist }\n",
		"no body":            "package errors\n\nfunc New(s string) error\n",
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "errors.go"), []byte(text), 0o644))
			_, err := gocoreexport.AddStdlib(goroot, src)
			require.Error(t, err)
		})
	}
}

// TestRunnableStdlibIsQuiet: GoCore runs only the listed files of a runnable
// package, so the initializers of its other files do not run. That is right
// only for a package that prints nothing and does not panic while it
// initializes, which GoCore's quietInitPackages (formal/Gala/GoCore/Run.lean)
// lists, the same list as galaspec's noOutputInitImports.
func TestRunnableStdlibIsQuiet(t *testing.T) {
	path, err := runfiles.Rlocation(os.Getenv("GOCORE_RUN_LEAN"))
	require.NoError(t, err)
	text, err := os.ReadFile(path)
	require.NoError(t, err)
	m := regexp.MustCompile(`(?s)def quietInitPackages : List String :=\s*\[(.*?)\]`).FindStringSubmatch(string(text))
	require.NotNil(t, m, "quietInitPackages not found in Run.lean")
	quiet := map[string]bool{}
	for _, q := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(m[1], -1) {
		quiet[q[1]] = true
	}
	for ip := range gocoreexport.RunnableStdlib {
		require.True(t, quiet[ip], "%s is runnable but not in quietInitPackages", ip)
	}
}
