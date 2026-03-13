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
| If-err-nil on Go error return | `val x, err = f(); if err == nil { ... }` | Wrap with `Try(() => f())` then use `.Map`, `.GetOrElse`, or `match` |
| Sequential if-err-nil fallback | Multiple `val x, err = f(); if err == nil { return x }` in sequence | `Try(() => f1()).OrElse(Try(() => f2()))` chain |

### 3. Sealed Types (HIGH priority)

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| Manual discriminator | Struct with `kind int` or `_variant uint8` field + iota constants + switch/if-else on kind | Use `sealed type` declaration |
| Interface for closed variants | Interface with a small, fixed set of implementations that are all in the same package | Use `sealed type` instead |
| Iota enum without data | `const ( Red = iota; Green; Blue )` for a fixed set of values | `sealed type Color { case Red() case Green() case Blue() }` |
| Iota enum with data | Iota constants + separate struct fields per variant | `sealed type` with fields on each case |
| Struct union pattern | A struct with fields for multiple variants where only some are used at a time | `sealed type` with variant-specific fields |

**Check**: Search for `iota` declarations that represent a fixed set of categories/kinds/variants. Search for struct fields named `kind`, `type`, `tag`, `variant` of integer type paired with constants.

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

### 4. Go Slices vs GALA Collections (HIGH priority)

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| SliceOf for general use | `val items = SliceOf(1, 2, 3)` when not passing to Go API | `val items = ArrayOf(1, 2, 3)` or `ListOf(1, 2, 3)` |
| SliceEmpty for general use | `val items = SliceEmpty[int]()` | `val items = EmptyArray[int]()` or `EmptyList[int]()` |
| Go slice type in struct | `type Foo struct { Items []int }` when functional ops needed | Use `Array[int]` or `List[int]` |
| Manual loop on slice | `for i := 0; i < len(slice); i++ { ... }` | Use GALA collection with `.ForEach`, `.Map`, `.Filter` |
| append on slice | `result = append(result, item)` in accumulation loop | Use `Array.Append()` or `List.Prepend()`, or `FoldLeft` |
| SliceOf import confusion | `import . "martianoff/gala/std"` expecting SliceOf | `SliceOf` is in `go_interop`, but prefer `ArrayOf`/`ListOf` from `collection_immutable` |
| Missing functional ops | Using `[]T` then writing manual Map/Filter loops | Switch to `Array[T]` or `List[T]` which have `.Map()`, `.Filter()`, `.FoldLeft()` |
| Manual loop on variadic args | `for i := 0; i < len(args); i++` on variadic `[]T` | Convert with `ArrayOf(args...)` then use functional methods |
| Variadic accumulation loop | `var acc; for { acc = f(acc, args[i]) }` on variadic | `ArrayOf(args...).FoldLeft(init, f)` or `.FoldRight(init, f)` |

**Check**: Search for `SliceOf`, `SliceEmpty`, `SliceWithCapacity`, `[]T` declarations, `append(` calls, and manual loops over variadic parameters. For each, determine if the slice is passed to a Go API or used internally. Internal use should prefer GALA collections.

**Acceptable Go slice uses**:
- Passing to Go standard library functions (`strings.Join`, `sort.Slice`, etc.)
- Simple pass-through variadic forwarding (`other(args...)`)
- Interop with Go libraries that expect `[]T`
- Converting at boundaries: `collection.ToGoSlice()` when needed

