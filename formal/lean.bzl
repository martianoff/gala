"""rules_lean wrappers for formal/: every Lean target builds only under --config=formal.

Without the config the targets are incompatible with the target platform, so
`bazel build //...` and `bazel test //...` skip them and never download Lean.
Use these instead of loading @rules_lean directly, so no target misses the gate.
"""

load(
    "@rules_lean//lean:defs.bzl",
    _lean_binary = "lean_binary",
    _lean_library = "lean_library",
    _lean_proof_test = "lean_proof_test",
)

_LEAN_ONLY = select({
    "//formal:lean_enabled": [],
    "//conditions:default": ["@platforms//:incompatible"],
})

def lean_library(**kwargs):
    _lean_library(target_compatible_with = _LEAN_ONLY, **kwargs)

def lean_binary(**kwargs):
    _lean_binary(target_compatible_with = _LEAN_ONLY, **kwargs)

def lean_proof_test(**kwargs):
    _lean_proof_test(target_compatible_with = _LEAN_ONLY, **kwargs)
