package analyzer_test

import (
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func skipIfNoGoSDK(t *testing.T) {
	t.Helper()
	if !analyzer.GoImporterAvailable() {
		t.Skip("Go SDK not available (Bazel sandbox)")
	}
}

func TestAnalyzeGoPackage_FmtPackage(t *testing.T) {
	skipIfNoGoSDK(t)

	info := analyzer.AnalyzeGoPackage("fmt")
	require.NotNil(t, info)

	// fmt.Sprintf should return string
	sig := info.GetFuncSignature("fmt.Sprintf")
	require.NotNil(t, sig, "fmt.Sprintf should be found")
	require.Len(t, sig.Returns, 1)
	assert.Equal(t, "string", sig.Returns[0].String())
	assert.True(t, sig.IsVariadic)

	// fmt.Println should return (int, error)
	sig = info.GetFuncSignature("fmt.Println")
	require.NotNil(t, sig, "fmt.Println should be found")
	require.Len(t, sig.Returns, 2)
	assert.Equal(t, "int", sig.Returns[0].String())
	assert.Equal(t, "error", sig.Returns[1].String())

	// fmt.Errorf should return error
	retType := info.GetFuncReturnType("fmt.Errorf")
	require.NotNil(t, retType)
	assert.Equal(t, "error", retType.String())
}

func TestAnalyzeGoPackage_StringsPackage(t *testing.T) {
	skipIfNoGoSDK(t)

	info := analyzer.AnalyzeGoPackage("strings")
	require.NotNil(t, info)

	// strings.Split should return []string
	sig := info.GetFuncSignature("strings.Split")
	require.NotNil(t, sig, "strings.Split should be found")
	require.Len(t, sig.Returns, 1)
	assert.Equal(t, "[]string", sig.Returns[0].String())

	// strings.Contains should return bool
	retType := info.GetFuncReturnType("strings.Contains")
	require.NotNil(t, retType)
	assert.Equal(t, "bool", retType.String())

	// strings.NewReader should return *strings.Reader
	sig = info.GetFuncSignature("strings.NewReader")
	require.NotNil(t, sig, "strings.NewReader should be found")
	require.Len(t, sig.Returns, 1)
	retStr := sig.Returns[0].String()
	assert.Equal(t, "*strings.Reader", retStr)

	// strings.Reader should have methods
	td := info.GetTypeData("strings.Reader")
	require.NotNil(t, td, "strings.Reader type should be found")
	assert.Equal(t, "struct", td.Kind)
	// Reader.Read should exist
	readSig, ok := td.Methods["Read"]
	assert.True(t, ok, "Reader.Read method should exist")
	if ok {
		require.Len(t, readSig.Returns, 2)
		assert.Equal(t, "int", readSig.Returns[0].String())
		assert.Equal(t, "error", readSig.Returns[1].String())
	}
}

func TestAnalyzeGoPackage_NetHTTP(t *testing.T) {
	skipIfNoGoSDK(t)

	info := analyzer.AnalyzeGoPackage("net/http")
	require.NotNil(t, info)

	// http.NewRequest should return (*http.Request, error)
	sig := info.GetFuncSignature("http.NewRequest")
	require.NotNil(t, sig, "http.NewRequest should be found")
	require.Len(t, sig.Returns, 2)
	assert.Equal(t, "*http.Request", sig.Returns[0].String())
	assert.Equal(t, "error", sig.Returns[1].String())

	// http.Request should be a struct with fields
	td := info.GetTypeData("http.Request")
	require.NotNil(t, td, "http.Request type should be found")
	assert.Equal(t, "struct", td.Kind)

	// Request.Method should be string
	methodType := td.Fields["Method"]
	require.NotNil(t, methodType, "Request.Method field should exist")
	assert.Equal(t, "string", methodType.String())

	// Request.URL should be *url.URL
	urlType := td.Fields["URL"]
	require.NotNil(t, urlType, "Request.URL field should exist")
	assert.Equal(t, "*url.URL", urlType.String())
}

func TestAnalyzeGoPackage_TypeAliases(t *testing.T) {
	skipIfNoGoSDK(t)

	info := analyzer.AnalyzeGoPackage("net/http")
	require.NotNil(t, info)

	// http.Header is a type definition: type Header map[string][]string
	td := info.GetTypeData("http.Header")
	require.NotNil(t, td, "http.Header type should be found")
	// Header has methods like Set, Get, Add
	setSig, ok := td.Methods["Set"]
	assert.True(t, ok, "Header.Set method should exist")
	if ok {
		assert.Len(t, setSig.Returns, 0, "Header.Set returns nothing")
	}
	getSig, ok := td.Methods["Get"]
	assert.True(t, ok, "Header.Get method should exist")
	if ok {
		require.Len(t, getSig.Returns, 1)
		assert.Equal(t, "string", getSig.Returns[0].String())
	}
}

func TestAnalyzeGoPackage_GoTypeInfo_FieldAccess(t *testing.T) {
	skipIfNoGoSDK(t)

	info := analyzer.AnalyzeGoPackage("net/http")
	require.NotNil(t, info)

	// Test GetFieldType helper
	methodType := info.GetFieldType("http.Request", "Method")
	require.NotNil(t, methodType)
	assert.Equal(t, "string", methodType.String())

	// Non-existent field returns nil
	nilType := info.GetFieldType("http.Request", "NonExistent")
	assert.Nil(t, nilType)

	// Non-existent type returns nil
	nilType = info.GetFieldType("http.NonExistent", "Method")
	assert.Nil(t, nilType)
}

func TestAnalyzeGoPackage_GoTypeInfo_MethodReturnType(t *testing.T) {
	skipIfNoGoSDK(t)

	info := analyzer.AnalyzeGoPackage("strings")
	require.NotNil(t, info)

	// strings.Builder.String() should return string
	retType := info.GetMethodReturnType("strings.Builder", "String")
	require.NotNil(t, retType)
	assert.Equal(t, "string", retType.String())

	// strings.Builder.Len() should return int
	retType = info.GetMethodReturnType("strings.Builder", "Len")
	require.NotNil(t, retType)
	assert.Equal(t, "int", retType.String())
}

func TestAnalyzeGoPackage_Variables(t *testing.T) {
	skipIfNoGoSDK(t)

	info := analyzer.AnalyzeGoPackage("os")
	require.NotNil(t, info)

	// os.Stdin, os.Stdout, os.Stderr should be *os.File
	stdinType := info.Variables["os.Stdin"]
	require.NotNil(t, stdinType, "os.Stdin should be found")
	assert.Equal(t, "*os.File", stdinType.String())
}

func TestAnalyzeGoPackage_Constants(t *testing.T) {
	skipIfNoGoSDK(t)

	info := analyzer.AnalyzeGoPackage("math")
	require.NotNil(t, info)

	// math.Pi should be a constant
	piType := info.Constants["math.Pi"]
	require.NotNil(t, piType, "math.Pi should be found")
	assert.Contains(t, piType.String(), "float")

	// math.Abs should return float64
	retType := info.GetFuncReturnType("math.Abs")
	require.NotNil(t, retType)
	assert.Equal(t, "float64", retType.String())
}

func TestAnalyzeGoPackage_StrconvMultiReturn(t *testing.T) {
	skipIfNoGoSDK(t)

	info := analyzer.AnalyzeGoPackage("strconv")
	require.NotNil(t, info)

	// strconv.Atoi should return (int, error)
	sig := info.GetFuncSignature("strconv.Atoi")
	require.NotNil(t, sig)
	require.Len(t, sig.Returns, 2)
	assert.Equal(t, "int", sig.Returns[0].String())
	assert.Equal(t, "error", sig.Returns[1].String())

	// strconv.Itoa should return string
	retType := info.GetFuncReturnType("strconv.Itoa")
	require.NotNil(t, retType)
	assert.Equal(t, "string", retType.String())
}

func TestAnalyzeGoPackage_MapType(t *testing.T) {
	skipIfNoGoSDK(t)

	info := analyzer.AnalyzeGoPackage("net/http")
	require.NotNil(t, info)

	// http.Header underlying type should be map[string][]string
	td := info.GetTypeData("http.Header")
	require.NotNil(t, td)
	assert.Equal(t, "named", td.Kind)
	underlying := td.Underlying
	require.NotNil(t, underlying)
	_, isMap := underlying.(transpiler.MapType)
	assert.True(t, isMap, "Header underlying type should be MapType, got %T: %s", underlying, underlying)
}

func TestAnalyzeGoPackage_FuncType(t *testing.T) {
	skipIfNoGoSDK(t)

	info := analyzer.AnalyzeGoPackage("sort")
	require.NotNil(t, info)

	// sort.Slice should have func params
	sig := info.GetFuncSignature("sort.Slice")
	require.NotNil(t, sig, "sort.Slice should be found")
	require.Len(t, sig.Params, 2)
	// Second param should be a FuncType
	lessType := sig.Params[1].Type
	funcType, ok := lessType.(transpiler.FuncType)
	assert.True(t, ok, "sort.Slice second param should be FuncType, got %T", lessType)
	if ok {
		require.Len(t, funcType.Params, 2)
		assert.Equal(t, "int", funcType.Params[0].String())
		assert.Equal(t, "int", funcType.Params[1].String())
		require.Len(t, funcType.Results, 1)
		assert.Equal(t, "bool", funcType.Results[0].String())
	}
}

func TestAnalyzeGoPackage_ResolveTypeAlias(t *testing.T) {
	info := transpiler.NewGoTypeInfo()

	// Simulate a Go type alias: type MyString = string
	info.TypeAliases["mypkg.MyString"] = transpiler.BasicType{Name: "string"}

	// ResolveTypeAlias should return the underlying type
	resolved := info.ResolveTypeAlias("mypkg.MyString")
	require.NotNil(t, resolved)
	assert.Equal(t, "string", resolved.String())

	// Non-alias should return nil
	resolved = info.ResolveTypeAlias("mypkg.NotAnAlias")
	assert.Nil(t, resolved)
}

// TestAnalyzeGoPackage_ReexportedAliasTargetMethods guards the fix that makes
// methods on a type re-exported through an alias resolvable under the type's
// own canonical "pkgName.TypeName" key. `os.DirEntry` is an alias for
// `io/fs.DirEntry`; `os.ReadDir` returns `[]os.DirEntry`, which Go resolves to
// `[]fs.DirEntry`. So calling a method on an element (`entries[i].Info()`) must
// find the method set under "fs.DirEntry" even though only "os" was analyzed —
// io/fs is never directly imported. Without registerAliasTargetType,
// info.Types["fs.DirEntry"] is nil and the method lookup misses, which in turn
// breaks `Try(entries[i].Info())` (the Try type param can't be inferred and the
// (T, error) return can't be thunked). Analyzing only "os" here mirrors the
// real GALA import surface and keeps the test adversarially honest.
func TestAnalyzeGoPackage_ReexportedAliasTargetMethods(t *testing.T) {
	skipIfNoGoSDK(t)

	info := analyzer.AnalyzeGoPackage("os")
	require.NotNil(t, info)

	// Baseline: the alias itself is recorded under the aliasing package's name.
	require.NotNil(t, info.TypeAliases["os.DirEntry"], "os.DirEntry alias should be recorded")

	// The fix: the alias target's method set is also registered under its own
	// canonical name, so a value typed as fs.DirEntry can resolve its methods.
	sig := info.GetMethodSignature("fs.DirEntry", "Info")
	require.NotNil(t, sig, "fs.DirEntry.Info method must be resolvable from an os-only analysis")

	// fs.DirEntry.Info() returns (fs.FileInfo, error) — the (T, error) shape the
	// Try sugar depends on to thunk + catch the error.
	require.Len(t, sig.Returns, 2)
	assert.Equal(t, "error", sig.Returns[len(sig.Returns)-1].String())
	assert.Equal(t, "fs.FileInfo", sig.Returns[0].String())

	// The same must hold for os.FileInfo = io/fs.FileInfo (used by os.Stat).
	fiSig := info.GetMethodSignature("fs.FileInfo", "Size")
	require.NotNil(t, fiSig, "fs.FileInfo.Size must be resolvable from an os-only analysis")
	require.Len(t, fiSig.Returns, 1)
	assert.Equal(t, "int64", fiSig.Returns[0].String())
}

func TestAnalyzeGoPackage_NonExistentPackage(t *testing.T) {
	info := analyzer.AnalyzeGoPackage("nonexistent/package/path")
	require.NotNil(t, info)
	assert.Empty(t, info.Functions)
	assert.Empty(t, info.Types)
}

func TestAnalyzeGoPackage_Merge(t *testing.T) {
	info1 := transpiler.NewGoTypeInfo()
	info1.Functions["pkg1.Foo"] = &transpiler.GoFuncSignature{
		Returns: []transpiler.Type{transpiler.BasicType{Name: "string"}},
	}
	info1.TypeAliases["pkg1.MyAlias"] = transpiler.BasicType{Name: "int"}

	info2 := transpiler.NewGoTypeInfo()
	info2.Functions["pkg2.Bar"] = &transpiler.GoFuncSignature{
		Returns: []transpiler.Type{transpiler.BasicType{Name: "bool"}},
	}

	info1.Merge(info2)

	assert.NotNil(t, info1.Functions["pkg1.Foo"])
	assert.NotNil(t, info1.Functions["pkg2.Bar"])
	assert.NotNil(t, info1.TypeAliases["pkg1.MyAlias"])
}

func TestAnalyzeGoPackage_SliceType(t *testing.T) {
	skipIfNoGoSDK(t)

	info := analyzer.AnalyzeGoPackage("strings")
	require.NotNil(t, info)

	// strings.Split returns []string
	sig := info.GetFuncSignature("strings.Split")
	require.NotNil(t, sig)
	require.Len(t, sig.Returns, 1)
	retType := sig.Returns[0]
	arrType, ok := retType.(transpiler.ArrayType)
	assert.True(t, ok, "strings.Split should return ArrayType, got %T", retType)
	if ok {
		assert.Equal(t, "string", arrType.Elem.String())
	}
}

func TestAnalyzeGoPackage_PointerType(t *testing.T) {
	skipIfNoGoSDK(t)

	info := analyzer.AnalyzeGoPackage("bufio")
	require.NotNil(t, info)

	// bufio.NewScanner should return *bufio.Scanner
	sig := info.GetFuncSignature("bufio.NewScanner")
	require.NotNil(t, sig)
	require.Len(t, sig.Returns, 1)
	retType := sig.Returns[0]
	ptrType, ok := retType.(transpiler.PointerType)
	assert.True(t, ok, "bufio.NewScanner should return PointerType, got %T: %s", retType, retType)
	if ok {
		assert.Equal(t, "bufio.Scanner", ptrType.Elem.String())
	}

	// Scanner should have Text() method returning string
	td := info.GetTypeData("bufio.Scanner")
	require.NotNil(t, td)
	textSig, ok := td.Methods["Text"]
	assert.True(t, ok, "Scanner.Text method should exist")
	if ok {
		require.Len(t, textSig.Returns, 1)
		assert.Equal(t, "string", textSig.Returns[0].String())
	}
}

func TestAnalyzeGoPackage_OsPackage(t *testing.T) {
	skipIfNoGoSDK(t)

	info := analyzer.AnalyzeGoPackage("os")
	require.NotNil(t, info)

	// os.Open should return (*os.File, error)
	sig := info.GetFuncSignature("os.Open")
	require.NotNil(t, sig)
	require.Len(t, sig.Returns, 2)
	assert.Equal(t, "*os.File", sig.Returns[0].String())
	assert.Equal(t, "error", sig.Returns[1].String())

	// os.File should have methods
	td := info.GetTypeData("os.File")
	require.NotNil(t, td)
	readSig, ok := td.Methods["Read"]
	assert.True(t, ok, "os.File.Read method should exist")
	if ok {
		require.Len(t, readSig.Returns, 2)
		assert.Equal(t, "int", readSig.Returns[0].String())
		assert.Equal(t, "error", readSig.Returns[1].String())
	}
}

func TestGoTypeInfo_NilSafety(t *testing.T) {
	// All methods should be nil-safe
	var info *transpiler.GoTypeInfo

	assert.Nil(t, info.GetFuncReturnType("foo.Bar"))
	assert.Nil(t, info.GetFuncSignature("foo.Bar"))
	assert.Nil(t, info.GetTypeData("foo.Bar"))
	assert.Nil(t, info.GetFieldType("foo.Bar", "Baz"))
	assert.Nil(t, info.GetMethodReturnType("foo.Bar", "Baz"))
	assert.Nil(t, info.ResolveTypeAlias("foo.Bar"))
}
