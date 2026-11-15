package gala2lean_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/formal/coreexport"
	"martianoff/gala/internal/formal/gala2lean"
)

// moduleDeps is the packages of module example.com/m that main imports,
// their sources in pkgs by the path below the module.
func moduleDeps(t *testing.T, main *coreexport.Program, pkgs map[string]string) []gala2lean.Package {
	t.Helper()
	deps, err := gala2lean.ModuleDeps(main, "example.com/m", func(path string) (*coreexport.Program, error) {
		dir := strings.TrimPrefix(path, "example.com/m/")
		src, ok := pkgs[dir]
		require.True(t, ok, "no package %s", path)
		return export(t, dir+"/"+dir+".gala", src), nil
	})
	require.NoError(t, err)
	return deps
}

// TestPackages checks what is reported of a program that imports packages
// of its module, and that its translation does not depend on the order the
// packages are given in.
func TestPackages(t *testing.T) {
	lib := "package lib\n\nstruct Pt(X int, Y int)\n\nfunc Origin() Pt = Pt(0, 0)\n"
	cases := []struct {
		name, main string
		pkgs       map[string]string
		want       string
	}{
		{"a dot import of a package of the module",
			"package main\n\nimport . \"example.com/m/lib\"\n\nfunc main() { Println(Origin()) }\n",
			map[string]string{"lib": lib}, "dot-import-module"},
		{"an init function",
			"package main\n\nimport \"example.com/m/lib\"\n\nfunc main() { Println(lib.Origin()) }\n",
			map[string]string{"lib": lib + "\nfunc init() { Println(1) }\n"}, "init-func"},
		{"a type of a package outside the module",
			"package main\n\nimport \"example.com/other/lib\"\n\nfunc show(p lib.Pt) int = 1\n\nfunc main() { Println(show(lib.Origin())) }\n",
			nil, "qualified-type"},
		{"an unused import of a package with package-level variables",
			"package main\n\nimport \"example.com/m/lib\"\n\nfunc main() { Println(1) }\n",
			map[string]string{"lib": lib + "\nval Ready = 1\n"}, "unused-import-init"},
		{"an initializer outside the subset that nothing uses",
			"package main\n\nimport \"example.com/m/lib\"\n\nfunc main() { Println(lib.Origin()) }\n",
			map[string]string{"lib": lib + "\nval unused int = println(1)\n"}, "call-of-println"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := export(t, "main.gala", c.main)
			res, err := gala2lean.Translate(p, gala2lean.Options{Namespace: "P", Main: true, Deps: moduleDeps(t, p, c.pkgs)})
			require.Nil(t, res)
			var u *gala2lean.UnsupportedError
			require.True(t, errors.As(err, &u), "%v", err)
			require.Equal(t, c.want, u.Construct(), "%v", err)
		})
	}

	t.Run("the order the packages are given in", func(t *testing.T) {
		main := "package main\n\nimport (\n    \"example.com/m/a\"\n    \"example.com/m/b\"\n)\n\nfunc main() { Println(a.F(), b.G()) }\n"
		pkgs := map[string]string{
			"a": "package a\n\nfunc F() int = 1\n",
			"b": "package b\n\nimport \"example.com/m/a\"\n\nfunc G() int = a.F() + 1\n",
		}
		p := export(t, "main.gala", main)
		deps := moduleDeps(t, p, pkgs)
		first, err := gala2lean.Translate(p, gala2lean.Options{Namespace: "P", Main: true, Deps: deps})
		require.NoError(t, err)
		slices.Reverse(deps)
		second, err := gala2lean.Translate(p, gala2lean.Options{Namespace: "P", Main: true, Deps: deps})
		require.NoError(t, err)
		require.Equal(t, first.Lean, second.Lean)
	})
}
