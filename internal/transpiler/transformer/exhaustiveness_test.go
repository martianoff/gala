package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// TestExhaustivenessCountsOnlyFullCoverage pins that a variant is covered
// only when its cases match every value of its fields. A refutable pattern
// (`Some(0)`) or a guarded case counted as covering the whole variant, the
// match got a synthetic unreachable default, and other values panicked.
func TestExhaustivenessCountsOnlyFullCoverage(t *testing.T) {
	const decls = `package main

sealed type Shape {
    case Circle(Radius int)
    case Rect(Width int, Height int)
}

sealed type Pair {
    case Both(A bool, B bool)
}

sealed type Tree {
    case Leaf()
    case Node(Left Tree, Right Tree)
}

sealed type Cell {
    case Slot(Pos Tuple[bool, Option[int]])
}

struct Point(X int, Y int)

sealed type Mark {
    case At(P Point)
    case Nowhere()
}

val Zero = 0

type Flag bool
type AnyShape Shape
type Scored Tuple[int, bool]

struct Switches(On bool, Off bool)
`
	cases := []struct {
		name    string
		subject string
		arms    string
		// wantMissing is the missing-cases list, or "" for an exhaustive match.
		wantMissing string
		wantHint    string
	}{
		{
			name:        "literal field",
			subject:     "Option[int]",
			arms:        "case Some(0) => 1\n    case None() => 2",
			wantMissing: "Some(...)",
		},
		{
			name:        "stable identifier field",
			subject:     "Option[int]",
			arms:        "case Some(Zero) => 1\n    case None() => 2",
			wantMissing: "Some(...)",
		},
		{
			name:        "guarded case",
			subject:     "Option[int]",
			arms:        "case Some(n) if n > 0 => n\n    case None() => 2",
			wantMissing: "Some",
			wantHint:    "a case with an `if` guard does not count",
		},
		{
			name:        "nested variant left out",
			subject:     "Option[Option[int]]",
			arms:        "case Some(Some(_)) => 1\n    case None() => 2",
			wantMissing: "Some(...)",
		},
		{
			name:        "one field of several refutable",
			subject:     "Shape",
			arms:        "case Circle(_) => 1\n    case Rect(1, _) => 2",
			wantMissing: "Rect(...)",
		},
		{
			name:        "refutable alternative",
			subject:     "Option[int]",
			arms:        "case Some(0) | None() => 1",
			wantMissing: "Some(...)",
		},
		{
			name:    "nested variants together",
			subject: "Option[Option[int]]",
			arms:    "case Some(Some(_)) => 1\n    case Some(None()) => 2\n    case None() => 3",
		},
		{
			name:    "bool fields together",
			subject: "Pair",
			arms:    "case Both(true, _) => 1\n    case Both(false, true) => 2\n    case Both(false, false) => 3",
		},
		{
			name:    "alternatives inside a field",
			subject: "Option[bool]",
			arms:    "case Some(true | false) => 1\n    case None() => 2",
		},
		{
			name:    "capitalized binding",
			subject: "Option[int]",
			arms:    "case Some(Value) => Value\n    case None() => 2",
		},
		{
			name:    "bare zero-field variant",
			subject: "Option[int]",
			arms:    "case Some(_) => 1\n    case None => 2",
		},
		{
			name:    "typed field of the field's type",
			subject: "Option[int]",
			arms:    "case Some(x: int) => x\n    case None() => 2",
		},
		{
			name:    "recursive sealed type",
			subject: "Tree",
			arms:    "case Leaf() => 1\n    case Node(Leaf(), _) => 2\n    case Node(Node(_, _), _) => 3",
		},
		{
			name:        "recursive sealed type left partial",
			subject:     "Tree",
			arms:        "case Leaf() => 1\n    case Node(Leaf(), Leaf()) => 2",
			wantMissing: "Node(...)",
		},
		{
			name:    "tuple field",
			subject: "Cell",
			arms:    "case Slot((true, _)) => 1\n    case Slot((false, Some(_))) => 2\n    case Slot((false, None())) => 3",
		},
		{
			name:        "tuple field left partial",
			subject:     "Cell",
			arms:        "case Slot((true, _)) => 1\n    case Slot((_, None())) => 2",
			wantMissing: "Slot(...)",
		},
		{
			name:    "struct field read through its fields",
			subject: "Mark",
			arms:    "case At(Point(_, y)) => y\n    case Nowhere() => 0",
		},
		{
			name:        "struct field with a refutable field",
			subject:     "Mark",
			arms:        "case At(Point(0, _)) => 0\n    case Nowhere() => 1",
			wantMissing: "At(...)",
		},
		{
			name:    "alias of a sealed type",
			subject: "AnyShape",
			arms:    "case Circle(_) => 1\n    case Rect(_, _) => 2",
		},
		{
			name:        "alias of a sealed type left partial",
			subject:     "AnyShape",
			arms:        "case Circle(_) => 1",
			wantMissing: "Rect",
		},
		{
			name:    "alias of bool",
			subject: "Option[Flag]",
			arms:    "case Some(true) => 1\n    case Some(false) => 2\n    case None() => 3",
		},
		{
			name:    "alias of a tuple",
			subject: "Option[Scored]",
			arms:    "case Some((_, true)) => 1\n    case Some((_, false)) => 2\n    case None() => 3",
		},
		{
			name:    "struct fields held as Immutable",
			subject: "Option[Switches]",
			arms:    "case Some(Switches(true, _)) => 1\n    case Some(Switches(false, _)) => 2\n    case None() => 3",
		},
		{
			name:        "struct fields held as Immutable left partial",
			subject:     "Option[Switches]",
			arms:        "case Some(Switches(true, _)) => 1\n    case None() => 3",
			wantMissing: "Some(...)",
		},
		{
			name:    "guarded case backed by an unguarded one",
			subject: "Option[int]",
			arms:    "case Some(n) if n > 0 => n\n    case Some(_) => 0\n    case None() => 2",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + `
func f(x ` + tc.subject + `) int = x match {
    ` + tc.arms + `
}

func main() {
    Println("ok")
}`
			_, err := transpileBareVariant(t, src)
			if tc.wantMissing == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), string(galaerr.CodeNonExhaustiveMatch))
			require.Contains(t, err.Error(), "missing cases: "+tc.wantMissing)
			if tc.wantHint != "" {
				require.Contains(t, err.Error(), tc.wantHint)
			}
		})
	}
}

