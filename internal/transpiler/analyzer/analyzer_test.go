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

func TestAnalyzer(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	searchPaths := getStdSearchPath()
	a := analyzer.NewGalaAnalyzer(p, searchPaths)

	tests := []struct {
		name     string
		input    string
		validate func(*testing.T, *transpiler.RichAST)
	}{
		{
			name: "Basic struct with fields",
			input: `package main

struct Person(val name string, var age int)`,
			validate: func(t *testing.T, ast *transpiler.RichAST) {
				require.Contains(t, ast.Types, "Person")
				meta := ast.Types["Person"]
				assert.Equal(t, "Person", meta.Name)
				assert.Equal(t, []string{"name", "age"}, meta.FieldNames)
				assert.Equal(t, "string", meta.Fields["name"].String())
				assert.Equal(t, "int", meta.Fields["age"].String())
			},
		},
		{
			name: "Generic struct",
			input: `package main

type Box[T any] struct {
    Value T
}`,
			validate: func(t *testing.T, ast *transpiler.RichAST) {
				require.Contains(t, ast.Types, "Box")
				meta := ast.Types["Box"]
				assert.Equal(t, []string{"T"}, meta.TypeParams)
				assert.Equal(t, "T", meta.Fields["Value"].String())
			},
		},
		{
			name: "Method collection",
			input: `package main

struct Person(name string)

func (p Person) Greet() string = "Hello"`,
			validate: func(t *testing.T, ast *transpiler.RichAST) {
				require.Contains(t, ast.Types, "Person")
				meta := ast.Types["Person"]
				require.Contains(t, meta.Methods, "Greet")
				assert.Equal(t, "Greet", meta.Methods["Greet"].Name)
			},
		},
		{
			name: "Pointer receiver",
			input: `package main

struct Counter(count int)

func (c *Counter) Increment() {
    c.count = c.count + 1
}`,
			validate: func(t *testing.T, ast *transpiler.RichAST) {
				require.Contains(t, ast.Types, "Counter")
				meta := ast.Types["Counter"]
				require.Contains(t, meta.Methods, "Increment")
			},
		},
		{
			name: "Generic method",
			input: `package main

type Box[T any] struct {
    value T
}

func (b Box[T]) Map[U any](f func(T) U) Box[U] = Box(f(b.value))`,
			validate: func(t *testing.T, ast *transpiler.RichAST) {
				require.Contains(t, ast.Types, "Box")
				meta := ast.Types["Box"]
				require.Contains(t, meta.Methods, "Map")
				assert.Equal(t, []string{"U"}, meta.Methods["Map"].TypeParams)
			},
		},
		{
			name: "Multiple types and methods",
			input: `package main

struct A(x int)
struct B(y string)

func (a A) Foo() int = a.x
func (b B) Bar() string = b.y`,
			validate: func(t *testing.T, ast *transpiler.RichAST) {
				assert.Contains(t, ast.Types, "A")
				assert.Contains(t, ast.Types, "B")
				assert.Contains(t, ast.Types["A"].Methods, "Foo")
				assert.Contains(t, ast.Types["B"].Methods, "Bar")
			},
		},
		{
			name: "Method for type not in this file (placeholder)",
			input: `package main

func (e External) Action() = 1`,
			validate: func(t *testing.T, ast *transpiler.RichAST) {
				require.Contains(t, ast.Types, "External")
				meta := ast.Types["External"]
				assert.Contains(t, meta.Methods, "Action")
				assert.Empty(t, meta.Fields)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree, err := p.Parse(tt.input)
			require.NoError(t, err)

			richAST, err := a.Analyze(tree, "")
			require.NoError(t, err)
			require.NotNil(t, richAST)

			tt.validate(t, richAST)
		})
	}
}

