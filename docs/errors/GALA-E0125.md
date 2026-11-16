# GALA-E0125 — Go type with no GALA spelling

**When it fires.** A type is written in a form only Go has:

- the empty interface, `interface{}`, which GALA writes as `any`;
- an interface type written inline, `func f(s interface{ String() string })`,
  where GALA uses the name of a declared interface;
- a fixed-size array type, `[16]byte` or `[...]int`, which GALA has no form
  for.

**Minimal repro.**

```gala
package main

func show(v interface{}) {
    Println(v)
}

func main() {
    show(42)
}
```

**Error output.**

```
error[GALA-E0125]: Go `interface{}`: GALA writes the empty interface as `any`
  --> main.gala:3:13
  |
3 | func show(v interface{}) {
  |             ^^^^^^^^^^^ write `any`
  |
  = hint: write `any`
```

**Fix.** Write `any` for `interface{}`:

```gala
package main

func show(v any) {
    Println(v)
}

func main() {
    show(42)
}
```

An inline interface type becomes a declared one, `type Stringer interface {
String() string }`, written by its name. A fixed-size array becomes a Go
slice, `[]byte`, made with `go_interop.SliceWithSize[byte](16)`, or a GALA
`Array[byte]`; a Go API whose signature needs `[16]byte` keeps that code in a
`.go` file of the same package.

**Rationale.** The parser used to stop with `mismatched input 'interface'
expecting ')'`, or, for an array, `no viable alternative at input '[16'`,
neither of which named the GALA type. Each is now this error, whose hint
names the replacement for the type written.

**Scope.** A declared empty interface, `type Any interface{}`, is valid GALA
and is not reported. An anonymous struct type is
[GALA-E0090](GALA-E0090.md) where the generated Go would have to spell it.
