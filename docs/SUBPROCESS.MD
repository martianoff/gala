# Subprocess Package

The `subprocess` package spawns and drives external child processes from GALA. It
wraps `os/exec` behind a small, functional API: you describe a command with
`SpawnOpts`, launch it with `Spawn`, and then read its output, write to its
stdin, wait for it to exit, or kill it — all through a value-type `Process`
handle that returns `Try`/`Option` results instead of naked error pairs.

## Import

```gala
import . "martianoff/gala/subprocess"
```

---

## The goroutine-safe handle model

`Process` is a **value-type handle** wrapping a pointer to shared, mutable child
state (the `*exec.Cmd`, its stdin writer, buffered stdout/stderr scanners, and
the synchronisation primitives that guard them). Copying a `Process` shares the
same underlying child — the design mirrors `concurrent.Future[T]`, so you never
need `*Process`.

The methods are safe to call from multiple goroutines: you can `Kill` or `Wait`
from one goroutine while `ReadLine` runs on another. What is **not** serialised
for you is two calls of the *same* read: a single stdout stream is sequential, so
the caller must not run two `ReadLine`s concurrently. Writes to stdin *are*
serialised internally (through a mutex), so concurrent `WriteLine` callers can't
interleave bytes.

Because that goroutine-safety is an invariant the package *maintains* rather than
one the compiler can *prove*, `Process` is not statically Shareable — app code
may not capture a `Process` across a `concurrent.Future` (Sendable) boundary
without tripping the data-race check (`GALA-E0037`). The package therefore ships
its own [async methods](#async-methods) that own the invariant internally, so you
never have to hand-roll a goroutine that captures the handle. See
[Concurrency Safety: the Shareability Model](CONCURRENCY_SAFETY.MD) for the full
model.

---

## Spawning a process

### SpawnOpts

`SpawnOpts` is the bag of inputs to `Spawn`:

```gala
struct SpawnOpts(
    Cmd  string,     // executable name (looked up on PATH unless absolute)
    Args []string,   // arguments, NOT including argv[0]
    Env  []string,   // extra "KEY=VAL" entries appended to os.Environ; empty inherits
    Dir  string,     // working directory; empty inherits the parent's cwd
)
```

Notes on the fields:

- **`Cmd`** is resolved on `PATH` unless it is an absolute path. An empty `Cmd`
  makes `Spawn` fail.
- **`Args`** does *not* include the program name (`argv[0]`) — pass only the real
  arguments.
- **`Env`** entries are **appended** to the inherited environment, not a
  replacement for it. Replace-mode would be a footgun (forgetting `PATH` breaks
  every spawn), so pass an empty slice to inherit the parent environment
  unchanged.
- **`Dir`** empty inherits the parent's current working directory.

`SpawnOpts` is a plain GALA struct, so construct it with the struct literal —
there is no separate `New…` constructor at the GALA level:

```gala
import "martianoff/gala/go_interop"

val opts = SpawnOpts(
    Cmd  = "grep",
    Args = go_interop.SliceOf("-n", "TODO"),
    Env  = go_interop.SliceEmpty[string](),
    Dir  = "",
)
```

### Spawn

```gala
func Spawn(opts SpawnOpts) Try[Process]
```

`Spawn` launches the child described by `opts`. On success the process is
running and its stdin/stdout/stderr pipes are connected. Any failure while wiring
the pipes or starting the child short-circuits to `Failure`.

> **Reaping.** A spawned `Process` is only reaped once `Wait` or `Kill` is
> called. Long-running consumers **must** call `Wait` or `Kill` exactly once when
> done, otherwise the child becomes a zombie.

```gala
val result = Spawn(opts)
result match {
    case Success(p) => Println(s"started pid ${p.Pid()}")
    case Failure(e) => Println(s"spawn failed: ${e.Error()}")
}
```

---

## Synchronous Process methods

All methods below block the calling goroutine.

### Writing to stdin

```gala
func (p Process) WriteLine(s string) Try[Void]
func (p Process) CloseStdin() Try[Void]
```

- **`WriteLine(s)`** writes `s + "\n"` to the child's stdin, flushing
  immediately. The success value is `Void()` — there is nothing to report beyond
  "it went through". A `Failure` usually means the child closed stdin or exited;
  it carries the underlying io error. Concurrent writers are serialised so their
  bytes can't interleave.
- **`CloseStdin()`** closes the child's stdin pipe. This is required for children
  like `claude --print` that read stdin to EOF before producing any output —
  without an explicit close they wait forever for more input. It is **idempotent**
  (a second close is a no-op `Success`) and serialised against `WriteLine`
  through the stdin mutex, so a write in flight is never truncated.

### Reading output

```gala
func (p Process) ReadLine() Try[string]
func (p Process) ReadStderrLine() Try[string]
```

Both block until the child writes a complete line, then return it **without** the
trailing newline. `ReadStderrLine` is the stderr counterpart to `ReadLine`.

End-of-stream (the child closed the stream, or exited) returns
`Failure(io.EOF)`. EOF is "stream done", not an error condition — recognise it
with the standard `Try` API rather than treating it as a hard failure:

```gala
import "io"

func drain(p Process): List[string] = {
    // Loop reading lines until EOF, collecting them.
    p.ReadLine() match {
        case Success(line) => drain(p).Prepend(line)
        case Failure(e) if e == io.EOF => ListOf[string]()   // stream done
        case Failure(e) => { Println(s"read error: ${e.Error()}"); ListOf[string]() }
    }
}
```

### Waiting and exit codes

```gala
func (p Process) Wait() Try[int]
```

`Wait` blocks until the child exits and returns its exit code. It is
**idempotent** — the underlying `Wait` runs at most once, and subsequent calls
return the cached result. A **non-zero** exit code is returned as
`Success(code)`, not a `Failure`: a non-zero exit is information, not an error.
`Failure` is reserved for a genuine wait error (in which case the code is `-1`).

> Drain `ReadLine` / `ReadStderrLine` to EOF **before** `Wait`, otherwise the
> child may block writing into a full pipe. The scanner returning EOF naturally
> signals "you can `Wait` now".

### Killing

```gala
func (p Process) Kill() Try[Void]
```

`Kill` terminates the child immediately (SIGKILL on Unix, `TerminateProcess` on
Windows — `exec.Cmd` handles the platform difference). It is **idempotent**
against an already-exited process: a kill after `Wait` returns `Success` without
error. You are still responsible for calling `Wait` afterwards to reap.

### Status queries

```gala
func (p Process) IsAlive() bool
func (p Process) Pid() int
```

- **`IsAlive()`** returns `true` while the child is still running. It is
  non-blocking — it consults the cached `Wait` result first, then polls the
  process state.
- **`Pid()`** returns the OS process id, or `-1` if the child failed to start.

---

## Async methods

The async methods run an operation off the calling goroutine and hand you a
`concurrent.Future`. They exist precisely so downstream code can read or drive a
child off-thread **without** capturing the opaque, not-statically-Shareable
`Process` handle across a `Future`'s Sendable boundary. The subprocess package
owns the handle's goroutine-safety invariant and dispatches the work through the
sanctioned `go_interop.Spawn` / `SpawnWithRecover` escape hatch internally, so
your app code just calls these typed, Future-returning methods and captures
nothing unshareable. (See
[Concurrency Safety](CONCURRENCY_SAFETY.MD) / `GALA-E0037`.)

```gala
func (p Process) ReadLineAsync() concurrent.Future[Try[string]]
func (p Process) ReadStderrLineAsync() concurrent.Future[Try[string]]
func (p Process) WriteLineAsync(s string) concurrent.Future[Try[Void]]
func (p Process) CloseStdinAsync() concurrent.Future[Try[Void]]
```

### Future[Try[...]] semantics — EOF is data, not a Future failure

Each async method carries the **same inner `Try`** the synchronous call would
produce as the Future's *success* value. The **outer** `Future` only fails if the
worker goroutine panics (surfaced as the Future's `Failure`).

Concretely, for `ReadLineAsync`:

- a normal line is `Success(Success(line))`
- end-of-stream is `Success(Failure(io.EOF))` — EOF is information *about the
  stream*, carried inside a **succeeded** Future, so you unwrap it with the
  standard `Try` API exactly as you would with synchronous `ReadLine`
- only a panic in the read goroutine makes the Future itself `Failure`

The same shape applies to the others: `WriteLineAsync` / `CloseStdinAsync`
complete with `Success(Try[Void])`, and `WriteLineAsync` serialises against
concurrent writers through the stdin mutex just like `WriteLine`.

Unwrap the two layers with two `Get()`s (outer awaits the Future, inner unwraps
the `Try`) — or pattern-match on each layer:

```gala
val line = p.ReadLineAsync().Get().Get()   // outer awaits, inner unwraps
```

---

## Timed kill: KillAfter / KillTimer

```gala
func (p Process) KillAfter(d Duration) KillTimer

struct KillTimer(signal go_interop.Signal)
func (k KillTimer) Cancel()
```

`KillAfter(d)` arranges for the child to be killed if it is still running after
`Duration` `d`. It returns **immediately** with a `KillTimer` cancellation
handle; a background goroutine waits up to `d` and, *only* if that wait times out
(i.e. `Cancel` was not called in time), invokes `p.Kill()`. Because `Kill` is
idempotent and alive-checked, a late fire after the child has already exited is a
safe no-op — you are still responsible for calling `Wait` to reap.

`KillTimer.Cancel()` aborts the pending kill by waking the timer goroutine so it
exits without killing the child. Call it **at most once** (it closes the
underlying signal, and closing an already-closed signal panics). A `Cancel` that
races a timer which has already fired is harmless.

```gala
import . "martianoff/gala/time_utils"

val p = Spawn(opts).Get()
val timer = p.KillAfter(Seconds(5))     // guard: kill if it hangs past 5s
val out = p.ReadLine()                  // do the real work
timer.Cancel()                          // finished in time — abort the kill
p.Wait()
```

---

## Examples

### Simple spawn and read

Spawn a command, read its stdout line by line to EOF, then wait for the exit
code.

```gala
package main

import (
    "io"
    . "martianoff/gala/subprocess"
    "martianoff/gala/go_interop"
)

func echoOpts(): SpawnOpts = SpawnOpts(
    Cmd  = "echo",
    Args = go_interop.SliceOf("hello world"),
    Env  = go_interop.SliceEmpty[string](),
    Dir  = "",
)

func pump(p Process) {
    p.ReadLine() match {
        case Success(line) => { Println(line); pump(p) }
        case Failure(e) if e == io.EOF => {}                  // stream done
        case Failure(e) => Println(s"read error: ${e.Error()}")
    }
}

func main() {
    Spawn(echoOpts()) match {
        case Success(p) => {
            pump(p)
            val code = p.Wait().GetOrElse(-1)
            Println(s"exited with $code")
        }
        case Failure(e) => Println(s"spawn failed: ${e.Error()}")
    }
}
```

### Async read + write round-trip

Feed a line to a `sort` filter off-thread, close its stdin off-thread so it
reaches EOF and emits, then read the echoed line back — never capturing the
`Process` handle into a goroutine by hand. (Mirrors
`examples/subprocess_async.gala`.)

```gala
package main

import (
    "runtime"
    . "martianoff/gala/subprocess"
    "martianoff/gala/go_interop"
)

func shellOpts(script string): SpawnOpts = {
    val cmd = if (runtime.GOOS == "windows") "cmd" else "sh"
    val flag = if (runtime.GOOS == "windows") "/c" else "-c"
    return SpawnOpts(
        Cmd  = cmd,
        Args = go_interop.SliceOf(flag, script),
        Env  = go_interop.SliceEmpty[string](),
        Dir  = "",
    )
}

func sortOpts(): SpawnOpts = SpawnOpts(
    Cmd  = "sort",
    Args = go_interop.SliceEmpty[string](),
    Env  = go_interop.SliceEmpty[string](),
    Dir  = "",
)

func main() {
    // Read a known line OFF-THREAD via the returned Future.
    val p = Spawn(shellOpts("echo hello-async")).Get()
    val line = p.ReadLineAsync().Get().Get()   // outer awaits Future, inner unwraps Try
    Println(line)
    p.Wait()

    // Write / close OFF-THREAD, then read the echoed line back.
    val f = Spawn(sortOpts()).Get()
    f.WriteLineAsync("written-async").Get().Get()
    f.CloseStdinAsync().Get().Get()
    val echoed = f.ReadLineAsync().Get().Get()
    Println(echoed)
    f.Wait()
}
```

---

## Method Reference

### Free functions

| Function | Returns | Description |
|----------|---------|-------------|
| `Spawn(opts)` | `Try[Process]` | Launch the child described by `opts`; pipes connected on success |

### Types

| Type | Description |
|------|-------------|
| `SpawnOpts(Cmd, Args, Env, Dir)` | Struct literal describing the command to spawn |
| `Process` | Value-type handle to a running child and its IO pipes |
| `KillTimer(signal)` | Cancellation handle returned by `KillAfter` |

### Synchronous `Process` methods

| Method | Returns | Description |
|--------|---------|-------------|
| `WriteLine(s)` | `Try[Void]` | Write `s + "\n"` to stdin (serialised); `Success(Void())` on clean write |
| `CloseStdin()` | `Try[Void]` | Close stdin; idempotent, serialised against `WriteLine` |
| `ReadLine()` | `Try[string]` | Next stdout line without newline; `Failure(io.EOF)` at end-of-stream |
| `ReadStderrLine()` | `Try[string]` | Next stderr line without newline; same EOF semantics |
| `Wait()` | `Try[int]` | Block until exit; returns exit code (non-zero is `Success`); idempotent |
| `Kill()` | `Try[Void]` | Terminate immediately; idempotent; still requires `Wait` to reap |
| `IsAlive()` | `bool` | `true` while the child runs; non-blocking |
| `Pid()` | `int` | OS process id, or `-1` if the child failed to start |
| `KillAfter(d)` | `KillTimer` | Kill the child after `Duration` `d` unless cancelled |

### Async `Process` methods

| Method | Returns | Description |
|--------|---------|-------------|
| `ReadLineAsync()` | `concurrent.Future[Try[string]]` | Off-thread `ReadLine`; EOF is `Success(Failure(io.EOF))` |
| `ReadStderrLineAsync()` | `concurrent.Future[Try[string]]` | Off-thread `ReadStderrLine`; same semantics |
| `WriteLineAsync(s)` | `concurrent.Future[Try[Void]]` | Off-thread `WriteLine`; serialised against writers |
| `CloseStdinAsync()` | `concurrent.Future[Try[Void]]` | Off-thread `CloseStdin`; idempotent |

> For every async method the inner `Try` is the *success* value of the Future;
> the Future itself only fails on a panic in the worker goroutine.

### `KillTimer` methods

| Method | Returns | Description |
|--------|---------|-------------|
| `Cancel()` | — | Abort the pending kill; call at most once |

---

## Further Reading

- [Concurrent](CONCURRENT.MD) — `Future`, `Promise`, and `ExecutionContext` for async programming.
- [Concurrency Safety](CONCURRENCY_SAFETY.MD) — the Shareability model and the `Sendable` boundary that motivates the async methods.
- [Time Utils](TIME_UTILS.MD) — the `Duration` type used by `KillAfter`.