func TestCompanionObjectDiscovery(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	searchPaths := []string{"../../../", "../../", "../"}
	base := analyzer.GetBaseMetadata(p, searchPaths)

	// Test that companion objects are discovered from std library
	t.Run("Some companion object", func(t *testing.T) {
		require.NotNil(t, base.CompanionObjects)
		// Check for Some companion object
		someMeta, found := base.CompanionObjects["Some"]
		if !found {
			someMeta, found = base.CompanionObjects["std.Some"]
		}
		require.True(t, found, "Some companion object should be discovered")
		assert.Equal(t, "Some", someMeta.Name)
		assert.Contains(t, someMeta.TargetType, "Option")
		assert.Equal(t, []int{0}, someMeta.ExtractIndices)
	})

	t.Run("Left companion object", func(t *testing.T) {
		leftMeta, found := base.CompanionObjects["Left"]
		if !found {
			leftMeta, found = base.CompanionObjects["std.Left"]
		}
		require.True(t, found, "Left companion object should be discovered")
		assert.Equal(t, "Left", leftMeta.Name)
		assert.Contains(t, leftMeta.TargetType, "Either")
		assert.Equal(t, []int{0}, leftMeta.ExtractIndices)
	})

	t.Run("Right companion object", func(t *testing.T) {
		rightMeta, found := base.CompanionObjects["Right"]
		if !found {
			rightMeta, found = base.CompanionObjects["std.Right"]
		}
		require.True(t, found, "Right companion object should be discovered")
		assert.Equal(t, "Right", rightMeta.Name)
		assert.Contains(t, rightMeta.TargetType, "Either")
		assert.Equal(t, []int{1}, rightMeta.ExtractIndices)
	})
}

func TestPackageFilesFullMetadata(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	searchPaths := getStdSearchPath()

	// Create temp directory with two sibling .gala files
	tmpDir := t.TempDir()

	// types.gala: defines struct + sealed type
	typesContent := `package shapes

struct Point(X int, Y int)

sealed type Shape {
    case Circle(Radius float64)
    case Rect(Width float64, Height float64)
}
`
	typesPath := filepath.Join(tmpDir, "types.gala")
	require.NoError(t, os.WriteFile(typesPath, []byte(typesContent), 0644))

	// ops.gala: defines methods on types from sibling
	opsContent := `package shapes

import "fmt"

func (p Point) String() string = fmt.Sprintf("(%d, %d)", p.X, p.Y)

func Describe(s Shape) string = s match {
    case Circle(r) => "circle"
    case Rect(w, h) => "rect"
}
`
	opsPath := filepath.Join(tmpDir, "ops.gala")
	require.NoError(t, os.WriteFile(opsPath, []byte(opsContent), 0644))

	t.Run("sibling shorthand struct has full field metadata", func(t *testing.T) {
		// Analyze ops.gala with types.gala as package file
		a := analyzer.NewGalaAnalyzerWithPackageFiles(p, searchPaths, []string{typesPath})
		tree, err := p.Parse(opsContent)
		require.NoError(t, err)
		richAST, err := a.Analyze(tree, opsPath)
		require.NoError(t, err)

		// Point should be registered with full field info
		pointMeta, ok := richAST.Types["shapes.Point"]
		require.True(t, ok, "shapes.Point should exist in Types, got: %v", keysOf(richAST.Types))
		assert.Equal(t, []string{"X", "Y"}, pointMeta.FieldNames)
		assert.Equal(t, []bool{true, true}, pointMeta.ImmutFlags)
		assert.Equal(t, "int", pointMeta.Fields["X"].String())
	})

	t.Run("sibling sealed type has full metadata", func(t *testing.T) {
		a := analyzer.NewGalaAnalyzerWithPackageFiles(p, searchPaths, []string{typesPath})
		tree, err := p.Parse(opsContent)
		require.NoError(t, err)
		richAST, err := a.Analyze(tree, opsPath)
		require.NoError(t, err)

		// Shape should be registered as sealed
		shapeMeta, ok := richAST.Types["shapes.Shape"]
		require.True(t, ok, "shapes.Shape should exist")
		assert.True(t, shapeMeta.IsSealed)
		assert.Len(t, shapeMeta.SealedVariants, 2)

		// Circle companion should exist
		_, ok = richAST.Types["shapes.Circle"]
		assert.True(t, ok, "shapes.Circle companion should exist")
	})

	t.Run("sealed type preserves methods when method file is processed first", func(t *testing.T) {
		// Regression: when sibling extraction processes a file declaring a
		// method on a sealed type BEFORE processing the file with the sealed
		// type declaration itself, analyzeSealedType used to overwrite the
		// placeholder entry in richAST.Types and drop the registered Methods.
		// Downstream this lost the method's return type, leaving val-bound
		// chained calls untyped and suppressing the auto-inserted .Get() on
		// subsequent field accesses (manifests as a Go build failure on the
		// generated code).
		//
		// We trigger the bad ordering by analyzing the *method* file as the
		// main tree (so its sibling extraction registers the method onto a
		// placeholder Shape entry first), and supplying the *types* file as
		// a package sibling (so analyzeSealedType runs second and overwrites).
		methodContent := `package shapes

func (s Shape) Tag() string = s match {
    case Circle(_) => "c"
    case Rect(_, _) => "r"
}
`
		methodPath := filepath.Join(tmpDir, "method.gala")
		require.NoError(t, os.WriteFile(methodPath, []byte(methodContent), 0644))

		a := analyzer.NewGalaAnalyzerWithPackageFiles(p, searchPaths, []string{typesPath})
		tree, err := p.Parse(methodContent)
		require.NoError(t, err)
		richAST, err := a.Analyze(tree, methodPath)
		require.NoError(t, err)

		shapeMeta, ok := richAST.Types["shapes.Shape"]
		require.True(t, ok, "shapes.Shape should exist after sealed-decl processed")
		require.Contains(t, shapeMeta.Methods, "Tag", "Tag method registered before analyzeSealedType ran should survive")
		assert.Equal(t, "string", shapeMeta.Methods["Tag"].ReturnType.String())
	})

	t.Run("main package sibling has full field metadata", func(t *testing.T) {
		// Test with main package (which was previously blocked for directory scanning)
		mainTypesContent := `package main

struct Person(Name string, Age int)
`
		mainOpsContent := `package main

import "fmt"

func (p Person) Greet() string = fmt.Sprintf("Hi %s", p.Name)
`
		mainTypesPath := filepath.Join(tmpDir, "main_types.gala")
		mainOpsPath := filepath.Join(tmpDir, "main_ops.gala")
		require.NoError(t, os.WriteFile(mainTypesPath, []byte(mainTypesContent), 0644))
		require.NoError(t, os.WriteFile(mainOpsPath, []byte(mainOpsContent), 0644))

		a := analyzer.NewGalaAnalyzerWithPackageFiles(p, searchPaths, []string{mainTypesPath})
		tree, err := p.Parse(mainOpsContent)
		require.NoError(t, err)
		richAST, err := a.Analyze(tree, mainOpsPath)
		require.NoError(t, err)

		personMeta, ok := richAST.Types["Person"]
		require.True(t, ok, "Person should exist in Types")
		assert.Equal(t, []string{"Name", "Age"}, personMeta.FieldNames)
		assert.Equal(t, []bool{true, true}, personMeta.ImmutFlags)
	})
}

