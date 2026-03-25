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
            default = Label("//cmd/gala_test_runner"),
            executable = True,
            cfg = "target",
        ),
    },
)

def _gala_sources_label(dep):
    """Convert a dependency label to its _gala_sources filegroup target."""
    if ":" in dep:
        return dep + "_gala_sources"
    # Shorthand //pkg/path means //pkg/path:path — extract target name from last component
    name = dep.split("/")[-1]
    return dep + ":" + name + "_gala_sources"

def _dep_parent_dir(dep):
    """Extract the parent directory from a dependency label for --search.

    For //examples/cross_file_block_lambda/crossfile, returns
    examples/cross_file_block_lambda so the resolver can find crossfile/
    as a subdirectory.
    """
    if ":" in dep:
        pkg = dep.split(":")[0]
    else:
        pkg = dep
    pkg = pkg.lstrip("/")
    parts = pkg.rsplit("/", 1)
    if len(parts) > 1:
        return parts[0]
    return "."

def gala_transpile(name, src, out = None, package_files = [], extra_srcs = [], gala_deps = []):
    """Transpile a GALA source file to Go using the full gala binary.

    Args:
        name: Target name
        src: The .gala source file to transpile
        out: Output .go file name (optional)
        package_files: List of sibling .gala files in the same package for cross-file type resolution
        extra_srcs: Additional GALA source files/filegroups to make available during transpilation
        gala_deps: GALA library dependency labels. Their _gala_sources filegroups are
            automatically included for cross-package type resolution.
    """
    if not out:
        out = name + ".go"

    pf_flag = ""
    if package_files:
        locs = ",".join(["$(location %s)" % f for f in package_files])
        pf_flag = " --package-files " + locs

    # Auto-include GALA source files from dependencies for cross-package type resolution
    dep_srcs = [_gala_sources_label(dep) for dep in gala_deps]

    # Build search path: repo root + parent dirs of dependencies
    dep_search = ""
    if gala_deps:
        parents = [_dep_parent_dir(dep) for dep in gala_deps]
        dep_search = "," + ",".join(parents)

    # Use go.mod location to find the repository root for search path.
    # Pass GOROOT via --goroot flag so the transpiler can use go/importer
    # for Go type inference (function return types, struct fields, etc.).
    native.genrule(
        name = name,
        srcs = [src] + package_files + extra_srcs + dep_srcs + [Label("//:all_gala_sources"), Label("//:go.mod")],
        outs = [out],
        cmd = "$(location {tool}) --input $(location {src}) --output $@ --search $$(dirname $(location {gomod})){dep_search}{pf} --goroot=$${{GOROOT:-}}".format(
            tool = Label("//cmd/gala"),
            src = src,
            gomod = Label("//:go.mod"),
            dep_search = dep_search,
            pf = pf_flag,
        ),
        tools = [Label("//cmd/gala")],
        visibility = ["//visibility:public"],
        # Allow access to Go SDK filesystem for type inference (go/importer)
        tags = ["no-sandbox"],
    )

def gala_bootstrap_transpile(name, src, out = None, package_files = []):
    """Transpile a GALA source file using the bootstrap transpiler.

    Used only for stdlib packages to avoid circular dependency.
    """
    if not out:
        out = name + ".go"

    pf_flag = ""
    if package_files:
        locs = ",".join(["$(location %s)" % f for f in package_files])
        pf_flag = " --package-files " + locs

    # Use go.mod location to find the repository root for search path.
    # Pass GOROOT for Go type inference support.
    native.genrule(
        name = name,
        srcs = [src] + package_files + [Label("//:all_gala_sources"), Label("//:go.mod")],
        outs = [out],
        cmd = "$(location {tool}) --input $(location {src}) --output $@ --search $$(dirname $(location {gomod})){pf} --goroot=$${{GOROOT:-}}".format(
            tool = Label("//cmd/gala_bootstrap"),
            src = src,
            gomod = Label("//:go.mod"),
            pf = pf_flag,
        ),
        tools = [Label("//cmd/gala_bootstrap")],
        visibility = ["//visibility:public"],
        # Allow access to Go SDK filesystem for type inference (go/importer)
        tags = ["no-sandbox"],
    )

