package collection_immutable

// Red-black invariant tests for the immutable TreeMap and TreeSet.
// checkRedBlack walks the nodes and checks that the root is black, no red node
// has a red child, every root-to-leaf path has the same number of black nodes,
// the keys are in order, and the height stays within the red-black bound
// 2*log2(n+1). checkTreeMap and checkTreeSet run it on every tree the
// differential tests build; the workloads below add the removal patterns that
// used to unbalance the tree (#809).

import (
	"cmp"
	"fmt"
	"math"
	"testing"

	. "martianoff/gala/internal/collprop"
	"martianoff/gala/std"
)

// rbShape reads one node of either tree: its key, color and children.
type rbShape[N any, K cmp.Ordered] struct {
	key   func(*N) K
	red   func(*N) bool
	left  func(*N) std.OptionPtr[N]
	right func(*N) std.OptionPtr[N]
}

func treeMapShape[K cmp.Ordered, V any]() rbShape[treeMapNode[K, V], K] {
	return rbShape[treeMapNode[K, V], K]{
		key:   func(n *treeMapNode[K, V]) K { return n.key.Get() },
		red:   func(n *treeMapNode[K, V]) bool { return n.color.Get() == treeMapRed.Get() },
		left:  func(n *treeMapNode[K, V]) std.OptionPtr[treeMapNode[K, V]] { return n.left.Get() },
		right: func(n *treeMapNode[K, V]) std.OptionPtr[treeMapNode[K, V]] { return n.right.Get() },
	}
}

func treeSetShape[T cmp.Ordered]() rbShape[treeNode[T], T] {
	return rbShape[treeNode[T], T]{
		key:   func(n *treeNode[T]) T { return n.value.Get() },
		red:   func(n *treeNode[T]) bool { return n.color.Get() == treeRed.Get() },
		left:  func(n *treeNode[T]) std.OptionPtr[treeNode[T]] { return n.left.Get() },
		right: func(n *treeNode[T]) std.OptionPtr[treeNode[T]] { return n.right.Get() },
	}
}

// checkRedBlack checks every red-black invariant of the tree under root, which
// must hold size keys.
func checkRedBlack[N any, K cmp.Ordered](p *Prop, what string, s rbShape[N, K], root std.OptionPtr[N], size int) {
	p.T.Helper()
	if root.IsDefined() && s.red(root.Get()) {
		p.Fatalf("%s: root is red", what)
	}
	keys := make([]K, 0, size)
	// walk returns the height and black-height of the subtree under tree.
	var walk func(tree std.OptionPtr[N], parentRed bool) (height, blackHeight int)
	walk = func(tree std.OptionPtr[N], parentRed bool) (int, int) {
		if tree.IsEmpty() {
			return 0, 1
		}
		n := tree.Get()
		red := s.red(n)
		if red && parentRed {
			p.Fatalf("%s: red node %v has a red parent", what, s.key(n))
		}
		lh, lb := walk(s.left(n), red)
		keys = append(keys, s.key(n))
		rh, rb := walk(s.right(n), red)
		if lb != rb {
			p.Fatalf("%s: node %v: left black-height %d, right black-height %d", what, s.key(n), lb, rb)
		}
		if !red {
			lb++
		}
		return 1 + max(lh, rh), lb
	}
	height, _ := walk(root, false)
	if len(keys) != size {
		p.Fatalf("%s: %d nodes, Size %d", what, len(keys), size)
	}
	for i := 1; i < len(keys); i++ {
		if keys[i-1] >= keys[i] {
			p.Fatalf("%s: keys out of order: %v before %v", what, keys[i-1], keys[i])
		}
	}
	if bound := 2 * math.Log2(float64(size+1)); float64(height) > bound {
		p.Fatalf("%s: height %d exceeds the red-black bound %.1f for %d keys", what, height, bound, size)
	}
}

