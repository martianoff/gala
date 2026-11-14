"""rules_lean wrappers for formal/: every Lean target builds only under --config=formal.

Libraries also fail on any Lean warning, like `lake build --wfail`.

Without the config the targets are incompatible with the target platform, so
`bazel build //...` and `bazel test //...` skip them and never download Lean.
Load the Lean rules from here, never from @rules_lean directly, so no target
misses the gate; tools/check_repo_policy.sh enforces that.
"""

load(
    "@rules_lean//lean:defs.bzl",
    _lean_binary = "lean_binary",
    _lean_library = "lean_library",
    _lean_proof_test = "lean_proof_test",
    _lean_test = "lean_test",
)
load("@rules_lean//lean:toolchain.bzl", "LEAN_TOOLCHAIN_TYPE")

_LEAN_ONLY = select({
    "//formal:lean_enabled": [],
    "//conditions:default": ["@platforms//:incompatible"],
})

def _gated(rule, kwargs):
    # Constraints of the caller's own (e.g. an OS) are kept, on top of the gate.
    rule(
        target_compatible_with = _LEAN_ONLY + kwargs.pop("target_compatible_with", []),
        **kwargs
    )

def lean_library(**kwargs):
    # A warning fails the build, as with `lake build --wfail` in CI.
    kwargs.setdefault("warnings_as_errors", True)
    _gated(_lean_library, kwargs)

def lean_binary(**kwargs):
    _gated(_lean_binary, kwargs)

def lean_test(**kwargs):
    _gated(_lean_test, kwargs)

def lean_proof_test(**kwargs):
    _gated(_lean_proof_test, kwargs)

def _lean_runtime_impl(ctx):
    tc = ctx.toolchains[LEAN_TOOLCHAIN_TYPE]
    return [DefaultInfo(
        files = depset([tc.lean]),
        runfiles = ctx.runfiles(files = [tc.lean], transitive_files = tc.lean_files),
    )]

_lean_runtime = rule(
    implementation = _lean_runtime_impl,
    toolchains = [LEAN_TOOLCHAIN_TYPE],
    doc = "The toolchain's `lean` executable, with everything it needs to run in the " +
          "runfiles of a target that has this as data: its libraries and Lean's own " +
          "prebuilt modules. For tests that elaborate and run Lean files they write.",
)

def lean_runtime(**kwargs):
    """The `lean` executable of the registered toolchain, as data for a test."""
    _gated(_lean_runtime, kwargs)