func TestStructFieldNameCollidesWithType(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	searchPaths := getStdSearchPath()

	t.Run("field name matches sealed type in same file", func(t *testing.T) {
		// Repro for the IIFE param-type doubling bug surfaced by gala-tui:
		// `func (b Box[T]) Run() T = b.Mode match { ... }` emits
		// `func(obj Mode[T][T]) T {...}` (invalid Go) when the field is named
		// after its sealed type. We reject at the analyzer instead.
		tmpDir := t.TempDir()
		src := `package mylib

sealed type Mode[T any] {
    case A(Fn func(int) T)
    case B(Fn func(string) T)
}

struct Box[T any](Mode Mode[T])
`
		filePath := filepath.Join(tmpDir, "file.gala")
		require.NoError(t, os.WriteFile(filePath, []byte(src), 0644))

		a := analyzer.NewGalaAnalyzer(p, searchPaths)
		tree, err := p.Parse(src)
		require.NoError(t, err)
		_, err = a.Analyze(tree, filePath)
		require.Error(t, err, "should reject field named after a sealed type")
		assert.Contains(t, err.Error(), "GALA-E0016")
		assert.Contains(t, err.Error(), `"Mode"`)
		assert.Contains(t, err.Error(), `"Box"`)
	})

	t.Run("field name matches generic struct in same file", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := `package mylib

struct Inner[T any](X T)

struct Outer[T any](Inner Inner[T])
`
		filePath := filepath.Join(tmpDir, "file.gala")
		require.NoError(t, os.WriteFile(filePath, []byte(src), 0644))

		a := analyzer.NewGalaAnalyzer(p, searchPaths)
		tree, err := p.Parse(src)
		require.NoError(t, err)
		_, err = a.Analyze(tree, filePath)
		require.Error(t, err, "should reject field named after another generic struct in same package")
		assert.Contains(t, err.Error(), "GALA-E0016")
		assert.Contains(t, err.Error(), `"Inner"`)
	})

	t.Run("non-generic field-type collision is allowed", func(t *testing.T) {
		// Pre-existing pattern in examples/cross_file_unwrap/types.gala —
		// `struct Route(Handler Handler)` works fine because no type-param
		// substitution is needed. Stay narrow: only flag when both the
		// containing struct and the shadowed type are generic.
		tmpDir := t.TempDir()
		src := `package mylib

struct Handler(Name string)

struct Route(Method string, Pattern string, Handler Handler)
`
		filePath := filepath.Join(tmpDir, "file.gala")
		require.NoError(t, os.WriteFile(filePath, []byte(src), 0644))

		a := analyzer.NewGalaAnalyzer(p, searchPaths)
		tree, err := p.Parse(src)
		require.NoError(t, err)
		_, err = a.Analyze(tree, filePath)
		assert.NoError(t, err, "non-generic field-type collision should not trigger E0016")
	})

	t.Run("field named after own struct is allowed", func(t *testing.T) {
		// `struct Foo(Foo int)` is non-idiomatic but does not trigger the
		// IIFE codegen bug; Go itself accepts it. Stay narrow and pass it.
		tmpDir := t.TempDir()
		src := `package mylib

struct Foo(Foo int)
`
		filePath := filepath.Join(tmpDir, "file.gala")
		require.NoError(t, os.WriteFile(filePath, []byte(src), 0644))

		a := analyzer.NewGalaAnalyzer(p, searchPaths)
		tree, err := p.Parse(src)
		require.NoError(t, err)
		_, err = a.Analyze(tree, filePath)
		assert.NoError(t, err, "self-named field should not trigger E0016")
	})

	t.Run("renamed field passes (positive control)", func(t *testing.T) {
		// User's gala-tui Harness pattern — field `Mode` on `Harness[M, T]` whose
		// type is `HarnessMode[T]` (different identifier). Should compile.
		tmpDir := t.TempDir()
		src := `package mylib

sealed type HarnessMode[T any] {
    case KeyMode(Fn func(int) T)
    case FullMode(Fn func(string) T)
}

struct Harness[M any, T any](Mode HarnessMode[T])
`
		filePath := filepath.Join(tmpDir, "file.gala")
		require.NoError(t, os.WriteFile(filePath, []byte(src), 0644))

		a := analyzer.NewGalaAnalyzer(p, searchPaths)
		tree, err := p.Parse(src)
		require.NoError(t, err)
		_, err = a.Analyze(tree, filePath)
		assert.NoError(t, err, "field name distinct from type name should compile")
	})
}

