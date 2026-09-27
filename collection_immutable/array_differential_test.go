package collection_immutable

// Differential tests: immutable Array against a Go slice reference model at
// every trie-depth boundary, for every construction path (the paths build
// structurally different tries: builder-made, append-grown, prefix-grown).

import (
	"fmt"
	"slices"
	"testing"

	. "martianoff/gala/internal/collprop"
	. "martianoff/gala/std"
)

// arrayPaths builds the same sequence through every construction path.
func arrayPaths(p *Prop, ref []int) map[string]Array[int] {
	n := len(ref)
	paths := map[string]Array[int]{
		"ArrayFromSlice": ArrayFromSlice(ref),
		"ArrayOf":        ArrayOf(ref...),
		"ArrayTabulate":  ArrayTabulate(n, func(i int) int { return ref[i] }),
		"Map":            Array_Map(ArrayTabulate(n, func(i int) int { return i }), func(i int) int { return ref[i] }),
		"Filter": Array_Map(
			ArrayTabulate(2*n, func(i int) int { return i }).Filter(func(i int) bool { return i%2 == 0 }),
			func(i int) int { return ref[i/2] }),
		"ListToArray": ListFromSlice(ref).ToArray(),
	}
	appended := EmptyArray[int]()
	for _, x := range ref {
		appended = appended.Append(x)
	}
	paths["AppendChain"] = appended

	prepended := EmptyArray[int]()
	for i := n - 1; i >= 0; i-- {
		prepended = prepended.Prepend(ref[i])
	}
	paths["PrependChain"] = prepended

	// Grow outwards from a random midpoint: prepends and appends interleave, so
	// the prefix buffer and the trie are both non-trivial at the same time.
	mid := p.Rng.Intn(n + 1)
	mixed := EmptyArray[int]()
	lo, hi := mid, mid
	for lo > 0 || hi < n {
		if lo > 0 && (hi == n || p.Rng.Intn(2) == 0) {
			lo--
			mixed = mixed.Prepend(ref[lo])
		} else {
			mixed = mixed.Append(ref[hi])
			hi++
		}
	}
	paths["MixedChain"] = mixed

	cut := p.Rng.Intn(n + 1)
	paths["AppendAll"] = ArrayFromSlice(ref[:cut]).AppendAll(ArrayFromSlice(ref[cut:]))
	paths["PrependAll"] = ArrayFromSlice(ref[cut:]).PrependAll(ArrayFromSlice(ref[:cut]))
	paths["ReverseReverse"] = ArrayFromSlice(ref).Reverse().Reverse()
	return paths
}

// checkArray verifies every read path of a against ref.
func checkArray(p *Prop, what string, a Array[int], ref []int) {
	p.T.Helper()
	n := len(ref)
	if a.Length() != n || a.Size() != n {
		p.Fatalf("%s: Length %d / Size %d, want %d", what, a.Length(), a.Size(), n)
	}
	if a.IsEmpty() != (n == 0) || a.NonEmpty() != (n > 0) {
		p.Fatalf("%s: IsEmpty %v for length %d", what, a.IsEmpty(), n)
	}
	p.EqInts(what+" ToGoSlice", a.ToGoSlice(), ref)
	for i, want := range ref {
		if got := a.Get(i); got != want {
			p.Fatalf("%s: Get(%d) = %d, want %d", what, i, got, want)
		}
	}
	var visited []int
	a.ForEach(func(x int) { visited = append(visited, x) })
	p.EqInts(what+" ForEach", visited, ref)
	if a.GetOption(-1).IsDefined() || a.GetOption(n).IsDefined() {
		p.Fatalf("%s: GetOption out of range is defined", what)
	}
	if n > 0 {
		if a.Head() != ref[0] || a.Last() != ref[n-1] {
			p.Fatalf("%s: Head/Last = %d/%d, want %d/%d", what, a.Head(), a.Last(), ref[0], ref[n-1])
		}
	} else if a.HeadOption().IsDefined() || a.LastOption().IsDefined() {
		p.Fatalf("%s: HeadOption/LastOption defined on empty array", what)
	}
}

