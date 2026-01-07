# Marker file: this is the root of a Bazel workspace

load("@bazel_tools//tools/build_defs/repo:http.bzl", "http_archive", "http_jar")

http_jar(
    name = "antlr4_tool",
    sha256 = "bc13a9c57a8dd7d5196888211e5ede657cb64a3ce968608697e4f668251a8487",
    urls = ["https://www.antlr.org/download/antlr-4.13.1-complete.jar"],
)

http_archive(
    name = "rules_antlr",
    sha256 = "94b0eaf7ea6a47bc4e67b4412486ffd79e6740fac3107a607844a70db0d8b2ed",
    strip_prefix = "rules_antlr-0.3.0",
    urls = ["https://github.com/vaticle/rules_antlr/archive/0.3.0.tar.gz"],
)

load("@rules_antlr//antlr:repositories.bzl", "rules_antlr_dependencies")

rules_antlr_dependencies("4.7.2")

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