func TestTypeRedefinitionError(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	searchPaths := getStdSearchPath()

	t.Run("struct redefined in sibling file", func(t *testing.T) {
		tmpDir := t.TempDir()

		// file1.gala: defines struct Person
		file1 := `package mylib

struct Person(Name string, Age int)
`
		// file2.gala: also defines struct Person — should be an error
		file2 := `package mylib

struct Person(Email string)
`
		file1Path := filepath.Join(tmpDir, "file1.gala")
		file2Path := filepath.Join(tmpDir, "file2.gala")
		require.NoError(t, os.WriteFile(file1Path, []byte(file1), 0644))
		require.NoError(t, os.WriteFile(file2Path, []byte(file2), 0644))

		// Analyze file2 with file1 as package-file sibling
		a := analyzer.NewGalaAnalyzerWithPackageFiles(p, searchPaths, []string{file1Path})
		tree, err := p.Parse(file2)
		require.NoError(t, err)
		_, err = a.Analyze(tree, file2Path)
		require.Error(t, err, "should error on type redefinition")
		assert.Contains(t, err.Error(), "redefined")
		assert.Contains(t, err.Error(), "Person")
	})

	t.Run("shorthand struct redefined in sibling file", func(t *testing.T) {
		tmpDir := t.TempDir()

		file1 := `package mylib

struct Point(X int, Y int)
`
		file2 := `package mylib

struct Point(A float64, B float64)
`
		file1Path := filepath.Join(tmpDir, "file1.gala")
		file2Path := filepath.Join(tmpDir, "file2.gala")
		require.NoError(t, os.WriteFile(file1Path, []byte(file1), 0644))
		require.NoError(t, os.WriteFile(file2Path, []byte(file2), 0644))

		a := analyzer.NewGalaAnalyzerWithPackageFiles(p, searchPaths, []string{file1Path})
		tree, err := p.Parse(file2)
		require.NoError(t, err)
		_, err = a.Analyze(tree, file2Path)
		require.Error(t, err, "should error on shorthand struct redefinition")
		assert.Contains(t, err.Error(), "redefined")
		assert.Contains(t, err.Error(), "Point")
	})

	t.Run("sealed type redefined in sibling file", func(t *testing.T) {
		tmpDir := t.TempDir()

		file1 := `package mylib

sealed type Color {
    case Red()
    case Blue()
}
`
		file2 := `package mylib

sealed type Color {
    case Green()
    case Yellow()
}
`
		file1Path := filepath.Join(tmpDir, "file1.gala")
		file2Path := filepath.Join(tmpDir, "file2.gala")
		require.NoError(t, os.WriteFile(file1Path, []byte(file1), 0644))
		require.NoError(t, os.WriteFile(file2Path, []byte(file2), 0644))

		a := analyzer.NewGalaAnalyzerWithPackageFiles(p, searchPaths, []string{file1Path})
		tree, err := p.Parse(file2)
		require.NoError(t, err)
		_, err = a.Analyze(tree, file2Path)
		require.Error(t, err, "should error on sealed type redefinition")
		assert.Contains(t, err.Error(), "redefined")
		assert.Contains(t, err.Error(), "Color")
	})

	t.Run("struct redefined in same file", func(t *testing.T) {
		src := `package mylib

type Person struct {
    Name string
    Age int
}

type Person struct {
    Email string
}
`
		a := analyzer.NewGalaAnalyzer(p, searchPaths)
		tree, err := p.Parse(src)
		require.NoError(t, err)
		_, err = a.Analyze(tree, "test.gala")
		require.Error(t, err, "should error on same-file struct redefinition")
		assert.Contains(t, err.Error(), "redefined")
		assert.Contains(t, err.Error(), "Person")
	})

	t.Run("shorthand struct redefined in same file", func(t *testing.T) {
		src := `package mylib

struct Point(X int, Y int)

struct Point(A float64)
`
		a := analyzer.NewGalaAnalyzer(p, searchPaths)
		tree, err := p.Parse(src)
		require.NoError(t, err)
		_, err = a.Analyze(tree, "test.gala")
		require.Error(t, err, "should error on same-file shorthand struct redefinition")
		assert.Contains(t, err.Error(), "redefined")
		assert.Contains(t, err.Error(), "Point")
	})

	t.Run("sealed type redefined in same file", func(t *testing.T) {
		src := `package mylib

sealed type Color {
    case Red()
    case Blue()
}

sealed type Color {
    case Green()
}
`
		a := analyzer.NewGalaAnalyzer(p, searchPaths)
		tree, err := p.Parse(src)
		require.NoError(t, err)
		_, err = a.Analyze(tree, "test.gala")
		require.Error(t, err, "should error on same-file sealed type redefinition")
		assert.Contains(t, err.Error(), "redefined")
		assert.Contains(t, err.Error(), "Color")
	})

	t.Run("method redefined in same file", func(t *testing.T) {
		src := `package mylib

struct Person(Name string)

func (p Person) Greet() string = "hello"

func (p Person) Greet() string = "hi"
`
		a := analyzer.NewGalaAnalyzer(p, searchPaths)
		tree, err := p.Parse(src)
		require.NoError(t, err)
		_, err = a.Analyze(tree, "test.gala")
		require.Error(t, err, "should error on same-file method redefinition")
		assert.Contains(t, err.Error(), "redefined")
		assert.Contains(t, err.Error(), "Greet")
	})

	t.Run("methods on sibling type should NOT error", func(t *testing.T) {
		tmpDir := t.TempDir()

		// file1: defines struct
		file1 := `package mylib

struct Person(Name string, Age int)
`
		// file2: adds methods — should be fine
		file2 := `package mylib

import "fmt"

func (p Person) Greet() string = fmt.Sprintf("Hello %s", p.Name)
`
		file1Path := filepath.Join(tmpDir, "file1.gala")
		file2Path := filepath.Join(tmpDir, "file2.gala")
		require.NoError(t, os.WriteFile(file1Path, []byte(file1), 0644))
		require.NoError(t, os.WriteFile(file2Path, []byte(file2), 0644))

		a := analyzer.NewGalaAnalyzerWithPackageFiles(p, searchPaths, []string{file1Path})
		tree, err := p.Parse(file2)
		require.NoError(t, err)
		_, err = a.Analyze(tree, file2Path)
		assert.NoError(t, err, "methods on sibling types should not trigger redefinition error")
	})
}