func TestArrayConstructionDifferential(t *testing.T) {
	for _, n := range BoundarySizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			p := NewProp(t, n)
			ref := p.Ints(n, n/4+2)
			for name, a := range arrayPaths(p, ref) {
				checkArray(p, name, a, ref)
			}
			// ArrayFromSlice copies: mutating the source afterwards is invisible.
			src := slices.Clone(ref)
			a := ArrayFromSlice(src)
			for i := range src {
				src[i] = -1
			}
			checkArray(p, "ArrayFromSlice after source mutation", a, ref)
			// ToGoSlice hands out a fresh copy.
			if n > 0 {
				out := a.ToGoSlice()
				out[0] = -1
				checkArray(p, "Array after ToGoSlice mutation", a, ref)
			}
			fill := ArrayFill(n, 7)
			checkArray(p, "ArrayFill", fill, slices.Repeat([]int{7}, n))
		})
	}
}

func TestArrayHugeConstructionDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("huge sizes skipped in -short mode")
	}
	for _, n := range HugeSizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			p := NewProp(t, n)
			ref := p.Ints(n, 1<<30)
			identity := ArrayTabulate(n, func(i int) int { return i })
			paths := map[string]Array[int]{
				"ArrayFromSlice": ArrayFromSlice(ref),
				"ArrayTabulate":  ArrayTabulate(n, func(i int) int { return ref[i] }),
				"Map":            Array_Map(identity, func(i int) int { return ref[i] }),
				"Filter": Array_Map(identity.Filter(func(i int) bool { return i%2 == 0 }),
					func(i int) int { return ref[i/2*2] }),
			}
			wantFilter := make([]int, 0, n/2+1)
			for i := 0; i < n; i += 2 {
				wantFilter = append(wantFilter, ref[i])
			}
			for name, a := range paths {
				want := ref
				if name == "Filter" {
					want = wantFilter
				}
				p.EqInts(name, a.ToGoSlice(), want)
				for k := 0; k < 2000; k++ {
					i := p.Rng.Intn(len(want))
					if a.Get(i) != want[i] {
						p.Fatalf("%s: Get(%d) = %d, want %d", name, i, a.Get(i), want[i])
					}
				}
			}
			// Appending across the depth boundary.
			grown := ArrayFromSlice(ref[:n-2]).Append(ref[n-2]).Append(ref[n-1])
			p.EqInts("Append across depth boundary", grown.ToGoSlice(), ref)
		})
	}
}

