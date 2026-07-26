package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newConcurrencyTranspiler builds a full transpiler wired to the std search path
// so the Sendable boundary marker resolves and the collection packages are
// available for the capture-safety cases.
func newConcurrencyTranspiler() *transpiler.GalaToGoTranspiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return transpiler.NewGalaToGoTranspiler(p, a, tr, g)
}

// TestSendableCaptureErrors covers PR3 enforcement: a closure (explicit lambda
// or by-name thunk) passed to a `Sendable[F]` boundary may only capture `val`
// bindings of Shareable types. Each negative case must fail with GALA-E0037 and
// name the offending capture; each positive case must transpile cleanly.
//
// The boundary here is a locally-declared function `func run(body Sendable[...])`
// — proving the check keys off the marker type, not off std function names.
func TestSendableCaptureErrors(t *testing.T) {
	trans := newConcurrencyTranspiler()

	type errCase struct {
		name           string
		input          string
		expectContains string // substring the message must contain (beyond the code)
		expectCapture  string // the identifier the caret must point at
	}

	cases := []errCase{
		{
			name: "var capture is a reassignment race",
			input: `package main

func run(body Sendable[func() int]) int = body()

func main() {
    var counter = 0
    Println(run(() => counter + 1))
}`,
			expectContains: "reassignable var",
			expectCapture:  "counter",
		},
		{
			name: "var capture via thunk sugar",
			input: `package main

func run(body Sendable[func() int]) int = body()

func main() {
    var counter = 0
    Println(run(counter + 1))
}`,
			expectContains: "reassignable var",
			expectCapture:  "counter",
		},
		{
			name: "collection_mutable capture is a mutable-pointee race",
			input: `package main

import . "martianoff/gala/collection_mutable"

func run(body Sendable[func() int]) int = body()

func main() {
    val buffer = ArrayOf(1, 2, 3)
    Println(run(() => buffer.Size()))
}`,
			expectContains: "not safe to share",
			expectCapture:  "buffer",
		},
		{
			name: "struct with a var field is not shareable",
			input: `package main

struct Counter(var n int)

func run(body Sendable[func() int]) int = body()

func main() {
    val c = Counter(0)
    Println(run(() => c.n))
}`,
			expectContains: "not safe to share",
			expectCapture:  "c",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.input, "concurrency_check_test.gala")
			require.Error(t, err, "expected the boundary capture to be rejected")
			msg := err.Error()
			assert.Contains(t, msg, string(galaerr.CodeUnshareableCapture),
				"error must carry the stable code GALA-E0037")
			assert.Contains(t, msg, tc.expectContains,
				"error must explain WHY the capture is unsafe")
			assert.Contains(t, msg, tc.expectCapture,
				"error must name the offending capture")

			// The caret must point exactly at the offending capture identifier:
			// read the source at the reported (line, column) and confirm it
			// begins with that identifier. Column is 0-based (ANTLR convention).
			var semErr *galaerr.SemanticError
			require.ErrorAs(t, err, &semErr, "expected a coded SemanticError")
			assert.Equal(t, galaerr.CodeUnshareableCapture, semErr.Code)
			srcLines := strings.Split(tc.input, "\n")
			require.GreaterOrEqual(t, semErr.Line, 1)
			require.LessOrEqual(t, semErr.Line, len(srcLines))
			lineText := srcLines[semErr.Line-1]
			require.LessOrEqual(t, semErr.Column, len(lineText))
			assert.True(t, strings.HasPrefix(lineText[semErr.Column:], tc.expectCapture),
				"caret at %d:%d should point at %q, got %q",
				semErr.Line, semErr.Column, tc.expectCapture, lineText[semErr.Column:])
		})
	}
}

