package analyzer_test

import (
	"os"
	"path/filepath"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A value of a package's API can have a type from a package the GALA file
// never imports: through a field (httptest.ResponseRecorder.Body is a
// *bytes.Buffer), a method result (http.Request.MultipartReader returns a
// *multipart.Reader) or a chain of them (multipart.Reader.NextPart returns a
// *multipart.Part). Each such type's methods must be recorded, so a call of
// one returning (T, error) lowers to Try.
func TestAnalyzeGoPackage_RecordsTypesReachedThroughFieldsAndMethods(t *testing.T) {
	skipIfNoGoSDK(t)

	httptest := analyzer.AnalyzeGoPackage("net/http/httptest")
	sig := httptest.GetMethodSignature("bytes.Buffer", "ReadString")
	require.NotNil(t, sig, "bytes.Buffer, the type of the field ResponseRecorder.Body, must be recorded")
	require.Len(t, sig.Returns, 2)
	assert.Equal(t, "error", sig.Returns[1].String())

	http := analyzer.AnalyzeGoPackage("net/http")
	sig = http.GetMethodSignature("multipart.Reader", "NextPart")
	require.NotNil(t, sig, "multipart.Reader, the result of Request.MultipartReader, must be recorded")
	require.Len(t, sig.Returns, 2)
	assert.Equal(t, "*multipart.Part", sig.Returns[0].String())
	assert.NotNil(t, http.GetMethodSignature("multipart.Part", "FormName"),
		"multipart.Part, reached only through multipart.Reader.NextPart, must be recorded")
}

// TestGoSrcDirs_ModuleImportedByModule is the regression test for a Go module
// whose packages import another module (cobra imports pflag). go/importer
// cannot load the second one, so the first used to be type-checked with that
// import unresolved: Command.Flags() had no result type, and a call of a
// FlagSet method returning (T, error) through it was not lowered to Try. With
// both modules' sources wired in, the import is checked from source, and the
// types the file reaches through the first module are recorded with their
// methods, through a method result, a field and an interface alike.
func TestGoSrcDirs_ModuleImportedByModule(t *testing.T) {
	root := t.TempDir()
	write := func(rel, src string) {
		path := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(src), 0o644))
	}
	write("flags/flags.go", `package flags

type Set struct{ values map[string]string }

func (s *Set) GetString(name string) (string, error) { return s.values[name], nil }

func (s *Set) Lookup(name string) *Flag { return &Flag{} }

type Flag struct {
	Name  string
	Value Value
}

type Value interface {
	Parse() (int, error)
}
`)
	write("cmd/cmd.go", `package cmd

import "example.test/flags"

type Command struct{ set *flags.Set }

func (c *Command) Flags() *flags.Set { return c.set }
`)

	p := transpiler.NewAntlrGalaParser()
	batch := analyzer.NewBatchAnalyzer(p, getStdSearchPath(), t.TempDir())
	batch.SetGoSrcDirs(map[string]string{
		"example.test/cmd":   filepath.Join(root, "cmd"),
		"example.test/flags": filepath.Join(root, "flags"),
	})
	parsed := p.ParseFile(`package main

import "example.test/cmd"

func main() {
}
`)
	require.NoError(t, parsed.Err())
	richAST, err := batch.AnalyzeParsed(parsed, "")
	require.NoError(t, err)
	info := richAST.GoTypeInfo
	require.NotNil(t, info)

	ret := info.GetMethodReturnType("cmd.Command", "Flags")
	require.NotNil(t, ret)
	assert.Equal(t, "*flags.Set", ret.String())

	sig := info.GetMethodSignature("flags.Set", "GetString")
	require.NotNil(t, sig, "flags.Set, reached through Command.Flags(), must be recorded")
	require.Len(t, sig.Returns, 2)
	assert.Equal(t, "string", sig.Returns[0].String())
	assert.Equal(t, "error", sig.Returns[1].String())

	field := info.GetFieldType("flags.Flag", "Value")
	require.NotNil(t, field, "flags.Flag, reached through Set.Lookup, must be recorded")
	assert.Equal(t, "flags.Value", field.String())
	sig = info.GetMethodSignature("flags.Value", "Parse")
	require.NotNil(t, sig, "flags.Value, reached through the field Flag.Value, must be recorded")
	assert.Len(t, sig.Returns, 2)
}

// TestRequiredModules_GoImportOfAModuleNotRequired: with the modules the build
// requires known (the CLI builder sets them from gala.mod), a Go import that
// none of them provides is GALA-E0020 at the import, while the standard library,
// a required module's packages and an unknown set (Bazel) pass.
func TestRequiredModules_GoImportOfAModuleNotRequired(t *testing.T) {
	src := `package main

import (
    "strings"
    "example.test/mod/sub"
    "example.test/other"
)

func main() {
    Println(strings.ToUpper("x"))
}
`
	analyzeSrc := func(src string, required []string) error {
		p := transpiler.NewAntlrGalaParser()
		batch := analyzer.NewBatchAnalyzer(p, getStdSearchPath(), t.TempDir())
		batch.SetRequiredModules(required)
		parsed := p.ParseFile(src)
		require.NoError(t, parsed.Err())
		_, err := batch.AnalyzeParsed(parsed, "main.gala")
		return err
	}
	analyze := func(required []string) error { return analyzeSrc(src, required) }

	err := analyze([]string{"example.test/app", "example.test/mod"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0020")
	assert.Contains(t, err.Error(), `no module required in gala.mod provides Go package "example.test/other"`)
	assert.Contains(t, err.Error(), "gala mod add example.test/other@<version> --go")

	assert.NoError(t, analyze([]string{"example.test/mod", "example.test/other"}))
	assert.NoError(t, analyze(nil), "an unknown set of required modules checks nothing")

	// A major-version suffix names another module: requiring v1 does not
	// provide v2's packages.
	v2 := "package main\n\nimport \"example.test/mod/v2/sub\"\n\nfunc main() {\n}\n"
	err = analyzeSrc(v2, []string{"example.test/mod"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `Go package "example.test/mod/v2/sub"`)
	assert.NoError(t, analyzeSrc(v2, []string{"example.test/mod", "example.test/mod/v2"}))
}
