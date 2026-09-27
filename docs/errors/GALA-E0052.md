# GALA-E0052 — function declared inside a function body

**When it fires.** A named `func` is declared inside the body of another
function, lambda, `if` or `for` block:

```gala
func main() {
    func helper(x int) int {
        return x + 1
    }
    Println(helper(2))
}
```

GALA declares named functions and methods only at the top level of a file. A
local function is a lambda bound to a `val`.

**Minimal repro.**

```gala
package main

func main() {
    func helper(x int) int {
        return x + 1
    }
    Println(helper(2))
}
```

**Error output.**

```
error[GALA-E0052]: function `helper` is declared inside a function body
  --> main.gala:4:5
  |
4 |     func helper(x int) int {
  |     ^^^^^^^^^^^ write it as a lambda bound to a `val`
  |
  = hint: write it as a lambda bound to a `val`; here, `val helper = (x int) int => ...`
```

**Fix.** Bind a lambda. A lambda states its result type in the same place a
function does — after the parameter list — so the declaration's signature
carries over unchanged, and the hint spells it out from your own code:

```gala
package main

func main() {
    val helper = (x int) int => x + 1
    Println(helper(2))
}
```

A block body works the same way, `return` included:

```gala
val clamp = (x int) int => {
    if x < 0 {
        return 0
    }
    return x
}
```

A lambda cannot refer to its own `val` while it is being defined. For a
recursive local helper, declare the function type with `var` first and assign
the lambda on the next line:

```gala
var fact func(int) int
fact = (n int) => if (n <= 1) 1 else n * fact(n - 1)
Println(fact(5))
```

Three shapes have no lambda form and move to the top level of the file instead,
which the hint says for each:

- a **generic** function, `func id[T any](x T) T` — a lambda cannot take type
  parameters;
- a function with a **default parameter value**, `func greet(name string = "gala")`
  — a lambda parameter cannot carry a default;
- a **method**, `func (b Box) Double() int` — a method belongs with its
  receiver type.

**Rationale.** The grammar admits a function declaration anywhere a statement
can go, but a local named function was never part of the language: the
reference documents functions at the top level, and every local helper in the
standard library, the examples and the tests is a lambda. Go has no nested
named functions either, so the declaration used to be copied verbatim into the
enclosing Go body and the generated file did not parse. That surfaced as
[GALA-E0017](GALA-E0017.md), the internal-error code, with a request to file a
transpiler bug — for source that simply was not GALA. The declaration is now
refused where it is written, with the lambda that replaces it.

Making the declaration legal instead would add a second spelling for something
the language already has, and would still need its own answer for every
feature a top-level function carries — default parameter values, named
arguments, type parameters, receivers — none of which a local binding has.

**Scope.** Only named declarations inside a body are refused. Top-level
functions and methods, lambdas anywhere, and calls to a top-level function from
inside a body are unaffected.