// TestSendableCaptureAllowed is the negative half: safe captures — immutable
// collections, strings, ints, and top-level symbols — must transpile cleanly.
func TestSendableCaptureAllowed(t *testing.T) {
	trans := newConcurrencyTranspiler()

	cases := []struct {
		name  string
		input string
	}{
		{
			name: "immutable-int val capture",
			input: `package main

func run(body Sendable[func() int]) int = body()

func main() {
    val base = 41
    Println(run(() => base + 1))
}`,
		},
		{
			name: "immutable-collection capture",
			input: `package main

import . "martianoff/gala/collection_immutable"

func run(body Sendable[func() int]) int = body()

func main() {
    val xs = ArrayOf(1, 2, 3)
    Println(run(() => xs.Size()))
}`,
		},
		{
			name: "thunk sugar with immutable capture",
			input: `package main

func run(body Sendable[func() int]) int = body()

func main() {
    val label = "hi"
    Println(run(label.Size()))
}`,
		},
		{
			name: "capturing a top-level function is not a capture race",
			input: `package main

func helper() int = 7

func run(body Sendable[func() int]) int = body()

func main() {
    Println(run(() => helper()))
}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "concurrency_check_ok.gala")
			require.NoError(t, err, "safe captures must transpile cleanly")
			assert.NotEmpty(t, out)
			// The transparent marker must never leak into generated Go.
			assert.NotContains(t, out, "Sendable", "Sendable must not appear in generated Go")
		})
	}
}

// TestSendableForwardedIntoFuture covers Sendable propagation across a NESTED
// boundary: a wrapper takes a caller-supplied computation typed
// Sendable[func() int] and forwards it into a real concurrent.Future (itself a
// Sendable[func() T] boundary). Because the parameter is Sendable-typed,
// capturing it into the further Future boundary is accepted — in BOTH the
// explicit-lambda form and the by-name thunk-sugar form. The capture-safety
// obligation lands at the CALL SITE where the actual closure literal is written,
// not at the forward.
func TestSendableForwardedIntoFuture(t *testing.T) {
	trans := newConcurrencyTranspiler()

	cases := []struct {
		name  string
		input string
	}{
		{
			name: "explicit lambda forwards Sendable param",
			input: `package main

import . "martianoff/gala/concurrent"

func afterCompute(compute Sendable[func() int]) Future[int] =
    Future(() => compute() + 1)

func main() {
    val base = 41
    Println(afterCompute(() => base).Get())
}`,
		},
		{
			name: "thunk sugar forwards Sendable param",
			input: `package main

import . "martianoff/gala/concurrent"

func afterComputeThunk(compute Sendable[func() int]) Future[int] =
    Future(compute() + 1)

func main() {
    val base = 41
    Println(afterComputeThunk(() => base).Get())
}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "sendable_forward.gala")
			require.NoError(t, err, "forwarding a Sendable param into a Future must transpile cleanly")
			assert.NotEmpty(t, out)
			// The transparent marker must never leak into generated Go.
			assert.NotContains(t, out, "Sendable", "Sendable must not appear in generated Go")
		})
	}
}

// ---------------------------------------------------------------------------
// Expanded corner-case coverage (structs, sealed/std wrappers, Go interop,
// collections & wrappers, binding kinds & capture forms, negatives).
//
// Each case is a full GALA program transpiled end-to-end. A reject case must
// fail with GALA-E0037, explain WHY, name the offending capture, and (unless
// skipCaret) put the caret exactly on that capture identifier. An accept case
// must transpile cleanly and never leak the Sendable marker into the Go output.
// ---------------------------------------------------------------------------

// sendableRejectCase is a program expected to be rejected with GALA-E0037.
type sendableRejectCase struct {
	name           string
	input          string
	expectContains string // substring the message must contain (beyond the code)
	expectCapture  string // the identifier the caret must point at / message must name
	skipCaret      bool   // interpolation captures re-parse a fragment, so the caret
	// position is not the original-source column; only the name is asserted.
}

// assertSendableRejected drives the reject-case assertions shared by every
// corner-case table: the coded error, the explanation, the named capture, and
// (unless skipped) the caret pointing at the exact offending identifier.
func assertSendableRejected(t *testing.T, trans *transpiler.GalaToGoTranspiler, tc sendableRejectCase) {
	t.Helper()
	_, err := trans.Transpile(tc.input, "concurrency_check_corner.gala")
	require.Error(t, err, "expected the boundary capture to be rejected")
	msg := err.Error()
	assert.Contains(t, msg, string(galaerr.CodeUnshareableCapture),
		"error must carry the stable code GALA-E0037")
	if tc.expectContains != "" {
		assert.Contains(t, msg, tc.expectContains, "error must explain WHY the capture is unsafe")
	}
	assert.Contains(t, msg, tc.expectCapture, "error must name the offending capture")

	var semErr *galaerr.SemanticError
	require.ErrorAs(t, err, &semErr, "expected a coded SemanticError")
	assert.Equal(t, galaerr.CodeUnshareableCapture, semErr.Code)
	if tc.skipCaret {
		return
	}
	// The caret must point exactly at the offending capture identifier: read the
	// source at the reported (line, column) and confirm it begins with that
	// identifier. Column is 0-based (ANTLR convention).
	srcLines := strings.Split(tc.input, "\n")
	require.GreaterOrEqual(t, semErr.Line, 1)
	require.LessOrEqual(t, semErr.Line, len(srcLines))
	lineText := srcLines[semErr.Line-1]
	require.LessOrEqual(t, semErr.Column, len(lineText))
	assert.True(t, strings.HasPrefix(lineText[semErr.Column:], tc.expectCapture),
		"caret at %d:%d should point at %q, got %q",
		semErr.Line, semErr.Column, tc.expectCapture, lineText[semErr.Column:])
}

// assertSendableAccepted drives the accept-case assertions: clean transpile and
// no leak of the transparent marker into the generated Go.
func assertSendableAccepted(t *testing.T, trans *transpiler.GalaToGoTranspiler, input string) {
	t.Helper()
	out, err := trans.Transpile(input, "concurrency_check_corner_ok.gala")
	require.NoError(t, err, "safe captures must transpile cleanly")
	assert.NotEmpty(t, out)
	assert.NotContains(t, out, "Sendable", "Sendable must not appear in generated Go")
}

