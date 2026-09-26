---
description: Review a GALA pull request for correctness, regressions, policy violations, tool creep, architecture fit, test coverage and false claims, then draft concise, evidence-backed comments. TRIGGER when user asks to review a PR, a branch, or a GitHub pull request link in this repository.
user-invocable: true
---

# GALA PR Review

Review a pull request against this repository's rules and produce findings the user
can walk through one at a time, each with a ready-to-paste comment.

**Argument:** `$ARGUMENTS` - PR number, PR URL, or branch name.

## Ground rules

- **Never post, approve, or request changes without explicit user approval.** Draft
  everything locally first. When the user asks to go "step by step", present one finding
  per message and wait for keep / change / drop.
- **Never touch the user's main checkout.** All work happens in a dedicated worktree
  under `.claude/worktrees/`. No stash, reset, or checkout in the main repo.
- **No speculation.** Every finding must be backed by a line of code, a quote from the PR,
  or a test you ran. If you cannot verify something, either verify it or drop it. Do not
  soften unverified claims with "might" or "could potentially" and post them anyway.
- **Review what the PR asks you to review.** If the PR stacks on another branch, review
  only its own commits, but check whether its base has since landed (squash merges
  leave the old commits in the PR diff).

## Step 1: Set up

1. Read the PR: `gh pr view <N> --json title,body,baseRefName,headRefOid,mergeable,statusCheckRollup,commits,files`.
2. Fetch it without switching branches: `git fetch origin pull/<N>/head:pr-<N>`.
3. Find the commits under review: `git log --oneline origin/master..pr-<N>` and
   `git merge-base pr-<N> origin/master`. If the description names a commit range, use it.
4. Create a worktree for testing the merged state:
   `git worktree add --detach .claude/worktrees/pr<N>-review pr-<N>`, then
   `git merge --no-commit origin/master` inside it to surface conflicts.
5. Note the CI state. "No checks reported" usually means the branch conflicts with
   master, not that it passed.

## Step 2: Extract every claim

Before reading code, list every factual statement in the PR description: what it
changes, what it fixes, what was "already" true, what tests cover, benchmark numbers,
and what the environment was. Each one is checked against the actual work in Step 4.
The description is what stays on record, so a description that doesn't match the diff
is a finding in its own right.

Also read commit messages, code comments, and test names the PR adds. They are claims
too: a comment saying "disabled under tracing" or a test named "…MatchesLL" must be
true of the code it sits on.

## Step 3: Review the diff

Read the diff commit by commit (`git diff <base>..<head> -- <paths>`), code before
docs and tooling. For each change, name the layer it belongs to (see
`internal/transpiler/README.MD`): parser, analyzer, infer, transformer, generator,
LSP, build driver, stdlib. Then work through the checklists below.

### Correctness and regressions

- **Caches and snapshots.** For every new cache, memo, or snapshot, write down:
  1. every piece of state it reads;
  2. every writer of that state, found with `git grep`, not assumed;
  3. what invalidates it, and whether every writer triggers that;
  4. its lifetime: per call, per file, per transform, or per process. Does it survive
     into the next file or the next `Transform`?
  5. whether its validity check compares against the state at *build* time or only the
     *current* state (a cache built under a temporary condition must not outlive it);
  6. whether shared values are truly immutable.
- **Concurrency.** The analyzer parses a package's files concurrently, and ANTLR's Go
  runtime is not thread-safe. Any new shared state in the parser, analyzer, or
  transformer needs a clear owner.
- **Mode differences.** Check CLI vs LSP (`Transform` vs `TransformForLSP`), batch vs
  single file, persistent worker vs `gala_bootstrap`, and debug modes
  (`GALA_TRACE_TYPES`, `GALA_PROFILE`, `GALA_WARN_TYPES`). An optimization that is
  "disabled under tracing" must actually be disabled.
- **Output and diagnostics.** Any change to generated Go or to error text is a behaviour
  change. Error pages in `docs/errors/` are asserted byte-for-byte against the renderer.
- **Nil safety.** Writes to maps that may now be nil, receivers that may be nil,
  deferred calls skipped by `os.Exit`.

### Breaking changes

Flag anything that can make existing user code, downstream repos, or tooling stop
working or behave differently. GALA is in beta, so breaking changes are allowed, but
they must be **intentional and stated in the PR description** so they reach the release
notes. Don't ask for deprecation shims, compatibility flags, or migration notes.

Look for:

- **Language:** changes to `internal/parser/grammar/gala.g4`, new or removed keywords
  (a new keyword breaks identifiers with that name), changed semantics of existing
  syntax, new or stricter errors on code that used to compile, changed inference results.
- **Generated Go:** renamed or reshaped generated identifiers (types, methods,
  companion objects, `_StructMeta_*`, sealed-type variants), changed signatures, or
  changed runtime behaviour. Go code that imports GALA packages depends on these.
- **Stdlib API:** removed, renamed, or re-typed exported functions, methods, and types in
  `std/` and the other stdlib packages, including changed defaults and error behaviour.
- **CLI and build interfaces:** `gala` commands, flags, exit codes, and output formats
  (`transpile-package`, the persistent-worker protocol, `gala imports --json`), the
  `gala.mod` format, `GALA_*` environment variables, the stdlib cache layout, and
  anything `rules_gala` or `gala_gazelle` relies on.
- **Diagnostics:** renumbered or repurposed `GALA-Exxxx` codes, removed error pages.
- **Toolchain:** Go or Bazel version bumps, new required tools.
- **Tests as a signal:** changed `.out` files under `examples/`, or tests edited to
  accept new behaviour, usually mean user-visible behaviour changed.

For each breaking change, say who is affected and how. For a language, stdlib, or
generated-Go change, check the downstream repos (gala_tui, gala_team, gala-acp) with
`git grep` on a fresh checkout at their default branch, or ask for a manual run of
`.github/workflows/downstream-drift.yml`. An undeclared breaking change is a blocking
finding; a declared one is fine if the description names what breaks.

### Repository policy (`CLAUDE.MD`)

- No edits to generated `internal/parser/grammar/*.go`.
- No special treatment of `std` or any stdlib package in the transpiler.
- Generated Go uses concrete types, never `any` / `interface{}` unless the GALA source
  asks for it; unresolved types are errors.
- Transpiler bugs are fixed with a repro test, not worked around in GALA code.
- No internal references in checked-in files: ticket IDs, private tracker links, agent
  session logs, machine-local paths, fork-only commit SHAs.
- Strict checks are not softened to warnings, and no opt-in flags are added to restore
  strictness.
- No new GALA syntax for Go slices (`arr[:]`, `arr[a:b]`).
- `gala_go_test` auto-injects only `//std` and `//test`.
- A new stdlib package must be registered for the CLI (`gala build` / `gala run`), not
  only for Bazel.
- Language features add a verification example under `examples/` with a `.out` file,
  and update `docs/GALA.MD`, `docs/TYPE_INFERENCE.MD`, and `docs/EXAMPLES.MD` as relevant.

### Simplicity and tool creep

- New scripts, tools, or generated reports: is anything going to run or test them? Are
  they cross-platform? The maintainer works on Windows. Do they duplicate something that
  exists (`GALA_PROFILE`, `GALA_CPUPROFILE`, `docs/PERFORMANCE_DEBUGGING.MD`, Go
  benchmarks, `.github/workflows/perf-real-build.yml`)?
- Files that are records rather than project code: handoffs, plans, measurement logs.
  They belong in the PR description, not the repo.
- Changes unrelated to the PR's stated goal (instrumentation in a perf PR, cleanups in a
  fix PR). Ask to split them, or at least to list them in the description.
- Dead parameters, flags always passed the same value, helpers with one caller,
  mixed styles for the same pattern.

### Architecture fit

- The change lives in the layer that owns the concern (resolution in `resolver`, type
  metadata in `analyzer`, inference in `infer`, emission in `generator`).
- It reuses the existing mechanism instead of adding a parallel one (`resolver.TypeResolver`,
  `registry`, `profiler`, `ImportManager`).
- New invariants are enforced in code (invalidation hooks, assertions), not only
  documented in comments.

### Test coverage

- Every behaviour change has a test that drives the **real code path**. A test that
  mutates state and then calls the invalidation function itself proves nothing about
  the real call sites.
- Every bug fix has a regression test that fails without the fix.
- Invariants the change relies on have a test that would break if a future edit
  violated them.
- Language features: an `examples/` program with expected output.
- Go tests are table-driven with `testify`; new test files are wired into `BUILD.bazel`.
- Benchmarks are welcome but are not correctness tests.

### Go practices

- gofmt clean. On Windows, check the committed blobs rather than working-tree files
  (CRLF checkouts make every file look unformatted), for example
  `git show HEAD:<file> | gofmt -l`, and compare with master to separate pre-existing
  issues.
