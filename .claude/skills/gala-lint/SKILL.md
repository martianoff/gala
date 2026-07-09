---
description: Lint GALA code for best practices and coding standards. TRIGGER when user asks to lint, review, or check GALA code quality, or when reviewing .gala files for style/pattern issues.
user-invocable: true
---

# GALA Best Practices Linter

Verify that all GALA code in the codebase follows GALA best practices and coding standards.

**Argument:** `$ARGUMENTS` - Optional path to a specific `.gala` file or directory to lint (default: scan all `.gala` files in the project)

## Instructions

### Step 1: Find all GALA files

Search for `.gala` files in the target path (or entire project), excluding build outputs and generated files.

### Step 2: Analyze each file

For each `.gala` file, check all linting rules below and collect violations.

### Step 3: Generate report

Present findings in the output format specified at the end.

---

## Linting Rules

### 0. Bare Go builtins are a HARD ERROR (`GALA-E0035`)

Bare Go builtins are **not** part of GALA's surface — calling one is a hard
transpile error (`GALA-E0035`), not merely "prefer GALA". This applies to
`append`, `make`, `new`, `cap`, `copy`, `delete`, `close`, `complex`, `real`,
`imag`, `panic`, `recover`, and `len`. Code that still calls them will not
compile, so treat any occurrence as a must-fix. The check is resolver-aware: a
user-defined function that happens to share one of these names (e.g. a local
`func delete(...)`) is fine — only the builtin is forbidden.

| Forbidden builtin | Sanctioned replacement |
|---|---|
| `len(x)` | `x.Size()` — logical size (**characters** for strings, element count for slices/maps/collections). `x.ByteSize()` — a string's raw **byte** count. |
| `append(s, v)` | `go_interop.SliceAppend(s, v)` / `SliceAppendAll(s, more)` at a Go-slice boundary; otherwise `Array.Append` / `List.Prepend` |
| `cap(x)` | `go_interop.SliceCap(x)` |
| `make([]T, n)` | `go_interop.SliceWithSize` / `SliceWithCapacity`; `MapEmpty` for maps; or an empty `Array`/`HashMap` |
| `new(T)` | `go_interop.New[T]()` (pointer), or a zero value / `Option[T]` |
| `delete(m, k)` | `go_interop.MapDelete(m, k)`, or `HashMap.Remove(k)` |
| `close`/`complex`/`real`/`imag` | `go_interop.CloseChan` / `Complex` / `Real` / `Imag` |
| `panic(v)` | `go_builtins.Panic(v)` — **only** where a panic is genuinely intended (see rule 11); prefer `Option`/`Try`/`Either` |
| `recover()` | not available — `Try` captures panics (`Try(() => …)` / `TryApply`) |

> `.Size()` also fixes a footgun: Go's `len(string)` returns **bytes**, so
> non-ASCII text mis-counts. `.Size()` is logical size (`"héllo".Size() == 5`);
> `.ByteSize()` is the raw byte count (`== 6`).

The rows below (rule 4a Go-slices, rule 11 error handling) reference these
replacements; a bare builtin is always the higher-severity `GALA-E0035` finding.

### 1. Immutability (HIGH priority)

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| Unnecessary `var` | `var x = <literal>` where x is never reassigned | Use `val x = <literal>` |
| Mutable loop counter | `var i = 0; for { i++ }` when FoldLeft works | Use `FoldLeft` or `ForEach` |
| Mutation instead of copy | `person.age = 31` | Use `person.Copy(age = 31)` |
| Nil for optional fields | `var next *Node = nil` (mutable just to allow nil) | Use `val next Option[Node]` or `val next func() Option[Node]` |
| Mutable pointer for optional | `var data *T` assigned once then read | Use `val data Option[T]` |

**Check**: Search for `var ` declarations and verify each is reassigned later in the same scope.

**Acceptable `var` uses**: loop counters, accumulators in Fold-like operations, stream traversal cursors.

### 2. Pattern Matching (HIGH priority)

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| If-else on Option | `if opt.IsDefined() { opt.Get() }` | `opt match { case Some(x) => ... }` |
| If-else on Either | `if either.IsRight() { either.Right() }` | `either match { case Right(x) => ... }` |
| Type assertion chains | `if _, ok := x.(T); ok { }` | `x match { case t: T => ... }` |
| Long if-else chains | 3+ else-if branches | Use `match` expression |
| If-else on isEmpty/NonEmpty | `if x.IsEmpty() { ... } else { ... }` | Use extractors: `x match { case Empty() => ...; case NonEmpty(h, t) => ... }` |
| Head/Tail after empty check | `if !list.IsEmpty() { list.Head(); list.Tail() }` | `list match { case Cons(h, t) => ... }` |
| Unused extractors | Defining `Unapply` extractors but using if-else internally | Use your own extractors! |
| Get(0) after length check | `if x.Length() > 0 { x.Get(0) }` | `x.HeadOption()` or pattern match |
| Unnecessary default on sealed | `case _ =>` when all sealed variants are covered | Remove `case _ =>`; exhaustive match is verified by the transpiler |
| IsXxx() chains on sealed type | `if s.IsCircle() { ... } else if s.IsRectangle() { ... }` | `s match { case Circle(r) => ... case Rectangle(w, h) => ... }` |
| If-err-nil on Go error return | `val x, err = f(); if err == nil { ... }` | Wrap with `Try(f)` (if f takes no args) or `Try(() => f(args))` then use `.Map`, `.GetOrElse`, or `match` |
| Sequential if-err-nil fallback | Multiple `val x, err = f(); if err == nil { return x }` in sequence | `Try(f1).OrElse(Try(f2))` chain (or `Try(() => f1(args)).OrElse(Try(() => f2(args)))` if args needed) |
| Lambda wrapper for zero-arg func | `Try(() => f())` where f takes no arguments | `Try(f)` — pass function reference directly (for any other single-expression body, drop `() =>` instead — see rule 11e) |

### 3. Sealed Types (HIGH priority)

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| Manual discriminator | Struct with `kind int` or `_variant uint8` field + iota constants + switch/if-else on kind | Use `sealed type` declaration |
| Interface for closed variants | Interface with a small, fixed set of implementations that are all in the same package | Use `sealed type` instead |
| Iota enum without data | `const ( Red = iota; Green; Blue )` for a fixed set of values | `sealed type Color { case Red() case Green() case Blue() }` |
| Iota enum with data | Iota constants + separate struct fields per variant | `sealed type` with fields on each case |
| Struct union pattern | A struct with fields for multiple variants where only some are used at a time | `sealed type` with variant-specific fields |
| `{}.Apply()` on zero-arg case | `TeamLead{}.Apply()`, `Idle{}.Apply()`, `None[string]{}.Apply()` | `TeamLead()`, `Idle()`, `None[string]()` — call the case constructor directly |