def gala_library(name, src = None, srcs = None, importpath = "", deps = [], embedsrcs = [], **kwargs):
    """
    Build a GALA library.

    Args:
        name: Target name
        src: Single source .gala file (deprecated, use srcs)
        srcs: List of source .gala files
        importpath: Go import path for the library
        deps: Go/Bazel dependencies (labels), including external GALA modules
        embedsrcs: Files to embed via //go:embed directives in GALA source.
        **kwargs: Additional arguments passed to go_library

    External GALA dependencies are loaded via gala_dependencies() in WORKSPACE
    or gala.from_file() in MODULE.bazel, then referenced in deps as
    "@com_github_example_utils//:utils".
    """
    if src and srcs:
        fail("Specify either 'src' or 'srcs', not both")
    if src:
        srcs = [src]
    if not srcs:
        fail("Either 'src' or 'srcs' must be specified")

    go_srcs = []
    for i, s in enumerate(srcs):
        go_src = name + "_" + str(i) + ".gen.go"
        siblings = [other for j, other in enumerate(srcs) if j != i]
        gala_transpile(
            name = name + "_transpile_" + str(i),
            src = s,
            out = go_src,
            package_files = siblings,
        )
        go_srcs.append(go_src)

    # Combine deps with std (using Label to ensure it resolves to @gala//std)
    all_deps = list(deps) + [Label("//std")]

    go_library(
        name = name,
        srcs = go_srcs,
        importpath = importpath,
        deps = all_deps,
        embedsrcs = embedsrcs,
        **kwargs
    )

    # Auto-create gala_sources filegroup so dependents can access GALA source files
    # for cross-package type resolution during transpilation.
    native.filegroup(
        name = name + "_gala_sources",
        srcs = srcs,
        visibility = ["//visibility:public"],
    )

