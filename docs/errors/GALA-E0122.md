# GALA-E0122 — Go declaration group

**When it fires.** Several declarations are grouped in parentheses, as Go
groups them: `var ( ... )`, `val ( ... )` or `type ( ... )`. GALA declares
one name per declaration.

**Minimal repro.**

```gala
package main

var (
    host = "localhost"
    port = 8080
)

func main() {
    Println(host, port)
}
```

**Error output.**

```
error[GALA-E0122]: Go declaration group `var ( ... )`: GALA declares one name per `var`
  --> main.gala:3:1
  |
3 | var (
  | ^^^ write one `var` declaration per name, without the parenthese…
  |
  = hint: write one `var` declaration per name, without the parentheses; here, `var host = "localhost"`
```

**Fix.** Drop the parentheses and write the keyword before each name. A
value that is never reassigned is a `val`:

```gala
package main

val host = "localhost"
val port = 8080

func main() {
    Println(host, port)
}
```

**Rationale.** The parser used to stop at the first name of the group with
`missing ')' at '='`, read as a tuple pattern that went wrong, and then
report the next name as extraneous input. The group is now this one error,
whose hint spells the first declaration on its own.

**Scope.** A tuple pattern is not a group and is not reported:
`val (a, b) = pair` and `var (x, y) = (1, 2)` destructure a tuple. A
`const ( ... )` block is [GALA-E0121](GALA-E0121.md).
