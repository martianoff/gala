package galaerr_test

import (
	"martianoff/gala/galaerr"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSyntaxError(t *testing.T) {
	err := galaerr.NewSyntaxError(10, 5, "unexpected token")
	assert.Equal(t, galaerr.TypeSyntax, err.Type())
	assert.Equal(t, 10, err.Line)
	assert.Equal(t, 5, err.Column)
	assert.Contains(t, err.Error(), "[SyntaxError] line 10:5 unexpected token")
}

func TestSemanticError(t *testing.T) {
	err := galaerr.NewSemanticError("undefined variable x")
	assert.Equal(t, galaerr.TypeSemantic, err.Type())
	assert.Contains(t, err.Error(), "[SemanticError] undefined variable x")
}

func TestSemanticErrorAt(t *testing.T) {
	err := galaerr.NewSemanticErrorAt(10, 5, "cannot assign to immutable variable x")
	assert.Equal(t, galaerr.TypeSemantic, err.Type())
	assert.Equal(t, 10, err.Line)
	assert.Equal(t, 5, err.Column)
	assert.Equal(t, "[SemanticError] line 10:5 cannot assign to immutable variable x", err.Error())
}

func TestSemanticErrorInFile(t *testing.T) {
	err := galaerr.NewSemanticErrorInFile("main.gala", 10, 5, "undefined variable x")
	assert.Equal(t, galaerr.TypeSemantic, err.Type())
	assert.Equal(t, 10, err.Line)
	assert.Equal(t, 5, err.Column)
	assert.Equal(t, "main.gala", err.FilePath)
	assert.Equal(t, "[SemanticError] main.gala:10:5 undefined variable x", err.Error())
}

func TestSemanticErrorNoPosition(t *testing.T) {
	err := galaerr.NewSemanticError("undefined variable x")
	assert.Equal(t, galaerr.TypeSemantic, err.Type())
	assert.Equal(t, 0, err.Line)
	assert.Equal(t, "[SemanticError] undefined variable x", err.Error())
}

func TestMultiError(t *testing.T) {
	e1 := galaerr.NewSyntaxError(1, 1, "error 1")
	e2 := galaerr.NewSyntaxError(2, 2, "error 2")
	multi := &galaerr.MultiError{Errors: []error{e1, e2}}

	assert.Equal(t, galaerr.TypeSyntax, multi.Type())
	errMsg := multi.Error()
	assert.Contains(t, errMsg, "2 error(s) occurred:")
	assert.Contains(t, errMsg, "- [SyntaxError] line 1:1 error 1")
	assert.Contains(t, errMsg, "- [SyntaxError] line 2:2 error 2")
}

func TestMultiErrorMixed(t *testing.T) {
	e1 := galaerr.NewSemanticError("semantic error")
	e2 := galaerr.NewSyntaxError(1, 1, "syntax error")
	multi := &galaerr.MultiError{Errors: []error{e1, e2}}

	assert.Equal(t, galaerr.TypeSemantic, multi.Type())
}

func TestMultiErrorEmpty(t *testing.T) {
	multi := &galaerr.MultiError{Errors: []error{}}
	assert.Equal(t, galaerr.ErrorType("MultiError"), multi.Type())
	assert.True(t, strings.HasPrefix(multi.Error(), "0 error(s) occurred:"))
}
