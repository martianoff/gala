package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestImports(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, tr, g)

	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{
			name: "single import",
			input: `package main
import "fmt"`,
			expected: `package main

import "fmt"
`,
		},
		{
			name: "multiple imports",
			input: `package main
import (
    "fmt"
    "math"
)`,
			expected: `package main

import (
	"fmt"
	"math"
)
`,
		},
		{
			name: "aliased import",
			input: `package main
import f "fmt"`,
			expected: `package main

import f "fmt"
`,
		},
		{
			name: "dot import",
			input: `package main
import . "math"`,
			expected: `package main

import . "math"
`,
		},
		{
			name: "mixed imports in block",
			input: `package main
import (
    "fmt"
    m "math"
    . "net/http"
)`,
			expected: `package main

import (
	"fmt"
	m "math"
	. "net/http"
)
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input)
			if tt.wantErr {
				// It might fail at parsing or transformation
				if err != nil {
					return
				}
				// If it didn't fail, check if the output is NOT what we want
				if !strings.Contains(got, tt.expected) {
					return
				}
				t.Errorf("Expected error or different output for %s, but got: %s", tt.name, got)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, strings.TrimSpace(tt.expected), strings.TrimSpace(got))
			}
		})
	}
}
