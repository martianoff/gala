package collection_immutable

// Differential tests: immutable HashMap / HashSet / TreeMap / TreeSet against
// a Go map reference model, driven by random insert/remove sequences. Every
// intermediate version is kept and re-checked at the end (persistence).

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"testing"

	. "martianoff/gala/internal/collprop"
	. "martianoff/gala/std"
)

func checkHashMap[K comparable](p *Prop, what string, m HashMap[K, int], ref map[K]int, absent []K) {
	p.T.Helper()
	if m.Size() != len(ref) || m.Length() != len(ref) || m.IsEmpty() != (len(ref) == 0) {
		p.Fatalf("%s: Size %d, want %d", what, m.Size(), len(ref))
	}
	EqMaps(p, what+" ToGoMap", m.ToGoMap(), ref)
	for k, v := range ref {
		if !m.Contains(k) || m.Get(k).GetOrElse(-1) != v {
			p.Fatalf("%s: key %v: Contains %v Get %v, want %d", what, k, m.Contains(k), m.Get(k), v)
		}
	}
	for _, k := range absent {
		if _, ok := ref[k]; !ok && (m.Contains(k) || m.Get(k).IsDefined()) {
			p.Fatalf("%s: absent key %v is reported present", what, k)
		}
	}
	seen := map[K]int{}
	m.ForEachKV(func(k K, v int) {
		seen[k]++
		if ref[k] != v {
			p.Fatalf("%s: ForEachKV saw %v -> %d, want %d", what, k, v, ref[k])
		}
	})
	for k, c := range seen {
		if c != 1 {
			p.Fatalf("%s: ForEachKV visited %v %d times", what, k, c)
		}
	}
	if len(seen) != len(ref) {
		p.Fatalf("%s: ForEachKV visited %d keys, want %d", what, len(seen), len(ref))
	}
}

