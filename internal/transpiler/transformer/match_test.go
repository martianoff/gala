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
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
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
			name: "Match expression with typed variable",
			input: `package main

val x int = 5
val res = x match {
	case 1 => "one"
	case 2 => "two"
	case _ => "many"
}`,
			expected: `package main

import "martianoff/gala/std"

var x std.Immutable[int] = std.NewImmutable[int](5)
var res = std.NewImmutable(func(x int) string {
	if std.UnapplyCheck(x, 1) {
		return "one"
	} else if std.UnapplyCheck(x, 2) {
		return "two"
	} else {
		return "many"
	}
}(x.Get()))
`,
		},
		{
			name: "Match expression with inferred int result type",
			input: `package main

val x = 10
val res = x match {
	case 10 => 1
	case _ => 0
}`,
			expected: `package main

import "martianoff/gala/std"

var x = std.NewImmutable(10)
var res = std.NewImmutable(func(x int) int {
	if std.UnapplyCheck(x, 10) {
		return 1
	} else {
		return 0
	}
}(x.Get()))
`,
		},
		{
			name: "Match expression with string literal",
			input: `package main

val x = "hello"
val res = x match {
	case "hello" => "world"
	case _ => "fail"
}`,
			expected: `package main

import "martianoff/gala/std"

var x = std.NewImmutable("hello")
var res = std.NewImmutable(func(x string) string {
	if std.UnapplyCheck(x, "hello") {
		return "world"
	} else {
		return "fail"
	}
}(x.Get()))
`,
		},
		{
			name: "Match expression with var binding returning int",
			input: `package main

val x = 42
val res = x match {
	case y => y + 1
	case _ => 0
}`,
			expected: `package main

import "martianoff/gala/std"

var x = std.NewImmutable(42)
var res = std.NewImmutable(func(x int) int {
	{
		y := x
		if true {
			return y + 1
		} else {
			return 0
		}
	}
}(x.Get()))
`,
		},
		{
			name: "Match expression with extraction and explicitly typed Option",
			input: `package main

val x Option[int] = Some(1)
val res = x match {
	case Some(y) => y
	case _ => 0
}`,
			expected: `package main

import "martianoff/gala/std"

var x std.Immutable[std.Option[int]] = std.NewImmutable[std.Option[int]](std.Some_Apply(std.Some{}, 1))
var res = std.NewImmutable(func(x std.Option[int]) int {
	{
		_tmp_1, _tmp_2 := std.UnapplyFull(x, std.Some{})
		y, _tmp_3 := std.As[int](std.GetSafe(_tmp_1, 0))
		if _tmp_2 && _tmp_3 {
			return y
		} else {
			return 0
		}
	}
}(x.Get()))
`,
		},
		{
			name: "Nested type-based pattern match with Option returning string",
			input: `package main

val x Option[any] = Some("test")
val res = x match {
	case Some(s: string) => s
	case _ => "unknown"
}`,
			expected: `package main

import "martianoff/gala/std"

var x std.Immutable[std.Option[any]] = std.NewImmutable[std.Option[any]](std.Some_Apply(std.Some{}, "test"))
var res = std.NewImmutable(func(x std.Option[any]) string {
	{
		_tmp_1, _tmp_2 := std.UnapplyFull(x, std.Some{})
		s, _tmp_4 := std.As[string](std.GetSafe(_tmp_1, 0))
		if _tmp_2 && _tmp_4 {
			return s
		} else {
			return "unknown"
		}
	}
}(x.Get()))
`,
		},
		{
			name: "Missing default case",
			input: `package main

val x = 1
val res = x match {
	case 1 => "one"
}`,
			wantErr: true,
		},
		{
			name: "Cannot infer type for untyped variable",
			input: `package main

val res = unknownVar match {
	case 1 => "one"
	case _ => "other"
}`,
			wantErr: true,
		},
		{
			name: "Type mismatch in match branches",
			input: `package main

val x = 1
val res = x match {
	case 1 => "one"
	case _ => 0
}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, strings.TrimSpace(tt.expected), strings.TrimSpace(got))
		})
	}
}
