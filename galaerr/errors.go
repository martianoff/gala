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

	// E0016: a struct field's name collides with a type name in the same
	// package. The transpiler's IIFE param-type generator can produce
	// invalid Go (e.g., duplicated type args like `Mode[T][T]`) when the
	// collided type appears in a `match` on the field; rejecting at the
	// analyzer keeps the failure local and points to a rename.
	CodeFieldNameCollidesWithType ErrorCode = "GALA-E0016"

	// E0017: an unhandled panic inside the transformer was caught and
	// surfaced as a coded error. Indicates a transpiler bug; the user is
	// asked to file an issue with the offending source snippet.
	CodeInternalTransformerPanic ErrorCode = "GALA-E0017"

	// E0018: a sealed-variant zero-arg constructor (e.g. `NoCmd()` from
	// `sealed type Cmd[T] = case NoCmd() | case RunCmd(arg: T)`) appears in
	// a position where the parent's type parameter cannot be inferred —
	// no enclosing match subject, val annotation, or function return type
	// pins it. Without that signal the transpiler can only emit an
	// untyped `Variant{}` literal that Go cannot instantiate.
	CodeSealedVariantUninferred ErrorCode = "GALA-E0018"

	// E0019: empty parenthesized expression `()` in expression position.
	// The grammar permits the form, but GALA has no unit/void value the
	// transpiler can emit for it. Common offender: a void match arm
	// written as `case _ => ()` — replace with a real statement (e.g.
	// `case _ => Println("…")`) or remove the arm if it cannot occur.
	CodeEmptyParenExpression ErrorCode = "GALA-E0019"

	// E0020: an `import` statement (or directory walk) references a GALA
	// package the analyzer cannot locate on its search paths. Distinct
	// from a malformed import (parser error) — the path is well-formed
	// but no matching directory exists in the workspace.
	CodePackageNotFound ErrorCode = "GALA-E0020"

	// E0021: type unification failure during inference. Two types that
	// must agree (call argument vs. parameter, both arms of an if, etc.)
	// could not be reconciled. Covers the inference engine's general
	// "cannot unify" diagnostics so users see one stable code regardless
	// of which specific unification site fired.
	CodeTypeMismatch ErrorCode = "GALA-E0021"

	// E0022: occurs check failure during type unification. A type
	// variable would have to expand into a type that contains itself
	// (e.g. `T = List[T]` with no constructor) — Hindley-Milner rejects
	// these to keep types finite.
	CodeOccursCheck ErrorCode = "GALA-E0022"

	// E0023: a variable referenced in an expression has no binding in
	// the type-inference environment. Usually means the name is mis-
	// spelled, the import is missing, or the variable is shadowed by a
	// pattern that did not actually fire.
	CodeUndefinedVariable ErrorCode = "GALA-E0023"

	// E0024: the inference engine encountered an expression node it does
	// not know how to handle. Indicates a transpiler bug rather than user
	// error; the user is asked to file an issue with the snippet.
	CodeInternalInferenceFailure ErrorCode = "GALA-E0024"

	// E0025: a type or function name was used unqualified but does not
	// resolve through the file's explicit imports, dot imports, std
	// prelude, or the current package's siblings. GALA mirrors Go's
	// rule: cross-package symbols require an explicit `import "..."`
	// (or `import . "..."`) in the file that uses them. Sibling files'
	// imports do not propagate.
	CodeUnresolvedCrossPackageSymbol ErrorCode = "GALA-E0025"

	// E0026: an unqualified sealed-variant constructor name matches a case
	// in two or more dot-imported sealed types. The transpiler cannot
	// pick one without silently shadowing the others, so it asks the user
	// to qualify the call site (`pkg.Variant(...)`) to disambiguate. Local
	// (same-package) declarations always shadow dot-imports, so this code
	// only fires when the conflict is across two imports.
	CodeAmbiguousSealedVariant ErrorCode = "GALA-E0026"

	// E0027: a top-level function with the same name is declared more than
	// once within a package. Mirrors Go's "redeclared in this block" rule.
	// The analyzer previously silently overwrote the earlier metadata entry
	// with the latter one, causing the second declaration to win and the
	// first to be lost; now both are surfaced as a hard error so the user
	// can rename or remove one.
	CodeFunctionRedeclared ErrorCode = "GALA-E0027"

	// E0028: a type alias (`type Foo = Bar`) with the same name is declared
	// more than once in the same file. Without this guard the second
	// alias silently replaced the first in the transformer's lookup table,
	// leading to surprising type resolution at unrelated call sites.
	CodeTypeAliasRedeclared ErrorCode = "GALA-E0028"

	// E0029: an interface lists two method specs with the same name. The
	// earlier method metadata was silently overwritten by the later one,
	// which could mask a parameter-list or return-type mismatch the user
	// intended to keep distinct (and would have hit at the Go compiler
	// otherwise, with a less useful message).
	CodeInterfaceMethodRedeclared ErrorCode = "GALA-E0029"

	// E0030: a struct declares two fields with the same name. The earlier
	// field type was silently overwritten by the later one, and the
	// `FieldNames` slice would contain the name twice — producing invalid
	// Go output. Rejecting at the analyzer keeps the error close to the
	// source.
	CodeStructFieldRedeclared ErrorCode = "GALA-E0030"

	// E0031: a sealed type lists two `case` variants with the same name.
	// The earlier variant's companion type, Apply/Unapply, and isXxx
	// methods would all be overwritten by the later variant, so only one
	// remained reachable — silent loss of code the user wrote.
	CodeSealedVariantCaseRedeclared ErrorCode = "GALA-E0031"

	// E0032: two dot-imported packages export the same identifier. Go
	// would reject the resulting generated code with "redeclared in this
	// block"; surfacing the collision at the GALA level lets the user
	// resolve it by qualifying or aliasing one import. Previously this
	// case was reported as an uncoded semantic error.
	CodeDotImportCollision ErrorCode = "GALA-E0032"

	// E0033: a lambda parameter has no type annotation and no expected type
	// can be inferred from the surrounding context (e.g. `val f = (x) => x + 1`
	// with no declared type, or a lambda element of an untyped collection
	// literal). Emitting `any` for the parameter would violate the
	// concrete-types invariant and produce Go that either fails to compile or
	// silently erases the type. The author must annotate the parameter
	// (`(x int) => ...`) or place the lambda in a typed context that supplies
	// the parameter types.
	CodeUntypedLambdaParam ErrorCode = "GALA-E0033"
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
