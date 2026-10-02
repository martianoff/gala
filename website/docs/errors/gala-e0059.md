---
layout: default
title: "GALA-E0059 — break or continue Cannot Reach Its Loop"
description: "\"`break` inside a match whose value is used cannot reach the loop around it\" — GALA-E0059 fires when a break or continue sits outside a for loop, in a lambda, or in a match or if-expression whose value is used."
keywords: "gala-e0059, gala break, gala continue, gala break in match, gala break in lambda, gala loop control, gala break not in loop"
permalink: /docs/errors/gala-e0059/
last_modified_at: 2026-10-01
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0059</p>

# GALA-E0059 — `break` or `continue` cannot reach its loop

**When it fires.** A `break` or `continue` does not sit where it can control
the `for` loop written around it. That is the case when it is:

- inside an arm of a `match`, or a branch of an if-expression, whose value is
  used — assigned, returned, passed as an argument, or the trailing value of a
  function. Such a construct is a value, and has to produce one on every path;
- inside a lambda, even one written inside the loop. A lambda is a separate
  function, so it cannot leave or advance its caller's loop;
- outside any `for` loop at all;
- used as a value itself, as in `val x = if (done) { break } else 1`.

**Minimal repro.**

```gala
package main

func main() {
    for i := 0; i < 5; i++ {
        val label = i match {
            case 3 => break
            case n => s"item $n"
        }
        Println(label)
    }
}
```

**Error output.**

```
error[GALA-E0059]: `break` inside a match whose value is used cannot reach the loop around it
  --> main.gala:6:23
  |
6 |             case 3 => break
  |                       ^^^^^ a match whose value is used must produce one on every path
  |
  = hint: a match whose value is used must produce one on every path; use it as a statement, or test the condition before it and `break` there
```

**Fix.** Use the `match` as a statement, so its arms run as statements and
`break` leaves the loop:

```gala
package main

func main() {
    for i := 0; i < 5; i++ {
        i match {
            case 3 => break
            case n => Println(s"item $n")
        }
    }
}
```

Or decide before computing the value:

```gala
package main

func main() {
    for i := 0; i < 5; i++ {
        if i == 3 {
            break
        }
        val label = s"item $i"
        Println(label)
    }
}
```

**In a lambda.** A `continue` written in a lambda inside a loop cannot reach
that loop:

```gala
package main

import . "martianoff/gala/collection_immutable"

func main() {
    for i := 0; i < 3; i++ {
        ArrayOf(1, 2, 3).ForEach((x) => {
            if x == i {
                continue
            }
            Println(x)
        })
    }
}
```

```
error[GALA-E0059]: `continue` cannot leave the lambda it is in to reach the loop around it
  --> main.gala:9:17
  |
9 |                 continue
  |                 ^^^^^^^^ a lambda is a separate function
  |
  = hint: a lambda is a separate function; `continue` inside it cannot control the caller's loop, so iterate with a `for` loop instead, or select the elements first (Filter, TakeWhile)
```

Iterate with a `for` loop where the loop has to be controlled, or select the
elements first (`Filter`, `TakeWhile`) and pass only those to the lambda.

**Outside a loop.**

```gala
package main

func check(n int) {
    n match {
        case 0 => break
        case _ => Println(n)
    }
}

func main() {
    check(1)
}
```

```
error[GALA-E0059]: `break` is not inside a `for` loop
  --> main.gala:5:19
  |
5 |         case 0 => break
  |                   ^^^^^ `break` controls the innermost enclosing `for` loop
  |
  = hint: `break` controls the innermost enclosing `for` loop; move it into a loop, or return from the function instead
```

Return from the function instead, or move the `match` into the loop it is
meant to control.

**What still works.** A `break` or `continue` in an arm of a `match` used as a
statement inside a loop controls that loop, in every loop form and through
nested matches:

```gala
for i := 0; i < 6; i++ {
    i match {
        case 1 => continue
        case 4 => { break }
        case _ => Println(i)
    }
}
```

A loop written inside a value-producing arm keeps its own `break`:

```gala
func sumBelowThree(n int) int = n match {
    case 0 => 0
    case _ => {
        var acc = 0
        for k := 0; k < n; k++ {
            if k == 3 { break }
            acc += k
        }
        acc
    }
}
```

**Rationale.** GALA lowers a `match` to a Go function literal that is called
on the spot. A `break` in one of its arms used to be emitted inside that
function, where it named no loop: in a statement `match` it was silently
turned into a `return` from the function literal, so the loop went on running,
and in a value `match` it produced Go that did not parse ([GALA-E0017](/docs/errors/gala-e0017/)).
A statement `match` whose arms hold `break` or `continue` is now emitted inline,
so the loop sees them. Where no loop can be reached the code is rejected
instead, because quietly picking a different control flow is the one outcome
that must never happen.

**Scope.** Only `break` and `continue` written in GALA source are checked. A
`return` in an arm is covered by [GALA-E0015](/docs/errors/gala-e0015/).
