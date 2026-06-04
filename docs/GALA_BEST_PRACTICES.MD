# GALA Best Practices

A scannable rule list for writing idiomatic GALA — useful as both a learning checklist and a quick reference during code review. For full explanations and edge cases, see [GALA.MD](GALA.MD); rules link back to the relevant section.

---

## Hard rules (never violate)

- **Research syntax first.** Before writing GALA, consult [GALA.MD](GALA.MD) — GALA is functional and relies heavily on pattern matching; do not assume Go syntax works.
- **No workarounds for transpiler bugs.** If a construct fails to transpile, write a minimal repro test and fix the transpiler. Do not rewrite GALA code to dodge the bug.
- **No special-casing `std`.** The standard library uses the same import/resolution mechanism as any other library.
- **Concrete types only.** Never emit `any`/`interface{}` unless the GALA source explicitly asks for it.

---

## Variables

- **Default to `val`.** Use `var` only for accumulators and loop counters. ([§2](GALA.MD#2-variable-declarations))
- **Updates by copy, not mutation.** `person.Copy(age = 31)` — not `person.age = 31`.
- **`:=` is immutable** inside functions (unlike Go).

---

## Type inference (omit everything inferrable)

- **No generic params on calls** — `Some(42)`, `ListOf(1, 2, 3)`, `HashMapOf(("a", 1))`.
- **No lambda param types** when passed to a typed method — `list.Map((x) => x * 2)`.
- **No variable types** — `val x = 42`, not `val x int = 42`.
- **No method type params** — `list.Map((x) => x * 2)`, not `list.Map[int](...)`.
- **No FoldLeft accumulator type** — `list.FoldLeft(0, (acc, x) => acc + x)`.
- **Annotate only when there's nothing to infer from**: `None[int]()`, `Left[string, int]("err")`, empty collections, standalone lambdas.

---

## Collections — prefer GALA over Go

Use `collection_immutable.{Array, List, HashMap}` for general work. Use `go_interop.Slice*` only for Go interop or variadics. ([§9 Slices](GALA.MD#slices), [§16 Collections](GALA.MD#collections))

| Need | Use |
|------|-----|
| Random access, transforms | `Array` from `collection_immutable` |
| Prepend-heavy, recursion | `List` from `collection_immutable` |
| Map/Set | `HashMap` / `HashSet` from `collection_immutable` (or `collection_mutable` if mutating) |
| Pass to Go libraries | `SliceOf(...)` from `go_interop`, or `.ToGoSlice()` |
| Variadic call | Go slice (`[]T`) via `SliceOf` |

- **Use functional ops, not loops:** `arr.Map`, `arr.Filter`, `arr.FoldLeft`, `arr.ForEach`, `arr.MkString`, `arr.ZipWithIndex`, `arr.Collect`.
- **Short-circuit ops still loop:** `Exists`, `ForAll`, `Find`, `Contains` — keep these.
- **Safe access:** `arr.GetOption(i)` returns `Option[T]` instead of panicking.
- **Build incrementally:** use `arrayBuilder` for hot paths.
- **Performance:** `Array` is O(log32 n) random access; `List` is O(1) prepend.
- See [IMMUTABLE_COLLECTIONS.MD](IMMUTABLE_COLLECTIONS.MD) and [MUTABLE_COLLECTIONS.MD](MUTABLE_COLLECTIONS.MD).

---

## Optional / error values

- **`Option[T]` for "may be absent"** — never use `nil` for missing values. ([§16 Optional Values](GALA.MD#optional-values-in-immutable-fields))
- **`Try[T]` for operations that may fail** — wraps success or panic-equivalent.
- **`Either[E, T]` when the error type carries data.**
- **Chain with `FlatMap`** for railway-oriented composition.
- **Pattern-match to extract:** `case Some(x) =>` / `case None =>`. Do not call `.Get()` without first checking.

---

## Pattern matching

- **Prefer `match` over if/else** for type or value dispatch. ([§6 Match Expression](GALA.MD#match-expression))
- **Use sealed types** for closed variant sets — `sealed type Shape { case Circle(...); case Rectangle(...) }`. The transpiler enforces exhaustiveness; no `case _ =>` needed.
- **Replace iota enums with sealed cases** — `case Red() case Green() case Blue()`.
- **Use guards, not nested ifs** — `case n: int if n > 0 =>`.
- **Use sequence patterns** for `Array`/`List` — `case List(first, second, rest...) =>`.

---

## Lambdas

- **Implicit param types when called from a typed context** — `(x) => x * 2`.
- **Block body when multi-statement** — `(x) => { val y = x*2; return y + 1 }`.
- **Underscore shorthand** for trivial transforms — `nums.Map(_ * 2)`, `people.Map(_.Name)`.
- **Explicit types only for standalone lambdas** — `val f = (x int) => x * 2`.

---

## Strings & printing

- **`s"Hello $name"`** instead of `fmt.Sprintf("Hello %s", name)` — no `fmt` import. ([§10 String Interpolation](GALA.MD#string-interpolation))
- **`f"$price%.2f"`** for explicit format control (precision, padding).
- **`Println(...)` / `Print(...)`** instead of `fmt.Println` / `fmt.Print` — no `fmt` import.
- **Import `fmt` only** for `fmt.Errorf`, `fmt.Fprintf`, `fmt.Stringer`.
- **Escaped quotes are fine inside `${...}`** — `s"${m.Get(\"k\").GetOrElse(\"x\")}"`.

---

## If as expression

- **Bind the result** — `val x = if (cond) a else b`. Don't `var x = a; if (cond) { x = b }`.
- **Block branches** — last expression is the value: `val u = if (q != "") { val e = encode(q); s"$base?$e" } else { base }`.

---

## Library API design

- **Use named function types with concrete params** so callers can write `(req) => ...`:
  ```gala
  type Handler func(Request) Response
  func GET(path string, handler Handler) { ... }
  GET("/", (req) => Ok("hello"))
  ```
- **Avoid `any` in callback signatures** — it forces callers to annotate lambdas.

---

## Tuples

- **Parenthesis syntax** — `(1, "hello")`, not `Tuple[int, string](V1=1, V2="hello")`.
- **Use for map entries** — `HashMapOf(("a", 1), ("b", 2))`.
- **Destructure with patterns** — `val (x, y) = tuple`, `case (a, b) =>`.

---

## Type conversions

- **Use `go_interop`** — `ToBytes("s")`, `ToString(bytes)`, `ToRunes("s")`. Go's `[]byte("s")` syntax is not supported.
- **All numeric conversions are explicit** — no implicit widening.

---

## Naming & formatting

- **PascalCase** for types and exported names; **camelCase** for locals and unexported.
- **Trailing commas** in multi-line param/struct/sealed-case lists.
- **Break lines >100 chars** into multi-line form.

---

## Testing

Use the GALA test framework from `martianoff/gala/test` — not Go's `testing`/`testify`. ([§15 Testing](GALA.MD#15-testing))

- **File convention:** `_test.gala` collocated with source files.
- **Test signature:** `func TestXxx(t T) T` — assertions return `T`, chain by passing it forward.
  ```gala
  import . "martianoff/gala/test"

  func TestAddition(t T) T {
      var t1 = Eq(t, 1 + 1, 2)
      return IsTrue(t1, 2 > 1)
  }
  ```
- **Assertions:** `Eq` / `NotEq` / `IsNil`, `IsTrue` / `IsFalse`, `Greater` / `Less`, `Contains` / `HasPrefix` / `HasSuffix`, `IsSome` / `IsNone` / `IsSuccess` / `IsFailure`, `Panics` / `NotPanics`, `Fail`.
- **Table-driven:** use `RunCases[In, Out]` with `Case[In, Out](Name=..., Input=..., Expected=...)` — each case runs as a subtest.
- **Subtests:** `t.Run("name", (sub) => Eq(sub, ...))` — panic-recovered.
- **Internal tests** live in the same package (access to unexported names); **external tests** use `package main` and import the lib.
- **Run with `gala test`** (auto-discovers `_test.gala`, generates the runner). For Bazel, generate the test target with `bazel run //:gazelle` rather than hand-writing it — it emits the `gala_go_test` macro (`gala_test` for output-comparison tests).
- **When adding a language feature**, add a verification program under `examples/` in addition to unit tests.

---

## Pre-commit checklist

1. `bazel build //...` passes.
2. `bazel test //...` passes.
3. `bazel run //:gazelle` if files were added/removed — it generates and maintains BUILD files for Go, GALA, and mixed GALA+GO packages, so prefer it over hand-writing `gala_library` / `gala_binary` / `gala_test` targets.
4. Examples in `examples/` compile.
5. Update [GALA.MD](GALA.MD) / [TYPE_INFERENCE.MD](TYPE_INFERENCE.MD) / [EXAMPLES.MD](EXAMPLES.MD) if grammar or features changed.
