# GALA-E0114 — `inout` method called on a value that is not a mutable place

**When it fires.** A method with an `inout` receiver, `func (inout c T) m()`,
lends the receiver's place for the call. It is called on a mutable place: a
`var`, an `inout` parameter, a `var` field of one, or a `Ptr[T]`. The error
fires when it is called on anything else: a `val`, a parameter not declared
`var`, a `val` field, a call result or a literal. It fires even when the
method only reads its receiver.

**Minimal repro.**

```gala
package main

struct Counter(var n int)

func (inout c Counter) bump() {
    c.n = c.n + 1
}

func main() {
    val c = Counter(0)
    c.bump()
    Println(c.n)
}
```

**Error output.**

```
error[GALA-E0114]: cannot call bump on this Counter: bump has an inout receiver, and this Counter is not a mutable place
  --> main.gala:11:7
   |
11 |     c.bump()
   |       ^^^^ call it on a `var`, an inout parameter or a `Ptr`
   |
   = hint: call it on a `var`, an inout parameter or a `Ptr`; to change a copy, copy the value into a `var` first
```

**Fix.** Call it on a place: declare the value `var`, or copy it into one.

```gala
package main

struct Counter(var n int)

func (inout c Counter) bump() {
    c.n = c.n + 1
}

func main() {
    var c = Counter(0)
    c.bump()
    Println(c.n) // 1
}
```

**Rationale.** A `Ptr` receiver called on a `val` runs on a hidden copy when
it only reads its receiver, and is [GALA-E0110](GALA-E0110.md) when it can
change it. An `inout` receiver is a contract that the method borrows a
place, so it is never called on a copy. A method that only reads its
receiver needs no `inout`: give it a value receiver, `func (c T) m()`.

**See also.** The [ownership guide](../OWNERSHIP.MD#the-rules-and-how-to-fix-each-error)
explains every `inout` rule with its fix.