- Doc comments sit on the declaration they describe; exported identifiers are documented;
  explanatory comments are not deleted in a refactor.
- Errors are wrapped with `%w`; custom error types use the `errors` package.
- No new mutable globals or singletons. Immutable package-level tables are fine.
- Generics over reflection; compile-time interface checks for new implementations.

### GALA code

For any changed `.gala` file, run `/gala-lint` and check it against
`docs/GALA_BEST_PRACTICES.MD` (hard rules, collections, `.Size()` vs `.ByteSize()`,
`Option`/`Try`/`Either`, pattern matching, resource cleanup). Verify examples with a
full build, not just `gala transpile`.

## Step 4: Verify

### Description vs. actual work

Check the description against the diff in both directions:

- **Every claim is backed by the work.** For each Step 2 claim, find the code, test, or
  measurement that proves it. Mark each claim as confirmed, false, overstated, or
  unverifiable.
  - "Already" / "previously" statements: check the base branch
    (`git show origin/master:<file>`, `git log -S <symbol>`). Work credited to this PR
    that landed earlier, and behaviour described as existing that never existed, are
    both false claims.
  - Test claims: open the test and confirm it exercises what the description says (a
    test "comparing SLL and LL" must actually force SLL; a test "per invalidation site"
    must drive the real sites).
  - Numbers: check they come from a run whose environment matches ours (Go version,
    pinned toolchain, OS). If they can't be reproduced, say where they came from rather
    than repeating them as fact.
  - "No behaviour change" / "byte-identical": look for output, diagnostics, ordering, or
    debug-output changes in the diff.
- **Every change is described.** List diff changes the description doesn't mention:
  removed fields, renamed output labels, behaviour fixes bundled into a refactor, new
  flags or env vars, new files. Undisclosed changes are findings: ask for them to be
  listed, or split out if they're unrelated.

### Code

- For each suspected bug, write a minimal failing test in the review worktree and run it.
  A reproduced finding is stated as fact; an unreproduced one is either verified another
  way or dropped.
- Run the suite on the merged state: `bazel test //... --test_output=errors`.
  - If the shared Bazel install base is corrupt, use a private
    `--install_base=<dir>`; do not delete the shared one.
  - Bazel downloads its own pinned Go SDK. If that download fails, generate the parser
    with `bazel build //internal/parser/grammar:gala_grammar_go`, copy the outputs into
    the review worktree's `internal/parser/grammar/`, and run the targeted package with
    the host `go test` for a local repro only. CI remains the authority.
- Confirm a proposed fix works by applying it locally, running the repro and the PR's
  own tests, then reverting.

## Step 5: Present findings

Order by severity:

1. **Blocking:** correctness regressions, breaking changes not declared in the
   description, policy violations, files that must not be checked in.
2. **Should fix:** false, overstated, or missing claims in the description (Step 4), missing coverage of invariants, misleading comments.
3. **Non-blocking:** scope, style, naming.

For each finding give: location (`path:line`), what is wrong, the evidence, the
suggested fix, and a draft comment. Also list what you checked and found fine, so the
user knows it was covered.

### Writing comments

- One issue per comment, anchored to the line it concerns.
- First sentence states the problem. No preamble, no praise padding.
- Quote the PR's own words when correcting a claim.
- Show evidence: the sequence of events, the line, or a repro test in a code block.
- Propose a concrete fix; phrase judgement calls as questions.
- Plain words, short sentences. No internal references, no speculation.
- Be courteous to external contributors: acknowledge real wins once, in the summary.

## Step 6: Post (only after approval)

Post a single review with `event: "COMMENT"` unless the user asks otherwise. Write the
payload to a JSON file in the scratchpad (inline shell quoting breaks on markdown), then:

```bash
gh api -X POST repos/<owner>/<repo>/pulls/<N>/reviews --input review.json
```

- `commit_id` is the PR head SHA; inline comments use `path`, `line`, `side: "RIGHT"`,
  and the line must be an added or changed line in the PR diff.
- Findings without a natural line (description corrections, rebase requests) go in the
  review body.
- Afterwards, confirm the number of posted inline comments matches the drafts.

## Step 7: Clean up

Remove the review worktree (`git worktree remove --force`) and the local `pr-<N>`
branch. If the directory is locked, run `bazel shutdown` from inside it with the same
`--install_base` you used, then retry.
