# GALA-E0057 — Validate method with the wrong signature

**When it fires.** A codec — `json.Codec[T]`, `yaml.Codec[T]`, `StructMeta[T]()`,
`Value[T]()`, `.Array()` / `.List()` — would decode a struct that has an
unexported field, and that struct declares a `Validate` method decoding cannot
call. Decoding calls exactly

```
func (x T) Validate() Try[T]
```

so any other shape fires: a pointer receiver (`func (x *T) ...`), a parameter,
a type parameter, or a result other than `Try[T]` (`bool`, `error`,
`Option[T]`, `Try[U]`). The struct may sit anywhere the codec reaches: the
root, a nested field, or inside an `Option`, `Array`, `List` or `HashMap`.

**Minimal repro.**

```gala
package main

import (
    "martianoff/gala/json"
    "strings"
)

struct Email(address string)

func (e Email) Validate() bool = strings.Contains(e.address, "@")

func main() {
    val codec = json.Codec[Email](json.AsIs())
    Println(codec.Decode("{\"address\":\"not an address\"}"))
}
```

**Error output.**

```
error[GALA-E0057]: cannot generate a codec for Email: Email has private fields, and its Validate method is `func (e Email) Validate() bool`, not `func (e Email) Validate() Try[Email]`
  --> main.gala:13:35
   |
13 |     val codec = json.Codec[Email](json.AsIs())
   |                                   ^^^^ Validate needs a value receiver and a Try[Email] result
   |
   = hint: Validate needs a value receiver and a Try[Email] result; declare it as `func (e Email) Validate() Try[Email]`: decoding builds the raw value and returns what Validate returns, so a Failure rejects the input with its error
```

**Fix.** Return the value itself on success and the reason it is invalid on
failure:

```gala
package main

import (
    "errors"
    "martianoff/gala/json"
    "strings"
)

struct Email(address string)

func (e Email) Validate() Try[Email] =
    if (strings.Contains(e.address, "@")) Success(e) else Failure(errors.New(s"not an email address: ${e.address}"))

func main() {
    val codec = json.Codec[Email](json.AsIs())
    Println(codec.Decode("{\"address\":\"not an address\"}"))
}
```

`Decode` now returns `Failure(not an email address: not an address)`: the
error your `Validate` returned, unchanged.

**Rationale.** A struct with an unexported field is an encapsulated value: its
package builds it only through a constructor that checks it. Decoding builds
the struct from whatever the input holds, so the codec builds the raw value and
returns what `Validate` returns — a value the constructor would refuse is
refused by the codec too. That needs one fixed signature: a `bool` or `error`
result would leave the codec to invent the failure, and a pointer receiver or a
parameter cannot be called on the freshly built value. A method with the right
name and the wrong shape is reported rather than ignored, since ignoring it
would leave the struct undecodable for a reason the author cannot see.

**Related.** A struct with private fields and no `Validate` method at all is
[GALA-E0050](GALA-E0050.md). See
[Structs With Private Fields](../GALA.MD#structs-with-private-fields).