func TestArrayOpsDifferential(t *testing.T) {
	isEven := func(x int) bool { return x%2 == 0 }
	for _, n := range BoundarySizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			p := NewProp(t, n)
			ref := p.Ints(n, n/4+2)
			paths := arrayPaths(p, ref)
			for _, name := range []string{"ArrayFromSlice", "AppendChain", "PrependChain", "MixedChain", "Filter"} {
				a := paths[name]
				w := func(op string) string { return name + "." + op }
				var log VisitLog

				mapped := Array_Map(a, func(x int) int { log.Record(x); return x*3 + 1 })
				log.Expect(p, w("Map"), ref)
				checkArray(p, w("Map"), mapped, RefMap(ref, func(x int) int { return x*3 + 1 }))

				filtered := a.Filter(func(x int) bool { log.Record(x); return isEven(x) })
				log.Expect(p, w("Filter"), ref)
				checkArray(p, w("Filter"), filtered, RefFilter(ref, isEven))
				checkArray(p, w("FilterNot"), a.FilterNot(isEven), RefFilter(ref, func(x int) bool { return !isEven(x) }))

				flat := Array_FlatMap(a, func(x int) Array[int] {
					log.Record(x)
					return ArrayTabulate(x%3, func(j int) int { return x*10 + j })
				})
				log.Expect(p, w("FlatMap"), ref)
				checkArray(p, w("FlatMap"), flat, RefFlatMap(ref))

				collected := Array_Collect(a, func(x int) Option[int] {
					log.Record(x)
					if x%3 == 0 {
						return Some[int]{}.Apply(x / 3)
					}
					return None[int]{}.Apply()
				})
				log.Expect(p, w("Collect"), ref)
				want := []int{}
				for _, x := range ref {
					if x%3 == 0 {
						want = append(want, x/3)
					}
				}
				checkArray(p, w("Collect"), collected, want)

				folded := Array_FoldLeft(a, []int{}, func(acc []int, x int) []int { return append(acc, x) })
				p.EqInts(w("FoldLeft"), folded, ref)
				foldedR := Array_FoldRight(a, []int{}, func(x int, acc []int) []int { return append(acc, x) })
				p.EqInts(w("FoldRight"), foldedR, RefReverse(ref))
				if n > 0 {
					sum := 0
					for _, x := range ref {
						sum += x
					}
					if got := a.Reduce(func(x, y int) int { return x + y }); got != sum {
						p.Fatalf("%s: %d, want %d", w("Reduce"), got, sum)
					}
				} else if a.ReduceOption(func(x, y int) int { return x + y }).IsDefined() {
					p.Fatalf("%s: ReduceOption defined on empty", name)
				}

				for _, k := range p.Cuts(n) {
					checkArray(p, w(fmt.Sprintf("Take(%d)", k)), a.Take(k), RefTake(ref, k))
					checkArray(p, w(fmt.Sprintf("Drop(%d)", k)), a.Drop(k), RefDrop(ref, k))
					j := k + p.Rng.Intn(n+2) - 1
					checkArray(p, w(fmt.Sprintf("Slice(%d,%d)", k, j)), a.Slice(k, j), RefSlice(ref, k, j))
					split := a.SplitAt(k)
					checkArray(p, w("SplitAt.1"), split.V1.Get(), RefTake(ref, k))
					checkArray(p, w("SplitAt.2"), split.V2.Get(), RefDrop(ref, k))
				}
				if n > 0 {
					checkArray(p, w("Tail"), a.Tail(), ref[1:])
					checkArray(p, w("Init"), a.Init(), ref[:n-1])
				}

				threshold := n / 8
				below := func(x int) bool { return x < threshold }
				checkArray(p, w("TakeWhile"), a.TakeWhile(below), RefTakeWhile(ref, below))
				checkArray(p, w("DropWhile"), a.DropWhile(below), RefDropWhile(ref, below))
				span := a.Span(below)
				checkArray(p, w("Span.1"), span.V1.Get(), RefTakeWhile(ref, below))
				checkArray(p, w("Span.2"), span.V2.Get(), RefDropWhile(ref, below))
				part := a.Partition(isEven)
				checkArray(p, w("Partition.1"), part.V1.Get(), RefFilter(ref, isEven))
				checkArray(p, w("Partition.2"), part.V2.Get(), RefFilter(ref, func(x int) bool { return !isEven(x) }))
				pm := Array_PartitionMap(a, func(x int) Either[int, string] {
					if isEven(x) {
						return Left[int, string]{}.Apply(x)
					}
					return Right[int, string]{}.Apply(fmt.Sprint(x))
				})
				checkArray(p, w("PartitionMap.left"), pm.V1.Get(), RefFilter(ref, isEven))
				if pm.V2.Get().Length() != n-len(RefFilter(ref, isEven)) {
					p.Fatalf("%s: right side has %d elements", w("PartitionMap"), pm.V2.Get().Length())
				}

				checkArray(p, w("Reverse"), a.Reverse(), RefReverse(ref))

				other := p.Ints(n/2+3, 100)
				zipped := Array_Zip(a, ArrayFromSlice(other)).ToGoSlice()
				if len(zipped) != min(n, len(other)) {
					p.Fatalf("%s: length %d, want %d", w("Zip"), len(zipped), min(n, len(other)))
				}
				for i, z := range zipped {
					if z.V1.Get() != ref[i] || z.V2.Get() != other[i] {
						p.Fatalf("%s: element %d is %v", w("Zip"), i, z)
					}
				}
				for i, z := range Array_ZipWithIndex(a).ToGoSlice() {
					if z.V1.Get() != ref[i] || z.V2.Get() != i {
						p.Fatalf("%s: element %d is %v", w("ZipWithIndex"), i, z)
					}
				}

				key := func(x int) int { return x % 7 }
				groups := map[int][]int{}
				for k, g := range Array_GroupBy(a, func(x int) int { log.Record(x); return key(x) }) {
					groups[k] = g.ToGoSlice()
				}
				log.Expect(p, w("GroupBy"), ref)
				EqGroups(p, w("GroupBy"), groups, RefGroupBy(ref, key))
				sums := Array_GroupMapReduce(a, key, func(x int) int { return x }, func(x, y int) int { return x + y })
				wantSums := map[int]int{}
				for _, x := range ref {
					wantSums[key(x)] += x
				}
				EqMaps(p, w("GroupMapReduce"), sums, wantSums)

				count := 0
				for _, x := range ref {
					if isEven(x) {
						count++
					}
				}
				if got := a.Count(isEven); got != count {
					p.Fatalf("%s: %d, want %d", w("Count"), got, count)
				}
				if a.Exists(isEven) != (count > 0) || a.ForAll(isEven) != (count == n) {
					p.Fatalf("%s: Exists/ForAll disagree with count %d of %d", name, count, n)
				}
				for _, probe := range []int{0, 1, n / 8, n/4 + 5} {
					if got := a.IndexOf(probe); got != RefIndexOf(ref, probe) {
						p.Fatalf("%s: IndexOf(%d) = %d, want %d", name, probe, got, RefIndexOf(ref, probe))
					}
					if got := a.LastIndexOf(probe); got != RefLastIndexOf(ref, probe) {
						p.Fatalf("%s: LastIndexOf(%d) = %d, want %d", name, probe, got, RefLastIndexOf(ref, probe))
					}
					if a.Contains(probe) != (RefIndexOf(ref, probe) >= 0) {
						p.Fatalf("%s: Contains(%d) wrong", name, probe)
					}
					eq := func(x int) bool { return x == probe }
					if f := a.Find(eq); f.IsDefined() != (RefIndexOf(ref, probe) >= 0) {
						p.Fatalf("%s: Find(%d) defined = %v", name, probe, f.IsDefined())
					}
					if f := a.FindLast(eq); f.IsDefined() != (RefIndexOf(ref, probe) >= 0) {
						p.Fatalf("%s: FindLast(%d) defined = %v", name, probe, f.IsDefined())
					}
				}

				if n <= 2048 {
					// Distinct is quadratic; it keeps the first occurrence of each element.
					checkArray(p, w("Distinct"), a.Distinct(), RefDistinct(ref))
					for _, size := range []int{1, 3, 32} {
						groupsOf := Array_Grouped(a, size).ToGoSlice()
						flat := []int{}
						for gi, g := range groupsOf {
							if g.Length() != min(size, n-gi*size) {
								p.Fatalf("%s: group %d has %d elements", w("Grouped"), gi, g.Length())
							}
							flat = append(flat, g.ToGoSlice()...)
						}
						p.EqInts(w(fmt.Sprintf("Grouped(%d)", size)), flat, ref)
						windows := Array_Sliding(a, size).ToGoSlice()
						if len(windows) != max(0, n-size+1) {
							p.Fatalf("%s: %d windows, want %d", w("Sliding"), len(windows), max(0, n-size+1))
						}
						for wi, win := range windows {
							p.EqInts(w("Sliding window"), win.ToGoSlice(), ref[wi:wi+size])
						}
					}
				}
			}
		})
	}
}

