package collection_mutable

// Differential tests: mutable HashMap / HashSet / TreeMap / TreeSet against a
// Go map reference model, driven by random insert/remove sequences that grow
// the tables through every resize and the trees through every rebalancing case.

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"testing"

	. "martianoff/gala/internal/collprop"
	. "martianoff/gala/std"
)

// mutMap is the in-place API that HashMap and TreeMap share.
type mutMap[K comparable] interface {
	Size() int
	IsEmpty() bool
	Put(key K, value int) bool
	PutIfAbsent(key K, value int) bool
	Update(key K, f func(int) int) bool
	GetOrElseUpdate(key K, f func() int) int
	Remove(key K) bool
	Contains(key K) bool
	GetOrElse(key K, defaultValue int) int
	ForEachKV(f func(K, int))
	ToGoMap() map[K]int
}

var (
	_ mutMap[int] = (*HashMap[int, int])(nil)
	_ mutMap[int] = (*TreeMap[int, int])(nil)
)

// checkMutMap compares m with ref; ordered is non-nil for a TreeMap and gives
// the iteration order ForEachKV must follow.
func checkMutMap[K comparable](p *Prop, what string, m mutMap[K], ref map[K]int, ordered []K, absent []K) {
	p.T.Helper()
	if m.Size() != len(ref) || m.IsEmpty() != (len(ref) == 0) {
		p.Fatalf("%s: Size %d, want %d", what, m.Size(), len(ref))
	}
	EqMaps(p, what+" ToGoMap", m.ToGoMap(), ref)
	for k, v := range ref {
		if !m.Contains(k) || m.GetOrElse(k, -1) != v {
			p.Fatalf("%s: key %v: Contains %v, GetOrElse %d, want %d", what, k, m.Contains(k), m.GetOrElse(k, -1), v)
		}
	}
	for _, k := range absent {
		if _, ok := ref[k]; !ok && m.Contains(k) {
			p.Fatalf("%s: absent key %v reported present", what, k)
		}
	}
	var keys []K
	m.ForEachKV(func(k K, v int) {
		keys = append(keys, k)
		if ref[k] != v {
			p.Fatalf("%s: ForEachKV saw %v -> %d, want %d", what, k, v, ref[k])
		}
	})
	if ordered != nil {
		EqSlices(p, what+" ForEachKV key order", keys, ordered)
		return
	}
	seen := map[K]bool{}
	for _, k := range keys {
		if seen[k] {
			p.Fatalf("%s: ForEachKV visited %v twice", what, k)
		}
		seen[k] = true
	}
	if len(seen) != len(ref) {
		p.Fatalf("%s: ForEachKV visited %d keys, want %d", what, len(seen), len(ref))
	}
}

// runMutMapWorkload drives m through a random workload. order, when non-nil,
// sorts keys for the ordered-iteration check.
func runMutMapWorkload[K comparable](p *Prop, kind KeyKind[K], n int, m mutMap[K], order func(map[K]int) []K) map[K]int {
	p.T.Helper()
	ref := map[K]int{}
	absent := []K{kind.Mk(-1), kind.Mk(KeySpace(n) + 7)}
	orderOf := func() []K {
		if order == nil {
			return nil
		}
		return order(ref)
	}
	ops := p.MapWorkload(n)
	full := SnapshotSteps(len(ops))
	for i, op := range ops {
		k := kind.Mk(op.Key)
		_, present := ref[k]
		var ok bool
		var name string
		switch variant := p.Rng.Intn(8); {
		case !op.Put:
			name, ok = "Remove", m.Remove(k)
			if ok != present {
				p.Fatalf("op %d %s(%v) returned %v, key present: %v", i, name, k, ok, present)
			}
			delete(ref, k)
		case variant == 0:
			name, ok = "PutIfAbsent", m.PutIfAbsent(k, op.Value)
			if ok == present {
				p.Fatalf("op %d %s(%v) returned %v, key present: %v", i, name, k, ok, present)
			}
			if !present {
				ref[k] = op.Value
			}
		case variant == 1:
			name, ok = "Update", m.Update(k, func(v int) int { return v + 1 })
			if ok != present {
				p.Fatalf("op %d %s(%v) returned %v, key present: %v", i, name, k, ok, present)
			}
			if present {
				ref[k]++
			}
		case variant == 2:
			name = "GetOrElseUpdate"
			calls := 0
			got := m.GetOrElseUpdate(k, func() int { calls++; return op.Value })
			if !present {
				ref[k] = op.Value
			}
			if got != ref[k] || calls != map[bool]int{true: 0, false: 1}[present] {
				p.Fatalf("op %d %s(%v) = %d with %d calls, want %d (key present: %v)", i, name, k, got, calls, ref[k], present)
			}
		default:
			name, ok = "Put", m.Put(k, op.Value)
			if ok == present {
				p.Fatalf("op %d %s(%v) returned %v, key present: %v", i, name, k, ok, present)
			}
			ref[k] = op.Value
		}
		if m.Size() != len(ref) {
			p.Fatalf("after op %d %s(%v): Size %d, want %d", i, name, k, m.Size(), len(ref))
		}
		if full[i] {
			checkMutMap(p, fmt.Sprintf("after op %d %s", i, name), m, ref, orderOf(), absent)
		}
	}
	checkMutMap(p, "final", m, ref, orderOf(), absent)
	return ref
}

