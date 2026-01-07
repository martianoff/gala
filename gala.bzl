load("@rules_go//go:def.bzl", "go_binary", "go_library")

def _gala_test_impl(ctx):
    binary = ctx.executable.binary
    expected = ctx.file.expected
    runner = ctx.executable._runner

    is_windows = ctx.attr.is_windows
    extension = ".bat" if is_windows else ".sh"
    executable = ctx.actions.declare_file(ctx.label.name + extension)

    if is_windows:
        # Use backslashes for Windows paths to avoid issues with %c etc in .bat
        runner_path = runner.short_path.replace("/", "\\")
        binary_path = binary.short_path.replace("/", "\\")
        expected_path = expected.short_path.replace("/", "\\")
        ctx.actions.write(
            output = executable,
            content = "@echo off\n\"%s\" %%* \"%s\" \"%s\"" % (runner_path, binary_path, expected_path),
            is_executable = True,
        )
    else:
        ctx.actions.write(
            output = executable,
            content = "#!/bin/bash\n%s \"$@\" %s %s" % (runner.short_path, binary.short_path, expected.short_path),
            is_executable = True,
        )

    return [DefaultInfo(
        executable = executable,
        runfiles = ctx.runfiles(files = [binary, expected, runner]),
    )]

gala_exec_test = rule(
    implementation = _gala_test_impl,
    test = True,
    attrs = {
        "binary": attr.label(
            executable = True,
            cfg = "target",
            mandatory = True,
        ),
        "expected": attr.label(
            allow_single_file = True,
            mandatory = True,
        ),
        "is_windows": attr.bool(default = False),
        "_runner": attr.label(
            default = "//cmd/gala_test_runner",
            executable = True,
            cfg = "target",
        ),
    },
)

def gala_transpile(name, src, out = None):
    if not out:
        out = name + ".go"

    native.genrule(
        name = name,
        srcs = [src, "//:all_gala_sources"],
        outs = [out],
        cmd = "$(location //cmd/gala) -input $(location %s) -output $@ -search ." % src,
        tools = ["//cmd/gala"],
        visibility = ["//visibility:public"],
    )

def gala_library(name, src, importpath, deps = [], **kwargs):
    go_src = name + "_gen.go"
    gala_transpile(
        name = name + "_transpile",
        src = src,
        out = go_src,
    )
    go_library(
        name = name,
        srcs = [go_src],
        importpath = importpath,
        deps = deps + ["//std"],
        **kwargs
    )

def gala_binary(name, src, deps = [], **kwargs):
    go_src = name + "_gen.go"
    gala_transpile(
        name = name + "_transpile",
        src = src,
        out = go_src,
    )
    go_binary(
        name = name,
        srcs = [go_src],
        deps = deps + ["//std"],
        **kwargs
    )

def _gala_unit_test_impl(ctx):
    binary = ctx.executable.binary
    is_windows = ctx.attr.is_windows
    extension = ".bat" if is_windows else ".sh"
    executable = ctx.actions.declare_file(ctx.label.name + extension)

    if is_windows:
        binary_path = binary.short_path.replace("/", "\\")
        ctx.actions.write(
            output = executable,
            content = "@echo off\n\"%s\" %%*" % (binary_path),
            is_executable = True,
        )
    else:
        ctx.actions.write(
            output = executable,
            content = "#!/bin/bash\n%s \"$@\"" % (binary.short_path),
            is_executable = True,
        )

    return [DefaultInfo(
        executable = executable,
        runfiles = ctx.runfiles(files = [binary]),
    )]

gala_internal_unit_test = rule(
    implementation = _gala_unit_test_impl,
    test = True,
    attrs = {
        "binary": attr.label(
            executable = True,
            cfg = "target",
            mandatory = True,
        ),
        "is_windows": attr.bool(default = False),
    },
)

def gala_unit_test(name, src, deps = [], **kwargs):
    binary_name = name + "_bin"
    gala_binary(
        name = binary_name,
        src = src,
        deps = deps,
        **kwargs
    )
    gala_internal_unit_test(
        name = name,
        binary = ":" + binary_name,
        is_windows = select({
            "@platforms//os:windows": True,
            "//conditions:default": False,
        }),
    )

def gala_test(name, src, expected, deps = [], **kwargs):
    binary_name = name + "_bin"
    gala_binary(
        name = binary_name,
        src = src,
        deps = deps,
        **kwargs
    )
    gala_exec_test(
        name = name,
        binary = ":" + binary_name,
        expected = expected,
        is_windows = select({
            "@platforms//os:windows": True,
            "//conditions:default": False,
        }),
    )
