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
        # Use POSIX parameter expansion (no `dirname` dependency) — Bazel's
        # genrule shell on Windows msys2 doesn't have coreutils on PATH.
        parts.append("_locs=\"$(locations %s)\"" % src_label)
        parts.append("_first=\"$${_locs%% *}\"")
        parts.append("_pkg_dir=\"$${_first%/*}\"")
        parts.append("_dep_search=\"$${_dep_search},$${_pkg_dir},$${_pkg_dir%/*}\"")

    prelude = " ; ".join(parts) + " ; "
    return prelude, "$${_dep_search}"

# ---- persistent-worker transpile -------------------------------------------
#
# Bazel persistent worker variant of gala_transpile / gala_transpile_package.
# The worker process stays alive for the duration of the build invocation
# (one process per worker_key) and amortizes analyzer cold-start across all
# per-package transpiles. See cmd/gala/commands/worker.go for the worker
# loop and protocol details.
#
# Each line of the param file Bazel writes becomes one argument the worker
# reads from the WorkRequest. The conventional shape is:
#     transpile-package
#     --inputs=a.gala,b.gala
#     --outputs=a.gen.go,b.gen.go
#     --search=/path1,/path2
#     --goroot=/sdk/go
# (or `transpile` for the single-file case).
#
# The non-worker `gala_transpile` / `gala_transpile_package` macros below
# are kept as-is so existing call sites continue to work. Toggle worker
# mode at the macro layer via gala_use_persistent_worker (default: True)
# — set to False to fall back to the genrule path for one release.

# --define=gala_use_persistent_worker=false disables worker mode for one
# release of escape hatch. Default ON: the whole point of this PR is to
# turn workers on by default. Consumers who hit a regression can opt out.
_USE_WORKER_DEFAULT = True

def _gala_use_persistent_worker():
    """Return True if worker mode is enabled. Read at macro evaluation."""
    return _USE_WORKER_DEFAULT

def _collect_dep_search_paths(gala_deps):
    """Compute --search path entries for gala_deps at rule-construction time.

    The genrule path uses a shell prelude (_dep_search_shell_prelude) that
    expands $(locations) at action time. The worker rule cannot do that —
    it builds args.add_joined() entries from the dep filegroups directly.

    Returns a list of (label, parent_dir, grandparent_dir) tuples. parent
    and grandparent are computed from the FIRST file's path inside the
    rule implementation (where File.path is available). Here we just
    capture the labels; the rule impl handles path derivation.
    """
    return [_gala_sources_label(dep) for dep in gala_deps]

def _gala_transpile_worker_impl(ctx):
    args = ctx.actions.args()
    args.set_param_file_format("multiline")
    args.use_param_file("@%s", use_always = True)

    # Sub-command — first positional arg the worker dispatches on.
    if ctx.attr.batch:
        args.add("transpile-package")
        # batch mode: --inputs and --outputs are both repeated, in pairs.
        in_paths = [f.path for f in ctx.files.srcs]
        out_paths = [f.path for f in ctx.outputs.outs]
        args.add("--inputs=" + ",".join(in_paths))
        args.add("--outputs=" + ",".join(out_paths))
        outputs = list(ctx.outputs.outs)
    else:
        args.add("transpile")
        args.add("--input=" + ctx.file.src.path)
        args.add("--output=" + ctx.outputs.out.path)
        if ctx.files.package_files:
            args.add("--package-files=" + ",".join([f.path for f in ctx.files.package_files]))
        outputs = [ctx.outputs.out]

    # --search paths: project root (from go.mod) + each gala_dep's package
    # dir + module root, mirroring _dep_search_shell_prelude exactly.
    search_paths = []

    # Project root from go.mod's parent.
    gomod = ctx.file._gomod
    # gomod.path is e.g. "go.mod" at repo root; its dirname is "" (repo root)
    # or a sub-path. Bazel's File.dirname is the directory portion.
    proj_root = gomod.dirname
    if proj_root == "":
        proj_root = "."
    search_paths.append(proj_root)

    # For each gala_dep, derive (pkg_dir, module_root) from the FIRST
    # source file's path. The dep is a filegroup whose files are all
    # under the same package dir, so the first file's dirname is the
    # package dir and its parent is (typically) the module root.
    for dep_target in ctx.attr.gala_deps:
        files = dep_target[DefaultInfo].files.to_list()
        if not files:
            continue
        first = files[0]
        pkg_dir = first.dirname
        # POSIX parent — strip trailing component.
        if "/" in pkg_dir:
            mod_root = pkg_dir.rsplit("/", 1)[0]
        else:
            mod_root = pkg_dir
        if pkg_dir not in search_paths:
            search_paths.append(pkg_dir)
        if mod_root not in search_paths:
            search_paths.append(mod_root)

    args.add("--search=" + ",".join(search_paths))

    # Pass GOROOT through; the worker forwards to go/importer.
    goroot = ctx.configuration.default_shell_env.get("GOROOT", "")
    if goroot:
        args.add("--goroot=" + goroot)

    direct_inputs = list(ctx.files.srcs) + list(ctx.files.package_files) + list(ctx.files.extra_srcs) + [gomod]
    if not ctx.attr.batch:
        direct_inputs.append(ctx.file.src)
    inputs = depset(
        direct = direct_inputs,
        transitive = [d[DefaultInfo].files for d in ctx.attr.gala_deps] + [ctx.attr._all_gala_sources[DefaultInfo].files],
    )

    ctx.actions.run(
        executable = ctx.executable._worker,
        arguments = [args],
        inputs = inputs,
        outputs = outputs,
        mnemonic = "GalaTranspile",
        progress_message = "GalaTranspile %s" % ctx.label,
        execution_requirements = {
            "supports-workers": "1",
            # GOROOT discovery requires reading the Go SDK — sandbox can
            # block that on some hosts (matching the no-sandbox tag the
            # genrule path uses).
            "no-sandbox": "1",
        },
        # Inherit the bazel client's --action_env-declared env (GOROOT, PATH).
        # The genrule path got these automatically; ctx.actions.run does NOT
        # unless we opt in here. Without this, the worker subprocess starts
        # with an empty PATH and possibly no GOROOT, so go/importer's
        # findGOROOT() fails on the FIRST request — and because the importer
        # is sync.Once-initialized inside the long-lived worker process,
        # every subsequent request silently emits `any` for Go callback
        # parameters (`func(any)` instead of `func(net.Listener)` etc.),
        # breaking downstream Go compilation. Reproduces on CI Linux runners
        # where the runner's PATH (containing `go`) is the only path GOROOT
        # can be derived from.
        use_default_shell_env = True,
        # `env` still overrides matching keys in the default shell env, so
        # an explicit GOROOT (already extracted from default_shell_env above)
        # is preserved as a defensive belt-and-braces. If GOROOT is empty
        # we omit the dict so default_shell_env passes through untouched.
        env = {"GOROOT": goroot} if goroot else {},
    )

    return [DefaultInfo(files = depset(outputs))]