**Check**: Search for `iota` declarations that represent a fixed set of categories/kinds/variants. Search for struct fields named `kind`, `type`, `tag`, `variant` of integer type paired with constants. Grep `\{\}\.Apply\(\)` (and `\][a-z_, 0-9]*\{\}\.Apply\(\)` for the parameterized form like `None[string]{}.Apply()`) — every hit is a leftover workaround from an early transpiler bug where bare zero-arg sealed-case constructors didn't lower correctly. The bug has been fixed; the `{}.Apply()` form should be replaced with the direct call. Pervasive in test files and any code that predates the fix.

**Bad pattern** — `{}.Apply()` boilerplate:
```gala
// BAD: leftover workaround
val msg = TeamLead{}.Apply()
val state = Idle{}.Apply()
val maybe = None[string]{}.Apply()
val s = Transition(Idle{}.Apply(), UserPrompted{}.Apply(), ctx)
```

**Good pattern** — direct case construction:
```gala
val msg = TeamLead()
val state = Idle()
val maybe = None[string]()
val s = Transition(Idle(), UserPrompted(), ctx)
```

**Good pattern** - sealed type with exhaustive match:
```gala
sealed type Shape {
    case Circle(Radius float64)
    case Rectangle(Width float64, Height float64)
    case Point()
}

// No case _ needed - all variants covered
func describe(s Shape) string = s match {
    case Circle(r) => f"radius=$r%.2f"
    case Rectangle(w, h) => s"${w}x${h}"
    case Point() => "point"
}
```

**Bad pattern** - manual discriminator:
```gala
// BAD: Manual variant encoding
type Shape struct {
    kind int
    var radius float64
    var width float64
    var height float64
}
val circleKind = 0
val rectKind = 1
```

### 4. Go Native Types vs GALA Standard Types (HIGH priority)

**Principle**: GALA prefers its own standard data structures over Go native types. Go native types should only be used at interop boundaries (calling Go APIs, receiving Go returns). Internal logic should use GALA types for immutability, pattern matching, and functional operations.

#### 4.0. Boundary vs Internal Detection Heuristics

Before flagging a Go slice or map, determine whether it sits at a true interop boundary. A value is "at a boundary" only if **every** use of it is one of:

- **Argument to an imported Go package function** — `f.Write(buf)`, `strings.Join(parts, ",")`, `http.DefaultClient.Do(req)`
- **Argument to a method on a Go-imported struct** — `(*bytes.Buffer).Read(buf)`, `(*sql.Rows).Scan(dst...)`
- **Return value from a Go API that the caller will pass straight to another Go API** — simple forwarding, no inspection
- **Field of a Go-imported struct being constructed** — `http.Request(Body = body, Header = hdr)`
- **Target of JSON/YAML/gob marshal or unmarshal**
- **Variadic spread into another variadic Go call** — `log.Printf(fmt, args...)`

If the value is also:
- indexed (`xs[i]`), iterated (`for _, x := range xs`), appended to, measured (`len(xs)`), or returned from a GALA-internal function — it has **leaked past the boundary** and should be a GALA collection.

**The "scratch buffer" exception**: `go_interop.SliceWithSize[byte](n)` created *solely* to be filled by a Go `.Read(buf)` / `.Write(buf)` / `.Scan(buf)` call on the next few lines is acceptable (bare `make([]byte, n)` is a hard error — use `SliceWithSize`) — this is the canonical I/O buffer pattern and has no GALA equivalent. Flag only if the buffer is subsequently inspected with manual index loops, appended to, or stored as a field of a GALA struct for later GALA-side use.

**Smell signals** (strong indicators the Go type has leaked internally):
- GALA-internal function returning `[]T` or `map[K]V` (not a Go-interop shim)
- Local `var xs []T` / `var m map[K]V` that is only read and written inside GALA code (no Go call consumes it)
- `for _, x := range xs` over a Go slice when `xs.ForEach(f)` would work on an `Array[T]`
- `collection_immutable` already imported in the same file but a Go slice/map is used anyway for similar work
- Helper functions named `mapOf`, `sliceOf`, `indexBy` that build Go maps/slices — usually a sign `HashMap.GroupBy` / `Array.FoldLeft` were overlooked

Apply these heuristics before flagging; record the inferred boundary/internal classification in the issue so the user can contest borderline cases.

#### 4a. Go Slices → GALA Collections

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| SliceOf for general use | `val items = SliceOf(1, 2, 3)` when not passing to Go API | `val items = ArrayOf(1, 2, 3)` or `ListOf(1, 2, 3)` |
| SliceEmpty for general use | `val items = SliceEmpty[int]()` | `val items = EmptyArray[int]()` or `EmptyList[int]()` |
| Go slice type in struct | `type Foo struct { Items []int }` when functional ops needed | Use `Array[int]` or `List[int]` |
| Bare `len` for length | `len(x)` — a hard error (`GALA-E0035`) | `x.Size()` (logical size; **characters** for strings) or `x.ByteSize()` (string bytes) — see rule 0 |
| Manual loop on slice | `for i := 0; i < slice.Size(); i++ { ... }` | Use GALA collection with `.ForEach`, `.Map`, `.Filter` |
| Bare `append` on slice | `result = append(result, item)` — a hard error (`GALA-E0035`) | `Array.Append()` / `List.Prepend()` / `FoldLeft`; `go_interop.SliceAppend`/`SliceAppendAll` only at a Go boundary |
| SliceOf import confusion | `import . "martianoff/gala/std"` expecting SliceOf | `SliceOf` is in `go_interop`, but prefer `ArrayOf`/`ListOf` from `collection_immutable` |
| Missing functional ops | Using `[]T` then writing manual Map/Filter loops | Switch to `Array[T]` or `List[T]` which have `.Map()`, `.Filter()`, `.FoldLeft()` |
| Manual loop on variadic args | `for i := 0; i < len(args); i++` on variadic `[]T` | Convert with `ArrayOf(args...)` then use functional methods |
| Variadic accumulation loop | `var acc; for { acc = f(acc, args[i]) }` on variadic | `ArrayOf(args...).FoldLeft(init, f)` or `.FoldRight(init, f)` |
| Internal function returning `[]T` | `func groupBy(xs Array[T]) []Group` — GALA-private helper returning Go slice | Return `Array[Group]` / `List[Group]` so the caller keeps functional ops |
| Internal function taking `[]T` | Non-interop helper with `[]T` param | Take `Array[T]` / `List[T]` and let the Go boundary do the conversion |
| Local `var xs []T` inspected in GALA code | `var xs []T; ...; for _, x := range xs` / `xs[i]` / `len(xs)` with no Go call consuming `xs` | Rebuild with `Array[T]` / `List[T]`; use `.Size()` / `.Get(i)` / `.ForEach` |
| Scratch buffer used beyond the Go read | `val dst = SliceWithSize[byte](n); lr.Read(dst); for _, b := range dst { ... }` | Read into a scratch buffer, then `ArrayFromSlice(dst[:n])` before further processing |

