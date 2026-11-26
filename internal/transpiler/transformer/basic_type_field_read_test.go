package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFieldReadOnBasicTypeIsRejected pins #1396: a member read on a parameter
// whose type is surely a predeclared basic type (`int`, `string`) is
// GALA-E0044, the way the same read on a GALA struct without the field is. It
// used to pass GALA's checks and fail in go build ("acc.Lo undefined (type
// int has no field or method Lo)").
func TestFieldReadOnBasicTypeIsRejected(t *testing.T) {
	trans := newUnknownMethodTranspiler()
	cases := []struct {
		name, input, want string
	}{
		{"lambda accumulator", `package main

import . "martianoff/gala/collection_immutable"

struct R(Lo int)

func main() {
    val rs = ArrayOf(R(1), R(2), R(3))
    Println(rs.FoldLeft(0, (acc, r) => acc.Lo + r.Lo))
}`, "int has no field or method Lo"},
		{"string parameter", `package main

func f(s string) int = s.Length

func main() {
    Println(f("abc"))
}`, "string has no field or method Length"},
		{"annotated lambda parameter", `package main

func main() {
    val f = (x float64) => x.Value
    Println(f(1.5))
}`, "float64 has no field or method Value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.input, "basic_field_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			var se *galaerr.SemanticError
			require.ErrorAs(t, err, &se)
			assert.Equal(t, galaerr.CodeUnknownMethod, se.Code)
		})
	}
}

// TestMembersOfGoNamedTypesStayAccepted pins what #1396 must leave alone: a
// value whose GALA type is a basic type only by inference from Go values
// (`math.MaxInt8 * time.Second` is typed `int`, Go has a time.Duration), and
// a Go type defined over a basic type, keep their member reads; they compile.
func TestMembersOfGoNamedTypesStayAccepted(t *testing.T) {
	trans := newUnknownMethodTranspiler()
	cases := []struct {
		name, input, want string
	}{
		{"untyped Go constant times a Duration", `package main

import "time"
import "math"

func main() {
    val d = math.MaxInt8 * time.Second
    val f = d.Minutes
    Println(f())
}`, "d.Minutes"},
		{"Go status constant times a Duration", `package main

import "time"
import "net/http"

func main() {
    val e = http.StatusOK * time.Millisecond
    val g = e.Seconds
    Println(g())
}`, "e.Seconds"},
		{"a Duration and a sugar call", `package main

import "time"

func main() {
    val d = 90 * time.Minute
    val hours = d.Hours
    Println(hours(), "abc".Size())
}`, "d.Hours"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "basic_field_test.gala")
			require.NoError(t, err)
			assert.Contains(t, out, tc.want)
		})
	}
}