// TestExhaustivenessInGenericFunction pins coverage when the variant's field
// types are type parameters of the enclosing function.
func TestExhaustivenessInGenericFunction(t *testing.T) {
	src := `package main

func depth[T any](o Option[Option[T]]) int = o match {
    case Some(Some(_)) => 2
    case Some(None())  => 1
    case None()        => 0
}

func main() {
    Println(depth(Some(Some(1))))
}`
	_, err := transpileBareVariant(t, src)
	require.NoError(t, err)
}

// TestExhaustivenessGuardHintOnlyForGuardedVariant pins that the hint about
// guarded cases is given only when a guarded case names a missing variant.
func TestExhaustivenessGuardHintOnlyForGuardedVariant(t *testing.T) {
	src := `package main

sealed type Shape {
    case Circle(Radius int)
    case Rect(Width int, Height int)
}

func f(s Shape) int = s match {
    case Circle(r) if r > 0 => r
    case Circle(_)          => 0
}

func main() {
    Println(f(Circle(1)))
}`
	_, err := transpileBareVariant(t, src)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing cases: Rect")
	require.NotContains(t, err.Error(), "guard")
}

// TestExhaustivenessSequenceFieldIsRefutable pins that a sequence pattern in a
// field covers only the sequences of its length: a sequence is a struct, but
// its pattern matches elements, not its fields.
func TestExhaustivenessSequenceFieldIsRefutable(t *testing.T) {
	src := `package main

import . "martianoff/gala/collection_immutable"

func f(o Option[Array[int]]) int = o match {
    case Some(Array(x, y)) => x + y
    case None()            => 0
}

func main() {
    Println(f(None[Array[int]]()))
}`
	_, err := transpileBareVariant(t, src)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing cases: Some(...)")
}
