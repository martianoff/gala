---
layout: default
title: "GALA Performance — Measured Build Speed and Memory"
description: "Measured build times, compiler memory and Go build cache size for recent GALA releases, with the method, the machine and the caveats from each release's notes."
keywords: "gala performance, gala build speed, gala compiler memory, compile to go build time, galop parser, go build cache trimpath, gala benchmarks"
permalink: /performance/
last_modified_at: 2026-11-14
---

<div class="breadcrumb">
  <a href="{{ '/' | relative_url }}">Home</a> &raquo; Performance
</div>

*Every number on this page comes from the published release notes. Each table states how it was measured and what it doesn't show.*

# GALA Performance

GALA compiles to Go, so a GALA build has two parts: GALA turns `.gala` files into Go, and then the Go toolchain compiles that Go. Recent releases made both parts cheaper. 0.100.0 sped up the GALA half with a new parser. 0.101.0 made the Go half cheaper by emitting less generic code and sharing the Go build cache between `gala test` and `gala build`.

The numbers below are copied from the release notes. Wall times are noisy, so the memory and cache figures are the most reliable ones.

---

## 0.101.0: less Go to compile, one shared build cache

**What changed.** GALA now emits much less code for its generics. `Equal` and `Copy` compile their reflective parts once instead of once per type, and instantiating `Array[T]` no longer pulls in the whole of `List[T]`, so the Go compiler has less machine code to generate. The build driver also passes `-trimpath`, so `gala test` and `gala build` produce the same Go cache keys and the second command reuses the first one's work.

**Method.** Measured in one session on one machine (Windows 11, 32 cores), with the 0.100.0 and 0.101.0 release binaries back to back. Each run started with an empty private `GOCACHE` and `GALA_HOME`, with `GOFLAGS=-p=2` and Go 1.24.0. The repositories were gala-tui `9ddbac5` and galop `5f73a77`. Each value is the median of two runs. "Peak compile memory" is the largest peak working set of any single `compile` process in the build.

| Workload | 0.100.0 | 0.101.0 | Change |
|---|---:|---:|---:|
| gala-tui `gala test`: peak compile memory | 16.5 GB | 7.4 GB | −55% |
| gala-tui `gala test`: wall time | 335 s | 159 s | −53% |
| gala-tui `gala build ./demo`: peak compile memory | 9.8 GB | 4.4 GB | −55% |
| gala-tui `gala build ./demo`: wall time | 120 s | 119 s | ≈ 0 (noisy, see below) |
| galop cold `gala build`: peak compile memory | 9.1 GB | 3.7 GB | −59% |
| galop cold `gala build`: wall time | 316 s | 135 s | −57% |
| galop `gala build` after `gala test`: wall time | 256 s | 12 s | −95% |
| galop `gala build` after `gala test`: Go cache growth | +4.4 GB | 0 | |
| galop `gala test` + `gala build`: total Go cache | 14.2 GB | 4.4 GB | −69% |

**Caveats.** Other builds were running on the machine at the same time, so wall times are noisy. Peak memory repeated within 6% between runs, and cache sizes were identical. The gala-tui demo build's wall time varied too much to call (0.100.0: 129 s and 111 s; 0.101.0: 184 s and 53 s), even though its peak memory halved.

Source: [GALA 0.101.0 release notes](https://github.com/martianoff/gala/releases/tag/0.101.0).

---

## 0.100.0: the galop parser

**What changed.** GALA now parses with [galop](https://github.com/martianoff/galop)'s native parser instead of building an ANTLR parse tree for every clean file. The whole compiler (analyzer, transformer, language server and tools) reads galop's tree directly. The generated Go is byte-identical to 0.95.0: the parser change alters build speed and memory, not output.

**Method.** Measured on [gala-tui](https://github.com/martianoff/gala-tui), all versions in one alternating run on a quiet machine, reporting medians. 0.93.2 is the last release before galop.

| | 0.93.2 | 0.95.0 | 0.100.0 | vs 0.95.0 |
|---|---:|---:|---:|---:|
| `gala build` | 10.0 s | 7.43 s | 6.03 s | −19% |
| GALA→Go transpile | 7.91 s | 5.33 s | 3.65 s | −32% |
| parsing (all files) | 491 ms | 114 ms | 32 ms | −72% |
| `gala build` peak memory | 859 MB | 484 MB | 178 MB | −63% |
| `gala test` | 15.4 s | 12.3 s | 9.14 s | −26% |
| `gala test` peak memory | 880 MB | 527 MB | 180 MB | −66% |
| `gala imports --json` (737 files) | 1.70 s | 349 ms | 182 ms | 1.9× faster |
| editor keystroke, clean / syntax error (array.gala) | 81.5 / 655 ms | 40.2 / 313 ms | 24.2 / 166 ms | −40% / −47% |
| build worker memory kept (565 packages) | 510 MB | 406 MB | 112 MB | −72% |

Compared with 0.93.2, `gala build` is 40% faster and uses 79% less peak memory.

**Caveats.** This is one project on one machine, and the notes don't name the hardware. The memory rows here are reported as `gala build` and `gala test` peak memory, while the 0.101.0 table reports the largest single Go `compile` process. The two tables use different setups and metrics, so don't compare numbers across them.

Source: [GALA 0.100.0 release notes](https://github.com/martianoff/gala/releases/tag/0.100.0).

---

## See also

- [GALA vs Lisette]({{ '/vs-lisette/' | relative_url }}): how GALA compares with another language that compiles to Go
- [GALA vs Go]({{ '/vs-go/' | relative_url }}): the same programs side by side
- [All releases](https://github.com/martianoff/gala/releases)
