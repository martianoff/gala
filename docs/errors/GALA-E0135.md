# GALA-E0135 — Default value depends on itself

**When it fires.** A parameter's default value, or a field's, needs itself to
be expanded. A default is expanded at every call or construction that leaves
its argument out, and a call written inside a default is such a call too: the
arguments it leaves out take their own defaults. When that leads back to the
default being expanded, directly or through the defaults of other parameters,
functions, methods or fields, the expansion would never end. The message names
the parameter and the path of defaults that leads back to it.

**Minimal repro.**

```gala
package main

func f(a int, b int = f(1)) int = a + b

func main() {
    Println(f(0))
}
```

**Error output.**

```
error[GALA-E0135]: the default value of parameter b of f depends on itself: f.b → f.b
  --> main.gala:3:23
  |
3 | func f(a int, b int = f(1)) int = a + b
  |                       ^^^^ pass the argument explicitly in the call
  |
  = hint: pass the argument explicitly in the call; a call that leaves an argument out takes its default, so expanding this default needs itself again
```

The cycle may run through other functions:

```gala
package main

func g(a int = h()) int = a

func h(b int = g()) int = b

func main() {
    Println(g())
}
```

```
error[GALA-E0135]: the default value of parameter a of g depends on itself, through the default of h.b: g.a → h.b → g.a
  --> main.gala:3:16
  |
3 | func g(a int = h()) int = a
  |                ^^^ pass the argument explicitly in the call
  |
  = hint: pass the argument explicitly in the call; a call that leaves an argument out takes its default, so expanding this default needs itself again
```

**Fix.** Pass the argument in the call the default makes, so that call needs no
default:

```gala
package main

func f(a int, b int = f(1, 2)) int = a + b

func main() {
    Println(f(0))
}
```

A default may call its own function, and may leave out a parameter whose
default does not lead back: `b int = f(a, 0)` in
`func f(a int, b int = f(a, 0), c int = 3)` is fine.

**Rationale.** A default is not a value computed once: it is re-expanded at
each use, so `time.Now()` is read per call. A default that needs itself has no
expansion. It is reported at a use that leaves the argument out, at the
default the cycle starts from.
