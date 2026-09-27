package collection_immutable

// Differential tests: immutable List against a Go slice reference model.

import (
	"fmt"
	"maps"
	"slices"
	"testing"

	. "martianoff/gala/internal/collprop"
	. "martianoff/gala/std"
)

func listPaths(p *Prop, ref []int) map[string]List[int] {
	n := len(ref)
	paths := map[string]List[int]{
		"ListFromSlice": ListFromSlice(ref),
		"ListOf":        ListOf(ref...),
		"ArrayToList":   ArrayFromSlice(ref).ToList(),
		"Map":           List_Map(ListFromSlice(ref), func(x int) int { return x }),
	}
	prepended := EmptyList[int]()
	for i := n - 1; i >= 0; i-- {
		prepended = prepended.Prepend(ref[i])
	}
	paths["PrependChain"] = prepended
	if n <= 1025 {
		// Append is O(n): an append chain is quadratic, so it stays small.
		appended := EmptyList[int]()
		for _, x := range ref {
			appended = appended.Append(x)
		}
		paths["AppendChain"] = appended
	}
	cut := p.Rng.Intn(n + 1)
	paths["AppendAll"] = ListFromSlice(ref[:cut]).AppendAll(ListFromSlice(ref[cut:]))
	paths["PrependAll"] = ListFromSlice(ref[cut:]).PrependAll(ListFromSlice(ref[:cut]))
	paths["Flatten"] = Flatten(ListOf(ListFromSlice(ref[:cut]), EmptyList[int](), ListFromSlice(ref[cut:])))
	return paths
}

func checkList(p *Prop, what string, l List[int], ref []int) {
	p.T.Helper()
	n := len(ref)
	if l.Length() != n || l.Size() != n || l.IsEmpty() != (n == 0) || l.NonEmpty() != (n > 0) {
		p.Fatalf("%s: Length %d / IsEmpty %v, want length %d", what, l.Length(), l.IsEmpty(), n)
	}
	p.EqInts(what+" ToGoSlice", l.ToGoSlice(), ref)
	var visited []int
	l.ForEach(func(x int) { visited = append(visited, x) })
	p.EqInts(what+" ForEach", visited, ref)
	// Get is O(i): spot-check it rather than reading every index.
	for k := 0; k < min(n, 20); k++ {
		i := p.Rng.Intn(n)
		if got := l.Get(i); got != ref[i] {
			p.Fatalf("%s: Get(%d) = %d, want %d", what, i, got, ref[i])
		}
	}
	if l.GetOption(-1).IsDefined() || l.GetOption(n).IsDefined() {
		p.Fatalf("%s: GetOption out of range is defined", what)
	}
	if n > 0 && (l.Head() != ref[0] || l.Last() != ref[n-1]) {
		p.Fatalf("%s: Head/Last = %d/%d, want %d/%d", what, l.Head(), l.Last(), ref[0], ref[n-1])
	}
}

func TestListConstructionDifferential(t *testing.T) {
	for _, n := range BoundarySizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			p := NewProp(t, n)
			ref := p.Ints(n, n/4+2)
			paths := listPaths(p, ref)
			// Sorted names: checkList draws from p.Rng, so the path order must
			// not follow Go's unseeded map iteration.
			for _, name := range slices.Sorted(maps.Keys(paths)) {
				checkList(p, name, paths[name], ref)
			}
		})
	}
}

