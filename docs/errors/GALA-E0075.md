# GALA-E0075 — No default value

**When it fires.** GALA code needs a value of a type and none is written, and
the type has no default value. That happens for:

- an uninitialized `var x T`;
- a field a construction leaves out, in a brace literal (`Job{Name: "build"}`)
  or a call;

when the type is a sealed type without a `default case` (`Try`, `Either`,
most user sealed types), or a struct holding one. GALA never creates such a
value implicitly: Go's zero value of a sealed type without a default case is
none of its cases.

It also fires when a construction leaves out a field of another package's
struct that is private to that package and has a default value: only that
package can set it.

**Minimal repro.**

```gala
package main

struct Job(Name string, Result Try[int])

func main() {
    val job = Job{Name: "build"}
    Println(job.Name)
}
```

**Error output.**

```
error[GALA-E0075]: field "Result" of "Job" is left out, and Try has no default value: it is a sealed type without a default case
  --> main.gala:6:15
  |
6 |     val job = Job{Name: "build"}
  |               ^^^ give Result a value
  |
  = hint: give Result a value
```

**Fix.** Give the value one of the type's cases, or give the type a default
case:

```gala
package main

struct Job(Name string, Result Try[int])

func main() {
    val job = Job{Name: "build", Result: Success(0)}
    Println(job.Name)
}
```

**Rationale.** A type's default value is what GALA uses where its code needs a
value and none is written: a sealed type's default case built with its
fields' declared defaults, a struct with each field at its declared default
or its type's default value, and Go's zero value for every other type. A type
with no default value is one whose Go zero value means nothing, so GALA asks
for a value instead. See [Default Values](../GALA.MD#default-values).
