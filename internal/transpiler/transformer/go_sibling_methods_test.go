package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// goSiblingMethods declares, in Go, methods on the GALA struct Repo of the
// same package. TestSamePackageGoSiblingDeclarations covers calling them.
const goSiblingMethods = `
func (r Repo) Save() error      { return nil }
func (r Repo) Names() []string  { return nil }
func (r *Repo) Touch()          {}
`

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

	files[galaFile] = "package main\n\nstruct Repo(Name string)\n\nfunc load(r Repo) int = r.Load()\n"
	_, err = transpileInModule(t, files, galaFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0044")
	assert.Contains(t, err.Error(), "Repo declares: Names, Save, Touch")
	assert.NotContains(t, err.Error(), "declares no methods")
}

// TestGoSiblingMethodsShapeTheType covers what else a Go-declared method on a
// GALA struct decides: a result naming the GALA type itself is typed as it, a
// Go-declared Equal replaces the generated one, and Go-declared pointer
// Lock/Unlock make the struct unsafe to copy (GALA-E0053).
func TestGoSiblingMethodsShapeTheType(t *testing.T) {
	transpile := func(goMethods, gala string) (string, error) {
		files, galaFile := samePackageModule(".", "package main\n"+goMethods,
			"package main\n\nstruct Repo(Name string)\n\n"+gala)
		return transpileInModule(t, files, galaFile)
	}

	out, err := transpile("\nfunc (r Repo) Renamed() Repo { return r }\n",
		"func size(r Repo) int = r.Renamed().Name.Size()\n")
	require.NoError(t, err)
	assert.Contains(t, out, "utf8.RuneCountInString(r.Renamed().Name.Get())")

	out, err = transpile("\nfunc (r Repo) Equal(o Repo) bool { return true }\n",
		"func same(a Repo, b Repo) bool = a.Equal(b)\n")
	require.NoError(t, err)
	assert.NotContains(t, out, "func (s Repo) Equal(", "Equal is declared in Go; generating it too would not compile")

	_, err = transpile("\nfunc (r *Repo) Lock()   {}\nfunc (r *Repo) Unlock() {}\n",
		"func lockIt() {\n    val r = Repo(\"x\")\n    r.Lock()\n}\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0053")
}

// TestGoSiblingMethodsFromAnotherPackage covers the same split seen from an
// importing package: lib.Repo declares a GALA method and a Go one, and both
// are callable from main.
func TestGoSiblingMethodsFromAnotherPackage(t *testing.T) {
	files, _ := samePackageModule("lib", "package lib\n"+goSiblingMethods,
		"package lib\n\nstruct Repo(Name string)\n\nfunc (r Repo) Label() string = r.Name\n")
	files["main.gala"] = "package main\n\nimport \"example.com/sibs/lib\"\n\n" +
		"func save(r lib.Repo) error = r.Save()\n\n" +
		"func label(r lib.Repo) string = r.Label()\n"
	out, err := transpileInModule(t, files, "main.gala")
	require.NoError(t, err)
	assert.Contains(t, out, "r.Save()")
	assert.Contains(t, out, "r.Label()")
}