// TestArrayUpdatePersistence applies a random mix of updates and structural
// edits, keeping every intermediate version: an immutable array must never
// change after it was built, whatever is derived from it later.
func TestArrayUpdatePersistence(t *testing.T) {
	for _, n := range BoundarySizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			p := NewProp(t, n)
			ref := p.Ints(n, 1000)
			a := ArrayFromSlice(ref)
			type version struct {
				a   Array[int]
				ref []int
				op  string
			}
			versions := []version{{a, slices.Clone(ref), "start"}}
			steps := 200
			for s := 0; s < steps; s++ {
				var op string
				switch r := p.Rng.Intn(10); {
				case r < 5 && len(ref) > 0:
					i, v := p.Rng.Intn(len(ref)), p.Rng.Intn(1000)
					op = fmt.Sprintf("Updated(%d,%d)", i, v)
					a = a.Updated(i, v)
					ref[i] = v
				case r < 7:
					v := p.Rng.Intn(1000)
					op = fmt.Sprintf("Append(%d)", v)
					a = a.Append(v)
					ref = append(ref, v)
				case r < 9:
					v := p.Rng.Intn(1000)
					op = fmt.Sprintf("Prepend(%d)", v)
					a = a.Prepend(v)
					ref = append([]int{v}, ref...)
				case len(ref) > 0:
					if p.Rng.Intn(2) == 0 {
						op = "Tail"
						a = a.Tail()
						ref = ref[1:]
					} else {
						op = "Init"
						a = a.Init()
						ref = ref[:len(ref)-1]
					}
				default:
					continue
				}
				ref = slices.Clone(ref)
				// Cheap spot checks after every step; full checks on a sample.
				if a.Length() != len(ref) {
					p.Fatalf("after step %d %s: length %d, want %d", s, op, a.Length(), len(ref))
				}
				if len(ref) > 0 {
					i := p.Rng.Intn(len(ref))
					if a.Get(i) != ref[i] {
						p.Fatalf("after step %d %s: Get(%d) = %d, want %d", s, op, i, a.Get(i), ref[i])
					}
				}
				if s%20 == 0 || n <= 64 {
					checkArray(p, fmt.Sprintf("after step %d %s", s, op), a, ref)
					versions = append(versions, version{a, slices.Clone(ref), op})
				}
			}
			for i, v := range versions {
				checkArray(p, fmt.Sprintf("version %d (%s) re-read at the end", i, v.op), v.a, v.ref)
			}
		})
	}
}

