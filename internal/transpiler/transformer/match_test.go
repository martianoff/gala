package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatch(t *testing.T) {
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
			name: "Match expression",
			input: `package main

val res = x match {
	case 1 => "one"
	case 2 => "two"
	case _ => "many"
}`,
			expected: `package main

import "martianoff/gala/std"

var res = std.NewImmutable(func(x any) any {
	switch {
	case std.UnapplyCheck(x, 1):
		return "one"
	case std.UnapplyCheck(x, 2):
		return "two"
	}
	return "many"
}(x))
`,
		},
		{
			name: "Match expression with shadowing",
			input: `package main

val x = 10
val res = x match {
	case 10 => x
	case _ => 0
}`,
			expected: `package main

import "martianoff/gala/std"

var x = std.NewImmutable(10)
var res = std.NewImmutable(func(x any) any {
	switch {
	case std.UnapplyCheck(x, 10):
		return x
	}
	return 0
}(x.Get()))
`,
		},
		{
			name: "Match expression with Unapply",
			input: `package main

val res = x match {
	case "unapplied" => "success"
	case _ => "fail"
}`,
			expected: `package main

import "martianoff/gala/std"

var res = std.NewImmutable(func(x any) any {
	switch {
	case std.UnapplyCheck(x, "unapplied"):
		return "success"
	}
	return "fail"
}(x))
`,
		},
		{
			name: "Missing default case",
			input: `val res = x match {
				case 1 => "one"
			}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, strings.TrimSpace(tt.expected), strings.TrimSpace(got))
		})
	}
}
