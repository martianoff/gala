# GALA-E0068 — Match or if-expression value has no type

**When it fires.** The value of a `match` or an if-expression is used — bound
with `val`, passed as an argument, put in a struct field or a string
interpolation, or called a method on — but none of its arms or branches has a
typed value, and nothing it fills gives it a type. Arms that produce no value
are an empty block `{}`, a block ending in a statement, an assignment, or a
call that returns nothing.

It also fires, as "this match has no value: every arm leaves with ...", for a
match or if-expression a `val` is initialized with when every arm leaves with
`break`, `continue` or a `return` with no value: the declaration is never
reached.

**Minimal repro.** (`main.gala`)

```gala
package main

sealed type Shape {
    case Circle(Radius int)
    case Square(Side int)
}

func report(msg string) {
    Println(msg)
}

func main() {
    val s Shape = Circle(1)
    val done = s match {
        case Circle(_) => report("circle")
        case Square(_) => report("square")
    }
    Println(done)
}
```

**Error output.**

```text
error[GALA-E0068]: cannot infer the type of this match: its value is used, but no arm has a typed value
  --> main.gala:14:16
   |
14 |     val done = s match {
   |                ^ end each arm in the value the match stands for
   |
   = hint: end each arm in the value the match stands for (e.g. `Some(1)`, or `None[int]()` with its type spelled out), or use the match as a statement on its own line
```

**An if-expression whose branches have no known type.** An if-expression
whose branches are values of no known type (`nil`, say), with nothing it fills
to give it one, fires the same code:

```gala
package main

func main() {
    val ok = true
    val p = if (ok) nil else nil
    Println(p)
}
```

```text
error[GALA-E0068]: cannot infer the type of this if-expression: no branch has a known type
  --> main.gala:5:13
  |
5 |     val p = if (ok) nil else nil
  |             ^^ declare the type its value fills
  |
  = hint: declare the type its value fills (e.g. `val x Option[int] = if (...) ...`) or give a branch a typed value (e.g. `None[int]()`)
```

**Fix.** If you only want the side effects, run the match as a statement; if
you want a value, end each arm in it:

```gala
package main

sealed type Shape {
    case Circle(Radius int)
    case Square(Side int)
}

func report(msg string) {
    Println(msg)
}

func main() {
    val s Shape = Circle(1)
    s match {
        case Circle(_) => report("circle")
        case Square(_) => report("square")
    }
    val name = s match {
        case Circle(_) => "circle"
        case Square(_) => "square"
    }
    Println(name)
}
```

**What still works.** A match or if-expression with no value is fine wherever
its value is not used: on its own line, as the body of a function or lambda
that returns nothing, or as an arm of such a match.

**Rationale.** Such a construct lowers to a Go function with no result, so a
use of its value used to fail in `go build` with `(no value) used as value`
on the generated code, and an if-expression with untyped branches was
emitted as a function returning `any`.