// TestSendableStructCaptures exercises struct shareability at the boundary:
// all-val shareable fields, a var field, immutable/mutable collection fields,
// transitive nested-struct fields, generic-struct instantiations, and a
// Go-interop-typed field.
func TestSendableStructCaptures(t *testing.T) {
	trans := newConcurrencyTranspiler()

	rejects := []sendableRejectCase{
		{
			name: "struct with a var field",
			input: `package main

struct MutBox(var n int)

func run(body Sendable[func() int]) int = body()

func main() {
    val b = MutBox(0)
    Println(run(() => b.n))
}`,
			expectContains: "not safe to share",
			expectCapture:  "b",
		},
		{
			name: "struct with a collection_mutable field",
			input: `package main

import cm "martianoff/gala/collection_mutable"

struct Bag(data cm.Array[int])

func run(body Sendable[func() int]) int = body()

func main() {
    val bag = Bag(cm.ArrayOf(1, 2, 3))
    Println(run(() => bag.data.Size()))
}`,
			expectContains: "not safe to share",
			expectCapture:  "bag",
		},
		{
			name: "nested struct whose inner struct has a var field (transitive)",
			input: `package main

struct Inner(var flag bool)

struct Outer(inner Inner)

func run(body Sendable[func() bool]) bool = body()

func main() {
    val o = Outer(Inner(false))
    Println(run(() => o.inner.flag))
}`,
			expectContains: "not safe to share",
			expectCapture:  "o",
		},
		{
			name: "generic struct instantiated at a mutable collection",
			input: `package main

import cm "martianoff/gala/collection_mutable"

struct Box[T any](v T)

func run(body Sendable[func() int]) int = body()

func main() {
    val boxed = Box[cm.Array[int]](cm.ArrayOf(1, 2, 3))
    Println(run(() => boxed.v.Size()))
}`,
			expectContains: "not safe to share",
			expectCapture:  "boxed",
		},
		{
			name: "struct with a Go-interop-typed field",
			input: `package main

import gi "martianoff/gala/go_interop"

struct Holder(tok gi.CancelToken)

func run(body Sendable[func() bool]) bool = body()

func main() {
    val h = Holder(gi.NewCancelToken())
    Println(run(() => gi.IsCancelled(h.tok)))
}`,
			expectContains: "not safe to share",
			expectCapture:  "h",
		},
	}
	for _, tc := range rejects {
		t.Run("reject/"+tc.name, func(t *testing.T) { assertSendableRejected(t, trans, tc) })
	}

	accepts := []struct {
		name  string
		input string
	}{
		{
			name: "struct with all-val shareable fields",
			input: `package main

struct Point(x int, y int)

func run(body Sendable[func() int]) int = body()

func main() {
    val p = Point(1, 2)
    Println(run(() => p.x + p.y))
}`,
		},
		{
			name: "struct with an immutable-collection field",
			input: `package main

import . "martianoff/gala/collection_immutable"

struct Config(items Array[int])

func run(body Sendable[func() int]) int = body()

func main() {
    val c = Config(ArrayOf(1, 2, 3))
    Println(run(() => c.items.Size()))
}`,
		},
		{
			name: "generic struct instantiated at int",
			input: `package main

struct Box[T any](v T)

func run(body Sendable[func() int]) int = body()

func main() {
    val boxed = Box[int](7)
    Println(run(() => boxed.v + 1))
}`,
		},
	}
	for _, tc := range accepts {
		t.Run("accept/"+tc.name, func(t *testing.T) { assertSendableAccepted(t, trans, tc.input) })
	}
}

// appModelPreamble is the shared header for the field-access-sensitive cases. It
// mirrors the real-world shape that motivated field sensitivity: an application
// model whose OVERALL type is unshareable (it holds `sessions`, a mutable
// collection, and a `policy` that itself carries a `var` field), yet whose
// immutable projections (`team`, `statuses`, `policy.retries`) are perfectly safe
// to read off-thread.
const appModelPreamble = `package main

import (
    . "martianoff/gala/collection_immutable"
    cm "martianoff/gala/collection_mutable"
)

struct Policy(retries int, var override bool)

struct AppModel(team string, statuses Array[int], policy Policy, sessions cm.Array[int])

func (m AppModel) label() string = m.team

func run(body Sendable[func() int]) int = body()

func runBool(body Sendable[func() bool]) bool = body()

func g(a string, b Array[int]) int = a.Size() + b.Size()

func sz(a cm.Array[int]) int = a.Size()

func consume(x AppModel) int = x.statuses.Size()

func mkModel() AppModel = AppModel("qa", ArrayOf(1, 2, 3), Policy(2, false), cm.ArrayOf(9))

`

