package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// goSiblingMethods declares, in Go, methods on the GALA struct Repo of the
// same package.
const goSiblingMethods = `
func (r Repo) Save() error      { return nil }
func (r Repo) Names() []string  { return nil }
func (r *Repo) Touch()          {}
`

// TestGoSiblingMethodsOnGalaType covers the declarations-in-GALA,
// implementations-in-Go split: a struct declared in a .gala file whose methods
// are declared in a hand-written .go file of the same package. Those methods
// are part of its method set, so GALA-E0044 must not reject a call of one
// (#615), and the call is typed and lowered like any other Go method call.
func TestGoSiblingMethodsOnGalaType(t *testing.T) {
	cases := []struct {
		name    string
		galaSrc string // after the package clause and `struct Repo(Name string)`
		want    []string
	}{
		{
			name:    "a Go-declared method is callable",
			galaSrc: "func save(r Repo) error = r.Save()\n",
			want:    []string{"r.Save()"},
		},
		{
			name:    "its result is typed",
			galaSrc: "func count(r Repo) int = r.Names().Size()\n",
			want:    []string{"len(r.Names())"},
		},
		{
			name:    "a pointer-receiver one through a val runs on a copy",
			galaSrc: "func touch() {\n    val r = Repo(\"x\")\n    r.Touch()\n}\n",
			want:    []string{"std.AddrOfCopy(r.Get()).Touch()"},
		},
	}
	layouts := []struct {
		name, dir, pkg string
	}{
		{"package main", ".", "main"},
		{"library package", "lib", "lib"},
	}
	for _, tc := range cases {
		for _, l := range layouts {
			t.Run(tc.name+"/"+l.name, func(t *testing.T) {
				files, galaFile := samePackageModule(l.dir,
					"package "+l.pkg+"\n"+goSiblingMethods,
					"package "+l.pkg+"\n\nstruct Repo(Name string)\n\n"+tc.galaSrc)
				out, err := transpileInModule(t, files, galaFile)
				require.NoError(t, err)
				for _, w := range tc.want {
					assert.Contains(t, out, w)
				}
			})
		}
	}
}

// TestGoSiblingMethodsStillChecked covers what stays an error: a method that
// neither the GALA declaration nor a Go sibling declares. The hint suggests the
// Go-declared method too, instead of claiming the type has no methods.
func TestGoSiblingMethodsStillChecked(t *testing.T) {
	files, galaFile := samePackageModule(".",
		"package main\n"+goSiblingMethods,
		"package main\n\nstruct Repo(Name string)\n\nfunc save(r Repo) error = r.Sav()\n")
	_, err := transpileInModule(t, files, galaFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0044")
	assert.Contains(t, err.Error(), "did you mean `Save`?")

	files["program.gala"] = "package main\n\nstruct Repo(Name string)\n\nfunc load(r Repo) int = r.Load()\n"
	_, err = transpileInModule(t, files, galaFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0044")
	assert.Contains(t, err.Error(), "Repo declares: Names, Save, Touch")
	assert.NotContains(t, err.Error(), "declares no methods")
}

// TestGoSiblingMethodsFromAnotherPackage covers the same split seen from an
// importing package: lib.Repo declares a GALA method and a Go one, and both
// are callable from main.
func TestGoSiblingMethodsFromAnotherPackage(t *testing.T) {
	files := map[string]string{
		"go.mod":      "module example.com/sibs\n\ngo 1.25\n",
		"gala.mod":    "module example.com/sibs\n",
		"lib/repo.go": "package lib\n" + goSiblingMethods,
		"lib/repo.gala": "package lib\n\nstruct Repo(Name string)\n\n" +
			"func (r Repo) Label() string = r.Name\n",
		"main.gala": "package main\n\nimport \"example.com/sibs/lib\"\n\n" +
			"func save(r lib.Repo) error = r.Save()\n\n" +
			"func label(r lib.Repo) string = r.Label()\n",
	}
	out, err := transpileInModule(t, files, "main.gala")
	require.NoError(t, err)
	assert.Contains(t, out, "r.Save()")
}
