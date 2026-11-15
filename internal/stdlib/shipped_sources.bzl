"""Macros the stdlib packages' BUILD files share."""

load("@rules_gala//gala:defs.bzl", "gala_package_marker")

def stdlib_shipped_sources():
    """Declares :shipped_sources, every non-test source file of this package.

    The released CLI embeds a snapshot of the stdlib
    (//internal/stdlib:generate_embedded). Its completeness test compares that
    snapshot against this glob, so a new file that is not added to the embed
    fails `bazel test` instead of the released CLI.
    """
    native.filegroup(
        name = "shipped_sources",
        srcs = native.glob(
            [
                "*.gala",
                "*.go",
            ],
            exclude = [
                "*_test.gala",
                "*_test.go",
                "*.gen.go",
            ],
            # A pure-Go or pure-GALA package leaves one pattern empty.
            allow_empty = True,
        ),
        visibility = ["//internal/stdlib:__pkg__"],
    )

def stdlib_gala_package():
    """Declares :gala_package, which a hand-wired stdlib package's go_library embeds.

    A package built with gala_bootstrap_transpile and a plain go_library is a
    GALA package that no GALA macro marks. Embedding the marker keeps the
    transpiler reading its types from its .gala source, never from its
    compiled Go (see rules_gala's gala_package_marker).
    """
    gala_package_marker(name = "gala_package")