// TestSendableFieldAccessSensitive is the headline coverage for field-access
// sensitivity: capturing an otherwise-unshareable value but reading only its
// immutable fields is accepted, with no `val`-snapshot workaround. Its mirror
// negatives confirm the sound boundary: reading a mutable field, a nested var
// field, using the value whole, or calling a method on it is still rejected, and
// a reassignable `var` is rejected even through a pure immutable path.
func TestSendableFieldAccessSensitive(t *testing.T) {
	trans := newConcurrencyTranspiler()

	accepts := []struct {
		name  string
		input string
	}{
		{
			// The qaPromptReadyCmd shape: `Future(() => g(m.A, m.B))` — the whole
			// AppModel is unshareable, but only its immutable `team`/`statuses`
			// fields are read off-thread, so it is accepted with NO snapshot vals.
			name:  "reads only immutable fields of an unshareable struct",
			input: appModelPreamble + `func main() {
    val m = mkModel()
    Println(run(() => g(m.team, m.statuses)))
}`,
		},
		{
			// A nested immutable path, immutable through two hops (policy is a val
			// field; retries is a val field of shareable type int).
			name:  "reads a nested immutable field path",
			input: appModelPreamble + `func main() {
    val m = mkModel()
    Println(run(() => m.policy.retries + 1))
}`,
		},
		{
			// Reading only the immutable V1 slot of a tuple whose OTHER slot is a
			// mutable collection is race-free — the old coarse whole-type check
			// rejected this; field sensitivity now accepts it.
			name: "reads only the immutable slot of a partly-mutable tuple",
			input: `package main

import cm "martianoff/gala/collection_mutable"

func run(body Sendable[func() int]) int = body()

func main() {
    val pair = Tuple(1, cm.ArrayOf(1, 2, 3))
    Println(run(() => pair.V1 + 1))
}`,
		},
		{
			// Regression: an all-immutable struct used WHOLE (matched) is still
			// accepted via the whole-type Shareable check.
			name: "all-immutable struct used whole is accepted",
			input: `package main

struct Point(x int, y int)

func run(body Sendable[func() int]) int = body()

func main() {
    val p = Point(1, 2)
    Println(run(() => p match {
        case _ => p.x + p.y
    }))
}`,
		},
	}
	for _, tc := range accepts {
		t.Run("accept/"+tc.name, func(t *testing.T) { assertSendableAccepted(t, trans, tc.input) })
	}

	rejects := []sendableRejectCase{
		{
			// Reading the mutable-collection field `sessions` (a val field whose
			// TYPE is unshareable) — the accessed path is unshareable.
			name:  "reads a mutable-collection field",
			input: appModelPreamble + `func main() {
    val m = mkModel()
    Println(run(() => sz(m.sessions)))
}`,
			expectContains: "not safe to share",
			expectCapture:  "m",
		},
		{
			// A nested path whose final hop is a `var` field — reading a
			// reassignable field races with its reassignment.
			name:  "reads a nested mutable (var) field",
			input: appModelPreamble + `func main() {
    val m = mkModel()
    Println(runBool(() => m.policy.override))
}`,
			expectContains: "mutable (`var`) field",
			expectCapture:  "m",
		},
		{
			// The value is used WHOLE (passed as a function argument), so the whole
			// unshareable type is rejected.
			name:  "uses the capture whole",
			input: appModelPreamble + `func main() {
    val m = mkModel()
    Println(run(() => consume(m)))
}`,
			expectContains: "not safe to share",
			expectCapture:  "m",
		},
		{
			// A METHOD call on the capture (`m.label()`) is a whole use — a method
			// may read/write mutable internals — so the whole type is rejected.
			name:  "calls a method on the capture",
			input: appModelPreamble + `func main() {
    val m = mkModel()
    Println(run(() => m.label().Size()))
}`,
			expectContains: "not safe to share",
			expectCapture:  "m",
		},
		{
			// A reassignable `var` accessed only via an immutable field path is
			// STILL rejected: the binding slot itself can be rebound.
			name:  "var binding accessed via immutable path is still rejected",
			input: appModelPreamble + `func main() {
    var m = mkModel()
    Println(run(() => g(m.team, m.statuses)))
}`,
			expectContains: "reassignable var",
			expectCapture:  "m",
		},
	}
	for _, tc := range rejects {
		t.Run("reject/"+tc.name, func(t *testing.T) { assertSendableRejected(t, trans, tc) })
	}
}