# Two thin rule variants — one for single-file transpiles (src/out
# attributes), one for batch (srcs/outs). They share an implementation
# and only differ in attribute cardinality. Bazel rules cannot make a
# single attr both optional and required, so we duplicate the attrs
# rather than try to fold them.
gala_transpile_worker_single = rule(
    implementation = _gala_transpile_worker_impl,
    attrs = {
        "src": attr.label(allow_single_file = [".gala"], mandatory = True),
        "out": attr.output(mandatory = True),
        "srcs": attr.label_list(allow_files = [".gala"]),  # unused
        "package_files": attr.label_list(allow_files = [".gala"], default = []),
        "extra_srcs": attr.label_list(allow_files = True, default = []),
        "gala_deps": attr.label_list(default = []),
        "batch": attr.bool(default = False),
        "_worker": attr.label(
            default = Label("//cmd/gala"),
            executable = True,
            cfg = "exec",
        ),
        "_gomod": attr.label(
            default = Label("//:go.mod"),
            allow_single_file = True,
        ),
        "_all_gala_sources": attr.label(default = Label("//:all_gala_sources")),
    },
)

gala_transpile_worker_batch = rule(
    implementation = _gala_transpile_worker_impl,
    attrs = {
        "src": attr.label(allow_single_file = [".gala"]),  # unused
        "srcs": attr.label_list(allow_files = [".gala"], mandatory = True),
        "outs": attr.output_list(mandatory = True),
        "package_files": attr.label_list(allow_files = [".gala"], default = []),
        "extra_srcs": attr.label_list(allow_files = True, default = []),
        "gala_deps": attr.label_list(default = []),
        "batch": attr.bool(default = True),
        "_worker": attr.label(
            default = Label("//cmd/gala"),
            executable = True,
            cfg = "exec",
        ),
        "_gomod": attr.label(
            default = Label("//:go.mod"),
            allow_single_file = True,
        ),
        "_all_gala_sources": attr.label(default = Label("//:all_gala_sources")),
    },
)

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

    if _gala_use_persistent_worker():
        # Translate gala_deps (gala_library labels) into their _gala_sources
        # filegroup labels — that's what the worker rule iterates to build
        # --search paths and to feed inputs. Mirrors the genrule path below.
        dep_src_labels = [_gala_sources_label(dep) for dep in gala_deps]
        gala_transpile_worker_single(
            name = name,
            src = src,
            out = out,
            package_files = package_files,
            extra_srcs = extra_srcs,
            gala_deps = dep_src_labels,
            visibility = ["//visibility:public"],
        )
        return

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

    if _gala_use_persistent_worker():
        dep_src_labels = [_gala_sources_label(dep) for dep in gala_deps]
        gala_transpile_worker_batch(
            name = name,
            srcs = srcs,
            outs = outs,
            extra_srcs = extra_srcs,
            gala_deps = dep_src_labels,
            visibility = ["//visibility:public"],
        )
        return

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