**Variadic args pattern** — convert to Array for functional processing:
```gala
// BAD: manual loop over variadic Go slice
func applyAll(handler Handler, filters ...Filter) Handler {
    var h = handler
    for i := len(filters) - 1; i >= 0; i-- {
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
val evens = nums.Filter((x) => x % 2 == 0)
val doubled = evens.Map((x) => x * 2)
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

### 6. Type Inference (MEDIUM priority)

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|-----------------|
| Redundant variable type | `val x int = 42` | `val x = 42` |
| Redundant generic params on helpers | `Some[int](42)` | `Some(42)` |
| Redundant collection types | `ListOf[int](1, 2, 3)` | `ListOf(1, 2, 3)` |
| Redundant single-param struct constructor | `Box[int](Value = 42)` | `Box(Value = 42)` (type inferred from named field value) |
| Redundant generic function call type params | `Try[int](() => { return 1 })` | `Try(() => { return 1 })` (type inferred from lambda return) |
| Redundant generic function call type params | `NewCons[int](head, tail)` | `NewCons(head, tail)` (type inferred from arguments) |
| Redundant lambda param type | `list.Map((x int) => x * 2)` | `list.Map((x) => x * 2)` (type inferred from method signature) |
| Redundant method type param | `list.Map[int]((x) => x * 2)` | `list.Map((x) => x * 2)` (Go infers from lambda) |
| Redundant FoldLeft type param | `list.FoldLeft[int](0, (acc int, x int) => acc + x)` | `list.FoldLeft(0, (acc, x) => acc + x)` (accumulator type inferred from zero value) |
| Redundant wrapper method lambda types | `str.Filter((r rune) => r == 'a')` | `str.Filter((r) => r == 'a')` (type inferred from non-generic method signature) |

**Check**: Search for the pattern `Name[ConcreteTypes](args)` — any call where `[...]` contains concrete types (not type parameter declarations like `[T any]`) and the arguments already provide enough information for Go to infer the type parameters. This includes:
- **Single-type-param generic struct constructors**: `Box[int](Value = 42)` → `Box(Value = 42)` — Go infers the single type param from the named field value
- **Single-type-param generic function calls**: `Try[int](f)`, `NewCons[int](head, tail)` — argument types determine the type param
- **Helper constructors**: `Some[int](42)`, `ListOf[int](1, 2, 3)` — element values determine type params

**Exception**: Explicit types ARE required for:
- `None[T]()`, `Left[L, R]()`, `Right[L, R]()` — no value arguments to infer from
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
| Index-based iteration | `for i := 0; i < x.Length(); i++` | `x.ForEach(f)` or `for _, elem := range x` |
| Option side effects | `if opt.IsDefined() { f(opt.Get()) }` | `opt.ForEach(f)` |
| Reimplementing collection methods | Defining `Map`, `Filter`, `Fold` etc. with manual loops when wrapping a collection | Delegate to underlying collection's method |
| Manual ForAll pattern | `for { if !p(x) { return false } }; return true` | `collection.ForAll(p)` |
| Manual Exists pattern | `for { if p(x) { return true } }; return false` | `collection.Exists(p)` |
| Manual Find pattern | `for { if p(x) { return Some(x) } }; return None` | `collection.Find(p)` |
| Manual Reverse | `for i := len-1; i >= 0; i-- { append }` | `collection.Reverse()` |
| Manual ZipWithIndex | `for i := 0; ...; result.Append((elem, i))` | `collection.ZipWithIndex()` |
| Manual IndexOf | `for i := 0; ...; if elem == target { return i }` | `collection.IndexOfFirst(x => x == target)` |

### 8. Expression-Bodied Functions (MEDIUM priority)

| Issue | Pattern to Flag | Recommended Fix |
|-------|-----------------|----------------|
| Block body for single expr | `func f() T { return expr }` | `func f() T = expr` |
| Lambda block for single expr | `(x) => { return x * 2 }` | `(x) => x * 2` |
| Missing return in block | `(x) => { val y = x * 2; y }` | Add explicit `return y` |
| Multi-line when one-liner works | `if cond { return a } else { return b }` | Use ternary or match |

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
| Panic for errors | `panic("error message")` | Return `Try[T]` or `Either[E, T]` |
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

**When to use FlatMap vs OrElse**:
```gala
// OrElse: independent fallbacks (second doesn't need first's result)
Try(() => lookupInCache(key)).OrElse(Try(() => lookupInDB(key)))

// FlatMap: dependent chain (second uses first's result)
Try(() => findConfig()).FlatMap((path) => Try(() => readFile(path)))
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