// TestSendableSealedAndWrapperCaptures covers sealed types and the std value
// wrappers (Option / Try / Either / TupleN) at the boundary.
func TestSendableSealedAndWrapperCaptures(t *testing.T) {
	trans := newConcurrencyTranspiler()

	rejects := []sendableRejectCase{
		{
			name: "sealed type with a mutable-collection variant field",
			input: `package main

import cm "martianoff/gala/collection_mutable"

sealed type Event {
    case Tick
    case Batch(items cm.Array[int])
}

func run(body Sendable[func() int]) int = body()

func main() {
    val e = Batch(cm.ArrayOf(1, 2, 3))
    Println(run(() => e match {
        case _ => 1
    }))
}`,
			expectContains: "not safe to share",
			expectCapture:  "e",
		},
		{
			name: "Option holding a mutable collection",
			input: `package main

import cm "martianoff/gala/collection_mutable"

func run(body Sendable[func() int]) int = body()

func main() {
    val opt = Some(cm.ArrayOf(1, 2, 3))
    Println(run(() => opt match {
        case Some(_) => 1
        case _ => 0
    }))
}`,
			expectContains: "not safe to share",
			expectCapture:  "opt",
		},
		{
			name: "tuple containing a mutable collection, matched whole",
			input: `package main

import cm "martianoff/gala/collection_mutable"

func run(body Sendable[func() int]) int = body()

func main() {
    val pair = Tuple(1, cm.ArrayOf(1, 2, 3))
    Println(run(() => pair match {
        case _ => 1
    }))
}`,
			expectContains: "not safe to share",
			expectCapture:  "pair",
		},
	}
	for _, tc := range rejects {
		t.Run("reject/"+tc.name, func(t *testing.T) { assertSendableRejected(t, trans, tc) })
	}

	accepts := []struct {
		name  string
		input string
	}{
		{
			name: "sealed type with all-shareable variants",
			input: `package main

sealed type Shape {
    case Circle(r int)
    case Square(side int)
}

func run(body Sendable[func() int]) int = body()

func main() {
    val s = Circle(3)
    Println(run(() => s match {
        case _ => 1
    }))
}`,
		},
		{
			name: "Option of int",
			input: `package main

func run(body Sendable[func() int]) int = body()

func main() {
    val opt = Some(42)
    Println(run(() => opt.GetOrElse(0)))
}`,
		},
		{
			name: "Try of int",
			input: `package main

func run(body Sendable[func() int]) int = body()

func main() {
    val res = Success(42)
    Println(run(() => res.GetOrElse(0)))
}`,
		},
		{
			name: "Either of string and int",
			input: `package main

func run(body Sendable[func() int]) int = body()

func main() {
    val e Either[string, int] = Right(7)
    Println(run(() => e match {
        case Right(v) => v
        case Left(_) => 0
    }))
}`,
		},
		{
			name: "Tuple2 of int and string",
			input: `package main

func run(body Sendable[func() int]) int = body()

func main() {
    val pair = Tuple(1, "two")
    Println(run(() => pair.V1))
}`,
		},
	}
	for _, tc := range accepts {
		t.Run("accept/"+tc.name, func(t *testing.T) { assertSendableAccepted(t, trans, tc.input) })
	}
}

// TestSendableInteropCaptures covers values whose types come from Go interop: a
// Go pointer / slice / map value is unshareable, while a Go primitive obtained
// from a Go call is shareable.
func TestSendableInteropCaptures(t *testing.T) {
	trans := newConcurrencyTranspiler()

	rejects := []sendableRejectCase{
		{
			// An opaque Go value-struct whose only fields are unexported (a
			// context + a cancel func). GALA cannot see that mutable state, so
			// the type must be treated as unshareable rather than as a
			// vacuously-shareable field-less struct.
			name: "captured opaque Go value-struct",
			input: `package main

import gi "martianoff/gala/go_interop"

func run(body Sendable[func() bool]) bool = body()

func main() {
    val tok = gi.NewCancelToken()
    Println(run(() => gi.IsCancelled(tok)))
}`,
			expectContains: "not safe to share",
			expectCapture:  "tok",
		},
		{
			name: "captured Go pointer value",
			input: `package main

import gi "martianoff/gala/go_interop"

func run(body Sendable[func() int]) int = body()

func main() {
    val m = gi.NewMutex()
    Println(run(() => {
        m.Lock()
        m.Unlock()
        0
    }))
}`,
			expectContains: "not safe to share",
			expectCapture:  "m",
		},
		{
			name: "captured Go slice value",
			input: `package main

import gi "martianoff/gala/go_interop"

func run(body Sendable[func() int]) int = body()

func main() {
    val s = gi.SliceOf(1, 2, 3)
    Println(run(() => gi.SliceCap(s)))
}`,
			expectContains: "not safe to share",
			expectCapture:  "s",
		},
		{
			name: "captured Go map value",
			input: `package main

import gi "martianoff/gala/go_interop"

func run(body Sendable[func() int]) int = body()

func main() {
    val mp = gi.MapEmpty[string, int]()
    Println(run(() => gi.MapLen(mp)))
}`,
			expectContains: "not safe to share",
			expectCapture:  "mp",
		},
	}
	for _, tc := range rejects {
		t.Run("reject/"+tc.name, func(t *testing.T) { assertSendableRejected(t, trans, tc) })
	}

	accepts := []struct {
		name  string
		input string
	}{
		{
			name: "captured Go primitive int from a Go call",
			input: `package main

import gi "martianoff/gala/go_interop"

func run(body Sendable[func() int]) int = body()

func main() {
    val n = gi.SliceCap(gi.SliceOf(1, 2, 3))
    Println(run(() => n + 1))
}`,
		},
		{
			name: "captured Go string from a Go call",
			input: `package main

import gi "martianoff/gala/go_interop"

func run(body Sendable[func() int]) int = body()

func main() {
    val s = gi.ToString(gi.ToBytes("hello"))
    Println(run(() => s.Size()))
}`,
		},
	}
	for _, tc := range accepts {
		t.Run("accept/"+tc.name, func(t *testing.T) { assertSendableAccepted(t, trans, tc.input) })
	}
}

