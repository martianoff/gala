// Package collprop holds the shared machinery of the differential / property
// tests for the collection_immutable and collection_mutable packages.
//
// Every property compares a GALA collection against a trivially-correct Go
// reference model (a slice or a map) on randomized inputs. Inputs are drawn
// from a math/rand source whose seed is derived from the test name and the
// input size, so every run is reproducible. A failure message always carries
// the base seed and the size: rerun with PROP_SEED set to that base seed to get
// the same inputs, or to any other integer to explore different ones.
package collprop

import (
	"cmp"
	"fmt"
	"hash/fnv"
	"maps"
	"math/rand"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"martianoff/gala/std"
)

// BoundarySizes are the sizes every sequence property runs at: the trivial
// sizes, and 32^k-1, 32^k, 32^k+1 around every depth change of the 32-way trie
// behind the immutable Array (one leaf, one full level of leaves, one full
// level of internal nodes).
var BoundarySizes = []int{0, 1, 2, 31, 32, 33, 63, 64, 65, 1023, 1024, 1025, 1056, 32767, 32768, 32769}

// HugeSizes cross the next trie depth (32^4). Only cheap construction paths
// run at these sizes.
var HugeSizes = []int{1<<20 - 1, 1 << 20, 1<<20 + 1}

// SortSizes add sizes on both sides of the insertion-sort cutoff of Go's sort
// package (12): below it, an unstable sort still looks stable.
var SortSizes = append([]int{3, 11, 12, 13, 50, 100}, BoundarySizes...)

// MapSizes are the op-sequence lengths for maps and sets: the sizes they
// reach cross the 32-way HAMT fan-out at every level.
var MapSizes = []int{0, 1, 2, 31, 32, 33, 1023, 1024, 1025, 32767, 32768, 32769}

const defaultBaseSeed = 0x6a1a5eed

// baseSeed reads PROP_SEED. A value that does not parse fails the test rather
// than silently falling back to the default seed.
func baseSeed(t *testing.T) int64 {
	t.Helper()
	s := os.Getenv("PROP_SEED")
	if s == "" {
		return defaultBaseSeed
	}
	v, err := strconv.ParseInt(s, 0, 64)
	if err != nil {
		t.Fatalf("PROP_SEED=%q is not an integer: %v", s, err)
	}
	return v
}

// Prop is one reproducible property run: a seeded source plus a test handle
// whose failures report the base seed and the input size.
type Prop struct {
	T    *testing.T
	Base int64 // the PROP_SEED value this run's seed is derived from
	Seed int64
	N    int
	Rng  *rand.Rand
}

// NewProp seeds a run from the base seed, the test name and the input size.
func NewProp(t *testing.T, n int) *Prop {
	t.Helper()
	base := baseSeed(t)
	h := fnv.New64a()
	h.Write([]byte(t.Name()))
	seed := base ^ int64(h.Sum64()) ^ int64(n)*0x3c6ef372fe94f82b
	return &Prop{T: t, Base: base, Seed: seed, N: n, Rng: rand.New(rand.NewSource(seed))}
}

// Fatalf fails the test, prefixing the message with the PROP_SEED value that
// reproduces it and the input size.
func (p *Prop) Fatalf(format string, args ...any) {
	p.T.Helper()
	p.T.Fatalf("[PROP_SEED=%d n=%d] %s", p.Base, p.N, fmt.Sprintf(format, args...))
}

// Ints returns n random ints in [0, bound): small bounds give many duplicates.
func (p *Prop) Ints(n, bound int) []int {
	if bound < 1 {
		bound = 1
	}
	out := make([]int, n)
	for i := range out {
		out[i] = p.Rng.Intn(bound)
	}
	return out
}

// Cuts returns interesting cut points for Take/Drop/Slice/SplitAt at size n.
func (p *Prop) Cuts(n int) []int {
	c := []int{-1, 0, 1, n / 2, n - 1, n, n + 1, 31, 32, 33, 1024}
	for i := 0; i < 3; i++ {
		c = append(c, p.Rng.Intn(n+2))
	}
	return c
}

// EqInts compares int slices element by element.
func (p *Prop) EqInts(what string, got, want []int) {
	p.T.Helper()
	EqSlices(p, what, got, want)
}

// EqSlices compares slices element by element, reporting the first mismatch.
func EqSlices[T comparable](p *Prop, what string, got, want []T) {
	p.T.Helper()
	if len(got) != len(want) {
		p.Fatalf("%s: length %d, want %d", what, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			p.Fatalf("%s: element %d is %v, want %v", what, i, got[i], want[i])
		}
	}
}

