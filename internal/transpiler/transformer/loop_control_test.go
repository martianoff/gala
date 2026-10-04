package transformer_test

import (
	"errors"
	"strings"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

// TestLoopControlInStatementMatch pins that a `break` / `continue` in an arm of
// a match used as a statement controls the enclosing loop. The match used to be
// lowered to a closure, `func(obj int) { ... }(i)`, in which the `break` of
// `case 2 => { break }` became a `return` from the closure: the loop silently
// kept going. A match whose arms hold loop control is now inlined as an
// if-else chain, as one whose arms return already was.
func TestLoopControlInStatementMatch(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name: "block arm and expression arm in a for-clause loop",
			input: `package main

func main() {
    for i := 0; i < 5; i++ {
        i match {
            case 1 => continue
            case 3 => { break }
            case _ => Println(i)
        }
    }
}
`,
			want: []string{"continue", "break"},
		},
		{
			name: "condition loop, loop control after a statement in the arm",
			input: `package main

func main() {
    var n = 0
    for n < 10 {
        n++
        (n % 3) match {
            case 0 => {
                Println(n)
                continue
            }
            case _ => Println(-n)
        }
    }
}
`,
			want: []string{"continue"},
		},
		{
			name: "range loop over a sealed type, default arm",
			input: `package main

import . "martianoff/gala/go_interop"

sealed type Cmd {
    case Skip()
    case Say(Text string)
}

func run(cmds []Cmd) {
    for _, c := range cmds {
        c match {
            case Say(t) => Println(t)
            case _ => break
        }
    }
}

func main() {
    run(SliceOf[Cmd](Say("a"), Skip()))
}
`,
			want: []string{"break"},
		},
		{
			name: "nested statement matches",
			input: `package main

func main() {
    for i := 0; i < 3; i++ {
        (i, i * 2) match {
            case (a, b) => {
                a match {
                    case 1 => { if b == 2 { break } }
                    case _ => Println(b)
                }
            }
        }
    }
}
`,
			want: []string{"break"},
		},
		{
			name: "loop inside a value match arm keeps its own break",
			input: `package main

func main() {
    for i := 0; i < 3; i++ {
        val v = i match {
            case 1 => {
                var acc = 0
                for k := 0; k < 10; k++ {
                    if k == 3 { break }
                    acc += k
                }
                acc
            }
            case n => n
        }
        Println(v)
    }
}
`,
			want: []string{"break"},
		},
		{
			// A bare `return` was trapped the same way: it left the match's
			// function literal and the loop went on.
			name: "bare return in a void function",
			input: `package main

func scan() {
    for i := 0; i < 5; i++ {
        i match {
            case 2 => { return }
            case _ => Println(i)
        }
    }
    Println("finished")
}

func main() {
    scan()
}
`,
			want: []string{"return"},
		},
		{
			// No arm reads the subject: the inlined match must not bind it to
			// an unused Go variable.
			name: "wildcard-only arm",
			input: `package main

func main() {
    for i := 0; i < 3; i++ {
        i match {
            case _ => break
        }
    }
}
`,
			want: []string{"break", "_ = i"},
		},
		{
			// An expression arm of a statement match is a statement too, so a
			// nested match or if-expression written there may hold loop control.
			name: "nested match and if-expression as expression arms",
			input: `package main

func main() {
    for i := 0; i < 5; i++ {
        (i, i) match {
            case (0, _) => Println("zero")
            case (1, n) => n match {
                case 1 => continue
                case _ => break
            }
            case (n, _) => if (n > 3) { break } else { Println(n) }
        }
    }
}
`,
			want: []string{"continue", "break"},
		},
		{
			name: "if-expression statement with a block branch",
			input: `package main

func main() {
    for i := 0; i < 5; i++ {
        if (i == 2) { break } else Println(i)
    }
}
`,
			want: []string{"break"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := trans.Transpile(tc.input, "loop_control_test.gala")
			require.NoError(t, err)
			gen := stripGeneratedHeader(got)
			for _, w := range tc.want {
				require.Contains(t, gen, "\t"+w+"\n", "generated:\n%s", gen)
			}
			// The statement match is inlined: no closure over the subject.
			require.NotContains(t, gen, "func(obj int) {", "generated:\n%s", gen)
			require.NotContains(t, gen, "func(obj main.Cmd) {", "generated:\n%s", gen)
			require.NotContains(t, gen, "func(obj Cmd) {", "generated:\n%s", gen)
		})
	}
}