func runMutHashMap[K comparable](t *testing.T, kind KeyKind[K]) {
	for _, n := range MapSizes {
		if kind.Skips(n) {
			continue
		}
		t.Run(fmt.Sprintf("%s/%d", kind.Name, n), func(t *testing.T) {
			p := NewProp(t, n)
			m := EmptyHashMap[K, int]()
			ref := runMutMapWorkload[K](p, kind, n, m, nil)
			absent := []K{kind.Mk(-1)}

			clone := m.Clone()
			entries := make([]Tuple[K, int], 0, len(ref))
			for k, v := range ref {
				entries = append(entries, TupleOf(k, v))
			}
			checkMutMap[K](p, "HashMapFromSlice", HashMapFromSlice(entries), ref, nil, absent)
			checkMutMap[K](p, "HashMapFromGoMap", HashMapFromGoMap(ref), ref, nil, absent)

			even := func(_ K, v int) bool { return v%2 == 0 }
			wantEven := maps.Clone(ref)
			maps.DeleteFunc(wantEven, func(_ K, v int) bool { return v%2 != 0 })
			checkMutMap[K](p, "Filter", m.Filter(even), wantEven, nil, absent)
			part := m.Partition(even)
			checkMutMap[K](p, "Partition.1", part.V1.Get(), wantEven, nil, absent)
			calls := 0
			doubled := HashMap_MapValues(m, func(v int) int { calls++; return v * 2 })
			if calls != len(ref) || doubled.Size() != len(ref) {
				p.Fatalf("MapValues: %d calls, size %d, want %d", calls, doubled.Size(), len(ref))
			}

			// In-place bulk edits; the clone must not see any of them.
			m.FilterInPlace(even)
			checkMutMap[K](p, "FilterInPlace", m, wantEven, nil, absent)
			m.UpdateAll(func(_ K, v int) int { return v + 3 })
			for k := range wantEven {
				wantEven[k] += 3
			}
			checkMutMap[K](p, "UpdateAll", m, wantEven, nil, absent)
			m.Merge(clone, func(a, b int) int { return a - b })
			for k, v := range ref {
				if old, ok := wantEven[k]; ok {
					wantEven[k] = old - v
				} else {
					wantEven[k] = v
				}
			}
			checkMutMap[K](p, "Merge", m, wantEven, nil, absent)
			checkMutMap[K](p, "Clone after edits to the original", clone, ref, nil, absent)
			m.Clear()
			checkMutMap[K](p, "Clear", m, map[K]int{}, nil, absent)
		})
	}
}

func TestMutableHashMapDifferential(t *testing.T) {
	runMutHashMap(t, IntKeys)
	runMutHashMap(t, StringKeys)
	runMutHashMap(t, CollidingKeys)
}

func runMutTreeMap[K cmp.Ordered](t *testing.T, kind KeyKind[K]) {
	for _, n := range MapSizes {
		t.Run(fmt.Sprintf("%s/%d", kind.Name, n), func(t *testing.T) {
			p := NewProp(t, n)
			m := EmptyTreeMap[K, int]()
			ref := runMutMapWorkload[K](p, kind, n, m, SortedKeys[K, int])
			clone := m.Clone()
			absent := []K{kind.Mk(-1)}
			checkMutMap[K](p, "TreeMapFromGoMap", TreeMapFromGoMap(ref), ref, SortedKeys(ref), absent)

			for r := 0; r < 5; r++ {
				lo, hi := kind.Mk(p.Rng.Intn(n+2)), kind.Mk(p.Rng.Intn(n+2))
				want, wantFrom, wantTo := map[K]int{}, map[K]int{}, map[K]int{}
				for k, v := range ref {
					if k >= lo && k <= hi {
						want[k] = v
					}
					if k >= lo {
						wantFrom[k] = v
					}
					if k <= hi {
						wantTo[k] = v
					}
				}
				checkMutMap[K](p, fmt.Sprintf("Range(%v,%v)", lo, hi), m.Range(lo, hi), want, SortedKeys(want), absent)
				checkMutMap[K](p, fmt.Sprintf("RangeFrom(%v)", lo), m.RangeFrom(lo), wantFrom, SortedKeys(wantFrom), absent)
				checkMutMap[K](p, fmt.Sprintf("RangeTo(%v)", hi), m.RangeTo(hi), wantTo, SortedKeys(wantTo), absent)
			}

			// Draining from both ends visits the keys in order.
			keys := SortedKeys(ref)
			lo, hi := 0, len(keys)-1
			for m.NonEmpty() {
				if p.Rng.Intn(2) == 0 {
					e := m.PopMinEntry()
					if e.V1.Get() != keys[lo] || e.V2.Get() != ref[keys[lo]] {
						p.Fatalf("PopMinEntry = %v, want (%v, %d)", e, keys[lo], ref[keys[lo]])
					}
					lo++
				} else {
					e := m.PopMaxEntry()
					if e.V1.Get() != keys[hi] || e.V2.Get() != ref[keys[hi]] {
						p.Fatalf("PopMaxEntry = %v, want (%v, %d)", e, keys[hi], ref[keys[hi]])
					}
					hi--
				}
				if m.Size() != hi-lo+1 {
					p.Fatalf("Size %d after popping, want %d", m.Size(), hi-lo+1)
				}
			}
			checkMutMap[K](p, "Clone after draining the original", clone, ref, keys, absent)
		})
	}
}

