---
layout: default
title: "GALA-E0053 — Pointer Method on a Copy of a Value That Must Not Be Copied"
description: "GALA-E0053 rejects a pointer-receiver call on a val, val field, call result or literal whose type holds a sync.Mutex, WaitGroup, atomic, strings.Builder or bytes.Buffer, because the call would run on a copy."
keywords: "gala-e0053, gala mutex val, gala copylocks, gala sync.Mutex copy, gala strings.Builder val, gala pointer receiver val, gala nocopy"
permalink: /docs/errors/gala-e0053/
last_modified_at: 2026-09-27
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0053</p>

# GALA-E0053 — pointer method on a copy of a value that must not be copied

**When it fires.** A method with a pointer receiver is called on a value Go
cannot address, and the value's type must not be copied:

```gala
struct Counter(mu sync.Mutex, var n int)

func (c *Counter) Inc() {
    c.mu.Lock()   // mu is a val field
    c.mu.Unlock()
}
```

A `val`, a `val` struct field (fields are `val` unless marked `var`), a call
result, a literal and a type assertion cannot be addressed. GALA calls a
pointer method on such a receiver through a fresh copy, which is fine for
`url.URL.String` but wrong for these types, so the call is rejected.

A type must not be copied when it, or any field reached through struct fields
(and, for a Go type, array elements; not through pointers, slices, maps or
collections), is one of the following. For a generic struct, its type
arguments count as its fields.
This follows `go vet`'s copylocks check:

- a `sync` or `sync/atomic` type: `Mutex`, `RWMutex`, `WaitGroup`, `Once`,
  `Cond`, `Map`, `Pool`, `atomic.Int64`, `atomic.Value`, …;
- a type whose pointer has `Lock()` and `Unlock()` methods its value lacks;
- a type named `noCopy`, the conventional marker;
- `strings.Builder` or `bytes.Buffer`.

**Minimal repro.**

```gala
package main

import "sync"

struct Counter(mu sync.Mutex, var n int)

func (c *Counter) Inc() {
    c.mu.Lock()
    c.n = c.n + 1
    c.mu.Unlock()
}

func main() {
    var c = Counter(sync.Mutex{}, 0)
    c.Inc()
    Println(c.n)
}
```

**Error output.**

```
error[GALA-E0053]: cannot call Lock on this sync.Mutex: it cannot be addressed here, so the call would run on a copy, and sync.Mutex must not be copied
  --> main.gala:8:10
  |
8 |     c.mu.Lock()
  |          ^^^^ declare it with `var`, or hold a pointer
  |
  = hint: declare it with `var`, or hold a pointer (`&T{...}`)
```

**Fix.** Keep the value somewhere Go can address. Mark the field (or local)
`var`:

```gala
struct Counter(var mu sync.Mutex, var n int)
```

or hold a pointer to it:

```gala
struct Counter(mu *sync.Mutex, var n int)

var c = Counter(&sync.Mutex{}, 0)
```

**Rationale.** A `val` is stored as `Immutable[T]` and read through `Get()`,
which returns a copy. Go calls a pointer-receiver method only on an
addressable value, so the call runs on a copy made for it. For most types the
only effect is that the method's writes to the receiver are dropped, and the
`val` stays as it was. For these types the copy breaks the program:

- `Lock` and `Unlock` each get their own copy of a `Mutex`, so nothing is
  locked, and the `Unlock` is a fatal runtime error (`sync: unlock of unlocked
  mutex`);
- `WaitGroup.Wait` returns at once, and `Done` panics on a negative counter;
- an atomic's `Add` and `Store` are lost;
- a `bytes.Buffer` loses every write, and a `strings.Builder` loses them or
  panics ("illegal use of non-zero Builder copied by value").

Before this check, such a call failed in `go build` with "cannot call pointer
method Lock on sync.Mutex"; this code reports it at the GALA source instead.

**Where it stands down.** An addressable receiver is called directly, so the
method works on the value itself: a `var`, a function parameter, a pattern
binding, and a `var` field (of a `var`, or reached through a pointer). A `val`
field is read through a copy even behind a pointer, which is why the repro
above is rejected.
Types that are fine to copy (`url.URL`, `time.Time`) still run their pointer
methods on a copy of a `val`.
