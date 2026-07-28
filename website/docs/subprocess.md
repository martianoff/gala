---
layout: default
title: "Subprocess in GALA — Spawn and Drive Child Processes"
description: "GALA's subprocess package spawns and drives external child processes with a goroutine-safe Process handle. Read stdout/stderr, write to stdin, wait, kill, and drive children off-thread with Future-returning async methods."
keywords: "gala subprocess, golang os/exec alternative, gala spawn process, gala child process, gala process handle, go subprocess functional, gala async process"
permalink: /docs/subprocess/
last_modified_at: 2026-07-26
---

<p class="breadcrumb"><a href="/">Home</a> / <a href="/docs/">Docs</a> / Subprocess</p>

{% raw %}

# Subprocess — Spawn and Drive Child Processes

The `subprocess` package spawns and drives external child processes from GALA. It
wraps `os/exec` behind a small, functional API: you describe a command with
`SpawnOpts`, launch it with `Spawn`, and then read its output, write to its
stdin, wait for it to exit, or kill it — all through a value-type `Process`
handle that returns `Try`/`Option` results instead of naked error pairs.

```gala
import . "martianoff/gala/subprocess"
```

---

## The goroutine-safe handle model

`Process` is a **value-type handle** wrapping a pointer to shared, mutable child
state (the `*exec.Cmd`, its stdin writer, buffered stdout/stderr scanners, and
the synchronisation primitives that guard them). Copying a `Process` shares the
same underlying child — the design mirrors `Future[T]`, so you never need
`*Process`.

The methods are safe to call from multiple goroutines: you can `Kill` or `Wait`
from one goroutine while `ReadLine` runs on another. What is **not** serialised
for you is two calls of the *same* read — a single stdout stream is sequential —
so don't run two `ReadLine`s concurrently. Writes to stdin *are* serialised
internally, so concurrent `WriteLine` callers can't interleave bytes.

