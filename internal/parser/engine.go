package parser

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"sync/atomic"

	"martianoff/gala/internal/parser/galop"
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
	// EngineGalop parses with galop and falls back to ANTLR. It is the
	// default where galop's parser is built in.
	EngineGalop Engine = "galop"
	// EngineANTLR parses with ANTLR's runtime alone.
	EngineANTLR Engine = "antlr"
)

// engineChoice is a resolved engine, or why the requested one is unusable.
type engineChoice struct {
	engine Engine
	err    error
}

var (
	// requested is the engine name the CLI was asked for (RequestEngine); ""
	// defers to GALA_PARSER.
	requested atomic.Pointer[string]
	// chosen caches the engine resolved from requested, GALA_PARSER and the
	// built-in default; nil until the first parse.
	chosen atomic.Pointer[engineChoice]
)

// ParseEngine returns the engine named s, or why it cannot be used.
func ParseEngine(s string) (Engine, error) {
	switch e := Engine(s); e {
	case EngineANTLR:
		return e, nil
	case EngineGalop:
		if !galop.BuiltIn {
			return "", fmt.Errorf("parser %q is not built into this binary; use %q", s, EngineANTLR)
		}
		return e, nil
	}
	return "", fmt.Errorf("unknown parser %q: want %q or %q", s, EngineGalop, EngineANTLR)
}

// RequestEngine asks for the engine named name (the CLI's --parser flag); ""
// leaves the choice to the GALA_PARSER environment variable, then to the
// built-in default. The name is checked when a parse needs it, so commands
// that parse nothing never fail on it.
func RequestEngine(name string) {
	requested.Store(&name)
	chosen.Store(nil)
}

// SetEngine makes later parses use e.
func SetEngine(e Engine) { chosen.Store(&engineChoice{engine: e}) }

// currentEngine returns the engine parses use, resolving it on first use.
func currentEngine() (Engine, error) {
	if c := chosen.Load(); c != nil {
		return c.engine, c.err
	}
	name := ""
	if r := requested.Load(); r != nil {
		name = *r
	}
	if name == "" {
		name = os.Getenv("GALA_PARSER")
	}
	c := &engineChoice{engine: EngineANTLR}
	if name != "" {
		c.engine, c.err = ParseEngine(name)
	} else if galop.BuiltIn {
		c.engine = EngineGalop
	}
	chosen.Store(c)
	return c.engine, c.err
}

// CurrentEngine returns the engine parses use; ANTLR's when the requested
// one is unusable (parses then report why).
func CurrentEngine() Engine {
	e, err := currentEngine()
	if err != nil {
		return EngineANTLR
	}
	return e
}

// logGalopPanic reports, with GALA_PANIC_STACK=1, that galop's parser
// panicked on input and ANTLR's parsed it instead. The input is named by its
// size and first line: the parser is given text, not a file.
func logGalopPanic(input string, r any) {
	if os.Getenv("GALA_PANIC_STACK") != "1" {
		return
	}
	first, _, _ := strings.Cut(input, "\n")
	if len(first) > 80 {
		first = first[:80]
	}
	fmt.Fprintf(os.Stderr, "gala: galop's parser panicked on %d bytes starting %q: %v; parsed with ANTLR instead\n%s\n",
		len(input), first, r, debug.Stack())
}
