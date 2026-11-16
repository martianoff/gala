"""go_toolchain_manifest: what the Go toolchain call budget test reads from Bazel.

It writes a manifest naming, by runfiles path, the pinned Go SDK, the standard
library rules_go compiled, and every Go library `deps` reaches: its export data
(the .x file) and its sources. The test builds a GALA worker request from the
export data, the way rules-gala's transpile rules do, and Go modules from the
sources, for the CLI. Every named file is in the runfiles.

Lines:

    goroot <the SDK's root marker file; the test resolves it to the SDK on disk>
    stdlib <runtime.a in the compiled standard library's archive directory>
    pkg <importpath> <export file> <direct|transitive>
    src <source file of the pkg line above>
"""

load("@rules_go//go:def.bzl", "GoArchive", "go_context")

def _rlocationpath(ctx, f):
    if f.short_path.startswith("../"):
        return f.short_path[3:]
    return ctx.workspace_name + "/" + f.short_path

def _go_toolchain_manifest_impl(ctx):
    go = go_context(ctx, include_deprecated_properties = False)
    sdk = go.sdk
    lines = ["goroot " + _rlocationpath(ctx, sdk.root_file)]
    files = [sdk.root_file]

    # rules_go's installsuffix(mode), which it does not export; the test runs
    # in the default mode.
    stdlib = go.stdlib.root_file
    if not stdlib.is_directory:
        fail("the standard library is not compiled by rules_go, so it has no archives")
    lines.append("stdlib %s/%s_%s/runtime.a" % (_rlocationpath(ctx, stdlib), go.mode.goos, go.mode.goarch))
    files.append(stdlib)

    direct = {dep[GoArchive].data.importpath: True for dep in ctx.attr.deps}
    archives = depset(transitive = [dep[GoArchive].transitive for dep in ctx.attr.deps])
    for data in archives.to_list():
        lines.append("pkg %s %s %s" % (
            data.importpath,
            _rlocationpath(ctx, data.export_file),
            "direct" if data.importpath in direct else "transitive",
        ))
        files.append(data.export_file)
        for src in data.srcs:
            lines.append("src " + _rlocationpath(ctx, src))
            files.append(src)

    manifest = ctx.actions.declare_file(ctx.label.name + ".txt")
    ctx.actions.write(manifest, "\n".join(lines) + "\n")
    return [DefaultInfo(
        files = depset([manifest]),
        runfiles = ctx.runfiles(files = [manifest] + files),
    )]

go_toolchain_manifest = rule(
    implementation = _go_toolchain_manifest_impl,
    doc = "Writes the manifest of the Go SDK, compiled standard library and Go libraries the call budget test uses.",
    attrs = {
        "deps": attr.label_list(providers = [GoArchive], doc = "Go libraries the test's programs import."),
        "_go_context_data": attr.label(default = "@rules_go//:go_context_data"),
    },
    toolchains = ["@rules_go//go:toolchain"],
)
