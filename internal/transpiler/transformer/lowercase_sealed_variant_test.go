package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

// lowercaseFrame is a sealed type whose variants are all lowercase, so that
// nothing about them can be recognized by capitalization.
const lowercaseFrame = `package main

sealed type frame {
    case lineFrame(Text string)
    case endFrame()
}

`

// TestLowercaseSealedVariants pins that a variant is classified by what its
// name resolves to, never by the case of its first letter: lowercase variants
// count towards exhaustiveness, and a bare lowercase zero-field variant tests
// the value — at the top of a match and nested inside an extractor — instead
// of binding a variable named after it. PascalCase rows are the controls.
func TestLowercaseSealedVariants(t *testing.T) {
	cases := []struct {
		name string
		src  string
		// wantContains must appear in the generated Go.
		wantContains []string
		// wantAbsent must not appear in the generated Go.
		wantAbsent []string
		// wantCode is the expected diagnostic; empty when the source compiles.
		wantCode galaerr.ErrorCode
		// wantMsg must appear in the diagnostic's message.
		wantMsg string
	}{
		{
			name: "exhaustive extractor arms without a default",
			src: lowercaseFrame + `func describe(f frame) string = f match {
    case lineFrame(text) => s"line $text"
    case endFrame() => "end"
}

func main() { Println(describe(lineFrame("x"))) }`,
			wantContains: []string{`panic("unreachable")`},
		},
		{
			name: "PascalCase control",
			src: `package main

sealed type Frame {
    case LineFrame(Text string)
    case EndFrame()
}

func describe(f Frame) string = f match {
    case LineFrame(text) => s"line $text"
    case EndFrame() => "end"
}

func main() { Println(describe(LineFrame("x"))) }`,
			wantContains: []string{`panic("unreachable")`},
		},
		{
			name: "explicit default still compiles",
			src: lowercaseFrame + `func describe(f frame) string = f match {
    case lineFrame(text) => s"line $text"
    case _ => "end"
}

func main() { Println(describe(endFrame())) }`,
			wantContains: []string{"lineFrame{}.Unapply(obj)"},
		},
		{
			name: "missing lowercase variant is reported",
			src: lowercaseFrame + `func describe(f frame) string = f match {
    case lineFrame(text) => s"line $text"
}

func main() { Println(describe(lineFrame("x"))) }`,
			wantCode: galaerr.CodeNonExhaustiveMatch,
			wantMsg:  "missing cases: endFrame",
		},
		{
			name: "bare zero-field variant matches it",
			src: lowercaseFrame + `func describe(f frame) string = f match {
    case lineFrame(text) => s"line $text"
    case endFrame => "end"
}

func main() { Println(describe(endFrame())) }`,
			wantContains: []string{"endFrame{}.Unapply(obj)", `panic("unreachable")`},
			wantAbsent:   []string{"endFrame := obj"},
		},
		{
			name: "guarded bare zero-field variant matches it",
			src: lowercaseFrame + `func describe(f frame, quiet bool) string = f match {
    case endFrame if quiet => ""
    case _ => "loud"
}

func main() { Println(describe(endFrame(), true)) }`,
			wantContains: []string{"endFrame{}.Unapply(obj)"},
			wantAbsent:   []string{"endFrame := obj"},
		},
		{
			name: "bare field-bearing variant is rejected",
			src: lowercaseFrame + `func describe(f frame) string = f match {
    case lineFrame => "line"
    case endFrame() => "end"
}

func main() { Println(describe(endFrame())) }`,
			wantCode: galaerr.CodeBareVariantBinding,
			wantMsg:  "`lineFrame` is a variant of sealed type \"frame\"",
		},
		{
			name: "name that is no variant still binds",
			src: lowercaseFrame + `func describe(f frame) string = f match {
    case endFrame => "end"
    case other => s"other $other"
}

func main() { Println(describe(lineFrame("x"))) }`,
			wantContains: []string{"endFrame{}.Unapply(obj)", "other := obj"},
		},
		{
			// A name spelled like a zero-field variant of some OTHER sealed
			// type is an ordinary binding: the variant cannot match an int.
			name: "variant of an unrelated type still binds",
			src: lowercaseFrame + `func describe(n int) string = n match {
    case 0 => "zero"
    case endFrame => s"other $endFrame"
}

func opt(o Option[int]) string = o match {
    case Some(endFrame) => s"some $endFrame"
    case None => "none"
}

func main() {
    Println(describe(3))
    Println(opt(Some(4)))
    Println(endFrame())
}`,
			wantContains: []string{"endFrame := obj"},
			wantAbsent:   []string{"endFrame{}.Unapply("},
		},
		{
			// The same name in a guarded arm and in a tuple element binds too:
			// the lowering decides as the default-arm classification does.
			name: "variant of an unrelated type binds in every position",
			src: lowercaseFrame + `func describe(n int) string = n match {
    case endFrame if endFrame > 5 => s"big $endFrame"
    case _ => "small"
}

func pair(p Tuple[int, int]) string = p match {
    case (endFrame, k) => s"$endFrame $k"
}

func main() {
    Println(describe(7))
    Println(pair((1, 2)))
    Println(endFrame())
}`,
			wantContains: []string{"endFrame := obj"},
			wantAbsent:   []string{"endFrame{}.Unapply("},
		},
		{
			// A field typed by an alias of a sealed type is matched as that
			// sealed type: the nested variant tests, it does not bind.
			name: "nested variant under an alias-typed field",
			src: lowercaseFrame + `type alias frame

func describe(o Option[alias]) string = o match {
    case Some(endFrame) => "some end"
    case Some(other) => s"some $other"
    case None => "none"
}

func main() { Println(describe(Some[alias](endFrame()))) }`,
			wantContains: []string{"endFrame{}.Unapply("},
			wantAbsent:   []string{"endFrame := "},
		},
		{
			// concurrent.Future declares an unexported `fut` case. Another
			// package cannot name it, so there `fut` is an ordinary binding.
			name: "unexported variant of another package's type binds",
			src: `package main

import "martianoff/gala/concurrent"

func count(o Option[concurrent.Future[int]]) int = o match {
    case Some(fut) => fut.Await().GetOrElse(0)
    case None => 0
}

func main() { Println(count(None[concurrent.Future[int]]())) }`,
			wantContains: []string{"fut := "},
		},
		{
			// An `any` subject is no sealed type, so a variant's name binds.
			name: "variant name against an any subject still binds",
			src: lowercaseFrame + `func describe(v any) string = v match {
    case 1 => "one"
    case endFrame => s"other $endFrame"
}

func main() {
    Println(describe(2))
    Println(endFrame())
}`,
			wantContains: []string{"endFrame := obj"},
			wantAbsent:   []string{"endFrame{}.Unapply("},
		},
		{
			name: "nested inside a generic lowercase sealed type",
			src: lowercaseFrame + `sealed type maybe[T any] {
    case just(Value T)
    case nothing
}

func describe(m maybe[frame]) string = m match {
    case just(endFrame) => "just end"
    case just(lineFrame(text)) => s"just $text"
    case nothing => "nothing"
}

func main() { Println(describe(just[frame](endFrame()))) }`,
			wantContains: []string{"endFrame{}.Unapply(", "nothing[frame]{}.Unapply(obj)", `panic("unreachable")`},
			wantAbsent:   []string{"endFrame := ", "nothing := obj"},
		},
		{
			name: "nested inside Option",
			src: lowercaseFrame + `func describe(o Option[frame]) string = o match {
    case Some(endFrame) => "some end"
    case Some(other) => s"some $other"
    case None => "none"
}

func main() { Println(describe(Some[frame](endFrame()))) }`,
			wantContains: []string{"endFrame{}.Unapply("},
			wantAbsent:   []string{"endFrame := "},
		},
		{
			// Nesting was decided without the variants for capitalized
			// names too: `Some(EndFrame)` bound a variable named EndFrame.
			name: "PascalCase nested inside Option",
			src: `package main

sealed type Frame {
    case LineFrame(Text string)
    case EndFrame()
}

func describe(o Option[Frame]) string = o match {
    case Some(EndFrame) => "some end"
    case Some(LineFrame(text)) => s"some $text"
    case None => "none"
}

func main() { Println(describe(Some[Frame](EndFrame()))) }`,
			wantContains: []string{"EndFrame{}.Unapply("},
			wantAbsent:   []string{"EndFrame := "},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileBareVariant(t, tc.src)
			if tc.wantCode != "" {
				require.Error(t, err)
				var se *galaerr.SemanticError
				require.ErrorAs(t, err, &se)
				require.Equal(t, tc.wantCode, se.Code)
				require.Contains(t, se.Msg, tc.wantMsg)
				return
			}
			require.NoError(t, err)
			for _, want := range tc.wantContains {
				require.Contains(t, out, want)
			}
			for _, absent := range tc.wantAbsent {
				require.NotContains(t, out, absent)
			}
		})
	}
}