func runHashMapWorkload[K comparable](t *testing.T, kind KeyKind[K]) {
	for _, n := range MapSizes {
		if kind.Skips(n) {
			continue
		}
		t.Run(fmt.Sprintf("%s/%d", kind.Name, n), func(t *testing.T) {
			p := NewProp(t, n)
			ops := p.MapWorkload(n)
			absent := []K{kind.Mk(-1), kind.Mk(KeySpace(n) + 7)}
			type version struct {
				m   HashMap[K, int]
				ref map[K]int
			}
			var versions []version
			keep := SnapshotSteps(len(ops))
			m := EmptyHashMap[K, int]()
			ref := map[K]int{}
			for i, op := range ops {
				k := kind.Mk(op.Key)
				if op.Put {
					m = m.Put(k, op.Value)
					ref[k] = op.Value
				} else {
					m = m.Remove(k)
					delete(ref, k)
				}
				if m.Size() != len(ref) {
					p.Fatalf("after op %d (%+v): Size %d, want %d", i, op, m.Size(), len(ref))
				}
				if keep[i] {
					checkHashMap(p, fmt.Sprintf("after op %d", i), m, ref, absent)
					versions = append(versions, version{m, maps.Clone(ref)})
				}
			}
			for i, v := range versions {
				checkHashMap(p, fmt.Sprintf("version %d re-read", i), v.m, v.ref, absent)
			}
			checkHashMap(p, "final", m, ref, absent)

			// Bulk construction paths agree with the incremental one.
			entries := make([]Tuple[K, int], 0, len(ref))
			for k, v := range ref {
				entries = append(entries, TupleOf(k, v))
			}
			checkHashMap(p, "HashMapFromSlice", HashMapFromSlice(entries), ref, absent)
			checkHashMap(p, "HashMapOf", HashMapOf(entries...), ref, absent)
			checkHashMap(p, "HashMapFromGoMap", HashMapFromGoMap(ref), ref, absent)

			// Derived maps.
			even := func(_ K, v int) bool { return v%2 == 0 }
			wantEven := maps.Clone(ref)
			maps.DeleteFunc(wantEven, func(_ K, v int) bool { return v%2 != 0 })
			checkHashMap(p, "Filter", m.Filter(even), wantEven, absent)
			part := m.Partition(even)
			checkHashMap(p, "Partition.1", part.V1.Get(), wantEven, absent)
			wantOdd := maps.Clone(ref)
			maps.DeleteFunc(wantOdd, func(_ K, v int) bool { return v%2 == 0 })
			checkHashMap(p, "Partition.2", part.V2.Get(), wantOdd, absent)
			if m.Count(even) != len(wantEven) {
				p.Fatalf("Count = %d, want %d", m.Count(even), len(wantEven))
			}
			calls := 0
			doubled := HashMap_MapValues(m, func(v int) int { calls++; return v * 2 })
			if calls != len(ref) {
				p.Fatalf("MapValues ran its function %d times for %d entries", calls, len(ref))
			}
			wantDoubled := map[K]int{}
			for k, v := range ref {
				wantDoubled[k] = v * 2
			}
			checkHashMap(p, "MapValues", doubled, wantDoubled, absent)
			sum := HashMap_FoldLeftKV(m, 0, func(acc int, _ K, v int) int { return acc + v })
			wantSum := 0
			for _, v := range ref {
				wantSum += v
			}
			if sum != wantSum {
				p.Fatalf("FoldLeftKV = %d, want %d", sum, wantSum)
			}
			if m.Keys().Size() != len(ref) || m.Values().Length() != len(ref) || m.ToList().Length() != len(ref) {
				p.Fatalf("Keys/Values/ToList sizes %d/%d/%d, want %d",
					m.Keys().Size(), m.Values().Length(), m.ToList().Length(), len(ref))
			}
			gotValues := m.Values().ToGoSlice()
			wantValues := slices.Collect(maps.Values(ref))
			slices.Sort(gotValues)
			slices.Sort(wantValues)
			p.EqInts("Values multiset", gotValues, wantValues)

			// PutAll / Merge against a second random map.
			other := EmptyHashMap[K, int]()
			refOther := map[K]int{}
			for i := 0; i < n/2+1; i++ {
				k, v := kind.Mk(p.Rng.Intn(2*n+2)), p.Rng.Intn(100)
				other = other.Put(k, v)
				refOther[k] = v
			}
			wantAll := maps.Clone(ref)
			maps.Copy(wantAll, refOther)
			checkHashMap(p, "PutAll", m.PutAll(other), wantAll, absent)
			wantMerged := maps.Clone(ref)
			for k, v := range refOther {
				if old, ok := wantMerged[k]; ok {
					wantMerged[k] = old - v
				} else {
					wantMerged[k] = v
				}
			}
			checkHashMap(p, "Merge", m.Merge(other, func(a, b int) int { return a - b }), wantMerged, absent)
			checkHashMap(p, "receiver after PutAll/Merge", m, ref, absent)
		})
	}
}

func TestHashMapDifferential(t *testing.T) {
	runHashMapWorkload(t, IntKeys)
	runHashMapWorkload(t, StringKeys)
	runHashMapWorkload(t, CollidingKeys)
}

func checkHashSet[T comparable](p *Prop, what string, s HashSet[T], ref map[T]bool, absent []T) {
	p.T.Helper()
	if s.Size() != len(ref) || s.IsEmpty() != (len(ref) == 0) {
		p.Fatalf("%s: Size %d, want %d", what, s.Size(), len(ref))
	}
	for k := range ref {
		if !s.Contains(k) {
			p.Fatalf("%s: missing %v", what, k)
		}
	}
	for _, k := range absent {
		if !ref[k] && s.Contains(k) {
			p.Fatalf("%s: absent %v reported present", what, k)
		}
	}
	seen := map[T]int{}
	s.ForEach(func(x T) { seen[x]++ })
	for _, x := range s.ToGoSlice() {
		seen[x] += 10
	}
	for x, c := range seen {
		if c != 11 || !ref[x] {
			p.Fatalf("%s: element %v seen %d times by ForEach and %d times by ToGoSlice (member: %v)",
				what, x, c%10, c/10, ref[x])
		}
	}
	if len(seen) != len(ref) {
		p.Fatalf("%s: iteration saw %d elements, want %d", what, len(seen), len(ref))
	}
}