// TestT4SiblingExtractionSkipsMainAndTest encodes the foot-gun that
// directory-discovered sibling files must NOT be pulled in when the
// current file is in a `main` or `test` package. In those packages,
// sibling `.gala` files are independent programs sharing a directory
// (the canonical example is `examples/`), so cross-contaminating their
// metadata would pollute type resolution with unrelated packages.
//
// The live skip lives at analyzer.go:~909 in the directory-discovery
// branch. This test exercises the skip by placing two main-package
// files in the same directory with conflicting type definitions and
// asserting that analysis succeeds (the conflict is NOT surfaced
// because the sibling is not scanned).
func TestT4SiblingExtractionSkipsMainAndTest(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	searchPaths := getStdSearchPath()

	t.Run("main package does not scan siblings", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Two independent "main" programs sharing a directory — the
		// canonical examples/ layout. If the analyzer scanned siblings
		// for main, it would see the duplicate Foo definition and
		// emit GALA-E0011; the skip should prevent that.
		file1 := `package main

struct Foo(A int)

func main() {
    val f = Foo(1)
    _ = f
}
`
		file2 := `package main

struct Foo(B string)

func main() {
    val g = Foo("hello")
    _ = g
}
`
		file1Path := filepath.Join(tmpDir, "prog1.gala")
		file2Path := filepath.Join(tmpDir, "prog2.gala")
		require.NoError(t, os.WriteFile(file1Path, []byte(file1), 0644))
		require.NoError(t, os.WriteFile(file2Path, []byte(file2), 0644))

		a := analyzer.NewGalaAnalyzer(p, searchPaths)
		tree, err := p.Parse(file1)
		require.NoError(t, err)
		_, err = a.Analyze(tree, file1Path)
		assert.NoError(t, err,
			"main-package sibling scanning must be skipped; otherwise prog2's Foo would collide with prog1's")
	})

	t.Run("test package does not scan siblings", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Two test-package programs in the same directory.
		file1 := `package test

struct Bar(X int)
`
		file2 := `package test

struct Bar(Y string)
`
		file1Path := filepath.Join(tmpDir, "t1.gala")
		file2Path := filepath.Join(tmpDir, "t2.gala")
		require.NoError(t, os.WriteFile(file1Path, []byte(file1), 0644))
		require.NoError(t, os.WriteFile(file2Path, []byte(file2), 0644))

		a := analyzer.NewGalaAnalyzer(p, searchPaths)
		tree, err := p.Parse(file1)
		require.NoError(t, err)
		_, err = a.Analyze(tree, file1Path)
		assert.NoError(t, err,
			"test-package sibling scanning must be skipped")
	})

	t.Run("library package DOES scan siblings (control)", func(t *testing.T) {
		// The inverse assertion: for a normal library package, sibling
		// scanning IS active, so a duplicate type across files DOES
		// surface as GALA-E0011. This keeps the skip narrow and
		// guards against an over-broad regression.
		tmpDir := t.TempDir()
		file1 := `package mylib

struct Widget(A int)
`
		file2 := `package mylib

struct Widget(B string)
`
		file1Path := filepath.Join(tmpDir, "f1.gala")
		file2Path := filepath.Join(tmpDir, "f2.gala")
		require.NoError(t, os.WriteFile(file1Path, []byte(file1), 0644))
		require.NoError(t, os.WriteFile(file2Path, []byte(file2), 0644))

		a := analyzer.NewGalaAnalyzer(p, searchPaths)
		tree, err := p.Parse(file1)
		require.NoError(t, err)
		_, err = a.Analyze(tree, file1Path)
		require.Error(t, err,
			"library sibling scanning should fire redefinition error")
		assert.Contains(t, err.Error(), "GALA-E0011")
		assert.Contains(t, err.Error(), "Widget")
	})
}

