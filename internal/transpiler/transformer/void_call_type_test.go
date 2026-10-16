package transformer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const voidCallDecls = `package main

func log(s string) {
    Println(s)
}

`

// TestVoidCallIsTypedVoid pins that a call of a GALA function declared with no
// result, and a closure with no result called on the spot, are void: a lambda
// whose body is one returns nothing, and a match or if-expression over them
// runs as a statement. Every output also goes through the package's Go
// type-check oracle.
func TestVoidCallIsTypedVoid(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	tests := []struct {
		name, input string
	}{
		{"expression lambda calling a void function", "func f() {\n    val g = (x int) => log(s\"v=$x\")\n    g(1)\n}"},
		{"expression lambda calling a void closure", "func f() {\n    val g = (x int) => (() => log(\"a\"))()\n    g(1)\n}"},
		{"if-expression of void calls in a lambda tail", "func f(ok bool) {\n    val g = () => if (ok) log(\"a\") else log(\"b\")\n    g()\n}"},
		{"if-expression of an empty branch and a void call", "func f(ok bool) {\n    val g = () => if (ok) log(\"a\") else {}\n    g()\n}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(voidCallDecls+tt.input+"\n", "")
			require.NoError(t, err)
			_, body, found := strings.Cut(got, "\nfunc f(")
			require.True(t, found)
			assert.NotContains(t, body, "void")
			assert.NotContains(t, body, "any")
		})
	}
}