// TestSendableCollectionCaptures covers every immutable collection (accept) and
// its mutable counterpart (reject), nested collections, and the Immutable /
// ConstPtr wrappers.
func TestSendableCollectionCaptures(t *testing.T) {
	trans := newConcurrencyTranspiler()

	// Each mutable collection constructor, captured, must be rejected.
	mutableCtors := []struct {
		name string
		ctor string
	}{
		{"mutable Array", "cm.ArrayOf(1, 2, 3)"},
		{"mutable List", "cm.ListOf(1, 2, 3)"},
		{"mutable HashSet", "cm.HashSetOf(1, 2, 3)"},
		{"mutable HashMap", `cm.HashMapOf(("a", 1))`},
		{"mutable TreeSet", "cm.TreeSetOf(1, 2, 3)"},
		{"mutable TreeMap", `cm.TreeMapOf(("a", 1))`},
	}
	for _, mc := range mutableCtors {
		tc := sendableRejectCase{
			name: mc.name,
			input: `package main

import cm "martianoff/gala/collection_mutable"

func run(body Sendable[func() int]) int = body()

func main() {
    val coll = ` + mc.ctor + `
    Println(run(() => coll.Size()))
}`,
			expectContains: "not safe to share",
			expectCapture:  "coll",
		}
		t.Run("reject/"+mc.name, func(t *testing.T) { assertSendableRejected(t, trans, tc) })
	}

	rejects := []sendableRejectCase{
		{
			name: "immutable Array of mutable Array (nested)",
			input: `package main

import . "martianoff/gala/collection_immutable"
import cm "martianoff/gala/collection_mutable"

func run(body Sendable[func() int]) int = body()

func main() {
    val nested = ArrayOf(cm.ArrayOf(1, 2, 3))
    Println(run(() => nested.Size()))
}`,
			expectContains: "not safe to share",
			expectCapture:  "nested",
		},
		{
			name: "Immutable wrapping a mutable collection",
			input: `package main

import cm "martianoff/gala/collection_mutable"

func run(body Sendable[func() int]) int = body()

func main() {
    val im = NewImmutable(cm.ArrayOf(1, 2, 3))
    Println(run(() => im.Get().Size()))
}`,
			expectContains: "not safe to share",
			expectCapture:  "im",
		},
	}
	for _, tc := range rejects {
		t.Run("reject/"+tc.name, func(t *testing.T) { assertSendableRejected(t, trans, tc) })
	}

	// Each immutable collection constructor, captured, must be accepted.
	immutableCtors := []struct {
		name string
		ctor string
	}{
		{"immutable Array", "ArrayOf(1, 2, 3)"},
		{"immutable List", "ListOf(1, 2, 3)"},
		{"immutable HashSet", "HashSetOf(1, 2, 3)"},
		{"immutable HashMap", `HashMapOf(("a", 1))`},
		{"immutable TreeSet", "TreeSetOf(1, 2, 3)"},
		{"immutable TreeMap", `TreeMapOf(("a", 1))`},
	}
	for _, ic := range immutableCtors {
		input := `package main

import . "martianoff/gala/collection_immutable"

func run(body Sendable[func() int]) int = body()

func main() {
    val coll = ` + ic.ctor + `
    Println(run(() => coll.Size()))
}`
		t.Run("accept/"+ic.name, func(t *testing.T) { assertSendableAccepted(t, trans, input) })
	}

	accepts := []struct {
		name  string
		input string
	}{
		{
			name: "immutable Array of immutable Array (nested)",
			input: `package main

import . "martianoff/gala/collection_immutable"

func run(body Sendable[func() int]) int = body()

func main() {
    val nested = ArrayOf(ArrayOf(1, 2, 3))
    Println(run(() => nested.Size()))
}`,
		},
		{
			name: "Immutable wrapping an int",
			input: `package main

func run(body Sendable[func() int]) int = body()

func main() {
    val im = NewImmutable(41)
    Println(run(() => im.Get() + 1))
}`,
		},
		{
			name: "ConstPtr of string",
			input: `package main

import gi "martianoff/gala/go_interop"

func run(body Sendable[func() int]) int = body()

func main() {
    val c = NewConstPtr[string](gi.New[string]())
    Println(run(() => c.Deref().Size()))
}`,
		},
	}
	for _, tc := range accepts {
		t.Run("accept/"+tc.name, func(t *testing.T) { assertSendableAccepted(t, trans, tc.input) })
	}
}

