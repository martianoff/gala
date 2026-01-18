package analyzer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"testing"

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
