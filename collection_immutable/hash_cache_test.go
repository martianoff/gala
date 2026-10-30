package collection_immutable

// Tests of the hashes collections keep (Array's cached trie hash, HashSet's
// running sums): hashing primitives allocates nothing, and immutable
// collections shared between goroutines, hashed, compared and derived from
// by many of them at once, all see the same hashes. Run it under the race
// detector too.

import (
	"sync"
	"testing"
)

// TestCollectionHashAllocatesNothing: Array, List and HashSet of primitive
// elements hash without an allocation, cached or not (a List rehashes).
func TestCollectionHashAllocatesNothing(t *testing.T) {
	ints := ArrayTabulate(1000, func(i int) int { return i * 7919 })
	strs := Array_Map(ints, func(i int) string { return "k" + string(rune('a'+i%26)) })
	set := HashSetFromSlice(ints.ToGoSlice())
	for name, hash := range map[string]func() uint32{
		"Array[int]":             ints.Hash,
		"Array[string]":          strs.Hash,
		"prefixed Array[string]": strs.Prepend("x").Hash,
		"List[int]":              ints.ToList().Hash,
		"HashSet[int]":           set.Hash,
		"one-element HashSet":    HashSetOf("x").Hash,
		"HashSet shrunk to one":  HashSetOf(1, 2, 3).Remove(1).Remove(3).Hash,
	} {
		if allocs := testing.AllocsPerRun(20, func() { hash() }); allocs != 0 {
			t.Errorf("%s: %v allocs per run, want 0", name, allocs)
		}
	}
	if allocs := testing.AllocsPerRun(20, func() { set.Contains(7919) }); allocs != 0 {
		t.Errorf("HashSet[int].Contains: %v allocs per run, want 0", allocs)
	}
	// Hash on a fresh trie, which walks every element, adds no allocation to
	// Updated's.
	checkFirstHash(t, "Array[int]", func() Array[int] { return ints.Updated(0, -1) })
	checkFirstHash(t, "Array[string]", func() Array[string] { return strs.Updated(0, "y") })
}

// checkFirstHash checks that hashing a fresh Array from fresh allocates
// nothing beyond fresh itself.
func checkFirstHash[T any](t *testing.T, name string, fresh func() Array[T]) {
	t.Helper()
	want := testing.AllocsPerRun(20, func() { fresh() })
	if allocs := testing.AllocsPerRun(20, func() { fresh().Hash() }); allocs != want {
		t.Errorf("first Hash of an updated %s: %v allocs per run, want Updated's %v", name, allocs, want)
	}
}

// TestSharedCollectionsHashConcurrently: goroutines hashing, comparing and
// deriving from the same never-hashed Array and HashSet see the same hashes.
func TestSharedCollectionsHashConcurrently(t *testing.T) {
	const goroutines = 16
	for round := 0; round < 20; round++ {
		elems := ArrayTabulate(2000, func(i int) int { return i }).ToGoSlice()
		base := ArrayFromSlice(elems)
		prefixed := base.Prepend(-1)
		set := HashSetFromSlice(elems)
		wantArray := refArrayHash(elems)
		wantPrefixed := refArrayHash(append([]int{-1}, elems...))
		wantSet := set.Hash()
		keys := HashSetOf(ArrayOf(1, 2, 3))

		var wg sync.WaitGroup
		errs := make(chan string, goroutines*5)
		for g := 0; g < goroutines; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				if base.Hash() != wantArray {
					errs <- "Array.Hash"
				}
				if prefixed.Hash() != wantPrefixed {
					errs <- "prefixed Array.Hash"
				}
				if !base.Equal(prefixed.Tail()) || base.Equal(base.Updated(g, -g-1)) {
					errs <- "Array.Equal"
				}
				if set.Remove(g).Add(g).Hash() != wantSet {
					errs <- "HashSet.Hash"
				}
				if !keys.Add(base).Contains(base) {
					errs <- "HashSet[Array].Contains"
				}
			}(g)
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			t.Fatalf("round %d: %s disagrees across goroutines", round, e)
		}
	}
}