// TestArraySortStabilityDifferential compares Sorted / SortWith / SortBy with
// slices.SortStableFunc on inputs with many duplicate keys, at sizes on both
// sides of the insertion-sort threshold of Go's sort (12).
func TestArraySortStabilityDifferential(t *testing.T) {
	sizes := SortSizes
	for _, n := range sizes {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			p := NewProp(t, n)
			items := p.SortItems(n)
			want := RefStableSort(items)
			a := ArrayFromSlice(items)
			byKey := func(x, y SortItem) bool { return x.Key < y.Key }

			EqSlices(p, "Sorted (Ordered)", a.Sorted().ToGoSlice(), want)
			EqSlices(p, "SortWith(<)", a.SortWith(byKey).ToGoSlice(), want)
			// A non-strict comparator counts both-ways-true pairs as equal.
			EqSlices(p, "SortWith(<=)", a.SortWith(func(x, y SortItem) bool { return x.Key <= y.Key }).ToGoSlice(), want)
			calls := 0
			EqSlices(p, "SortBy", Array_SortBy(a, func(x SortItem) int { calls++; return x.Key }).ToGoSlice(), want)
			if calls != n && n > 1 {
				p.Fatalf("SortBy: key function ran %d times for %d elements", calls, n)
			}
			// Sorting never touches the receiver.
			EqSlices(p, "receiver after sorting", a.ToGoSlice(), items)

			list := ListFromSlice(items)
			EqSlices(p, "List.Sorted", list.Sorted().ToGoSlice(), want)
			EqSlices(p, "List.SortWith", list.SortWith(byKey).ToGoSlice(), want)
			EqSlices(p, "List.SortBy", List_SortBy(list, func(x SortItem) int { return x.Key }).ToGoSlice(), want)

			ints := p.Ints(n, n/8+2)
			wantInts := slices.Clone(ints)
			slices.Sort(wantInts)
			p.EqInts("Sorted (int)", ArrayFromSlice(ints).Sorted().ToGoSlice(), wantInts)
			strs := make([]string, n)
			for i, x := range ints {
				strs[i] = fmt.Sprintf("s%05d", x)
			}
			wantStrs := slices.Clone(strs)
			slices.Sort(wantStrs)
			EqSlices(p, "Sorted (string)", ArrayFromSlice(strs).Sorted().ToGoSlice(), wantStrs)
		})
	}
}
