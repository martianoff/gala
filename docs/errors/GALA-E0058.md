# GALA-E0058 — Generated codec metadata named in GALA code

**When it fires.** GALA source names a type the transpiler generates for a
codec: `StructMeta_X` (a library struct's metadata, exported for its
importers), `_StructMeta_X` (a `main` package struct's metadata) or
`_ValueMeta_X`. It fires for a use, qualified (`billing.StructMeta_Email`) or
not, and for a declaration of that name.

**Minimal repro.**

```gala
package main

struct Email(address string)

func main() {
    Println(_StructMeta_Email{}.NumFields())
}
```

**Error output.**

```
error[GALA-E0058]: _StructMeta_Email is codec metadata the transpiler generates and cannot be named in GALA code
  --> main.gala:6:13
  |
6 |     Println(_StructMeta_Email{}.NumFields())
  |             ^^^^^^^^^^^^^^^^^ use json.Codec[T], yaml.Codec[T] or StructMeta[T]() instead
  |
  = hint: use json.Codec[T], yaml.Codec[T] or StructMeta[T]() instead; those check that a struct with private fields may be decoded
```

**Fix.** Ask for the metadata through the intrinsic, or use a codec:

```gala
package main

struct Email(Address string)

func main() {
    Println(StructMeta[Email]().NumFields())
}
```

**Rationale.** A package's `StructMeta_X` reads and builds the struct's
unexported fields: that is what lets another package encode it. `Codec[T]`
and `StructMeta[T]()` reach the same metadata but first check, while building,
that a struct with private fields declares `Validate`
([GALA-E0050](GALA-E0050.md), [GALA-E0057](GALA-E0057.md)). Named directly,
the metadata skips that check — decoding a struct without `Validate` would
fail only when the program runs — and its `EncodeFields` hands any caller the
fields the package keeps to itself. A declaration of such a name is rejected
too, since it could collide with the type the transpiler emits.

**Scope.** Generated Go code and hand-written Go siblings are not GALA source
and are not checked. See
[Structs With Private Fields](../GALA.MD#structs-with-private-fields).
