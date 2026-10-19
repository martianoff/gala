package transformer_test

import (
	"errors"
	"strings"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

// sealedViewPrelude declares MaybeInt, a type whose MatchView() gives it the
// sealed view Option[int], and Gauge, viewed as a user sealed type.
const sealedViewPrelude = `package main

struct MaybeInt(raw int)

func (m MaybeInt) MatchView() Option[int] = When(m.raw >= 0, m.raw)

sealed type Level {
    case Low()
    case High(N int)
}

struct Gauge(v int)

func (g Gauge) MatchView() Level = if (g.v < 10) Low() else High(g.v)

// NotAView's MatchView returns a type that is not sealed.
struct NotAView(v int)

func (n NotAView) MatchView() int = n.v
`

// TestSealedViewMatchLowering pins the generated shape of a match over a
// sealed view: the subject is read once through MatchView() and the cases are
// lowered against the view type, while a match whose cases name no variant of
// the view stays an ordinary match over the subject.
func TestSealedViewMatchLowering(t *testing.T) {
	cases := []struct {
		name string
		body string
		// want are fragments the generated Go must contain.
		want []string
		// wantViews is how many MatchView() calls the generated Go holds.
		wantViews int
	}{
		{
			name: "option view",
			body: `func f(m MaybeInt) string = m match {
    case Some(n) => s"some $n"
    case None()  => "none"
}`,
			want:      []string{"func(obj std.Option[int]) string", "}(m.MatchView())", "std.Some[int]{}.Unapply(obj)"},
			wantViews: 1,
		},
		{
			name: "bare zero-field variant is the variant",
			body: `func f(m MaybeInt) string = m match {
    case None    => "none"
    case Some(n) => s"some $n"
}`,
			want:      []string{"std.None[int]{}.Unapply(obj)"},
			wantViews: 1,
		},
		{
			name: "user sealed view with alternatives",
			body: `func f(g Gauge) string = g match {
    case Low() | High(_) => "any"
}`,
			want:      []string{"func(obj Level) string", "}(g.MatchView())"},
			wantViews: 1,
		},
		{
			name: "cases over the subject itself are not a view match",
			body: `func f(m MaybeInt) int = m match {
    case MaybeInt(r) => r
    case _           => 0
}`,
			want:      []string{"func(obj MaybeInt) int"},
			wantViews: 0,
		},
		{
			name: "partial function reads the view once",
			body: `func f(m MaybeInt) Option[int] {
    val pf func(MaybeInt) Option[int] = { case Some(n) => n * 2 }
    pf(m)
}`,
			want:      []string{"_pf_view := _pf_arg.MatchView()", "std.Some[int]{}.Unapply(_pf_view)"},
			wantViews: 1,
		},
		{
			name: "stable identifier and typed pattern of the view type",
			body: `func f(m MaybeInt) string {
    val Zero = Some(0)
    m match {
        case Zero                          => "zero"
        case None                          => "none"
        case o: Option[int] if o.IsEmpty() => "empty"
        case _                             => "other"
    }
}`,
			want:      []string{"func(obj std.Option[int]) string", "}(m.MatchView())"},
			wantViews: 1,
		},
		{
			name: "sub-pattern reads the view of its own value",
			body: `func f(o Option[MaybeInt]) int = o match {
    case Some(Some(n)) => n
    case Some(None)    => 0
    case None()        => -1
}`,
			want:      []string{"func(obj std.Option[MaybeInt]) int", "std.None[int]{}.Unapply("},
			wantViews: 2,
		},
		{
			name: "binding beside a view sub-pattern binds the value",
			body: `func f(a MaybeInt, b MaybeInt) MaybeInt = (a, b) match {
    case (Some(_), _) => a
    case (_, other)   => other
}`,
			want:      []string{":= obj.V1.Get().MatchView()", "other := obj.V2.Get()"},
			wantViews: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := sealedViewPrelude + "\n" + tc.body + "\n\nfunc main() {}\n"
			out, err := transpileBareVariant(t, src)
			require.NoError(t, err)
			for _, w := range tc.want {
				require.Contains(t, out, w)
			}
			// One MatchView declaration per viewed type in the prelude
			// (MaybeInt, Gauge, NotAView), plus the calls the matches make.
			require.Equal(t, 3+tc.wantViews, strings.Count(out, "MatchView()"), out)
		})
	}
}