// TestLoopControlThatCannotReachItsLoop pins GALA-E0059: loop control that
// cannot reach the loop written around it is an error at the `break` /
// `continue`, never a silently different control flow. A match or
// if-expression whose value is used must produce a value, a lambda is a
// separate function, and outside any loop there is nothing to control.
func TestLoopControlThatCannotReachItsLoop(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()
	cases := []struct {
		name      string
		input     string
		line, col int
		msg       string
	}{
		{
			name: "block arm of a match whose value is used",
			input: `package main

func main() {
    for i := 0; i < 5; i++ {
        Println(i match {
            case 2 => { break }
            case n => n * 10
        })
    }
}
`,
			line: 6, col: 24,
			msg: "`break` inside a match whose value is used cannot reach the loop around it",
		},
		{
			name: "expression arm of a match passed as an argument",
			input: `package main

func main() {
    for i := 0; i < 5; i++ {
        Println(i match {
            case 2 => continue
            case n => n
        })
    }
}
`,
			line: 6, col: 22,
			msg: "`continue` inside a match whose value is used cannot reach the loop around it",
		},
		{
			name: "default arm of a value match after a statement",
			input: `package main

func f(i int) int = i match {
    case 1 => 10
    case _ => {
        Println(i)
        break
    }
}

func main() {
    for i := 0; i < 3; i++ {
        Println(f(i))
    }
}
`,
			line: 7, col: 8,
			msg: "`break` inside a match whose value is used cannot reach the loop around it",
		},
		{
			name: "non-trailing break in a value if-expression branch",
			input: `package main

func main() {
    for i := 0; i < 5; i++ {
        Println(if (i > 1) {
            if i == 3 { break }
            i
        } else 0)
    }
}
`,
			line: 6, col: 24,
			msg: "`break` inside an if-expression whose value is used cannot reach the loop around it",
		},
		{
			name: "trailing break of a value if-expression branch",
			input: `package main

func main() {
    for i := 0; i < 5; i++ {
        Println(if (i == 2) { break } else 1)
    }
}
`,
			line: 5, col: 30,
			msg: "`break` inside an if-expression whose value is used cannot reach the loop around it",
		},
		{
			name: "loop control read as a value",
			input: `package main

func main() {
    for i := 0; i < 5; i++ {
        val x = break
        Println(x)
    }
}
`,
			line: 5, col: 16,
			msg: "`break` is a statement, not a value",
		},
		{
			name: "statement match inside a lambda inside a loop",
			input: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    for i := 0; i < 3; i++ {
        ArrayOf(1, 2, 3).ForEach((x) => {
            x match {
                case 2 => break
                case _ => Println(x)
            }
        })
    }
}
`,
			line: 9, col: 26,
			msg: "`break` cannot leave the lambda it is in to reach the loop around it",
		},
		{
			name: "continue in a lambda body",
			input: `package main

func main() {
    for i := 0; i < 5; i++ {
        val f = () => {
            if i == 2 {
                continue
            }
            Println(i)
        }
        f()
    }
}
`,
			line: 7, col: 16,
			msg: "`continue` cannot leave the lambda it is in to reach the loop around it",
		},
		{
			name: "statement match outside any loop",
			input: `package main

func main() {
    val i = 3
    i match {
        case 3 => break
        case _ => Println(i)
    }
}
`,
			line: 6, col: 18,
			msg: "`break` is not inside a `for` loop",
		},
		{
			// The body is the function's value. Tail-call elimination wraps
			// it in a loop of its own, which must not capture the `break`.
			name: "break in a tail-recursive function",
			input: `package main

func sum(n int, acc int) int = if (n == 0) acc else if (n == 5) {
    if acc > 3 {
        break
    }
    acc
} else sum(n - 1, acc + n)

func main() {
    Println(sum(10, 0))
}
`,
			line: 5, col: 8,
			msg: "`break` is not inside a `for` loop",
		},
		{
			name: "break as the value of a function",
			input: `package main

func f(i int) int = if (i > 3) i else {
    break
}

func main() {
    Println(f(1))
}
`,
			line: 4, col: 4,
			msg: "`break` inside an if-expression whose value is used cannot reach the loop around it",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.input, "loop_control_test.gala")
			require.Error(t, err)
			var se *galaerr.SemanticError
			require.True(t, errors.As(err, &se), "want a semantic error, got %T: %v", err, err)
			require.Equal(t, galaerr.CodeLoopControlOutsideLoop, se.Code, "got: %v", err)
			require.Contains(t, se.Msg, tc.msg)
			require.Equal(t, tc.line, se.Line, "line of %v", err)
			require.Equal(t, tc.col, se.Column, "column of %v", err)
			require.False(t, strings.Contains(se.Msg, "internal"), "got: %v", err)
		})
	}
}
