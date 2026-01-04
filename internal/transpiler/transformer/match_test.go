package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatch(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer()
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

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
	if std.UnapplyCheck(x, 1) {
		return "one"
	} else if std.UnapplyCheck(x, 2) {
		return "two"
	} else {
		return "many"
	}
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
	if std.UnapplyCheck(x, 10) {
		return x
	} else {
		return 0
	}
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
	if std.UnapplyCheck(x, "unapplied") {
		return "success"
	} else {
		return "fail"
	}
}(x))
`,
		},
		{
			name: "Match expression with var binding",
			input: `package main

val res = x match {
	case y => y
	case _ => "fail"
}`,
			expected: `package main

import "martianoff/gala/std"

var res = std.NewImmutable(func(x any) any {
	{
		y := x
		if true {
			return y
		} else {
			return "fail"
		}
	}
}(x))
`,
		},
		{
			name: "Match expression with extraction and var binding",
			input: `package main

val x = Some(1)
val res = x match {
	case Some(y) => y
	case _ => "fail"
}`,
			expected: `package main

import "martianoff/gala/std"

var x = std.NewImmutable(std.Some(1))
var res = std.NewImmutable(func(x any) any {
	{
		_tmp_1, _tmp_2 := std.UnapplySome(x)
		y := std.GetSafe(_tmp_1, 0)
		if _tmp_2 && true {
			return y
		} else {
			return "fail"
		}
	}
}(x.Get()))
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
