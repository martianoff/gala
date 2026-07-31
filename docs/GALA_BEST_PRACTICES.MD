# GALA Best Practices

A scannable rule list for writing idiomatic GALA — useful as both a learning checklist and a quick reference during code review. For full explanations and edge cases, see [GALA.MD](GALA.MD); rules link back to the relevant section.

---

## Hard rules (never violate)

- **Research syntax first.** Before writing GALA, consult [GALA.MD](GALA.MD) — GALA is functional and relies heavily on pattern matching; do not assume Go syntax works.
- **No workarounds for transpiler bugs.** If a construct fails to transpile, write a minimal repro test and fix the transpiler. Do not rewrite GALA code to dodge the bug.
- **No special-casing `std`.** The standard library uses the same import/resolution mechanism as any other library.
- **Concrete types only.** Never emit `any`/`interface{}` unless the GALA source explicitly asks for it.
- **No bare Go builtins.** `append`, `make`, `new`, `cap`, `copy`, `delete`, `close`, `complex`, `real`, `imag`, `panic`, `recover`, and `len` are a hard error (GALA-E0035). Use the GALA-native form or the sanctioned interop wrapper (see *Length & size* and *Resource cleanup* below). A user-defined function that happens to share one of these names is fine — only the builtin is forbidden.
- **No Go statement keywords.** `defer`, `go`, `goto`, `fallthrough`, `select`, and `chan` are not GALA statements — a bare use is a hard error (GALA-E0036). Use a `use` binding or the `resource` combinators for cleanup, and `go_interop.Spawn(...)` for goroutines (see *Resource cleanup* below).

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
- **No type arg on `None()`** when the context pins the type — `func f() Option[int] = None()`, `val x Option[int] = None()`, or a `None()` arm of a match returning `Option[int]`. The type flows down from the return/annotation, so `None[int]()` is redundant there.
- **Annotate only when there's nothing to infer from**: `None[int]()` *only* where no context pins it (e.g. a bare `None[int]()` in a lambda whose result type is unconstrained), `Left[string, int]("err")`, empty collections, standalone lambdas.

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

