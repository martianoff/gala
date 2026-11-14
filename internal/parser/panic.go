package parser

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"martianoff/gala/galaerr"
)

// parserPanicHint is the hint of the internal compiler error a panic in
// galop's lexer or parser gives (galopPanicError). Like
// galaerr.InternalTransformerPanicHint it leads with whose fault it is not,
// since the renderer's caret annotation shows only its first clause.
const parserPanicHint = "not this file's fault: an internal compiler defect in the parser; " +
	"rerun with GALA_PANIC_STACK=1 for the stack, and please file an issue at " +
	"https://github.com/martianoff/gala/issues with the source that triggered it"

// galopPanicError is the diagnostic of a panic r in galop's stage, "lexer" or
// "parser", on input, which starts at line/col of its file (line <= 0 for a
// whole file, reported at 1:0): an internal compiler error (GALA-E0017). The
// panic is a galop bug; it fails that file's parse, and only that file's, so
// a build reports it with the file's other diagnostics and the language
// server keeps serving. GALA_PANIC_STACK=1 also prints the panic and its
// stack on standard error, naming the input by its size and first line: the
// lexer and parser are given text, not a file. It must be called from the
// deferred function that recovered r, for the stack to be the panic's.
func galopPanicError(stage, input string, line, col int, r any) *galaerr.SemanticError {
	if line <= 0 {
		line, col = 1, 0
	}
	if os.Getenv("GALA_PANIC_STACK") == "1" {
		first, _, _ := strings.Cut(input, "\n")
		if len(first) > 80 {
			first = first[:80]
		}
		fmt.Fprintf(os.Stderr, "gala: galop's %s panicked on %d bytes starting %q: %v\n%s",
			stage, len(input), first, r, debug.Stack())
	}
	return galaerr.NewCodedSemanticError(galaerr.CodeInternalTransformerPanic, line, col,
		fmt.Sprintf("internal compiler error: galop's %s panicked: %v", stage, r), parserPanicHint)
}

// IsInternalError reports whether err is, or holds, an internal compiler
// error (GALA-E0017) of a parse: galop's lexer or parser panicked on the
// input, so the parse has no tree and says nothing about the source. The
// parser reports no other GALA-E0017.
func IsInternalError(err error) bool {
	var se *galaerr.SemanticError
	return errors.As(err, &se) && se.Code == galaerr.CodeInternalTransformerPanic
}