Because that goroutine-safety is an invariant the package *maintains* rather than
one the compiler can *prove*, `Process` is not statically Shareable — app code
may not capture a `Process` across a `Future` (Sendable) boundary without
tripping GALA's data-race check. The package therefore ships its own
[async methods](#async-methods) that own the invariant internally, so you never
have to hand-roll a goroutine that captures the handle.

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

- **`Cmd`** is resolved on `PATH` unless absolute. An empty `Cmd` makes `Spawn` fail.
- **`Args`** does *not* include the program name (`argv[0]`).
- **`Env`** entries are **appended** to the inherited environment, never a
  replacement — pass an empty slice to inherit unchanged (replace-mode would be a
  footgun: forgetting `PATH` breaks every spawn).
- **`Dir`** empty inherits the parent's current working directory.

`SpawnOpts` is a plain GALA struct — construct it with the struct literal (there
is no separate `New…` constructor at the GALA level):

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

`Spawn` launches the child described by `opts`. On success it is running with its
stdin/stdout/stderr pipes connected. Any failure wiring the pipes or starting the
child short-circuits to `Failure`.

> **Reaping.** A spawned `Process` is reaped only when `Wait` or `Kill` is
> called. Long-running consumers **must** call `Wait` or `Kill` exactly once when
> done, otherwise the child becomes a zombie.

```gala
Spawn(opts) match {
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

- **`WriteLine(s)`** writes `s + "\n"` to stdin and flushes. The success value is
  `Void()`. A `Failure` usually means the child closed stdin or exited. Concurrent
  writers are serialised so their bytes can't interleave.
- **`CloseStdin()`** closes stdin — required for children that read to EOF before
  producing output (e.g. `claude --print`). It is **idempotent** and serialised
  against `WriteLine`, so a write in flight is never truncated.

### Reading output

```gala
func (p Process) ReadLine() Try[string]
func (p Process) ReadStderrLine() Try[string]
```

Both block until a complete line arrives, then return it **without** the trailing
newline. End-of-stream returns `Failure(io.EOF)` — "stream done", not an error.
Recognise it with the standard `Try` API:

```gala
import "io"

func pump(p Process) {
    p.ReadLine() match {
        case Success(line) => { Println(line); pump(p) }
        case Failure(e) if e == io.EOF => {}                  // stream done
        case Failure(e) => Println(s"read error: ${e.Error()}")
    }
}
```

### Waiting and exit codes

```gala
func (p Process) Wait() Try[int]
```

`Wait` blocks until the child exits and returns its exit code. It is
**idempotent** — the underlying wait runs at most once. A **non-zero** exit code
is `Success(code)`, not a `Failure` (a non-zero exit is information); `Failure`
is reserved for a genuine wait error, in which case the code is `-1`.

> Drain the read streams to EOF **before** `Wait`, otherwise the child may block
> writing into a full pipe.

### Killing and status

```gala
func (p Process) Kill() Try[Void]
func (p Process) IsAlive() bool
func (p Process) Pid() int
```

- **`Kill()`** terminates immediately (SIGKILL on Unix, `TerminateProcess` on
  Windows). Idempotent against an already-exited child; you still call `Wait` to reap.
- **`IsAlive()`** returns `true` while the child runs; non-blocking.
- **`Pid()`** returns the OS process id, or `-1` if the child failed to start.

---

## Async methods

The async methods run an operation off the calling goroutine and hand you a
`Future`. They exist so downstream code can read or drive a child off-thread
**without** capturing the opaque, not-statically-Shareable `Process` handle
across a `Future`'s Sendable boundary. The package owns the handle's
goroutine-safety invariant and dispatches the work through the sanctioned
`go_interop` escape hatch internally — your app code just calls these typed,
Future-returning methods and captures nothing unshareable.

```gala
func (p Process) ReadLineAsync() Future[Try[string]]
func (p Process) ReadStderrLineAsync() Future[Try[string]]
func (p Process) WriteLineAsync(s string) Future[Try[Void]]
func (p Process) CloseStdinAsync() Future[Try[Void]]
```

### Future[Try[...]] — EOF is data, not a Future failure

Each async method carries the **same inner `Try`** the synchronous call would
produce as the Future's *success* value. The **outer** `Future` only fails if the
worker goroutine panics.

For `ReadLineAsync`:

- a normal line is `Success(Success(line))`
- end-of-stream is `Success(Failure(io.EOF))` — EOF is information *about the
  stream*, carried inside a **succeeded** Future, unwrapped with the standard
  `Try` API exactly as with synchronous `ReadLine`
- only a panic in the read goroutine makes the Future itself `Failure`

Unwrap the two layers with two `Get()`s — outer awaits the Future, inner unwraps
the `Try` — or pattern-match each layer:

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
`Duration` `d`. It returns **immediately** with a `KillTimer` handle; a
background goroutine waits up to `d` and, *only* if that wait times out (i.e.
`Cancel` was not called in time), invokes `Kill`. Because `Kill` is idempotent
and alive-checked, a late fire after the child has exited is a safe no-op.

`KillTimer.Cancel()` aborts the pending kill. Call it **at most once**.

```gala
import . "martianoff/gala/time_utils"

val p = Spawn(opts).Get()
val timer = p.KillAfter(Seconds(5))     // guard: kill if it hangs past 5s
val out = p.ReadLine()                  // do the real work
timer.Cancel()                          // finished in time — abort the kill
p.Wait()
```

---

## Example: async read + write round-trip

Feed a line to a `sort` filter off-thread, close its stdin off-thread so it
reaches EOF and emits, then read the echoed line back — never capturing the
`Process` handle into a goroutine by hand.

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

### Synchronous `Process` methods

| Method | Returns | Description |
|--------|---------|-------------|
| `WriteLine(s)` | `Try[Void]` | Write `s + "\n"` to stdin (serialised) |
| `CloseStdin()` | `Try[Void]` | Close stdin; idempotent |
| `ReadLine()` | `Try[string]` | Next stdout line; `Failure(io.EOF)` at end-of-stream |
| `ReadStderrLine()` | `Try[string]` | Next stderr line; same EOF semantics |
| `Wait()` | `Try[int]` | Block until exit; non-zero code is `Success`; idempotent |
| `Kill()` | `Try[Void]` | Terminate immediately; idempotent; still `Wait` to reap |
| `IsAlive()` | `bool` | `true` while the child runs; non-blocking |
| `Pid()` | `int` | OS process id, or `-1` if the child failed to start |
| `KillAfter(d)` | `KillTimer` | Kill the child after `Duration` `d` unless cancelled |

### Async `Process` methods

| Method | Returns | Description |
|--------|---------|-------------|
| `ReadLineAsync()` | `Future[Try[string]]` | Off-thread `ReadLine`; EOF is `Success(Failure(io.EOF))` |
| `ReadStderrLineAsync()` | `Future[Try[string]]` | Off-thread `ReadStderrLine` |
| `WriteLineAsync(s)` | `Future[Try[Void]]` | Off-thread `WriteLine`; serialised against writers |
| `CloseStdinAsync()` | `Future[Try[Void]]` | Off-thread `CloseStdin`; idempotent |

> For every async method the inner `Try` is the *success* value of the Future;
> the Future itself only fails on a panic in the worker goroutine.

### `KillTimer` methods

| Method | Returns | Description |
|--------|---------|-------------|
| `Cancel()` | — | Abort the pending kill; call at most once |

---

## Further Reading

- [Concurrency](/features/concurrency/) — `Future` and `Promise` for async computation.
- [Time Utilities](/docs/time-utils/) — the `Duration` type used by `KillAfter`.
- [Error Handling](/features/error-handling/) — `Option`, `Either`, and `Try` monads.

{% endraw %}