func TestListOpsDifferential(t *testing.T) {
	isEven := func(x int) bool { return x%2 == 0 }
	for _, n := range BoundarySizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			p := NewProp(t, n)
			ref := p.Ints(n, n/4+2)
			l := ListFromSlice(ref)
			var log VisitLog

			checkList(p, "Map", List_Map(l, func(x int) int { log.Record(x); return x*3 + 1 }),
				RefMap(ref, func(x int) int { return x*3 + 1 }))
			log.Expect(p, "Map", ref)
			checkList(p, "Filter", l.Filter(func(x int) bool { log.Record(x); return isEven(x) }), RefFilter(ref, isEven))
			log.Expect(p, "Filter", ref)
			checkList(p, "FilterNot", l.FilterNot(isEven), RefFilter(ref, func(x int) bool { return !isEven(x) }))
			checkList(p, "FlatMap", List_FlatMap(l, func(x int) List[int] {
				log.Record(x)
				return ListFromSlice(FlatMapFn(x))
			}), RefFlatMap(ref))
			log.Expect(p, "FlatMap", ref)
			checkList(p, "Collect", List_Collect(l, func(x int) Option[int] {
				log.Record(x)
				if isEven(x) {
					return Some[int]{}.Apply(x)
				}
				return None[int]{}.Apply()
			}), RefFilter(ref, isEven))
			log.Expect(p, "Collect", ref)

			p.EqInts("FoldLeft", List_FoldLeft(l, []int{}, func(acc []int, x int) []int { return append(acc, x) }), ref)
			p.EqInts("FoldRight", List_FoldRight(l, []int{}, func(x int, acc []int) []int { return append(acc, x) }), RefReverse(ref))
			checkList(p, "Reverse", l.Reverse(), RefReverse(ref))

			for _, k := range p.Cuts(n) {
				checkList(p, fmt.Sprintf("Take(%d)", k), l.Take(k), RefTake(ref, k))
				checkList(p, fmt.Sprintf("Drop(%d)", k), l.Drop(k), RefDrop(ref, k))
				j := k + p.Rng.Intn(n+2) - 1
				checkList(p, fmt.Sprintf("Slice(%d,%d)", k, j), l.Slice(k, j), RefSlice(ref, k, j))
				split := l.SplitAt(k)
				checkList(p, "SplitAt.1", split.V1.Get(), RefTake(ref, k))
				checkList(p, "SplitAt.2", split.V2.Get(), RefDrop(ref, k))
			}
			if n > 0 {
				checkList(p, "Tail", l.Tail(), ref[1:])
				checkList(p, "Init", l.Init(), ref[:n-1])
				i, v := p.Rng.Intn(n), -5
				want := slices.Clone(ref)
				want[i] = v
				checkList(p, "Updated", l.Updated(i, v), want)
				checkList(p, "receiver after Updated", l, ref)
			}

			below := func(x int) bool { return x < n/8 }
			checkList(p, "TakeWhile", l.TakeWhile(below), RefTakeWhile(ref, below))
			checkList(p, "DropWhile", l.DropWhile(below), RefDropWhile(ref, below))
			span := l.Span(below)
			checkList(p, "Span.1", span.V1.Get(), RefTakeWhile(ref, below))
			checkList(p, "Span.2", span.V2.Get(), RefDropWhile(ref, below))
			part := l.Partition(isEven)
			checkList(p, "Partition.1", part.V1.Get(), RefFilter(ref, isEven))
			checkList(p, "Partition.2", part.V2.Get(), RefFilter(ref, func(x int) bool { return !isEven(x) }))
			pm := List_PartitionMap(l, func(x int) Either[int, int] {
				if isEven(x) {
					return Left[int, int]{}.Apply(x)
				}
				return Right[int, int]{}.Apply(x)
			})
			checkList(p, "PartitionMap.left", pm.V1.Get(), RefFilter(ref, isEven))
			checkList(p, "PartitionMap.right", pm.V2.Get(), RefFilter(ref, func(x int) bool { return !isEven(x) }))

			other := p.Ints(n/2+3, 100)
			zipped := List_Zip(l, ListFromSlice(other)).ToGoSlice()
			if len(zipped) != min(n, len(other)) {
				p.Fatalf("Zip: length %d, want %d", len(zipped), min(n, len(other)))
			}
			for i, z := range zipped {
				if z.V1.Get() != ref[i] || z.V2.Get() != other[i] {
					p.Fatalf("Zip: element %d is %v", i, z)
				}
			}
			for i, z := range List_ZipWithIndex(l).ToGoSlice() {
				if z.V1.Get() != ref[i] || z.V2.Get() != i {
					p.Fatalf("ZipWithIndex: element %d is %v", i, z)
				}
			}

			key := func(x int) int { return x % 7 }
			groups := map[int][]int{}
			for k, g := range List_GroupBy(l, func(x int) int { log.Record(x); return key(x) }) {
				groups[k] = g.ToGoSlice()
			}
			log.Expect(p, "GroupBy", ref)
			EqGroups(p, "GroupBy", groups, RefGroupBy(ref, key))
			mapped := map[int][]int{}
			var valueLog VisitLog
			for k, g := range List_GroupMap(l,
				func(x int) int { log.Record(x); return key(x) },
				func(x int) int { valueLog.Record(x); return x + 1 }) {
				mapped[k] = g.ToGoSlice()
			}
			log.Expect(p, "GroupMap key", ref)
			valueLog.Expect(p, "GroupMap value", ref)
			wantMapped := map[int][]int{}
			for k, g := range RefGroupBy(ref, key) {
				wantMapped[k] = RefMap(g, func(x int) int { return x + 1 })
			}
			EqGroups(p, "GroupMap", mapped, wantMapped)

			for _, probe := range []int{0, 1, n / 8, n/4 + 5} {
				if got := l.IndexOf(probe); got != RefIndexOf(ref, probe) {
					p.Fatalf("IndexOf(%d) = %d, want %d", probe, got, RefIndexOf(ref, probe))
				}
				if got := l.LastIndexOf(probe); got != RefLastIndexOf(ref, probe) {
					p.Fatalf("LastIndexOf(%d) = %d, want %d", probe, got, RefLastIndexOf(ref, probe))
				}
				if l.Contains(probe) != (RefIndexOf(ref, probe) >= 0) {
					p.Fatalf("Contains(%d) wrong", probe)
				}
			}
			count := len(RefFilter(ref, isEven))
			if l.Count(isEven) != count || l.Exists(isEven) != (count > 0) || l.ForAll(isEven) != (count == n) {
				p.Fatalf("Count/Exists/ForAll disagree with count %d of %d", count, n)
			}
			if n <= 2048 {
				// Distinct is quadratic. Like Array.Distinct (and Scala), it keeps
				// the first occurrence of each element, in order.
				checkList(p, "Distinct", l.Distinct(), RefDistinct(ref))
			}
		})
	}
}