- **Go slice/map types go in type positions only.** `[]T` and `map[K]V` may be
  written wherever a type is expected — signature, struct field, `val`/`var`
  annotation, lambda parameter annotation, type alias, or as a type argument of a
  generic type in one of those — and **never inside an expression**. So
  `func take(m HashMap[string, []byte])` is fine, but
  `EmptyHashMap[string, []byte]()` is not: write `EmptyHashMap[string, Array[byte]]()`,
  or `EmptyHashMap[string, string]()` when the bytes are text. Name the Go type
  with a type alias (`type Bytes []byte`) if it really has to stay a Go slice.
  ([GALA-E0040](errors/GALA-E0040.md),
  [§9 positional rule](GALA.MD#where-go-slice-and-map-types-may-be-written))

- **Use functional ops, not loops:** `arr.Map`, `arr.Filter`, `arr.FoldLeft`, `arr.ForEach`, `arr.MkString`, `arr.ZipWithIndex`, `arr.Collect`.
- **Short-circuit ops still loop:** `Exists`, `ForAll`, `Find`, `Contains` — keep these.
- **Safe access:** `arr.GetOption(i)` returns `Option[T]` instead of panicking.
- **Build incrementally:** use `arrayBuilder` for hot paths.
- **Performance:** `Array` is O(log32 n) random access; `List` is O(1) prepend.
- See [IMMUTABLE_COLLECTIONS.MD](IMMUTABLE_COLLECTIONS.MD) and [MUTABLE_COLLECTIONS.MD](MUTABLE_COLLECTIONS.MD).

---

## Length & size (`.Size()` / `.ByteSize()`, not `len`)

Bare `len` is forbidden. Ask a value for its size instead:

- **`.Size()`** — logical size everywhere: element count for `Array`/`List`/`HashMap` and Go slices/maps, **character count** for strings (`"héllo".Size() == 5`). This avoids Go's `len(string)`-is-bytes footgun.
- **`.ByteSize()`** — a string's raw **byte** count (`"héllo".ByteSize() == 6`). Use it when you genuinely mean bytes (hashing, byte offsets).
- **`append` → `go_interop.SliceAppend` / `SliceAppendAll`** (or an `Array`); **`cap` → `go_interop.SliceCap`**; **`make` → `SliceWithSize` / `MapEmpty`**; **`new` → `go_interop.New[T]()`**.

---

## Resource cleanup (`resource` combinators)

Import `martianoff/gala/resource` and wrap the using-code in a combinator that guarantees release on **every** exit path — normal or panic — and composes with `Try`/`Either`, instead of a hand-written `Close()` you can forget:

- **`Using(res, (r) => …)`** — for anything with `Close() error` (a `Closeable`, matching Go's `io.Closer`). `Close()` runs on the way out.
- **`Bracket(res, release, body)`** — the general run-then-release primitive over an eagerly-acquired resource.
- **`WithLock(mu, () => …)`** — hold a `go_interop.Mutex` for the body, unlock after.

```gala
func readAll(path string) string =
    Using(OpenFile(path), (f) => f.ReadString())  // Close() guaranteed
```

- **`use x = acquire`** — the top-to-bottom binding form (no nesting pyramid): `x` is bound for the rest of the block and `x.Close()` runs when the function returns, on every path. Multiple `use` bindings release LIFO. No import needed.

```gala
func copyFile(src string, dst string) Try[Int] = Try(() => {
    use in  = OpenFile(src)     // in.Close()  runs last
    use out = CreateFile(dst)   // out.Close() runs first (LIFO)
    out.Write(in.ReadString())
})
```

**No `defer`, no bare `go`.** `defer` and `go` are not GALA statements — a bare use is a hard error (GALA-E0036). Cleanup is a `use` binding or a `resource` combinator; a goroutine is `go_interop.Spawn(() => f())`.

`panic` → `go_builtins.Panic(...)` for a truly unrecoverable invariant; prefer `Option`/`Try`/`Either`.

---

## Optional / error values

- **`Option[T]` for "may be absent"** — never use `nil` for missing values. ([§16 Optional Values](GALA.MD#optional-values-in-immutable-fields))
- **`Try[T]` for operations that may fail** — wraps success or panic-equivalent.
- **`Either[E, T]` when the error type carries data.**
- **Chain with `FlatMap`** for railway-oriented composition.
- **Prefer `bind`/`also` over nested `FlatMap` pyramids** for multi-step monadic code. A `bind` block keeps every intermediate value in scope (the "graph" case a linear pipe can't express) and reads top-to-bottom instead of nesting. Reach for it whenever a `FlatMap` chain would nest more than once or a later step reuses an earlier value. ([bind / also notation](BIND_NOTATION.MD))
- **Use `also` for independent clauses** — a `bind` + `also` group. Over `Validated` (in the `validation` package) it **accumulates** all errors; over `Future` (in `concurrent`) it runs the clauses **concurrently**; over the fail-fast `std` monads it is a sequential fallback. Never make `also` clauses reference each other — use `bind` when a later step depends on an earlier one.
- **Bound names are immutable `val`s; the trailing value must be `M[R]`** — write the constructor (`Success`/`Some`/`Valid`); it infers its type argument from the block.
- **Still use `Map`/`match`/`Recover`/`GetOrElse` where they fit** — a single transform is `Map`, extracting a result is `match` or `GetOrElse`, and error recovery is `Recover`/`RecoverWith`. `bind`/`also` is for *sequencing* several fallible steps, not a replacement for those.
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
- **Drop `() =>` for zero-arg thunk params** — where a parameter is `func() T`, pass the bare expression: `Try(strconv.Atoi(s))`, `Future(loadFromDB(id))` instead of `Try(() => …)` / `Future(() => …)`. Keep the `() =>` only for block bodies. ([§3 By-Name Arguments](GALA.MD#by-name-arguments-thunk-sugar))

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
- **Keep shared-but-unsupported code in `internal/`** when you ship a library to consumers you do not control. A package under `internal/` is importable only from the tree rooted at that directory's parent, so it can be split, renamed or deleted without breaking anyone:
  ```
  mylib/
    mylib.gala             # public API
    internal/
      detail/detail.gala   # shared across mylib's packages, invisible to consumers
  ```
  Not a default layout — for an application the whole module is already private, and an `internal/` tree just adds a directory level. Reach for it where there is a real API boundary to defend. Publishing later means moving the directory, which changes the import path. See [Package Visibility](GALA.MD#package-visibility).

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
