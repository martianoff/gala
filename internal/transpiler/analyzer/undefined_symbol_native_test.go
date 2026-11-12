package analyzer

// undefinedInputs are packages, as files, whose compiled file (the first) the
// undefined-symbol check finds each kind of error in, or, for clean, none:
// every decision of the value walk and the type pass, every binder the check
// adds to the walk's, and every message and hint it writes. Each file holds
// many errors; the check reports them all, and the analysis the first.
var undefinedInputs = []struct {
	name  string
	clean bool
	files [][2]string
}{
	{"values", false, [][2]string{{"main.gala", `package main

import "martianoff/gala/collection_immutable"

struct Point(x int = origin, y int = 0)

val (left, right) = (1, two)

var counter = left + right + missingVar

func useImportedByName() int = ArrayOf(1, 2).Size()

func useUndeclared() string = s"héllo ${nowhere + 1} $alsoNowhere"

func useUnimported() int = EmptyArray[int]().Size()

func (p Point) sum() int = p.x + p.y + offset

func shadow(p int) int {
    val q = (r int) => r + p + missing
    func local[U any](u U) int = 1 + localMissing
    return q(local(p))
}

func repeated() int = missing + missing

func localTypeOutOfBlock(ok bool) int {
    if ok {
        type Inner struct {
            N int
        }
        Println(Inner(1).N)
    }
    return Inner(2).N
}

func qualifiable(xs Array[int]) int = xs.Size()

func main() {
    Println(collection_immutable.ArrayOf(1))
}
`}}},
	{"types", false, [][2]string{{"main.gala", `package main

import "time"

type Coord Pointt

type Pair[K Numbr, V any] struct {
    Key K
    Value V
}

func f(sb strings.Builder) int = 1

func g(x Widgt) int = 1

func h(cb func(Wdg, int) int) int = 1

func notType(x helper) int = 1

func helper() int = 1

func dur(d Duration) int = 1

func movedEarlier(x Later) int = 1

val laterUse = Later

func generic[T any](x T, y _) T = x

struct Holder[T any](items []T)

func (h Holder[T]) first(m map[string]T) T = h.items[0]

func localType() int {
    type Inner struct {
        N int
    }
    val i Inner = Inner{N: 1}
    return i.N
}

sealed type Shape {
    case Circle(r float64)
    case Square(side Sidee)
}

func usesCase(c Circle) float64 = c.r

func pointer(p *Missing, q []other.T) int = 1

func main() {
    Println(time.Now())
}
`}}},
	{"method signature allowance", false, [][2]string{
		{"b.gala", `package main

func (b Box) total(xs Array[int]) int = xs.Size() + EmptyArray[int]().Size()

func (b *Box) ptr(xs Array[int]) Array[int] = xs
`},
		{"a.gala", `package main

import . "martianoff/gala/collection_immutable"

struct Box(v int)

func main() {
    Println(ArrayOf(1).Size())
}
`},
	}},
	{"binders", true, [][2]string{{"main.gala", `package main

import . "martianoff/gala/go_interop"

struct Node[T any](value T, next Option[Node[T]])

val (a, b) = (1, 2)

var (c, d) = (a, b)

func id[T any](x T) T = x

func (n Node[T]) get() T = n.value

func (n *Node[T]) set(v T) T = v

func pairUp[K comparable, V any](k K, v V) (K, V) = (k, v)

func walk(xs []int) int {
    var total = 0
    for i, x := range xs {
        total = total + i + x
    }
    val f = (y int) => y + total
    func inner[U any](u U) U = u
    type Cell struct {
        V int
    }
    return f(inner(Cell(c + d).V))
}

func pick(o Option[int]) int = o match {
    case Some(v) => v
    case _ => 0
}

func main() {
    Println(walk(SliceOf(1, 2)))
}
`}}},
}
