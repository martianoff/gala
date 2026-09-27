package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStableIdentifierPatternComparesByEquality pins the lowering of a bare
// capitalized identifier that names an in-scope value.
//
// GALA follows Scala here: such an identifier is a stable identifier, and the
// arm tests the subject for equality with that value. Treating it as a fresh
// binding instead turns the arm into a catch-all — every value matches the
// first such arm, the program compiles, exits 0, and answers with the wrong
// arm. The runtime consequence is pinned by examples/stable_identifier_pattern;
// these cases pin the generated shape, where a regression would first appear.
func TestStableIdentifierPatternComparesByEquality(t *testing.T) {
	cases := []struct {
		name string
		src  string
		// wantContains is the equality test the arm must lower to.
		wantContains []string
		// wantAbsent is the binding assignment that must not appear.
		wantAbsent []string
	}{
		{
			name: "package-level val and var",
			src: `package main

type Environment string

val Development Environment = "development"
var Production Environment = "production"

func describe(env Environment) string = env match {
    case Development => "dev"
    case Production  => "prod"
    case _           => "other"
}

func main() {
    Println(describe(Production))
}`,
			wantContains: []string{"== Development.Get()", "== Production"},
			wantAbsent:   []string{"Development := obj", "Production := obj"},
		},
		{
			name: "local val",
			src: `package main

func main() {
    val Limit = 10
    val n = 3
    val r = n match {
        case Limit => "at limit"
        case _     => "elsewhere"
    }
    Println(r)
}`,
			wantContains: []string{"== Limit"},
			wantAbsent:   []string{"Limit := obj"},
		},
		{
			name: "nested inside an extractor",
			src: `package main

val Answer = 42

func main() {
    val o = Some(7)
    val r = o match {
        case Some(Answer) => "the answer"
        case Some(v)      => s"some $v"
        case None()       => "none"
    }
    Println(r)
}`,
			wantContains: []string{"== Answer.Get()"},
			wantAbsent:   []string{"Answer := "},
		},
		{
			name: "nested inside a tuple",
			src: `package main

val Answer = 42

func main() {
    val p = (7, "x")
    val r = p match {
        case (Answer, s) => s"the answer $s"
        case _           => "other"
    }
    Println(r)
}`,
			wantContains: []string{"== Answer.Get()"},
			wantAbsent:   []string{"Answer := "},
		},
		{
			name: "nested inside a struct pattern",
			src: `package main

struct Point(X int, Y int)

val Origin = 0

func main() {
    val p = Point(1, 2)
    val r = p match {
        case Point(Origin, y) => s"on the axis at $y"
        case _                => "other"
    }
    Println(r)
}`,
			wantContains: []string{"== Origin.Get()"},
			wantAbsent:   []string{"Origin := "},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileBareVariant(t, tc.src)
			require.NoError(t, err)
			for _, want := range tc.wantContains {
				require.Contains(t, out, want,
					"a capitalized in-scope value in pattern position must compare by equality")
			}
			for _, absent := range tc.wantAbsent {
				require.NotContains(t, out, absent,
					"a capitalized in-scope value in pattern position must not bind a fresh variable")
			}
		})
	}
}

// TestStableIdentifierPatternBoundary pins what stays a binding. Only a
// CAPITALIZED identifier naming an in-scope value is a stable identifier; a
// lowercase identifier is always a fresh binding (it may shadow an outer
// value), and a capitalized identifier that names no value binds as before.
func TestStableIdentifierPatternBoundary(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		wantBinds string
	}{
		{
			name: "lowercase name shadows an in-scope value",
			src: `package main

func main() {
    val limit = 10
    val r = 3 match {
        case limit => s"bound $limit"
    }
    Println(r)
}`,
			wantBinds: "limit := obj",
		},
		{
			name: "capitalized name that is not a value",
			src: `package main

func main() {
    val r = 3 match {
        case Other => s"bound $Other"
        case _     => "other"
    }
    Println(r)
}`,
			wantBinds: "Other := obj",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileBareVariant(t, tc.src)
			require.NoError(t, err)
			require.Contains(t, out, tc.wantBinds)
		})
	}
}
