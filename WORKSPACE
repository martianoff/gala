# Marker file: this is the root of a Bazel workspace
# Note: Most dependencies are managed in MODULE.bazel (bzlmod)

load("@bazel_tools//tools/build_defs/repo:http.bzl", "http_archive")

load("//:deps.bzl", "go_dependencies")

# gazelle:repository_macro deps.bzl%go_dependencies
go_dependencies()

http_archive(
    name = "gradle_bin",
    build_file_content = """
filegroup(
    name = "bin",
    srcs = ["bin/gradle", "bin/gradle.bat"],
    visibility = ["//visibility:public"],
)
filegroup(
    name = "all_files",
    srcs = glob(["**"]),
    visibility = ["//visibility:public"],
)
""",
    sha256 = "9d926787066a081739e8200858338b4a69e837c3a821a33aca9db09dd4a41026",
    strip_prefix = "gradle-8.5",
    urls = ["https://services.gradle.org/distributions/gradle-8.5-bin.zip"],
)
