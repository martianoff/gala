package collection_mutable

// Differential tests: mutable Array and List against a Go slice reference
// model, at the same boundary sizes as the immutable collections.

import (
	"fmt"
	"slices"
	"testing"

	. "martianoff/gala/internal/collprop"
	. "martianoff/gala/std"
)

// mutSeq is the in-place API that Array and List share.
type mutSeq interface {
	Length() int
	Size() int
	IsEmpty() bool
	Get(index int) int
	Set(index int, value int)
	Append(value int)
	Prepend(value int)
	Insert(index int, value int)
	RemoveFirst() int
	RemoveLast() int
	Reverse()
	Clear()
	AppendAll(values []int)
	PrependAll(values []int)
	ToGoSlice() []int
	ForEach(f func(int))
	Contains(elem int) bool
	IndexOf(elem int) int
	LastIndexOf(elem int) int
	Count(p func(int) bool) int
	Exists(p func(int) bool) bool
	ForAll(p func(int) bool) bool
}

var (
	_ mutSeq = (*Array[int])(nil)
	_ mutSeq = (*List[int])(nil)
)

type seqKind struct {
	name     string
	from     func([]int) mutSeq
	clone    func(mutSeq) mutSeq
	removeAt func(mutSeq, int) // RemoveAt returns the element on List only
}

var seqKinds = []seqKind{
	{
		name:     "Array",
		from:     func(xs []int) mutSeq { return ArrayFromSlice(xs) },
		clone:    func(s mutSeq) mutSeq { return s.(*Array[int]).Clone() },
		removeAt: func(s mutSeq, i int) { s.(*Array[int]).RemoveAt(i) },
	},
	{
		name:     "List",
		from:     func(xs []int) mutSeq { return ListFromSlice(xs) },
		clone:    func(s mutSeq) mutSeq { return s.(*List[int]).Clone() },
		removeAt: func(s mutSeq, i int) { s.(*List[int]).RemoveAt(i) },
	},
}

func checkSeq(p *Prop, what string, s mutSeq, ref []int, full bool) {
	p.T.Helper()
	n := len(ref)
	if s.Length() != n || s.Size() != n || s.IsEmpty() != (n == 0) {
		p.Fatalf("%s: Length %d / IsEmpty %v, want length %d", what, s.Length(), s.IsEmpty(), n)
	}
	p.EqInts(what+" ToGoSlice", s.ToGoSlice(), ref)
	var visited []int
	s.ForEach(func(x int) { visited = append(visited, x) })
	p.EqInts(what+" ForEach", visited, ref)
	// List.Get is O(n): read every index only when asked to.
	reads := min(n, 50)
	if full {
		reads = n
	}
	for k := 0; k < reads; k++ {
		i := k
		if !full {
			i = p.Rng.Intn(n)
		}
		if got := s.Get(i); got != ref[i] {
			p.Fatalf("%s: Get(%d) = %d, want %d", what, i, got, ref[i])
		}
	}
}