**Check**: Search for `SliceOf`, `SliceEmpty`, `SliceWithCapacity`, `SliceWithSize`, `[]T` declarations, `append(` calls, and manual loops over variadic parameters. Apply the §4.0 boundary heuristics. Only flag when the slice is observed internally (index, range, len, returned from a GALA-internal function) rather than exclusively at a Go call site.

**Acceptable Go slice uses**:
- Passing to Go standard library functions (`strings.Join`, `sort.Slice`, etc.)
- Scratch buffers for Go I/O: `SliceWithSize[byte](n)` immediately passed to `.Read` / `.Write` / `.Scan` (and not inspected afterward — see §4.0 exception)
- Simple pass-through variadic forwarding (`other(args...)`)
- Interop with Go libraries that expect `[]T`
- Converting at boundaries: `collection.ToGoSlice()` when needed

#### 4b. Go Maps → GALA HashMap/TreeMap

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| Go map for general use | `val m = map[string]int{}` or `make(map[K]V)` for internal logic | `EmptyHashMap[string, int]()` from `collection_immutable` |
| Go map literal | `map[K]V{"a": 1, "b": 2}` for application data | Build with `EmptyHashMap[K,V]().Put("a", 1).Put("b", 2)` |
| Manual map iteration | `for k, v := range m { ... }` for transform/filter | Use `HashMap.Map()`, `.Filter()`, `.ForEach()`, `.FoldLeft()` |
| Go map in struct field | `type Cache struct { data map[string]Entry }` | Use `HashMap[string, Entry]` for immutability + functional ops |
| Manual map lookup with default | `val v, ok = m[key]; if !ok { v = defaultVal }` | `hashMap.GetOrElse(key, defaultVal)` |
| Manual map existence check | `val _, ok = m[key]; if ok { ... }` | `hashMap.Contains(key)` or `hashMap.Get(key) match { case Some(v) => ... }` |
| MapForEach from go_interop | `MapForEach(goMap, func)` for internal processing | Convert to HashMap first: `HashMapFromGoMap(goMap).ForEach(...)` |
| Mutable map accumulation | `var m = map[K]V{}; for { m[k] = v }` | Use `FoldLeft` to build HashMap, or use `collection_mutable.HashMap` |
| Internal function returning `map[K]V` | `func countBy(xs Array[T]) map[K]int` — GALA-private helper returning Go map | Return `HashMap[K, int]` so the caller keeps functional ops |
| Internal function taking `map[K]V` | Non-interop helper with `map[K]V` param | Take `HashMap[K, V]` and convert at the Go boundary |
| Index-by-build-up pattern | `var m = map[K]V{}; xs.ForEach((x) => m[f(x)] = g(x))` | `xs.GroupBy(f).Map((k, vs) => (k, vs.Map(g).Head()))` or `HashMap` builder |

**Check**: Apply the §4.0 boundary heuristics. Search for `map[`, `make(map`, `MapEmpty`, `MapForEach`, `MapPut` from go_interop. Flag when the map is observed internally (range-loop, indexed assignment, passed to another GALA function) rather than exclusively at a Go call site.

**Acceptable Go map uses**:
- Passing to Go standard library or third-party functions expecting `map[K]V`
- Receiving from Go API calls (convert to HashMap at the boundary)
- Simple key lookups at Go interop boundaries
- JSON/YAML marshal/unmarshal targets
- Struct tags / reflect-driven code where `map[string]any` is unavoidable

**Good pattern** — GALA HashMap:
```gala
import . "martianoff/gala/collection_immutable"

val config = EmptyHashMap[string, string]()
    .Put("host", "localhost")
    .Put("port", "8080")

val upper = config.Map((k, v) => (k, strings.ToUpper(v)))
val port = config.Get("port").GetOrElse("3000")
```

**Bad pattern** — Go map for internal logic:
```gala
// BAD: Go map with manual iteration
var config = map[string]string{}
config["host"] = "localhost"
config["port"] = "8080"
for k, v := range config {
    Println(s"$k=$v")
}
```

#### 4c. Go Channels → GALA Future/Promise

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| Channel for single async result | `ch := make(chan T, 1); go func() { ch <- f() }(); <-ch` | `Future[T](() => f())` then `.Await()` or `.Map()` |
| Channel for timeout | `select { case r := <-ch: ... case <-time.After(d): ... }` | `future.WithTimeout(d)` or `FirstCompletedOf` |
| Channel for fan-out | Spawning goroutines writing to shared channel | `Future.Sequence(futures)` or `Future.Traverse(items, f)` |
| WaitGroup for completion | `sync.WaitGroup` + goroutines | `Future.Sequence(ArrayOf(futures...))` |
| Mutex for shared state | `sync.Mutex` protecting shared map/counter | Use immutable data + `Future` composition, or `Promise[T]` |

**Check**: Search for `make(chan`, `go func()`, `sync.WaitGroup`, `sync.Mutex`, `select {` in non-interop code. These often indicate Go concurrency patterns that GALA's Future/Promise can express more safely.

**Acceptable Go concurrency uses**:
- Long-running goroutines (servers, workers) that don't fit the Future model
- Streaming data (channels as iterators) — consider `Stream[T]` instead
- Interfacing with Go libraries that use channels
- Performance-critical code where Future overhead matters

**Variadic args pattern** — convert to Array for functional processing:
```gala
// BAD: manual loop over variadic Go slice
func applyAll(handler Handler, filters ...Filter) Handler {
    var h = handler
    for i := filters.Size() - 1; i >= 0; i-- {
        val f = filters[i]
        val inner = h
        h = (req) => f(req, inner)
    }
    return h
}

// GOOD: convert variadic to Array, use FoldRight
func applyAll(handler Handler, filters ...Filter) Handler =
    ArrayOf(filters...).FoldRight(handler, (f, inner) => (req) => f(req, inner))
```

**Good pattern** - GALA collections:
```gala
import . "martianoff/gala/collection_immutable"

val nums = ArrayOf(1, 2, 3, 4, 5)
val evenDoubled = nums.Collect({ case n if n % 2 == 0 => n * 2 })
val sum = nums.FoldLeft(0, (acc, x) => acc + x)
```

