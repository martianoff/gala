# Benchmarking the staged Lean ports

This directory compares the Lean ports under `formal/` with their Go
counterparts in the `gala` module. The Go code is the reference implementation;
the point of the benchmark is to keep the ports honest without guessing from
the batch protocol used by `test.sh`, which measures a hex exchange rather than
the ported functions.

The benchmark times **native Lean**, compiled by `lean --c` and
`leanc -O3 -flto -DNDEBUG` — not the interpreter. `-DNDEBUG` removes the Lean
runtime assertions that the generated C otherwise keeps and was worth roughly
8–11% fewer instructions on these corpora; `-O3 -flto` changed little over
`-O2`, so the scripts use both as plain defaults. Set `LEANC_FLAGS` to compare
other C flags without editing the scripts. Both sides call the same pure
function on identical input, and the drivers print a checksum of everything
they computed; the harness refuses to report a ratio when the Lean and Go
checksums differ.

The CPU benchmark and the memory benchmark are separate measurements of the
same calls, so a speed ratio is never mistaken for a whole cost model. The
latest optimization round's numbers, changes and validation are recorded in
[RESULTS.md](RESULTS.md).

## Metrics: what the numbers are

Three metrics are CPU work, and one is memory:

- `run.sh`: wall-clock nanoseconds for one iteration of the ported function
  over a whole corpus; each process's first iteration is dropped as warm-up,
  so start-up and corpus loading are outside the reported loop.
- `hyperfine.sh`: wall-clock time for a whole process, including start-up and
  corpus loading.
- `callgrind.sh`: Valgrind instruction counts (Ir), which do not move with
  CPU frequency. It runs without `--cache-sim`/`--branch-sim`, so these are
  instruction counts only.
- `mem.sh`: peak resident set size (RSS) in KiB for a whole process, read
  from `VmHWM` in procfs by the drivers themselves. Linux only.

The CPU ratios say nothing about the heap. `mem.sh` reports peak RSS only:
not allocation counts, GC statistics or cache behaviour. Lean's runtime has
no allocation counter comparable to Go's `runtime.MemStats`, so an
"allocated bytes" column would compare one side's instrumentation with the
other's silence; peak RSS is the one memory number both processes can report
the same way.

The wall-clock ratios can sit next to a comparable instruction count because
allocation-heavy Go code stalls more; see [RESULTS.md](RESULTS.md). That is a
CPU-time effect, not a memory result — use `mem.sh` for memory.

## Run it

From the repository root, in the Lean dev shell:

```console
$ nix develop .#gala-lean
$ formal/bench/run.sh --quick
```

That builds the ports and drivers into `formal/bench/build/`, generates the
corpora, and prints one line per benchmark:

```text
interp s       go=  200.123 ms lean=  150.456 ms ratio= 0.75x min_ratio= 0.78x (n=8)
```

`ratio` is `median(Lean) / median(Go)`; below `1.0` means Lean is faster.
`min_ratio` uses each side's fastest iteration instead of the median. An
iteration is one pass over a whole corpus. The first iteration of each process
is dropped as warm-up, so process start and corpus loading do not count; that
is what makes this a component measurement. Use `--full` for the larger run
counts, `--runs N` to change repetitions, and `--no-build` to reuse binaries.

`mem.sh` measures memory on the same scenarios:

```console
$ formal/bench/mem.sh --runs 7
memory: peak RSS (VmHWM), 7 process runs per side, 1 pass(es) per run
startup is an empty-corpus run: process and runtime baseline

scenario       lean load/peak/work KiB    go load/peak/work KiB    peak lean/go
startup        lean   12960/  13000/    40 KiB  go    2376/   2408/    32 KiB  peak  5.40x
interp s       lean   49832/  49844/    12 KiB  go   19808/  40064/ 20256 KiB  peak  1.24x
...
```

Each cell is the median of `--runs` processes (default 3; the sample above
uses 7, like [RESULTS.md](RESULTS.md)). `--reps N` runs N passes per process
instead of one, which shows whether memory is retained between passes. The
script needs Linux procfs; "Memory: peak RSS" below explains the columns.

`hyperfine.sh` and `callgrind.sh` answer different questions:

```console
$ formal/bench/hyperfine.sh          # wall clock, including process start and setup
$ formal/bench/callgrind.sh          # instruction counts (frequency-independent)
```

Both are in the dev shell's package set. Hyperfine runs each command's
repetitions as a block, and desktop CPUs under a powersave governor drift
between blocks, so `hyperfine.sh` measures every scenario twice — Lean first,
then Go first — and writes both JSON and Markdown under
`formal/bench/build/hyperfine/`. Compare both passes rather than trusting one.
`callgrind.sh` profiles the same calls with Valgrind; its instruction counts do
not depend on the CPU frequency. Go requires
`GODEBUG=asyncpreemptoff=1 GOMAXPROCS=1` under Valgrind, which the script sets.

