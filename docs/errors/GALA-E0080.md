# GALA-E0080 — Pointer that may be nil where a Ptr is needed

**When it fires.** A `Ptr[T]` is a pointer that is never nil, and something
would let a nil into one, or asks whether one is nil:

- `nil` fills a `Ptr[T]` — an argument, a `val`/`var` declaration or
  assignment, a return value, a constructor field;
- a nullable `*T` fills a `Ptr[T]` — typically a pointer a Go function
  returned — or sits where a type expects a `Ptr` inside it
  (`Some(p)` for an `Option[Ptr[T]]`);
- a `Ptr[T]` is compared with nil (`p == nil`, `p != nil`);
- a typed pattern tests a dynamic value for a `Ptr` (`case p: Ptr[T]`): Go's
  type test accepts a nil `*T`.

`&x` is a `Ptr[T]`, so a variable inferred from it is one too:
`var p = &x; p = nil` is this error.

**Minimal repro.**

```gala
package main

struct Counter(var N int)

func bump(c Ptr[Counter]) {
    c.N = c.N + 1
}

func lookup(id int) *Counter = nil

func main() {
    bump(lookup(1))
}
```

**Error output.**

```
error[GALA-E0080]: cannot use lookup(1) (*Counter) as Ptr[Counter]: a pointer that may be nil cannot fill a Ptr
  --> main.gala:12:10
   |
12 |     bump(lookup(1))
   |          ^^^^^^^^^ pass RequirePtr(lookup(1))
   |
   = hint: pass RequirePtr(lookup(1)); RequirePtr panics on nil, and a pointer that may be nil is kept as OptionPtrOf(lookup(1))
```

**Fix.** Check a pointer from Go once, where it enters GALA code.
`RequirePtr(p)` returns it as a `Ptr[T]` and panics with a GALA message when it
is nil:

```gala
package main

struct Counter(var N int)

func bump(c Ptr[Counter]) {
    c.N = c.N + 1
}

func lookup(id int) *Counter = nil

func main() {
    var c = Counter(0)
    bump(&c)
    Println(c.N) // 1
    val found = OptionPtrOf(lookup(1))
    found.ForEach((p) => bump(p))
    Println(found.IsEmpty()) // true
}
```

A pointer that may be absent is an `OptionPtr[T]`: `OptionPtrOf(p)` takes a Go
pointer that may be nil, `EmptyOptionPtr()` is the empty one, and its methods
hand out `Ptr[T]` values. A `var` that is reassigned a Go pointer is declared
with the nullable type, `var p *T = &x`.

**Rationale.** A `Ptr[T]` erases to `*T` in generated Go, so nothing at run
time tells a `Ptr` from a nil pointer. The guarantee lives in the type: a value
that may be nil never fills a `Ptr`, so code that holds one never checks it.
See [Pointers](../GALA.MD#non-null-pointers-ptrt-and-optionptrt).