**Bad pattern** - Go slices with manual loops:
```gala
import . "martianoff/gala/go_interop"

val nums = SliceOf(1, 2, 3, 4, 5)
var evens = SliceEmpty[int]()
for _, x := range nums {
    if x % 2 == 0 {
        evens = append(evens, x)
    }
}
```

### 5. String Interpolation (HIGH priority)

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| `fmt.Sprintf` for simple formatting | `fmt.Sprintf("Hello %s", name)` | `s"Hello $name"` |
| `fmt.Sprintf` with expressions | `fmt.Sprintf("sum=%d", a + b)` | `s"sum=${a + b}"` |
| `fmt.Println` | `fmt.Println("result:", x)` | `Println("result:", x)` |
| `fmt.Print` | `fmt.Print("hello")` | `Print("hello")` |
| `fmt.Sprintf` with explicit format | `fmt.Sprintf("%.2f", price)` | `f"$price%.2f"` |
| Unnecessary `import "fmt"` | `import "fmt"` used only for Println/Sprintf | Remove import, use `s"..."` / `Println` |
| String concatenation for formatting | `"Hello " + name + ", age " + fmt.Sprintf("%d", age)` | `s"Hello $name, age $age"` |

**Check**: Search for `fmt.Sprintf`, `fmt.Println`, `fmt.Print`, `fmt.Printf`, and `import "fmt"`. For each:
- `fmt.Sprintf` → replace with `s"..."` (auto-inferred verbs) or `f"..."` (explicit format specs)
- `fmt.Println` / `fmt.Print` → replace with `Println` / `Print`
- `import "fmt"` → remove if only used for Sprintf/Println/Print (keep for `fmt.Errorf`, `fmt.Fprintf`, etc.)

**Acceptable `fmt` uses**: `fmt.Errorf`, `fmt.Fprintf`, `fmt.Fscan*`, `fmt.Stringer` interface implementation, and any `fmt` function not covered by interpolation.

### 5b. Multi-Line Formatting (MEDIUM priority)

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| Long parameter list on single line | `func f(a string, b string, c string, d string) T` (>100 chars) | Use multi-line with trailing commas |
| Long struct declaration on single line | `struct Foo(a string, b string, c string, d string)` (>100 chars) | Use multi-line with trailing commas |
| Missing trailing comma in multi-line | Multi-line params without trailing comma | Add trailing comma for consistency |

**Check**: Search for `func` declarations and `struct` shorthand declarations with lines exceeding ~100 characters. These benefit from multi-line formatting with trailing commas.

**Good pattern** — multi-line with trailing commas:
```gala
func createServer(
    host string,
    port int = 8080,
    tls bool = true,
    maxConnections int = 100,
) Server = ...

struct Cookie(
    Name string,
    Value string,
    Path string,
    MaxAge int,
)
```

**Bad pattern** — long single line:
```gala
func createServer(host string, port int = 8080, tls bool = true, maxConnections int = 100) Server = ...
```

### 6. Default Parameters and Named Arguments (MEDIUM priority)

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| Options pattern instead of defaults | Struct with `WithPort`, `WithTimeout` option functions | Use default parameter values: `func connect(host string, port int = 8080)` |
| Multiple wrapper functions | `func NewFoo()`, `func NewFooWithBar()`, `func NewFooWithBarAndBaz()` | Single function with defaults: `func NewFoo(bar int = 0, baz string = "")` |
| Boolean flag parameter without name | `connect("localhost", 8080, true)` where `true` is ambiguous | Use named arg: `connect("localhost", tls = true)` |
| Positional args reducing readability | `createUser("Alice", 30, "admin", true, false)` | Use named args for clarity: `createUser(name = "Alice", age = 30, role = "admin")` |
| Default after required without default | `func f(a int = 1, b int, c string = "x")` | Defaults must be contiguous at end: `func f(b int, a int = 1, c string = "x")` |

**Check**: Search for the functional options pattern (structs named `*Option` or `*Config` with `With*` functions), multiple function overloads with incremental parameters, and call sites with 3+ positional boolean/int literal arguments where named args would clarify intent.

**Acceptable patterns**: Go interop functions that must match Go signatures cannot use defaults.

### 7. Type Inference (MEDIUM priority)
<!-- Note: sections below renumbered after inserting rule 6 -->

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| Redundant variable type | `val x int = 42` | `val x = 42` |
| Redundant generic params on helpers | `Some[int](42)` | `Some(42)` |
| Redundant type arg on `None[T]()` in inferable context | `func parse() Option[int] = None[int]()` | `func parse() Option[int] = None()` (type pinned by the return type) |
| Redundant collection types | `ListOf[int](1, 2, 3)` | `ListOf(1, 2, 3)` |
| Redundant single-param struct constructor | `Box[int](Value = 42)` | `Box(Value = 42)` (type inferred from named field value) |
| Redundant generic function call type params | `Try[int](() => { return 1 })` | `Try(() => { return 1 })` (type inferred from lambda return) |
| Unnecessary lambda for zero-arg func | `Try(() => os.TempDir())` | `Try(os.TempDir)` — pass function reference directly when func takes no args; for any other single-expression body drop `() =>` (see rule 11e) |
| Redundant generic function call type params | `NewCons[int](head, tail)` | `NewCons(head, tail)` (type inferred from arguments) |
| Redundant lambda param type | `list.Map((x int) => x * 2)` | `list.Map((x) => x * 2)` (type inferred from method signature) |
| Redundant method type param | `list.Map[int]((x) => x * 2)` | `list.Map((x) => x * 2)` (Go infers from lambda) |
| Redundant FoldLeft type param | `list.FoldLeft[int](0, (acc int, x int) => acc + x)` | `list.FoldLeft(0, (acc, x) => acc + x)` (accumulator type inferred from zero value) |
| Redundant wrapper method lambda types | `str.Filter((r rune) => r == 'a')` | `str.Filter((r) => r == 'a')` (type inferred from non-generic method signature) |

