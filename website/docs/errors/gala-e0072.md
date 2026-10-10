---
layout: default
title: "GALA-E0072 — Sealed Variant With Too Many Fields"
description: "GALA-E0072 fires when a sealed variant declares more than 10 fields, the widest std tuple its extractor can return. Group related fields into a struct."
keywords: "gala-e0072, gala sealed variant fields limit, gala sealed type too many fields, gala tuple10, gala unapply tuple"
permalink: /docs/errors/gala-e0072/
last_modified_at: 2026-10-10
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / <a href="/docs/errors/">Error Codes</a> / GALA-E0072</p>

# GALA-E0072 — Sealed variant with too many fields

**When it fires.** A sealed variant declares more than 10 fields. A variant's
extractor returns its fields as one std tuple, so `case Order(id, _, …)` can
unpack them, and the widest std tuple is `Tuple10`. That is the same limit
GALA puts on the values of a Go call returned as one tuple.

**Minimal repro.**

```gala
package main

sealed type Event {
    case Order(Id int, User int, Item int, Qty int, Price int, Tax int, Ship int, Total int, Paid bool, Sent bool, Note string)
    case Cancel(Id int)
}

func main() {
    Println(Cancel(1))
}
```

**Error output.**

```
error[GALA-E0072]: sealed variant "Order" has 11 fields; a variant can have at most 10
  --> main.gala:4:10
  |
4 |     case Order(Id int, User int, Item int, Qty int, Price int, Tax int, Ship int, Total int, Paid bool, Sent bool, Note string)
  |          ^^^^^ group related fields into a struct and give the variant a fi…
  |
  = hint: group related fields into a struct and give the variant a field of that type
```

**Fix.** Group related fields into a struct, and give the variant one field
of that type. A pattern then matches the struct by its own fields:

```gala
package main

struct Line(Item int, Qty int, Price int, Tax int, Ship int, Total int)

sealed type Event {
    case Order(Id int, User int, Line Line, Paid bool, Sent bool, Note string)
    case Cancel(Id int)
}

func total(e Event) int = e match {
    case Order(_, _, Line(_, _, _, _, _, t), _, _, _) => t
    case Cancel(_)                                    => 0
}

func main() {
    Println(total(Order(1, 2, Line(3, 1, 10, 1, 2, 13), true, false, "")))
}
```

**Rationale.** Before this check, a variant with more than 10 fields
generated an extractor whose tuple type does not exist, and the transpile
failed with GALA-E0017 (unparseable generated Go) even when no case matched
the variant. Plain structs have no such limit: their patterns read the
fields directly.