// TestSendableCaptureForms covers the binding-kind and capture-form matrix:
// var vs val, lambda parameters, explicit-lambda vs thunk-sugar equivalence,
// string-interpolation captures, nested lambdas, multiple captures, a non-zero
// boundary argument index, captures threaded through an inner call, and the
// named-argument call form.
func TestSendableCaptureForms(t *testing.T) {
	trans := newConcurrencyTranspiler()

	rejects := []sendableRejectCase{
		{
			name: "explicit lambda capturing a var",
			input: `package main

func run(body Sendable[func() int]) int = body()

func main() {
    var counter = 0
    Println(run(() => counter + 1))
}`,
			expectContains: "reassignable var",
			expectCapture:  "counter",
		},
		{
			name: "thunk sugar capturing the same var (identical result)",
			input: `package main

func run(body Sendable[func() int]) int = body()

func main() {
    var counter = 0
    Println(run(counter + 1))
}`,
			expectContains: "reassignable var",
			expectCapture:  "counter",
		},
		{
			name: "lambda parameter of a non-shareable type",
			input: `package main

struct MutBox(var n int)

func run(body Sendable[func() int]) int = body()

func apply(f func(MutBox) int) int = f(MutBox(0))

func main() {
    Println(apply((p) => run(() => p.n)))
}`,
			expectContains: "not safe to share",
			expectCapture:  "p",
		},
		{
			name: "string interpolation capturing a var",
			input: `package main

func run(body Sendable[func() string]) string = body()

func main() {
    var counter = 0
    Println(run(s"x=$counter"))
}`,
			expectContains: "reassignable var",
			expectCapture:  "counter",
			skipCaret:      true,
		},
		{
			name: "nested lambda capturing an outer var",
			input: `package main

func run(body Sendable[func() int]) int = body()

func main() {
    var counter = 0
    Println(run(() => {
        val f = () => counter + 1
        f()
    }))
}`,
			expectContains: "reassignable var",
			expectCapture:  "counter",
		},
		{
			name: "multiple unsafe captures report the first",
			input: `package main

func run(body Sendable[func() int]) int = body()

func main() {
    var a = 1
    var b = 2
    Println(run(() => a + b))
}`,
			expectContains: "reassignable var",
			expectCapture:  "a",
		},
		{
			name: "boundary at a non-zero argument index",
			input: `package main

func runAt(ec int, body Sendable[func() int]) int = body()

func main() {
    var counter = 0
    Println(runAt(0, () => counter + 1))
}`,
			expectContains: "reassignable var",
			expectCapture:  "counter",
		},
		{
			name: "capture threaded through an inner call",
			input: `package main

func addOne(x int) int = x + 1

func run(body Sendable[func() int]) int = body()

func main() {
    var counter = 0
    Println(run(() => addOne(counter)))
}`,
			expectContains: "reassignable var",
			expectCapture:  "counter",
		},
		{
			name: "named-argument call form",
			input: `package main

func run(body Sendable[func() int]) int = body()

func main() {
    var counter = 0
    Println(run(body = () => counter + 1))
}`,
			expectContains: "reassignable var",
			expectCapture:  "counter",
		},
	}
	for _, tc := range rejects {
		t.Run("reject/"+tc.name, func(t *testing.T) { assertSendableRejected(t, trans, tc) })
	}

	accepts := []struct {
		name  string
		input string
	}{
		{
			name: "lambda parameter of a shareable type",
			input: `package main

func run(body Sendable[func() int]) int = body()

func apply(f func(int) int) int = f(5)

func main() {
    Println(apply((p) => run(() => p + 1)))
}`,
		},
		{
			name: "closure own parameters are not captures",
			input: `package main

func run(body Sendable[func(int) int]) int = body(5)

func main() {
    Println(run((n) => n + 1))
}`,
		},
	}
	for _, tc := range accepts {
		t.Run("accept/"+tc.name, func(t *testing.T) { assertSendableAccepted(t, trans, tc.input) })
	}
}

// TestSendableNegativesExtended covers references that must NOT be treated as
// unsafe captures: top-level type / package symbols, locals declared inside the
// closure body, and an immutable snapshot of a mutable value.
func TestSendableNegativesExtended(t *testing.T) {
	trans := newConcurrencyTranspiler()

	accepts := []struct {
		name  string
		input string
	}{
		{
			name: "referencing a top-level type is not a capture",
			input: `package main

struct Point(x int, y int)

func run(body Sendable[func() int]) int = body()

func main() {
    Println(run(() => Point(1, 2).x))
}`,
		},
		{
			name: "referencing an imported package symbol is not a capture",
			input: `package main

import gi "martianoff/gala/go_interop"

func run(body Sendable[func() int]) int = body()

func main() {
    Println(run(() => gi.SliceCap(gi.SliceOf(1, 2, 3))))
}`,
		},
		{
			name: "a val declared inside the closure body is not a capture",
			input: `package main

func run(body Sendable[func() int]) int = body()

func main() {
    Println(run(() => {
        val local = 5
        local + 1
    }))
}`,
		},
		{
			name: "a var declared inside the closure body is not a capture",
			input: `package main

func run(body Sendable[func() int]) int = body()

func main() {
    Println(run(() => {
        var local = 0
        local + 1
    }))
}`,
		},
		{
			name: "capturing an immutable snapshot of a mutable value",
			input: `package main

import cm "martianoff/gala/collection_mutable"

func run(body Sendable[func() int]) int = body()

func main() {
    val buffer = cm.ArrayOf(1, 2, 3)
    val snapshot = buffer.Size()
    Println(run(() => snapshot + 1))
}`,
		},
	}
	for _, tc := range accepts {
		t.Run("accept/"+tc.name, func(t *testing.T) { assertSendableAccepted(t, trans, tc.input) })
	}
}