// TestMutableSeqWorkload drives Array and List through random in-place edits,
// checking against a slice after every step, and checks that a Clone taken
// up front never sees any of them.
func TestMutableSeqWorkload(t *testing.T) {
	for _, kind := range seqKinds {
		for _, n := range BoundarySizes {
			t.Run(fmt.Sprintf("%s/%d", kind.name, n), func(t *testing.T) {
				p := NewProp(t, n)
				ref := p.Ints(n, 1000)
				src := slices.Clone(ref)
				s := kind.from(src)
				for i := range src {
					src[i] = -1 // FromSlice copies
				}
				snapshot := kind.clone(s)
				snapshotRef := slices.Clone(ref)
				checkSeq(p, "start", s, ref, n <= 1056 || kind.name == "Array")

				for step := 0; step < 300; step++ {
					var op string
					v := p.Rng.Intn(1000)
					switch r := p.Rng.Intn(20); {
					case r < 4:
						op = fmt.Sprintf("Append(%d)", v)
						s.Append(v)
						ref = append(ref, v)
					case r < 7:
						op = fmt.Sprintf("Prepend(%d)", v)
						s.Prepend(v)
						ref = slices.Insert(ref, 0, v)
					case r < 10:
						i := p.Rng.Intn(len(ref) + 1)
						op = fmt.Sprintf("Insert(%d,%d)", i, v)
						s.Insert(i, v)
						ref = slices.Insert(ref, i, v)
					case r < 12 && len(ref) > 0:
						i := p.Rng.Intn(len(ref))
						op = fmt.Sprintf("RemoveAt(%d)", i)
						kind.removeAt(s, i)
						ref = slices.Delete(ref, i, i+1)
					case r < 14 && len(ref) > 0:
						i := p.Rng.Intn(len(ref))
						op = fmt.Sprintf("Set(%d,%d)", i, v)
						s.Set(i, v)
						ref[i] = v
					case r < 15 && len(ref) > 0:
						op = "RemoveFirst"
						if got := s.RemoveFirst(); got != ref[0] {
							p.Fatalf("step %d RemoveFirst returned %d, want %d", step, got, ref[0])
						}
						ref = ref[1:]
					case r < 16 && len(ref) > 0:
						op = "RemoveLast"
						if got := s.RemoveLast(); got != ref[len(ref)-1] {
							p.Fatalf("step %d RemoveLast returned %d, want %d", step, got, ref[len(ref)-1])
						}
						ref = ref[:len(ref)-1]
					case r < 17:
						op = "Reverse"
						s.Reverse()
						ref = RefReverse(ref)
					case r < 18:
						extra := p.Ints(p.Rng.Intn(40), 1000)
						op = fmt.Sprintf("AppendAll(%d)", len(extra))
						s.AppendAll(extra)
						ref = append(ref, extra...)
					case r < 19:
						extra := p.Ints(p.Rng.Intn(40), 1000)
						op = fmt.Sprintf("PrependAll(%d)", len(extra))
						s.PrependAll(extra)
						ref = append(slices.Clone(extra), ref...)
					default:
						if p.Rng.Intn(10) != 0 {
							continue
						}
						op = "Clear"
						s.Clear()
						ref = nil
					}
					ref = slices.Clone(ref)
					if s.Length() != len(ref) {
						p.Fatalf("after step %d %s: Length %d, want %d", step, op, s.Length(), len(ref))
					}
					if len(ref) > 0 {
						for _, i := range []int{0, len(ref) - 1, p.Rng.Intn(len(ref))} {
							if s.Get(i) != ref[i] {
								p.Fatalf("after step %d %s: Get(%d) = %d, want %d", step, op, i, s.Get(i), ref[i])
							}
						}
					}
					if step%25 == 0 || n <= 64 {
						checkSeq(p, fmt.Sprintf("after step %d %s", step, op), s, ref, false)
					}
				}
				checkSeq(p, "end", s, ref, len(ref) <= 1056 || kind.name == "Array")
				checkSeq(p, "clone taken at start", snapshot, snapshotRef, false)
			})
		}
	}
}

// checkQueries compares the read-only queries of s with ref.
func checkQueries(p *Prop, s mutSeq, ref []int) {
	p.T.Helper()
	n := len(ref)
	isEven := func(x int) bool { return x%2 == 0 }
	count := len(RefFilter(ref, isEven))
	if s.Count(isEven) != count || s.Exists(isEven) != (count > 0) || s.ForAll(isEven) != (count == n) {
		p.Fatalf("Count/Exists/ForAll disagree with count %d of %d", count, n)
	}
	for _, probe := range []int{0, 1, n / 8, n/4 + 5} {
		if got := s.IndexOf(probe); got != RefIndexOf(ref, probe) {
			p.Fatalf("IndexOf(%d) = %d, want %d", probe, got, RefIndexOf(ref, probe))
		}
		if got := s.LastIndexOf(probe); got != RefLastIndexOf(ref, probe) {
			p.Fatalf("LastIndexOf(%d) = %d, want %d", probe, got, RefLastIndexOf(ref, probe))
		}
		if s.Contains(probe) != (RefIndexOf(ref, probe) >= 0) {
			p.Fatalf("Contains(%d) wrong", probe)
		}
	}
}