// EqGroups compares a grouping (already converted to Go slices) with the reference.
func EqGroups[K comparable](p *Prop, what string, got, want map[K][]int) {
	p.T.Helper()
	if len(got) != len(want) {
		p.Fatalf("%s: %d groups, want %d", what, len(got), len(want))
	}
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			p.Fatalf("%s: missing group %v", what, k)
		}
		p.EqInts(fmt.Sprintf("%s[%v]", what, k), g, w)
	}
}

// EqMaps compares two Go maps.
func EqMaps[K comparable, V comparable](p *Prop, what string, got, want map[K]V) {
	p.T.Helper()
	if len(got) != len(want) {
		p.Fatalf("%s: %d entries, want %d", what, len(got), len(want))
	}
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			p.Fatalf("%s: missing key %v", what, k)
		}
		if g != w {
			p.Fatalf("%s: key %v maps to %v, want %v", what, k, g, w)
		}
	}
}

// === Sequence reference model ===
// Out-of-range cut points clamp to [0, len], as in the GALA collections.

func clampCut(k, n int) int { return max(0, min(k, n)) }

func RefTake(xs []int, k int) []int { return slices.Clone(xs[:clampCut(k, len(xs))]) }
func RefDrop(xs []int, k int) []int { return slices.Clone(xs[clampCut(k, len(xs)):]) }

func RefSlice(xs []int, from, to int) []int {
	from, to = clampCut(from, len(xs)), clampCut(to, len(xs))
	if from >= to {
		return []int{}
	}
	return slices.Clone(xs[from:to])
}

func RefReverse(xs []int) []int {
	out := slices.Clone(xs)
	slices.Reverse(out)
	return out
}

func RefFilter(xs []int, keep func(int) bool) []int {
	out := []int{}
	for _, x := range xs {
		if keep(x) {
			out = append(out, x)
		}
	}
	return out
}

func RefMap(xs []int, f func(int) int) []int {
	out := make([]int, len(xs))
	for i, x := range xs {
		out[i] = f(x)
	}
	return out
}

// FlatMapFn repeats x (x % 3) times: some elements vanish, some multiply.
func FlatMapFn(x int) []int {
	out := []int{}
	for j := 0; j < x%3; j++ {
		out = append(out, x*10+j)
	}
	return out
}

func RefFlatMap(xs []int) []int {
	out := []int{}
	for _, x := range xs {
		out = append(out, FlatMapFn(x)...)
	}
	return out
}

func RefTakeWhile(xs []int, keep func(int) bool) []int {
	i := 0
	for i < len(xs) && keep(xs[i]) {
		i++
	}
	return slices.Clone(xs[:i])
}

func RefDropWhile(xs []int, keep func(int) bool) []int {
	i := 0
	for i < len(xs) && keep(xs[i]) {
		i++
	}
	return slices.Clone(xs[i:])
}

