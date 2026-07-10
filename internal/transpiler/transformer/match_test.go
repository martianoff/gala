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
var res = std.NewImmutable(func(obj int) string {
	if obj == 1 {
		return "one"
	} else if obj == 2 {
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
var res = std.NewImmutable(func(obj int) int {
	if obj == 10 {
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
var res = std.NewImmutable(func(obj string) string {
	if obj == "hello" {
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
var res = std.NewImmutable(func(obj int) int {
	{
		y := obj
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

var x std.Immutable[std.Option[int]] = std.NewImmutable[std.Option[int]](std.Some[int]{}.Apply(1))
var res = std.NewImmutable(func(obj std.Option[int]) int {
	{
		_tmp_1 := std.Some[int]{}.Unapply(obj)
		_tmp_2 := _tmp_1.IsDefined()
		var _tmp_3 int
		if _tmp_2 {
			_tmp_3 = _tmp_1.Get()
		}
		_ = _tmp_3
		y := _tmp_3
		if _tmp_2 {
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

var x std.Immutable[std.Option[any]] = std.NewImmutable[std.Option[any]](std.Some[any]{}.Apply("test"))
var res = std.NewImmutable(func(obj std.Option[any]) string {
	{
		_tmp_1 := std.Some[any]{}.Unapply(obj)
		_tmp_2 := _tmp_1.IsDefined()
		var _tmp_3 any
		if _tmp_2 {
			_tmp_3 = _tmp_1.Get()
		}
		_ = _tmp_3
		s, _tmp_4 := std.As[string](_tmp_3)
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
		{
			name: "Sealed exhaustive match without default generates panic",
			input: `package main

sealed type Light {
	case On()
	case Off()
}

func describe(l Light) string = l match {
	case On() => "on"
	case Off() => "off"
}`,
			expected: `package main

import "martianoff/gala/std"

type Light struct {
	_variant uint8
}

const (
	_Light_On uint8 = iota
	_Light_Off
)

type On struct {
}

func (_ On) Apply() Light {
	return Light{_variant: _Light_On}
}
func (_ On) Unapply(v Light) bool {
	return v._variant == _Light_On
}

type Off struct {
}

func (_ Off) Apply() Light {
	return Light{_variant: _Light_Off}
}
func (_ Off) Unapply(v Light) bool {
	return v._variant == _Light_Off
}
func (s Light) isOn() bool {
	return s._variant == _Light_On
}
func (s Light) isOff() bool {
	return s._variant == _Light_Off
}
func (s Light) Copy() Light {
	return Light{_variant: std.Copy(s._variant)}
}
func (s Light) Equal(other Light) bool {
	return std.Equal(s._variant, other._variant)
}
func (s Light) String() string {
	switch s._variant {
	case _Light_On:
		return "On()"
	case _Light_Off:
		return "Off()"
	default:
		return "Light(<unknown>)"
	}
}
func describe(l Light) string {
	return func(obj Light) string {
		{
			_tmp_1 := On{}.Unapply(obj)
			if _tmp_1 {
				return "on"
			} else {
				_tmp_2 := Off{}.Unapply(obj)
				if _tmp_2 {
					return "off"
				} else {
					panic("unreachable")
				}
			}
		}
	}(l)
}`,
		},
		{
			name: "Sealed exhaustive match with unreachable default is allowed",
			input: `package main

sealed type Light {
	case On()
	case Off()
}

func describe(l Light) string = l match {
	case On() => "on"
	case Off() => "off"
	case _ => "unknown"
}`,
			expected: `package main

import "martianoff/gala/std"

type Light struct {
	_variant uint8
}

const (
	_Light_On uint8 = iota
	_Light_Off
)

type On struct {
}

func (_ On) Apply() Light {
	return Light{_variant: _Light_On}
}
func (_ On) Unapply(v Light) bool {
	return v._variant == _Light_On
}

type Off struct {
}

func (_ Off) Apply() Light {
	return Light{_variant: _Light_Off}
}
func (_ Off) Unapply(v Light) bool {
	return v._variant == _Light_Off
}
func (s Light) isOn() bool {
	return s._variant == _Light_On
}
func (s Light) isOff() bool {
	return s._variant == _Light_Off
}
func (s Light) Copy() Light {
	return Light{_variant: std.Copy(s._variant)}
}
func (s Light) Equal(other Light) bool {
	return std.Equal(s._variant, other._variant)
}
func (s Light) String() string {
	switch s._variant {
	case _Light_On:
		return "On()"
	case _Light_Off:
		return "Off()"
	default:
		return "Light(<unknown>)"
	}
}
func describe(l Light) string {
	return func(obj Light) string {
		{
			_tmp_1 := On{}.Unapply(obj)
			if _tmp_1 {
				return "on"
			} else {
				_tmp_2 := Off{}.Unapply(obj)
				if _tmp_2 {
					return "off"
				} else {
					return "unknown"
				}
			}
		}
	}(l)
}`,
		},
		{
			name: "Sealed non-exhaustive match without default is error",
			input: `package main

sealed type Light {
	case On()
	case Off()
}

func describe(l Light) string = l match {
	case On() => "on"
}`,
			wantErr: true,
		},
		{
			name: "Bool exhaustive match (true+false) generates panic unreachable",
			input: `package main

func describe(b bool) string = b match {
	case true => "yes"
	case false => "no"
}`,
			expected: `package main

func describe(b bool) string {
	return func(obj bool) string {
		if obj == true {
			return "yes"
		} else if obj == false {
			return "no"
		} else {
			panic("unreachable")
		}
	}(b)
}`,
		},
		{
			name: "Bool missing false is error",
			input: `package main

func describe(b bool) string = b match {
	case true => "yes"
}`,
			wantErr: true,
		},
		{
			name: "Bool missing true is error",
			input: `package main

func describe(b bool) string = b match {
	case false => "no"
}`,
			wantErr: true,
		},
		{
			name: "Unused match variable is a compiler error",
			input: `package main

val x = 42
val res = x match {
	case y => 0
	case _ => 0
}`,
			wantErr: true,
		},
		{
			name: "Unused match variable with guard referencing it is allowed",
			input: `package main

val x = 42
val res = x match {
	case y if y > 10 => 1
	case _ => 0
}`,
			expected: `package main

import "martianoff/gala/std"

var x = std.NewImmutable(42)
var res = std.NewImmutable(func(obj int) int {
	{
		y := obj
		if true && y > 10 {
			return 1
		} else {
			return 0
		}
	}
}(x.Get()))
`,
		},
		{
			name: "Bool with redundant default is allowed",
			input: `package main

func describe(b bool) string = b match {
	case true => "yes"
	case false => "no"
	case _ => "unknown"
}`,
			expected: `package main

func describe(b bool) string {
	return func(obj bool) string {
		if obj == true {
			return "yes"
		} else if obj == false {
			return "no"
		} else {
			return "unknown"
		}
	}(b)
}`,
		},
		{
			name: "Match on method return value returning Option",
			input: `package main

struct Config(val token string)

func (c Config) GetToken() Option[string] {
    if c.token != "" {
        return Some(c.token)
    }
    return None[string]()
}

func main() {
    val config = Config("secret")
    val result = config.GetToken() match {
        case Some(v) => v
        case None() => "empty"
    }
}`,
			wantErr: false,
		},
		{
			// Regression: when a struct field shares its name with the generic
			// type it holds (idiomatic Go: http.Response.Header is http.Header),
			// the transformer's struct-shorthand pass used to overwrite the
			// analyzer-supplied field type with a doubled generic by routing
			// through a value-scope entry that the parameter loop had just
			// added. The match-as-IIFE on `m.Cell.Get()` then collapsed to
			// `Option[any]`. Verify the IIFE preserves the concrete generic.
			name: "Match on chained method call where field name shadows generic type",
			input: `package main

struct Cell[T any](Value T)

func (c Cell[T]) Get() Option[T] {
    return Some[T](c.Value)
}

struct Holder(Cell Cell[int])

func unwrap(h Holder) int {
    return h.Cell.Get() match {
        case Some(v) => v
        case None()  => 0
    }
}`,
			expected: `package main

import "martianoff/gala/std"

type Cell[T any] struct {
	Value std.Immutable[T]
}

func (s Cell[T]) Copy() Cell[T] {
	return Cell[T]{Value: std.Copy(s.Value)}
}
func (s Cell[T]) Equal(other Cell[T]) bool {
	return std.Equal(s.Value, other.Value)
}

type CellInstance interface {
	IsCell() bool
}

func (_ Cell[T]) IsCell() bool {
	return true
}
func (s Cell[T]) Unapply(v any) (std.Immutable[T], bool) {
	if p, ok := v.(Cell[T]); ok {
		return p.Value, true
	}
	if p, ok := v.(*Cell[T]); ok && p != nil {
		return p.Value, true
	}
	return *new(std.Immutable[T]), false
}
func (c Cell[T]) Get() std.Option[T] {
	return std.Some[T]{}.Apply(c.Value.Get())
}

type Holder struct {
	Cell std.Immutable[Cell[int]]
}

func (s Holder) Copy() Holder {
	return Holder{Cell: std.Copy(s.Cell)}
}
func (s Holder) Equal(other Holder) bool {
	return std.Equal(s.Cell, other.Cell)
}
func (s Holder) Unapply(v any) (std.Immutable[Cell[int]], bool) {
	if p, ok := v.(Holder); ok {
		return p.Cell, true
	}
	if p, ok := v.(*Holder); ok && p != nil {
		return p.Cell, true
	}
	return *new(std.Immutable[Cell[int]]), false
}
func unwrap(h Holder) int {
	return func(obj std.Option[int]) int {
		{
			_tmp_1 := std.Some[int]{}.Unapply(obj)
			_tmp_2 := _tmp_1.IsDefined()
			var _tmp_3 int
			if _tmp_2 {
				_tmp_3 = _tmp_1.Get()
			}
			_ = _tmp_3
			v := _tmp_3
			if _tmp_2 {
				return v
			} else {
				_tmp_4 := std.None[int]{}.Unapply(obj)
				if _tmp_4 {
					return 0
				} else {
					panic("unreachable")
				}
			}
		}
	}(h.Cell.Get().Get())
}`,
		},
		{
			// Regression: panic(...) as a value-producing match arm used to
			// emit invalid `return panic(...)` Go ("no value used as value").
			// The arm must be lowered to a bare panic statement so the IIFE
			// type-checks via Go's terminating-statement analysis.
			name: "Panic as match arm emits bare statement (not return)",
			input: `package main

func resolve(opt Option[int]) int {
	return opt match {
		case Some(n) => n
		case None()  => Panic("missing")
	}
}`,
			expected: `package main

import "martianoff/gala/std"

func resolve(opt std.Option[int]) int {
	return func(obj std.Option[int]) int {
		{
			_tmp_1 := std.Some[int]{}.Unapply(obj)
			_tmp_2 := _tmp_1.IsDefined()
			var _tmp_3 int
			if _tmp_2 {
				_tmp_3 = _tmp_1.Get()
			}
			_ = _tmp_3
			n := _tmp_3
			if _tmp_2 {
				return n
			} else {
				_tmp_4 := std.None[int]{}.Unapply(obj)
				if _tmp_4 {
					panic("missing")
				} else {
					panic("unreachable")
				}
			}
		}
	}(opt)
}`,
		},
		{
			// Regression: panic on the default `case _` arm. Exercises the
			// distinct default-case lowering path in transformMatchClauses.
			name: "Panic as default match arm emits bare statement",
			input: `package main

func describe(code int) string {
	return code match {
		case 0 => "ok"
		case 1 => "warn"
		case _ => Panic("invalid")
	}
}`,
			expected: `package main

func describe(code int) string {
	return func(obj int) string {
		if obj == 0 {
			return "ok"
		} else if obj == 1 {
			return "warn"
		} else {
			panic("invalid")
		}
	}(code)
}`,
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
			if tt.expected != "" {
				assert.Equal(t, strings.TrimSpace(tt.expected), strings.TrimSpace(stripGeneratedHeader(got)))
			} else {
				assert.NotEmpty(t, got)
			}
		})
	}
}