func TestMutableArrayOpsDifferential(t *testing.T) {
	isEven := func(x int) bool { return x%2 == 0 }
	odd := func(x int) bool { return !isEven(x) }
	for _, n := range BoundarySizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			p := NewProp(t, n)
			ref := p.Ints(n, n/4+2)
			a := ArrayFromSlice(ref)
			var log VisitLog
			for name, got := range map[string]*Array[int]{
				"ArrayOf":       ArrayOf(ref...),
				"ArrayTabulate": ArrayTabulate(n, func(i int) int { return ref[i] }),
				"ListToArray":   ListFromSlice(ref).ToArray(),
			} {
				p.EqInts(name, got.ToGoSlice(), ref)
			}

			p.EqInts("Map", Array_Map(a, func(x int) int { log.Record(x); return x*3 + 1 }).ToGoSlice(),
				RefMap(ref, func(x int) int { return x*3 + 1 }))
			log.Expect(p, "Map", ref)
			p.EqInts("Filter", a.Filter(func(x int) bool { log.Record(x); return isEven(x) }).ToGoSlice(), RefFilter(ref, isEven))
			log.Expect(p, "Filter", ref)
			p.EqInts("FilterNot", a.FilterNot(isEven).ToGoSlice(), RefFilter(ref, odd))
			p.EqInts("FlatMap", Array_FlatMap(a, func(x int) *Array[int] { log.Record(x); return ArrayFromSlice(FlatMapFn(x)) }).ToGoSlice(),
				RefFlatMap(ref))
			log.Expect(p, "FlatMap", ref)
			p.EqInts("Collect", Array_Collect(a, func(x int) Option[int] {
				log.Record(x)
				if isEven(x) {
					return Some[int]{}.Apply(x)
				}
				return None[int]{}.Apply()
			}).ToGoSlice(), RefFilter(ref, isEven))
			log.Expect(p, "Collect", ref)
			p.EqInts("FoldLeft", Array_FoldLeft(a, []int{}, func(acc []int, x int) []int { return append(acc, x) }), ref)
			p.EqInts("FoldRight", Array_FoldRight(a, []int{}, func(x int, acc []int) []int { return append(acc, x) }), RefReverse(ref))
			p.EqInts("Reversed", a.Reversed().ToGoSlice(), RefReverse(ref))
			for _, k := range p.Cuts(n) {
				p.EqInts(fmt.Sprintf("Take(%d)", k), a.Take(k).ToGoSlice(), RefTake(ref, k))
				p.EqInts(fmt.Sprintf("Drop(%d)", k), a.Drop(k).ToGoSlice(), RefDrop(ref, k))
				j := k + p.Rng.Intn(n+2) - 1
				p.EqInts(fmt.Sprintf("Slice(%d,%d)", k, j), a.Slice(k, j).ToGoSlice(), RefSlice(ref, k, j))
				split := a.SplitAt(k)
				p.EqInts("SplitAt.1", split.V1.Get().ToGoSlice(), RefTake(ref, k))
				p.EqInts("SplitAt.2", split.V2.Get().ToGoSlice(), RefDrop(ref, k))
			}
			if n > 0 {
				p.EqInts("Tail", a.Tail().ToGoSlice(), ref[1:])
				p.EqInts("Init", a.Init().ToGoSlice(), ref[:n-1])
			}
			below := func(x int) bool { return x < n/8 }
			p.EqInts("TakeWhile", a.TakeWhile(below).ToGoSlice(), RefTakeWhile(ref, below))
			p.EqInts("DropWhile", a.DropWhile(below).ToGoSlice(), RefDropWhile(ref, below))
			part := a.Partition(isEven)
			p.EqInts("Partition.1", part.V1.Get().ToGoSlice(), RefFilter(ref, isEven))
			p.EqInts("Partition.2", part.V2.Get().ToGoSlice(), RefFilter(ref, odd))
			other := p.Ints(n/2+3, 100)
			zipped := Array_Zip(a, ArrayFromSlice(other)).ToGoSlice()
			if len(zipped) != min(n, len(other)) {
				p.Fatalf("Zip: length %d, want %d", len(zipped), min(n, len(other)))
			}
			for i, z := range zipped {
				if z.V1.Get() != ref[i] || z.V2.Get() != other[i] {
					p.Fatalf("Zip: element %d is %v", i, z)
				}
			}
			for i, z := range Array_ZipWithIndex(a).ToGoSlice() {
				if z.V1.Get() != ref[i] || z.V2.Get() != i {
					p.Fatalf("ZipWithIndex: element %d is %v", i, z)
				}
			}
			key := func(x int) int { return x % 7 }
			groups := map[int][]int{}
			for k, g := range Array_GroupBy(a, func(x int) int { log.Record(x); return key(x) }) {
				groups[k] = g.ToGoSlice()
			}
			log.Expect(p, "GroupBy", ref)
			EqGroups(p, "GroupBy", groups, RefGroupBy(ref, key))
			if n <= 2048 {
				p.EqInts("Distinct", a.Distinct().ToGoSlice(), RefDistinct(ref))
				for _, size := range []int{1, 3, 32} {
					flat := []int{}
					for _, g := range Array_Grouped(a, size).ToGoSlice() {
						flat = append(flat, g.ToGoSlice()...)
					}
					p.EqInts(fmt.Sprintf("Grouped(%d)", size), flat, ref)
					windows := Array_Sliding(a, size).ToGoSlice()
					if len(windows) != max(0, n-size+1) {
						p.Fatalf("Sliding(%d): %d windows, want %d", size, len(windows), max(0, n-size+1))
					}
					for wi, win := range windows {
						p.EqInts("Sliding window", win.ToGoSlice(), ref[wi:wi+size])
					}
				}
			}
			checkQueries(p, a, ref)
			// None of the derived operations touched the receiver.
			p.EqInts("receiver after derived ops", a.ToGoSlice(), ref)
		})
	}
}

