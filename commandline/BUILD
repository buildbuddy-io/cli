load("@io_bazel_rules_go//go:def.bzl", "go_library")

go_library(
    name = "go_default_library",
    srcs = ["commandline.go"],
    importpath = "github.com/buildbuddy-io/buildbuddy-cli/commandline",
    visibility = ["//visibility:public"],
    deps = ["@com_github_jessevdk_go_flags//:go_default_library"],
)
