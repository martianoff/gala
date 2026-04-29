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
			name: "Basic val declaration",
			input: `package main

val x = 10`,
			wantErr: false,
		},
		{
			name: "Basic var declaration",
			input: `package main

var y = 20`,
			wantErr: false,
		},
		{
			name: "Function declaration Go-style",
			input: `package main

func add(a int, b int) int { return a + b }`,
			wantErr: false,
		},
		{
			name: "Function declaration Scala-style",
			input: `package main

func square(x int) int = x * x`,
			wantErr: false,
		},
		{
			name: "Lambda expression",
			input: `package main

val f = (x int) => x * x`,
			wantErr: false,
		},
		{
			name: "Match expression",
			input: `package main

val res = x match {
	case 1 => "one"
	case 2 => "two"
	case _ => "many"
}`,
			wantErr: false,
		},
		{
			name: "If expression",
			input: `package main

val status = if (score > 50) "pass" else "fail"`,
			wantErr: false,
		},
		{
			name: "Generics support",
			input: `package main

func identity[T any](x T) T { return x }`,
			wantErr: false,
		},
		{
			name: "Struct type",
			input: `package main

type Person struct {
	Name string
	Age int
}`,
			wantErr: false,
		},
		{
			name: "Expression statement (not allowed at top level)",
			input: `package main

1 + 1`,
			wantErr: true,
		},
		{
			name: "Struct with mutable and immutable fields",
			input: `package main

type Config struct {
	val ID string
	var Count int
	Timeout int
}`,
			wantErr: false,
		},
		{
			name: "Function with mutable and immutable parameters",
			input: `package main

func process(val data string, var count int, mode int) = "ok"`,
			wantErr: false,
		},
		{
			name: "Lambda with mutable and immutable parameters",
			input: `package main

val f = (val a int, var b int) => a + b`,
			wantErr: false,
		},
		{
			name: "All fields immutable",
			input: `package main

type Point struct {
	val X int
	val Y int
}`,
			wantErr: false,
		},
		{
			name: "All fields mutable",
			input: `package main

type Box struct {
	var Width int
	var Height int
}`,
			wantErr: false,
		},
		{
			name:    "Missing package declaration",
			input:   `val x = 10`,
			wantErr: true,
		},
		{
			name: "Hex integer literal",
			input: `package main

val x = 0x1F`,
			wantErr: false,
		},
		{
			name: "Hex integer literal uppercase prefix",
			input: `package main

val x = 0X1f`,
			wantErr: false,
		},
		{
			name: "Hex integer literal in expression",
			input: `package main

val x = 0xFF + 0x01`,
			wantErr: false,
		},
		{
			name: "Single-line tuple as match arm body",
			input: `package main

func dispatch(m string, n int) Tuple[string, int] = n match {
	case 0 => (m, n)
	case _ => (m, n)
}`,
			wantErr: false,
		},
		{
			name: "Multi-line tuple as match arm body without trailing comma",
			input: `package main

func dispatch(m string, n int) Tuple[string, int] = n match {
	case 0 => (
		m,
		n
	)
	case _ => (m, n)
}`,
			wantErr: false,
		},
		{
			name: "Multi-line tuple as match arm body with trailing comma",
			input: `package main

func dispatch(m string, n int) Tuple[string, int] = n match {
	case 0 => (
		m,
		n,
	)
	case _ => (m, n)
}`,
			wantErr: false,
		},
		{
			name: "Tuple literal with trailing comma",
			input: `package main

val t = (1, 2,)`,
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