func TestMutableListOpsDifferential(t *testing.T) {
	isEven := func(x int) bool { return x%2 == 0 }
	odd := func(x int) bool { return !isEven(x) }
	for _, n := range BoundarySizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			p := NewProp(t, n)
			ref := p.Ints(n, n/4+2)
			l := ListFromSlice(ref)
			var log VisitLog
			p.EqInts("ListOf", ListOf(ref...).ToGoSlice(), ref)
			p.EqInts("ArrayToList", ArrayFromSlice(ref).ToList().ToGoSlice(), ref)

			p.EqInts("Map", List_Map(l, func(x int) int { log.Record(x); return x*3 + 1 }).ToGoSlice(),
				RefMap(ref, func(x int) int { return x*3 + 1 }))
			log.Expect(p, "Map", ref)
			p.EqInts("Filter", l.Filter(func(x int) bool { log.Record(x); return isEven(x) }).ToGoSlice(), RefFilter(ref, isEven))
			log.Expect(p, "Filter", ref)
			p.EqInts("FilterNot", l.FilterNot(isEven).ToGoSlice(), RefFilter(ref, odd))
			p.EqInts("FlatMap", List_FlatMap(l, func(x int) *List[int] { log.Record(x); return ListFromSlice(FlatMapFn(x)) }).ToGoSlice(),
				RefFlatMap(ref))
			log.Expect(p, "FlatMap", ref)
			p.EqInts("FoldLeft", List_FoldLeft(l, []int{}, func(acc []int, x int) []int { return append(acc, x) }), ref)
			p.EqInts("FoldRight", List_FoldRight(l, []int{}, func(x int, acc []int) []int { return append(acc, x) }), RefReverse(ref))
			p.EqInts("Reversed", l.Reversed().ToGoSlice(), RefReverse(ref))
			for _, k := range p.Cuts(n) {
				p.EqInts(fmt.Sprintf("Take(%d)", k), l.Take(k).ToGoSlice(), RefTake(ref, k))
				p.EqInts(fmt.Sprintf("Drop(%d)", k), l.Drop(k).ToGoSlice(), RefDrop(ref, k))
				j := k + p.Rng.Intn(n+2) - 1
				p.EqInts(fmt.Sprintf("Slice(%d,%d)", k, j), l.Slice(k, j).ToGoSlice(), RefSlice(ref, k, j))
				split := l.SplitAt(k)
				p.EqInts("SplitAt.1", split.V1.Get().ToGoSlice(), RefTake(ref, k))
				p.EqInts("SplitAt.2", split.V2.Get().ToGoSlice(), RefDrop(ref, k))
			}
			if n > 0 {
				p.EqInts("Tail", l.Tail().ToGoSlice(), ref[1:])
				p.EqInts("Init", l.Init().ToGoSlice(), ref[:n-1])
			}
			below := func(x int) bool { return x < n/8 }
			p.EqInts("TakeWhile", l.TakeWhile(below).ToGoSlice(), RefTakeWhile(ref, below))
			p.EqInts("DropWhile", l.DropWhile(below).ToGoSlice(), RefDropWhile(ref, below))
			part := l.Partition(isEven)
			p.EqInts("Partition.1", part.V1.Get().ToGoSlice(), RefFilter(ref, isEven))
			p.EqInts("Partition.2", part.V2.Get().ToGoSlice(), RefFilter(ref, odd))
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
			if n <= 2048 {
				p.EqInts("Distinct", l.Distinct().ToGoSlice(), RefDistinct(ref))
			}
			checkQueries(p, l, ref)
			p.EqInts("receiver after derived ops", l.ToGoSlice(), ref)
		})
	}
}

