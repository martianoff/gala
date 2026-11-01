# GALA-E0088 — Parameter with no name

**When it fires.** A parameter of a function, a method or a struct shorthand
is written as a type alone, as in `func sum(...int)` or `func sum(val ...int)`.
The parameters of a function *type* are types alone (`func(...int) int`), so
the grammar accepts the form, but a declared parameter is bound by its name:
the body has no other way to reach it.

**Minimal repro.**

```gala
package main

func sum(...int) int = 0

func main() = Println(sum(1, 2))
```

**Error output.**

```
error[GALA-E0088]: parameter `...int` has no name
  --> main.gala:3:10
  |
3 | func sum(...int) int = 0
  |          ^ name it, as in `x ...int`
  |
  = hint: name it, as in `x ...int`
```

**Fix.** Name the parameter. A `val` or `var` keyword comes before the name:

```gala
package main

func sum(xs ...int) int = ArrayOf(xs...).FoldLeft(0, (acc, x) => acc + x)

func main() = Println(sum(1, 2))
```

**Rationale.** The parameter has to be named for the body to read it, and a
made-up name would be one the body cannot know. The declaration used to stop
the build with an internal error ([GALA-E0017](GALA-E0017.md)) instead of an
error at the parameter.
