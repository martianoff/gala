# GALA-E0069 — `return` in a match or if-expression whose value is used

**When it fires.** A `return` is written in an arm of a `match`, or a branch of
an if-expression, whose value is used inside a larger expression: passed as an
argument, used as an operand, interpolated into a string, or called a method
on. A `return` there has to leave the enclosing function, but the rest of the
expression around the match would still have to be evaluated first, so the
construct cannot be lowered as statements; it is lowered to a Go function
literal called on the spot, which the `return` could only leave. (A match or
if-expression that initializes a local `val` or `var`, is assigned to a
variable, or is the function's result, is fine: there the `return` leaves the
function.)

**Minimal repro.** (`main.gala`)

```gala
package main

func double(n int) int = n * 2

func pick(o Option[int]) int {
    val y = double(o match {
        case Some(v) => v
        case None() => { return -1 }
    })
    y + 1
}

func main() {
    Println(pick(None[int]()))
}
```

**Error output.**

```text
error[GALA-E0069]: `return` inside a match whose value is used leaves only the match, not the function
  --> main.gala:8:26
  |
8 |         case None() => { return -1 }
  |                          ^^^^^^ initialize a `val` with the match first
  |
  = hint: initialize a `val` with the match first (`val x = ...`) and use `x`: there a `return` in it leaves the function
```

**Fix.** Initialize a `val` with the match, and use the `val`. A match or
if-expression a local `val` or `var` is initialized with, or a variable is
assigned, is lowered as statements, so a `return` in it leaves the function,
and a `break` or `continue` acts on the loop around it:

```gala
package main

func double(n int) int = n * 2

func pick(o Option[int]) int {
    val x = o match {
        case Some(v) => v
        case None() => { return -1 }
    }
    double(x) + 1
}

func main() {
    Println(pick(None[int]()))
    Println(pick(Some(2)))
}
```

**What still works.** A `return` in a match or if-expression that is itself
the function's result — `return o match { ... }`, or the trailing expression
of a function or lambda body — leaves the function, as written. A `return` in
a lambda inside an arm leaves that lambda.

**Rationale.** Before this check such a `return` compiled, and the program
ran on with the wrong value: `val x = o match { case None() => { return -1 }
... }` followed by `x * 10` returned `-10` instead of `-1`, and in a loop that
was meant to stop it kept going. A `val` or `var` initializer and an
assignment are now lowered so that the `return` does what it says; inside a
larger expression the construct has to be a function literal, so the `return`
is rejected rather than silently confined.
