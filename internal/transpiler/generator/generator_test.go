package generator

import (
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGoCodeGenerator_Generate(t *testing.T) {
	g := NewGoCodeGenerator()

	tests := []struct {
		name     string
		source   string
		expected string
		wantErr  bool
	}{
		{
			name: "Simple package and function",
			source: `package main
func main() {
	println("hello")
}
`,
			expected: `package main

func main() {
	println("hello")
}
`,
			wantErr: false,
		},
		{
			name: "Struct and method",
			source: `package test
type Point struct {
	X, Y int
}
func (p Point) Sum() int { return p.X + p.Y }
`,
			expected: `package test

type Point struct {
	X, Y int
}

func (p Point) Sum() int { return p.X + p.Y }
`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "test.go", tt.source, parser.ParseComments)
			assert.NoError(t, err)

			got, err := g.Generate(fset, file)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}
