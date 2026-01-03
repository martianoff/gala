package galaerr

import (
	"fmt"
	"strings"
)

// ErrorType defines the category of the error.
type ErrorType string

const (
	TypeSyntax   ErrorType = "SyntaxError"
	TypeSemantic ErrorType = "SemanticError"
)

// GalaError is the interface for all GALA-related errors.
type GalaError interface {
	error
	Type() ErrorType
}

// BaseError provides common fields for GALA errors.
type BaseError struct {
	Msg     string
	ErrType ErrorType
}

func (e *BaseError) Error() string {
	return fmt.Sprintf("[%s] %s", e.ErrType, e.Msg)
}

func (e *BaseError) Type() ErrorType {
	return e.ErrType
}

// SyntaxError represents an error during the parsing phase.
type SyntaxError struct {
	BaseError
	Line   int
	Column int
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("[%s] line %d:%d %s", e.ErrType, e.Line, e.Column, e.Msg)
}

// SemanticError represents an error during the transformation/transpilation phase.
type SemanticError struct {
	BaseError
}

// MultiError collects multiple GALA errors.
type MultiError struct {
	Errors []error
}

func (m *MultiError) Error() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d error(s) occurred:\n", len(m.Errors)))
	for _, err := range m.Errors {
		sb.WriteString(fmt.Sprintf("- %v\n", err))
	}
	return sb.String()
}

func (m *MultiError) Type() ErrorType {
	if len(m.Errors) > 0 {
		if ge, ok := m.Errors[0].(GalaError); ok {
			return ge.Type()
		}
	}
	return "MultiError"
}

// NewSyntaxError creates a new SyntaxError.
func NewSyntaxError(line, column int, msg string) *SyntaxError {
	return &SyntaxError{
		BaseError: BaseError{
			Msg:     msg,
			ErrType: TypeSyntax,
		},
		Line:   line,
		Column: column,
	}
}

// NewSemanticError creates a new SemanticError.
func NewSemanticError(msg string) *SemanticError {
	return &SemanticError{
		BaseError: BaseError{
			Msg:     msg,
			ErrType: TypeSemantic,
		},
	}
}
