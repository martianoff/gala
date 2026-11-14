---
layout: default
title: "GALA-E0113 — An inout Place Escapes Its Call"
description: "GALA-E0113 rejects a reference to an inout place that could outlive the call lending it: a stored or returned pointer, a lambda capture, a method value, or a method that may keep its receiver."
keywords: "gala-e0113, gala inout, gala ownership, gala exclusivity"
permalink: /docs/errors/gala-e0113/
last_modified_at: 2026-11-14
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0113</p>

# GALA-E0113 — an `inout` place escapes its call

**When it fires.** An `inout` parameter lends the caller's place for one
call, so nothing may keep a reference to it once the call returns. The error
fires when a function with an `inout` binding `c`:

- takes `&c` (or `&c.f`) for anything but an `inout` argument: binds it
  (`val p = &c`), returns it, or passes it to a parameter that is not `inout`
  (a `Ptr[T]` or `*T` parameter, a Go function, `Println`);
- captures `c` in a lambda, a partial function or a by-name argument;
- takes a method value of a pointer-receiver method on it (`c.inc` without
  a call);
- calls a pointer-receiver method on it, or on a field of it, that may keep
  its receiver: a `Ptr`-receiver method of this package, a Go pointer
  method that is not on the compiler's no-retain allow-list, or a method of another package whose ownership summary does not
  show its receiver as borrowed. A method with an `inout` receiver, and a
  value-receiver method, are always fine.

**Minimal repro.**

```gala
package main

struct Counter(var n int)

func later(inout c Counter) func() int = () => c.n

func main() {
    var c = Counter(1)
    Println(later(&c)())
}
```

**Error output.**

```
error[GALA-E0113]: inout place c is captured by a lambda, which could outlive the call that lends it
  --> main.gala:5:48
  |
5 | func later(inout c Counter) func() int = () => c.n
  |                                                ^ pass the value instead
  |
  = hint: pass the value instead (a copy), or give the lambda its own inout parameter and lend c to it with `&c`
```

**Fix.** Read the value while the place is lent, and let the lambda keep the
copy:

```gala
package main

struct Counter(var n int)

func later(inout c Counter) func() int {
    val n = c.n
    () => n
}

func main() {
    var c = Counter(1)
    Println(later(&c)()) // 1
}
```

To call a `Ptr`-receiver method of the same package on an `inout` place,
declare its receiver `inout` instead.

**Rationale.** An `inout` place is borrowed, not owned. A reference that
outlives the call would be a second path to the caller's value, so neither
exclusivity nor the translation of the function to value semantics would
hold.

---

## Related

- [`inout` parameters](/docs/language-reference/#inout-parameters)
- [All GALA error codes](/docs/errors/)