// RefDistinct keeps the first occurrence of each element, in order.
func RefDistinct(xs []int) []int {
	seen := map[int]bool{}
	out := []int{}
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// RefGroupBy groups xs by key; every group keeps the input order.
func RefGroupBy(xs []int, key func(int) int) map[int][]int {
	out := map[int][]int{}
	for _, x := range xs {
		out[key(x)] = append(out[key(x)], x)
	}
	return out
}

func RefIndexOf(xs []int, v int) int { return slices.Index(xs, v) }

func RefLastIndexOf(xs []int, v int) int {
	for i := len(xs) - 1; i >= 0; i-- {
		if xs[i] == v {
			return i
		}
	}
	return -1
}

// RefSetAlgebra returns the reference union, intersection and difference.
func RefSetAlgebra[T comparable](a, b map[T]bool) (union, inter, diff map[T]bool) {
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

// RefRange returns the entries of m in [lo, hi], in [lo, ...) and in (..., hi]:
// the reference for the inclusive Range / RangeFrom / RangeTo of tree collections.
func RefRange[K cmp.Ordered, V any](m map[K]V, lo, hi K) (in, from, to map[K]V) {
	in, from, to = map[K]V{}, map[K]V{}, map[K]V{}
	for k, v := range m {
		if k >= lo && k <= hi {
			in[k] = v
		}
		if k >= lo {
			from[k] = v
		}
		if k <= hi {
			to[k] = v
		}
	}
	return in, from, to
}

// === Sorting ===

// SortItem orders by Key only, so items with equal keys stay distinguishable
// by ID: any reordering of equal-key items is a stability violation.
type SortItem struct {
	Key int
	ID  int
}

func (s SortItem) Compare(other SortItem) int { return cmp.Compare(s.Key, other.Key) }

var _ std.Ordered[SortItem] = SortItem{}

// SortItems returns n items whose keys fall in a range of about n/8 values, so
// most keys repeat many times; IDs record the input position.
func (p *Prop) SortItems(n int) []SortItem {
	out := make([]SortItem, n)
	for i := range out {
		out[i] = SortItem{Key: p.Rng.Intn(n/8 + 2), ID: i}
	}
	return out
}

// RefStableSort is the reference: slices.SortStableFunc by key.
func RefStableSort(xs []SortItem) []SortItem {
	out := slices.Clone(xs)
	slices.SortStableFunc(out, SortItem.Compare)
	return out
}

// === Maps and sets ===

// TupleOf builds a std.Tuple from Go.
func TupleOf[A any, B any](a A, b B) std.Tuple[A, B] {
	return std.Tuple[A, B]{V1: std.NewImmutable(a), V2: std.NewImmutable(b)}
}

// ShuffledKeys returns the keys of m in an order drawn from p.Rng. Go's map
// iteration order is not seeded, so an input built by ranging over a map would
// change from run to run, and a failure would not reproduce under its seed.
func ShuffledKeys[K comparable, V any](p *Prop, m map[K]V) []K {
	keys := slices.Collect(maps.Keys(m))
	names := make(map[K]string, len(keys))
	for _, k := range keys {
		names[k] = fmt.Sprint(k)
	}
	slices.SortFunc(keys, func(a, b K) int { return strings.Compare(names[a], names[b]) })
	p.Rng.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
	return keys
}

// ShuffledEntries returns the entries of m as tuples, in ShuffledKeys order.
func ShuffledEntries[K comparable, V any](p *Prop, m map[K]V) []std.Tuple[K, V] {
	keys := ShuffledKeys(p, m)
	out := make([]std.Tuple[K, V], len(keys))
	for i, k := range keys {
		out[i] = TupleOf(k, m[k])
	}
	return out
}

// CollidingKey hashes into only a handful of buckets, so hash-trie and bucket
// implementations must handle many keys that share a full 32-bit hash.
type CollidingKey struct{ V int }

// Hash keeps negative keys (absent-key probes) in the same five buckets.
func (k CollidingKey) Hash() uint32 { return uint32((k.V%5 + 5) % 5) }

var _ std.Hashable = CollidingKey{}

// KeyKind is a key type a map or set workload runs with.
type KeyKind[K comparable] struct {
	Name string
	Mk   func(int) K
	// MaxN caps the workload size (0: no cap). Keys that share a full hash
	// land in linear collision buckets, so their workloads are quadratic.
	MaxN int
}

// Skips reports whether a workload of size n is beyond this kind's cap.
func (k KeyKind[K]) Skips(n int) bool { return k.MaxN > 0 && n > k.MaxN }

var (
	IntKeys       = KeyKind[int]{"int", func(i int) int { return i }, 0}
	StringKeys    = KeyKind[string]{"string", func(i int) string { return fmt.Sprintf("k%d", i) }, 0}
	CollidingKeys = KeyKind[CollidingKey]{"colliding", func(i int) CollidingKey { return CollidingKey{i} }, 1025}
)

// MapOp is one step of a random workload: Put(Key, Value) or Remove(Key).
type MapOp struct {
	Put   bool
	Key   int
	Value int
}

// KeySpace is the key range a workload of size n draws from: it includes keys
// the workload never inserts, so lookups and removes also miss.
func KeySpace(n int) int { return n + n/4 + 1 }

// MapWorkload inserts n distinct keys in random order, then runs n random
// mixed operations over KeySpace(n).
func (p *Prop) MapWorkload(n int) []MapOp {
	ops := make([]MapOp, 0, 2*n)
	for _, k := range p.Rng.Perm(n) {
		ops = append(ops, MapOp{Put: true, Key: k, Value: p.Rng.Intn(1 << 20)})
	}
	for i := 0; i < n; i++ {
		ops = append(ops, MapOp{Put: p.Rng.Intn(3) != 0, Key: p.Rng.Intn(KeySpace(n)), Value: p.Rng.Intn(1 << 20)})
	}
	return ops
}

// SnapshotSteps picks the op indices at which a workload does a full check.
func SnapshotSteps(total int) map[int]bool {
	s := map[int]bool{}
	for _, i := range []int{0, 1, 31, 32, 33, 1023, 1024, 1025, total / 2, total - 1} {
		if i >= 0 && i < total {
			s[i] = true
		}
	}
	return s
}

// SortedKeys returns the keys of m in ascending order.
func SortedKeys[K cmp.Ordered, V any](m map[K]V) []K { return slices.Sorted(maps.Keys(m)) }

// === Evaluation counting ===

// VisitLog records the arguments a callback saw, to prove that a function
// passed to a collection operation runs exactly once per element, in order.
type VisitLog struct{ seen []int }

func (v *VisitLog) Record(x int) { v.seen = append(v.seen, x) }

// Expect checks the recorded visits against want and resets the log.
func (v *VisitLog) Expect(p *Prop, what string, want []int) {
	p.T.Helper()
	p.EqInts(what+" callback visits", v.seen, want)
	v.seen = nil
}
