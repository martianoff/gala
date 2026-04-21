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
//
// Stable error codes (A8): the optional Code field tags the error with a short
// identifier (e.g., "GALA-E0042") that tools, tests, and documentation can
// link against. Codes live in the ErrorCode constants below; new codes should
// be appended (never renumbered) and documented under docs/errors/<code>.md
// once that directory is added. Errors without a Code still render cleanly.
type SemanticError struct {
	BaseError
	Line     int
	Column   int
	FilePath string
	Code     ErrorCode // optional stable error code; empty = uncoded
	Hint     string    // optional remediation hint shown after the message
}

func (e *SemanticError) Error() string {
	prefix := string(e.ErrType)
	if e.Code != "" {
		prefix = fmt.Sprintf("%s %s", e.ErrType, e.Code)
	}
	msg := e.Msg
	if e.Hint != "" {
		msg = msg + " (hint: " + e.Hint + ")"
	}
	if e.Line > 0 {
		if e.FilePath != "" {
			return fmt.Sprintf("[%s] %s:%d:%d %s", prefix, e.FilePath, e.Line, e.Column, msg)
		}
		return fmt.Sprintf("[%s] line %d:%d %s", prefix, e.Line, e.Column, msg)
	}
	return fmt.Sprintf("[%s] %s", prefix, msg)
}

// ErrorCode is a short stable identifier for a class of semantic errors.
// Codes are opaque tokens rendered verbatim in error output.
type ErrorCode string

// Stable error codes. New codes append; do not renumber existing codes.
// Keep this list alphabetical by code for easy maintenance.
const (
	// E0001: recursive Immutable[T] wrap (e.g., Immutable[Immutable[int]]).
	CodeRecursiveImmutable ErrorCode = "GALA-E0001"

	// E0002: non-exhaustive match on a sealed type.
	CodeNonExhaustiveMatch ErrorCode = "GALA-E0002"

	// E0003: match expression missing a default case on a non-sealed subject.
	CodeMissingDefault ErrorCode = "GALA-E0003"

	// E0004: sealed variant pattern binds wrong number of fields.
	CodeVariantArityMismatch ErrorCode = "GALA-E0004"

	// E0005: extractor referenced in a pattern has no Unapply method.
	CodeMissingUnapply ErrorCode = "GALA-E0005"

	// E0006: multiple default cases in a single match expression.
	CodeMultipleDefaults ErrorCode = "GALA-E0006"

	// E0007: slice literal in GALA source; slices are not a first-class literal.
	CodeSliceLiteralNotSupported ErrorCode = "GALA-E0007"

	// E0008: map literal in GALA source; maps are not a first-class literal.
	CodeMapLiteralNotSupported ErrorCode = "GALA-E0008"

	// E0009: pattern AST node the transformer does not recognize (defensive).
	CodeUnknownPatternType ErrorCode = "GALA-E0009"

	// E0010: sibling .gala files in the same directory declare different packages.
	CodeDuplicatePackageName ErrorCode = "GALA-E0010"

	// E0011: type with the same name declared more than once in a package.
	CodeTypeRedefinition ErrorCode = "GALA-E0011"

	// E0012: method with the same name declared more than once on a type.
	CodeMethodRedefinition ErrorCode = "GALA-E0012"

	// E0013: non-defaulted parameter follows a parameter with a default value.
	CodeParamMissingDefaultAfterDefault ErrorCode = "GALA-E0013"

	// E0014: default value expression type does not match the parameter type.
	CodeParamDefaultTypeMismatch ErrorCode = "GALA-E0014"

	// E0015: bare `return` inside a match branch that is used as a value.
	// The match is wrapped in an IIFE whose return type is non-void, so a
	// bare `return` would produce invalid Go. Users intending to early-exit
	// the enclosing function must restructure the code (e.g., `.Recover`, or
	// pattern-match on the value first and then return outside the match).
	CodeBareReturnInValueMatch ErrorCode = "GALA-E0015"
)

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

// NewSemanticErrorAt creates a SemanticError with line and column position.
func NewSemanticErrorAt(line, column int, msg string) *SemanticError {
	return &SemanticError{
		BaseError: BaseError{
			Msg:     msg,
			ErrType: TypeSemantic,
		},
		Line:   line,
		Column: column,
	}
}

// NewSemanticErrorInFile creates a SemanticError with file path, line, and column position.
func NewSemanticErrorInFile(filePath string, line, column int, msg string) *SemanticError {
	return &SemanticError{
		BaseError: BaseError{
			Msg:     msg,
			ErrType: TypeSemantic,
		},
		Line:     line,
		Column:   column,
		FilePath: filePath,
	}
}

// NewCodedSemanticError creates a SemanticError tagged with a stable error code
// and an optional remediation hint. The code is shown verbatim in the error
// output; the hint is appended in parentheses after the message.
func NewCodedSemanticError(code ErrorCode, line, column int, msg, hint string) *SemanticError {
	return &SemanticError{
		BaseError: BaseError{
			Msg:     msg,
			ErrType: TypeSemantic,
		},
		Line:   line,
		Column: column,
		Code:   code,
		Hint:   hint,
	}
}
