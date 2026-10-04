package main

import "example.com/gointerop/report"

// goSummary is Go in package main, next to main.gala, calling a Go package.
func goSummary() string { return report.Line("from a sibling Go file\n") }

// shout is Go calling exclaim, a GALA function declared in main.gala.
func shout(s string) string { return exclaim(s) + "!!" }
