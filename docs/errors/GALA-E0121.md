# GALA-E0121 — Go `const` declaration

**When it fires.** A constant is declared with Go's `const`: `const N = 10`,
inside a function or at the top level, or a `const ( ... )` block. GALA
declares a constant with `val`.

**Minimal repro.**

```gala
package main

const MaxRetries = 3

func main() {
    Println(MaxRetries)
}
```

**Error output.**

```
error[GALA-E0121]: Go `const` declaration: GALA declares a constant with `val`
  --> main.gala:3:1
  |
3 | const MaxRetries = 3
  | ^^^^^^^^^^^^^^^^^^^^ write it with `val`
  |
  = hint: write it with `val`; here, `val MaxRetries = 3`
```

**Fix.** Write `val` for `const`; the rest of the declaration stays:

```gala
package main

val MaxRetries = 3

func main() {
    Println(MaxRetries)
}
```

A top-level `val` is immutable, and a capitalized one compares in a `match`
as a Go constant does in a `switch` case (`case MaxRetries =>`). A `const`
block is one `val` per constant, and the hint spells the first. A block that
numbers its constants with `iota` is an enumeration: write it as a
`sealed type`, or, when the numbers themselves matter to a Go API, keep the
block in a `.go` file of the same package
([porting guide](../PORTING_FROM_GO.MD#const-exported-values-and-methods-on-scalar-types)).

**Rationale.** `const` is not a GALA keyword; it is reserved as a name
([GALA-E0055](GALA-E0055.md)). A top-level `const` used to stop the parser
with `mismatched input 'const' expecting {<EOF>, 'val', ...}`; it is now this
one error, at the `const`, as is a top-level `const ( ... )` block.

**Scope.** The constructs are found only in a file that does not parse.
A `const` inside a function body that is the file's only error parses, as the name `const`
followed by an assignment, so it is still reported as a use of that name.
A name spelled `const`, such as `val const = 1`, is still
[GALA-E0055](GALA-E0055.md).
