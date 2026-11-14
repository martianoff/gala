package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// printChoiceDecls declares types fmt prints by a Format or an Error before
// a pointer-receiver String, and methods that print a field of their
// receiver.
const printChoiceDecls = `
struct Fancy(var n int)

func (f Ptr[Fancy]) String() string {
    f.n = f.n + 1
    "plain"
}

func (f Fancy) Format(st fmt.State, verb rune) {
    fmt.Fprint(st, "fancy")
}

struct LoudPtrError(var n int)

func (e Ptr[LoudPtrError]) String() string = "quiet string"

func (e Ptr[LoudPtrError]) Error() string {
    e.n = e.n + 1
    "loud error"
}

struct QuietPtrError(var n int)

func (e Ptr[QuietPtrError]) String() string {
    e.n = e.n + 1
    "loud string"
}

func (e Ptr[QuietPtrError]) Error() string = "quiet error"

struct Shows(var c Loud, var q Quiet, var f Fault, var p Ptr[Loud])

func (s Ptr[Shows]) Line() { Println(s.c) }
func (s Ptr[Shows]) Spaced() { Print("c:", s.c, "\n") }
func (s Ptr[Shows]) Text() string = s"c=${s.c}"
func (s Ptr[Shows]) Spec() string = f"c=${s.c}%s"
func (s Ptr[Shows]) Hex() string = f"c=${s.c}%x"
func (s Ptr[Shows]) Count() int = s.c.n
func (s Ptr[Shows]) Quiet() { Println(s.q) }
func (s Ptr[Shows]) ByError() { Println(s.f) }
func (s Ptr[Shows]) ByPointer() { Println(s.p) }
func (s Ptr[Shows]) Sum() { Println(s.c.n + 1) }

struct Shown(var c Loud)

func (s Ptr[Shown]) String() string = s"Shown(${s.c})"

func (l Ptr[Loud]) Again() { Println(*l) }
`

// printChoiceSource is a program with receiverMutationDecls,
// printedFieldDecls and printChoiceDecls, and body as main's body.
func printChoiceSource(body string) string {
	return `package main

import "fmt"
` + receiverMutationDecls + printedFieldDecls + printChoiceDecls + `
func main() {
    ` + body + `
}
`
}

// TestPrintMethodChoice pins the method GALA prints a value by: fmt's
// choice, Format, then Error, then String. A value whose own methods include
// a Format or an Error prints by it and not by a pointer-receiver String, so
// that String is no reason for GALA-E0110. A value printed through its
// pointer prints by a pointer-receiver Format or Error before that String,
// and it is that method whose changes count.
func TestPrintMethodChoice(t *testing.T) {
	trans := newDefaultsTranspiler()

	rejected := []struct{ name, body, want string }{
		{"a val printed by a pointer Error that changes it", "val e = LoudPtrError(0)\n    Println(e)", "cannot print this LoudPtrError: its Error can change its receiver"},
		{"an interpolated val printed by such an Error", "val e = LoudPtrError(0)\n    Println(s\"e=$e\")", "its Error can change its receiver"},
	}
	for _, tc := range rejected {
		t.Run("rejected/"+tc.name, func(t *testing.T) {
			_, err := trans.Transpile(printChoiceSource(tc.body), "print_choice_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0110")
			assert.Contains(t, err.Error(), tc.want)
		})
	}

	accepted := []struct{ name, body, mustContain string }{
		{"a val printed by its Error", "val f = Fault(0)\n    Println(f)", "fmt.Println(f)"},
		{"a var printed by its Error", "var f = Fault(0)\n    Println(f)", "fmt.Println(f)"},
		{"an interpolated val printed by its Error", "val f = Fault(0)\n    Println(s\"f=$f\")", "fmt.Sprintf(\"f=%v\", f)"},
		{"a val printed by its Format", "val f = Fancy(0)\n    Println(f)", "fmt.Println(f)"},
		{"a var printed by its Format", "var f = Fancy(0)\n    Println(f)", "fmt.Println(f)"},
		{"a val whose pointer Error reads it", "val e = QuietPtrError(0)\n    Println(e)", "std.PtrString(e)"},
		{"a var whose pointer Error changes it", "var e = LoudPtrError(0)\n    Println(e)", "std.StringAt(&e)"},
	}
	for _, tc := range accepted {
		t.Run("accepted/"+tc.name, func(t *testing.T) {
			out, err := trans.Transpile(printChoiceSource(tc.body), "print_choice_test.gala")
			require.NoError(t, err)
			assert.Contains(t, out, tc.mustContain)
			assert.NotContains(t, out, "std.PtrString(f)")
			assert.NotContains(t, out, "std.StringAt(&f)")
		})
	}
}

// TestPrintedReceiverFieldMutates pins GALA-E0110 for a method that prints a
// field of its receiver: Println, Print and interpolation print the field in
// place, through its address, when it prints by a pointer-receiver String,
// so a String that changes the field changes the receiver, and the method
// cannot be called on a value that cannot be addressed. A field printed by a
// String that only reads it, by its own Error, or through a pointer it
// holds leaves the receiver alone.
func TestPrintedReceiverFieldMutates(t *testing.T) {
	trans := newDefaultsTranspiler()

	const shows = "val s = Shows(Loud(0), Quiet(0), Fault(0), &Loud(0))\n    "
	rejected := []struct{ name, body, want string }{
		{"a field printed by Println", shows + "s.Line()", "cannot call Line on this Shows"},
		{"a field printed by Print", shows + "s.Spaced()", "cannot call Spaced on this Shows"},
		{"an interpolated field", shows + "Println(s.Text())", "cannot call Text on this Shows"},
		{"a field formatted with %s", shows + "Println(s.Spec())", "cannot call Spec on this Shows"},
		{"a val printed by a String that prints such a field", "val h = Shown(Loud(0))\n    Println(h)", "cannot print this Shown: its String can change its receiver"},
		{"the receiver itself printed", "val l = Loud(0)\n    l.Again()", "cannot call Again on this Loud"},
	}
	for _, tc := range rejected {
		t.Run("rejected/"+tc.name, func(t *testing.T) {
			_, err := trans.Transpile(printChoiceSource(tc.body), "printed_receiver_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0110")
			assert.Contains(t, err.Error(), tc.want)
		})
	}

	accepted := []struct{ name, body string }{
		{"a field formatted with %x", shows + "Println(s.Hex())"},
		{"a field read as a value", shows + "Println(s.Count())"},
		{"a field whose String only reads it", shows + "s.Quiet()"},
		{"a field printed by its Error", shows + "s.ByError()"},
		{"a field held by pointer", shows + "s.ByPointer()"},
		{"an expression of a field", shows + "s.Sum()"},
		{"a var printed by such a String", "var h = Shown(Loud(0))\n    Println(h)"},
	}
	for _, tc := range accepted {
		t.Run("accepted/"+tc.name, func(t *testing.T) {
			_, err := trans.Transpile(printChoiceSource(tc.body), "printed_receiver_test.gala")
			require.NoError(t, err)
		})
	}
}