func TestMutableTreeMapDifferential(t *testing.T) {
	runMutTreeMap(t, IntKeys)
	runMutTreeMap(t, StringKeys)
}

// mutSet is the in-place API that HashSet and TreeSet share.
type mutSet[T comparable] interface {
	Size() int
	IsEmpty() bool
	Add(elem T) bool
	Remove(elem T) bool
	Contains(elem T) bool
	ForEach(f func(T))
	ToGoSlice() []T
}

var (
	_ mutSet[int] = (*HashSet[int])(nil)
	_ mutSet[int] = (*TreeSet[int])(nil)
)

func checkMutSet[T comparable](p *Prop, what string, s mutSet[T], ref map[T]bool, ordered []T) {
	p.T.Helper()
	if s.Size() != len(ref) || s.IsEmpty() != (len(ref) == 0) {
		p.Fatalf("%s: Size %d, want %d", what, s.Size(), len(ref))
	}
	for k := range ref {
		if !s.Contains(k) {
			p.Fatalf("%s: missing %v", what, k)
		}
	}
	var visited []T
	s.ForEach(func(x T) { visited = append(visited, x) })
	if ordered != nil {
		EqSlices(p, what+" ForEach order", visited, ordered)
		EqSlices(p, what+" ToGoSlice order", s.ToGoSlice(), ordered)
		return
	}
	for _, elems := range [][]T{visited, s.ToGoSlice()} {
		seen := map[T]bool{}
		for _, x := range elems {
			if seen[x] || !ref[x] {
				p.Fatalf("%s: iteration yields %v twice or as a non-member", what, x)
			}
			seen[x] = true
		}
		if len(seen) != len(ref) {
			p.Fatalf("%s: iteration yields %d elements, want %d", what, len(seen), len(ref))
		}
	}
}

func runMutSetWorkload[T comparable](p *Prop, kind KeyKind[T], n int, s mutSet[T], order func(map[T]bool) []T) map[T]bool {
	p.T.Helper()
	ref := map[T]bool{}
	orderOf := func(r map[T]bool) []T {
		if order == nil {
			return nil
		}
		return order(r)
	}
	ops := p.MapWorkload(n)
	full := SnapshotSteps(len(ops))
	for i, op := range ops {
		k := kind.Mk(op.Key)
		if op.Put {
			if s.Add(k) == ref[k] {
				p.Fatalf("op %d Add(%v) result disagrees with membership %v", i, k, ref[k])
			}
			ref[k] = true
		} else {
			if s.Remove(k) != ref[k] {
				p.Fatalf("op %d Remove(%v) result disagrees with membership %v", i, k, ref[k])
			}
			delete(ref, k)
		}
		if s.Size() != len(ref) {
			p.Fatalf("after op %d (%+v): Size %d, want %d", i, op, s.Size(), len(ref))
		}
		if full[i] {
			checkMutSet(p, fmt.Sprintf("after op %d", i), s, ref, orderOf(ref))
		}
	}
	return ref
}

// setAlgebra returns the reference union, intersection and difference.
func setAlgebra[T comparable](a, b map[T]bool) (union, inter, diff map[T]bool) {
	union, inter, diff = maps.Clone(a), map[T]bool{}, map[T]bool{}
	maps.Copy(union, b)
	for k := range a {
		if b[k] {
			inter[k] = true
		} else {
			diff[k] = true
		}
	}
	return union, inter, diff
}