// TestMutableSortStabilityDifferential compares Sorted / SortWith / SortBy on
// mutable Array and List with slices.SortStableFunc, and checks that sorting
// returns a new collection without reordering the receiver.
func TestMutableSortStabilityDifferential(t *testing.T) {
	for _, n := range SortSizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			p := NewProp(t, n)
			items := p.SortItems(n)
			want := RefStableSort(items)
			byKey := func(x, y SortItem) bool { return x.Key < y.Key }
			byKeyNonStrict := func(x, y SortItem) bool { return x.Key <= y.Key }
			key := func(x SortItem) int { return x.Key }

			a := ArrayFromSlice(items)
			EqSlices(p, "Array.Sorted", a.Sorted().ToGoSlice(), want)
			EqSlices(p, "Array.SortWith(<)", a.SortWith(byKey).ToGoSlice(), want)
			EqSlices(p, "Array.SortWith(<=)", a.SortWith(byKeyNonStrict).ToGoSlice(), want)
			calls := 0
			EqSlices(p, "Array.SortBy", Array_SortBy(a, func(x SortItem) int { calls++; return x.Key }).ToGoSlice(), want)
			if calls != n {
				p.Fatalf("Array.SortBy: key function ran %d times for %d elements", calls, n)
			}
			EqSlices(p, "Array receiver after sorting", a.ToGoSlice(), items)

			l := ListFromSlice(items)
			EqSlices(p, "List.Sorted", l.Sorted().ToGoSlice(), want)
			EqSlices(p, "List.SortWith(<)", l.SortWith(byKey).ToGoSlice(), want)
			EqSlices(p, "List.SortBy", List_SortBy(l, key).ToGoSlice(), want)
			EqSlices(p, "List receiver after sorting", l.ToGoSlice(), items)
		})
	}
}
