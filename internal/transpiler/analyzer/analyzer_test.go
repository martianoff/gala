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

func keysOf(m map[string]*transpiler.TypeMetadata) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