def gala_binary(name, src = None, srcs = None, deps = [], gala_deps = [], embedsrcs = [], **kwargs):
    """
    Build a GALA binary.

    Args:
        name: Target name
        src: Single source .gala file (deprecated, use srcs)
        srcs: List of source .gala files
        deps: Go/Bazel dependencies (labels), including external GALA modules
        gala_deps: GALA library dependency labels for cross-package type resolution.
            Their source files are automatically included during transpilation.
        embedsrcs: Files to embed via //go:embed directives in GALA source.
        **kwargs: Additional arguments passed to go_binary

    External GALA dependencies are loaded via gala_dependencies() in WORKSPACE
    or gala.from_file() in MODULE.bazel, then referenced in deps as
    "@com_github_example_utils//:utils".
    """
    if src and srcs:
        fail("Specify either 'src' or 'srcs', not both")
    if src:
        srcs = [src]
    if not srcs:
        fail("Either 'src' or 'srcs' must be specified")

    go_srcs = []
    for i, s in enumerate(srcs):
        go_src = name + "_" + str(i) + ".gen.go"
        siblings = [other for j, other in enumerate(srcs) if j != i]
        gala_transpile(
            name = name + "_transpile_" + str(i),
            src = s,
            out = go_src,
            package_files = siblings,
            gala_deps = gala_deps,
        )
        go_srcs.append(go_src)

    # Combine deps with std (using Label to ensure it resolves to @gala//std)
    all_deps = list(deps) + list(gala_deps) + [Label("//std")]

    go_binary(
        name = name,
        srcs = go_srcs,
        deps = all_deps,
        embedsrcs = embedsrcs,
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

    binary_runfiles = ctx.attr.binary[DefaultInfo].default_runfiles
    return [DefaultInfo(
        executable = executable,
        runfiles = ctx.runfiles(files = [binary]).merge(binary_runfiles),
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

def gala_unit_test(name, src = None, srcs = None, deps = [], **kwargs):
    binary_name = name + "_bin"
    gala_binary(
        name = binary_name,
        src = src,
        srcs = srcs,
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

def gala_test(name, src = None, srcs = None, expected = "", deps = [], gala_deps = [], **kwargs):
    binary_name = name + "_bin"
    gala_binary(
        name = binary_name,
        src = src,
        srcs = srcs,
        deps = deps,
        gala_deps = gala_deps,
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

def gala_go_test(name, srcs, deps = [], gala_deps = [], pkg = "main", embed = [], lib_srcs = [], **kwargs):
    """
    Creates a GALA test using Go-style conventions.

    Test functions must:
    - Start with "Test" prefix (e.g., TestAddition)
    - Take a single parameter of type T (e.g., func TestXxx(t T) T)

    For external tests (pkg="main"):
    - Use package main and import the packages being tested

    For internal tests (pkg=same as library):
    - Use the same package as the library
    - Specify lib_srcs with GALA library source files to compile together
      with the test, OR embed with pre-transpiled Go source labels

    The macro automatically generates a main function that discovers and runs
    all Test* functions.

    Args:
        name: The name of the test target.
        srcs: List of test source files (e.g., ["foo_test.gala"]).
        deps: Dependencies for the test.
        gala_deps: GALA library dependency labels for cross-package type resolution.
            Their source files are automatically included during transpilation.
        pkg: Package name for tests (default "main" for external tests).
        embed: Go source file labels to include (for internal tests in same package).
        lib_srcs: GALA library source files to transpile and bundle into the test
            binary. Use this for internal tests instead of creating a separate
            go_library target. Each file is transpiled and compiled together with
            the test sources.
        **kwargs: Additional arguments passed to the underlying rules.
    """
    # Generate the main.gala file
    gen_name = name + "_gen"
    gala_go_test_gen(
        name = gen_name,
        srcs = srcs,
        pkg = pkg,
    )

    # Make test framework sources available during transpilation (for type resolution)
    test_extra_srcs = [Label("//test:gala_sources")] if pkg != "test" else []

    # Transpile library source files (lib_srcs) so they can be compiled with the test
    transpiled_lib_srcs = []
    for i, lib_src in enumerate(lib_srcs):
        lib_transpile_name = name + "_lib_transpile_" + str(i)
        lib_go_src = name + "_lib_" + str(i) + ".gen.go"
        lib_siblings = [other for j, other in enumerate(lib_srcs) if j != i]
        gala_transpile(
            name = lib_transpile_name,
            src = lib_src,
            out = lib_go_src,
            package_files = lib_siblings,
            extra_srcs = test_extra_srcs,
            gala_deps = gala_deps,
        )
        transpiled_lib_srcs.append(lib_go_src)

    # Transpile each test source file, including lib_srcs as siblings for type resolution
    transpiled_srcs = []
    all_package_files = list(srcs) + list(lib_srcs)
    for i, src in enumerate(srcs):
        transpile_name = name + "_transpile_" + str(i)
        go_src = name + "_test_" + str(i) + ".go"
        siblings = [f for f in all_package_files if f != src]
        gala_transpile(
            name = transpile_name,
            src = src,
            out = go_src,
            package_files = siblings,
            extra_srcs = test_extra_srcs,
            gala_deps = gala_deps,
        )
        transpiled_srcs.append(go_src)

    # The generated main is already Go code, no transpiling needed
    # Use the output from gala_go_test_gen directly
    main_go_src = ":" + gen_name

    # Build the test binary
    binary_name = name + "_bin"
    all_srcs = transpiled_lib_srcs + transpiled_srcs + [main_go_src] + embed

    # Determine deps - skip //test and //std if testing those packages
    final_deps = list(deps) + list(gala_deps)
    if pkg != "test":
        final_deps.append(Label("//test"))
    if pkg != "std":
        final_deps.append(Label("//std"))

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