// rbTree is the part of TreeMap / TreeSet a workload drives.
type rbTree interface {
	put(k int) rbTree
	remove(k int) rbTree
	same(other rbTree) bool
	check(p *Prop, what string, ref map[int]int)
}

type rbTreeMap struct{ m TreeMap[int, int] }

func (w rbTreeMap) put(k int) rbTree    { return rbTreeMap{w.m.Put(k, k)} }
func (w rbTreeMap) remove(k int) rbTree { return rbTreeMap{w.m.Remove(k)} }
func (w rbTreeMap) same(other rbTree) bool {
	o := other.(rbTreeMap)
	return w.m.root.Get() == o.m.root.Get() && w.m.Size() == o.m.Size()
}
func (w rbTreeMap) check(p *Prop, what string, ref map[int]int) {
	p.T.Helper()
	checkTreeMap(p, what, w.m, ref)
}

type rbTreeSet struct{ s TreeSet[int] }

func (w rbTreeSet) put(k int) rbTree    { return rbTreeSet{w.s.Add(k)} }
func (w rbTreeSet) remove(k int) rbTree { return rbTreeSet{w.s.Remove(k)} }
func (w rbTreeSet) same(other rbTree) bool {
	o := other.(rbTreeSet)
	return w.s.root.Get() == o.s.root.Get() && w.s.Size() == o.s.Size()
}
func (w rbTreeSet) check(p *Prop, what string, ref map[int]int) {
	p.T.Helper()
	keys := map[int]bool{}
	for k := range ref {
		keys[k] = true
	}
	checkTreeSet(p, what, w.s, keys)
}

var rbTrees = []struct {
	name  string
	empty func() rbTree
}{
	{"TreeMap", func() rbTree { return rbTreeMap{EmptyTreeMap[int, int]()} }},
	{"TreeSet", func() rbTree { return rbTreeSet{EmptyTreeSet[int]()} }},
}

// rbOp is one step of a workload: put key, or remove it.
type rbOp struct {
	key    int
	remove bool
}

func rbPut(k int) rbOp { return rbOp{key: k} }
func rbDel(k int) rbOp { return rbOp{key: k, remove: true} }

// rbPuts puts each key in order.
func rbPuts(keys []int) []rbOp {
	ops := make([]rbOp, len(keys))
	for i, k := range keys {
		ops[i] = rbPut(k)
	}
	return ops
}

// rbRun applies ops to a fresh tree, checking it every checkEvery operations
// and at the end.
func rbRun(p *Prop, empty func() rbTree, ops []rbOp, checkEvery int) {
	p.T.Helper()
	tree := empty()
	ref := map[int]int{}
	for i, op := range ops {
		if op.remove {
			tree = tree.remove(op.key)
			delete(ref, op.key)
		} else {
			tree = tree.put(op.key)
			ref[op.key] = op.key
		}
		if (i+1)%checkEvery == 0 {
			tree.check(p, fmt.Sprintf("after op %d", i+1), ref)
		}
	}
	tree.check(p, "at the end", ref)
}

// rbFillThenRemove puts the keys in insert order, then removes all but the
// last keep of them in remove order.
func rbFillThenRemove(insert, remove []int, keep int) []rbOp {
	ops := rbPuts(insert)
	for _, k := range remove[:len(remove)-keep] {
		ops = append(ops, rbDel(k))
	}
	return ops
}

