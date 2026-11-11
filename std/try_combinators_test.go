package std

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

var (
	trySuccess = Success[int]{}.Apply(1)
	tryFailure = Failure[int]{}.Apply(NoSuchElementError{})
)

func addOne(x int) int { return x + 1 }

func recoverToZero(error) int { return 0 }

func keepTry(x int) Try[int] { return Success[int]{}.Apply(x) }

func recoverTry(error) Try[int] { return keepTry(0) }

func isPositive(x int) bool { return x > 0 }

// unguardedMap is the code Try.Map compiled to before it captured panics,
// the baseline BenchmarkTryMap compares against.
func unguardedMap[T, U any](t Try[T], f func(T) U) Try[U] {
	if t.isFailure() {
		return Failure[U]{}.Apply(t.Err.Get())
	}
	return Success[U]{}.Apply(f(t.Value.Get()))
}

// Capturing a panic in the combinators must cost nothing on the path that
// does not panic: Try is used in hot loops.
func TestTryCombinators_NoAllocationWithoutPanic(t *testing.T) {
	tests := []struct {
		name string
		run  func()
	}{
		{"Map on Success", func() { Try_Map(trySuccess, addOne) }},
		{"Map on Failure", func() { Try_Map(tryFailure, addOne) }},
		{"Recover on Failure", func() { tryFailure.Recover(recoverToZero) }},
		{"FlatMap on Success", func() { Try_FlatMap(trySuccess, keepTry) }},
		{"Filter on Success", func() { trySuccess.Filter(isPositive) }},
		{"RecoverWith on Failure", func() { tryFailure.RecoverWith(recoverTry) }},
		{"Transform on Success", func() { Try_Transform(trySuccess, keepTry, recoverTry) }},
		{"Transform on Failure", func() { Try_Transform(tryFailure, keepTry, recoverTry) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Zero(t, testing.AllocsPerRun(1000, tt.run))
		})
	}
}

func BenchmarkTryMap(b *testing.B) {
	t := trySuccess
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		t = Try_Map(t, addOne)
	}
}

func BenchmarkTryMapUnguarded(b *testing.B) {
	t := trySuccess
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		t = unguardedMap(t, addOne)
	}
}
