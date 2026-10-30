package std

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Values that are not constants, so the compiler cannot box them statically:
// boxing any of them into an any is a heap allocation.
var (
	hashInt     = 1000 + len(strings.Repeat("x", 3))
	hashString  = strings.Repeat("key", 3)
	hashInt64   = int64(hashInt) << 40
	hashFloat64 = float64(hashInt) / 7
)

// allocsOfHashes is the allocations HashOf(v) and HashKeyOf(v) make.
func allocsOfHashes[T any](v T) (of, keyOf float64) {
	return testing.AllocsPerRun(100, func() { HashOf(v) }), testing.AllocsPerRun(100, func() { HashKeyOf(v) })
}

// TestHashOfDoesNotAllocate holds HashOf and HashKeyOf to hashing a primitive
// without boxing it: their match is dispatched by a Go type switch, whose box
// of the value does not escape.
func TestHashOfDoesNotAllocate(t *testing.T) {
	tests := []struct {
		name   string
		allocs func() (float64, float64)
	}{
		{"int", func() (float64, float64) { return allocsOfHashes(hashInt) }},
		{"string", func() (float64, float64) { return allocsOfHashes(hashString) }},
		{"int64", func() (float64, float64) { return allocsOfHashes(hashInt64) }},
		{"float64", func() (float64, float64) { return allocsOfHashes(hashFloat64) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			of, keyOf := tt.allocs()
			assert.Zero(t, of, "HashOf")
			assert.Zero(t, keyOf, "HashKeyOf")
		})
	}
}

// asChainHashInt is hashPrimitive's first arms as a match lowers when it
// tests each type pattern with As: each As call boxes v.
func asChainHashInt[T any](v T) uint32 {
	if i, ok := As[int](v); ok {
		return HashInt(int64(i))
	}
	if s, ok := As[string](v); ok {
		return HashString(s)
	}
	return 0
}

// TestAsChainAllocates shows that the measurement above can fail: the same
// hash through As calls boxes the value on the heap.
func TestAsChainAllocates(t *testing.T) {
	assert.NotZero(t, testing.AllocsPerRun(100, func() { asChainHashInt(hashInt) }))
	assert.NotZero(t, testing.AllocsPerRun(100, func() { asChainHashInt(hashString) }))
}

type hashOfNamedInt int

type hashOfHashable struct{ n int }

func (h hashOfHashable) Hash() uint32 { return uint32(h.n) * 31 }

// assertHashesAgree holds HashOf(v) to HashValue(v) and HashKeyOf(v) to
// HashKey(v).
func assertHashesAgree[T any](t *testing.T, v T) {
	t.Helper()
	assert.Equal(t, HashValue(v), HashOf(v), "HashOf(%#v)", v)
	assert.Equal(t, HashKey(v), HashKeyOf(v), "HashKeyOf(%#v)", v)
}

// TestHashOfAgreesWithHashValue holds HashOf to hashing every value as
// HashValue does, and HashKeyOf as HashKey does, the values As looks through
// (an Immutable) included.
func TestHashOfAgreesWithHashValue(t *testing.T) {
	assertHashesAgree(t, 1000)
	assertHashesAgree(t, "abc")
	assertHashesAgree(t, int64(-7))
	assertHashesAgree(t, int32(7))
	assertHashesAgree(t, uint(7))
	assertHashesAgree(t, uint64(1<<60))
	assertHashesAgree(t, uint32(7))
	assertHashesAgree(t, true)
	assertHashesAgree(t, int8(-3))
	assertHashesAgree(t, int16(300))
	assertHashesAgree(t, uint8(200))
	assertHashesAgree(t, uint16(60000))
	assertHashesAgree(t, 1.5)
	assertHashesAgree(t, float32(1.5))
	assertHashesAgree(t, hashOfHashable{3})
	assertHashesAgree[any](t, hashOfHashable{3})
	assertHashesAgree(t, NewImmutable(1000))
	assertHashesAgree(t, NewImmutable(hashOfHashable{3}))
	assert.Equal(t, HashInt(1000), HashOf(NewImmutable(1000)), "an Immutable hashes as the value it holds")
}

// TestHashOfRejectsWhatHashValueRejects holds HashOf to HashValue's panic for
// a value that is neither primitive nor Hashable, a named primitive type
// included, and HashKeyOf to HashKey's hash of a pointer by its address.
func TestHashOfRejectsWhatHashValueRejects(t *testing.T) {
	assert.PanicsWithValue(t, panicValue(func() { HashValue(hashOfNamedInt(1)) }), func() { HashOf(hashOfNamedInt(1)) })
	assert.Panics(t, func() { HashOf(struct{ n int }{1}) })
	p := &struct{ n int }{1}
	assert.Panics(t, func() { HashOf(p) })
	assert.Equal(t, HashKey(p), HashKeyOf(p))
	assert.True(t, HashesByValue(1000))
	assert.True(t, HashesByValue(NewImmutable("s")))
	assert.False(t, HashesByValue(hashOfNamedInt(1)))
	assert.False(t, HashesByValue(nil))
}

// The benchmarks compare HashOf with HashValue on a primitive.
func BenchmarkHashOfInt(b *testing.B) {
	for b.Loop() {
		HashOf(hashInt)
	}
}

func BenchmarkHashValueInt(b *testing.B) {
	for b.Loop() {
		HashValue(hashInt)
	}
}

func BenchmarkHashOfString(b *testing.B) {
	for b.Loop() {
		HashOf(hashString)
	}
}

func BenchmarkHashValueString(b *testing.B) {
	for b.Loop() {
		HashValue(hashString)
	}
}

// panicValue is the value f panics with.
func panicValue(f func()) (v any) {
	defer func() { v = recover() }()
	f()
	return nil
}
