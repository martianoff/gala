package parser

import (
	"fmt"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/antlr4-go/antlr/v4"
	"github.com/stretchr/testify/assert"
)

func TestGalaParser(t *testing.T) {
	p := NewAntlrGalaParser()

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name: "Basic val declaration",
			input: `package main

val x = 10`,
			wantErr: false,
		},
		{
			name: "Basic var declaration",
			input: `package main

var y = 20`,
			wantErr: false,
		},
		{
			name: "Function declaration Go-style",
			input: `package main

func add(a int, b int) int { return a + b }`,
			wantErr: false,
		},
		{
			name: "Function declaration Scala-style",
			input: `package main

func square(x int) int = x * x`,
			wantErr: false,
		},
		{
			name: "Lambda expression",
			input: `package main

val f = (x int) => x * x`,
			wantErr: false,
		},
		{
			name: "Match expression",
			input: `package main

val res = x match {
	case 1 => "one"
	case 2 => "two"
	case _ => "many"
}`,
			wantErr: false,
		},
		{
			name: "If expression",
			input: `package main

val status = if (score > 50) "pass" else "fail"`,
			wantErr: false,
		},
		{
			name: "Generics support",
			input: `package main

func identity[T any](x T) T { return x }`,
			wantErr: false,
		},
		{
			name: "Struct type",
			input: `package main

type Person struct {
	Name string
	Age int
}`,
			wantErr: false,
		},
		{
			name: "Expression statement (not allowed at top level)",
			input: `package main

1 + 1`,
			wantErr: true,
		},
		{
			name: "Struct with mutable and immutable fields",
			input: `package main

type Config struct {
	val ID string
	var Count int
	Timeout int
}`,
			wantErr: false,
		},
		{
			name: "Function with mutable and immutable parameters",
			input: `package main

func process(val data string, var count int, mode int) = "ok"`,
			wantErr: false,
		},
		{
			name: "Lambda with mutable and immutable parameters",
			input: `package main

val f = (val a int, var b int) => a + b`,
			wantErr: false,
		},
		{
			name: "All fields immutable",
			input: `package main

type Point struct {
	val X int
	val Y int
}`,
			wantErr: false,
		},
		{
			name: "All fields mutable",
			input: `package main

type Box struct {
	var Width int
	var Height int
}`,
			wantErr: false,
		},
		{
			name:    "Missing package declaration",
			input:   `val x = 10`,
			wantErr: true,
		},
		{
			name: "Hex integer literal",
			input: `package main

val x = 0x1F`,
			wantErr: false,
		},
		{
			name: "Hex integer literal uppercase prefix",
			input: `package main

val x = 0X1f`,
			wantErr: false,
		},
		{
			name: "Hex integer literal in expression",
			input: `package main

val x = 0xFF + 0x01`,
			wantErr: false,
		},
		{
			name: "Single-line tuple as match arm body",
			input: `package main

func dispatch(m string, n int) Tuple[string, int] = n match {
	case 0 => (m, n)
	case _ => (m, n)
}`,
			wantErr: false,
		},
		{
			name: "Multi-line tuple as match arm body without trailing comma",
			input: `package main

func dispatch(m string, n int) Tuple[string, int] = n match {
	case 0 => (
		m,
		n
	)
	case _ => (m, n)
}`,
			wantErr: false,
		},
		{
			name: "Multi-line tuple as match arm body with trailing comma",
			input: `package main

func dispatch(m string, n int) Tuple[string, int] = n match {
	case 0 => (
		m,
		n,
	)
	case _ => (m, n)
}`,
			wantErr: false,
		},
		{
			name: "Tuple literal with trailing comma",
			input: `package main

val t = (1, 2,)`,
			wantErr: false,
		},
		{
			name: "bind declaration in a block",
			input: `package main

func f() Try[int] {
	bind a = g()
	Success(a)
}`,
			wantErr: false,
		},
		{
			name: "bind followed by also in a block",
			input: `package main

func f() Validated[Errs, Form] {
	bind a = va()
	also b = vb()
	Form(a, b)
}`,
			wantErr: false,
		},
		{
			name: "bind with explicit unwrapped type",
			input: `package main

func f() Try[int] {
	bind a int = g()
	Success(a)
}`,
			wantErr: false,
		},
		{
			name: "bind not allowed at top level",
			input: `package main

bind x = foo()`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := p.Parse(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func treeFingerprint(tree antlr.Tree) string {
	if tree == nil {
		return "<nil>"
	}
	result := fmt.Sprintf("%T:%d", tree, tree.GetChildCount())
	if parseTree, ok := tree.(antlr.ParseTree); ok {
		result += ":" + parseTree.GetText()
	}
	for i := 0; i < tree.GetChildCount(); i++ {
		result += "[" + treeFingerprint(tree.GetChild(i)) + "]"
	}
	return result
}

func parseFingerprint(input string, mode int) (string, map[int]string, []string) {
	p := NewAntlrGalaParser()
	input = galaerr.StripBOM(input)
	result := parseSourceFileAttempt(input, mode)
	errs := append([]error(nil), result.errors...)
	if err := p.checkEmptyLines(result.input, result.tree); err != nil {
		errs = append(errs, err)
	}
	messages := make([]string, len(errs))
	for i, err := range errs {
		messages[i] = fmt.Sprintf("%T:%s", err, err)
	}
	return treeFingerprint(result.tree), extractDocComments(result.tokens), messages
}

func publicParseFingerprint(input string) (string, map[int]string, []string) {
	tree, docs, errs := NewAntlrGalaParser().ParseLenient(input)
	messages := make([]string, len(errs))
	for i, err := range errs {
		messages[i] = fmt.Sprintf("%T:%s", err, err)
	}
	return treeFingerprint(tree), docs, messages
}

// The public path is SLL-first and falls back to LL on any error, so it has to
// agree with a forced LL parse on every input — including malformed input,
// where the fallback is what it retained. Where SLL alone succeeds, the public
// path retained the SLL attempt, so that has to agree with LL too; that is the
// comparison a two-column test would skip, and it is the one that would catch a
// prediction mode changing what the grammar accepts.
func TestPredictionModeFallbackMatchesLL(t *testing.T) {
	cases := []struct {
		name  string
		input string
		// sllRejected marks the cases SLL does not parse on its own, so the
		// public path re-parses them in LL and the SLL attempt is discarded.
		sllRejected bool
	}{
		{
			name:  "doc comment",
			input: "package main\n\n// Value is documented.\nval value = 1",
		},
		{
			name:  "precedence chain",
			input: "package main\n\nfunc f(x int) int = ((x + 1) * 2) - x\n",
		},
		{
			name:  "match expression",
			input: "package main\n\nval f = (x int) => x match {\n\tcase 0 => 1\n\tcase _ => x\n}\n",
		},
		{
			name:  "unclosed paren",
			input: "package main\n\nfunc f() { val x = (1 }\n",
			// Asserted below rather than assumed: if SLL accepted this, the
			// case would silently stop covering the fallback.
			sllRejected: true,
		},
		{
			name:  "untyped lambda param",
			input: "package main\n\nval f = (x) => x\n",
		},
		{
			name:  "typed empty slice literal",
			input: "package main\n\nval x = []int{1, 2}\n",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			llTree, llDocs, llErrors := parseFingerprint(tt.input, antlr.PredictionModeLL)
			publicTree, publicDocs, publicErrors := publicParseFingerprint(tt.input)
			assert.Equal(t, llTree, publicTree)
			assert.Equal(t, llDocs, publicDocs)
			assert.Equal(t, llErrors, publicErrors)

			sllTree, sllDocs, sllErrors := parseFingerprint(tt.input, antlr.PredictionModeSLL)
			if tt.sllRejected {
				assert.NotEmpty(t, sllErrors, "case is marked as rejected by SLL, but SLL accepted it")
				return
			}
			assert.Empty(t, sllErrors, "SLL rejected input the public path kept, so the fallback was not exercised")
			assert.Equal(t, llTree, sllTree, "SLL and LL disagree on input SLL accepted")
			assert.Equal(t, llDocs, sllDocs)
			assert.Equal(t, llErrors, sllErrors)
		})
	}
}