**Check**: Search for the pattern `Name[ConcreteTypes](args)` — any call where `[...]` contains concrete types (not type parameter declarations like `[T any]`) and the arguments already provide enough information for Go to infer the type parameters. This includes:
- **Single-type-param generic struct constructors**: `Box[int](Value = 42)` → `Box(Value = 42)` — Go infers the single type param from the named field value
- **Single-type-param generic function calls**: `Try[int](f)`, `NewCons[int](head, tail)` — argument types determine the type param
- **Helper constructors**: `Some[int](42)`, `ListOf[int](1, 2, 3)` — element values determine type params
- **Zero-arg sealed-variant constructors when the context pins a CONCRETE type**: `None[int]()` (and other zero-field cases of a generic sealed type) when the surrounding context supplies a **concrete** type argument. A zero-field variant has no argument to infer from, but the transpiler propagates an **expected type** downward and injects the type arg, so the explicit `[…]` is redundant. Flag `None[Concrete]()` → `None()` when it appears as any of:
  - a function body or `return` whose declared return type is a concrete `Option[Concrete]` — `func parse() Option[int] = None[int]()` → `None()`
  - the RHS of a `val`/`var` with an explicit concrete `Option[Concrete]` type — `val x Option[string] = None[string]()` → `None()`
  - an argument to a function/constructor whose parameter type is a concrete `Option[Concrete]`
  - an element of a typed container — `ArrayOf[Option[int]](None[int]())` → `ArrayOf[Option[int]](None())`
  - a `match` arm whose result type is externally pinned to a concrete `Option[Concrete]` (e.g. the match is the body of a function returning `Option[int]`)
  - an `if`/`else` branch whose sibling branch is a `Some(...)` of known concrete type — `if (c) Some(x) else None[int]()`

  Do NOT flag (explicit typing is still REQUIRED) when:
  - the pinning type is an **abstract type parameter** of the enclosing generic function/method, e.g. `func (a Array[T]) HeadOption() Option[T] = … None[T]()` or `func (o Option[T]) OrElse(...) = … None[T]()`. The transpiler cannot infer an unresolved `T` from context and rejects bare `None()` with `GALA-E0018`. Keep `None[T]()`. (This is the common case inside the collection/std library — do not flag those.)
  - no context pins the type at all — most commonly a bare `None[int]()` inside a lambda whose result type is unconstrained, e.g. `arr.Map((x) => None[int]())`: removing `[int]` makes the type undeterminable (`GALA-E0018`). Keep it.

  Rule of thumb: only flag when the type argument you would remove is a **concrete** type (`int`, `string`, `Array[JField]`, …), never when it is an in-scope abstract type parameter.

**Exception**: Explicit types ARE required for:
- `Left[L, R]()`, `Right[L, R]()` — no value arguments to infer from (and Either carries two params)
- Empty collections: `EmptyArray[T]()`, `EmptyList[T]()`, `EmptyHashMap[K, V]()`, `Empty[T]()` — no elements to infer from
- **Multi-type-param generic struct constructors**: `Pair[string, int]("a", 1)` — Go cannot infer when there are 2+ type params on struct instantiation
- **Generic struct constructors inside generic method bodies**: `Pair[C, B](First = ...)` — abstract type params from enclosing method must be explicit
- **Multi-type-param generic functions** like `Unfold[A, S](seed, f)` — Go often cannot infer all params when multiple are involved
- Standalone lambdas not passed to a typed method (e.g., `val f = (x int) => x * 2`)
- Ambiguous cases where removing the type param would cause a compile error

### 7. Functional Patterns (HIGH priority)

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| Manual accumulation | `var acc; for { acc = f(acc, x) }` | `list.FoldLeft(init, (acc, x) => f(acc, x))` |
| Manual filter | `for { if cond { append } }` | `list.Filter(predicate)` |
| Manual map | `for { result = append(result, f(x)) }` | `list.Map(f)` |
| Filter then Map | `list.Filter(p).Map(f)` | `list.Collect({ case x if p(x) => f(x) })` |
| Map then Filter (flatMap pattern) | `list.Map(f).Filter(p)` when f returns Option-like | `list.Collect({ case x if p(x) => f(x) })` |
| FlatMap + Option for filter+transform | `list.FlatMap((x) => if p(x) { Some(f(x)) } else { None })` | `list.Collect({ case x if p(x) => f(x) })` |
| Index-based iteration | `for i := 0; i < x.Length(); i++` | `x.ForEach(f)` or `for _, elem := range x` |
| Option side effects | `if opt.IsDefined() { f(opt.Get()) }` | `opt.ForEach(f)` |
| Reimplementing collection methods | Defining `Map`, `Filter`, `Fold` etc. with manual loops when wrapping a collection | Delegate to underlying collection's method |
| Manual ForAll pattern | `for { if !p(x) { return false } }; return true` | `collection.ForAll(p)` |
| Manual Exists pattern | `for { if p(x) { return true } }; return false` | `collection.Exists(p)` |
| Manual Find pattern | `for { if p(x) { return Some(x) } }; return None` | `collection.Find(p)` |
| Manual Reverse | `for i := len-1; i >= 0; i-- { append }` | `collection.Reverse()` |
| Manual ZipWithIndex | `for i := 0; ...; result.Append((elem, i))` | `collection.ZipWithIndex()` |
| Manual IndexOf | `for i := 0; ...; if elem == target { return i }` | `collection.IndexOfFirst(x => x == target)` |

**Check**: Also search for `.Filter(` followed by `.Map(` on the same collection (chained or via intermediate val). These should use `.Collect` with a partial function instead.

**Bad pattern** — Filter + Map chain:
```gala
// BAD: Two passes over the collection
val evenDoubled = numbers.Filter((x) => x % 2 == 0).Map((x) => x * 2)

// BAD: Filter + Map with intermediate val
val adults = people.Filter((p) => p.Age >= 18)
val names = adults.Map((p) => p.Name)

// BAD: FlatMap with Option for conditional transform
val results = items.FlatMap((x) => if x.IsValid() { Some(x.Transform()) } else { None[Output]() })
```

**Good pattern** — Collect with partial function:
```gala
// GOOD: Single pass, filter and transform together
val evenDoubled = numbers.Collect({ case n if n % 2 == 0 => n * 2 })

// GOOD: Collect with extractor
val names = people.Collect({ case p if p.Age >= 18 => p.Name })

// GOOD: Collect replaces FlatMap + Option
val results = items.Collect({ case x if x.IsValid() => x.Transform() })

// GOOD: Collect with sealed type extractor
val values = options.Collect({ case Some(v) => v * 2 })
```

### 8. Option.When and Conditional Construction (HIGH priority)

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| If-else for conditional Option | `if (cond) Some(v) else None[T]()` | `When(cond, v)` |
| Negated condition for Option | `if (!cond) Some(v) else None[T]()` | `Unless(cond, v)` |
| Custom helper for conditional Option | `func optionFromBool[T](cond bool, v T) Option[T]` | Remove custom helper, use `When(cond, v)` from std |
| Nil map lookup boilerplate | `val v = m[key]; if v == nil { None() } else { Some(v) }` | `OptionFromMap(m, key)` from go_interop |
| Double nil check on Go map | `if m == nil { return empty }; val v = m[k]; if v == nil { ... }` | `OptionFromMap(m, k)` handles both nil map and missing key |
| Range over possibly-nil slice | `if s != nil { for _, x := range s }` | `for _, x := range SliceFromNil(s)` from go_interop |

