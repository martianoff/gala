package transformer

import (
	"fmt"

	"martianoff/gala/internal/transpiler"
)

// anonymousStructTypeHint is the GALA-E0090 hint: the ways to reach the
// value without writing its type.
const anonymousStructTypeHint = "give the type a name on the Go side; " +
	"for a signal channel use go_interop.Signal and go_interop.WaitClosed, IsClosed or WaitClosedTimeout; " +
	"for ctx.Done() use go_interop.WaitDone, IsDone or WaitDoneTimeout"

// anonymousStructTypeMessage is the GALA-E0090 message for typ, a type that
// is or holds an anonymous Go struct type (transpiler.StructType).
//
// Such a type comes only from Go: GALA has no syntax for it. It is typed, so
// a value of it can pass through, be received from or have its fields read.
// What GALA cannot do is write it, so wherever the generated Go would have to
// (a type argument, a var, field or parameter type, a result type)
// typeToExpr raises GALA-E0090 instead. Spelling it would tie the program to
// a structural type that GALA source cannot name, and one with an unexported
// field cannot be written outside its own package at all.
func (t *galaASTTransformer) anonymousStructTypeMessage(typ transpiler.Type) string {
	what := "holds"
	if _, ok := typ.(transpiler.StructType); ok {
		what = "is"
	}
	return fmt.Sprintf("type `%s` %s an anonymous Go struct type, which GALA cannot write", t.diagTypeName(typ), what)
}