func runMutHashSet[T comparable](t *testing.T, kind KeyKind[T]) {
	for _, n := range MapSizes {
		if kind.Skips(n) {
			continue
		}
		t.Run(fmt.Sprintf("%s/%d", kind.Name, n), func(t *testing.T) {
			p := NewProp(t, n)
			s := EmptyHashSet[T]()
			ref := runMutSetWorkload[T](p, kind, n, s, nil)
			checkMutSet[T](p, "HashSetFromSlice", HashSetFromSlice(slices.Collect(maps.Keys(ref))), ref, nil)

			other := EmptyHashSet[T]()
			refOther := map[T]bool{}
			for i := 0; i < n/2+1; i++ {
				k := kind.Mk(p.Rng.Intn(2*n + 2))
				other.Add(k)
				refOther[k] = true
			}
			union, inter, diff := setAlgebra(ref, refOther)
			checkMutSet[T](p, "Union", s.Union(other), union, nil)
			checkMutSet[T](p, "Intersect", s.Intersect(other), inter, nil)
			checkMutSet[T](p, "Diff", s.Diff(other), diff, nil)
			if s.SubsetOf(other) != (len(diff) == 0) || s.Disjoint(other) != (len(inter) == 0) {
				p.Fatalf("SubsetOf/Disjoint disagree with the model")
			}
			clone := s.Clone()
			s.IntersectInPlace(other)
			checkMutSet[T](p, "IntersectInPlace", s, inter, nil)
			s.UnionInPlace(other)
			checkMutSet[T](p, "UnionInPlace", s, refOther, nil)
			s.DiffInPlace(clone)
			_, _, rest := setAlgebra(refOther, ref)
			checkMutSet[T](p, "DiffInPlace", s, rest, nil)
			checkMutSet[T](p, "Clone after edits to the original", clone, ref, nil)
		})
	}
}

func TestMutableHashSetDifferential(t *testing.T) {
	runMutHashSet(t, IntKeys)
	runMutHashSet(t, StringKeys)
	runMutHashSet(t, CollidingKeys)
}

func runMutTreeSet[T cmp.Ordered](t *testing.T, kind KeyKind[T]) {
	for _, n := range MapSizes {
		t.Run(fmt.Sprintf("%s/%d", kind.Name, n), func(t *testing.T) {
			p := NewProp(t, n)
			s := EmptyTreeSet[T]()
			ref := runMutSetWorkload[T](p, kind, n, s, SortedKeys[T, bool])
			keys := SortedKeys(ref)
			checkMutSet[T](p, "final", s, ref, keys)
			var backwards []T
			s.ForEachReverse(func(x T) { backwards = append(backwards, x) })
			slices.Reverse(backwards)
			EqSlices(p, "ForEachReverse", backwards, keys)
			checkMutSet[T](p, "TreeSetFromSlice", TreeSetFromSlice(slices.Collect(maps.Keys(ref))), ref, keys)

			for r := 0; r < 5; r++ {
				lo, hi := kind.Mk(p.Rng.Intn(n+2)), kind.Mk(p.Rng.Intn(n+2))
				want := map[T]bool{}
				for k := range ref {
					if k >= lo && k <= hi {
						want[k] = true
					}
				}
				checkMutSet[T](p, fmt.Sprintf("Range(%v,%v)", lo, hi), s.Range(lo, hi), want, SortedKeys(want))
			}
			other := EmptyTreeSet[T]()
			refOther := map[T]bool{}
			for i := 0; i < n/2+1; i++ {
				k := kind.Mk(p.Rng.Intn(2*n + 2))
				other.Add(k)
				refOther[k] = true
			}
			union, inter, diff := setAlgebra(ref, refOther)
			checkMutSet[T](p, "Union", s.Union(other), union, SortedKeys(union))
			checkMutSet[T](p, "Intersect", s.Intersect(other), inter, SortedKeys(inter))
			checkMutSet[T](p, "Diff", s.Diff(other), diff, SortedKeys(diff))

			clone := s.Clone()
			lo, hi := 0, len(keys)-1
			for s.NonEmpty() {
				if p.Rng.Intn(2) == 0 {
					if got := s.PopMin(); got != keys[lo] {
						p.Fatalf("PopMin = %v, want %v", got, keys[lo])
					}
					lo++
				} else {
					if got := s.PopMax(); got != keys[hi] {
						p.Fatalf("PopMax = %v, want %v", got, keys[hi])
					}
					hi--
				}
			}
			checkMutSet[T](p, "Clone after draining the original", clone, ref, keys)
		})
	}
}

func TestMutableTreeSetDifferential(t *testing.T) {
	runMutTreeSet(t, IntKeys)
	runMutTreeSet(t, StringKeys)
}