// TestSealedViewMatchErrors pins the diagnostics of a match over a sealed
// view: a case that does not apply to the view is GALA-E0079, and the view's
// exhaustiveness is checked as for a match over the view type.
func TestSealedViewMatchErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
		code galaerr.ErrorCode
		msg  string
	}{
		{
			name: "extractor of the subject beside a view variant",
			body: `func f(m MaybeInt) int = m match {
    case Some(n)     => n
    case MaybeInt(r) => r
}`,
			code: galaerr.CodeInvalidSealedViewMatch,
			msg:  "`MaybeInt(r)` is not a variant of Option[int]",
		},
		{
			name: "binding of the whole subject",
			body: `func f(m MaybeInt) int = m match {
    case Some(n) => n
    case other   => 0
}`,
			code: galaerr.CodeInvalidSealedViewMatch,
			msg:  "`case other` would bind the Option[int] view, not the MaybeInt itself",
		},
		{
			name: "typed pattern of the subject type",
			body: `func f(m MaybeInt) int = m match {
    case Some(n)     => n
    case x: MaybeInt => 0
}`,
			code: galaerr.CodeInvalidSealedViewMatch,
			msg:  "tests for MaybeInt",
		},
		{
			name: "literal beside a view variant",
			body: `func f(g Gauge) int = g match {
    case Low() => 0
    case 7     => 7
    case _     => 1
}`,
			code: galaerr.CodeInvalidSealedViewMatch,
			msg:  "`7` is not a variant of Level",
		},
		{
			name: "stable identifier of the subject type",
			body: `func f(m MaybeInt) int {
    val Empty = MaybeInt(-1)
    m match {
        case Some(n) => n
        case Empty   => 0
        case _       => 1
    }
}`,
			code: galaerr.CodeInvalidSealedViewMatch,
			msg:  "`Empty` is not a value of Option[int]",
		},
		{
			name: "binding in a partial function",
			body: `func f(m MaybeInt) Option[int] {
    val pf func(MaybeInt) Option[int] = {
        case Some(n) => n
        case other   => 0
    }
    pf(m)
}`,
			code: galaerr.CodeInvalidSealedViewMatch,
			msg:  "`case other` would bind",
		},
		{
			name: "missing variant of the view",
			body: `func f(m MaybeInt) int = m match {
    case Some(n) => n
}`,
			code: galaerr.CodeNonExhaustiveMatch,
			msg:  "missing cases: None",
		},
		{
			name: "bare None alone is the variant, not a catch-all",
			body: `func f(m MaybeInt) int = m match {
    case None => 0
}`,
			code: galaerr.CodeNonExhaustiveMatch,
			msg:  "missing cases: Some",
		},
		{
			name: "sub-pattern mixing a view variant with another pattern",
			body: `func f(o Option[MaybeInt]) int = o match {
    case Some(None | MaybeInt(_)) => 0
    case _                        => 1
}`,
			code: galaerr.CodeInvalidSealedViewMatch,
			msg:  "`MaybeInt(_)` is not a variant of Option[int]",
		},
		{
			name: "missing variant of a nested view",
			body: `func f(o Option[MaybeInt]) int = o match {
    case Some(Some(n)) => n
    case None()        => -1
}`,
			code: galaerr.CodeNonExhaustiveMatch,
			msg:  "missing cases: Some(...)",
		},
		{
			name: "missing variant of a user sealed view",
			body: `func f(g Gauge) int = g match {
    case High(n) => n
}`,
			code: galaerr.CodeNonExhaustiveMatch,
			msg:  "missing cases: Low",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := sealedViewPrelude + "\n" + tc.body + "\n\nfunc main() {}\n"
			_, err := transpileBareVariant(t, src)
			require.Error(t, err)
			var se *galaerr.SemanticError
			require.ErrorAs(t, err, &se)
			require.Equal(t, tc.code, se.Code, se.Error())
			require.Contains(t, se.Msg, tc.msg)
		})
	}
}

// TestSealedViewNeedsASealedResult pins that a MatchView whose result is not
// a sealed type gives no view: the variant pattern is checked against the
// subject's own type, as before.
func TestSealedViewNeedsASealedResult(t *testing.T) {
	src := sealedViewPrelude + `
func f(n NotAView) int = n match {
    case Some(v) => v
    case _       => 0
}

func main() {}
`
	_, err := transpileBareVariant(t, src)
	require.Error(t, err)
	var se *galaerr.SemanticError
	if errors.As(err, &se) {
		require.NotEqual(t, galaerr.CodeInvalidSealedViewMatch, se.Code)
	}
	require.NotContains(t, err.Error(), "MatchView")
}