## What is measured

| Benchmark | Lean | Go |
| --- | --- | --- |
| `interp s` | `Interpolation.split content false` | `interpolation.Split(content, false)` |
| `interp f` | `Interpolation.split content true` | `interpolation.Split(content, true)` |
| `mod N` | `Manifest.parse text` then `format` | `mod.Parse(text)` then `mod.Format` |

The interpolation benchmark uses raw component calls: `split` returns parts,
and the driver sums their text and format-spec lengths for the checksum. The
Go driver does the same with `Split`. The manifest benchmark parses the whole
`gala.mod`, formats it back, and sums the output length, mirroring what
`gala mod` does for a manifest. The Go driver's `parse`/`format` modes can be
selected manually; `run.sh` uses `both`.

The corpora are generated by `gala-bench gen` with a fixed seed:

- `s-cases.txt`: 200,000 interpolation bodies (literal text, `$name`,
  `${expr}` with nested braces, `$$`, escapes, and `%` as a literal in `s`
  strings).
- `f-cases.txt`: 100,000 format-string bodies with valid and invalid printf
  specs after `${...}`.
- `manifest-N.mod`: `require` blocks with 40, 400, 1,000, and 4,000 entries;
  about one in five entries carries `// indirect`.

They are written under `formal/bench/build/` (ignored by git). Nothing in this
directory depends on the repository's absolute path, so the same command works
from any checkout.

## Memory: peak RSS

`mem.sh` reports `VmHWM`, the process's peak resident set size, not its live
heap. It is a high-water mark, so it counts whatever garbage the garbage
collector had not reclaimed yet. That is deliberate: it answers the question
a CLI user asks — how much memory does this run need? — without forcing a
collection point that would flatter either runtime.

The three numbers in each cell:

- `load`: the mark after process start and corpus loading, before the
  measured work.
- `peak`: the mark after the measured work. This is the end-to-end footprint
  for the same input and number of passes, and it is what the `peak lean/go`
  ratio compares.
- `work`: `peak - load`, the additional high-water mark the measured work
  caused. It is the closest thing to the port's own memory cost, but it
  still includes each runtime's GC headroom: Go's default `GOGC=100` lets the
  heap grow before collecting, and Lean's runtime has its own policy.

`load` is not comparable between the interpolation sides: the Lean driver
materializes every corpus line as a `ByteArray`, while the Go driver keeps
strings, so Lean's baseline is larger before `split` is called at all. That
is why `work` is reported. Do not compare across scenarios either: `interp s`
loads 200,000 bodies and `mod 40` loads a few hundred bytes.

The manifest driver parses its input once before the loop because `format`
mode needs a parsed manifest; `both` and `parse` then parse again in the
loop. That setup parse is inside `work` on both sides, so it does not bias
the comparison, but a manifest row does one parse more than the timed loop
in `run.sh`.

Readings cover the whole process, including the benchmark driver and the
corpus; they are not a measurement of the ported function's live set alone.
Reading `VmHWM` allocates a little, which is why the empty `startup` row has
a small non-zero `work` instead of exactly zero. Peak RSS also moves with GC
timing — the Go interpolation peak varies by several MiB between processes —
so the harness reports medians and alternates Lean and Go. Differences of a
few percent are noise.

## Why both in-process and hyperfine numbers

`run.sh` measures the steady-state cost of the component: corpus loading and
the first, cold iteration are outside the reported loop. That is the right
number for a compiler that parses many strings in one process, and it is the
most stable one on a noisy laptop.

`hyperfine.sh` measures the whole process, so it includes one-time corpus
loading and the Lean/Go startup difference. It is useful as a sanity check on
end-to-end cost, but a slow corpus loader can move the ratio without the
component changing. The two numbers are expected to differ; neither is wrong,
they measure different things. `callgrind.sh` then removes frequency noise
entirely and shows where the instructions go.

`mem.sh` cannot separate start-up from the measured work the way `run.sh`
does: `VmHWM` belongs to the whole process. Its `load` and `peak` columns
make that boundary visible instead of hiding it, and `--reps N` is the
memory-side equivalent of running more than one iteration.

## Adding a port

1. Add the Lean call and the Go call to the two drivers (or a new pair of
   subcommands/scenarios).
2. Add a corpus generation branch in `driver/main.go` if the existing corpora
   do not exercise the new component.
3. Add a scenario to `run.sh`, `hyperfine.sh`, `callgrind.sh`, and `mem.sh`,
   and verify that the checksum lines still match.

The ports are built with Lean directly, not by Bazel: `run.sh` calls the
per-port `build.sh` and links the benchmark driver against the generated C.
This keeps the benchmark outside the opt-in `--config=formal` build and out of
the release pipeline. The Go driver is an ordinary module package, so Bazel
(and `gazelle`) can compile it with the rest of the repository.