func runHashSetWorkload[T comparable](t *testing.T, kind KeyKind[T]) {
	for _, n := range MapSizes {
		if kind.Skips(n) {
			continue
		}
		t.Run(fmt.Sprintf("%s/%d", kind.Name, n), func(t *testing.T) {
			p := NewProp(t, n)
			absent := []T{kind.Mk(-1), kind.Mk(KeySpace(n) + 7)}
			s := EmptyHashSet[T]()
			ref := map[T]bool{}
			type version struct {
				s   HashSet[T]
				ref map[T]bool
			}
			var versions []version
			ops := p.MapWorkload(n)
			keep := SnapshotSteps(len(ops))
			for i, op := range ops {
				k := kind.Mk(op.Key)
				if op.Put {
					s = s.Add(k)
					ref[k] = true
				} else {
					s = s.Remove(k)
					delete(ref, k)
				}
				if s.Size() != len(ref) {
					p.Fatalf("after op %d (%+v): Size %d, want %d", i, op, s.Size(), len(ref))
				}
				if keep[i] {
					checkHashSet(p, fmt.Sprintf("after op %d", i), s, ref, absent)
					versions = append(versions, version{s, maps.Clone(ref)})
				}
			}
			for i, v := range versions {
				checkHashSet(p, fmt.Sprintf("version %d re-read", i), v.s, v.ref, absent)
			}
			elems := slices.Collect(maps.Keys(ref))
			checkHashSet(p, "HashSetFromSlice", HashSetFromSlice(elems), ref, absent)
			checkHashSet(p, "HashSetOf", HashSetOf(elems...), ref, absent)

			other := EmptyHashSet[T]()
			refOther := map[T]bool{}
			for i := 0; i < n/2+1; i++ {
				k := kind.Mk(p.Rng.Intn(2*n + 2))
				other = other.Add(k)
				refOther[k] = true
			}
			union, inter, diff := maps.Clone(ref), map[T]bool{}, map[T]bool{}
			maps.Copy(union, refOther)
			for k := range ref {
				if refOther[k] {
					inter[k] = true
				} else {
					diff[k] = true
				}
			}
			checkHashSet(p, "Union", s.Union(other), union, absent)
			checkHashSet(p, "Intersect", s.Intersect(other), inter, absent)
			checkHashSet(p, "Diff", s.Diff(other), diff, absent)
			if s.SubsetOf(other) != (len(diff) == 0) || !s.Intersect(other).SubsetOf(s) {
				p.Fatalf("SubsetOf disagrees with the model")
			}
			checkHashSet(p, "receiver after set algebra", s, ref, absent)
			if HashSet_FoldLeft(s, 0, func(acc int, _ T) int { return acc + 1 }) != len(ref) {
				p.Fatalf("FoldLeft visited a wrong number of elements")
			}
		})
	}
}

func TestHashSetDifferential(t *testing.T) {
	runHashSetWorkload(t, IntKeys)
	runHashSetWorkload(t, StringKeys)
	runHashSetWorkload(t, CollidingKeys)
}