func TestRedBlackInvariantsAfterRemove(t *testing.T) {
	const n = 4096
	ascending := make([]int, n)
	descending := make([]int, n)
	for i := range n {
		ascending[i] = i
		descending[i] = n - 1 - i
	}
	start := ascending[:1024]

	workloads := []struct {
		name       string
		ops        func(p *Prop) []rbOp
		checkEvery int
	}{
		{"remove ascending", func(*Prop) []rbOp { return rbFillThenRemove(ascending, ascending, 256) }, 256},
		{"remove descending", func(*Prop) []rbOp { return rbFillThenRemove(ascending, descending, 256) }, 256},
		{"remove random", func(p *Prop) []rbOp { return rbFillThenRemove(ascending, p.Rng.Perm(n), 256) }, 256},
		{"insert and remove random", func(p *Prop) []rbOp { return rbFillThenRemove(p.Rng.Perm(n), p.Rng.Perm(n), 256) }, 256},
		{"insert descending, remove random", func(p *Prop) []rbOp { return rbFillThenRemove(descending, p.Rng.Perm(n), 256) }, 256},
		{"remove everything", func(p *Prop) []rbOp { return rbFillThenRemove(p.Rng.Perm(n), p.Rng.Perm(n), 0) }, 256},
		// The oldest key leaves as the newest arrives.
		{"sliding window", func(*Prop) []rbOp {
			ops := rbPuts(start)
			for i := 1024; i < 21024; i++ {
				ops = append(ops, rbDel(i-1024), rbPut(i))
			}
			return ops
		}, 1000},
		// Random removes and puts over a key space four times the start size.
		{"random churn", func(p *Prop) []rbOp {
			ops := rbPuts(start)
			for range 20000 {
				ops = append(ops, rbDel(p.Rng.Intn(n)), rbPut(p.Rng.Intn(n)))
			}
			return ops
		}, 1000},
	}
	for _, tree := range rbTrees {
		for _, w := range workloads {
			t.Run(tree.name+"/"+w.name, func(t *testing.T) {
				p := NewProp(t, n)
				rbRun(p, tree.empty, w.ops(p), w.checkEvery)
			})
		}
	}
}

// TestRedBlackInvariantsSmallTrees removes every key of small trees built in
// random orders, checking after each step, so each rebalancing case is reached
// near the root as well as deep down.
func TestRedBlackInvariantsSmallTrees(t *testing.T) {
	for _, tree := range rbTrees {
		for size := 1; size <= 40; size++ {
			t.Run(fmt.Sprintf("%s/%d", tree.name, size), func(t *testing.T) {
				p := NewProp(t, size)
				for range 20 {
					ops := rbPuts(p.Rng.Perm(size))
					for _, k := range p.Rng.Perm(size) {
						ops = append(ops, rbDel(k))
					}
					rbRun(p, tree.empty, ops, 1)
				}
			})
		}
	}
}

// TestRedBlackRemoveMissingKey checks removing each missing key of trees of
// several sizes: Remove hands back the same tree, and the remove helpers it
// skips keep the entries, the balance and the black-height.
func TestRedBlackRemoveMissingKey(t *testing.T) {
	for _, size := range []int{0, 1, 2, 3, 7, 100, 1000} {
		t.Run(fmt.Sprintf("%d", size), func(t *testing.T) {
			p := NewProp(t, size)
			m := EmptyTreeMap[int, int]()
			s := EmptyTreeSet[int]()
			ref := map[int]int{}
			for _, k := range p.Rng.Perm(size) {
				m, s = m.Put(2*k, 2*k), s.Add(2*k)
				ref[2*k] = 2 * k
			}
			// Every odd key from below the smallest to above the largest misses.
			for k := -1; k <= 2*size+1; k += 2 {
				what := fmt.Sprintf("miss %d", k)
				for _, tree := range []rbTree{rbTreeMap{m}, rbTreeSet{s}} {
					if !tree.remove(k).same(tree) {
						p.Fatalf("%s: Remove returned a different tree", what)
					}
				}
				mt := treeMapRemove(m.root.Get(), k)
				st := removeTree(s.root.Get(), k)
				if mt.V2.Get() || st.V2.Get() {
					p.Fatalf("%s: a remove helper reports a shorter tree", what)
				}
				rbTreeMap{TreeMap[int, int]{root: std.NewImmutable(mt.V1.Get()), size: m.size}}.check(p, what, ref)
				rbTreeSet{TreeSet[int]{root: std.NewImmutable(st.V1.Get()), size: s.size}}.check(p, what, ref)
			}
		})
	}
}
