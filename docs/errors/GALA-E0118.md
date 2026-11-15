# GALA-E0118 — `use` of a value with no Close method

**When it fires.** The value a `use` binding acquires is a GALA type with no
`Close` method. `use x = acquire` releases `x` with `x.Close()` when the
function returns, so the resource must have one. The usual case is a fallible
Go acquire: `os.Open` returns `(*os.File, error)`, which GALA reads as one
`Try[*os.File]`, and a `Try` has no `Close`.

**Minimal repro.**

```gala
package main

import "os"

func main() {
    use f = os.Open("config.json")
    Println(f.Name())
}
```

**Error output.**

```
error[GALA-E0118]: `use f` releases its value with Close(), but `os.Open("config.json")` is a Try[*os.File], which has no Close method
  --> main.gala:6:13
  |
6 |     use f = os.Open("config.json")
  |             ^^^^^^^^^^^^^^^^^^^^^^ take the resource out of the Try
  |
  = hint: take the resource out of the Try; here, `use f = os.Open("config.json").Get()` inside a `Try(() => { ... })`, which turns a failed acquire into a Failure, or `match` it and `use` the value in its success arm
```

**Fix.** Take the resource out of the `Try` before `use` binds it. Inside a
`Try(() => { ... })` block, `.Get()` gives the file, and a failed open ends the
block as a `Failure` instead of reaching `use`:

```gala
package main

import "os"

func fileName(path string) Try[string] = Try(() => {
    use f = os.Open(path).Get()
    f.Name()
})

func main() {
    Println(fileName("config.json").IsFailure())
}
```

Or `match` the `Try` and bind the file with `use` in the `Success` arm, where
it is a `*os.File`. An `Option` is unwrapped the same way. For a GALA type of
your own, give it a `Close() error` method; a value that holds nothing to
release is bound with `val`.

**Rationale.** `use` lowers to `x := acquire` and `defer x.Close()` in the
generated Go. Without this check the program reached `go build`, which
rejected the generated code with a message about Go, not GALA:
`f.Close undefined (type std.Try[*os.File] has no field or method Close)`.

**Scope.** Only a GALA type whose methods the compiler knows is checked: a
struct, a sealed type, or a std type such as `Try` or `Option`. A Go type and
an interface are left to Go, which checks their `Close`.
