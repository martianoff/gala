package transformer

import (
	"fmt"
	"go/ast"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// A type name called as a constructor
//
// `Array(1, 2, 3)` is how Scala spells collection construction, and it is a
// common first attempt in GALA, where the same thing is `ArrayOf(1, 2, 3)`.
// The type name itself is not callable.
//
// Until this check existed the call was emitted verbatim and left to `go
// build`, which reported
//
//	cannot use generic type collection_immutable.Array[T any] without instantiation
//
// naming Go generics and an instantiation step that have no GALA surface. The
// line was right — line directives map it back to the .gala file — but the
// sentence described the generated code rather than the source.

// constructorCandidates returns the names GALA's own constructors take for a
// type, in the order they are offered as a suggestion. Derived from the
// stdlib's naming: `ArrayOf`, `EmptyArray`, `ArrayFromSlice`.
func constructorCandidates(typeName string) []string {
	return []string{typeName + "Of", "Empty" + typeName, typeName + "FromSlice"}
}

// checkTypeUsedAsConstructor rejects a bare call whose callee names a known
// GALA type rather than anything callable.
//
// It runs LAST in the call dispatcher, immediately before the verbatim-emit
// fallback, which is what keeps it safe: a positional struct constructor
// (section 10), a companion `Apply` (sections 10-12) and a sealed-variant
// constructor are all resolved earlier and return before reaching here. By the
// time this runs, every constructive reading has already declined.
func (t *galaASTTransformer) checkTypeUsedAsConstructor(fun ast.Expr, line, col int, exact bool) error {
	// The callee is a bare identifier as written, but a dot-imported name has
	// already been qualified into a SelectorExpr by the time it gets here, so
	// both spellings have to be decoded. Anything else (an index expression, a
	// call result) is not a type name and is left alone.
	typeName, qualifiedName := extractTypeNameFromExpr(fun)
	if typeName == "" {
		return nil
	}

	// The lookup key must respect the qualifier. A package-qualified callee is
	// looked up ONLY under its qualified name: falling back to the bare name
	// would strip the package and match an unrelated type that happens to share
	// it. That is not hypothetical — `time.Duration(d.nanos)`, an ordinary Go
	// type conversion in time_utils, matched time_utils' own `Duration` struct
	// and was reported as a constructor misuse.
	//
	// Restricting the qualified case to types GALA knows under their qualified
	// name is also what keeps Go type conversions out of this check entirely:
	// `time.Duration(x)`, `int64(n)` and friends resolve to no GALA type
	// metadata and are left alone.
	var meta *transpiler.TypeMetadata
	switch fun.(type) {
	case *ast.Ident:
		meta = t.getTypeMeta(typeName)
	case *ast.SelectorExpr:
		meta = t.getTypeMeta(qualifiedName)
	default:
		return nil
	}
	if meta == nil {
		return nil
	}

	msg := fmt.Sprintf("%s is a type, not a constructor", typeName)
	hint := t.constructorHintFor(typeName, meta.TypeParams)

	err := galaerr.NewCodedSemanticError(galaerr.CodeTypeUsedAsConstructor, line, col, msg, hint)
	if exact {
		// Span the callee name so the caret covers what has to be renamed. The
		// span is over the name the user wrote, which is the unqualified one
		// even when the emitted callee has acquired a package qualifier.
		err = err.WithSpan(col + len([]rune(typeName)))
	}
	return err
}

// constructorHintFor names the constructor to use instead. It prefers a real
// function that exists in scope — so the suggestion is something the compiler
// will actually accept — and falls back to describing the convention when the
// type has no discoverable constructor.
func (t *galaASTTransformer) constructorHintFor(typeName string, typeParams []string) string {
	for _, candidate := range constructorCandidates(typeName) {
		if t.isKnownFunctionName(candidate) {
			return fmt.Sprintf("use `%s(...)`; GALA constructs values through named "+
				"functions, so a type name is never callable", candidate)
		}
	}
	if len(typeParams) > 0 {
		return fmt.Sprintf("%s is generic and has no constructor function in scope; "+
			"look for a `%sOf` or `Empty%s` in the package that declares it",
			typeName, typeName, typeName)
	}
	return fmt.Sprintf("%s has no constructor function in scope; construct it by "+
		"naming its fields, or call the function that builds it", typeName)
}

// isKnownFunctionName reports whether name resolves to a GALA function the
// current file can call. Both the analyzed metadata and the primary RichAST are
// consulted for the same reason getTypeMeta consults both: metadata can be
// added late by sibling scanning.
func (t *galaASTTransformer) isKnownFunctionName(name string) bool {
	if t.richAST == nil {
		return false
	}
	if _, ok := t.richAST.Functions[name]; ok {
		return true
	}
	// Dot-imported and package-qualified entries are keyed by their qualified
	// name; match on the bare half so `collection_immutable.ArrayOf` answers
	// for a dot-imported `ArrayOf`.
	for qualified := range t.richAST.Functions {
		if stripPackagePrefix(qualified) == name {
			return true
		}
	}
	return false
}

// positionalCtorIsUnavailable reports whether constructing resolvedTypeName
// positionally from the CURRENT package would be setting fields that the
// declaring package keeps to itself.
//
// This is what makes the check above reachable for the case that motivated it.
// `Array(1, 2, 3)` is not an unresolved call: the positional struct
// constructor claimed it first and emitted
//
//	Array{root: 1, length: 2, depth: 3}
//
// mapping three arguments onto the first three of Array's four PRIVATE fields
// — `root` is a `*arrayNode[T]`, so the literal assigns an int to a pointer.
// The subset rule that allows a prefix of fields to be supplied is what let a
// Scala-shaped call through; requiring the fields to be nameable from the call
// site is what stops it. Go would reject the literal anyway, so nothing that
// used to compile stops compiling — the difference is that the rejection now
// happens in GALA, in GALA's vocabulary, with the real constructor named.
func (t *galaASTTransformer) positionalCtorIsUnavailable(typePackage string, fields []string, argCount int) bool {
	if typePackage == "" || typePackage == t.packageName {
		return false
	}
	n := argCount
	if n > len(fields) {
		n = len(fields)
	}
	for i := 0; i < n; i++ {
		if !ast.IsExported(fields[i]) {
			return true
		}
	}
	return false
}
