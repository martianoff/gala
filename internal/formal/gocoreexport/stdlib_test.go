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

// TestAddStdlib: a runnable standard-library package the program imports is
// added from GOROOT, marked partial; the guards refuse a file GoCore could run
// differently from Go, and a sibling file that could change it.
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
		{"imported", "package main\nimport \"errors\"\nvar e = errors.New(\"x\")\n", []string{"main", "errors"}},
		{"an alias", "package main\nimport e \"errors\"\nvar x = e.New(\"x\")\n", []string{"main", "errors"}},
		// GoCore reports errors.Is, which the file lacks, when a run reaches it.
		{"a name the file lacks", "package main\nimport \"errors\"\nvar e = errors.Is(nil, nil)\n", []string{"main", "errors"}},
		{"a dot import", "package main\nimport . \"errors\"\nvar e = New(\"x\")\n", []string{"main"}},
		{"not imported", "package main\nvar e = 1\n", []string{"main"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := gocoreexport.AddStdlib(goroot, main(tc.src))
			require.NoError(t, err)
			require.Equal(t, tc.want, paths(got))
			for _, p := range got[1:] {
				require.True(t, p.Partial)
			}
		})
	}
	src := main("package main\nimport \"errors\"\nvar e = errors.New(\"x\")\n")
	// A sibling file that adds a method to a listed type, or names a listed
	// variable, fails the export.
	listed, err := os.ReadFile(filepath.Join(dir, "errors.go"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "errors.go"), append(listed, []byte("\nvar Sentinel = New(\"s\")\n")...), 0o644))
	for name, text := range map[string]string{
		"a method":           "package errors\n\nfunc (e *errorString) Unwrap() error { return nil }\n",
		"an assignment":      "package errors\n\nfunc init() { Sentinel = nil }\n",
		"a field assignment": "package errors\n\nfunc init() { Sentinel.(*errorString).s = \"\" }\n",
		"an address":         "package errors\n\nvar p = &Sentinel\n",
		"an increment":       "package errors\n\nfunc f() { Sentinel++ }\n",
		"a range assignment": "package errors\n\nfunc f() { for Sentinel = range []error{} {} }\n",
		"a method call":      "package errors\n\nfunc f() string { return Sentinel.Error() }\n",
	} {
		t.Run("sibling with "+name, func(t *testing.T) {
			sib := filepath.Join(dir, "wrap.go")
			require.NoError(t, os.WriteFile(sib, []byte(text), 0o644))
			defer os.Remove(sib)
			_, err := gocoreexport.AddStdlib(goroot, src)
			require.Error(t, err)
		})
	}
	// A sibling that only reads a listed variable cannot change what the
	// listed file does.
	// Even a read of a variable that is not a sealed error may alias it.
	t.Run("sibling reading a variable", func(t *testing.T) {
		sib := filepath.Join(dir, "wrap.go")
		require.NoError(t, os.WriteFile(sib, []byte("package errors\n\nfunc isSentinel(e error) bool { x := Sentinel; return e == x }\n"), 0o644))
		defer os.Remove(sib)
		_, err := gocoreexport.AddStdlib(goroot, src)
		require.Error(t, err)
	})
	// A listed file may import a package GoCore does not hold only when that
	// package's initialization does nothing (no init function, no variable)
	// and it is one of fmt's dependencies.
	fmtDir := filepath.Join(goroot, "src", "fmt")
	require.NoError(t, os.MkdirAll(fmtDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(fmtDir, "print.go"),
		[]byte("package fmt\n\nimport (\n\t_ \"quiet\"\n\t_ \"noisy\"\n\t_ \"initp\"\n)\n"), 0o644))
	quiet := filepath.Join(goroot, "src", "quiet")
	require.NoError(t, os.MkdirAll(quiet, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(quiet, "q.go"), []byte("package quiet\n\nconst K = 1\n\nfunc F() int { return K }\n"), 0o644))
	aside := filepath.Join(goroot, "src", "aside")
	require.NoError(t, os.MkdirAll(aside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(aside, "a.go"), []byte("package aside\n\nfunc F() int { return 1 }\n"), 0o644))
	noisy := filepath.Join(goroot, "src", "noisy")
	require.NoError(t, os.MkdirAll(noisy, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(noisy, "n.go"), []byte("package noisy\n\nvar V = 1\n"), 0o644))
	initp := filepath.Join(goroot, "src", "initp")
	require.NoError(t, os.MkdirAll(initp, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(initp, "i.go"), []byte("package initp\n\nfunc init() {}\n"), 0o644))
	for name, tc := range map[string]struct {
		imp string
		ok  bool
	}{
		"a quiet package":           {"import \"quiet\"\n\nvar _ = quiet.F", true},
		"a package with a variable": {"import \"noisy\"\n\nvar _ = noisy.V", false},
		"a package with an init":    {"import \"initp\"\n\nvar _ = 1", false},
		"unsafe":                    {"import \"unsafe\"\n\nvar _ = unsafe.Sizeof(1)", false},
		"an aliased import":         {"import q \"quiet\"\n\nvar _ = q.F", false},
		"an absent package":         {"import \"absent\"\n\nvar _ = 1", false},
		"a quiet package not fmt's":  {"import \"aside\"\n\nvar _ = aside.F", false},
	} {
		t.Run("listed file importing "+name, func(t *testing.T) {
			text := "package errors\n\n" + tc.imp + "\n\nfunc New(s string) error { return nil }\n"
			require.NoError(t, os.WriteFile(filepath.Join(dir, "errors.go"), []byte(text), 0o644))
			_, err := gocoreexport.AddStdlib(goroot, src)
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "errors.go"), listed, 0o644))
	// A runnable package's runnable imports come with it, before it.
	t.Run("a runnable import of a runnable package", func(t *testing.T) {
		sc := filepath.Join(goroot, "src", "strconv")
		require.NoError(t, os.MkdirAll(sc, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(sc, "atoi.go"),
			[]byte("package strconv\n\nimport \"errors\"\n\nvar ErrRange = errors.New(\"r\")\n"), 0o644))
		// The other files strconv lists, empty here.
		for _, name := range gocoreexport.RunnableStdlib["strconv"][1:] {
			require.NoError(t, os.WriteFile(filepath.Join(sc, name), []byte("package strconv\n"), 0o644))
		}
		prog := main("package main\nimport \"strconv\"\nvar e = strconv.ErrRange\n")
		got, err := gocoreexport.AddStdlib(goroot, prog)
		require.NoError(t, err)
		require.Equal(t, []string{"main", "errors", "strconv"}, paths(got))
		// A sibling may read a sealed error (errors.New), never write it.
		sib := filepath.Join(sc, "atof.go")
		require.NoError(t, os.WriteFile(sib, []byte("package strconv\n\nfunc f(e error) bool { x := ErrRange; return e == x }\n"), 0o644))
		_, err = gocoreexport.AddStdlib(goroot, prog)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(sib, []byte("package strconv\n\nfunc f() { ErrRange = nil }\n"), 0o644))
		_, err = gocoreexport.AddStdlib(goroot, prog)
		require.Error(t, err)
		require.NoError(t, os.Remove(sib))
	})
	// A listed file GoCore could run differently from Go fails the export.
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