// TestP1SiblingASTCacheReusesParsedTrees verifies that the P1 sibling
// AST cache reuses parsed trees across multiple Analyze() calls on
// the same directory. The proof-of-cache-hit strategy is to overwrite
// a sibling's content with bytes that would FAIL to parse, keeping
// the .gala-file count unchanged so the cache validity check
// (dirSize) does not invalidate. A second Analyze() call that still
// succeeds can only have come from the cached tree.
//
// Cache invalidation on size change is covered by
// TestP1SiblingASTCacheInvalidatesOnSizeChange below.
func TestP1SiblingASTCacheReusesParsedTrees(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	searchPaths := getStdSearchPath()

	tmpDir := t.TempDir()

	file1 := `package demo

struct Point(X int, Y int)
`
	file2 := `package demo

struct Label(Text string)
`
	file1Path := filepath.Join(tmpDir, "f1.gala")
	file2Path := filepath.Join(tmpDir, "f2.gala")
	require.NoError(t, os.WriteFile(file1Path, []byte(file1), 0644))
	require.NoError(t, os.WriteFile(file2Path, []byte(file2), 0644))

	a := analyzer.NewGalaAnalyzer(p, searchPaths)

	// First Analyze — populates the sibling cache for tmpDir.
	tree1, err := p.Parse(file1)
	require.NoError(t, err)
	ast1, err := a.Analyze(tree1, file1Path)
	require.NoError(t, err)
	require.Contains(t, ast1.Types, "demo.Label",
		"sibling Label should be resolved via directory scan on first call")

	// Corrupt f2.gala with bytes that would fail to parse. The file
	// count stays at 2, so the cache's dirSize validity check passes
	// and the cache is used. If the analyzer re-read and re-parsed
	// f2 from disk, it would see the garbage and fail.
	require.NoError(t, os.WriteFile(file2Path, []byte("!! not valid GALA !!"), 0644))

	tree1b, err := p.Parse(file1)
	require.NoError(t, err)
	ast2, err := a.Analyze(tree1b, file1Path)
	require.NoError(t, err,
		"second Analyze call must reuse the cached sibling tree and not re-parse from disk (would have failed otherwise)")
	assert.Contains(t, ast2.Types, "demo.Label",
		"sibling Label should still resolve — cache hit preserved the original parse")
}

