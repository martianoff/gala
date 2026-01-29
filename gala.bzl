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
        cmd = "$(location //cmd/gala) --input $(location %s) --output $@ --search ." % src,
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

def _gala_go_test_gen_impl(ctx):
    """Generate a main.go file that runs all Test* functions."""
    out = ctx.actions.declare_file(ctx.label.name + "_main.go")

    # Build the command to scan test files and generate main
    args = ctx.actions.args()
    args.add("-output", out)
    args.add("-package", ctx.attr.pkg)
    args.add_all(ctx.files.srcs)

    ctx.actions.run(
        outputs = [out],
        inputs = ctx.files.srcs,
        executable = ctx.executable._test_gen,
        arguments = [args],
        mnemonic = "GalaTestGen",
        progress_message = "Generating test main for %s" % ctx.label,
    )

    return [DefaultInfo(files = depset([out]))]

gala_go_test_gen = rule(
    implementation = _gala_go_test_gen_impl,
    attrs = {
        "srcs": attr.label_list(
            allow_files = [".gala"],
            mandatory = True,
        ),
        "pkg": attr.string(
            default = "main",
            doc = "Package name for the generated main file",
        ),
        "_test_gen": attr.label(
            default = "//cmd/gala_test_gen",
            executable = True,
            cfg = "exec",
        ),
    },
)

def gala_go_test(name, srcs, deps = [], pkg = "main", embed = [], **kwargs):
    """
    Creates a GALA test using Go-style conventions.

    Test functions must:
    - Start with "Test" prefix (e.g., TestAddition)
    - Take a single parameter of type T (e.g., func TestXxx(t T) T)

    For external tests (pkg="main"):
    - Use package main and import the packages being tested

    For internal tests (pkg=same as library):
    - Use the same package as the library
    - Specify embed parameter with library Go sources

    The macro automatically generates a main function that discovers and runs
    all Test* functions.

    Args:
        name: The name of the test target.
        srcs: List of test source files (e.g., ["foo_test.gala"]).
        deps: Dependencies for the test.
        pkg: Package name for tests (default "main" for external tests).
        embed: Go source files to embed (for internal tests in same package).
        **kwargs: Additional arguments passed to the underlying rules.
    """
    # Generate the main.gala file
    gen_name = name + "_gen"
    gala_go_test_gen(
        name = gen_name,
        srcs = srcs,
        pkg = pkg,
    )

    # Transpile each test source file
    transpiled_srcs = []
    for i, src in enumerate(srcs):
        transpile_name = name + "_transpile_" + str(i)
        go_src = name + "_test_" + str(i) + ".go"
        gala_transpile(
            name = transpile_name,
            src = src,
            out = go_src,
        )
        transpiled_srcs.append(go_src)

    # The generated main is already Go code, no transpiling needed
    # Use the output from gala_go_test_gen directly
    main_go_src = ":" + gen_name

    # Build the test binary
    binary_name = name + "_bin"
    all_srcs = transpiled_srcs + [main_go_src] + embed

    # Determine deps - skip //test and //std if testing those packages
    final_deps = list(deps)
    if pkg != "test":
        final_deps.append("//test")
    if pkg != "std":
        final_deps.append("//std")

    go_binary(
        name = binary_name,
        srcs = all_srcs,
        deps = final_deps,
        **kwargs
    )

    # Create the test rule
    gala_internal_unit_test(
        name = name,
        binary = ":" + binary_name,
        is_windows = select({
            "@platforms//os:windows": True,
            "//conditions:default": False,
        }),
    )
