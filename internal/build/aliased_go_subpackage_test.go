package build

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// transpileAndRun transpiles projectDir and returns the generated
// main.gen.go, then builds the program with the same Builder and returns its
// output (fields joined by "|", as buildAndRun does).
func transpileAndRun(t *testing.T, projectDir string) (mainGen, out string) {
	t.Helper()
	chdirForTest(t, projectDir)
	b, err := NewBuilder(projectDir, "test", false)
	require.NoError(t, err)
	require.NoError(t, b.workspace.Ensure())
	require.NoError(t, b.ensureStdlib())
	require.NoError(t, b.transpileDeps())
	require.NoError(t, b.transpile())
	mainGen = readFileString(t, filepath.Join(b.workspace.GenDir, "main.gen.go"))
	// The first transpile in the process points GOROOT at the SDK the type
	// inference uses (the analyzer's Go importer, set up once); align it with
	// the PATH go again before building.
	alignGorootWithPathGo(t)
	binPath, buildErr := b.Build("")
	if buildErr != nil {
		if isToolchainEnvError(buildErr.Error()) {
			t.Skipf("skipping end-to-end check: Go toolchain unavailable/mismatched in this environment: %v", buildErr)
		}
		t.Fatalf("gala build failed: %v", buildErr)
	}
	raw, runErr := runBuiltBinary(binPath)
	require.NoError(t, runErr, "built binary failed to run; output:\n%s", raw)
	return mainGen, strings.Join(strings.Fields(raw), "|")
}

// TestBuild_AliasedGoSubpackageKeepsGoResults builds a project whose GALA
// code imports a hand-written Go package of the same module under an alias
// that differs from the package's name. A call through the alias returning
// `(T, error)` is a Try value, as it is through the package's own name: the
// generated Go wraps it in std.GoTry, and the program builds and runs.
func TestBuild_AliasedGoSubpackageKeepsGoResults(t *testing.T) {
	projectDir := newForeignGenGoProject(t, "example.com/aliasgo", map[string]string{
		"store/store.go": "package store\n\nfunc Load() (string, error) { return \"ok\", nil }\n",
		"main.gala": `package main

import st "example.com/aliasgo/store"

func load() string = st.Load().OnFailure((err error) => {}).GetOrElse("failed")

func main() {
    Println(load())
}
`,
	})

	mainGen, out := transpileAndRun(t, projectDir)
	assert.Contains(t, mainGen, "std.GoTry(st.Load())",
		"a (T, error) call through the alias must become a Try value:\n%s", mainGen)
	assert.Equal(t, "ok", out)
}

// TestBuild_SameNameGoSubpackagesKeepTheirOwnSignatures imports hand-written
// Go packages of the module that share a package name: under aliases in one
// file, from sibling files of one package, and as the name of the package
// being compiled. Go type info records each under its import path, so every
// call gets its own package's signature, in any import order.
func TestBuild_SameNameGoSubpackagesKeepTheirOwnSignatures(t *testing.T) {
	const a = "package util\n\nfunc Get() (int, error) { return 1, nil }\n"
	const b = "package util\n\nfunc Get() string { return \"b\" }\n"

	t.Run("aliases in one file, either order", func(t *testing.T) {
		for _, imports := range []string{
			"ua \"example.com/samename/a/util\"\n    ub \"example.com/samename/b/util\"",
			"ub \"example.com/samename/b/util\"\n    ua \"example.com/samename/a/util\"",
		} {
			projectDir := newForeignGenGoProject(t, "example.com/samename", map[string]string{
				"a/util/util.go": a,
				"b/util/util.go": b,
				"main.gala": `package main

import (
    ` + imports + `
)

func main() {
    Println(ua.Get().GetOrElse(0), ub.Get())
}
`,
			})
			mainGen, out := transpileAndRun(t, projectDir)
			assert.Contains(t, mainGen, "std.GoTry(ua.Get())", "ua.Get returns (int, error):\n%s", mainGen)
			assert.NotContains(t, mainGen, "std.GoTry(ub.Get())", "ub.Get returns one value:\n%s", mainGen)
			assert.Equal(t, "1|b", out)
		}
	})

	t.Run("sibling files", func(t *testing.T) {
		projectDir := newForeignGenGoProject(t, "example.com/samename", map[string]string{
			"a/util/util.go": a,
			"b/util/util.go": b,
			"main.gala": `package main

import ua "example.com/samename/a/util"

func main() {
    Println(ua.Get().GetOrElse(0), fromB())
}
`,
			"other.gala": `package main

import "example.com/samename/b/util"

func fromB() string = util.Get()
`,
		})
		assert.Equal(t, "1|b", buildAndRun(t, projectDir))
	})

	t.Run("the compiled package's own name", func(t *testing.T) {
		projectDir := newForeignGenGoProject(t, "example.com/samename", map[string]string{
			"lib/util.go":    "package util\n\nfunc Local() string { return \"local\" }\n",
			"lib/lib.gala":   "package util\n\nimport o \"example.com/samename/a/util\"\n\nfunc Both() string = s\"${o.Get().GetOrElse(0)} ${Local()}\"\n",
			"a/util/util.go": a,
			"main.gala": `package main

import u "example.com/samename/lib"

func main() {
    Println(u.Both())
}
`,
		})
		assert.Equal(t, "1|local", buildAndRun(t, projectDir))
	})

	t.Run("the compiled package declares a function of the same name", func(t *testing.T) {
		projectDir := newForeignGenGoProject(t, "example.com/samename", map[string]string{
			"lib/lib.gala":   "package util\n\nimport o \"example.com/samename/a/util\"\n\nfunc Get() string = s\"lib ${o.Get().GetOrElse(0)}\"\n",
			"a/util/util.go": a,
			"main.gala": `package main

import u "example.com/samename/lib"

func main() {
    Println(u.Get())
}
`,
		})
		assert.Equal(t, "lib|1", buildAndRun(t, projectDir))
	})
}

