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

// TestHashOfDoesNotAllocate holds HashOf and HashKeyOf to hashing a primitive
// without boxing it: their match is dispatched by a Go type switch, whose box
// of the value does not escape.
func TestHashOfDoesNotAllocate(t *testing.T) {
	tests := []struct {
		name string
		hash func()
	}{
		{"int", func() { HashOf(hashInt) }},
		{"string", func() { HashOf(hashString) }},
		{"int64", func() { HashOf(hashInt64) }},
		{"float64", func() { HashOf(hashFloat64) }},
		{"key int", func() { HashKeyOf(hashInt) }},
		{"key string", func() { HashKeyOf(hashString) }},
		{"key int64", func() { HashKeyOf(hashInt64) }},
		{"key float64", func() { HashKeyOf(hashFloat64) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Zero(t, testing.AllocsPerRun(100, tt.hash))
		})
	}
}

// asChainHashInt is hashPrimitive's int arm as a match lowers when it tests
// each type pattern with As: each As call boxes v.
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

// TestHashOfAgreesWithHashValue holds HashOf to hashing every value as
// HashValue does, and HashKeyOf as HashKey does, the values As looks through
// (an Immutable, a recovered panic's error) included.
func TestHashOfAgreesWithHashValue(t *testing.T) {
	values := []struct {
		name  string
		value any
		of    func() uint32
		keyOf func() uint32
	}{
		{"int", 1000, func() uint32 { return HashOf(1000) }, func() uint32 { return HashKeyOf(1000) }},
		{"string", "abc", func() uint32 { return HashOf("abc") }, func() uint32 { return HashKeyOf("abc") }},
		{"int64", int64(-7), func() uint32 { return HashOf(int64(-7)) }, func() uint32 { return HashKeyOf(int64(-7)) }},
		{"int32", int32(7), func() uint32 { return HashOf(int32(7)) }, func() uint32 { return HashKeyOf(int32(7)) }},
		{"uint", uint(7), func() uint32 { return HashOf(uint(7)) }, func() uint32 { return HashKeyOf(uint(7)) }},
		{"uint64", uint64(1 << 60), func() uint32 { return HashOf(uint64(1 << 60)) }, func() uint32 { return HashKeyOf(uint64(1 << 60)) }},
		{"uint32", uint32(7), func() uint32 { return HashOf(uint32(7)) }, func() uint32 { return HashKeyOf(uint32(7)) }},
		{"bool", true, func() uint32 { return HashOf(true) }, func() uint32 { return HashKeyOf(true) }},
		{"int8", int8(-3), func() uint32 { return HashOf(int8(-3)) }, func() uint32 { return HashKeyOf(int8(-3)) }},
		{"int16", int16(300), func() uint32 { return HashOf(int16(300)) }, func() uint32 { return HashKeyOf(int16(300)) }},
		{"uint8", uint8(200), func() uint32 { return HashOf(uint8(200)) }, func() uint32 { return HashKeyOf(uint8(200)) }},
		{"uint16", uint16(60000), func() uint32 { return HashOf(uint16(60000)) }, func() uint32 { return HashKeyOf(uint16(60000)) }},
		{"float64", 1.5, func() uint32 { return HashOf(1.5) }, func() uint32 { return HashKeyOf(1.5) }},
		{"float32", float32(1.5), func() uint32 { return HashOf(float32(1.5)) }, func() uint32 { return HashKeyOf(float32(1.5)) }},
		{"Hashable", hashOfHashable{3}, func() uint32 { return HashOf(hashOfHashable{3}) }, func() uint32 { return HashKeyOf(hashOfHashable{3}) }},
		{"Hashable as any", any(hashOfHashable{3}), func() uint32 { return HashOf(any(hashOfHashable{3})) }, func() uint32 { return HashKeyOf(any(hashOfHashable{3})) }},
		{"Immutable int", NewImmutable(1000), func() uint32 { return HashOf(NewImmutable(1000)) }, func() uint32 { return HashKeyOf(NewImmutable(1000)) }},
		{"Immutable Hashable", NewImmutable(hashOfHashable{3}), func() uint32 { return HashOf(NewImmutable(hashOfHashable{3})) }, func() uint32 { return HashKeyOf(NewImmutable(hashOfHashable{3})) }},
	}
	for _, v := range values {
		t.Run(v.name, func(t *testing.T) {
			assert.Equal(t, HashValue(v.value), v.of())
			assert.Equal(t, HashKey(v.value), v.keyOf())
		})
	}
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

// panicValue is the value f panics with.
func panicValue(f func()) (v any) {
	defer func() { v = recover() }()
	f()
	return nil
}
