"""Declares a stdlib package's shipped sources for the embed completeness test."""

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