**Check**: Search for `if.*Some.*else.*None` patterns, custom `optionFrom*` helper functions, double nil-check patterns on Go maps, and nil guards before range loops.

**Good patterns**:
```gala
import . "martianoff/gala/std"
import . "martianoff/gala/go_interop"

// Conditional Option creation
val name = When(s != "", s)
val deadline = When(ok, t)
val fallback = Unless(isDisabled, defaultValue)

// Nil-safe Go map lookup
val params = OptionFromMap(queryMap, "page")
    .Map((v) => strconv.Atoi(v))
    .GetOrElse(1)

// Nil-safe Go slice iteration
for _, item := range SliceFromNil(maybeNilSlice) {
    process(item)
}
```

**Bad patterns**:
```gala
// BAD: verbose conditional Option
val name = if (s != "") Some(s) else None[string]()

// BAD: double nil check
val qp = r.bridge.AllQueryParams()
if qp == nil { return EmptyArray[string]() }
val values = qp[name]
if values == nil { return EmptyArray[string]() }
```

### 8b. Go Struct Construction (MEDIUM priority)

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| Go colon syntax for bridge structs | `pkg.GoStruct{Field: value}` | `pkg.GoStruct(Field = value)` — GALA named-arg syntax |
| Mixed construction styles | GALA structs use `=`, Go structs use `:` in same file | Use `=` syntax consistently for both |

**Check**: Search for composite literal syntax (`Type{Field: value}`) on Go-imported types. If the type comes from a non-GALA import, suggest GALA named-arg syntax.

**Acceptable Go literal uses**: When constructing Go types that require specific field ordering or embedding, or when the composite literal includes non-named elements.

**Good pattern**:
```gala
// Consistent GALA syntax for both GALA and Go structs
val cookie = Cookie(Name = "session", Value = "abc")
val goCookie = http.Cookie(Name = "session", Value = "abc", Path = "/")
```

**Bad pattern**:
```gala
// Inconsistent: GALA uses =, Go uses :
val cookie = Cookie(Name = "session", Value = "abc")
val goCookie = http.Cookie{Name: "session", Value: "abc", Path: "/"}
```

### 8c. Retry Patterns (HIGH priority)

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| Imperative retry loop | `var attempts = 0; for attempts < max { ... attempts++ }` | `Retry(max, backoff, action)` from concurrent |
| Manual backoff calculation | `var delay = initial; delay = delay * 2; if delay > max { delay = max }` | `ExponentialBackoff(initial, max)` |
| Manual constant delay | `time.Sleep(fixedDuration)` inside retry loop | `ConstantBackoff(fixedDuration)` |
| Retry without backoff | Retry loop with no sleep between attempts | `Retry(max, NoBackoff(), action)` |

**Check**: Search for `var attempts` or `var retries` paired with `for` loops and `time.Sleep`. These are retry patterns that should use the `Retry` combinator.

**Good pattern**:
```gala
import . "martianoff/gala/concurrent"

val result = Retry(3, ExponentialBackoff(100 * time.Millisecond, 5 * time.Second), (attempt) => {
    val resp = callService()
    if resp.Code() < 500 { return Success(resp) }
    return Failure[Response](fmt.Errorf("attempt %d: status %d", attempt, resp.Code()))
})
```

**Bad pattern**:
```gala
// BAD: imperative retry with manual backoff
var lastResp = InternalError("no attempts")
var attempts = 0
var backoff = 100 * time.Millisecond
for attempts < 3 {
    val resp = callService()
    lastResp = resp
    if resp.Code() < 500 { return resp }
    time.Sleep(backoff)
    backoff = backoff * 2
    attempts = attempts + 1
}
return lastResp
```

### 8d. Expression-Bodied Functions (MEDIUM priority)

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|----------------|
| Block body for single expr | `func f() T { return expr }` | `func f() T = expr` |
| Lambda block for single expr | `(x) => { return x * 2 }` | `(x) => x * 2` |
| Missing return in block | `(x) => { val y = x * 2; y }` | Add explicit `return y` |
| Multi-line when one-liner works | `if cond { return a } else { return b }` | Use if-expression: `if (cond) a else b` |

### 8e. If-Expressions (MEDIUM priority)

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|----------------|
| Mutable var + if-statement for val | `var x = a; if (cond) { x = b }` | `val x = if (cond) b else a` |
| If-statement with return in both branches | `if cond { return a } else { return b }` | `return if (cond) a else b` |
| Ternary workaround with var | `var result; if c { result = x } else { result = y }; use(result)` | `val result = if (c) x else y` |
| Block if-expression when one-liner works | `val x = if (c) { a } else { b }` where a, b are simple | `val x = if (c) a else b` |

**Check**: Search for `var` declarations immediately followed by `if/else` that assign different values. These should use if-expressions.

**Good patterns**:
```gala
// Simple if-expression
val status = if (score > 50) "pass" else "fail"

// Block branches for complex logic
val url = if (query != "") {
    val encoded = encode(query)
    s"$base?$encoded"
} else {
    base
}

// Mixed: one block, one expression
val label = if (count > 1) {
    val suffix = "s"
    s"$count item$suffix"
} else "1 item"
```

### 9. Unnecessary Variables (MEDIUM priority)

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| Single-use intermediate val | `val x = f(); g(x)` where x used once | `g(f())` inline directly |
| Verbose tuple creation | `Tuple[A, B](V1 = a, V2 = b)` | `(a, b)` |
| Chained val assignments | `val a = x; val b = f(a); val c = g(b); c` | `g(f(x))` or method chaining |
| Val for simple field access | `val x = obj.field; use(x)` once | `use(obj.field)` |
| Intermediate collection | `val arr = toX(); arr.Method()` | `toX().Method()` chain directly |

**Exception**: Keep intermediate vals for:
- Values used multiple times
- Complex expressions that benefit from naming for readability
- Debugging breakpoints

### 10. Collection Wrapper Types (HIGH priority)