// TestSendableFunctionValueCaptures covers the Sendable-propagation rule for
// captured/forwarded FUNCTION VALUES (the Rust `Send`-style bound):
//
//   - A parameter declared `Sendable[func() T]` is a caller-vouched safe closure.
//     Forwarding it across a further concurrency boundary — captured inside a
//     Future body, or passed directly as the boundary argument — is ACCEPTED,
//     because the vouch was already discharged at the call site that supplied the
//     real closure literal.
//   - A BARE `func() T` parameter carries no such guarantee, so forwarding it is
//     REJECTED with GALA-E0037, and the message points the author at `Sendable`.
//
// This is what makes the canonical concurrency-wrapper pattern (a wrapper that
// forwards a caller-provided `compute` into a Future, e.g. CancellableAsync /
// AfterDelay) type-check. The boundary here is a locally-declared `run` so the
// check keys off the `Sendable` marker, not off any std function name.
func TestSendableFunctionValueCaptures(t *testing.T) {
	trans := newConcurrencyTranspiler()

	rejects := []sendableRejectCase{
		{
			// A bare `func() int` parameter forwarded directly into a boundary:
			// nothing vouches for its captures, so it must be rejected with the
			// DISTINCT function-value wording (not the mutable-pointee message).
			name: "bare func param forwarded to a boundary is rejected",
			input: `package main

func run(body Sendable[func() int]) int = body()

func forward(compute func() int) int = run(compute)

func main() {
    Println(forward(() => 7))
}`,
			expectContains: "function value",
			expectCapture:  "compute",
		},
		{
			// Same, but captured inside an explicit Future-body lambda rather than
			// passed directly. Still a bare func, still rejected.
			name: "bare func param captured in a boundary lambda is rejected",
			input: `package main

func run(body Sendable[func() int]) int = body()

func forward(compute func() int) int = run(() => compute())

func main() {
    Println(forward(() => 7))
}`,
			expectContains: "function value",
			expectCapture:  "compute",
		},
	}
	for _, tc := range rejects {
		t.Run("reject/"+tc.name, func(t *testing.T) { assertSendableRejected(t, trans, tc) })
	}

	// The bare-func rejection must carry an actionable AUTO-SUGGESTION: the hint
	// names the concrete fix `<param> Sendable[<funcType>]`, built from the
	// capture's own name and rendered type — not a generic "make it immutable"
	// message. Assert both the distinct function-value wording and the suggestion.
	t.Run("reject/bare func hint contains the concrete Sendable suggestion", func(t *testing.T) {
		input := `package main

func run(body Sendable[func() int]) int = body()

func forward(compute func() int) int = run(compute)

func main() {
    Println(forward(() => 7))
}`
		_, err := trans.Transpile(input, "concurrency_check_suggest.gala")
		require.Error(t, err)
		msg := err.Error()
		assert.Contains(t, msg, string(galaerr.CodeUnshareableCapture))
		// Distinct function-value message (vs. var / mutable-pointee wording).
		assert.Contains(t, msg, `function value "compute"`)
		assert.Contains(t, msg, "unless it is Sendable")
		// The concrete auto-suggestion: parameter name + rendered function type.
		assert.Contains(t, msg, "compute Sendable[func() int]")
		// It must NOT fall back to the generic mutable-pointee guidance.
		assert.NotContains(t, msg, "immutable collection (collection_immutable)")
	})

	accepts := []struct {
		name  string
		input string
	}{
		{
			// The forwarded function value is declared `Sendable[func() int]` and
			// passed DIRECTLY as the boundary argument (the `FutureApply(compute)`
			// shape: the by-name path analyzes `compute` as a free var).
			name: "Sendable func param passed directly to a boundary",
			input: `package main

func run(body Sendable[func() int]) int = body()

func wrapper(compute Sendable[func() int]) int = run(compute)

func main() {
    Println(wrapper(() => 7))
}`,
		},
		{
			// The same Sendable function value CAPTURED inside a Future-body lambda
			// (the `FutureApply(() => { …; make() })` shape from AfterDelay).
			name: "Sendable func param captured in a boundary lambda",
			input: `package main

func run(body Sendable[func() int]) int = body()

func afterDelay(compute Sendable[func() int]) int = run(() => compute())

func main() {
    Println(afterDelay(() => 7))
}`,
		},
		{
			// A wrapper mirroring CancellableAsync/AfterDelay: it forwards a
			// caller-provided Sendable computation to a boundary, alongside a
			// shareable `val` capture, and is accepted end-to-end.
			name: "concurrency wrapper forwarding a Sendable computation",
			input: `package main

func run(body Sendable[func() int]) int = body()

func cancellableAsync(compute Sendable[func() int]) int = run(() => compute() + 1)

func main() {
    Println(cancellableAsync(() => 41))
}`,
		},
		{
			// Generic wrapper: the forwarded Sendable closure is typed over the
			// function's own type parameter, exactly like the real signatures.
			name: "generic Sendable wrapper forwards over a type parameter",
			input: `package main

func run[R any](body Sendable[func() R]) R = body()

func afterDelay[R any](compute Sendable[func() R]) R = run[R](() => compute())

func main() {
    Println(afterDelay[int](() => 7))
}`,
		},
	}
	for _, tc := range accepts {
		t.Run("accept/"+tc.name, func(t *testing.T) { assertSendableAccepted(t, trans, tc.input) })
	}
}
