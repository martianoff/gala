package parser

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
)

// Engine names the parser that turns GALA source into a tree.
//
// Both engines build the same tree: the context types ANTLR generates for
// gala.g4. galop's parser (its antlr flavor, generated next to ANTLR's in
// internal/parser/grammar) gives exactly ANTLR's tree on every input ANTLR
// accepts, many times faster. On input with a syntax error, or if it fails
// in any other way, ANTLR's parser parses the input instead, so syntax
// errors, their hints and the error-recovered tree the language server reads
// are always ANTLR's.
type Engine string

const (
	// EngineGalop parses with galop and falls back to ANTLR.
	EngineGalop Engine = "galop"
	// EngineANTLR parses with ANTLR's runtime alone.
	EngineANTLR Engine = "antlr"
)

// defaultEngine names the engine this binary uses unless asked for another.
// Bazel's --//internal/parser:antlr sets it to "antlr" at link time (an x_defs
// of this library); both engines are built in either way. Like GALA_PARSER, a
// name that is no engine fails the parses.
var defaultEngine = string(EngineGalop)

// DefaultEngine is the engine this binary uses unless asked for another (see
// defaultEngine).
func DefaultEngine() Engine {
	return Engine(defaultEngine)
}

// engineChoice is a resolved engine, or why the requested one is unusable.
type engineChoice struct {
	engine Engine
	err    error
}

var (
	// engineMu guards requested and the resolution of chosen.
	engineMu sync.Mutex
	// requested is the engine name the CLI asked for (RequestEngine); ""
	// defers to GALA_PARSER.
	requested string
	// chosen caches the engine resolved from requested, GALA_PARSER and the
	// default; nil until a parse needs it. Read without the lock.
	chosen atomic.Pointer[engineChoice]
)

// parseEngine returns the engine named s, or why it cannot be used.
func parseEngine(s string) (Engine, error) {
	switch e := Engine(s); e {
	case EngineANTLR, EngineGalop:
		return e, nil
	}
	return "", fmt.Errorf("unknown parser %q: want %q or %q", s, EngineGalop, EngineANTLR)
}

// RequestEngine asks for the engine named name (the CLI's --parser flag); ""
// leaves the choice to the GALA_PARSER environment variable, then to
// DefaultEngine. The name is checked when a parse needs it, so commands that
// parse nothing never fail on it.
func RequestEngine(name string) {
	engineMu.Lock()
	defer engineMu.Unlock()
	requested = name
	chosen.Store(nil)
}

// currentEngine returns the engine parses use, resolving it on first use.
func currentEngine() (Engine, error) {
	if c := chosen.Load(); c != nil {
		return c.engine, c.err
	}
	engineMu.Lock()
	defer engineMu.Unlock()
	if c := chosen.Load(); c != nil {
		return c.engine, c.err
	}
	name := requested
	if name == "" {
		name = os.Getenv("GALA_PARSER")
	}
	if name == "" {
		name = defaultEngine
	}
	e, err := parseEngine(name)
	c := &engineChoice{engine: e, err: err}
	chosen.Store(c)
	return c.engine, c.err
}

// galopPanics counts the panics galop's parser recovered from.
var galopPanics atomic.Int64

// noteGalopPanic records that galop's parser panicked on input and ANTLR's
// parsed it instead. Each panic is a galop bug that costs a second parse, so
// it is reported on standard error: the first, then the 10th, 100th, ... with
// the running count. GALA_PANIC_STACK=1 prints every one with its stack. The
// input is named by its size and first line: the parser is given text, not a
// file.
func noteGalopPanic(input string, r any) {
	n := galopPanics.Add(1)
	stack := os.Getenv("GALA_PANIC_STACK") == "1"
	if !stack && !isPowerOf10(n) {
		return
	}
	first, _, _ := strings.Cut(input, "\n")
	if len(first) > 80 {
		first = first[:80]
	}
	fmt.Fprintf(os.Stderr, "gala: warning: galop's parser panicked (%d so far in this process) on %d bytes starting %q: %v; parsed with ANTLR's parser instead\n",
		n, len(input), first, r)
	if stack {
		os.Stderr.Write(debug.Stack())
	}
}

func isPowerOf10(n int64) bool {
	for n >= 10 && n%10 == 0 {
		n /= 10
	}
	return n == 1
}
