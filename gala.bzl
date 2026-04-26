load("@rules_go//go:def.bzl", "go_binary", "go_library", "go_test")

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

def _dep_search_shell_prelude(gala_deps):
    """Build a shell prelude that populates the _dep_search variable.

    For each GALA dep, expand $(locations <_gala_sources>) to the actual
    on-disk file paths produced by Bazel, take the first one, and derive
    both its parent directory (the dep package dir) and grandparent
    directory (typically the dep module root containing gala.mod/go.mod).
    Both are appended to _dep_search as comma-separated entries.

    This works uniformly for in-repo deps (paths like
    examples/cross_file_block_lambda/crossfile/methods.gala) and
    cross-module deps (paths like
    bazel-out/.../external/<repo>+/some_pkg/foo.gala) because Bazel
    resolves the locations to real filesystem paths inside the genrule
    sandbox at execution time. The previous label-string-derived
    approach returned tokens like @<repo> that are not filesystem
    paths and could not be walked by the transpiler's resolver, so
    cross-repository GALA deps were misclassified as Go packages and
    their sealed-type / struct metadata was silently dropped.

    Returns the shell snippet (terminated with ' ; ' so it can be
    prepended to the existing cmd) plus the shell expansion to splice
    into the --search argument. Both are empty when gala_deps is empty.
    """
    if not gala_deps:
        return "", ""

    parts = ["_dep_search=\"\""]
    for dep in gala_deps:
        src_label = _gala_sources_label(dep)
        # _locs is the space-separated list of dep source file paths.
        # _first is the first path; _pkg_dir is its directory; the
        # grandparent typically equals the module root.
        parts.append("_locs=\"$(locations %s)\"" % src_label)
        parts.append("_first=\"$${_locs%% *}\"")
        parts.append("_pkg_dir=\"$$(dirname \"$$_first\")\"")
        parts.append("_dep_search=\"$${_dep_search},$${_pkg_dir},$$(dirname \"$$_pkg_dir\")\"")

    prelude = " ; ".join(parts) + " ; "
    return prelude, "$${_dep_search}"

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

    # Build a shell prelude that derives --search paths from the actual
    # Bazel-resolved dep source file locations (so cross-module deps in
    # external Bazel repositories work the same as in-repo deps).
    dep_prelude, dep_search_expansion = _dep_search_shell_prelude(gala_deps)

    # Use go.mod location to find the repository root for search path.
    # Pass GOROOT via --goroot flag so the transpiler can use go/importer
    # for Go type inference (function return types, struct fields, etc.).
    native.genrule(
        name = name,
        srcs = [src] + package_files + extra_srcs + dep_srcs + [Label("//:all_gala_sources"), Label("//:go.mod")],
        outs = [out],
        cmd = "_gomod=$(location {gomod}) ; {dep_prelude}$(location {tool}) --input $(location {src}) --output $@ --search $${{_gomod%/*}}{dep_search}{pf} --goroot=$${{GOROOT:-}}".format(
            tool = Label("//cmd/gala"),
            src = src,
            gomod = Label("//:go.mod"),
            dep_prelude = dep_prelude,
            dep_search = dep_search_expansion,
            pf = pf_flag,
        ),
        tools = [Label("//cmd/gala")],
        visibility = ["//visibility:public"],
        # Allow access to Go SDK filesystem for type inference (go/importer)
        tags = ["no-sandbox"],
    )

