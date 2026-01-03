load("@rules_go//go:def.bzl", "go_binary")

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
