# Security Policy

GALA is a language that transpiles to Go, plus a CLI, a standard library, and IDE
tooling. This document explains how to report a vulnerability, what we consider a
vulnerability, and what you can expect after you report one.

## Reporting a Vulnerability

**Do not open a public issue, discussion, or pull request for a security problem.**

Report privately through GitHub:

1. Go to <https://github.com/martianoff/gala/security/advisories/new>
   (or repository -> **Security** -> **Report a vulnerability**).
2. Describe the issue and how to reproduce it.

This creates a private advisory visible only to you and the maintainers. If GitHub
private reporting is unavailable to you, open a blank issue that says only *"I would
like to report a security issue, please open a private channel"* — with no details —
and a maintainer will follow up.

### What to include

The more of this you can supply, the faster we can confirm and fix:

- A minimal GALA program, `gala.mod`, or command line that reproduces the issue.
- The exact command you ran (`gala build`, `gala run`, `gala test`, `gala lsp`, ...).
- Expected behavior and actual behavior, including the generated Go source if the
  issue is in transpiler output.
- `gala version` output and your OS/architecture.
- Impact as you see it: what an attacker gains, and what access they need first.
- Any proof-of-concept, crash trace, or race-detector output you have.

Please report in English, and let us know if you intend to publish or present the
finding on a fixed date so we can plan around it.

## Supported Versions

GALA is pre-1.0 and moves fast. Security fixes land on `master` and ship in the next
release; we do not backport to older minor versions.

| Version                       | Supported          |
|-------------------------------|--------------------|
| Latest released `0.x` version | :white_check_mark: |
| Any earlier version           | :x:                |

Check what you are running with `gala version`, and upgrade before reporting if you
are behind — the issue may already be fixed.

## Scope

### In scope

- **The transpiler** (`internal/transpiler/...`): generated Go code that is unsafe in
  a way the GALA source is not — for example, a construct that lets attacker-controlled
  content escape string interpolation, an unchecked bound in generated indexing code,
  or a code path that silently emits `any` where a concrete type was required.
- **Soundness holes in the safety guarantees GALA advertises**: escaping the
  data-race/`Sendable` checker, defeating immutability (`val`, immutable fields,
  `ConstPtr[T]`), or bypassing exhaustiveness checking in a way that turns a
  compile-time error into a runtime panic in code that type-checked.
- **The standard library** (`std`, `collection_*`, `crypto`, `json`, `yaml`, `regex`,
  `io`, `fs`, `path`, `subprocess`, `concurrent`, ...): memory-unsafety, injection
  (command, path traversal, header/format), unbounded resource consumption on
  well-formed-looking input, timing leaks in `crypto`, or a parser that can be made to
  panic or exhaust memory on hostile input.
- **The CLI and build driver** (`cmd/gala`, `internal/build`, `internal/depman`,
  `internal/stdlib`): dependency resolution that fetches or executes something the
  `gala.mod` did not ask for, writes outside the workspace and module cache, or is
  vulnerable to path traversal from archive or module contents.
- **The language server** (`internal/lsp`) when it processes a workspace: opening a
  hostile project should not execute code beyond what a normal build of that project
  would, nor read or write outside it.
- **Release artifacts**: a published binary that does not match its entry in
  `checksums-sha256.txt`, or evidence of tampering in the release pipeline.

### Out of scope

- **Compiling or running untrusted code.** `gala build` and `gala run` invoke the Go
  toolchain and execute the resulting program. GALA is not a sandbox: building or
  running an untrusted GALA project is exactly as dangerous as building or running an
  untrusted Go project. If you need isolation, supply it yourself (container, VM,
  seccomp). Reports amounting to "untrusted GALA source can run arbitrary code when I
  build and run it" describe expected behavior, not a vulnerability.
- **Ordinary transpiler and standard-library bugs** — wrong output, compile errors on
  valid code, panics on invalid code, missing features. These belong in a public
  [bug report](https://github.com/martianoff/gala/issues/new/choose); please file them
  there so they get fixed faster.
- **Unsafe Go interop you asked for.** GALA gives you direct access to Go packages via
  `go_interop`. Calling `os/exec`, `unsafe`, or `net/http` does what Go does; the
  transpiler does not audit the Go you reach for.
- **Vulnerabilities in Go itself, in third-party Go modules, or in Bazel.** Report
  those upstream. Tell us anyway if GALA's use of the dependency makes the impact
  materially worse, or if we should pin a fixed version.
- **Missing hardening with no demonstrated impact**: absent compiler flags, unsigned
  binaries, dependency-scanner output without an exploitable path, or best-practice
  checklists.
- **Denial of service against your own machine** by feeding the transpiler a huge or
  pathological input file.
- **Separate repositories.** The playground, IDE plugins published outside this repo,
  and other `martianoff/*` projects have their own issue trackers — though if you are
  unsure which repo owns a problem, report it here and we will route it.

## What Happens Next

GALA is a small project and does not staff a 24/7 security team; the timelines below
are what we aim for, not a contractual SLA.

| Stage                   | Target                                                       |
|-------------------------|--------------------------------------------------------------|
| Acknowledge your report | Within 5 business days                                       |
| Initial assessment      | Within 10 business days                                      |
| Fix released            | Depends on severity and complexity; we will keep you updated |

Then:

1. We confirm the issue and agree with you on its severity and impact.
2. We prepare a fix with a regression test, on a private branch where the issue is
   sensitive enough to warrant it.
3. We release a new version containing the fix.
4. We publish a GitHub Security Advisory crediting you, unless you prefer to stay
   anonymous. Tell us the name or handle you would like used.

Please give us a reasonable window to ship a fix before publishing details. We will
tell you when the fix is out, and we will not ask you to stay quiet indefinitely.

## Safe Harbor

We will not pursue or support legal action against anyone who reports a vulnerability
in good faith under this policy: research on your own systems and your own copies of
GALA, staying within the scope above, avoiding privacy violations and service
disruption, and giving us a chance to fix the issue before disclosing it.

There is no bug bounty program. We can offer credit in the advisory and our thanks.

## Security Notes for GALA Users

A few properties worth knowing when you deploy GALA-built software:

- **Verify what you download.** Every release publishes `checksums-sha256.txt`; check
  your binary against it. Releases are not code-signed today.
- **The build reaches the network.** `gala build` resolves GALA dependencies from
  `gala.mod` and Go dependencies through the Go toolchain, so it fetches from the
  configured module sources. Pin your dependencies, and vendor or cache them if your
  environment requires reproducible, offline builds.
- **GALA's guarantees are compile-time.** Exhaustive matching, immutability, and the
  data-race checker are enforced when you build. They say nothing about input
  validation, authentication, or secrets handling — those remain yours.
- **`subprocess` runs commands.** It does not shell out through an interpreter by
  default, but arguments you build from untrusted input are still your responsibility.
- **Generated Go is normal Go.** Anything that applies to securing a Go binary —
  dependency updates, `govulncheck`, TLS configuration, container hardening — applies
  unchanged to a GALA binary.