def gala_transpile_package(name, srcs, outs = None, extra_srcs = [], gala_deps = []):
    """Transpile all GALA files in a package in ONE process (much faster than per-file).

    This uses 'gala transpile-package' which shares the analyzer cache across files,
    avoiding redundant re-analysis of imports (std, collection_immutable, etc.).

    Args:
        name: Target name
        srcs: List of .gala source files in the package
        outs: List of output .go file names (same order as srcs). Defaults to *.gen.go.
        extra_srcs: Additional GALA source files/filegroups to make available during transpilation
        gala_deps: GALA library dependency labels for cross-package type resolution
    """
    if not outs:
        outs = [s.replace(".gala", ".gen.go") for s in srcs]

    if len(srcs) != len(outs):
        fail("gala_transpile_package: srcs and outs must have the same length")

    # Auto-include GALA source files from dependencies for cross-package type resolution
    dep_srcs = [_gala_sources_label(dep) for dep in gala_deps]

    # Build a shell prelude that derives --search paths from the actual
    # Bazel-resolved dep source file locations (so cross-module deps in
    # external Bazel repositories work the same as in-repo deps).
    dep_prelude, dep_search_expansion = _dep_search_shell_prelude(gala_deps)

    inputs_flag = ",".join(["$(location %s)" % s for s in srcs])
    outputs_flag = ",".join(["$(location %s)" % o for o in outs])

    native.genrule(
        name = name,
        srcs = srcs + extra_srcs + dep_srcs + [Label("//:all_gala_sources"), Label("//:go.mod")],
        outs = outs,
        cmd = "_gomod=$(location {gomod}) ; {dep_prelude}$(location {tool}) transpile-package --inputs {inputs} --outputs {outputs} --search $${{_gomod%/*}}{dep_search} --goroot=$${{GOROOT:-}}".format(
            tool = Label("//cmd/gala"),
            inputs = inputs_flag,
            outputs = outputs_flag,
            gomod = Label("//:go.mod"),
            dep_prelude = dep_prelude,
            dep_search = dep_search_expansion,
        ),
        tools = [Label("//cmd/gala")],
        visibility = ["//visibility:public"],
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
        cmd = "_gomod=$(location {gomod}) ; $(location {tool}) --input $(location {src}) --output $@ --search $${{_gomod%/*}}{pf} --goroot=$${{GOROOT:-}}".format(
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

def gala_library(name, src = None, srcs = None, importpath = "", deps = [], gala_deps = [], embedsrcs = [], go_srcs = [], **kwargs):
    """
    Build a GALA library.

    Args:
        name: Target name
        src: Single source .gala file (deprecated, use srcs)
        srcs: List of source .gala files
        importpath: Go import path for the library
        deps: Go/Bazel dependencies (labels), including external GALA modules
        gala_deps: GALA library dependency labels for cross-package type resolution.
            Their source files are automatically included during transpilation.
        embedsrcs: Files to embed via //go:embed directives in GALA source.
        go_srcs: Hand-written .go files in the same package. They are made
            available to the transpiler (for cross-language type inference of
            Go-declared functions/types in the local package) and included
            verbatim in the resulting go_library.
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

    gen_go_srcs = [name + "_" + str(i) + ".gen.go" for i in range(len(srcs))]

    # Use batch transpilation for packages with multiple files (much faster)
    if len(srcs) > 1:
        gala_transpile_package(
            name = name + "_transpile",
            srcs = srcs,
            outs = gen_go_srcs,
            extra_srcs = go_srcs,
            gala_deps = gala_deps,
        )
    else:
        gala_transpile(
            name = name + "_transpile_0",
            src = srcs[0],
            out = gen_go_srcs[0],
            extra_srcs = go_srcs,
            gala_deps = gala_deps,
        )

    # Combine deps with std and gala_deps (using Label to ensure it resolves to @gala//std)
    all_deps = list(deps) + list(gala_deps) + [Label("//std")]

    go_library(
        name = name,
        srcs = gen_go_srcs + go_srcs,
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

    go_srcs = [name + "_" + str(i) + ".gen.go" for i in range(len(srcs))]

    if len(srcs) > 1:
        gala_transpile_package(
            name = name + "_transpile",
            srcs = srcs,
            outs = go_srcs,
            gala_deps = gala_deps,
        )
    else:
        gala_transpile(
            name = name + "_transpile_0",
            src = srcs[0],
            out = go_srcs[0],
            gala_deps = gala_deps,
        )

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
    transpiled_lib_srcs = [name + "_lib_" + str(i) + ".gen.go" for i in range(len(lib_srcs))]
    if len(lib_srcs) > 1:
        gala_transpile_package(
            name = name + "_lib_transpile",
            srcs = lib_srcs,
            outs = transpiled_lib_srcs,
            extra_srcs = test_extra_srcs,
            gala_deps = gala_deps,
        )
    elif len(lib_srcs) == 1:
        gala_transpile(
            name = name + "_lib_transpile_0",
            src = lib_srcs[0],
            out = transpiled_lib_srcs[0],
            extra_srcs = test_extra_srcs,
            gala_deps = gala_deps,
        )
    else:
        transpiled_lib_srcs = []

    # Transpile test source files (with lib_srcs as siblings for type resolution)
    # Tests need all lib_srcs + other test srcs as package files
    all_package_files = list(srcs) + list(lib_srcs)
    transpiled_srcs = [name + "_test_" + str(i) + ".go" for i in range(len(srcs))]
    if len(srcs) > 1:
        # Batch transpile test files too
        # Note: test files need lib_srcs as extra package files, but gala_transpile_package
        # doesn't support mixed package files. Fall back to per-file for tests.
        for i, src in enumerate(srcs):
            transpile_name = name + "_transpile_" + str(i)
            siblings = [f for f in all_package_files if f != src]
            gala_transpile(
                name = transpile_name,
                src = src,
                out = transpiled_srcs[i],
                package_files = siblings,
                extra_srcs = test_extra_srcs,
                gala_deps = gala_deps,
            )
    elif len(srcs) == 1:
        siblings = [f for f in all_package_files if f != srcs[0]]
        gala_transpile(
            name = name + "_transpile_0",
            src = srcs[0],
            out = transpiled_srcs[0],
            package_files = siblings,
            extra_srcs = test_extra_srcs,
            gala_deps = gala_deps,
        )

    # The generated main is already Go code, no transpiling needed
    # Use the output from gala_go_test_gen directly
    main_go_src = ":" + gen_name

    # Determine deps - skip //test and //std if testing those packages
    final_deps = list(deps) + list(gala_deps)
    if pkg != "test":
        final_deps.append(Label("//test"))
    if pkg != "std":
        final_deps.append(Label("//std"))

    if pkg == "main":
        # External tests: use go_binary (all sources are package main)
        binary_name = name + "_bin"
        all_srcs = transpiled_lib_srcs + transpiled_srcs + [main_go_src] + embed

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
    else:
        # Internal tests: use go_library + go_test with embed.
        # This avoids the go_binary package-main requirement that conflicts
        # with internal test packages (USABILITY-010).
        lib_name = name + "_lib"
        if transpiled_lib_srcs or embed:
            go_library(
                name = lib_name,
                srcs = transpiled_lib_srcs + embed,
                deps = final_deps,
                importpath = pkg,
            )
            test_embed = [":" + lib_name]
        else:
            test_embed = []

        go_test(
            name = name,
            srcs = transpiled_srcs + [main_go_src],
            embed = test_embed,
            deps = final_deps,
            **kwargs
        )
