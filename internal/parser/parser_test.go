package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGalaParser(t *testing.T) {
	p := NewAntlrGalaParser()

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "Basic val declaration",
			input:   `val x = 10`,
			wantErr: false,
		},
		{
			name:    "Basic var declaration",
			input:   `var y = 20`,
			wantErr: false,
		},
		{
			name:    "Function declaration Go-style",
			input:   `func add(a int, b int) int { return a + b }`,
			wantErr: false,
		},
		{
			name:    "Function declaration Scala-style",
			input:   `func square(x int) int = x * x`,
			wantErr: false,
		},
		{
			name:    "Lambda expression",
			input:   `val f = (x int) => x * x`,
			wantErr: false,
		},
		{
			name: "Match expression",
			input: `val res = x match {
				case 1 => "one"
				case 2 => "two"
				case _ => "many"
			}`,
			wantErr: false,
		},
		{
			name:    "If expression",
			input:   `val status = if (score > 50) "pass" else "fail"`,
			wantErr: false,
		},
		{
			name:    "Generics support",
			input:   `func identity[T any](x T) T { return x }`,
			wantErr: false,
		},
		{
			name: "Struct type",
			input: `type Person struct {
				Name string
				Age int
			}`,
			wantErr: false,
		},
		{
			name:    "Everything is a function (expression statement)",
			input:   `1 + 1`,
			wantErr: false,
		},
		{
			name: "Struct with mutable and immutable fields",
			input: `type Config struct {
				val ID string
				var Count int
				Timeout int
			}`,
			wantErr: false,
		},
		{
			name:    "Function with mutable and immutable parameters",
			input:   `func process(val data string, var count int, mode int) = "ok"`,
			wantErr: false,
		},
		{
			name:    "Lambda with mutable and immutable parameters",
			input:   `val f = (val a int, var b int) => a + b`,
			wantErr: false,
		},
		{
			name: "All fields immutable",
			input: `type Point struct {
				val X int
				val Y int
			}`,
			wantErr: false,
		},
		{
			name: "All fields mutable",
			input: `type Box struct {
				var Width int
				var Height int
			}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := p.Parse(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