// TestP1SiblingASTCacheInvalidatesOnSizeChange verifies that when the
// directory's .gala-file count changes between Analyze() calls, the
// cache is dropped and a fresh scan picks up the new set. This is the
// cheap-and-correct invalidation strategy P1 relies on.
func TestP1SiblingASTCacheInvalidatesOnSizeChange(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	searchPaths := getStdSearchPath()

	tmpDir := t.TempDir()

	file1 := `package demo

struct Point(X int, Y int)
`
	file1Path := filepath.Join(tmpDir, "f1.gala")
	require.NoError(t, os.WriteFile(file1Path, []byte(file1), 0644))

	a := analyzer.NewGalaAnalyzer(p, searchPaths)

	tree1, err := p.Parse(file1)
	require.NoError(t, err)
	ast1, err := a.Analyze(tree1, file1Path)
	require.NoError(t, err)
	// Only Point is present; no siblings yet.
	assert.Contains(t, ast1.Types, "demo.Point")
	assert.NotContains(t, ast1.Types, "demo.Widget")

	// Add a new sibling after the cache was populated.
	newSibling := filepath.Join(tmpDir, "f2.gala")
	require.NoError(t, os.WriteFile(newSibling, []byte(`package demo

struct Widget(Name string)
`), 0644))

	tree1b, err := p.Parse(file1)
	require.NoError(t, err)
	ast2, err := a.Analyze(tree1b, file1Path)
	require.NoError(t, err)
	// The new sibling appears — cache was invalidated by the size change.
	assert.Contains(t, ast2.Types, "demo.Widget",
		"new sibling should surface: cache must invalidate when .gala-file count changes")
}

func keysOf(m map[string]*transpiler.TypeMetadata) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
