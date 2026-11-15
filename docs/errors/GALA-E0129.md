# GALA-E0129 — Go label

**When it fires.** A loop is labeled, `outer:`, or a `break` or `continue`
names a label, `continue outer`. GALA's `break` and `continue` act on the
innermost loop and take no label.

**Minimal repro.**

```gala
package main

func main() {
outer:
    for i := 0; i < 3; i++ {
        for j := 0; j < 3; j++ {
            if j == i {
                continue outer
            }
            Println(i, j)
        }
    }
}
```

**Error output.**

```
error[GALA-E0129]: Go label `outer:`: GALA has no labeled statements
  --> main.gala:4:1
  |
4 | outer:
  | ^^^^^^ leave the outer loop through a flag `var` that its condition…
  |
  = hint: leave the outer loop through a flag `var` that its condition tests, or move the loops into a function and `return`; `break` and `continue` alone act on the innermost loop

error[GALA-E0129]: Go labeled `continue outer`: GALA's `continue` takes no label
  --> main.gala:8:17
  |
8 |                 continue outer
  |                 ^^^^^^^^^^^^^^ leave the outer loop through a flag `var` that its condition…
  |
  = hint: leave the outer loop through a flag `var` that its condition tests, or move the loops into a function and `return`; `break` and `continue` alone act on the innermost loop
```

**Fix.** Leave the inner loop with a plain `break`. To leave the outer one
as well, set a flag that its condition tests, or move the loops into a
function and `return` from it:

```gala
package main

func main() {
    for i := 0; i < 3; i++ {
        for j := 0; j < 3; j++ {
            if j == i {
                break
            }
            Println(i, j)
        }
    }
}
```

**Rationale.** A label used to stop the parser with `extraneous input ':'`,
and a labeled `break outer` was read as a `break` followed by the name
`outer`, reported as undefined ([GALA-E0023](GALA-E0023.md)). In a function
with a label, both are now this error: the label, and each jump that names
it. A `break outer` alone, with no label written, parses, and is still
GALA-E0023 for the name `outer`.

**Scope.** `goto` is [GALA-E0036](GALA-E0036.md).
