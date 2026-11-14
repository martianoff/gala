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

// parserPanicPrefix starts the message of every galopPanicError, which
// IsParserPanic tells from the transformer's GALA-E0017 by.
const parserPanicPrefix = "internal compiler error: galop's "

// galopPanicError is the diagnostic of a panic r in galop's stage, "lexer" or
// "parser", on input, which starts at line/col of its file (line <= 0 for a
// whole file, reported at 1:0): an internal compiler error (GALA-E0017). The
// panic is a galop bug; it fails that file's parse, and only that file's, so
// a build reports it with the file's other diagnostics and the language
// server keeps serving. The panic is also reported on standard error under
// GALA_PANIC_STACK=1 (reportGalopPanic).
func galopPanicError(stage, input string, line, col int, r any) *galaerr.SemanticError {
	reportGalopPanic(stage, input, r)
	if line <= 0 {
		line, col = 1, 0
	}
	return galaerr.NewCodedSemanticError(galaerr.CodeInternalTransformerPanic, line, col,
		fmt.Sprintf("%s%s panicked: %v", parserPanicPrefix, stage, r), parserPanicHint)
}

// reportGalopPanic prints a panic r in galop's stage on input, with its
// stack, on standard error when GALA_PANIC_STACK=1, naming the input by its
// size and first line: the lexer and parser are given text, not a file. It
// must be called from the deferred function that recovered r, for the stack
// to be the panic's.
func reportGalopPanic(stage, input string, r any) {
	if os.Getenv("GALA_PANIC_STACK") != "1" {
		return
	}
	first, _, _ := strings.Cut(input, "\n")
	if len(first) > 80 {
		first = first[:80]
	}
	fmt.Fprintf(os.Stderr, "gala: galop's %s panicked on %d bytes starting %q: %v\n%s",
		stage, len(input), first, r, debug.Stack())
}

// IsParserPanic reports whether err is, or holds, the internal compiler
// error (GALA-E0017) of a panic in galop's lexer or parser: the parse has no
// tree and says nothing about the source.
func IsParserPanic(err error) bool {
	var se *galaerr.SemanticError
	return errors.As(err, &se) && se.Code == galaerr.CodeInternalTransformerPanic &&
		strings.HasPrefix(se.Msg, parserPanicPrefix)
}