// TestBuild_SameNameGalaPackagesKeepTheirOwnFunctions imports GALA packages
// that share a package name, and declares a function of that name in the
// importing package too. Each qualified call resolves to the function of the
// package its qualifier imports, whatever its signature, in a function body
// and in a package-level val's initializer.
func TestBuild_SameNameGalaPackagesKeepTheirOwnFunctions(t *testing.T) {
	projectDir := newForeignGenGoProject(t, "example.com/samegala", map[string]string{
		"a/util/util.gala": "package util\n\nfunc Get(n int) int = n + 1\n",
		"b/util/util.gala": "package util\n\nfunc Get(s string, suffix string = \"!\") string = s + suffix\n",
		"lib/lib.gala": `package util

import (
    ua "example.com/samegala/a/util"
    ub "example.com/samegala/b/util"
)

val one = ua.Get(1)

func Get() string = s"${one} ${ub.Get("b")}"
`,
		"main.gala": `package main

import u "example.com/samegala/lib"

func main() {
    Println(u.Get())
}
`,
	})
	assert.Equal(t, "2|b!", buildAndRun(t, projectDir))

	for _, imports := range []string{
		"ua \"example.com/samegala/a/util\"\n    ub \"example.com/samegala/b/util\"",
		"ub \"example.com/samegala/b/util\"\n    ua \"example.com/samegala/a/util\"",
	} {
		projectDir := newForeignGenGoProject(t, "example.com/samegala", map[string]string{
			"a/util/util.gala": "package util\n\nfunc Get(n int) int = n + 1\n",
			"b/util/util.gala": "package util\n\nfunc Get(s string) string = s + \"!\"\n",
			"main.gala": `package main

import (
    ` + imports + `
)

val one = ua.Get(1)
val bang = ub.Get("b")
`,
			"use.gala": `package main

func main() {
    Println(one * 10, bang + "?")
}
`,
		})
		assert.Equal(t, "20|b!?", buildAndRun(t, projectDir))
	}

	t.Run("dot import of a package of the compiled package's name", func(t *testing.T) {
		projectDir := newForeignGenGoProject(t, "example.com/samegala", map[string]string{
			"a/util/util.gala": "package util\n\nfunc Only() int = 7\n",
			"lib/lib.gala":     "package util\n\nimport . \"example.com/samegala/a/util\"\n\nfunc Get() int = Only() + 1\n",
			"main.gala": `package main

import u "example.com/samegala/lib"

func main() {
    Println(u.Get())
}
`,
		})
		assert.Equal(t, "8", buildAndRun(t, projectDir))
	})

	t.Run("hand-written Go of the compiled package imports a package of its name", func(t *testing.T) {
		projectDir := newForeignGenGoProject(t, "example.com/samegala", map[string]string{
			"a/util/util.gala": "package util\n\nfunc Get() int = 1\n\nfunc Extra() int = 2\n",
			"lib/conv.go":      "package util\n\nimport au \"example.com/samegala/a/util\"\n\nfunc FromA() int { return au.Get() }\n",
			"lib/lib.gala":     "package util\n\nfunc Get() string = s\"lib ${FromA()}\"\n",
			"main.gala": `package main

import u "example.com/samegala/lib"

func main() {
    Println(u.Get())
}
`,
		})
		assert.Equal(t, "lib|1", buildAndRun(t, projectDir))
	})
}