func checkTreeMap[K cmp.Ordered](p *Prop, what string, m TreeMap[K, int], ref map[K]int) {
	p.T.Helper()
	if m.Size() != len(ref) || m.IsEmpty() != (len(ref) == 0) {
		p.Fatalf("%s: Size %d, want %d", what, m.Size(), len(ref))
	}
	keys := SortedKeys(ref)
	var gotKeys []K
	m.ForEachKV(func(k K, v int) {
		gotKeys = append(gotKeys, k)
		if ref[k] != v {
			p.Fatalf("%s: ForEachKV saw %v -> %d, want %d", what, k, v, ref[k])
		}
	})
	EqSlices(p, what+" ForEachKV key order", gotKeys, keys)
	entries := m.ToList().ToGoSlice()
	if len(entries) != len(keys) {
		p.Fatalf("%s: ToList has %d entries, want %d", what, len(entries), len(keys))
	}
	for i, e := range entries {
		if e.V1.Get() != keys[i] || e.V2.Get() != ref[keys[i]] {
			p.Fatalf("%s: ToList entry %d is %v, want (%v, %d)", what, i, e, keys[i], ref[keys[i]])
		}
	}
	for k, v := range ref {
		if !m.Contains(k) || m.Get(k).GetOrElse(-1) != v {
			p.Fatalf("%s: key %v: Contains %v, Get %v, want %d", what, k, m.Contains(k), m.Get(k), v)
		}
	}
	if len(keys) > 0 {
		if m.MinKey() != keys[0] || m.MaxKey() != keys[len(keys)-1] {
			p.Fatalf("%s: MinKey/MaxKey %v/%v, want %v/%v", what, m.MinKey(), m.MaxKey(), keys[0], keys[len(keys)-1])
		}
	} else if m.MinKeyOption().IsDefined() || m.MaxKeyOption().IsDefined() {
		p.Fatalf("%s: Min/MaxKeyOption defined on empty map", what)
	}
}

func runTreeMapWorkload[K cmp.Ordered](t *testing.T, kind KeyKind[K]) {
	for _, n := range MapSizes {
		if kind.Skips(n) {
			continue
		}
		t.Run(fmt.Sprintf("%s/%d", kind.Name, n), func(t *testing.T) {
			p := NewProp(t, n)
			m := EmptyTreeMap[K, int]()
			ref := map[K]int{}
			type version struct {
				m   TreeMap[K, int]
				ref map[K]int
			}
			var versions []version
			ops := p.MapWorkload(n)
			keep := SnapshotSteps(len(ops))
			for i, op := range ops {
				k := kind.Mk(op.Key)
				if op.Put {
					m = m.Put(k, op.Value)
					ref[k] = op.Value
				} else {
					m = m.Remove(k)
					delete(ref, k)
				}
				if m.Size() != len(ref) {
					p.Fatalf("after op %d (%+v): Size %d, want %d", i, op, m.Size(), len(ref))
				}
				if keep[i] {
					checkTreeMap(p, fmt.Sprintf("after op %d", i), m, ref)
					versions = append(versions, version{m, maps.Clone(ref)})
				}
			}
			for i, v := range versions {
				checkTreeMap(p, fmt.Sprintf("version %d re-read", i), v.m, v.ref)
			}
			checkTreeMap(p, "TreeMapFromGoMap", TreeMapFromGoMap(ref), ref)
			entries := make([]Tuple[K, int], 0, len(ref))
			for k, v := range ref {
				entries = append(entries, TupleOf(k, v))
			}
			checkTreeMap(p, "TreeMapFromSlice", TreeMapFromSlice(entries), ref)

			// Range bounds are inclusive on both ends.
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
				checkTreeMap(p, fmt.Sprintf("Range(%v,%v)", lo, hi), m.Range(lo, hi), want)
				checkTreeMap(p, fmt.Sprintf("RangeFrom(%v)", lo), m.RangeFrom(lo), wantFrom)
				checkTreeMap(p, fmt.Sprintf("RangeTo(%v)", hi), m.RangeTo(hi), wantTo)
			}
			even := func(_ K, v int) bool { return v%2 == 0 }
			wantEven := maps.Clone(ref)
			maps.DeleteFunc(wantEven, func(_ K, v int) bool { return v%2 != 0 })
			checkTreeMap(p, "Filter", m.Filter(even), wantEven)
			EqSlices(p, "Keys order", m.Keys().ToGoSlice(), SortedKeys(ref))
		})
	}
}

func TestTreeMapDifferential(t *testing.T) {
	runTreeMapWorkload(t, IntKeys)
	runTreeMapWorkload(t, StringKeys)
}

