"""
GALA Dependency Management for Bazel.

This file provides rules and macros for integrating GALA dependencies into Bazel builds.

Usage in WORKSPACE:

    load("@gala//:gala_deps.bzl", "gala_dependencies")

    # Load all dependencies from gala.mod
    gala_dependencies()

Then reference dependencies in BUILD files:

    gala_library(
        name = "mylib",
        src = "mylib.gala",
        importpath = "github.com/user/project/mylib",
        deps = ["@com_github_example_utils//:utils"],
    )
"""

load("@rules_go//go:def.bzl", "go_library")

def _gala_module_impl(repository_ctx):
    """Implementation of the gala_module repository rule.

    Fetches a GALA module from the cache or downloads it, and creates
    a BUILD.bazel file with go_library targets.
    """
    module_path = repository_ctx.attr.module_path
    version = repository_ctx.attr.version

    # Determine cache path
    # On Windows: %USERPROFILE%\.gala\pkg\mod
    # On Unix: ~/.gala/pkg/mod
    if repository_ctx.os.name.startswith("windows"):
        home = repository_ctx.os.environ.get("USERPROFILE", "")
        cache_base = home + "\\.gala\\pkg\\mod"
    else:
        home = repository_ctx.os.environ.get("HOME", "")
        cache_base = home + "/.gala/pkg/mod"

    cache_path = cache_base + "/" + module_path + "@" + version

    # Check if module is cached
    cache_dir = repository_ctx.path(cache_path)

    if not cache_dir.exists:
        # Module not in cache - fail with instructions
        fail("""
GALA module not found in cache: %s@%s

Run 'gala mod add %s@%s' to fetch it first, then re-run bazel.
""" % (module_path, version, module_path, version))

    # Copy files from cache to repository via symlink
    repository_ctx.symlink(cache_path, "src")

    # Find all .go files (already transpiled) and determine package name
    go_files = []
    package_name = ""

    src_path = repository_ctx.path("src")
    for f in src_path.readdir():
        name = f.basename
        if name.endswith("_gen.go"):
            go_files.append("src/" + name)
        elif name.endswith(".gala") and not name.endswith("_test.gala"):
            # Read first line to get package name
            if not package_name:
                content = repository_ctx.read(f)
                for line in content.split("\n"):
                    line = line.strip()
                    if line.startswith("package "):
                        package_name = line[8:].strip()
                        break

    if not package_name:
        package_name = module_path.split("/")[-1]

    # Generate BUILD.bazel
    srcs_str = "[" + ", ".join(['"%s"' % f for f in go_files]) + "]" if go_files else "[]"
    deps_str = "[" + ", ".join(['"%s"' % d for d in repository_ctx.attr.deps]) + "]" if repository_ctx.attr.deps else "[]"

    build_content = '''
load("@rules_go//go:def.bzl", "go_library")

go_library(
    name = "{name}",
    srcs = {srcs},
    importpath = "{importpath}",
    visibility = ["//visibility:public"],
    deps = {deps},
)
'''.format(
        name = package_name,
        srcs = srcs_str,
        importpath = module_path,
        deps = deps_str,
    )

    repository_ctx.file("BUILD.bazel", build_content)

gala_module = repository_rule(
    implementation = _gala_module_impl,
    attrs = {
        "module_path": attr.string(
            mandatory = True,
            doc = "The module path (e.g., github.com/user/repo)",
        ),
        "version": attr.string(
            mandatory = True,
            doc = "The version to fetch (e.g., v1.2.3)",
        ),
        "sum": attr.string(
            doc = "Expected hash from gala.sum (optional, for verification)",
        ),
        "deps": attr.string_list(
            doc = "Dependencies of this module (Bazel labels)",
        ),
    },
    doc = "Fetches a GALA module and makes it available as a Bazel target.",
)

def _gala_deps_impl(repository_ctx):
    """Repository rule that reads gala.mod and generates dependency info."""
    gala_mod_path = repository_ctx.attr.gala_mod

    # Read gala.mod content
    gala_mod_content = repository_ctx.read(gala_mod_path)

    # Parse dependencies
    deps = _parse_gala_mod(gala_mod_content)

    # Generate a .bzl file with dependency declarations
    bzl_content = '''"""Auto-generated GALA dependencies from gala.mod."""

load("@gala//:gala_deps.bzl", "gala_module")

def declare_gala_deps():
    """Declare all GALA module dependencies."""
'''

    for path, version, is_go in deps:
        if is_go:
            # Skip Go dependencies - handled by rules_go
            continue
        repo_name = module_path_to_repo_name(path)
        bzl_content += '''
    gala_module(
        name = "{repo_name}",
        module_path = "{path}",
        version = "{version}",
    )
'''.format(repo_name = repo_name, path = path, version = version)

    # Write the .bzl file
    repository_ctx.file("deps.bzl", bzl_content)

    # Write BUILD file
    repository_ctx.file("BUILD.bazel", "")

_gala_deps = repository_rule(
    implementation = _gala_deps_impl,
    attrs = {
        "gala_mod": attr.label(
            mandatory = True,
            allow_single_file = True,
            doc = "Label to the gala.mod file",
        ),
    },
    doc = "Generates dependency declarations from gala.mod",
)

def gala_dependencies(gala_mod = "//:gala.mod"):
    """
    Load all GALA dependencies from a gala.mod file.

    This macro reads the gala.mod file and creates gala_module repository rules
    for each required GALA dependency.

    Args:
        gala_mod: Label to the gala.mod file (default: "//:gala.mod")

    Example in WORKSPACE:
        load("@gala//:gala_deps.bzl", "gala_dependencies")
        gala_dependencies()

    Then reference dependencies in BUILD files:
        deps = ["@com_github_example_utils//:utils"]
    """
    # Create the deps repository that reads gala.mod
    _gala_deps(
        name = "gala_deps",
        gala_mod = gala_mod,
    )

def _parse_gala_mod(content):
    """Parse gala.mod content and return list of (path, version, is_go) tuples."""
    requires = []
    in_require_block = False

    for line in content.split("\n"):
        line = line.strip()

        # Skip empty lines
        if not line:
            continue

        # Check for markers before stripping comments
        is_go = "// go" in line
        is_indirect = "// indirect" in line

        # Remove inline comments
        if "//" in line:
            comment_idx = line.index("//")
            line = line[:comment_idx].strip()

        # Handle require block
        if line == "require (" or line.startswith("require("):
            in_require_block = True
            continue

        if line == ")":
            in_require_block = False
            continue

        # Single-line require
        if line.startswith("require ") and "(" not in line:
            parts = line[8:].split()
            if len(parts) >= 2:
                path = parts[0]
                version = parts[1]
                requires.append((path, version, is_go))
            continue

        # Inside require block
        if in_require_block:
            parts = line.split()
            if len(parts) >= 2:
                path = parts[0]
                version = parts[1]
                requires.append((path, version, is_go))

    return requires

def module_path_to_repo_name(module_path):
    """Convert a module path to a valid Bazel repository name.

    Example: github.com/example/utils -> com_github_example_utils
    """
    # Replace special characters
    name = module_path.replace(".", "_").replace("/", "_").replace("-", "_")
    # Reverse domain parts for consistency with Go conventions
    parts = name.split("_")
    if len(parts) >= 2 and parts[0] in ["github", "gitlab", "bitbucket"]:
        # github_com_user_repo -> com_github_user_repo
        parts = [parts[1], parts[0]] + parts[2:]
    return "_".join(parts)
