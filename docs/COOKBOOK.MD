# GALA Cookbook

Short recipes for the things a Go developer does on day one: files, flags, HTTP, JSON, environment variables, errors, collections, concurrency, tests and Go libraries. Each recipe states the problem, gives a complete program, and points out what to notice.

Every listing on this page is quoted from [`examples/cookbook`](../examples/cookbook). Bazel builds and runs each program, `bazel test //examples/cookbook/...` checks what it prints against the `.out` file beside it, and a doc test fails when a listing stops matching its file. To run one yourself, copy it into a directory with a `gala.mod` (`gala mod init example.com/demo`) and run `gala run .` (for the test recipe, `gala test ./...`).

New to GALA from Go? [From Go to GALA](PORTING_FROM_GO.MD) maps every Go construct to its GALA form, and [Best Practices](GALA_BEST_PRACTICES.MD) is the rule list.

## Contents

- [Read, write and count a file](#read-write-and-count-a-file)
- [Parse command-line flags](#parse-command-line-flags)
- [Encode and decode JSON](#encode-and-decode-json)
- [Serve JSON over HTTP, and GET it back](#serve-json-over-http-and-get-it-back)
- [Read environment variables with defaults](#read-environment-variables-with-defaults)
- [Handle errors from Go calls](#handle-errors-from-go-calls)
- [Sort, dedupe, group and count](#sort-dedupe-group-and-count)
- [Run work concurrently and collect the results](#run-work-concurrently-and-collect-the-results)
- [Write a table-driven test](#write-a-table-driven-test)
- [Call a Go library](#call-a-go-library)

---

## Read, write and count a file

**Problem:** write a text file, read it back, and count its lines, words and characters, like `wc`.

<!-- doc-source: examples/cookbook/files.gala -->
```gala
package main

import (
    "path/filepath"
    "strings"

    "martianoff/gala/fs"
)

// Counts is what `wc` prints for one file.
struct Counts(Lines int, Words int, Chars int)

// count counts the lines, words and characters (not bytes) of a text.
func count(text string) Counts = Counts(
    Lines = strings.Count(text, "\n"),
    Words = strings.Fields(text).Size(),
    Chars = text.Size(),
)

// writeAndCount writes a file into dir, then reads it back and counts it.
// Each step is a Try; the first failure ends the block.
func writeAndCount(dir string) Try[Counts] {
    val path = filepath.Join(dir, "notes.txt")
    bind _ = fs.WriteFileString(path, "héllo world\nsecond line here\n", 0o644)
    bind text = fs.ReadFileString(path)
    Success(count(text))
}

func main() {
    // Clean up whatever happened, and report a failed cleanup too.
    val counted = fs.MkdirTemp("", "cookbook").FlatMap((dir) => {
        val result = writeAndCount(dir)
        fs.RemoveAll(dir).FlatMap((_) => result)
    })
    counted match {
        case Success(c) => Println(s"${c.Lines} lines, ${c.Words} words, ${c.Chars} chars")
        case Failure(err) => Println(s"failed: ${err.Error()}")
    }

    // A missing file is a Failure, not a panic.
    fs.ReadLines("no-such-file.txt") match {
        case Success(lines) => Println(s"${lines.Size()} lines")
        case Failure(_) => Println("no-such-file.txt: cannot read it")
    }
}
```

Output:

<!-- doc-source: examples/cookbook/files.out -->
```text
2 lines, 5 words, 29 chars
no-such-file.txt: cannot read it
```

- The [`fs`](../fs/fs.gala) package returns a `Try` from every call, so a `bind` block reads top to bottom and stops at the first failure.
- `text.Size()` counts characters, so `héllo` is 5; use `.ByteSize()` when you mean bytes.
- `fs.ReadLines` gives an `Array[string]`; `fs.ForEachLine` streams a file too big to read at once.

## Parse command-line flags

**Problem:** read `-name`, `-n` and `-loud` flags and the remaining arguments.

<!-- doc-source: examples/cookbook/flags.gala -->
```gala
package main

import (
    "flag"
    "io"
    "os"
    "strings"

    . "martianoff/gala/collection_immutable"
    . "martianoff/gala/go_interop"
)

// Options are the settings one run takes from its command line.
struct Options(Name string, Times int, Loud bool, Rest Array[string])

// parseArgs reads the flags in args, which leaves out the program name.
// A bad flag is a Failure that holds flag's own message.
func parseArgs(args []string) Try[Options] {
    val set = flag.NewFlagSet("greet", flag.ContinueOnError)
    set.SetOutput(io.Discard)
    val name = set.String("name", "world", "who to greet")
    val times = set.Int("n", 1, "how many times")
    val loud = set.Bool("loud", false, "shout")
    FromError(set.Parse(args)).Map((_) => Options(*name, *times, *loud, ArrayFromSlice(set.Args())))
}

func greet(o Options) string {
    val line = s"hello, ${o.Name}"
    val text = if (o.Loud) strings.ToUpper(line) else line
    ArrayFill(o.Times, text).MkString(" / ") + s" (rest: ${o.Rest.MkString(",")})"
}

func run(args []string) string = parseArgs(args) match {
    case Success(o) => greet(o)
    case Failure(err) => s"error: ${err.Error()}"
}

func main() {
    // A real program passes its own arguments, without the program name.
    Println(run(SliceDrop(os.Args, 1)))
    Println(run(SliceOf("-name", "gala", "-n", "2", "-loud", "a.txt", "b.txt")))
    Println(run(SliceOf("-n", "two")))
}
```

Output, run with no arguments:

<!-- doc-source: examples/cookbook/flags.out -->
```text
hello, world (rest: )
HELLO, GALA / HELLO, GALA (rest: a.txt,b.txt)
error: invalid value "two" for flag -n: parse error
```

- Go's `flag` package works as it is. `set.Parse` returns only an `error`, so `FromError` makes it a `Try[Void]`; `*name` reads the flag's pointer.
- `os.Args[1:]` is `SliceDrop(os.Args, 1)`: GALA has no slice expressions. The Go slice stays at the boundary and becomes an `Array` inside.
- For subcommands and help text, use [cobra](https://github.com/spf13/cobra): [`examples/cobra_cli`](../examples/cobra_cli/main.gala) is a cobra CLI written in GALA, walked through in [Add GALA to a Go Repository](ADD_GALA_TO_GO_REPO.MD).

## Encode and decode JSON

**Problem:** turn a struct into JSON and back, with your own key names, and reject malformed input.

<!-- doc-source: examples/cookbook/json_codec.gala -->
```gala
package main

import (
    . "martianoff/gala/collection_immutable"
    "martianoff/gala/json"
)

// Address is a nested struct; the codec handles it with no extra code.
struct Address(City string, Zip string)

// User has no struct tags: the codec sets the JSON keys.
struct User(ID int, FullName string, Email Option[string], Tags Array[string], Home Address)

// userCodec writes snake_case keys, "id" for ID, and leaves Email out when
// it is None.
val userCodec = json.Codec[User](json.SnakeCase()).Rename("ID", "id").OmitEmpty("Email")

// show is an encoded document, or why encoding failed.
func show(encoded Try[string]) string = encoded match {
    case Success(text) => text
    case Failure(err) => s"cannot encode: ${err.Error()}"
}

func main() {
    val ann = User(1, "Ann Lee", None(), ArrayOf("admin"), Address("Oslo", "0150"))
    Println(show(userCodec.Encode(ann)))

    val text = `{"id":2,"full_name":"Bo","email":"bo@example.com","tags":[],"home":{"city":"Rome","zip":"00100"}}`
    userCodec.Decode(text) match {
        case Success(u) => Println(s"${u.FullName}, ${u.Home.City}, ${u.Email.GetOrElse("no email")}")
        case Failure(err) => Println(s"bad user: ${err.Error()}")
    }

    // Malformed or mistyped input is a Failure. A missing key takes its empty
    // value, so check required fields yourself.
    Println(userCodec.Decode(`{"id":"three"}`).IsFailure())

    // A JSON array at the root: the element codec's .Array().
    Println(show(userCodec.Array().Encode(ArrayOf(ann, ann.Copy(ID = 3, Tags = EmptyArray())))))
}
```

Output:

<!-- doc-source: examples/cookbook/json_codec.out -->
```text
{"id":1,"full_name":"Ann Lee","tags":["admin"],"home":{"city":"Oslo","zip":"0150"}}
Bo, Rome, bo@example.com
true
[{"id":1,"full_name":"Ann Lee","tags":["admin"],"home":{"city":"Oslo","zip":"0150"}},{"id":3,"full_name":"Ann Lee","tags":[],"home":{"city":"Oslo","zip":"0150"}}]
```

- GALA has no struct tags. Key names, omitted fields and empty-value rules are set on the codec, which is generated at compile time with no reflection.
- `Option`, `Array` and nested structs need no extra code; `Decode` returns a `Try`.
- [Json](../website/docs/json.md) covers the codec in full, including `Value[T]()` for a root that is not an object.

## Serve JSON over HTTP, and GET it back

**Problem:** serve a resource as JSON with `net/http`, and write a client that GETs it and decodes the body.

<!-- doc-source: examples/cookbook/http_json.gala -->
```gala
package main

import (
    "errors"
    "io"
    "net/http"
    "net/http/httptest"
    "strconv"
    "time"

    . "martianoff/gala/collection_immutable"
    . "martianoff/gala/go_interop"
    "martianoff/gala/json"
)

// Todo is the resource the server hands out.
struct Todo(ID int, Title string, Done bool)

val todoCodec = json.Codec[Todo](json.SnakeCase()).Rename("ID", "id")

val todos = HashMapOf((1, Todo(1, "write the cookbook", true)), (2, Todo(2, "ship it", false)))

// writeJSON sends a JSON body with a status code and a JSON content type.
func writeJSON(w http.ResponseWriter, status int, body string) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    io.WriteString(w, body)
}

// getTodo serves GET /todos/{id}.
func getTodo(w http.ResponseWriter, r *http.Request) {
    val found = strconv.Atoi(r.PathValue("id")).ToOption().FlatMap((id) => todos.Get(id))
    found.Map((todo) => todoCodec.Encode(todo)) match {
        case Some(Success(body)) => writeJSON(w, http.StatusOK, body)
        case Some(Failure(err)) => http.Error(w, err.Error(), http.StatusInternalServerError)
        case None() => writeJSON(w, http.StatusNotFound, "{\"error\":\"no such todo\"}")
    }
}

func newServer() *http.ServeMux {
    val mux = http.NewServeMux()
    mux.HandleFunc("GET /todos/{id}", getTodo)
    mux
}

// client gives up on a server that does not answer; http.Get never does.
val client = &http.Client(Timeout = 5 * time.Second)

// fetchTodo GETs a URL and decodes the body. client.Get returns
// (*http.Response, error), so it is a Try.
func fetchTodo(url string) Try[Todo] = client.Get(url).FlatMap(readTodo)

// readTodo decodes a response; `use` closes the body on every path out.
func readTodo(resp *http.Response) Try[Todo] {
    use body = resp.Body
    if (resp.StatusCode != http.StatusOK) {
        return Failure(errors.New(s"unexpected status ${resp.Status}"))
    }
    io.ReadAll(body).FlatMap((data) => todoCodec.Decode(ToString(data)))
}

func main() {
    // A real server: http.ListenAndServe(":8080", newServer())
    val server = httptest.NewServer(newServer())
    ArrayOf("/todos/1", "/todos/7").ForEach((path) => fetchTodo(server.URL + path) match {
        case Success(t) => Println(s"#${t.ID} ${t.Title} (done: ${t.Done})")
        case Failure(err) => Println(s"error: ${err.Error()}")
    })
    server.Close()
}
```

Output:

<!-- doc-source: examples/cookbook/http_json.out -->
```text
#1 write the cookbook (done: true)
error: unexpected status 404 Not Found
```

- A handler is a plain function with Go's signature, so it registers with `HandleFunc` like any Go handler. The example serves on `httptest`'s loopback listener; a real program calls `http.ListenAndServe`.
- `use body = resp.Body` is GALA's `defer resp.Body.Close()`: the body is closed when the function returns, on every path.
- One `match` on `Option[Try[string]]` covers found, failed to encode and not found.

## Read environment variables with defaults

**Problem:** read configuration from the environment, falling back to a default when a variable is unset or does not parse.

<!-- doc-source: examples/cookbook/env.gala -->
```gala
package main

import (
    "os"
    "strconv"
    "time"
)

// env is a variable that is set, or None. os.LookupEnv returns
// (string, bool), which GALA gives as a Tuple.
func env(name string) Option[string] = os.LookupEnv(name) match {
    case (value, true) => Some(value)
    case _ => None()
}

// Config is the settings a server reads from its environment.
struct Config(Host string, Port int, Timeout time.Duration)

// loadConfig falls back to a default for a variable that is unset or does
// not parse.
func loadConfig() Config = Config(
    Host = env("APP_HOST").GetOrElse("localhost"),
    Port = env("APP_PORT").FlatMap((v) => strconv.Atoi(v).ToOption()).GetOrElse(8080),
    Timeout = env("APP_TIMEOUT")
        .FlatMap((v) => time.ParseDuration(v).ToOption())
        .GetOrElse(5 * time.Second),
)

func main() {
    // Set the variables for the demo.
    os.Unsetenv("APP_HOST")
    os.Setenv("APP_PORT", "9000")
    os.Setenv("APP_TIMEOUT", "not a duration")

    val c = loadConfig()
    Println(s"${c.Host}:${c.Port}, timeout ${c.Timeout}")
}
```

Output:

<!-- doc-source: examples/cookbook/env.out -->
```text
localhost:9000, timeout 5s
```

- A Go call that returns `(string, bool)` is a tuple, matched with `case (value, true)`.
- `ToOption()` drops a parse error, so a bad value falls back to the default with `GetOrElse`. Keep the `Try` instead when a bad value should stop the program.

## Handle errors from Go calls

**Problem:** call Go functions that return `(T, error)` or only `error`, and report failures without `if err != nil`.

<!-- doc-source: examples/cookbook/errors.gala -->
```gala
package main

import (
    "os"
    "strconv"
    "strings"

    . "martianoff/gala/collection_immutable"
)

// A Go call that returns (T, error) is a Try[T]: no `if err != nil`.
func parsePort(text string) Try[int] = strconv.Atoi(strings.TrimSpace(text))

// A Go call that returns only an error is FromError(call), a Try[Void].
// RecoverWith turns the one failure we expect back into a success.
func removeIfPresent(path string) Try[Void] =
    FromError(os.Remove(path))
        .RecoverWith((err) => if (os.IsNotExist(err)) Success(Void()) else Failure(err))

// ConfigError is why a configuration is unusable: an error type of your
// own, which an Either carries and callers match on.
sealed type ConfigError {
    case Missing(Key string)
    case Invalid(Key string, Value string)
}

func lookup(settings HashMap[string, string], key string) Either[ConfigError, string] =
    settings.Get(key) match {
        case Some(v) => Right(v)
        case None() => Left(Missing(key))
    }

func portSetting(raw string) Either[ConfigError, int] = parsePort(raw) match {
    case Success(p) => Right(p)
    case Failure(_) => Left(Invalid("port", raw))
}

// bind runs fallible steps in order and stops at the first Left.
func address(settings HashMap[string, string]) Either[ConfigError, string] {
    bind host = lookup(settings, "host")
    bind raw = lookup(settings, "port")
    bind port = portSetting(raw)
    Right(s"$host:$port")
}

func explain(e Either[ConfigError, string]) string = e match {
    case Right(addr) => s"listening on $addr"
    case Left(Missing(key)) => s"missing setting: $key"
    case Left(Invalid(key, value)) => s"bad $key: $value"
}

func main() {
    parsePort("eighty") match {
        case Success(p) => Println(s"port $p")
        case Failure(err) => Println(s"error: ${err.Error()}")
    }
    Println(parsePort(" 443 ").GetOrElse(80), parsePort("").GetOrElse(80))
    Println(removeIfPresent("no-such-file.tmp").IsSuccess())

    Println(explain(address(HashMapOf(("host", "example.com"), ("port", "443")))))
    Println(explain(address(HashMapOf(("host", "example.com")))))
    Println(explain(address(HashMapOf(("host", "example.com"), ("port", "https")))))
}
```

Output:

<!-- doc-source: examples/cookbook/errors.out -->
```text
error: strconv.Atoi: parsing "eighty": invalid syntax
443 80
true
listening on example.com:443
missing setting: port
bad port: https
```

- `strconv.Atoi` already is a `Try[int]`: don't wrap it in `Try(...)`. `match` on it, or use `Map`, `Filter`, `GetOrElse` and `bind`.
- A call that returns only `error` becomes a `Try[Void]` with `FromError(call)`, which fails when the error is not nil.
- Reach for `Either` when the caller should match on *which* error it got; the compiler checks that `explain` handles every `ConfigError` case.

## Sort, dedupe, group and count

**Problem:** sort records by a field, remove duplicates, group by a key, and find the most frequent words.

<!-- doc-source: examples/cookbook/collections.gala -->
```gala
package main

import (
    . "martianoff/gala/collection_immutable"
    "martianoff/gala/strings"
)

// Person is one row of the data the examples sort and group.
struct Person(Name string, City string, Age int)

// wordFrequency counts each word, case-insensitively. GALA's strings.Fields
// gives an Array. The HashMap is immutable: Put returns a new map, so
// FoldLeft threads it through.
func wordFrequency(text string) HashMap[string, int] =
    strings.Fields(strings.ToLower(text))
        .FoldLeft(EmptyHashMap[string, int](), (counts, w) =>
            counts.Put(w, counts.GetOrElse(w, 0) + 1))

// byCity groups people into a HashMap from city to names.
func byCity(people Array[Person]) HashMap[string, Array[string]] =
    people.FoldLeft(EmptyHashMap[string, Array[string]](), (groups, p) =>
        groups.Put(p.City, groups.GetOrElse(p.City, EmptyArray()).Append(p.Name)))

func main() {
    val people = ArrayOf(
        Person("Ann", "Oslo", 34),
        Person("Bo", "Rome", 27),
        Person("Cy", "Oslo", 41),
        Person("Di", "Lima", 27),
    )

    // Sort by a key, or by a comparison of your own; both are stable.
    Println(people.SortBy(_.Age).Map(_.Name).MkString(", "))
    Println(people.SortWith((a, b) => a.Age > b.Age).Map(_.Name).MkString(", "))

    // Dedupe, keeping the first occurrence of each.
    Println(people.Map(_.Age).Distinct().Sorted().MkString(" "))

    // Group; a HashMap has no order, so sort its entries to print them.
    byCity(people).Sorted().ForEach((e) => Println(s"${e.V1}: ${e.V2.MkString(", ")}"))

    // Top three words, most frequent first, ties alphabetically.
    val counts = wordFrequency("the cat and the hat and THE bat")
    counts.SortWith((a, b) => a.V2 > b.V2 || (a.V2 == b.V2 && a.V1 < b.V1))
        .Take(3)
        .ForEach((e) => Println(s"${e.V2} ${e.V1}"))
}
```

Output:

<!-- doc-source: examples/cookbook/collections.out -->
```text
Bo, Di, Ann, Cy
Cy, Ann, Bo, Di
27 34 41
Lima: Di
Oslo: Ann, Cy
Rome: Bo
3 the
2 and
1 bat
```

- `Array`, `List` and `HashMap` from `collection_immutable` are persistent: `Put` and `Append` return a new collection, so building one is a `FoldLeft` rather than a loop that mutates a map.
- `_.Age` is shorthand for `(p) => p.Age`.
- A `HashMap`'s entries are `(key, value)` tuples, read as `.V1` and `.V2`. [Immutable Collections](IMMUTABLE_COLLECTIONS.MD) lists every method.

## Run work concurrently and collect the results

**Problem:** run a slow call for each item at the same time, collect the results in order, and fail as soon as one of them fails.

<!-- doc-source: examples/cookbook/concurrency.gala -->
```gala
package main

import (
    "errors"
    "strings"
    "time"

    . "martianoff/gala/collection_immutable"
    . "martianoff/gala/concurrent"
)

// slowLength stands in for a call that takes a while, such as an HTTP request.
func slowLength(word string) int {
    time.Sleep(10 * time.Millisecond)
    word.Size()
}

// lengthOf is the work of one item as a Future, which fails on a blank word.
func lengthOf(word string) Future[int] =
    if (strings.TrimSpace(word) == "") FutureFailed(errors.New("empty word"))
    else Future(slowLength(word))

// lengths fans out: one Future per word. Traverse keeps the input order.
func lengths(words Array[string]) Future[Array[int]] = Traverse(words, lengthOf)

// longestWord is the first of the longest words, or None for no words.
func longestWord(words Array[string]) Option[string] =
    words.ReduceOption((a, b) => if (b.Size() > a.Size()) b else a)

// summarize runs two different jobs at once: the clauses of an `also`
// group run concurrently, and the block waits for both.
func summarize(words Array[string]) Future[string] {
    bind all = lengths(words)
    also longest = Future(longestWord(words).GetOrElse("(none)"))
    Future(s"${all.FoldLeft(0, (n, l) => n + l)} letters, longest word $longest")
}

// report is the lengths, or why they could not be computed.
func report(r Try[Array[int]]) string =
    r.Fold((err) => s"failed: ${err.Error()}", (ls) => ls.MkString(" "))

func main() {
    val words = ArrayOf("alpha", "beta", "gamma", "delta")

    // Await blocks until every Future is done; a failure in any of them
    // fails the whole result.
    Println(report(lengths(words).Await()))
    Println(report(lengths(words.Append(" ")).Await()))

    Println(summarize(words).Await().GetOrElse("failed"))
}
```

Output:

<!-- doc-source: examples/cookbook/concurrency.out -->
```text
5 4 5 5
failed: empty word
19 letters, longest word alpha
```

- GALA has no `go` statement or channels: `Future(expr)` runs `expr` on another goroutine, and `Traverse` is the fan-out/fan-in of a `WaitGroup` plus a results slice.
- `Await()` gives a `Try`, so a failure (or a panic) in any task arrives as a `Failure` instead of crashing the program. The other tasks are not cancelled; they run to the end.
- The compiler checks that what a `Future` captures is safe to share ([Concurrency Safety](CONCURRENCY_SAFETY.MD)). [Concurrent](CONCURRENT.MD) covers timeouts, cancellation and `Race`.

## Write a table-driven test

**Problem:** test a function against a table of inputs, each case reported as its own subtest.

The function under test:

<!-- doc-source: examples/cookbook/slug/slug.gala -->
```gala
package slug

import (
    "strings"
    "unicode"

    . "martianoff/gala/collection_immutable"
)

// Slug turns a title into a URL path segment: lower case, letters and
// digits only, words joined by single dashes.
func Slug(title string) string {
    val keep = (r rune) => unicode.IsLetter(r) || unicode.IsDigit(r)
    val cleaned = strings.Map((r) => if (keep(r)) unicode.ToLower(r) else ' ', title)
    ArrayFromSlice(strings.Fields(cleaned)).MkString("-")
}
```

The test, in `slug_test.gala` beside it:

<!-- doc-source: examples/cookbook/slug/slug_test.gala -->
<!-- doc-check: fragment -->
```gala
package slug

import (
    "strings"

    . "martianoff/gala/test"
)

// Each Case runs as its own subtest, named by Name.
func TestSlug(t T) T = RunCases(t,
    (sub, title, want) => Eq(sub, Slug(title), want),
    Case[string, string](Name = "lower-cases words", Input = "Hello World", Expected = "hello-world"),
    Case[string, string](Name = "drops punctuation", Input = "GALA: a cookbook!", Expected = "gala-a-cookbook"),
    Case[string, string](Name = "squeezes spaces", Input = "  many   spaces ", Expected = "many-spaces"),
    Case[string, string](Name = "keeps digits and accents", Input = "Café 2026", Expected = "café-2026"),
    Case[string, string](Name = "empty title", Input = "", Expected = ""),
)

// A plain test chains assertions: each one takes and returns the T.
func TestSlugIsIdempotent(t T) T {
    val once = Slug("Twice Is Nice")
    val t1 = Eq(t, Slug(once), once)
    IsFalse(t1, strings.Contains(once, " "))
}
```

- `gala test ./...` finds `_test.gala` files and runs every `TestXxx(t T) T`. Use the `martianoff/gala/test` package, not Go's `testing` and testify.
- Assertions return the `T`, so a test passes it from one assertion to the next and returns the last.
- With Bazel, `bazel run //:gazelle` writes the test target. See [Testing](GALA.MD#15-testing) for every assertion.

## Call a Go library

**Problem:** use Go's standard library or a third-party Go package from GALA.

<!-- doc-source: examples/cookbook/go_library.gala -->
```gala
package main

import (
    "fmt"
    "net/url"
    "os"
    "strings"
    "text/tabwriter"
    "time"

    . "martianoff/gala/collection_immutable"
)

// Release is one published version.
struct Release(Version string, Date time.Time)

func main() {
    // A Go call that can fail is a Try; its result is an ordinary Go value.
    url.Parse("https://example.com/search?q=gala&page=2") match {
        case Success(u) => Println(s"${u.Host} q=${u.Query().Get("q")}")
        case Failure(err) => Println(err.Error())
    }

    // Go constants, functions and methods, called as in Go.
    val today = time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
    val releases = ArrayOf(
        Release("1.0.0", time.Date(2025, time.June, 10, 0, 0, 0, 0, time.UTC)),
        Release("1.1.0", time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC)),
    )

    // A Go constructor returns a Go pointer; fmt writes to it as to any io.Writer.
    val w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
    fmt.Fprintln(w, "VERSION\tRELEASED\tAGE")
    releases.ForEach((r) => {
        val days = int(today.Sub(r.Date).Hours() / 24)
        fmt.Fprintf(w, "%s\t%s\t%d days\n", r.Version, r.Date.Format("2006-01-02"), days)
    })
    w.Flush()

    // A lambda where Go wants a func value. The []string Go returns stays a
    // Go slice until ArrayFromSlice turns it into an Array.
    val fields = strings.FieldsFunc("kiwi, fig;banana", (r) => r == ',' || r == ';' || r == ' ')
    Println(ArrayFromSlice(fields).SortBy(_.Size()).MkString(" "))
}
```

Output:

<!-- doc-source: examples/cookbook/go_library.out -->
```text
example.com q=gala
VERSION  RELEASED    AGE
1.0.0    2025-06-10  264 days
1.1.0    2026-02-01  28 days
fig kiwi banana
```

- Import a Go package and call it: no bindings or wrappers. A third-party Go module goes in `gala.mod` with `gala mod add <module>@<version> --go` ([Dependency Management](DEPENDENCY_MANAGEMENT.MD)).
- A Go `(T, error)` result is a `Try[T]` and a `(T, bool)` result is a tuple; everything else comes through unchanged.
- A GALA lambda passes wherever Go expects a `func` value. [Go Interop Guide](GO_INTEROP.MD) covers the rest, including implementing Go interfaces in GALA.