When creating wrapper types around collections (like `Str` wrapping `string` as `Array[rune]`), delegate to the underlying collection's methods instead of reimplementing.

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| Reimplementing Map | `func (w Wrapper) Map(f) { for i := 0; ... }` | `toCollection(w).Map(f)` then rewrap |
| Reimplementing Filter | `func (w Wrapper) Filter(p) { for ... if p(x) { append } }` | `toCollection(w).Filter(p)` |
| Reimplementing Fold | `func (w Wrapper) Fold(z, f) { var acc = z; for ... }` | Delegate to `toCollection(w).FoldLeft(z, f)` |
| Reimplementing ForAll | `for { if !p(x) { return false } }; return true` | `toCollection(w).ForAll(p)` |
| Reimplementing Exists | `for { if p(x) { return true } }; return false` | `toCollection(w).Exists(p)` |
| Reimplementing Find | `for { if p(x) { return Some(x) } }; return None` | `toCollection(w).Find(p)` |
| Reimplementing Reverse | `for i := len-1; i >= 0; i--` | `toCollection(w).Reverse()` |
| Reimplementing ZipWithIndex | `for i := 0; ...; append((x, i))` | `toCollection(w).ZipWithIndex()` |
| Reimplementing ForEach | `for i := 0; ...; f(x)` | `toCollection(w).ForEach(f)` |

**Good pattern**:
```gala
// Str wraps string, converts to Array[rune] for operations
func (s Str) Map(f func(rune) rune) Str =
    Str(value = runesToString(toRunes(s.value).Map(f)))

func (s Str) Filter(p func(rune) bool) Str =
    Str(value = runesToString(toRunes(s.value).Filter(p)))

func (s Str) ForAll(p func(rune) bool) bool =
    s.NonEmpty() && toRunes(s.value).ForAll(p)

func (s Str) IsAlpha() bool = s.NonEmpty() && toRunes(s.value).ForAll(unicode.IsLetter)
```

### 11. Error Handling (HIGH priority)

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| Nullable without Option | `func f() *T` returning nil | `func f() Option[T]` |
| Bare `panic` for errors | `panic("error message")` — a hard error (`GALA-E0035`) | Return `Try[T]` or `Either[E, T]` |
| `go_builtins.Panic` to propagate an error | `val x, err = f(); if err != nil { go_builtins.Panic(err) }` (or the same inside a `Try` block) | Wrap the `(T, error)` call in `Try`: `Try(f(args))` returns `Try[T]` (the error becomes a `Failure`); use `.Get()` only if you truly want to re-raise |
| Ignored error | `result, _ := fallibleOp()` | Handle with `Try` or check error |
| Sentinel return value | Returning `""`, `0`, `-1`, or `nil` to signal "not found" / failure | Return `Option[T]` or `Try[T]` instead |
| Go-style if-err-nil | `val x, err = f(); if err == nil { use(x) }` | `Try(() => f())` then `.Map`, `.GetOrElse`, or `match` |
| Sequential if-err-nil fallback | Multiple `val x, err = f(); if err == nil { return x }` blocks trying alternatives | `Try(() => f1()).OrElse(Try(() => f2()))` chain |
| FlatMap vs OrElse confusion | Using `FlatMap` when fallback calls are independent | `OrElse` for independent fallbacks; `FlatMap` only when second call depends on first result |

**Check**: Search for patterns like `val x, err = ...; if err == nil` or `if err != nil`. Multiple such blocks in sequence (trying alternatives and returning the first success) should use `Try` + `OrElse`. Single error checks should use `Try` + `Map`/`GetOrElse`/`match`.

**Bad pattern** — Go-style sequential error fallback:
```gala
// BAD: imperative if-err-nil chain with sentinel return
func findBinary() string {
    val p, err = exec.LookPath("gala")
    if err == nil {
        return p
    }
    val p2, err2 = exec.LookPath("gala.exe")
    if err2 == nil {
        return p2
    }
    return ""
}
```

**Good pattern** — Try + OrElse with Option return:
```gala
// GOOD: functional fallback chain, no sentinel value
func findBinary() Option[string] =
    Try(() => exec.LookPath("gala"))
        .OrElse(Try(() => exec.LookPath("gala.exe")))
        .ToOption()
```

**Best pattern** — function reference when zero-arg:
```gala
// BEST: pass zero-arg function references directly (no lambda wrapper)
val dir = Try(os.TempDir)                // same as Try(() => os.TempDir())
val answer = Try(getAnswer)              // works with GALA functions too
```

**When to use FlatMap vs OrElse**:
```gala
// OrElse: independent fallbacks (second doesn't need first's result)
Try(() => lookupInCache(key)).OrElse(Try(() => lookupInDB(key)))

// FlatMap: dependent chain (second uses first's result)
Try(() => findConfig()).FlatMap((path) => Try(() => readFile(path)))
```

**`go_builtins.Panic(err)` to propagate an error is the same anti-pattern as bare
`panic` for errors.** A `(T, error)` call whose error you turn into a panic
should be a `Try` — `Try` wraps the call and turns the error into a `Failure`,
which the caller (or an enclosing `Try`) composes with. Reserve
`go_builtins.Panic` for a *genuinely intended* panic: an unrecoverable invariant
(a broken RNG), a programmer error (out-of-bounds, `Option.Get` on `None`), or a
recursive parser that panics out to the caller's surrounding `Try`.

**Bad pattern** — mechanical if-err-nil that panics the error:
```gala
// BAD: os.OpenFile's error is turned into a panic by hand
func openConfig(p string) *os.File {
    val f, err = os.OpenFile(p, os.O_RDONLY, 0)
    if err != nil {
        go_builtins.Panic(err)
    }
    return f
}

// BAD: same shape inside a Try block
func Abs(p string) Try[string] = Try[string](() => {
    val resolved, err = filepath.Abs(p)
    if err != nil { go_builtins.Panic(err) }
    return resolved
})
```

**Good pattern** — `Try` turns the Go error into a `Failure`:
```gala
// GOOD: return a Try; the caller decides how to handle the failure
func openConfig(p string) Try[*os.File] = Try(os.OpenFile(p, os.O_RDONLY, 0))

// GOOD: the whole manual Try-block + if-err-nil collapses
func Abs(p string) Try[string] = Try(filepath.Abs(p))

// GOOD: when the surrounding code genuinely must abort (unrecoverable),
// re-raise explicitly via Get — but that panic is now deliberate, not a
// mechanical if-err-nil:
val decoded = Try(hex.DecodeString(hardCodedVector)).Get()
```

### 11b. `Array.Grouped` / `Array.Sliding` over Index Loops (HIGH priority)

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| Index-stepping loop by 2 | `var i = 0; for i < x.Size() - 1 { use(x[i], x[i+1]); i += 2 }` | `ArrayOf(x...).Grouped(2).FoldLeft(...)` |
| Sliding window loop | Manual index loop with window | `ArrayOf(x...).Sliding(n)` |

