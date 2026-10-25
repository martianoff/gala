package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sendableCrossPkgFixture is a module of three packages: `leaf` declares
// deeply immutable and mutable structs, `mid` declares types whose fields name
// `leaf`'s types, and the file under test imports `mid` but not `leaf`.
func sendableCrossPkgFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/sendx\n\ngala dev\n")
	write("leaf/leaf.gala", `package leaf

import . "martianoff/gala/collection_immutable"

struct Command(Name string, Arg Option[string])

struct Counter(var N int)
`)
	write("mid/mid.gala", `package mid

import (
    . "martianoff/gala/collection_immutable"
    "example.com/sendx/leaf"
)

sealed type Node {
    case Leafy(Cs Array[leaf.Command])
    case Empty()
}

struct Holder(C leaf.Command)

struct Batch(Cs Array[leaf.Command])

struct MutBatch(Cs Array[leaf.Counter])

struct MutHolder(C leaf.Counter)

struct Vocabulary(Names Array[string])

func EmptyVocabulary() Vocabulary = Vocabulary(ArrayOf[string]())

func WrapCommand() Holder = Holder(leaf.Command("x", None[string]()))

func WrapCounter() MutHolder = MutHolder(leaf.Counter(1))

func NoCommands() Batch = Batch(ArrayOf[leaf.Command]())

func NoCounters() MutBatch = MutBatch(ArrayOf[leaf.Counter]())
`)
	return root
}

// TestSendableCaptureCrossPackageTransitiveTypes: a struct or sealed field
// whose type is a generic with a package-qualified argument
// (`Array[leaf.Command]`) is decided by its real base type. The analyzer took
// the dot in the argument for a qualified base and recorded the base as the
// bare, unresolved `Array`, so the Shareable predicate no longer recognised the
// immutable collection and rejected deeply immutable values with GALA-E0037 —
// whether the capturing file imported the argument's package or not.
func TestSendableCaptureCrossPackageTransitiveTypes(t *testing.T) {
	root := sendableCrossPkgFixture(t)
	const header = `package main

import (
    . "martianoff/gala/collection_immutable"
    . "martianoff/gala/concurrent"
    "example.com/sendx/mid"
)

`
	allowed := []struct{ name, body string }{
		{"sealed param with transitive field type", `func f(ts Array[mid.Node]) int = Future(() => ts.Size()).Get()

func main() { Println(f(ArrayOf(mid.Empty()))) }`},
		{"struct param with transitive field type", `func f(h mid.Holder) string = Future(() => h.C.Name).Get()

func main() { Println(f(mid.WrapCommand())) }`},
		{"whole capture of struct with transitive field type", `func f(h mid.Holder) mid.Holder = Future(() => h).Get()

func main() { Println(f(mid.WrapCommand())) }`},
		{"val inferred from cross-package expression-bodied call", `func main() {
    val v = mid.EmptyVocabulary()
    Println(Future(() => v).Get())
}`},
		{"val inferred from cross-package call returning transitive type", `func main() {
    val h = mid.WrapCommand()
    Println(Future(() => h).Get())
}`},
		{"field path through cross-package val", `func main() {
    val v = mid.EmptyVocabulary()
    Println(Future(() => v.Names.Size()).Get())
}`},
		{"struct with collection of transitive type", `func f(b mid.Batch) int = Future(() => b.Cs.Size()).Get()

func main() { Println(f(mid.NoCommands())) }`},
		{"own struct with collection of imported type", `struct Local(Hs Array[mid.Holder])

func f(l Local) Local = Future(l).Get()

func main() { Println(f(Local(ArrayOf(mid.WrapCommand())))) }`},
		{"own sealed type with collection of imported type", `sealed type Tree {
    case Branch(Hs Array[mid.Holder])
    case Bare()
}

func f(t Tree) Tree = Future(t).Get()

func main() { Println(f(Bare())) }`},
		{"whole struct with collection of transitive type", `func main() {
    val b = mid.NoCommands()
    Println(Future(() => b).Get())
}`},
	}
	for _, tc := range allowed {
		t.Run("allowed/"+tc.name, func(t *testing.T) {
			_, err := transpileCrossPkg(t, root, header+tc.body)
			require.NoError(t, err)
		})
	}

	rejected := []struct{ name, body string }{
		{"param holding a transitive mutable struct", `func f(h mid.MutHolder) mid.MutHolder = Future(() => h).Get()

func main() { Println(f(mid.WrapCounter())) }`},
		{"val inferred from call returning a transitive mutable struct", `func main() {
    val h = mid.WrapCounter()
    Println(Future(() => h).Get())
}`},
		{"own struct with collection of an imported mutable struct", `struct Local(Hs Array[mid.MutHolder])

func f(l Local) Local = Future(l).Get()

func main() { Println(f(Local(ArrayOf(mid.WrapCounter())))) }`},
		{"collection of a transitive mutable struct", `func main() {
    val b = mid.NoCounters()
    Println(Future(() => b).Get())
}`},
	}
	for _, tc := range rejected {
		t.Run("rejected/"+tc.name, func(t *testing.T) {
			_, err := transpileCrossPkg(t, root, header+tc.body)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0037")
		})
	}
}

// TestSendableCaptureOfUninferredVal: a val whose defining call does not
// resolve (here a function its package does not declare) has no known type.
// The check still fails closed, but says the type is unknown instead of
// reporting a data race on a value it knows nothing about.
func TestSendableCaptureOfUninferredVal(t *testing.T) {
	root := sendableCrossPkgFixture(t)
	_, err := transpileCrossPkg(t, root, `package main

import (
    . "martianoff/gala/concurrent"
    "example.com/sendx/mid"
)

func main() {
    val v = mid.NoSuchVocabulary()
    Println(Future(() => v.Names.Size()).Get())
}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0037")
	assert.Contains(t, err.Error(), `captures "v", whose type could not be inferred`)
	assert.NotContains(t, err.Error(), "data race on its mutable contents")
}