// TestBuild_SameNameGalaPackagesKeepTheirOwnTypes declares a type of one name
// in GALA packages that share a package name, and in the importing package
// too. Each qualified type, constructor and pattern names the type of the
// package its qualifier imports, with that type's own fields and methods.
func TestBuild_SameNameGalaPackagesKeepTheirOwnTypes(t *testing.T) {
	const a = `package util

struct Config(N int)

func (c Config) Show() string = s"a${c.N}"

sealed type Shape {
    case Dot()
    case Line(Len int)
}
`
	const b = `package util

struct Config(Name string, Debug bool = false)

func (c Config) Describe() string = s"b-${c.Name}"
`
	t.Run("two imports", func(t *testing.T) {
		projectDir := newForeignGenGoProject(t, "example.com/sametype", map[string]string{
			"a/util/util.gala": a,
			"b/util/util.gala": b,
			"main.gala": `package main

import (
    ua "example.com/sametype/a/util"
    ub "example.com/sametype/b/util"
)

func size(s ua.Shape) int = s match {
    case ua.Dot() => 0
    case ua.Line(n) => n
}

func main() {
    val ca ua.Config = ua.Config(1)
    val cb = ub.Config(Name = "x")
    Println(ca.Show(), cb.Describe(), cb.Debug, size(ua.Line(3)))
}
`,
		})
		assert.Equal(t, "a1|b-x|false|3", buildAndRun(t, projectDir))
	})

	t.Run("an import of the compiled package's name", func(t *testing.T) {
		projectDir := newForeignGenGoProject(t, "example.com/sametype", map[string]string{
			"a/util/util.gala": a,
			"lib/lib.gala": `package util

import ua "example.com/sametype/a/util"

struct Config(Label string)

func (c Config) Show() string = s"lib-${c.Label}"

func Both() string = s"${Config("own").Show()} ${ua.Config(2).Show()}"
`,
			"main.gala": `package main

import u "example.com/sametype/lib"

func main() {
    Println(u.Both())
}
`,
		})
		assert.Equal(t, "lib-own|a2", buildAndRun(t, projectDir))
	})

	t.Run("a package that reaches only one of them", func(t *testing.T) {
		for _, imports := range []string{
			"\"example.com/sametype/x\"\n    ub \"example.com/sametype/b/util\"",
			"ub \"example.com/sametype/b/util\"\n    \"example.com/sametype/x\"",
		} {
			projectDir := newForeignGenGoProject(t, "example.com/sametype", map[string]string{
				"a/util/util.gala": a,
				"b/util/util.gala": b,
				"x/x.gala": `package x

import "example.com/sametype/a/util"

func Make(n int) util.Config = util.Config(n)

val made = Make(4)
`,
				"main.gala": `package main

import (
    ` + imports + `
)

func main() {
    Println(x.Make(3).Show(), ub.Config(Name = "y").Describe())
}
`,
			})
			assert.Equal(t, "a3|b-y", buildAndRun(t, projectDir))
		}
	})

	t.Run("an unaliased import while another package of its name is reached", func(t *testing.T) {
		projectDir := newForeignGenGoProject(t, "example.com/sametype", map[string]string{
			"a/util/util.gala": a,
			"b/util/util.gala": b,
			"x/x.gala":         "package x\n\nimport \"example.com/sametype/a/util\"\n\nfunc Make(n int) util.Config = util.Config(n)\n",
			"main.gala": `package main

import (
    "example.com/sametype/x"
    "example.com/sametype/b/util"
)

func describe(c util.Config) string = c.Describe()

func main() {
    Println(x.Make(5).Show(), describe(util.Config(Name = "z")))
}
`,
		})
		assert.Equal(t, "a5|b-z", buildAndRun(t, projectDir))
	})

	t.Run("a package with hand-written Go of the compiled package's name", func(t *testing.T) {
		projectDir := newForeignGenGoProject(t, "example.com/sametype", map[string]string{
			"a/util/util.gala": a,
			"a/util/extra.go":  "package util\n\nfunc (c Config) Twice() int { return c.N.Get() * 2 }\n\nfunc Base() int { return 10 }\n",
			"lib/lib.gala": `package util

import ua "example.com/sametype/a/util"

struct Config(Label string)

func Both() string = s"${Config("own").Label} ${ua.Config(4).Twice()} ${ua.Base()}"
`,
			"main.gala": `package main

import u "example.com/sametype/lib"

func main() {
    Println(u.Both())
}
`,
		})
		assert.Equal(t, "own|8|10", buildAndRun(t, projectDir))
	})

	t.Run("a codec of a struct holding both", func(t *testing.T) {
		projectDir := newForeignGenGoProject(t, "example.com/sametype", map[string]string{
			"a/util/util.gala": a,
			"b/util/util.gala": b,
			"main.gala": `package main

import (
    . "martianoff/gala/json"
    ua "example.com/sametype/a/util"
    ub "example.com/sametype/b/util"
)

struct Pair(A ua.Config, B ub.Config)

func main() {
    Println(Codec[Pair](SnakeCase()).Encode(Pair(ua.Config(1), ub.Config(Name = "x"))).Get())
}
`,
		})
		assert.Equal(t, `{"a":{"n":1},"b":{"name":"x","debug":false}}`, buildAndRun(t, projectDir))
	})

	t.Run("a default declared in an import of the compiled package's name", func(t *testing.T) {
		projectDir := newForeignGenGoProject(t, "example.com/sametype", map[string]string{
			"b/util/util.gala": `package util

func DefaultLevel() int = 5

struct Opts(Level int = DefaultLevel())

func Describe(o Opts, suffix string = Tag()) string = s"${o.Level}${suffix}"

func Tag() string = "!"
`,
			"lib/lib.gala": `package util

import ub "example.com/sametype/b/util"

func DefaultLevel() int = 0

func Run() string = ub.Describe(ub.Opts())
`,
			"main.gala": `package main

import u "example.com/sametype/lib"

func main() {
    Println(u.Run())
}
`,
		})
		assert.Equal(t, "5!", buildAndRun(t, projectDir))
	})
}