### 11c. Go Struct Named-Arg Construction (HIGH priority)

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| Go struct colon literal | `GoType{Field: value}` | `GoType(Field = value)` — GALA named-arg syntax works for Go structs |

### 11d. `val _ =` is a code smell — use a bare statement (HIGH priority)

`val _ = <expr>` is **always** a smell. Whatever the right-hand side is, it should stand on its own as a statement.

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| Discard call result | `val _ = fs.WriteFileString(p, s, mode)` | `fs.WriteFileString(p, s, mode)` — bare call |
| Discard match result | `val _ = x match { ... }` | `x match { ... }` — bare match |
| Discard `error`-returning call | `val _ = file.Close()` | `file.Close()` — bare call (function-body), or `FromError(file.Close())` if inside a void lambda |
| ForEach when match fits | `opt.ForEach((v) => ...)` when multiple cases needed | `opt match { case Some(v) => ...; case None() => ... }` |

Rationale: `val _ = ...` adds noise without expressing any intent the bare expression doesn't already express. If the result genuinely matters, bind it to a real name and use it (e.g. assert `.IsSuccess()` in tests). If it doesn't, the call/match should stand alone.

**Void-lambda exception.** Inside a lambda whose body is `func()` (no return), the analyzer rejects bare `error`-returning calls — error: "cannot discard error return from X — use FromError(X) to handle the error". Use `FromError(call())` from `std`: it returns `Try[Void]` and can itself stand as a bare statement (or chain `.OnFailure((err) => ...)`). Function-body bare calls are unaffected.

**If bare-statement form does not transpile elsewhere** — that is a **transpiler bug**, not a license to keep `val _ =`. Open a repro test against the transpiler and fix the bug. Per CLAUDE.md rule 6, never work around transpiler bugs.

### 11e. By-Name Argument Sugar for Zero-Arg Thunks (MEDIUM priority)

When a parameter's expected type is a **zero-arg** function type (`func() T`, or
void `func()`), a bare expression can be passed instead of an explicit `() => expr`
lambda — the transpiler lifts it into a thunk automatically (`f(expr)` means
`f(() => expr)`). Prefer the bare-expression form; it strips ceremony from the
most common `Try` and `Future` call sites while preserving the lazy, panic/error-
catching semantics.

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| Zero-arg lambda wrapping a single expression | `Try(() => strconv.Atoi(s))` | `Try(strconv.Atoi(s))` |
| Zero-arg lambda for a Future body | `Future(() => compute())` / `Future[int](() => compute())` | `Future(compute())` / `Future[int](compute())` |
| Zero-arg lambda over a Go `(T, error)` call | `Try(() => os.ReadFile(p))` | `Try(os.ReadFile(p))` — the error is still caught as `Failure` |
| Zero-arg lambda in any thunk param | `FutureOn(() => compute(), pool)` | `FutureOn(compute(), pool)` |

**Preferred forms, most concise first:**
1. **Bare function reference** — when the lambda body is *exactly* a call to a
   zero-arg function with no other arguments, pass the reference (see rules 2 and
   11): `Try(() => os.TempDir())` → `Try(os.TempDir)`.
2. **Bare expression (by-name sugar)** — for every other single-expression body,
   drop the `() =>`: `Try(() => f(x))` → `Try(f(x))`, `Future(() => a * b)` →
   `Future(a * b)`.

**Check**: Grep for `(() => ` — an open paren immediately followed by a zero-arg
lambda (empty parameter list). When the lambda body is a **single expression**
(the char after `=>` is not `{`), flag it and remove the `() => `. Applies to
`Try`, `Future`, `FutureOn`, and any call whose argument is a zero-parameter
lambda.

**Do NOT flag** (keep the explicit lambda):
- **Block bodies** — `Future(() => { setup(); compute() })` cannot be desugared;
  only single-expression lambdas convert.
- **Lambdas with parameters** — `xs.Map((x) => x * 2)` is a `func(T) U`, not a
  zero-arg thunk. This rule is strictly about `() =>` (empty parameter list).
- **A body that is itself a function value** — e.g. `schedule(() => makeHandler())`
  where `makeHandler()` returns a `func()`. Dropping `() =>` would pass the inner
  function through directly (the sugar never re-wraps an existing function value),
  changing meaning. Keep the lambda when the body's own type is a zero-arg function.

**Good pattern**:
```gala
val parsed = Try(strconv.Atoi(input))   // was Try(() => strconv.Atoi(input))
val result = Try(riskyDivide(10, 2))    // was Try(() => riskyDivide(10, 2))
val async  = Future(loadFromDB(id))     // was Future(() => loadFromDB(id))
val onPool = FutureOn(compute(), pool)  // was FutureOn(() => compute(), pool)
```

**Bad pattern** — redundant zero-arg lambda wrapper:
```gala
val parsed = Try(() => strconv.Atoi(input))
val async  = Future(() => loadFromDB(id))
```

### 12. Naming Conventions (LOW priority)

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| Non-PascalCase type | `type myType struct` | `type MyType struct` |
| Non-PascalCase export | `func myPublicFunc()` | `func MyPublicFunc()` |
| Verbose lambda params | `(element int) =>` in simple collection ops | `(x) =>` -- prefer short names with implicit types |

### 13. Import Style (LOW priority)

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| Missing blank line after imports | `import (...)\nfunc` | Add blank line after import block |
| Unused import | Import not referenced | Remove unused import |

---

## Directories to Skip

- `bazel-*` (build outputs)
- `node_modules`
- `vendor`
- Any generated files (check for `// Code generated` comment)

## Severity Levels

- **HIGH**: Violations of core GALA principles (immutability, pattern matching, functional patterns, sealed types, collection delegation, error handling with Try/Option)
- **MEDIUM**: Missed opportunities for type inference, unnecessary variables, expression-bodied functions
- **LOW**: Style and naming conventions

---

## Output Format

Generate a report in this format:

```markdown
# GALA Lint Report

**Files scanned**: X
**Issues found**: Y

## Summary

| Severity | Count |
|----------|-------|
| HIGH     | X     |
| MEDIUM   | Y     |
| LOW      | Z     |

## Issues by File

### `path/to/file.gala`

| Line | Severity | Rule | Issue | Suggestion |
|------|----------|------|-------|------------|
| 15   | HIGH     | Immutability | `var x = 10` never reassigned | Use `val x = 10` |
| 42   | MEDIUM   | Pattern Matching | If-else chain on Option | Use `match` expression |

### `path/to/another.gala`

...

## Files with No Issues

- `path/to/clean.gala`
- `path/to/good.gala`
```

## After Linting

1. Present the report to the user
2. Ask if they want to auto-fix any issues
3. For auto-fixable issues, offer to apply corrections