func runTreeSetWorkload[T cmp.Ordered](t *testing.T, kind KeyKind[T]) {
	for _, n := range MapSizes {
		if kind.Skips(n) {
			continue
		}
		t.Run(fmt.Sprintf("%s/%d", kind.Name, n), func(t *testing.T) {
			p := NewProp(t, n)
			s := EmptyTreeSet[T]()
			ref := map[T]bool{}
			check := func(what string, s TreeSet[T], ref map[T]bool) {
				t.Helper()
				want := SortedKeys(ref)
				if s.Size() != len(want) {
					p.Fatalf("%s: Size %d, want %d", what, s.Size(), len(want))
				}
				EqSlices(p, what+" ToGoSlice order", s.ToGoSlice(), want)
				var visited []T
				s.ForEach(func(x T) { visited = append(visited, x) })
				EqSlices(p, what+" ForEach order", visited, want)
				if len(want) > 0 && (s.Min() != want[0] || s.Max() != want[len(want)-1]) {
					p.Fatalf("%s: Min/Max %v/%v, want %v/%v", what, s.Min(), s.Max(), want[0], want[len(want)-1])
				}
			}
			type version struct {
				s   TreeSet[T]
				ref map[T]bool
			}
			var versions []version
			ops := p.MapWorkload(n)
			keep := SnapshotSteps(len(ops))
			for i, op := range ops {
				k := kind.Mk(op.Key)
				if op.Put {
					s = s.Add(k)
					ref[k] = true
				} else {
					s = s.Remove(k)
					delete(ref, k)
				}
				if s.Size() != len(ref) || s.Contains(k) != op.Put {
					p.Fatalf("after op %d (%+v): Size %d (want %d), Contains %v", i, op, s.Size(), len(ref), s.Contains(k))
				}
				if keep[i] {
					check(fmt.Sprintf("after op %d", i), s, ref)
					versions = append(versions, version{s, maps.Clone(ref)})
				}
			}
			for i, v := range versions {
				check(fmt.Sprintf("version %d re-read", i), v.s, v.ref)
			}
			check("TreeSetFromSlice", TreeSetFromSlice(slices.Collect(maps.Keys(ref))), ref)
			for r := 0; r < 5; r++ {
				lo, hi := kind.Mk(p.Rng.Intn(n+2)), kind.Mk(p.Rng.Intn(n+2))
				want, wantFrom, wantTo := map[T]bool{}, map[T]bool{}, map[T]bool{}
				for k := range ref {
					if k >= lo && k <= hi {
						want[k] = true
					}
					if k >= lo {
						wantFrom[k] = true
					}
					if k <= hi {
						wantTo[k] = true
					}
				}
				check(fmt.Sprintf("Range(%v,%v)", lo, hi), s.Range(lo, hi), want)
				check(fmt.Sprintf("RangeFrom(%v)", lo), s.RangeFrom(lo), wantFrom)
				check(fmt.Sprintf("RangeTo(%v)", hi), s.RangeTo(hi), wantTo)
			}
			other := EmptyTreeSet[T]()
			refOther := map[T]bool{}
			for i := 0; i < n/2+1; i++ {
				k := kind.Mk(p.Rng.Intn(2*n + 2))
				other = other.Add(k)
				refOther[k] = true
			}
			union, inter, diff := maps.Clone(ref), map[T]bool{}, map[T]bool{}
			maps.Copy(union, refOther)
			for k := range ref {
				if refOther[k] {
					inter[k] = true
				} else {
					diff[k] = true
				}
			}
			check("Union", s.Union(other), union)
			check("Intersect", s.Intersect(other), inter)
			check("Diff", s.Diff(other), diff)
			check("receiver after set algebra", s, ref)
		})
	}
}

func TestTreeSetDifferential(t *testing.T) {
	runTreeSetWorkload(t, IntKeys)
	runTreeSetWorkload(t, StringKeys)
}
