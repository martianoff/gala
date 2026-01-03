load("@rules_go//go:def.bzl", "go_binary", "go_library")

def gala_transpile(name, src, out = None):
    if not out:
        out = name + ".go"
    native.genrule(
        name = name,
        srcs = [src],
        outs = [out],
        cmd = "$(location //cmd/gala) -input $< -output $@",
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
